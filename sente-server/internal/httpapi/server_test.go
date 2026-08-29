package httpapi

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"sente.app/server/internal/auth"
	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/hub"
	"sente.app/server/internal/node"
	"sente.app/server/internal/ratelimit"
	"sente.app/server/internal/store"
)

// End to end through the real edge: HTTP for sign-in and game creation, a
// WebSocket for playing. If the protocol in docs/06 and the engine underneath
// ever disagree, this is where it shows.

var (
	testPool  *pgxpool.Pool
	testRedis *redis.Client
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("sente"), postgres.WithUsername("sente"), postgres.WithPassword("sente"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start postgres (is Docker running?): %v\n", err)
		os.Exit(1)
	}
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	if err := store.Migrate(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	_ = conn.Close(ctx)
	testPool, _ = pgxpool.New(ctx, dsn)

	rd, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start redis: %v\n", err)
		os.Exit(1)
	}
	uri, _ := rd.ConnectionString(ctx)
	options, _ := redis.ParseURL(uri)
	testRedis = redis.NewClient(options)

	code := m.Run()
	testPool.Close()
	_ = testRedis.Close()
	_ = testcontainers.TerminateContainer(pg)
	_ = testcontainers.TerminateContainer(rd)
	os.Exit(code)
}

// newTestServer stands up a full node on the shared containers. Options adjust
// the Config before the node is built, e.g. to attach a fake Apple or APNs.
func newTestServer(t *testing.T, opts ...func(*Config)) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	issuer, err := auth.NewIssuer(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Pool: testPool, Redis: testRedis, Issuer: issuer,
		// Each test server gets its own buckets; otherwise the sign-up limit one
		// test exhausts starves every test after it.
		Limiter:       ratelimit.New(testRedis).WithPrefix(t.Name() + ":"),
		PublicBaseURL: "https://sente.test"}
	for _, opt := range opts {
		opt(&config)
	}

	leases := cluster.New(testRedis, t.Name())
	var messageHub *hub.Hub
	registry := node.New(node.Config{
		Leases: leases, Games: store.NewGames(testPool), IdleAfter: time.Hour,
		Broadcast: func(id string, events []game.Event) {
			messageHub.Broadcast(id, events)
			config.Notifier.Broadcast(id, events)
		},
	})
	messageHub = hub.New(hub.Config{
		Registry: registry, Leases: leases, Redis: testRedis, NodeID: t.Name()})

	ctx, cancel := context.WithCancel(context.Background())
	go messageHub.Serve(ctx)
	if config.Notifier != nil {
		go config.Notifier.Run(ctx)
	}

	config.Hub, config.Registry = messageHub, registry
	api := New(config)
	server := httptest.NewServer(api.Handler())
	t.Cleanup(func() {
		cancel()
		server.Close()
		registry.Drain(context.Background())
		messageHub.Close()
	})
	return server
}

type player struct {
	token  string
	userID string
}

func signUp(t *testing.T, server *httptest.Server) player {
	t.Helper()
	response, err := http.Post(server.URL+"/v1/auth/guest", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("guest sign-up returned %d", response.StatusCode)
	}
	var body struct {
		User struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			FriendCode  string `json:"friend_code"`
		} `json:"user"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" || body.User.ID == "" || len(body.User.FriendCode) != 8 {
		t.Fatalf("incomplete sign-up: %+v", body)
	}
	return player{token: body.AccessToken, userID: body.User.ID}
}

func createGame(t *testing.T, server *httptest.Server, p player, body string) string {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/games", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+p.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		payload, _ := json.Marshal(response.Status)
		t.Fatalf("creating a game returned %d %s", response.StatusCode, payload)
	}
	var created struct {
		GameID string `json:"game_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created.GameID
}

func connect(t *testing.T, server *httptest.Server, p player, gameID string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/v1/ws?pv=1&game_id=" + gameID + "&token=" + p.token
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func readUntil(t *testing.T, conn *websocket.Conn, kind string) Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("waiting for %q: %v", kind, err)
		}
		var message Message
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatalf("bad message: %v", err)
		}
		if message.Type == kind {
			return message
		}
		if message.Type == "error" && kind != "error" {
			t.Fatalf("waiting for %q, got an error: %s", kind, message.Payload)
		}
	}
	t.Fatalf("never received %q", kind)
	return Message{}
}

// readMoveMade waits for the move with the given number. The server echoes a
// player's own move back to them on the same channel the opponent sees it on
// (docs/06 §3.5), so waiting for "any move_made" can return that echo and let
// the test race ahead of the opponent. Waiting for the number cannot.
func readMoveMade(t *testing.T, conn *websocket.Conn, moveNo int) moveMadePayload {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		message := readUntil(t, conn, "move_made")
		var made moveMadePayload
		if err := json.Unmarshal(message.Payload, &made); err != nil {
			t.Fatal(err)
		}
		if made.MoveNumber == moveNo {
			return made
		}
		if made.MoveNumber > moveNo {
			t.Fatalf("move %d arrived before move %d", made.MoveNumber, moveNo)
		}
	}
	t.Fatalf("never saw move %d", moveNo)
	return moveMadePayload{}
}

func send(t *testing.T, conn *websocket.Conn, kind string, payload any) {
	t.Helper()
	message, err := newMessage(kind, payload)
	if err != nil {
		t.Fatal(err)
	}
	message.ID = "c-1"
	data, _ := json.Marshal(message)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

const blitz = `{"board_size":9,"rules":"japanese",
	"time_control":{"kind":"absolute","main_time_ms":600000}}`

func TestHealthAndConfig(t *testing.T) {
	server := newTestServer(t)
	for path, want := range map[string]int{
		"/healthz": http.StatusOK, "/readyz": http.StatusOK, "/v1/config": http.StatusOK,
	} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Errorf("%s returned %d, want %d", path, response.StatusCode, want)
		}
	}
}

func TestEndpointsNeedATokenAndRejectABadOne(t *testing.T) {
	server := newTestServer(t)
	for _, header := range []string{"", "Bearer nonsense", "Basic abc"} {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/games",
			strings.NewReader(blitz))
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("header %q returned %d, want 401", header, response.StatusCode)
		}
	}
}

// The whole point: two people, one game, moves and events over the socket.
func TestTwoPlayersPlayOverWebSocket(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	blackConn := connect(t, server, black, gameID)
	state := readUntil(t, blackConn, "game_state")
	var initial gameStatePayload
	if err := json.Unmarshal(state.Payload, &initial); err != nil {
		t.Fatal(err)
	}
	if initial.BoardSize != 9 || initial.ToPlay != "black" || initial.MoveNumber != 0 {
		t.Fatalf("unexpected opening state: %+v", initial)
	}
	if len(initial.Board) != 81 || strings.Trim(initial.Board, ".") != "" {
		t.Errorf("a new game should have an empty board, got %q", initial.Board)
	}

	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")

	// Black plays; both sockets must see it.
	send(t, blackConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	for name, conn := range map[string]*websocket.Conn{"black": blackConn, "white": whiteConn} {
		made := readMoveMade(t, conn, 1)
		if made.Color != "black" || made.Point == nil || *made.Point != "E5" {
			t.Errorf("%s saw the wrong move: %+v", name, made)
		}
	}

	// White answers from the other socket.
	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "e7", ExpectedMoveNumber: 1})
	if second := readMoveMade(t, blackConn, 2); second.Color != "white" {
		t.Errorf("black saw the wrong reply: %+v", second)
	}

	// A client joining now must be told where the last stone went, or it cannot
	// draw the marker until the next move.
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/games/"+gameID, nil)
	request.Header.Set("Authorization", "Bearer "+black.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var fetched gameStatePayload
	if err := json.NewDecoder(response.Body).Decode(&fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.LastMove == nil || *fetched.LastMove != "E7" {
		t.Errorf("want last_move E7, got %v", fetched.LastMove)
	}
}

// A connection is bound to one colour, so a client cannot move for its opponent
// however it words the message.
func TestAPlayerCannotMoveForTheOpponent(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	blackConn := connect(t, server, black, gameID)
	readUntil(t, blackConn, "game_state")
	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")

	// White tries to move first, when it is Black's turn.
	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	message := readUntil(t, whiteConn, "error")
	var failure errorPayload
	if err := json.Unmarshal(message.Payload, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "not_your_turn" {
		t.Errorf("want not_your_turn, got %+v", failure)
	}
	if failure.Message == "" {
		t.Error("an error a player sees needs a message they can read")
	}
}

func TestIllegalMovesComeBackWithTheRulesCode(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	blackConn := connect(t, server, black, gameID)
	readUntil(t, blackConn, "game_state")
	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")

	send(t, blackConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	readMoveMade(t, whiteConn, 1)

	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 1})
	message := readUntil(t, whiteConn, "error")
	var failure errorPayload
	if err := json.Unmarshal(message.Payload, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "occupied" {
		t.Errorf("want occupied, got %+v", failure)
	}
}

func TestPassingTwiceOpensScoringAndAgreementEndsTheGame(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	blackConn := connect(t, server, black, gameID)
	readUntil(t, blackConn, "game_state")
	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")

	send(t, blackConn, "move", incomingMove{Kind: "play", Point: "c3", ExpectedMoveNumber: 0})
	readMoveMade(t, whiteConn, 1)
	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "g7", ExpectedMoveNumber: 1})
	readMoveMade(t, blackConn, 2)

	send(t, blackConn, "move", incomingMove{Kind: "pass", ExpectedMoveNumber: 2})
	readMoveMade(t, whiteConn, 3)
	send(t, whiteConn, "move", incomingMove{Kind: "pass", ExpectedMoveNumber: 3})
	readUntil(t, blackConn, "scoring_state")

	send(t, blackConn, "scoring_accept", incomingAccept{Accepted: true})
	readUntil(t, whiteConn, "scoring_state")
	send(t, whiteConn, "scoring_accept", incomingAccept{Accepted: true})

	message := readUntil(t, blackConn, "game_over")
	var over gameOverPayload
	if err := json.Unmarshal(message.Payload, &over); err != nil {
		t.Fatal(err)
	}
	if over.Result.Reason != "counting" || over.Result.Score == nil {
		t.Fatalf("want a counted result, got %+v", over.Result)
	}
}

func TestResigningEndsTheGameForBothSides(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	blackConn := connect(t, server, black, gameID)
	readUntil(t, blackConn, "game_state")
	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")

	send(t, blackConn, "move", incomingMove{Kind: "resign", ExpectedMoveNumber: 0})
	for name, conn := range map[string]*websocket.Conn{"black": blackConn, "white": whiteConn} {
		message := readUntil(t, conn, "game_over")
		var over gameOverPayload
		if err := json.Unmarshal(message.Payload, &over); err != nil {
			t.Fatal(err)
		}
		if over.Result.Reason != "resignation" || over.Result.Winner == nil || *over.Result.Winner != "white" {
			t.Errorf("%s saw the wrong result: %+v", name, over.Result)
		}
	}
}

func TestPingIsAnsweredForClockSkew(t *testing.T) {
	server := newTestServer(t)
	black := signUp(t, server)
	gameID := createGame(t, server, black, blitz)
	conn := connect(t, server, black, gameID)
	readUntil(t, conn, "game_state")

	sent := time.Now().UTC()
	send(t, conn, "ping", incomingPing{ClientTime: sent})
	message := readUntil(t, conn, "pong")
	var pong pongPayload
	if err := json.Unmarshal(message.Payload, &pong); err != nil {
		t.Fatal(err)
	}
	if !pong.ClientTime.Equal(sent) {
		t.Errorf("the client's own timestamp must come back untouched: %v", pong.ClientTime)
	}
	if pong.ServerTime.IsZero() {
		t.Error("pong must carry the server clock, or skew cannot be corrected")
	}
}

func TestAThirdPersonCannotJoinAFullGame(t *testing.T) {
	server := newTestServer(t)
	black, white, stranger := signUp(t, server), signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	readUntil(t, connect(t, server, black, gameID), "game_state")
	readUntil(t, connect(t, server, white, gameID), "game_state")

	url := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/v1/ws?pv=1&game_id=" + gameID + "&token=" + stranger.token
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		return // refused at the handshake is fine too
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Error("a game with two players must not admit a third")
	}
}

func TestTheWebSocketRefusesAnUnknownProtocolVersion(t *testing.T) {
	server := newTestServer(t)
	black := signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	url := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/v1/ws?pv=99&game_id=" + gameID + "&token=" + black.token
	if _, _, err := websocket.Dial(context.Background(), url, nil); err == nil {
		t.Error("an unsupported protocol version must be refused at the handshake")
	}
}

func TestCreatingAGameValidatesItsConfiguration(t *testing.T) {
	server := newTestServer(t)
	black := signUp(t, server)
	for name, body := range map[string]string{
		"unsupported size": `{"board_size":11}`,
		"unknown rules":    `{"board_size":9,"rules":"martian"}`,
		"broken clock":     `{"board_size":9,"time_control":{"kind":"byoyomi","main_time_ms":1000}}`,
		"malformed":        `not json`,
	} {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/games", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+black.token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: returned %d, want 400", name, response.StatusCode)
		}
	}
}

func TestFetchingAGameOverREST(t *testing.T) {
	server := newTestServer(t)
	black := signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/games/"+gameID, nil)
	request.Header.Set("Authorization", "Bearer "+black.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state gameStatePayload
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.GameID != gameID || state.BoardSize != 9 || state.Phase != "playing" {
		t.Errorf("unexpected state: %+v", state)
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/games/"+store.NewID(), nil)
	request.Header.Set("Authorization", "Bearer "+black.token)
	missing, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("a missing game returned %d, want 404", missing.StatusCode)
	}
}

// An accepted undo is the one event a client cannot apply by itself, so the full
// position follows it; and either side can ask for the position at any time.
func TestAnAcceptedUndoRewindsBothClients(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)
	blackConn, whiteConn := connect(t, server, black, gameID), connect(t, server, white, gameID)
	readUntil(t, blackConn, "game_state")
	readUntil(t, whiteConn, "game_state")

	send(t, blackConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	readMoveMade(t, whiteConn, 1)
	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "e7", ExpectedMoveNumber: 1})
	readMoveMade(t, blackConn, 2)
	readMoveMade(t, whiteConn, 2)

	send(t, whiteConn, "undo_request", map[string]any{})
	readUntil(t, blackConn, "undo_requested")
	send(t, blackConn, "undo_response", map[string]bool{"accept": true})

	type position struct {
		MoveNo int    `json:"move_no"`
		ToPlay string `json:"to_play"`
		Board  string `json:"board"`
	}
	for name, conn := range map[string]*websocket.Conn{"black": blackConn, "white": whiteConn} {
		result := readUntil(t, conn, "undo_result")
		var verdict struct {
			Accepted bool `json:"accepted"`
			MoveNo   int  `json:"move_no"`
		}
		_ = json.Unmarshal(result.Payload, &verdict)
		if !verdict.Accepted || verdict.MoveNo != 1 {
			t.Errorf("%s: undo_result %+v", name, verdict)
		}
		state := readUntil(t, conn, "game_state")
		var got position
		_ = json.Unmarshal(state.Payload, &got)
		if got.MoveNo != 1 || got.ToPlay != "white" || strings.Count(got.Board, "w") != 0 {
			t.Errorf("%s: after undo want move 1, white to play, no white stone; got %+v", name, got)
		}
	}

	// White plays something else and the game goes on from move 1.
	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "c3", ExpectedMoveNumber: 1})
	if made := readMoveMade(t, blackConn, 2); made.Point == nil || *made.Point != "C3" {
		t.Errorf("replacement move: %+v", made)
	}

	// What was stored agrees: a reload sees two moves, the second at C3.
	loaded, err := store.NewGames(testPool).Load(context.Background(), gameID)
	if err != nil {
		t.Fatalf("reload after undo: %v", err)
	}
	if loaded.Session.MoveNumber() != 2 || loaded.Session.UndosUsed() != 1 {
		t.Errorf("stored game: move %d, undos %d", loaded.Session.MoveNumber(), loaded.Session.UndosUsed())
	}

	// resume: the client asks, the server answers with the position.
	send(t, blackConn, "resume", map[string]any{})
	var asked position
	_ = json.Unmarshal(readUntil(t, blackConn, "game_state").Payload, &asked)
	if asked.MoveNo != 2 || asked.ToPlay != "black" {
		t.Errorf("resume: %+v", asked)
	}
}

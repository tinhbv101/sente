package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"sente.app/server/internal/ratelimit"
)

// The J1 journey from docs/01: one person makes a link, sends it, and the friend
// who opens it ends up in a game with them.

func do(t *testing.T, server *httptest.Server, method, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response, readAll(t, response)
}

func readAll(t *testing.T, response *http.Response) []byte {
	t.Helper()
	var out []byte
	buffer := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buffer)
		out = append(out, buffer[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

const inviteBody = `{"board_size":9,"rules":"japanese","creator_color":"black",
	"time_control":{"kind":"absolute","main_time_ms":600000}}`

func createInvite(t *testing.T, server *httptest.Server, p player, body string) challengeResponse {
	t.Helper()
	response, payload := do(t, server, http.MethodPost, "/v1/challenges", p.token, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("creating an invitation returned %d: %s", response.StatusCode, payload)
	}
	var created challengeResponse
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestTheInviteLinkJourney(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)

	// An makes a link.
	invite := createInvite(t, server, an, inviteBody)
	if len(invite.Code) != 8 || !invite.IsMine {
		t.Fatalf("unexpected invitation: %+v", invite)
	}
	if invite.ShareURL != "https://sente.test/j/"+invite.Code {
		t.Errorf("share URL should be built from the public base, got %q", invite.ShareURL)
	}

	// Binh opens it before signing in: the preview needs no token.
	response, payload := do(t, server, http.MethodGet, "/v1/challenges/"+invite.Code, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview returned %d", response.StatusCode)
	}
	var preview challengeResponse
	if err := json.Unmarshal(payload, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.CreatorName == "" || preview.Config.BoardSize != 9 || preview.Status != "pending" {
		t.Errorf("the preview must say who is inviting and to what: %+v", preview)
	}
	if preview.IsMine {
		t.Error("an anonymous viewer does not own the invitation")
	}

	// Binh accepts and is told which colour they got.
	response, payload = do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept",
		binh.token, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("accepting returned %d: %s", response.StatusCode, payload)
	}
	var accepted struct {
		GameID    string `json:"game_id"`
		YourColor string `json:"your_color"`
	}
	if err := json.Unmarshal(payload, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.GameID == "" || accepted.YourColor != "white" {
		t.Errorf("An chose black, so Binh must be white: %+v", accepted)
	}

	// Both connect to the game and Black moves first.
	anConn := connect(t, server, an, accepted.GameID)
	readUntil(t, anConn, "game_state")
	binhConn := connect(t, server, binh, accepted.GameID)
	readUntil(t, binhConn, "game_state")
	send(t, anConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	readUntil(t, binhConn, "move_made")

	// The link is spent.
	var after challengeResponse
	_ = json.Unmarshal(readAllFrom(t, server, "/v1/challenges/"+invite.Code), &after)
	if after.Status != "accepted" || after.GameID != accepted.GameID {
		t.Errorf("the invitation should point at the game it became: %+v", after)
	}
}

func readAllFrom(t *testing.T, server *httptest.Server, path string) []byte {
	t.Helper()
	response, err := http.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return readAll(t, response)
}

// Two friends both tap the same link. One gets the seat; the other gets a clear
// answer rather than a second game.
func TestASharedLinkAdmitsExactlyOnePerson(t *testing.T) {
	server := newTestServer(t)
	an := signUp(t, server)
	invite := createInvite(t, server, an, inviteBody)

	const racers = 8
	players := make([]player, racers)
	for i := range players {
		players[i] = signUp(t, server)
	}
	var mu sync.Mutex
	statuses := map[int]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, p := range players {
		wg.Add(1)
		go func(p player) {
			defer wg.Done()
			<-start
			response, _ := do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept",
				p.token, "")
			mu.Lock()
			statuses[response.StatusCode]++
			mu.Unlock()
		}(p)
	}
	close(start)
	wg.Wait()

	if statuses[http.StatusCreated] != 1 {
		t.Errorf("want exactly one 201, got %v", statuses)
	}
	if statuses[http.StatusConflict] != racers-1 {
		t.Errorf("everyone else should get 409, got %v", statuses)
	}
}

func TestInvitationEdgeCasesSpeakClearly(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)
	invite := createInvite(t, server, an, inviteBody)

	// Your own link.
	response, payload := do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept", an.token, "")
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(payload), "own_challenge") {
		t.Errorf("accepting your own invitation: %d %s", response.StatusCode, payload)
	}
	// A code that never existed looks exactly like one that was used up.
	response, payload = do(t, server, http.MethodPost, "/v1/challenges/ZZZZZZZZ/accept", binh.token, "")
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(payload), "challenge_gone") {
		t.Errorf("unknown code: %d %s", response.StatusCode, payload)
	}
	response, _ = do(t, server, http.MethodGet, "/v1/challenges/ZZZZZZZZ", "", "")
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("previewing an unknown code: want 404, got %d", response.StatusCode)
	}
	// Declining, then trying anyway.
	response, _ = do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/decline", binh.token, "")
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("declining: want 204, got %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept", binh.token, "")
	if response.StatusCode != http.StatusConflict {
		t.Errorf("a declined invitation: want 409, got %d", response.StatusCode)
	}
}

func TestOnlyTheCreatorCanCancel(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)
	invite := createInvite(t, server, an, inviteBody)

	if response, _ := do(t, server, http.MethodDelete, "/v1/challenges/"+invite.Code, binh.token, ""); response.StatusCode != http.StatusConflict {
		t.Errorf("a stranger cancelling: want 409, got %d", response.StatusCode)
	}
	if response, _ := do(t, server, http.MethodDelete, "/v1/challenges/"+invite.Code, an.token, ""); response.StatusCode != http.StatusNoContent {
		t.Errorf("the creator cancelling: want 204, got %d", response.StatusCode)
	}
}

func TestListingShowsOpenInvitations(t *testing.T) {
	server := newTestServer(t)
	an := signUp(t, server)
	first := createInvite(t, server, an, inviteBody)
	second := createInvite(t, server, an, inviteBody)

	_, payload := do(t, server, http.MethodGet, "/v1/challenges", an.token, "")
	var list struct {
		Items []challengeResponse `json:"items"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, item := range list.Items {
		codes[item.Code] = true
	}
	if !codes[first.Code] || !codes[second.Code] {
		t.Errorf("both invitations should be listed, got %v", codes)
	}
}

func TestCreatingAnInvitationValidatesLikeAGame(t *testing.T) {
	server := newTestServer(t)
	an := signUp(t, server)
	for name, body := range map[string]string{
		"unsupported size": `{"board_size":11}`,
		"unknown colour":   `{"board_size":9,"creator_color":"purple"}`,
		"broken clock":     `{"board_size":9,"time_control":{"kind":"byoyomi","main_time_ms":1000}}`,
	} {
		response, _ := do(t, server, http.MethodPost, "/v1/challenges", an.token, body)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, response.StatusCode)
		}
	}
}

// ── rate limiting ───────────────────────────────────────────────────────────

func TestSignUpIsRateLimitedPerAddress(t *testing.T) {
	server := newTestServer(t)
	// httptest clients all come from 127.0.0.1, so they share one bucket -- which
	// is exactly the situation the limit exists for.
	var last *http.Response
	limited := false
	for i := 0; i < ratelimit.SignUp.Capacity+5; i++ {
		response, err := http.Post(server.URL+"/v1/auth/guest", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		last = response
		if response.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("sign-up was never rate limited")
	}
	if retry := last.Header.Get("Retry-After"); retry == "" {
		t.Error("a refused caller must be told when to come back")
	} else if seconds, err := strconv.Atoi(retry); err != nil || seconds < 1 {
		t.Errorf("Retry-After must be a positive number of seconds, got %q", retry)
	}
	if last.Header.Get("X-RateLimit-Limit") != strconv.Itoa(ratelimit.SignUp.Capacity) {
		t.Errorf("the limit header should state the rule, got %q", last.Header.Get("X-RateLimit-Limit"))
	}
}

func TestRateLimitHeadersAppearOnEveryResponse(t *testing.T) {
	server := newTestServer(t)
	response, err := http.Get(server.URL + "/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	for _, header := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if response.Header.Get(header) == "" {
			t.Errorf("%s missing on an allowed response", header)
		}
	}
}

// Authenticated limits are charged to the user, not the address: two people
// behind one NAT must not share a budget.
func TestAuthenticatedLimitsAreChargedPerUser(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)

	for i := 0; i < ratelimit.CreateInvite.Capacity; i++ {
		response, _ := do(t, server, http.MethodPost, "/v1/challenges", an.token, inviteBody)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("An's invitation %d returned %d", i+1, response.StatusCode)
		}
	}
	response, _ := do(t, server, http.MethodPost, "/v1/challenges", an.token, inviteBody)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("An should be out of invitations, got %d", response.StatusCode)
	}
	// Same address, different person: their own budget.
	response, _ = do(t, server, http.MethodPost, "/v1/challenges", binh.token, inviteBody)
	if response.StatusCode != http.StatusCreated {
		t.Errorf("Binh's budget is separate from An's, got %d", response.StatusCode)
	}
}

func TestHealthProbesAreNeverRateLimited(t *testing.T) {
	server := newTestServer(t)
	for i := 0; i < 200; i++ {
		response, err := http.Get(server.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("probe %d returned %d", i+1, response.StatusCode)
		}
	}
}

// X-Forwarded-For is only believed when configured, or every limit would be
// bypassable by setting a header.
func TestForwardedForIsIgnoredUnlessTrusted(t *testing.T) {
	server := newTestServer(t)
	api := New(Config{Pool: testPool, Redis: testRedis, Issuer: nil, Limiter: ratelimit.New(testRedis)})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.7:5000"
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := api.clientIP(request); got != "10.0.0.7" {
		t.Errorf("without trust the header must be ignored, got %q", got)
	}

	api.config.TrustProxyHeaders = true
	// The rightmost entry is what our own proxy appended; the left is forgeable.
	if got := api.clientIP(request); got != "10.0.0.1" {
		t.Errorf("with trust the proxy's own entry wins, got %q", got)
	}
	_ = server
}

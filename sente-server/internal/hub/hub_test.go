package hub

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/node"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
	"sente.app/server/internal/wire"
)

// Two players on two different nodes is the normal case, not the exotic one: a
// load balancer has no idea which games a connection belongs to. These tests run
// two independent hubs against one Redis and one PostgreSQL.

var (
	testPool  *pgxpool.Pool
	testRedis *redis.Client
)

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

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
}

func blitz() game.Config {
	return game.Config{
		Size: 9, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
		TimeControl: game.TimeControl{Kind: game.Absolute, MainTime: 10 * time.Minute},
	}
}

type testNode struct {
	hub      *Hub
	registry *node.Registry
	cancel   context.CancelFunc
}

// newNode builds a hub as if it were a separate server process.
func newNode(t *testing.T, name string) *testNode {
	t.Helper()
	skipIfShort(t)
	nodeID := t.Name() + "-" + name
	leases := cluster.New(testRedis, nodeID).WithTTL(time.Minute)

	var hub *Hub
	registry := node.New(node.Config{
		Leases:    leases,
		Games:     store.NewGames(testPool),
		IdleAfter: time.Hour,
		Broadcast: func(gameID string, events []game.Event) { hub.Broadcast(gameID, events) },
	})
	hub = New(Config{Registry: registry, Leases: leases, Redis: testRedis, NodeID: nodeID})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Serve(ctx)

	n := &testNode{hub: hub, registry: registry, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		registry.Drain(context.Background())
		hub.Close()
	})
	return n
}

func newGame(t *testing.T) string {
	t.Helper()
	id, err := store.NewGames(testPool).Create(context.Background(), store.CreateParams{
		Config: blitz(), StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func coord(t *testing.T, text string) rules.Point {
	t.Helper()
	p, ok := rules.ParseCoordinate(text, 9)
	if !ok {
		t.Fatalf("bad coordinate %q", text)
	}
	return p
}

func moveNumberOf(t *testing.T, events []game.Event) int {
	t.Helper()
	for _, event := range events {
		if made, ok := event.(game.MoveMade); ok {
			return made.MoveNumber
		}
	}
	t.Fatalf("no move_made among %d events", len(events))
	return 0
}

func TestAGameRunsOnWhicheverNodeTouchedItFirst(t *testing.T) {
	first := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)

	events, err := first.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if moveNumberOf(t, events) != 1 {
		t.Error("the move was not applied")
	}
	if _, ok := first.registry.Get(id); !ok {
		t.Error("the node that took the command should be running the game")
	}
}

// The case the architecture exists for: two players, two nodes, one game.
func TestTwoPlayersOnTwoNodesPlayOneGame(t *testing.T) {
	nodeA, nodeB := newNode(t, "a"), newNode(t, "b")
	ctx := context.Background()
	id := newGame(t)

	// Black is connected to node A, which becomes the owner.
	events, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if moveNumberOf(t, events) != 1 {
		t.Fatal("black's move did not land")
	}

	// White is connected to node B, which owns nothing and must forward.
	events, err = nodeB.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e7")), ExpectedMoveNumber: 1,
	})
	if err != nil {
		t.Fatalf("the forwarded move was rejected: %v", err)
	}
	if moveNumberOf(t, events) != 2 {
		t.Error("the forwarded move did not land")
	}
	if _, ok := nodeB.registry.Get(id); ok {
		t.Error("a gateway must not start running someone else's game")
	}

	// Several more, alternating which node they arrive at.
	for i, step := range []struct {
		on   *testNode
		by   rules.Color
		text string
	}{
		{nodeB, rules.Black, "c3"}, {nodeA, rules.White, "g7"},
		{nodeB, rules.Black, "c7"}, {nodeA, rules.White, "g3"},
	} {
		if _, err := step.on.hub.Execute(ctx, id, game.PlayCommand{
			By: step.by, Move: rules.Play(coord(t, step.text)), ExpectedMoveNumber: 2 + i,
		}); err != nil {
			t.Fatalf("move %d via the other node: %v", i, err)
		}
	}

	actor, _ := nodeA.registry.Get(id)
	if got := actor.Snapshot().MoveNumber(); got != 6 {
		t.Errorf("want six moves, got %d", got)
	}
	// And the database agrees, whichever node wrote each move.
	var stored int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM moves WHERE game_id = $1`, id).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 6 {
		t.Errorf("want six stored moves, got %d", stored)
	}
}

// A rejection has to survive the forward: the gateway must not turn the owner's
// verdict into something vaguer.
func TestARejectionSurvivesTheForward(t *testing.T) {
	nodeA, nodeB := newNode(t, "a"), newNode(t, "b")
	ctx := context.Background()
	id := newGame(t)

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// Playing on top of an existing stone, through the gateway.
	_, err := nodeB.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 1,
	})
	if err == nil {
		t.Fatal("an illegal move must be refused even when forwarded")
	}
	if err.Error() != string(rules.ErrOccupied) {
		t.Errorf("want the owner's own code %q, got %q", rules.ErrOccupied, err.Error())
	}

	// A stale expectation likewise.
	_, err = nodeB.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "f5")), ExpectedMoveNumber: 0,
	})
	if err == nil || err.Error() != string(game.ErrOutOfSync) {
		t.Errorf("want out_of_sync, got %v", err)
	}
}

// Both players must see every event, wherever it was produced.
func TestEventsReachSubscribersOnEveryNode(t *testing.T) {
	nodeA, nodeB := newNode(t, "a"), newNode(t, "b")
	ctx := context.Background()
	id := newGame(t)

	fromA, stopA := nodeA.hub.Subscribe(ctx, id)
	defer stopA()
	fromB, stopB := nodeB.hub.Subscribe(ctx, id)
	defer stopB()
	// Redis needs a moment to register the subscriptions before anything is sent.
	time.Sleep(100 * time.Millisecond)

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}

	for name, channel := range map[string]<-chan game.Event{"owner": fromA, "gateway": fromB} {
		select {
		case event := <-channel:
			made, ok := event.(game.MoveMade)
			if !ok {
				t.Errorf("%s: want a move_made, got %T", name, event)
				continue
			}
			if made.MoveNumber != 1 || made.By != rules.Black {
				t.Errorf("%s: event mangled in transit: %+v", name, made)
			}
			if made.Move.Point != coord(t, "e5") {
				t.Errorf("%s: wrong point: %+v", name, made.Move)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("%s never saw the move", name)
		}
	}
}

func TestUnsubscribingStopsDelivery(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)

	events, stop := nodeA.hub.Subscribe(ctx, id)
	time.Sleep(100 * time.Millisecond)
	stop()

	if _, ok := <-events; ok {
		t.Error("the channel should be closed after unsubscribing")
	}
	// A second stop must not panic on the closed channel.
	stop()
}

// Whoever holds the game answers, so a duplicate is recognised no matter which
// node the retry arrives at.
func TestIdempotencyHoldsAcrossNodes(t *testing.T) {
	nodeA, nodeB := newNode(t, "a"), newNode(t, "b")
	ctx := context.Background()
	id := newGame(t)
	key := store.NewID()

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")),
		ClientMoveID: key, ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// The client reconnects to the other node and resends.
	events, err := nodeB.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")),
		ClientMoveID: key, ExpectedMoveNumber: 0,
	})
	if err != nil {
		t.Fatalf("a resend must succeed: %v", err)
	}
	for _, event := range events {
		if made, ok := event.(game.MoveMade); ok && !made.Duplicate {
			t.Error("the resend was treated as a new move")
		}
	}
	var stored int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM moves WHERE game_id = $1`, id).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Errorf("want one stored move, got %d", stored)
	}
}

// If the owner stops running the game between the forward and its arrival, the
// gateway is told so rather than left waiting.
func TestForwardingToANodeThatDroppedTheGame(t *testing.T) {
	nodeA, nodeB := newNode(t, "a"), newNode(t, "b")
	ctx := context.Background()
	id := newGame(t)

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// Node A stops running the game but the lease has not lapsed yet.
	actor, _ := nodeA.registry.Get(id)
	actor.Abandon()

	_, err := nodeB.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e7")), ExpectedMoveNumber: 1,
	})
	if err == nil {
		t.Fatal("a game nobody is running cannot accept a move")
	}
	if err.Error() != node.ErrHeldElsewhere.Error() {
		t.Errorf("want held-elsewhere, got %v", err)
	}
}

func TestForwardingTimesOutWhenNobodyIsListening(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)

	// A lease naming a node that does not exist: nothing will ever consume it.
	ghost := cluster.New(testRedis, t.Name()+"-ghost").WithTTL(time.Minute)
	if got, err := ghost.Acquire(ctx, id); err != nil || !got {
		t.Fatal("setup")
	}

	// Shorten the wait so the test does not sit for the full timeout.
	deadline, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	_, err := nodeA.hub.Execute(deadline, id, game.PlayCommand{
		By: rules.Black, Move: rules.Pass, ExpectedMoveNumber: 0,
	})
	if err == nil {
		t.Fatal("a command nobody consumes must not appear to succeed")
	}
}

func TestGarbageOnTheCommandStreamIsIgnored(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()

	// A malformed entry must not stop the consumer serving real commands after it.
	if err := testRedis.XAdd(ctx, &redis.XAddArgs{
		Stream: commandStream(t.Name() + "-a"),
		Values: map[string]any{"payload": "not json"},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	id := newGame(t)
	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatalf("the consumer stopped after bad input: %v", err)
	}
}

// A node can hold a lease for a game it is not running: after a restart under the
// same id, or when an actor stopped without releasing. It must pick the game back
// up rather than forward to itself and find nobody home.
func TestANodeAdoptsAGameItAlreadyHoldsTheLeaseFor(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// The actor goes away without releasing, as it would if the process had been
	// restarted with the same node id.
	actor, _ := nodeA.registry.Get(id)
	actor.Abandon()
	nodeA.registry.Forget(id)
	if _, ok := nodeA.registry.Get(id); ok {
		t.Fatal("setup: the game should not be running")
	}

	events, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e7")), ExpectedMoveNumber: 1,
	})
	if err != nil {
		t.Fatalf("the node should adopt its own game: %v", err)
	}
	if moveNumberOf(t, events) != 2 {
		t.Error("the adopted game did not continue")
	}
}

func TestClosingTearsDownEverySubscription(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	first, _ := nodeA.hub.Subscribe(ctx, newGame(t))
	second, _ := nodeA.hub.Subscribe(ctx, newGame(t))
	time.Sleep(50 * time.Millisecond)

	nodeA.hub.Close()
	for name, channel := range map[string]<-chan game.Event{"first": first, "second": second} {
		select {
		case _, open := <-channel:
			if open {
				t.Errorf("%s: want a closed channel", name)
			}
		case <-time.After(time.Second):
			t.Errorf("%s: the channel was not closed", name)
		}
	}
	nodeA.hub.Close() // must not panic on the second pass
}

func TestSeveralSubscribersOnOneNodeAllGetEvents(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)

	first, stopFirst := nodeA.hub.Subscribe(ctx, id)
	defer stopFirst()
	second, stopSecond := nodeA.hub.Subscribe(ctx, id)
	defer stopSecond()
	time.Sleep(100 * time.Millisecond)

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	for name, channel := range map[string]<-chan game.Event{"first": first, "second": second} {
		select {
		case <-channel:
		case <-time.After(3 * time.Second):
			t.Errorf("%s subscriber saw nothing", name)
		}
	}
	// Dropping one must not silence the other.
	stopFirst()
	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e7")), ExpectedMoveNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second:
	case <-time.After(3 * time.Second):
		t.Error("the remaining subscriber stopped receiving")
	}
}

func TestServeStopsWithItsContext(t *testing.T) {
	skipIfShort(t)
	leases := cluster.New(testRedis, t.Name())
	hub := New(Config{
		Registry: node.New(node.Config{Leases: leases, Games: store.NewGames(testPool)}),
		Leases:   leases, Redis: testRedis, NodeID: t.Name(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { hub.Serve(ctx); close(finished) }()

	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve should stop when its context is cancelled")
	}
}

func TestRedisFailuresArePropagated(t *testing.T) {
	nodeA := newNode(t, "a")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := nodeA.hub.Execute(cancelled, newGame(t), game.PlayCommand{
		By: rules.Black, Move: rules.Pass,
	}); err == nil {
		t.Error("a failed lease lookup must be reported")
	}
}

func TestAVoidGameKeepsNoWinnerAcrossTheWire(t *testing.T) {
	// Black winning has to survive too; the "no winner" case is the one that would
	// silently become Black if the mapping were wrong.
	for _, winner := range []rules.Color{rules.Black, rules.White, rules.Empty} {
		encoded, err := wireEncode(game.GameEnded{Result: rules.Result{
			Winner: winner, Reason: rules.ReasonRepetition}})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := wireDecode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got := decoded.(game.GameEnded).Result.Winner; got != winner {
			t.Errorf("want %v, got %v", winner, got)
		}
	}
}

func wireEncode(event game.Event) ([]byte, error) { return wire.EncodeEvent(event) }
func wireDecode(data []byte) (game.Event, error)  { return wire.DecodeEvent(data) }

// A command the codec cannot encode must fail at the gateway rather than travel
// as something half-formed.
func TestForwardingAnUnknownCommandFails(t *testing.T) {
	nodeA, nodeB := newNode(t, "a"), newNode(t, "b")
	ctx := context.Background()
	id := newGame(t)

	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeB.hub.Execute(ctx, id, struct{ game.Command }{}); err == nil {
		t.Error("an unencodable command must not be forwarded")
	}
}

// The owner answers a corrupt command with an error rather than silence, so the
// gateway does not sit out the whole timeout.
func TestTheOwnerAnswersACorruptCommand(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)
	if _, err := nodeA.hub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}

	replyKey := "reply:test:" + store.NewID()
	payload, err := json.Marshal(forwardRequest{
		GameID: id, Command: []byte(`{"type":"teleport"}`), ReplyTo: replyKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := testRedis.XAdd(ctx, &redis.XAddArgs{
		Stream: commandStream(t.Name() + "-a"), Values: map[string]any{"payload": payload},
	}).Err(); err != nil {
		t.Fatal(err)
	}

	result, err := testRedis.BLPop(ctx, 5*time.Second, replyKey).Result()
	if err != nil {
		t.Fatalf("the owner never answered: %v", err)
	}
	var response forwardResponse
	if err := json.Unmarshal([]byte(result[1]), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == "" {
		t.Error("a corrupt command should come back as an error, not a success")
	}
}

func TestForwardingReportsRedisFailures(t *testing.T) {
	nodeA := newNode(t, "a")
	ctx := context.Background()
	id := newGame(t)

	// A lease naming a node that will never consume, so Execute takes the forward
	// path, with a context that is already dead.
	ghost := cluster.New(testRedis, t.Name()+"-ghost").WithTTL(time.Minute)
	if got, err := ghost.Acquire(ctx, id); err != nil || !got {
		t.Fatal("setup")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := nodeA.hub.Execute(cancelled, id, game.PlayCommand{
		By: rules.Black, Move: rules.Pass,
	}); err == nil {
		t.Error("a failed forward must be reported")
	}
}

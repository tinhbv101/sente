package node

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
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
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

// The tests that actually prove R2: a node dies mid-game and another rebuilds it
// from the database with the right position and the right clock. Everything up to
// now tested the pieces; this tests them together, against a real PostgreSQL and a
// real Redis.

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

var epoch = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

func blitz() game.Config {
	return game.Config{
		Size: 9, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
		TimeControl: game.TimeControl{Kind: game.Absolute, MainTime: 10 * time.Minute},
	}
}

type collector struct {
	mu     sync.Mutex
	events []game.Event
}

func (c *collector) broadcast(_ string, events []game.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, events...)
}

func (c *collector) all() []game.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]game.Event(nil), c.events...)
}

// newNode builds an independent registry, as if it were a separate process.
func newNode(t *testing.T, name string, ttl time.Duration) (*Registry, *collector) {
	t.Helper()
	skipIfShort(t)
	events := &collector{}
	registry := New(Config{
		Leases:    cluster.New(testRedis, t.Name()+"-"+name).WithTTL(ttl),
		Games:     store.NewGames(testPool),
		Broadcast: events.broadcast,
		IdleAfter: time.Hour,
	})
	return registry, events
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

func play(t *testing.T, actor *game.Actor, by rules.Color, text string) {
	t.Helper()
	session := actor.Snapshot()
	_, _, err := actor.Send(context.Background(), game.PlayCommand{
		By: by, Move: rules.Play(coord(t, text)), ExpectedMoveNumber: session.MoveNumber(),
	})
	if err != nil {
		t.Fatalf("playing %s: %v", text, err)
	}
}

func TestAcquiringStartsAGameFromTheDatabase(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := registry.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if actor.Snapshot().Config.Size != 9 {
		t.Error("the game was not rebuilt")
	}
	// Acquiring again returns the running actor rather than starting a second one.
	again, err := registry.Acquire(ctx, id)
	if err != nil || again != actor {
		t.Errorf("want the same actor back, got %v %v", again, err)
	}
	if held := registry.Held(); len(held) != 1 || held[0] != id {
		t.Errorf("want the game listed as held, got %v", held)
	}
	registry.Drain(ctx)
}

func TestASecondNodeCannotTakeARunningGame(t *testing.T) {
	first, _ := newNode(t, "a", time.Minute)
	second, _ := newNode(t, "b", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	if _, err := first.Acquire(ctx, id); err != nil {
		t.Fatal(err)
	}
	defer first.Drain(ctx)

	if _, err := second.Acquire(ctx, id); err != ErrHeldElsewhere {
		t.Errorf("want ErrHeldElsewhere, got %v", err)
	}
	if _, ok := second.Get(id); ok {
		t.Error("the second node must not be running the game")
	}
}

func TestMovesReachTheDatabase(t *testing.T) {
	registry, events := newNode(t, "a", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := registry.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	play(t, actor, rules.Black, "e5")
	play(t, actor, rules.White, "e7")
	registry.Drain(ctx)

	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM moves WHERE game_id = $1`, id).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("want two stored moves, got %d", count)
	}
	if len(events.all()) < 2 {
		t.Errorf("want both moves broadcast, got %d events", len(events.all()))
	}
}

// The heart of it: node A dies mid-game, node B picks the game up and must have
// the same position, the same captures and the same turn.
func TestAnotherNodeTakesOverAfterACrash(t *testing.T) {
	crashing, _ := newNode(t, "crashing", 300*time.Millisecond)
	survivor, survivorEvents := newNode(t, "survivor", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := crashing.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// A game with a capture in it, so the takeover has something to get wrong.
	for _, step := range []struct {
		by   rules.Color
		text string
	}{
		{rules.Black, "a2"}, {rules.White, "a1"}, {rules.Black, "j9"}, {rules.White, "j8"},
		{rules.Black, "b1"},
	} {
		play(t, actor, step.by, step.text)
	}
	before := actor.Snapshot()
	if before.Engine.State.Captures.Black != 1 {
		t.Fatalf("setup: expected a capture, got %+v", before.Engine.State.Captures)
	}

	crashing.Crash() // goroutines stop; nothing is released

	// The survivor cannot take it until the lease lapses -- that is the safety net.
	if _, err := survivor.Acquire(ctx, id); err != ErrHeldElsewhere {
		t.Fatalf("the lease should still be held, got %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		owner, _ := survivor.config.Leases.Owner(ctx, id)
		return owner == ""
	}, "the crashed node's lease to lapse")

	taken, err := survivor.Acquire(ctx, id)
	if err != nil {
		t.Fatalf("the survivor should be able to take over: %v", err)
	}
	defer survivor.Drain(ctx)

	after := taken.Snapshot()
	if after.Engine.State.BoardHash != before.Engine.State.BoardHash {
		t.Error("the rebuilt position differs from the one that was lost")
	}
	if after.MoveNumber() != before.MoveNumber() {
		t.Errorf("move count: want %d, got %d", before.MoveNumber(), after.MoveNumber())
	}
	if after.Engine.State.Captures != before.Engine.State.Captures {
		t.Errorf("captures: want %+v, got %+v", before.Engine.State.Captures,
			after.Engine.State.Captures)
	}
	if after.ToPlay() != before.ToPlay() {
		t.Errorf("turn: want %v, got %v", before.ToPlay(), after.ToPlay())
	}

	// And the game carries on where it left off.
	play(t, taken, after.ToPlay(), "f5")
	if taken.Snapshot().MoveNumber() != before.MoveNumber()+1 {
		t.Error("the game did not continue after the takeover")
	}
	// This takeover was immediate, so there is nothing to give back. Handing out
	// time here would make restarting a node a way to farm it.
	for _, event := range survivorEvents.all() {
		if _, ok := event.(game.ClockAdjusted); ok {
			t.Error("a takeover inside the grace period must not adjust the clock")
		}
	}
}

// Nobody should lose on time because the server was down. The player on move gets
// the whole outage back (docs/04 §4.5).
func TestAStrandedGameDoesNotCostThePlayerTheirClock(t *testing.T) {
	crashing, _ := newNode(t, "crashing", 300*time.Millisecond)
	survivor, survivorEvents := newNode(t, "survivor", time.Minute)
	ctx := context.Background()

	config := blitz()
	config.TimeControl = game.TimeControl{Kind: game.Absolute, MainTime: 30 * time.Second}
	id, err := store.NewGames(testPool).Create(ctx, store.CreateParams{
		Config: config, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	actor, err := crashing.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	play(t, actor, rules.Black, "e5")
	crashing.Crash()

	// Backdate the game so the takeover sees a long outage, without waiting for one.
	if _, err := testPool.Exec(ctx, `
		UPDATE games SET last_activity_at = now() - interval '20 seconds' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		owner, _ := survivor.config.Leases.Owner(ctx, id)
		return owner == ""
	}, "the lease to lapse")

	taken, err := survivor.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer survivor.Drain(ctx)

	// White is on move with 30 seconds. Twenty of them were lost to the outage; if
	// they had been charged, White would be nearly out of time.
	session := taken.Snapshot()
	remaining := session.Clock.Remaining(session.ToPlay(), time.Now())
	if remaining < 25*time.Second {
		t.Errorf("the outage was charged to the player: %v left of 30s", remaining)
	}
	// And the game is still playable, not already lost.
	if session.Phase() != rules.Playing {
		t.Errorf("the game ended during the outage: %v", session.Phase())
	}

	// The players are told why their clock jumped, rather than left suspecting a bug.
	var adjustment *game.ClockAdjusted
	for _, event := range survivorEvents.all() {
		if typed, ok := event.(game.ClockAdjusted); ok {
			adjustment = &typed
		}
	}
	if adjustment == nil {
		t.Fatal("a stranded game must announce the time it gave back")
	}
	if adjustment.Player != session.ToPlay() {
		t.Errorf("the time goes to the player on move, not %v", adjustment.Player)
	}
	if adjustment.Delta < 15*time.Second || adjustment.Reason != "server_interruption" {
		t.Errorf("unexpected adjustment: %+v", adjustment)
	}
}

// A short blip is ordinary thinking time and must still be charged, or restarting
// a node would become a way to get free time.
func TestAShortInterruptionIsStillChargedAsThinkingTime(t *testing.T) {
	first, _ := newNode(t, "a", 300*time.Millisecond)
	second, _ := newNode(t, "b", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := first.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	play(t, actor, rules.Black, "e5")
	turnStarted := actor.Snapshot().Clock.TurnStartedAt
	first.Crash()

	waitFor(t, 3*time.Second, func() bool {
		owner, _ := second.config.Leases.Owner(ctx, id)
		return owner == ""
	}, "the lease to lapse")

	taken, err := second.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Drain(ctx)

	// Under the grace period the turn keeps its original reference point.
	if !taken.Snapshot().Clock.TurnStartedAt.Equal(turnStarted) {
		t.Error("a blip shorter than the grace period must not reset the turn")
	}
}

// Losing a lease means another node may already be writing. Carrying on would give
// the game two owners, so the actor has to stop.
func TestLosingALeaseStopsTheGameHere(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := registry.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the lease being taken: delete it behind the node's back.
	if err := testRedis.Del(ctx, "game:"+id+":owner").Err(); err != nil {
		t.Fatal(err)
	}

	lost, err := registry.Renew(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0] != id {
		t.Fatalf("want the game reported lost, got %v", lost)
	}
	select {
	case <-actor.Stopped():
	case <-time.After(2 * time.Second):
		t.Fatal("a game whose lease was lost must stop running")
	}
	if _, ok := registry.Get(id); ok {
		t.Error("a stopped game must leave the registry")
	}
}

func TestDrainingReleasesEverythingAtOnce(t *testing.T) {
	first, _ := newNode(t, "a", time.Hour)
	second, _ := newNode(t, "b", time.Minute)
	ctx := context.Background()

	var ids []string
	for i := 0; i < 3; i++ {
		id := newGame(t)
		ids = append(ids, id)
		if _, err := first.Acquire(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	first.Drain(ctx)

	if held := first.Held(); len(held) != 0 {
		t.Errorf("a drained node should hold nothing, got %v", held)
	}
	// With a one-hour TTL, only a proper release makes these available at once.
	for _, id := range ids {
		if _, err := second.Acquire(ctx, id); err != nil {
			t.Errorf("game %s was not released: %v", id, err)
		}
	}
	second.Drain(ctx)
}

func TestAFinishedGameReleasesItsLease(t *testing.T) {
	registry, _ := newNode(t, "a", time.Hour)
	other, _ := newNode(t, "b", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := registry.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := actor.Send(ctx, game.PlayCommand{By: rules.Black, Move: rules.Resign}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-actor.Stopped():
	case <-time.After(2 * time.Second):
		t.Fatal("a finished game should stop")
	}
	waitFor(t, 2*time.Second, func() bool {
		owner, _ := other.config.Leases.Owner(ctx, id)
		return owner == ""
	}, "the finished game's lease to be released")
}

func TestAcquiringAMissingGameHoldsNoLease(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx := context.Background()
	missing := store.NewID()

	if _, err := registry.Acquire(ctx, missing); err == nil {
		t.Fatal("a game that does not exist cannot be run")
	}
	// The important half: a failed start must not leave the lease behind.
	owner, err := registry.config.Leases.Owner(ctx, missing)
	if err != nil {
		t.Fatal(err)
	}
	if owner != "" {
		t.Errorf("a failed start left the lease held by %q", owner)
	}
}

func TestDrainedRegistryRefusesNewGames(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx := context.Background()
	registry.Drain(ctx)
	if _, err := registry.Acquire(ctx, newGame(t)); err == nil {
		t.Error("a draining node must not take on new games")
	}
}

func TestRunKeepsTheHeartbeatGoing(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go registry.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		nodes, err := registry.config.Leases.LiveNodes(ctx, time.Now())
		if err != nil {
			return false
		}
		for _, node := range nodes {
			if node == registry.NodeID() {
				return true
			}
		}
		return false
	}, "the node to register itself")
}

func waitFor(t *testing.T, limit time.Duration, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A whole game through the running stack: actor, session, clock, negotiation and
// database. If any layer disagrees with another, this is where it shows.
func TestAWholeGameThroughToAnAgreedScore(t *testing.T) {
	registry, events := newNode(t, "a", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	actor, err := registry.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	play(t, actor, rules.Black, "c7")
	play(t, actor, rules.White, "g3")

	for _, by := range []rules.Color{rules.Black, rules.White} {
		if _, _, err := actor.Send(ctx, game.PlayCommand{
			By: by, Move: rules.Pass, ExpectedMoveNumber: actor.Snapshot().MoveNumber(),
		}); err != nil {
			t.Fatalf("passing as %v: %v", by, err)
		}
	}
	if actor.Snapshot().Phase() != rules.ScoringP {
		t.Fatalf("two passes should open scoring, got %v", actor.Snapshot().Phase())
	}

	// White decides Black's lone stone is dead, then both agree.
	if _, _, err := actor.Send(ctx, game.MarkDeadCommand{By: rules.White, Point: coord(t, "c7")}); err != nil {
		t.Fatal(err)
	}
	for _, by := range []rules.Color{rules.Black, rules.White} {
		if _, _, err := actor.Send(ctx, game.AcceptScoreCommand{By: by, Accepted: true}); err != nil {
			t.Fatalf("accepting as %v: %v", by, err)
		}
	}
	select {
	case <-actor.Stopped():
	case <-time.After(2 * time.Second):
		t.Fatal("an agreed result should end the game")
	}

	// The negotiation was checkpointed and the result written.
	var deadCount int
	if err := testPool.QueryRow(ctx,
		`SELECT coalesce(array_length(dead_points, 1), 0) FROM game_scoring WHERE game_id = $1`, id).
		Scan(&deadCount); err != nil {
		t.Fatalf("the negotiation was not checkpointed: %v", err)
	}
	if deadCount != 1 {
		t.Errorf("want one dead stone stored, got %d", deadCount)
	}

	loaded, err := store.NewGames(testPool).Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	result := loaded.Session.Result()
	if result == nil || result.Reason != rules.ReasonCounting || result.Score == nil {
		t.Fatalf("the agreed result was not stored: %+v", result)
	}
	// White holds a live stone and Black's was agreed dead, so White must be ahead.
	if result.Winner != rules.White {
		t.Errorf("want White to win this position, got %v", result.Winner)
	}

	var sawScoringChange bool
	for _, event := range events.all() {
		if _, ok := event.(game.ScoringChanged); ok {
			sawScoringChange = true
		}
	}
	if !sawScoringChange {
		t.Error("both players must see the score move as stones are marked")
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	registry := New(Config{Leases: cluster.New(testRedis, "defaults")})
	if registry.config.Time == nil {
		t.Error("a registry with no time source should use the system clock")
	}
	if registry.config.IdleAfter <= 0 {
		t.Error("a registry with no idle timeout should get a sensible one")
	}
	if registry.NodeID() != "defaults" {
		t.Errorf("node id: got %q", registry.NodeID())
	}
}

func TestRenewReportsRedisFailures(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx := context.Background()
	if _, err := registry.Acquire(ctx, newGame(t)); err != nil {
		t.Fatal(err)
	}
	defer registry.Drain(ctx)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := registry.Renew(cancelled); err == nil {
		t.Error("a failed renewal must be reported, not swallowed")
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { registry.Run(ctx); close(finished) }()

	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Run should stop when its context is cancelled")
	}
}

func TestAdoptRefusesWhatIsNotOurs(t *testing.T) {
	first, _ := newNode(t, "a", time.Minute)
	second, _ := newNode(t, "b", time.Minute)
	ctx := context.Background()
	id := newGame(t)

	// Nobody holds it: adopting is not the same as acquiring.
	if _, err := second.Adopt(ctx, id); err != ErrHeldElsewhere {
		t.Errorf("an unowned game cannot be adopted, got %v", err)
	}
	if _, err := first.Acquire(ctx, id); err != nil {
		t.Fatal(err)
	}
	defer first.Drain(ctx)

	// Held by another node: still not ours to adopt.
	if _, err := second.Adopt(ctx, id); err != ErrHeldElsewhere {
		t.Errorf("another node's game cannot be adopted, got %v", err)
	}
	// Already running here: adopting returns the actor we have.
	actor, err := first.Adopt(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if running, _ := first.Get(id); running != actor {
		t.Error("adopting a running game should return the running actor")
	}
}

func TestADrainingNodeAdoptsNothing(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	ctx := context.Background()
	id := newGame(t)
	if _, err := registry.Acquire(ctx, id); err != nil {
		t.Fatal(err)
	}
	registry.Drain(ctx)
	if _, err := registry.Adopt(ctx, id); err == nil {
		t.Error("a draining node must not take work back on")
	}
}

func TestAdoptReportsRedisFailures(t *testing.T) {
	registry, _ := newNode(t, "a", time.Minute)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Adopt(cancelled, "some-game"); err == nil {
		t.Error("a failed lease lookup must be reported")
	}
}

package cluster

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// Ownership is the riskiest part of the architecture (docs/10 R2), so these tests
// run against a real Redis: the Lua compare-and-swap and the TTL behaviour are
// exactly what a fake would get wrong.

var testClient *redis.Client

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start redis (is Docker running?): %v\n", err)
		os.Exit(1)
	}
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}
	options, err := redis.ParseURL(uri)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse url: %v\n", err)
		os.Exit(1)
	}
	testClient = redis.NewClient(options)
	code := m.Run()
	_ = testClient.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

var gameCounter atomic64

type atomic64 struct {
	mu sync.Mutex
	n  int64
}

func (a *atomic64) next() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	return a.n
}

// newGameID keeps tests independent without flushing the shared Redis.
func newGameID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), gameCounter.next())
}

func node(t *testing.T, id string) *Leases {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	return New(testClient, t.Name()+"-"+id).WithTTL(2 * time.Second)
}

func TestOnlyOneNodeCanHoldAGame(t *testing.T) {
	ctx := context.Background()
	first, second := node(t, "a"), node(t, "b")
	gameID := newGameID(t)

	got, err := first.Acquire(ctx, gameID)
	if err != nil || !got {
		t.Fatalf("the first node should win: %v %v", got, err)
	}
	got, err = second.Acquire(ctx, gameID)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("two nodes cannot own the same game")
	}
	owner, err := second.Owner(ctx, gameID)
	if err != nil {
		t.Fatal(err)
	}
	if owner != first.NodeID() {
		t.Errorf("want %s to own it, got %q", first.NodeID(), owner)
	}
}

// Many nodes racing for the same game: exactly one may win.
func TestConcurrentAcquisitionHasExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	gameID := newGameID(t)
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}

	const nodes = 25
	var wins int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < nodes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			leases := New(testClient, fmt.Sprintf("%s-%d", t.Name(), i)).WithTTL(5 * time.Second)
			<-start
			if got, err := leases.Acquire(ctx, gameID); err == nil && got {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Errorf("want exactly one winner, got %d", wins)
	}
}

func TestAnUnownedGameReportsNoOwner(t *testing.T) {
	leases := node(t, "a")
	owner, err := leases.Owner(context.Background(), newGameID(t))
	if err != nil {
		t.Fatal(err)
	}
	if owner != "" {
		t.Errorf("want no owner, got %q", owner)
	}
}

// The bug this guards against: a node that already lost its lease extending or
// deleting the new owner's.
func TestOnlyTheOwnerCanRenewOrRelease(t *testing.T) {
	ctx := context.Background()
	owner, other := node(t, "a"), node(t, "b")
	gameID := newGameID(t)

	if got, _ := owner.Acquire(ctx, gameID); !got {
		t.Fatal("setup: the first node should win")
	}
	renewed, err := other.Renew(ctx, gameID)
	if err != nil {
		t.Fatal(err)
	}
	if renewed {
		t.Error("a non-owner must not be able to extend the lease")
	}
	if err := other.Release(ctx, gameID); err != ErrNotOwner {
		t.Errorf("want ErrNotOwner, got %v", err)
	}
	// The real owner is untouched by the attempts.
	if current, _ := owner.Owner(ctx, gameID); current != owner.NodeID() {
		t.Errorf("ownership changed to %q", current)
	}
	if renewed, err := owner.Renew(ctx, gameID); err != nil || !renewed {
		t.Errorf("the owner must be able to renew: %v %v", renewed, err)
	}
}

func TestReleasingHandsTheGameOverImmediately(t *testing.T) {
	ctx := context.Background()
	first, second := node(t, "a"), node(t, "b")
	gameID := newGameID(t)

	if got, _ := first.Acquire(ctx, gameID); !got {
		t.Fatal("setup")
	}
	if err := first.Release(ctx, gameID); err != nil {
		t.Fatal(err)
	}
	// A draining node releases so the next owner does not wait out the TTL.
	if got, err := second.Acquire(ctx, gameID); err != nil || !got {
		t.Errorf("the next node should take over at once: %v %v", got, err)
	}
	held, err := first.HeldGames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range held {
		if id == gameID {
			t.Error("a released game must leave the node's index")
		}
	}
}

// A dead node stops renewing; the lease expires and someone else takes over. This
// is the fallback that makes the reaper an optimisation rather than a requirement.
func TestALeaseExpiresWhenNobodyRenewsIt(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	dead := New(testClient, t.Name()+"-dead").WithTTL(300 * time.Millisecond)
	live := New(testClient, t.Name()+"-live").WithTTL(time.Second)
	gameID := newGameID(t)

	if got, _ := dead.Acquire(ctx, gameID); !got {
		t.Fatal("setup")
	}
	if got, _ := live.Acquire(ctx, gameID); got {
		t.Fatal("the lease is still held")
	}

	waitFor(t, time.Second, func() bool {
		owner, _ := live.Owner(ctx, gameID)
		return owner == ""
	}, "lease to expire")

	if got, err := live.Acquire(ctx, gameID); err != nil || !got {
		t.Errorf("an expired lease must be takeable: %v %v", got, err)
	}
	// The dead node can no longer renew what it lost.
	if renewed, _ := dead.Renew(ctx, gameID); renewed {
		t.Error("a node that lost its lease must not be able to renew it")
	}
}

func TestRenewingKeepsALeaseAlive(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	owner := New(testClient, t.Name()+"-a").WithTTL(400 * time.Millisecond)
	gameID := newGameID(t)
	if got, _ := owner.Acquire(ctx, gameID); !got {
		t.Fatal("setup")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		renewed, err := owner.Renew(ctx, gameID)
		if err != nil {
			t.Fatal(err)
		}
		if !renewed {
			t.Fatal("a renewed lease must never lapse")
		}
	}
	if current, _ := owner.Owner(ctx, gameID); current != owner.NodeID() {
		t.Error("ownership lost despite renewal")
	}
}

// ── node liveness and reaping ───────────────────────────────────────────────

func TestHeartbeatsSeparateLiveNodesFromDead(t *testing.T) {
	ctx := context.Background()
	live, stale := node(t, "live"), node(t, "stale")
	now := time.Now()

	if err := live.Heartbeat(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := stale.Heartbeat(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	liveNodes, err := live.LiveNodes(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(liveNodes, live.NodeID()) {
		t.Error("a node that just beat must count as live")
	}
	if contains(liveNodes, stale.NodeID()) {
		t.Error("an hour-old heartbeat is not alive")
	}

	deadNodes, err := live.DeadNodes(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(deadNodes, stale.NodeID()) {
		t.Error("the stale node should be listed as dead")
	}
	if contains(deadNodes, live.NodeID()) {
		t.Error("the live node must not be listed as dead")
	}
}

// The reaper frees a crashed node's games without waiting out their TTL.
func TestReapingFreesTheGamesOfADeadNode(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	crashed := New(testClient, t.Name()+"-crashed").WithTTL(time.Hour)
	survivor := New(testClient, t.Name()+"-survivor").WithTTL(time.Minute)
	now := time.Now()

	var games []string
	for i := 0; i < 3; i++ {
		id := newGameID(t)
		games = append(games, id)
		if got, _ := crashed.Acquire(ctx, id); !got {
			t.Fatal("setup")
		}
	}
	if err := crashed.Heartbeat(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := survivor.Heartbeat(ctx, now); err != nil {
		t.Fatal(err)
	}

	freed, err := survivor.Reap(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if freed != 3 {
		t.Errorf("want three games freed, got %d", freed)
	}
	for _, id := range games {
		if got, err := survivor.Acquire(ctx, id); err != nil || !got {
			t.Errorf("game %s should be takeable after reaping: %v %v", id, got, err)
		}
	}
	// The dead node is forgotten, so a second pass finds nothing to do.
	if freed, err := survivor.Reap(ctx, now); err != nil || freed != 0 {
		t.Errorf("a second pass should be a no-op: %v %v", freed, err)
	}
}

// Several nodes may reap at once. Each lease is only cleared if it still names the
// dead node, so a live node's games are never taken from under it.
func TestReapingNeverTouchesALiveNodesGames(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	crashed := New(testClient, t.Name()+"-crashed").WithTTL(time.Hour)
	busy := New(testClient, t.Name()+"-busy").WithTTL(time.Hour)
	now := time.Now()

	crashedGame, busyGame := newGameID(t), newGameID(t)
	if got, _ := crashed.Acquire(ctx, crashedGame); !got {
		t.Fatal("setup")
	}
	if got, _ := busy.Acquire(ctx, busyGame); !got {
		t.Fatal("setup")
	}
	if err := crashed.Heartbeat(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := busy.Heartbeat(ctx, now); err != nil {
		t.Fatal(err)
	}

	// Two reapers racing, as would happen with several survivors.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = busy.Reap(ctx, now)
		}()
	}
	wg.Wait()

	if owner, _ := busy.Owner(ctx, busyGame); owner != busy.NodeID() {
		t.Errorf("a live node's game was reaped, owner is now %q", owner)
	}
	if owner, _ := busy.Owner(ctx, crashedGame); owner != "" {
		t.Errorf("the crashed node's game should be free, owner is %q", owner)
	}
}

// A node that drains cleanly takes itself off the roster.
func TestDeregisteringRemovesANodeFromTheRoster(t *testing.T) {
	ctx := context.Background()
	leases := node(t, "a")
	now := time.Now()
	if err := leases.Heartbeat(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := leases.Deregister(ctx); err != nil {
		t.Fatal(err)
	}
	live, err := leases.LiveNodes(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if contains(live, leases.NodeID()) {
		t.Error("a deregistered node must not be listed")
	}
}

func TestHeldGamesTracksWhatThisNodeOwns(t *testing.T) {
	ctx := context.Background()
	leases := node(t, "a")
	first, second := newGameID(t), newGameID(t)
	for _, id := range []string{first, second} {
		if got, _ := leases.Acquire(ctx, id); !got {
			t.Fatal("setup")
		}
	}
	held, err := leases.HeldGames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(held, first) || !contains(held, second) {
		t.Errorf("want both games listed, got %v", held)
	}
}

func TestRedisFailuresArePropagated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	leases := New(testClient, "cancelled").WithTTL(time.Second)

	if _, err := leases.Acquire(ctx, "g"); err == nil {
		t.Error("Acquire must report a failed write")
	}
	if _, err := leases.Renew(ctx, "g"); err == nil {
		t.Error("Renew must report a failed write")
	}
	if err := leases.Release(ctx, "g"); err == nil {
		t.Error("Release must report a failed write")
	}
	if _, err := leases.Owner(ctx, "g"); err == nil {
		t.Error("Owner must report a failed read")
	}
	if _, err := leases.HeldGames(ctx); err == nil {
		t.Error("HeldGames must report a failed read")
	}
	if err := leases.Heartbeat(ctx, time.Now()); err == nil {
		t.Error("Heartbeat must report a failed write")
	}
	if _, err := leases.LiveNodes(ctx, time.Now()); err == nil {
		t.Error("LiveNodes must report a failed read")
	}
	if _, err := leases.DeadNodes(ctx, time.Now()); err == nil {
		t.Error("DeadNodes must report a failed read")
	}
	if err := leases.Deregister(ctx); err == nil {
		t.Error("Deregister must report a failed write")
	}
	if _, err := leases.Reap(ctx, time.Now()); err == nil {
		t.Error("Reap must report a failed read")
	}
}

func TestDefaultsAreConsistent(t *testing.T) {
	if RenewInterval*3 > DefaultTTL {
		t.Error("renewal must leave room for at least two failed attempts")
	}
	if NodeTTL >= DefaultTTL {
		t.Error("a node must be declared dead before its leases expire, or reaping is pointless")
	}
	leases := New(nil, "n")
	if leases.ttl != DefaultTTL || leases.NodeID() != "n" {
		t.Error("New did not apply the defaults")
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// waitFor polls instead of sleeping a fixed amount, so the test is neither flaky
// nor slower than it needs to be.
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

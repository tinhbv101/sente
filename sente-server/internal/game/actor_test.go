package game

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"sente.app/server/internal/rules"
)

// The actor is tested with a fake clock rather than sleeps: a timeout test that
// waits on the wall clock is either slow or flaky, and usually both.

type fakeTimer struct {
	clock   *fakeClock
	dueAt   time.Time
	fire    func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	timer := &fakeTimer{clock: c, dueAt: c.now.Add(d), fire: f}
	if d <= 0 {
		// Already due. Fire off the lock, the way time.AfterFunc would.
		timer.stopped = true
		c.mu.Unlock()
		go f()
		return timer
	}
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
	return timer
}

// Advance moves time forward and fires whatever came due, earliest first.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := make([]*fakeTimer, 0, len(c.timers))
	remaining := c.timers[:0]
	for _, timer := range c.timers {
		if !timer.stopped && !timer.dueAt.After(c.now) {
			timer.stopped = true
			due = append(due, timer)
			continue
		}
		remaining = append(remaining, timer)
	}
	c.timers = remaining
	c.mu.Unlock()

	sort.Slice(due, func(i, j int) bool { return due[i].dueAt.Before(due[j].dueAt) })
	for _, timer := range due {
		timer.fire()
	}
}

type recorder struct {
	mu       sync.Mutex
	events   []Event
	persists int
	failNext error
}

func (r *recorder) persist(_ context.Context, _ GameSession, _ []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext != nil {
		err := r.failNext
		r.failNext = nil
		return err
	}
	r.persists++
	return nil
}

func (r *recorder) broadcast(_ string, events []Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, events...)
}

func (r *recorder) snapshot() (events []Event, persists int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...), r.persists
}

func startTestActor(t *testing.T, config Config) (*Actor, *fakeClock, *recorder) {
	t.Helper()
	clock := newFakeClock(epoch)
	session, err := NewSession(config, epoch)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	actor := StartActor(ActorConfig{
		GameID: "g1", Session: session, Time: clock,
		Persist: rec.persist, Broadcast: rec.broadcast,
	})
	t.Cleanup(actor.Stop)
	return actor, clock, rec
}

func TestActorAppliesCommandsInOrder(t *testing.T) {
	actor, clock, rec := startTestActor(t, blitz())
	ctx := context.Background()

	clock.Advance(30 * time.Second)
	events, session, err := actor.Send(ctx, PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eventOfType[MoveMade](events); !ok {
		t.Fatal("expected a move_made event")
	}
	if session.MoveNumber() != 1 || session.ToPlay() != rules.White {
		t.Errorf("unexpected state: move %d, %v to play", session.MoveNumber(), session.ToPlay())
	}
	broadcast, persists := rec.snapshot()
	if persists != 1 || len(broadcast) != 1 {
		t.Errorf("want one persist and one broadcast, got %d and %d", persists, len(broadcast))
	}
}

func TestSnapshotTracksTheLatestState(t *testing.T) {
	actor, _, _ := startTestActor(t, blitz())
	if actor.Snapshot().MoveNumber() != 0 {
		t.Fatal("a new game has no moves")
	}
	if _, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if actor.Snapshot().MoveNumber() != 1 {
		t.Error("the snapshot did not follow the move")
	}
}

// A rejected command must leave the game exactly as it was.
func TestARejectedCommandChangesNothing(t *testing.T) {
	actor, _, rec := startTestActor(t, blitz())
	_, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	})
	if err != rules.ErrNotYourTurn {
		t.Fatalf("want not_your_turn, got %v", err)
	}
	if actor.Snapshot().MoveNumber() != 0 {
		t.Error("a rejected move advanced the game")
	}
	if broadcast, persists := rec.snapshot(); persists != 0 || len(broadcast) != 0 {
		t.Error("a rejected move must not be written or announced")
	}
}

// The rule from docs/04 §7: refusing a move is recoverable, losing one is not. If
// the write fails the command is rejected and nothing is announced.
func TestAFailedWriteRejectsTheCommandAndAnnouncesNothing(t *testing.T) {
	actor, _, rec := startTestActor(t, blitz())
	rec.mu.Lock()
	rec.failNext = errors.New("disk on fire")
	rec.mu.Unlock()

	_, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	})
	if !errors.Is(err, ErrNotPersisted) {
		t.Fatalf("want ErrNotPersisted, got %v", err)
	}
	if actor.Snapshot().MoveNumber() != 0 {
		t.Error("an unwritten move must not be applied")
	}
	if broadcast, _ := rec.snapshot(); len(broadcast) != 0 {
		t.Error("an unwritten move must not be broadcast")
	}

	// The same move succeeds once the write works again.
	if _, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatalf("the retry should succeed: %v", err)
	}
}

// The clock timer is the server's own: nobody has to send anything for a player to
// lose on time.
func TestTheClockRunsOutWithoutAnyoneSendingAnything(t *testing.T) {
	actor, clock, rec := startTestActor(t, blitz())

	clock.Advance(11 * time.Minute)
	waitForActor(t, actor)

	session := actor.Snapshot()
	if session.Phase() != rules.Finished {
		t.Fatalf("want the game finished, got %v", session.Phase())
	}
	result := session.Result()
	if result == nil || result.Reason != rules.ReasonTimeout || result.Winner != rules.White {
		t.Errorf("want White by timeout, got %+v", result)
	}
	broadcast, _ := rec.snapshot()
	if _, ok := eventOfType[GameEnded](broadcast); !ok {
		t.Error("the timeout must be broadcast")
	}
}

// Rearming after every command is what stops a stale deadline from firing.
func TestTheTimerFollowsTheMovingDeadline(t *testing.T) {
	actor, clock, _ := startTestActor(t, blitz())
	ctx := context.Background()

	// Black moves after nine minutes, so White now has the full ten.
	clock.Advance(9 * time.Minute)
	if _, _, err := actor.Send(ctx, PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}

	// Black's old deadline would have fallen here; it must not fire.
	clock.Advance(2 * time.Minute)
	if actor.Snapshot().Phase() != rules.Playing {
		t.Fatal("a stale deadline ended the game")
	}
	// White's own deadline, on the other hand, does.
	clock.Advance(9 * time.Minute)
	waitForActor(t, actor)
	if result := actor.Snapshot().Result(); result == nil || result.Winner != rules.Black {
		t.Errorf("want Black by timeout, got %+v", result)
	}
}

func TestByoyomiPeriodsCountDownThroughTheActor(t *testing.T) {
	config := blitz()
	config.TimeControl = TimeControl{Kind: Byoyomi, MainTime: time.Minute, Periods: 2,
		PeriodTime: 30 * time.Second}
	actor, clock, _ := startTestActor(t, config)
	ctx := context.Background()

	// One whole period past the main time, once the lag grace is forgiven.
	clock.Advance(60*time.Second + 30*time.Second + LagGracePerMove)
	if _, _, err := actor.Send(ctx, PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if left := actor.Snapshot().Clock.Black.PeriodsLeft; left != 1 {
		t.Fatalf("want one period left, got %d", left)
	}
	// White now has a minute plus two periods before losing.
	clock.Advance(2*time.Minute + time.Second)
	waitForActor(t, actor)
	if result := actor.Snapshot().Result(); result == nil || result.Winner != rules.Black {
		t.Errorf("want Black by timeout, got %+v", result)
	}
}

// The actor exists to serialise access. Hammering it from many goroutines must
// still produce one coherent game.
func TestConcurrentSendersProduceOneCoherentGame(t *testing.T) {
	actor, _, _ := startTestActor(t, blitz())
	ctx := context.Background()

	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	points := []string{"a1", "b1", "c1", "d1", "e1", "f1", "g1", "h1"}

	for _, text := range points {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			// Every sender claims it is Black's first move; only one can be right.
			_, _, err := actor.Send(ctx, PlayCommand{
				By: rules.Black, Move: rules.Play(coord(t, text)), ExpectedMoveNumber: 0,
			})
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(text)
	}
	wg.Wait()

	if accepted != 1 {
		t.Errorf("want exactly one accepted move, got %d", accepted)
	}
	if got := actor.Snapshot().MoveNumber(); got != 1 {
		t.Errorf("want one move on the board, got %d", got)
	}
}

func TestTheActorStopsItselfOnceTheGameIsOver(t *testing.T) {
	actor, _, _ := startTestActor(t, blitz())
	if _, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.Black, Move: rules.Resign, ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-actor.Stopped():
	case <-time.After(2 * time.Second):
		t.Fatal("a finished game should not keep a goroutine alive")
	}
	if _, _, err := actor.Send(context.Background(), PlayCommand{By: rules.White, Move: rules.Pass}); err != ErrActorStopped {
		t.Errorf("want ErrActorStopped, got %v", err)
	}
}

// A quiet game gives up its lease so another node can take it (docs/04 §3).
func TestAQuietGameStopsAndReleasesItsLease(t *testing.T) {
	clock := newFakeClock(epoch)
	session, err := NewSession(blitz(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan string, 1)
	actor := StartActor(ActorConfig{
		GameID: "g1", Session: session, Time: clock,
		IdleAfter: 5 * time.Minute,
		OnStop:    func(id string) { released <- id },
	})
	t.Cleanup(actor.Stop)

	clock.Advance(6 * time.Minute)
	select {
	case id := <-released:
		if id != "g1" {
			t.Errorf("released the wrong game: %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an idle game should have stopped")
	}
}

func TestActivityPostponesTheIdleTimeout(t *testing.T) {
	clock := newFakeClock(epoch)
	session, _ := NewSession(blitz(), epoch)
	actor := StartActor(ActorConfig{
		GameID: "g1", Session: session, Time: clock, IdleAfter: 5 * time.Minute,
	})
	t.Cleanup(actor.Stop)

	clock.Advance(4 * time.Minute)
	if _, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(4 * time.Minute)
	select {
	case <-actor.Stopped():
		t.Fatal("the idle timer should have been restarted by the move")
	default:
	}
}

func TestStopIsIdempotentAndUnblocksSenders(t *testing.T) {
	actor, _, _ := startTestActor(t, blitz())
	actor.Stop()
	actor.Stop() // must not panic on a closed channel

	if _, _, err := actor.Send(context.Background(), PlayCommand{By: rules.Black, Move: rules.Pass}); err != ErrActorStopped {
		t.Errorf("want ErrActorStopped, got %v", err)
	}
}

func TestSendRespectsAContextDeadline(t *testing.T) {
	actor, _, _ := startTestActor(t, blitz())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := actor.Send(ctx, PlayCommand{By: rules.Black, Move: rules.Pass}); err != context.Canceled {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func TestAnActorWithNoTimeSourceUsesTheSystemClock(t *testing.T) {
	session, _ := NewSession(blitz(), time.Now())
	actor := StartActor(ActorConfig{GameID: "g1", Session: session})
	t.Cleanup(actor.Stop)
	if _, _, err := actor.Send(context.Background(), PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}); err != nil {
		t.Fatalf("the default time source should work: %v", err)
	}
}

func TestSystemTimeImplementsTheInterface(t *testing.T) {
	var source TimeSource = SystemTime{}
	if source.Now().IsZero() {
		t.Error("Now returned the zero time")
	}
	fired := make(chan struct{})
	timer := source.AfterFunc(time.Millisecond, func() { close(fired) })
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("AfterFunc never fired")
	}
	if timer.Stop() {
		t.Error("stopping an already fired timer should report false")
	}
}

// waitForActor waits for the loop to settle after a timer fired on another
// goroutine, without sleeping a fixed amount.
func waitForActor(t *testing.T, actor *Actor) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if actor.Snapshot().Phase() == rules.Finished {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the actor did not settle")
}

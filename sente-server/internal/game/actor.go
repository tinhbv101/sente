package game

import (
	"context"
	"errors"
	"sync"
	"time"

	"sente.app/server/internal/rules"
)

// The goroutine that owns one live game (docs/04 §3).
//
// All the decisions live in GameSession; this only supplies the three things a
// pure state machine cannot have: a single owner so no lock is needed, a timer for
// the clock, and the order in which a move is written and announced.

// TimeSource is injected so tests can drive the clock instead of sleeping.
type TimeSource interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type Timer interface{ Stop() bool }

// SystemTime is the production implementation.
type SystemTime struct{}

func (SystemTime) Now() time.Time { return time.Now() }

func (SystemTime) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

var (
	ErrActorStopped = errors.New("game: actor has stopped")
	// ErrNotPersisted means the command was legal but could not be written. The
	// session is left untouched: refusing a move is recoverable, losing one is not
	// (docs/04 §7).
	ErrNotPersisted = errors.New("game: could not persist the command")
)

type ActorConfig struct {
	GameID  string
	Session GameSession
	Time    TimeSource

	// Persist writes the result of a command. It runs before Broadcast, so a
	// client is never told about a move the database has not accepted.
	Persist func(ctx context.Context, session GameSession, events []Event) error
	// Broadcast fans events out to subscribers. Failures here are not fatal:
	// clients recover by reconnecting and resyncing.
	Broadcast func(gameID string, events []Event)
	// OnStop runs after the loop exits, which is where the owning node releases
	// the game's lease.
	OnStop func(gameID string)
	// IdleAfter stops the actor once a game has been quiet this long, freeing its
	// lease for another node. Zero means never.
	IdleAfter time.Duration
}

type Actor struct {
	config   ActorConfig
	inbox    chan request
	timeouts chan rules.Color
	quit     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once

	// Owned by the loop goroutine alone; never touched from outside.
	session     GameSession
	clockTimer  Timer
	idleTimer   Timer
	snapshotMu  sync.RWMutex
	snapshotted GameSession

	mu        sync.Mutex
	abandoned bool
}

type request struct {
	command Command
	at      time.Time
	reply   chan result
}

type result struct {
	events  []Event
	session GameSession
	err     error
}

func StartActor(config ActorConfig) *Actor {
	if config.Time == nil {
		config.Time = SystemTime{}
	}
	a := &Actor{
		config:      config,
		inbox:       make(chan request),
		timeouts:    make(chan rules.Color, 1),
		quit:        make(chan struct{}),
		stopped:     make(chan struct{}),
		session:     config.Session,
		snapshotted: config.Session,
	}
	// Armed before the goroutine starts: otherwise a caller can advance the clock
	// past a deadline that has not been registered yet.
	a.armClock()
	a.armIdle()
	go a.run()
	return a
}

// Send hands a command to the owning goroutine and waits for the outcome.
func (a *Actor) Send(ctx context.Context, command Command) ([]Event, GameSession, error) {
	reply := make(chan result, 1)
	req := request{command: command, at: a.config.Time.Now(), reply: reply}

	select {
	case a.inbox <- req:
	case <-a.stopped:
		return nil, GameSession{}, ErrActorStopped
	case <-ctx.Done():
		return nil, GameSession{}, ctx.Err()
	}

	select {
	case out := <-reply:
		return out.events, out.session, out.err
	case <-ctx.Done():
		return nil, GameSession{}, ctx.Err()
	}
}

// Snapshot is a consistent copy for readers that must not block the loop, such as
// a client joining mid-game.
func (a *Actor) Snapshot() GameSession {
	a.snapshotMu.RLock()
	defer a.snapshotMu.RUnlock()
	return a.snapshotted
}

// Stop shuts the actor down. Safe to call more than once.
func (a *Actor) Stop() {
	a.stopOnce.Do(func() { close(a.quit) })
	<-a.stopped
}

// Abandon stops the loop without running OnStop, modelling a node that died
// rather than one that shut down. Used by the takeover tests.
func (a *Actor) Abandon() {
	a.mu.Lock()
	a.abandoned = true
	a.mu.Unlock()
	a.stopOnce.Do(func() { close(a.quit) })
	<-a.stopped
}

// Stopped is closed once the loop has exited.
func (a *Actor) Stopped() <-chan struct{} { return a.stopped }

func (a *Actor) run() {
	defer func() {
		a.cancelTimers()
		a.mu.Lock()
		abandoned := a.abandoned
		a.mu.Unlock()
		// OnStop runs before the stopped channel closes, so a caller waiting in
		// Stop can rely on the lease already being released. Closing first would
		// let a drained node's games look held for a moment longer.
		if a.config.OnStop != nil && !abandoned {
			a.config.OnStop(a.config.GameID)
		}
		close(a.stopped)
	}()

	for {
		select {
		case req := <-a.inbox:
			a.handle(req)
		case player := <-a.timeouts:
			a.handle(request{
				command: TimeoutCommand{Player: player},
				at:      a.config.Time.Now(),
				reply:   nil,
			})
		case <-a.quit:
			return
		}
		if a.session.Phase() == rules.Finished {
			// A finished game has nothing left to own.
			return
		}
	}
}

func (a *Actor) handle(req request) {
	events, session, err := a.apply(req)
	// Rearm before answering: once Send returns, the caller may advance a clock or
	// inspect state, and a timer that has not been registered yet would simply
	// never fire. Broadcasting is not state, so it can wait until after.
	if err == nil {
		a.armClock()
		a.armIdle()
	}
	if req.reply != nil {
		req.reply <- result{events: events, session: session, err: err}
	}
	if err != nil {
		return
	}
	if a.config.Broadcast != nil && len(events) > 0 {
		a.config.Broadcast(a.config.GameID, events)
	}
}

// apply runs the command, persists the outcome, and only then commits it. The
// order matters: a client must never be told about a move the database refused.
func (a *Actor) apply(req request) ([]Event, GameSession, error) {
	next, events, err := a.session.Apply(req.command, req.at)
	if err != nil {
		return nil, a.session, err
	}
	if a.config.Persist != nil {
		if persistErr := a.config.Persist(context.Background(), next, events); persistErr != nil {
			return nil, a.session, errors.Join(ErrNotPersisted, persistErr)
		}
	}
	a.session = next
	a.snapshotMu.Lock()
	a.snapshotted = next
	a.snapshotMu.Unlock()
	return events, next, nil
}

// armClock points the timer at the moment the player on move loses. Rearmed after
// every command, because every command can change the deadline.
func (a *Actor) armClock() {
	if a.clockTimer != nil {
		a.clockTimer.Stop()
		a.clockTimer = nil
	}
	if a.session.Phase() != rules.Playing {
		return
	}
	player := a.session.ToPlay()
	delay := a.session.Clock.TimerDeadline(player).Sub(a.config.Time.Now())
	if delay < 0 {
		delay = 0
	}
	a.clockTimer = a.config.Time.AfterFunc(delay, func() {
		// Non-blocking: if a timeout is already queued, one is enough.
		select {
		case a.timeouts <- player:
		default:
		}
	})
}

func (a *Actor) armIdle() {
	if a.idleTimer != nil {
		a.idleTimer.Stop()
		a.idleTimer = nil
	}
	if a.config.IdleAfter <= 0 {
		return
	}
	a.idleTimer = a.config.Time.AfterFunc(a.config.IdleAfter, func() {
		a.stopOnce.Do(func() { close(a.quit) })
	})
}

func (a *Actor) cancelTimers() {
	if a.clockTimer != nil {
		a.clockTimer.Stop()
	}
	if a.idleTimer != nil {
		a.idleTimer.Stop()
	}
}

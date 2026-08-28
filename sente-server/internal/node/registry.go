// Package node ties the three halves of a running server together: the lease that
// says who owns a game, the store it is rebuilt from, and the goroutine that runs it.
package node

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

// TakeoverGrace is how long an interruption may last before the player on move is
// given the time back. Below it, the gap is charged as ordinary thinking time
// (docs/04 §4.5).
const TakeoverGrace = 15 * time.Second

// ErrHeldElsewhere means another node owns the game. The caller should forward the
// command rather than treat this as a failure.
var ErrHeldElsewhere = errors.New("node: game is held by another node")

type Config struct {
	Leases *cluster.Leases
	Games  *store.Games
	Time   game.TimeSource

	// Broadcast fans events out to whoever is watching the game.
	Broadcast func(gameID string, events []game.Event)
	// IdleAfter releases a quiet game so another node can take it.
	IdleAfter time.Duration
}

type Registry struct {
	config Config

	mu     sync.Mutex
	actors map[string]*game.Actor
	// closed guards against starting new actors while draining.
	draining bool
}

func New(config Config) *Registry {
	if config.Time == nil {
		config.Time = game.SystemTime{}
	}
	if config.IdleAfter == 0 {
		config.IdleAfter = 5 * time.Minute
	}
	return &Registry{config: config, actors: map[string]*game.Actor{}}
}

func (r *Registry) NodeID() string { return r.config.Leases.NodeID() }

// Held lists the games this node is currently running.
func (r *Registry) Held() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.actors))
	for id := range r.actors {
		ids = append(ids, id)
	}
	return ids
}

// Acquire takes ownership of a game and starts running it, or returns the actor
// already running here. ErrHeldElsewhere means another node has it.
func (r *Registry) Acquire(ctx context.Context, gameID string) (*game.Actor, error) {
	r.mu.Lock()
	if r.draining {
		r.mu.Unlock()
		return nil, errors.New("node: registry is draining")
	}
	if actor, ok := r.actors[gameID]; ok {
		r.mu.Unlock()
		return actor, nil
	}
	r.mu.Unlock()

	got, err := r.config.Leases.Acquire(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if !got {
		return nil, ErrHeldElsewhere
	}

	actor, err := r.start(ctx, gameID)
	if err != nil {
		// Never hold a lease for a game we failed to run.
		_ = r.config.Leases.Release(ctx, gameID)
		return nil, err
	}
	return actor, nil
}

// Adopt starts running a game whose lease this node already holds. That happens
// after a restart under the same node id, or when an actor stopped without
// releasing: the lease is ours, so there is nothing to acquire.
func (r *Registry) Adopt(ctx context.Context, gameID string) (*game.Actor, error) {
	r.mu.Lock()
	if r.draining {
		r.mu.Unlock()
		return nil, errors.New("node: registry is draining")
	}
	if actor, ok := r.actors[gameID]; ok {
		r.mu.Unlock()
		return actor, nil
	}
	r.mu.Unlock()

	owner, err := r.config.Leases.Owner(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if owner != r.NodeID() {
		return nil, ErrHeldElsewhere
	}
	return r.start(ctx, gameID)
}

// Get returns the actor for a game if this node is running it.
func (r *Registry) Get(gameID string) (*game.Actor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	actor, ok := r.actors[gameID]
	return actor, ok
}

func (r *Registry) start(ctx context.Context, gameID string) (*game.Actor, error) {
	loaded, err := r.config.Games.Load(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("node: rebuilding game %s: %w", gameID, err)
	}
	session := loaded.Session
	now := r.config.Time.Now()

	// Work out whether the game was stranded, and if so hand the time back.
	var adjustments []game.Event
	if session.Phase() == rules.Playing {
		gap := now.Sub(loaded.LastActivityAt)
		if gap > TakeoverGrace {
			session = session.ResumeTurnAt(now)
			adjustments = append(adjustments, game.ClockAdjusted{
				Player: session.ToPlay(), Delta: gap, Reason: "server_interruption",
			})
		}
	}

	actor := game.StartActor(game.ActorConfig{
		GameID:  gameID,
		Session: session,
		Time:    r.config.Time,
		Persist: func(ctx context.Context, session game.GameSession, events []game.Event) error {
			return r.persist(ctx, gameID, session, events)
		},
		Broadcast: r.config.Broadcast,
		OnStop:    func(id string) { r.onActorStopped(id) },
		IdleAfter: r.config.IdleAfter,
	})

	r.mu.Lock()
	r.actors[gameID] = actor
	r.mu.Unlock()

	if len(adjustments) > 0 {
		// Persisted as an audit entry, not as game state: the clock itself is
		// already written by the next command.
		_ = r.config.Games.RecordEvent(ctx, gameID, session.MoveNumber(), "clock_adjusted", "",
			map[string]any{"reason": "server_interruption", "ms": now.Sub(loaded.LastActivityAt).Milliseconds()})
		if r.config.Broadcast != nil {
			r.config.Broadcast(gameID, adjustments)
		}
	}
	return actor, nil
}

// persist writes whatever a command produced. Moves go to the append-only log;
// everything else updates the game row.
func (r *Registry) persist(ctx context.Context, gameID string, session game.GameSession,
	events []game.Event) error {
	for _, event := range events {
		made, ok := event.(game.MoveMade)
		if !ok || made.Duplicate {
			continue
		}
		clock := session.Clock.For(made.By.Opponent())
		timeLeft := int(clock.Main / time.Millisecond)
		periods := clock.PeriodsLeft
		if _, err := r.config.Games.AppendMove(ctx, gameID, session, store.MoveRecord{
			MoveNo: made.MoveNumber, Color: made.By, Move: made.Move,
			CapturedCount: len(made.Captured), BoardHash: made.BoardHash,
			PlayedAt: session.Clock.TurnStartedAt, TimeLeftMs: &timeLeft, PeriodsLeft: &periods,
		}); err != nil {
			return err
		}
	}
	// Anything that ended the game or changed the negotiation still has to land.
	for _, event := range events {
		switch event.(type) {
		case game.GameEnded, game.PlayResumed, game.UndoResolved:
			if err := r.config.Games.Finish(ctx, gameID, session, r.config.Time.Now()); err != nil {
				return err
			}
		case game.ScoringOpened, game.ScoringChanged:
			if err := r.config.Games.SaveScoring(ctx, gameID, session); err != nil {
				return err
			}
		}
	}
	return nil
}

// Forget drops a game from this node's table without touching its lease. Used
// when an actor was abandoned rather than stopped cleanly.
func (r *Registry) Forget(gameID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.actors, gameID)
}

func (r *Registry) onActorStopped(gameID string) {
	r.mu.Lock()
	delete(r.actors, gameID)
	r.mu.Unlock()
	// Release on a fresh context: the one that started the game may be long gone.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.config.Leases.Release(ctx, gameID)
}

// Renew extends the lease on every game this node runs, and stops any game whose
// lease was lost -- continuing to write to it would mean two owners.
func (r *Registry) Renew(ctx context.Context) (lost []string, err error) {
	for _, gameID := range r.Held() {
		held, renewErr := r.config.Leases.Renew(ctx, gameID)
		if renewErr != nil {
			return lost, renewErr
		}
		if held {
			continue
		}
		lost = append(lost, gameID)
		if actor, ok := r.Get(gameID); ok {
			actor.Stop()
		}
	}
	return lost, nil
}

// Run keeps the node's heartbeat, lease renewals and reaping going until the
// context is cancelled.
func (r *Registry) Run(ctx context.Context) {
	ticker := time.NewTicker(cluster.RenewInterval)
	defer ticker.Stop()
	for {
		_ = r.config.Leases.Heartbeat(ctx, r.config.Time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = r.Renew(ctx)
			_, _ = r.config.Leases.Reap(ctx, r.config.Time.Now())
		}
	}
}

// Drain stops every game and hands its lease back, so a redeploying node does not
// leave games waiting out the TTL (docs/04 §5.2).
func (r *Registry) Drain(ctx context.Context) {
	r.mu.Lock()
	r.draining = true
	actors := make([]*game.Actor, 0, len(r.actors))
	for _, actor := range r.actors {
		actors = append(actors, actor)
	}
	r.mu.Unlock()

	for _, actor := range actors {
		actor.Stop() // OnStop releases the lease
	}
	_ = r.config.Leases.Deregister(ctx)
}

// Crash simulates a node dying: goroutines stop, but nothing is released and no
// deregistration happens. Only the lease TTL and the reaper can free these games.
// Exists for the takeover tests, which are the only proof that R2 is handled.
func (r *Registry) Crash() {
	r.mu.Lock()
	r.draining = true
	actors := r.actors
	r.actors = map[string]*game.Actor{}
	r.mu.Unlock()
	for _, actor := range actors {
		actor.Abandon()
	}
}

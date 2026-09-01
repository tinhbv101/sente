// Package sweep ends games whose clock ran out while nobody was running them.
//
// A live game's timer lives in its actor. Correspondence games sit idle for days,
// the actor stops after a few minutes, and with it the timer -- so something has
// to look at the database instead (docs/04 §4.4).
package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"sente.app/server/internal/game"
	"sente.app/server/internal/hub"
	"sente.app/server/internal/notify"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

type Sweeper struct {
	Games  *store.Games
	Hub    *hub.Hub
	Logger *slog.Logger
	Now    func() time.Time
	// Optional: with these set, players close to a correspondence timeout get
	// one warning per move (docs/01 FR-N1).
	Notifier *notify.Notifier
	Redis    *redis.Client
}

// Once expires every overdue game it can find. Going through the hub means the
// owning node -- whichever it is -- applies the timeout, writes the result and
// tells any connected clients, exactly as if the timer had fired in memory.
func (s *Sweeper) Once(ctx context.Context) (ended int) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.warnLowTime(ctx, now())
	ids, err := s.Games.ListExpired(ctx, now(), 100)
	if err != nil {
		s.Logger.Warn("listing expired games", "error", err)
		return 0
	}
	for _, id := range ids {
		loaded, err := s.Games.Load(ctx, id)
		if err != nil {
			s.Logger.Warn("loading expired game", "game_id", id, "error", err)
			continue
		}
		if loaded.Session.Phase() != rules.Playing {
			continue
		}
		_, err = s.Hub.Execute(ctx, id, game.TimeoutCommand{Player: loaded.Session.ToPlay()})
		if err != nil && err.Error() != string(game.ErrTimeNotUp) && err.Error() != string(game.ErrNotPlaying) {
			s.Logger.Warn("expiring game", "game_id", id, "error", err)
			continue
		}
		// Count what actually happened, not what we sent: the actor's own timer
		// usually ends the game the instant it is started, and then answers our
		// command with "not playing".
		if after, err := s.Games.Load(ctx, id); err == nil && after.Session.Phase() == rules.Finished {
			ended++
		}
	}
	return ended
}

// Run sweeps on an interval until the context ends. A minute is plenty: nobody
// notices a correspondence game ending sixty seconds late.
func (s *Sweeper) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ended := s.Once(ctx); ended > 0 {
				s.Logger.Info("expired games", "count", ended)
			}
		}
	}
}

// warnLowTime pushes at most one warning per (game, move): redis SETNX is the
// memory, sized to outlive any correspondence move.
func (s *Sweeper) warnLowTime(ctx context.Context, now time.Time) {
	if s.Notifier == nil || s.Redis == nil || !s.Notifier.Enabled() {
		return
	}
	games, err := s.Games.ListLowTime(ctx, now, 200)
	if err != nil {
		s.Logger.Warn("listing low-time games", "error", err)
		return
	}
	for _, g := range games {
		key := fmt.Sprintf("lowtime:%s:%d", g.ID, g.MoveNo)
		set, err := s.Redis.SetNX(ctx, key, 1, 14*24*time.Hour).Result()
		if err != nil || !set {
			continue
		}
		s.Notifier.LowTime(g.UserID, g.ID, g.Deadline.Sub(now))
	}
}

// Package game holds the parts of a game that the rules engine deliberately does
// not know about: the clock, and the actor that owns a live game.
package game

import (
	"errors"
	"fmt"
	"time"

	"sente.app/server/internal/rules"
)

// The server is the only authority on time (docs/02 §7). Clients never send how
// much time they have left, and anything they do send is ignored.

type TimeControlKind string

const (
	Absolute       TimeControlKind = "absolute"
	Byoyomi        TimeControlKind = "byoyomi"
	Fischer        TimeControlKind = "fischer"
	Correspondence TimeControlKind = "correspondence"
)

const (
	// LagGracePerMove forgives a little network latency on every move.
	LagGracePerMove = 500 * time.Millisecond
	// LagGraceTotal caps the whole game's forgiveness. Without it, deliberately
	// slowing every packet would buy 100 seconds over a 200-move game.
	LagGraceTotal = 30 * time.Second
)

type TimeControl struct {
	Kind       TimeControlKind `json:"kind"`
	MainTime   time.Duration   `json:"main_time"`
	Periods    int             `json:"periods,omitempty"`
	PeriodTime time.Duration   `json:"period_time,omitempty"`
	Increment  time.Duration   `json:"increment,omitempty"`
	MaxTime    time.Duration   `json:"max_time,omitempty"`
	PerMove    time.Duration   `json:"per_move,omitempty"`
}

func (tc TimeControl) Validate() error {
	switch tc.Kind {
	case Absolute:
		if tc.MainTime <= 0 {
			return errors.New("absolute: main time must be positive")
		}
	case Fischer:
		if tc.MainTime <= 0 {
			return errors.New("fischer: main time must be positive")
		}
		if tc.Increment < 0 {
			return errors.New("fischer: increment cannot be negative")
		}
		if tc.MaxTime > 0 && tc.MaxTime < tc.MainTime {
			return errors.New("fischer: max time is below the starting time")
		}
	case Byoyomi:
		if tc.MainTime < 0 {
			return errors.New("byo-yomi: main time cannot be negative")
		}
		if tc.Periods < 1 || tc.PeriodTime <= 0 {
			return errors.New("byo-yomi: needs at least one period of positive length")
		}
	case Correspondence:
		if tc.PerMove <= 0 {
			return errors.New("correspondence: time per move must be positive")
		}
	default:
		return fmt.Errorf("unknown time control %q", tc.Kind)
	}
	return nil
}

// PlayerClock is one player's remaining time.
type PlayerClock struct {
	Main         time.Duration `json:"main"`
	PeriodsLeft  int           `json:"periods_left,omitempty"`
	PeriodTime   time.Duration `json:"period_time,omitempty"`
	LagGraceUsed time.Duration `json:"lag_grace_used"`
}

// InByoyomi reports whether main time is spent and periods are counting.
func (p PlayerClock) InByoyomi() bool { return p.Main <= 0 && p.PeriodsLeft > 0 }

// Clock is immutable: Charge and StartTurn return a new value.
type Clock struct {
	Control       TimeControl `json:"control"`
	Black         PlayerClock `json:"black"`
	White         PlayerClock `json:"white"`
	TurnStartedAt time.Time   `json:"turn_started_at"`
}

func NewClock(control TimeControl, startedAt time.Time) (Clock, error) {
	if err := control.Validate(); err != nil {
		return Clock{}, err
	}
	side := PlayerClock{Main: control.MainTime}
	if control.Kind == Byoyomi {
		side.PeriodsLeft = control.Periods
		side.PeriodTime = control.PeriodTime
	}
	return Clock{Control: control, Black: side, White: side, TurnStartedAt: startedAt}, nil
}

func (c Clock) For(player rules.Color) PlayerClock {
	if player == rules.Black {
		return c.Black
	}
	return c.White
}

func (c Clock) with(player rules.Color, side PlayerClock) Clock {
	if player == rules.Black {
		c.Black = side
	} else {
		c.White = side
	}
	return c
}

// StartTurn resets the reference point without charging anyone. Used when a game
// resumes after an interruption.
func (c Clock) StartTurn(at time.Time) Clock {
	c.TurnStartedAt = at
	return c
}

// availableGrace is what is left of this player's lag allowance, capped per move.
func (p PlayerClock) availableGrace() time.Duration {
	remaining := LagGraceTotal - p.LagGraceUsed
	if remaining <= 0 {
		return 0
	}
	if remaining < LagGracePerMove {
		return remaining
	}
	return LagGracePerMove
}

// Deadline is the moment the player loses on time if nothing arrives. Lag grace is
// excluded on purpose: this is the number clients count down to, and a player
// should never see time they are not really allowed to use.
func (c Clock) Deadline(player rules.Color) time.Time {
	side := c.For(player)
	switch c.Control.Kind {
	case Byoyomi:
		periods := time.Duration(side.PeriodsLeft) * c.Control.PeriodTime
		return c.TurnStartedAt.Add(side.Main + periods)
	case Correspondence:
		return c.TurnStartedAt.Add(c.Control.PerMove)
	default:
		return c.TurnStartedAt.Add(side.Main)
	}
}

// TimerDeadline is when the server's own timeout should fire: the deadline plus
// whatever lag grace the player still has.
func (c Clock) TimerDeadline(player rules.Color) time.Time {
	return c.Deadline(player).Add(c.For(player).availableGrace())
}

// Remaining is what a client should display for a player mid-turn.
func (c Clock) Remaining(player rules.Color, now time.Time) time.Duration {
	if remaining := c.Deadline(player).Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}

// Charge deducts the mover's thinking time and hands the turn to the opponent.
// The two always happen together; splitting them invites a caller to forget one.
// The boolean reports a loss on time, in which case the returned clock is the
// state at the moment of the loss.
func (c Clock) Charge(player rules.Color, receivedAt time.Time) (Clock, bool) {
	side := c.For(player)

	elapsed := receivedAt.Sub(c.TurnStartedAt)
	if elapsed < 0 {
		// A clock that ran backwards is a bug elsewhere; never award time for it.
		elapsed = 0
	}
	grace := side.availableGrace()
	if grace > elapsed {
		grace = elapsed
	}
	elapsed -= grace
	side.LagGraceUsed += grace

	switch c.Control.Kind {
	case Absolute:
		side.Main -= elapsed
		if side.Main <= 0 {
			side.Main = 0
			return c.with(player, side), true
		}

	case Fischer:
		side.Main -= elapsed
		if side.Main <= 0 {
			side.Main = 0
			return c.with(player, side), true
		}
		side.Main += c.Control.Increment
		if c.Control.MaxTime > 0 && side.Main > c.Control.MaxTime {
			side.Main = c.Control.MaxTime
		}

	case Byoyomi:
		remaining := side.Main - elapsed
		if remaining >= 0 {
			side.Main = remaining
			break
		}
		// Main time is spent: each whole period overrun costs one period, and any
		// move made inside a period resets it in full.
		overrun := -remaining
		consumed := int(overrun / c.Control.PeriodTime)
		if consumed >= side.PeriodsLeft {
			side.Main = 0
			side.PeriodsLeft = 0
			side.PeriodTime = 0
			return c.with(player, side), true
		}
		side.Main = 0
		side.PeriodsLeft -= consumed
		side.PeriodTime = c.Control.PeriodTime

	case Correspondence:
		if elapsed > c.Control.PerMove {
			return c.with(player, side), true
		}
	}

	next := c.with(player, side)
	next.TurnStartedAt = receivedAt
	return next, false
}

// HasTimedOut answers the server timer: has this player already lost on time?
func (c Clock) HasTimedOut(player rules.Color, now time.Time) bool {
	return !now.Before(c.TimerDeadline(player))
}

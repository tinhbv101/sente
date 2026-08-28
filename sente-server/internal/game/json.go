package game

import (
	"encoding/json"
	"fmt"
	"time"
)

// Wire and storage representation of the clock (docs/05 §5.1 and §6.1).
//
// Durations travel as whole milliseconds rather than Go's nanoseconds: the shape
// is read by the iOS client and by anyone looking at a JSONB column, and
// nanosecond counts are unreadable in both places.

func millis(d time.Duration) int64 { return int64(d / time.Millisecond) }

func fromMillis(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

type timeControlJSON struct {
	Kind        TimeControlKind `json:"kind"`
	MainTimeMs  int64           `json:"main_time_ms,omitempty"`
	Periods     int             `json:"periods,omitempty"`
	PeriodMs    int64           `json:"period_time_ms,omitempty"`
	IncrementMs int64           `json:"increment_ms,omitempty"`
	MaxTimeMs   int64           `json:"max_time_ms,omitempty"`
	DaysPerMove int             `json:"days_per_move,omitempty"`
}

func (tc TimeControl) MarshalJSON() ([]byte, error) {
	out := timeControlJSON{
		Kind:        tc.Kind,
		MainTimeMs:  millis(tc.MainTime),
		Periods:     tc.Periods,
		PeriodMs:    millis(tc.PeriodTime),
		IncrementMs: millis(tc.Increment),
		MaxTimeMs:   millis(tc.MaxTime),
	}
	if tc.Kind == Correspondence {
		if tc.PerMove%(24*time.Hour) != 0 {
			return nil, fmt.Errorf("correspondence time must be whole days, got %v", tc.PerMove)
		}
		out.DaysPerMove = int(tc.PerMove / (24 * time.Hour))
	}
	return json.Marshal(out)
}

func (tc *TimeControl) UnmarshalJSON(data []byte) error {
	var in timeControlJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*tc = TimeControl{
		Kind:       in.Kind,
		MainTime:   fromMillis(in.MainTimeMs),
		Periods:    in.Periods,
		PeriodTime: fromMillis(in.PeriodMs),
		Increment:  fromMillis(in.IncrementMs),
		MaxTime:    fromMillis(in.MaxTimeMs),
		PerMove:    time.Duration(in.DaysPerMove) * 24 * time.Hour,
	}
	if tc.Kind == "" {
		// An absent control is the zero value, not a corrupt one: a clock embedded
		// in an event may legitimately carry none. Stored configs are validated
		// where they are used, by NewSession and Restore.
		return nil
	}
	return tc.Validate()
}

type playerClockJSON struct {
	MainMs         int64 `json:"main_ms"`
	PeriodsLeft    int   `json:"periods_left,omitempty"`
	PeriodMs       int64 `json:"period_ms,omitempty"`
	LagGraceUsedMs int64 `json:"lag_grace_used_ms"`
}

func (p PlayerClock) MarshalJSON() ([]byte, error) {
	return json.Marshal(playerClockJSON{
		MainMs:         millis(p.Main),
		PeriodsLeft:    p.PeriodsLeft,
		PeriodMs:       millis(p.PeriodTime),
		LagGraceUsedMs: millis(p.LagGraceUsed),
	})
}

func (p *PlayerClock) UnmarshalJSON(data []byte) error {
	var in playerClockJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*p = PlayerClock{
		Main:         fromMillis(in.MainMs),
		PeriodsLeft:  in.PeriodsLeft,
		PeriodTime:   fromMillis(in.PeriodMs),
		LagGraceUsed: fromMillis(in.LagGraceUsedMs),
	}
	return nil
}

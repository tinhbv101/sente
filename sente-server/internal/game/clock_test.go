package game

import (
	"testing"
	"time"

	"sente.app/server/internal/rules"
)

// Time is never read from the wall here: every test hands the clock explicit
// timestamps, so byo-yomi boundaries can be probed to the millisecond without
// sleeping. Docs/10 lists a wrong byo-yomi edge case as risk R4.

var epoch = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

func newClock(t *testing.T, control TimeControl) Clock {
	t.Helper()
	clock, err := NewClock(control, epoch)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	return clock
}

// move charges `spent` of thinking time, skipping past the lag grace so tests read
// as the time actually consumed.
func move(t *testing.T, c Clock, player rules.Color, spent time.Duration) (Clock, bool) {
	t.Helper()
	return c.Charge(player, c.TurnStartedAt.Add(spent+LagGracePerMove))
}

func byoyomi() TimeControl {
	return TimeControl{Kind: Byoyomi, MainTime: 60 * time.Second, Periods: 3,
		PeriodTime: 30 * time.Second}
}

func TestValidateRejectsNonsense(t *testing.T) {
	cases := map[string]TimeControl{
		"unknown kind":        {Kind: "hourglass"},
		"absolute no time":    {Kind: Absolute},
		"fischer no time":     {Kind: Fischer, Increment: time.Second},
		"fischer bad max":     {Kind: Fischer, MainTime: time.Minute, MaxTime: time.Second},
		"fischer negative":    {Kind: Fischer, MainTime: time.Minute, Increment: -time.Second},
		"byoyomi no periods":  {Kind: Byoyomi, MainTime: time.Minute, PeriodTime: time.Second},
		"byoyomi zero period": {Kind: Byoyomi, MainTime: time.Minute, Periods: 3},
		"byoyomi negative":    {Kind: Byoyomi, MainTime: -time.Second, Periods: 3, PeriodTime: time.Second},
		"correspondence zero": {Kind: Correspondence},
	}
	for name, control := range cases {
		if err := control.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if _, err := NewClock(control, epoch); err == nil {
			t.Errorf("%s: NewClock accepted it", name)
		}
	}
	// Byo-yomi with no main time at all is legal: sudden death into periods.
	if err := (TimeControl{Kind: Byoyomi, Periods: 1, PeriodTime: time.Second}).Validate(); err != nil {
		t.Errorf("sudden-death byo-yomi should be valid: %v", err)
	}
}

func TestAbsoluteCountsDown(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Absolute, MainTime: 10 * time.Minute})
	clock, out := move(t, clock, rules.Black, 90*time.Second)
	if out {
		t.Fatal("should not have timed out")
	}
	if got := clock.Black.Main; got != 8*time.Minute+30*time.Second {
		t.Errorf("main time: got %v", got)
	}
	if !clock.TurnStartedAt.After(epoch) {
		t.Error("the turn reference did not move to the opponent")
	}
}

func TestAbsoluteTimesOutExactlyAtZero(t *testing.T) {
	control := TimeControl{Kind: Absolute, MainTime: 10 * time.Second}
	// One millisecond inside the limit still counts.
	if _, out := move(t, newClock(t, control), rules.Black, 10*time.Second-time.Millisecond); out {
		t.Error("a move just inside the limit must not lose")
	}
	// Spending exactly the whole clock loses: the spec says remaining <= 0.
	clock, out := move(t, newClock(t, control), rules.Black, 10*time.Second)
	if !out {
		t.Error("spending the whole clock must lose")
	}
	if clock.Black.Main != 0 {
		t.Errorf("a lost clock should read zero, got %v", clock.Black.Main)
	}
}

func TestFischerAddsIncrementAndRespectsTheCap(t *testing.T) {
	control := TimeControl{Kind: Fischer, MainTime: 60 * time.Second,
		Increment: 10 * time.Second, MaxTime: 65 * time.Second}
	clock := newClock(t, control)

	clock, _ = move(t, clock, rules.Black, 2*time.Second)
	if got := clock.Black.Main; got != 65*time.Second {
		t.Errorf("58s + 10s should clamp to the 65s cap, got %v", got)
	}
	clock, _ = move(t, clock, rules.Black, 30*time.Second)
	if got := clock.Black.Main; got != 45*time.Second {
		t.Errorf("65s - 30s + 10s = 45s, got %v", got)
	}
	// The increment is never awarded on the move that runs the clock out.
	if _, out := move(t, newClock(t, control), rules.Black, 60*time.Second); !out {
		t.Error("running out must lose even though an increment was due")
	}
}

func TestFischerWithoutACapGrowsFreely(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Fischer, MainTime: time.Minute, Increment: 10 * time.Second})
	for i := 0; i < 5; i++ {
		clock, _ = move(t, clock, rules.Black, time.Second)
	}
	if got := clock.Black.Main; got != time.Minute+45*time.Second {
		t.Errorf("five moves of 1s with a 10s increment should add 45s, got %v", got)
	}
}

func TestByoyomiSpendsMainTimeFirst(t *testing.T) {
	clock := newClock(t, byoyomi())
	clock, out := move(t, clock, rules.Black, 20*time.Second)
	if out {
		t.Fatal("plenty of time left")
	}
	if clock.Black.Main != 40*time.Second || clock.Black.PeriodsLeft != 3 {
		t.Errorf("main time should absorb the move: %+v", clock.Black)
	}
	if clock.Black.InByoyomi() {
		t.Error("still in main time")
	}
}

func TestByoyomiEntersPeriodsWhenMainTimeRunsOut(t *testing.T) {
	clock := newClock(t, byoyomi())
	// 60s main + 45s means the move overran by 45s: one whole period is consumed.
	clock, out := move(t, clock, rules.Black, 105*time.Second)
	if out {
		t.Fatal("two periods still remain")
	}
	if clock.Black.Main != 0 || clock.Black.PeriodsLeft != 2 {
		t.Errorf("want 0 main and 2 periods, got %+v", clock.Black)
	}
	if !clock.Black.InByoyomi() {
		t.Error("should be in byo-yomi now")
	}
	if clock.Black.PeriodTime != 30*time.Second {
		t.Error("the current period must reset in full")
	}
}

func TestByoyomiResetsEveryPeriodMovedInside(t *testing.T) {
	clock := newClock(t, byoyomi())
	clock, _ = move(t, clock, rules.Black, 60*time.Second) // main time exactly spent
	for i := 0; i < 20; i++ {
		var out bool
		clock, out = move(t, clock, rules.Black, 29*time.Second)
		if out {
			t.Fatalf("move %d: a move inside the period must never lose", i+1)
		}
		if clock.Black.PeriodsLeft != 3 {
			t.Fatalf("move %d: periods dropped to %d", i+1, clock.Black.PeriodsLeft)
		}
	}
}

func TestByoyomiConsumesSeveralPeriodsAtOnce(t *testing.T) {
	clock := newClock(t, byoyomi())
	clock, _ = move(t, clock, rules.Black, 60*time.Second)
	// 75s in byo-yomi is two whole periods plus 15s: two periods gone, one left.
	clock, out := move(t, clock, rules.Black, 75*time.Second)
	if out {
		t.Fatal("one period should still remain")
	}
	if clock.Black.PeriodsLeft != 1 {
		t.Errorf("want 1 period left, got %d", clock.Black.PeriodsLeft)
	}
}

func TestByoyomiLosesOnTheLastPeriod(t *testing.T) {
	clock := newClock(t, byoyomi())
	clock, _ = move(t, clock, rules.Black, 60*time.Second)
	clock, _ = move(t, clock, rules.Black, 75*time.Second) // down to one period
	if clock.Black.PeriodsLeft != 1 {
		t.Fatalf("setup wrong: %+v", clock.Black)
	}
	// Using exactly the last period is a loss: the move must land inside it.
	clock, out := move(t, clock, rules.Black, 30*time.Second)
	if !out {
		t.Fatal("exhausting the final period must lose")
	}
	if clock.Black.PeriodsLeft != 0 || clock.Black.Main != 0 {
		t.Errorf("a lost clock should read zero: %+v", clock.Black)
	}
}

func TestByoyomiOnePeriodInsideTheLastIsSurvived(t *testing.T) {
	clock := newClock(t, byoyomi())
	clock, _ = move(t, clock, rules.Black, 60*time.Second)
	clock, _ = move(t, clock, rules.Black, 75*time.Second)
	if _, out := move(t, clock, rules.Black, 30*time.Second-time.Millisecond); out {
		t.Error("one millisecond inside the last period must survive")
	}
}

func TestSuddenDeathByoyomi(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Byoyomi, Periods: 1, PeriodTime: 10 * time.Second})
	if !clock.Black.InByoyomi() {
		t.Error("with no main time the player starts in byo-yomi")
	}
	clock, out := move(t, clock, rules.Black, 9*time.Second)
	if out || clock.Black.PeriodsLeft != 1 {
		t.Errorf("a move inside the only period must survive: out=%v %+v", out, clock.Black)
	}
	if _, out := move(t, clock, rules.Black, 10*time.Second); !out {
		t.Error("exhausting the only period must lose")
	}
}

func TestCorrespondenceUsesAFlatBudgetPerMove(t *testing.T) {
	control := TimeControl{Kind: Correspondence, PerMove: 48 * time.Hour}
	if _, out := move(t, newClock(t, control), rules.Black, 47*time.Hour); out {
		t.Error("inside the budget must not lose")
	}
	if _, out := move(t, newClock(t, control), rules.Black, 48*time.Hour+time.Second); !out {
		t.Error("past the budget must lose")
	}
	// A correspondence move never eats into a running main clock.
	clock, _ := move(t, newClock(t, control), rules.Black, time.Hour)
	if clock.Black.Main != 0 {
		t.Errorf("correspondence has no main time, got %v", clock.Black.Main)
	}
}

func TestLagGraceIsForgivenPerMoveAndCappedOverTheGame(t *testing.T) {
	control := TimeControl{Kind: Absolute, MainTime: 10 * time.Minute}
	clock := newClock(t, control)

	// Exactly the grace is forgiven, so the clock does not move at all.
	clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(LagGracePerMove))
	if clock.Black.Main != 10*time.Minute {
		t.Errorf("the grace should absorb the whole move, got %v", clock.Black.Main)
	}
	if clock.Black.LagGraceUsed != LagGracePerMove {
		t.Errorf("grace used: got %v", clock.Black.LagGraceUsed)
	}

	// Burn the allowance: 60 moves at 500ms is exactly 30 seconds.
	for i := 1; i < 60; i++ {
		clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(LagGracePerMove))
	}
	if clock.Black.LagGraceUsed != LagGraceTotal {
		t.Fatalf("want the full allowance spent, got %v", clock.Black.LagGraceUsed)
	}
	if clock.Black.Main != 10*time.Minute {
		t.Fatalf("no time should have been charged yet, got %v", clock.Black.Main)
	}

	// The allowance is gone, so the next move is charged in full.
	clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(LagGracePerMove))
	if clock.Black.Main != 10*time.Minute-LagGracePerMove {
		t.Errorf("past the cap the move must be charged, got %v", clock.Black.Main)
	}
}

func TestGraceNeverAwardsTimeOnAShortMove(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Absolute, MainTime: time.Minute})
	clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(100*time.Millisecond))
	if clock.Black.Main != time.Minute {
		t.Errorf("main time must not grow, got %v", clock.Black.Main)
	}
	if clock.Black.LagGraceUsed != 100*time.Millisecond {
		t.Errorf("only the time actually spent may be forgiven, got %v", clock.Black.LagGraceUsed)
	}
}

func TestAClockRunningBackwardsChargesNothing(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Absolute, MainTime: time.Minute})
	clock, out := clock.Charge(rules.Black, clock.TurnStartedAt.Add(-time.Hour))
	if out || clock.Black.Main != time.Minute {
		t.Errorf("a backwards timestamp must never award or charge time: %+v", clock.Black)
	}
}

func TestChargingOnePlayerLeavesTheOtherAlone(t *testing.T) {
	clock := newClock(t, byoyomi())
	clock, _ = move(t, clock, rules.Black, 30*time.Second)
	if clock.White.Main != 60*time.Second || clock.White.PeriodsLeft != 3 {
		t.Errorf("white's clock changed: %+v", clock.White)
	}
}

func TestDeadlineAndTimerDeadline(t *testing.T) {
	clock := newClock(t, byoyomi())
	// Everything that is still available: main time plus every remaining period.
	want := epoch.Add(60*time.Second + 3*30*time.Second)
	if got := clock.Deadline(rules.Black); !got.Equal(want) {
		t.Errorf("byo-yomi deadline: want %v, got %v", want, got)
	}
	// The server's own timer waits out the lag grace on top.
	if got := clock.TimerDeadline(rules.Black); !got.Equal(want.Add(LagGracePerMove)) {
		t.Errorf("timer deadline: got %v", got)
	}
	if !clock.HasTimedOut(rules.Black, want.Add(LagGracePerMove)) {
		t.Error("at the timer deadline the player has lost")
	}
	if clock.HasTimedOut(rules.Black, want) {
		t.Error("at the displayed deadline the grace has not run out yet")
	}
}

func TestDeadlinePerKind(t *testing.T) {
	cases := []struct {
		name    string
		control TimeControl
		want    time.Duration
	}{
		{"absolute", TimeControl{Kind: Absolute, MainTime: 5 * time.Minute}, 5 * time.Minute},
		{"fischer", TimeControl{Kind: Fischer, MainTime: 3 * time.Minute}, 3 * time.Minute},
		{"correspondence", TimeControl{Kind: Correspondence, PerMove: 24 * time.Hour}, 24 * time.Hour},
	}
	for _, c := range cases {
		clock := newClock(t, c.control)
		if got := clock.Deadline(rules.Black); !got.Equal(epoch.Add(c.want)) {
			t.Errorf("%s: want %v, got %v", c.name, epoch.Add(c.want), got)
		}
	}
}

func TestRemainingNeverGoesNegative(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Absolute, MainTime: time.Minute})
	if got := clock.Remaining(rules.Black, epoch.Add(20*time.Second)); got != 40*time.Second {
		t.Errorf("want 40s remaining, got %v", got)
	}
	if got := clock.Remaining(rules.Black, epoch.Add(2*time.Minute)); got != 0 {
		t.Errorf("want 0 once the deadline passes, got %v", got)
	}
}

func TestStartTurnMovesTheReferenceWithoutCharging(t *testing.T) {
	clock := newClock(t, byoyomi())
	later := epoch.Add(time.Hour)
	resumed := clock.StartTurn(later)
	if resumed.Black != clock.Black || resumed.White != clock.White {
		t.Error("StartTurn must not touch either clock")
	}
	if !resumed.TurnStartedAt.Equal(later) {
		t.Error("StartTurn must move the reference point")
	}
}

// A whole game of alternating moves must leave both clocks consistent, which is
// the property the actor depends on when it rebuilds a game from the moves table.
func TestAlternatingMovesStayConsistent(t *testing.T) {
	clock := newClock(t, byoyomi())
	player := rules.Black
	for i := 0; i < 40; i++ {
		var out bool
		clock, out = move(t, clock, player, 2*time.Second)
		if out {
			t.Fatalf("move %d: nobody should be short of time yet", i+1)
		}
		player = player.Opponent()
	}
	// 20 moves of 2s each leaves 20s of the original minute.
	if clock.Black.Main != 20*time.Second || clock.White.Main != 20*time.Second {
		t.Errorf("clocks drifted: black=%v white=%v", clock.Black.Main, clock.White.Main)
	}
}

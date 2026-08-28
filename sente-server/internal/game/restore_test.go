package game

import (
	"testing"
	"time"

	"sente.app/server/internal/rules"
)

// What Restore has to reproduce is everything replaying the moves cannot: the
// clock, the negotiation, an ending no move caused, and the two counters the
// session keeps privately.

func playedSession(t *testing.T) GameSession {
	t.Helper()
	session := newSession(t, blitz())
	session, _, err := session.Apply(PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")),
		ClientMoveID: "key-1", ExpectedMoveNumber: 0,
	}, at(session, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	session, _ = mustPlay(t, session, rules.White, rules.Play(coord(t, "e7")), time.Second)
	return session
}

func TestRestoreReproducesEverythingReplayCannot(t *testing.T) {
	original := playedSession(t)
	// Undo one move and put it back, so the undo counter is non-zero.
	original, _, _ = original.Apply(RequestUndoCommand{By: rules.White}, epoch)
	original, _, _ = original.Apply(RespondUndoCommand{By: rules.Black, Accept: true}, epoch)
	original, _ = mustPlay(t, original, rules.White, rules.Play(coord(t, "f5")), time.Second)

	if original.UndosUsed() != 1 {
		t.Fatalf("setup: want one undo used, got %d", original.UndosUsed())
	}
	ids := original.AppliedMoveIDs()
	if ids["key-1"] != 1 {
		t.Fatalf("setup: idempotency key missing, got %v", ids)
	}

	restored, err := Restore(RestoreParams{
		Config: original.Config, Moves: original.Moves, Clock: original.Clock,
		AppliedMoveIDs: ids, UndosUsed: original.UndosUsed(),
		ExpectedBoardHash: original.Engine.State.BoardHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Engine.State.BoardHash != original.Engine.State.BoardHash {
		t.Error("position lost")
	}
	if restored.UndosUsed() != 1 {
		t.Error("undo counter lost")
	}
	if restored.Clock.Black.Main != original.Clock.Black.Main {
		t.Error("clock lost")
	}

	// The restored session must still honour the idempotency key.
	_, events, err := restored.Apply(PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ClientMoveID: "key-1",
	}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if made, ok := eventOfType[MoveMade](events); !ok || !made.Duplicate {
		t.Error("a key that survived a restart must still be recognised")
	}
}

func TestAppliedMoveIDsIsACopy(t *testing.T) {
	session := playedSession(t)
	ids := session.AppliedMoveIDs()
	ids["key-1"] = 99
	if session.AppliedMoveIDs()["key-1"] != 1 {
		t.Error("the caller must not be able to reach into the session")
	}
}

func TestRestoreRefusesAPositionThatDisagreesWithItsChecksum(t *testing.T) {
	original := playedSession(t)
	_, err := Restore(RestoreParams{
		Config: original.Config, Moves: original.Moves, Clock: original.Clock,
		ExpectedBoardHash: 0xDEADBEEF,
	})
	mismatch, ok := err.(ErrChecksumMismatch)
	if !ok {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
	if mismatch.Stored != 0xDEADBEEF || mismatch.Rebuilt != original.Engine.State.BoardHash {
		t.Errorf("the error must name both hashes: %+v", mismatch)
	}
	if mismatch.Error() == "" {
		t.Error("the error needs a message someone can act on")
	}
}

func TestRestoreRefusesAnUnplayableMoveList(t *testing.T) {
	point := coord(t, "e5")
	_, err := Restore(RestoreParams{
		Config: blitz(),
		Moves: []rules.RecordedMove{
			{Player: rules.Black, Move: rules.Play(point)},
			{Player: rules.White, Move: rules.Play(point)}, // already occupied
		},
		Clock: newClock(t, blitz().TimeControl),
	})
	if err == nil {
		t.Fatal("a corrupt move list must not load as a shorter game")
	}
}

func TestRestoreRefusesAnInvalidTimeControl(t *testing.T) {
	config := blitz()
	config.TimeControl = TimeControl{Kind: Byoyomi, MainTime: time.Minute}
	if _, err := Restore(RestoreParams{Config: config}); err == nil {
		t.Error("an unplayable time control must not be restored")
	}
}

func TestRestoreAppliesAnEndingNoMoveCaused(t *testing.T) {
	original := playedSession(t)
	result := rules.Result{Winner: rules.White, Reason: rules.ReasonTimeout}
	restored, err := Restore(RestoreParams{
		Config: original.Config, Moves: original.Moves, Clock: original.Clock,
		Result: &result,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Phase() != rules.Finished || restored.Result().Reason != rules.ReasonTimeout {
		t.Errorf("the stored ending was lost: %+v", restored.Result())
	}
}

func TestRestoreScoringSessionCarriesConsent(t *testing.T) {
	dead := []rules.Point{coord(t, "e5")}
	suggested := []rules.Point{coord(t, "e5"), coord(t, "e7")}
	session := RestoreScoringSession(dead, suggested, true, false, 42)

	if len(session.DeadStones()) != 1 || session.ResumeFromMoveNumber != 42 {
		t.Errorf("marking or resume point lost: %+v", session)
	}
	if !session.Accepted(rules.Black) || session.Accepted(rules.White) {
		t.Error("consent lost")
	}
	if !session.EditedFromSuggestion() {
		t.Error("a marking narrower than the suggestion counts as edited")
	}
}

// Territory is what the client draws on the board while the players negotiate.
func TestScoringSessionExposesTheTerritoryMap(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(points(t, 9, "b2", "b1"), 128)

	territory := session.Territory(engine)
	if len(territory.Black) != 26 || len(territory.White) != 31 {
		t.Errorf("want 26 and 31 points, got %d and %d", len(territory.Black), len(territory.White))
	}
	if len(territory.Neutral) != 0 {
		t.Errorf("this position has no dame, got %d", len(territory.Neutral))
	}
}

func TestEditedFromSuggestionSpotsASwap(t *testing.T) {
	engine := halfPointGame(t)
	// Same number of stones marked, but a different chain: still an edit.
	session := NewScoringSession(points(t, 9, "b2", "b1"), 0).
		ToggleChain(engine.Board(), coord(t, "b1")).
		ToggleChain(engine.Board(), coord(t, "g7"))
	if !session.EditedFromSuggestion() {
		t.Error("swapping which stones are dead is an edit")
	}
}

// The lag allowance runs out mid-move: part of the move is forgiven, the rest is
// charged. Getting this wrong on the boundary would hand out free time.
func TestGraceRunningOutPartWayThroughAMove(t *testing.T) {
	clock := newClock(t, TimeControl{Kind: Absolute, MainTime: 10 * time.Minute})
	// Spend all but 200ms of the allowance.
	for i := 0; i < 59; i++ {
		clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(LagGracePerMove))
	}
	clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(300*time.Millisecond))
	if clock.Black.LagGraceUsed != LagGraceTotal-200*time.Millisecond {
		t.Fatalf("setup: allowance left is %v", LagGraceTotal-clock.Black.LagGraceUsed)
	}

	// A one-second move: 200ms forgiven, 800ms charged.
	clock, _ = clock.Charge(rules.Black, clock.TurnStartedAt.Add(time.Second))
	if got := 10*time.Minute - clock.Black.Main; got != 800*time.Millisecond {
		t.Errorf("want 800ms charged, got %v", got)
	}
	if clock.Black.LagGraceUsed != LagGraceTotal {
		t.Errorf("the allowance should be spent, got %v", clock.Black.LagGraceUsed)
	}
}

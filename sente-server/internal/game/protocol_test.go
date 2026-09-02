package game

import (
	"testing"
	"time"

	"sente.app/server/internal/rules"
)

// These names are the discriminator the cross-node codec writes into Redis
// (internal/wire). Renaming one makes every in-flight event undecodable on the
// other node, so they are pinned here. The client-facing message shape in
// docs/06 §3.3 is a separate, narrower mapping.
func TestEventNamesMatchTheWireProtocol(t *testing.T) {
	expected := map[string]Event{
		"move_made":       MoveMade{},
		"scoring_opened":  ScoringOpened{},
		"scoring_changed": ScoringChanged{},
		"play_resumed":    PlayResumed{},
		"undo_requested":  UndoRequested{},
		"undo_resolved":   UndoResolved{},
		"game_ended":      GameEnded{},
		"clock_adjusted":  ClockAdjusted{},
		"chat_said":       ChatSaid{},
	}
	seen := make(map[string]bool, len(expected))
	for name, event := range expected {
		if got := event.eventName(); got != name {
			t.Errorf("%T: want %q, got %q", event, name, got)
		}
		if seen[name] {
			t.Errorf("%q is used by more than one event", name)
		}
		seen[name] = true
	}
}

// Command errors are wire codes too, and the client branches on them.
func TestCommandErrorCodes(t *testing.T) {
	for err, code := range map[CommandError]string{
		ErrOutOfSync: "out_of_sync", ErrNotScoring: "not_scoring", ErrNotPlaying: "game_not_playing",
		ErrNoUndoPending: "no_undo_pending", ErrUndoAlreadyPending: "undo_already_pending",
		ErrUndoLimitReached: "undo_limit_reached", ErrNothingToUndo: "nothing_to_undo",
		ErrNotAPlayer: "not_a_player", ErrTimeNotUp: "time_not_up",
	} {
		if err.Error() != code {
			t.Errorf("want %q, got %q", code, err.Error())
		}
	}
}

// Every command reports who sent it: that is what the gateway checks a connection
// against before the session ever sees the command.
func TestEveryCommandReportsItsSender(t *testing.T) {
	commands := []Command{
		PlayCommand{By: rules.Black},
		RequestUndoCommand{By: rules.Black},
		RespondUndoCommand{By: rules.Black},
		MarkDeadCommand{By: rules.Black},
		AcceptScoreCommand{By: rules.Black},
		ResumePlayCommand{By: rules.Black},
		TimeoutCommand{Player: rules.Black},
	}
	for _, command := range commands {
		if got := command.commandBy(); got != rules.Black {
			t.Errorf("%T: want black, got %v", command, got)
		}
	}
}

// A corrupt move list must stop the replay rather than quietly produce a wrong
// position: the caller has to be able to tell a short replay from a good one.
func TestRebuildStopsOnAnImpossibleMoveList(t *testing.T) {
	config := blitz()
	point := coord(t, "e5")
	moves := []rules.RecordedMove{
		{Player: rules.Black, Move: rules.Play(point)},
		{Player: rules.White, Move: rules.Play(point)}, // already occupied
		{Player: rules.Black, Move: rules.Pass},
	}
	engine := rebuild(config, moves)
	if engine.State.MoveNumber != 1 {
		t.Errorf("replay should stop at the bad move, got move %d", engine.State.MoveNumber)
	}
}

func TestCapturedStonesAreReportedOnTheEvent(t *testing.T) {
	config := blitz()
	session, err := NewSession(config, epoch)
	if err != nil {
		t.Fatal(err)
	}
	// Black surrounds a white stone at a1 and takes it with b1.
	for _, step := range []struct {
		by   rules.Color
		text string
	}{
		{rules.Black, "a2"}, {rules.White, "a1"}, {rules.Black, "j9"}, {rules.White, "j8"},
	} {
		session, _ = mustPlay(t, session, step.by, rules.Play(coord(t, step.text)), time.Second)
	}
	session, events := mustPlay(t, session, rules.Black, rules.Play(coord(t, "b1")), time.Second)

	made, ok := eventOfType[MoveMade](events)
	if !ok {
		t.Fatal("expected a move_made event")
	}
	if len(made.Captured) != 1 || rules.CoordinateText(made.Captured[0], 9) != "A1" {
		t.Errorf("want A1 captured, got %v", made.Captured)
	}
	if made.BoardHash != session.Engine.State.BoardHash {
		t.Error("the event's hash must match the position it announces")
	}
}

func TestFinishOverridesThePhaseWithoutTouchingTheBoard(t *testing.T) {
	engine := rules.NewGame(9, rules.Japanese, 6.5, 0)
	engine, err := engine.Apply(rules.Play(coord(t, "e5")))
	if err != nil {
		t.Fatal(err)
	}
	before := engine.State.BoardHash

	result := rules.Result{Winner: rules.White, Reason: rules.ReasonTimeout}
	finished := engine.Finish(result)
	if finished.State.Phase != rules.Finished || finished.State.Result.Reason != rules.ReasonTimeout {
		t.Errorf("Finish did not end the game: %+v", finished.State)
	}
	if finished.State.BoardHash != before {
		t.Error("Finish must not touch the position")
	}
	if engine.State.Phase != rules.Playing {
		t.Error("Finish must not mutate the receiver")
	}
}

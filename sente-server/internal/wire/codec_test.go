package wire

import (
	"reflect"
	"testing"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

// Anything that crosses a process boundary has to come back identical. A field
// dropped here becomes a game that plays differently depending on which node the
// two players happened to connect to.

func TestEveryCommandRoundTrips(t *testing.T) {
	point := rules.Point{Col: 3, Row: 15}
	commands := []game.Command{
		game.PlayCommand{By: rules.Black, Move: rules.Play(point),
			ClientMoveID: "abc-123", ExpectedMoveNumber: 42},
		game.PlayCommand{By: rules.White, Move: rules.Pass, ExpectedMoveNumber: 7},
		game.PlayCommand{By: rules.Black, Move: rules.Resign, SkipExpectedCheck: true},
		game.RequestUndoCommand{By: rules.White},
		game.RespondUndoCommand{By: rules.Black, Accept: true},
		game.RespondUndoCommand{By: rules.Black, Accept: false},
		game.MarkDeadCommand{By: rules.White, Point: point},
		game.AcceptScoreCommand{By: rules.Black, Accepted: true},
		game.ResumePlayCommand{By: rules.White},
		game.TimeoutCommand{Player: rules.Black},
	}
	for _, command := range commands {
		encoded, err := EncodeCommand(command)
		if err != nil {
			t.Fatalf("%T: %v", command, err)
		}
		decoded, err := DecodeCommand(encoded)
		if err != nil {
			t.Fatalf("%T: %v", command, err)
		}
		if !reflect.DeepEqual(decoded, command) {
			t.Errorf("%T changed in transit:\n want %+v\n got  %+v", command, command, decoded)
		}
	}
}

func TestEveryEventRoundTrips(t *testing.T) {
	clock, err := game.NewClock(game.TimeControl{Kind: game.Byoyomi, MainTime: time.Minute,
		Periods: 3, PeriodTime: 30 * time.Second}, time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	score := rules.NewGame(9, rules.Japanese, 6.5, 0).Score(nil)
	point := rules.Point{Col: 4, Row: 4}

	events := []game.Event{
		game.MoveMade{MoveNumber: 12, By: rules.Black, Move: rules.Play(point),
			Captured:  []rules.Point{{Col: 1, Row: 2}, {Col: 1, Row: 3}},
			BoardHash: 0xDEADBEEFCAFEF00D, Clock: clock},
		game.MoveMade{MoveNumber: 1, By: rules.White, Move: rules.Pass, BoardHash: 0, Clock: clock,
			Duplicate: true},
		game.ScoringOpened{Suggested: []rules.Point{point}, ResumeFromMoveNumber: 128},
		game.ScoringChanged{Dead: []rules.Point{point}, BlackAccepted: true, Score: score},
		game.PlayResumed{MoveNumber: 126, ToPlay: rules.White},
		game.UndoRequested{By: rules.Black},
		game.UndoResolved{By: rules.White, Accepted: true, MoveNumber: 11},
		game.GameEnded{Result: rules.Result{Winner: rules.White, Reason: rules.ReasonTimeout}},
		game.GameEnded{Result: rules.Result{Winner: rules.White, Reason: rules.ReasonCounting,
			Score: &score}},
		game.ClockAdjusted{Player: rules.Black, Delta: 21 * time.Second,
			Reason: "server_interruption"},
		game.ChatSaid{By: rules.White, Code: "gg"},
	}
	for _, event := range events {
		encoded, err := EncodeEvent(event)
		if err != nil {
			t.Fatalf("%T: %v", event, err)
		}
		decoded, err := DecodeEvent(encoded)
		if err != nil {
			t.Fatalf("%T: %v", event, err)
		}
		if !reflect.DeepEqual(decoded, event) {
			t.Errorf("%T changed in transit:\n want %+v\n got  %+v", event, event, decoded)
		}
	}
}

// A void game has no winner. Encoding it as a colour would hand the win to Black.
func TestAVoidGameKeepsItsMissingWinner(t *testing.T) {
	event := game.GameEnded{Result: rules.Result{Winner: rules.Empty, Reason: rules.ReasonRepetition}}
	encoded, err := EncodeEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	ended := decoded.(game.GameEnded)
	if ended.Result.Winner != rules.Empty {
		t.Errorf("a void game gained a winner: %v", ended.Result.Winner)
	}
}

// The board hash is what the desync check compares, so a mangled one is worse
// than no hash at all.
func TestBoardHashesSurviveTheFullRange(t *testing.T) {
	for _, hash := range []uint64{0, 1, 0x00000000FFFFFFFF, 0xFFFFFFFFFFFFFFFF,
		0x8000000000000000, 0x0123456789ABCDEF} {
		encoded, err := EncodeEvent(game.MoveMade{BoardHash: hash, By: rules.Black, Move: rules.Pass})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeEvent(encoded)
		if err != nil {
			t.Fatalf("%#x: %v", hash, err)
		}
		if got := decoded.(game.MoveMade).BoardHash; got != hash {
			t.Errorf("want %#016x, got %#016x", hash, got)
		}
	}
}

func TestUnknownAndMalformedInputIsRefused(t *testing.T) {
	if _, err := DecodeCommand([]byte(`{"type":"teleport","data":{}}`)); err == nil {
		t.Error("an unknown command type must be refused")
	}
	if _, err := DecodeEvent([]byte(`{"type":"teleport","data":{}}`)); err == nil {
		t.Error("an unknown event type must be refused")
	}
	if _, err := DecodeCommand([]byte(`not json`)); err == nil {
		t.Error("malformed input must be refused")
	}
	if _, err := DecodeEvent([]byte(`not json`)); err == nil {
		t.Error("malformed input must be refused")
	}
	if _, err := DecodeCommand([]byte(`{"type":"play","data":{"move":{"kind":"teleport"}}}`)); err == nil {
		t.Error("an unknown move kind must be refused")
	}
	if _, err := DecodeCommand([]byte(`{"type":"play","data":"not an object"}`)); err == nil {
		t.Error("a mistyped payload must be refused")
	}
	if _, err := DecodeEvent([]byte(`{"type":"move_made","data":{"board_hash":"nonsense"}}`)); err == nil {
		t.Error("an unparseable board hash must be refused")
	}
}

func TestEncodingRefusesTypesItDoesNotKnow(t *testing.T) {
	if _, err := EncodeCommand(struct{ game.Command }{}); err == nil {
		t.Error("an unknown command must not encode to something silently wrong")
	}
	if _, err := EncodeEvent(struct{ game.Event }{}); err == nil {
		t.Error("an unknown event must not encode to something silently wrong")
	}
}

// Every payload variant a malformed decode could hit, so a corrupt message is an
// error rather than a half-built command.
func TestMistypedPayloadsAreRefusedForEveryType(t *testing.T) {
	for _, kind := range []string{"undo_request", "undo_response", "mark_dead",
		"scoring_accept", "resume_play", "timeout"} {
		if _, err := DecodeCommand([]byte(`{"type":"` + kind + `","data":"nope"}`)); err == nil {
			t.Errorf("%s: a mistyped payload must be refused", kind)
		}
	}
	for _, kind := range []string{"scoring_opened", "scoring_changed", "play_resumed",
		"undo_requested", "undo_resolved", "game_ended", "clock_adjusted", "chat_said"} {
		if _, err := DecodeEvent([]byte(`{"type":"` + kind + `","data":"nope"}`)); err == nil {
			t.Errorf("%s: a mistyped payload must be refused", kind)
		}
	}
	if _, err := DecodeEvent([]byte(`{"type":"move_made","data":"nope"}`)); err == nil {
		t.Error("move_made: a mistyped payload must be refused")
	}
}

package game

import (
	"testing"
	"time"

	"sente.app/server/internal/rules"
)

func blitz() Config {
	return Config{
		Size: 9, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
		TimeControl: TimeControl{Kind: Absolute, MainTime: 10 * time.Minute},
	}
}

func newSession(t *testing.T, config Config) GameSession {
	t.Helper()
	session, err := NewSession(config, epoch)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

// at returns a timestamp `spent` after the current turn began, past the lag grace
// so the number reads as time actually consumed.
func at(s GameSession, spent time.Duration) time.Time {
	return s.Clock.TurnStartedAt.Add(spent + LagGracePerMove)
}

func mustPlay(t *testing.T, s GameSession, by rules.Color, move rules.Move, spent time.Duration) (GameSession, []Event) {
	t.Helper()
	next, events, err := s.Apply(PlayCommand{
		By: by, Move: move, ExpectedMoveNumber: s.MoveNumber(),
	}, at(s, spent))
	if err != nil {
		t.Fatalf("play %v by %v: %v", move, by, err)
	}
	return next, events
}

func coord(t *testing.T, text string) rules.Point {
	t.Helper()
	p, ok := rules.ParseCoordinate(text, 9)
	if !ok {
		t.Fatalf("bad coordinate %q", text)
	}
	return p
}

func eventOfType[T Event](events []Event) (T, bool) {
	for _, e := range events {
		if typed, ok := e.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

// ── playing ─────────────────────────────────────────────────────────────────

func TestPlayingAMoveChargesTheClockAndAdvancesTheTurn(t *testing.T) {
	session := newSession(t, blitz())
	session, events := mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), 30*time.Second)

	made, ok := eventOfType[MoveMade](events)
	if !ok {
		t.Fatal("expected a move_made event")
	}
	if made.MoveNumber != 1 || made.By != rules.Black {
		t.Errorf("unexpected event: %+v", made)
	}
	if session.ToPlay() != rules.White {
		t.Error("the turn should have passed to White")
	}
	if session.Clock.Black.Main != 9*time.Minute+30*time.Second {
		t.Errorf("clock not charged: %v", session.Clock.Black.Main)
	}
	if session.Clock.White.Main != 10*time.Minute {
		t.Error("White's clock must not move")
	}
}

func TestMovingOutOfTurnIsRejected(t *testing.T) {
	session := newSession(t, blitz())
	_, _, err := session.Apply(PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}, at(session, time.Second))
	if err != rules.ErrNotYourTurn {
		t.Errorf("want not_your_turn, got %v", err)
	}
}

// A client that missed its opponent's move must be told to resync rather than
// have its move applied against the wrong position (docs/06 §3.6).
func TestAStaleExpectedMoveNumberIsRejected(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)

	_, _, err := session.Apply(PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "f5")), ExpectedMoveNumber: 0,
	}, at(session, time.Second))
	if err != ErrOutOfSync {
		t.Errorf("want out_of_sync, got %v", err)
	}
}

func TestIllegalMovesAreRejectedByTheRules(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)
	_, _, err := session.Apply(PlayCommand{
		By: rules.White, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 1,
	}, at(session, time.Second))
	if err != rules.ErrOccupied {
		t.Errorf("want occupied, got %v", err)
	}
}

// A move resent after a reconnect must return the original result and must not be
// charged to the clock a second time (docs/03 ADR-007).
func TestResendingAMoveIsIdempotent(t *testing.T) {
	session := newSession(t, blitz())
	session, _, err := session.Apply(PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")),
		ClientMoveID: "abc", ExpectedMoveNumber: 0,
	}, at(session, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	afterFirst := session

	// The same key arrives again, out of turn and with a stale expectation.
	session, events, err := session.Apply(PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")),
		ClientMoveID: "abc", ExpectedMoveNumber: 0,
	}, at(session, 90*time.Second))
	if err != nil {
		t.Fatalf("a resend must succeed, got %v", err)
	}
	made, ok := eventOfType[MoveMade](events)
	if !ok || !made.Duplicate || made.MoveNumber != 1 {
		t.Errorf("want the original result marked duplicate, got %+v", made)
	}
	if session.MoveNumber() != afterFirst.MoveNumber() {
		t.Error("a resend must not add a move")
	}
	if session.Clock.Black.Main != afterFirst.Clock.Black.Main {
		t.Error("a resend must not charge the clock again")
	}
}

func TestSessionsAreImmutableUnderRejectedCommands(t *testing.T) {
	session := newSession(t, blitz())
	before := session.MoveNumber()
	_, _, _ = session.Apply(PlayCommand{By: rules.White, Move: rules.Pass}, at(session, time.Second))
	if session.MoveNumber() != before {
		t.Error("a rejected command changed the session")
	}
}

// ── the clock ───────────────────────────────────────────────────────────────

func TestAMoveArrivingAfterTheDeadlineLosesOnTime(t *testing.T) {
	session := newSession(t, blitz())
	_, events, err := session.Apply(PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "e5")), ExpectedMoveNumber: 0,
	}, epoch.Add(11*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ended, ok := eventOfType[GameEnded](events)
	if !ok {
		t.Fatal("expected the game to end")
	}
	if ended.Result.Reason != rules.ReasonTimeout || ended.Result.Winner != rules.White {
		t.Errorf("want White by timeout, got %+v", ended.Result)
	}
	if _, ok := eventOfType[MoveMade](events); ok {
		t.Error("a late move must not be played")
	}
}

func TestTheTimerFiresOnlyOnceTimeIsActuallyUp(t *testing.T) {
	session := newSession(t, blitz())
	// A stale timer that fires while the player still has time is refused.
	if _, _, err := session.Apply(TimeoutCommand{Player: rules.Black}, epoch.Add(time.Minute)); err != ErrTimeNotUp {
		t.Errorf("want time_not_up, got %v", err)
	}
	next, events, err := session.Apply(TimeoutCommand{Player: rules.Black}, epoch.Add(11*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eventOfType[GameEnded](events); !ok || next.Phase() != rules.Finished {
		t.Error("the game should have ended on time")
	}
}

func TestTimingOutTheWrongPlayerIsRefused(t *testing.T) {
	session := newSession(t, blitz())
	if _, _, err := session.Apply(TimeoutCommand{Player: rules.White}, epoch.Add(11*time.Minute)); err != rules.ErrNotYourTurn {
		t.Errorf("only the player on move can lose on time, got %v", err)
	}
}

func TestByoyomiRunsThroughTheSession(t *testing.T) {
	config := blitz()
	config.TimeControl = TimeControl{Kind: Byoyomi, MainTime: time.Minute, Periods: 2,
		PeriodTime: 30 * time.Second}
	session := newSession(t, config)

	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), 90*time.Second)
	if session.Clock.Black.PeriodsLeft != 1 || !session.Clock.Black.InByoyomi() {
		t.Fatalf("black should be down to one period: %+v", session.Clock.Black)
	}
	session, _ = mustPlay(t, session, rules.White, rules.Play(coord(t, "f5")), time.Second)

	// Exhausting the last period ends the game.
	_, events, err := session.Apply(PlayCommand{
		By: rules.Black, Move: rules.Play(coord(t, "d5")), ExpectedMoveNumber: session.MoveNumber(),
	}, at(session, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if ended, ok := eventOfType[GameEnded](events); !ok || ended.Result.Winner != rules.White {
		t.Errorf("want White by timeout, got %+v", events)
	}
}

// ── ending play ─────────────────────────────────────────────────────────────

func TestTwoPassesOpenScoring(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)
	session, _ = mustPlay(t, session, rules.White, rules.Pass, time.Second)
	session, events := mustPlay(t, session, rules.Black, rules.Pass, time.Second)

	opened, ok := eventOfType[ScoringOpened](events)
	if !ok {
		t.Fatal("expected scoring to open")
	}
	if session.Phase() != rules.ScoringP || session.Scoring == nil {
		t.Fatal("the session should be in the scoring phase")
	}
	// Rewind target is the position before the first of the two passes.
	if opened.ResumeFromMoveNumber != 1 {
		t.Errorf("want to rewind to move 1, got %d", opened.ResumeFromMoveNumber)
	}
}

func TestResigningEndsTheGame(t *testing.T) {
	session := newSession(t, blitz())
	session, events := mustPlay(t, session, rules.Black, rules.Resign, time.Second)
	ended, ok := eventOfType[GameEnded](events)
	if !ok || ended.Result.Winner != rules.White || ended.Result.Reason != rules.ReasonResignation {
		t.Errorf("want White by resignation, got %+v", events)
	}
	if session.Phase() != rules.Finished {
		t.Error("the session should be finished")
	}
}

func TestNoCommandsAfterTheGameEnds(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Resign, time.Second)
	for _, command := range []Command{
		PlayCommand{By: rules.White, Move: rules.Pass},
		RequestUndoCommand{By: rules.White},
		TimeoutCommand{Player: rules.White},
	} {
		if _, _, err := session.Apply(command, at(session, time.Second)); err == nil {
			t.Errorf("%T should be refused once the game is over", command)
		}
	}
}

// ── scoring ─────────────────────────────────────────────────────────────────

func scoringSession(t *testing.T) GameSession {
	t.Helper()
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)
	session, _ = mustPlay(t, session, rules.White, rules.Play(coord(t, "e7")), time.Second)
	session, _ = mustPlay(t, session, rules.Black, rules.Pass, time.Second)
	session, _ = mustPlay(t, session, rules.White, rules.Pass, time.Second)
	return session
}

func TestMarkingStonesClearsBothAcceptances(t *testing.T) {
	session := scoringSession(t)
	session, _, _ = session.Apply(AcceptScoreCommand{By: rules.Black, Accepted: true}, epoch)
	session, _, _ = session.Apply(AcceptScoreCommand{By: rules.White, Accepted: true}, epoch)
	if session.Phase() != rules.Finished {
		t.Fatal("both accepted, the game should be over")
	}

	// Do it again, but change the marking in between.
	session = scoringSession(t)
	session, _, _ = session.Apply(AcceptScoreCommand{By: rules.Black, Accepted: true}, epoch)
	session, events, err := session.Apply(MarkDeadCommand{By: rules.White, Point: coord(t, "e5")}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	changed, ok := eventOfType[ScoringChanged](events)
	if !ok {
		t.Fatal("expected a scoring_changed event")
	}
	if changed.BlackAccepted || changed.WhiteAccepted {
		t.Error("a change must clear both acceptances")
	}
	if session.Phase() != rules.ScoringP {
		t.Error("the game must not end while consent is stale")
	}
}

func TestBothAcceptingEndsTheGameWithTheAgreedScore(t *testing.T) {
	session := scoringSession(t)
	session, _, _ = session.Apply(AcceptScoreCommand{By: rules.Black, Accepted: true}, epoch)
	session, events, err := session.Apply(AcceptScoreCommand{By: rules.White, Accepted: true}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	ended, ok := eventOfType[GameEnded](events)
	if !ok {
		t.Fatal("expected the game to end")
	}
	if ended.Result.Reason != rules.ReasonCounting || ended.Result.Score == nil {
		t.Fatalf("want a counted result, got %+v", ended.Result)
	}
	if session.Result().Score.White != ended.Result.Score.White {
		t.Error("the stored result and the event disagree")
	}
}

func TestRefusingTheResultResumesPlayForTheRefuser(t *testing.T) {
	session := scoringSession(t)
	movesBefore := session.MoveNumber()

	// White passed second, so undoing one pass already puts White on move.
	next, events, err := session.Apply(ResumePlayCommand{By: rules.White}, epoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	resumed, ok := eventOfType[PlayResumed](events)
	if !ok {
		t.Fatal("expected play to resume")
	}
	if next.Phase() != rules.Playing || next.Scoring != nil {
		t.Error("the scoring session should be gone")
	}
	if resumed.ToPlay != rules.White || next.ToPlay() != rules.White {
		t.Errorf("the refuser should be on move, got %v", next.ToPlay())
	}
	if next.MoveNumber() != movesBefore-1 {
		t.Errorf("want one pass undone, got move %d from %d", next.MoveNumber(), movesBefore)
	}
}

func TestTheOtherPlayerRefusingRewindsBothPasses(t *testing.T) {
	session := scoringSession(t)
	movesBefore := session.MoveNumber()

	// Black passed first, so both passes have to come off for Black to be on move.
	next, _, err := session.Apply(ResumePlayCommand{By: rules.Black}, epoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if next.ToPlay() != rules.Black {
		t.Errorf("the refuser should be on move, got %v", next.ToPlay())
	}
	if next.MoveNumber() != movesBefore-2 {
		t.Errorf("want both passes undone, got move %d from %d", next.MoveNumber(), movesBefore)
	}
	// The board must be back to what it was before the passes.
	if next.Engine.Board().At(coord(t, "e7")) != rules.White {
		t.Error("rewinding a pass must not remove stones")
	}
}

func TestScoringCommandsAreRefusedWhilePlaying(t *testing.T) {
	session := newSession(t, blitz())
	for _, command := range []Command{
		MarkDeadCommand{By: rules.Black, Point: coord(t, "e5")},
		AcceptScoreCommand{By: rules.Black, Accepted: true},
		ResumePlayCommand{By: rules.Black},
	} {
		if _, _, err := session.Apply(command, epoch); err != ErrNotScoring {
			t.Errorf("%T: want not_scoring, got %v", command, err)
		}
	}
}

// ── undo ────────────────────────────────────────────────────────────────────

func TestUndoNeedsTheOpponentsConsent(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)

	session, events, err := session.Apply(RequestUndoCommand{By: rules.Black}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eventOfType[UndoRequested](events); !ok {
		t.Fatal("expected an undo_requested event")
	}
	// Answering your own request would make undo unilateral.
	if _, _, err := session.Apply(RespondUndoCommand{By: rules.Black, Accept: true}, epoch); err != ErrNotAPlayer {
		t.Errorf("want not_a_player, got %v", err)
	}

	next, events, err := session.Apply(RespondUndoCommand{By: rules.White, Accept: true}, epoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := eventOfType[UndoResolved](events)
	if !ok || !resolved.Accepted {
		t.Fatalf("want an accepted undo, got %+v", events)
	}
	if next.MoveNumber() != 0 || next.Engine.Board().At(coord(t, "e5")) != rules.Empty {
		t.Error("the stone should be back off the board")
	}
	if next.ToPlay() != rules.Black {
		t.Error("the move should be Black's again")
	}
}

func TestDecliningAnUndoLeavesThePositionAlone(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)
	session, _, _ = session.Apply(RequestUndoCommand{By: rules.Black}, epoch)

	next, events, err := session.Apply(RespondUndoCommand{By: rules.White, Accept: false}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, ok := eventOfType[UndoResolved](events); !ok || resolved.Accepted {
		t.Fatalf("want a declined undo, got %+v", events)
	}
	if next.MoveNumber() != 1 || next.Undo != nil {
		t.Error("declining should clear the request and keep the move")
	}
}

func TestOnlyOneUndoRequestAtATime(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)
	session, _, _ = session.Apply(RequestUndoCommand{By: rules.Black}, epoch)
	if _, _, err := session.Apply(RequestUndoCommand{By: rules.White}, epoch); err != ErrUndoAlreadyPending {
		t.Errorf("want undo_already_pending, got %v", err)
	}
}

func TestUndoIsCappedPerGame(t *testing.T) {
	config := blitz()
	config.MaxUndos = 1
	session := newSession(t, config)
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)

	session, _, _ = session.Apply(RequestUndoCommand{By: rules.Black}, epoch)
	session, _, _ = session.Apply(RespondUndoCommand{By: rules.White, Accept: true}, epoch)

	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "f5")), time.Second)
	if _, _, err := session.Apply(RequestUndoCommand{By: rules.Black}, epoch); err != ErrUndoLimitReached {
		t.Errorf("want undo_limit_reached, got %v", err)
	}
}

func TestUndoNeedsAMoveToUndo(t *testing.T) {
	session := newSession(t, blitz())
	if _, _, err := session.Apply(RequestUndoCommand{By: rules.Black}, epoch); err != ErrNothingToUndo {
		t.Errorf("want nothing_to_undo, got %v", err)
	}
	if _, _, err := session.Apply(RespondUndoCommand{By: rules.White, Accept: true}, epoch); err != ErrNoUndoPending {
		t.Errorf("want no_undo_pending, got %v", err)
	}
}

func TestPlayingClearsAPendingUndo(t *testing.T) {
	session := newSession(t, blitz())
	session, _ = mustPlay(t, session, rules.Black, rules.Play(coord(t, "e5")), time.Second)
	session, _, _ = session.Apply(RequestUndoCommand{By: rules.Black}, epoch)
	session, _ = mustPlay(t, session, rules.White, rules.Play(coord(t, "f5")), time.Second)
	if session.Undo != nil {
		t.Error("carrying on with the game withdraws the request")
	}
}

// ── whole games ─────────────────────────────────────────────────────────────

// A game played end to end must leave a move list that rebuilds the same position,
// which is what the server relies on to restore a game from the database.
func TestAWholeGameRebuildsFromItsMoveList(t *testing.T) {
	session := newSession(t, blitz())
	for i, text := range []string{"e5", "e7", "d5", "d7", "c5", "c7"} {
		player := rules.Black
		if i%2 == 1 {
			player = rules.White
		}
		session, _ = mustPlay(t, session, player, rules.Play(coord(t, text)), time.Second)
	}
	rebuilt := rebuild(session.Config, session.Moves)
	if rebuilt.State.BoardHash != session.Engine.State.BoardHash {
		t.Error("replaying the move list gave a different position")
	}
	if rebuilt.State.MoveNumber != session.MoveNumber() {
		t.Error("replaying the move list gave a different move count")
	}
}

func TestNewSessionRejectsAnInvalidTimeControl(t *testing.T) {
	config := blitz()
	config.TimeControl = TimeControl{Kind: Byoyomi, MainTime: time.Minute}
	if _, err := NewSession(config, epoch); err == nil {
		t.Error("a byo-yomi with no periods should be refused")
	}
}

func TestUnknownCommandsAreReported(t *testing.T) {
	session := newSession(t, blitz())
	if _, _, err := session.Apply(struct{ Command }{}, epoch); err == nil {
		t.Error("an unknown command should be an error, not a silent no-op")
	}
}

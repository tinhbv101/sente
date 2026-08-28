package game

import (
	"testing"

	"sente.app/server/internal/rules"
)

func points(t *testing.T, size int, list ...string) []rules.Point {
	t.Helper()
	out := make([]rules.Point, 0, len(list))
	for _, text := range list {
		p, ok := rules.ParseCoordinate(text, size)
		if !ok {
			t.Fatalf("bad coordinate %q", text)
		}
		out = append(out, p)
	}
	return out
}

func names(list []rules.Point, size int) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, rules.CoordinateText(p, size))
	}
	return out
}

// The half-point position from rules-spec/vectors/scoring: Black owns the left,
// White the right, with two dead white stones in Black's corner.
func halfPointGame(t *testing.T) rules.Engine {
	t.Helper()
	return rules.Position(rules.PositionOptions{
		Size: 9,
		Black: points(t, 9, "d9", "d8", "d7", "e6", "e5", "d4", "d3", "d2", "d1",
			"b7", "b4", "c2"),
		White: points(t, 9, "e9", "e8", "e7", "f6", "f5", "e4", "e3", "e2", "e1",
			"g7", "h5", "g3", "b2", "b1"),
		Rules:    rules.Japanese,
		Komi:     6.5,
		Captures: rules.Captures{Black: 12, White: 3},
	})
}

func TestSessionStartsFromTheSuggestion(t *testing.T) {
	session := NewScoringSession(points(t, 9, "b2", "b1"), 128)
	if got := names(session.DeadStones(), 9); len(got) != 2 {
		t.Fatalf("want two suggested stones, got %v", got)
	}
	if session.EditedFromSuggestion() {
		t.Error("nothing has been changed yet")
	}
	if session.Settled() {
		t.Error("nobody has accepted yet")
	}
	if session.ResumeFromMoveNumber != 128 {
		t.Error("the resume point was lost")
	}
}

func TestScoreMatchesTheSharedVector(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(points(t, 9, "b2", "b1"), 128)

	score := session.Score(engine)
	if score.Black != 40 || score.White != 40.5 {
		t.Fatalf("want 40 to 40.5, got %v to %v", score.Black, score.White)
	}
	if score.Winner() != rules.White || score.Margin() != 0.5 {
		t.Errorf("want White by half a point, got %v by %v", score.Winner(), score.Margin())
	}
}

func TestTogglingFlipsTheWholeChain(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(nil, 128)

	// b1 and b2 are one chain: marking either must mark both.
	session = session.ToggleChain(engine.Board(), points(t, 9, "b1")[0])
	if got := names(session.DeadStones(), 9); len(got) != 2 {
		t.Fatalf("want the whole chain marked, got %v", got)
	}
	// Tapping again clears the whole chain.
	session = session.ToggleChain(engine.Board(), points(t, 9, "b2")[0])
	if got := session.DeadStones(); len(got) != 0 {
		t.Fatalf("want the chain cleared, got %v", names(got, 9))
	}
}

func TestTogglingAnEmptyPointDoesNothing(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(points(t, 9, "b2", "b1"), 128)
	after := session.ToggleChain(engine.Board(), points(t, 9, "a1")[0])
	if len(after.DeadStones()) != 2 {
		t.Error("an empty intersection has no chain to mark")
	}
}

func TestAnyChangeClearsBothAcceptances(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(points(t, 9, "b2", "b1"), 128).
		SetAccepted(rules.Black, true).
		SetAccepted(rules.White, true)
	if !session.Settled() {
		t.Fatal("both players accepted")
	}

	// One player quietly changing the marking must invalidate the other's consent.
	session = session.ToggleChain(engine.Board(), points(t, 9, "b1")[0])
	if session.Settled() || session.Accepted(rules.Black) || session.Accepted(rules.White) {
		t.Error("a change must clear both acceptances")
	}
}

func TestAcceptanceCanBeWithdrawn(t *testing.T) {
	session := NewScoringSession(nil, 0).SetAccepted(rules.Black, true)
	if !session.Accepted(rules.Black) || session.Accepted(rules.White) {
		t.Fatal("only black accepted")
	}
	session = session.SetAccepted(rules.Black, false)
	if session.Accepted(rules.Black) {
		t.Error("black withdrew")
	}
}

func TestResetGoesBackToTheServerProposal(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(points(t, 9, "b2", "b1"), 128)
	session = session.ToggleChain(engine.Board(), points(t, 9, "b1")[0])
	if !session.EditedFromSuggestion() {
		t.Fatal("the marking was changed")
	}
	session = session.ResetToSuggestion()
	if session.EditedFromSuggestion() {
		t.Error("reset should restore the proposal exactly")
	}
	if len(session.DeadStones()) != 2 {
		t.Error("the proposed stones should be back")
	}
}

func TestSessionsAreImmutable(t *testing.T) {
	engine := halfPointGame(t)
	original := NewScoringSession(points(t, 9, "b2", "b1"), 128).SetAccepted(rules.Black, true)

	_ = original.ToggleChain(engine.Board(), points(t, 9, "b1")[0])
	_ = original.SetAccepted(rules.White, true)
	_ = original.ResetToSuggestion()

	if len(original.DeadStones()) != 2 || !original.Accepted(rules.Black) || original.Settled() {
		t.Errorf("the original session was mutated: %+v", original)
	}
}

func TestScoreFollowsTheMarkingLive(t *testing.T) {
	engine := halfPointGame(t)
	session := NewScoringSession(nil, 128)

	// While the two white stones still stand, the whole left side borders both
	// colours and is dame, so Black holds no territory at all.
	before := session.Score(engine)
	if before.BlackSide().Territory != 0 {
		t.Fatalf("live enemy stones should make the region neutral, got %d",
			before.BlackSide().Territory)
	}

	session = session.ToggleChain(engine.Board(), points(t, 9, "b1")[0])
	after := session.Score(engine)

	// Marking them dead does far more than add two prisoners: it turns the entire
	// left side from dame into Black's territory. 26 points plus 2 prisoners.
	// This swing is exactly why the score has to update live as players tap.
	if got := after.Black - before.Black; got != 28 {
		t.Errorf("want Black up by 28, got %v (%v -> %v)", got, before.Black, after.Black)
	}
	if after.BlackSide().Territory != 26 {
		t.Errorf("want 26 points of territory, got %d", after.BlackSide().Territory)
	}
}

func TestDeadStonesAreReturnedInAStableOrder(t *testing.T) {
	engine := halfPointGame(t)
	first := NewScoringSession(nil, 0).ToggleChain(engine.Board(), points(t, 9, "b1")[0])
	second := NewScoringSession(nil, 0).ToggleChain(engine.Board(), points(t, 9, "b2")[0])
	a, b := names(first.DeadStones(), 9), names(second.DeadStones(), 9)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("order depends on which stone was tapped: %v vs %v", a, b)
		}
	}
}

func TestSuggestionProvesDeathWhenItCan(t *testing.T) {
	// Black holds the top three rows as one chain with eyes at a9 and c9, so it is
	// unconditionally alive. Two white stones sit in a sealed pocket at f8/h8 with
	// only g8 between them: they can never make a second eye, and the wall around
	// them can never be broken. That is a proof of death, not a guess.
	engine := rules.Position(rules.PositionOptions{
		Size: 9,
		Black: points(t, 9,
			"b9", "d9", "e9", "f9", "g9", "h9", "j9",
			"a8", "b8", "c8", "d8", "e8", "j8",
			"a7", "b7", "c7", "d7", "e7", "f7", "g7", "h7", "j7"),
		White: points(t, 9, "f8", "h8"),
	})
	if len(engine.Board().PassAliveChains(rules.Black)) != 1 {
		t.Fatal("the black wall should be pass-alive in this position")
	}
	got := names(SuggestDeadStones(engine.Board()), 9)
	if len(got) != 2 || got[0] != "F8" || got[1] != "H8" {
		t.Errorf("want both sealed white stones suggested dead, got %v", got)
	}
}

func TestSuggestionStaysSilentWhenDeathIsNotProven(t *testing.T) {
	// The real endgame position: Black's wall is not pass-alive because players
	// stop before filling their own territory, so nothing can be proved.
	if got := SuggestDeadStones(halfPointGame(t).Board()); len(got) != 0 {
		t.Errorf("want no suggestion without proof, got %v", names(got, 9))
	}
	// An empty board has nothing to say either.
	if got := SuggestDeadStones(rules.NewBoard(9)); len(got) != 0 {
		t.Errorf("want nothing on an empty board, got %v", names(got, 9))
	}
}

func TestSuggestionNeverMarksAPassAliveChain(t *testing.T) {
	engine := rules.Position(rules.PositionOptions{
		Size:  9,
		Black: points(t, 9, "b9", "d9", "a8", "b8", "c8", "d8"),
	})
	alive := engine.Board().PassAliveChains(rules.Black)
	if len(alive) == 0 {
		t.Fatal("expected a pass-alive chain in this position")
	}
	for _, p := range SuggestDeadStones(engine.Board()) {
		for _, chain := range alive {
			for _, stone := range chain {
				if p == stone {
					t.Fatalf("suggested a pass-alive stone at %v as dead", p)
				}
			}
		}
	}
}

package rules

import (
	"sort"
	"testing"
)

func mustPoints(t *testing.T, size int, list ...string) []Point {
	t.Helper()
	out := make([]Point, 0, len(list))
	for _, text := range list {
		p, ok := ParseCoordinate(text, size)
		if !ok {
			t.Fatalf("bad coordinate %q", text)
		}
		out = append(out, p)
	}
	return out
}

func namesOf(points []Point, size int) []string {
	out := make([]string, 0, len(points))
	for _, p := range points {
		out = append(out, CoordinateText(p, size))
	}
	sort.Strings(out)
	return out
}

func TestHandicapEveryCountOn19x19(t *testing.T) {
	stars := map[Point]bool{}
	for _, p := range StarPoints(19) {
		stars[p] = true
	}
	for count := 2; count <= 9; count++ {
		stones := HandicapStones(count, 19)
		if len(stones) != count {
			t.Errorf("handicap %d placed %d stones", count, len(stones))
		}
		seen := map[Point]bool{}
		for _, p := range stones {
			if seen[p] {
				t.Errorf("handicap %d placed a duplicate at %v", count, p)
			}
			seen[p] = true
			if !stars[p] {
				t.Errorf("handicap %d landed off a star point at %v", count, p)
			}
		}
	}
}

func TestHandicapThreeAndSevenPlacements(t *testing.T) {
	got := namesOf(HandicapStones(3, 19), 19)
	want := []string{"D4", "Q16", "Q4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("handicap 3: want %v, got %v", want, got)
		}
	}
	if len(namesOf(HandicapStones(7, 19), 19)) != 7 {
		t.Error("handicap 7 should place seven stones")
	}
}

func TestHandicapCentreIsTakenLastOnOddCounts(t *testing.T) {
	for _, count := range []int{5, 7, 9} {
		stones := HandicapStones(count, 19)
		if CoordinateText(stones[len(stones)-1], 19) != "K10" {
			t.Errorf("handicap %d should end at the centre", count)
		}
	}
	for _, count := range []int{4, 6, 8} {
		for _, name := range namesOf(HandicapStones(count, 19), 19) {
			if name == "K10" {
				t.Errorf("handicap %d should not use the centre", count)
			}
		}
	}
}

func TestHandicapRejectsOutOfRangeCountsAndSizes(t *testing.T) {
	for _, count := range []int{-1, 0, 1, 10} {
		if stones := HandicapStones(count, 19); stones != nil {
			t.Errorf("handicap %d should place nothing, got %v", count, stones)
		}
	}
	if HandicapStones(4, 11) != nil || len(StarPoints(11)) != 0 {
		t.Error("an unsupported size has no star points")
	}
}

func TestStarPointCountPerSize(t *testing.T) {
	for _, size := range SupportedSizes {
		if got := len(StarPoints(size)); got != 9 {
			t.Errorf("%dx%d has %d star points, want 9", size, size, got)
		}
	}
}

func TestWireStringRoundTrips(t *testing.T) {
	engine := Position(PositionOptions{Size: 9,
		Black: []Point{{Col: 0, Row: 0}, {Col: 4, Row: 4}},
		White: []Point{{Col: 8, Row: 8}}})
	wire := engine.State.Board.WireString()
	if len(wire) != 81 || wire[0] != 'b' || wire[80] != 'w' {
		t.Fatalf("bad wire string: %q", wire)
	}
	back, ok := BoardFromWireString(9, wire)
	if !ok || !back.Equal(engine.State.Board) {
		t.Error("wire string did not round trip")
	}
}

func TestWireStringRejectsBadInput(t *testing.T) {
	if _, ok := BoardFromWireString(9, "too short"); ok {
		t.Error("short input should be rejected")
	}
	bad := make([]byte, 81)
	for i := range bad {
		bad[i] = 'x'
	}
	if _, ok := BoardFromWireString(9, string(bad)); ok {
		t.Error("unknown characters should be rejected")
	}
	if _, ok := BoardFromWireString(11, string(make([]byte, 121))); ok {
		t.Error("unsupported size should be rejected")
	}
}

func TestChainAndLiberties(t *testing.T) {
	points := mustPoints(t, 9, "d5", "e5", "f5")
	board := Position(PositionOptions{Size: 9, Black: points}).State.Board
	if got := len(board.Chain(points[0])); got != 3 {
		t.Errorf("chain size: want 3, got %d", got)
	}
	// Three stones in a row: two liberties at each end plus three above and below.
	if got := board.Liberties(points[1]); got != 8 {
		t.Errorf("liberties: want 8, got %d", got)
	}
}

func TestChainOnEmptyOrOffBoardIsNil(t *testing.T) {
	board := NewBoard(9)
	if board.Chain(Point{Col: 4, Row: 4}) != nil {
		t.Error("an empty point has no chain")
	}
	if board.Chain(Point{Col: 99, Row: 99}) != nil {
		t.Error("an off-board point has no chain")
	}
	if board.Liberties(Point{Col: 4, Row: 4}) != 0 || board.Liberties(Point{Col: 99, Row: 0}) != 0 {
		t.Error("liberties of a non-stone must be zero")
	}
}

func TestBoardsOfDifferentSizesAreNotEqual(t *testing.T) {
	if NewBoard(9).Equal(NewBoard(13)) {
		t.Error("boards of different sizes must not compare equal")
	}
	if NewBoard(9).At(Point{Col: 99, Row: 0}) != offBoard {
		t.Error("an off-board point must read as offBoard")
	}
}

func TestColourHelpers(t *testing.T) {
	if Black.Opponent() != White || White.Opponent() != Black || Empty.Opponent() != Empty {
		t.Error("Opponent is wrong")
	}
	for colour, want := range map[Color]string{
		Black: "black", White: "white", Empty: "empty", offBoard: "offboard",
	} {
		if colour.String() != want {
			t.Errorf("String of %d: want %s, got %s", colour, want, colour.String())
		}
	}
	captures := Captures{Black: 3, White: 5}
	if captures.Get(Black) != 3 || captures.Get(White) != 5 {
		t.Error("Captures.Get is wrong")
	}
}

func TestMoveErrorCodesMatchTheWireProtocol(t *testing.T) {
	for err, code := range map[MoveError]string{
		ErrOutOfBounds: "out_of_bounds", ErrOccupied: "occupied", ErrNotYourTurn: "not_your_turn",
		ErrSuicide: "suicide", ErrKo: "ko", ErrSuperko: "superko",
		ErrGameNotPlaying: "game_not_playing",
	} {
		if err.Error() != code {
			t.Errorf("want %s, got %s", code, err.Error())
		}
	}
}

func TestGameFlow(t *testing.T) {
	engine := NewGame(9, Japanese, 6.5, 0)
	if engine.State.Size() != 9 {
		t.Error("State.Size is wrong")
	}
	engine, _ = engine.Apply(Pass)
	if engine.State.Phase != Playing {
		t.Error("one pass must not end play")
	}
	engine, _ = engine.Apply(Pass)
	if engine.State.Phase != ScoringP {
		t.Error("two passes must open scoring")
	}
	// Everything is rejected once play is over.
	for _, move := range []Move{Pass, Resign, Play(Point{Col: 0, Row: 0})} {
		if _, err := engine.Apply(move); err != ErrGameNotPlaying {
			t.Errorf("want game_not_playing, got %v", err)
		}
	}
	if engine.LegalMoves() != nil {
		t.Error("a game that is not playing has no legal moves")
	}
}

func TestPassResetsAfterAPlay(t *testing.T) {
	engine := NewGame(9, Japanese, 6.5, 0)
	engine, _ = engine.Apply(Pass)
	engine, _ = engine.Apply(Play(mustPoints(t, 9, "e5")[0]))
	if engine.State.ConsecutivePasses != 0 {
		t.Error("a play must reset the pass counter")
	}
	engine, _ = engine.Apply(Pass)
	if engine.State.Phase != Playing {
		t.Error("one pass on each side is not two in a row")
	}
}

func TestResignFinishesTheGame(t *testing.T) {
	engine, err := NewGame(9, Japanese, 6.5, 0).Apply(Resign)
	if err != nil {
		t.Fatal(err)
	}
	if engine.State.Phase != Finished || engine.State.Result.Winner != White {
		t.Errorf("black resigning should hand White the win: %+v", engine.State.Result)
	}
}

func TestDefaultKomi(t *testing.T) {
	if DefaultKomi(Japanese, 0) != 6.5 || DefaultKomi(Chinese, 0) != 7.5 || DefaultKomi(Japanese, 4) != 0.5 {
		t.Error("default komi is wrong")
	}
}

func TestHandicapStonesAreNotCountedAsMoves(t *testing.T) {
	engine := NewGame(19, Japanese, 0.5, 9)
	if engine.State.MoveNumber != 0 || engine.ToPlay() != White {
		t.Error("handicap stones are placed, not played")
	}
	if len(engine.State.Board.Stones(Black)) != 9 {
		t.Error("nine stones should be on the board")
	}
	if engine.HistorySize() != 1 {
		t.Error("the handicap position is the starting position")
	}
}

func TestPositionFillsInDefaults(t *testing.T) {
	engine := Position(PositionOptions{Size: 9})
	if engine.Rules != Japanese || engine.Komi != 6.5 || engine.ToPlay() != Black {
		t.Errorf("bad defaults: %+v", engine)
	}
}

func TestApplyByRejectsTheWrongColour(t *testing.T) {
	engine := NewGame(9, Japanese, 6.5, 0)
	if _, err := engine.ApplyBy(Pass, White); err != ErrNotYourTurn {
		t.Errorf("want not_your_turn, got %v", err)
	}
}

func TestNewGameRejectsAnImpossibleHandicap(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a panic for handicap 1")
		}
	}()
	NewGame(19, Japanese, 6.5, 1)
}

func TestNewBoardRejectsAnUnsupportedSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a panic for size 11")
		}
	}()
	NewBoard(11)
}

func TestScoreWinnerAndMarginCoverEveryOutcome(t *testing.T) {
	cases := []struct {
		black, white float64
		winner       Color
		margin       float64
	}{
		{40, 40.5, White, 0.5},
		{45.5, 40, Black, 5.5},
		{40, 40, Empty, 0}, // only reachable with an integer komi
	}
	for _, c := range cases {
		score := Score{Black: c.black, White: c.white}
		if got := score.Winner(); got != c.winner {
			t.Errorf("%v vs %v: want %v, got %v", c.black, c.white, c.winner, got)
		}
		if got := score.Margin(); got != c.margin {
			t.Errorf("%v vs %v: want a margin of %v, got %v", c.black, c.white, c.margin, got)
		}
	}
}

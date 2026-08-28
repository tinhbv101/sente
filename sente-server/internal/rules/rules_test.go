package rules

import (
	"math/rand"
	"testing"
	"time"
)

// Invariants checked over randomly generated games rather than hand-picked ones.
// The seeds are fixed so any failure is reproducible.

func selfPlay(t *testing.T, seed int64, size int, ruleSet RuleSet, moves int,
	onEachMove func(before, after Engine, move Move)) Engine {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	engine := NewGame(size, ruleSet, DefaultKomi(ruleSet, 0), 0)
	for i := 0; i < moves; i++ {
		if engine.State.Phase != Playing {
			break
		}
		legal := engine.LegalMoves()
		move := Pass
		if len(legal) > 0 {
			move = Play(legal[rng.Intn(len(legal))])
		}
		before := engine
		next, err := engine.Apply(move)
		if err != nil {
			t.Fatalf("seed %d: a move reported legal was rejected: %v", seed, err)
		}
		if onEachMove != nil {
			onEachMove(before, next, move)
		}
		engine = next
	}
	return engine
}

// No chain may ever sit on the board without a liberty: that is the whole point of
// the capture-then-suicide ordering.
func TestEveryChainAlwaysHasALiberty(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		selfPlay(t, seed, 9, Chinese, 120, func(_, engine Engine, move Move) {
			for _, p := range engine.State.Board.AllPoints() {
				if c := engine.State.Board.At(p); c != Black && c != White {
					continue
				}
				if engine.State.Board.Liberties(p) == 0 {
					t.Fatalf("seed %d: %v has no liberty after %v", seed, p, move)
				}
			}
		})
	}
}

// Stones placed must equal stones still standing plus stones taken.
func TestCaptureAccountingBalances(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		var placed Captures
		selfPlay(t, seed, 9, Chinese, 150, func(before, engine Engine, move Move) {
			if move.Kind == KindPlay {
				placed = placed.adding(1, before.ToPlay())
			}
			if got := len(engine.State.Board.Stones(Black)) + engine.State.Captures.White; got != placed.Black {
				t.Fatalf("seed %d: black accounting: want %d, got %d", seed, placed.Black, got)
			}
			if got := len(engine.State.Board.Stones(White)) + engine.State.Captures.Black; got != placed.White {
				t.Fatalf("seed %d: white accounting: want %d, got %d", seed, placed.White, got)
			}
		})
	}
}

// Replaying the same moves must reproduce the same position and hash -- the
// property the server relies on to rebuild a game from the moves table.
func TestReplayIsDeterministic(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		first := selfPlay(t, seed, 9, Japanese, 100, nil)
		second := selfPlay(t, seed, 9, Japanese, 100, nil)
		if !first.State.Board.Equal(second.State.Board) {
			t.Errorf("seed %d: boards differ", seed)
		}
		if first.State.BoardHash != second.State.BoardHash {
			t.Errorf("seed %d: hashes differ", seed)
		}
		if first.State.Captures != second.State.Captures {
			t.Errorf("seed %d: captures differ", seed)
		}
	}
}

// Apply must leave the receiver untouched: optimistic rollback on the client is
// nothing more than keeping the old value (docs/03 ADR-007).
func TestApplyNeverMutatesTheReceiver(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		selfPlay(t, seed, 9, Chinese, 80, func(before, after Engine, _ Move) {
			if before.State.BoardHash == after.State.BoardHash && after.State.MoveNumber > before.State.MoveNumber {
				return // a pass legitimately leaves the position alone
			}
			if before.State.Board.Equal(after.State.Board) {
				t.Fatalf("seed %d: the receiver's board changed with it", seed)
			}
		})
	}
}

// A position reached by playing moves must hash identically to the same position
// rebuilt from its stone lists.
func TestHashDoesNotDependOnHowThePositionWasBuilt(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		played := selfPlay(t, seed, 9, Chinese, 90, nil)
		rebuilt := Position(PositionOptions{
			Size:  9,
			Black: played.State.Board.Stones(Black),
			White: played.State.Board.Stones(White),
			Rules: Chinese,
		})
		if played.State.BoardHash != rebuilt.State.BoardHash {
			t.Errorf("seed %d: hash depends on construction path", seed)
		}
	}
}

// Legality is a property of the shape, not of where the shape sits: the eight
// board symmetries must all agree.
func TestLegalityIsInvariantUnderBoardSymmetries(t *testing.T) {
	const size = 9
	transform := func(p Point, index int) Point {
		last := size - 1
		switch index {
		case 1:
			return Point{Col: last - p.Col, Row: p.Row}
		case 2:
			return Point{Col: p.Col, Row: last - p.Row}
		case 3:
			return Point{Col: last - p.Col, Row: last - p.Row}
		case 4:
			return Point{Col: p.Row, Row: p.Col}
		case 5:
			return Point{Col: last - p.Row, Row: p.Col}
		case 6:
			return Point{Col: p.Row, Row: last - p.Col}
		case 7:
			return Point{Col: last - p.Row, Row: last - p.Col}
		default:
			return p
		}
	}
	mapAll := func(points []Point, index int) []Point {
		out := make([]Point, len(points))
		for i, p := range points {
			out[i] = transform(p, index)
		}
		return out
	}

	for seed := int64(1); seed <= 8; seed++ {
		source := selfPlay(t, seed, size, Chinese, 70, nil)
		black := source.State.Board.Stones(Black)
		white := source.State.Board.Stones(White)
		plain := Position(PositionOptions{Size: size, Black: black, White: white,
			ToPlay: source.ToPlay(), Rules: Chinese})

		for symmetry := 1; symmetry < 8; symmetry++ {
			mirrored := Position(PositionOptions{Size: size,
				Black: mapAll(black, symmetry), White: mapAll(white, symmetry),
				ToPlay: source.ToPlay(), Rules: Chinese})
			for _, p := range plain.State.Board.AllPoints() {
				a := plain.Validate(Play(p)) == nil
				b := mirrored.Validate(Play(transform(p, symmetry))) == nil
				if a != b {
					t.Fatalf("seed %d symmetry %d at %v: %v vs %v", seed, symmetry, p, a, b)
				}
			}
		}
	}
}

// Under Chinese rules every intersection is either area or neutral, so the three
// counts must add up to the whole board.
func TestAreaAndNeutralCoverTheWholeBoard(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		engine := selfPlay(t, seed, 9, Chinese, 140, nil)
		score := engine.Score(nil)
		neutral := len(engine.Territory(nil).Neutral)
		if got := score.BlackSide().Area + score.WhiteSide().Area + neutral; got != 81 {
			t.Errorf("seed %d: area+neutral = %d, want 81", seed, got)
		}
	}
}

// Scores are always multiples of a half point, so they stay exact in float64.
func TestScoresAreExactHalfPoints(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		score := selfPlay(t, seed, 9, Japanese, 120, nil).Score(nil)
		for _, v := range []float64{score.Black, score.White} {
			if doubled := v * 2; doubled != float64(int(doubled)) {
				t.Errorf("seed %d: %v is not a half point", seed, v)
			}
		}
	}
}

// A pass-alive chain must never be capturable, so the opponent playing every legal
// move in a row can never remove it.
func TestPassAliveChainsSurviveUnansweredAttack(t *testing.T) {
	parse := func(list ...string) []Point {
		out := make([]Point, 0, len(list))
		for _, text := range list {
			p, ok := ParseCoordinate(text, 9)
			if !ok {
				t.Fatalf("bad coordinate %q", text)
			}
			out = append(out, p)
		}
		return out
	}
	engine := Position(PositionOptions{
		Size:   9,
		Black:  parse("b9", "d9", "a8", "b8", "c8", "d8"),
		ToPlay: White,
		Rules:  Chinese,
	})
	var alive []Point
	for _, chain := range engine.State.Board.PassAliveChains(Black) {
		alive = append(alive, chain...)
	}
	if len(alive) == 0 {
		t.Fatal("expected a pass-alive chain to attack")
	}

	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 200 && engine.State.Phase == Playing; i++ {
		legal := engine.LegalMoves()
		if len(legal) == 0 {
			break
		}
		next, err := engine.Apply(Play(legal[rng.Intn(len(legal))]))
		if err != nil {
			t.Fatal(err)
		}
		engine = next
		for _, p := range alive {
			if engine.State.Board.At(p) != Black {
				t.Fatalf("pass-alive stone at %v was captured", p)
			}
		}
		// Black answers every move with a pass.
		if engine, err = engine.Apply(Pass); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCoordinateRoundTripsForEverySize(t *testing.T) {
	for _, size := range SupportedSizes {
		for _, p := range NewBoard(size).AllPoints() {
			text := CoordinateText(p, size)
			back, ok := ParseCoordinate(text, size)
			if !ok || back != p {
				t.Errorf("%s on %dx%d: got %v, ok=%v", text, size, size, back, ok)
			}
		}
	}
}

func TestColumnLettersSkipI(t *testing.T) {
	for i := 0; i < len(ColumnLetters); i++ {
		if ColumnLetters[i] == 'I' {
			t.Fatal("column letters must not contain I")
		}
	}
	if len(ColumnLetters) != 19 {
		t.Fatalf("want 19 column letters, got %d", len(ColumnLetters))
	}
}

func TestEmptyBoardHashesToZero(t *testing.T) {
	for _, size := range SupportedSizes {
		if h := ZobristHash(NewBoard(size)); h != 0 {
			t.Errorf("%dx%d empty board hashes to %#x", size, size, h)
		}
	}
}

// NFR-P5 allows 50us per legality check plus apply on 19x19. The bound is loose on
// purpose: it exists to catch an order-of-magnitude regression, not to benchmark.
func TestMoveCostStaysWithinBudget(t *testing.T) {
	engine := NewGame(19, Chinese, DefaultKomi(Chinese, 0), 0)
	rng := rand.New(rand.NewSource(7))
	points := engine.State.Board.AllPoints()
	rng.Shuffle(len(points), func(i, j int) { points[i], points[j] = points[j], points[i] })

	start := time.Now()
	applied := 0
	for _, p := range points {
		if engine.Validate(Play(p)) != nil {
			continue
		}
		next, err := engine.Apply(Play(p))
		if err != nil {
			t.Fatal(err)
		}
		engine = next
		if applied++; applied >= 200 {
			break
		}
	}
	perMove := time.Since(start).Seconds() / float64(applied) * 1e6
	t.Logf("validate+apply on 19x19: %.1fus per move", perMove)
	if perMove > 500 {
		t.Errorf("move cost regressed: %.1fus per move", perMove)
	}
}

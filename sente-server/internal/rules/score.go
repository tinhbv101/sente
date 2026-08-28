package rules

// TerritoryMap says which player each empty region belongs to once dead stones
// are removed.
type TerritoryMap struct {
	Black   []Point
	White   []Point
	Neutral []Point
}

// ScoreSide is one player's breakdown. Area is zero under Japanese rules;
// Captures and DeadStones are zero under Chinese rules.
type ScoreSide struct {
	Territory          int     `json:"territory"`
	Area               int     `json:"area"`
	Captures           int     `json:"captures"`
	DeadStones         int     `json:"dead_stones"`
	Komi               float64 `json:"komi"`
	HandicapAdjustment int     `json:"handicap_adjustment"`
	Total              float64 `json:"total"`
}

type Score struct {
	Rules RuleSet `json:"rules"`
	Black float64 `json:"black"`
	White float64 `json:"white"`
	Sides [2]ScoreSide
}

// BlackSide and WhiteSide keep callers away from the array index.
func (s Score) BlackSide() ScoreSide { return s.Sides[0] }
func (s Score) WhiteSide() ScoreSide { return s.Sides[1] }

func (s Score) Winner() Color {
	switch {
	case s.Black > s.White:
		return Black
	case s.White > s.Black:
		return White
	default:
		return Empty
	}
}

func (s Score) Margin() float64 {
	if s.Black > s.White {
		return s.Black - s.White
	}
	return s.White - s.Black
}

func (e Engine) boardRemovingDead(dead []Point) Board {
	return e.State.Board.clearing(dead)
}

// Territory flood-fills the empty regions of the board with dead stones removed.
func (e Engine) Territory(dead []Point) TerritoryMap {
	working := e.boardRemovingDead(dead)
	var out TerritoryMap
	visited := make(map[Point]bool)

	for _, start := range working.AllPoints() {
		if !working.IsEmpty(start) || visited[start] {
			continue
		}
		stack := []Point{start}
		visited[start] = true
		var region []Point
		borders := map[Color]bool{}

		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			region = append(region, current)
			for _, n := range working.neighbours(working.index(current)) {
				switch c := working.cells[n]; c {
				case Black, White:
					borders[c] = true
				case Empty:
					p := working.pointAt(n)
					if !visited[p] {
						visited[p] = true
						stack = append(stack, p)
					}
				}
			}
		}

		// A region touching exactly one colour is that player's territory; anything
		// else scores for nobody, which is also what makes seki come out right.
		switch {
		case len(borders) == 1 && borders[Black]:
			out.Black = append(out.Black, region...)
		case len(borders) == 1 && borders[White]:
			out.White = append(out.White, region...)
		default:
			out.Neutral = append(out.Neutral, region...)
		}
	}
	return out
}

// Score computes the final score for an agreed set of dead stones. Japanese counts
// territory plus prisoners; Chinese counts area and ignores prisoners (docs/02 §6.2-6.3).
func (e Engine) Score(dead []Point) Score {
	working := e.boardRemovingDead(dead)
	territory := e.Territory(dead)

	// A dead stone becomes a prisoner of the opponent of its own colour.
	var deadFor Captures
	for _, p := range dead {
		switch e.State.Board.At(p) {
		case Black:
			deadFor = deadFor.adding(1, White)
		case White:
			deadFor = deadFor.adding(1, Black)
		}
	}

	side := func(colour Color) ScoreSide {
		count := len(territory.Black)
		if colour == White {
			count = len(territory.White)
		}
		stones := len(working.Stones(colour))
		var komi float64
		if colour == White {
			komi = e.Komi
		}
		adjustment := 0
		if e.Rules == Chinese && colour == Black {
			adjustment = -e.Handicap
		}

		s := ScoreSide{Territory: count, Komi: komi, HandicapAdjustment: adjustment}
		if e.Rules == Japanese {
			s.Captures = e.State.Captures.Get(colour)
			s.DeadStones = deadFor.Get(colour)
			s.Total = float64(count+s.Captures+s.DeadStones) + komi
		} else {
			s.Area = stones + count
			s.Total = float64(s.Area+adjustment) + komi
		}
		return s
	}

	black, white := side(Black), side(White)
	return Score{
		Rules: e.Rules,
		Black: black.Total,
		White: white.Total,
		Sides: [2]ScoreSide{black, white},
	}
}

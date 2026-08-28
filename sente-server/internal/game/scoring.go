package game

import (
	"sort"

	"sente.app/server/internal/rules"
)

// The dead-stone negotiation (docs/02 §6.1). The rules give no algorithm for
// deciding life and death: the two players agree, and the server only keeps score
// of what they have agreed so far.

// ScoringSession is immutable; every method returns a new value.
type ScoringSession struct {
	// Dead is the set currently marked. A map keyed by point so toggling is cheap
	// and the order of marking never leaks into the result.
	Dead map[rules.Point]bool
	// Suggested is what the server proposed when scoring opened, kept so the UI can
	// show what a player changed.
	Suggested map[rules.Point]bool
	// ResumeFromMoveNumber is the position to return to if either player refuses:
	// the one before the first of the two passes.
	ResumeFromMoveNumber int

	blackAccepted bool
	whiteAccepted bool
}

func NewScoringSession(suggested []rules.Point, resumeFromMoveNumber int) ScoringSession {
	dead := make(map[rules.Point]bool, len(suggested))
	proposed := make(map[rules.Point]bool, len(suggested))
	for _, p := range suggested {
		dead[p] = true
		proposed[p] = true
	}
	return ScoringSession{Dead: dead, Suggested: proposed, ResumeFromMoveNumber: resumeFromMoveNumber}
}

func (s ScoringSession) clone() ScoringSession {
	dead := make(map[rules.Point]bool, len(s.Dead))
	for p := range s.Dead {
		dead[p] = true
	}
	s.Dead = dead
	return s
}

// DeadStones returns the marked points in a stable order, for hashing and for the wire.
func (s ScoringSession) DeadStones() []rules.Point {
	points := make([]rules.Point, 0, len(s.Dead))
	for p := range s.Dead {
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Row != points[j].Row {
			return points[i].Row < points[j].Row
		}
		return points[i].Col < points[j].Col
	})
	return points
}

// ToggleChain flips the whole chain containing p, not the single intersection --
// players think in groups, and marking half a group has no meaning.
//
// Any change clears both acceptances, so one player cannot accept and then have
// the other quietly alter what was agreed.
func (s ScoringSession) ToggleChain(board rules.Board, p rules.Point) ScoringSession {
	chain := board.Chain(p)
	if len(chain) == 0 {
		return s
	}
	next := s.clone()
	markDead := !next.Dead[chain[0]]
	for _, stone := range chain {
		if markDead {
			next.Dead[stone] = true
		} else {
			delete(next.Dead, stone)
		}
	}
	next.blackAccepted = false
	next.whiteAccepted = false
	return next
}

// ResetToSuggestion puts the server's proposal back, discarding every edit.
func (s ScoringSession) ResetToSuggestion() ScoringSession {
	next := s.clone()
	next.Dead = make(map[rules.Point]bool, len(s.Suggested))
	for p := range s.Suggested {
		next.Dead[p] = true
	}
	next.blackAccepted = false
	next.whiteAccepted = false
	return next
}

// SetAccepted records or withdraws one player's agreement.
func (s ScoringSession) SetAccepted(player rules.Color, accepted bool) ScoringSession {
	next := s.clone()
	if player == rules.Black {
		next.blackAccepted = accepted
	} else {
		next.whiteAccepted = accepted
	}
	return next
}

func (s ScoringSession) Accepted(player rules.Color) bool {
	if player == rules.Black {
		return s.blackAccepted
	}
	return s.whiteAccepted
}

// Settled reports whether both players have agreed to the current marking.
func (s ScoringSession) Settled() bool { return s.blackAccepted && s.whiteAccepted }

// Score is the result of the current marking, recomputed on every change so both
// clients can watch the number move as they tap.
func (s ScoringSession) Score(engine rules.Engine) rules.Score {
	return engine.Score(s.DeadStones())
}

func (s ScoringSession) Territory(engine rules.Engine) rules.TerritoryMap {
	return engine.Territory(s.DeadStones())
}

// EditedFromSuggestion reports whether a player changed the server's proposal.
// Tracked because a high rate means the estimator is not good enough (docs/10 R3).
func (s ScoringSession) EditedFromSuggestion() bool {
	if len(s.Dead) != len(s.Suggested) {
		return true
	}
	for p := range s.Dead {
		if !s.Suggested[p] {
			return true
		}
	}
	return false
}

// SuggestDeadStones proves death where it can be proved, and stays silent
// otherwise.
//
// A chain is certainly dead when it is not pass-alive and it sits inside a region
// sealed by opponent chains that are all pass-alive: the opponent can then capture
// it with unanswered moves, which is exactly the situation after two passes.
//
// This is the exact tier of ADR-009 only. In real games players stop before
// filling their own territory, so few groups are pass-alive and this suggests
// very little. The Monte Carlo tier that makes the feature actually useful needs
// a playout-speed engine that does not exist yet -- see docs/10 R3. Until then the
// server proposes almost nothing and the players mark stones themselves.
func SuggestDeadStones(board rules.Board) []rules.Point {
	passAlive := make(map[rules.Point]bool)
	aliveChainID := make(map[rules.Point]int)
	id := 0
	for _, colour := range []rules.Color{rules.Black, rules.White} {
		for _, chain := range board.PassAliveChains(colour) {
			for _, p := range chain {
				passAlive[p] = true
				aliveChainID[p] = id
			}
			id++
		}
	}

	var dead []rules.Point
	seen := make(map[rules.Point]bool)
	for _, start := range board.AllPoints() {
		colour := board.At(start)
		if colour != rules.Black && colour != rules.White {
			continue
		}
		if seen[start] || passAlive[start] {
			continue
		}
		chain := board.Chain(start)
		for _, p := range chain {
			seen[p] = true
		}
		if enclosedByLivingOpponent(board, chain, colour.Opponent(), passAlive) {
			dead = append(dead, chain...)
		}
	}
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].Row != dead[j].Row {
			return dead[i].Row < dead[j].Row
		}
		return dead[i].Col < dead[j].Col
	})
	return dead
}

// enclosedByLivingOpponent floods out from a chain through everything that is not
// an opponent stone. If the flood is bounded entirely by pass-alive opponent
// chains, the chain has nowhere to live.
func enclosedByLivingOpponent(board rules.Board, chain []rules.Point, opponent rules.Color,
	passAlive map[rules.Point]bool) bool {
	visited := make(map[rules.Point]bool, len(chain))
	stack := append([]rules.Point(nil), chain...)
	for _, p := range stack {
		visited[p] = true
	}
	sawBorder := false

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, n := range neighbours(current) {
			if !board.Contains(n) {
				continue
			}
			if board.At(n) == opponent {
				sawBorder = true
				if !passAlive[n] {
					return false
				}
				continue
			}
			if !visited[n] {
				visited[n] = true
				stack = append(stack, n)
			}
		}
	}
	return sawBorder
}

func neighbours(p rules.Point) [4]rules.Point {
	return [4]rules.Point{
		{Col: p.Col - 1, Row: p.Row},
		{Col: p.Col + 1, Row: p.Row},
		{Col: p.Col, Row: p.Row - 1},
		{Col: p.Col, Row: p.Row + 1},
	}
}

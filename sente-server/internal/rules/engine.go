package rules

import "sort"

// Engine is the rules engine. Every operation is pure: Apply returns a new Engine
// and leaves the receiver untouched.
type Engine struct {
	State    State
	Rules    RuleSet
	Komi     float64
	Handicap int

	// history holds the hash of every position seen, including the starting one.
	history map[uint64]struct{}
}

// DefaultKomi follows the usual conventions, dropping to half a point whenever
// stones are given so a handicap game cannot be drawn (docs/02 §8).
func DefaultKomi(rules RuleSet, handicap int) float64 {
	if handicap > 0 {
		return 0.5
	}
	if rules == Chinese {
		return 7.5
	}
	return 6.5
}

func NewGame(size int, rules RuleSet, komi float64, handicap int) Engine {
	if handicap != 0 && (handicap < 2 || handicap > 9) {
		panic("rules: handicap must be 0 or 2..9")
	}
	board := NewBoard(size)
	for _, p := range HandicapStones(handicap, size) {
		board = board.placing(p, Black)
	}
	toPlay := Black
	if handicap > 0 {
		// White moves first in a handicap game; the placed stones are not moves.
		toPlay = White
	}
	hash := ZobristHash(board)
	return Engine{
		State: State{
			Board:     board,
			ToPlay:    toPlay,
			Phase:     Playing,
			BoardHash: hash,
		},
		Rules:    rules,
		Komi:     komi,
		Handicap: handicap,
		history:  map[uint64]struct{}{hash: {}},
	}
}

// PositionOptions builds an engine from an arbitrary position, which is how the
// server rebuilds a game without replaying every move.
type PositionOptions struct {
	Size       int
	Black      []Point
	White      []Point
	ToPlay     Color
	Rules      RuleSet
	Komi       float64
	Handicap   int
	Captures   Captures
	MoveNumber int
}

func Position(opts PositionOptions) Engine {
	board := NewBoard(opts.Size)
	for _, p := range opts.Black {
		board = board.placing(p, Black)
	}
	for _, p := range opts.White {
		board = board.placing(p, White)
	}
	toPlay := opts.ToPlay
	if toPlay != Black && toPlay != White {
		toPlay = Black
	}
	rules := opts.Rules
	if rules == "" {
		rules = Japanese
	}
	komi := opts.Komi
	if komi == 0 {
		komi = DefaultKomi(rules, opts.Handicap)
	}
	hash := ZobristHash(board)
	return Engine{
		State: State{
			Board:      board,
			ToPlay:     toPlay,
			MoveNumber: opts.MoveNumber,
			Captures:   opts.Captures,
			Phase:      Playing,
			BoardHash:  hash,
		},
		Rules:    rules,
		Komi:     komi,
		Handicap: opts.Handicap,
		history:  map[uint64]struct{}{hash: {}},
	}
}

func (e Engine) Board() Board  { return e.State.Board }
func (e Engine) ToPlay() Color { return e.State.ToPlay }

func (e Engine) HistorySize() int { return len(e.history) }

func (e Engine) HistoryContains(hash uint64) bool {
	_, ok := e.history[hash]
	return ok
}

// LegalMoves returns every point the player to move may play, sorted so callers
// iterating it behave deterministically.
func (e Engine) LegalMoves() []Point {
	if e.State.Phase != Playing {
		return nil
	}
	var legal []Point
	for _, p := range e.State.Board.AllPoints() {
		if e.Validate(Play(p)) == nil {
			legal = append(legal, p)
		}
	}
	sort.Slice(legal, func(i, j int) bool {
		if legal[i].Row != legal[j].Row {
			return legal[i].Row < legal[j].Row
		}
		return legal[i].Col < legal[j].Col
	})
	return legal
}

// Validate checks a move for the player to move.
func (e Engine) Validate(move Move) error { return e.ValidateBy(move, e.State.ToPlay) }

// ValidateBy takes the colour explicitly. The server needs it because it maps a
// connection to a colour before knowing whether that colour is to move.
func (e Engine) ValidateBy(move Move, player Color) error {
	if e.State.Phase != Playing {
		return ErrGameNotPlaying
	}
	if player != e.State.ToPlay {
		return ErrNotYourTurn
	}
	if move.Kind != KindPlay {
		return nil
	}
	_, err := e.validatePlay(move.Point, player)
	return err
}

type simulation struct {
	board        Board
	captured     []Point
	ownLiberties int
	ownChainSize int
	hash         uint64
}

// validatePlay is shared by Validate and Apply so legality is decided exactly once.
func (e Engine) validatePlay(p Point, player Color) (simulation, error) {
	if !e.State.Board.Contains(p) {
		return simulation{}, ErrOutOfBounds
	}
	if !e.State.Board.IsEmpty(p) {
		return simulation{}, ErrOccupied
	}
	if player != e.State.ToPlay {
		return simulation{}, ErrNotYourTurn
	}
	// Basic ko is a cheap pre-check; Chinese has no basic ko at all.
	if e.Rules == Japanese && e.State.KoPoint != nil && *e.State.KoPoint == p {
		return simulation{}, ErrKo
	}

	sim := e.simulate(p, player)

	// Capturing first can create liberties, so suicide is judged last (docs/02 §3.2).
	if sim.ownLiberties == 0 {
		return simulation{}, ErrSuicide
	}
	if e.Rules == Chinese && e.HistoryContains(sim.hash) {
		return simulation{}, ErrSuperko
	}
	return sim, nil
}

// simulate places a stone, removes any opponent chain left without liberties,
// then measures the played chain. The order is mandatory -- see docs/02 §3.2.
func (e Engine) simulate(p Point, player Color) simulation {
	placed := e.State.Board.placing(p, player)
	origin := placed.index(p)
	opponent := player.Opponent()

	var captured []Point
	inspected := make(map[int]struct{})
	for _, n := range placed.neighbours(origin) {
		if placed.cells[n] != opponent {
			continue
		}
		if _, seen := inspected[n]; seen {
			continue
		}
		stones, liberties := placed.chainAndLiberties(n)
		for _, s := range stones {
			inspected[s] = struct{}{}
		}
		// Two distinct chains of one colour are never adjacent, so removing one
		// cannot revive another: captures can be collected before clearing.
		if liberties == 0 {
			for _, s := range stones {
				captured = append(captured, placed.pointAt(s))
			}
		}
	}

	board := placed.clearing(captured)
	ownChain, ownLiberties := board.chainAndLiberties(board.index(p))
	return simulation{
		board:        board,
		captured:     captured,
		ownLiberties: ownLiberties,
		ownChainSize: len(ownChain),
		hash:         ZobristHash(board),
	}
}

// Apply returns a new engine; the receiver is untouched.
func (e Engine) Apply(move Move) (Engine, error) {
	switch move.Kind {
	case KindResign:
		return e.applyResign()
	case KindPass:
		return e.applyPass()
	default:
		return e.applyPlay(move.Point)
	}
}

func (e Engine) ApplyBy(move Move, player Color) (Engine, error) {
	if err := e.ValidateBy(move, player); err != nil {
		return e, err
	}
	return e.Apply(move)
}

func (e Engine) applyResign() (Engine, error) {
	if e.State.Phase != Playing {
		return e, ErrGameNotPlaying
	}
	next := e
	next.State.MoveNumber++
	next.State.KoPoint = nil
	next.State.Phase = Finished
	next.State.Result = &Result{Winner: e.State.ToPlay.Opponent(), Reason: ReasonResignation}
	return next, nil
}

func (e Engine) applyPass() (Engine, error) {
	if e.State.Phase != Playing {
		return e, ErrGameNotPlaying
	}
	next := e
	next.State.ToPlay = e.State.ToPlay.Opponent()
	next.State.MoveNumber++
	next.State.ConsecutivePasses = e.State.ConsecutivePasses + 1
	next.State.KoPoint = nil
	if next.State.ConsecutivePasses >= 2 {
		next.State.Phase = ScoringP
	}
	// A pass leaves the position unchanged, so it never enters the history.
	return next, nil
}

func (e Engine) applyPlay(p Point) (Engine, error) {
	if e.State.Phase != Playing {
		return e, ErrGameNotPlaying
	}
	player := e.State.ToPlay
	sim, err := e.validatePlay(p, player)
	if err != nil {
		return e, err
	}

	// Japanese has no superko: a repeated position is a legal move that voids the
	// game (docs/02 §4.3).
	voidsGame := e.Rules == Japanese && e.HistoryContains(sim.hash)

	history := make(map[uint64]struct{}, len(e.history)+1)
	for hash := range e.history {
		history[hash] = struct{}{}
	}
	history[sim.hash] = struct{}{}

	next := Engine{
		State: State{
			Board:      sim.board,
			ToPlay:     player.Opponent(),
			MoveNumber: e.State.MoveNumber + 1,
			KoPoint:    koPointAfter(sim),
			Captures:   e.State.Captures.adding(len(sim.captured), player),
			Phase:      Playing,
			BoardHash:  sim.hash,
		},
		Rules:    e.Rules,
		Komi:     e.Komi,
		Handicap: e.Handicap,
		history:  history,
	}
	if voidsGame {
		next.State.Phase = Finished
		next.State.Result = &Result{Winner: Empty, Reason: ReasonRepetition}
	}
	return next, nil
}

// koPointAfter: a ko point exists only in the classic single-stone recapture
// shape (docs/02 §4.1).
func koPointAfter(sim simulation) *Point {
	if len(sim.captured) != 1 || sim.ownChainSize != 1 || sim.ownLiberties != 1 {
		return nil
	}
	captured := sim.captured[0]
	return &captured
}

// Finish ends a game for a reason the rules themselves do not decide: a lost
// clock, an agreed score, an abandoned game. Playing moves is the engine's
// business; wall-clock time and player agreement are not.
func (e Engine) Finish(result Result) Engine {
	next := e
	next.State.Phase = Finished
	next.State.Result = &result
	next.State.KoPoint = nil
	return next
}

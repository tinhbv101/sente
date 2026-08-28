package rules

// RuleSet picks more than a counting method: Japanese uses basic ko and voids a
// game on repetition, Chinese uses positional superko (docs/02).
type RuleSet string

const (
	Japanese RuleSet = "japanese"
	Chinese  RuleSet = "chinese"
)

type Phase string

const (
	Playing  Phase = "playing"
	ScoringP Phase = "scoring"
	Finished Phase = "finished"
)

type MoveKind uint8

const (
	KindPlay MoveKind = iota
	KindPass
	KindResign
)

type Move struct {
	Kind  MoveKind
	Point Point
}

func Play(p Point) Move { return Move{Kind: KindPlay, Point: p} }

var (
	Pass   = Move{Kind: KindPass}
	Resign = Move{Kind: KindResign}
)

// MoveError doubles as the wire code sent to clients (docs/06 §3.6), so the
// string here and the string on the protocol can never drift apart.
type MoveError string

const (
	ErrOutOfBounds    MoveError = "out_of_bounds"
	ErrOccupied       MoveError = "occupied"
	ErrNotYourTurn    MoveError = "not_your_turn"
	ErrSuicide        MoveError = "suicide"
	ErrKo             MoveError = "ko"
	ErrSuperko        MoveError = "superko"
	ErrGameNotPlaying MoveError = "game_not_playing"
)

func (e MoveError) Error() string { return string(e) }

type EndReason string

const (
	ReasonCounting    EndReason = "counting"
	ReasonResignation EndReason = "resignation"
	ReasonTimeout     EndReason = "timeout"
	ReasonRepetition  EndReason = "repetition"
	ReasonAbandonment EndReason = "abandonment"
	ReasonMutualDraw  EndReason = "mutual_draw"
)

// Result carries Empty as Winner for a void game -- Japanese repetition has no winner.
type Result struct {
	Winner Color
	Reason EndReason
	Score  *Score
}

// Captures counts prisoners, keyed by the player holding them.
type Captures struct {
	Black int
	White int
}

func (c Captures) Get(colour Color) int {
	if colour == Black {
		return c.Black
	}
	return c.White
}

func (c Captures) adding(n int, colour Color) Captures {
	if colour == Black {
		c.Black += n
	} else {
		c.White += n
	}
	return c
}

// State is a snapshot: plain data, no history and no rule configuration, so it is
// cheap to copy and to encode.
type State struct {
	Board             Board
	ToPlay            Color
	MoveNumber        int
	KoPoint           *Point // set only for basic ko; superko uses the history
	Captures          Captures
	ConsecutivePasses int
	Phase             Phase
	Result            *Result
	BoardHash         uint64
}

func (s State) Size() int { return s.Board.Size() }

package game

import (
	"fmt"
	"time"

	"sente.app/server/internal/rules"
)

// GameSession is a live game as a pure state machine: rules, clock, dead-stone
// negotiation and undo, with no goroutines, no I/O and no wall clock. Every
// command carries the timestamp it was received at.
//
// The goroutine that owns a game (docs/04 §3) is a thin shell around this. Keeping
// the decisions here is what lets timeouts and byo-yomi boundaries be tested to
// the millisecond instead of with sleeps.

type Config struct {
	Size        int
	Rules       rules.RuleSet
	Komi        float64
	Handicap    int
	TimeControl TimeControl
	// MaxUndos caps undo requests per game (FR-G10). Zero disables undo entirely.
	MaxUndos int
}

type CommandError string

func (e CommandError) Error() string { return string(e) }

const (
	ErrOutOfSync          CommandError = "out_of_sync"
	ErrNotScoring         CommandError = "not_scoring"
	ErrNotPlaying         CommandError = "game_not_playing"
	ErrNoUndoPending      CommandError = "no_undo_pending"
	ErrUndoAlreadyPending CommandError = "undo_already_pending"
	ErrUndoLimitReached   CommandError = "undo_limit_reached"
	ErrNothingToUndo      CommandError = "nothing_to_undo"
	ErrNotAPlayer         CommandError = "not_a_player"
	ErrTimeNotUp          CommandError = "time_not_up"
)

// UndoRequest is at most one outstanding request; a second is refused rather than
// queued, so neither player can stack them up as a distraction.
type UndoRequest struct {
	By rules.Color
}

type GameSession struct {
	Config  Config
	Engine  rules.Engine
	Clock   Clock
	Moves   []rules.RecordedMove
	Scoring *ScoringSession
	Undo    *UndoRequest

	undosUsed int
	// applied maps a client's idempotency key to the move number it produced, so a
	// move resent after a reconnect returns the original result (docs/03 ADR-007).
	applied map[string]int
}

func NewSession(config Config, startedAt time.Time) (GameSession, error) {
	if err := config.TimeControl.Validate(); err != nil {
		return GameSession{}, err
	}
	clock, err := NewClock(config.TimeControl, startedAt)
	if err != nil {
		return GameSession{}, err
	}
	komi := config.Komi
	if komi == 0 {
		komi = rules.DefaultKomi(config.Rules, config.Handicap)
	}
	return GameSession{
		Config:  config,
		Engine:  rules.NewGame(config.Size, config.Rules, komi, config.Handicap),
		Clock:   clock,
		applied: map[string]int{},
	}, nil
}

func (s GameSession) Phase() rules.Phase    { return s.Engine.State.Phase }
func (s GameSession) ToPlay() rules.Color   { return s.Engine.ToPlay() }
func (s GameSession) MoveNumber() int       { return s.Engine.State.MoveNumber }
func (s GameSession) Result() *rules.Result { return s.Engine.State.Result }

// clone copies the maps that would otherwise be shared with the previous value.
func (s GameSession) clone() GameSession {
	applied := make(map[string]int, len(s.applied)+1)
	for k, v := range s.applied {
		applied[k] = v
	}
	s.applied = applied
	s.Moves = append([]rules.RecordedMove(nil), s.Moves...)
	return s
}

// ── commands ────────────────────────────────────────────────────────────────

type Command interface{ commandBy() rules.Color }

// PlayCommand carries the optimistic-concurrency and idempotency keys from
// docs/06 §3.5 alongside the move itself.
type PlayCommand struct {
	By                 rules.Color
	Move               rules.Move
	ClientMoveID       string
	ExpectedMoveNumber int
	// SkipExpectedCheck is for replaying a stored game, where there is no client.
	SkipExpectedCheck bool
}

type RequestUndoCommand struct{ By rules.Color }
type RespondUndoCommand struct {
	By     rules.Color
	Accept bool
}
type MarkDeadCommand struct {
	By    rules.Color
	Point rules.Point
}
type AcceptScoreCommand struct {
	By       rules.Color
	Accepted bool
}
type ResumePlayCommand struct{ By rules.Color }

// TimeoutCommand is raised by the owning node's timer, never by a client.
type TimeoutCommand struct{ Player rules.Color }

func (c PlayCommand) commandBy() rules.Color        { return c.By }
func (c RequestUndoCommand) commandBy() rules.Color { return c.By }
func (c RespondUndoCommand) commandBy() rules.Color { return c.By }
func (c MarkDeadCommand) commandBy() rules.Color    { return c.By }
func (c AcceptScoreCommand) commandBy() rules.Color { return c.By }
func (c ResumePlayCommand) commandBy() rules.Color  { return c.By }
func (c TimeoutCommand) commandBy() rules.Color     { return c.Player }

// ── events ──────────────────────────────────────────────────────────────────

type Event interface{ eventName() string }

type MoveMade struct {
	MoveNumber int
	By         rules.Color
	Move       rules.Move
	Captured   []rules.Point
	BoardHash  uint64
	Clock      Clock
	// Duplicate marks a move replayed from the idempotency cache rather than newly
	// applied, so the caller knows not to write it to the database again.
	Duplicate bool
}

type ScoringOpened struct {
	Suggested            []rules.Point
	ResumeFromMoveNumber int
}

type ScoringChanged struct {
	Dead          []rules.Point
	BlackAccepted bool
	WhiteAccepted bool
	Score         rules.Score
}

type PlayResumed struct {
	MoveNumber int
	ToPlay     rules.Color
}

type UndoRequested struct{ By rules.Color }
type UndoResolved struct {
	By         rules.Color
	Accepted   bool
	MoveNumber int
}

type GameEnded struct{ Result rules.Result }

// ClockAdjusted announces time handed back after a server-side interruption. It is
// broadcast so players see why the clock jumped rather than suspecting a bug
// (docs/04 §4.5).
type ClockAdjusted struct {
	Player rules.Color
	Delta  time.Duration
	Reason string
}

func (MoveMade) eventName() string       { return "move_made" }
func (ScoringOpened) eventName() string  { return "scoring_opened" }
func (ScoringChanged) eventName() string { return "scoring_changed" }
func (PlayResumed) eventName() string    { return "play_resumed" }
func (UndoRequested) eventName() string  { return "undo_requested" }
func (UndoResolved) eventName() string   { return "undo_resolved" }
func (GameEnded) eventName() string      { return "game_ended" }
func (ClockAdjusted) eventName() string  { return "clock_adjusted" }

// ── the state machine ───────────────────────────────────────────────────────

// Apply returns a new session and the events to broadcast. The receiver is never
// modified, so a rejected command leaves the caller holding the original.
func (s GameSession) Apply(command Command, at time.Time) (GameSession, []Event, error) {
	switch c := command.(type) {
	case PlayCommand:
		return s.play(c, at)
	case TimeoutCommand:
		return s.timeout(c, at)
	case RequestUndoCommand:
		return s.requestUndo(c)
	case RespondUndoCommand:
		return s.respondUndo(c, at)
	case MarkDeadCommand:
		return s.markDead(c)
	case AcceptScoreCommand:
		return s.acceptScore(c, at)
	case ResumePlayCommand:
		return s.resumePlay(c, at)
	default:
		return s, nil, fmt.Errorf("unknown command %T", command)
	}
}

func (s GameSession) play(c PlayCommand, at time.Time) (GameSession, []Event, error) {
	// Idempotency first: a resent move must not be judged against the clock or the
	// turn order a second time.
	if c.ClientMoveID != "" {
		if moveNumber, seen := s.applied[c.ClientMoveID]; seen {
			return s, []Event{MoveMade{
				MoveNumber: moveNumber, By: c.By, Move: c.Move,
				BoardHash: s.Engine.State.BoardHash, Clock: s.Clock, Duplicate: true,
			}}, nil
		}
	}
	if s.Phase() != rules.Playing {
		return s, nil, ErrNotPlaying
	}
	if c.By != s.ToPlay() {
		return s, nil, rules.ErrNotYourTurn
	}
	if !c.SkipExpectedCheck && c.ExpectedMoveNumber != s.MoveNumber() {
		return s, nil, ErrOutOfSync
	}

	// A move that arrives after the deadline loses, whatever it says on the board.
	if s.Clock.HasTimedOut(c.By, at) {
		return s.endOnTime(c.By, at)
	}

	before := s.Engine
	engine, err := s.Engine.ApplyBy(c.Move, c.By)
	if err != nil {
		return s, nil, err
	}
	clock, timedOut := s.Clock.Charge(c.By, at)
	if timedOut {
		return s.endOnTime(c.By, at)
	}

	next := s.clone()
	next.Engine = engine
	next.Clock = clock
	next.Moves = append(next.Moves, rules.RecordedMove{Player: c.By, Move: c.Move})
	next.Undo = nil
	if c.ClientMoveID != "" {
		next.applied[c.ClientMoveID] = engine.State.MoveNumber
	}

	events := []Event{MoveMade{
		MoveNumber: engine.State.MoveNumber,
		By:         c.By,
		Move:       c.Move,
		Captured:   capturedBy(before.Board(), engine.Board()),
		BoardHash:  engine.State.BoardHash,
		Clock:      clock,
	}}

	switch engine.State.Phase {
	case rules.ScoringP:
		next, opened := next.openScoring()
		return next, append(events, opened), nil
	case rules.Finished:
		return next, append(events, GameEnded{Result: *engine.State.Result}), nil
	}
	return next, events, nil
}

// openScoring proposes what can be proved dead and records where to rewind to if
// the players cannot agree (docs/02 §6.1).
func (s GameSession) openScoring() (GameSession, Event) {
	suggested := SuggestDeadStones(s.Engine.Board())
	resumeFrom := s.MoveNumber() - 2
	if resumeFrom < 0 {
		resumeFrom = 0
	}
	session := NewScoringSession(suggested, resumeFrom)
	s.Scoring = &session
	return s, ScoringOpened{Suggested: suggested, ResumeFromMoveNumber: resumeFrom}
}

func (s GameSession) timeout(c TimeoutCommand, at time.Time) (GameSession, []Event, error) {
	if s.Phase() != rules.Playing {
		return s, nil, ErrNotPlaying
	}
	if c.Player != s.ToPlay() {
		return s, nil, rules.ErrNotYourTurn
	}
	if !s.Clock.HasTimedOut(c.Player, at) {
		// Guards against a stale timer firing after the move already arrived.
		return s, nil, ErrTimeNotUp
	}
	return s.endOnTime(c.Player, at)
}

func (s GameSession) endOnTime(loser rules.Color, at time.Time) (GameSession, []Event, error) {
	next := s.clone()
	clock, _ := next.Clock.Charge(loser, at)
	next.Clock = clock
	result := rules.Result{Winner: loser.Opponent(), Reason: rules.ReasonTimeout}
	next.Engine = next.Engine.Finish(result)
	return next, []Event{GameEnded{Result: result}}, nil
}

func (s GameSession) requestUndo(c RequestUndoCommand) (GameSession, []Event, error) {
	if s.Phase() != rules.Playing {
		return s, nil, ErrNotPlaying
	}
	if len(s.Moves) == 0 {
		return s, nil, ErrNothingToUndo
	}
	if s.Undo != nil {
		return s, nil, ErrUndoAlreadyPending
	}
	if s.undosUsed >= s.Config.MaxUndos {
		return s, nil, ErrUndoLimitReached
	}
	next := s.clone()
	next.Undo = &UndoRequest{By: c.By}
	return next, []Event{UndoRequested{By: c.By}}, nil
}

func (s GameSession) respondUndo(c RespondUndoCommand, at time.Time) (GameSession, []Event, error) {
	if s.Undo == nil {
		return s, nil, ErrNoUndoPending
	}
	if c.By == s.Undo.By {
		// Answering your own request would let one player undo unilaterally.
		return s, nil, ErrNotAPlayer
	}
	next := s.clone()
	next.Undo = nil
	if !c.Accept {
		return next, []Event{UndoResolved{By: c.By, Accepted: false, MoveNumber: s.MoveNumber()}}, nil
	}

	next.undosUsed++
	next.Moves = next.Moves[:len(next.Moves)-1]
	next.Engine = rebuild(next.Config, next.Moves)
	next.Clock = next.Clock.StartTurn(at)
	return next, []Event{UndoResolved{
		By: c.By, Accepted: true, MoveNumber: next.MoveNumber(),
	}}, nil
}

func (s GameSession) markDead(c MarkDeadCommand) (GameSession, []Event, error) {
	if s.Phase() != rules.ScoringP || s.Scoring == nil {
		return s, nil, ErrNotScoring
	}
	session := s.Scoring.ToggleChain(s.Engine.Board(), c.Point)
	next := s.clone()
	next.Scoring = &session
	return next, []Event{next.scoringChanged()}, nil
}

func (s GameSession) acceptScore(c AcceptScoreCommand, at time.Time) (GameSession, []Event, error) {
	if s.Phase() != rules.ScoringP || s.Scoring == nil {
		return s, nil, ErrNotScoring
	}
	session := s.Scoring.SetAccepted(c.By, c.Accepted)
	next := s.clone()
	next.Scoring = &session
	events := []Event{next.scoringChanged()}

	if !session.Settled() {
		return next, events, nil
	}
	score := session.Score(next.Engine)
	result := rules.Result{Winner: score.Winner(), Reason: rules.ReasonCounting, Score: &score}
	next.Engine = next.Engine.Finish(result)
	return next, append(events, GameEnded{Result: result}), nil
}

// resumePlay rewinds past the trailing passes until the player who refused the
// result is on move again.
//
// Docs/02 §6.1 says to rewind to the position before the first pass and give the
// move to the refuser. Those two are not always the same position: implementing it
// this way satisfies both halves of the sentence.
func (s GameSession) resumePlay(c ResumePlayCommand, at time.Time) (GameSession, []Event, error) {
	if s.Phase() != rules.ScoringP {
		return s, nil, ErrNotScoring
	}
	moves := s.Moves
	for len(moves) > 0 && moves[len(moves)-1].Move.Kind == rules.KindPass {
		moves = moves[:len(moves)-1]
		if rebuild(s.Config, moves).ToPlay() == c.By {
			break
		}
	}

	next := s.clone()
	next.Moves = moves
	next.Engine = rebuild(next.Config, moves)
	next.Clock = next.Clock.StartTurn(at)
	next.Scoring = nil
	return next, []Event{PlayResumed{
		MoveNumber: next.MoveNumber(), ToPlay: next.ToPlay(),
	}}, nil
}

func (s GameSession) scoringChanged() ScoringChanged {
	return ScoringChanged{
		Dead:          s.Scoring.DeadStones(),
		BlackAccepted: s.Scoring.Accepted(rules.Black),
		WhiteAccepted: s.Scoring.Accepted(rules.White),
		Score:         s.Scoring.Score(s.Engine),
	}
}

// rebuild replays a move list from the start. Replaying ~300 moves costs about a
// millisecond, which is cheaper than keeping every intermediate position in memory
// (docs/03 ADR-006).
func rebuild(config Config, moves []rules.RecordedMove) rules.Engine {
	komi := config.Komi
	if komi == 0 {
		komi = rules.DefaultKomi(config.Rules, config.Handicap)
	}
	engine := rules.NewGame(config.Size, config.Rules, komi, config.Handicap)
	for _, m := range moves {
		next, err := engine.ApplyBy(m.Move, m.Player)
		if err != nil {
			// Only reachable if a stored move list is corrupt; the caller should
			// treat a short replay as a data problem, not carry on silently.
			return engine
		}
		engine = next
	}
	return engine
}

// capturedBy diffs two boards to name the stones a move removed.
func capturedBy(before, after rules.Board) []rules.Point {
	var captured []rules.Point
	for _, p := range before.AllPoints() {
		if before.At(p) != rules.Empty && after.At(p) == rules.Empty {
			captured = append(captured, p)
		}
	}
	return captured
}

// ── restoring from storage ──────────────────────────────────────────────────

type RestoreParams struct {
	Config  Config
	Moves   []rules.RecordedMove
	Clock   Clock
	Scoring *ScoringSession
	// Result is set for games that ended for a reason the move list cannot show:
	// a lost clock, an agreed score, an abandoned game.
	Result         *rules.Result
	AppliedMoveIDs map[string]int
	UndosUsed      int
	// ExpectedBoardHash is the checksum stored alongside the game. Zero skips the
	// check; anything else must match or the replay is refused.
	ExpectedBoardHash uint64
}

// ErrChecksumMismatch means replaying the stored moves produced a different
// position from the one recorded when they were written. That is an engine
// change, not a data glitch, and it must never be papered over (docs/03 ADR-006).
type ErrChecksumMismatch struct {
	GameMoveNumber int
	Stored         uint64
	Rebuilt        uint64
}

func (e ErrChecksumMismatch) Error() string {
	return fmt.Sprintf("board hash mismatch after replaying %d moves: stored %#016x, rebuilt %#016x",
		e.GameMoveNumber, e.Stored, e.Rebuilt)
}

// Restore rebuilds a session by replaying its moves, then layers on the state that
// replaying cannot produce.
func Restore(p RestoreParams) (GameSession, error) {
	if err := p.Config.TimeControl.Validate(); err != nil {
		return GameSession{}, err
	}
	engine := rebuild(p.Config, p.Moves)
	if engine.State.MoveNumber != len(p.Moves) {
		return GameSession{}, fmt.Errorf("stored move list is not playable: stopped at move %d of %d",
			engine.State.MoveNumber, len(p.Moves))
	}
	if p.ExpectedBoardHash != 0 && engine.State.BoardHash != p.ExpectedBoardHash {
		return GameSession{}, ErrChecksumMismatch{
			GameMoveNumber: engine.State.MoveNumber,
			Stored:         p.ExpectedBoardHash,
			Rebuilt:        engine.State.BoardHash,
		}
	}
	if p.Result != nil {
		engine = engine.Finish(*p.Result)
	}

	applied := make(map[string]int, len(p.AppliedMoveIDs))
	for k, v := range p.AppliedMoveIDs {
		applied[k] = v
	}
	return GameSession{
		Config:    p.Config,
		Engine:    engine,
		Clock:     p.Clock,
		Moves:     append([]rules.RecordedMove(nil), p.Moves...),
		Scoring:   p.Scoring,
		undosUsed: p.UndosUsed,
		applied:   applied,
	}, nil
}

// ResumeTurnAt restarts the current turn's reference point without charging
// anyone, which is how a node taking a game over forgives an outage.
func (s GameSession) ResumeTurnAt(at time.Time) GameSession {
	next := s.clone()
	next.Clock = next.Clock.StartTurn(at)
	return next
}

// UndosUsed exposes the counter for persistence.
func (s GameSession) UndosUsed() int { return s.undosUsed }

// AppliedMoveIDs exposes the idempotency cache for persistence.
func (s GameSession) AppliedMoveIDs() map[string]int {
	out := make(map[string]int, len(s.applied))
	for k, v := range s.applied {
		out[k] = v
	}
	return out
}

// RestoreScoringSession rebuilds a negotiation from stored columns.
func RestoreScoringSession(dead, suggested []rules.Point, blackAccepted, whiteAccepted bool,
	resumeFromMoveNumber int) *ScoringSession {
	session := NewScoringSession(suggested, resumeFromMoveNumber)
	session.Dead = make(map[rules.Point]bool, len(dead))
	for _, p := range dead {
		session.Dead[p] = true
	}
	session.blackAccepted = blackAccepted
	session.whiteAccepted = whiteAccepted
	return &session
}

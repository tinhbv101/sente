package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

// RulesVersion is stamped on every game so it can be replayed by the engine that
// produced it, even after the rules package changes (docs/03 ADR-006).
const RulesVersion = "1.0.0"

var ErrNotFound = errors.New("store: game not found")

type Games struct {
	pool *pgxpool.Pool
}

func NewGames(pool *pgxpool.Pool) *Games { return &Games{pool: pool} }

type CreateParams struct {
	Config      game.Config
	BlackUserID string
	WhiteUserID string
	IsRanked    bool
	StartedAt   time.Time
}

type LoadedGame struct {
	ID          string
	Session     game.GameSession
	BlackUserID string
	WhiteUserID string
	IsRanked    bool
	// LastActivityAt is when the game was last written. A node taking a game over
	// uses it to work out how long the game was stranded (docs/04 §4.5).
	LastActivityAt time.Time
	// ParkedAt is set while no node runs the game on purpose. Nil on load means
	// the last owner never said goodbye -- it crashed.
	ParkedAt *time.Time
}

// MoveRecord is one row of the append-only move log.
type MoveRecord struct {
	MoveNo        int
	Color         rules.Color
	Move          rules.Move
	CapturedCount int
	BoardHash     uint64
	// ClientMoveID is the caller's idempotency key; empty means none.
	ClientMoveID string
	PlayedAt     time.Time
	TimeLeftMs   *int
	PeriodsLeft  *int
}

// Create writes a new game and returns its id.
func (g *Games) Create(ctx context.Context, params CreateParams) (string, error) {
	return createGameTx(ctx, g.pool, params)
}

// execer is satisfied by both the pool and a transaction, so creating a game
// works standalone or as part of accepting an invitation.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func createGameTx(ctx context.Context, db execer, params CreateParams) (string, error) {
	session, err := game.NewSession(params.Config, params.StartedAt)
	if err != nil {
		return "", err
	}
	id := NewID()
	timeControl, err := json.Marshal(params.Config.TimeControl)
	if err != nil {
		return "", err
	}
	clock, err := json.Marshal(session.Clock)
	if err != nil {
		return "", err
	}
	deadline := session.Clock.Deadline(session.ToPlay())

	_, err = db.Exec(ctx, `
		INSERT INTO games (
			id, board_size, rules, rules_version, komi, handicap, time_control,
			is_ranked, is_correspondence, black_user_id, white_user_id,
			phase, to_play, current_move_no, board_hash, clock, move_deadline,
			started_at, last_activity_at, parked_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11,
			'playing', $12, 0, $13, $14, $15,
			$16, $16, $16
		)`,
		id, params.Config.Size, string(params.Config.Rules), RulesVersion,
		komiOf(params.Config), params.Config.Handicap, timeControl,
		params.IsRanked, params.Config.TimeControl.Kind == game.Correspondence,
		nullable(params.BlackUserID), nullable(params.WhiteUserID),
		session.ToPlay().String(), int64(session.Engine.State.BoardHash), clock, deadline,
		params.StartedAt,
	)
	if err != nil {
		return "", fmt.Errorf("store: creating game: %w", err)
	}
	return id, nil
}

// Load replays a game from its move log. The stored board hash is verified as a
// checksum, so a silent engine change surfaces here rather than mid-game.
func (g *Games) Load(ctx context.Context, id string) (*LoadedGame, error) {
	var (
		size, handicap          int
		ruleSet, phase          string
		komi                    float64
		timeControlJSON         []byte
		clockJSON               []byte
		resultJSON              []byte
		boardHash               int64
		isRanked                bool
		black, white            *string
		storedRulesVersion      string
		currentMoveNo, undoUsed int
		lastActivityAt          time.Time
		parkedAt                *time.Time
	)
	err := g.pool.QueryRow(ctx, `
		SELECT board_size, rules, rules_version, komi, handicap, time_control,
		       clock, result, board_hash, is_ranked, black_user_id, white_user_id,
		       current_move_no, undos_used, last_activity_at, parked_at
		  FROM games WHERE id = $1`, id).
		Scan(&size, &ruleSet, &storedRulesVersion, &komi, &handicap, &timeControlJSON,
			&clockJSON, &resultJSON, &boardHash, &isRanked, &black, &white,
			&currentMoveNo, &undoUsed, &lastActivityAt, &parkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: loading game: %w", err)
	}

	var timeControl game.TimeControl
	if err := json.Unmarshal(timeControlJSON, &timeControl); err != nil {
		return nil, fmt.Errorf("store: decoding time control: %w", err)
	}
	var clock game.Clock
	if err := json.Unmarshal(clockJSON, &clock); err != nil {
		return nil, fmt.Errorf("store: decoding clock: %w", err)
	}
	var result *rules.Result
	if len(resultJSON) > 0 {
		result = &rules.Result{}
		if err := json.Unmarshal(resultJSON, result); err != nil {
			return nil, fmt.Errorf("store: decoding result: %w", err)
		}
	}

	moves, appliedIDs, err := g.loadMoves(ctx, id, size)
	if err != nil {
		return nil, err
	}
	scoring, err := g.loadScoring(ctx, id, size)
	if err != nil {
		return nil, err
	}

	session, err := game.Restore(game.RestoreParams{
		Config: game.Config{
			Size: size, Rules: rules.RuleSet(ruleSet), Komi: komi,
			Handicap: handicap, TimeControl: timeControl, MaxUndos: defaultMaxUndos,
		},
		Moves:             moves,
		Clock:             clock,
		Scoring:           scoring,
		Result:            result,
		AppliedMoveIDs:    appliedIDs,
		UndosUsed:         undoUsed,
		ExpectedBoardHash: uint64(boardHash),
	})
	if err != nil {
		return nil, fmt.Errorf("store: restoring game %s: %w", id, err)
	}
	_ = phase

	return &LoadedGame{
		ID: id, Session: session, IsRanked: isRanked,
		BlackUserID: deref(black), WhiteUserID: deref(white),
		LastActivityAt: lastActivityAt, ParkedAt: parkedAt,
	}, nil
}

const defaultMaxUndos = 3

func (g *Games) loadMoves(ctx context.Context, id string, size int) ([]rules.RecordedMove, map[string]int, error) {
	rows, err := g.pool.Query(ctx, `
		SELECT move_no, color, kind, point, client_move_id
		  FROM moves WHERE game_id = $1 ORDER BY move_no`, id)
	if err != nil {
		return nil, nil, fmt.Errorf("store: loading moves: %w", err)
	}
	defer rows.Close()

	var moves []rules.RecordedMove
	applied := map[string]int{}
	for rows.Next() {
		var (
			moveNo       int
			colour, kind string
			point        *int16
			clientMoveID *string
		)
		if err := rows.Scan(&moveNo, &colour, &kind, &point, &clientMoveID); err != nil {
			return nil, nil, err
		}
		move, err := decodeMove(kind, point, size)
		if err != nil {
			return nil, nil, fmt.Errorf("store: move %d of game %s: %w", moveNo, id, err)
		}
		moves = append(moves, rules.RecordedMove{Player: colourFromString(colour), Move: move})
		if clientMoveID != nil {
			applied[*clientMoveID] = moveNo
		}
	}
	return moves, applied, rows.Err()
}

func (g *Games) loadScoring(ctx context.Context, id string, size int) (*game.ScoringSession, error) {
	var (
		dead, suggested              []int16
		blackAccepted, whiteAccepted bool
		resumeFrom                   int
	)
	err := g.pool.QueryRow(ctx, `
		SELECT dead_points, suggested_points, black_accepted, white_accepted, resume_from_move_no
		  FROM game_scoring WHERE game_id = $1`, id).
		Scan(&dead, &suggested, &blackAccepted, &whiteAccepted, &resumeFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: loading scoring: %w", err)
	}
	return game.RestoreScoringSession(decodePoints(dead, size), decodePoints(suggested, size),
		blackAccepted, whiteAccepted, resumeFrom), nil
}

// AppendMove writes one move and the state it produced in a single transaction.
//
// Returns false when the move was already stored: a client that resends after a
// reconnect must not create a second row, and the unique index on
// (game_id, client_move_id) is what actually enforces that under concurrency.
func (g *Games) AppendMove(ctx context.Context, id string, session game.GameSession,
	record MoveRecord) (bool, error) {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	point, err := encodePoint(record.Move, session.Config.Size)
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO moves (game_id, move_no, color, kind, point, captured_count,
		                   board_hash, client_move_id, played_at, time_left_ms, periods_left)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT DO NOTHING`,
		id, record.MoveNo, record.Color.String(), moveKind(record.Move), point,
		record.CapturedCount, int64(record.BoardHash), nullable(record.ClientMoveID),
		record.PlayedAt, record.TimeLeftMs, record.PeriodsLeft)
	if err != nil {
		return false, fmt.Errorf("store: appending move: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := updateGameRow(ctx, tx, id, session, record.PlayedAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func updateGameRow(ctx context.Context, tx pgx.Tx, id string, session game.GameSession,
	at time.Time) error {
	clock, err := json.Marshal(session.Clock)
	if err != nil {
		return err
	}
	var resultJSON []byte
	var endedAt *time.Time
	if result := session.Result(); result != nil {
		if resultJSON, err = json.Marshal(result); err != nil {
			return err
		}
		endedAt = &at
	}

	var deadline *time.Time
	if session.Phase() == rules.Playing {
		d := session.Clock.Deadline(session.ToPlay())
		deadline = &d
	}
	var koPoint *int16
	if ko := session.Engine.State.KoPoint; ko != nil {
		encoded := int16(ko.Row*session.Config.Size + ko.Col)
		koPoint = &encoded
	}

	_, err = tx.Exec(ctx, `
		UPDATE games SET
			phase = $2, to_play = $3, current_move_no = $4, consecutive_passes = $5,
			ko_point = $6, board_hash = $7, captures_black = $8, captures_white = $9,
			clock = $10, move_deadline = $11, result = $12, ended_at = COALESCE($13, ended_at),
			last_activity_at = $14, undos_used = $15
		WHERE id = $1`,
		id, string(session.Phase()), session.ToPlay().String(), session.MoveNumber(),
		session.Engine.State.ConsecutivePasses, koPoint,
		int64(session.Engine.State.BoardHash),
		session.Engine.State.Captures.Black, session.Engine.State.Captures.White,
		clock, deadline, resultJSON, endedAt, at, session.UndosUsed())
	if err != nil {
		return fmt.Errorf("store: updating game: %w", err)
	}
	return nil
}

// SaveScoring checkpoints the dead-stone negotiation. It is written periodically
// rather than on every tap: losing a few taps to a crash costs nothing, and the
// agreed result is written by Finish.
func (g *Games) SaveScoring(ctx context.Context, id string, session game.GameSession) error {
	if session.Scoring == nil {
		return errors.New("store: no scoring session to save")
	}
	size := session.Config.Size
	score := session.Scoring.Score(session.Engine)
	scoreJSON, err := json.Marshal(score)
	if err != nil {
		return err
	}
	_, err = g.pool.Exec(ctx, `
		INSERT INTO game_scoring (game_id, dead_points, suggested_points,
		                          black_accepted, white_accepted, computed_score,
		                          resume_from_move_no, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (game_id) DO UPDATE SET
			dead_points = EXCLUDED.dead_points,
			black_accepted = EXCLUDED.black_accepted,
			white_accepted = EXCLUDED.white_accepted,
			computed_score = EXCLUDED.computed_score,
			updated_at = now()`,
		id, encodePoints(session.Scoring.DeadStones(), size),
		encodePoints(suggestedOf(session.Scoring), size),
		session.Scoring.Accepted(rules.Black), session.Scoring.Accepted(rules.White),
		scoreJSON, session.Scoring.ResumeFromMoveNumber)
	if err != nil {
		return fmt.Errorf("store: saving scoring: %w", err)
	}
	return nil
}

// Finish records an ending that no move produced: a lost clock, an agreed score,
// an abandoned game.
func (g *Games) Finish(ctx context.Context, id string, session game.GameSession,
	at time.Time) error {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := updateGameRow(ctx, tx, id, session, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Rewind persists a session whose move list got shorter: an accepted undo, or
// "play on" from scoring, which drops the passes. The log is append-only in
// spirit, but moves both players agreed never happened must not be replayed on
// the next load, or the rebuilt position disagrees with the row and the game is
// stuck -- in the wrong phase, or refused by the checksum.
func (g *Games) Rewind(ctx context.Context, id string, session game.GameSession, at time.Time) error {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM moves WHERE game_id = $1 AND move_no > $2`,
		id, session.MoveNumber()); err != nil {
		return fmt.Errorf("store: rewinding moves: %w", err)
	}
	if session.Scoring == nil {
		// A stale negotiation would put the reloaded game back into scoring.
		if _, err := tx.Exec(ctx, `DELETE FROM game_scoring WHERE game_id = $1`, id); err != nil {
			return fmt.Errorf("store: clearing scoring: %w", err)
		}
	}
	if err := updateGameRow(ctx, tx, id, session, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordEvent appends to the audit log that answers "why did this game end like
// that?" when a player complains (docs/05 §9).
func (g *Games) RecordEvent(ctx context.Context, id string, moveNo int, kind string,
	actorUserID string, payload any) error {
	encoded := []byte("{}")
	if payload != nil {
		var err error
		if encoded, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	_, err := g.pool.Exec(ctx, `
		INSERT INTO game_events (game_id, move_no, actor_user_id, kind, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		id, moveNo, nullable(actorUserID), kind, encoded)
	if err != nil {
		return fmt.Errorf("store: recording event: %w", err)
	}
	return nil
}

// ── encoding helpers ────────────────────────────────────────────────────────

// Points are stored as a single smallint, row-major. Two columns would cost more
// space and index for no query anyone needs (docs/05 §2).
func encodePoints(points []rules.Point, size int) []int16 {
	out := make([]int16, 0, len(points))
	for _, p := range points {
		out = append(out, int16(p.Row*size+p.Col))
	}
	return out
}

func decodePoints(encoded []int16, size int) []rules.Point {
	out := make([]rules.Point, 0, len(encoded))
	for _, v := range encoded {
		out = append(out, rules.Point{Col: int(v) % size, Row: int(v) / size})
	}
	return out
}

func encodePoint(move rules.Move, size int) (*int16, error) {
	if move.Kind != rules.KindPlay {
		return nil, nil
	}
	encoded := int16(move.Point.Row*size + move.Point.Col)
	return &encoded, nil
}

func decodeMove(kind string, point *int16, size int) (rules.Move, error) {
	switch kind {
	case "pass":
		return rules.Pass, nil
	case "resign":
		return rules.Resign, nil
	case "play":
		if point == nil {
			return rules.Move{}, errors.New("a play with no point")
		}
		return rules.Play(rules.Point{Col: int(*point) % size, Row: int(*point) / size}), nil
	default:
		return rules.Move{}, fmt.Errorf("unknown move kind %q", kind)
	}
}

func moveKind(move rules.Move) string {
	switch move.Kind {
	case rules.KindPass:
		return "pass"
	case rules.KindResign:
		return "resign"
	default:
		return "play"
	}
}

func colourFromString(name string) rules.Color {
	if name == "white" {
		return rules.White
	}
	return rules.Black
}

func komiOf(config game.Config) float64 {
	if config.Komi != 0 {
		return config.Komi
	}
	return rules.DefaultKomi(config.Rules, config.Handicap)
}

func suggestedOf(session *game.ScoringSession) []rules.Point {
	out := make([]rules.Point, 0, len(session.Suggested))
	for p := range session.Suggested {
		out = append(out, p)
	}
	return out
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// ErrNoSeat means both colours are already taken.
var ErrNoSeat = errors.New("store: the game is full")

// ClaimSeat gives a connecting player whichever colour is still free. One
// statement, so two players connecting at the same instant cannot both take the
// same seat -- the row lock decides, not the application.
//
// A player already holding a seat is refused the other one: a game needs two
// people, and letting one hold both would make every result meaningless.
func (g *Games) ClaimSeat(ctx context.Context, gameID, userID string) (rules.Color, error) {
	var seat string
	err := g.pool.QueryRow(ctx, `
		UPDATE games SET
			black_user_id = CASE WHEN black_user_id IS NULL THEN $2 ELSE black_user_id END,
			white_user_id = CASE WHEN black_user_id IS NOT NULL AND white_user_id IS NULL
			                     THEN $2 ELSE white_user_id END
		WHERE id = $1
		  AND (black_user_id IS NULL OR white_user_id IS NULL)
		  AND black_user_id IS DISTINCT FROM $2
		  AND white_user_id IS DISTINCT FROM $2
		RETURNING CASE WHEN black_user_id = $2 THEN 'black' ELSE 'white' END`,
		gameID, userID).Scan(&seat)
	if errors.Is(err, pgx.ErrNoRows) {
		return rules.Empty, ErrNoSeat
	}
	if err != nil {
		return rules.Empty, fmt.Errorf("store: claiming a seat: %w", err)
	}
	return colourFromString(seat), nil
}

// GameSummary is one row of a player's game list -- enough to draw the home
// screen without loading any moves.
type GameSummary struct {
	ID           string
	BoardSize    int
	Rules        rules.RuleSet
	Phase        rules.Phase
	ToPlay       rules.Color
	MyColor      rules.Color
	OpponentID   string
	OpponentName string
	MoveNumber   int
	MoveDeadline *time.Time
	LastActivity time.Time
	Result       *rules.Result
}

// ListForUser returns a player's games, most recently active first. Active games
// (playing, scoring) come before finished ones so the home screen can lead with
// "your move" without a second query.
func (g *Games) ListForUser(ctx context.Context, userID string, limit int) ([]GameSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := g.pool.Query(ctx, `
		SELECT g.id, g.board_size, g.rules, g.phase, g.to_play, g.current_move_no,
		       g.move_deadline, g.last_activity_at, g.result,
		       g.black_user_id, g.white_user_id,
		       coalesce(b.display_name, ''), coalesce(w.display_name, '')
		  FROM games g
		  LEFT JOIN users b ON b.id = g.black_user_id
		  LEFT JOIN users w ON w.id = g.white_user_id
		 WHERE g.black_user_id = $1 OR g.white_user_id = $1
		 ORDER BY (g.phase IN ('playing', 'scoring')) DESC, g.last_activity_at DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing games: %w", err)
	}
	defer rows.Close()

	var out []GameSummary
	for rows.Next() {
		var (
			summary              GameSummary
			ruleSet, phase       string
			toPlay               *string
			resultJSON           []byte
			black, white         *string
			blackName, whiteName string
		)
		if err := rows.Scan(&summary.ID, &summary.BoardSize, &ruleSet, &phase, &toPlay,
			&summary.MoveNumber, &summary.MoveDeadline, &summary.LastActivity, &resultJSON,
			&black, &white, &blackName, &whiteName); err != nil {
			return nil, err
		}
		summary.Rules = rules.RuleSet(ruleSet)
		summary.Phase = rules.Phase(phase)
		if toPlay != nil {
			summary.ToPlay = colourFromString(*toPlay)
		}
		if deref(black) == userID {
			summary.MyColor, summary.OpponentID, summary.OpponentName = rules.Black, deref(white), whiteName
		} else {
			summary.MyColor, summary.OpponentID, summary.OpponentName = rules.White, deref(black), blackName
		}
		if len(resultJSON) > 0 {
			summary.Result = &rules.Result{}
			if err := json.Unmarshal(resultJSON, summary.Result); err != nil {
				return nil, fmt.Errorf("store: decoding result: %w", err)
			}
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

// ListExpired finds games whose player on move has run out of time but that no
// node is currently running -- typically correspondence games, whose actor stops
// after a few idle minutes and takes its timer with it (docs/04 §4.4).
func (g *Games) ListExpired(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := g.pool.Query(ctx, `
		SELECT id FROM games
		 WHERE phase = 'playing' AND move_deadline IS NOT NULL AND move_deadline < $1
		 ORDER BY move_deadline LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing expired games: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Park records that a node stopped running a game on purpose. A game created
// fresh is parked too: nobody has run it yet, so nothing was interrupted.
func (g *Games) Park(ctx context.Context, id string, at time.Time) error {
	_, err := g.pool.Exec(ctx, `UPDATE games SET parked_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("store: parking game: %w", err)
	}
	return nil
}

// Unpark marks a game as running somewhere; a crash from here on is a stranding.
func (g *Games) Unpark(ctx context.Context, id string) error {
	_, err := g.pool.Exec(ctx, `UPDATE games SET parked_at = NULL WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: unparking game: %w", err)
	}
	return nil
}

// Participants is the little the notifier needs to know about a game: who plays
// and whether it is slow enough that a "your turn" push makes sense.
type Participants struct {
	BlackUserID, WhiteUserID string
	BlackName, WhiteName     string
	IsCorrespondence         bool
}

func (g *Games) Participants(ctx context.Context, id string) (Participants, error) {
	var p Participants
	var black, white *string
	err := g.pool.QueryRow(ctx, `
		SELECT g.black_user_id, g.white_user_id, g.is_correspondence,
		       coalesce(b.display_name, ''), coalesce(w.display_name, '')
		  FROM games g
		  LEFT JOIN users b ON b.id = g.black_user_id
		  LEFT JOIN users w ON w.id = g.white_user_id
		 WHERE g.id = $1`, id).Scan(&black, &white, &p.IsCorrespondence, &p.BlackName, &p.WhiteName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Participants{}, ErrNotFound
	}
	if err != nil {
		return Participants{}, fmt.Errorf("store: loading participants: %w", err)
	}
	if black != nil {
		p.BlackUserID = *black
	}
	if white != nil {
		p.WhiteUserID = *white
	}
	return p, nil
}

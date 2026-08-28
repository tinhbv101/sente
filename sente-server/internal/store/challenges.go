package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

// DefaultChallengeTTL is how long an invitation stays open (docs/01 FR-M3).
const DefaultChallengeTTL = 7 * 24 * time.Hour

var (
	// ErrChallengeGone covers every reason an invitation cannot be accepted:
	// already taken, declined, cancelled, expired, or never existed. The caller
	// must not tell them apart, or an open link becomes an oracle for guessing
	// codes (docs/08 §4.2).
	ErrChallengeGone = errors.New("store: the invitation is no longer open")
	// ErrOwnChallenge is separate because it is the creator's own mistake, not a
	// probe: they need to be told what happened.
	ErrOwnChallenge = errors.New("store: you cannot accept your own invitation")
)

type ChallengeStatus string

const (
	ChallengePending   ChallengeStatus = "pending"
	ChallengeAccepted  ChallengeStatus = "accepted"
	ChallengeDeclined  ChallengeStatus = "declined"
	ChallengeExpired   ChallengeStatus = "expired"
	ChallengeCancelled ChallengeStatus = "cancelled"
)

type Challenge struct {
	ID           string
	Code         string
	CreatorID    string
	CreatorName  string
	InviteeID    string
	Config       game.Config
	CreatorColor string
	Status       ChallengeStatus
	GameID       string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

type Challenges struct{ pool *pgxpool.Pool }

// afterAcceptLock runs between locking an invitation and creating its game. The
// race test replaces it with a sleep so every racer is inside the critical
// section at once; nothing else touches it.
var afterAcceptLock = func() {}

func NewChallenges(pool *pgxpool.Pool) *Challenges { return &Challenges{pool: pool} }

type CreateChallengeParams struct {
	CreatorID string
	InviteeID string
	Config    game.Config
	// CreatorColor is black, white, or random. Random is resolved when the
	// invitation is accepted, not when it is created, so neither side can peek.
	CreatorColor string
	TTL          time.Duration
}

func (c *Challenges) Create(ctx context.Context, params CreateChallengeParams) (Challenge, error) {
	if err := params.Config.TimeControl.Validate(); err != nil {
		return Challenge{}, err
	}
	if params.CreatorColor == "" {
		params.CreatorColor = "random"
	}
	switch params.CreatorColor {
	case "black", "white", "random":
	default:
		return Challenge{}, fmt.Errorf("store: unknown colour choice %q", params.CreatorColor)
	}
	ttl := params.TTL
	if ttl <= 0 {
		ttl = DefaultChallengeTTL
	}
	encoded, err := json.Marshal(configPayload(params.Config))
	if err != nil {
		return Challenge{}, err
	}

	challenge := Challenge{
		ID: NewID(), CreatorID: params.CreatorID, InviteeID: params.InviteeID,
		Config: params.Config, CreatorColor: params.CreatorColor,
		Status: ChallengePending, ExpiresAt: time.Now().Add(ttl),
	}
	// Retry on the vanishing chance of a code collision; the unique index decides.
	for attempt := 0; attempt < 5; attempt++ {
		challenge.Code = NewFriendCode()
		_, err := c.pool.Exec(ctx, `
			INSERT INTO challenges (id, code, creator_id, invitee_id, config,
			                        creator_color, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
			challenge.ID, challenge.Code, challenge.CreatorID, nullable(challenge.InviteeID),
			encoded, challenge.CreatorColor, challenge.ExpiresAt)
		if err == nil {
			return challenge, nil
		}
		if attempt == 4 {
			return Challenge{}, fmt.Errorf("store: creating invitation: %w", err)
		}
	}
	return Challenge{}, errors.New("store: could not allocate an invitation code")
}

// ByCode is the preview an invitee sees before signing in. Expired invitations
// read as gone rather than as pending.
func (c *Challenges) ByCode(ctx context.Context, code string) (Challenge, error) {
	var (
		challenge  Challenge
		invitee    *string
		gameID     *string
		configJSON []byte
	)
	err := c.pool.QueryRow(ctx, `
		SELECT c.id, c.code, c.creator_id, u.display_name, c.invitee_id, c.config,
		       c.creator_color, c.status, c.game_id, c.expires_at, c.created_at
		  FROM challenges c JOIN users u ON u.id = c.creator_id
		 WHERE c.code = $1`, code).
		Scan(&challenge.ID, &challenge.Code, &challenge.CreatorID, &challenge.CreatorName,
			&invitee, &configJSON, &challenge.CreatorColor, &challenge.Status,
			&gameID, &challenge.ExpiresAt, &challenge.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, ErrChallengeGone
	}
	if err != nil {
		return Challenge{}, fmt.Errorf("store: reading invitation: %w", err)
	}
	challenge.InviteeID = deref(invitee)
	challenge.GameID = deref(gameID)
	if challenge.Config, err = configFromPayload(configJSON); err != nil {
		return Challenge{}, err
	}
	if challenge.Status == ChallengePending && time.Now().After(challenge.ExpiresAt) {
		challenge.Status = ChallengeExpired
	}
	return challenge, nil
}

// Accept turns an invitation into a game, in one transaction.
//
// Two people opening the same link at the same instant is the normal case, not an
// edge case: the conditional UPDATE is what makes exactly one of them win. Doing
// this as a read-then-write would hand both of them a game.
func (c *Challenges) Accept(ctx context.Context, code, userID string) (Challenge, error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return Challenge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id, creatorID, creatorColor string
		configJSON                  []byte
	)
	// Lock the row before creating anything. Under READ COMMITTED a second racer
	// blocks here, then re-evaluates the WHERE clause and finds the invitation
	// already taken -- so exactly one of them proceeds, and no game is created for
	// the loser. Marking it accepted first would break `accepted_has_game`, which
	// PostgreSQL checks per statement and cannot defer.
	err = tx.QueryRow(ctx, `
		SELECT id, creator_id, creator_color, config
		  FROM challenges
		 WHERE code = $1
		   AND status = 'pending'
		   AND expires_at > now()
		   AND creator_id <> $2
		   AND (invitee_id IS NULL OR invitee_id = $2)
		   FOR UPDATE`, code, userID).
		Scan(&id, &creatorID, &creatorColor, &configJSON)
	if err == nil {
		// Widened critical section for the race test: without a pause here, the
		// winner commits before anyone else has even read the row, and a missing
		// lock goes unnoticed. A no-op in production.
		afterAcceptLock()
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish only the creator's own mistake; everything else is opaque.
		// Reuse the transaction's connection: asking the pool for a second one
		// while a dozen callers are blocked on the same row exhausts it, and the
		// whole thing deadlocks. A plain read never blocks on FOR UPDATE anyway.
		var creator string
		if lookupErr := tx.QueryRow(ctx,
			`SELECT creator_id FROM challenges WHERE code = $1`, code).Scan(&creator); lookupErr == nil {
			if creator == userID {
				return Challenge{}, ErrOwnChallenge
			}
		}
		return Challenge{}, ErrChallengeGone
	}
	if err != nil {
		return Challenge{}, fmt.Errorf("store: accepting invitation: %w", err)
	}

	config, err := configFromPayload(configJSON)
	if err != nil {
		return Challenge{}, err
	}
	// Random is decided here, once, where neither side can influence it.
	black, white := creatorID, userID
	switch creatorColor {
	case "white":
		black, white = userID, creatorID
	case "random":
		if rand.Intn(2) == 1 {
			black, white = userID, creatorID
		}
	}

	gameID, err := createGameTx(ctx, tx, CreateParams{
		Config: config, BlackUserID: black, WhiteUserID: white, StartedAt: time.Now(),
	})
	if err != nil {
		return Challenge{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE challenges SET status = 'accepted', game_id = $2, resolved_at = now()
		 WHERE id = $1`, id, gameID); err != nil {
		return Challenge{}, fmt.Errorf("store: closing invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Challenge{}, err
	}
	return Challenge{
		ID: id, Code: code, CreatorID: creatorID, Config: config,
		Status: ChallengeAccepted, GameID: gameID,
	}, nil
}

// Decline and Cancel both close an invitation without creating a game; they
// differ only in who is allowed to do it.
func (c *Challenges) Decline(ctx context.Context, code, userID string) error {
	return c.close(ctx, `
		UPDATE challenges SET status = 'declined', resolved_at = now()
		 WHERE code = $1 AND status = 'pending' AND creator_id <> $2
		   AND (invitee_id IS NULL OR invitee_id = $2)`, code, userID)
}

func (c *Challenges) Cancel(ctx context.Context, code, creatorID string) error {
	return c.close(ctx, `
		UPDATE challenges SET status = 'cancelled', resolved_at = now()
		 WHERE code = $1 AND status = 'pending' AND creator_id = $2`, code, creatorID)
}

func (c *Challenges) close(ctx context.Context, query, code, userID string) error {
	tag, err := c.pool.Exec(ctx, query, code, userID)
	if err != nil {
		return fmt.Errorf("store: closing invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrChallengeGone
	}
	return nil
}

// ListForUser returns the invitations someone sent and the ones sent to them.
func (c *Challenges) ListForUser(ctx context.Context, userID string) ([]Challenge, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT c.id, c.code, c.creator_id, u.display_name, c.invitee_id, c.config,
		       c.creator_color, c.status, c.game_id, c.expires_at, c.created_at
		  FROM challenges c JOIN users u ON u.id = c.creator_id
		 WHERE (c.creator_id = $1 OR c.invitee_id = $1) AND c.status = 'pending'
		   AND c.expires_at > now()
		 ORDER BY c.created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing invitations: %w", err)
	}
	defer rows.Close()

	var out []Challenge
	for rows.Next() {
		var (
			challenge       Challenge
			invitee, gameID *string
			configJSON      []byte
		)
		if err := rows.Scan(&challenge.ID, &challenge.Code, &challenge.CreatorID,
			&challenge.CreatorName, &invitee, &configJSON, &challenge.CreatorColor,
			&challenge.Status, &gameID, &challenge.ExpiresAt, &challenge.CreatedAt); err != nil {
			return nil, err
		}
		challenge.InviteeID = deref(invitee)
		challenge.GameID = deref(gameID)
		if challenge.Config, err = configFromPayload(configJSON); err != nil {
			return nil, err
		}
		out = append(out, challenge)
	}
	return out, rows.Err()
}

// ExpireStale closes invitations nobody answered. Run from a sweeper; the status
// is also corrected on read, so this is tidying rather than correctness.
func (c *Challenges) ExpireStale(ctx context.Context) (int, error) {
	tag, err := c.pool.Exec(ctx, `
		UPDATE challenges SET status = 'expired', resolved_at = now()
		 WHERE status = 'pending' AND expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("store: expiring invitations: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ── config as stored ────────────────────────────────────────────────────────

type challengeConfig struct {
	BoardSize   int              `json:"board_size"`
	Rules       string           `json:"rules"`
	Komi        float64          `json:"komi"`
	Handicap    int              `json:"handicap"`
	TimeControl game.TimeControl `json:"time_control"`
}

func configPayload(config game.Config) challengeConfig {
	return challengeConfig{
		BoardSize: config.Size, Rules: string(config.Rules), Komi: config.Komi,
		Handicap: config.Handicap, TimeControl: config.TimeControl,
	}
}

func configFromPayload(data []byte) (game.Config, error) {
	var payload challengeConfig
	if err := json.Unmarshal(data, &payload); err != nil {
		return game.Config{}, fmt.Errorf("store: decoding invitation config: %w", err)
	}
	return game.Config{
		Size: payload.BoardSize, Rules: rules.RuleSet(payload.Rules), Komi: payload.Komi,
		Handicap: payload.Handicap, TimeControl: payload.TimeControl, MaxUndos: defaultMaxUndos,
	}, nil
}

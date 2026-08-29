package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrSelfTarget      = errors.New("store: you cannot target yourself")
	ErrUnknownCategory = errors.New("store: unknown report category")
)

type Moderation struct{ pool poolLike }

// poolLike is the subset of pgxpool the moderation store needs, so the account
// deletion path can run inside a transaction.
type poolLike interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func NewModeration(pool poolLike) *Moderation { return &Moderation{pool: pool} }

var reportCategories = map[string]bool{
	"abuse": true, "cheating": true, "escaping": true, "name": true, "other": true,
}

// Report files a complaint for a human to look at. It never acts on its own:
// automatic bans on a heuristic are how you lose your strongest players
// (docs/08 §5.1).
func (m *Moderation) Report(ctx context.Context, reporter, reported, gameID, category, note string) error {
	if reporter == reported {
		return ErrSelfTarget
	}
	if !reportCategories[category] {
		return ErrUnknownCategory
	}
	note = strings.TrimSpace(note)
	if len(note) > 1000 {
		note = note[:1000]
	}
	_, err := m.pool.Exec(ctx, `
		INSERT INTO reports (id, reporter_id, reported_id, game_id, category, note)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		NewID(), reporter, reported, nullable(gameID), category, nullable(note))
	if err != nil {
		return fmt.Errorf("store: filing report: %w", err)
	}
	return nil
}

// Block hides one player from another. Games in progress are not cancelled --
// that would make blocking a way out of a lost game (docs/05 §4).
func (m *Moderation) Block(ctx context.Context, blocker, blocked string) error {
	if blocker == blocked {
		return ErrSelfTarget
	}
	_, err := m.pool.Exec(ctx, `
		INSERT INTO blocks (blocker_id, blocked_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, blocker, blocked)
	if err != nil {
		return fmt.Errorf("store: blocking: %w", err)
	}
	return nil
}

func (m *Moderation) Unblock(ctx context.Context, blocker, blocked string) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2`, blocker, blocked)
	if err != nil {
		return fmt.Errorf("store: unblocking: %w", err)
	}
	return nil
}

// DeleteAccount is the App Store's required in-app deletion (docs/05 §14): the
// person is anonymised, everything that identifies the device is removed, and
// their games stay -- a game has two players and the other one keeps their history.
func (u *Users) DeleteAccount(ctx context.Context, userID string) error {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	statements := []string{
		`DELETE FROM refresh_tokens WHERE user_id = $1`,
		`DELETE FROM blocks WHERE blocker_id = $1`,
		`UPDATE challenges SET status = 'cancelled', resolved_at = now()
		   WHERE creator_id = $1 AND status = 'pending'`,
		`UPDATE games SET black_user_id = NULL WHERE black_user_id = $1`,
		`UPDATE games SET white_user_id = NULL WHERE white_user_id = $1`,
		// The row is kept so foreign keys from reports stay valid; the person is gone.
		// The friend code is freed for reuse by turning it into a token from the
		// same alphabet, derived from the id so the update is idempotent.
		`UPDATE users SET display_name = 'Người chơi đã xóa',
		                  friend_code = translate(upper(substr(md5(id::text), 1, 8)),
		                                          '0123456789ABCDEF', 'ZYXWVUTSRQPNMKJH'),
		                  deleted_at = now(), updated_at = now()
		   WHERE id = $1`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, userID); err != nil {
			return fmt.Errorf("store: deleting account: %w", err)
		}
	}
	return tx.Commit(ctx)
}

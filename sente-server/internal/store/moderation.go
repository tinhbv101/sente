package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
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
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
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
//
// One statement, so a block can never half-apply: it also drops any friendship
// or pending request between the two, and cancels invitations aimed at each
// other. Open links (invitee_id IS NULL) are left alone; they are not aimed at
// anybody, and Accept refuses a blocked pair anyway.
func (m *Moderation) Block(ctx context.Context, blocker, blocked string) error {
	if blocker == blocked {
		return ErrSelfTarget
	}
	_, err := m.pool.Exec(ctx, `
		WITH blocked AS (
			INSERT INTO blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)
			ON CONFLICT DO NOTHING
		), unfriended AS (
			DELETE FROM friendships
			 WHERE user_low = least($1::uuid, $2::uuid)
			   AND user_high = greatest($1::uuid, $2::uuid)
			   -- A refusal outlives a block. Erasing it would hand the refused
			   -- party a way back in: block, unblock, ask again.
			   AND status <> 'declined'
		)
		UPDATE challenges SET status = 'cancelled', resolved_at = now()
		 WHERE status = 'pending' AND invitee_id IS NOT NULL
		   AND ((creator_id = $1::uuid AND invitee_id = $2::uuid)
		     OR (creator_id = $2::uuid AND invitee_id = $1::uuid))`, blocker, blocked)
	if err != nil {
		return fmt.Errorf("store: blocking: %w", err)
	}
	return nil
}

// Blocked is one row of the "people you blocked" list.
type Blocked struct {
	UserID      string
	DisplayName string
}

// ListBlocked exists so a block can be undone from the app. Without it the
// block button in the game screen is one-way, which App Store guideline 1.2
// does not allow.
func (m *Moderation) ListBlocked(ctx context.Context, blocker string) ([]Blocked, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT u.id, u.display_name FROM blocks b
		  JOIN users u ON u.id = b.blocked_id
		 WHERE b.blocker_id = $1::uuid
		 ORDER BY b.created_at DESC LIMIT 200`, blocker)
	if err != nil {
		return nil, fmt.Errorf("store: listing blocks: %w", err)
	}
	defer rows.Close()
	out := []Blocked{}
	for rows.Next() {
		var item Blocked
		if err := rows.Scan(&item.UserID, &item.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
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
//
// The freed friend code is drawn fresh rather than derived from the id: a
// derived code cannot be retried, and one collision would abort the deletion --
// failing the one path the App Store requires to work.
func (u *Users) DeleteAccount(ctx context.Context, userID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		err := u.deleteAccount(ctx, userID, NewFriendCode())
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "users_friend_code_key") {
			return err
		}
	}
	return errors.New("store: could not free the friend code")
}

func (u *Users) deleteAccount(ctx context.Context, userID, freedCode string) error {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	statements := []string{
		`DELETE FROM refresh_tokens WHERE user_id = $1`,
		// The Apple ID must be free to sign in again as a new person (docs/05 §14).
		`DELETE FROM user_identities WHERE user_id = $1`,
		`DELETE FROM devices WHERE user_id = $1`,
		`DELETE FROM blocks WHERE blocker_id = $1`,
		`UPDATE challenges SET status = 'cancelled', resolved_at = now()
		   WHERE creator_id = $1 AND status = 'pending'`,
		// The users row survives deletion, so the ON DELETE CASCADE never fires:
		// without this the friendships outlive the person.
		`DELETE FROM friendships WHERE user_low = $1 OR user_high = $1`,
		`UPDATE games SET black_user_id = NULL WHERE black_user_id = $1`,
		`UPDATE games SET white_user_id = NULL WHERE white_user_id = $1`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, userID); err != nil {
			return fmt.Errorf("store: deleting account: %w", err)
		}
	}
	// The row is kept so foreign keys from reports stay valid; the person is gone.
	if _, err := tx.Exec(ctx, `
		UPDATE users SET display_name = 'Người chơi đã xóa', friend_code = $2,
		                 deleted_at = now(), updated_at = now()
		 WHERE id = $1`, userID, freedCode); err != nil {
		return fmt.Errorf("store: deleting account: %w", err)
	}
	return tx.Commit(ctx)
}

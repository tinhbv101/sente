package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ProviderApple = "apple"

// ErrIdentityTaken: this Apple ID already belongs to a different account.
var ErrIdentityTaken = errors.New("store: identity is linked to another account")

type Identities struct{ pool *pgxpool.Pool }

func NewIdentities(pool *pgxpool.Pool) *Identities { return &Identities{pool: pool} }

// UserFor finds the account an external identity signs into, if any.
func (i *Identities) UserFor(ctx context.Context, provider, subject string) (string, error) {
	var userID string
	err := i.pool.QueryRow(ctx, `
		SELECT ui.user_id FROM user_identities ui
		  JOIN users u ON u.id = ui.user_id
		 WHERE ui.provider = $1 AND ui.provider_subject = $2 AND u.deleted_at IS NULL`,
		provider, subject).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: looking up identity: %w", err)
	}
	return userID, nil
}

// Link attaches an identity to an account and promotes it from guest. Linking
// is what turns "this phone" into "this person" (docs/01 FR-A2): the games,
// friend code and name all stay.
func (i *Identities) Link(ctx context.Context, userID, provider, subject, email string) error {
	tx, err := i.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: linking identity: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// An identity still attached to a deleted account is free: the person left
	// and came back, and must not be locked out by their own old row.
	var owner string
	var ownerDeleted bool
	err = tx.QueryRow(ctx, `
		SELECT ui.user_id, u.deleted_at IS NOT NULL
		  FROM user_identities ui JOIN users u ON u.id = ui.user_id
		 WHERE ui.provider = $1 AND ui.provider_subject = $2
		 FOR UPDATE OF ui`, provider, subject).Scan(&owner, &ownerDeleted)
	switch {
	case err == nil && owner != userID && !ownerDeleted:
		return ErrIdentityTaken
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("store: linking identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_identities (user_id, provider, provider_subject, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_subject) DO UPDATE
		   SET user_id = EXCLUDED.user_id, email = coalesce(EXCLUDED.email, user_identities.email)`,
		userID, provider, subject, nullable(email)); err != nil {
		return fmt.Errorf("store: linking identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET is_guest = FALSE, updated_at = now() WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("store: promoting user: %w", err)
	}
	return tx.Commit(ctx)
}

// Unlink is what Apple's revocation notice triggers: the sign-in is gone, the
// account falls back to guest and stays usable on the devices already signed in.
func (i *Identities) Unlink(ctx context.Context, provider, subject string) (string, error) {
	var userID string
	err := i.pool.QueryRow(ctx, `
		DELETE FROM user_identities WHERE provider = $1 AND provider_subject = $2 RETURNING user_id`,
		provider, subject).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: unlinking identity: %w", err)
	}
	if _, err := i.pool.Exec(ctx, `
		UPDATE users SET is_guest = NOT EXISTS (SELECT 1 FROM user_identities WHERE user_id = $1), updated_at = now()
		 WHERE id = $1`, userID); err != nil {
		return "", fmt.Errorf("store: demoting user: %w", err)
	}
	return userID, nil
}

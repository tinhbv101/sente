package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// RefreshTokenTTL is how long a device can stay away and still come back as
	// the same person (docs/03 ADR-008).
	RefreshTokenTTL = 30 * 24 * time.Hour
	// ReuseGrace tolerates a client retrying a refresh it never saw the answer to.
	// Reuse inside the window rotates again; reuse after it is treated as theft.
	ReuseGrace = 10 * time.Second
)

var (
	// ErrRefreshInvalid covers unknown, expired and revoked tokens alike, so the
	// endpoint cannot be used to tell them apart.
	ErrRefreshInvalid = errors.New("store: refresh token is not valid")
	// ErrRefreshReused means a rotated token was presented again after the grace
	// window. The family has been revoked; the user has to sign in again.
	ErrRefreshReused = errors.New("store: refresh token reuse detected")
)

type RefreshTokens struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRefreshTokens(pool *pgxpool.Pool) *RefreshTokens {
	return &RefreshTokens{pool: pool, now: time.Now}
}

// hashToken is what gets stored. Unsalted SHA-256 is fine here: the input is 256
// bits of randomness, so there is nothing for a rainbow table to find.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newRefreshToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("store: generating refresh token: %w", err)
	}
	return "rt_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Issue starts a new token family for a user, as at sign-up.
func (r *RefreshTokens) Issue(ctx context.Context, userID string) (string, error) {
	token, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		NewID(), userID, hashToken(token), NewID(), r.now().Add(RefreshTokenTTL))
	if err != nil {
		return "", fmt.Errorf("store: issuing refresh token: %w", err)
	}
	return token, nil
}

// Rotate exchanges a refresh token for a new one in the same family and returns
// the user it belongs to. The presented token is marked used; presenting it again
// after the grace window revokes the family (docs/08 §2.2).
func (r *RefreshTokens) Rotate(ctx context.Context, presented string) (userID, next string, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id, family string
		expiresAt  time.Time
		usedAt     *time.Time
		revokedAt  *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, family_id, expires_at, used_at, revoked_at
		  FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, hashToken(presented)).
		Scan(&id, &userID, &family, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrRefreshInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("store: looking up refresh token: %w", err)
	}
	now := r.now()
	if revokedAt != nil || now.After(expiresAt) {
		return "", "", ErrRefreshInvalid
	}
	if usedAt != nil && now.Sub(*usedAt) > ReuseGrace {
		// Someone is holding a copy of a token that was already spent. Whoever they
		// are, the whole family is burned; the legitimate user signs in again.
		if _, err := tx.Exec(ctx, `
			UPDATE refresh_tokens SET revoked_at = now()
			 WHERE family_id = $1 AND revoked_at IS NULL`, family); err != nil {
			return "", "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", "", err
		}
		return "", "", ErrRefreshReused
	}

	next, err = newRefreshToken()
	if err != nil {
		return "", "", err
	}
	if usedAt == nil {
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = now() WHERE id = $1`, id); err != nil {
			return "", "", err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		NewID(), userID, hashToken(next), family, now.Add(RefreshTokenTTL)); err != nil {
		return "", "", fmt.Errorf("store: rotating refresh token: %w", err)
	}
	return userID, next, tx.Commit(ctx)
}

// Revoke ends one token's family: sign-out from this device.
func (r *RefreshTokens) Revoke(ctx context.Context, presented string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		 WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)
		   AND revoked_at IS NULL`, hashToken(presented))
	if err != nil {
		return fmt.Errorf("store: revoking refresh token: %w", err)
	}
	return nil
}

// RevokeAllForUser ends every session: account deletion, or a ban.
func (r *RefreshTokens) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("store: revoking user tokens: %w", err)
	}
	return nil
}

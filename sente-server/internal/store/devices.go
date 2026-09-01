package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Devices struct{ pool *pgxpool.Pool }

func NewDevices(pool *pgxpool.Pool) *Devices { return &Devices{pool: pool} }

type Device struct {
	Token       string
	Environment string
}

// Register records a token for a user. The same token moving to another user --
// a phone handed to a friend who signs in as someone else -- follows the person
// now holding it.
func (d *Devices) Register(ctx context.Context, userID, token, environment, appVersion string) error {
	if environment != "sandbox" && environment != "production" {
		return fmt.Errorf("store: unknown push environment %q", environment)
	}
	_, err := d.pool.Exec(ctx, `
		INSERT INTO devices (id, user_id, apns_token, bundle_env, app_version)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (apns_token, bundle_env) DO UPDATE
		   SET user_id = EXCLUDED.user_id, app_version = EXCLUDED.app_version, last_seen_at = now()`,
		NewID(), userID, token, environment, nullable(appVersion))
	if err != nil {
		return fmt.Errorf("store: registering device: %w", err)
	}
	return nil
}

// Unregister drops a token APNs reported dead, whoever it belonged to.
func (d *Devices) Unregister(ctx context.Context, token string) error {
	if _, err := d.pool.Exec(ctx, `DELETE FROM devices WHERE apns_token = $1`, token); err != nil {
		return fmt.Errorf("store: unregistering device: %w", err)
	}
	return nil
}

// UnregisterForUser is the user's own opt-out; scoped so nobody can unregister
// someone else's phone by guessing a token.
func (d *Devices) UnregisterForUser(ctx context.Context, userID, token string) error {
	if _, err := d.pool.Exec(ctx, `DELETE FROM devices WHERE apns_token = $1 AND user_id = $2`, token, userID); err != nil {
		return fmt.Errorf("store: unregistering device: %w", err)
	}
	return nil
}

// ForUser lists the tokens to notify, filtered by the preference for one kind of
// notification ("turn", "low_time", "game_end", "invite").
func (d *Devices) ForUser(ctx context.Context, userID, kind string) ([]Device, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT apns_token, bundle_env FROM devices
		 WHERE user_id = $1 AND coalesce((push_prefs ->> $2)::boolean, true)`, userID, kind)
	if err != nil {
		return nil, fmt.Errorf("store: listing devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var device Device
		if err := rows.Scan(&device.Token, &device.Environment); err != nil {
			return nil, err
		}
		out = append(out, device)
	}
	return out, rows.Err()
}

// SetPrefs merges a partial preference patch ({"turn": false, …}) into the
// device's push_prefs and returns the result. Scoped to the owner.
func (d *Devices) SetPrefs(ctx context.Context, userID, token string, patch map[string]bool) (map[string]bool, error) {
	encoded, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	var merged []byte
	err = d.pool.QueryRow(ctx, `
		UPDATE devices SET push_prefs = push_prefs || $3::jsonb, last_seen_at = now()
		 WHERE apns_token = $1 AND user_id = $2
		 RETURNING push_prefs`, token, userID, encoded).Scan(&merged)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: updating push prefs: %w", err)
	}
	var prefs map[string]bool
	if err := json.Unmarshal(merged, &prefs); err != nil {
		return nil, err
	}
	return prefs, nil
}

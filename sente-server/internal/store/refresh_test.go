package store

import (
	"context"
	"testing"
	"time"
)

func newRefresh(t *testing.T) *RefreshTokens {
	t.Helper()
	skipIfShort(t)
	return NewRefreshTokens(testPool)
}

func TestIssueAndRotate(t *testing.T) {
	tokens := newRefresh(t)
	ctx := context.Background()
	user := seedUser(t)

	first, err := tokens.Issue(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 40 {
		t.Fatalf("token too short to be 256 bits: %q", first)
	}
	owner, second, err := tokens.Rotate(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if owner != user {
		t.Errorf("want the issuing user, got %s", owner)
	}
	if second == first {
		t.Error("rotation must hand out a different token")
	}
	// The new token works; the old one is spent.
	if _, _, err := tokens.Rotate(ctx, second); err != nil {
		t.Errorf("the rotated token must be usable: %v", err)
	}
}

// A client that retried a refresh whose answer it never saw is not an attacker.
func TestReuseInsideTheGraceWindowRotatesAgain(t *testing.T) {
	tokens := newRefresh(t)
	ctx := context.Background()
	first, _ := tokens.Issue(ctx, seedUser(t))

	_, _, err := tokens.Rotate(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := tokens.Rotate(ctx, first)
	if err != nil {
		t.Fatalf("reuse within the grace window should be tolerated: %v", err)
	}
	if again == "" {
		t.Error("expected a fresh token")
	}
}

// After the window, reuse means a copy exists somewhere. Burn the family.
func TestReuseAfterTheGraceWindowRevokesTheFamily(t *testing.T) {
	tokens := newRefresh(t)
	ctx := context.Background()
	first, _ := tokens.Issue(ctx, seedUser(t))
	_, second, err := tokens.Rotate(ctx, first)
	if err != nil {
		t.Fatal(err)
	}

	tokens.now = func() time.Time { return time.Now().Add(ReuseGrace + time.Second) }
	if _, _, err := tokens.Rotate(ctx, first); err != ErrRefreshReused {
		t.Fatalf("want ErrRefreshReused, got %v", err)
	}
	// The legitimate successor is dead too: the user must sign in again.
	if _, _, err := tokens.Rotate(ctx, second); err != ErrRefreshInvalid {
		t.Errorf("the whole family must be revoked, got %v", err)
	}
}

func TestExpiredUnknownAndRevokedLookTheSame(t *testing.T) {
	tokens := newRefresh(t)
	ctx := context.Background()
	user := seedUser(t)

	if _, _, err := tokens.Rotate(ctx, "rt_never-issued"); err != ErrRefreshInvalid {
		t.Errorf("unknown: want ErrRefreshInvalid, got %v", err)
	}

	expired, _ := tokens.Issue(ctx, user)
	tokens.now = func() time.Time { return time.Now().Add(RefreshTokenTTL + time.Hour) }
	if _, _, err := tokens.Rotate(ctx, expired); err != ErrRefreshInvalid {
		t.Errorf("expired: want ErrRefreshInvalid, got %v", err)
	}
	tokens.now = time.Now

	revoked, _ := tokens.Issue(ctx, user)
	if err := tokens.Revoke(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tokens.Rotate(ctx, revoked); err != ErrRefreshInvalid {
		t.Errorf("revoked: want ErrRefreshInvalid, got %v", err)
	}
}

func TestRevokingAllEndsEverySession(t *testing.T) {
	tokens := newRefresh(t)
	ctx := context.Background()
	user := seedUser(t)
	phone, _ := tokens.Issue(ctx, user)
	tablet, _ := tokens.Issue(ctx, user)

	if err := tokens.RevokeAllForUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"phone": phone, "tablet": tablet} {
		if _, _, err := tokens.Rotate(ctx, token); err != ErrRefreshInvalid {
			t.Errorf("%s should be signed out, got %v", name, err)
		}
	}
}

func TestOnlyAHashIsStored(t *testing.T) {
	tokens := newRefresh(t)
	ctx := context.Background()
	token, _ := tokens.Issue(ctx, seedUser(t))
	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE encode(token_hash, 'escape') = $1`, token).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("the plaintext token must never be in the database")
	}
}

func TestRefreshDatabaseFailuresArePropagated(t *testing.T) {
	tokens := newRefresh(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tokens.Issue(cancelled, "u"); err == nil {
		t.Error("Issue must report a failed write")
	}
	if _, _, err := tokens.Rotate(cancelled, "rt_x"); err == nil {
		t.Error("Rotate must report a failed read")
	}
	if err := tokens.Revoke(cancelled, "rt_x"); err == nil {
		t.Error("Revoke must report a failed write")
	}
	if err := tokens.RevokeAllForUser(cancelled, "u"); err == nil {
		t.Error("RevokeAllForUser must report a failed write")
	}
}

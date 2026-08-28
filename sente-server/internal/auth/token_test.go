package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	issuer, err := NewIssuer(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, expiry, err := issuer.Issue("user-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expiry) > AccessTokenTTL+time.Second {
		t.Errorf("token outlives the configured lifetime: %v", expiry)
	}
	claims, err := issuer.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "user-1" || !claims.Guest {
		t.Errorf("claims lost: %+v", claims)
	}
}

func TestAShortSecretIsRefused(t *testing.T) {
	if _, err := NewIssuer("too short"); err == nil {
		t.Error("a weak signing secret must be refused, not quietly accepted")
	}
}

func TestATokenFromAnotherIssuerIsRejected(t *testing.T) {
	mine, _ := NewIssuer(testSecret)
	theirs, _ := NewIssuer(strings.Repeat("z", 32))
	token, _, err := theirs.Issue("user-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mine.Verify(token); err == nil {
		t.Error("a token signed with another secret must not verify")
	}
}

// Without an explicit method check, a token claiming "alg":"none" verifies
// against an empty signature. This is the classic JWT hole.
func TestAnUnsignedTokenIsRejected(t *testing.T) {
	issuer, _ := NewIssuer(testSecret)
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{UserID: "attacker"})
	token, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(token); err == nil {
		t.Error("an unsigned token must never be accepted")
	}
}

func TestAnExpiredTokenIsRejected(t *testing.T) {
	issuer, _ := NewIssuer(testSecret)
	issuer.now = func() time.Time { return time.Now().Add(-2 * AccessTokenTTL) }
	token, _, err := issuer.Issue("user-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(token); err == nil {
		t.Error("an expired token must be rejected")
	}
}

func TestGarbageIsRejected(t *testing.T) {
	issuer, _ := NewIssuer(testSecret)
	for _, token := range []string{"", "not.a.token", "a.b.c"} {
		if _, err := issuer.Verify(token); err == nil {
			t.Errorf("%q should not verify", token)
		}
	}
}

// Package auth issues and verifies the tokens that identify a player.
//
// Partial: guest accounts and short-lived access tokens only. Sign in with Apple
// and rotating refresh tokens (docs/03 ADR-008) are not built yet, so an access
// token expiring means signing in as a guest again.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTokenTTL is deliberately short so that banning a player, or deleting an
// account, takes effect quickly (docs/03 ADR-008).
const AccessTokenTTL = 15 * time.Minute

var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrNoSecret     = errors.New("auth: signing secret is not configured")
)

type Claims struct {
	UserID string `json:"sub"`
	Guest  bool   `json:"gst,omitempty"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret []byte
	now    func() time.Time
}

// NewIssuer refuses a short secret rather than pretending to be secure.
func NewIssuer(secret string) (*Issuer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("auth: signing secret must be at least 32 bytes, got %d", len(secret))
	}
	return &Issuer{secret: []byte(secret), now: time.Now}, nil
}

func (i *Issuer) Issue(userID string, guest bool) (string, time.Time, error) {
	expiry := i.now().Add(AccessTokenTTL)
	claims := Claims{
		UserID: userID,
		Guest:  guest,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(i.now()),
			ExpiresAt: jwt.NewNumericDate(expiry),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiry, nil
}

// Verify rejects anything that is not an HS256 token signed by this issuer. The
// method check matters: without it, a token claiming "alg":"none" would pass.
func (i *Issuer) Verify(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method %v", t.Header["alg"])
		}
		return i.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, errors.Join(ErrInvalidToken, err)
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid || claims.UserID == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

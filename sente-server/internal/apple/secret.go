package apple

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ClientSecret is the JWT Apple wants as client_secret when the server talks to
// its token endpoint: to validate an authorization code or to be told about a
// revoked sign-in. Apple caps its lifetime at six months; a day is plenty.
func ClientSecret(teamID, keyID, clientID string, key *ecdsa.PrivateKey, now time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer:    teamID,
		Subject:   clientID,
		Audience:  jwt.ClaimStrings{Issuer},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
	})
	token.Header["kid"] = keyID
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("apple: signing client secret: %w", err)
	}
	return signed, nil
}

// ParseKey reads the Sign in with Apple .p8 key.
func ParseKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("apple: key file is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple: parsing key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apple: key is not an EC key")
	}
	return key, nil
}

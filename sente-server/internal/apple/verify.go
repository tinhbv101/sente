// Package apple verifies Sign in with Apple identity tokens (docs/03 ADR-008).
// The app hands the server the identity token Apple issued; the server checks
// its signature against Apple's published keys and never trusts the client's
// word for who signed in.
package apple

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	Issuer  = "https://appleid.apple.com"
	JWKSURL = "https://appleid.apple.com/auth/keys"
	// Apple rotates keys rarely; a day of caching keeps sign-in off Apple's servers.
	keysTTL = 24 * time.Hour
)

var (
	ErrInvalidToken = errors.New("apple: identity token is not valid")
	ErrNonce        = errors.New("apple: nonce does not match")
	ErrUnknownKey   = errors.New("apple: token signed by an unknown key")
)

// Identity is what the server keeps from a verified token. Email is only
// present the first time a person signs in; later tokens omit it.
type Identity struct {
	Subject        string
	Email          string
	EmailVerified  bool
	IsPrivateEmail bool
}

type Verifier struct {
	jwksURL  string
	audience string
	http     *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewVerifier accepts tokens minted for `audience`, the app's bundle id.
func NewVerifier(jwksURL, audience string) *Verifier {
	return &Verifier{
		jwksURL: jwksURL, audience: audience,
		http: &http.Client{Timeout: 10 * time.Second},
		now:  time.Now,
	}
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) fetchKeys(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	response, err := v.http.Do(request)
	if err != nil {
		return fmt.Errorf("apple: fetching keys: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("apple: fetching keys: status %d", response.StatusCode)
	}
	var body struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return fmt.Errorf("apple: decoding keys: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(body.Keys))
	for _, key := range body.Keys {
		if key.Kty != "RSA" {
			continue
		}
		public, err := rsaFromJWK(key)
		if err != nil {
			return err
		}
		keys[key.Kid] = public
	}
	v.keys, v.fetchedAt = keys, v.now()
	return nil
}

func rsaFromJWK(key jwk) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("apple: key %s modulus: %w", key.Kid, err)
	}
	e, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("apple: key %s exponent: %w", key.Kid, err)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
}

// keyFor refetches once on an unknown kid, which is how a key rotation looks
// from here; a second miss means the token was not signed by Apple.
func (v *Verifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	stale := v.keys == nil || v.now().Sub(v.fetchedAt) > keysTTL
	if key, ok := v.keys[kid]; ok && !stale {
		return key, nil
	}
	if err := v.fetchKeys(ctx); err != nil {
		return nil, err
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, ErrUnknownKey
}

type claims struct {
	jwt.RegisteredClaims
	Nonce          string `json:"nonce"`
	Email          string `json:"email"`
	EmailVerified  any    `json:"email_verified"`
	IsPrivateEmail any    `json:"is_private_email"`
}

// Verify checks signature, issuer, audience, expiry and nonce. `nonce` is the
// raw value the app generated; Apple stores its SHA-256 in the token, though
// the raw value is also accepted in case the app hashed it itself.
func (v *Verifier) Verify(ctx context.Context, identityToken, nonce string) (Identity, error) {
	var parsed claims
	token, err := jwt.ParseWithClaims(identityToken, &parsed, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, ErrUnknownKey
		}
		return v.keyFor(ctx, kid)
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return v.now() }),
	)
	if err != nil {
		if errors.Is(err, ErrUnknownKey) {
			return Identity{}, err
		}
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid || parsed.Subject == "" {
		return Identity{}, ErrInvalidToken
	}
	if !nonceMatches(parsed.Nonce, nonce) {
		return Identity{}, ErrNonce
	}
	return Identity{
		Subject:        parsed.Subject,
		Email:          parsed.Email,
		EmailVerified:  truthy(parsed.EmailVerified),
		IsPrivateEmail: truthy(parsed.IsPrivateEmail),
	}, nil
}

func nonceMatches(claim, raw string) bool {
	if claim == "" || raw == "" {
		return claim == raw
	}
	sum := sha256.Sum256([]byte(raw))
	return claim == raw || claim == hex.EncodeToString(sum[:])
}

// Apple encodes these booleans as either true or the string "true".
func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// Event is one of Apple's server-to-server notices: a person revoked the app's
// access or deleted their Apple ID (docs/08 §2.4).
type Event struct {
	Type    string
	Subject string
}

const (
	EventConsentRevoked = "consent-revoked"
	EventAccountDeleted = "account-delete"
)

type notificationClaims struct {
	jwt.RegisteredClaims
	// Apple nests the event as a JSON string, not an object.
	Events string `json:"events"`
}

// VerifyNotification checks a notice's signature and unwraps its event. Apple
// signs these with the same keys as identity tokens.
func (v *Verifier) VerifyNotification(ctx context.Context, token string) (Event, error) {
	var parsed notificationClaims
	_, err := jwt.ParseWithClaims(token, &parsed, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, ErrUnknownKey
		}
		return v.keyFor(ctx, kid)
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(v.audience),
		jwt.WithTimeFunc(func() time.Time { return v.now() }),
	)
	if err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	var event struct {
		Type string `json:"type"`
		Sub  string `json:"sub"`
	}
	if err := json.Unmarshal([]byte(parsed.Events), &event); err != nil || event.Sub == "" {
		return Event{}, fmt.Errorf("%w: malformed events claim", ErrInvalidToken)
	}
	return Event{Type: event.Type, Subject: event.Sub}, nil
}

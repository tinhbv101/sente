package apple

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Apple's servers are stood in for by a fake JWKS endpoint and tokens signed
// with our own RSA keys; the verification logic is identical.

type fakeApple struct {
	keys   map[string]*rsa.PrivateKey
	server *httptest.Server
	hits   atomic.Int32
}

func newFakeApple(t *testing.T, kids ...string) *fakeApple {
	t.Helper()
	f := &fakeApple{keys: map[string]*rsa.PrivateKey{}}
	for _, kid := range kids {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		f.keys[kid] = key
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		var keys []map[string]string
		for kid, key := range f.keys {
			keys = append(keys, map[string]string{
				"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(f.server.Close)
	return f
}

type tokenOpts struct {
	kid, iss, aud, sub, nonce string
	exp                       time.Time
	email                     string
	emailVerified             any
}

func (f *fakeApple) token(t *testing.T, o tokenOpts) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": o.iss, "aud": o.aud, "sub": o.sub, "iat": time.Now().Add(-time.Minute).Unix(), "exp": o.exp.Unix()}
	if o.nonce != "" {
		claims["nonce"] = o.nonce
	}
	if o.email != "" {
		claims["email"] = o.email
		claims["email_verified"] = o.emailVerified
		claims["is_private_email"] = "true"
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = o.kid
	signed, err := token.SignedString(f.keys[o.kid])
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func hashed(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func good(f *fakeApple, t *testing.T) string {
	return f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "001234.abc", nonce: hashed("n0nce"),
		exp: time.Now().Add(5 * time.Minute), email: "x@privaterelay.appleid.com", emailVerified: "true"})
}

func TestAValidTokenYieldsTheIdentity(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	id, err := v.Verify(context.Background(), good(f, t), "n0nce")
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "001234.abc" || id.Email != "x@privaterelay.appleid.com" || !id.EmailVerified || !id.IsPrivateEmail {
		t.Errorf("identity: %+v", id)
	}
}

func TestRawNonceIsAcceptedWhenAppleStoredItUnhashed(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	token := f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: "plain", exp: time.Now().Add(time.Minute)})
	if _, err := v.Verify(context.Background(), token, "plain"); err != nil {
		t.Error(err)
	}
}

func TestBooleanClaimsMayBeRealBooleans(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	token := f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"),
		exp: time.Now().Add(time.Minute), email: "a@b.c", emailVerified: true})
	id, err := v.Verify(context.Background(), token, "n")
	if err != nil || !id.EmailVerified {
		t.Errorf("want verified email, got %+v %v", id, err)
	}
}

func TestEveryForgeryIsRefused(t *testing.T) {
	f := newFakeApple(t, "k1")
	other := newFakeApple(t, "k1") // same kid, different key: a signature that will not check out
	v := NewVerifier(f.server.URL, "app.sente.go")
	future := time.Now().Add(5 * time.Minute)

	cases := map[string]struct {
		token string
		nonce string
		want  error
	}{
		"wrong audience":         {f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "com.other.app", sub: "s", nonce: hashed("n"), exp: future}), "n", ErrInvalidToken},
		"wrong issuer":           {f.token(t, tokenOpts{kid: "k1", iss: "https://evil.example", aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: future}), "n", ErrInvalidToken},
		"expired":                {f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: time.Now().Add(-time.Minute)}), "n", ErrInvalidToken},
		"no subject":             {f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", nonce: hashed("n"), exp: future}), "n", ErrInvalidToken},
		"wrong nonce":            {f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: future}), "other", ErrNonce},
		"replayed without nonce": {f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", exp: future}), "n", ErrNonce},
		"unknown key":            {f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: future}), "n", ErrInvalidToken},
		"garbage":                {"not.a.jwt", "n", ErrInvalidToken},
	}
	cases["unknown key"] = struct {
		token string
		nonce string
		want  error
	}{other.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: future}), "n", ErrInvalidToken}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), c.token, c.nonce); !errors.Is(err, c.want) {
				t.Errorf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestNoneAlgorithmIsRefused(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"iss": Issuer, "aud": "app.sente.go", "sub": "s", "exp": time.Now().Add(time.Minute).Unix()})
	token.Header["kid"] = "k1"
	signed, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := v.Verify(context.Background(), signed, ""); err == nil {
		t.Error("alg=none must never verify")
	}
}

func TestKeysAreCachedAndRefetchedOnRotation(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	for range 3 {
		if _, err := v.Verify(context.Background(), good(f, t), "n0nce"); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.hits.Load(); got != 1 {
		t.Errorf("keys should be fetched once and cached, fetched %d times", got)
	}

	// Apple rotates: a new kid appears. One refetch, then it verifies.
	rotated, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.keys["k2"] = rotated
	token := f.token(t, tokenOpts{kid: "k2", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: time.Now().Add(time.Minute)})
	if _, err := v.Verify(context.Background(), token, "n"); err != nil {
		t.Fatal(err)
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("a new kid should trigger exactly one refetch, got %d fetches", got)
	}

	// A kid Apple never published stays unknown -- and costs one more fetch, not a loop.
	bogus := f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: time.Now().Add(time.Minute)})
	unknownKid, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": Issuer, "aud": "app.sente.go", "sub": "s", "exp": time.Now().Add(time.Minute).Unix()}).SignedString(f.keys["k1"])
	_ = bogus
	if _, err := v.Verify(context.Background(), unknownKid, ""); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("token without kid: want ErrUnknownKey, got %v", err)
	}
}

func TestCacheExpiresAfterADay(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	base := time.Now()
	v.now = func() time.Time { return base }
	if _, err := v.Verify(context.Background(), good(f, t), "n0nce"); err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return base.Add(2 * time.Minute) }
	token := f.token(t, tokenOpts{kid: "k1", iss: Issuer, aud: "app.sente.go", sub: "s", nonce: hashed("n"), exp: base.Add(48 * time.Hour)})
	v.now = func() time.Time { return base.Add(25 * time.Hour) }
	if _, err := v.Verify(context.Background(), token, "n"); err != nil {
		t.Fatal(err)
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("stale cache should be refreshed, got %d fetches", got)
	}
}

func TestJWKSOutageIsAnError(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	f := newFakeApple(t, "k1")
	v := NewVerifier(down.URL, "app.sente.go")
	if _, err := v.Verify(context.Background(), good(f, t), "n0nce"); err == nil {
		t.Error("cannot verify anything while keys are unavailable")
	}
}

func TestClientSecretIsAnES256JWTForApple(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	parsed, err := ParseKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	secret, err := ClientSecret("TEAM123456", "KEY1234567", "app.sente.go", parsed, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Parse(secret, func(token *jwt.Token) (any, error) {
		if token.Header["kid"] != "KEY1234567" {
			return nil, errors.New("wrong kid")
		}
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer("TEAM123456"), jwt.WithSubject("app.sente.go"), jwt.WithAudience(Issuer))
	if err != nil || !token.Valid {
		t.Fatalf("client secret does not verify: %v", err)
	}
	exp, _ := token.Claims.GetExpirationTime()
	if exp.Time.Sub(now) > 24*time.Hour+time.Second {
		t.Error("client secret must not outlive a day")
	}
	if _, err := ParseKey([]byte("junk")); err == nil {
		t.Error("junk must not parse")
	}
}

func TestNotificationsAreUnwrappedAndChecked(t *testing.T) {
	f := newFakeApple(t, "k1")
	v := NewVerifier(f.server.URL, "app.sente.go")
	events := `{"type":"consent-revoked","sub":"001234.abc","event_time":1700000000000}`
	claims := jwt.MapClaims{"iss": Issuer, "aud": "app.sente.go", "iat": time.Now().Unix(), "jti": "x", "events": events}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "k1"
	signed, _ := token.SignedString(f.keys["k1"])

	event, err := v.VerifyNotification(context.Background(), signed)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventConsentRevoked || event.Subject != "001234.abc" {
		t.Errorf("event: %+v", event)
	}

	other := newFakeApple(t, "k1")
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(other.keys["k1"])
	if _, err := v.VerifyNotification(context.Background(), forged); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("forged notice: want ErrInvalidToken, got %v", err)
	}
	claims["events"] = "not json"
	garbled := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	garbled.Header["kid"] = "k1"
	signedGarbled, _ := garbled.SignedString(f.keys["k1"])
	if _, err := v.VerifyNotification(context.Background(), signedGarbled); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("garbled events: want ErrInvalidToken, got %v", err)
	}
}

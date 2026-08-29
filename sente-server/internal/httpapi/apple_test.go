package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"sente.app/server/internal/apple"
)

// Apple is replaced by a JWKS endpoint of our own and tokens signed with its key.

type fakeApple struct {
	key *rsa.PrivateKey
	url string
}

func newFakeApple(t *testing.T) *fakeApple {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(server.Close)
	return &fakeApple{key: key, url: server.URL}
}

func (f *fakeApple) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "k1"
	signed, err := token.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// identityToken is what the app receives from Apple after the sheet: the nonce
// the app generated, hashed, travels inside it.
func (f *fakeApple) identityToken(t *testing.T, subject, rawNonce string) string {
	sum := sha256.Sum256([]byte(rawNonce))
	return f.sign(t, jwt.MapClaims{
		"iss": apple.Issuer, "aud": "app.sente.go", "sub": subject, "nonce": hex.EncodeToString(sum[:]),
		"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		"email": subject + "@privaterelay.appleid.com", "email_verified": "true",
	})
}

func (f *fakeApple) notification(t *testing.T, kind, subject string) string {
	events, _ := json.Marshal(map[string]any{"type": kind, "sub": subject, "event_time": time.Now().UnixMilli()})
	return f.sign(t, jwt.MapClaims{
		"iss": apple.Issuer, "aud": "app.sente.go", "iat": time.Now().Unix(), "jti": subject, "events": string(events),
	})
}

func withApple(f *fakeApple) func(*Config) {
	return func(c *Config) { c.Apple = apple.NewVerifier(f.url, "app.sente.go") }
}

type signInResponse struct {
	User struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		IsGuest     bool   `json:"is_guest"`
	} `json:"user"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func appleSignIn(t *testing.T, server *httptest.Server, bearer, identityToken, nonce, name string) (int, signInResponse) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"identity_token": identityToken, "nonce": nonce, "full_name": name})
	response, raw := do(t, server, http.MethodPost, "/v1/auth/apple", bearer, string(body))
	var parsed signInResponse
	_ = json.Unmarshal(raw, &parsed)
	return response.StatusCode, parsed
}

func meOf(t *testing.T, server *httptest.Server, token string) (id string, guest bool) {
	t.Helper()
	response, raw := do(t, server, http.MethodGet, "/v1/me", token, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/v1/me: %d %s", response.StatusCode, raw)
	}
	var me struct {
		ID      string `json:"id"`
		IsGuest bool   `json:"is_guest"`
	}
	_ = json.Unmarshal(raw, &me)
	return me.ID, me.IsGuest
}

func TestSignInWithAppleOpensLinksAndReturnsAccounts(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))

	// A stranger: a brand-new account, not a guest, named after Apple's answer.
	status, first := appleSignIn(t, server, "", f.identityToken(t, "sub-1", "n1"), "n1", "Nguyễn Văn An")
	if status != http.StatusOK {
		t.Fatalf("first sign-in: %d", status)
	}
	if first.User.IsGuest || first.User.DisplayName != "Nguyễn Văn An" || first.RefreshToken == "" {
		t.Errorf("first: %+v", first)
	}
	if id, guest := meOf(t, server, first.AccessToken); id != first.User.ID || guest {
		t.Errorf("token should open the new account, got %s guest=%v", id, guest)
	}

	// The same Apple ID from another phone: the same account.
	status, again := appleSignIn(t, server, "", f.identityToken(t, "sub-1", "n2"), "n2", "")
	if status != http.StatusOK || again.User.ID != first.User.ID {
		t.Errorf("returning sign-in: %d %+v", status, again)
	}

	// A guest linking: keeps its id, stops being a guest.
	guest := signUp(t, server)
	status, linked := appleSignIn(t, server, guest.token, f.identityToken(t, "sub-2", "n3"), "n3", "")
	if status != http.StatusOK || linked.User.ID != guest.userID || linked.User.IsGuest {
		t.Fatalf("linking: %d %+v (guest %s)", status, linked, guest.userID)
	}
	if _, isGuest := meOf(t, server, guest.token); isGuest {
		t.Error("the old token should now see a linked account")
	}

	// A guest whose Apple ID already opens another account gets that account back.
	other := signUp(t, server)
	status, recovered := appleSignIn(t, server, other.token, f.identityToken(t, "sub-1", "n4"), "n4", "")
	if status != http.StatusOK || recovered.User.ID != first.User.ID {
		t.Errorf("recovering: %d got %s, want %s", status, recovered.User.ID, first.User.ID)
	}
}

func TestSignInWithAppleRefusesWhatAppleDidNotSay(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))

	if status, _ := appleSignIn(t, server, "", f.identityToken(t, "sub-1", "n1"), "wrong", ""); status != http.StatusUnauthorized {
		t.Errorf("wrong nonce: %d", status)
	}
	if status, _ := appleSignIn(t, server, "", "not.a.token", "n1", ""); status != http.StatusUnauthorized {
		t.Errorf("garbage token: %d", status)
	}
	if status, _ := appleSignIn(t, server, "", "", "n1", ""); status != http.StatusBadRequest {
		t.Errorf("missing token: %d", status)
	}
	expired := f.sign(t, jwt.MapClaims{"iss": apple.Issuer, "aud": "app.sente.go", "sub": "s",
		"iat": time.Now().Add(-time.Hour).Unix(), "exp": time.Now().Add(-time.Minute).Unix()})
	if status, _ := appleSignIn(t, server, "", expired, "", ""); status != http.StatusUnauthorized {
		t.Errorf("expired: %d", status)
	}
}

func TestSignInWithAppleIsOffUntilConfigured(t *testing.T) {
	plain := newTestServer(t)
	if status, _ := appleSignIn(t, plain, "", "x", "n", ""); status != http.StatusNotFound {
		t.Errorf("unconfigured: %d", status)
	}
	response, _ := do(t, plain, http.MethodPost, "/v1/auth/apple/notifications", "", `{"payload":"x"}`)
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("unconfigured notifications: %d", response.StatusCode)
	}
	flags := func(server *httptest.Server) map[string]bool {
		var config struct {
			Flags map[string]bool `json:"feature_flags"`
		}
		_ = json.Unmarshal(readAllFrom(t, server, "/v1/config"), &config)
		return config.Flags
	}
	if flags(plain)["apple_sign_in"] {
		t.Error("flag should be off")
	}
	if !flags(newTestServer(t, withApple(newFakeApple(t))))["apple_sign_in"] {
		t.Error("flag should be on once configured")
	}
}

// Someone revokes Sente in their Apple ID settings: the link is gone and every
// session ends, but the account and its games remain, as a guest's.
func TestRevocationNoticeUnlinksAndEndsSessions(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))
	_, signedIn := appleSignIn(t, server, "", f.identityToken(t, "sub-9", "n"), "n", "")

	response, _ := do(t, server, http.MethodPost, "/v1/auth/apple/notifications", "",
		fmt.Sprintf(`{"payload":%q}`, f.notification(t, apple.EventConsentRevoked, "sub-9")))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("notice: %d", response.StatusCode)
	}
	if _, guest := meOf(t, server, signedIn.AccessToken); !guest {
		t.Error("account should be a guest again")
	}
	response, _ = do(t, server, http.MethodPost, "/v1/auth/refresh", "",
		fmt.Sprintf(`{"refresh_token":%q}`, signedIn.RefreshToken))
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh after revocation: %d", response.StatusCode)
	}
	// Signing in again with the same Apple ID makes a fresh account: the old one
	// no longer answers to it.
	_, fresh := appleSignIn(t, server, "", f.identityToken(t, "sub-9", "n2"), "n2", "")
	if fresh.User.ID == signedIn.User.ID {
		t.Error("a revoked Apple ID must not reopen the old account")
	}

	for name, payload := range map[string]string{
		"garbage": `{"payload":"nope"}`, "empty": `{}`, "not json": `x`,
		"forged": fmt.Sprintf(`{"payload":%q}`, newFakeApple(t).notification(t, apple.EventConsentRevoked, "sub-9")),
	} {
		response, _ := do(t, server, http.MethodPost, "/v1/auth/apple/notifications", "", payload)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", name, response.StatusCode)
		}
	}
}

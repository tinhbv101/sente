package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APNs itself cannot be called from a test, but everything that makes a request
// acceptable to it can be checked against a fake that speaks the same protocol.

func testKeyPEM(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), key
}

type captured struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []map[string]any
	status   int
	reason   string
}

func fakeAPNs(t *testing.T) (*httptest.Server, *captured) {
	t.Helper()
	state := &captured{status: http.StatusOK}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		state.mu.Lock()
		state.requests = append(state.requests, r.Clone(r.Context()))
		state.bodies = append(state.bodies, body)
		status, reason := state.status, state.reason
		state.mu.Unlock()
		w.WriteHeader(status)
		if reason != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, state
}

func newClient(t *testing.T, server *httptest.Server) (*Client, *ecdsa.PrivateKey) {
	t.Helper()
	pemBytes, _ := testKeyPEM(t)
	key, err := ParseKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	client := New(server.URL, "app.sente.go", "TEAM123456", "KEY1234567", key)
	client.http = server.Client() // trusts the test certificate; still HTTP/2
	return client, key
}

func TestParseKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseKey([]byte("not a key")); err == nil {
		t.Error("garbage must not parse")
	}
	rsaDER := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}})
	if _, err := ParseKey(rsaDER); err == nil {
		t.Error("bad DER must not parse")
	}
}

func TestSendShapesTheRequestTheWayAPNsExpects(t *testing.T) {
	server, state := fakeAPNs(t)
	client, key := newClient(t, server)
	badge := 2

	err := client.Send(context.Background(), "abc123", Notification{
		Title: "Đến lượt bạn", Body: "Ván với bình", CollapseID: "game:g1", ThreadID: "g1",
		Payload: map[string]any{"game_id": "g1"}, Priority: 5, Badge: &badge,
	})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.requests) != 1 {
		t.Fatalf("want one request, got %d", len(state.requests))
	}
	r := state.requests[0]
	if r.URL.Path != "/3/device/abc123" {
		t.Errorf("path: %s", r.URL.Path)
	}
	if r.ProtoMajor != 2 {
		t.Errorf("APNs requires HTTP/2, got HTTP/%d", r.ProtoMajor)
	}
	for header, want := range map[string]string{
		"apns-topic": "app.sente.go", "apns-push-type": "alert", "apns-priority": "5",
		"apns-collapse-id": "game:g1",
	} {
		if got := r.Header.Get(header); got != want {
			t.Errorf("%s: want %q, got %q", header, want, got)
		}
	}
	if r.Header.Get("apns-expiration") == "" {
		t.Error("an expiration must be set so stale reminders are dropped")
	}

	// The bearer token is an ES256 JWT with our team and key ids.
	bearer := strings.TrimPrefix(r.Header.Get("authorization"), "bearer ")
	parsed, err := jwt.Parse(bearer, func(token *jwt.Token) (any, error) {
		if token.Header["kid"] != "KEY1234567" {
			return nil, errors.New("wrong kid")
		}
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("signing token does not verify: %v", err)
	}
	if claims := parsed.Claims.(jwt.MapClaims); claims["iss"] != "TEAM123456" {
		t.Errorf("iss: %v", claims["iss"])
	}

	body := state.bodies[0]
	aps := body["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	if alert["title"] != "Đến lượt bạn" || alert["body"] != "Ván với bình" {
		t.Errorf("alert: %v", alert)
	}
	if aps["badge"].(float64) != 2 || aps["thread-id"] != "g1" {
		t.Errorf("aps: %v", aps)
	}
	if body["game_id"] != "g1" {
		t.Error("custom payload must sit beside aps for the app to deep-link")
	}
}

func TestSigningTokenIsReusedWithinAppleWindow(t *testing.T) {
	server, state := fakeAPNs(t)
	client, _ := newClient(t, server)
	base := time.Now()
	client.now = func() time.Time { return base }

	_ = client.Send(context.Background(), "t", Notification{Title: "a"})
	client.now = func() time.Time { return base.Add(30 * time.Minute) }
	_ = client.Send(context.Background(), "t", Notification{Title: "b"})
	client.now = func() time.Time { return base.Add(55 * time.Minute) }
	_ = client.Send(context.Background(), "t", Notification{Title: "c"})

	state.mu.Lock()
	defer state.mu.Unlock()
	first := state.requests[0].Header.Get("authorization")
	second := state.requests[1].Header.Get("authorization")
	third := state.requests[2].Header.Get("authorization")
	if first != second {
		t.Error("the token should be reused inside the window")
	}
	if third == first {
		t.Error("the token must be refreshed before Apple's 60-minute limit")
	}
}

func TestDefaultPriorityIsImmediate(t *testing.T) {
	server, state := fakeAPNs(t)
	client, _ := newClient(t, server)
	_ = client.Send(context.Background(), "t", Notification{Title: "x"})
	state.mu.Lock()
	defer state.mu.Unlock()
	if got := state.requests[0].Header.Get("apns-priority"); got != "10" {
		t.Errorf("want 10, got %s", got)
	}
}

// A dead token must be reported as such, so the caller deletes it instead of
// hammering APNs with it forever.
func TestADeadTokenIsReportedAsUnregistered(t *testing.T) {
	server, state := fakeAPNs(t)
	client, _ := newClient(t, server)
	for _, failure := range []struct {
		status int
		reason string
	}{{http.StatusGone, "Unregistered"}, {http.StatusBadRequest, "BadDeviceToken"}} {
		state.mu.Lock()
		state.status, state.reason = failure.status, failure.reason
		state.mu.Unlock()
		if err := client.Send(context.Background(), "dead", Notification{Title: "x"}); !errors.Is(err, ErrUnregistered) {
			t.Errorf("%d %s: want ErrUnregistered, got %v", failure.status, failure.reason, err)
		}
	}
}

func TestOtherRejectionsCarryTheReason(t *testing.T) {
	server, state := fakeAPNs(t)
	client, _ := newClient(t, server)
	state.mu.Lock()
	state.status, state.reason = http.StatusTooManyRequests, "TooManyRequests"
	state.mu.Unlock()
	err := client.Send(context.Background(), "t", Notification{Title: "x"})
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "TooManyRequests") {
		t.Errorf("want a rejection naming the reason, got %v", err)
	}
}

// Package push delivers notifications through APNs with token-based auth
// (docs/03 ADR-013). One .p8 key signs short-lived JWTs; no certificates to renew.
package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	ProductionHost = "https://api.push.apple.com"
	SandboxHost    = "https://api.sandbox.push.apple.com"
	// tokenLifetime keeps the signing JWT inside Apple's 20–60 minute window.
	tokenLifetime = 50 * time.Minute
)

var (
	// ErrUnregistered means the device token is dead: the app was removed or the
	// token rotated. The caller must delete it, or APNs will keep refusing it.
	ErrUnregistered = errors.New("push: device token is no longer registered")
	ErrRejected     = errors.New("push: APNs rejected the notification")
)

type Notification struct {
	Title string
	Body  string
	// CollapseID folds repeated notifications for one game into one entry.
	CollapseID string
	ThreadID   string
	// Payload travels under the top-level object next to "aps"; the app reads
	// game_id from it to deep-link.
	Payload map[string]any
	// Background priority (5) for turn reminders in slow games; 10 wakes the
	// device immediately, for a clock about to run out.
	Priority int
	Badge    *int
}

type Client struct {
	host   string
	topic  string
	teamID string
	keyID  string
	key    *ecdsa.PrivateKey
	http   *http.Client
	now    func() time.Time

	mu      sync.Mutex
	token   string
	tokenAt time.Time
}

// ParseKey reads the .p8 file Apple hands out.
func ParseKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("push: key file is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("push: parsing key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("push: key is not an EC key")
	}
	return key, nil
}

// New builds a client. `topic` is the app's bundle id.
func New(host, topic, teamID, keyID string, key *ecdsa.PrivateKey) *Client {
	return &Client{
		host: strings.TrimRight(host, "/"), topic: topic, teamID: teamID, keyID: keyID, key: key,
		// http.Client speaks HTTP/2 over TLS on its own, which APNs requires.
		http: &http.Client{Timeout: 10 * time.Second},
		now:  time.Now,
	}
}

// signingToken is cached and refreshed inside Apple's window. Signing on every
// send would be wasteful; signing too rarely gets the token rejected.
func (c *Client) signingToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Sub(c.tokenAt) < tokenLifetime {
		return c.token, nil
	}
	claims := jwt.MapClaims{"iss": c.teamID, "iat": c.now().Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = c.keyID
	signed, err := token.SignedString(c.key)
	if err != nil {
		return "", fmt.Errorf("push: signing token: %w", err)
	}
	c.token, c.tokenAt = signed, c.now()
	return signed, nil
}

type apnsError struct {
	Reason string `json:"reason"`
}

// Send delivers one notification. Delivery is best-effort: the caller decides
// whether to retry, except for ErrUnregistered which must not be retried.
func (c *Client) Send(ctx context.Context, deviceToken string, n Notification) error {
	token, err := c.signingToken()
	if err != nil {
		return err
	}
	aps := map[string]any{
		"alert": map[string]string{"title": n.Title, "body": n.Body},
		"sound": "default",
	}
	if n.ThreadID != "" {
		aps["thread-id"] = n.ThreadID
	}
	if n.Badge != nil {
		aps["badge"] = *n.Badge
	}
	body := map[string]any{"aps": aps}
	for key, value := range n.Payload {
		body[key] = value
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/3/device/"+deviceToken, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("authorization", "bearer "+token)
	request.Header.Set("apns-topic", c.topic)
	request.Header.Set("apns-push-type", "alert")
	priority := n.Priority
	if priority == 0 {
		priority = 10
	}
	request.Header.Set("apns-priority", fmt.Sprint(priority))
	// A turn reminder that cannot be delivered within a day is stale anyway.
	request.Header.Set("apns-expiration", fmt.Sprint(c.now().Add(24*time.Hour).Unix()))
	if n.CollapseID != "" {
		request.Header.Set("apns-collapse-id", n.CollapseID)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("push: sending: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return nil
	}
	var failure apnsError
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = json.Unmarshal(raw, &failure)
	if response.StatusCode == http.StatusGone || failure.Reason == "Unregistered" || failure.Reason == "BadDeviceToken" {
		return ErrUnregistered
	}
	return fmt.Errorf("%w: %d %s", ErrRejected, response.StatusCode, failure.Reason)
}

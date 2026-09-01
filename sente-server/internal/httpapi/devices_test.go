package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"sente.app/server/internal/notify"
	"sente.app/server/internal/push"
	"sente.app/server/internal/store"
)

type pushed struct {
	token string
	note  push.Notification
}

type recordingSender struct{ got chan pushed }

func (r *recordingSender) Send(_ context.Context, token string, n push.Notification) error {
	r.got <- pushed{token, n}
	return nil
}

func withPush(sender *recordingSender) func(*Config) {
	return func(c *Config) {
		c.Notifier = notify.New(notify.Config{
			Games: store.NewGames(testPool), Devices: store.NewDevices(testPool), Production: sender,
		})
	}
}

func awaitPush(t *testing.T, sender *recordingSender) pushed {
	t.Helper()
	select {
	case got := <-sender.got:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("no push arrived")
		return pushed{}
	}
}

func TestDeviceRegistrationIsValidatedAndScoped(t *testing.T) {
	server := newTestServer(t)
	p := signUp(t, server)
	token := strings.Repeat("ab", 32)

	for name, body := range map[string]string{
		"short token": `{"apns_token":"abc","environment":"production"}`,
		"not hex":     fmt.Sprintf(`{"apns_token":%q,"environment":"production"}`, strings.Repeat("zz", 32)),
		"bad env":     fmt.Sprintf(`{"apns_token":%q,"environment":"staging"}`, token),
		"not json":    `nope`,
	} {
		if response, _ := do(t, server, http.MethodPost, "/v1/devices", p.token, body); response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", name, response.StatusCode)
		}
	}
	good := fmt.Sprintf(`{"apns_token":%q,"environment":"production","app_version":"0.1.0"}`, token)
	if response, _ := do(t, server, http.MethodPost, "/v1/devices", "", good); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", response.StatusCode)
	}
	if response, _ := do(t, server, http.MethodPost, "/v1/devices", p.token, good); response.StatusCode != http.StatusNoContent {
		t.Errorf("register: %d", response.StatusCode)
	}
	// Registering twice is fine: tokens are re-sent on every launch.
	if response, _ := do(t, server, http.MethodPost, "/v1/devices", p.token, good); response.StatusCode != http.StatusNoContent {
		t.Errorf("re-register: %d", response.StatusCode)
	}

	devices := store.NewDevices(testPool)
	if list, _ := devices.ForUser(context.Background(), p.userID, notify.KindTurn); len(list) != 1 {
		t.Fatalf("want one device, got %d", len(list))
	}
	// Someone else cannot remove it; the owner can.
	other := signUp(t, server)
	do(t, server, http.MethodDelete, "/v1/devices/"+token, other.token, "")
	if list, _ := devices.ForUser(context.Background(), p.userID, notify.KindTurn); len(list) != 1 {
		t.Error("a stranger removed the device")
	}
	if response, _ := do(t, server, http.MethodDelete, "/v1/devices/"+token, p.token, ""); response.StatusCode != http.StatusNoContent {
		t.Errorf("unregister: %d", response.StatusCode)
	}
	if list, _ := devices.ForUser(context.Background(), p.userID, notify.KindTurn); len(list) != 0 {
		t.Error("device should be gone")
	}
}

// The whole chain: a registered phone, a slow game, a move by the other side.
func TestAMoveInASlowGameReachesTheAbsentOpponentsPhone(t *testing.T) {
	sender := &recordingSender{got: make(chan pushed, 8)}
	server := newTestServer(t, withPush(sender))
	black, white := signUp(t, server), signUp(t, server)
	whiteToken := strings.Repeat("cd", 32)
	do(t, server, http.MethodPost, "/v1/devices", white.token,
		fmt.Sprintf(`{"apns_token":%q,"environment":"production"}`, whiteToken))

	gameID := createGame(t, server, black, fmt.Sprintf(
		`{"board_size":9,"time_control":{"kind":"correspondence","days_per_move":1},"opponent_user_id":%q}`, white.userID))
	blackConn := connect(t, server, black, gameID)
	send(t, blackConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	readMoveMade(t, blackConn, 1)

	got := awaitPush(t, sender)
	if got.token != whiteToken {
		t.Errorf("push went to %s", got.token)
	}
	if got.note.Title != "Đến lượt bạn" || got.note.Payload["game_id"] != gameID || got.note.CollapseID != "game:"+gameID {
		t.Errorf("note: %+v", got.note)
	}

	// The feature flag tells the app push is worth asking permission for.
	var config struct {
		Flags map[string]bool `json:"feature_flags"`
	}
	_ = json.Unmarshal(readAllFrom(t, server, "/v1/config"), &config)
	if !config.Flags["push"] {
		t.Error("push flag should be on")
	}
}

func TestAcceptingAnInvitationPushesItsCreator(t *testing.T) {
	sender := &recordingSender{got: make(chan pushed, 8)}
	server := newTestServer(t, withPush(sender))
	creator, friend := signUp(t, server), signUp(t, server)
	creatorToken := strings.Repeat("ef", 32)
	do(t, server, http.MethodPost, "/v1/devices", creator.token,
		fmt.Sprintf(`{"apns_token":%q,"environment":"production"}`, creatorToken))

	invite := createInvite(t, server, creator, `{"board_size":9,"creator_color":"black","time_control":{"kind":"absolute","main_time_ms":600000}}`)
	response, _ := do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept", friend.token, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("accept: %d", response.StatusCode)
	}
	got := awaitPush(t, sender)
	if got.token != creatorToken || got.note.Payload["kind"] != notify.KindInvite {
		t.Errorf("got %+v", got)
	}
}

func TestDevicePrefsEndpointTogglesKinds(t *testing.T) {
	server := newTestServer(t)
	p := signUp(t, server)
	token := strings.Repeat("ba", 32)
	do(t, server, http.MethodPost, "/v1/devices", p.token,
		fmt.Sprintf(`{"apns_token":%q,"environment":"production"}`, token))

	response, raw := do(t, server, http.MethodPatch, "/v1/devices/"+token, p.token, `{"turn":false}`)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"turn":false`) {
		t.Fatalf("patch: %d %s", response.StatusCode, raw)
	}
	devices := store.NewDevices(testPool)
	if list, _ := devices.ForUser(context.Background(), p.userID, notify.KindTurn); len(list) != 0 {
		t.Error("muted kind still delivers")
	}
	if list, _ := devices.ForUser(context.Background(), p.userID, notify.KindGameEnd); len(list) != 1 {
		t.Error("other kinds must keep working")
	}
	if response, _ := do(t, server, http.MethodPatch, "/v1/devices/"+token, p.token, `{"sound":true}`); response.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown kind: %d", response.StatusCode)
	}
	if response, _ := do(t, server, http.MethodPatch, "/v1/devices/"+strings.Repeat("cc", 32), p.token, `{"turn":true}`); response.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token: %d", response.StatusCode)
	}
}

// The rematch path: a directed invitation lands on the invitee's phone and in
// their invitation list.
func TestADirectedInvitationReachesTheInvitee(t *testing.T) {
	sender := &recordingSender{got: make(chan pushed, 8)}
	server := newTestServer(t, withPush(sender))
	creator, friend := signUp(t, server), signUp(t, server)
	friendToken := strings.Repeat("dd", 32)
	do(t, server, http.MethodPost, "/v1/devices", friend.token,
		fmt.Sprintf(`{"apns_token":%q,"environment":"production"}`, friendToken))

	response, raw := do(t, server, http.MethodPost, "/v1/challenges", creator.token,
		fmt.Sprintf(`{"board_size":9,"creator_color":"black","invitee_user_id":%q,"time_control":{"kind":"absolute","main_time_ms":600000}}`, friend.userID))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", response.StatusCode, raw)
	}
	got := awaitPush(t, sender)
	if got.token != friendToken || got.note.BodyKey != "push.challenge.body" {
		t.Errorf("push: %+v", got)
	}
	_, listRaw := do(t, server, http.MethodGet, "/v1/challenges", friend.token, "")
	if !strings.Contains(string(listRaw), `"is_mine":false`) {
		t.Errorf("the invitee should see the invitation: %s", listRaw)
	}
}

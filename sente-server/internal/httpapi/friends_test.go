package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// appleUser signs a fresh account in with Apple, which is what the friends
// endpoints require, and reports its id and friend code.
func appleUser(t *testing.T, server *httptest.Server, f *fakeApple, subject string) player {
	t.Helper()
	status, session := appleSignIn(t, server, "", f.identityToken(t, subject, subject), subject, "")
	if status != http.StatusOK {
		t.Fatalf("apple sign-in for %s: %d", subject, status)
	}
	return player{token: session.AccessToken, userID: session.User.ID}
}

func friendCodeOf(t *testing.T, server *httptest.Server, token string) string {
	t.Helper()
	response, raw := do(t, server, http.MethodGet, "/v1/me", token, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/v1/me: %d", response.StatusCode)
	}
	var me struct {
		FriendCode string `json:"friend_code"`
	}
	_ = json.Unmarshal(raw, &me)
	return me.FriendCode
}

type friendList struct {
	Items []struct {
		UserID      string `json:"user_id"`
		DisplayName string `json:"display_name"`
		Status      string `json:"status"`
		Incoming    bool   `json:"incoming"`
	} `json:"items"`
}

func listFriends(t *testing.T, server *httptest.Server, token string) friendList {
	t.Helper()
	response, raw := do(t, server, http.MethodGet, "/v1/friends", token, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/friends: %d %s", response.StatusCode, raw)
	}
	var list friendList
	_ = json.Unmarshal(raw, &list)
	return list
}

func TestFriendsFromCodeToGame(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))
	an := appleUser(t, server, f, "sub-an")
	binh := appleUser(t, server, f, "sub-binh")
	binhCode := friendCodeOf(t, server, binh.token)

	// An looks Binh up by the code Binh read out to him.
	response, raw := do(t, server, http.MethodGet, "/v1/users/by-code/"+strings.ToLower(binhCode), an.token, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("lookup: %d %s", response.StatusCode, raw)
	}
	var found struct {
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(raw, &found)
	if found.UserID != binh.userID {
		t.Fatalf("lookup returned %s, want %s", found.UserID, binh.userID)
	}

	response, raw = do(t, server, http.MethodPost, "/v1/friends", an.token,
		fmt.Sprintf(`{"user_id":%q}`, binh.userID))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("request: %d %s", response.StatusCode, raw)
	}

	if list := listFriends(t, server, binh.token); len(list.Items) != 1 || !list.Items[0].Incoming {
		t.Fatalf("binh's inbox: %+v", list)
	}
	if response, raw := do(t, server, http.MethodPost, "/v1/friends/"+an.userID+"/accept", binh.token, ""); response.StatusCode != http.StatusNoContent {
		t.Fatalf("accept: %d %s", response.StatusCode, raw)
	}
	list := listFriends(t, server, an.token)
	if len(list.Items) != 1 || list.Items[0].Status != "accepted" {
		t.Fatalf("an's friends: %+v", list)
	}

	// A friend can be invited straight into a game: the directed invitation that
	// already existed, now aimed by user id.
	response, raw = do(t, server, http.MethodPost, "/v1/challenges", an.token,
		fmt.Sprintf(`{"board_size":9,"time_control":{"kind":"absolute","main_time_ms":600000},"invitee_user_id":%q}`, binh.userID))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("directed invitation: %d %s", response.StatusCode, raw)
	}

	// Unfriending is symmetric.
	if response, _ := do(t, server, http.MethodDelete, "/v1/friends/"+binh.userID, an.token, ""); response.StatusCode != http.StatusNoContent {
		t.Fatalf("unfriend: %d", response.StatusCode)
	}
	if list := listFriends(t, server, binh.token); len(list.Items) != 0 {
		t.Errorf("binh should have no friends left: %+v", list)
	}
}

func TestFriendsRefuseGuestsAndMalformedIDs(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))
	linked := appleUser(t, server, f, "sub-linked")
	guest := signUp(t, server)

	response, raw := do(t, server, http.MethodPost, "/v1/friends", guest.token,
		fmt.Sprintf(`{"user_id":%q}`, linked.userID))
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a guest asking: %d %s", response.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "apple_required") {
		t.Errorf("want apple_required, got %s", raw)
	}
	// English follows Accept-Language, like every other error (docs/06).
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/friends",
		strings.NewReader(fmt.Sprintf(`{"user_id":%q}`, linked.userID)))
	request.Header.Set("Authorization", "Bearer "+guest.token)
	request.Header.Set("Accept-Language", "en-US")
	english, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, english)
	english.Body.Close()
	if !strings.Contains(string(body), "Sign in with Apple") {
		t.Errorf("English error text missing: %s", body)
	}

	// A malformed id is the caller's mistake, not a 500 with pgx's words in it.
	for path, method := range map[string]string{
		"/v1/friends/not-a-uuid":         http.MethodDelete,
		"/v1/friends/not-a-uuid/accept":  http.MethodPost,
		"/v1/friends/not-a-uuid/decline": http.MethodPost,
		"/v1/blocks/not-a-uuid":          http.MethodDelete,
	} {
		if response, raw := do(t, server, method, path, linked.token, ""); response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s", method, path, response.StatusCode, raw)
		}
	}
	if response, _ := do(t, server, http.MethodPost, "/v1/friends", linked.token, `{"user_id":"nope"}`); response.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed body id: %d", response.StatusCode)
	}
}

// docs/08 §4.2: someone who blocked you looks exactly like a code nobody owns.
func TestLookupTellsBlockedAndUnknownApart(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))
	an := appleUser(t, server, f, "sub-look-an")
	binh := appleUser(t, server, f, "sub-look-binh")
	binhCode := friendCodeOf(t, server, binh.token)

	if response, _ := do(t, server, http.MethodPost, "/v1/blocks", binh.token,
		fmt.Sprintf(`{"user_id":%q}`, an.userID)); response.StatusCode != http.StatusNoContent {
		t.Fatal("block failed")
	}
	blockedStatus, blockedBody := do(t, server, http.MethodGet, "/v1/users/by-code/"+binhCode, an.token, "")
	unknownStatus, unknownBody := do(t, server, http.MethodGet, "/v1/users/by-code/ZZZZZZZZ", an.token, "")
	if blockedStatus.StatusCode != unknownStatus.StatusCode || string(blockedBody) != string(unknownBody) {
		t.Errorf("blocked and unknown must be indistinguishable:\n %d %s\n %d %s",
			blockedStatus.StatusCode, blockedBody, unknownStatus.StatusCode, unknownBody)
	}
	if blockedStatus.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", blockedStatus.StatusCode)
	}
}

// Blocking cancels the invitation the blocked person had already sent, and the
// friendship with it; an open link stops working for them too.
func TestBlockingClearsInvitationsAndFriendship(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))
	an := appleUser(t, server, f, "sub-blk-an")
	binh := appleUser(t, server, f, "sub-blk-binh")

	// An aims an invitation at Binh, and separately posts an open link.
	response, raw := do(t, server, http.MethodPost, "/v1/challenges", an.token,
		fmt.Sprintf(`{"board_size":9,"time_control":{"kind":"absolute","main_time_ms":600000},"invitee_user_id":%q}`, binh.userID))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("directed invitation: %d %s", response.StatusCode, raw)
	}
	var directed struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &directed)

	_, raw = do(t, server, http.MethodPost, "/v1/challenges", an.token,
		`{"board_size":9,"time_control":{"kind":"absolute","main_time_ms":600000}}`)
	var open struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &open)

	if response, _ := do(t, server, http.MethodPost, "/v1/blocks", binh.token,
		fmt.Sprintf(`{"user_id":%q}`, an.userID)); response.StatusCode != http.StatusNoContent {
		t.Fatal("block failed")
	}

	// The directed invitation is gone from Binh's list and cannot be accepted.
	if response, raw := do(t, server, http.MethodPost, "/v1/challenges/"+directed.Code+"/accept", binh.token, ""); response.StatusCode != http.StatusConflict {
		t.Errorf("a blocked directed invitation: %d %s", response.StatusCode, raw)
	}
	// And so is the open link -- blocking someone must not leave them one shared
	// link away from a game with you.
	if response, raw := do(t, server, http.MethodPost, "/v1/challenges/"+open.Code+"/accept", binh.token, ""); response.StatusCode != http.StatusConflict {
		t.Errorf("a blocked open link: %d %s", response.StatusCode, raw)
	}
	// A new invitation aimed at the blocker is refused, and says so in its own
	// error rather than leaking the store's wording as a config problem.
	response, raw = do(t, server, http.MethodPost, "/v1/challenges", an.token,
		fmt.Sprintf(`{"board_size":9,"time_control":{"kind":"absolute","main_time_ms":600000},"invitee_user_id":%q}`, binh.userID))
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(raw), "invite_refused") {
		t.Errorf("inviting someone who blocked you: %d %s", response.StatusCode, raw)
	}
	if strings.Contains(string(raw), "store:") {
		t.Errorf("a store error leaked to the client: %s", raw)
	}
}

func TestBlocksCanBeListedAndUndone(t *testing.T) {
	f := newFakeApple(t)
	server := newTestServer(t, withApple(f))
	an := appleUser(t, server, f, "sub-list-an")
	binh := appleUser(t, server, f, "sub-list-binh")

	if response, _ := do(t, server, http.MethodPost, "/v1/blocks", an.token,
		fmt.Sprintf(`{"user_id":%q}`, binh.userID)); response.StatusCode != http.StatusNoContent {
		t.Fatal("block failed")
	}
	response, raw := do(t, server, http.MethodGet, "/v1/blocks", an.token, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/blocks: %d %s", response.StatusCode, raw)
	}
	var list struct {
		Items []struct {
			UserID string `json:"user_id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(raw, &list)
	if len(list.Items) != 1 || list.Items[0].UserID != binh.userID {
		t.Fatalf("blocked list: %s", raw)
	}
	if response, _ := do(t, server, http.MethodDelete, "/v1/blocks/"+binh.userID, an.token, ""); response.StatusCode != http.StatusNoContent {
		t.Fatal("unblock failed")
	}
	_, raw = do(t, server, http.MethodGet, "/v1/blocks", an.token, "")
	_ = json.Unmarshal(raw, &list)
	if len(list.Items) != 0 {
		t.Errorf("unblock should empty the list: %s", raw)
	}
}

// A friend request rings the other phone once, and asking again does not ring
// it a second time.
func TestFriendRequestPushesOnceAndCarriesNoGame(t *testing.T) {
	f := newFakeApple(t)
	sender := &recordingSender{got: make(chan pushed, 8)}
	server := newTestServer(t, withApple(f), withPush(sender))
	an := appleUser(t, server, f, "sub-push-an")
	binh := appleUser(t, server, f, "sub-push-binh")

	token := strings.Repeat("cd", 32)
	if response, _ := do(t, server, http.MethodPost, "/v1/devices", binh.token,
		fmt.Sprintf(`{"apns_token":%q,"environment":"production"}`, token)); response.StatusCode != http.StatusNoContent {
		t.Fatal("device registration failed")
	}

	if response, raw := do(t, server, http.MethodPost, "/v1/friends", an.token,
		fmt.Sprintf(`{"user_id":%q}`, binh.userID)); response.StatusCode != http.StatusCreated {
		t.Fatalf("request: %d %s", response.StatusCode, raw)
	}
	got := awaitPush(t, sender)
	if got.note.TitleKey != "push.friend.request.title" || got.note.BodyKey != "push.friend.request.body" {
		t.Errorf("push keys: %+v", got.note)
	}
	if _, hasGame := got.note.Payload["game_id"]; hasGame {
		t.Errorf("a friend push has no game: %+v", got.note.Payload)
	}
	if got.note.Payload["friend_id"] != an.userID {
		t.Errorf("payload should name the sender: %+v", got.note.Payload)
	}

	// Asking again writes nothing, so it must not ring again.
	if response, _ := do(t, server, http.MethodPost, "/v1/friends", an.token,
		fmt.Sprintf(`{"user_id":%q}`, binh.userID)); response.StatusCode != http.StatusCreated {
		t.Fatal("repeat request failed")
	}
	select {
	case extra := <-sender.got:
		t.Errorf("a repeat request must not push again: %+v", extra.note)
	default:
	}
}

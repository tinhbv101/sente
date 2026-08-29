package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type sessionJSON struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	User         struct {
		ID string `json:"id"`
	} `json:"user"`
}

func TestGuestSignUpIssuesARefreshTokenAndItRotates(t *testing.T) {
	server := newTestServer(t)
	response, payload := do(t, server, http.MethodPost, "/v1/auth/guest", "", "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("sign-up: %d", response.StatusCode)
	}
	var first sessionJSON
	if err := json.Unmarshal(payload, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.RefreshToken, "rt_") {
		t.Fatalf("want a refresh token, got %q", first.RefreshToken)
	}

	// Rotate: a new access token for the same user, and a new refresh token.
	response, payload = do(t, server, http.MethodPost, "/v1/auth/refresh", "",
		`{"refresh_token":"`+first.RefreshToken+`"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("refresh: %d %s", response.StatusCode, payload)
	}
	var second sessionJSON
	if err := json.Unmarshal(payload, &second); err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == "" {
		t.Error("rotation must hand out new tokens")
	}
	// The new access token is the same person.
	_, me := do(t, server, http.MethodGet, "/v1/me", second.AccessToken, "")
	var profile struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(me, &profile)
	if profile.ID != first.User.ID {
		t.Errorf("refresh changed identity: %s → %s", first.User.ID, profile.ID)
	}
}

func TestBadRefreshTokensAreAllTheSame401(t *testing.T) {
	server := newTestServer(t)
	for name, body := range map[string]string{
		"unknown": `{"refresh_token":"rt_nope"}`,
		"empty":   `{"refresh_token":""}`,
	} {
		response, _ := do(t, server, http.MethodPost, "/v1/auth/refresh", "", body)
		if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: want 401/400, got %d", name, response.StatusCode)
		}
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	server := newTestServer(t)
	_, payload := do(t, server, http.MethodPost, "/v1/auth/guest", "", "")
	var session sessionJSON
	_ = json.Unmarshal(payload, &session)

	response, _ := do(t, server, http.MethodPost, "/v1/auth/logout", "",
		`{"refresh_token":"`+session.RefreshToken+`"}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodPost, "/v1/auth/refresh", "",
		`{"refresh_token":"`+session.RefreshToken+`"}`)
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("a logged-out token must not refresh, got %d", response.StatusCode)
	}
}

// Deleting the account ends the person's sessions and anonymises them, but the
// game their opponent played stays.
func TestDeletingAnAccount(t *testing.T) {
	server := newTestServer(t)
	_, payload := do(t, server, http.MethodPost, "/v1/auth/guest", "", "")
	var an sessionJSON
	_ = json.Unmarshal(payload, &an)
	binh := signUp(t, server)

	invite := createInvite(t, server, player{token: an.AccessToken, userID: an.User.ID}, inviteBody)
	response, _ := do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept", binh.token, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatal("setup: accept failed")
	}

	response, _ = do(t, server, http.MethodDelete, "/v1/me", an.AccessToken, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", response.StatusCode)
	}
	// The access token is still a valid signature, but the person is gone.
	response, _ = do(t, server, http.MethodGet, "/v1/me", an.AccessToken, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("a deleted account must not resolve, got %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodPost, "/v1/auth/refresh", "",
		`{"refresh_token":"`+an.RefreshToken+`"}`)
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("a deleted account's refresh token must be dead, got %d", response.StatusCode)
	}
	// Binh still sees the game, with an anonymised opponent.
	_, payload = do(t, server, http.MethodGet, "/v1/games", binh.token, "")
	var list struct {
		Items []gameSummaryJSON `json:"items"`
	}
	_ = json.Unmarshal(payload, &list)
	if len(list.Items) != 1 {
		t.Fatalf("the opponent's history must survive, got %d games", len(list.Items))
	}
	if list.Items[0].OpponentName != "" {
		t.Errorf("the deleted player must not be named, got %q", list.Items[0].OpponentName)
	}
}

func TestReportAndBlock(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)

	response, _ := do(t, server, http.MethodPost, "/v1/reports", an.token,
		`{"user_id":"`+binh.userID+`","category":"abuse","note":"nói tục trong chat"}`)
	if response.StatusCode != http.StatusAccepted {
		t.Errorf("report: want 202, got %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodPost, "/v1/reports", an.token,
		`{"user_id":"`+an.userID+`","category":"abuse"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("reporting yourself: want 400, got %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodPost, "/v1/reports", an.token,
		`{"user_id":"`+binh.userID+`","category":"ugly"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown category: want 400, got %d", response.StatusCode)
	}

	response, _ = do(t, server, http.MethodPost, "/v1/blocks", an.token, `{"user_id":"`+binh.userID+`"}`)
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("block: want 204, got %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodPost, "/v1/blocks", an.token, `{"user_id":"`+binh.userID+`"}`)
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("blocking twice is idempotent, got %d", response.StatusCode)
	}
	response, _ = do(t, server, http.MethodDelete, "/v1/blocks/"+binh.userID, an.token, "")
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("unblock: want 204, got %d", response.StatusCode)
	}
}

func TestMovesAndSGFExport(t *testing.T) {
	server := newTestServer(t)
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)

	blackConn := connect(t, server, black, gameID)
	readUntil(t, blackConn, "game_state")
	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")
	send(t, blackConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	readMoveMade(t, whiteConn, 1)
	send(t, whiteConn, "move", incomingMove{Kind: "pass", ExpectedMoveNumber: 1})
	readMoveMade(t, blackConn, 2)

	_, payload := do(t, server, http.MethodGet, "/v1/games/"+gameID+"/moves", black.token, "")
	var moves struct {
		BoardSize int        `json:"board_size"`
		Items     []moveJSON `json:"items"`
	}
	if err := json.Unmarshal(payload, &moves); err != nil {
		t.Fatal(err)
	}
	if len(moves.Items) != 2 || moves.Items[0].Point == nil || *moves.Items[0].Point != "E5" ||
		moves.Items[1].Kind != "pass" {
		t.Errorf("unexpected move list: %+v", moves.Items)
	}

	response, payload := do(t, server, http.MethodGet, "/v1/games/"+gameID+"/sgf", black.token, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("sgf: %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-go-sgf") {
		t.Errorf("content type: %q", got)
	}
	sgf := string(payload)
	for _, fragment := range []string{"(;GM[1]FF[4]", "SZ[9]", ";B[ee]", ";W[]", "PB[", "PW["} {
		if !strings.Contains(sgf, fragment) {
			t.Errorf("SGF missing %q: %s", fragment, sgf)
		}
	}
}

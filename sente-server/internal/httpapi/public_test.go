package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestLandingPageForAnOpenInvitation(t *testing.T) {
	server := newTestServer(t)
	an := signUp(t, server)
	invite := createInvite(t, server, an, inviteBody)

	response, err := http.Get(server.URL + "/j/" + invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := string(readAll(t, response))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("want an HTML page, got %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	for _, fragment := range []string{invite.Code, "mời bạn", "9 × 9", "Nhật Bản", `href="sente://j/` + invite.Code + `"`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("landing page missing %q", fragment)
		}
	}
	if strings.Contains(body, "App Store") {
		t.Error("no store link should be offered when none is configured")
	}
}

func TestLandingPageForAnUnknownCodeEscapesIt(t *testing.T) {
	server := newTestServer(t)
	// No slash in the payload: one would split the path and miss the route.
	response, err := http.Get(server.URL + "/j/%3Cimg%20src=x%20onerror=alert(1)%3E")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := string(readAll(t, response))
	if strings.Contains(body, "<img") {
		t.Fatal("the code must be HTML-escaped")
	}
	if !strings.Contains(body, "&lt;img") {
		t.Error("the escaped code should still be shown")
	}
	if !strings.Contains(body, "Không tìm thấy") {
		t.Error("an unknown code should say so")
	}
}

func TestLandingPageForASpentInvitation(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)
	invite := createInvite(t, server, an, inviteBody)
	do(t, server, http.MethodPost, "/v1/challenges/"+invite.Code+"/accept", binh.token, "")

	response, _ := http.Get(server.URL + "/j/" + invite.Code)
	body := string(readAll(t, response))
	_ = response.Body.Close()
	if !strings.Contains(body, "không còn hiệu lực") {
		t.Error("a used invitation must say it is gone")
	}
	if strings.Contains(body, `href="sente://`) {
		t.Error("no open-in-app link for a spent invitation")
	}
}

func TestAASAIsServedOnlyWhenConfigured(t *testing.T) {
	server := newTestServer(t)
	response, err := http.Get(server.URL + "/.well-known/apple-app-site-association")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("without a team id the file must not exist, got %d", response.StatusCode)
	}

	api := New(Config{Pool: testPool, Redis: testRedis, Issuer: nil, AppleTeamID: "ABCDE12345"})
	recorder := newRecorder()
	api.handleAASA(recorder, nil)
	body := recorder.body.String()
	if recorder.status != http.StatusOK && recorder.status != 0 {
		t.Fatalf("status %d", recorder.status)
	}
	for _, fragment := range []string{`"ABCDE12345.app.sente.go"`, `"/j/*"`, `"/g/*"`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("AASA missing %s: %s", fragment, body)
		}
	}
}

func TestMetricsAreExposed(t *testing.T) {
	server := newTestServer(t)
	// Reject a move so the drift counter has a value to show.
	black, white := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, black, blitz)
	blackConn := connect(t, server, black, gameID)
	readUntil(t, blackConn, "game_state")
	whiteConn := connect(t, server, white, gameID)
	readUntil(t, whiteConn, "game_state")
	send(t, whiteConn, "move", incomingMove{Kind: "play", Point: "e5", ExpectedMoveNumber: 0})
	readUntil(t, whiteConn, "error")
	// And end a game, so the endings counter has a label to show.
	send(t, blackConn, "move", incomingMove{Kind: "resign", ExpectedMoveNumber: 0})
	readUntil(t, blackConn, "game_over")

	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := string(readAll(t, response))
	for _, series := range []string{"sente_illegal_move_rejected_total", "sente_move_apply_seconds", "sente_game_ended_total"} {
		if !strings.Contains(body, series) {
			t.Errorf("metrics missing %s", series)
		}
	}
	if !strings.Contains(body, `sente_illegal_move_rejected_total{reason="not_your_turn"}`) {
		t.Error("the rejected move should have been counted under its code")
	}
	if !strings.Contains(body, `sente_game_ended_total{reason="resignation"}`) {
		t.Error("the resignation should have been counted")
	}
}

// A tiny ResponseWriter so handlers can be called directly.
type recorder struct {
	header http.Header
	status int
	body   strings.Builder
}

func newRecorder() *recorder { return &recorder{header: http.Header{}} }

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(status int)      { r.status = status }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }

// The privacy policy is what App Store Connect links to; it must be there in
// both languages, pick the reader's, and only show a contact when there is one.
func TestPrivacyPolicyIsServedInTheReadersLanguage(t *testing.T) {
	server := newTestServer(t, func(c *Config) { c.ContactEmail = "hello@example.test" })
	get := func(path, acceptLanguage string) (*http.Response, string) {
		request, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if acceptLanguage != "" {
			request.Header.Set("Accept-Language", acceptLanguage)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response, string(readAll(t, response))
	}

	response, body := get("/privacy", "")
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status %d type %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "Chính sách quyền riêng tư") || !strings.Contains(body, `lang="vi"`) {
		t.Error("default should be Vietnamese")
	}
	if !strings.Contains(body, "mailto:hello@example.test") || !strings.Contains(body, "Xóa tài khoản") {
		t.Error("contact and deletion instructions must be present")
	}

	if _, body := get("/privacy?lang=en", ""); !strings.Contains(body, "Privacy Policy") || !strings.Contains(body, `lang="en"`) {
		t.Error("?lang=en should switch to English")
	}
	if _, body := get("/privacy", "en-US,en;q=0.9"); !strings.Contains(body, "Privacy Policy") {
		t.Error("Accept-Language en should pick English")
	}
	if _, body := get("/privacy", "vi-VN,vi;q=0.9,en;q=0.8"); !strings.Contains(body, "Chính sách quyền riêng tư") {
		t.Error("Accept-Language vi should pick Vietnamese")
	}
	if _, body := get("/privacy?lang=fr", "fr-FR"); !strings.Contains(body, `lang="vi"`) {
		t.Error("unknown languages fall back to Vietnamese")
	}

	plain := newTestServer(t)
	if body := string(readAllFrom(t, plain, "/privacy")); strings.Contains(body, "mailto:") {
		t.Error("no contact configured: no contact section")
	}
	// The landing page links to it.
	if body := string(readAllFrom(t, server, "/j/NOPE1234")); !strings.Contains(body, `href="/privacy"`) {
		t.Error("landing page should link to the policy")
	}
}

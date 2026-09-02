package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/coder/websocket"
)

// Quick chat is broadcast-only: both players see the line, nothing is stored,
// and only whitelisted codes pass.
func TestQuickChatReachesBothPlayers(t *testing.T) {
	server := newTestServer(t)
	an, binh := signUp(t, server), signUp(t, server)
	gameID := createGame(t, server, an, `{"board_size":9,"time_control":{"kind":"absolute","main_time_ms":600000}}`)

	anConn := connect(t, server, an, gameID)
	readUntil(t, anConn, "game_state")
	binhConn := connect(t, server, binh, gameID)
	readUntil(t, binhConn, "game_state")

	send(t, anConn, "chat", map[string]string{"code": "hi"})

	for _, conn := range []*websocket.Conn{anConn, binhConn} {
		message := readUntil(t, conn, "chat")
		var line struct {
			By   string `json:"by"`
			Code string `json:"code"`
		}
		if err := json.Unmarshal(message.Payload, &line); err != nil {
			t.Fatal(err)
		}
		if line.Code != "hi" || (line.By != "black" && line.By != "white") {
			t.Errorf("chat line = %+v", line)
		}
	}

	// Free text and unknown codes never pass: the wire carries codes only.
	send(t, binhConn, "chat", map[string]string{"code": "free text"})
	message := readUntil(t, binhConn, "error")
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(message.Payload, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "malformed" {
		t.Errorf("unknown chat code must be refused as malformed, got %q", failure.Code)
	}
}

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

const (
	heartbeatInterval = 20 * time.Second
	// readTimeout closes a connection that has gone quiet. The client heartbeats
	// every 20 seconds, so 45 gives it two chances to be late (docs/06 §3.9).
	readTimeout  = 45 * time.Second
	writeTimeout = 10 * time.Second
	maxMessage   = 16 << 10
	// sendBuffer bounds how far behind a slow client may fall before it is
	// dropped and told to resync (docs/06 §3.12).
	sendBuffer = 64
)

// Close codes from docs/06 §3.10. The client branches on these, so they are part
// of the contract, not an implementation detail.
const (
	closeUnauthorized        = 4000
	closeProtocolUnsupported = 4002
	closeHeartbeatTimeout    = 4006
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	claims, err := s.claimsFrom(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Phiên đăng nhập đã hết hạn.")
		return
	}
	if version := r.URL.Query().Get("pv"); version != "" {
		parsed, convErr := strconv.Atoi(version)
		if convErr != nil || parsed != ProtocolVersion {
			writeError(w, http.StatusBadRequest, "protocol_unsupported",
				"Phiên bản ứng dụng quá cũ. Vui lòng cập nhật.")
			return
		}
	}
	gameID := r.URL.Query().Get("game_id")
	if gameID == "" {
		writeError(w, http.StatusBadRequest, "malformed", "Thiếu game_id.")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.config.AllowedOrigins,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxMessage)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &connection{
		server: s, conn: conn, gameID: gameID, userID: claims.UserID,
		outgoing: make(chan Message, sendBuffer),
	}
	c.run(ctx)
}

type connection struct {
	server *Server
	conn   *websocket.Conn
	gameID string
	userID string
	colour rules.Color
	// size is read once at connect time; the board never changes size mid-game.
	size     int
	outgoing chan Message
}

func (c *connection) run(ctx context.Context) {
	loaded, err := c.server.games.Load(ctx, c.gameID)
	if err != nil {
		c.closeWith(closeUnauthorized, "game not found")
		return
	}
	c.size = loaded.Session.Config.Size

	// A connection is bound to one colour for the life of the socket, so a client
	// can never move for its opponent by putting the other colour on the wire.
	switch c.userID {
	case loaded.BlackUserID:
		c.colour = rules.Black
	case loaded.WhiteUserID:
		c.colour = rules.White
	default:
		// An unclaimed seat is taken by the first player to connect, which is what
		// makes an invite link work before accounts are wired up.
		colour, claimErr := c.server.games.ClaimSeat(ctx, c.gameID, c.userID)
		if claimErr != nil {
			c.closeWith(closeUnauthorized, "not a player in this game")
			return
		}
		c.colour = colour
	}

	events, unsubscribe := c.server.config.Hub.Subscribe(ctx, c.gameID)
	defer unsubscribe()

	go c.writeLoop(ctx)
	c.send(newMessageOrDrop("hello", helloPayload{
		ServerTime: time.Now().UTC(), RulesVersion: store.RulesVersion,
		HeartbeatIntervalMs: int(heartbeatInterval / time.Millisecond),
		ProtocolVersion:     ProtocolVersion,
	}))
	c.send(newMessageOrDrop("game_state", gameStateOf(c.gameID, store.RulesVersion, loaded.Session)))

	go c.forwardEvents(ctx, events)
	c.readLoop(ctx)
}

func (c *connection) readLoop(ctx context.Context) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		_, data, err := c.conn.Read(readCtx)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				c.closeWith(closeHeartbeatTimeout, "no heartbeat")
			}
			return
		}
		var message Message
		if err := json.Unmarshal(data, &message); err != nil {
			c.sendError(message.ID, "malformed", "Tin nhắn không hợp lệ.")
			continue
		}
		c.handle(ctx, message)
	}
}

func (c *connection) handle(ctx context.Context, message Message) {
	if message.Type == "ping" {
		var payload incomingPing
		_ = json.Unmarshal(message.Payload, &payload)
		reply := newMessageOrDrop("pong", pongPayload{
			ClientTime: payload.ClientTime, ServerTime: time.Now().UTC()})
		reply.Re = message.ID
		c.send(reply)
		return
	}

	command, err := c.commandFrom(message)
	if err != nil {
		c.sendError(message.ID, "malformed", err.Error())
		return
	}
	if _, err := c.server.config.Hub.Execute(ctx, c.gameID, command); err != nil {
		// The rules engine's own code is what the client branches on (docs/06 §3.6).
		c.sendError(message.ID, err.Error(), messageForCode(err.Error()))
		return
	}
	// The result is not echoed here: it arrives through the subscription, the same
	// way the opponent sees it. One delivery path, one ordering.
}

func (c *connection) commandFrom(message Message) (game.Command, error) {
	switch message.Type {
	case "move":
		var payload incomingMove
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			return nil, errors.New("Nước đi không hợp lệ.")
		}
		move, err := moveFrom(payload, c.size)
		if err != nil {
			return nil, err
		}
		return game.PlayCommand{
			By: c.colour, Move: move, ClientMoveID: payload.ClientMoveID,
			ExpectedMoveNumber: payload.ExpectedMoveNumber,
		}, nil
	case "mark_dead":
		var payload incomingMarkDead
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			return nil, errors.New("Toạ độ không hợp lệ.")
		}
		point, ok := rules.ParseCoordinate(payload.Point, c.size)
		if !ok {
			return nil, errors.New("Toạ độ không hợp lệ.")
		}
		return game.MarkDeadCommand{By: c.colour, Point: point}, nil
	case "scoring_accept":
		var payload incomingAccept
		_ = json.Unmarshal(message.Payload, &payload)
		return game.AcceptScoreCommand{By: c.colour, Accepted: payload.Accepted}, nil
	case "scoring_resume":
		return game.ResumePlayCommand{By: c.colour}, nil
	case "undo_request":
		return game.RequestUndoCommand{By: c.colour}, nil
	case "undo_response":
		var payload incomingUndoResponse
		_ = json.Unmarshal(message.Payload, &payload)
		return game.RespondUndoCommand{By: c.colour, Accept: payload.Accept}, nil
	default:
		return nil, errors.New("Loại tin nhắn không được hỗ trợ.")
	}
}

func moveFrom(payload incomingMove, size int) (rules.Move, error) {
	switch payload.Kind {
	case "pass":
		return rules.Pass, nil
	case "resign":
		return rules.Resign, nil
	case "play", "":
		point, ok := rules.ParseCoordinate(payload.Point, size)
		if !ok {
			return rules.Move{}, errors.New("Toạ độ không hợp lệ.")
		}
		return rules.Play(point), nil
	default:
		return rules.Move{}, errors.New("Loại nước đi không hợp lệ.")
	}
}

func (c *connection) forwardEvents(ctx context.Context, events <-chan game.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if message, ok := protocolMessage(c.gameID, c.size, event); ok {
				c.send(message)
			}
		}
	}
}

func (c *connection) writeLoop(ctx context.Context) {
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Ping(writeCtx)
			cancel()
			if err != nil {
				return
			}
		case message := <-c.outgoing:
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = c.conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// send never blocks. A client that cannot keep up is closed rather than allowed
// to stall the game it is watching; it recovers by reconnecting (docs/06 §3.12).
func (c *connection) send(message Message) {
	select {
	case c.outgoing <- message:
	default:
		c.closeWith(closeHeartbeatTimeout, "client too slow")
	}
}

func (c *connection) sendError(replyTo, code, message string) {
	out := newMessageOrDrop("error", errorPayload{Code: code, Message: message})
	out.Re = replyTo
	c.send(out)
}

func (c *connection) closeWith(code int, reason string) {
	_ = c.conn.Close(websocket.StatusCode(code), reason)
}

func newMessageOrDrop(kind string, payload any) Message {
	message, err := newMessage(kind, payload)
	if err != nil {
		return Message{Type: "error", TS: time.Now().UTC()}
	}
	return message
}

// protocolMessage maps an internal event onto the client protocol. Several
// internal events collapse onto one client message: a player wants the current
// scoring state, not a distinction between "opened" and "changed".
func protocolMessage(gameID string, size int, event game.Event) (Message, bool) {
	switch e := event.(type) {
	case game.MoveMade:
		payload := moveMadePayload{
			GameID: gameID, MoveNumber: e.MoveNumber, Color: e.By.String(),
			Kind: moveKindName(e.Move), BoardHash: hexHash(e.BoardHash),
			Captured: pointNames(e.Captured, size),
		}
		if e.Move.Kind == rules.KindPlay {
			point := rules.CoordinateText(e.Move.Point, size)
			payload.Point = &point
		}
		payload.Clock = clockSnapshot(e.Clock)
		return newMessageOrDrop("move_made", payload), true
	case game.ScoringOpened:
		return newMessageOrDrop("scoring_state", scoringStatePayload{
			GameID: gameID, Suggested: pointNames(e.Suggested, size),
			Dead: pointNames(e.Suggested, size),
		}), true
	case game.ScoringChanged:
		return newMessageOrDrop("scoring_state", scoringStatePayload{
			GameID: gameID, Dead: pointNames(e.Dead, size),
			BlackAccepted: e.BlackAccepted, WhiteAccepted: e.WhiteAccepted,
			Score: &scoreJSON{Black: e.Score.Black, White: e.Score.White,
				Margin: e.Score.Margin()},
		}), true
	case game.GameEnded:
		return newMessageOrDrop("game_over", gameOverPayload{
			GameID: gameID, Result: *resultOf(&e.Result)}), true
	case game.ClockAdjusted:
		return newMessageOrDrop("clock_adjusted", clockAdjustedPayload{
			GameID: gameID, Color: e.Player.String(),
			DeltaMs: e.Delta.Milliseconds(), Reason: e.Reason}), true
	case game.UndoRequested:
		return newMessageOrDrop("undo_requested", map[string]string{
			"game_id": gameID, "by": e.By.String()}), true
	case game.UndoResolved:
		return newMessageOrDrop("undo_result", map[string]any{
			"game_id": gameID, "accepted": e.Accepted, "move_no": e.MoveNumber}), true
	case game.PlayResumed:
		// A rewind changes more than the client can patch, so it is told to resync.
		return newMessageOrDrop("resync_required", map[string]any{
			"game_id": gameID, "move_no": e.MoveNumber}), true
	default:
		return Message{}, false
	}
}

func clockSnapshot(clock game.Clock) clockPayload {
	deadline := clock.Deadline(rules.Black).UTC()
	return clockPayload{
		Black: clockSidePayload{MainMs: clock.Black.Main.Milliseconds(),
			PeriodsLeft: clock.Black.PeriodsLeft, PeriodMs: clock.Black.PeriodTime.Milliseconds()},
		White: clockSidePayload{MainMs: clock.White.Main.Milliseconds(),
			PeriodsLeft: clock.White.PeriodsLeft, PeriodMs: clock.White.PeriodTime.Milliseconds()},
		MoveDeadline: &deadline,
	}
}

func moveKindName(move rules.Move) string {
	switch move.Kind {
	case rules.KindPass:
		return "pass"
	case rules.KindResign:
		return "resign"
	default:
		return "play"
	}
}

// messageForCode turns a wire code into something a player can read.
func messageForCode(code string) string {
	switch code {
	case string(rules.ErrOccupied):
		return "Đã có quân ở đó."
	case string(rules.ErrSuicide):
		return "Nước này tự sát."
	case string(rules.ErrKo):
		return "Nước này bị cấm bởi luật ko."
	case string(rules.ErrSuperko):
		return "Nước này lặp lại thế cờ đã xuất hiện."
	case string(rules.ErrNotYourTurn):
		return "Chưa tới lượt bạn."
	case string(rules.ErrOutOfBounds):
		return "Toạ độ nằm ngoài bàn cờ."
	case string(game.ErrOutOfSync):
		return "Bàn cờ đã thay đổi. Đang đồng bộ lại…"
	case string(game.ErrNotPlaying):
		return "Ván đã kết thúc."
	default:
		return "Không thực hiện được."
	}
}

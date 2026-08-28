package httpapi

import (
	"encoding/json"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

// The client-facing protocol from docs/06. Deliberately separate from the
// cross-node codec in internal/wire: what a player is shown is not the same as
// what two servers say to each other.

// ProtocolVersion is sent in the handshake and checked against ?pv= (docs/06 §1).
const ProtocolVersion = 1

type Message struct {
	ID      string          `json:"id,omitempty"`
	Re      string          `json:"re,omitempty"`
	Type    string          `json:"type"`
	TS      time.Time       `json:"ts"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func newMessage(kind string, payload any) (Message, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	return Message{Type: kind, TS: time.Now().UTC(), Payload: encoded}, nil
}

type helloPayload struct {
	ServerTime          time.Time `json:"server_time"`
	RulesVersion        string    `json:"rules_version"`
	HeartbeatIntervalMs int       `json:"heartbeat_interval_ms"`
	ProtocolVersion     int       `json:"protocol_version"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type clockSidePayload struct {
	MainMs      int64 `json:"main_ms"`
	PeriodsLeft int   `json:"periods_left,omitempty"`
	PeriodMs    int64 `json:"period_ms,omitempty"`
}

type clockPayload struct {
	Black        clockSidePayload `json:"black"`
	White        clockSidePayload `json:"white"`
	MoveDeadline *time.Time       `json:"move_deadline,omitempty"`
}

type gameStatePayload struct {
	GameID       string       `json:"game_id"`
	Phase        string       `json:"phase"`
	Rules        string       `json:"rules"`
	RulesVersion string       `json:"rules_version"`
	BoardSize    int          `json:"board_size"`
	Komi         float64      `json:"komi"`
	Handicap     int          `json:"handicap"`
	Board        string       `json:"board"`
	BoardHash    string       `json:"board_hash"`
	ToPlay       string       `json:"to_play"`
	MoveNumber   int          `json:"move_no"`
	KoPoint      *string      `json:"ko_point"`
	Captures     capturesJSON `json:"captures"`
	Passes       int          `json:"consecutive_passes"`
	Clock        clockPayload `json:"clock"`
	Result       *resultJSON  `json:"result,omitempty"`
	ServerTime   time.Time    `json:"server_time"`
}

type capturesJSON struct {
	Black int `json:"black"`
	White int `json:"white"`
}

type resultJSON struct {
	Winner *string    `json:"winner"`
	Reason string     `json:"reason"`
	Score  *scoreJSON `json:"score,omitempty"`
}

type scoreJSON struct {
	Black  float64 `json:"black"`
	White  float64 `json:"white"`
	Margin float64 `json:"margin"`
}

type moveMadePayload struct {
	GameID     string       `json:"game_id"`
	MoveNumber int          `json:"move_no"`
	Color      string       `json:"color"`
	Kind       string       `json:"kind"`
	Point      *string      `json:"point,omitempty"`
	Captured   []string     `json:"captured,omitempty"`
	BoardHash  string       `json:"board_hash"`
	Clock      clockPayload `json:"clock"`
}

type scoringStatePayload struct {
	GameID        string     `json:"game_id"`
	Dead          []string   `json:"dead_points"`
	Suggested     []string   `json:"suggested_points,omitempty"`
	BlackAccepted bool       `json:"black_accepted"`
	WhiteAccepted bool       `json:"white_accepted"`
	Score         *scoreJSON `json:"score,omitempty"`
}

type gameOverPayload struct {
	GameID string     `json:"game_id"`
	Result resultJSON `json:"result"`
}

type clockAdjustedPayload struct {
	GameID  string `json:"game_id"`
	Color   string `json:"color"`
	DeltaMs int64  `json:"delta_ms"`
	Reason  string `json:"reason"`
}

// ── incoming ────────────────────────────────────────────────────────────────

type incomingMove struct {
	ClientMoveID       string `json:"client_move_id"`
	ExpectedMoveNumber int    `json:"expected_move_no"`
	Kind               string `json:"kind"`
	Point              string `json:"point"`
}

type incomingMarkDead struct {
	Point string `json:"point"`
}

type incomingAccept struct {
	Accepted bool `json:"accepted"`
}

type incomingUndoResponse struct {
	Accept bool `json:"accept"`
}

type incomingPing struct {
	ClientTime time.Time `json:"client_time"`
}

type pongPayload struct {
	ClientTime time.Time `json:"client_time"`
	ServerTime time.Time `json:"server_time"`
}

// ── conversion ──────────────────────────────────────────────────────────────

func clockOf(session game.GameSession) clockPayload {
	out := clockPayload{
		Black: clockSidePayload{
			MainMs:      session.Clock.Black.Main.Milliseconds(),
			PeriodsLeft: session.Clock.Black.PeriodsLeft,
			PeriodMs:    session.Clock.Black.PeriodTime.Milliseconds(),
		},
		White: clockSidePayload{
			MainMs:      session.Clock.White.Main.Milliseconds(),
			PeriodsLeft: session.Clock.White.PeriodsLeft,
			PeriodMs:    session.Clock.White.PeriodTime.Milliseconds(),
		},
	}
	if session.Phase() == rules.Playing {
		deadline := session.Clock.Deadline(session.ToPlay()).UTC()
		out.MoveDeadline = &deadline
	}
	return out
}

func resultOf(result *rules.Result) *resultJSON {
	if result == nil {
		return nil
	}
	out := resultJSON{Reason: string(result.Reason)}
	// A void game has no winner, and must not be encoded as a colour.
	if result.Winner == rules.Black || result.Winner == rules.White {
		winner := result.Winner.String()
		out.Winner = &winner
	}
	if result.Score != nil {
		out.Score = &scoreJSON{
			Black: result.Score.Black, White: result.Score.White, Margin: result.Score.Margin()}
	}
	return &out
}

func pointNames(points []rules.Point, size int) []string {
	out := make([]string, 0, len(points))
	for _, p := range points {
		out = append(out, rules.CoordinateText(p, size))
	}
	return out
}

func gameStateOf(gameID, rulesVersion string, session game.GameSession) gameStatePayload {
	size := session.Config.Size
	state := session.Engine.State
	payload := gameStatePayload{
		GameID: gameID, Phase: string(session.Phase()), Rules: string(session.Config.Rules),
		RulesVersion: rulesVersion, BoardSize: size, Komi: session.Engine.Komi,
		Handicap: session.Config.Handicap,
		Board:    state.Board.WireString(), BoardHash: hexHash(state.BoardHash),
		ToPlay: session.ToPlay().String(), MoveNumber: session.MoveNumber(),
		Captures: capturesJSON{Black: state.Captures.Black, White: state.Captures.White},
		Passes:   state.ConsecutivePasses,
		Clock:    clockOf(session), Result: resultOf(state.Result),
		ServerTime: time.Now().UTC(),
	}
	if state.KoPoint != nil {
		ko := rules.CoordinateText(*state.KoPoint, size)
		payload.KoPoint = &ko
	}
	return payload
}

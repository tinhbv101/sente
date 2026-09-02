// Package wire encodes commands and events so they can cross a process boundary.
//
// This is the internal codec used to forward a command to the node that owns a
// game and to fan the resulting events back out. It round-trips the domain types
// exactly. The client-facing protocol in docs/06 is a separate, narrower shape:
// what a player is shown is not the same as what two servers say to each other.
package wire

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func wrap(kind string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("wire: encoding %s: %w", kind, err)
	}
	return json.Marshal(envelope{Type: kind, Data: data})
}

// ── moves and points ────────────────────────────────────────────────────────

type movePayload struct {
	Kind  string `json:"kind"`
	Col   int    `json:"col,omitempty"`
	Row   int    `json:"row,omitempty"`
	IsSet bool   `json:"is_set,omitempty"`
}

func encodeMove(move rules.Move) movePayload {
	switch move.Kind {
	case rules.KindPass:
		return movePayload{Kind: "pass"}
	case rules.KindResign:
		return movePayload{Kind: "resign"}
	default:
		return movePayload{Kind: "play", Col: move.Point.Col, Row: move.Point.Row, IsSet: true}
	}
}

func (m movePayload) decode() (rules.Move, error) {
	switch m.Kind {
	case "pass":
		return rules.Pass, nil
	case "resign":
		return rules.Resign, nil
	case "play":
		return rules.Play(rules.Point{Col: m.Col, Row: m.Row}), nil
	default:
		return rules.Move{}, fmt.Errorf("wire: unknown move kind %q", m.Kind)
	}
}

// Empty and absent stay distinguishable in both directions: an event with no
// captures must come back with none, not with an empty list.
func encodePoints(points []rules.Point) []pointPayload {
	if len(points) == 0 {
		return nil
	}
	out := make([]pointPayload, 0, len(points))
	for _, p := range points {
		out = append(out, pointPayload{Col: p.Col, Row: p.Row})
	}
	return out
}

func decodePoints(payload []pointPayload) []rules.Point {
	if len(payload) == 0 {
		return nil
	}
	out := make([]rules.Point, 0, len(payload))
	for _, p := range payload {
		out = append(out, rules.Point{Col: p.Col, Row: p.Row})
	}
	return out
}

type pointPayload struct {
	Col int `json:"col"`
	Row int `json:"row"`
}

// ── commands ────────────────────────────────────────────────────────────────

type playPayload struct {
	By                 string      `json:"by"`
	Move               movePayload `json:"move"`
	ClientMoveID       string      `json:"client_move_id,omitempty"`
	ExpectedMoveNumber int         `json:"expected_move_number"`
	SkipExpectedCheck  bool        `json:"skip_expected_check,omitempty"`
}

type byPayload struct {
	By string `json:"by"`
}

type undoResponsePayload struct {
	By     string `json:"by"`
	Accept bool   `json:"accept"`
}

type markDeadPayload struct {
	By    string       `json:"by"`
	Point pointPayload `json:"point"`
}

type acceptScorePayload struct {
	By       string `json:"by"`
	Accepted bool   `json:"accepted"`
}

func EncodeCommand(command game.Command) ([]byte, error) {
	switch c := command.(type) {
	case game.PlayCommand:
		return wrap("play", playPayload{
			By: c.By.String(), Move: encodeMove(c.Move), ClientMoveID: c.ClientMoveID,
			ExpectedMoveNumber: c.ExpectedMoveNumber, SkipExpectedCheck: c.SkipExpectedCheck,
		})
	case game.RequestUndoCommand:
		return wrap("undo_request", byPayload{By: c.By.String()})
	case game.RespondUndoCommand:
		return wrap("undo_response", undoResponsePayload{By: c.By.String(), Accept: c.Accept})
	case game.MarkDeadCommand:
		return wrap("mark_dead", markDeadPayload{
			By: c.By.String(), Point: pointPayload{Col: c.Point.Col, Row: c.Point.Row}})
	case game.AcceptScoreCommand:
		return wrap("scoring_accept", acceptScorePayload{By: c.By.String(), Accepted: c.Accepted})
	case game.ResumePlayCommand:
		return wrap("resume_play", byPayload{By: c.By.String()})
	case game.TimeoutCommand:
		return wrap("timeout", byPayload{By: c.Player.String()})
	default:
		return nil, fmt.Errorf("wire: cannot encode command %T", command)
	}
}

func DecodeCommand(data []byte) (game.Command, error) {
	var outer envelope
	if err := json.Unmarshal(data, &outer); err != nil {
		return nil, fmt.Errorf("wire: decoding command: %w", err)
	}
	switch outer.Type {
	case "play":
		var p playPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		move, err := p.Move.decode()
		if err != nil {
			return nil, err
		}
		return game.PlayCommand{
			By: colour(p.By), Move: move, ClientMoveID: p.ClientMoveID,
			ExpectedMoveNumber: p.ExpectedMoveNumber, SkipExpectedCheck: p.SkipExpectedCheck,
		}, nil
	case "undo_request":
		var p byPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.RequestUndoCommand{By: colour(p.By)}, nil
	case "undo_response":
		var p undoResponsePayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.RespondUndoCommand{By: colour(p.By), Accept: p.Accept}, nil
	case "mark_dead":
		var p markDeadPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.MarkDeadCommand{
			By: colour(p.By), Point: rules.Point{Col: p.Point.Col, Row: p.Point.Row}}, nil
	case "scoring_accept":
		var p acceptScorePayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.AcceptScoreCommand{By: colour(p.By), Accepted: p.Accepted}, nil
	case "resume_play":
		var p byPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.ResumePlayCommand{By: colour(p.By)}, nil
	case "timeout":
		var p byPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.TimeoutCommand{Player: colour(p.By)}, nil
	default:
		return nil, fmt.Errorf("wire: unknown command type %q", outer.Type)
	}
}

// ── events ──────────────────────────────────────────────────────────────────

type moveMadePayload struct {
	MoveNumber int            `json:"move_number"`
	By         string         `json:"by"`
	Move       movePayload    `json:"move"`
	Captured   []pointPayload `json:"captured,omitempty"`
	BoardHash  string         `json:"board_hash"`
	Clock      game.Clock     `json:"clock"`
	Duplicate  bool           `json:"duplicate,omitempty"`
}

type scoringOpenedPayload struct {
	Suggested            []pointPayload `json:"suggested"`
	ResumeFromMoveNumber int            `json:"resume_from_move_number"`
}

type scoringChangedPayload struct {
	Dead          []pointPayload `json:"dead"`
	BlackAccepted bool           `json:"black_accepted"`
	WhiteAccepted bool           `json:"white_accepted"`
	Score         rules.Score    `json:"score"`
}

type playResumedPayload struct {
	MoveNumber int    `json:"move_number"`
	ToPlay     string `json:"to_play"`
}

type undoResolvedPayload struct {
	By         string `json:"by"`
	Accepted   bool   `json:"accepted"`
	MoveNumber int    `json:"move_number"`
}

type gameEndedPayload struct {
	Winner string       `json:"winner"`
	Reason string       `json:"reason"`
	Score  *rules.Score `json:"score,omitempty"`
}

type clockAdjustedPayload struct {
	Player  string `json:"player"`
	DeltaMs int64  `json:"delta_ms"`
	Reason  string `json:"reason"`
}

type chatSaidPayload struct {
	By   string `json:"by"`
	Code string `json:"code"`
}

func EncodeEvent(event game.Event) ([]byte, error) {
	switch e := event.(type) {
	case game.MoveMade:
		return wrap("move_made", moveMadePayload{
			MoveNumber: e.MoveNumber, By: e.By.String(), Move: encodeMove(e.Move),
			Captured: encodePoints(e.Captured), BoardHash: fmt.Sprintf("0x%016x", e.BoardHash),
			Clock: e.Clock, Duplicate: e.Duplicate,
		})
	case game.ScoringOpened:
		return wrap("scoring_opened", scoringOpenedPayload{
			Suggested: encodePoints(e.Suggested), ResumeFromMoveNumber: e.ResumeFromMoveNumber})
	case game.ScoringChanged:
		return wrap("scoring_changed", scoringChangedPayload{
			Dead: encodePoints(e.Dead), BlackAccepted: e.BlackAccepted,
			WhiteAccepted: e.WhiteAccepted, Score: e.Score})
	case game.PlayResumed:
		return wrap("play_resumed", playResumedPayload{
			MoveNumber: e.MoveNumber, ToPlay: e.ToPlay.String()})
	case game.UndoRequested:
		return wrap("undo_requested", byPayload{By: e.By.String()})
	case game.UndoResolved:
		return wrap("undo_resolved", undoResolvedPayload{
			By: e.By.String(), Accepted: e.Accepted, MoveNumber: e.MoveNumber})
	case game.GameEnded:
		return wrap("game_ended", gameEndedPayload{
			Winner: e.Result.Winner.String(), Reason: string(e.Result.Reason), Score: e.Result.Score})
	case game.ClockAdjusted:
		return wrap("clock_adjusted", clockAdjustedPayload{
			Player: e.Player.String(), DeltaMs: e.Delta.Milliseconds(), Reason: e.Reason})
	case game.ChatSaid:
		return wrap("chat_said", chatSaidPayload{By: e.By.String(), Code: e.Code})
	default:
		return nil, fmt.Errorf("wire: cannot encode event %T", event)
	}
}

func DecodeEvent(data []byte) (game.Event, error) {
	var outer envelope
	if err := json.Unmarshal(data, &outer); err != nil {
		return nil, fmt.Errorf("wire: decoding event: %w", err)
	}
	switch outer.Type {
	case "move_made":
		var p moveMadePayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		move, err := p.Move.decode()
		if err != nil {
			return nil, err
		}
		// ParseUint with base 0 reads the 0x prefix, and unlike Sscanf it rejects
		// anything that is not actually a number.
		hash, err := strconv.ParseUint(p.BoardHash, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("wire: bad board hash %q: %w", p.BoardHash, err)
		}
		return game.MoveMade{
			MoveNumber: p.MoveNumber, By: colour(p.By), Move: move,
			Captured: decodePoints(p.Captured), BoardHash: hash,
			Clock: p.Clock, Duplicate: p.Duplicate,
		}, nil
	case "scoring_opened":
		var p scoringOpenedPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.ScoringOpened{
			Suggested: decodePoints(p.Suggested), ResumeFromMoveNumber: p.ResumeFromMoveNumber}, nil
	case "scoring_changed":
		var p scoringChangedPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.ScoringChanged{
			Dead: decodePoints(p.Dead), BlackAccepted: p.BlackAccepted,
			WhiteAccepted: p.WhiteAccepted, Score: p.Score}, nil
	case "play_resumed":
		var p playResumedPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.PlayResumed{MoveNumber: p.MoveNumber, ToPlay: colour(p.ToPlay)}, nil
	case "undo_requested":
		var p byPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.UndoRequested{By: colour(p.By)}, nil
	case "undo_resolved":
		var p undoResolvedPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.UndoResolved{By: colour(p.By), Accepted: p.Accepted, MoveNumber: p.MoveNumber}, nil
	case "game_ended":
		var p gameEndedPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.GameEnded{Result: rules.Result{
			Winner: colourOrEmpty(p.Winner), Reason: rules.EndReason(p.Reason), Score: p.Score}}, nil
	case "clock_adjusted":
		var p clockAdjustedPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.ClockAdjusted{
			Player: colour(p.Player), Delta: time.Duration(p.DeltaMs) * time.Millisecond,
			Reason: p.Reason}, nil
	case "chat_said":
		var p chatSaidPayload
		if err := json.Unmarshal(outer.Data, &p); err != nil {
			return nil, err
		}
		return game.ChatSaid{By: colour(p.By), Code: p.Code}, nil
	default:
		return nil, fmt.Errorf("wire: unknown event type %q", outer.Type)
	}
}

func colour(name string) rules.Color {
	if name == "white" {
		return rules.White
	}
	return rules.Black
}

// colourOrEmpty keeps "no winner" distinct from black, which matters for a void game.
func colourOrEmpty(name string) rules.Color {
	switch name {
	case "black":
		return rules.Black
	case "white":
		return rules.White
	default:
		return rules.Empty
	}
}

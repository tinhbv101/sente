package rules

import (
	"fmt"
	"strconv"
	"strings"
)

type RecordedMove struct {
	Player Color
	Move   Move
}

// GameRecord is everything needed to write a game out and read it back.
// Deliberately flat: a record is transport, not a live game.
type GameRecord struct {
	Size           int
	Rules          RuleSet
	Komi           float64
	Handicap       int
	HandicapStones []Point
	BlackPlayer    string
	WhitePlayer    string
	Date           string
	TimeControl    string
	Result         *Result
	Moves          []RecordedMove
}

const sgfApplication = "Sente:1.0"

// EncodeSGF writes FF[4] for the main line of a game (docs/02 §9).
func EncodeSGF(r GameRecord) string {
	var root strings.Builder
	root.WriteString("GM[1]FF[4]CA[UTF-8]AP[" + sgfApplication + "]")
	root.WriteString(fmt.Sprintf("SZ[%d]", r.Size))
	root.WriteString("KM[" + FormatPoints(r.Komi) + "]")
	if r.Rules == Chinese {
		root.WriteString("RU[Chinese]")
	} else {
		root.WriteString("RU[Japanese]")
	}
	if r.BlackPlayer != "" {
		root.WriteString("PB[" + escapeSGF(r.BlackPlayer) + "]")
	}
	if r.WhitePlayer != "" {
		root.WriteString("PW[" + escapeSGF(r.WhitePlayer) + "]")
	}
	if r.Date != "" {
		root.WriteString("DT[" + escapeSGF(r.Date) + "]")
	}
	if r.TimeControl != "" {
		root.WriteString("TM[" + escapeSGF(r.TimeControl) + "]")
	}
	if r.Handicap > 0 {
		root.WriteString(fmt.Sprintf("HA[%d]AB", r.Handicap))
		for _, p := range r.HandicapStones {
			root.WriteString("[" + SGFText(p) + "]")
		}
	}
	if r.Result != nil {
		root.WriteString("RE[" + sgfResultText(*r.Result) + "]")
	}

	var body strings.Builder
	for _, m := range r.Moves {
		tag := "B"
		if m.Player == White {
			tag = "W"
		}
		if m.Move.Kind == KindPlay {
			body.WriteString(";" + tag + "[" + SGFText(m.Move.Point) + "]")
		} else {
			// An empty value is the FF[4] spelling of a pass.
			body.WriteString(";" + tag + "[]")
		}
	}
	return "(;" + root.String() + body.String() + ")"
}

func sgfResultText(r Result) string {
	winner := "W"
	if r.Winner == Black {
		winner = "B"
	}
	switch r.Reason {
	case ReasonRepetition:
		return "Void"
	case ReasonResignation:
		return winner + "+R"
	case ReasonTimeout:
		return winner + "+T"
	case ReasonAbandonment:
		return winner + "+F"
	case ReasonMutualDraw:
		return "0"
	default:
		if r.Score == nil || r.Winner == Empty {
			return "0"
		}
		return winner + "+" + FormatPoints(r.Score.Margin())
	}
}

// FormatPoints renders a score. Points are always multiples of a half, so one
// decimal is exact and whole numbers lose the decimal entirely.
func FormatPoints(v float64) string {
	if v == float64(int(v)) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func escapeSGF(text string) string {
	return strings.NewReplacer("\\", "\\\\", "]", "\\]").Replace(text)
}

// DecodeSGF reads the main line of a game. Variations are skipped: Sente records
// one line of play per game.
func DecodeSGF(text string) (GameRecord, error) {
	nodes, err := parseSGFNodes(text)
	if err != nil {
		return GameRecord{}, err
	}
	if len(nodes) == 0 {
		return GameRecord{}, fmt.Errorf("sgf: no root node")
	}
	root := nodes[0]

	size := 19
	if raw, ok := first(root, "SZ"); ok {
		if parsed, err := strconv.Atoi(raw); err == nil {
			size = parsed
		}
	}
	if !IsSupportedSize(size) {
		return GameRecord{}, fmt.Errorf("sgf: unsupported board size %d", size)
	}

	record := GameRecord{Size: size, Rules: Japanese, Komi: 6.5}
	if raw, ok := first(root, "KM"); ok {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			record.Komi = parsed
		}
	}
	if raw, ok := first(root, "RU"); ok && strings.HasPrefix(strings.ToLower(raw), "chinese") {
		record.Rules = Chinese
	}
	record.BlackPlayer, _ = first(root, "PB")
	record.WhitePlayer, _ = first(root, "PW")
	record.Date, _ = first(root, "DT")
	record.TimeControl, _ = first(root, "TM")
	if raw, ok := first(root, "HA"); ok {
		record.Handicap, _ = strconv.Atoi(raw)
	}
	for _, raw := range root["AB"] {
		if p, ok := ParseSGFPoint(raw, size); ok {
			record.HandicapStones = append(record.HandicapStones, p)
		}
	}

	for _, node := range nodes[1:] {
		for _, pair := range []struct {
			tag    string
			player Color
		}{{"B", Black}, {"W", White}} {
			values, ok := node[pair.tag]
			if !ok || len(values) == 0 {
				continue
			}
			raw := values[0]
			// "" and the legacy "tt" both mean pass.
			if raw == "" || (raw == "tt" && size <= 19) {
				record.Moves = append(record.Moves, RecordedMove{Player: pair.player, Move: Pass})
				break
			}
			if p, ok := ParseSGFPoint(raw, size); ok {
				record.Moves = append(record.Moves, RecordedMove{Player: pair.player, Move: Play(p)})
			}
			break
		}
	}
	return record, nil
}

func first(node map[string][]string, key string) (string, bool) {
	if values, ok := node[key]; ok && len(values) > 0 {
		return values[0], true
	}
	return "", false
}

// parseSGFNodes is a minimal scanner: property identifiers followed by bracketed
// values, nodes separated by ';'. A nested '(' starts a variation, ending the main line.
func parseSGFNodes(text string) ([]map[string][]string, error) {
	runes := []rune(text)
	i := 0
	skipSpace := func() {
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\n' || runes[i] == '\t' || runes[i] == '\r') {
			i++
		}
	}

	skipSpace()
	if i >= len(runes) || runes[i] != '(' {
		return nil, fmt.Errorf("sgf: expected '('")
	}
	i++

	var nodes []map[string][]string
	current := map[string][]string{}
	started := false

	for i < len(runes) {
		c := runes[i]
		switch {
		case c == ' ' || c == '\n' || c == '\t' || c == '\r':
			i++
			continue
		case c == ')' || c == '(':
			i = len(runes)
			continue
		case c == ';':
			if started {
				nodes = append(nodes, current)
			}
			current = map[string][]string{}
			started = true
			i++
			continue
		case c < 'A' || c > 'Z':
			i++
			continue
		}

		var identifier strings.Builder
		for i < len(runes) && runes[i] >= 'A' && runes[i] <= 'Z' {
			identifier.WriteRune(runes[i])
			i++
		}
		skipSpace()
		for i < len(runes) && runes[i] == '[' {
			i++
			var value strings.Builder
			for i < len(runes) && runes[i] != ']' {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
				}
				value.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				return nil, fmt.Errorf("sgf: unterminated property value")
			}
			i++
			current[identifier.String()] = append(current[identifier.String()], value.String())
			skipSpace()
		}
	}
	if started {
		nodes = append(nodes, current)
	}
	return nodes, nil
}

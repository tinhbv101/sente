package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Runs the shared conformance vectors. GoKit runs the exact same files; if the two
// suites ever disagree, the engines have drifted (docs/03 ADR-002).

type vectorSetup struct {
	Black []string `json:"black"`
	White []string `json:"white"`
}

type vectorResult struct {
	Winner *string `json:"winner"`
	Reason string  `json:"reason"`
}

type vectorExpect struct {
	Legal                *bool               `json:"legal"`
	Reason               *string             `json:"reason"`
	Captured             []string            `json:"captured"`
	CapturesAfter        map[string]int      `json:"captures_after"`
	KoPoint              *string             `json:"ko_point"`
	KoPointAbsent        *bool               `json:"ko_point_absent"`
	Phase                *string             `json:"phase"`
	Result               *vectorResult       `json:"result"`
	Territory            map[string]int      `json:"territory"`
	Neutral              *int                `json:"neutral"`
	Area                 map[string]int      `json:"area"`
	Score                map[string]float64  `json:"score"`
	Winner               *string             `json:"winner"`
	Margin               *float64            `json:"margin"`
	PassAlive            map[string][]string `json:"pass_alive"`
	HandicapStones       []string            `json:"handicap_stones"`
	ToPlay               *string             `json:"to_play"`
	Komi                 *float64            `json:"komi"`
	MoveNumber           *int                `json:"move_number"`
	HistoryContainsStart *bool               `json:"history_contains_start"`
	HistorySize          *int                `json:"history_size"`
	SGFContains          []string            `json:"sgf_contains"`
	RoundTrip            *bool               `json:"round_trip"`
}

type vector struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Rules       string         `json:"rules"`
	BoardSize   *int           `json:"board_size"`
	Komi        *float64       `json:"komi"`
	Handicap    int            `json:"handicap"`
	Setup       *vectorSetup   `json:"setup"`
	ToPlay      string         `json:"to_play"`
	Moves       []string       `json:"moves"`
	Move        *string        `json:"move"`
	MoveBy      *string        `json:"move_by"`
	Captures    map[string]int `json:"captures"`
	DeadStones  []string       `json:"dead_stones"`
	Expect      vectorExpect   `json:"expect"`

	group string
}

// specRoot walks up from this source file until it finds the shared spec, so both
// engines provably read the same directory.
func specRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		dir = filepath.Dir(dir)
		candidate := filepath.Join(dir, "rules-spec")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	t.Fatalf("rules-spec not found above %s", file)
	return ""
}

func loadVectors(t *testing.T, group string) []vector {
	t.Helper()
	dir := filepath.Join(specRoot(t), "vectors", group)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var vectors []vector
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		var v vector
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("parsing %s: %v", entry.Name(), err)
		}
		v.group = group
		vectors = append(vectors, v)
	}
	sort.Slice(vectors, func(i, j int) bool { return vectors[i].ID < vectors[j].ID })
	return vectors
}

var vectorGroups = []string{
	"legality", "capture", "suicide", "ko", "superko",
	"life_death", "scoring", "handicap", "sgf",
}

func TestConformance(t *testing.T) {
	for _, group := range vectorGroups {
		vectors := loadVectors(t, group)
		if len(vectors) == 0 {
			t.Fatalf("group %s has no vectors", group)
		}
		for _, v := range vectors {
			t.Run(group+"/"+v.ID, func(t *testing.T) { runVector(t, v) })
		}
	}
}

func TestEveryVectorGroupIsCovered(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(specRoot(t), "vectors"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, g := range vectorGroups {
		covered[g] = true
	}
	for _, entry := range entries {
		if entry.IsDir() && !covered[entry.Name()] {
			t.Errorf("vector group %q has no test", entry.Name())
		}
	}
}

func colourFrom(name string) Color {
	if name == "white" {
		return White
	}
	return Black
}

func runVector(t *testing.T, v vector) {
	t.Helper()
	size := 19
	if v.BoardSize != nil {
		size = *v.BoardSize
	}
	ruleSet := Japanese
	if v.Rules == "chinese" {
		ruleSet = Chinese
	}

	point := func(text string) Point {
		// An unparseable coordinate is deliberately mapped off the board so the
		// engine -- not the parser -- decides that it is out of bounds.
		if p, ok := ParseCoordinate(text, size); ok {
			return p
		}
		return Point{Col: size, Row: size}
	}
	points := func(list []string) []Point {
		out := make([]Point, 0, len(list))
		for _, text := range list {
			out = append(out, point(text))
		}
		return out
	}
	names := func(list []Point) []string {
		out := make([]string, 0, len(list))
		for _, p := range list {
			out = append(out, CoordinateText(p, size))
		}
		sort.Strings(out)
		return out
	}
	upperSorted := func(list []string) []string {
		out := make([]string, 0, len(list))
		for _, s := range list {
			out = append(out, strings.ToUpper(s))
		}
		sort.Strings(out)
		return out
	}

	var engine Engine
	if v.Handicap > 0 {
		komi := DefaultKomi(ruleSet, v.Handicap)
		if v.Komi != nil {
			komi = *v.Komi
		}
		engine = NewGame(size, ruleSet, komi, v.Handicap)
	} else {
		opts := PositionOptions{Size: size, ToPlay: colourFrom(v.ToPlay), Rules: ruleSet}
		if v.Setup != nil {
			opts.Black = points(v.Setup.Black)
			opts.White = points(v.Setup.White)
		}
		if v.Komi != nil {
			opts.Komi = *v.Komi
		}
		if v.Captures != nil {
			opts.Captures = Captures{Black: v.Captures["black"], White: v.Captures["white"]}
		}
		engine = Position(opts)
	}
	startHash := engine.State.BoardHash

	// Pre-moves run before the assertion; any failure here is a broken vector.
	for _, encoded := range v.Moves {
		player, move, ok := parseShorthand(encoded, size)
		if !ok {
			t.Fatalf("cannot parse pre-move %q", encoded)
		}
		next, err := engine.ApplyBy(move, player)
		if err != nil {
			t.Fatalf("pre-move %q rejected: %v", encoded, err)
		}
		engine = next
	}

	expect := v.Expect

	if v.Move != nil {
		mover := engine.ToPlay()
		if v.MoveBy != nil {
			mover = colourFrom(*v.MoveBy)
		}
		var move Move
		switch *v.Move {
		case "pass":
			move = Pass
		case "resign":
			move = Resign
		default:
			move = Play(point(*v.Move))
		}

		err := engine.ValidateBy(move, mover)
		if expect.Legal != nil && (err == nil) != *expect.Legal {
			t.Fatalf("legality: want legal=%v, got err=%v", *expect.Legal, err)
		}
		if expect.Reason != nil {
			if err == nil {
				t.Fatalf("expected rejection %q but the move was accepted", *expect.Reason)
			}
			if got := err.Error(); got != *expect.Reason {
				t.Fatalf("reason: want %q, got %q", *expect.Reason, got)
			}
		}
		if expect.Legal != nil && !*expect.Legal {
			return
		}

		before := engine
		after, err := engine.ApplyBy(move, mover)
		if err != nil {
			t.Fatalf("apply returned %v", err)
		}
		if !before.State.Board.Equal(engine.State.Board) || before.State.MoveNumber != engine.State.MoveNumber {
			t.Fatal("apply mutated the receiver")
		}

		if expect.Captured != nil {
			var removed []Point
			for _, p := range before.State.Board.AllPoints() {
				if before.State.Board.At(p) != Empty && after.State.Board.At(p) == Empty {
					removed = append(removed, p)
				}
			}
			assertStrings(t, "captured stones", names(removed), upperSorted(expect.Captured))
		}
		if expect.CapturesAfter != nil {
			if after.State.Captures.Black != expect.CapturesAfter["black"] {
				t.Errorf("black prisoners: want %d, got %d", expect.CapturesAfter["black"], after.State.Captures.Black)
			}
			if after.State.Captures.White != expect.CapturesAfter["white"] {
				t.Errorf("white prisoners: want %d, got %d", expect.CapturesAfter["white"], after.State.Captures.White)
			}
		}
		if expect.KoPoint != nil {
			if after.State.KoPoint == nil {
				t.Errorf("ko point: want %s, got none", *expect.KoPoint)
			} else if got := CoordinateText(*after.State.KoPoint, size); got != strings.ToUpper(*expect.KoPoint) {
				t.Errorf("ko point: want %s, got %s", strings.ToUpper(*expect.KoPoint), got)
			}
		}
		if expect.KoPointAbsent != nil && *expect.KoPointAbsent && after.State.KoPoint != nil {
			t.Errorf("expected no ko point, got %v", *after.State.KoPoint)
		}
		if expect.Phase != nil && string(after.State.Phase) != *expect.Phase {
			t.Errorf("phase: want %s, got %s", *expect.Phase, after.State.Phase)
		}
		if expect.Result != nil {
			if after.State.Result == nil {
				t.Fatalf("expected a result, got none")
			}
			var gotWinner *string
			if after.State.Result.Winner != Empty {
				name := after.State.Result.Winner.String()
				gotWinner = &name
			}
			assertOptionalString(t, "winner", gotWinner, expect.Result.Winner)
			if string(after.State.Result.Reason) != expect.Result.Reason {
				t.Errorf("end reason: want %s, got %s", expect.Result.Reason, after.State.Result.Reason)
			}
		}
		engine = after
	}

	dead := points(v.DeadStones)

	if expect.Territory != nil {
		territory := engine.Territory(dead)
		if len(territory.Black) != expect.Territory["black"] {
			t.Errorf("black territory: want %d, got %d", expect.Territory["black"], len(territory.Black))
		}
		if len(territory.White) != expect.Territory["white"] {
			t.Errorf("white territory: want %d, got %d", expect.Territory["white"], len(territory.White))
		}
	}
	if expect.Neutral != nil {
		if got := len(engine.Territory(dead).Neutral); got != *expect.Neutral {
			t.Errorf("neutral points: want %d, got %d", *expect.Neutral, got)
		}
	}
	if expect.Area != nil {
		score := engine.Score(dead)
		if score.BlackSide().Area != expect.Area["black"] {
			t.Errorf("black area: want %d, got %d", expect.Area["black"], score.BlackSide().Area)
		}
		if score.WhiteSide().Area != expect.Area["white"] {
			t.Errorf("white area: want %d, got %d", expect.Area["white"], score.WhiteSide().Area)
		}
	}
	if expect.Score != nil {
		score := engine.Score(dead)
		if score.Black != expect.Score["black"] {
			t.Errorf("black score: want %v, got %v", expect.Score["black"], score.Black)
		}
		if score.White != expect.Score["white"] {
			t.Errorf("white score: want %v, got %v", expect.Score["white"], score.White)
		}
	}
	if expect.Winner != nil {
		if got := engine.Score(dead).Winner().String(); got != *expect.Winner {
			t.Errorf("score winner: want %s, got %s", *expect.Winner, got)
		}
	}
	if expect.Margin != nil {
		if got := engine.Score(dead).Margin(); got != *expect.Margin {
			t.Errorf("margin: want %v, got %v", *expect.Margin, got)
		}
	}
	if expect.PassAlive != nil {
		for _, colour := range []Color{Black, White} {
			var flat []Point
			for _, chain := range engine.State.Board.PassAliveChains(colour) {
				flat = append(flat, chain...)
			}
			assertStrings(t, colour.String()+" pass-alive", names(flat), upperSorted(expect.PassAlive[colour.String()]))
		}
	}
	if expect.HandicapStones != nil {
		assertStrings(t, "handicap placement", names(engine.State.Board.Stones(Black)), upperSorted(expect.HandicapStones))
	}
	if expect.ToPlay != nil && engine.ToPlay().String() != *expect.ToPlay {
		t.Errorf("to play: want %s, got %s", *expect.ToPlay, engine.ToPlay())
	}
	if expect.Komi != nil && engine.Komi != *expect.Komi {
		t.Errorf("komi: want %v, got %v", *expect.Komi, engine.Komi)
	}
	if expect.MoveNumber != nil && engine.State.MoveNumber != *expect.MoveNumber {
		t.Errorf("move number: want %d, got %d", *expect.MoveNumber, engine.State.MoveNumber)
	}
	if expect.HistoryContainsStart != nil && *expect.HistoryContainsStart && !engine.HistoryContains(startHash) {
		t.Error("starting position missing from history")
	}
	if expect.HistorySize != nil && engine.HistorySize() != *expect.HistorySize {
		t.Errorf("history size: want %d, got %d", *expect.HistorySize, engine.HistorySize())
	}

	if expect.SGFContains != nil || (expect.RoundTrip != nil && *expect.RoundTrip) {
		runSGFVector(t, v, size, ruleSet)
	}
}

func runSGFVector(t *testing.T, v vector, size int, ruleSet RuleSet) {
	t.Helper()
	komi := DefaultKomi(ruleSet, v.Handicap)
	if v.Komi != nil {
		komi = *v.Komi
	}
	record := GameRecord{
		Size:           size,
		Rules:          ruleSet,
		Komi:           komi,
		Handicap:       v.Handicap,
		HandicapStones: HandicapStones(v.Handicap, size),
		BlackPlayer:    "an",
		WhitePlayer:    "binh",
	}
	for _, encoded := range v.Moves {
		player, move, ok := parseShorthand(encoded, size)
		if !ok {
			continue
		}
		record.Moves = append(record.Moves, RecordedMove{Player: player, Move: move})
	}

	text := EncodeSGF(record)
	for _, fragment := range v.Expect.SGFContains {
		if !strings.Contains(text, fragment) {
			t.Errorf("SGF missing %q in %s", fragment, text)
		}
	}
	if v.Expect.RoundTrip == nil || !*v.Expect.RoundTrip {
		return
	}
	decoded, err := DecodeSGF(text)
	if err != nil {
		t.Fatalf("SGF decode failed: %v", err)
	}
	if decoded.Size != record.Size || decoded.Komi != record.Komi || decoded.Rules != record.Rules {
		t.Errorf("round trip changed the header: %+v", decoded)
	}
	if decoded.Handicap != record.Handicap || len(decoded.HandicapStones) != len(record.HandicapStones) {
		t.Errorf("round trip changed the handicap: %+v", decoded)
	}
	if len(decoded.Moves) != len(record.Moves) {
		t.Fatalf("round trip changed the move count: want %d, got %d", len(record.Moves), len(decoded.Moves))
	}
	for i, m := range record.Moves {
		if decoded.Moves[i] != m {
			t.Errorf("move %d: want %+v, got %+v", i, m, decoded.Moves[i])
		}
	}
}

// parseShorthand reads the "B:e5" / "W:pass" form used for pre-moves.
func parseShorthand(encoded string, size int) (Color, Move, bool) {
	parts := strings.SplitN(encoded, ":", 2)
	if len(parts) != 2 {
		return Empty, Move{}, false
	}
	player := White
	if strings.EqualFold(parts[0], "B") {
		player = Black
	}
	switch strings.ToLower(parts[1]) {
	case "pass":
		return player, Pass, true
	case "resign":
		return player, Resign, true
	}
	p, ok := ParseCoordinate(parts[1], size)
	if !ok {
		return Empty, Move{}, false
	}
	return player, Play(p), true
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: want %v, got %v", what, want, got)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: want %v, got %v", what, want, got)
			return
		}
	}
}

func assertOptionalString(t *testing.T, what string, got, want *string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s: want %v, got %v", what, deref(want), deref(got))
	case *got != *want:
		t.Errorf("%s: want %s, got %s", what, *want, *got)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", *s)
}

package rules

import (
	"strings"
	"testing"
)

func baseRecord(result *Result) GameRecord {
	return GameRecord{Size: 19, Rules: Japanese, Komi: 6.5,
		BlackPlayer: "an", WhitePlayer: "binh", Result: result}
}

func TestResultEncodingForEveryEnding(t *testing.T) {
	counting := NewGame(9, Japanese, 6.5, 0).Score(nil)
	cases := []struct {
		result *Result
		want   string
	}{
		{&Result{Winner: Black, Reason: ReasonResignation}, "RE[B+R]"},
		{&Result{Winner: White, Reason: ReasonTimeout}, "RE[W+T]"},
		{&Result{Winner: Black, Reason: ReasonAbandonment}, "RE[B+F]"},
		{&Result{Winner: Empty, Reason: ReasonMutualDraw}, "RE[0]"},
		{&Result{Winner: Empty, Reason: ReasonRepetition}, "RE[Void]"},
		{&Result{Winner: White, Reason: ReasonCounting, Score: &counting}, "RE[W+6.5]"},
		// Counting without a score is not something the engine produces, but the
		// encoder must not invent a winner if it ever sees one.
		{&Result{Winner: White, Reason: ReasonCounting}, "RE[0]"},
	}
	for _, c := range cases {
		if got := EncodeSGF(baseRecord(c.result)); !strings.Contains(got, c.want) {
			t.Errorf("want %s in %s", c.want, got)
		}
	}
	if strings.Contains(EncodeSGF(baseRecord(nil)), "RE[") {
		t.Error("a game with no result must not write RE")
	}
}

func TestFormatPointsDropsWholeDecimals(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{{0, "0"}, {7, "7"}, {6.5, "6.5"}, {0.5, "0.5"}} {
		if got := FormatPoints(c.in); got != c.want {
			t.Errorf("FormatPoints(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPlayerNamesAreEscaped(t *testing.T) {
	record := baseRecord(nil)
	record.BlackPlayer = "an]nguy[en"
	text := EncodeSGF(record)
	if !strings.Contains(text, `PB[an\]nguy[en]`) {
		t.Fatalf("name not escaped: %s", text)
	}
	decoded, err := DecodeSGF(text)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.BlackPlayer != "an]nguy[en" {
		t.Errorf("round trip lost the escape: %q", decoded.BlackPlayer)
	}
}

func TestOptionalRootPropertiesRoundTrip(t *testing.T) {
	record := baseRecord(nil)
	record.Date = "2026-08-28"
	record.TimeControl = "1200"
	decoded, err := DecodeSGF(EncodeSGF(record))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Date != "2026-08-28" || decoded.TimeControl != "1200" {
		t.Errorf("optional properties lost: %+v", decoded)
	}
}

func TestLegacyTTMeansPass(t *testing.T) {
	decoded, err := DecodeSGF("(;GM[1]FF[4]SZ[19];B[tt];W[pd])")
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Moves) != 2 || decoded.Moves[0].Move.Kind != KindPass {
		t.Fatalf("tt should decode as a pass: %+v", decoded.Moves)
	}
	if decoded.Moves[1].Move.Point != (Point{Col: 15, Row: 3}) {
		t.Errorf("pd should be Q16: %+v", decoded.Moves[1])
	}
}

func TestMissingPropertiesFallBackToDefaults(t *testing.T) {
	decoded, err := DecodeSGF("(;GM[1]FF[4];B[pd])")
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Size != 19 || decoded.Komi != 6.5 || decoded.Rules != Japanese || decoded.Handicap != 0 {
		t.Errorf("bad defaults: %+v", decoded)
	}
}

func TestChineseRulesAreRecognised(t *testing.T) {
	for _, c := range []struct {
		text string
		want RuleSet
	}{{"(;SZ[9]RU[Chinese])", Chinese}, {"(;SZ[9]RU[Japanese])", Japanese}} {
		decoded, err := DecodeSGF(c.text)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Rules != c.want {
			t.Errorf("%s: want %s, got %s", c.text, c.want, decoded.Rules)
		}
	}
}

func TestVariationsAreIgnored(t *testing.T) {
	// Sente records one line of play; a branch ends the main line.
	decoded, err := DecodeSGF("(;SZ[9];B[cc];W[dd](;B[ee])(;B[ff]))")
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Moves) != 2 {
		t.Errorf("want 2 moves on the main line, got %d", len(decoded.Moves))
	}
}

func TestMalformedInputIsRejected(t *testing.T) {
	for _, text := range []string{"no parenthesis here", "(;SZ[9];B[cc", "(;SZ[11])"} {
		if _, err := DecodeSGF(text); err == nil {
			t.Errorf("expected an error for %q", text)
		}
	}
}

func TestUnparseableMoveIsDropped(t *testing.T) {
	// "zz" is off every supported board, so the node contributes no move.
	decoded, err := DecodeSGF("(;SZ[9];B[zz])")
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Moves) != 0 {
		t.Errorf("want no moves, got %d", len(decoded.Moves))
	}
}

func TestResignEncodesAsAnEmptyValue(t *testing.T) {
	record := baseRecord(nil)
	record.Moves = []RecordedMove{{Player: Black, Move: Resign}}
	if !strings.Contains(EncodeSGF(record), ";B[]") {
		t.Error("resign should encode as an empty value")
	}
}

func TestSGFPointRejectsBadInput(t *testing.T) {
	for _, text := range []string{"", "a", "abc", "zz"} {
		if _, ok := ParseSGFPoint(text, 19); ok {
			t.Errorf("%q should not parse", text)
		}
	}
}

package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

// Pure encoding helpers: no database needed, so these run in -short too.

func TestPointEncodingRoundTripsOnEverySize(t *testing.T) {
	for _, size := range rules.SupportedSizes {
		points := rules.NewBoard(size).AllPoints()
		back := decodePoints(encodePoints(points, size), size)
		if len(back) != len(points) {
			t.Fatalf("%dx%d: lost points", size, size)
		}
		for i := range points {
			if back[i] != points[i] {
				t.Fatalf("%dx%d: %v became %v", size, size, points[i], back[i])
			}
		}
	}
}

func TestMoveEncoding(t *testing.T) {
	point := rules.Point{Col: 4, Row: 2}
	cases := []struct {
		move rules.Move
		kind string
	}{
		{rules.Play(point), "play"},
		{rules.Pass, "pass"},
		{rules.Resign, "resign"},
	}
	for _, c := range cases {
		if got := moveKind(c.move); got != c.kind {
			t.Errorf("%v: want kind %q, got %q", c.move, c.kind, got)
		}
		encoded, err := encodePoint(c.move, 9)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeMove(c.kind, encoded, 9)
		if err != nil {
			t.Fatal(err)
		}
		if decoded != c.move {
			t.Errorf("round trip: want %v, got %v", c.move, decoded)
		}
	}
}

func TestDecodingRefusesCorruptRows(t *testing.T) {
	if _, err := decodeMove("teleport", nil, 9); err == nil {
		t.Error("an unknown move kind must be an error")
	}
	// A stored play with no point is a corrupt row, not a pass.
	if _, err := decodeMove("play", nil, 9); err == nil {
		t.Error("a play needs a point")
	}
}

func TestColourAndNullableHelpers(t *testing.T) {
	if colourFromString("white") != rules.White || colourFromString("black") != rules.Black {
		t.Error("colour parsing is wrong")
	}
	if nullable("") != nil || *nullable("x") != "x" {
		t.Error("nullable is wrong")
	}
	if deref(nil) != "" {
		t.Error("deref of nil should be empty")
	}
	value := "y"
	if deref(&value) != "y" {
		t.Error("deref is wrong")
	}
}

func TestKomiFallsBackToTheRulesDefault(t *testing.T) {
	if got := komiOf(game.Config{Rules: rules.Japanese}); got != 6.5 {
		t.Errorf("want the Japanese default, got %v", got)
	}
	if got := komiOf(game.Config{Rules: rules.Chinese}); got != 7.5 {
		t.Errorf("want the Chinese default, got %v", got)
	}
	if got := komiOf(game.Config{Rules: rules.Japanese, Komi: 0.5}); got != 0.5 {
		t.Errorf("an explicit komi must win, got %v", got)
	}
}

// Ids must sort by creation time so inserts land at the right-hand edge of the
// primary key index instead of scattering across it.
func TestIDsAreTimeOrderedAndWellFormed(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 200; i++ {
		ids = append(ids, newIDAt(base.Add(time.Duration(i)*time.Millisecond)))
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("ids do not sort chronologically at %d", i)
		}
	}
	for _, id := range ids {
		if len(id) != 36 || strings.Count(id, "-") != 4 {
			t.Fatalf("not a uuid: %q", id)
		}
		if id[14] != '7' {
			t.Fatalf("want version 7, got %q in %s", id[14], id)
		}
		if variant := id[19]; variant != '8' && variant != '9' && variant != 'a' && variant != 'b' {
			t.Fatalf("want the RFC 4122 variant, got %q in %s", variant, id)
		}
	}
	if NewID() == NewID() {
		t.Error("ids must not repeat")
	}
}

// The stored shape is read by the iOS client and by anyone looking at the JSONB
// column, so it is pinned here (docs/05 §5.1).
func TestTimeControlStoresMilliseconds(t *testing.T) {
	encoded, err := json.Marshal(game.TimeControl{
		Kind: game.Byoyomi, MainTime: 20 * time.Minute, Periods: 3,
		PeriodTime: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"byoyomi"`, `"main_time_ms":1200000`,
		`"periods":3`, `"period_time_ms":30000`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("want %s in %s", want, encoded)
		}
	}
	var back game.TimeControl
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if back.MainTime != 20*time.Minute || back.Periods != 3 || back.PeriodTime != 30*time.Second {
		t.Errorf("round trip lost detail: %+v", back)
	}
}

func TestCorrespondenceStoresWholeDays(t *testing.T) {
	encoded, err := json.Marshal(game.TimeControl{Kind: game.Correspondence, PerMove: 48 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"days_per_move":2`) {
		t.Errorf("want days_per_move, got %s", encoded)
	}
	var back game.TimeControl
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if back.PerMove != 48*time.Hour {
		t.Errorf("round trip lost the budget: %v", back.PerMove)
	}
	// Half a day has no representation in the stored shape, so encoding must fail
	// rather than silently round.
	if _, err := json.Marshal(game.TimeControl{Kind: game.Correspondence, PerMove: 36 * time.Hour}); err == nil {
		t.Error("a fractional day should be refused")
	}
}

func TestClockStoresMilliseconds(t *testing.T) {
	clock, err := game.NewClock(game.TimeControl{Kind: game.Byoyomi, MainTime: time.Minute,
		Periods: 2, PeriodTime: 30 * time.Second}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(clock)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"main_ms":60000`) ||
		!strings.Contains(string(encoded), `"periods_left":2`) {
		t.Errorf("unexpected clock shape: %s", encoded)
	}
	var back game.Clock
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if back.Black.Main != time.Minute || back.Black.PeriodsLeft != 2 {
		t.Errorf("round trip lost the clock: %+v", back.Black)
	}
}

func TestBadlyShapedStoredJSONIsRefused(t *testing.T) {
	var control game.TimeControl
	if err := json.Unmarshal([]byte(`{"kind":"hourglass"}`), &control); err == nil {
		t.Error("an unknown kind must be refused on load")
	}
	if err := json.Unmarshal([]byte(`{"kind":"byoyomi","main_time_ms":1000}`), &control); err == nil {
		t.Error("a byo-yomi with no periods must be refused on load")
	}
}

// ── error propagation ───────────────────────────────────────────────────────

func TestDatabaseErrorsArePropagated(t *testing.T) {
	games := newGames(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := games.Create(cancelled, CreateParams{Config: blitzConfig(), StartedAt: epoch}); err == nil {
		t.Error("Create must report a failed write")
	}
	if _, err := games.Load(cancelled, NewID()); err == nil {
		t.Error("Load must report a failed read")
	}
	if err := games.RecordEvent(cancelled, NewID(), 0, "kind", "", nil); err == nil {
		t.Error("RecordEvent must report a failed write")
	}
	session, _ := game.NewSession(blitzConfig(), epoch)
	if err := games.Finish(cancelled, NewID(), session, epoch); err == nil {
		t.Error("Finish must report a failed write")
	}
	if _, err := games.AppendMove(cancelled, NewID(), session, MoveRecord{}); err == nil {
		t.Error("AppendMove must report a failed write")
	}
}

func TestSavingScoringWithNoNegotiationIsRefused(t *testing.T) {
	games := newGames(t)
	session, _ := game.NewSession(blitzConfig(), epoch)
	if err := games.SaveScoring(context.Background(), NewID(), session); err == nil {
		t.Error("there is no negotiation to save")
	}
}

func TestCreateRejectsAnInvalidConfig(t *testing.T) {
	games := newGames(t)
	config := blitzConfig()
	config.TimeControl = game.TimeControl{Kind: game.Byoyomi, MainTime: time.Minute}
	if _, err := games.Create(context.Background(), CreateParams{Config: config, StartedAt: epoch}); err == nil {
		t.Error("a byo-yomi with no periods should never reach the database")
	}
}

func TestResignIsStoredAndReplayed(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, err := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := game.NewSession(blitzConfig(), epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Resign, "", epoch.Add(time.Second))

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	result := loaded.Session.Result()
	if result == nil || result.Reason != rules.ReasonResignation || result.Winner != rules.White {
		t.Errorf("resignation lost: %+v", result)
	}
}

func TestCorrespondenceGameRoundTrips(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	config := blitzConfig()
	config.TimeControl = game.TimeControl{Kind: game.Correspondence, PerMove: 48 * time.Hour}

	id, err := games.Create(ctx, CreateParams{Config: config, StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	var isCorrespondence bool
	if err := testPool.QueryRow(ctx, `SELECT is_correspondence FROM games WHERE id = $1`, id).
		Scan(&isCorrespondence); err != nil {
		t.Fatal(err)
	}
	if !isCorrespondence {
		t.Error("the sweeper finds correspondence games by this flag")
	}
	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Config.TimeControl.PerMove != 48*time.Hour {
		t.Errorf("budget lost: %v", loaded.Session.Config.TimeControl.PerMove)
	}
}

// The alphabet is enforced by a CHECK constraint, so a generator that drifts from
// it would fail at insert time rather than at review time.
func TestFriendCodesMatchTheStoredConstraint(t *testing.T) {
	skipIfShort(t)
	if len(friendCodeAlphabet) != 32 {
		t.Fatalf("want 32 symbols for 40 bits of entropy, got %d", len(friendCodeAlphabet))
	}
	for _, banned := range []string{"I", "O", "0", "1"} {
		if strings.Contains(friendCodeAlphabet, banned) {
			t.Errorf("%q is too easy to misread to be in a friend code", banned)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		code := NewFriendCode()
		if len(code) != 8 {
			t.Fatalf("want 8 characters, got %q", code)
		}
		seen[code] = true
		// The database is the real check: insert it and let the constraint judge.
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO users (id, display_name, friend_code) VALUES ($1, $2, $3)`,
			NewID(), "codecheck", code); err != nil {
			t.Fatalf("the constraint rejected %q: %v", code, err)
		}
	}
	if len(seen) < 495 {
		t.Errorf("only %d distinct codes in 500 draws", len(seen))
	}
}

func TestPlayersAreStoredAgainstTheGame(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	black, white := NewID(), NewID()
	for i, id := range []string{black, white} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO users (id, display_name, friend_code) VALUES ($1, $2, $3)`,
			id, "player", NewFriendCode()); err != nil {
			t.Fatalf("seeding user %d: %v", i, err)
		}
	}
	id, err := games.Create(ctx, CreateParams{
		Config: blitzConfig(), BlackUserID: black, WhiteUserID: white,
		IsRanked: true, StartedAt: epoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BlackUserID != black || loaded.WhiteUserID != white || !loaded.IsRanked {
		t.Errorf("player links lost: %+v", loaded)
	}
}

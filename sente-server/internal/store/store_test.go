package store

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

// These tests run against a real PostgreSQL. Mocking the database would hide
// exactly the bugs they exist to catch: constraint violations, transaction
// boundaries and the idempotency index (docs/09 §4.3).
//
// `go test -short` skips them. Anything else fails loudly if Docker is missing,
// because a silently skipped integration suite is worse than none.

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// testing.Short() reads a flag, so the flags have to be parsed first.
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("sente"),
		postgres.WithUsername("sente"),
		postgres.WithPassword("sente"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start postgres (is Docker running?): %v\n", err)
		os.Exit(1)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	if err := Migrate(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	_ = conn.Close(ctx)

	if testPool, err = pgxpool.New(ctx, dsn); err != nil {
		fmt.Fprintf(os.Stderr, "pool: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	testPool.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
}

var epoch = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

func blitzConfig() game.Config {
	return game.Config{
		Size: 9, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
		TimeControl: game.TimeControl{Kind: game.Absolute, MainTime: 10 * time.Minute},
	}
}

func newGames(t *testing.T) *Games {
	t.Helper()
	skipIfShort(t)
	return NewGames(testPool)
}

func coord(t *testing.T, text string) rules.Point {
	t.Helper()
	p, ok := rules.ParseCoordinate(text, 9)
	if !ok {
		t.Fatalf("bad coordinate %q", text)
	}
	return p
}

// playAndStore applies a move to the session and writes it, the way the game actor
// will once it exists.
func playAndStore(t *testing.T, games *Games, id string, session game.GameSession,
	by rules.Color, move rules.Move, clientMoveID string, at time.Time) (game.GameSession, bool) {
	t.Helper()
	next, _, err := session.Apply(game.PlayCommand{
		By: by, Move: move, ClientMoveID: clientMoveID,
		ExpectedMoveNumber: session.MoveNumber(),
	}, at)
	if err != nil {
		t.Fatalf("applying %v: %v", move, err)
	}
	captured := 0
	if next.Engine.State.Captures.Get(by) > session.Engine.State.Captures.Get(by) {
		captured = next.Engine.State.Captures.Get(by) - session.Engine.State.Captures.Get(by)
	}
	applied, err := games.AppendMove(context.Background(), id, next, MoveRecord{
		MoveNo: next.MoveNumber(), Color: by, Move: move, CapturedCount: captured,
		BoardHash: next.Engine.State.BoardHash, ClientMoveID: clientMoveID, PlayedAt: at,
	})
	if err != nil {
		t.Fatalf("storing %v: %v", move, err)
	}
	return next, applied
}

func TestCreateAndLoadRoundTrip(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()

	id, err := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Config.Size != 9 || loaded.Session.Config.Komi != 6.5 {
		t.Errorf("config lost in the round trip: %+v", loaded.Session.Config)
	}
	if loaded.Session.Config.TimeControl.MainTime != 10*time.Minute {
		t.Errorf("time control lost: %+v", loaded.Session.Config.TimeControl)
	}
	if loaded.Session.Clock.Black.Main != 10*time.Minute {
		t.Errorf("clock lost: %+v", loaded.Session.Clock.Black)
	}
	if loaded.Session.Phase() != rules.Playing || loaded.Session.ToPlay() != rules.Black {
		t.Errorf("wrong starting state: %v %v", loaded.Session.Phase(), loaded.Session.ToPlay())
	}
}

func TestLoadingAMissingGame(t *testing.T) {
	games := newGames(t)
	if _, err := games.Load(context.Background(), NewID()); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// The whole point of the append-only design: a game is whatever its moves say it
// is, and replaying them must reproduce the position exactly (docs/03 ADR-006).
func TestAWholeGameReplaysToTheSamePosition(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, err := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}

	session, err := game.NewSession(blitzConfig(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	// Black surrounds and takes a white stone, so captures and the ko point are
	// exercised too, not just plain placements.
	script := []struct {
		by   rules.Color
		text string
	}{
		{rules.Black, "a2"}, {rules.White, "a1"}, {rules.Black, "j9"}, {rules.White, "j8"},
		{rules.Black, "b1"}, {rules.White, "h9"}, {rules.Black, "e5"}, {rules.White, "e7"},
	}
	at := epoch
	for _, step := range script {
		at = at.Add(5 * time.Second)
		session, _ = playAndStore(t, games, id, session, step.by, rules.Play(coord(t, step.text)), "", at)
	}
	if session.Engine.State.Captures.Black != 1 {
		t.Fatalf("the script should capture one stone, got %d", session.Engine.State.Captures.Black)
	}

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if loaded.Session.Engine.State.BoardHash != session.Engine.State.BoardHash {
		t.Error("replayed position differs from the live one")
	}
	if loaded.Session.MoveNumber() != session.MoveNumber() {
		t.Errorf("move count: want %d, got %d", session.MoveNumber(), loaded.Session.MoveNumber())
	}
	if loaded.Session.Engine.State.Captures != session.Engine.State.Captures {
		t.Errorf("captures: want %+v, got %+v", session.Engine.State.Captures,
			loaded.Session.Engine.State.Captures)
	}
	if loaded.Session.ToPlay() != session.ToPlay() {
		t.Error("turn order lost in the replay")
	}
}

// A move resent after a reconnect must not create a second row. The unique index
// is what enforces this under concurrency, so the test goes through the database.
func TestResendingAMoveWritesOneRow(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, err := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := game.NewSession(blitzConfig(), epoch)
	clientMoveID := NewID()

	session, applied := playAndStore(t, games, id, session, rules.Black,
		rules.Play(coord(t, "e5")), clientMoveID, epoch.Add(time.Second))
	if !applied {
		t.Fatal("the first write should have been applied")
	}

	// The same key arrives again, exactly as it would after a dropped connection.
	applied, err = games.AppendMove(ctx, id, session, MoveRecord{
		MoveNo: 1, Color: rules.Black, Move: rules.Play(coord(t, "e5")),
		BoardHash: session.Engine.State.BoardHash, ClientMoveID: clientMoveID,
		PlayedAt: epoch.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("a resend must not error: %v", err)
	}
	if applied {
		t.Error("a resend must not be applied twice")
	}

	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM moves WHERE game_id = $1`, id).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("want one stored move, got %d", count)
	}
}

// Writing the same move number twice must also be refused, even without an
// idempotency key: the primary key is the last line of defence.
func TestDuplicateMoveNumbersAreRefused(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "e5")), "", epoch)

	applied, err := games.AppendMove(ctx, id, session, MoveRecord{
		MoveNo: 1, Color: rules.White, Move: rules.Play(coord(t, "f5")),
		BoardHash: 123, PlayedAt: epoch,
	})
	if err != nil {
		t.Fatalf("a conflicting write should be reported, not error: %v", err)
	}
	if applied {
		t.Error("move number 1 already exists")
	}
}

// If the engine ever changes how it hashes a position, a stored game will replay
// to a different board. That must be shouted about, not silently accepted.
func TestAChecksumMismatchRefusesToLoad(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)
	_, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "e5")), "", epoch)

	if _, err := testPool.Exec(ctx, `UPDATE games SET board_hash = 999 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	_, err := games.Load(ctx, id)
	if err == nil {
		t.Fatal("a corrupt checksum must refuse to load")
	}
	var mismatch game.ErrChecksumMismatch
	if !errorsAs(err, &mismatch) {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
	if mismatch.Stored != 999 {
		t.Errorf("the error should name the stored hash, got %+v", mismatch)
	}
}

func TestPassesAndResignsSurviveTheRoundTrip(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)

	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "e5")), "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.White, rules.Pass, "", epoch.Add(time.Second))
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Pass, "", epoch.Add(2*time.Second))

	if session.Phase() != rules.ScoringP {
		t.Fatalf("two passes should open scoring, got %v", session.Phase())
	}
	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Phase() != rules.ScoringP {
		t.Errorf("phase lost: %v", loaded.Session.Phase())
	}
	if loaded.Session.Engine.State.ConsecutivePasses != 2 {
		t.Errorf("pass count lost: %d", loaded.Session.Engine.State.ConsecutivePasses)
	}
}

func TestScoringNegotiationIsCheckpointedAndRestored(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "e5")), "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.White, rules.Pass, "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Pass, "", epoch)

	session, _, err := session.Apply(game.MarkDeadCommand{By: rules.White, Point: coord(t, "e5")}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err = session.Apply(game.AcceptScoreCommand{By: rules.White, Accepted: true}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := games.SaveScoring(ctx, id, session); err != nil {
		t.Fatal(err)
	}

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Scoring == nil {
		t.Fatal("the negotiation was not restored")
	}
	if len(loaded.Session.Scoring.DeadStones()) != 1 {
		t.Errorf("dead stones lost: %v", loaded.Session.Scoring.DeadStones())
	}
	if !loaded.Session.Scoring.Accepted(rules.White) || loaded.Session.Scoring.Accepted(rules.Black) {
		t.Error("acceptance state lost")
	}
	if loaded.Session.Scoring.Settled() {
		t.Error("only one player accepted")
	}
}

func TestFinishingOnTimeIsStored(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)

	// Nobody moved and the clock ran out.
	late := epoch.Add(11 * time.Minute)
	session, _, err := session.Apply(game.TimeoutCommand{Player: rules.Black}, late)
	if err != nil {
		t.Fatal(err)
	}
	if err := games.Finish(ctx, id, session, late); err != nil {
		t.Fatal(err)
	}

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Phase() != rules.Finished {
		t.Fatalf("phase: %v", loaded.Session.Phase())
	}
	result := loaded.Session.Result()
	if result == nil || result.Reason != rules.ReasonTimeout || result.Winner != rules.White {
		t.Errorf("result lost: %+v", result)
	}
	// A finished game must have no deadline left hanging for the sweeper to find.
	var deadline *time.Time
	if err := testPool.QueryRow(ctx, `SELECT move_deadline FROM games WHERE id = $1`, id).
		Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline != nil {
		t.Error("a finished game should have no move deadline")
	}
}

func TestAgreedScoreIsStoredWithTheResult(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "e5")), "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.White, rules.Pass, "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Pass, "", epoch)

	session, _, _ = session.Apply(game.AcceptScoreCommand{By: rules.Black, Accepted: true}, epoch)
	session, _, _ = session.Apply(game.AcceptScoreCommand{By: rules.White, Accepted: true}, epoch)
	if err := games.Finish(ctx, id, session, epoch); err != nil {
		t.Fatal(err)
	}

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	result := loaded.Session.Result()
	if result == nil || result.Reason != rules.ReasonCounting || result.Score == nil {
		t.Fatalf("counted result lost: %+v", result)
	}
	if result.Score.White != session.Result().Score.White {
		t.Errorf("score changed: want %v, got %v", session.Result().Score.White, result.Score.White)
	}
}

func TestEventsAreAppended(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})

	for _, kind := range []string{"player_disconnected", "owner_reclaimed", "clock_adjusted"} {
		if err := games.RecordEvent(ctx, id, 0, kind, "", map[string]any{"ms": 1500}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM game_events WHERE game_id = $1`, id).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("want three events, got %d", count)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	conn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// Running the migrations again on an already-migrated database must do nothing.
	if err := Migrate(ctx, conn.Conn()); err != nil {
		t.Fatalf("re-running migrations failed: %v", err)
	}
}

func errorsAs(err error, target *game.ErrChecksumMismatch) bool {
	for err != nil {
		if typed, ok := err.(game.ErrChecksumMismatch); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// The ko point is game state that only exists for one move. If a takeover lost it,
// a player could immediately recapture a ko they had just lost -- an illegal move
// the server would then wave through.
func TestTheKoPointSurvivesTheRoundTrip(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	config := blitzConfig()

	// The ko shape from rules-spec/vectors/ko: White takes at d5, and Black must
	// not be able to take straight back at e5.
	setup := []struct {
		by   rules.Color
		text string
	}{
		{rules.Black, "c5"}, {rules.White, "e6"}, {rules.Black, "d6"}, {rules.White, "e4"},
		{rules.Black, "d4"}, {rules.White, "f5"}, {rules.Black, "e5"}, {rules.White, "d5"},
	}
	id, err := games.Create(ctx, CreateParams{Config: config, StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := game.NewSession(config, epoch)
	at := epoch
	for _, step := range setup {
		at = at.Add(time.Second)
		session, _ = playAndStore(t, games, id, session, step.by, rules.Play(coord(t, step.text)), "", at)
	}
	if session.Engine.State.KoPoint == nil {
		t.Fatal("setup: the capture should have created a ko point")
	}
	koPoint := *session.Engine.State.KoPoint

	// Stored in the games row, and it survives a rebuild.
	var stored *int16
	if err := testPool.QueryRow(ctx, `SELECT ko_point FROM games WHERE id = $1`, id).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == nil {
		t.Fatal("the ko point was not written")
	}

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	restored := loaded.Session.Engine.State.KoPoint
	if restored == nil || *restored != koPoint {
		t.Fatalf("ko point lost: want %v, got %v", koPoint, restored)
	}
	// And the rule it exists for still holds after the round trip.
	if err := loaded.Session.Engine.ValidateBy(rules.Play(koPoint), rules.Black); err != rules.ErrKo {
		t.Errorf("an immediate recapture must still be refused, got %v", err)
	}
}

func TestSuggestedStonesAreStoredAlongsideTheMarking(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	session, _ := game.NewSession(blitzConfig(), epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "e5")), "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.White, rules.Pass, "", epoch)
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Pass, "", epoch)

	// Pretend the estimator had something to say, so the UI can show what a player
	// changed away from.
	suggested := []rules.Point{coord(t, "e5")}
	scoring := game.RestoreScoringSession(suggested, suggested, false, false, 1)
	session.Scoring = scoring
	if err := games.SaveScoring(ctx, id, session); err != nil {
		t.Fatal(err)
	}

	var suggestedCount int
	if err := testPool.QueryRow(ctx,
		`SELECT coalesce(array_length(suggested_points, 1), 0) FROM game_scoring WHERE game_id = $1`, id).
		Scan(&suggestedCount); err != nil {
		t.Fatal(err)
	}
	if suggestedCount != 1 {
		t.Errorf("want the suggestion stored, got %d points", suggestedCount)
	}

	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Scoring == nil || loaded.Session.Scoring.EditedFromSuggestion() {
		t.Error("an untouched marking should not read as edited after a reload")
	}
}

func TestListingAPlayersGames(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	me, them := seedUser(t), seedUser(t)

	// One game where I am black and it is my move, one where I am white and it
	// is theirs, one finished, and one that is not mine at all.
	mine1, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), BlackUserID: me, WhiteUserID: them, StartedAt: epoch})
	mine2, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), BlackUserID: them, WhiteUserID: me, StartedAt: epoch})
	done, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), BlackUserID: me, WhiteUserID: them, StartedAt: epoch})
	_, _ = games.Create(ctx, CreateParams{Config: blitzConfig(), BlackUserID: them, StartedAt: epoch})

	session, _ := game.NewSession(blitzConfig(), epoch)
	session, _, _ = session.Apply(game.PlayCommand{By: rules.Black, Move: rules.Resign}, epoch)
	if err := games.Finish(ctx, done, session, epoch); err != nil {
		t.Fatal(err)
	}

	list, err := games.ListForUser(ctx, me, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want my three games, got %d", len(list))
	}
	byID := map[string]GameSummary{}
	for _, s := range list {
		byID[s.ID] = s
	}
	if s := byID[mine1]; s.MyColor != rules.Black || s.ToPlay != rules.Black || s.OpponentID != them {
		t.Errorf("mine1: %+v", s)
	}
	if s := byID[mine2]; s.MyColor != rules.White || s.ToPlay != rules.Black || s.OpponentName == "" {
		t.Errorf("mine2 should show me as white with a named opponent: %+v", s)
	}
	if s := byID[done]; s.Phase != rules.Finished || s.Result == nil || s.Result.Reason != rules.ReasonResignation {
		t.Errorf("finished game lost its result: %+v", s)
	}
	// Active games lead, so the home screen can show "your move" first.
	if list[len(list)-1].ID != done {
		t.Errorf("the finished game should sort last, got %v", list[len(list)-1].ID)
	}
	if _, err := games.ListForUser(context.Background(), them, 0); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := games.ListForUser(cancelled, me, 0); err == nil {
		t.Error("ListForUser must report a failed read")
	}
}

// An accepted undo must survive a reload: the removed move may not come back,
// and the undo budget must not reset (docs/01 FR-G10).
func TestAnAcceptedUndoRewindsTheStoredGame(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, err := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := games.Load(ctx, id)
	session := loaded.Session
	session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "E5")), NewID(), epoch)
	session, _ = playAndStore(t, games, id, session, rules.White, rules.Play(coord(t, "E7")), NewID(), epoch.Add(time.Second))

	session, _, err = session.Apply(game.RequestUndoCommand{By: rules.White}, epoch.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	session, events, err := session.Apply(game.RespondUndoCommand{By: rules.Black, Accept: true}, epoch.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if resolved, ok := events[0].(game.UndoResolved); !ok || !resolved.Accepted || resolved.MoveNumber != 1 {
		t.Fatalf("unexpected events %+v", events)
	}
	if err := games.Rewind(ctx, id, session, epoch.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	reloaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatalf("a rewound game must still load: %v", err)
	}
	if reloaded.Session.MoveNumber() != 1 || reloaded.Session.ToPlay() != rules.White {
		t.Errorf("want move 1, white to play; got move %d, %v", reloaded.Session.MoveNumber(), reloaded.Session.ToPlay())
	}
	if reloaded.Session.UndosUsed() != 1 {
		t.Errorf("undo budget lost on reload: %d", reloaded.Session.UndosUsed())
	}
	if reloaded.Session.Engine.Board().At(coord(t, "E7")) != rules.Empty {
		t.Error("the undone stone came back")
	}

	// The seat is free again for a different move.
	_, applied := playAndStore(t, games, id, reloaded.Session, rules.White, rules.Play(coord(t, "C3")), NewID(), epoch.Add(4*time.Second))
	if !applied {
		t.Error("the replacement move was not stored")
	}
	final, err := games.Load(ctx, id)
	if err != nil || final.Session.MoveNumber() != 2 {
		t.Errorf("after replaying: %v, move %d", err, final.Session.MoveNumber())
	}
}

// "Play on" from scoring drops the passes and the negotiation. The stored game
// must agree, or the next load lands back in scoring while the actor is playing.
func TestPlayingOnFromScoringRewindsTheStoredGame(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	id, err := games.Create(ctx, CreateParams{Config: blitzConfig(), StartedAt: epoch})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := games.Load(ctx, id)
	session := loaded.Session
	at := epoch
	for i, step := range []struct {
		by   rules.Color
		move rules.Move
	}{
		{rules.Black, rules.Play(coord(t, "E5"))}, {rules.White, rules.Play(coord(t, "E7"))},
		{rules.Black, rules.Pass}, {rules.White, rules.Pass},
	} {
		at = epoch.Add(time.Duration(i+1) * time.Second)
		session, _ = playAndStore(t, games, id, session, step.by, step.move, NewID(), at)
	}
	if session.Phase() != rules.ScoringP || session.Scoring == nil {
		t.Fatalf("two passes should open scoring, got %v", session.Phase())
	}
	if err := games.SaveScoring(ctx, id, session); err != nil {
		t.Fatal(err)
	}

	session, events, err := session.Apply(game.ResumePlayCommand{By: rules.White}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := events[0].(game.PlayResumed); !ok {
		t.Fatalf("unexpected events %+v", events)
	}
	if err := games.Rewind(ctx, id, session, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	reloaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatalf("reload after playing on: %v", err)
	}
	if reloaded.Session.Phase() != rules.Playing || reloaded.Session.Scoring != nil {
		t.Errorf("reloaded game should be playing with no negotiation, got %v scoring=%v",
			reloaded.Session.Phase(), reloaded.Session.Scoring != nil)
	}
	if reloaded.Session.MoveNumber() != 3 || reloaded.Session.ToPlay() != rules.White {
		t.Errorf("want move 3, white to play; got %d, %v", reloaded.Session.MoveNumber(), reloaded.Session.ToPlay())
	}
	// And scoring can open again later on a clean slate.
	_, applied := playAndStore(t, games, id, reloaded.Session, rules.White, rules.Play(coord(t, "C3")), NewID(), at.Add(2*time.Second))
	if !applied {
		t.Error("the next move was not stored")
	}
}

// Seen in the field: sign in with Apple, delete the account, sign in again ->
// "could not link". The old row must not lock the person out.
func TestAnAppleIDIsFreeAgainAfterItsAccountIsDeleted(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	users, identities := NewUsers(testPool), NewIdentities(testPool)
	first, err := users.CreateGuest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subject := "apple-" + NewID()
	if err := identities.Link(ctx, first.ID, ProviderApple, subject, "a@b.c"); err != nil {
		t.Fatal(err)
	}

	// A live owner keeps the identity.
	second, _ := users.CreateGuest(ctx)
	if err := identities.Link(ctx, second.ID, ProviderApple, subject, ""); !errors.Is(err, ErrIdentityTaken) {
		t.Fatalf("a live account's identity must not be taken over, got %v", err)
	}

	if err := users.DeleteAccount(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := identities.UserFor(ctx, ProviderApple, subject); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted account's identity should not resolve, got %v", err)
	}
	if err := identities.Link(ctx, second.ID, ProviderApple, subject, ""); err != nil {
		t.Fatalf("linking after deletion: %v", err)
	}
	owner, err := identities.UserFor(ctx, ProviderApple, subject)
	if err != nil || owner != second.ID {
		t.Errorf("identity should now open the new account: %s %v", owner, err)
	}
	if got, _ := users.Get(ctx, second.ID); got.IsGuest {
		t.Error("the new owner should be promoted")
	}

	// Deleting also forgets the phone: no pushes to a person who left.
	devices := NewDevices(testPool)
	_ = devices.Register(ctx, second.ID, strings.Repeat("ab", 32), "production", "")
	_ = users.DeleteAccount(ctx, second.ID)
	if list, _ := devices.ForUser(ctx, second.ID, "turn"); len(list) != 0 {
		t.Error("devices of a deleted account should be gone")
	}
}

func TestPushPrefsPatchAndFiltering(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	users, devices := NewUsers(testPool), NewDevices(testPool)
	user, _ := users.CreateGuest(ctx)
	token := strings.Repeat("aa", 32)
	if err := devices.Register(ctx, user.ID, token, "production", ""); err != nil {
		t.Fatal(err)
	}

	prefs, err := devices.SetPrefs(ctx, user.ID, token, map[string]bool{"turn": false})
	if err != nil {
		t.Fatal(err)
	}
	if prefs["turn"] || !prefs["game_end"] {
		t.Errorf("patch should merge, got %v", prefs)
	}
	if list, _ := devices.ForUser(ctx, user.ID, "turn"); len(list) != 0 {
		t.Error("a muted kind must not be delivered")
	}
	if list, _ := devices.ForUser(ctx, user.ID, "game_end"); len(list) != 1 {
		t.Error("other kinds keep flowing")
	}
	// Only the owner can change it.
	other, _ := users.CreateGuest(ctx)
	if _, err := devices.SetPrefs(ctx, other.ID, token, map[string]bool{"turn": true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger's patch: %v", err)
	}
}

func TestLowTimeListsOnlyImminentCorrespondenceDeadlines(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	corr := game.Config{Size: 9, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
		TimeControl: game.TimeControl{Kind: game.Correspondence, PerMove: 24 * time.Hour}}
	users := NewUsers(testPool)
	an, _ := users.CreateGuest(ctx)

	imminent, _ := games.Create(ctx, CreateParams{Config: corr, BlackUserID: an.ID, StartedAt: epoch})
	comfortable, _ := games.Create(ctx, CreateParams{Config: corr, BlackUserID: an.ID, StartedAt: epoch})
	blitzID, _ := games.Create(ctx, CreateParams{Config: blitzConfig(), BlackUserID: an.ID, StartedAt: epoch})

	now := time.Now()
	set := func(id string, deadline time.Time) {
		if _, err := testPool.Exec(ctx, `UPDATE games SET move_deadline = $2 WHERE id = $1`, id, deadline); err != nil {
			t.Fatal(err)
		}
	}
	set(imminent, now.Add(time.Hour))      // 1h left of a 2.4h threshold
	set(comfortable, now.Add(5*time.Hour)) // outside the 10% window
	set(blitzID, now.Add(30*time.Second))  // live game: the actor owns this one

	list, err := games.ListLowTime(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]LowTimeGame{}
	for _, item := range list {
		ids[item.ID] = item
	}
	if item, ok := ids[imminent]; !ok || item.UserID != an.ID {
		t.Errorf("the imminent game should be listed for its player, got %v", list)
	}
	if _, ok := ids[comfortable]; ok {
		t.Error("a comfortable deadline must not warn")
	}
	if _, ok := ids[blitzID]; ok {
		t.Error("live games are not the sweeper's business")
	}
}

func TestStatsCountWinsBySeatAndSize(t *testing.T) {
	games := newGames(t)
	ctx := context.Background()
	users := NewUsers(testPool)
	me, _ := users.CreateGuest(ctx)
	other, _ := users.CreateGuest(ctx)

	finish := func(size int, black, white string, resignBy rules.Color) {
		config := game.Config{Size: size, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
			TimeControl: game.TimeControl{Kind: game.Absolute, MainTime: 10 * time.Minute}}
		id, err := games.Create(ctx, CreateParams{Config: config, BlackUserID: black, WhiteUserID: white, StartedAt: epoch})
		if err != nil {
			t.Fatal(err)
		}
		loaded, _ := games.Load(ctx, id)
		session := loaded.Session
		session, _ = playAndStore(t, games, id, session, rules.Black, rules.Play(coord(t, "E5")), NewID(), epoch)
		if resignBy == rules.White {
			session, _ = playAndStore(t, games, id, session, rules.White, rules.Play(coord(t, "D5")), NewID(), epoch.Add(time.Second))
			// Black to move resigns? No: we want the given side to resign on their turn.
		}
		next, _, err := session.Apply(game.PlayCommand{By: session.ToPlay(), Move: rules.Resign,
			ClientMoveID: NewID(), ExpectedMoveNumber: session.MoveNumber()}, epoch.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := games.Finish(ctx, id, next, epoch.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	// me as black, white resigns → win (white was to move after E5... craft):
	finish(9, me.ID, other.ID, rules.White)  // after two plays, black resigns?! see assertion below
	finish(9, other.ID, me.ID, rules.Black)  // black (them) resigns on move 2 → my win as white
	finish(13, me.ID, other.ID, rules.Black) // white to move resigns? black played then white resigns → my win

	stats, err := games.StatsForUser(ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Games != 3 {
		t.Fatalf("games: %+v", stats)
	}
	if stats.Wins+stats.Losses != 3 || stats.Draws != 0 {
		t.Errorf("every resignation has a winner: %+v", stats)
	}
	if stats.BySize[9].Games != 2 || stats.BySize[13].Games != 1 {
		t.Errorf("per-size split: %+v", stats.BySize)
	}
	empty, _ := games.StatsForUser(ctx, NewID())
	if empty.Games != 0 {
		t.Errorf("a stranger has no games: %+v", empty)
	}
}

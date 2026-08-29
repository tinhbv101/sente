package sweep

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/hub"
	"sente.app/server/internal/node"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

var (
	testPool  *pgxpool.Pool
	testRedis *redis.Client
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("sente"), postgres.WithUsername("sente"), postgres.WithPassword("sente"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start postgres: %v\n", err)
		os.Exit(1)
	}
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	conn, _ := pgx.Connect(ctx, dsn)
	if err := store.Migrate(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	_ = conn.Close(ctx)
	testPool, _ = pgxpool.New(ctx, dsn)
	rd, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start redis: %v\n", err)
		os.Exit(1)
	}
	uri, _ := rd.ConnectionString(ctx)
	options, _ := redis.ParseURL(uri)
	testRedis = redis.NewClient(options)
	code := m.Run()
	testPool.Close()
	_ = testRedis.Close()
	_ = testcontainers.TerminateContainer(pg)
	_ = testcontainers.TerminateContainer(rd)
	os.Exit(code)
}

func newStack(t *testing.T) (*Sweeper, *hub.Hub) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	leases := cluster.New(testRedis, t.Name()).WithTTL(time.Minute)
	var messageHub *hub.Hub
	registry := node.New(node.Config{
		Leases: leases, Games: store.NewGames(testPool), IdleAfter: time.Hour,
		Broadcast: func(id string, events []game.Event) { messageHub.Broadcast(id, events) },
	})
	messageHub = hub.New(hub.Config{Registry: registry, Leases: leases, Redis: testRedis, NodeID: t.Name()})
	ctx, cancel := context.WithCancel(context.Background())
	go messageHub.Serve(ctx)
	t.Cleanup(func() { cancel(); registry.Drain(context.Background()); messageHub.Close() })
	return &Sweeper{Games: store.NewGames(testPool), Hub: messageHub,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}, messageHub
}

func correspondence() game.Config {
	return game.Config{Size: 9, Rules: rules.Japanese, Komi: 6.5, MaxUndos: 3,
		TimeControl: game.TimeControl{Kind: game.Correspondence, PerMove: 48 * time.Hour}}
}

// A correspondence game nobody has touched for three days: the sweeper ends it,
// and the player who failed to move loses.
func TestAnOverdueGameIsEndedOnTime(t *testing.T) {
	sweeper, _ := newStack(t)
	ctx := context.Background()
	games := store.NewGames(testPool)
	id, err := games.Create(ctx, store.CreateParams{Config: correspondence(), StartedAt: time.Now().Add(-72 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// Create stamps the deadline from StartedAt: two days after, one day ago.
	if ended := sweeper.Once(ctx); ended != 1 {
		t.Fatalf("want one game ended, got %d", ended)
	}
	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	result := loaded.Session.Result()
	if loaded.Session.Phase() != rules.Finished || result == nil || result.Reason != rules.ReasonTimeout {
		t.Fatalf("want a timeout result, got %v %+v", loaded.Session.Phase(), result)
	}
	if result.Winner != rules.White {
		t.Errorf("black never moved, so White wins; got %v", result.Winner)
	}
	// A second pass finds nothing.
	if ended := sweeper.Once(ctx); ended != 0 {
		t.Errorf("a finished game must not be ended twice, got %d", ended)
	}
}

func TestAGameStillInsideItsDeadlineIsLeftAlone(t *testing.T) {
	sweeper, _ := newStack(t)
	ctx := context.Background()
	games := store.NewGames(testPool)
	id, _ := games.Create(ctx, store.CreateParams{Config: correspondence(), StartedAt: time.Now()})
	if ended := sweeper.Once(ctx); ended != 0 {
		t.Fatalf("nothing is overdue, got %d", ended)
	}
	loaded, _ := games.Load(ctx, id)
	if loaded.Session.Phase() != rules.Playing {
		t.Error("the game must still be playing")
	}
}

// A move that arrives after the deadline never plays. Two things race to end the
// game -- the actor's own timer, armed the instant it starts, and the clock check
// on the move itself -- and the outcome must be the same whichever wins.
func TestALateMoveLosesOnTimeWhicheverPathJudgesIt(t *testing.T) {
	sweeper, messageHub := newStack(t)
	ctx := context.Background()
	games := store.NewGames(testPool)
	id, _ := games.Create(ctx, store.CreateParams{Config: correspondence(), StartedAt: time.Now().Add(-72 * time.Hour)})

	_, err := messageHub.Execute(ctx, id, game.PlayCommand{
		By: rules.Black, Move: rules.Pass, SkipExpectedCheck: true,
	})
	if err != nil && err.Error() != string(game.ErrNotPlaying) {
		t.Fatalf("want either the move judged late or the game already over, got %v", err)
	}
	loaded, err := games.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	result := loaded.Session.Result()
	if loaded.Session.Phase() != rules.Finished || result == nil || result.Reason != rules.ReasonTimeout {
		t.Fatalf("the late mover must have lost on time, got %v %+v", loaded.Session.Phase(), result)
	}
	if result.Winner != rules.White {
		t.Errorf("Black was late, so White wins; got %v", result.Winner)
	}
	if loaded.Session.MoveNumber() != 0 {
		t.Error("the late pass must not have been recorded as a move")
	}
	if n := sweeper.Once(ctx); n != 0 {
		t.Errorf("an already-finished game is not ended again, got %d", n)
	}
}

// Command server runs one Sente node: the REST API, the WebSocket gateway, and
// the games this node owns.
//
// Every node is identical. Which games a node runs is decided at runtime by the
// leases in Redis (docs/03 ADR-005), so scaling out means starting more of these
// and nothing else.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"sente.app/server/internal/auth"
	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/httpapi"
	"sente.app/server/internal/hub"
	"sente.app/server/internal/node"
	"sente.app/server/internal/ratelimit"
	"sente.app/server/internal/store"
)

// drainTimeout bounds how long a shutting-down node waits to hand its games over.
// Games are released one by one; anything still running when this expires falls
// back to the lease TTL (docs/04 §5.2).
const drainTimeout = 30 * time.Second

type config struct {
	addr           string
	databaseURL    string
	redisURL       string
	jwtSecret      string
	nodeID         string
	allowedOrigins []string
	runMigrations  bool
	publicBaseURL  string
	trustProxy     bool
	clientIPHeader string
}

func loadConfig() (config, error) {
	hostname, _ := os.Hostname()
	c := config{
		addr:           envOr("SENTE_ADDR", ":8080"),
		databaseURL:    os.Getenv("SENTE_DATABASE_URL"),
		redisURL:       envOr("SENTE_REDIS_URL", "redis://localhost:6379"),
		jwtSecret:      os.Getenv("SENTE_JWT_SECRET"),
		nodeID:         envOr("SENTE_NODE_ID", hostname),
		runMigrations:  envOr("SENTE_MIGRATE", "true") == "true",
		publicBaseURL:  strings.TrimRight(os.Getenv("SENTE_PUBLIC_URL"), "/"),
		trustProxy:     envOr("SENTE_TRUST_PROXY", "false") == "true",
		clientIPHeader: os.Getenv("SENTE_CLIENT_IP_HEADER"),
	}
	if origins := os.Getenv("SENTE_ALLOWED_ORIGINS"); origins != "" {
		c.allowedOrigins = strings.Split(origins, ",")
	}

	// Fail at startup, loudly, rather than at the first request that needs them.
	var missing []string
	if c.databaseURL == "" {
		missing = append(missing, "SENTE_DATABASE_URL")
	}
	if c.jwtSecret == "" {
		missing = append(missing, "SENTE_JWT_SECRET")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	if c.nodeID == "" {
		return config{}, errors.New("SENTE_NODE_ID is empty and the hostname is unavailable")
	}
	return c, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// version is stamped in at build time.
var version = "dev"

func main() {
	// The container image has no shell and no curl, so the binary answers its own
	// health check. `sente-server -healthcheck` exits 0 when the node is serving.
	healthcheck := flag.Bool("healthcheck", false, "probe this node and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *healthcheck {
		os.Exit(probe())
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	logger.Info("starting", "version", version)
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// probe checks the local node the way an orchestrator would.
func probe() int {
	addr := envOr("SENTE_ADDR", ":8080")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", response.StatusCode)
		return 1
	}
	return 0
}

func run(logger *slog.Logger) error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	logger = logger.With("node_id", config.nodeID)

	// SIGTERM is what a container runtime sends; handling it is the difference
	// between a clean handover and every game waiting out its lease.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, config.databaseURL)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()
	if err := waitForPostgres(ctx, pool, logger); err != nil {
		return err
	}

	if config.runMigrations {
		conn, err := pgx.Connect(ctx, config.databaseURL)
		if err != nil {
			return fmt.Errorf("connecting for migrations: %w", err)
		}
		if err := store.Migrate(ctx, conn); err != nil {
			_ = conn.Close(ctx)
			return fmt.Errorf("running migrations: %w", err)
		}
		_ = conn.Close(ctx)
		logger.Info("migrations applied")
	}

	redisOptions, err := redis.ParseURL(config.redisURL)
	if err != nil {
		return fmt.Errorf("parsing SENTE_REDIS_URL: %w", err)
	}
	redisClient := redis.NewClient(redisOptions)
	defer func() { _ = redisClient.Close() }()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("connecting to redis: %w", err)
	}

	issuer, err := auth.NewIssuer(config.jwtSecret)
	if err != nil {
		return err
	}

	leases := cluster.New(redisClient, config.nodeID)
	var messageHub *hub.Hub
	registry := node.New(node.Config{
		Leases:    leases,
		Games:     store.NewGames(pool),
		Time:      game.SystemTime{},
		IdleAfter: 5 * time.Minute,
		Broadcast: func(gameID string, events []game.Event) { messageHub.Broadcast(gameID, events) },
	})
	messageHub = hub.New(hub.Config{
		Registry: registry, Leases: leases, Redis: redisClient, NodeID: config.nodeID,
	})

	background, stopBackground := context.WithCancel(context.Background())
	go registry.Run(background)     // heartbeat, lease renewal, reaping
	go messageHub.Serve(background) // commands forwarded from other nodes

	api := httpapi.New(httpapi.Config{
		Pool: pool, Redis: redisClient, Hub: messageHub, Registry: registry,
		Issuer: issuer, Limiter: ratelimit.New(redisClient), Logger: logger,
		AllowedOrigins: config.allowedOrigins,
		PublicBaseURL:  config.publicBaseURL, TrustProxyHeaders: config.trustProxy,
		ClientIPHeader: config.clientIPHeader,
	})

	// Invitations nobody answered are closed once an hour. Reads already treat them
	// as expired, so this is tidying, not correctness.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		challenges := store.NewChallenges(pool)
		for {
			select {
			case <-background.Done():
				return
			case <-ticker.C:
				if swept, err := challenges.ExpireStale(background); err != nil {
					logger.Warn("expiring invitations", "error", err)
				} else if swept > 0 {
					logger.Info("expired invitations", "count", swept)
				}
			}
		}
	}()
	server := &http.Server{
		Addr:    config.addr,
		Handler: api.Handler(),
		// No WriteTimeout: a WebSocket connection is meant to stay open. Read and
		// idle limits live in the connection handler instead (docs/06 §3.9).
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       300 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", config.addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		stopBackground()
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	// Stop taking new work first, then hand the games over, then let the process go.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown was not clean", "error", err)
	}
	registry.Drain(shutdownCtx)
	messageHub.Close()
	stopBackground()
	logger.Info("stopped cleanly")
	return nil
}

// waitForPostgres tolerates the database still starting, which is the normal case
// when the whole stack comes up together.
func waitForPostgres(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	deadline := time.Now().Add(60 * time.Second)
	for attempt := 1; ; attempt++ {
		if err := pool.Ping(ctx); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("postgres never became reachable: %w", err)
		}
		logger.Info("waiting for postgres", "attempt", attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

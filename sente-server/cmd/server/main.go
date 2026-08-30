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

	"sente.app/server/internal/apple"
	"sente.app/server/internal/auth"
	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/httpapi"
	"sente.app/server/internal/hub"
	"sente.app/server/internal/node"
	"sente.app/server/internal/notify"
	"sente.app/server/internal/push"
	"sente.app/server/internal/ratelimit"
	"sente.app/server/internal/store"
	"sente.app/server/internal/sweep"
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
	appleTeamID    string
	appStoreURL    string
	contactEmail   string
	appleBundleID  string
	apnsKeyID      string
	apnsKeyFile    string
	siwaKeyID      string
	siwaKeyFile    string
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
		appleTeamID:    os.Getenv("SENTE_APPLE_TEAM_ID"),
		appStoreURL:    os.Getenv("SENTE_APP_STORE_URL"),
		contactEmail:   os.Getenv("SENTE_CONTACT_EMAIL"),
		appleBundleID:  envOr("SENTE_APPLE_BUNDLE_ID", "app.sente.go"),
		apnsKeyID:      os.Getenv("SENTE_APNS_KEY_ID"),
		siwaKeyID:      os.Getenv("SENTE_SIWA_KEY_ID"),
	}
	// Apple names the download AuthKey_<KEY_ID>.p8; default to that so the file
	// can be dropped in as is.
	secretsDir := envOr("SENTE_SECRETS_DIR", "/run/secrets")
	c.apnsKeyFile = envOr("SENTE_APNS_KEY_FILE", secretsDir+"/AuthKey_"+c.apnsKeyID+".p8")
	c.siwaKeyFile = envOr("SENTE_SIWA_KEY_FILE", secretsDir+"/AuthKey_"+c.siwaKeyID+".p8")
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
	if c.apnsKeyID != "" && c.appleTeamID == "" {
		return config{}, errors.New("SENTE_APNS_KEY_ID needs SENTE_APPLE_TEAM_ID")
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

	notifier, err := buildNotifier(config, pool, logger)
	if err != nil {
		return err
	}
	verifier, err := buildAppleVerifier(config, logger)
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
		Broadcast: func(gameID string, events []game.Event) {
			messageHub.Broadcast(gameID, events)
			notifier.Broadcast(gameID, events)
		},
	})
	messageHub = hub.New(hub.Config{
		Registry: registry, Leases: leases, Redis: redisClient, NodeID: config.nodeID,
	})

	background, stopBackground := context.WithCancel(context.Background())
	go registry.Run(background)     // heartbeat, lease renewal, reaping
	go messageHub.Serve(background) // commands forwarded from other nodes
	if notifier.Enabled() {
		go notifier.Run(background)
	}

	api := httpapi.New(httpapi.Config{
		Pool: pool, Redis: redisClient, Hub: messageHub, Registry: registry,
		Issuer: issuer, Limiter: ratelimit.New(redisClient), Logger: logger,
		AllowedOrigins: config.allowedOrigins,
		PublicBaseURL:  config.publicBaseURL, TrustProxyHeaders: config.trustProxy,
		ClientIPHeader: config.clientIPHeader,
		AppleTeamID:    config.appleTeamID, AppStoreURL: config.appStoreURL,
		ContactEmail: config.contactEmail,
		Apple:        verifier, Notifier: notifier,
	})

	// Games whose clock ran out while no node was running them -- correspondence
	// games, mostly -- are ended by looking at the database (docs/04 §4.4).
	go (&sweep.Sweeper{Games: store.NewGames(pool), Hub: messageHub, Logger: logger}).
		Run(background, time.Minute)

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

// buildNotifier is nil-safe to use when push is not configured.
func buildNotifier(config config, pool *pgxpool.Pool, logger *slog.Logger) (*notify.Notifier, error) {
	if config.apnsKeyID == "" {
		logger.Info("push disabled: SENTE_APNS_KEY_ID not set")
		return nil, nil
	}
	pemBytes, err := os.ReadFile(config.apnsKeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading APNs key: %w", err)
	}
	key, err := push.ParseKey(pemBytes)
	if err != nil {
		return nil, err
	}
	// Both environments are served: a TestFlight build and a debug build may be
	// signed into the same account at once.
	notifier := notify.New(notify.Config{
		Games:      store.NewGames(pool),
		Devices:    store.NewDevices(pool),
		Sandbox:    push.New(push.SandboxHost, config.appleBundleID, config.appleTeamID, config.apnsKeyID, key),
		Production: push.New(push.ProductionHost, config.appleBundleID, config.appleTeamID, config.apnsKeyID, key),
		Logger:     logger,
	})
	logger.Info("push enabled", "key_id", config.apnsKeyID, "topic", config.appleBundleID)
	return notifier, nil
}

// buildAppleVerifier enables Sign in with Apple once there is a team to sign in
// to. The key is only checked for readability here; verifying identity tokens
// needs Apple's public keys, not ours.
func buildAppleVerifier(config config, logger *slog.Logger) (*apple.Verifier, error) {
	if config.appleTeamID == "" {
		logger.Info("sign in with apple disabled: SENTE_APPLE_TEAM_ID not set")
		return nil, nil
	}
	if config.siwaKeyID != "" {
		pemBytes, err := os.ReadFile(config.siwaKeyFile)
		if err != nil {
			return nil, fmt.Errorf("reading Sign in with Apple key: %w", err)
		}
		if _, err := apple.ParseKey(pemBytes); err != nil {
			return nil, err
		}
	}
	logger.Info("sign in with apple enabled", "audience", config.appleBundleID)
	return apple.NewVerifier(apple.JWKSURL, config.appleBundleID), nil
}

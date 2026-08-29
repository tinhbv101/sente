// Package httpapi is the edge of the server: REST for everything that is not
// realtime, and a WebSocket for playing (docs/06).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"sente.app/server/internal/auth"
	"sente.app/server/internal/game"
	"sente.app/server/internal/hub"
	"sente.app/server/internal/node"
	"sente.app/server/internal/ratelimit"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

type Config struct {
	Pool     *pgxpool.Pool
	Redis    *redis.Client
	Hub      *hub.Hub
	Registry *node.Registry
	Issuer   *auth.Issuer
	Limiter  *ratelimit.Limiter
	Logger   *slog.Logger

	// PublicBaseURL is the address invitation links are built from. Empty omits
	// share_url rather than emitting a wrong one.
	PublicBaseURL string
	// TrustProxyHeaders enables X-Forwarded-For. Only set it when the server is
	// actually behind a proxy that overwrites the header.
	TrustProxyHeaders bool
	// ClientIPHeader names a header carrying the real client address, for setups
	// with a CDN in front of the proxy. Honoured only with TrustProxyHeaders.
	ClientIPHeader string
	// AppleTeamID enables the apple-app-site-association file for universal links.
	AppleTeamID string
	// AppStoreURL is offered on the landing page to people without the app.
	AppStoreURL string
	// AllowedOrigins for the WebSocket handshake. Empty means same-origin only.
	AllowedOrigins []string
}

type Server struct {
	config     Config
	games      *store.Games
	users      *store.Users
	challenges *store.Challenges
	refresh    *store.RefreshTokens
	moderation *store.Moderation
	mux        *http.ServeMux
}

func New(config Config) *Server {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	s := &Server{
		config:     config,
		games:      store.NewGames(config.Pool),
		users:      store.NewUsers(config.Pool),
		challenges: store.NewChallenges(config.Pool),
		refresh:    store.NewRefreshTokens(config.Pool),
		moderation: store.NewModeration(config.Pool),
		mux:        http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return withRecovery(s.config.Logger, s.mux) }

func (s *Server) routes() {
	// Health probes are not rate limited: throttling the thing that tells you the
	// server is alive helps nobody.
	s.mux.HandleFunc("GET /healthz", s.handleLive)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.HandleFunc("GET /v1/config", s.limit(ratelimit.Read, s.handleConfig))

	// Charged to the caller's address, since there is no user yet.
	s.mux.HandleFunc("POST /v1/auth/guest", s.limit(ratelimit.SignUp, s.handleGuest))
	s.mux.HandleFunc("POST /v1/auth/refresh", s.limit(ratelimit.SignUp, s.handleRefresh))
	s.mux.HandleFunc("POST /v1/auth/logout", s.limit(ratelimit.Read, s.handleLogout))

	s.mux.HandleFunc("GET /v1/me", s.authed(ratelimit.Read, s.handleMe))
	s.mux.HandleFunc("DELETE /v1/me", s.authed(ratelimit.Read, s.handleDeleteAccount))
	s.mux.HandleFunc("POST /v1/reports", s.authed(ratelimit.CreateInvite, s.handleReport))
	s.mux.HandleFunc("POST /v1/blocks", s.authed(ratelimit.Read, s.handleBlock))
	s.mux.HandleFunc("DELETE /v1/blocks/{id}", s.authed(ratelimit.Read, s.handleUnblock))

	s.mux.HandleFunc("POST /v1/games", s.authed(ratelimit.CreateGame, s.handleCreateGame))
	s.mux.HandleFunc("GET /v1/games", s.authed(ratelimit.Read, s.handleListGames))
	s.mux.HandleFunc("GET /v1/games/{id}", s.authed(ratelimit.Read, s.handleGetGame))
	s.mux.HandleFunc("GET /v1/games/{id}/moves", s.authed(ratelimit.Read, s.handleMoves))
	s.mux.HandleFunc("GET /v1/games/{id}/sgf", s.authed(ratelimit.Read, s.handleSGF))

	// The preview needs no token: a link has to be readable before signing up.
	s.mux.HandleFunc("GET /v1/challenges/{code}", s.limit(ratelimit.Read, s.handleGetChallenge))
	s.mux.HandleFunc("POST /v1/challenges", s.authed(ratelimit.CreateInvite, s.handleCreateChallenge))
	s.mux.HandleFunc("GET /v1/challenges", s.authed(ratelimit.Read, s.handleListChallenges))
	s.mux.HandleFunc("POST /v1/challenges/{code}/accept",
		s.authed(ratelimit.AcceptInvite, s.handleAcceptChallenge))
	s.mux.HandleFunc("POST /v1/challenges/{code}/decline",
		s.authed(ratelimit.AcceptInvite, s.handleDeclineChallenge))
	s.mux.HandleFunc("DELETE /v1/challenges/{code}",
		s.authed(ratelimit.CreateInvite, s.handleCancelChallenge))

	s.mux.HandleFunc("GET /v1/ws", s.handleWebSocket)

	// Public pages and operational endpoints.
	s.mux.HandleFunc("GET /j/{code}", s.limit(ratelimit.Read, s.handleLanding))
	s.mux.HandleFunc("GET /.well-known/apple-app-site-association", s.handleAASA)
	s.mux.Handle("GET /metrics", metricsHandler())
}

// ── plumbing ────────────────────────────────────────────────────────────────

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

// Every failure has the same shape, 4xx and 5xx alike, so a client only ever
// needs one branch for errors (docs/06 §1.1).
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]apiError{
		"error": {Code: code, Message: message},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func withRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// A panic in one request must not take the process down: the other
				// games running on this node have nothing to do with it.
				logger.Error("panic serving request", "path", r.URL.Path, "panic", recovered)
				writeError(w, http.StatusInternalServerError, "internal",
					"Có lỗi xảy ra. Vui lòng thử lại.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type contextKey string

const userKey contextKey = "user"

// authed verifies the token, then charges the rate limit to the user rather than
// to their address -- otherwise everyone behind one office NAT shares a budget.
func (s *Server) authed(rule ratelimit.Rule, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := s.claimsFrom(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Phiên đăng nhập đã hết hạn.")
			return
		}
		s.limit(rule, next)(w, r.WithContext(context.WithValue(r.Context(), userKey, claims)))
	}
}

func (s *Server) claimsFrom(r *http.Request) (*auth.Claims, error) {
	header := r.Header.Get("Authorization")
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		// Browsers cannot set headers on a WebSocket handshake, so the query
		// string is allowed there. It ends up in access logs, which is why it is
		// only a fallback.
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return nil, auth.ErrInvalidToken
	}
	return s.config.Issuer.Verify(token)
}

func userFrom(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(userKey).(*auth.Claims)
	return claims
}

// ── health ──────────────────────────────────────────────────────────────────

// handleLive answers as long as the process is running. It must not touch the
// database: a liveness probe that fails on a slow query gets the container killed
// exactly when it is under load.
func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady checks the dependencies a request actually needs.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	healthy := true
	if err := s.config.Pool.Ping(ctx); err != nil {
		checks["postgres"] = err.Error()
		healthy = false
	}
	if err := s.config.Redis.Ping(ctx).Err(); err != nil {
		checks["redis"] = err.Error()
		healthy = false
	}
	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[healthy],
		"checks": checks, "held_games": len(s.config.Registry.Held())})
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"rules_version":     store.RulesVersion,
		"protocol_versions": []int{ProtocolVersion},
		"server_time":       time.Now().UTC(),
		"board_sizes":       rules.SupportedSizes,
		"feature_flags": map[string]bool{
			"matchmaking": false, "ai_opponent": false, "ranked": false,
			"apple_sign_in": false, "invite_links": true, "refresh_tokens": true,
		},
	})
}

// ── auth ────────────────────────────────────────────────────────────────────

func (s *Server) handleGuest(w http.ResponseWriter, r *http.Request) {
	user, err := s.users.CreateGuest(r.Context())
	if err != nil {
		s.config.Logger.Error("creating guest", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Không tạo được tài khoản.")
		return
	}
	session, ok := s.issueSession(w, r, user.ID, true)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": map[string]any{
			"id": user.ID, "display_name": user.DisplayName,
			"friend_code": user.FriendCode, "is_guest": true,
		},
		"access_token":  session.AccessToken,
		"expires_in":    session.ExpiresIn,
		"refresh_token": session.RefreshToken,
	})
}

// ── games ───────────────────────────────────────────────────────────────────

type createGameRequest struct {
	BoardSize   int     `json:"board_size"`
	Rules       string  `json:"rules"`
	Komi        float64 `json:"komi"`
	Handicap    int     `json:"handicap"`
	TimeControl struct {
		Kind        string `json:"kind"`
		MainTimeMs  int64  `json:"main_time_ms"`
		Periods     int    `json:"periods"`
		PeriodMs    int64  `json:"period_time_ms"`
		IncrementMs int64  `json:"increment_ms"`
		DaysPerMove int    `json:"days_per_move"`
	} `json:"time_control"`
	Opponent string `json:"opponent_user_id"`
}

func (s *Server) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	var request createGameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	config, err := configFromRequest(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	claims := userFrom(r.Context())
	id, err := s.games.Create(r.Context(), store.CreateParams{
		Config: config, BlackUserID: claims.UserID, WhiteUserID: request.Opponent,
		StartedAt: time.Now(),
	})
	if err != nil {
		s.config.Logger.Error("creating game", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Không tạo được ván.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"game_id": id, "your_color": "black"})
}

func configFromRequest(request createGameRequest) (game.Config, error) {
	size := request.BoardSize
	if size == 0 {
		size = 19
	}
	if !rules.IsSupportedSize(size) {
		return game.Config{}, fmt.Errorf("cỡ bàn %d không được hỗ trợ", size)
	}
	ruleSet := rules.RuleSet(request.Rules)
	if ruleSet == "" {
		ruleSet = rules.Japanese
	}
	if ruleSet != rules.Japanese && ruleSet != rules.Chinese {
		return game.Config{}, fmt.Errorf("hệ luật %q không được hỗ trợ", request.Rules)
	}

	control := game.TimeControl{
		Kind:       game.TimeControlKind(request.TimeControl.Kind),
		MainTime:   time.Duration(request.TimeControl.MainTimeMs) * time.Millisecond,
		Periods:    request.TimeControl.Periods,
		PeriodTime: time.Duration(request.TimeControl.PeriodMs) * time.Millisecond,
		Increment:  time.Duration(request.TimeControl.IncrementMs) * time.Millisecond,
		PerMove:    time.Duration(request.TimeControl.DaysPerMove) * 24 * time.Hour,
	}
	if control.Kind == "" {
		control = game.TimeControl{Kind: game.Byoyomi, MainTime: 20 * time.Minute,
			Periods: 3, PeriodTime: 30 * time.Second}
	}
	if err := control.Validate(); err != nil {
		return game.Config{}, err
	}
	komi := request.Komi
	if komi == 0 {
		komi = rules.DefaultKomi(ruleSet, request.Handicap)
	}
	return game.Config{
		Size: size, Rules: ruleSet, Komi: komi, Handicap: request.Handicap,
		TimeControl: control, MaxUndos: 3,
	}, nil
}

func (s *Server) handleGetGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	loaded, err := s.games.Load(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Không tìm thấy ván cờ.")
		return
	}
	if err != nil {
		s.config.Logger.Error("loading game", "game_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Không đọc được ván.")
		return
	}
	writeJSON(w, http.StatusOK, gameStateOf(id, store.RulesVersion, loaded.Session))
}

func hexHash(hash uint64) string { return fmt.Sprintf("0x%016x", hash) }

type gameSummaryJSON struct {
	GameID       string      `json:"game_id"`
	BoardSize    int         `json:"board_size"`
	Rules        string      `json:"rules"`
	Phase        string      `json:"phase"`
	ToPlay       string      `json:"to_play,omitempty"`
	MyColor      string      `json:"my_color"`
	YourTurn     bool        `json:"your_turn"`
	OpponentID   string      `json:"opponent_id,omitempty"`
	OpponentName string      `json:"opponent_name"`
	MoveNumber   int         `json:"move_no"`
	MoveDeadline *time.Time  `json:"move_deadline,omitempty"`
	LastActivity time.Time   `json:"last_activity_at"`
	Result       *resultJSON `json:"result,omitempty"`
}

// handleListGames backs the home screen: every game the caller is in, active
// ones first (docs/06 §2.5).
func (s *Server) handleListGames(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	list, err := s.games.ListForUser(r.Context(), claims.UserID, 50)
	if err != nil {
		s.config.Logger.Error("listing games", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Không đọc được danh sách ván.")
		return
	}
	items := make([]gameSummaryJSON, 0, len(list))
	for _, g := range list {
		item := gameSummaryJSON{
			GameID: g.ID, BoardSize: g.BoardSize, Rules: string(g.Rules), Phase: string(g.Phase),
			MyColor: g.MyColor.String(), OpponentID: g.OpponentID, OpponentName: g.OpponentName,
			MoveNumber:   g.MoveNumber,
			MoveDeadline: g.MoveDeadline, LastActivity: g.LastActivity, Result: resultOf(g.Result),
		}
		if g.Phase == rules.Playing {
			item.ToPlay = g.ToPlay.String()
			item.YourTurn = g.ToPlay == g.MyColor
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

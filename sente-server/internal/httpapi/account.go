package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

// Sessions, account deletion, moderation and game export.

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type sessionResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// issueSession mints the access/refresh pair a client keeps.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, userID string, guest bool) (sessionResponse, bool) {
	access, expiry, err := s.config.Issuer.Issue(userID, guest)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không tạo được phiên.")
		return sessionResponse{}, false
	}
	refresh, err := s.refresh.Issue(r.Context(), userID)
	if err != nil {
		s.config.Logger.Error("issuing refresh token", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không tạo được phiên.")
		return sessionResponse{}, false
	}
	return sessionResponse{
		AccessToken: access, ExpiresIn: int(time.Until(expiry).Seconds()), RefreshToken: refresh,
	}, true
}

// handleRefresh rotates a refresh token. Every failure is the same 401: which
// kind of invalid the token was is not the caller's business (docs/08 §2.2).
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil ||
		request.RefreshToken == "" {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	userID, next, err := s.refresh.Rotate(r.Context(), request.RefreshToken)
	switch {
	case errors.Is(err, store.ErrRefreshReused):
		s.config.Logger.Warn("refresh token reuse detected", "user_id", userID)
		writeError(w, r, http.StatusUnauthorized, "session_revoked", "Phiên đăng nhập đã bị thu hồi. Vui lòng đăng nhập lại.")
		return
	case err != nil:
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Phiên đăng nhập đã hết hạn.")
		return
	}
	user, err := s.users.Get(r.Context(), userID)
	if err != nil {
		// Deleted accounts keep their row but lose their sessions.
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Tài khoản không còn tồn tại.")
		return
	}
	access, expiry, err := s.config.Issuer.Issue(user.ID, user.IsGuest)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không tạo được phiên.")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		AccessToken: access, ExpiresIn: int(time.Until(expiry).Seconds()), RefreshToken: next,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request)
	if request.RefreshToken != "" {
		_ = s.refresh.Revoke(r.Context(), request.RefreshToken)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	user, err := s.users.Get(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Tài khoản không còn tồn tại.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": user.ID, "display_name": user.DisplayName,
		"friend_code": user.FriendCode, "is_guest": user.IsGuest,
	})
}

type updateMeRequest struct {
	DisplayName string `json:"display_name"`
}

// handleUpdateMe lets a person name themselves. Apple hands over a name only on
// the very first sign-in, so this is the reliable way to get one.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var request updateMeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	name := strings.TrimSpace(request.DisplayName)
	if length := utf8.RuneCountInString(name); length < 2 || length > 24 {
		writeError(w, r, http.StatusBadRequest, "invalid_name", "Tên cần từ 2 đến 24 ký tự.")
		return
	}
	claims := userFrom(r.Context())
	if err := s.users.SetDisplayName(r.Context(), claims.UserID, name); err != nil {
		s.config.Logger.Error("renaming user", "user_id", claims.UserID, "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đổi được tên.")
		return
	}
	s.handleMe(w, r)
}

// handleStats backs the profile's record section.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	stats, err := s.games.StatsForUser(r.Context(), claims.UserID)
	if err != nil {
		s.config.Logger.Error("reading stats", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được thống kê.")
		return
	}
	bySize := map[string]map[string]int{}
	for size, item := range stats.BySize {
		bySize[fmt.Sprint(size)] = map[string]int{"games": item.Games, "wins": item.Wins, "losses": item.Losses}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"games": stats.Games, "wins": stats.Wins, "losses": stats.Losses, "draws": stats.Draws,
		"by_size": bySize,
	})
}

// handleDeleteAccount is the in-app deletion App Store guideline 5.1.1(v) demands.
// Synchronous here because the work is small; the response is final.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	if err := s.users.DeleteAccount(r.Context(), claims.UserID); err != nil {
		s.config.Logger.Error("deleting account", "user_id", claims.UserID, "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không xóa được tài khoản.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type reportRequest struct {
	UserID   string `json:"user_id"`
	GameID   string `json:"game_id"`
	Category string `json:"category"`
	Note     string `json:"note"`
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	var request reportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&request); err != nil {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	err := s.moderation.Report(r.Context(), claims.UserID, request.UserID, request.GameID,
		request.Category, request.Note)
	switch {
	case errors.Is(err, store.ErrSelfTarget), errors.Is(err, store.ErrUnknownCategory):
		writeError(w, r, http.StatusBadRequest, "invalid_report", "Báo cáo không hợp lệ.")
		return
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "internal", "Không gửi được báo cáo.")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

type blockRequest struct {
	UserID string `json:"user_id"`
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	var request blockRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil ||
		request.UserID == "" {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	if err := s.moderation.Block(r.Context(), claims.UserID, request.UserID); err != nil {
		if errors.Is(err, store.ErrSelfTarget) {
			writeError(w, r, http.StatusBadRequest, "invalid_block", "Không thể chặn chính mình.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal", "Không chặn được.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	if err := s.moderation.Unblock(r.Context(), claims.UserID, r.PathValue("id")); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không bỏ chặn được.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSGF exports a game. Generated on demand from the move log, never stored:
// SGF is derived data (docs/05 §13).
func (s *Server) handleSGF(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	loaded, err := s.games.Load(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy ván cờ.")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được ván.")
		return
	}
	names := s.playerNames(r, loaded)
	record := rules.GameRecord{
		Size: loaded.Session.Config.Size, Rules: loaded.Session.Config.Rules,
		Komi: loaded.Session.Engine.Komi, Handicap: loaded.Session.Config.Handicap,
		HandicapStones: rules.HandicapStones(loaded.Session.Config.Handicap, loaded.Session.Config.Size),
		BlackPlayer:    names[0], WhitePlayer: names[1],
		Date:   loaded.LastActivityAt.UTC().Format("2006-01-02"),
		Result: loaded.Session.Result(), Moves: loaded.Session.Moves,
	}
	w.Header().Set("Content-Type", "application/x-go-sgf; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sente-`+id[:8]+`.sgf"`)
	_, _ = w.Write([]byte(rules.EncodeSGF(record)))
}

func (s *Server) playerNames(r *http.Request, loaded *store.LoadedGame) [2]string {
	names := [2]string{"Đen", "Trắng"}
	if black, err := s.users.Get(r.Context(), loaded.BlackUserID); err == nil {
		names[0] = black.DisplayName
	}
	if white, err := s.users.Get(r.Context(), loaded.WhiteUserID); err == nil {
		names[1] = white.DisplayName
	}
	return names
}

type moveJSON struct {
	MoveNo int     `json:"move_no"`
	Color  string  `json:"color"`
	Kind   string  `json:"kind"`
	Point  *string `json:"point,omitempty"`
}

// handleMoves lists a game's moves for replay (docs/01 FR-R3).
func (s *Server) handleMoves(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	loaded, err := s.games.Load(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy ván cờ.")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được ván.")
		return
	}
	size := loaded.Session.Config.Size
	items := make([]moveJSON, 0, len(loaded.Session.Moves))
	for i, m := range loaded.Session.Moves {
		item := moveJSON{MoveNo: i + 1, Color: m.Player.String(), Kind: moveKindName(m.Move)}
		if m.Move.Kind == rules.KindPlay {
			text := rules.CoordinateText(m.Move.Point, size)
			item.Point = &text
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"game_id": id, "board_size": size, "rules": string(loaded.Session.Config.Rules),
		"komi": loaded.Session.Engine.Komi, "handicap": loaded.Session.Config.Handicap,
		"items": items,
	})
}

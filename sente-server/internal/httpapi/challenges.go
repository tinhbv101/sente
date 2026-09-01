package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"sente.app/server/internal/store"
)

// Invitations: the "send your friend a link" flow (docs/01 J1).

type createChallengeRequest struct {
	createGameRequest
	// InviteeUserID directs the invitation at one person. Empty means an open
	// link that anyone holding it may accept.
	InviteeUserID string `json:"invitee_user_id"`
	CreatorColor  string `json:"creator_color"`
}

type challengeResponse struct {
	Code         string          `json:"code"`
	ShareURL     string          `json:"share_url,omitempty"`
	CreatorName  string          `json:"creator_name,omitempty"`
	CreatorColor string          `json:"creator_color"`
	Status       string          `json:"status"`
	Config       challengeConfig `json:"config"`
	GameID       string          `json:"game_id,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at"`
	IsMine       bool            `json:"is_mine"`
}

type challengeConfig struct {
	BoardSize   int     `json:"board_size"`
	Rules       string  `json:"rules"`
	Komi        float64 `json:"komi"`
	Handicap    int     `json:"handicap"`
	TimeControl any     `json:"time_control"`
}

func (s *Server) challengeResponse(challenge store.Challenge, viewer string) challengeResponse {
	response := challengeResponse{
		Code: challenge.Code, CreatorName: challenge.CreatorName,
		CreatorColor: challenge.CreatorColor, Status: string(challenge.Status),
		GameID: challenge.GameID, ExpiresAt: challenge.ExpiresAt,
		IsMine: viewer != "" && viewer == challenge.CreatorID,
		Config: challengeConfig{
			BoardSize: challenge.Config.Size, Rules: string(challenge.Config.Rules),
			Komi: challenge.Config.Komi, Handicap: challenge.Config.Handicap,
			TimeControl: challenge.Config.TimeControl,
		},
	}
	if s.config.PublicBaseURL != "" {
		response.ShareURL = s.config.PublicBaseURL + "/j/" + challenge.Code
	}
	return response
}

func (s *Server) handleCreateChallenge(w http.ResponseWriter, r *http.Request) {
	var request createChallengeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&request); err != nil {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	config, err := configFromRequest(request.createGameRequest)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	claims := userFrom(r.Context())
	challenge, err := s.challenges.Create(r.Context(), store.CreateChallengeParams{
		CreatorID: claims.UserID, InviteeID: request.InviteeUserID,
		Config: config, CreatorColor: request.CreatorColor,
	})
	if err != nil {
		s.config.Logger.Warn("creating invitation", "error", err)
		writeError(w, r, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	// A directed invitation (a rematch) reaches the other person's phone.
	if request.InviteeUserID != "" && s.config.Notifier.Enabled() {
		if creator, err := s.users.Get(r.Context(), claims.UserID); err == nil {
			s.config.Notifier.ChallengeReceived(request.InviteeUserID, creator.DisplayName, challenge.Code)
		}
	}
	writeJSON(w, http.StatusCreated, s.challengeResponse(challenge, claims.UserID))
}

// handleGetChallenge is the preview a link opens. It needs no token: the point of
// a link is that someone can look at it before deciding to sign up (docs/01 J1).
func (s *Server) handleGetChallenge(w http.ResponseWriter, r *http.Request) {
	challenge, err := s.challenges.ByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		// Unknown and used look the same, so the endpoint cannot be used to find
		// out which codes exist (docs/08 §4.2).
		writeError(w, r, http.StatusNotFound, "challenge_gone",
			"Lời mời này không còn hiệu lực.")
		return
	}
	viewer := ""
	if claims, err := s.claimsFrom(r); err == nil {
		viewer = claims.UserID
	}
	writeJSON(w, http.StatusOK, s.challengeResponse(challenge, viewer))
}

func (s *Server) handleAcceptChallenge(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	accepted, err := s.challenges.Accept(r.Context(), r.PathValue("code"), claims.UserID)
	switch {
	case errors.Is(err, store.ErrOwnChallenge):
		writeError(w, r, http.StatusConflict, "own_challenge",
			"Đây là lời mời của chính bạn.")
		return
	case errors.Is(err, store.ErrChallengeGone):
		writeError(w, r, http.StatusConflict, "challenge_gone",
			"Lời mời này không còn hiệu lực.")
		return
	case err != nil:
		s.config.Logger.Error("accepting invitation", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không nhận được lời mời.")
		return
	}

	loaded, err := s.games.Load(r.Context(), accepted.GameID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không mở được ván.")
		return
	}
	colour := "white"
	if loaded.BlackUserID == claims.UserID {
		colour = "black"
	}
	if s.config.Notifier.Enabled() {
		if acceptor, err := s.users.Get(r.Context(), claims.UserID); err == nil {
			s.config.Notifier.InvitationAccepted(accepted.CreatorID, acceptor.DisplayName, accepted.GameID)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"game_id": accepted.GameID, "your_color": colour,
	})
}

func (s *Server) handleDeclineChallenge(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	if err := s.challenges.Decline(r.Context(), r.PathValue("code"), claims.UserID); err != nil {
		writeError(w, r, http.StatusConflict, "challenge_gone", "Lời mời này không còn hiệu lực.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelChallenge(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	if err := s.challenges.Cancel(r.Context(), r.PathValue("code"), claims.UserID); err != nil {
		writeError(w, r, http.StatusConflict, "challenge_gone", "Lời mời này không còn hiệu lực.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListChallenges(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	list, err := s.challenges.ListForUser(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được danh sách.")
		return
	}
	items := make([]challengeResponse, 0, len(list))
	for _, challenge := range list {
		items = append(items, s.challengeResponse(challenge, claims.UserID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleRematch turns a finished game into a directed invitation with the same
// settings and swapped colours. The client never re-sends a config it might
// have reconstructed wrong; the stored game is the source.
func (s *Server) handleRematch(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	loaded, err := s.games.Load(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy ván cờ.")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được ván.")
		return
	}
	var opponent, myNewColor string
	switch claims.UserID {
	case loaded.BlackUserID:
		opponent, myNewColor = loaded.WhiteUserID, "white"
	case loaded.WhiteUserID:
		opponent, myNewColor = loaded.BlackUserID, "black"
	default:
		writeError(w, r, http.StatusForbidden, "not_found", "Bạn không ở trong ván này.")
		return
	}
	if opponent == "" {
		writeError(w, r, http.StatusConflict, "challenge_gone", "Đối thủ không còn tài khoản.")
		return
	}
	challenge, err := s.challenges.Create(r.Context(), store.CreateChallengeParams{
		CreatorID: claims.UserID, InviteeID: opponent,
		Config: loaded.Session.Config, CreatorColor: myNewColor,
	})
	if err != nil {
		s.config.Logger.Warn("creating rematch", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không tạo được lời mời.")
		return
	}
	if s.config.Notifier.Enabled() {
		if creator, err := s.users.Get(r.Context(), claims.UserID); err == nil {
			s.config.Notifier.ChallengeReceived(opponent, creator.DisplayName, challenge.Code)
		}
	}
	writeJSON(w, http.StatusCreated, s.challengeResponse(challenge, claims.UserID))
}

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"sente.app/server/internal/store"
)

// Friends (docs/01 FR-A3). Only accounts signed in with Apple take part: a
// guest account is a phone, not a person.

type friendResponse struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	FriendCode  string    `json:"friend_code"`
	Status      string    `json:"status"`
	Incoming    bool      `json:"incoming"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) handleListFriends(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	list, err := s.friends.List(r.Context(), claims.UserID)
	if err != nil {
		s.config.Logger.Error("listing friends", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được danh sách bạn bè.")
		return
	}
	items := make([]friendResponse, 0, len(list))
	for _, friend := range list {
		items = append(items, friendResponse{
			UserID: friend.UserID, DisplayName: friend.DisplayName, FriendCode: friend.FriendCode,
			Status: string(friend.Status), Incoming: friend.Incoming, CreatedAt: friend.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleLookupFriendCode is the only way to find a stranger, and the shape
// docs/08 §4.2 pins down: one 404 for a code nobody owns, a guest's code, a
// deleted account and someone either side has blocked.
func (s *Server) handleLookupFriendCode(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	user, err := s.friends.ByCode(r.Context(), claims.UserID, normaliseCode(r.PathValue("code")))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy mã bạn bè này.")
		return
	}
	if err != nil {
		s.config.Logger.Error("looking up friend code", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không tra được mã.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": user.ID, "display_name": user.DisplayName, "friend_code": user.FriendCode,
	})
}

type friendRequestBody struct {
	UserID string `json:"user_id"`
}

// handleAddFriend sends a request, or accepts one already waiting from the same
// person -- both asking is agreement.
func (s *Server) handleAddFriend(w http.ResponseWriter, r *http.Request) {
	var request friendRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil ||
		!isUUID(request.UserID) {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	outcome, err := s.friends.Request(r.Context(), claims.UserID, request.UserID)
	switch {
	case errors.Is(err, store.ErrGuestAccount):
		writeError(w, r, http.StatusForbidden, "apple_required",
			"Hãy đăng nhập bằng Apple để kết bạn.")
		return
	case errors.Is(err, store.ErrNotFriendable):
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy người chơi này.")
		return
	case err != nil:
		s.config.Logger.Error("sending friend request", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không gửi được lời mời kết bạn.")
		return
	}
	// Only a real transition is worth a push; asking twice must not ring twice.
	// A crossed request that just became a friendship tells the other side so.
	if outcome.Changed {
		s.notifyFriend(r, request.UserID, claims.UserID, outcome.Status)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": string(outcome.Status)})
}

func (s *Server) handleAcceptFriend(w http.ResponseWriter, r *http.Request) {
	s.answerFriend(w, r, true)
}

func (s *Server) handleDeclineFriend(w http.ResponseWriter, r *http.Request) {
	s.answerFriend(w, r, false)
}

func (s *Server) answerFriend(w http.ResponseWriter, r *http.Request, accept bool) {
	other := r.PathValue("id")
	if !isUUID(other) {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	var err error
	if accept {
		err = s.friends.Accept(r.Context(), claims.UserID, other)
	} else {
		err = s.friends.Decline(r.Context(), claims.UserID, other)
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Lời mời kết bạn không còn.")
		return
	}
	if err != nil {
		s.config.Logger.Error("answering friend request", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không trả lời được lời mời.")
		return
	}
	if accept {
		s.notifyFriend(r, other, claims.UserID, store.FriendAccepted)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveFriend unfriends, or takes back a request we sent. A refusal is
// not ours to delete -- see store.Friends.Remove.
func (s *Server) handleRemoveFriend(w http.ResponseWriter, r *http.Request) {
	other := r.PathValue("id")
	if !isUUID(other) {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	err := s.friends.Remove(r.Context(), claims.UserID, other)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy người bạn này.")
		return
	}
	if err != nil {
		s.config.Logger.Error("removing friend", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không bỏ kết bạn được.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyFriend rings the other phone at most once a day per pair and direction.
// The rate limit bounds how fast someone can act, not how often the same person
// is disturbed; Redis is the memory, the way the sweeper warns about time
// (internal/sweep).
func (s *Server) notifyFriend(r *http.Request, targetID, fromID string, status store.FriendStatus) {
	if !s.config.Notifier.Enabled() {
		return
	}
	from, err := s.users.Get(r.Context(), fromID)
	if err != nil {
		return
	}
	if s.config.Redis != nil {
		key := "friendpush:" + string(status) + ":" + fromID + ":" + targetID
		set, err := s.config.Redis.SetNX(r.Context(), key, 1, 24*time.Hour).Result()
		if err != nil || !set {
			return
		}
	}
	if status == store.FriendAccepted {
		s.config.Notifier.FriendRequestAccepted(targetID, from.DisplayName, fromID)
		return
	}
	s.config.Notifier.FriendRequestReceived(targetID, from.DisplayName, fromID)
}

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	list, err := s.moderation.ListBlocked(r.Context(), claims.UserID)
	if err != nil {
		s.config.Logger.Error("listing blocks", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đọc được danh sách đã chặn.")
		return
	}
	items := make([]map[string]string, 0, len(list))
	for _, blocked := range list {
		items = append(items, map[string]string{
			"user_id": blocked.UserID, "display_name": blocked.DisplayName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"sente.app/server/internal/apple"
	"sente.app/server/internal/store"
)

// Sign in with Apple (docs/03 ADR-008). The app never becomes a login screen: a
// guest links an Apple ID from Settings and keeps everything.

type appleSignInRequest struct {
	IdentityToken string `json:"identity_token"`
	Nonce         string `json:"nonce"`
	FullName      string `json:"full_name"`
}

func (s *Server) handleAppleSignIn(w http.ResponseWriter, r *http.Request) {
	if s.config.Apple == nil {
		writeError(w, r, http.StatusNotFound, "not_available", "Máy chủ chưa bật đăng nhập Apple.")
		return
	}
	var request appleSignInRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil ||
		request.IdentityToken == "" {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	identity, err := s.config.Apple.Verify(r.Context(), request.IdentityToken, request.Nonce)
	if err != nil {
		s.config.Logger.Warn("apple sign-in rejected", "error", err)
		writeError(w, r, http.StatusUnauthorized, "invalid_identity_token", "Apple không xác nhận được đăng nhập này.")
		return
	}

	// A signed-in guest gets the Apple ID attached. Anyone else gets the account
	// that Apple ID already opens -- their own, from another phone -- or a new one.
	var current string
	if claims, err := s.claimsFrom(r); err == nil {
		current = claims.UserID
	}
	userID, err := s.identities.UserFor(r.Context(), store.ProviderApple, identity.Subject)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		userID = current
		if userID == "" {
			user, err := s.users.CreateGuest(r.Context())
			if err != nil {
				s.config.Logger.Error("creating account for apple sign-in", "error", err)
				writeError(w, r, http.StatusInternalServerError, "internal", "Không tạo được tài khoản.")
				return
			}
			userID = user.ID
		}
		if err := s.identities.Link(r.Context(), userID, store.ProviderApple, identity.Subject, identity.Email); err != nil {
			if errors.Is(err, store.ErrIdentityTaken) {
				// Only reachable in a race with another sign-in of the same Apple ID.
				writeError(w, r, http.StatusConflict, "identity_taken", "Apple ID này vừa được dùng ở nơi khác. Thử lại.")
				return
			}
			s.config.Logger.Error("linking apple identity", "user_id", userID, "error", err)
			writeError(w, r, http.StatusInternalServerError, "internal", "Không liên kết được tài khoản.")
			return
		}
		if request.FullName != "" {
			_ = s.users.SetDisplayName(r.Context(), userID, request.FullName)
		}
	default:
		s.config.Logger.Error("looking up apple identity", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đăng nhập được.")
		return
	}

	user, err := s.users.Get(r.Context(), userID)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Tài khoản không còn tồn tại.")
		return
	}
	session, ok := s.issueSession(w, r, user.ID, user.IsGuest)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id": user.ID, "display_name": user.DisplayName,
			"friend_code": user.FriendCode, "is_guest": user.IsGuest,
		},
		"access_token":  session.AccessToken,
		"expires_in":    session.ExpiresIn,
		"refresh_token": session.RefreshToken,
	})
}

// handleAppleNotification receives Apple's server-to-server notices. A revoked
// sign-in unlinks the Apple ID and ends every session; the account itself stays,
// as a guest, so the games are not lost (docs/08 §2.4).
func (s *Server) handleAppleNotification(w http.ResponseWriter, r *http.Request) {
	if s.config.Apple == nil {
		writeError(w, r, http.StatusNotFound, "not_available", "Máy chủ chưa bật đăng nhập Apple.")
		return
	}
	var body struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil || body.Payload == "" {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	event, err := s.config.Apple.VerifyNotification(r.Context(), body.Payload)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_payload", "Thông báo không hợp lệ.")
		return
	}
	switch event.Type {
	case apple.EventConsentRevoked, apple.EventAccountDeleted:
		userID, err := s.identities.Unlink(r.Context(), store.ProviderApple, event.Subject)
		if err == nil {
			_ = s.refresh.RevokeAllForUser(r.Context(), userID)
			s.config.Logger.Info("apple sign-in revoked", "user_id", userID, "event", event.Type)
		}
	}
	w.WriteHeader(http.StatusOK)
}

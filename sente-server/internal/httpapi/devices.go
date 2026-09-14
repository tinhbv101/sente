package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"sente.app/server/internal/store"
)

type registerDeviceRequest struct {
	Token       string `json:"apns_token"`
	Environment string `json:"environment"`
	AppVersion  string `json:"app_version"`
}

// APNs tokens are hex; 32 bytes today, but Apple reserves the right to grow them.
var apnsTokenPattern = regexp.MustCompile(`^[0-9a-fA-F]{64,200}$`)

func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var request registerDeviceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	if !apnsTokenPattern.MatchString(request.Token) ||
		(request.Environment != "sandbox" && request.Environment != "production") {
		writeError(w, r, http.StatusBadRequest, "invalid_device", "Thông tin thiết bị không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	if err := s.devices.Register(r.Context(), claims.UserID, request.Token, request.Environment, request.AppVersion); err != nil {
		s.config.Logger.Error("registering device", "user_id", claims.UserID, "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không đăng ký được thiết bị.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnregisterDevice(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	if err := s.devices.UnregisterForUser(r.Context(), claims.UserID, r.PathValue("token")); err != nil {
		s.config.Logger.Error("unregistering device", "user_id", claims.UserID, "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không hủy được thiết bị.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The notification kinds a person may switch off, one by one.
var knownPrefKeys = map[string]bool{
	"turn": true, "low_time": true, "game_end": true, "invite": true, "friend": true,
}

func (s *Server) handleDevicePrefs(w http.ResponseWriter, r *http.Request) {
	var patch map[string]bool
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&patch); err != nil || len(patch) == 0 {
		writeError(w, r, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	for key := range patch {
		if !knownPrefKeys[key] {
			writeError(w, r, http.StatusBadRequest, "invalid_device", "Loại thông báo không hợp lệ.")
			return
		}
	}
	claims := userFrom(r.Context())
	prefs, err := s.devices.SetPrefs(r.Context(), claims.UserID, r.PathValue("token"), patch)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "Không tìm thấy thiết bị.")
		return
	}
	if err != nil {
		s.config.Logger.Error("updating push prefs", "error", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "Không lưu được cài đặt.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

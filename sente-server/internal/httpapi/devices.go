package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"
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
		writeError(w, http.StatusBadRequest, "malformed", "Yêu cầu không hợp lệ.")
		return
	}
	if !apnsTokenPattern.MatchString(request.Token) ||
		(request.Environment != "sandbox" && request.Environment != "production") {
		writeError(w, http.StatusBadRequest, "invalid_device", "Thông tin thiết bị không hợp lệ.")
		return
	}
	claims := userFrom(r.Context())
	if err := s.devices.Register(r.Context(), claims.UserID, request.Token, request.Environment, request.AppVersion); err != nil {
		s.config.Logger.Error("registering device", "user_id", claims.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Không đăng ký được thiết bị.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnregisterDevice(w http.ResponseWriter, r *http.Request) {
	claims := userFrom(r.Context())
	if err := s.devices.UnregisterForUser(r.Context(), claims.UserID, r.PathValue("token")); err != nil {
		s.config.Logger.Error("unregistering device", "user_id", claims.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Không hủy được thiết bị.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
)

type AuthHandler struct {
	jwt      *auth.JWTService
	users    *auth.UserStore
	sessions *auth.SessionStore
}

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	user, err := h.users.Create(req.Username, req.Password)
	if err != nil {
		slog.Error("create user failed", "error", err)
		writeError(w, http.StatusConflict, "username already taken")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(user.ID, user.Username)
	if err != nil {
		slog.Error("generate access token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, refreshToken, err := h.sessions.Create(user.ID)
	if err != nil {
		slog.Error("create session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user":          user,
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.users.Authenticate(req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(user.ID, user.Username)
	if err != nil {
		slog.Error("generate access token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, refreshToken, err := h.sessions.Create(user.ID)
	if err != nil {
		slog.Error("create session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user":          user,
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	session, err := h.sessions.ValidateRefresh(req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	user, err := h.users.GetByID(session.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	// Rotate: delete old session, create new one
	h.sessions.Delete(session.ID)

	accessToken, err := h.jwt.GenerateAccessToken(user.ID, user.Username)
	if err != nil {
		slog.Error("generate access token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, newRefresh, err := h.sessions.Create(user.ID)
	if err != nil {
		slog.Error("create session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": newRefresh,
	})
}

package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/sync"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"
)

type VaultHandler struct {
	vaults *vault.Store
	engine *sync.Engine
}

type createVaultRequest struct {
	Name string `json:"name"`
}

func (h *VaultHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	vaults, err := h.vaults.ListVaults(claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list vaults")
		return
	}
	if vaults == nil {
		vaults = []vault.Vault{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"vaults": vaults})
}

func (h *VaultHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)

	var req createVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	v, err := h.vaults.CreateVault(req.Name, claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create vault")
		return
	}

	writeJSON(w, http.StatusCreated, v)
}

func (h *VaultHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")

	v, err := h.vaults.GetVault(id, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	writeJSON(w, http.StatusOK, v)
}

func (h *VaultHandler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")

	if err := h.vaults.DeleteVault(id, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *VaultHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")

	// Verify ownership
	if _, err := h.vaults.GetVault(id, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	files, err := h.vaults.ListFiles(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list files")
		return
	}
	if files == nil {
		files = []vault.FileMeta{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"files": files})
}

func (h *VaultHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")
	path := r.PathValue("path")

	if _, err := h.vaults.GetVault(id, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	f, err := h.vaults.GetFile(id, path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	if f.IsBinary {
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("X-File-Hash", f.Hash)
	w.Write(f.Content)
}

func (h *VaultHandler) PutFile(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")
	path := r.PathValue("path")

	if _, err := h.vaults.GetVault(id, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	content, err := io.ReadAll(io.LimitReader(r.Body, 50<<20)) // 50MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	isBinary := isBinaryContent(path, content)
	f, err := h.vaults.PutFile(id, path, content, isBinary)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}

	// Update CRDT document so the change propagates to connected peers
	if h.engine != nil {
		if err := h.engine.PutFileFromREST(id, path, content, isBinary); err != nil {
			slog.Warn("failed to update CRDT from REST put", "vault", id, "path", path, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, f)
}

func (h *VaultHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")
	path := r.PathValue("path")

	if _, err := h.vaults.GetVault(id, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	if err := h.vaults.DeleteFile(id, path); err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	// Update CRDT document so the deletion propagates to connected peers
	if h.engine != nil {
		if err := h.engine.DeleteFileFromREST(id, path); err != nil {
			slog.Warn("failed to update CRDT from REST delete", "vault", id, "path", path, "error", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *VaultHandler) Snapshot(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	id := r.PathValue("id")

	if _, err := h.vaults.GetVault(id, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	snapshot, err := h.vaults.Snapshot(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get snapshot")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"files": snapshot})
}

func isBinaryContent(path string, content []byte) bool {
	// Check by extension
	binaryExts := []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".tar", ".gz", ".mp3", ".mp4", ".wav", ".ogg"}
	lower := strings.ToLower(path)
	for _, ext := range binaryExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	// Check for null bytes in first 512 bytes
	check := content
	if len(check) > 512 {
		check = check[:512]
	}
	for _, b := range check {
		if b == 0 {
			return true
		}
	}

	return false
}

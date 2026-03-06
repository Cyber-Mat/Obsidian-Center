package api

import (
	"encoding/json"
	"net/http"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/graph"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"
)

// GraphHandler serves link graph API endpoints.
type GraphHandler struct {
	vaults *vault.Store
	graphs *graph.Store
}

// Links returns all forward links from a given file.
// GET /api/vaults/{id}/graph/links?path=note.md
func (h *GraphHandler) Links(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	vaultID := r.PathValue("id")

	if _, err := h.vaults.GetVault(vaultID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	links, err := h.graphs.GetForwardLinks(vaultID, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get links")
		return
	}
	if links == nil {
		links = []graph.StoredLink{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"links": links})
}

// Backlinks returns all files linking to a given file.
// GET /api/vaults/{id}/graph/backlinks?path=note.md
func (h *GraphHandler) Backlinks(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	vaultID := r.PathValue("id")

	if _, err := h.vaults.GetVault(vaultID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	links, err := h.graphs.GetBacklinks(vaultID, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get backlinks")
		return
	}
	if links == nil {
		links = []graph.StoredLink{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"backlinks": links})
}

// Traverse performs a cycle-aware graph traversal.
// POST /api/vaults/{id}/graph/traverse
func (h *GraphHandler) Traverse(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	vaultID := r.PathValue("id")

	if _, err := h.vaults.GetVault(vaultID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	var req graph.TraverseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Start == "" {
		writeError(w, http.StatusBadRequest, "start is required")
		return
	}

	nodes, err := graph.Traverse(h.graphs, vaultID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "traversal failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"nodes": nodes})
}

// Stats returns graph-level metrics for a vault.
// GET /api/vaults/{id}/graph/stats
func (h *GraphHandler) Stats(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	vaultID := r.PathValue("id")

	if _, err := h.vaults.GetVault(vaultID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, "vault not found")
		return
	}

	stats, err := h.graphs.GetStats(vaultID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get graph stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

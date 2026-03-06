package api

import (
	"net/http"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/graph"
	"github.com/Cyber-Mat/obsidian-center/server/internal/sync"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"
)

func NewRouter(jwt *auth.JWTService, users *auth.UserStore, sessions *auth.SessionStore, vaults *vault.Store, engine *sync.Engine, graphs *graph.Store) http.Handler {
	mux := http.NewServeMux()

	authHandler := &AuthHandler{jwt: jwt, users: users, sessions: sessions}
	vaultHandler := &VaultHandler{vaults: vaults}
	syncHandler := NewSyncHandler(vaults, jwt, engine)
	graphHandler := &GraphHandler{vaults: vaults, graphs: graphs}

	// Health check (public, used by Docker healthcheck)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Public routes
	mux.HandleFunc("POST /api/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/auth/refresh", authHandler.Refresh)

	// Protected routes
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/vaults", vaultHandler.List)
	protected.HandleFunc("POST /api/vaults", vaultHandler.Create)
	protected.HandleFunc("GET /api/vaults/{id}", vaultHandler.Get)
	protected.HandleFunc("DELETE /api/vaults/{id}", vaultHandler.Delete)
	protected.HandleFunc("GET /api/vaults/{id}/files", vaultHandler.ListFiles)
	protected.HandleFunc("GET /api/vaults/{id}/files/{path...}", vaultHandler.GetFile)
	protected.HandleFunc("PUT /api/vaults/{id}/files/{path...}", vaultHandler.PutFile)
	protected.HandleFunc("DELETE /api/vaults/{id}/files/{path...}", vaultHandler.DeleteFile)
	protected.HandleFunc("GET /api/vaults/{id}/snapshot", vaultHandler.Snapshot)

	// Graph routes
	protected.HandleFunc("GET /api/vaults/{id}/graph/links", graphHandler.Links)
	protected.HandleFunc("GET /api/vaults/{id}/graph/backlinks", graphHandler.Backlinks)
	protected.HandleFunc("POST /api/vaults/{id}/graph/traverse", graphHandler.Traverse)
	protected.HandleFunc("GET /api/vaults/{id}/graph/stats", graphHandler.Stats)

	// WebSocket (auth handled inside the handler via query param or first message)
	mux.HandleFunc("/api/sync/{id}", syncHandler.HandleWebSocket)

	// Mount protected routes with auth middleware
	mux.Handle("/api/", auth.Middleware(jwt)(protected))

	return mux
}

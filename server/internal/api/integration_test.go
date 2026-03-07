package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cyber-Mat/obsidian-center/server/internal/api"
	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/graph"
	syncpkg "github.com/Cyber-Mat/obsidian-center/server/internal/sync"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"

	"github.com/gorilla/websocket"
)

// testEnv holds all the dependencies for integration tests.
type testEnv struct {
	server       *httptest.Server
	jwtService   *auth.JWTService
	userStore    *auth.UserStore
	sessionStore *auth.SessionStore
	vaultStore   *vault.Store
	syncEngine   *syncpkg.Engine
	graphStore   *graph.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	db, err := vault.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := vault.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	jwtSvc := auth.NewJWTService([]byte("test-secret-key-for-integration"))
	users := auth.NewUserStore(db)
	sessions := auth.NewSessionStore(db)
	vaults := vault.NewStore(db)
	graphs := graph.NewStore(db)
	adapter := syncpkg.NewStoreAdapter(vaults)
	engine := syncpkg.NewEngine(adapter, graphs)
	t.Cleanup(func() { engine.Shutdown() })

	router := api.NewRouter(jwtSvc, users, sessions, vaults, engine, graphs, nil)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testEnv{
		server:       srv,
		jwtService:   jwtSvc,
		userStore:    users,
		sessionStore: sessions,
		vaultStore:   vaults,
		syncEngine:   engine,
		graphStore:   graphs,
	}
}

// helper: register a user and return access + refresh tokens.
func (e *testEnv) registerUser(t *testing.T, username, password string) (accessToken, refreshToken string) {
	t.Helper()
	body := jsonBody(t, map[string]string{"username": username, "password": password})
	resp := e.post(t, "/api/auth/register", body, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register: expected 201, got %d: %s", resp.StatusCode, b)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result["access_token"].(string), result["refresh_token"].(string)
}

// helper: create a vault and return its ID.
func (e *testEnv) createVault(t *testing.T, name, token string) string {
	t.Helper()
	resp := e.post(t, "/api/vaults", jsonBody(t, map[string]string{"name": name}), token)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create vault: expected 201, got %d: %s", resp.StatusCode, b)
	}
	data := readJSON(t, resp)
	// Vault is returned directly (not nested under "vault" key)
	id, ok := data["id"].(string)
	if !ok || id == "" {
		t.Fatalf("create vault: missing id in response: %v", data)
	}
	return id
}

func (e *testEnv) post(t *testing.T, path string, body io.Reader, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", e.server.URL+path, body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (e *testEnv) get(t *testing.T, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", e.server.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *testEnv) put(t *testing.T, path string, body io.Reader, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PUT", e.server.URL+path, body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	return resp
}

func (e *testEnv) delete(t *testing.T, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", e.server.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func jsonBody(t *testing.T, v interface{}) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(b)
}

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return json.RawMessage(b)
}

func readJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return m
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// ─── Health ───

func TestHealthCheck(t *testing.T) {
	env := newTestEnv(t)
	resp := env.get(t, "/api/health", "")
	data := readJSON(t, resp)
	if data["status"] != "ok" {
		t.Fatalf("health: expected status=ok, got %v", data["status"])
	}
}

// ─── Auth Flow ───

func TestRegisterAndLogin(t *testing.T) {
	env := newTestEnv(t)

	// Register
	accessToken, refreshToken := env.registerUser(t, "alice", "password123")
	if accessToken == "" || refreshToken == "" {
		t.Fatal("register: empty tokens")
	}

	// Login with same credentials
	resp := env.post(t, "/api/auth/login",
		jsonBody(t, map[string]string{"username": "alice", "password": "password123"}), "")
	if resp.StatusCode != 200 {
		resp.Body.Close()
		t.Fatalf("login: expected 200, got %d", resp.StatusCode)
	}
	data := readJSON(t, resp)
	if data["access_token"].(string) == "" {
		t.Fatal("login: empty access token")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	env := newTestEnv(t)
	env.registerUser(t, "bob", "password123")

	resp := env.post(t, "/api/auth/register",
		jsonBody(t, map[string]string{"username": "bob", "password": "password456"}), "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d", resp.StatusCode)
	}
}

func TestRegisterValidation(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		name     string
		username string
		password string
		wantCode int
	}{
		{"empty username", "", "password123", 400},
		{"empty password", "alice", "", 400},
		{"short password", "alice", "short", 400},
		{"short username", "ab", "password123", 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.post(t, "/api/auth/register",
				jsonBody(t, map[string]string{"username": tt.username, "password": tt.password}), "")
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantCode {
				t.Fatalf("expected %d, got %d", tt.wantCode, resp.StatusCode)
			}
		})
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	env := newTestEnv(t)
	env.registerUser(t, "alice", "password123")

	resp := env.post(t, "/api/auth/login",
		jsonBody(t, map[string]string{"username": "alice", "password": "wrongpass"}), "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login: expected 401, got %d", resp.StatusCode)
	}
}

func TestTokenRefresh(t *testing.T) {
	env := newTestEnv(t)
	_, refreshToken := env.registerUser(t, "alice", "password123")

	resp := env.post(t, "/api/auth/refresh",
		jsonBody(t, map[string]string{"refresh_token": refreshToken}), "")
	data := readJSON(t, resp)

	newAccess := data["access_token"].(string)
	newRefresh := data["refresh_token"].(string)
	if newAccess == "" || newRefresh == "" {
		t.Fatal("refresh: empty tokens")
	}

	// Old refresh token should no longer work
	resp2 := env.post(t, "/api/auth/refresh",
		jsonBody(t, map[string]string{"refresh_token": refreshToken}), "")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old refresh token should be invalid, got %d", resp2.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	env := newTestEnv(t)
	_, refreshToken := env.registerUser(t, "alice", "password123")

	resp := env.post(t, "/api/auth/logout",
		jsonBody(t, map[string]string{"refresh_token": refreshToken}), "")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("logout: expected 200, got %d", resp.StatusCode)
	}

	// Refresh token should be invalid after logout
	resp2 := env.post(t, "/api/auth/refresh",
		jsonBody(t, map[string]string{"refresh_token": refreshToken}), "")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh after logout should fail, got %d", resp2.StatusCode)
	}
}

// ─── Auth Middleware ───

func TestProtectedRouteWithoutAuth(t *testing.T) {
	env := newTestEnv(t)
	resp := env.get(t, "/api/vaults", "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no auth: expected 401, got %d", resp.StatusCode)
	}

	// Should return JSON error, not plain text
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("middleware error should be JSON, got Content-Type: %s", ct)
	}
}

// ─── Vault CRUD ───

func TestVaultCRUD(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.registerUser(t, "alice", "password123")

	// Create vault
	vaultID := env.createVault(t, "My Vault", token)
	if vaultID == "" {
		t.Fatal("vault ID should not be empty")
	}

	// List vaults
	resp := env.get(t, "/api/vaults", token)
	data := readJSON(t, resp)
	vaults := data["vaults"].([]interface{})
	if len(vaults) != 1 {
		t.Fatalf("expected 1 vault, got %d", len(vaults))
	}

	// Get vault
	resp = env.get(t, "/api/vaults/"+vaultID, token)
	if resp.StatusCode != 200 {
		resp.Body.Close()
		t.Fatalf("get vault: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete vault
	resp = env.delete(t, "/api/vaults/"+vaultID, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete vault: expected 204, got %d", resp.StatusCode)
	}

	// Verify deleted
	resp = env.get(t, "/api/vaults", token)
	data = readJSON(t, resp)
	vaults = data["vaults"].([]interface{})
	if len(vaults) != 0 {
		t.Fatalf("expected 0 vaults after delete, got %d", len(vaults))
	}
}

func TestVaultIsolation(t *testing.T) {
	env := newTestEnv(t)
	aliceToken, _ := env.registerUser(t, "alice", "password123")
	bobToken, _ := env.registerUser(t, "bob", "password456")

	// Alice creates a vault
	aliceVaultID := env.createVault(t, "Alice Vault", aliceToken)

	// Bob should not see Alice's vault
	resp := env.get(t, "/api/vaults", bobToken)
	data := readJSON(t, resp)
	vaults := data["vaults"].([]interface{})
	if len(vaults) != 0 {
		t.Fatalf("bob should see 0 vaults, got %d", len(vaults))
	}

	// Bob should not access Alice's vault
	resp = env.get(t, "/api/vaults/"+aliceVaultID, bobToken)
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("bob should not access alice's vault")
	}
}

// ─── File Operations ───

func TestFileCRUD(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.registerUser(t, "alice", "password123")
	vaultID := env.createVault(t, "Test Vault", token)

	// Put file — handler reads raw body, not JSON
	resp := env.put(t, "/api/vaults/"+vaultID+"/files/notes/hello.md",
		strings.NewReader("# Hello World"), token)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("put file: expected 200, got %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	// Get file — returns raw content, not JSON
	resp = env.get(t, "/api/vaults/"+vaultID+"/files/notes/hello.md", token)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("get file: expected 200, got %d: %s", resp.StatusCode, b)
	}
	content := readBody(t, resp)
	if content != "# Hello World" {
		t.Fatalf("file content mismatch: got %q", content)
	}

	// List files
	resp = env.get(t, "/api/vaults/"+vaultID+"/files", token)
	data := readJSON(t, resp)
	files := data["files"].([]interface{})
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	// Delete file
	resp = env.delete(t, "/api/vaults/"+vaultID+"/files/notes/hello.md", token)
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("delete file: expected 204, got %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	// Verify deleted
	resp = env.get(t, "/api/vaults/"+vaultID+"/files", token)
	data = readJSON(t, resp)
	files = data["files"].([]interface{})
	if len(files) != 0 {
		t.Fatalf("expected 0 files after delete, got %d", len(files))
	}
}

// ─── WebSocket Sync ───

func TestWebSocketSync(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.registerUser(t, "alice", "password123")
	vaultID := env.createVault(t, "Sync Vault", token)

	// Connect WebSocket
	wsURL := strings.Replace(env.server.URL, "http://", "ws://", 1)
	dialer := websocket.Dialer{}
	conn, wsResp, err := dialer.Dial(wsURL+"/api/sync/"+vaultID, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close()
	if wsResp != nil {
		defer wsResp.Body.Close()
	}

	// Send auth message (first message must be auth token)
	err = conn.WriteJSON(syncpkg.WireMessage{Type: "auth", Data: mustJSON(t, syncpkg.AuthData{Token: token})})
	if err != nil {
		t.Fatalf("send auth: %v", err)
	}

	// Should receive auth_ok first
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var authOk syncpkg.WireMessage
	if err := conn.ReadJSON(&authOk); err != nil {
		t.Fatalf("read auth_ok: %v", err)
	}
	if authOk.Type != "auth_ok" {
		t.Fatalf("expected auth_ok, got %s", authOk.Type)
	}

	// Should receive sync messages (the initial sync)
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initial sync: %v", err)
	}
	if len(msg) == 0 {
		t.Fatal("expected non-empty initial sync message")
	}
}

func TestWebSocketSyncTwoClients(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.registerUser(t, "alice", "password123")
	vaultID := env.createVault(t, "Sync Vault", token)

	// Put a file first
	resp := env.put(t, "/api/vaults/"+vaultID+"/files/test.md",
		strings.NewReader("hello"), token)
	resp.Body.Close()

	// Connect two WebSocket clients
	wsURL := strings.Replace(env.server.URL, "http://", "ws://", 1)
	dialer := websocket.Dialer{}

	conn1, _, err := dialer.Dial(wsURL+"/api/sync/"+vaultID, nil)
	if err != nil {
		t.Fatalf("ws1 dial: %v", err)
	}
	defer conn1.Close()

	conn2, _, err := dialer.Dial(wsURL+"/api/sync/"+vaultID, nil)
	if err != nil {
		t.Fatalf("ws2 dial: %v", err)
	}
	defer conn2.Close()

	// Both send auth
	conn1.WriteJSON(syncpkg.WireMessage{Type: "auth", Data: mustJSON(t, syncpkg.AuthData{Token: token})})
	conn2.WriteJSON(syncpkg.WireMessage{Type: "auth", Data: mustJSON(t, syncpkg.AuthData{Token: token})})

	// Both should receive auth_ok first
	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	var authOk1 syncpkg.WireMessage
	if err := conn1.ReadJSON(&authOk1); err != nil {
		t.Fatalf("ws1 read auth_ok: %v", err)
	}
	if authOk1.Type != "auth_ok" {
		t.Fatalf("ws1 expected auth_ok, got %s", authOk1.Type)
	}

	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	var authOk2 syncpkg.WireMessage
	if err := conn2.ReadJSON(&authOk2); err != nil {
		t.Fatalf("ws2 read auth_ok: %v", err)
	}
	if authOk2.Type != "auth_ok" {
		t.Fatalf("ws2 expected auth_ok, got %s", authOk2.Type)
	}

	// Both should receive initial sync data
	_, _, err = conn1.ReadMessage()
	if err != nil {
		t.Fatalf("ws1 read: %v", err)
	}

	_, _, err = conn2.ReadMessage()
	if err != nil {
		t.Fatalf("ws2 read: %v", err)
	}
}

// ─── REST-to-CRDT Bridge ───

func TestRESTFilePropagatesCRDT(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.registerUser(t, "alice", "password123")
	vaultID := env.createVault(t, "Bridge Vault", token)

	// Put file via REST
	resp := env.put(t, "/api/vaults/"+vaultID+"/files/test.md",
		strings.NewReader("hello CRDT"), token)
	resp.Body.Close()

	// Verify CRDT doc has the file
	doc, err := env.syncEngine.GetDoc(vaultID)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}

	files, err := doc.ListFiles()
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Path == "test.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("file 'test.md' not found in CRDT doc; files: %v", files)
	}
}

// ─── Snapshot ───

func TestSnapshot(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.registerUser(t, "alice", "password123")
	vaultID := env.createVault(t, "Snap Vault", token)

	env.put(t, "/api/vaults/"+vaultID+"/files/a.md",
		strings.NewReader("aaa"), token).Body.Close()
	env.put(t, "/api/vaults/"+vaultID+"/files/b.md",
		strings.NewReader("bbb"), token).Body.Close()

	resp := env.get(t, "/api/vaults/"+vaultID+"/snapshot", token)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("snapshot: expected 200, got %d: %s", resp.StatusCode, b)
	}
	data := readJSON(t, resp)
	files := data["files"].(map[string]interface{})
	if len(files) != 2 {
		t.Fatalf("expected 2 files in snapshot, got %d", len(files))
	}
}

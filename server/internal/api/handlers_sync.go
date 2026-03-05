package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // TODO: tighten in production
}

type SyncHandler struct {
	vaults *vault.Store
	jwt    *auth.JWTService
	mu     sync.Mutex
	hubs   map[string]*SyncHub
}

// SyncHub manages all WebSocket connections for a single vault
type SyncHub struct {
	vaultID string
	clients map[*SyncClient]bool
	mu      sync.RWMutex
}

type SyncClient struct {
	conn    *websocket.Conn
	userID  int64
	send    chan []byte
	hub     *SyncHub
}

type syncMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type authMessage struct {
	Token string `json:"token"`
}

func (h *SyncHandler) getOrCreateHub(vaultID string) *SyncHub {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.hubs == nil {
		h.hubs = make(map[string]*SyncHub)
	}

	hub, ok := h.hubs[vaultID]
	if !ok {
		hub = &SyncHub{
			vaultID: vaultID,
			clients: make(map[*SyncClient]bool),
		}
		h.hubs[vaultID] = hub
	}
	return hub
}

func (h *SyncHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	vaultID := r.PathValue("id")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	// Auth: check query param first, then expect first message
	var userID int64
	tokenStr := r.URL.Query().Get("token")
	if tokenStr != "" {
		claims, err := h.jwt.ValidateToken(tokenStr)
		if err != nil {
			conn.WriteJSON(syncMessage{Type: "error", Data: rawJSON(`{"message":"invalid token"}`)})
			conn.Close()
			return
		}
		userID = claims.UserID
	} else {
		// Read first message as auth
		var msg syncMessage
		if err := conn.ReadJSON(&msg); err != nil || msg.Type != "auth" {
			conn.WriteJSON(syncMessage{Type: "error", Data: rawJSON(`{"message":"first message must be auth"}`)})
			conn.Close()
			return
		}
		var authMsg authMessage
		if err := json.Unmarshal(msg.Data, &authMsg); err != nil {
			conn.WriteJSON(syncMessage{Type: "error", Data: rawJSON(`{"message":"invalid auth message"}`)})
			conn.Close()
			return
		}
		claims, err := h.jwt.ValidateToken(authMsg.Token)
		if err != nil {
			conn.WriteJSON(syncMessage{Type: "error", Data: rawJSON(`{"message":"invalid token"}`)})
			conn.Close()
			return
		}
		userID = claims.UserID
	}

	// Verify vault ownership
	if _, err := h.vaults.GetVault(vaultID, userID); err != nil {
		conn.WriteJSON(syncMessage{Type: "error", Data: rawJSON(`{"message":"vault not found"}`)})
		conn.Close()
		return
	}

	hub := h.getOrCreateHub(vaultID)
	client := &SyncClient{
		conn:   conn,
		userID: userID,
		send:   make(chan []byte, 64),
		hub:    hub,
	}

	hub.mu.Lock()
	hub.clients[client] = true
	hub.mu.Unlock()

	slog.Info("sync client connected", "vault", vaultID, "user", userID)

	// Send current snapshot on connect
	snapshot, err := h.vaults.Snapshot(vaultID)
	if err == nil {
		data, _ := json.Marshal(map[string]interface{}{"files": snapshot})
		conn.WriteJSON(syncMessage{Type: "snapshot", Data: data})
	}

	go client.writePump()
	go client.readPump(h)
}

func (c *SyncClient) readPump(h *SyncHandler) {
	defer func() {
		c.hub.mu.Lock()
		delete(c.hub.clients, c)
		c.hub.mu.Unlock()
		c.conn.Close()
		slog.Info("sync client disconnected", "vault", c.hub.vaultID, "user", c.userID)
	}()

	for {
		var msg syncMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Error("websocket read error", "error", err)
			}
			return
		}

		switch msg.Type {
		case "put_file":
			h.handlePutFile(c, msg.Data)
		case "delete_file":
			h.handleDeleteFile(c, msg.Data)
		case "ping":
			c.conn.WriteJSON(syncMessage{Type: "pong"})
		}
	}
}

func (c *SyncClient) writePump() {
	defer c.conn.Close()

	for data := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

type putFileMessage struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

type deleteFileMessage struct {
	Path string `json:"path"`
}

func (h *SyncHandler) handlePutFile(sender *SyncClient, data json.RawMessage) {
	var msg putFileMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	isBinary := isBinaryContent(msg.Path, msg.Content)
	f, err := h.vaults.PutFile(sender.hub.vaultID, msg.Path, msg.Content, isBinary)
	if err != nil {
		slog.Error("sync put_file failed", "error", err)
		return
	}

	// Broadcast to other clients
	notification, _ := json.Marshal(syncMessage{
		Type: "file_changed",
		Data: rawJSON(mustMarshal(map[string]interface{}{
			"path": f.Path,
			"hash": f.Hash,
			"size": f.Size,
		})),
	})

	sender.hub.mu.RLock()
	for client := range sender.hub.clients {
		if client != sender {
			select {
			case client.send <- notification:
			default:
				// Client buffer full, skip
			}
		}
	}
	sender.hub.mu.RUnlock()
}

func (h *SyncHandler) handleDeleteFile(sender *SyncClient, data json.RawMessage) {
	var msg deleteFileMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	if err := h.vaults.DeleteFile(sender.hub.vaultID, msg.Path); err != nil {
		slog.Error("sync delete_file failed", "error", err)
		return
	}

	notification, _ := json.Marshal(syncMessage{
		Type: "file_deleted",
		Data: rawJSON(mustMarshal(map[string]string{"path": msg.Path})),
	})

	sender.hub.mu.RLock()
	for client := range sender.hub.clients {
		if client != sender {
			select {
			case client.send <- notification:
			default:
			}
		}
	}
	sender.hub.mu.RUnlock()
}

func rawJSON(s string) json.RawMessage {
	return json.RawMessage(s)
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	gosync "sync"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/sync"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"
	"github.com/automerge/automerge-go"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser clients (CLI, Obsidian plugin)
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

// SyncHandler manages WebSocket connections for CRDT sync.
type SyncHandler struct {
	vaults *vault.Store
	jwt    *auth.JWTService
	engine *sync.Engine
	mu     gosync.Mutex
	hubs   map[string]*SyncHub
}

// NewSyncHandler creates a SyncHandler with the given sync engine.
func NewSyncHandler(vaults *vault.Store, jwt *auth.JWTService, engine *sync.Engine) *SyncHandler {
	return &SyncHandler{
		vaults: vaults,
		jwt:    jwt,
		engine: engine,
		hubs:   make(map[string]*SyncHub),
	}
}

// SyncHub manages all WebSocket connections for a single vault.
type SyncHub struct {
	vaultID string
	clients map[*SyncClient]bool
	mu      gosync.RWMutex
}

// SyncClient represents a single connected peer.
type SyncClient struct {
	conn      *websocket.Conn
	userID    int64
	send      chan []byte
	hub       *SyncHub
	syncState *automerge.SyncState
}

func (h *SyncHandler) getOrCreateHub(vaultID string) *SyncHub {
	h.mu.Lock()
	defer h.mu.Unlock()

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
			sendError(conn, "invalid token")
			conn.Close()
			return
		}
		userID = claims.UserID
	} else {
		// Read first message as auth
		var msg sync.WireMessage
		if err := conn.ReadJSON(&msg); err != nil || msg.Type != sync.MsgTypeAuth {
			sendError(conn, "first message must be auth")
			conn.Close()
			return
		}
		var authData sync.AuthData
		if err := json.Unmarshal(msg.Data, &authData); err != nil {
			sendError(conn, "invalid auth message")
			conn.Close()
			return
		}
		claims, err := h.jwt.ValidateToken(authData.Token)
		if err != nil {
			sendError(conn, "invalid token")
			conn.Close()
			return
		}
		userID = claims.UserID
	}

	// Verify vault ownership
	if _, err := h.vaults.GetVault(vaultID, userID); err != nil {
		sendError(conn, "vault not found")
		conn.Close()
		return
	}

	// Ensure CRDT doc is loaded
	if _, err := h.engine.GetDoc(vaultID); err != nil {
		slog.Error("failed to load CRDT doc", "vault", vaultID, "error", err)
		sendError(conn, "failed to load vault state")
		conn.Close()
		return
	}

	// Create per-client sync state
	syncState, err := h.engine.NewSyncState(vaultID)
	if err != nil {
		slog.Error("failed to create sync state", "vault", vaultID, "error", err)
		sendError(conn, "sync state error")
		conn.Close()
		return
	}

	hub := h.getOrCreateHub(vaultID)
	client := &SyncClient{
		conn:      conn,
		userID:    userID,
		send:      make(chan []byte, 64),
		hub:       hub,
		syncState: syncState,
	}

	hub.mu.Lock()
	hub.clients[client] = true
	hub.mu.Unlock()

	slog.Info("sync client connected", "vault", vaultID, "user", userID)

	// Generate initial sync messages to bring client up to date
	if !h.sendPendingSyncMessages(client) {
		conn.Close()
		return
	}

	go client.writePump()
	go client.readPump(h)
}

// sendPendingSyncMessages generates and queues all pending sync messages for a client.
// Returns false if the client's send buffer overflowed and it should be disconnected.
func (h *SyncHandler) sendPendingSyncMessages(client *SyncClient) bool {
	for {
		msgBytes, valid := h.engine.GenerateSyncMessage(client.syncState)
		if !valid {
			break
		}
		data, _ := json.Marshal(sync.SyncData{Message: msgBytes})
		wireMsg, _ := json.Marshal(sync.WireMessage{Type: sync.MsgTypeSync, Data: data})
		select {
		case client.send <- wireMsg:
		default:
			slog.Warn("client send buffer full, disconnecting", "vault", client.hub.vaultID)
			close(client.send)
			return false
		}
	}
	return true
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
		var msg sync.WireMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Error("websocket read error", "error", err)
			}
			return
		}

		switch msg.Type {
		case sync.MsgTypeSync:
			h.handleSyncMessage(c, msg.Data)
		case sync.MsgTypePing:
			wireMsg, _ := json.Marshal(sync.WireMessage{Type: sync.MsgTypePong})
			select {
			case c.send <- wireMsg:
			default:
			}
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

func (h *SyncHandler) handleSyncMessage(sender *SyncClient, rawData json.RawMessage) {
	var syncData sync.SyncData
	if err := json.Unmarshal(rawData, &syncData); err != nil {
		slog.Error("invalid sync data", "error", err)
		return
	}

	vaultID := sender.hub.vaultID

	// Apply the incoming sync message to the document
	if err := h.engine.ReceiveSyncMessage(vaultID, sender.syncState, syncData.Message); err != nil {
		slog.Error("receive sync message failed", "vault", vaultID, "error", err)
		return
	}

	// Generate response sync messages for the sender
	h.sendPendingSyncMessages(sender)

	// Materialize changes to vault_files
	if err := h.engine.MaterializeFiles(vaultID); err != nil {
		slog.Error("materialize files failed", "vault", vaultID, "error", err)
	}

	// Broadcast to other connected clients: generate sync messages for each
	sender.hub.mu.RLock()
	peers := make([]*SyncClient, 0, len(sender.hub.clients))
	for client := range sender.hub.clients {
		if client != sender {
			peers = append(peers, client)
		}
	}
	sender.hub.mu.RUnlock()

	for _, peer := range peers {
		h.sendPendingSyncMessages(peer)
	}
}

func sendError(conn *websocket.Conn, message string) {
	data, _ := json.Marshal(sync.ErrorData{Message: message})
	conn.WriteJSON(sync.WireMessage{Type: sync.MsgTypeError, Data: data})
}

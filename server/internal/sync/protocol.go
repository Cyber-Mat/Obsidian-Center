package sync

import "encoding/json"

// Message types exchanged over WebSocket.
const (
	MsgTypeSync    = "sync"    // Automerge sync message (binary in base64)
	MsgTypeRequest = "request" // Request specific file contents
	MsgTypePing    = "ping"
	MsgTypePong    = "pong"
	MsgTypeError   = "error"
	MsgTypeAuth    = "auth"
	MsgTypeAuthOk  = "auth_ok"
)

// WireMessage is the envelope for all WebSocket messages.
type WireMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// SyncData carries base64-encoded Automerge sync message bytes.
type SyncData struct {
	Message []byte `json:"message"` // base64 encoded by json.Marshal
}

// RequestData asks the server for specific file paths.
type RequestData struct {
	Paths []string `json:"paths"`
}

// ErrorData carries error information.
type ErrorData struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// AuthData carries the auth token in first-message auth flow.
type AuthData struct {
	Token string `json:"token"`
}

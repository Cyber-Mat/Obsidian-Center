# Obsidian Center — Architecture

A self-hosted platform for synchronizing Obsidian vaults across devices, with a
web-based editor and real-time CRDT-powered sync.

## System Overview

```
┌─────────────────────────────────────────────────────────┐
│                  Docker Container (:8080)                │
│                                                         │
│  ┌────────────────────────────────────────────────────┐  │
│  │                  Go HTTP Server                    │  │
│  │                                                    │  │
│  │  ┌──────────┐  ┌────────────┐  ┌──────────────┐   │  │
│  │  │ REST API │  │ WebSocket  │  │ Static Files │   │  │
│  │  │ Handlers │  │  /api/sync │  │ (Web Editor) │   │  │
│  │  └────┬─────┘  └─────┬──────┘  └──────────────┘   │  │
│  │       │               │                            │  │
│  │  ┌────┴───────────────┴──────┐                     │  │
│  │  │      Auth Middleware      │                     │  │
│  │  │   (JWT + Refresh Tokens)  │                     │  │
│  │  └────┬───────────────┬──────┘                     │  │
│  │       │               │                            │  │
│  │  ┌────┴─────┐   ┌─────┴──────────┐                │  │
│  │  │  Vault   │   │  Sync Engine   │                │  │
│  │  │  Store   │   │  (Automerge)   │                │  │
│  │  │  (SQL)   │◄──┤  CRDT Docs     │                │  │
│  │  └────┬─────┘   └────────────────┘                │  │
│  │       │                                            │  │
│  │  ┌────┴─────┐                                      │  │
│  │  │  SQLite  │                                      │  │
│  │  │  (WAL)   │                                      │  │
│  │  └──────────┘                                      │  │
│  └────────────────────────────────────────────────────┘  │
│                                                         │
│  ┌────────────────────────────────────────────────────┐  │
│  │             Web Editor (Static SPA)                │  │
│  │                                                    │  │
│  │  ┌────────────┐  ┌────────────┐  ┌─────────────┐  │  │
│  │  │ CodeMirror │  │ Automerge  │  │  WebSocket  │  │  │
│  │  │  6 Editor  │  │ CRDT (WASM)│  │ Sync Client │  │  │
│  │  └────────────┘  └────────────┘  └─────────────┘  │  │
│  └────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘

                         ▲
          ┌──────────────┼──────────────┐
          │              │              │
  ┌───────┴───────┐ ┌───┴────┐ ┌───────┴───────┐
  │  Obsidian PC  │ │ Web    │ │  Obsidian PC  │
  │  + Plugin     │ │ Browser│ │  + Plugin     │
  │  (Automerge   │ │        │ │  (Automerge   │
  │   WASM)       │ │        │ │   WASM)       │
  └───────────────┘ └────────┘ └───────────────┘
```

## Data Flow: PC-to-PC Sync

```
PC A (Obsidian Plugin)         Server              PC B (Obsidian Plugin)
         │                       │                          │
         │  1. Edit file locally │                          │
         │  Automerge change     │                          │
         │                       │                          │
         │  2. WebSocket: send   │                          │
         │  Automerge sync msg   │                          │
         │──────────────────────>│                          │
         │                       │  3. Receive sync msg     │
         │                       │  Apply to server doc     │
         │                       │                          │
         │                       │  4. Generate sync msg    │
         │                       │  for PC B               │
         │                       │─────────────────────────>│
         │                       │                          │
         │                       │  5. PC B applies msg     │
         │                       │  to local Automerge doc  │
         │                       │                          │
         │                       │  6. Materialize: persist │
         │                       │  CRDT → SQLite files     │
```

---

## Component Details

### Go Server (`server/`)

| Package | Purpose |
|---------|---------|
| `cmd/server` | Entry point, config flags, graceful shutdown |
| `internal/api` | HTTP handlers, router, WebSocket sync handler |
| `internal/auth` | JWT tokens, bcrypt passwords, sessions, middleware |
| `internal/vault` | SQLite store for vaults, files, CRDT state, migrations |
| `internal/sync` | Automerge CRDT engine, document management, persist loop |
| `internal/graph` | Link graph indexing (wikilinks, backlinks, traversal) |

**Key design decisions:**

- **Single binary** serves both REST API and static web editor files
- **WriteTimeout = 0** on the HTTP server so WebSocket connections survive
- **Automerge-go with CGO** for server-side CRDT operations
- **Persist loop** periodically materializes CRDT state to SQLite
- **REST-to-CRDT bridge**: PUT/DELETE file REST endpoints update the CRDT document,
  ensuring changes propagate to all WebSocket peers

### Sync Engine (`internal/sync/engine.go`)

The sync engine manages one Automerge CRDT document per vault:

- **Lazy loading**: Documents loaded on first access (WebSocket connect or REST bridge)
- **Bootstrap**: If no saved CRDT state exists, builds document from existing DB files
- **Thread safety**: Per-vault mutex prevents concurrent document mutations
- **TOCTOU prevention**: Engine-level lock held during document load to prevent duplicate states
- **Dirty tracking**: Only persists vaults with actual changes
- **Context-based cancellation**: Persist loop stops cleanly on shutdown

**CRDT document schema per vault:**

```json
{
  "files": {
    "path/to/note.md": {
      "content": "<Automerge.Text>",
      "hash": "sha256:...",
      "modified": 1709827200000,
      "is_binary": false
    }
  }
}
```

### WebSocket Protocol (`/api/sync/{vaultId}`)

1. Client connects (no HTTP auth header required at upgrade)
2. Client sends first message: `{"type":"auth","token":"<JWT>"}`
3. Server validates token and vault ownership
4. Server initializes Automerge sync state, sends initial sync message
5. Both sides exchange binary Automerge sync messages
6. Server sends WebSocket pings every 30s; clients must pong within 60s
7. Message size limit: 16 MB
8. Per-client sync state mutex prevents race conditions

**Goroutine lifecycle:**
- `readPump`: reads client messages, closes `send` channel on exit
- `writePump`: select loop over `send` channel and ping ticker, exits when channel closes

### Authentication

- **JWT access tokens**: Short-lived (15 min), used in `Authorization: Bearer` header
- **Refresh tokens**: Long-lived (30 days), stored as bcrypt hashes in SQLite
- **Token rotation**: On refresh, new session created before old one is deleted (atomic ordering)
- **Middleware**: Returns JSON error responses with proper `Content-Type: application/json`
- **Logout endpoint**: Invalidates refresh token server-side

### Obsidian Plugin (`plugin/`)

- TypeScript, built with esbuild (CommonJS, es2018)
- Uses `@automerge/automerge` WASM for CRDT operations
- Listens to Obsidian vault events (`create`, `modify`, `delete`, `rename`)
- Maintains local Automerge document, syncs via WebSocket
- Stores CRDT state in plugin data directory for offline support

### Web Editor (`web/`)

- **CodeMirror 6** with markdown syntax highlighting and Catppuccin Mocha dark theme
- **Automerge WASM** (`fullfat_base64` entrypoint for browser compatibility)
- **File tree** sidebar with hierarchical folder/file rendering
- **Auth flow** with JWT access/refresh token rotation and concurrent refresh guard
- **Remote changes** applied as minimal diffs (preserves cursor position and undo history)
- **CRDT state** persisted to `sessionStorage` per vault for tab persistence
- **Built with esbuild**, output to `dist/app.js`

### Docker Build

Multi-stage build (defined in `Dockerfile`):

1. **Node stage** (`node:20-alpine`): `npm ci` + `npm run build` for web editor
2. **Go stage** (`golang:1.22-alpine`): CGO-enabled build for Automerge Go bindings
3. **Runtime** (`alpine:3.19`): Server binary + web assets, healthcheck via `/api/health`

```bash
docker build -t obsidian-center .
docker run -p 8080:8080 \
  -e OC_JWT_SECRET=your-secret-here \
  -v obsidian-data:/data \
  obsidian-center
```

---

## API Reference

### Auth (Public)
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/auth/register` | Create user account (username 3-64 chars, password 8+ chars) |
| POST | `/api/auth/login` | Log in, returns access + refresh tokens |
| POST | `/api/auth/refresh` | Rotate refresh token, get new token pair |
| POST | `/api/auth/logout` | Invalidate refresh token |

### Health (Public)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/health` | Health check (returns `{"status":"ok"}`) |

### Vaults (Authenticated)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/vaults` | List user's vaults |
| POST | `/api/vaults` | Create vault |
| GET | `/api/vaults/{id}` | Get vault details |
| DELETE | `/api/vaults/{id}` | Delete vault (204 No Content) |

### Files (Authenticated)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/vaults/{id}/files` | List file metadata |
| GET | `/api/vaults/{id}/files/{path}` | Get raw file content |
| PUT | `/api/vaults/{id}/files/{path}` | Create/update file (raw body) |
| DELETE | `/api/vaults/{id}/files/{path}` | Delete file (204 No Content) |
| GET | `/api/vaults/{id}/snapshot` | Get all file hashes |

### Graph (Authenticated)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/vaults/{id}/graph/links` | Outgoing links from a file |
| GET | `/api/vaults/{id}/graph/backlinks` | Files linking to a given file |
| POST | `/api/vaults/{id}/graph/traverse` | Cycle-aware graph traversal |
| GET | `/api/vaults/{id}/graph/stats` | Graph statistics |

### Sync
| Method | Path | Description |
|--------|------|-------------|
| WS | `/api/sync/{id}` | WebSocket sync (auth via first message) |

---

## Testing

Integration tests in `server/internal/api/integration_test.go` cover:

- **Auth flow**: Register, login, refresh, logout, input validation, duplicate prevention
- **Auth middleware**: JSON error responses, token enforcement on protected routes
- **Vault CRUD**: Create, list, get, delete with proper status codes
- **User isolation**: Users cannot see or access other users' vaults
- **File CRUD**: PUT (raw body), GET (raw content), list, delete
- **WebSocket sync**: Single client connect + auth + initial sync
- **Dual WebSocket**: Two clients connecting to same vault simultaneously
- **REST-to-CRDT bridge**: File PUT propagates to Automerge document
- **Snapshot**: Multiple files returned with correct hashes

```bash
cd server && go test -v ./...
```

---

## Project Structure

```
obsidian-center/
├── ARCHITECTURE.md            # This document
├── Dockerfile                 # Multi-stage: Node → Go → Alpine
├── plugin/                    # Obsidian plugin (TypeScript)
│   ├── src/
│   │   ├── main.ts            # Plugin entry point
│   │   ├── sync/
│   │   │   ├── crdt.ts        # Automerge WASM CRDT manager
│   │   │   └── client.ts      # WebSocket sync client
│   │   └── settings.ts        # Plugin settings UI
│   ├── manifest.json
│   ├── package.json
│   ├── tsconfig.json
│   └── esbuild.config.mjs
├── server/                    # Go server
│   ├── cmd/server/
│   │   └── main.go            # Entry point, config, shutdown
│   ├── internal/
│   │   ├── api/
│   │   │   ├── router.go             # Route definitions
│   │   │   ├── handlers_auth.go      # Register, login, refresh, logout
│   │   │   ├── handlers_vault.go     # Vault + file CRUD, REST-CRDT bridge
│   │   │   ├── handlers_sync.go      # WebSocket sync handler + hub
│   │   │   ├── handlers_graph.go     # Link graph queries
│   │   │   ├── helpers.go            # writeJSON, writeError
│   │   │   └── integration_test.go   # Integration test harness
│   │   ├── auth/
│   │   │   ├── jwt.go         # Token generation/validation
│   │   │   ├── middleware.go  # HTTP auth middleware (JSON errors)
│   │   │   └── store.go       # User + session stores
│   │   ├── sync/
│   │   │   ├── engine.go      # CRDT engine, persist loop, REST bridge
│   │   │   ├── doc.go         # VaultDoc (Automerge document wrapper)
│   │   │   └── store_adapter.go  # CRDTStore interface adapter
│   │   ├── vault/
│   │   │   ├── store.go       # Vault + file SQLite operations
│   │   │   ├── db.go          # Database open + WAL config
│   │   │   └── migrations/    # Embedded SQL migrations
│   │   └── graph/
│   │       ├── linker.go      # Wiki-link parser
│   │       ├── store.go       # Link graph SQL operations
│   │       └── linker_test.go # Link parser tests
│   ├── go.mod
│   └── go.sum
├── web/                       # Web editor (SPA)
│   ├── src/
│   │   ├── main.ts            # Entry point, auth UI, vault loading
│   │   ├── api.ts             # HTTP client with token refresh
│   │   ├── editor.ts          # CodeMirror 6 wrapper + CRDT integration
│   │   ├── filetree.ts        # File tree sidebar component
│   │   ├── theme.ts           # Catppuccin Mocha dark theme
│   │   └── sync/
│   │       ├── crdt.ts        # Automerge WASM CRDT manager
│   │       └── client.ts      # WebSocket sync client
│   ├── index.html             # SPA shell
│   ├── style.css              # Application styles
│   ├── package.json
│   ├── tsconfig.json
│   └── esbuild.config.mjs     # Build config with Automerge WASM alias
└── docker-compose.yml
```

---

## Implementation Status

| Phase | Feature | Status |
|-------|---------|--------|
| 1 | Go server, JWT auth, SQLite, plugin skeleton | Done |
| 2 | Automerge CRDT sync (server + plugin + WebSocket) | Done |
| 3 | Link graph (parser, backlinks, traversal, stats) | Done |
| 4 | Web editor (CodeMirror 6, Automerge WASM, file tree) | Done |
| 5 | Encryption (client-side AES-256-GCM) | Planned |
| 6 | RAG pipeline (embeddings, semantic search) | Planned |
| 7 | Agent runtime (Claude API integration) | Planned |

---

## Design Principles

1. **Single binary server.** No external runtime dependencies. SQLite is embedded.
   One `go build` away from deployment.

2. **CRDT-first.** All state changes flow through Automerge. Guarantees convergence
   regardless of network conditions, client count, or timing.

3. **Obsidian is the UI.** The server never renders markdown. Obsidian handles all
   user-facing formatting. The web editor is a lightweight supplement.

4. **Offline-first.** Every client maintains a complete local copy. The server is a
   coordination point, not a dependency.

5. **REST-CRDT consistency.** REST file operations update the CRDT document, so
   changes from any source (REST API, WebSocket, web editor) propagate to all peers.

6. **Graph-aware.** The link structure of a vault is a first-class data model with
   materialized graph, backlink queries, and cycle-safe traversal primitives.

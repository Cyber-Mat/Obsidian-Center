# Obsidian Center — Architecture

## Vision

Obsidian Center is a self-hosted platform that extends Obsidian into a
collaborative knowledge engine. It provides vault synchronization across
devices, a web-based editor for browser access, and a foundation for
RAG-powered retrieval and autonomous agents that use Obsidian vaults as
structured knowledge stores.

Obsidian remains the primary user interface and formatting standard.
Obsidian Center operates as the infrastructure layer beneath it.

---

## System Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      Clients                                │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐  │
│  │   Obsidian    │  │  Web Editor  │  │  API Consumers    │  │
│  │   Plugin      │  │  (Browser)   │  │  (Agents, CLI)    │  │
│  │              │  │              │  │                   │  │
│  │  TypeScript   │  │  CodeMirror  │  │  REST / WebSocket │  │
│  │  + Automerge  │  │  + Automerge │  │                   │  │
│  │    (WASM)     │  │    (WASM)    │  │                   │  │
│  └──────┬───────┘  └──────┬───────┘  └────────┬──────────┘  │
│         │                 │                    │             │
└─────────┼─────────────────┼────────────────────┼─────────────┘
          │                 │                    │
          │    WebSocket    │    WebSocket       │   REST
          │    + REST       │    + REST          │
          │                 │                    │
┌─────────▼─────────────────▼────────────────────▼─────────────┐
│                                                              │
│                    Obsidian Center Server                     │
│                         (Go binary)                          │
│                                                              │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌───────────┐  │
│  │    Sync    │ │    Auth    │ │   Vault    │ │    API    │  │
│  │   Engine   │ │  (JWT)     │ │  Storage   │ │  Router   │  │
│  │            │ │            │ │            │ │           │  │
│  │ Automerge  │ │ Access     │ │ Files +    │ │           │  │
│  │ (Go/CGO)   │ │ Refresh    │ │ Metadata   │ │           │  │
│  │            │ │ Tokens     │ │            │ │           │  │
│  └─────┬──────┘ └────────────┘ └─────┬──────┘ └───────────┘  │
│        │                             │                       │
│  ┌─────▼─────────────────────────────▼──────────────────┐    │
│  │                    SQLite                             │    │
│  │                                                      │    │
│  │  ┌──────────┐  ┌───────────┐  ┌───────────────────┐  │    │
│  │  │  CRDT    │  │  Vault    │  │  sqlite-vec       │  │    │
│  │  │  State   │  │  Files +  │  │  (embeddings)     │  │    │
│  │  │          │  │  Metadata │  │                   │  │    │
│  │  └──────────┘  └───────────┘  └───────────────────┘  │    │
│  │                                                      │    │
│  │  ┌──────────────────────────────────────────────┐    │    │
│  │  │  Link Graph (vault_links)                    │    │    │
│  │  │  source_path → target_path with backlinks    │    │    │
│  │  └──────────────────────────────────────────────┘    │    │
│  └──────────────────────────────────────────────────────┘    │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐    │
│  │              Future: RAG + Agents                     │    │
│  │                                                      │    │
│  │  Embedding pipeline → sqlite-vec index               │    │
│  │  Agent runtime (goroutines) → Claude API             │    │
│  │  Knowledge graph extraction from vault links          │    │
│  └──────────────────────────────────────────────────────┘    │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

---

## Components

### 1. Obsidian Plugin (`plugin/`)

**Language:** TypeScript
**Build:** esbuild (CommonJS output, es2018 target)
**Runtime:** Obsidian desktop/mobile app

The plugin is the bridge between Obsidian's local vault and the
Obsidian Center server. It runs inside Obsidian's Electron/Capacitor
environment.

**Responsibilities:**

- Monitor local vault changes via Obsidian's `Vault` event API
  (`create`, `modify`, `delete`, `rename`)
- Maintain a local Automerge document (WASM build) representing vault
  state
- Synchronize CRDT changes with the server over WebSocket
- Apply remote changes to the local vault
- Provide a settings UI for server URL, authentication, sync
  preferences, and conflict visibility
- Handle offline operation: queue changes locally, reconcile on
  reconnect

**Key Obsidian APIs used:**

```typescript
// File operations
app.vault.read(file: TFile): Promise<string>
app.vault.cachedRead(file: TFile): Promise<string>
app.vault.modify(file: TFile, data: string): Promise<void>
app.vault.process(file: TFile, fn: (data: string) => string): Promise<string>
app.vault.create(path: string, data: string): Promise<TFile>
app.vault.delete(file: TFile): Promise<void>
app.vault.getMarkdownFiles(): TFile[]
app.vault.getAbstractFileByPath(path: string): TAbstractFile | null

// Frontmatter
app.fileManager.processFrontMatter(file: TFile, fn): Promise<void>

// Events
app.vault.on('create', callback)
app.vault.on('modify', callback)
app.vault.on('delete', callback)
app.vault.on('rename', callback)
```

**Plugin lifecycle:**

```
onload()
  ├── Load settings from data.json
  ├── Initialize Automerge document (load persisted state or create new)
  ├── Register vault event listeners
  ├── Open WebSocket connection to server
  ├── Perform initial sync (full state comparison)
  └── Register settings tab, commands, status bar item

onunload()
  ├── Persist Automerge state
  ├── Close WebSocket connection
  └── (Framework auto-cleans registered resources)
```

---

### 2. Server (`server/`)

**Language:** Go
**Database:** SQLite (+ sqlite-vec extension for embeddings)
**Deployment:** Single static binary + SQLite database file

The server is the central coordination point. All clients sync through
it. It never needs to understand Markdown rendering or Obsidian's UI —
it only manages CRDT state, file storage, authentication, and the API
surface.

**Package structure:**

```
server/
├── cmd/
│   └── server/
│       └── main.go              # Entry point, config, startup
├── internal/
│   ├── auth/
│   │   ├── jwt.go               # Token generation, validation
│   │   ├── middleware.go        # HTTP/WebSocket auth middleware
│   │   └── store.go             # User/credential storage
│   ├── sync/
│   │   ├── engine.go            # CRDT merge logic, change tracking
│   │   ├── websocket.go         # WebSocket connection management
│   │   ├── protocol.go          # Message types, serialization
│   │   └── automerge.go         # Automerge Go bindings wrapper
│   ├── vault/
│   │   ├── store.go             # File read/write, metadata
│   │   ├── snapshot.go          # Point-in-time vault snapshots
│   │   └── watcher.go          # File system watcher (for local vaults)
│   ├── graph/
│   │   ├── linker.go            # Parse wiki-links from markdown
│   │   ├── index.go             # Build and query vault_links table
│   │   └── traversal.go        # Cycle-aware, budget-limited graph walks
│   ├── api/
│   │   ├── router.go            # Route definitions
│   │   ├── handlers_vault.go    # Vault CRUD endpoints
│   │   ├── handlers_sync.go     # Sync status, history
│   │   ├── handlers_graph.go    # Link graph and backlink queries
│   │   └── handlers_auth.go     # Login, register, refresh
│   └── rag/                     # Future
│       ├── embedder.go          # Chunking + embedding pipeline
│       ├── index.go             # sqlite-vec operations
│       └── query.go             # Semantic search
├── migrations/
│   ├── 001_initial.sql
│   └── 002_link_graph.sql
├── go.mod
└── go.sum
```

#### 2.1 Sync Engine

The sync engine is the core of the server. It manages CRDT state and
coordinates changes between connected clients.

**CRDT strategy — Automerge:**

Automerge is a JSON-like CRDT library with a Rust core and bindings for
multiple languages. This project uses:

- **Go bindings** (`automerge-go`) on the server via CGO
- **WASM build** (`@automerge/automerge`) in the plugin and web editor

All three environments use the same underlying Rust implementation,
guaranteeing identical merge semantics everywhere.

**Data model:**

Each vault is represented as a single Automerge document with this
structure:

```
{
  "files": {
    "path/to/note.md": {
      "content": Automerge.Text,   // CRDT text type for character-level merging
      "frontmatter": { ... },      // Parsed YAML as Automerge map
      "created": timestamp,
      "modified": timestamp,
      "hash": "sha256:..."         // Content hash for quick comparison
    },
    "attachments/image.png": {
      "binary": Automerge.Bytes,   // Binary content stored as bytes
      "created": timestamp,
      "modified": timestamp,
      "hash": "sha256:..."
    }
  },
  "metadata": {
    "vault_id": "uuid",
    "vault_name": "My Vault",
    "schema_version": 1
  }
}
```

**Why a single document per vault:**

- Automerge handles the internal change tracking and compaction
- Simplifies the sync protocol: clients exchange Automerge sync
  messages, which encode only the deltas
- Automerge's sync protocol is already designed for exactly this
  pattern (two peers converging on a shared document)

**Scaling consideration:** For very large vaults (10,000+ files), the
document can be sharded by directory subtree into multiple Automerge
documents. This is an optimization to defer, not a day-one requirement.

**Sync protocol (WebSocket messages):**

```
Client → Server:
  { type: "sync",    data: <Automerge sync message bytes> }
  { type: "request", data: { paths: ["file1.md", "file2.md"] } }

Server → Client:
  { type: "sync",    data: <Automerge sync message bytes> }
  { type: "patch",   data: { patches: [...] } }  // Optional: decoded patches for UI
  { type: "error",   data: { code: "...", message: "..." } }

Bidirectional:
  { type: "ping" } / { type: "pong" }
```

The heavy lifting is done by Automerge's built-in sync protocol. Each
side maintains a `SyncState` and generates/receives `SyncMessage`
objects. Automerge internally tracks which changes each peer has seen
and only transmits missing changes.

**Conflict resolution:**

Because Automerge is a CRDT, there are no conflicts in the traditional
sense — all changes merge deterministically. However, there are
semantic conflicts (two users editing the same paragraph simultaneously)
that produce merged text which may not read well. For these:

- The plugin can surface "recent concurrent edits" in a UI panel
- Automerge preserves full change history, so users can inspect and
  revert
- This is a UX concern, not a data integrity concern

#### 2.2 Authentication

**Mechanism:** JWT with access + refresh token pair.

```
Access token:  short-lived (15 min), used for API and WebSocket auth
Refresh token: long-lived (30 days), stored securely, used to obtain new access tokens
```

**Flow:**

```
1. Client sends credentials (username + password) to POST /api/auth/login
2. Server validates, returns { access_token, refresh_token }
3. Client includes access_token in:
   - REST: Authorization: Bearer <token>
   - WebSocket: sent as first message after connection, or as query param
4. On access_token expiry, client calls POST /api/auth/refresh
5. Server validates refresh_token, issues new pair
```

**Password storage:** Argon2id hash (winner of the Password Hashing
Competition, resistant to GPU/ASIC attacks).

**Future:** OAuth 2.1 for third-party integrations, API keys for
agent/automation access.

#### 2.3 Vault Storage

The server stores two representations of each vault:

1. **Automerge document state** — the CRDT binary, stored as a blob in
   SQLite. This is the source of truth for sync.
2. **Materialized files** — the actual file tree on disk (or in SQLite
   blob storage), derived from the Automerge state. Used for:
   - Serving files to the web editor
   - Input to the RAG embedding pipeline
   - Backup/export

#### 2.4 Link Graph

Obsidian vaults form arbitrary directed graphs through wiki-links
(`[[note]]`, `[[note|display text]]`) and embeds (`![[note]]`). These
graphs are frequently cyclic — `A → B → C → A` is common and expected.
The server materializes this graph into a dedicated table for efficient
querying by both the RAG pipeline and agents.

**Link parsing:**

On every file change (create, modify, rename, delete), the server
re-parses the affected file's content and updates the link graph. The
parser handles all Obsidian link forms:

```
[[note]]                    → target: "note.md"
[[note|display text]]       → target: "note.md", link_text: "display text"
[[folder/note]]             → target: "folder/note.md"
[[note#heading]]            → target: "note.md", anchor: "heading"
[[note#heading|display]]    → target: "note.md", anchor: "heading"
![[note]]                   → target: "note.md", is_embed: true
![[image.png]]              → target: "image.png", is_embed: true
```

**Graph storage:**

```sql
CREATE TABLE vault_links (
    vault_id    TEXT NOT NULL REFERENCES vaults(id),
    source_path TEXT NOT NULL,          -- file containing the link
    target_path TEXT NOT NULL,          -- resolved file the link points to
    link_text   TEXT,                   -- display text from [[target|display]]
    anchor      TEXT,                   -- heading anchor from [[note#heading]]
    is_embed    BOOLEAN DEFAULT FALSE,  -- true for ![[embeds]]
    position    INTEGER,               -- character offset in source file
    PRIMARY KEY (vault_id, source_path, target_path, position)
);

-- Backlink lookups: "what links to this note?"
CREATE INDEX idx_links_target
    ON vault_links(vault_id, target_path);

-- Forward link lookups: "what does this note link to?"
CREATE INDEX idx_links_source
    ON vault_links(vault_id, source_path);
```

**Cycle-aware traversal:**

All graph traversal operations use a visited-set algorithm to handle
cycles safely. The traversal engine provides two controls:

- **`max_depth`** — how many link hops to follow (default: 1 for RAG,
  configurable for agents)
- **`max_nodes`** — total node budget for a single traversal (prevents
  runaway walks in densely linked vaults)

Pseudocode for the traversal:

```go
func Traverse(startPath string, maxDepth int, maxNodes int) []Node {
    visited := map[string]bool{}
    queue := []QueueItem{{path: startPath, depth: 0}}
    result := []Node{}

    for len(queue) > 0 && len(result) < maxNodes {
        item := queue[0]
        queue = queue[1:]

        if visited[item.path] || item.depth > maxDepth {
            continue
        }
        visited[item.path] = true

        node := loadNode(item.path)
        result = append(result, node)

        if item.depth < maxDepth {
            for _, link := range getOutboundLinks(item.path) {
                if !visited[link.targetPath] {
                    queue = append(queue, QueueItem{
                        path:  link.targetPath,
                        depth: item.depth + 1,
                    })
                }
            }
        }
    }
    return result
}
```

**API endpoints:**

```
GET  /api/vaults/:id/graph/links?path=note.md
     → Forward links from a file

GET  /api/vaults/:id/graph/backlinks?path=note.md
     → All files linking to this file

POST /api/vaults/:id/graph/traverse
     { "start": "note.md", "max_depth": 2, "max_nodes": 50 }
     → Cycle-aware subgraph rooted at the given file

GET  /api/vaults/:id/graph/stats
     → Graph-level metrics: node count, edge count, strongly
       connected components, orphan notes
```

---

### 3. Web Editor (`web/`)

**Technology:** CodeMirror 6 + Automerge WASM
**Deployment:** Static files served by the Go server

The web editor provides browser-based access to vaults. It connects to
the same sync engine as the plugin, using the same Automerge WASM
library and WebSocket protocol.

**Capabilities (phased):**

| Phase | Feature |
|-------|---------|
| 1     | Read-only vault browsing, file tree, markdown preview |
| 2     | Full markdown editing with CodeMirror 6 |
| 3     | Live collaboration (multiple cursors, presence) |
| 4     | Obsidian-compatible rendering (callouts, embeds, links) |

**Why CodeMirror 6:**

- Obsidian uses CodeMirror 6 internally — similar editing experience
- First-class extension API for custom syntax, decorations, widgets
- Automerge has an official `@automerge/automerge-codemirror` binding
  that wires CRDT changes directly to the editor

---

### 4. Encryption

**Approach:** Client-side (zero-knowledge). The server never sees
plaintext vault content.

**Algorithms:**

- **AES-256-GCM** — authenticated encryption for file content
- **scrypt** — key derivation from user-provided vault password
- **HKDF** — derive per-file keys from the master key

**Flow:**

```
1. User sets a vault encryption password in settings
2. Client derives master key: scrypt(password, salt) → master_key
3. For each file change:
   a. Derive file key: HKDF(master_key, file_path) → file_key
   b. Encrypt: AES-256-GCM(file_key, nonce, plaintext) → ciphertext
   c. Send ciphertext through Automerge sync
4. Server stores only ciphertext in the CRDT document
5. Receiving clients decrypt using the same derived keys
```

**Tradeoff:** With E2E encryption enabled, server-side RAG indexing is
not possible (the server cannot read content). Options:

- Client-side embedding generation (slower, requires local model)
- Selective encryption (encrypt sensitive notes, leave others
  indexable)
- Trusted server mode (no E2E encryption, server can index)

This is a user-configurable choice per vault.

---

### 5. Future: RAG Pipeline

**Embedding flow:**

```
Vault file changed
  → Chunker splits into segments (by heading, paragraph, or sliding window)
  → Link resolver enriches chunks with neighbor context (depth 1, cycle-aware)
  → Embedder generates vectors (local model or API)
  → sqlite-vec stores vectors with chunk metadata
  → Semantic search available via REST API
```

**Chunking strategy for Obsidian markdown:**

- Split on `##` headings (preserve document structure)
- Each chunk includes the heading hierarchy as context
- Frontmatter tags and properties are appended to each chunk
- Typical chunk size: 512–1024 tokens

**Wiki-link resolution for chunk enrichment:**

Wiki-links (`[[note]]`) within a chunk are resolved using the link
graph to append contextual summaries from linked notes. This enrichment
is depth-limited and cycle-aware:

- **Default depth: 1** — only direct links are resolved, not links
  within linked notes
- **Cycle guard** — a visited set prevents re-processing notes already
  seen in the current enrichment pass
- **Summary extraction** — linked notes contribute their title and
  first paragraph (or frontmatter `description` field), not their full
  content
- **Budget** — a maximum of 10 linked-note summaries per chunk to
  bound token usage

Example: a chunk from `Architecture.md` containing `[[Authentication]]`
and `[[Sync Engine]]` is enriched to:

```
[Original chunk content here...]

[Linked: Authentication — JWT-based auth with access/refresh token pairs.
 The server issues short-lived access tokens and long-lived refresh tokens.]
[Linked: Sync Engine — Automerge CRDT-based real-time synchronization.
 All state changes flow through a single Automerge document per vault.]
```

This gives the embedding model awareness of the note's local
neighborhood without exploding into the full vault graph.

**Search API:**

```
POST /api/vaults/:id/search
{
  "query": "How does authentication work?",
  "limit": 20,
  "filters": {
    "tags": ["#architecture"],
    "folders": ["projects/"]
  }
}

Response:
{
  "results": [
    {
      "file": "projects/auth-design.md",
      "heading": "## Token Flow",
      "content": "...",
      "score": 0.87
    }
  ]
}
```

---

### 6. Future: Agent Runtime

Agents are long-running goroutines that operate on vault content via
the same CRDT layer. They read vault data, call external APIs (Claude,
tools), and write results back to the vault.

**Architecture:**

```
Agent goroutine
  ├── Reads from Automerge document (vault content)
  ├── Queries sqlite-vec (semantic search)
  ├── Traverses link graph (cycle-aware, budget-limited)
  ├── Calls Claude API (reasoning, generation)
  ├── Writes results back to Automerge document
  └── Changes sync to all connected clients automatically
```

**Link graph traversal for agents:**

Agents need to follow wiki-link chains to research topics, build
context, and discover related notes. Unlike RAG chunks (which use
depth 1), agents traverse deeper but within explicit budgets:

- **Configurable `max_depth`** — typically 2–3 for focused research,
  up to 5 for broad exploration
- **`max_nodes` budget** — caps the total notes visited per traversal
  (default: 50). Prevents runaway walks in densely linked vaults with
  thousands of interconnected notes
- **Cycle handling** — visited-set guard ensures each note is processed
  at most once per traversal, regardless of how many paths lead to it
- **Traversal strategies:**
  - **BFS** (breadth-first) — explores the immediate neighborhood
    first. Good for "what's related to X?"
  - **DFS** (depth-first) — follows a single thread deeply. Good for
    "trace the chain from X to Y"
  - **Weighted** — prioritize links based on semantic similarity to
    the agent's current query (requires embedding comparison)

**Use cases:**

- Automatic note summarization
- Link suggestion based on semantic similarity
- Knowledge graph extraction from vault structure + content
- Question answering over vault contents
- Automated tagging and categorization

Agents write to the vault through the same CRDT mechanism as any other
client. Their changes merge seamlessly with user edits. Users see agent
output appear in Obsidian in real time.

---

## Database Schema

```sql
-- Core tables

CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    username    TEXT UNIQUE NOT NULL,
    password    TEXT NOT NULL,          -- Argon2id hash
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE vaults (
    id          TEXT PRIMARY KEY,       -- UUID
    name        TEXT NOT NULL,
    owner_id    INTEGER REFERENCES users(id),
    crdt_state  BLOB,                  -- Automerge document binary
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE vault_files (
    id          INTEGER PRIMARY KEY,
    vault_id    TEXT REFERENCES vaults(id),
    path        TEXT NOT NULL,
    content     BLOB,                  -- File content (materialized)
    hash        TEXT NOT NULL,         -- SHA-256
    size        INTEGER NOT NULL,
    is_binary   BOOLEAN DEFAULT FALSE,
    created_at  DATETIME,
    modified_at DATETIME,
    UNIQUE(vault_id, path)
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    user_id      INTEGER REFERENCES users(id),
    refresh_hash TEXT NOT NULL,        -- Hashed refresh token
    expires_at   DATETIME NOT NULL,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Link graph

CREATE TABLE vault_links (
    vault_id    TEXT NOT NULL REFERENCES vaults(id),
    source_path TEXT NOT NULL,          -- file containing the link
    target_path TEXT NOT NULL,          -- resolved file the link points to
    link_text   TEXT,                   -- display text from [[target|display]]
    anchor      TEXT,                   -- heading anchor from [[note#heading]]
    is_embed    BOOLEAN DEFAULT FALSE,  -- true for ![[embeds]]
    position    INTEGER,               -- character offset in source file
    PRIMARY KEY (vault_id, source_path, target_path, position)
);

CREATE INDEX idx_links_target
    ON vault_links(vault_id, target_path);

CREATE INDEX idx_links_source
    ON vault_links(vault_id, source_path);

-- Future: embeddings

CREATE VIRTUAL TABLE vec_chunks USING vec0(
    chunk_id INTEGER PRIMARY KEY,
    embedding float[1536]
);

CREATE TABLE chunks (
    id        INTEGER PRIMARY KEY,
    vault_id  TEXT REFERENCES vaults(id),
    file_path TEXT NOT NULL,
    content   TEXT NOT NULL,           -- Chunk text
    start_pos INTEGER,
    end_pos   INTEGER,
    UNIQUE(vault_id, file_path, start_pos)
);
```

---

## Project Structure

```
obsidian-center/
├── ARCHITECTURE.md          # This document
├── plugin/                  # Obsidian plugin (TypeScript)
│   ├── src/
│   │   ├── main.ts          # Plugin entry point
│   │   ├── sync/            # Automerge WASM + WebSocket client
│   │   ├── settings.ts      # Plugin settings UI
│   │   └── crypto.ts        # Client-side encryption
│   ├── manifest.json
│   ├── package.json
│   ├── tsconfig.json
│   └── esbuild.config.mjs
├── server/                  # Go server
│   ├── cmd/server/
│   │   └── main.go
│   ├── internal/
│   │   ├── auth/
│   │   ├── sync/
│   │   ├── vault/
│   │   ├── graph/           # Link graph parsing, indexing, traversal
│   │   ├── api/
│   │   └── rag/             # Future
│   ├── migrations/
│   ├── go.mod
│   └── go.sum
├── web/                     # Web editor (later phase)
│   ├── src/
│   ├── index.html
│   └── package.json
├── proto/                   # Shared protocol definitions
│   └── messages.go          # Message types used by all components
├── docker-compose.yml       # Development environment
├── Dockerfile               # Server production build
└── Makefile                 # Build, test, dev commands
```

---

## Development Phases

### Phase 1 — Foundation
- Go server skeleton (HTTP + WebSocket)
- JWT authentication (register, login, refresh)
- SQLite schema and migrations
- Obsidian plugin skeleton (settings, lifecycle)
- Basic file sync (no CRDT yet — hash-based last-write-wins)

### Phase 2 — CRDT Sync
- Integrate Automerge Go bindings on server
- Integrate Automerge WASM in plugin
- Implement sync protocol over WebSocket
- Offline queue and reconnection logic
- Multi-device testing

### Phase 3 — Link Graph
- Wiki-link parser (all Obsidian link forms)
- `vault_links` table and incremental updates on file change
- Backlink query API
- Cycle-aware traversal engine with depth/node budgets
- Graph statistics endpoint

### Phase 4 — Web Editor
- Static file serving from Go server
- CodeMirror 6 markdown editor
- Automerge WASM integration for live sync
- File browser UI
- Authentication flow in browser

### Phase 5 — Encryption
- Client-side AES-256-GCM encryption
- Key derivation and management
- Encrypted sync protocol
- Per-vault encryption settings

### Phase 6 — RAG
- Markdown chunking pipeline
- Depth-1 wiki-link enrichment for chunks (cycle-aware)
- Embedding generation (local model or API)
- sqlite-vec index
- Semantic search API
- Obsidian plugin search command

### Phase 7 — Agents
- Agent runtime framework
- Claude API integration
- Budget-limited link graph traversal for agent research
- Vault read/write through CRDT layer
- Agent management API
- Agent configuration in Obsidian settings

---

## Design Principles

1. **Single binary server.** No external runtime dependencies. SQLite
   is embedded. The server is one `go build` away from deployment.

2. **CRDT-first.** All state changes flow through Automerge. This
   guarantees convergence regardless of network conditions, client
   count, or timing.

3. **Obsidian is the UI.** The server never renders markdown or manages
   layout. Obsidian handles all user-facing formatting. The web editor
   is a lightweight supplement, not a replacement.

4. **Zero-knowledge optional.** Users choose between full E2E
   encryption (server cannot read content) and trusted-server mode
   (enables server-side RAG). This is a per-vault setting.

5. **Offline-first.** Every client maintains a complete local copy. The
   server is a coordination point, not a dependency. Clients function
   fully offline and reconcile when connectivity returns.

6. **Agents are peers.** AI agents interact with vault data through the
   same CRDT sync layer as human users. No special write paths, no
   separate storage — just another client making changes that merge
   cleanly.

7. **Graph-aware.** The link structure of a vault is a first-class data
   model, not an afterthought. The server maintains a materialized link
   graph, supports backlink queries, and provides cycle-safe traversal
   primitives that both RAG and agents build on.

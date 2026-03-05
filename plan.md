# Phase 2 — CRDT Sync Implementation Plan

## Overview
Replace the Phase 1 hash-based last-write-wins sync with Automerge CRDT sync.
Each vault becomes a single Automerge document. Clients exchange Automerge sync
messages over WebSocket, giving character-level conflict-free merging.

---

## Step 1: Server — Add Automerge Go bindings

**Package:** `github.com/automerge/automerge-go` (CGO, wraps Rust core)

**New files in `server/internal/sync/`:**

- **`automerge.go`** — Thin wrapper around automerge-go:
  - `NewDoc()` / `LoadDoc(bytes)` / `SaveDoc(doc)`
  - `ApplyFile(doc, path, content)` — set/update a file entry in the doc
  - `DeleteFile(doc, path)` — remove a file entry
  - `ReadFile(doc, path)` — read content from doc
  - `ListFiles(doc)` — return all paths with hashes
  - `GenerateSyncMessage(doc, syncState)` / `ReceiveSyncMessage(doc, syncState, msg)`

- **`engine.go`** — Sync engine managing per-vault docs:
  - Loads/saves Automerge binary from `vaults.crdt_state` column
  - Lazy-loads docs on first connection, keeps in memory
  - Periodically persists to DB
  - Thread-safe access via per-vault mutexes

- **`protocol.go`** — WebSocket message types:
  ```
  { "type": "sync",    "data": "<base64 automerge sync message>" }
  { "type": "request", "data": { "paths": ["file.md"] } }
  { "type": "error",   "data": { "code": "...", "message": "..." } }
  { "type": "ping" } / { "type": "pong" }
  ```

- **`websocket.go`** — Refactored WebSocket handler:
  - Per-client `automerge.SyncState`
  - On connect: begin sync protocol (exchange sync messages until converged)
  - On `sync` message: receive into doc, generate reply, broadcast to others
  - On file mutation via REST API: update doc, broadcast sync messages
  - Persist doc state after changes

**Changes to existing files:**
- `vault/store.go` — Add `GetCRDTState(vaultID)` / `SaveCRDTState(vaultID, blob)`
- `api/router.go` — Pass sync engine to handler
- `cmd/server/main.go` — Initialize sync engine
- `go.mod` — Add `github.com/automerge/automerge-go`

**Migration `003_crdt_sync.sql`** — No schema change needed; `crdt_state` BLOB column
already exists in vaults table. Add a `sync_version` column to track doc format.

---

## Step 2: Server — Bootstrap CRDT from existing files

For vaults that already have files but no CRDT state:
- On first sync connection, if `crdt_state` is NULL:
  - Create new Automerge doc
  - Populate from `vault_files` table
  - Save CRDT state
- This preserves Phase 1 data during upgrade

---

## Step 3: Server — Keep vault_files materialized

When the Automerge doc changes (from any sync message):
- Diff the change and update `vault_files` accordingly
- This keeps the REST file API working
- Needed for future RAG pipeline (Phase 6)

---

## Step 4: Plugin — Add Automerge WASM

**Package:** `@automerge/automerge` (WASM build)

**New file `plugin/src/sync/crdt.ts`:**
- Initialize Automerge with `next.init()`
- `CRDTManager` class:
  - Holds the local Automerge document
  - `applyLocalChange(path, content)` — update file in doc
  - `deleteLocalFile(path)` — remove from doc
  - `generateSyncMessage(syncState)` — create msg for server
  - `receiveSyncMessage(syncState, msg)` — apply server msg, return patches
  - `getFileContent(path)` — read from doc
  - `getAllFiles()` — list all paths
  - `save()` / `load(binary)` — persist doc state

**Persistence:** Save Automerge doc binary to Obsidian's plugin data
(`this.app.vault.adapter.writeBinary`), load on startup.

---

## Step 5: Plugin — Rewrite sync client for CRDT

**Modify `plugin/src/sync/client.ts`:**
- On connect: exchange Automerge sync messages (loop until converged)
- Local file changes → update Automerge doc → generate sync msg → send
- Receive sync msg → apply to doc → extract patches → apply to local vault
- Remove hash-based put_file/delete_file messages
- Keep REST fallback for initial full-file downloads only if needed

---

## Step 6: Plugin — Offline queue and reconnection

**New file `plugin/src/sync/offline.ts`:**
- When disconnected, local changes still go into the Automerge doc
- The doc accumulates changes naturally (CRDT property)
- On reconnect, the sync protocol automatically sends only missing changes
- No explicit queue needed — Automerge's sync state handles this
- Persist doc state on every change so nothing is lost on crash

**Reconnection improvements in `client.ts`:**
- Exponential backoff already exists — keep it
- On reconnect: resume sync protocol with existing sync state
- Reset sync state only if server indicates doc was reset

---

## Step 7: Update main.ts for CRDT flow

**Modify `plugin/src/main.ts`:**
- Initialize CRDTManager on load, load persisted state
- File event handlers: update CRDT doc instead of direct WebSocket puts
- Sync client sends CRDT sync messages instead of file contents
- Apply remote patches from CRDT to local vault
- Save CRDT state on unload and periodically

---

## Implementation Order

1. Server: automerge-go integration + sync engine (Steps 1-3)
2. Plugin: automerge WASM + CRDT manager (Steps 4-5)
3. Plugin: offline + reconnection (Step 6)
4. Plugin: main.ts integration (Step 7)
5. Test end-to-end

## Dependencies to add
- Server: `github.com/automerge/automerge-go` (requires CGO + Rust toolchain)
- Plugin: `@automerge/automerge` (WASM, ~2MB)

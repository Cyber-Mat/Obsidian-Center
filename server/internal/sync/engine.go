package sync

import (
	"fmt"
	"log/slog"
	"strings"
	gosync "sync"
	"time"

	"github.com/Cyber-Mat/obsidian-center/server/internal/graph"
	"github.com/automerge/automerge-go"
)

// CRDTStore is the interface for persisting CRDT state.
type CRDTStore interface {
	GetCRDTState(vaultID string) ([]byte, error)
	SaveCRDTState(vaultID string, data []byte) error
	ListFilesRaw(vaultID string) ([]FileRecord, error)
	UpsertFileFromCRDT(vaultID, path string, content []byte, hash string, isBinary bool) error
	DeleteFileRecord(vaultID, path string) error
}

// FileRecord represents a file in the vault_files table.
type FileRecord struct {
	Path    string
	Hash    string
	Content []byte
}

// Engine manages CRDT documents for all vaults.
type Engine struct {
	store  CRDTStore
	graphs *graph.Store
	mu     gosync.Mutex
	vaults map[string]*vaultState
}

type vaultState struct {
	doc    *VaultDoc
	mu     gosync.Mutex
	dirty  bool
	lastSave time.Time
}

// NewEngine creates a sync engine.
func NewEngine(store CRDTStore, graphs *graph.Store) *Engine {
	e := &Engine{
		store:  store,
		graphs: graphs,
		vaults: make(map[string]*vaultState),
	}
	go e.persistLoop()
	return e
}

// persistLoop periodically saves dirty documents to the database.
func (e *Engine) persistLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		e.mu.Lock()
		vaultIDs := make([]string, 0, len(e.vaults))
		for id := range e.vaults {
			vaultIDs = append(vaultIDs, id)
		}
		e.mu.Unlock()

		for _, id := range vaultIDs {
			vs := e.getVaultState(id)
			if vs == nil {
				continue
			}
			vs.mu.Lock()
			if vs.dirty {
				data := vs.doc.Save()
				if err := e.store.SaveCRDTState(id, data); err != nil {
					slog.Error("failed to persist CRDT state", "vault", id, "error", err)
				} else {
					vs.dirty = false
					vs.lastSave = time.Now()
				}
			}
			vs.mu.Unlock()
		}
	}
}

func (e *Engine) getVaultState(vaultID string) *vaultState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vaults[vaultID]
}

// GetDoc loads or creates the CRDT document for a vault.
func (e *Engine) GetDoc(vaultID string) (*VaultDoc, error) {
	e.mu.Lock()
	vs, ok := e.vaults[vaultID]
	e.mu.Unlock()

	if ok {
		return vs.doc, nil
	}

	// Load from database
	data, err := e.store.GetCRDTState(vaultID)
	if err != nil {
		return nil, fmt.Errorf("load CRDT state: %w", err)
	}

	var doc *VaultDoc
	if data != nil && len(data) > 0 {
		doc, err = LoadVaultDoc(data)
		if err != nil {
			slog.Warn("corrupt CRDT state, creating new", "vault", vaultID, "error", err)
			doc = NewVaultDoc()
		}
	} else {
		// Bootstrap from existing vault_files
		doc = NewVaultDoc()
		if err := e.bootstrapFromFiles(vaultID, doc); err != nil {
			slog.Error("bootstrap from files failed", "vault", vaultID, "error", err)
		}
	}

	vs = &vaultState{doc: doc, dirty: true, lastSave: time.Now()}
	e.mu.Lock()
	e.vaults[vaultID] = vs
	e.mu.Unlock()

	// Persist immediately after bootstrap
	if err := e.store.SaveCRDTState(vaultID, doc.Save()); err != nil {
		slog.Error("failed to save bootstrapped CRDT", "vault", vaultID, "error", err)
	}
	vs.dirty = false

	return doc, nil
}

// bootstrapFromFiles populates a new CRDT doc from existing vault_files rows.
func (e *Engine) bootstrapFromFiles(vaultID string, doc *VaultDoc) error {
	files, err := e.store.ListFilesRaw(vaultID)
	if err != nil {
		return err
	}

	for _, f := range files {
		isBinary := isBinaryContent(f.Path, f.Content)
		if err := doc.PutFile(f.Path, f.Content, isBinary); err != nil {
			slog.Error("bootstrap file failed", "path", f.Path, "error", err)
		}
	}

	slog.Info("bootstrapped CRDT from files", "vault", vaultID, "count", len(files))
	return nil
}

// LockVault acquires the per-vault lock. Must call UnlockVault when done.
func (e *Engine) LockVault(vaultID string) {
	e.mu.Lock()
	vs, ok := e.vaults[vaultID]
	e.mu.Unlock()
	if ok {
		vs.mu.Lock()
	}
}

// UnlockVault releases the per-vault lock.
func (e *Engine) UnlockVault(vaultID string) {
	e.mu.Lock()
	vs, ok := e.vaults[vaultID]
	e.mu.Unlock()
	if ok {
		vs.mu.Unlock()
	}
}

// MarkDirty marks the vault's CRDT state as needing persistence.
func (e *Engine) MarkDirty(vaultID string) {
	e.mu.Lock()
	vs, ok := e.vaults[vaultID]
	e.mu.Unlock()
	if ok {
		vs.dirty = true
	}
}

// NewSyncState creates a new sync state for a peer connected to a vault.
func (e *Engine) NewSyncState(vaultID string) (*automerge.SyncState, error) {
	doc, err := e.GetDoc(vaultID)
	if err != nil {
		return nil, err
	}
	return automerge.NewSyncState(doc.Doc()), nil
}

// ReceiveSyncMessage processes an incoming sync message from a client.
func (e *Engine) ReceiveSyncMessage(vaultID string, syncState *automerge.SyncState, msg []byte) error {
	e.mu.Lock()
	vs, ok := e.vaults[vaultID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("vault not loaded: %s", vaultID)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	if _, err := syncState.ReceiveMessage(msg); err != nil {
		return fmt.Errorf("receive sync message: %w", err)
	}
	vs.dirty = true

	return nil
}

// GenerateSyncMessage generates the next message to send to a peer.
func (e *Engine) GenerateSyncMessage(syncState *automerge.SyncState) ([]byte, bool) {
	msg, valid := syncState.GenerateMessage()
	if !valid {
		return nil, false
	}
	return msg.Bytes(), true
}

// fileSnapshot holds a consistent snapshot of a file's state from the CRDT doc.
type fileSnapshot struct {
	Path     string
	Hash     string
	Content  []byte
	IsBinary bool
}

// MaterializeFiles syncs the CRDT document state to the vault_files table
// and re-indexes wiki-links for markdown files.
func (e *Engine) MaterializeFiles(vaultID string) error {
	e.mu.Lock()
	vs, ok := e.vaults[vaultID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("vault not loaded: %s", vaultID)
	}

	// Snapshot all file data under a single lock to avoid race conditions
	vs.mu.Lock()
	infos, err := vs.doc.ListFiles()
	if err != nil {
		vs.mu.Unlock()
		return err
	}

	snapshots := make([]fileSnapshot, 0, len(infos))
	allPaths := make([]string, 0, len(infos))
	for _, info := range infos {
		allPaths = append(allPaths, info.Path)
		content, isBinary, readErr := vs.doc.ReadFile(info.Path)
		if readErr != nil {
			slog.Error("materialize read failed", "path", info.Path, "error", readErr)
			continue
		}
		snapshots = append(snapshots, fileSnapshot{
			Path:     info.Path,
			Hash:     info.Hash,
			Content:  content,
			IsBinary: isBinary,
		})
	}
	vs.mu.Unlock()

	// Write to DB outside the lock
	for _, snap := range snapshots {
		if err := e.store.UpsertFileFromCRDT(vaultID, snap.Path, snap.Content, snap.Hash, snap.IsBinary); err != nil {
			slog.Error("materialize upsert failed", "path", snap.Path, "error", err)
			continue
		}

		// Re-index links for markdown files
		if e.graphs != nil && !snap.IsBinary && isMarkdown(snap.Path) {
			links := graph.ParseLinks(string(snap.Content), allPaths)
			if err := e.graphs.UpdateLinks(vaultID, snap.Path, links); err != nil {
				slog.Error("link index failed", "path", snap.Path, "error", err)
			}
		}
	}

	return nil
}

func isMarkdown(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".md")
}

// Shutdown persists all dirty documents.
func (e *Engine) Shutdown() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for id, vs := range e.vaults {
		vs.mu.Lock()
		if vs.dirty {
			data := vs.doc.Save()
			if err := e.store.SaveCRDTState(id, data); err != nil {
				slog.Error("shutdown persist failed", "vault", id, "error", err)
			}
		}
		vs.mu.Unlock()
	}
}

func isBinaryContent(path string, content []byte) bool {
	binaryExts := []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".tar", ".gz", ".mp3", ".mp4", ".wav", ".ogg"}
	for _, ext := range binaryExts {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
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

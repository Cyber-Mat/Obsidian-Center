package sync

import "github.com/Cyber-Mat/obsidian-center/server/internal/vault"

// StoreAdapter adapts vault.Store to the CRDTStore interface.
type StoreAdapter struct {
	store *vault.Store
}

// NewStoreAdapter wraps a vault.Store to satisfy CRDTStore.
func NewStoreAdapter(store *vault.Store) *StoreAdapter {
	return &StoreAdapter{store: store}
}

func (a *StoreAdapter) GetCRDTState(vaultID string) ([]byte, error) {
	return a.store.GetCRDTState(vaultID)
}

func (a *StoreAdapter) SaveCRDTState(vaultID string, data []byte) error {
	return a.store.SaveCRDTState(vaultID, data)
}

func (a *StoreAdapter) ListFilesRaw(vaultID string) ([]FileRecord, error) {
	raw, err := a.store.ListFilesRaw(vaultID)
	if err != nil {
		return nil, err
	}
	records := make([]FileRecord, len(raw))
	for i, f := range raw {
		records[i] = FileRecord{Path: f.Path, Hash: f.Hash, Content: f.Content}
	}
	return records, nil
}

func (a *StoreAdapter) UpsertFileFromCRDT(vaultID, path string, content []byte, hash string, isBinary bool) error {
	return a.store.UpsertFileFromCRDT(vaultID, path, content, hash, isBinary)
}

func (a *StoreAdapter) DeleteFileRecord(vaultID, path string) error {
	return a.store.DeleteFileRecord(vaultID, path)
}

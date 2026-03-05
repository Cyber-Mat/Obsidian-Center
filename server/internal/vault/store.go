package vault

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
)

type Vault struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   int64     `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type File struct {
	ID         int64     `json:"id"`
	VaultID    string    `json:"vault_id"`
	Path       string    `json:"path"`
	Content    []byte    `json:"-"`
	Hash       string    `json:"hash"`
	Size       int64     `json:"size"`
	IsBinary   bool      `json:"is_binary"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
}

type FileMeta struct {
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Size       int64  `json:"size"`
	IsBinary   bool   `json:"is_binary"`
	ModifiedAt string `json:"modified_at"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Vault operations

func (s *Store) CreateVault(name string, ownerID int64) (*Vault, error) {
	id := auth.NewID()
	now := time.Now()

	_, err := s.db.Exec(
		"INSERT INTO vaults (id, name, owner_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, name, ownerID, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert vault: %w", err)
	}

	return &Vault{ID: id, Name: name, OwnerID: ownerID, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) GetVault(id string, ownerID int64) (*Vault, error) {
	var v Vault
	err := s.db.QueryRow(
		"SELECT id, name, owner_id, created_at, updated_at FROM vaults WHERE id = ? AND owner_id = ?",
		id, ownerID,
	).Scan(&v.ID, &v.Name, &v.OwnerID, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("vault not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query vault: %w", err)
	}
	return &v, nil
}

func (s *Store) ListVaults(ownerID int64) ([]Vault, error) {
	rows, err := s.db.Query(
		"SELECT id, name, owner_id, created_at, updated_at FROM vaults WHERE owner_id = ? ORDER BY name",
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("query vaults: %w", err)
	}
	defer rows.Close()

	var vaults []Vault
	for rows.Next() {
		var v Vault
		if err := rows.Scan(&v.ID, &v.Name, &v.OwnerID, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vault: %w", err)
		}
		vaults = append(vaults, v)
	}
	return vaults, rows.Err()
}

func (s *Store) DeleteVault(id string, ownerID int64) error {
	result, err := s.db.Exec("DELETE FROM vaults WHERE id = ? AND owner_id = ?", id, ownerID)
	if err != nil {
		return fmt.Errorf("delete vault: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("vault not found")
	}
	return nil
}

// File operations

func (s *Store) PutFile(vaultID, path string, content []byte, isBinary bool) (*File, error) {
	h := sha256.Sum256(content)
	hash := hex.EncodeToString(h[:])
	now := time.Now()

	_, err := s.db.Exec(`
		INSERT INTO vault_files (vault_id, path, content, hash, size, is_binary, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(vault_id, path) DO UPDATE SET
			content = excluded.content,
			hash = excluded.hash,
			size = excluded.size,
			is_binary = excluded.is_binary,
			modified_at = excluded.modified_at
	`, vaultID, path, content, hash, len(content), isBinary, now, now)
	if err != nil {
		return nil, fmt.Errorf("upsert file: %w", err)
	}

	// Update vault timestamp
	s.db.Exec("UPDATE vaults SET updated_at = ? WHERE id = ?", now, vaultID)

	return &File{
		VaultID:    vaultID,
		Path:       path,
		Hash:       hash,
		Size:       int64(len(content)),
		IsBinary:   isBinary,
		ModifiedAt: now,
	}, nil
}

func (s *Store) GetFile(vaultID, path string) (*File, error) {
	var f File
	err := s.db.QueryRow(`
		SELECT id, vault_id, path, content, hash, size, is_binary, created_at, modified_at
		FROM vault_files WHERE vault_id = ? AND path = ?
	`, vaultID, path).Scan(&f.ID, &f.VaultID, &f.Path, &f.Content, &f.Hash, &f.Size, &f.IsBinary, &f.CreatedAt, &f.ModifiedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query file: %w", err)
	}
	return &f, nil
}

func (s *Store) DeleteFile(vaultID, path string) error {
	result, err := s.db.Exec("DELETE FROM vault_files WHERE vault_id = ? AND path = ?", vaultID, path)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("file not found")
	}
	return nil
}

func (s *Store) ListFiles(vaultID string) ([]FileMeta, error) {
	rows, err := s.db.Query(`
		SELECT path, hash, size, is_binary, modified_at
		FROM vault_files WHERE vault_id = ? ORDER BY path
	`, vaultID)
	if err != nil {
		return nil, fmt.Errorf("query files: %w", err)
	}
	defer rows.Close()

	var files []FileMeta
	for rows.Next() {
		var f FileMeta
		if err := rows.Scan(&f.Path, &f.Hash, &f.Size, &f.IsBinary, &f.ModifiedAt); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// Snapshot returns all file hashes for a vault (used for sync comparison)
func (s *Store) Snapshot(vaultID string) (map[string]string, error) {
	rows, err := s.db.Query("SELECT path, hash FROM vault_files WHERE vault_id = ?", vaultID)
	if err != nil {
		return nil, fmt.Errorf("query snapshot: %w", err)
	}
	defer rows.Close()

	snapshot := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		snapshot[path] = hash
	}
	return snapshot, rows.Err()
}

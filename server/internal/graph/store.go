package graph

import (
	"database/sql"
	"fmt"
)

// Store provides operations on the vault_links table.
type Store struct {
	db *sql.DB
}

// NewStore creates a graph store backed by the given database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// StoredLink represents a row from the vault_links table.
type StoredLink struct {
	VaultID    string `json:"vault_id,omitempty"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	LinkText   string `json:"link_text,omitempty"`
	Anchor     string `json:"anchor,omitempty"`
	IsEmbed    bool   `json:"is_embed"`
	Position   int    `json:"position"`
}

// UpdateLinks atomically replaces all links for a source file.
// It deletes existing links for the source, then inserts the new ones.
func (s *Store) UpdateLinks(vaultID, sourcePath string, links []Link) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete existing links from this source
	if _, err := tx.Exec(
		"DELETE FROM vault_links WHERE vault_id = ? AND source_path = ?",
		vaultID, sourcePath,
	); err != nil {
		return fmt.Errorf("delete old links: %w", err)
	}

	// Insert new links
	if len(links) > 0 {
		stmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO vault_links
				(vault_id, source_path, target_path, link_text, anchor, is_embed, position)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer stmt.Close()

		for _, l := range links {
			if l.TargetPath == "" {
				continue
			}
			if _, err := stmt.Exec(
				vaultID, sourcePath, l.TargetPath,
				nullString(l.LinkText), nullString(l.Anchor),
				l.IsEmbed, l.Position,
			); err != nil {
				return fmt.Errorf("insert link to %s: %w", l.TargetPath, err)
			}
		}
	}

	return tx.Commit()
}

// DeleteLinksForFile removes all links where the file is the source.
func (s *Store) DeleteLinksForFile(vaultID, sourcePath string) error {
	_, err := s.db.Exec(
		"DELETE FROM vault_links WHERE vault_id = ? AND source_path = ?",
		vaultID, sourcePath,
	)
	return err
}

// DeleteAllLinks removes all links for a vault.
func (s *Store) DeleteAllLinks(vaultID string) error {
	_, err := s.db.Exec("DELETE FROM vault_links WHERE vault_id = ?", vaultID)
	return err
}

// GetForwardLinks returns all links from a source file.
func (s *Store) GetForwardLinks(vaultID, sourcePath string) ([]StoredLink, error) {
	rows, err := s.db.Query(`
		SELECT source_path, target_path, COALESCE(link_text,''), COALESCE(anchor,''), is_embed, position
		FROM vault_links
		WHERE vault_id = ? AND source_path = ?
		ORDER BY position
	`, vaultID, sourcePath)
	if err != nil {
		return nil, fmt.Errorf("query forward links: %w", err)
	}
	defer rows.Close()
	return scanLinks(rows)
}

// GetBacklinks returns all links pointing to a target file.
func (s *Store) GetBacklinks(vaultID, targetPath string) ([]StoredLink, error) {
	rows, err := s.db.Query(`
		SELECT source_path, target_path, COALESCE(link_text,''), COALESCE(anchor,''), is_embed, position
		FROM vault_links
		WHERE vault_id = ? AND target_path = ?
		ORDER BY source_path, position
	`, vaultID, targetPath)
	if err != nil {
		return nil, fmt.Errorf("query backlinks: %w", err)
	}
	defer rows.Close()
	return scanLinks(rows)
}

// GetAllLinks returns every link in a vault (used for traversal/stats).
func (s *Store) GetAllLinks(vaultID string) ([]StoredLink, error) {
	rows, err := s.db.Query(`
		SELECT source_path, target_path, COALESCE(link_text,''), COALESCE(anchor,''), is_embed, position
		FROM vault_links
		WHERE vault_id = ?
	`, vaultID)
	if err != nil {
		return nil, fmt.Errorf("query all links: %w", err)
	}
	defer rows.Close()
	return scanLinks(rows)
}

// Stats holds graph-level metrics.
type Stats struct {
	NodeCount  int      `json:"node_count"`
	EdgeCount  int      `json:"edge_count"`
	OrphanNotes []string `json:"orphan_notes"`
}

// GetStats computes graph metrics for a vault.
func (s *Store) GetStats(vaultID string) (*Stats, error) {
	// Count unique edges (deduplicated by source+target)
	var edgeCount int
	err := s.db.QueryRow(`
		SELECT COUNT(DISTINCT source_path || '→' || target_path)
		FROM vault_links WHERE vault_id = ?
	`, vaultID).Scan(&edgeCount)
	if err != nil {
		return nil, fmt.Errorf("count edges: %w", err)
	}

	// Get all unique nodes (files that are either sources or targets)
	nodeSet := map[string]bool{}

	rows, err := s.db.Query(`
		SELECT DISTINCT source_path FROM vault_links WHERE vault_id = ?
		UNION
		SELECT DISTINCT target_path FROM vault_links WHERE vault_id = ?
	`, vaultID, vaultID)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		nodeSet[p] = true
	}

	// Find orphan notes: files that exist in vault_files but have no
	// incoming or outgoing links
	allFiles, err := s.getAllFilePaths(vaultID)
	if err != nil {
		return nil, err
	}

	var orphans []string
	for _, f := range allFiles {
		if !nodeSet[f] {
			orphans = append(orphans, f)
		}
	}

	return &Stats{
		NodeCount:   len(nodeSet),
		EdgeCount:   edgeCount,
		OrphanNotes: orphans,
	}, nil
}

func (s *Store) getAllFilePaths(vaultID string) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT path FROM vault_files WHERE vault_id = ? ORDER BY path", vaultID,
	)
	if err != nil {
		return nil, fmt.Errorf("query file paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

func scanLinks(rows *sql.Rows) ([]StoredLink, error) {
	var links []StoredLink
	for rows.Next() {
		var l StoredLink
		if err := rows.Scan(&l.SourcePath, &l.TargetPath, &l.LinkText, &l.Anchor, &l.IsEmbed, &l.Position); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

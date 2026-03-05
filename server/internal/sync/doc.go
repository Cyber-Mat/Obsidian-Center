package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/automerge/automerge-go"
)

// VaultDoc wraps an Automerge document representing a vault's file tree.
//
// Document structure:
//
//	{
//	  "files": {
//	    "path/to/note.md": {
//	      "content": Text("..."),
//	      "hash": "sha256:...",
//	      "modified": timestamp,
//	      "is_binary": false
//	    }
//	  },
//	  "metadata": {
//	    "schema_version": 1
//	  }
//	}
type VaultDoc struct {
	doc *automerge.Doc
}

// NewVaultDoc creates a new empty vault document.
func NewVaultDoc() *VaultDoc {
	doc := automerge.New()
	doc.Path("files").Set(&automerge.Map{})
	doc.Path("metadata", "schema_version").Set(int64(1))
	doc.Commit("init vault")
	return &VaultDoc{doc: doc}
}

// LoadVaultDoc loads a vault document from saved bytes.
func LoadVaultDoc(data []byte) (*VaultDoc, error) {
	doc, err := automerge.Load(data)
	if err != nil {
		return nil, fmt.Errorf("load vault doc: %w", err)
	}
	return &VaultDoc{doc: doc}, nil
}

// Save serializes the document to bytes.
func (vd *VaultDoc) Save() []byte {
	return vd.doc.Save()
}

// Doc returns the underlying automerge document.
func (vd *VaultDoc) Doc() *automerge.Doc {
	return vd.doc
}

// PutFile sets or updates a file in the document.
func (vd *VaultDoc) PutFile(path string, content []byte, isBinary bool) error {
	h := sha256.Sum256(content)
	hash := hex.EncodeToString(h[:])
	now := time.Now()

	filePath := vd.doc.Path("files", path)

	if isBinary {
		if err := filePath.Set(&automerge.Map{}); err != nil {
			return fmt.Errorf("set file map: %w", err)
		}
		if err := filePath.Path("content").Set(content); err != nil {
			return fmt.Errorf("set binary content: %w", err)
		}
	} else {
		// Check if the file entry already exists with a text field
		existing, _ := filePath.Path("content").Get()
		if existing != nil && !existing.IsVoid() && existing.Kind() == automerge.KindText {
			// Update existing text
			text := filePath.Path("content").Text()
			if err := text.Set(string(content)); err != nil {
				return fmt.Errorf("set text content: %w", err)
			}
		} else {
			// Create new file entry with text
			if err := filePath.Set(&automerge.Map{}); err != nil {
				return fmt.Errorf("set file map: %w", err)
			}
			if err := filePath.Path("content").Set(automerge.NewText(string(content))); err != nil {
				return fmt.Errorf("set text content: %w", err)
			}
		}
	}

	filePath.Path("hash").Set(hash)
	filePath.Path("modified").Set(now)
	filePath.Path("is_binary").Set(isBinary)

	vd.doc.Commit(fmt.Sprintf("update %s", path))
	return nil
}

// DeleteFile removes a file from the document.
func (vd *VaultDoc) DeleteFile(path string) error {
	if err := vd.doc.Path("files", path).Delete(); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	vd.doc.Commit(fmt.Sprintf("delete %s", path))
	return nil
}

// ReadFile returns the content of a file.
func (vd *VaultDoc) ReadFile(path string) ([]byte, bool, error) {
	filePath := vd.doc.Path("files", path)

	val, err := filePath.Get()
	if err != nil || val.IsVoid() {
		return nil, false, fmt.Errorf("file not found: %s", path)
	}

	isBinaryVal, _ := filePath.Path("is_binary").Get()
	isBinary := isBinaryVal != nil && !isBinaryVal.IsVoid() && isBinaryVal.Bool()

	contentVal, err := filePath.Path("content").Get()
	if err != nil || contentVal.IsVoid() {
		return nil, false, fmt.Errorf("content not found: %s", path)
	}

	if isBinary {
		return contentVal.Bytes(), true, nil
	}
	text, err := filePath.Path("content").Text().Get()
	if err != nil {
		return nil, false, fmt.Errorf("read text: %w", err)
	}
	return []byte(text), false, nil
}

// FileInfo holds metadata about a file in the document.
type FileInfo struct {
	Path     string
	Hash     string
	IsBinary bool
	Modified time.Time
}

// ListFiles returns metadata for all files in the document.
func (vd *VaultDoc) ListFiles() ([]FileInfo, error) {
	filesVal, err := vd.doc.Path("files").Get()
	if err != nil || filesVal.IsVoid() {
		return nil, nil
	}

	filesMap := vd.doc.Path("files").Map()
	keys, err := filesMap.Keys()
	if err != nil {
		return nil, fmt.Errorf("list file keys: %w", err)
	}

	var infos []FileInfo
	for _, key := range keys {
		info := FileInfo{Path: key}

		hashVal, _ := vd.doc.Path("files", key, "hash").Get()
		if hashVal != nil && !hashVal.IsVoid() {
			info.Hash = hashVal.Str()
		}

		binaryVal, _ := vd.doc.Path("files", key, "is_binary").Get()
		if binaryVal != nil && !binaryVal.IsVoid() {
			info.IsBinary = binaryVal.Bool()
		}

		modVal, _ := vd.doc.Path("files", key, "modified").Get()
		if modVal != nil && !modVal.IsVoid() {
			info.Modified = modVal.Time()
		}

		infos = append(infos, info)
	}
	return infos, nil
}

// Snapshot returns a map of path → hash for all files.
func (vd *VaultDoc) Snapshot() (map[string]string, error) {
	infos, err := vd.ListFiles()
	if err != nil {
		return nil, err
	}
	snapshot := make(map[string]string, len(infos))
	for _, info := range infos {
		snapshot[info.Path] = info.Hash
	}
	return snapshot, nil
}

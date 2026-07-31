package hypersync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Index is the mutable synchronization document stored at Manifest.SyncPath.
// It points to the published manifest and records the lineage and pending
// publication state needed to advance it safely.
type Index struct {
	Version   int          `json:"version"`
	UpdatedAt time.Time    `json:"updated_at"`
	Published Identifier   `json:"published,omitempty"`
	Pending   Identifier   `json:"pending,omitempty"`
	History   []Identifier `json:"history"`
}

// Manifest is an immutable complete snapshot of managed source files.
type Manifest struct {
	Version  int             `json:"version"`
	Parent   Identifier      `json:"parent,omitempty"`
	SyncPath string          `json:"sync_path"`
	Config   *ScanConfig     `json:"config,omitempty"`
	Entries  []ManifestEntry `json:"entries"`
}

// ManifestEntry describes exact bytes stored as an immutable blob.
type ManifestEntry struct {
	Path     string     `json:"path"`
	Blob     Identifier `json:"blob"`
	Size     int64      `json:"size"`
	Mimetype string     `json:"mimetype"`
}

// ScanConfig selects locally managed source files when present.
type ScanConfig struct {
	Include     Rules `json:"include"`
	Ignore      Rules `json:"ignore"`
	MaxFileSize int64 `json:"max_file_size,omitempty"`
}

// Rules contains optional path and MIME glob constraints.
type Rules struct {
	Paths     []string `json:"paths"`
	Mimetypes []string `json:"mimetypes"`
}

const Version = 1

// Validate enforces the v1 manifest invariants.
func (manifest Manifest) Validate() error {
	if manifest.Version != Version {
		return fmt.Errorf("%w: unsupported manifest version %d", ErrInvalidState, manifest.Version)
	}
	if err := validateSyncPath(manifest.SyncPath); err != nil {
		return err
	}
	if manifest.Parent != "" {
		if err := manifest.Parent.Validate(); err != nil {
			return err
		}
	}
	if manifest.Config != nil {
		if err := manifest.Config.Validate(); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("%w: duplicate entry path %q", ErrInvalidState, entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if manifest.Config != nil && !manifest.Config.Allows(entry) {
			return fmt.Errorf("%w: entry %q violates its manifest configuration", ErrInvalidState, entry.Path)
		}
	}
	return nil
}

func (entry ManifestEntry) Validate() error {
	if err := validateRelativePath(entry.Path); err != nil {
		return err
	}
	if err := entry.Blob.Validate(); err != nil {
		return err
	}
	if entry.Size < 0 || entry.Mimetype == "" {
		return fmt.Errorf("%w: invalid entry %q", ErrInvalidState, entry.Path)
	}
	return nil
}

// Validate enforces clean and pending index shape, while leaving the ancestry
// relationship to the manifest reader.
func (index Index) Validate() error {
	if index.Version != Version || index.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: invalid index version or updated_at", ErrInvalidState)
	}
	if len(index.History) == 0 || index.History[0] == "" {
		return fmt.Errorf("%w: index history is empty", ErrInvalidState)
	}
	for _, id := range index.History {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if index.Pending == "" {
		if err := index.Published.Validate(); err != nil {
			return err
		}
		if index.History[0] != index.Published {
			return fmt.Errorf("%w: clean index history must begin with published", ErrInvalidState)
		}
		return nil
	}
	if err := index.Pending.Validate(); err != nil {
		return err
	}
	if index.Published == "" {
		if len(index.History) != 1 || index.History[0] != index.Pending {
			return fmt.Errorf("%w: initial pending index history must contain only pending", ErrInvalidState)
		}
		return nil
	}
	if err := index.Published.Validate(); err != nil {
		return err
	}
	if len(index.History) < 2 || index.History[0] != index.Pending || index.History[1] != index.Published {
		return fmt.Errorf("%w: pending index history must begin pending, published", ErrInvalidState)
	}
	return nil
}

// PendingError reports whether a valid pending index blocks publication.
func (index Index) PendingError(now time.Time) error {
	if index.Pending == "" {
		return nil
	}
	if now.Sub(index.UpdatedAt) < 300*time.Second {
		return ErrDeploymentInProgress
	}
	return ErrStalePending
}

// ParseManifest verifies exact bytes against expected before returning parsed
// state. It never rewrites the supplied bytes.
func ParseManifest(raw []byte, expected Identifier) (Manifest, error) {
	if err := expected.Validate(); err != nil {
		return Manifest{}, err
	}
	if got := NewIdentifier(raw); got != expected {
		return Manifest{}, fmt.Errorf("%w: manifest digest is %s, want %s", ErrInvalidState, got, expected)
	}

	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode manifest: %v", ErrInvalidState, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, fmt.Errorf("%w: manifest has trailing JSON values", ErrInvalidState)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// SerializeManifest serializes a newly created manifest once and returns its
// identity. Existing manifests must be retained as their original raw bytes.
func SerializeManifest(manifest Manifest) ([]byte, Identifier, error) {
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", err
	}
	return raw, NewIdentifier(raw), nil
}

// ParseIndex decodes and validates observed index bytes. Callers retain raw
// bytes for compare-and-swap; this function does not serialize them again.
func ParseIndex(raw []byte) (Index, error) {
	var index Index
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&index); err != nil {
		return Index{}, fmt.Errorf("%w: decode index: %v", ErrInvalidState, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Index{}, fmt.Errorf("%w: index has trailing JSON values", ErrInvalidState)
	}
	if err := index.Validate(); err != nil {
		return Index{}, err
	}
	return index, nil
}

func validateSyncPath(value string) error {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") || value == "/" {
		return fmt.Errorf("%w: invalid sync_path %q", ErrInvalidState, value)
	}
	for _, part := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: invalid sync_path %q", ErrInvalidState, value)
		}
	}
	return nil
}

func validateRelativePath(value string) error {
	if value == "" || strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("%w: invalid relative path %q", ErrInvalidState, value)
	}
	return nil
}

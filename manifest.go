package hsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Index is the mutable synchronization document stored at Manifest.IndexPath.
// It points to the published manifest and records the lineage and pending
// publication state needed to advance it safely.
type Index struct {
	Version   int          `json:"version"`
	UpdatedAt time.Time    `json:"updated_at"`
	Published Identifier   `json:"published,omitempty"`
	Pending   Identifier   `json:"pending,omitempty"`
	History   []Identifier `json:"history"`
}

// Manifest records the complete managed file inventory for one repository
// revision. It can be changed until Seal records its exact serialized
// representation and identifier.
type Manifest struct {
	version   int
	parent    Identifier
	indexPath string
	entries   []ManifestEntry
	rawBytes  []byte
	id        Identifier
}

// ManifestEntry describes exact bytes stored as an immutable blob.
type ManifestEntry struct {
	Path     string     `json:"path"`
	Blob     Identifier `json:"blob"`
	Size     int64      `json:"size"`
	Mimetype string     `json:"mimetype"`
}

// ManifestVersion is the version of the serialized manifest and index format.
const ManifestVersion = 1

// NewManifest creates an unsealed manifest. It copies entries but deliberately
// does not validate: callers may construct an invalid value for validation tests.
func NewManifest(indexPath string, parent Identifier, entries []ManifestEntry) Manifest {
	return Manifest{version: ManifestVersion, parent: parent, indexPath: indexPath, entries: append([]ManifestEntry(nil), entries...)}
}

// NewSealedManifest creates and immediately seals a complete manifest.
func NewSealedManifest(indexPath string, parent Identifier, entries []ManifestEntry) (Manifest, error) {
	manifest := NewManifest(indexPath, parent, entries)
	if err := manifest.Seal(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// MarshalJSON encodes the v1 manifest wire format. It does not seal the
// manifest; Seal is responsible for retaining the exact encoded bytes and ID.
func (manifest Manifest) MarshalJSON() ([]byte, error) {
	version, err := json.Marshal(manifest.version)
	if err != nil {
		return nil, err
	}
	indexPath, err := json.Marshal(manifest.indexPath)
	if err != nil {
		return nil, err
	}
	entries, err := json.Marshal(manifest.entries)
	if err != nil {
		return nil, err
	}
	var raw bytes.Buffer
	raw.WriteString(`{"version":`)
	raw.Write(version)
	if manifest.parent != "" {
		parent, err := json.Marshal(manifest.parent)
		if err != nil {
			return nil, err
		}
		raw.WriteString(`,"parent":`)
		raw.Write(parent)
	}
	raw.WriteString(`,"index_path":`)
	raw.Write(indexPath)
	raw.WriteString(`,"entries":`)
	raw.Write(entries)
	raw.WriteByte('}')
	return raw.Bytes(), nil
}

// UnmarshalJSON decodes the v1 manifest wire format into an unsealed
// manifest. ParseManifest should be used when exact bytes and identity matter.
func (manifest *Manifest) UnmarshalJSON(raw []byte) error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var version int
	var parent Identifier
	var indexPath string
	var entries []ManifestEntry
	for name, value := range fields {
		switch name {
		case "version":
			if err := json.Unmarshal(value, &version); err != nil {
				return err
			}
		case "parent":
			if err := json.Unmarshal(value, &parent); err != nil {
				return err
			}
		case "index_path":
			if err := json.Unmarshal(value, &indexPath); err != nil {
				return err
			}
		case "entries":
			if err := json.Unmarshal(value, &entries); err != nil {
				return err
			}
		case "config": // accepted legacy field
		default:
			return fmt.Errorf("unknown manifest field %q", name)
		}
	}
	*manifest = Manifest{version: version, parent: parent, indexPath: indexPath, entries: append([]ManifestEntry(nil), entries...)}
	return nil
}

func (manifest Manifest) Version() int { return manifest.version }

func (manifest Manifest) ID() Identifier { return manifest.id }

// Filename returns this sealed manifest's immutable storage filename.
func (manifest Manifest) Filename() (string, error) {
	if !manifest.Sealed() {
		return "", fmt.Errorf("%w: manifest is not sealed", ErrInvalidState)
	}
	return manifestFilename(manifest.id), nil
}

// Sealed reports whether this manifest retains verified serialized bytes and ID.
func (manifest Manifest) Sealed() bool { return manifest.rawBytes != nil && manifest.id != "" }

func (manifest Manifest) Parent() Identifier { return manifest.parent }

func (manifest Manifest) IndexPath() string { return manifest.indexPath }

func (manifest Manifest) Entries() []ManifestEntry {
	return append([]ManifestEntry(nil), manifest.entries...)
}

func (manifest Manifest) Raw() []byte { return append([]byte(nil), manifest.rawBytes...) }

// SetParent updates an unsealed manifest's parent.
func (manifest *Manifest) SetParent(parent Identifier) error {
	if err := manifest.writable(); err != nil {
		return err
	}
	manifest.parent = parent
	return nil
}

// SetIndexPath updates an unsealed manifest's index path.
func (manifest *Manifest) SetIndexPath(indexPath string) error {
	if err := manifest.writable(); err != nil {
		return err
	}
	manifest.indexPath = indexPath
	return nil
}

// SetEntries updates an unsealed manifest's entries, retaining its own copy.
func (manifest *Manifest) SetEntries(entries []ManifestEntry) error {
	if err := manifest.writable(); err != nil {
		return err
	}
	manifest.entries = append([]ManifestEntry(nil), entries...)
	return nil
}

// Seal validates and records the exact serialized form of this manifest.
func (manifest *Manifest) Seal() error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	if manifest.rawBytes != nil {
		return nil
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(*manifest)
	if err != nil {
		return err
	}
	manifest.rawBytes = append([]byte(nil), raw...)
	manifest.id = NewIdentifier(raw)
	return nil
}

// Validate enforces the v1 manifest invariants.
func (manifest Manifest) Validate() error {
	if manifest.version != ManifestVersion {
		return fmt.Errorf("%w: unsupported manifest version %d", ErrInvalidState, manifest.version)
	}
	if err := validateIndexPath(manifest.indexPath); err != nil {
		return err
	}
	if manifest.parent != "" {
		if err := manifest.parent.Validate(); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(manifest.entries))
	for _, entry := range manifest.entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("%w: duplicate entry path %q", ErrInvalidState, entry.Path)
		}
		seen[entry.Path] = struct{}{}
	}
	return nil
}

func (manifest *Manifest) writable() error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	if manifest.rawBytes != nil {
		return fmt.Errorf("%w: manifest is sealed", ErrInvalidState)
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
	if index.Version != ManifestVersion || index.UpdatedAt.IsZero() {
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
	manifest.rawBytes = append([]byte(nil), raw...)
	manifest.id = expected
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
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

func validateIndexPath(value string) error {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") || value == "/" {
		return fmt.Errorf("%w: invalid index path %q", ErrInvalidState, value)
	}
	for _, part := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: invalid index path %q", ErrInvalidState, value)
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

func manifestFilename(id Identifier) string { return id.Filename() + ".json" }

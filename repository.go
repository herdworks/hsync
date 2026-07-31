package hypersync

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/herdworks/hyperfiles-go/mimetypes"
)

// Repository identifies the managed source tree and its one persistent local
// manifest. Snapshot is used only when a manifest intentionally has no scanner
// configuration.
type Repository struct {
	Root         string
	ManifestPath string
	Snapshot     *Snapshot
}

// Snapshot contains locally discovered managed entries and their exact blob
// bytes. Callers write these bytes without re-reading or reserializing them.
type Snapshot struct {
	Entries map[string]ManifestEntry
	Blobs   map[Identifier][]byte
}

// Initialize writes this repository's new immutable local base manifest. It
// refuses to replace an existing base, because editing or silently replacing
// that base would make reconciliation history ambiguous.
func (repository Repository) Initialize(syncPath string, config ScanConfig) ([]byte, Identifier, error) {
	manifest := Manifest{Version: Version, SyncPath: syncPath, Config: &config, Entries: []ManifestEntry{}}
	raw, id, err := SerializeManifest(manifest)
	if err != nil {
		return nil, "", err
	}
	manifestPath := filepath.Clean(repository.ManifestPath)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("create manifest directory: %w", err)
	}
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("initialize %s: %w", manifestPath, err)
	}
	defer file.Close()
	if _, err := file.Write(raw); err != nil {
		return nil, "", fmt.Errorf("write initial manifest: %w", err)
	}
	return raw, id, nil
}

// LoadManifest reads and validates this repository's persistent immutable local
// base.
func (repository Repository) LoadManifest() ([]byte, Identifier, Manifest, error) {
	raw, err := os.ReadFile(repository.ManifestPath)
	if err != nil {
		return nil, "", Manifest{}, err
	}
	id := NewIdentifier(raw)
	manifest, err := ParseManifest(raw, id)
	if err != nil {
		return nil, "", Manifest{}, err
	}
	return raw, id, manifest, nil
}

// Finalize validates the deployed changeset against the clean live index and
// atomically advances this repository's only persistent local state. It must
// be called only after a deployment has successfully uploaded its final index.
func (repository Repository) Finalize(changeset Changeset, liveIndexRaw []byte, now time.Time) error {
	if _, err := ParseManifest(changeset.Manifest, changeset.ManifestID); err != nil {
		return err
	}
	index, err := ParseIndex(liveIndexRaw)
	if err != nil {
		return err
	}
	if err := index.PendingError(now); err != nil {
		return err
	}
	if index.Published != changeset.ManifestID {
		return fmt.Errorf("%w: live index publishes %s, not changeset %s", ErrManualResolution, index.Published, changeset.ManifestID)
	}
	manifestPath := filepath.Clean(repository.ManifestPath)
	temporary, err := os.CreateTemp(filepath.Dir(manifestPath), ".manifest-*.json")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(changeset.Manifest); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, manifestPath)
}

// Reconcile merges this repository's local workspace against its immutable
// base and the authoritative live manifest. The returned changeset is always a
// child of live and has not yet been given deployment-index bytes.
func (repository Repository) Reconcile(base, live Manifest, liveID Identifier, workspace Snapshot) (Changeset, error) {
	if err := base.Validate(); err != nil {
		return Changeset{}, err
	}
	if err := live.Validate(); err != nil {
		return Changeset{}, err
	}
	if err := liveID.Validate(); err != nil {
		return Changeset{}, err
	}
	if base.SyncPath != live.SyncPath {
		return Changeset{}, fmt.Errorf("%w: base and live sync_path differ", ErrManualResolution)
	}
	if !sameConfig(base.Config, live.Config) {
		return Changeset{}, fmt.Errorf("%w: base and live scanner configuration differ", ErrManualResolution)
	}

	baseEntries := entriesByPath(base.Entries)
	liveEntries := entriesByPath(live.Entries)
	paths := make(map[string]struct{}, len(baseEntries)+len(liveEntries)+len(workspace.Entries))
	for name := range baseEntries {
		paths[name] = struct{}{}
	}
	for name := range liveEntries {
		paths[name] = struct{}{}
	}
	for name := range workspace.Entries {
		paths[name] = struct{}{}
	}

	entries := make([]ManifestEntry, 0, len(paths))
	for name := range paths {
		baseEntry, baseOK := baseEntries[name]
		workspaceEntry, workspaceOK := workspace.Entries[name]
		liveEntry, liveOK := liveEntries[name]
		selected, ok := mergeEntry(baseEntry, baseOK, workspaceEntry, workspaceOK, liveEntry, liveOK)
		if ok {
			entries = append(entries, selected)
		}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })

	manifest := Manifest{Version: Version, Parent: liveID, SyncPath: live.SyncPath, Config: live.Config, Entries: entries}
	if err := manifest.Validate(); err != nil {
		return Changeset{}, err
	}
	liveBlobs := make(map[Identifier]struct{}, len(live.Entries))
	for _, entry := range live.Entries {
		liveBlobs[entry.Blob] = struct{}{}
	}
	blobs := make(map[Identifier][]byte)
	for _, entry := range entries {
		if _, ok := liveBlobs[entry.Blob]; ok {
			continue
		}
		contents, ok := workspace.Blobs[entry.Blob]
		if !ok {
			return Changeset{}, fmt.Errorf("%w: missing workspace bytes for required blob %s", ErrInvalidState, entry.Blob)
		}
		blobs[entry.Blob] = contents
	}
	raw, id, err := SerializeManifest(manifest)
	if err != nil {
		return Changeset{}, err
	}
	return Changeset{ManifestID: id, Manifest: raw, Blobs: blobs, syncPath: manifest.SyncPath, manifest: manifest}, nil
}

func (repository Repository) workspaceSnapshot(config *ScanConfig, payload Payload) (Snapshot, error) {
	if config != nil {
		excluded, err := scanExclusions(repository.Root, repository.ManifestPath, payload.Root)
		if err != nil {
			return Snapshot{}, err
		}
		return scan(repository.Root, *config, excluded)
	}
	if repository.Snapshot == nil {
		return Snapshot{}, fmt.Errorf("%w: manifest without scanner configuration requires caller snapshot", ErrInvalidState)
	}
	return *repository.Snapshot, nil
}

// Allows reports whether entry meets the include, ignore, and size rules.
func (config ScanConfig) Allows(entry ManifestEntry) bool {
	base, err := mimetypes.Base(entry.Mimetype)
	if err != nil {
		return false
	}
	if config.MaxFileSize > 0 && entry.Size > config.MaxFileSize {
		return false
	}
	if len(config.Include.Paths) > 0 && !matchesAny(config.Include.Paths, entry.Path) {
		return false
	}
	if len(config.Include.Mimetypes) > 0 && !matchesAny(config.Include.Mimetypes, base) {
		return false
	}
	return !matchesAny(config.Ignore.Paths, entry.Path) && !matchesAny(config.Ignore.Mimetypes, base)
}

func (config ScanConfig) Validate() error {
	if len(config.Include.Paths) == 0 && len(config.Include.Mimetypes) == 0 {
		return fmt.Errorf("%w: scan configuration requires an include rule", ErrInvalidState)
	}
	if config.MaxFileSize < 0 {
		return fmt.Errorf("%w: max_file_size must not be negative", ErrInvalidState)
	}
	for _, rules := range []Rules{config.Include, config.Ignore} {
		for _, pattern := range rules.Paths {
			if err := validateGlob(pattern); err != nil {
				return fmt.Errorf("%w: invalid path glob %q: %v", ErrInvalidState, pattern, err)
			}
		}
		for _, pattern := range rules.Mimetypes {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("%w: invalid MIME glob %q: %v", ErrInvalidState, pattern, err)
			}
		}
	}
	return nil
}

func changesetMatchesLive(changeset Changeset, live Manifest) bool {
	return changeset.manifest.Version == live.Version &&
		changeset.manifest.SyncPath == live.SyncPath &&
		sameConfig(changeset.manifest.Config, live.Config) &&
		reflect.DeepEqual(changeset.manifest.Entries, live.Entries)
}

func mergeEntry(base ManifestEntry, baseOK bool, workspace ManifestEntry, workspaceOK bool, live ManifestEntry, liveOK bool) (ManifestEntry, bool) {
	if entriesEqual(workspace, workspaceOK, base, baseOK) {
		return live, liveOK
	}
	if entriesEqual(live, liveOK, base, baseOK) || entriesEqual(workspace, workspaceOK, live, liveOK) {
		return workspace, workspaceOK
	}
	return workspace, workspaceOK
}

func entriesEqual(left ManifestEntry, leftOK bool, right ManifestEntry, rightOK bool) bool {
	return leftOK == rightOK && (!leftOK || left.Blob == right.Blob)
}

func entriesByPath(entries []ManifestEntry) map[string]ManifestEntry {
	result := make(map[string]ManifestEntry, len(entries))
	for _, entry := range entries {
		result[entry.Path] = entry
	}
	return result
}

func sameConfig(left, right *ScanConfig) bool {
	if left == nil || right == nil {
		return left == right
	}
	return reflect.DeepEqual(*left, *right)
}

// Scan walks workspace and discovers files selected by config. It does not
// follow symlinks.
func Scan(workspace string, config ScanConfig) (Snapshot, error) {
	return scan(workspace, config, nil)
}

// IsAlreadyInitialized reports whether err came from an existing local base.
func IsAlreadyInitialized(err error) bool { return errors.Is(err, os.ErrExist) }

func scan(workspace string, config ScanConfig, excluded map[string]bool) (Snapshot, error) {
	if err := config.Validate(); err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Entries: make(map[string]ManifestEntry), Blobs: make(map[Identifier][]byte)}
	err := filepath.WalkDir(workspace, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := ""
		if filename != workspace {
			resolved, err := filepath.Rel(workspace, filename)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(resolved)
			if excluded[relative] {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		contents, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		mime, err := mimetypes.Detect(relative, bytes.NewReader(contents))
		if err != nil {
			return fmt.Errorf("detect %s: %w", relative, err)
		}
		candidate := ManifestEntry{Path: relative, Blob: NewIdentifier(contents), Size: int64(len(contents)), Mimetype: mime}
		if !config.Allows(candidate) {
			return nil
		}
		snapshot.Entries[relative] = candidate
		snapshot.Blobs[candidate.Blob] = contents
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("scan workspace: %w", err)
	}
	return snapshot, nil
}

func scanExclusions(sourceRoot string, paths ...string) (map[string]bool, error) {
	root, err := filepath.Abs(filepath.Clean(sourceRoot))
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(paths))
	for _, value := range paths {
		candidate, err := filepath.Abs(filepath.Clean(value))
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return nil, err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil, fmt.Errorf("%w: generated/state path must not equal source root", ErrInvalidState)
		}
		if relative == ".." || strings.HasPrefix(relative, "../") {
			continue
		}
		excluded[relative] = true
	}
	return excluded, nil
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if globMatch(pattern, value) {
			return true
		}
	}
	return false
}

func validateGlob(pattern string) error {
	for _, part := range strings.Split(pattern, "/") {
		if part == "**" {
			continue
		}
		if _, err := path.Match(part, ""); err != nil {
			return err
		}
	}
	return nil
}

func globMatch(pattern, value string) bool {
	patterns := strings.Split(pattern, "/")
	values := strings.Split(value, "/")
	var match func(int, int) bool
	match = func(patternIndex, valueIndex int) bool {
		if patternIndex == len(patterns) {
			return valueIndex == len(values)
		}
		if patterns[patternIndex] == "**" {
			for index := valueIndex; index <= len(values); index++ {
				if match(patternIndex+1, index) {
					return true
				}
			}
			return false
		}
		if valueIndex == len(values) {
			return false
		}
		ok, err := path.Match(patterns[patternIndex], values[valueIndex])
		return err == nil && ok && match(patternIndex+1, valueIndex+1)
	}
	return match(0, 0)
}

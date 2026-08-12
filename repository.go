package hsync

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

	"github.com/herdworks/hypermedia/mimetypes"
)

// Repository identifies the managed source tree and its one persistent local
// manifest.
type Repository struct {
	Root   string
	Config *SyncConfig
}

const LocalManifestPath = ".hsync/manifest.json"

var reservedPaths = []string{".git/**", ".hsync/**", ".deployment/**"}

// Snapshot contains locally discovered managed entries and their exact blob
// bytes. Callers write these bytes without re-reading or reserializing them.
type Snapshot struct {
	Entries map[string]ManifestEntry
	Blobs   map[Identifier][]byte
}

type gitignoreRules struct{ expressions PathExpressions }

func (snapshot *Snapshot) add(entry ManifestEntry, contents []byte) {
	if snapshot.Entries == nil {
		snapshot.Entries = make(map[string]ManifestEntry)
	}
	if snapshot.Blobs == nil {
		snapshot.Blobs = make(map[Identifier][]byte)
	}
	snapshot.Entries[entry.Path] = entry
	snapshot.Blobs[entry.Blob] = contents
}

// ChangeKind describes how a selected workspace file differs from its base.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

// Change describes one selected workspace file that differs from its base.
type Change struct {
	Path string
	Kind ChangeKind
}

// RepositoryStatus describes selected workspace changes from a repository's
// current immutable base manifest.
type RepositoryStatus struct {
	Base    Identifier
	Changes []Change
}

// Initialize writes this repository's new immutable local base manifest. It
// refuses to replace an existing base, because editing or silently replacing
// that base would make reconciliation history ambiguous.
func (repository Repository) Initialize(syncPath string) ([]byte, Identifier, error) {
	manifest := Manifest{Version: ManifestVersion, SyncPath: syncPath, Entries: []ManifestEntry{}}
	raw, id, err := SerializeManifest(manifest)
	if err != nil {
		return nil, "", err
	}
	manifestPath, err := repository.manifestTarget(true)
	if err != nil {
		return nil, "", err
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
	if repository.Root == "" {
		return nil, "", Manifest{}, fmt.Errorf("%w: repository root is required for local reads", ErrInvalidState)
	}
	manifestPath, err := repository.manifestTarget(false)
	if err != nil {
		return nil, "", Manifest{}, err
	}
	raw, err := os.ReadFile(manifestPath)
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

// Status reports selected workspace changes from this repository's current
// immutable base. It reads no remote state and does not modify local files.
func (repository Repository) Status() (RepositoryStatus, error) {
	if repository.Root == "" {
		return RepositoryStatus{}, fmt.Errorf("%w: repository root is required", ErrInvalidState)
	}
	_, baseID, base, err := repository.LoadManifest()
	if err != nil {
		return RepositoryStatus{}, err
	}
	workspace, err := repository.snapshot(&base)
	if err != nil {
		return RepositoryStatus{}, err
	}
	return RepositoryStatus{Base: baseID, Changes: compareWorkspace(base.Entries, workspace.Entries)}, nil
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
	manifestPath, err := repository.manifestTarget(true)
	if err != nil {
		return err
	}
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

func (repository Repository) manifestTarget(write bool) (string, error) {
	if repository.Root == "" {
		return "", fmt.Errorf("%w: repository root is required for local manifest", ErrInvalidState)
	}
	if write {
		return safeWriteTarget(repository.Root, LocalManifestPath)
	}
	return safeReadTarget(repository.Root, LocalManifestPath)
}

// Reconcile applies the lineage-based three-way divergence algorithm to each
// managed path during repository reconciliation from an immutable base to a
// verified remote live revision. Both deployment preparation and pull use this
// single file-selection implementation.
func (repository Repository) Reconcile(base Manifest, liveID Identifier, continuation Continuation, workspace Snapshot) (Changeset, error) {
	if err := base.Validate(); err != nil {
		return Changeset{}, err
	}
	if err := liveID.Validate(); err != nil {
		return Changeset{}, err
	}
	if len(continuation.Revisions) == 0 {
		return Changeset{}, fmt.Errorf("%w: reconciliation continuation is required", ErrInvalidState)
	}
	live := continuation.Revisions[0]
	if err := live.Validate(); err != nil {
		return Changeset{}, err
	}
	if base.SyncPath != live.SyncPath {
		return Changeset{}, fmt.Errorf("%w: base and live sync_path differ", ErrManualResolution)
	}
	for _, revision := range continuation.Revisions[1:] {
		if err := revision.Validate(); err != nil {
			return Changeset{}, err
		}
		if revision.SyncPath != live.SyncPath {
			return Changeset{}, fmt.Errorf("%w: continuation sync_path differs", ErrManualResolution)
		}
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
	for _, revision := range continuation.Revisions {
		for name := range entriesByPath(revision.Entries) {
			paths[name] = struct{}{}
		}
	}

	entries := make([]ManifestEntry, 0, len(paths))
	for name := range paths {
		baseEntry, baseOK := baseEntries[name]
		workspaceEntry, workspaceOK := workspace.Entries[name]
		liveEntry, liveOK := liveEntries[name]
		selected, ok := mergeEntry(name, baseEntry, baseOK, workspaceEntry, workspaceOK, liveEntry, liveOK, continuation.Revisions)
		if ok {
			entries = append(entries, selected)
		}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })

	manifest := Manifest{Version: ManifestVersion, Parent: liveID, SyncPath: live.SyncPath, Entries: entries}
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

// snapshot assembles a repository snapshot in its canonical order: manifest
// entries when present, newly discovered included entries, then ignore and
// size filtering over the complete set. It supports manifest-only mode when
// Config is nil and discovery-only mode when manifest is nil.
func (repository Repository) snapshot(manifest *Manifest) (Snapshot, error) {
	var config SyncConfig = SyncConfig{}
	var policy SyncPolicy
	var files, discovered Snapshot
	var err error
	if manifest == nil && repository.Config == nil {
		return Snapshot{}, fmt.Errorf("%w: snapshot requires a manifest or sync configuration", ErrInvalidState)
	}
	if repository.Config != nil {
		config = *repository.Config
	}
	if policy, err = config.Policy(reservedPaths...); err != nil {
		return Snapshot{}, err
	}
	if manifest == nil && !policy.includePaths.hasPositive() {
		return Snapshot{}, fmt.Errorf("%w: initial synchronization requires a positive sync.include path rule", ErrInvalidState)
	}
	gitignore, err := repository.gitignore()
	if err != nil {
		return Snapshot{}, err
	}
	if manifest != nil {
		tracked, err := repository.entries(manifest.Entries)
		if err != nil {
			return Snapshot{}, err
		}
		for _, entry := range tracked.Entries {
			if policy.Allows(entry) && gitignore.Allows(entry.Path) {
				files.add(entry, tracked.Blobs[entry.Blob])
			}
		}
	}
	if !policy.ScanningEnabled() {
		return files, nil // early return
	}
	if discovered, err = walk(repository.Root); err != nil {
		return Snapshot{}, err
	}
	for _, entry := range discovered.Entries {
		if policy.Includes(entry) && policy.Allows(entry) && gitignore.Allows(entry.Path) {
			files.add(entry, discovered.Blobs[entry.Blob])
		}
	}
	return files, nil
}

func (rules gitignoreRules) Allows(value string) bool {
	matched, ignored := rules.expressions.match(value)
	return !matched || !ignored
}

func (repository Repository) gitignore() (gitignoreRules, error) {
	filename, err := safeReadTarget(repository.Root, ".gitignore")
	if errors.Is(err, os.ErrNotExist) {
		return gitignoreRules{}, nil
	}
	if err != nil {
		return gitignoreRules{}, err
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return gitignoreRules{}, err
	}
	rules, err := parseGitIgnore(string(raw))
	if err != nil {
		return gitignoreRules{}, fmt.Errorf("parse .gitignore: %w", err)
	}
	return gitignoreRules{expressions: rules}, nil
}

func (repository Repository) entries(manifestEntries []ManifestEntry) (Snapshot, error) {
	snapshot := Snapshot{Entries: make(map[string]ManifestEntry), Blobs: make(map[Identifier][]byte)}
	for _, base := range manifestEntries {
		filename := filepath.Join(repository.Root, filepath.FromSlash(base.Path))
		entry, contents, ok, err := readEntry(filename, base.Path)
		if err != nil {
			return Snapshot{}, err
		}
		if !ok {
			continue
		}
		snapshot.add(entry, contents)
	}
	return snapshot, nil
}

func changesetMatchesLive(changeset Changeset, live Manifest) bool {
	return changeset.manifest.Version == live.Version &&
		changeset.manifest.SyncPath == live.SyncPath &&
		reflect.DeepEqual(changeset.manifest.Entries, live.Entries)
}

func mergeEntry(name string, base ManifestEntry, baseOK bool, workspace ManifestEntry, workspaceOK bool, live ManifestEntry, liveOK bool, history []Manifest) (ManifestEntry, bool) {
	if entriesEqual(workspace, workspaceOK, base, baseOK) {
		return live, liveOK
	}
	if entriesEqual(workspace, workspaceOK, live, liveOK) {
		return workspace, workspaceOK
	}
	for _, revision := range history {
		entry, ok := entriesByPath(revision.Entries)[name]
		if entriesEqual(workspace, workspaceOK, entry, ok) {
			return live, liveOK
		}
	}
	if entriesEqual(live, liveOK, base, baseOK) {
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

func compareWorkspace(base []ManifestEntry, workspace map[string]ManifestEntry) []Change {
	baseEntries := entriesByPath(base)
	paths := make(map[string]struct{}, len(baseEntries)+len(workspace))
	for name := range baseEntries {
		paths[name] = struct{}{}
	}
	for name := range workspace {
		paths[name] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	changes := make([]Change, 0, len(ordered))
	for _, name := range ordered {
		baseEntry, baseOK := baseEntries[name]
		workspaceEntry, workspaceOK := workspace[name]
		switch {
		case !baseOK && workspaceOK:
			changes = append(changes, Change{Path: name, Kind: ChangeAdded})
		case baseOK && !workspaceOK:
			changes = append(changes, Change{Path: name, Kind: ChangeDeleted})
		case !entriesEqual(baseEntry, baseOK, workspaceEntry, workspaceOK):
			changes = append(changes, Change{Path: name, Kind: ChangeModified})
		}
	}
	return changes
}

func walk(workspace string) (Snapshot, error) {
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
		candidate, contents, ok, err := readEntry(filename, relative)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		snapshot.add(candidate, contents)
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("scan workspace: %w", err)
	}
	return snapshot, nil
}

// readEntry returns one regular, non-symlink workspace file. It is shared by
// manifest reading and discovery so both build entries from identical bytes.
func readEntry(filename, relative string) (ManifestEntry, []byte, bool, error) {
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return ManifestEntry{}, nil, false, nil
	}
	if err != nil {
		return ManifestEntry{}, nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ManifestEntry{}, nil, false, nil
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		return ManifestEntry{}, nil, false, err
	}
	mime, err := mimetypes.Detect(relative, bytes.NewReader(contents))
	if err != nil {
		return ManifestEntry{}, nil, false, fmt.Errorf("detect %s: %w", relative, err)
	}
	entry := ManifestEntry{Path: relative, Blob: NewIdentifier(contents), Size: int64(len(contents)), Mimetype: mime}
	return entry, contents, true, nil
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

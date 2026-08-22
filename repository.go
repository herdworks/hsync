package hsync

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/herdworks/hypermedia/mimetypes"
)

// Repository identifies the managed source tree, its persistent base, and its
// ephemeral local workspace state.
type Repository struct {
	Root   string
	Config *SyncConfig
	Base   Manifest
	Local  Local
}

const (
	LocalManifestPath = ".hsync/manifest.json"
	stagedFilesPath   = "files"
)

var reservedPaths = []string{".git/**", ".hsync/**", ".deployment/**"}

// Local contains the current workspace scan. Entries and blobs form the
// effective synchronization set; ignored and limited entries are diagnostics.
type Local struct {
	entries map[string]ManifestEntry
	ignored map[string]ManifestEntry
	limited map[string]ManifestEntry
	Blobs   map[Identifier][]byte
}

type gitignoreRules struct{ expressions PathExpressions }

func (local Local) Entries() []ManifestEntry {
	return sortedEntries(local.entries)
}

// IgnoredEntries returns ignored workspace entries in path order.
func (local Local) IgnoredEntries() []ManifestEntry {
	return sortedEntries(local.ignored)
}

// LimitedEntries returns size-limited workspace entries in path order.
func (local Local) LimitedEntries() []ManifestEntry {
	return sortedEntries(local.limited)
}

func (local *Local) add(entry ManifestEntry, contents []byte) {
	if local.entries == nil {
		local.entries = make(map[string]ManifestEntry)
	}
	if local.Blobs == nil {
		local.Blobs = make(map[Identifier][]byte)
	}
	local.entries[entry.Path] = entry
	local.Blobs[entry.Blob] = contents
}

func (local *Local) ignore(entry ManifestEntry) {
	if local.ignored == nil {
		local.ignored = make(map[string]ManifestEntry)
	}
	local.ignored[entry.Path] = entry
}

func (local *Local) limit(entry ManifestEntry) {
	if local.limited == nil {
		local.limited = make(map[string]ManifestEntry)
	}
	local.limited[entry.Path] = entry
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
	Ignored []ManifestEntry
	Limited []ManifestEntry
}

// Initialize writes this repository's new immutable local base manifest. It
// refuses to replace an existing base, because editing or silently replacing
// that base would make reconciliation history ambiguous.
func (repository *Repository) Initialize(indexPath string) (Manifest, error) {
	manifest, err := NewSealedManifest(indexPath, "", nil)
	if err != nil {
		return Manifest{}, err
	}
	manifestPath, err := repository.manifestTarget(true)
	if err != nil {
		return Manifest{}, err
	}
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Manifest{}, fmt.Errorf("initialize %s: %w", manifestPath, err)
	}
	if _, err := file.Write(manifest.Raw()); err != nil {
		file.Close()
		return Manifest{}, fmt.Errorf("write initial manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close initial manifest: %w", err)
	}
	repository.Base = manifest
	repository.Local = Local{}
	return manifest, nil
}

// LoadBase loads this repository's persistent base when it is not already cached.
func (repository *Repository) LoadBase() error {
	if repository.Base.Sealed() {
		return nil
	}
	_, err := repository.RefreshBase()
	return err
}

// RefreshBase reads and validates this repository's current persistent base.
// It invalidates any cached local workspace state.
func (repository *Repository) RefreshBase() (Manifest, error) {
	if repository.Root == "" {
		return Manifest{}, fmt.Errorf("%w: repository root is required for local reads", ErrInvalidState)
	}
	manifestPath, err := repository.manifestTarget(false)
	if err != nil {
		return Manifest{}, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	id := NewIdentifier(raw)
	manifest, err := ParseManifest(raw, id)
	if err != nil {
		return Manifest{}, err
	}
	repository.Base = manifest
	repository.Local = Local{}
	return repository.Base, nil
}

// Snapshot scans the selected workspace files using this repository's current
// base when one is available, replacing its ephemeral Local state.
func (repository *Repository) Snapshot() error {
	var base *Manifest
	if repository.Base.Sealed() {
		base = &repository.Base
	}
	local, err := repository.scanLocal(base)
	if err != nil {
		return err
	}
	repository.Local = local
	return nil
}

// Status reports selected workspace changes from this repository's current
// immutable base. Without a local base, it reports selected files as added. It
// reads no remote state and does not modify local files.
func (repository *Repository) Status() (RepositoryStatus, error) {
	if repository.Root == "" {
		return RepositoryStatus{}, fmt.Errorf("%w: repository root is required", ErrInvalidState)
	}
	base, err := repository.RefreshBase()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return RepositoryStatus{}, err
	}
	if err := repository.Snapshot(); err != nil {
		return RepositoryStatus{}, err
	}
	return RepositoryStatus{
		Base:    base.ID(),
		Changes: compareWorkspace(base, repository.Local),
		Ignored: repository.Local.IgnoredEntries(),
		Limited: repository.Local.LimitedEntries(),
	}, nil
}

// StatusWithConfig reports workspace changes using config.
func StatusWithConfig(config RepositoryConfig) (RepositoryStatus, error) {
	if config.Sync == nil {
		return RepositoryStatus{}, fmt.Errorf("status requires sync configuration")
	}
	repository := Repository{Root: config.Directory(), Config: config.Sync}
	return repository.Status()
}

// Finalize validates the deployed changeset against the clean head index and
// atomically advances this repository's only persistent local state. It must
// be called only after a deployment has successfully uploaded its final index.
func (repository *Repository) Finalize(changeset Changeset, headIndexRaw []byte, now time.Time) error {
	if !changeset.Manifest.Sealed() {
		return fmt.Errorf("%w: changeset manifest is not sealed", ErrInvalidState)
	}
	manifestID, manifestRaw := changeset.Manifest.ID(), changeset.Manifest.Raw()
	index, err := ParseIndex(headIndexRaw)
	if err != nil {
		return err
	}
	if err := index.PendingError(now); err != nil {
		return err
	}
	if index.Published != manifestID {
		return fmt.Errorf("%w: head index publishes %s, not changeset %s", ErrManualResolution, index.Published, manifestID)
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
	if _, err := temporary.Write(manifestRaw); err != nil {
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
	if err := os.Rename(temporaryName, manifestPath); err != nil {
		return err
	}
	repository.Base = changeset.Manifest
	repository.Local = Local{}
	return nil
}

// commit promotes a complete staged repository from tempDir into this repository.
func (repository *Repository) commit(tempDir string) error {
	manifestRaw, err := os.ReadFile(filepath.Join(tempDir, "manifest.json"))
	if err != nil {
		return err
	}
	manifest, err := ParseManifest(manifestRaw, NewIdentifier(manifestRaw))
	if err != nil {
		return err
	}
	files := filepath.Join(tempDir, stagedFilesPath)
	entries, err := os.ReadDir(files)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(files, entry.Name()), filepath.Join(repository.Root, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.Rename(filepath.Join(tempDir, "manifest.json"), filepath.Join(repository.Root, LocalManifestPath)); err != nil {
		return err
	}
	repository.Base = manifest
	repository.Local = Local{}
	return nil
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

func (repository Repository) exists() (bool, error) {
	_, err := os.Stat(repository.Root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (repository Repository) isDirectory() (bool, error) {
	info, err := os.Stat(repository.Root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func (repository Repository) isEmpty() (bool, error) {
	entries, err := os.ReadDir(repository.Root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() != ".hsync" {
			return false, nil
		}
	}
	return true, nil
}

// prepare creates a named temporary workspace at .hsync/tmp/<id> below this
// repository's root. It creates the repository root when absent, creates each
// requested relative subdirectory, and refuses to reuse an active workspace.
func (repository Repository) prepare(id string, directories ...string) (string, error) {
	if repository.Root == "" || id == "" || filepath.Base(id) != id {
		return "", fmt.Errorf("repository root and operation ID are required")
	}
	if err := os.MkdirAll(repository.Root, 0o755); err != nil {
		return "", err
	}
	workspace := filepath.Join(repository.Root, ".hsync", "tmp")
	entries, err := os.ReadDir(workspace)
	if err == nil {
		if len(entries) != 0 {
			return "", fmt.Errorf("repository workspace %q has an active operation", workspace)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	temporary := filepath.Join(workspace, id)
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		return "", err
	}
	for _, directory := range directories {
		if err := validateRelativePath(directory); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Join(temporary, filepath.FromSlash(directory)), 0o755); err != nil {
			return "", err
		}
	}
	return temporary, nil
}

// scanLocal assembles the current local workspace in its canonical order:
// entries when present, newly discovered included entries, then ignore and
// size filtering over the complete set. It supports manifest-only mode when
// Config is nil and discovery-only mode when manifest is nil.
func (repository Repository) scanLocal(manifest *Manifest) (Local, error) {
	var config SyncConfig = SyncConfig{}
	var policy SyncPolicy
	var files, discovered Local
	var err error
	if manifest == nil && repository.Config == nil {
		return Local{}, fmt.Errorf("%w: local scan requires a manifest or sync configuration", ErrInvalidState)
	}
	if repository.Config != nil {
		config = *repository.Config
	}
	if policy, err = config.Policy(reservedPaths...); err != nil {
		return Local{}, err
	}
	if manifest == nil && !policy.includePaths.hasPositive() {
		return Local{}, fmt.Errorf("%w: initial synchronization requires a positive sync.include path rule", ErrInvalidState)
	}
	gitignore, err := repository.gitignore()
	if err != nil {
		return Local{}, err
	}
	if manifest != nil {
		tracked, err := repository.entries(manifest.Entries())
		if err != nil {
			return Local{}, err
		}
		for _, entry := range tracked.entries {
			if policy.Allows(entry) && gitignore.Allows(entry.Path) {
				files.add(entry, tracked.Blobs[entry.Blob])
			}
		}
	}
	if !policy.ScanningEnabled() {
		return files, nil // early return
	}
	if discovered, err = walk(repository.Root); err != nil {
		return Local{}, err
	}
	for _, entry := range discovered.entries {
		switch {
		case !policy.includes(entry):
			continue // a file cannot be ignored, limited, or tracked if it isn't first included
		case policy.ignores(entry) || !gitignore.Allows(entry.Path):
			files.ignore(entry)
		case policy.limits(entry):
			files.limit(entry)
		default:
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

func (repository Repository) entries(manifestEntries []ManifestEntry) (Local, error) {
	local := Local{entries: make(map[string]ManifestEntry), Blobs: make(map[Identifier][]byte)}
	for _, base := range manifestEntries {
		filename := filepath.Join(repository.Root, filepath.FromSlash(base.Path))
		entry, contents, ok, err := readEntry(filename, base.Path)
		if err != nil {
			return Local{}, err
		}
		if !ok {
			continue
		}
		local.add(entry, contents)
	}
	return local, nil
}

func compareWorkspace(base Manifest, local Local) []Change {
	baseEntries := entriesByPath(base)
	localEntries := entriesByPath(local)
	paths := make(map[string]struct{}, len(baseEntries)+len(localEntries))
	for name := range baseEntries {
		paths[name] = struct{}{}
	}
	for name := range localEntries {
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
		workspaceEntry, workspaceOK := localEntries[name]
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

func sortedEntries(entries map[string]ManifestEntry) []ManifestEntry {
	result := make([]ManifestEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Path < result[right].Path })
	return result
}

func walk(workspace string) (Local, error) {
	local := Local{entries: make(map[string]ManifestEntry), Blobs: make(map[Identifier][]byte)}
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
		local.add(candidate, contents)
		return nil
	})
	if err != nil {
		return Local{}, fmt.Errorf("scan workspace: %w", err)
	}
	return local, nil
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

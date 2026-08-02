package hsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/herdworks/hyperfiles-go/mimetypes"
	hyperproviders "github.com/herdworks/hyperproviders-go"
	"gopkg.in/yaml.v3"
)

// SyncConfig configures local synchronization policy. It is external to the
// immutable manifest so changing it becomes an ordinary synchronized file
// change rather than a manifest migration.
type SyncConfig struct {
	Remote       string `json:"remote,omitempty" yaml:"remote,omitempty"`
	Include      Rules  `json:"include" yaml:"include"`
	Ignore       Rules  `json:"ignore" yaml:"ignore"`
	MaxFileSize  string `json:"max_file_size,omitempty" yaml:"max_file_size,omitempty"`
	MaxFileBytes int64  `json:"max_file_bytes,omitempty" yaml:"max_file_bytes,omitempty"`
}

// Rules contains optional path and MIME glob constraints.
type Rules struct {
	Paths     []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Mimetypes []string `json:"mimetypes,omitempty" yaml:"mimetypes,omitempty"`
}

// SyncPolicy is the evaluated file-selection policy for one local snapshot.
type SyncPolicy struct {
	include  Rules
	ignore   Rules
	maxBytes int64
}

const DefaultConfigPath = "hsync.yaml"

const DefaultIndexPath = "/.hsync/index.json"

var configCandidates = []string{"hsync.yaml", "hsync.json", ".hsync.yaml", ".hsync.json"}

// ConfigPath resolves an explicit path, HSYNC_CONFIG, or one unambiguous
// configuration file in the current repository root. It never accepts
// symlinks.
func ConfigPath(explicit string) (string, error) {
	return ConfigPathAt(".", explicit)
}

// ConfigPathAt resolves an explicit path, HSYNC_CONFIG, or one unambiguous
// configuration file at root. A configuration file must be a regular file at
// the repository root; relative explicit paths are resolved from root.
func ConfigPathAt(root, explicit string) (string, error) {
	configured := explicit
	if configured == "" {
		configured = os.Getenv("HSYNC_CONFIG")
	}
	if configured != "" {
		return regularConfigPathAt(root, configured)
	}
	var found string
	for _, candidate := range configCandidates {
		filename, err := regularConfigPathAt(root, candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if found != "" {
			return "", fmt.Errorf("multiple hsync configuration files found; specify --config")
		}
		found = filename
	}
	return found, nil
}

// InitializeConfig creates a new repository-root configuration with no
// deployment providers and synchronization enabled for remote.
func InitializeConfig(filename, remote string) error {
	if filename == "" {
		filename = DefaultConfigPath
	}
	root := "."
	if filepath.IsAbs(filename) {
		root, filename = filepath.Dir(filename), filepath.Base(filename)
	}
	target, err := configTarget(root, filename)
	if err != nil {
		return err
	}
	target, err = safeWriteTarget(root, target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("hsync config already exists: %s", target)
		}
		return err
	}
	config := struct {
		Providers    hyperproviders.HostingProviders        `json:"providers" yaml:"providers"`
		Environments map[string]*hyperproviders.Environment `json:"environments" yaml:"environments"`
		Sync         *SyncConfig                            `json:"sync" yaml:"sync"`
	}{Providers: hyperproviders.HostingProviders{}, Environments: map[string]*hyperproviders.Environment{}, Sync: &SyncConfig{Remote: remote}}
	if err := config.Sync.Validate(); err != nil {
		return err
	}
	var raw []byte
	if strings.EqualFold(filepath.Ext(target), ".json") {
		raw, err = json.MarshalIndent(config, "", "  ")
	} else {
		raw, err = yaml.Marshal(config)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(raw, '\n'), 0o644)
}

// Validate checks a complete local synchronization configuration.
func (config SyncConfig) Validate() error {
	if _, err := config.RemoteURL(); err != nil {
		return err
	}
	_, err := config.Policy()
	return err
}

// RemoteURL parses and validates the configured public synchronization index.
func (config SyncConfig) RemoteURL() (*url.URL, error) {
	if config.Remote == "" {
		return nil, fmt.Errorf("sync remote is required")
	}
	remote, err := url.Parse(config.Remote)
	if err != nil {
		return nil, fmt.Errorf("sync remote is not a valid URL: %w", err)
	}
	if (remote.Scheme != "http" && remote.Scheme != "https") || remote.Host == "" {
		return nil, fmt.Errorf("sync remote must be an absolute HTTP(S) URL")
	}
	if remote.RawQuery != "" || remote.Fragment != "" || remote.RawPath != "" {
		return nil, fmt.Errorf("sync remote must not contain a query, fragment, or escaped path")
	}
	if remote.Path == "" || remote.Path == "/" {
		remote.Path = DefaultIndexPath
	}
	if err := validateSyncPath(remote.Path); err != nil {
		return nil, fmt.Errorf("sync remote path: %w", err)
	}
	return remote, nil
}

// MaxBytes returns the configured size limit without mutating configuration.
func (config SyncConfig) MaxBytes() (int64, error) {
	if config.MaxFileSize != "" && config.MaxFileBytes != 0 {
		return 0, fmt.Errorf("sync max_file_size and max_file_bytes are mutually exclusive")
	}
	if config.MaxFileSize != "" {
		return ParseSize(config.MaxFileSize)
	}
	if config.MaxFileBytes < 0 {
		return 0, fmt.Errorf("sync max_file_bytes must not be negative")
	}
	return config.MaxFileBytes, nil
}

// Policy compiles the configuration's file-selection rules. Reserved paths
// are additional ignore globs supplied by the local repository.
func (config SyncConfig) Policy(reserved ...string) (SyncPolicy, error) {
	maxBytes, err := config.MaxBytes()
	if err != nil {
		return SyncPolicy{}, fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	policy := SyncPolicy{
		include:  Rules{Paths: append([]string(nil), config.Include.Paths...), Mimetypes: append([]string(nil), config.Include.Mimetypes...)},
		ignore:   Rules{Paths: append([]string(nil), config.Ignore.Paths...), Mimetypes: append([]string(nil), config.Ignore.Mimetypes...)},
		maxBytes: maxBytes,
	}
	policy.ignore.Paths = append(policy.ignore.Paths, reserved...)
	if err := validateRules(policy.include, policy.ignore); err != nil {
		return SyncPolicy{}, err
	}
	return policy, nil
}

// Allows reports whether entry remains after the ignore and size rules are
// applied. These rules apply to both manifest entries and newly scanned files.
func (policy SyncPolicy) Allows(entry ManifestEntry) bool {
	base, err := mimetypes.Base(entry.Mimetype)
	if err != nil {
		return false
	}
	if policy.maxBytes > 0 && entry.Size > policy.maxBytes {
		return false
	}
	return !matchesAny(policy.ignore.Paths, entry.Path) && !matchesAny(policy.ignore.Mimetypes, base)
}

// Includes reports whether entry is eligible for discovery. An empty include
// selects no new files, while existing manifest entries remain eligible for
// the subsequent ignore and size rules.
func (policy SyncPolicy) Includes(entry ManifestEntry) bool {
	if !policy.ScanningEnabled() {
		return false
	}
	base, err := mimetypes.Base(entry.Mimetype)
	if err != nil {
		return false
	}
	return (len(policy.include.Paths) == 0 || matchesAny(policy.include.Paths, entry.Path)) &&
		(len(policy.include.Mimetypes) == 0 || matchesAny(policy.include.Mimetypes, base))
}

// ScanningEnabled reports whether config can discover files that are not already tracked.
func (policy SyncPolicy) ScanningEnabled() bool {
	return len(policy.include.Paths) != 0 || len(policy.include.Mimetypes) != 0
}

func validateRules(include, ignore Rules) error {
	for _, pattern := range include.Paths {
		if err := validateIncludeGlob(pattern); err != nil {
			return fmt.Errorf("%w: invalid include path glob %q: %v", ErrInvalidState, pattern, err)
		}
	}
	for _, pattern := range ignore.Paths {
		if err := validateGlob(pattern); err != nil {
			return fmt.Errorf("%w: invalid path glob %q: %v", ErrInvalidState, pattern, err)
		}
	}
	for _, rules := range []Rules{include, ignore} {
		for _, pattern := range rules.Mimetypes {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("%w: invalid MIME glob %q: %v", ErrInvalidState, pattern, err)
			}
		}
	}
	return nil
}

func regularConfigPathAt(root, filename string) (string, error) {
	target, err := configTarget(root, filename)
	if err != nil {
		return "", err
	}
	return safeReadTarget(root, target)
}

func configTarget(root, filename string) (string, error) {
	if root == "" {
		root = "."
	}
	if filepath.IsAbs(filename) {
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return "", err
		}
		filename = relative
	}
	filename = filepath.Clean(filename)
	if filepath.Dir(filename) != "." || filename == "." {
		return "", fmt.Errorf("hsync config must be at the repository root: %s", filename)
	}
	return filename, nil
}

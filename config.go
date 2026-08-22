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

	"gopkg.in/yaml.v3"

	"github.com/herdworks/htup"
	"github.com/herdworks/hypermedia/mimetypes"
)

// RepositoryConfig is the hsync-relevant subset of a repository-root project
// configuration. Unrelated top-level keys are ignored.
type RepositoryConfig struct {
	Path         string                       `json:"-" yaml:"-"`
	Providers    htup.HostingProviders        `json:"providers" yaml:"providers"`
	Environments map[string]*htup.Environment `json:"environments,omitempty" yaml:"environments,omitempty"`
	Sync         *SyncConfig                  `json:"sync,omitempty" yaml:"sync,omitempty"`
}

// ConfigManager resolves and initializes synchronization configuration for one repository.
type ConfigManager interface {
	DefaultConfigPath() string
	Read(directory, filename string) (RepositoryConfig, error)
	Initialize(directory, filename, remote string, verbose bool) error
}

// SyncConfig configures local synchronization policy. It is external to the
// immutable manifest so changing it becomes an ordinary synchronized file
// change rather than a manifest migration.
type SyncConfig struct {
	Remote       string `json:"remote,omitempty" yaml:"remote,omitempty" jsonschema:"required,format=uri,pattern=^https://[^/?#]+,description=An absolute HTTPS URL with a host. Queries fragments and escaped paths are not supported."`
	Include      Rules  `json:"include" yaml:"include"`
	Ignore       Rules  `json:"ignore" yaml:"ignore"`
	MaxFileSize  string `json:"max_file_size,omitempty" yaml:"max_file_size,omitempty"`
	MaxFileBytes int64  `json:"max_file_bytes,omitempty" yaml:"max_file_bytes,omitempty"`
}

// DefaultConfigManager manages hsync's standard file-backed configuration.
type DefaultConfigManager struct{}

// Rules contains optional path and MIME glob constraints.
type Rules struct {
	Paths     []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Mimetypes []string `json:"mimetypes,omitempty" yaml:"mimetypes,omitempty"`
}

// SyncPolicy is the evaluated file-selection policy for one local workspace.
type SyncPolicy struct {
	includePaths PathExpressions
	includeTypes []string
	ignorePaths  PathExpressions
	ignoreTypes  []string
	maxBytes     int64
}

// PathExpression is the deliberately small, gitignore-like path language
// shared by sync configuration and the repository-root .gitignore file.
type PathExpression struct {
	pattern   string
	negated   bool
	directory bool
	rooted    bool
	basename  bool
}

type PathExpressions []PathExpression

const DefaultConfigPath = "hsync.yaml"

const DefaultIndexPath = "/.hsync/index.json"

// DefaultEnvironment is the provider environment selected by hsync commands
// when a caller does not select providers or an environment explicitly.
const DefaultEnvironment = "hsync"

const DefaultMaxFileSize = "20MB"

var configCandidates = []string{"hsync.yaml", "hsync.json", ".hsync.yaml", ".hsync.json"}

// Directory returns the repository root containing this configuration.
func (config RepositoryConfig) Directory() string {
	if config.Path == "" {
		return ""
	}
	return filepath.Dir(config.Path)
}

// DefaultConfigPath returns hsync's standard configuration filename.
func (DefaultConfigManager) DefaultConfigPath() string { return DefaultConfigPath }

// Read resolves hsync's standard file-backed configuration.
func (DefaultConfigManager) Read(directory, filename string) (RepositoryConfig, error) {
	return LoadSyncConfig(directory, filename)
}

// Initialize creates hsync's standard file-backed configuration.
func (DefaultConfigManager) Initialize(directory, filename, remote string, verbose bool) error {
	return InitializeConfigAt(directory, filename, remote, verbose)
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
	if err := validateIndexPath(remote.Path); err != nil {
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
	includePaths, err := parsePathExpressions(config.Include.Paths)
	if err != nil {
		return SyncPolicy{}, fmt.Errorf("%w: invalid include path expression: %v", ErrInvalidState, err)
	}
	ignoreValues := append([]string(nil), config.Ignore.Paths...)
	ignoreValues = append(ignoreValues, reserved...)
	ignorePaths, err := parsePathExpressions(ignoreValues)
	if err != nil {
		return SyncPolicy{}, fmt.Errorf("%w: invalid ignore path expression: %v", ErrInvalidState, err)
	}
	if err := validateMimetypeExpressions(config.Include.Mimetypes); err != nil {
		return SyncPolicy{}, err
	}
	if err := validateMimetypeExpressions(config.Ignore.Mimetypes); err != nil {
		return SyncPolicy{}, err
	}
	return SyncPolicy{
		includePaths: includePaths,
		includeTypes: append([]string(nil), config.Include.Mimetypes...),
		ignorePaths:  ignorePaths,
		ignoreTypes:  append([]string(nil), config.Ignore.Mimetypes...),
		maxBytes:     maxBytes,
	}, nil
}

func (SyncConfig) JSONSchemaTitle() string {
	return "hsync configuration"
}
func (SyncConfig) JSONSchemaName() string { return "sync" }
func (SyncConfig) JSONSchemaSummary() string {
	return "Synchronization settings for a website source configuration."
}

func (SyncConfig) JSONSchemaDescription() string {
	return "Synchronization settings for a website source configuration."
}

// Allows reports whether entry remains after the ignore and size rules are
// applied. These rules apply to both manifest entries and newly scanned files.
func (policy SyncPolicy) Allows(entry ManifestEntry) bool {
	if _, err := mimetypes.Base(entry.Mimetype); err != nil {
		return false
	}
	return !policy.ignores(entry) && !policy.limits(entry)
}

func (policy SyncPolicy) ignores(entry ManifestEntry) bool {
	base, err := mimetypes.Base(entry.Mimetype)
	if err != nil {
		return false
	}
	matched, ignored := policy.ignorePaths.match(entry.Path)
	return matched && ignored || matchesAny(policy.ignoreTypes, base)
}

func (policy SyncPolicy) limits(entry ManifestEntry) bool {
	return policy.maxBytes > 0 && entry.Size > policy.maxBytes
}

// includes reports whether entry is eligible for discovery. An empty include
// selects no new files, while existing manifest entries remain eligible for
// the subsequent ignore and size rules.
func (policy SyncPolicy) includes(entry ManifestEntry) bool {
	if !policy.ScanningEnabled() {
		return false
	}
	base, err := mimetypes.Base(entry.Mimetype)
	if err != nil {
		return false
	}
	matched, included := policy.includePaths.match(entry.Path)
	pathIncluded := len(policy.includePaths) == 0 || (matched && included)
	return pathIncluded &&
		(len(policy.includeTypes) == 0 || matchesAny(policy.includeTypes, base))
}

// ScanningEnabled reports whether config can discover files that are not already tracked.
func (policy SyncPolicy) ScanningEnabled() bool {
	return policy.includePaths.hasPositive() || len(policy.includeTypes) != 0
}

func (Rules) JSONSchemaTitle() string {
	return "hsync rules"
}
func (Rules) JSONSchemaName() string    { return "sync-rules" }
func (Rules) JSONSchemaSummary() string { return "Optional synchronization path and MIME type rules." }

func (Rules) JSONSchemaDescription() string {
	return "Optional path and MIME type rules for synchronized files."
}

func (expressions PathExpressions) match(value string) (bool, bool) {
	matched, positive := false, false
	for _, expression := range expressions {
		if !expression.matches(value) {
			continue
		}
		matched = true
		positive = !expression.negated
	}
	return matched, positive
}

func (expressions PathExpressions) hasPositive() bool {
	for _, expression := range expressions {
		if !expression.negated {
			return true
		}
	}
	return false
}

// ParsePathExpression compiles one gitignore-like path expression for callers
// that need the same public path language as hsync configuration.
func ParsePathExpression(value string) (PathExpression, error) {
	return parsePathExpression(value, false)
}

// Matches reports whether this expression matches a slash-separated path.
func (expression PathExpression) Matches(value string) bool {
	return expression.matches(strings.Trim(filepath.ToSlash(value), "/"))
}

func (expression PathExpression) matches(value string) bool {
	if expression.directory {
		parts := strings.Split(value, "/")
		for end := 1; end < len(parts); end++ {
			if expression.matchesPath(strings.Join(parts[:end], "/")) {
				return true
			}
		}
		return false
	}
	return expression.matchesPath(value)
}

func (expression PathExpression) matchesPath(value string) bool {
	if expression.basename {
		for _, part := range strings.Split(value, "/") {
			matched, err := path.Match(expression.pattern, part)
			if err == nil && matched {
				return true
			}
		}
		return false
	}
	return globMatch(expression.pattern, value)
}

// LoadSyncConfig loads one explicit or discovered hsync configuration rooted at
// directory. It requires and validates the synchronization configuration.
func LoadSyncConfig(directory, filename string) (RepositoryConfig, error) {
	explicitFilename := filename != ""
	if filename == "" {
		var err error
		filename, err = DiscoverConfig(directory)
		if err != nil {
			return RepositoryConfig{}, err
		}
	} else if directory != "" && !filepath.IsAbs(filename) {
		filename = filepath.Join(directory, filename)
	}
	if filename == "" {
		return RepositoryConfig{}, ErrSyncConfigNotFound
	}
	config, _, err := ReadConfig(filename)
	if err != nil {
		return RepositoryConfig{}, err
	}
	if config.Sync == nil {
		return RepositoryConfig{}, fmt.Errorf("sync configuration is empty")
	}
	if err := config.Sync.Validate(); err != nil {
		return RepositoryConfig{}, err
	}
	if explicitFilename && directory != "" {
		provided, err := filepath.Abs(directory)
		if err != nil {
			return RepositoryConfig{}, err
		}
		configured, err := filepath.Abs(config.Directory())
		if err != nil {
			return RepositoryConfig{}, err
		}
		if provided != configured {
			return RepositoryConfig{}, fmt.Errorf("config %s belongs to %s, not %s", config.Path, configured, provided)
		}
	}
	return config, nil
}

// ConfiguredRemote returns the configured remote, or its resolved index URL.
func ConfiguredRemote(directory, configPath string, indexURL bool) (remote string, err error) {
	var config RepositoryConfig
	if config, err = LoadSyncConfig(directory, configPath); err != nil {
		return "", err
	}
	return RemoteWithConfig(config, indexURL)
}

// RemoteWithConfig returns config's configured remote, or its resolved index URL.
func RemoteWithConfig(config RepositoryConfig, indexURL bool) (remote string, err error) {
	if config.Sync == nil {
		return "", fmt.Errorf("sync configuration is empty")
	}
	if !indexURL {
		return config.Sync.Remote, nil
	}
	var resolved *url.URL
	if resolved, err = config.Sync.RemoteURL(); err != nil {
		return "", err
	}
	return resolved.String(), nil
}

// DiscoverConfig returns one unambiguous default configuration directly under
// directory. It returns an empty path when none is present.
func DiscoverConfig(directory string) (string, error) {
	var found string
	for _, candidate := range configCandidates {
		filename, err := regularConfigPathAt(directory, candidate)
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

// ReadConfig reads and parses a regular repository-root configuration file.
// It returns the exact bytes used for parsing and records the resolved path on
// the returned configuration.
func ReadConfig(filename string) (RepositoryConfig, []byte, error) {
	target, err := regularConfigPathAt(filepath.Dir(filename), filepath.Base(filename))
	if err != nil {
		return RepositoryConfig{}, nil, err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return RepositoryConfig{}, nil, err
	}
	config, err := ParseConfig(raw)
	if err != nil {
		return RepositoryConfig{}, nil, err
	}
	config.Path = target
	return config, raw, nil
}

// ParseConfig parses a repository configuration without accessing the
// filesystem. JSON is valid YAML and is accepted by the YAML parser.
func ParseConfig(raw []byte) (RepositoryConfig, error) {
	var config RepositoryConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return RepositoryConfig{}, fmt.Errorf("parse configuration: %w", err)
	}
	return config, nil
}

// InitializeConfig creates a new repository-root configuration with no
// deployment providers and synchronization enabled for remote.
func InitializeConfig(filename, remote string) error {
	return InitializeConfigAt("", filename, remote, false)
}

// InitializeConfigAt creates a new repository-root configuration in directory.
func InitializeConfigAt(directory, filename, remote string, verbose bool) error {
	var err error
	if directory == "" {
		if directory, err = os.Getwd(); err != nil {
			return err
		}
	}
	if filename == "" {
		filename = DefaultConfigPath
	}
	if filepath.IsAbs(filename) {
		directory, filename = filepath.Dir(filename), filepath.Base(filename)
	}
	target, err := configTarget(directory, filename)
	if err != nil {
		return err
	}
	target, err = safeWriteTarget(directory, target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("hsync config already exists: %s", target)
		}
		return err
	}
	config := RepositoryConfig{Providers: htup.HostingProviders{}, Environments: map[string]*htup.Environment{}, Sync: &SyncConfig{Remote: remote}}
	if verbose {
		configPath, err := filepath.Rel(directory, target)
		if err != nil {
			return err
		}
		config.Sync.Include.Paths = []string{filepath.ToSlash(configPath)}
		config.Sync.MaxFileSize = DefaultMaxFileSize
	}
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

func parsePathExpressions(values []string) (PathExpressions, error) {
	expressions := make(PathExpressions, 0, len(values))
	for _, value := range values {
		expression, err := parsePathExpression(value, false)
		if err != nil {
			return nil, err
		}
		expressions = append(expressions, expression)
	}
	return expressions, nil
}

func parseGitIgnore(raw string) (PathExpressions, error) {
	var expressions PathExpressions
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		expression, err := parsePathExpression(line, true)
		if err != nil {
			return nil, err
		}
		expressions = append(expressions, expression)
	}
	return expressions, nil
}

func parsePathExpression(value string, gitignore bool) (PathExpression, error) {
	if gitignore {
		value = strings.TrimRight(value, " ")
		if strings.HasPrefix(value, "\\ ") {
			value = strings.TrimSuffix(value, " ") + " "
		}
	}
	expression := PathExpression{}
	if strings.HasPrefix(value, "\\#") || strings.HasPrefix(value, "\\!") {
		value = value[1:]
	} else if strings.HasPrefix(value, "!") {
		expression.negated = true
		value = value[1:]
	}
	if strings.HasPrefix(value, "/") {
		expression.rooted = true
		value = strings.TrimPrefix(value, "/")
	}
	if strings.HasSuffix(value, "/") {
		expression.directory = true
		value = strings.TrimSuffix(value, "/")
	}
	if value == "" || value == "." || strings.HasPrefix(value, "../") || strings.Contains(value, "//") || path.Clean(value) != value {
		return PathExpression{}, fmt.Errorf("invalid path expression %q", value)
	}
	if err := validateGlob(value); err != nil {
		return PathExpression{}, fmt.Errorf("invalid path expression %q: %w", value, err)
	}
	expression.pattern = value
	expression.basename = !expression.rooted && !strings.Contains(value, "/")
	return expression, nil
}

func validateMimetypeExpressions(expressions []string) error {
	for _, expression := range expressions {
		if _, err := path.Match(expression, ""); err != nil {
			return fmt.Errorf("%w: invalid MIME expression %q: %v", ErrInvalidState, expression, err)
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

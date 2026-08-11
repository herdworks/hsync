package hsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncConfigRemoteURLDefaultsRootToIndex(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"https://example.com", "https://example.com/"} {
		remote, err := (SyncConfig{Remote: value}).RemoteURL()
		if err != nil {
			t.Fatalf("RemoteURL(%q) error = %v", value, err)
		}
		if remote.Path != DefaultIndexPath {
			t.Fatalf("RemoteURL(%q).Path = %q, want %q", value, remote.Path, DefaultIndexPath)
		}
	}
	remote, err := (SyncConfig{Remote: "https://example.com/custom/index.json"}).RemoteURL()
	if err != nil {
		t.Fatal(err)
	}
	if remote.Path != "/custom/index.json" {
		t.Fatalf("custom remote path = %q", remote.Path)
	}
}

func TestSyncConfigMaxBytes(t *testing.T) {
	t.Parallel()

	config := SyncConfig{MaxFileSize: "5MB"}
	max, err := config.MaxBytes()
	if err != nil {
		t.Fatal(err)
	}
	if max != 5*1024*1024 {
		t.Fatalf("max = %d, want 5242880", max)
	}
	if _, err := (SyncConfig{MaxFileSize: "5MB", MaxFileBytes: 5_000_000}).MaxBytes(); err == nil {
		t.Fatal("MaxBytes() error = nil when both size forms are configured")
	}
}

func TestSyncConfigWithoutIncludeDoesNotScan(t *testing.T) {
	t.Parallel()

	policy, err := (SyncConfig{}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if policy.ScanningEnabled() {
		t.Fatal("SyncConfig without include unexpectedly scans")
	}
	entry := ManifestEntry{Path: "content/page.txt", Size: 1, Mimetype: "text/plain"}
	if policy.Includes(entry) {
		t.Fatal("zero policy unexpectedly includes a file")
	}
	if !policy.Allows(entry) {
		t.Fatal("zero policy unexpectedly excludes a file")
	}
	policy, err = (SyncConfig{}).Policy(".hsync/**")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Allows(ManifestEntry{Path: ".hsync/manifest.json", Size: 1, Mimetype: "application/json"}) {
		t.Fatal("reserved path was not excluded")
	}
}

func TestSyncConfigRejectsEscapingIncludePaths(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{".", "../content/**", "content/../../secrets/**"} {
		config := SyncConfig{
			Remote:  "https://example.com/.sync/index.json",
			Include: Rules{Paths: []string{pattern}},
		}
		if err := config.Validate(); err == nil {
			t.Errorf("Validate() error = nil for escaping include %q", pattern)
		}
	}
	for _, pattern := range []string{"*", "**", "/content/**", "content/**"} {
		valid := SyncConfig{
			Remote:  "https://example.com/.sync/index.json",
			Include: Rules{Paths: []string{pattern}},
		}
		if err := valid.Validate(); err != nil {
			t.Fatalf("Validate() error = %v for valid include %q", err, pattern)
		}
	}
}

func TestDiscoverConfigUsesRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DefaultConfigPath), []byte("sync: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	filename, err := DiscoverConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, DefaultConfigPath); filename != want {
		t.Fatalf("config path = %q, want %q", filename, want)
	}
}

func TestReadConfigRecordsPath(t *testing.T) {
	root := t.TempDir()
	expected := filepath.Join(root, "site.yaml")
	if err := os.WriteFile(expected, []byte("sync: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, raw, err := ReadConfig(expected)
	if err != nil {
		t.Fatal(err)
	}
	if config.Path != expected {
		t.Fatalf("config path = %q, want %q", config.Path, expected)
	}
	if got, want := config.Directory(), root; got != want {
		t.Fatalf("config directory = %q, want %q", got, want)
	}
	if string(raw) != "sync: {}\n" {
		t.Fatalf("raw config = %q", raw)
	}
}

func TestDiscoverConfigRejectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"hsync.yaml", "hsync.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sync: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DiscoverConfig(root); err == nil {
		t.Fatal("DiscoverConfig() error = nil")
	}
}

func TestReadConfigUsesConfigParentAndDiscoverRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideConfig := filepath.Join(outside, DefaultConfigPath)
	if err := os.WriteFile(outsideConfig, []byte("sync: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := ReadConfig(outsideConfig)
	if err != nil {
		t.Fatal(err)
	}
	if config.Directory() != outside {
		t.Fatalf("config directory = %q, want %q", config.Directory(), outside)
	}
	link := filepath.Join(root, DefaultConfigPath)
	if err := os.Symlink(outsideConfig, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := DiscoverConfig(root); err == nil {
		t.Fatal("DiscoverConfig() error = nil for symlink")
	}
}

func TestRepositoryConfigDirectoryWithoutPathIsEmpty(t *testing.T) {
	if got := (RepositoryConfig{}).Directory(); got != "" {
		t.Fatalf("directory = %q, want empty", got)
	}
}

func TestParseConfigAcceptsJSON(t *testing.T) {
	config, err := ParseConfig([]byte(`{"sync":{"remote":"https://example.com/.hsync/index.json"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Sync == nil || config.Sync.Remote != "https://example.com/.hsync/index.json" {
		t.Fatalf("parsed config = %#v", config)
	}
	if config.Path != "" {
		t.Fatalf("parsed config path = %q, want empty", config.Path)
	}
}

func TestInitializeConfigRejectsNestedPath(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := InitializeConfig(filepath.Join("nested", DefaultConfigPath), "https://example.com/.sync/latest.json"); err == nil {
		t.Fatal("InitializeConfig() error = nil")
	} else if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("InitializeConfig() returned wrong error: %v", err)
	}
}

func TestInitializeConfigAcceptsAbsoluteRootConfigPath(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "hsync.json")
	if err := InitializeConfig(filename, "https://example.com/.sync/latest.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("config was not created: %v", err)
	}
}

func TestGitignoreLikePathExpressions(t *testing.T) {
	expressions, err := parseGitIgnore("# comment\nsecret\n/root.txt\ncache/\n!cache/keep.txt\n\\#literal\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"nested/secret", "root.txt", "cache/drop.txt", "#literal"} {
		if allowed := (gitignoreRules{expressions: expressions}).Allows(path); allowed {
			t.Fatalf("%q unexpectedly allowed", path)
		}
	}
	if !(gitignoreRules{expressions: expressions}).Allows("nested/root.txt") {
		t.Fatal("root-anchored rule matched nested file")
	}
	if !(gitignoreRules{expressions: expressions}).Allows("cache/keep.txt") {
		t.Fatal("negated later rule did not re-allow file")
	}
}

func TestConfiguredPathExpressionsUseGitignoreLikeMatching(t *testing.T) {
	expressions, err := parsePathExpressions([]string{"*.tmp", "!keep.tmp", "/root.txt", "cache/"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want bool
	}{
		{"nested/file.tmp", true},
		{"keep.tmp", false},
		{"root.txt", true},
		{"nested/root.txt", false},
		{"cache/value.txt", true},
	} {
		matched, got := expressions.match(test.path)
		if got != test.want || (test.want && !matched) {
			t.Fatalf("match(%q) = (%v, %v), want positive=%v", test.path, matched, got, test.want)
		}
	}
}

func TestParsePathExpressionMatchesPublicPaths(t *testing.T) {
	expression, err := ParsePathExpression("/blog/**")
	if err != nil {
		t.Fatal(err)
	}
	if !expression.Matches("/blog/hello/") {
		t.Fatal("rooted expression did not match canonical path")
	}
	if expression.Matches("/docs/hello/") {
		t.Fatal("rooted expression matched unrelated path")
	}
}

func TestParsePathExpressionMatchesCanonicalPagePath(t *testing.T) {
	expression, err := ParsePathExpression("/blog")
	if err != nil {
		t.Fatal(err)
	}
	if !expression.Matches("/blog/") {
		t.Fatal("exact rooted expression did not match canonical page path")
	}
	if expression.Matches("/blog/hello/") {
		t.Fatal("exact rooted expression matched a descendant page")
	}
}

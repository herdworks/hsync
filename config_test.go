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

func TestSyncConfigRejectsRootIncludePaths(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{".", "*", "**", "../content/**", "/content/**", "content/../../secrets/**"} {
		config := SyncConfig{
			Remote:  "https://example.com/.sync/index.json",
			Include: Rules{Paths: []string{pattern}},
		}
		if err := config.Validate(); err == nil {
			t.Errorf("Validate() error = nil for root or escaping include %q", pattern)
		}
	}
	valid := SyncConfig{
		Remote:  "https://example.com/.sync/index.json",
		Include: Rules{Paths: []string{"content/**"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v for a nested include", err)
	}
}

func TestConfigPathAtUsesRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DefaultConfigPath), []byte("sync: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	filename, err := ConfigPathAt(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, DefaultConfigPath); filename != want {
		t.Fatalf("config path = %q, want %q", filename, want)
	}
}

func TestConfigPathAtUsesExplicitThenEnvironmentBeforeDetection(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"hsync.yaml", "hsync.json", ".hsync.yaml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sync: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HSYNC_CONFIG", "hsync.json")
	filename, err := ConfigPathAt(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "hsync.json"); filename != want {
		t.Fatalf("environment config = %q, want %q", filename, want)
	}
	filename, err = ConfigPathAt(root, ".hsync.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".hsync.yaml"); filename != want {
		t.Fatalf("explicit config = %q, want %q", filename, want)
	}
}

func TestConfigPathAtRejectsAmbiguousDetection(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"hsync.yaml", "hsync.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sync: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ConfigPathAt(root, ""); err == nil {
		t.Fatal("ConfigPathAt() error = nil")
	}
}

func TestConfigPathAtRejectsOutsideAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideConfig := filepath.Join(outside, DefaultConfigPath)
	if err := os.WriteFile(outsideConfig, []byte("sync: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigPathAt(root, outsideConfig); err == nil {
		t.Fatal("ConfigPathAt() error = nil for outside configuration")
	}
	link := filepath.Join(root, DefaultConfigPath)
	if err := os.Symlink(outsideConfig, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ConfigPathAt(root, ""); err == nil {
		t.Fatal("ConfigPathAt() error = nil for symlink")
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

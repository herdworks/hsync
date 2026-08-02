package hsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositorySnapshotAppliesPathMIMEAndSizeRules(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "post.md"), "post")
	writeFile(t, filepath.Join(workspace, "content", "large.md"), "large")
	writeFile(t, filepath.Join(workspace, "data", "site.yaml"), "title: Site")

	config := SyncConfig{
		Include:      Rules{Paths: []string{"content/**"}, Mimetypes: []string{"text/*"}},
		Ignore:       Rules{Paths: []string{"**/large.md"}},
		MaxFileBytes: 32,
	}
	snapshot, err := (Repository{Root: workspace, Config: &config}).snapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snapshot.Entries))
	}
	if _, ok := snapshot.Entries["content/post.md"]; !ok {
		t.Fatalf("post was not scanned: %#v", snapshot.Entries)
	}
}

func TestGlobMatch(t *testing.T) {
	t.Parallel()
	if !globMatch("content/**", "content/posts/hello.md") {
		t.Fatal("content/** did not match nested path")
	}
	if globMatch("content/*.md", "content/posts/hello.md") {
		t.Fatal("single-segment glob matched nested path")
	}
}

func TestRepositorySnapshotSkipsSymlinks(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "post.md"), "post")
	if err := os.Symlink(filepath.Join(workspace, "content", "post.md"), filepath.Join(workspace, "content", "alias.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	config := SyncConfig{Include: Rules{Paths: []string{"content/**"}}}
	snapshot, err := (Repository{Root: workspace, Config: &config}).snapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %#v, want only regular file", snapshot.Entries)
	}
	if _, ok := snapshot.Entries["content/alias.md"]; ok {
		t.Fatal("symlink entered snapshot")
	}
}

func TestRepositorySnapshotWithEmptyIncludeSelectsNothing(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, ".git", "config"), "[core]")
	writeFile(t, filepath.Join(workspace, ".secrets"), "do not upload")
	writeFile(t, filepath.Join(workspace, "content", "page.html"), "<p>page</p>")

	config, err := ParseConfigDocument("hsync.yaml", []byte(`sync:
  remote: https://example.com/.sync/index.json
  include: {}
  ignore: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Sync == nil {
		t.Fatal("parsed sync config is nil")
	}
	snapshot, err := (Repository{Root: workspace, Config: config.Sync}).snapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 0 {
		t.Fatalf("entries = %#v, want no files for empty sync.include", snapshot.Entries)
	}
}

func writeFile(t *testing.T, filename, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

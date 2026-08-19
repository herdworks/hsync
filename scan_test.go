package hsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryLocalAppliesPathMIMEAndSizeRules(t *testing.T) {
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
	local, err := (Repository{Root: workspace, Config: &config}).scanLocal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(local.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(local.entries))
	}
	if _, ok := local.entries["content/post.md"]; !ok {
		t.Fatalf("post was not scanned: %#v", local.entries)
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

func TestRepositoryLocalSkipsSymlinks(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "post.md"), "post")
	if err := os.Symlink(filepath.Join(workspace, "content", "post.md"), filepath.Join(workspace, "content", "alias.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	config := SyncConfig{Include: Rules{Paths: []string{"content/**"}}}
	local, err := (Repository{Root: workspace, Config: &config}).scanLocal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(local.entries) != 1 {
		t.Fatalf("entries = %#v, want only regular file", local.entries)
	}
	if _, ok := local.entries["content/alias.md"]; ok {
		t.Fatal("symlink entered local")
	}
}

func TestRepositoryLocalWithEmptyIncludeSelectsNothing(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, ".git", "config"), "[core]")
	writeFile(t, filepath.Join(workspace, ".secrets"), "do not upload")
	writeFile(t, filepath.Join(workspace, "content", "page.html"), "<p>page</p>")

	config, err := ParseConfig([]byte(`sync:
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
	if _, err := (Repository{Root: workspace, Config: config.Sync}).scanLocal(nil); err == nil {
		t.Fatal("local(nil) error = nil for empty sync.include")
	}
}

func TestRepositoryLocalHonorsRootGitignore(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, ".gitignore"), "secret\ncache/\n!cache/keep.txt\n")
	writeFile(t, filepath.Join(workspace, "content", "secret"), "secret")
	writeFile(t, filepath.Join(workspace, "cache", "drop.txt"), "drop")
	writeFile(t, filepath.Join(workspace, "cache", "keep.txt"), "keep")
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")

	local, err := (Repository{Root: workspace, Config: &SyncConfig{Include: Rules{Paths: []string{"content/**", "cache/**"}}}}).scanLocal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := local.entries["content/secret"]; ok {
		t.Fatal("gitignored secret was included")
	}
	if _, ok := local.entries["cache/drop.txt"]; ok {
		t.Fatal("gitignored cache file was included")
	}
	for _, path := range []string{"cache/keep.txt", "content/page.txt"} {
		if _, ok := local.entries[path]; !ok {
			t.Fatalf("%q not included: %#v", path, local.entries)
		}
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

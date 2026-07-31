package hypersync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanAppliesPathMIMEAndSizeRules(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "post.md"), "post")
	writeFile(t, filepath.Join(workspace, "content", "large.md"), "large")
	writeFile(t, filepath.Join(workspace, "data", "site.yaml"), "title: Site")

	snapshot, err := Scan(workspace, ScanConfig{
		Include:     Rules{Paths: []string{"content/**"}, Mimetypes: []string{"text/*"}},
		Ignore:      Rules{Paths: []string{"**/large.md"}},
		MaxFileSize: 32,
	})
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

func writeFile(t *testing.T, filename, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

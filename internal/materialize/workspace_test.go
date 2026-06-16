package materialize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceScanStatusMaterialize(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "config.yaml", "title: Site\n")
	writeFile(t, root, "content/post.md", "---\ntitle: Post\n---\nHello\n")
	writeFile(t, root, "assets/logo.txt", "asset")
	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.OpsWritten == 0 || result.DocumentsChanged == 0 || result.AssetsStored != 1 {
		t.Fatalf("scan result = %#v", result)
	}
	status, err := LoadStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.DocumentCount != 2 || status.AssetCount != 1 {
		t.Fatalf("status = %#v", status)
	}
	if err := os.Remove(filepath.Join(root, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "content", "post.md")); err != nil {
		t.Fatal(err)
	}
	if err := Materialize(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "content", "post.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) == "" {
		t.Fatal("materialized post is empty")
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

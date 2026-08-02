package hsync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRepositoryStatus(t *testing.T) {
	workspace := t.TempDir()
	config := &SyncConfig{Include: Rules{Paths: []string{"added.txt", "deleted.txt", "modified.txt", "unchanged.txt"}}}
	base := Manifest{
		Version:  Version,
		SyncPath: "/.sync/latest.json",
		Entries: []ManifestEntry{
			{Path: "deleted.txt", Blob: NewIdentifier([]byte("deleted")), Size: 7, Mimetype: "text/plain"},
			{Path: "modified.txt", Blob: NewIdentifier([]byte("before")), Size: 6, Mimetype: "text/plain"},
			{Path: "unchanged.txt", Blob: NewIdentifier([]byte("same")), Size: 4, Mimetype: "text/plain"},
		},
	}
	raw, id, err := SerializeManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(workspace, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"added.txt":     "added",
		"modified.txt":  "after",
		"unchanged.txt": "same",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := (Repository{Root: workspace, Config: config}).Status()
	if err != nil {
		t.Fatal(err)
	}
	if result.Base != id {
		t.Fatalf("base = %s, want %s", result.Base, id)
	}
	want := []Change{
		{Path: "added.txt", Kind: ChangeAdded},
		{Path: "deleted.txt", Kind: ChangeDeleted},
		{Path: "modified.txt", Kind: ChangeModified},
	}
	if !reflect.DeepEqual(result.Changes, want) {
		t.Fatalf("changes = %#v, want %#v", result.Changes, want)
	}
}

func TestRepositoryStatusWithoutIncludeTracksOnlyManifestEntries(t *testing.T) {
	workspace := t.TempDir()
	base := Manifest{
		Version: Version, SyncPath: "/.sync/latest.json",
		Entries: []ManifestEntry{{Path: "tracked.txt", Blob: NewIdentifier([]byte("before")), Size: 6, Mimetype: "text/plain"}},
	}
	raw, _, err := SerializeManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(workspace, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (Repository{Root: workspace, Config: &SyncConfig{}}).Status()
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{Path: "tracked.txt", Kind: ChangeModified}}
	if !reflect.DeepEqual(result.Changes, want) {
		t.Fatalf("changes = %#v, want %#v", result.Changes, want)
	}
}

func TestRepositoryStatusAppliesIgnoreToManifestEntries(t *testing.T) {
	workspace := t.TempDir()
	base := Manifest{
		Version: Version, SyncPath: "/.sync/latest.json",
		Entries: []ManifestEntry{{Path: "secret.txt", Blob: NewIdentifier([]byte("before")), Size: 6, Mimetype: "text/plain"}},
	}
	raw, _, err := SerializeManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(workspace, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "secret.txt"), []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (Repository{
		Root:   workspace,
		Config: &SyncConfig{Ignore: Rules{Paths: []string{"secret.txt"}}},
	}).Status()
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{Path: "secret.txt", Kind: ChangeDeleted}}
	if !reflect.DeepEqual(result.Changes, want) {
		t.Fatalf("changes = %#v, want %#v", result.Changes, want)
	}
}

func TestRepositoryStatusRetainsManifestEntriesOutsideInclude(t *testing.T) {
	workspace := t.TempDir()
	base := Manifest{
		Version: Version, SyncPath: "/.sync/latest.json",
		Entries: []ManifestEntry{{Path: "tracked.txt", Blob: NewIdentifier([]byte("tracked")), Size: 7, Mimetype: "text/plain"}},
	}
	raw, _, err := SerializeManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(workspace, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}},
	}).Status()
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{Path: "content/new.txt", Kind: ChangeAdded}}
	if !reflect.DeepEqual(result.Changes, want) {
		t.Fatalf("changes = %#v, want %#v", result.Changes, want)
	}
}

package hsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotAddInitializesMaps(t *testing.T) {
	t.Parallel()

	contents := []byte("tracked")
	entry := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier(contents)}
	var snapshot Snapshot
	snapshot.add(entry, contents)
	if got := snapshot.Entries[entry.Path]; got != entry {
		t.Fatalf("entry = %#v, want %#v", got, entry)
	}
	if got := string(snapshot.Blobs[entry.Blob]); got != string(contents) {
		t.Fatalf("blob = %q, want %q", got, contents)
	}
}

func TestRepositorySnapshotWithoutIncludeRetainsManifestEntries(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	manifest := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier([]byte("tracked")), Size: 7, Mimetype: "text/plain"}
	if err := os.WriteFile(filepath.Join(workspace, manifest.Path), []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := (Repository{Root: workspace}).snapshot(&Manifest{Entries: []ManifestEntry{manifest}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Entries[manifest.Path]; !ok {
		t.Fatal("manifest entry was not retained")
	}
}

func TestRepositorySnapshotIncludesConfiguredNestedPayload(t *testing.T) {
	workspace := t.TempDir()
	payload := filepath.Join(workspace, "builds", "payload", "index.html")
	if err := os.MkdirAll(filepath.Dir(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("<p>payload</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := (Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"builds/payload/**"}}},
	}).snapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Entries["builds/payload/index.html"]; !ok {
		t.Fatalf("configured nested payload was not discovered: %#v", snapshot.Entries)
	}
}

func TestRepositorySnapshotIgnoresControlPaths(t *testing.T) {
	workspace := t.TempDir()
	control := filepath.Join(workspace, ".hsync", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(control), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, []byte("local state"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := &SyncConfig{Include: Rules{Paths: []string{".hsync/**"}}}
	snapshot, err := (Repository{Root: workspace, Config: config}).snapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 0 {
		t.Fatalf("control entries = %#v, want none", snapshot.Entries)
	}
	if len(config.Ignore.Paths) != 0 {
		t.Fatalf("config ignore paths mutated: %#v", config.Ignore.Paths)
	}
}

func TestRepositorySnapshotCombinesManifestAndConfiguredDiscovery(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "tracked.txt"), "tracked")
	writeFile(t, filepath.Join(workspace, "content", "new.txt"), "new")
	tracked := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier([]byte("tracked")), Size: 7, Mimetype: "text/plain"}

	snapshot, err := (Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}},
	}).snapshot(&Manifest{Entries: []ManifestEntry{tracked}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tracked.txt", "content/new.txt"} {
		if _, ok := snapshot.Entries[name]; !ok {
			t.Fatalf("snapshot did not include %q: %#v", name, snapshot.Entries)
		}
	}
}

func TestRepositorySnapshotRequiresManifestOrConfig(t *testing.T) {
	t.Parallel()

	if _, err := (Repository{}).snapshot(nil); err == nil {
		t.Fatal("snapshot error = nil without manifest or config")
	}
}

func TestReconcileLocalWinsConflict(t *testing.T) {
	t.Parallel()
	baseBlob := NewIdentifier([]byte("base"))
	localBlob := NewIdentifier([]byte("local"))
	remoteBlob := NewIdentifier([]byte("remote"))
	base := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "content/post.md", Blob: baseBlob, Size: 4, Mimetype: "text/markdown; charset=utf-8"}}}
	live := Manifest{Version: base.Version, SyncPath: base.SyncPath, Entries: append([]ManifestEntry(nil), base.Entries...)}
	live.Parent = NewIdentifier([]byte("ancestor"))
	live.Entries[0].Blob = remoteBlob
	workspace := Snapshot{Entries: map[string]ManifestEntry{"content/post.md": {Path: "content/post.md", Blob: localBlob, Size: 5, Mimetype: "text/markdown; charset=utf-8"}}, Blobs: map[Identifier][]byte{localBlob: []byte("local")}}

	liveID := NewIdentifier([]byte("live manifest"))
	changeset, err := (Repository{}).Reconcile(base, liveID, Continuation{Revisions: []Manifest{live}}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(changeset.Manifest, changeset.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Entries[0].Blob; got != localBlob {
		t.Fatalf("blob = %s, want local %s", got, localBlob)
	}
	if got := manifest.Parent; got != liveID {
		t.Fatalf("parent = %s, want %s", got, liveID)
	}
	if got := changeset.Blobs[localBlob]; string(got) != "local" || len(changeset.Blobs) != 1 {
		t.Fatalf("required blobs = %#v, want only local blob", changeset.Blobs)
	}
}

func TestReconcileOmitsBlobsAlreadyReferencedByLiveManifest(t *testing.T) {
	t.Parallel()
	sharedBlob := NewIdentifier([]byte("shared"))
	base := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "content/a.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"}}}
	live := Manifest{Version: Version, SyncPath: base.SyncPath, Entries: append([]ManifestEntry(nil), base.Entries...)}
	workspace := Snapshot{Entries: map[string]ManifestEntry{
		"content/a.md": {Path: "content/a.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"},
		"content/b.md": {Path: "content/b.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"},
	}, Blobs: map[Identifier][]byte{sharedBlob: []byte("shared")}}

	changeset, err := (Repository{}).Reconcile(base, NewIdentifier([]byte("live manifest")), Continuation{Revisions: []Manifest{live}}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(changeset.Blobs) != 0 {
		t.Fatalf("required blobs = %#v, want none", changeset.Blobs)
	}
}

func TestReconcileAdoptsWorkspaceValueFoundInRemoteHistory(t *testing.T) {
	t.Parallel()
	baseBlob := NewIdentifier([]byte("version: 1\n"))
	intermediateBlob := NewIdentifier([]byte("version: 2\n"))
	liveBlob := NewIdentifier([]byte("version: 10\n"))
	base := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "site.yaml", Blob: baseBlob, Size: 11, Mimetype: "application/yaml"}}}
	intermediate := Manifest{Version: Version, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "site.yaml", Blob: intermediateBlob, Size: 11, Mimetype: "application/yaml"}}}
	live := Manifest{Version: Version, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "site.yaml", Blob: liveBlob, Size: 12, Mimetype: "application/yaml"}}}
	workspace := Snapshot{Entries: map[string]ManifestEntry{"site.yaml": {Path: "site.yaml", Blob: intermediateBlob, Size: 11, Mimetype: "application/yaml"}}, Blobs: map[Identifier][]byte{intermediateBlob: []byte("version: 2\n")}}

	changeset, err := (Repository{}).Reconcile(base, NewIdentifier([]byte("live manifest")), Continuation{Revisions: []Manifest{live, intermediate}}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := changeset.manifest.Entries[0].Blob; got != liveBlob {
		t.Fatalf("blob = %s, want live %s", got, liveBlob)
	}
	if len(changeset.Blobs) != 0 {
		t.Fatalf("blobs = %#v, want none", changeset.Blobs)
	}
}

func TestReconcileAdoptsIntermediateDeletion(t *testing.T) {
	t.Parallel()
	baseBlob, liveBlob := NewIdentifier([]byte("base")), NewIdentifier([]byte("live"))
	base := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "page.txt", Blob: baseBlob, Size: 4, Mimetype: "text/plain"}}}
	middle := Manifest{Version: Version, SyncPath: base.SyncPath}
	live := Manifest{Version: Version, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "page.txt", Blob: liveBlob, Size: 4, Mimetype: "text/plain"}}}
	workspace := Snapshot{Entries: map[string]ManifestEntry{}, Blobs: map[Identifier][]byte{}}
	changeset, err := (Repository{}).Reconcile(base, NewIdentifier([]byte("live manifest")), Continuation{Revisions: []Manifest{live, middle}}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := changeset.manifest.Entries[0].Blob; got != liveBlob {
		t.Fatalf("blob = %s, want live %s", got, liveBlob)
	}
}

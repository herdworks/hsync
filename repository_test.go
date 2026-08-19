package hsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalAddInitializesMaps(t *testing.T) {
	t.Parallel()

	contents := []byte("tracked")
	entry := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier(contents)}
	var local Local
	local.add(entry, contents)
	if got := local.entries[entry.Path]; got != entry {
		t.Fatalf("entry = %#v, want %#v", got, entry)
	}
	if got := string(local.Blobs[entry.Blob]); got != string(contents) {
		t.Fatalf("blob = %q, want %q", got, contents)
	}
}

func TestRepositoryPrepareCreatesWorkspaceAlongsideManagedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "site.yaml"), []byte("site"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err := (Repository{Root: root}).prepare("operation")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".hsync", "tmp", "operation")
	if workspace != want {
		t.Fatalf("workspace = %q, want %q", workspace, want)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		t.Fatalf("workspace info = %#v, err=%v", info, err)
	}
}

func TestRepositoryCommitUpdatesBase(t *testing.T) {
	t.Parallel()

	repository := Repository{Root: t.TempDir()}
	temporary := filepath.Join(repository.Root, ".hsync", "tmp", "checkout")
	if err := os.MkdirAll(filepath.Join(temporary, stagedFilesPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := NewSealedManifest("/.sync/latest.json", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temporary, "manifest.json"), manifest.Raw(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repository.commit(temporary); err != nil {
		t.Fatal(err)
	}
	if repository.Base.ID() != manifest.ID() || string(repository.Base.Raw()) != string(manifest.Raw()) {
		t.Fatalf("repository base = %#v, want staged manifest", repository.Base)
	}
}

func TestRepositorySnapshotCachesWorkspaceState(t *testing.T) {
	t.Parallel()

	repository := Repository{
		Root:   t.TempDir(),
		Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}},
	}
	writeFile(t, filepath.Join(repository.Root, "content", "post.md"), "post")
	if err := repository.Snapshot(); err != nil {
		t.Fatal(err)
	}
	entries := repository.Local.Entries()
	if len(entries) != 1 || entries[0].Path != "content/post.md" {
		t.Fatalf("repository local entries = %#v", entries)
	}
	if got := string(repository.Local.Blobs[entries[0].Blob]); got != "post" {
		t.Fatalf("repository local blob = %q, want post", got)
	}
}

func TestRepositoryLocalWithoutIncludeRetainsManifestEntries(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	manifest := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier([]byte("tracked")), Size: 7, Mimetype: "text/plain"}
	if err := os.WriteFile(filepath.Join(workspace, manifest.Path), []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := (Repository{Root: workspace}).scanLocal(&Manifest{entries: []ManifestEntry{manifest}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := local.entries[manifest.Path]; !ok {
		t.Fatal("manifest entry was not retained")
	}
}

func TestRepositoryLocalIncludesConfiguredNestedPayload(t *testing.T) {
	workspace := t.TempDir()
	payload := filepath.Join(workspace, "builds", "payload", "index.html")
	if err := os.MkdirAll(filepath.Dir(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("<p>payload</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	local, err := (Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"builds/payload/**"}}},
	}).scanLocal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := local.entries["builds/payload/index.html"]; !ok {
		t.Fatalf("configured nested payload was not discovered: %#v", local.entries)
	}
}

func TestRepositoryLocalIgnoresControlPaths(t *testing.T) {
	workspace := t.TempDir()
	control := filepath.Join(workspace, ".hsync", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(control), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, []byte("local state"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := &SyncConfig{Include: Rules{Paths: []string{".hsync/**"}}}
	local, err := (Repository{Root: workspace, Config: config}).scanLocal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(local.entries) != 0 {
		t.Fatalf("control entries = %#v, want none", local.entries)
	}
	if len(config.Ignore.Paths) != 0 {
		t.Fatalf("config ignore paths mutated: %#v", config.Ignore.Paths)
	}
}

func TestRepositoryLocalCombinesManifestAndConfiguredDiscovery(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "tracked.txt"), "tracked")
	writeFile(t, filepath.Join(workspace, "content", "new.txt"), "new")
	tracked := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier([]byte("tracked")), Size: 7, Mimetype: "text/plain"}

	local, err := (Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}},
	}).scanLocal(&Manifest{entries: []ManifestEntry{tracked}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tracked.txt", "content/new.txt"} {
		if _, ok := local.entries[name]; !ok {
			t.Fatalf("local did not include %q: %#v", name, local.entries)
		}
	}
}

func TestRepositoryLocalRequiresManifestOrConfig(t *testing.T) {
	t.Parallel()

	if _, err := (Repository{}).scanLocal(nil); err == nil {
		t.Fatal("local error = nil without manifest or config")
	}
}

func TestReconcileLocalWinsConflict(t *testing.T) {
	t.Parallel()
	baseBlob := NewIdentifier([]byte("base"))
	localBlob := NewIdentifier([]byte("local"))
	remoteBlob := NewIdentifier([]byte("remote"))
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/post.md", Blob: baseBlob, Size: 4, Mimetype: "text/markdown; charset=utf-8"}}}
	head := Manifest{version: base.Version(), indexPath: base.IndexPath(), entries: append([]ManifestEntry(nil), base.Entries()...)}
	if err := head.SetParent(NewIdentifier([]byte("ancestor"))); err != nil {
		t.Fatal(err)
	}
	headEntries := head.Entries()
	headEntries[0].Blob = remoteBlob
	if err := head.SetEntries(headEntries); err != nil {
		t.Fatal(err)
	}
	workspace := Local{entries: map[string]ManifestEntry{"content/post.md": {Path: "content/post.md", Blob: localBlob, Size: 5, Mimetype: "text/markdown; charset=utf-8"}}, Blobs: map[Identifier][]byte{localBlob: []byte("local")}}

	headID := NewIdentifier([]byte("head manifest"))
	reconciliation, err := (Repository{Base: base, Local: workspace}).Reconcile(headID, Lineage{Revisions: []Manifest{head}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := reconciliation.Changeset()
	if err != nil {
		t.Fatal(err)
	}
	manifest := changeset.Manifest
	if got := manifest.Entries()[0].Blob; got != localBlob {
		t.Fatalf("blob = %s, want local %s", got, localBlob)
	}
	if got := manifest.Parent(); got != headID {
		t.Fatalf("parent = %s, want %s", got, headID)
	}
	if got := changeset.Blobs[localBlob]; string(got) != "local" || len(changeset.Blobs) != 1 {
		t.Fatalf("required blobs = %#v, want only local blob", changeset.Blobs)
	}
}

func TestReconcileOmitsBlobsAlreadyReferencedByLiveManifest(t *testing.T) {
	t.Parallel()
	sharedBlob := NewIdentifier([]byte("shared"))
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/a.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"}}}
	head := Manifest{version: ManifestVersion, indexPath: base.IndexPath(), entries: append([]ManifestEntry(nil), base.Entries()...)}
	workspace := Local{entries: map[string]ManifestEntry{
		"content/a.md": {Path: "content/a.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"},
		"content/b.md": {Path: "content/b.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"},
	}, Blobs: map[Identifier][]byte{sharedBlob: []byte("shared")}}

	reconciliation, err := (Repository{Base: base, Local: workspace}).Reconcile(NewIdentifier([]byte("head manifest")), Lineage{Revisions: []Manifest{head}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := reconciliation.Changeset()
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
	headBlob := NewIdentifier([]byte("version: 10\n"))
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "site.yaml", Blob: baseBlob, Size: 11, Mimetype: "application/yaml"}}}
	intermediate := Manifest{version: ManifestVersion, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "site.yaml", Blob: intermediateBlob, Size: 11, Mimetype: "application/yaml"}}}
	head := Manifest{version: ManifestVersion, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "site.yaml", Blob: headBlob, Size: 12, Mimetype: "application/yaml"}}}
	workspace := Local{entries: map[string]ManifestEntry{"site.yaml": {Path: "site.yaml", Blob: intermediateBlob, Size: 11, Mimetype: "application/yaml"}}, Blobs: map[Identifier][]byte{intermediateBlob: []byte("version: 2\n")}}

	reconciliation, err := (Repository{Base: base, Local: workspace}).Reconcile(NewIdentifier([]byte("head manifest")), Lineage{Revisions: []Manifest{head, intermediate}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := reconciliation.Changeset()
	if err != nil {
		t.Fatal(err)
	}
	if got := changeset.Manifest.Entries()[0].Blob; got != headBlob {
		t.Fatalf("blob = %s, want head %s", got, headBlob)
	}
	if len(changeset.Blobs) != 0 {
		t.Fatalf("blobs = %#v, want none", changeset.Blobs)
	}
}

func TestReconcileAdoptsIntermediateDeletion(t *testing.T) {
	t.Parallel()
	baseBlob, headBlob := NewIdentifier([]byte("base")), NewIdentifier([]byte("head"))
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "page.txt", Blob: baseBlob, Size: 4, Mimetype: "text/plain"}}}
	middle := Manifest{version: ManifestVersion, indexPath: base.IndexPath()}
	head := Manifest{version: ManifestVersion, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "page.txt", Blob: headBlob, Size: 4, Mimetype: "text/plain"}}}
	workspace := Local{entries: map[string]ManifestEntry{}, Blobs: map[Identifier][]byte{}}
	reconciliation, err := (Repository{Base: base, Local: workspace}).Reconcile(NewIdentifier([]byte("head manifest")), Lineage{Revisions: []Manifest{head, middle}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := reconciliation.Changeset()
	if err != nil {
		t.Fatal(err)
	}
	if got := changeset.Manifest.Entries()[0].Blob; got != headBlob {
		t.Fatalf("blob = %s, want head %s", got, headBlob)
	}
}

package hypersync

import "testing"

func TestReconcileLocalWinsConflict(t *testing.T) {
	t.Parallel()
	baseBlob := NewIdentifier([]byte("base"))
	localBlob := NewIdentifier([]byte("local"))
	remoteBlob := NewIdentifier([]byte("remote"))
	config := &ScanConfig{Include: Rules{Paths: []string{"content/**"}}}
	base := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Config: config, Entries: []ManifestEntry{{Path: "content/post.md", Blob: baseBlob, Size: 4, Mimetype: "text/markdown; charset=utf-8"}}}
	live := Manifest{Version: base.Version, SyncPath: base.SyncPath, Config: config, Entries: append([]ManifestEntry(nil), base.Entries...)}
	live.Parent = NewIdentifier([]byte("ancestor"))
	live.Entries[0].Blob = remoteBlob
	workspace := Snapshot{Entries: map[string]ManifestEntry{"content/post.md": {Path: "content/post.md", Blob: localBlob, Size: 5, Mimetype: "text/markdown; charset=utf-8"}}, Blobs: map[Identifier][]byte{localBlob: []byte("local")}}

	liveID := NewIdentifier([]byte("live manifest"))
	changeset, err := (Repository{}).Reconcile(base, live, liveID, workspace)
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
	config := &ScanConfig{Include: Rules{Paths: []string{"content/**"}}}
	base := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Config: config, Entries: []ManifestEntry{{Path: "content/a.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"}}}
	live := Manifest{Version: Version, SyncPath: base.SyncPath, Config: config, Entries: append([]ManifestEntry(nil), base.Entries...)}
	workspace := Snapshot{Entries: map[string]ManifestEntry{
		"content/a.md": {Path: "content/a.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"},
		"content/b.md": {Path: "content/b.md", Blob: sharedBlob, Size: 6, Mimetype: "text/markdown; charset=utf-8"},
	}, Blobs: map[Identifier][]byte{sharedBlob: []byte("shared")}}

	changeset, err := (Repository{}).Reconcile(base, live, NewIdentifier([]byte("live manifest")), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(changeset.Blobs) != 0 {
		t.Fatalf("required blobs = %#v, want none", changeset.Blobs)
	}
}

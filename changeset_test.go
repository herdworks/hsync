package hypersync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChangesetApplyInitializesPendingIndex(t *testing.T) {
	t.Parallel()
	build := t.TempDir()
	blob := []byte("post")
	manifestRaw, manifestID, err := SerializeManifest(Manifest{Version: Version, SyncPath: "/.hypersync/revision.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := completeChangeset(Changeset{Manifest: manifestRaw, ManifestID: manifestID, Blobs: map[Identifier][]byte{NewIdentifier(blob): blob}, syncPath: "/.hypersync/revision.json"}, nil, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := changeset.Apply(Payload{Root: build}); err != nil {
		t.Fatal(err)
	}
	pending, err := ParseIndex(changeset.PendingIndex)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Published != "" || pending.Pending != changeset.ManifestID {
		t.Fatalf("initial pending = %#v", pending)
	}
	if _, err := os.Stat(filepath.Join(build, ".deployment", "expected.json")); !os.IsNotExist(err) {
		t.Fatalf("expected initial expected.json to be absent, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(build, ".hypersync", "manifests", changeset.ManifestID.Filename()+".json")); err != nil {
		t.Fatal(err)
	}
	for filename, want := range map[string][]byte{
		filepath.Join(".hypersync", "manifests", changeset.ManifestID.Filename()+".json"): changeset.Manifest,
		filepath.Join(".deployment", "pending.json"):                                      changeset.PendingIndex,
		filepath.Join(".hypersync", "revision.json"):                                      changeset.CleanIndex,
		".deployment.json": changeset.Instructions,
	} {
		got, err := os.ReadFile(filepath.Join(build, filename))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s bytes = %q, want %q", filename, got, want)
		}
	}
	gotBlob, err := os.ReadFile(filepath.Join(build, ".hypersync", "blobs", NewIdentifier(blob).Filename()))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBlob) != string(blob) {
		t.Fatalf("blob bytes = %q, want %q", gotBlob, blob)
	}
}

func TestChangesetApplyRetainsObservedIndexBytes(t *testing.T) {
	t.Parallel()
	build := t.TempDir()
	old := NewIdentifier([]byte("old"))
	observed := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + old + `","history":["` + old + `"]}`)
	manifestRaw, manifestID, err := SerializeManifest(Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := completeChangeset(Changeset{Manifest: manifestRaw, ManifestID: manifestID, Blobs: map[Identifier][]byte{}, syncPath: "/.sync/latest.json"}, observed, time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := changeset.Apply(Payload{Root: build}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(build, ".deployment", "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(observed) {
		t.Fatalf("expected bytes changed: %q != %q", got, observed)
	}
	if string(changeset.ExpectedIndex) != string(observed) {
		t.Fatalf("changeset expected bytes changed: %q != %q", changeset.ExpectedIndex, observed)
	}
}

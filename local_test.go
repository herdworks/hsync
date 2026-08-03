package hsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitializeAndLoadLocalManifest(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	repository := Repository{Root: workspace}
	raw, id, err := repository.Initialize("/.hsync/revision.json")
	if err != nil {
		t.Fatal(err)
	}
	loadedRaw, loadedID, manifest, err := repository.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if string(loadedRaw) != string(raw) || loadedID != id || manifest.SyncPath != "/.hsync/revision.json" {
		t.Fatalf("loaded local base differs: %q %s %#v", loadedRaw, loadedID, manifest)
	}
	if _, err := os.Stat(filepath.Join(workspace, LocalManifestPath)); err != nil {
		t.Fatalf("local manifest was not written to the standard path: %v", err)
	}
	_, _, err = repository.Initialize("/.hsync/revision.json")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("second initialization error = %v, want os.ErrExist", err)
	}
}

func TestFinalizePreservesCandidateBytes(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, LocalManifestPath)
	repository := Repository{Root: workspace}
	candidateRaw, candidateID, err := SerializeManifest(Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + candidateID + `","history":["` + candidateID + `"]}`)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repository.Finalize(Changeset{Manifest: candidateRaw, ManifestID: candidateID}, indexRaw, time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(candidateRaw) {
		t.Fatalf("final manifest bytes = %q, want %q", got, candidateRaw)
	}
}

func TestFinalizeRejectsDifferentPublishedCandidate(t *testing.T) {
	t.Parallel()
	repository := Repository{}
	raw, id, err := SerializeManifest(Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	other := NewIdentifier([]byte("other"))
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + other + `","history":["` + other + `"]}`)
	err = repository.Finalize(Changeset{Manifest: raw, ManifestID: id}, indexRaw, time.Now())
	if !errors.Is(err, ErrManualResolution) {
		t.Fatalf("Finalize() error = %v, want manual resolution", err)
	}
}

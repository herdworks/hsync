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
	initialized, err := repository.Initialize("/.hsync/revision.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := repository.Base
	if string(manifest.Raw()) != string(initialized.Raw()) || manifest.ID() != initialized.ID() || manifest.IndexPath() != "/.hsync/revision.json" {
		t.Fatalf("loaded local base differs: %#v", manifest)
	}
	if _, err := os.Stat(filepath.Join(workspace, LocalManifestPath)); err != nil {
		t.Fatalf("local manifest was not written to the standard path: %v", err)
	}
	_, err = repository.Initialize("/.hsync/revision.json")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("second initialization error = %v, want os.ErrExist", err)
	}
}

func TestLoadBaseCachesAndRefreshBaseReplaces(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	repository := Repository{Root: workspace}
	base, err := repository.Initialize("/.hsync/revision.json")
	if err != nil {
		t.Fatal(err)
	}
	head, err := NewSealedManifest("/.hsync/revision.json", base.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, LocalManifestPath), head.Raw(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repository.LoadBase(); err != nil {
		t.Fatal(err)
	}
	if repository.Base.ID() != base.ID() {
		t.Fatalf("LoadBase replaced cached base %s with %s", base.ID(), repository.Base.ID())
	}
	if _, err := repository.RefreshBase(); err != nil {
		t.Fatal(err)
	}
	if repository.Base.ID() != head.ID() {
		t.Fatalf("RefreshBase left cached base %s, want %s", repository.Base.ID(), head.ID())
	}
}

func TestFinalizePreservesCandidateBytes(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, LocalManifestPath)
	repository := Repository{Root: workspace}
	candidateRaw, candidateID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + candidateID + `","history":["` + candidateID + `"]}`)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repository.Finalize(Changeset{Manifest: parseTestManifest(t, candidateRaw, candidateID)}, indexRaw, time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(candidateRaw) {
		t.Fatalf("final manifest bytes = %q, want %q", got, candidateRaw)
	}
	if repository.Base.ID() != candidateID || string(repository.Base.Raw()) != string(candidateRaw) {
		t.Fatalf("repository base = %#v, want finalized candidate", repository.Base)
	}
}

func TestFinalizeRejectsDifferentPublishedCandidate(t *testing.T) {
	t.Parallel()
	repository := Repository{}
	raw, id, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	other := NewIdentifier([]byte("other"))
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + other + `","history":["` + other + `"]}`)
	err = repository.Finalize(Changeset{Manifest: parseTestManifest(t, raw, id)}, indexRaw, time.Now())
	if !errors.Is(err, ErrManualResolution) {
		t.Fatalf("Finalize() error = %v, want manual resolution", err)
	}
}

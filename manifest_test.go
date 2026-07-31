package hypersync

import (
	"errors"
	"testing"
)

func TestSerializeManifestUsesExactBytes(t *testing.T) {
	t.Parallel()

	manifest := Manifest{Version: Version, SyncPath: "/.hypersync/revision.json", Entries: []ManifestEntry{}}
	raw, id, err := SerializeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseManifest(raw, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncPath != manifest.SyncPath {
		t.Fatalf("SyncPath = %q, want %q", got.SyncPath, manifest.SyncPath)
	}
}

func TestParseManifestRejectsMismatchedBytes(t *testing.T) {
	t.Parallel()

	_, err := ParseManifest([]byte(`{"version":1,"sync_path":"/.sync/latest.json","entries":[]}`), NewIdentifier([]byte("other")))
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ParseManifest() error = %v, want invalid state", err)
	}
}

func TestParseManifestRejectsTrailingValue(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"version":1,"sync_path":"/.sync/latest.json","entries":[]} {}`)
	if _, err := ParseManifest(raw, NewIdentifier(raw)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ParseManifest() error = %v, want invalid state", err)
	}
}

func TestManifestValidateRejectsNonconformingEntry(t *testing.T) {
	t.Parallel()

	manifest := Manifest{
		Version: Version, SyncPath: "/.sync/latest.json",
		Config:  &ScanConfig{Include: Rules{Paths: []string{"content/**"}}},
		Entries: []ManifestEntry{{Path: "data/site.yaml", Blob: NewIdentifier([]byte("site")), Size: 4, Mimetype: "application/yaml"}},
	}
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Validate() error = %v, want invalid state", err)
	}
}

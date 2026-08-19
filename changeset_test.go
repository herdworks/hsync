package hsync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompleteChangesetRejectsUnsealedManifest(t *testing.T) {
	t.Parallel()

	_, err := completeChangeset(Changeset{Manifest: NewManifest("/.hsync/revision.json", "", nil)}, nil, time.Now())
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("completeChangeset() error = %v, want invalid state", err)
	}
}

func TestChangesetApplyInitializesPendingIndex(t *testing.T) {
	t.Parallel()
	build := t.TempDir()
	blob := []byte("post")
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.hsync/revision.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := completeChangeset(Changeset{Manifest: parseTestManifest(t, manifestRaw, manifestID), Blobs: map[Identifier][]byte{NewIdentifier(blob): blob}}, nil, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := changeset.Apply(build); err != nil {
		t.Fatal(err)
	}
	pending, err := ParseIndex(changeset.PendingIndex)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Published != "" || pending.Pending != changeset.Manifest.ID() {
		t.Fatalf("initial pending = %#v", pending)
	}
	if _, err := os.Stat(filepath.Join(build, ".deployment", "expected.json")); !os.IsNotExist(err) {
		t.Fatalf("expected initial expected.json to be absent, err=%v", err)
	}
	manifestFilename, err := changeset.Manifest.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(build, ".hsync", "manifests", manifestFilename)); err != nil {
		t.Fatal(err)
	}
	for filename, want := range map[string][]byte{
		filepath.Join(".hsync", "manifests", manifestFilename): changeset.Manifest.Raw(),
		filepath.Join(".deployment", "pending.json"):           changeset.PendingIndex,
		filepath.Join(".hsync", "revision.json"):               changeset.CleanIndex,
		".deployment.json":                                     changeset.Instructions,
	} {
		got, err := os.ReadFile(filepath.Join(build, filename))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s bytes = %q, want %q", filename, got, want)
		}
	}
	gotBlob, err := os.ReadFile(filepath.Join(build, ".hsync", "blobs", NewIdentifier(blob).Filename()))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBlob) != string(blob) {
		t.Fatalf("blob bytes = %q, want %q", gotBlob, blob)
	}
	var instructions struct {
		Ignore []string `json:"ignore"`
	}
	if err := json.Unmarshal(changeset.Instructions, &instructions); err != nil {
		t.Fatal(err)
	}
	if len(instructions.Ignore) != 1 || instructions.Ignore[0] != ".deployment/" {
		t.Fatalf("instructions ignore = %#v, want [.deployment/]", instructions.Ignore)
	}
}

func TestChangesetApplyRetainsObservedIndexBytes(t *testing.T) {
	t.Parallel()
	build := t.TempDir()
	old := NewIdentifier([]byte("old"))
	observed := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + old + `","history":["` + old + `"]}`)
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	changeset, err := completeChangeset(Changeset{Manifest: parseTestManifest(t, manifestRaw, manifestID), Blobs: map[Identifier][]byte{}}, observed, time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := changeset.Apply(build); err != nil {
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

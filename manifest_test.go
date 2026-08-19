package hsync

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func sealManifest(manifest Manifest) ([]byte, Identifier, error) {
	if err := manifest.Seal(); err != nil {
		return nil, "", err
	}
	return manifest.Raw(), manifest.ID(), nil
}

func parseTestManifest(t *testing.T, raw []byte, id Identifier) Manifest {
	t.Helper()
	manifest, err := ParseManifest(raw, id)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestManifestSealUsesExactBytes(t *testing.T) {
	t.Parallel()

	manifest := NewManifest("/.hsync/revision.json", "", nil)
	if err := manifest.Seal(); err != nil {
		t.Fatal(err)
	}
	raw, id := manifest.Raw(), manifest.ID()
	got, err := ParseManifest(raw, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.IndexPath() != manifest.IndexPath() {
		t.Fatalf("IndexPath = %q, want %q", got.IndexPath(), manifest.IndexPath())
	}
}

func TestNewSealedManifest(t *testing.T) {
	t.Parallel()

	manifest, err := NewSealedManifest("/.hsync/revision.json", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Sealed() || manifest.ID() == "" || manifest.Raw() == nil {
		t.Fatal("NewSealedManifest returned an unsealed manifest")
	}
	filename, err := manifest.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if filename != manifest.ID().Filename()+".json" {
		t.Fatalf("Filename() = %q", filename)
	}
}

func TestManifestJSON(t *testing.T) {
	t.Parallel()

	entries := []ManifestEntry{{Path: "content/page.txt", Blob: NewIdentifier([]byte("page")), Size: 4, Mimetype: "text/plain"}}
	manifest := NewManifest("/.hsync/revision.json", NewIdentifier([]byte("parent")), entries)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"index_path"`) || strings.Contains(string(raw), `"sync_path"`) {
		t.Fatalf("manifest JSON uses unexpected index field: %s", raw)
	}
	var decoded Manifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID() != "" || decoded.Raw() != nil || decoded.Parent() != manifest.Parent() || decoded.IndexPath() != manifest.IndexPath() || len(decoded.Entries()) != 1 {
		t.Fatalf("decoded manifest = %#v", decoded)
	}
	if err := manifest.Seal(); err != nil {
		t.Fatal(err)
	}
	if string(manifest.Raw()) != string(raw) {
		t.Fatal("Seal did not retain MarshalJSON output")
	}
}

func TestManifestSealingProtectsState(t *testing.T) {
	t.Parallel()

	entries := []ManifestEntry{{Path: "content/page.txt", Blob: NewIdentifier([]byte("page")), Size: 4, Mimetype: "text/plain"}}
	manifest := NewManifest("/.sync/latest.json", "", entries)
	if manifest.Sealed() || manifest.ID() != "" || manifest.Raw() != nil {
		t.Fatalf("unsealed manifest retained serialized state")
	}
	if _, err := manifest.Filename(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Filename() error = %v, want sealed state error", err)
	}
	entries[0].Path = "changed.txt"
	if manifest.Entries()[0].Path != "content/page.txt" {
		t.Fatal("NewManifest did not copy entries")
	}
	if err := manifest.Seal(); err != nil {
		t.Fatal(err)
	}
	raw, id := manifest.Raw(), manifest.ID()
	returnedEntries := manifest.Entries()
	returnedEntries[0].Path = "changed.txt"
	returnedRaw := manifest.Raw()
	returnedRaw[0] = 'x'
	if manifest.Entries()[0].Path != "content/page.txt" || string(manifest.Raw()) != string(raw) || manifest.ID() != id {
		t.Fatal("sealed manifest exposed mutable state")
	}
	if err := manifest.SetParent(NewIdentifier([]byte("parent"))); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("SetParent() error = %v, want sealed state error", err)
	}
	if err := manifest.SetIndexPath("/.sync/other.json"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("SetIndexPath() error = %v, want sealed state error", err)
	}
	if err := manifest.SetEntries(nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("SetEntries() error = %v, want sealed state error", err)
	}
}

func TestParseManifestRetainsExactRawBytes(t *testing.T) {
	t.Parallel()

	raw := []byte("{\n  \"version\": 1, \"index_path\": \"/.sync/latest.json\", \"entries\": []\n}")
	manifest, err := ParseManifest(raw, NewIdentifier(raw))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID() != NewIdentifier(raw) || string(manifest.Raw()) != string(raw) {
		t.Fatal("parsed manifest did not retain its exact bytes and identifier")
	}
}

func TestParseManifestRejectsMismatchedBytes(t *testing.T) {
	t.Parallel()

	_, err := ParseManifest([]byte(`{"version":1,"index_path":"/.sync/latest.json","entries":[]}`), NewIdentifier([]byte("other")))
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ParseManifest() error = %v, want invalid state", err)
	}
}

func TestParseManifestRejectsTrailingValue(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"version":1,"index_path":"/.sync/latest.json","entries":[]} {}`)
	if _, err := ParseManifest(raw, NewIdentifier(raw)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ParseManifest() error = %v, want invalid state", err)
	}
}

func TestParseManifestRejectsSyncPath(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"version":1,"sync_path":"/.sync/latest.json","entries":[]}`)
	if _, err := ParseManifest(raw, NewIdentifier(raw)); err == nil {
		t.Fatal("ParseManifest accepted obsolete sync_path field")
	}
}

func TestManifestValidationDoesNotUseExternalSyncRules(t *testing.T) {
	t.Parallel()

	manifest := NewManifest("/.sync/latest.json", "", []ManifestEntry{{Path: "data/site.yaml", Blob: NewIdentifier([]byte("site")), Size: 4, Mimetype: "application/yaml"}})
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

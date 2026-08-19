package hsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type memoryManifestReader struct{ values map[Identifier]Manifest }

func testRemote(t *testing.T, base, indexPath string) Remote {
	t.Helper()
	indexURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	indexURL.Path = indexPath
	indexURL.RawPath = ""
	return Remote{IndexURL: indexURL}
}

func (reader memoryManifestReader) ReadManifest(_ context.Context, id Identifier) (Manifest, error) {
	return reader.values[id], nil
}

func TestRemoteReadsAndVerifiesManifest(t *testing.T) {
	t.Parallel()
	raw, id, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.sync/manifests/"+manifestFilename(id) {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(raw)
	}))
	defer server.Close()
	reader := testRemote(t, server.URL, "/.sync/latest.json")
	got, err := reader.ReadManifest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Raw()) != string(raw) || got.ID() != id {
		t.Fatalf("manifest changed: %#v", got)
	}
}

func TestRemoteRefreshHead(t *testing.T) {
	t.Parallel()

	head, err := NewSealedManifest("/.sync/latest.json", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: head.ID(), History: []Identifier{head.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(index)
		case "/.sync/manifests/" + manifestFilename(head.ID()):
			_, _ = writer.Write(head.Raw())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	remote := testRemote(t, server.URL, "/.sync/latest.json")
	if err := remote.RefreshHead(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remote.Head.ID() != head.ID() || string(remote.Head.Raw()) != string(head.Raw()) {
		t.Fatalf("remote head = %#v, want %#v", remote.Head, head)
	}
}

func TestRemoteTreatsIndexPathAsHostAbsolute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.sync/latest.json" {
			t.Fatalf("request path = %q, want host-absolute index path", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"sha256:0000000000000000000000000000000000000000000000000000000000000000","history":["sha256:0000000000000000000000000000000000000000000000000000000000000000"]}`))
	}))
	defer server.Close()
	reader := testRemote(t, server.URL+"/blog/", "/.sync/latest.json")
	if _, err := reader.ReadIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteReadsAndVerifiesBlob(t *testing.T) {
	contents := []byte("blob contents")
	id := NewIdentifier(contents)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.sync/blobs/"+id.Filename() {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(contents)
	}))
	defer server.Close()
	reader := testRemote(t, server.URL, "/.sync/latest.json")
	got, err := reader.ReadBlob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("blob = %q, want %q", got, contents)
	}
}

func TestReadLineage(t *testing.T) {
	t.Parallel()
	baseRaw, baseID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	headRaw, headID, err := sealManifest(Manifest{version: ManifestVersion, parent: baseID, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	reader := memoryManifestReader{values: map[Identifier]Manifest{baseID: parseTestManifest(t, baseRaw, baseID), headID: parseTestManifest(t, headRaw, headID)}}
	lineage, err := ReadLineage(context.Background(), reader, baseID, headID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage.Revisions) != 1 || lineage.Revisions[0].Parent() != baseID {
		t.Fatalf("lineage = %#v", lineage)
	}
	if _, err := ReadLineage(context.Background(), reader, NewIdentifier([]byte("other")), headID); !errors.Is(err, ErrManualResolution) {
		t.Fatalf("ReadLineage() error = %v, want manual resolution", err)
	}
}

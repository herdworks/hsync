package hsync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type memoryManifestReader struct{ values map[Identifier][]byte }

func (reader memoryManifestReader) ReadManifest(_ context.Context, id Identifier) ([]byte, error) {
	return reader.values[id], nil
}

func TestRemoteReadsAndVerifiesManifest(t *testing.T) {
	t.Parallel()
	raw, id, err := SerializeManifest(Manifest{Version: ManifestVersion, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.sync/manifests/"+id.Filename()+".json" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(raw)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	reader := Remote{BaseURL: base, SyncPath: "/.sync/latest.json"}
	got, err := reader.ReadManifest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("manifest changed: %q != %q", got, raw)
	}
}

func TestRemoteTreatsSyncPathAsHostAbsolute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.sync/latest.json" {
			t.Fatalf("request path = %q, want host-absolute sync path", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"sha256:0000000000000000000000000000000000000000000000000000000000000000","history":["sha256:0000000000000000000000000000000000000000000000000000000000000000"]}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/blog/")
	reader := Remote{BaseURL: base, SyncPath: "/.sync/latest.json"}
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
	base, _ := url.Parse(server.URL)
	reader := Remote{BaseURL: base, SyncPath: "/.sync/latest.json"}
	got, err := reader.ReadBlob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("blob = %q, want %q", got, contents)
	}
}

func TestReadContinuation(t *testing.T) {
	t.Parallel()
	baseRaw, baseID, err := SerializeManifest(Manifest{Version: ManifestVersion, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	liveRaw, liveID, err := SerializeManifest(Manifest{Version: ManifestVersion, Parent: baseID, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	reader := memoryManifestReader{values: map[Identifier][]byte{baseID: baseRaw, liveID: liveRaw}}
	continuation, err := ReadContinuation(context.Background(), reader, baseID, liveID)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuation.Revisions) != 1 || continuation.Revisions[0].Parent != baseID {
		t.Fatalf("continuation = %#v", continuation)
	}
	if _, err := ReadContinuation(context.Background(), reader, NewIdentifier([]byte("other")), liveID); !errors.Is(err, ErrManualResolution) {
		t.Fatalf("ReadContinuation() error = %v, want manual resolution", err)
	}
}

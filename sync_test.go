package hypersync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareInitialDeployment(t *testing.T) {
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, ".sync", "manifest.json")
	build := filepath.Join(workspace, "builds", "first")
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("post"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "stale.html"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	repository := Repository{Root: workspace, ManifestPath: manifestPath}
	if _, _, err := repository.Initialize("/.sync/latest.json", ScanConfig{Include: Rules{Paths: []string{"**"}}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	base, _ := url.Parse(server.URL)
	sync := &Sync{Repository: repository, Payload: Payload{Root: build}, Remote: Remote{BaseURL: base, SyncPath: "/.sync/latest.json"}, Now: func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) }}
	if err := sync.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(sync.Changeset.Manifest, sync.Changeset.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Parent != "" || len(manifest.Entries) != 1 || manifest.Entries[0].Path != "content/post.md" {
		t.Fatalf("changeset manifest = %#v", manifest)
	}
	if sync.Status.Status != StatusPrepared {
		t.Fatalf("status = %q", sync.Status.Status)
	}
	if _, err := os.Stat(filepath.Join(build, ".sync", "latest.json")); err != nil {
		t.Fatalf("clean index: %v", err)
	}
}

func TestPrepareContinuationUsesExactObservedIndex(t *testing.T) {
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, ".sync", "manifest.json")
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("local change"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseManifest := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Config: &ScanConfig{Include: Rules{Paths: []string{"content/**"}}}, Entries: []ManifestEntry{}}
	baseRaw, _, err := SerializeManifest(baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	liveRaw, liveID, err := SerializeManifest(baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + liveID + `","history":["` + liveID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + liveID.Filename() + ".json":
			_, _ = writer.Write(liveRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL)
	sync := &Sync{Repository: Repository{Root: workspace, ManifestPath: manifestPath}, Payload: Payload{Root: build}, Remote: Remote{BaseURL: baseURL, SyncPath: "/.sync/latest.json"}, Now: func() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }}
	if err := sync.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(sync.Changeset.Manifest, sync.Changeset.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Parent != liveID {
		t.Fatalf("changeset parent = %s, want %s", manifest.Parent, liveID)
	}
	expectedRaw, err := os.ReadFile(filepath.Join(build, ".deployment", "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(expectedRaw) != string(indexRaw) {
		t.Fatalf("expected index bytes changed: %q != %q", expectedRaw, indexRaw)
	}
}

func TestPrepareSkipsLiveEquivalentCandidate(t *testing.T) {
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, ".sync", "manifest.json")
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("post"), 0o644); err != nil {
		t.Fatal(err)
	}
	blob := NewIdentifier([]byte("post"))
	live := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Config: &ScanConfig{Include: Rules{Paths: []string{"content/**"}}}, Entries: []ManifestEntry{{Path: "content/post.md", Blob: blob, Size: 4, Mimetype: "text/markdown; charset=utf-8"}}}
	liveRaw, liveID, err := SerializeManifest(live)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, liveRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + liveID + `","history":["` + liveID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + liveID.Filename() + ".json":
			_, _ = writer.Write(liveRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL)
	sync := &Sync{Repository: Repository{Root: workspace, ManifestPath: manifestPath}, Payload: Payload{Root: build}, Remote: Remote{BaseURL: baseURL, SyncPath: "/.sync/latest.json"}, Now: func() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }}
	if err := sync.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sync.Changeset != nil || sync.Status.Status != StatusNoop {
		t.Fatalf("unexpected sync outcome: changeset=%#v status=%#v", sync.Changeset, sync.Status)
	}
	if _, err := os.Stat(filepath.Join(build, ".deployment.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected deployment instructions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(build, ".sync", "latest.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected synchronization index artifact: %v", err)
	}
}

func TestPrepareFastForwardsLiveEquivalentCandidate(t *testing.T) {
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, ".sync", "manifest.json")
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("post"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := &ScanConfig{Include: Rules{Paths: []string{"content/**"}}}
	baseRaw, baseID, err := SerializeManifest(Manifest{Version: Version, SyncPath: "/.sync/latest.json", Config: config, Entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	blob := NewIdentifier([]byte("post"))
	live := Manifest{Version: Version, Parent: baseID, SyncPath: "/.sync/latest.json", Config: config, Entries: []ManifestEntry{{Path: "content/post.md", Blob: blob, Size: 4, Mimetype: "text/plain; charset=utf-8"}}}
	liveRaw, liveID, err := SerializeManifest(live)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + liveID + `","history":["` + liveID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + liveID.Filename() + ".json":
			_, _ = writer.Write(liveRaw)
		case "/.sync/manifests/" + baseID.Filename() + ".json":
			_, _ = writer.Write(baseRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL)
	sync := &Sync{Repository: Repository{Root: workspace, ManifestPath: manifestPath}, Payload: Payload{Root: build}, Remote: Remote{BaseURL: baseURL, SyncPath: "/.sync/latest.json"}, Now: func() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }}
	if err := sync.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sync.Changeset != nil || sync.Status.Status != StatusAdopted {
		t.Fatalf("unexpected sync outcome: changeset=%#v status=%#v", sync.Changeset, sync.Status)
	}
	localRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(localRaw) != string(liveRaw) {
		t.Fatalf("local manifest bytes = %q, want exact live bytes %q", localRaw, liveRaw)
	}
}

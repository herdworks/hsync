package hsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDownloadDirectAndDiscoveredIndex(t *testing.T) {
	contents := []byte("hello from sync")
	blob := NewIdentifier(contents)
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "data/message.txt", Blob: blob, Size: int64(len(contents)), Mimetype: "text/plain; charset=utf-8"}}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: manifestID, History: []Identifier{manifestID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		case "/.sync/blobs/" + blob.Filename():
			_, _ = w.Write(contents)
		case "/":
			_, _ = w.Write([]byte(`<link rel="https://hsync.sh" href="/.sync/latest.json">`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, source := range []string{server.URL + "/.sync/latest.json", server.URL + "/"} {
		t.Run(source, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "checkout")
			u, err := url.Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			result, err := Download(context.Background(), u, destination, server.Client(), &output)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "downloaded" || result.Remote.Head.ID() != manifestID {
				t.Fatalf("result = %#v", result)
			}
			if result.Remote.IndexURL == nil || result.Remote.IndexURL.String() != server.URL+"/.sync/latest.json" {
				t.Fatalf("index URL = %v", result.Remote.IndexURL)
			}
			lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
			wantOutput := []string{
				"downloading 1 files (0.0 KB)",
				"file data/message.txt downloading",
				"file data/message.txt downloaded",
			}
			if len(lines) != len(wantOutput)+1 || !reflect.DeepEqual(lines[:len(wantOutput)], wantOutput) || !regexp.MustCompile(`^downloaded 1 files in [0-9]+\.[0-9]{3} seconds$`).MatchString(lines[3]) {
				t.Fatalf("output = %q", output.String())
			}
			got, err := os.ReadFile(filepath.Join(destination, "data", "message.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(contents) {
				t.Fatalf("file = %q, want %q", got, contents)
			}
			if _, err := os.Stat(filepath.Join(destination, ".hsync", "manifest.json")); err != nil {
				t.Fatal(err)
			}
			temporaryEntries, err := os.ReadDir(filepath.Join(destination, ".hsync", "tmp"))
			if err != nil {
				t.Fatal(err)
			}
			if len(temporaryEntries) != 0 {
				t.Fatalf("download left temporary jobs: %#v", temporaryEntries)
			}
			if _, err := os.Stat(filepath.Join(destination, ".hsync", "remote.json")); !os.IsNotExist(err) {
				t.Fatalf("download created remote metadata: %v", err)
			}
		})
	}
}

func TestDownloadRootPrefersStandardIndexAndDoesNotFallbackFromExplicitPath(t *testing.T) {
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: DefaultIndexPath, entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: manifestID, History: []Identifier{manifestID}})
	if err != nil {
		t.Fatal(err)
	}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case DefaultIndexPath:
			_, _ = w.Write(indexRaw)
		case "/.hsync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		case "/":
			_, _ = w.Write([]byte(`<link rel="https://hsync.sh" href="/custom/index.json">`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Download(context.Background(), root, filepath.Join(t.TempDir(), "checkout"), server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Remote.IndexURL.Path != DefaultIndexPath {
		t.Fatalf("root download index = %q, want %q", result.Remote.IndexURL.Path, DefaultIndexPath)
	}
	for _, request := range requests {
		if request == "/" {
			t.Fatalf("root download unexpectedly scraped HTML: %v", requests)
		}
	}

	explicit, err := url.Parse(server.URL + "/missing/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), explicit, filepath.Join(t.TempDir(), "missing"), server.Client(), nil); err == nil {
		t.Fatal("Download() error = nil for an unavailable explicit index")
	}
}

func TestDownloadFetchesBlobsConcurrently(t *testing.T) {
	first := []byte("first")
	second := []byte("second")
	firstID, secondID := NewIdentifier(first), NewIdentifier(second)
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{
		{Path: "first.txt", Blob: firstID, Size: int64(len(first)), Mimetype: "text/plain"},
		{Path: "second.txt", Blob: secondID, Size: int64(len(second)), Mimetype: "text/plain"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: manifestID, History: []Identifier{manifestID}})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBlobs := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBlobs()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		case "/.sync/blobs/" + firstID.Filename(), "/.sync/blobs/" + secondID.Filename():
			started <- struct{}{}
			<-release
			if r.URL.Path == "/.sync/blobs/"+firstID.Filename() {
				_, _ = w.Write(first)
			} else {
				_, _ = w.Write(second)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	source, err := url.Parse(server.URL + "/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 1)
	destination := filepath.Join(t.TempDir(), "checkout")
	go func() {
		_, err := Download(context.Background(), source, destination, server.Client(), nil)
		results <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("blob downloads did not overlap")
		}
	}
	releaseBlobs()
	if err := <-results; err != nil {
		t.Fatal(err)
	}
}

func TestDownloadPublishesIntoExistingEmptyDirectory(t *testing.T) {
	contents := []byte("published")
	blob := NewIdentifier(contents)
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/page.txt", Blob: blob, Size: int64(len(contents)), Mimetype: "text/plain"}}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: manifestID, History: []Identifier{manifestID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		case "/.sync/blobs/" + blob.Filename():
			_, _ = w.Write(contents)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := url.Parse(server.URL + "/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), source, destination, server.Client(), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "content", "page.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("downloaded contents = %q, want %q", got, contents)
	}
}

func TestDownloadPublishesIntoCurrentDirectory(t *testing.T) {
	contents := []byte("published")
	blob := NewIdentifier(contents)
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/page.txt", Blob: blob, Size: int64(len(contents)), Mimetype: "text/plain"}}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: manifestID, History: []Identifier{manifestID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		case "/.sync/blobs/" + blob.Filename():
			_, _ = w.Write(contents)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	workingDirectory := t.TempDir()
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	source, err := url.Parse(server.URL + "/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), source, ".", server.Client(), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("content", "page.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("downloaded contents = %q, want %q", got, contents)
	}
}

func TestSafeWriteTargetCreatesSharedParentsConcurrently(t *testing.T) {
	const workers = 8
	for range 20 {
		root := t.TempDir()
		start := make(chan struct{})
		errors := make(chan error, workers)
		var group sync.WaitGroup
		for index := range workers {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				_, err := safeWriteTarget(root, fmt.Sprintf("content/%d.txt", index))
				errors <- err
			}()
		}
		close(start)
		group.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDownloadFailureLeavesExistingEmptyDirectoryUntouched(t *testing.T) {
	contents := []byte("incorrect size")
	blob := NewIdentifier(contents)
	manifestRaw, manifestID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/page.txt", Blob: blob, Size: int64(len(contents) + 1), Mimetype: "text/plain"}}})
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: manifestID, History: []Identifier{manifestID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		case "/.sync/blobs/" + blob.Filename():
			_, _ = w.Write(contents)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := url.Parse(server.URL + "/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), source, destination, server.Client(), nil); err == nil {
		t.Fatal("Download() error = nil for blob size mismatch")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".hsync" {
		t.Fatalf("failed download populated destination: %#v", entries)
	}
	temporaryEntries, err := os.ReadDir(filepath.Join(destination, ".hsync", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryEntries) != 0 {
		t.Fatalf("failed download left temporary jobs: %#v", temporaryEntries)
	}
	if _, err := (Repository{Root: destination}).prepare("verify-clean-workspace"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRepositoryLoadsOnlyLocalManifest(t *testing.T) {
	directory := t.TempDir()
	manifestRaw, _, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(directory, ".hsync")
	if err := os.Mkdir(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), manifestRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRepository(directory); err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	if _, _, err := openConfiguredRepository(directory, SyncConfig{}, nil); err == nil {
		t.Fatal("openConfiguredRepository() error = nil without sync.remote")
	}
}

package hsync

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRepositoryResetRestoresTrackedFilesAndPreservesUntracked(t *testing.T) {
	checkout := t.TempDir()
	contents := []byte("restored")
	entry := ManifestEntry{Path: "content/tracked.txt", Blob: NewIdentifier(contents), Size: int64(len(contents)), Mimetype: "text/plain"}
	repository := writeResetManifest(t, checkout, []ManifestEntry{entry})
	writeResetTestFile(t, checkout, entry.Path, []byte("modified"))
	writeResetTestFile(t, checkout, "notes/untracked.txt", []byte("keep"))
	remote, requests, closeServer := resetRemote(t, map[Identifier][]byte{entry.Blob: contents})
	defer closeServer()
	var output bytes.Buffer
	result, err := repository.Reset(context.Background(), remote, ResetOptions{Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "reset" || result.Files != 1 {
		t.Fatalf("result = %#v", result)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "content", "tracked.txt")); err != nil || string(got) != "restored" {
		t.Fatalf("tracked file = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "notes", "untracked.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("untracked file = %q, err=%v", got, err)
	}
	if len(*requests) != 1 || !strings.Contains((*requests)[0], "/blobs/") {
		t.Fatalf("requests = %#v, want one blob request", *requests)
	}
	if !strings.Contains(output.String(), "downloading 1 files") || !strings.Contains(output.String(), "file content/tracked.txt downloaded") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRepositoryResetInteractiveDeclinesAndAcceptsInPathOrder(t *testing.T) {
	checkout := t.TempDir()
	first, second := []byte("first"), []byte("second")
	entries := []ManifestEntry{
		{Path: "z.txt", Blob: NewIdentifier(second), Size: int64(len(second)), Mimetype: "text/plain"},
		{Path: "a.txt", Blob: NewIdentifier(first), Size: int64(len(first)), Mimetype: "text/plain"},
	}
	repository := writeResetManifest(t, checkout, entries)
	writeResetTestFile(t, checkout, "a.txt", []byte("old-a"))
	writeResetTestFile(t, checkout, "z.txt", []byte("old-z"))
	remote, _, closeServer := resetRemote(t, map[Identifier][]byte{entries[0].Blob: second, entries[1].Blob: first})
	defer closeServer()
	var output bytes.Buffer
	_, err := repository.Reset(context.Background(), remote, ResetOptions{Interactive: true, Input: strings.NewReader("n\ny\n"), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(checkout, "a.txt")); string(got) != "old-a" {
		t.Fatalf("declined file = %q, want old-a", got)
	}
	if got, _ := os.ReadFile(filepath.Join(checkout, "z.txt")); string(got) != "second" {
		t.Fatalf("accepted file = %q, want second", got)
	}
	if !strings.Contains(output.String(), "file a.txt skipped") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRepositoryResetRejectsSymlinkWithoutChangingOtherFiles(t *testing.T) {
	checkout := t.TempDir()
	first, second := []byte("first"), []byte("second")
	entries := []ManifestEntry{
		{Path: "first.txt", Blob: NewIdentifier(first), Size: int64(len(first)), Mimetype: "text/plain"},
		{Path: "second.txt", Blob: NewIdentifier(second), Size: int64(len(second)), Mimetype: "text/plain"},
	}
	repository := writeResetManifest(t, checkout, entries)
	writeResetTestFile(t, checkout, "first.txt", []byte("old"))
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(checkout, "second.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	remote, _, closeServer := resetRemote(t, map[Identifier][]byte{entries[0].Blob: first, entries[1].Blob: second})
	defer closeServer()
	if _, err := repository.Reset(context.Background(), remote, ResetOptions{}); err == nil {
		t.Fatal("Reset() error = nil for symlink target")
	}
	if got, _ := os.ReadFile(filepath.Join(checkout, "first.txt")); string(got) != "old" {
		t.Fatalf("first file changed after unsafe reset: %q", got)
	}
	if got, _ := os.ReadFile(outside); string(got) != "outside" {
		t.Fatalf("symlink destination changed: %q", got)
	}
}

func TestRepositoryResetRejectsSymlinkAncestorBeforeCreatingDirectories(t *testing.T) {
	checkout, outside := t.TempDir(), t.TempDir()
	contents := []byte("restored")
	entry := ManifestEntry{Path: "content/tracked.txt", Blob: NewIdentifier(contents), Size: int64(len(contents)), Mimetype: "text/plain"}
	repository := writeResetManifest(t, checkout, []ManifestEntry{entry})
	if err := os.Symlink(outside, filepath.Join(checkout, "content")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	remote, _, closeServer := resetRemote(t, map[Identifier][]byte{entry.Blob: contents})
	defer closeServer()
	if _, err := repository.Reset(context.Background(), remote, ResetOptions{}); err == nil {
		t.Fatal("Reset() error = nil for symlink ancestor")
	}
	if _, err := os.Stat(filepath.Join(outside, "tracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("reset wrote through symlink ancestor: %v", err)
	}
}

func TestRepositoryResetVerificationFailureLeavesWorkspaceUntouched(t *testing.T) {
	checkout := t.TempDir()
	expected := []byte("expected")
	entry := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier(expected), Size: int64(len(expected)), Mimetype: "text/plain"}
	repository := writeResetManifest(t, checkout, []ManifestEntry{entry})
	writeResetTestFile(t, checkout, entry.Path, []byte("local"))
	remote, _, closeServer := resetRemote(t, map[Identifier][]byte{entry.Blob: []byte("wrong")})
	defer closeServer()
	if _, err := repository.Reset(context.Background(), remote, ResetOptions{}); err == nil {
		t.Fatal("Reset() error = nil for mismatched blob")
	}
	if got, _ := os.ReadFile(filepath.Join(checkout, entry.Path)); string(got) != "local" {
		t.Fatalf("tracked file changed after failed reset: %q", got)
	}
}

func TestRepositoryResetNoopDoesNotReadRemote(t *testing.T) {
	checkout := t.TempDir()
	contents := []byte("unchanged")
	entry := ManifestEntry{Path: "tracked.txt", Blob: NewIdentifier(contents), Size: int64(len(contents)), Mimetype: "text/plain"}
	repository := writeResetManifest(t, checkout, []ManifestEntry{entry})
	writeResetTestFile(t, checkout, entry.Path, contents)
	result, err := repository.Reset(context.Background(), Remote{}, ResetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "noop" || result.Files != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func writeResetManifest(t *testing.T, root string, entries []ManifestEntry) Repository {
	t.Helper()
	raw, _, err := SerializeManifest(Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(root, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(control, "manifest.json")
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return Repository{Root: root}
}

func writeResetTestFile(t *testing.T, root, relative string, contents []byte) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func resetRemote(t *testing.T, blobs map[Identifier][]byte) (Remote, *[]string, func()) {
	t.Helper()
	var mu sync.Mutex
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		for id, contents := range blobs {
			if r.URL.Path == "/.sync/blobs/"+id.Filename() {
				_, _ = w.Write(contents)
				return
			}
		}
		http.NotFound(w, r)
	}))
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return Remote{BaseURL: base, Client: server.Client(), SyncPath: "/.sync/latest.json"}, &requests, server.Close
}

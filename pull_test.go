package hsync

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPullUsesConcurrentDownloadProgress(t *testing.T) {
	checkout := t.TempDir()
	before, after := []byte("before"), []byte("after")
	base := Manifest{Version: ManifestVersion, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(before), Size: int64(len(before)), Mimetype: "text/plain"}}}
	baseRaw, baseID, err := SerializeManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	live := Manifest{Version: ManifestVersion, Parent: baseID, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(after), Size: int64(len(after)), Mimetype: "text/plain"}}}
	liveRaw, liveID, err := SerializeManifest(live)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: liveID, History: []Identifier{liveID, baseID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + liveID.Filename() + ".json":
			_, _ = w.Write(liveRaw)
		case "/.sync/blobs/" + NewIdentifier(after).Filename():
			_, _ = w.Write(after)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	control := filepath.Join(checkout, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckoutFile(checkout, "content/message.txt", before); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result, err := PullWithOptions(context.Background(), checkout, SyncConfig{Remote: server.URL + "/.sync/latest.json"}, PullOptions{Client: server.Client(), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "updated" || result.ManifestID != liveID {
		t.Fatalf("result = %#v", result)
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	want := []string{
		"downloading 1/1 files (0.0 KB)",
		"file content/message.txt downloading",
		"file content/message.txt downloaded",
	}
	if len(lines) != len(want)+1 {
		t.Fatalf("progress lines = %q", output.String())
	}
	for index, line := range want {
		if lines[index] != line {
			t.Fatalf("progress line %d = %q, want %q", index, lines[index], line)
		}
	}
	if !regexp.MustCompile(`^downloaded 1 files in [0-9]+\.[0-9]{3} seconds$`).MatchString(lines[3]) {
		t.Fatalf("final progress line = %q", lines[3])
	}
}

func TestPullPlanReportsOnlyDivergentLocalWinsFilesAsSkipped(t *testing.T) {
	base := Manifest{Entries: []ManifestEntry{
		{Path: "conflict.txt", Blob: NewIdentifier([]byte("base"))},
		{Path: "local.txt", Blob: NewIdentifier([]byte("base"))},
		{Path: "remote.txt", Blob: NewIdentifier([]byte("base"))},
	}}
	live := Manifest{Entries: []ManifestEntry{
		{Path: "conflict.txt", Blob: NewIdentifier([]byte("remote"))},
		{Path: "local.txt", Blob: NewIdentifier([]byte("base"))},
		{Path: "remote.txt", Blob: NewIdentifier([]byte("remote"))},
	}}
	workspace := Snapshot{Entries: map[string]ManifestEntry{
		"conflict.txt": {Path: "conflict.txt", Blob: NewIdentifier([]byte("local"))},
		"local.txt":    {Path: "local.txt", Blob: NewIdentifier([]byte("local"))},
		"remote.txt":   {Path: "remote.txt", Blob: NewIdentifier([]byte("base"))},
	}}
	resolved := Manifest{Entries: []ManifestEntry{
		workspace.Entries["conflict.txt"], workspace.Entries["local.txt"], live.Entries[2],
	}}
	plan := remoteSelections(base, live, workspace, resolved)
	if len(plan.skipped) != 1 || plan.skipped[0] != "conflict.txt" {
		t.Fatalf("skipped = %#v, want [conflict.txt]", plan.skipped)
	}
	if len(plan.materialize) != 1 || plan.materialize[0].path != "remote.txt" {
		t.Fatalf("materialize = %#v, want remote.txt", plan.materialize)
	}
}

func TestPullLogsDeletedFiles(t *testing.T) {
	checkout := t.TempDir()
	if err := writeCheckoutFile(checkout, "content/removed.txt", []byte("removed")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := materializeRemoteSelections(context.Background(), checkout, nil, []remoteSelection{{path: "content/removed.txt"}}, Remote{}, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "file content/removed.txt deleted\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(checkout, "content", "removed.txt")); !os.IsNotExist(err) {
		t.Fatalf("removed file = %v, want absent", err)
	}
}

func TestPullAdvancesIntermediateWorkspaceValueToLive(t *testing.T) {
	checkout := t.TempDir()
	baseBytes, oldBytes, liveBytes := []byte("version: 1\n"), []byte("version: 2\n"), []byte("version: 10\n")
	base := Manifest{Version: ManifestVersion, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "site.yaml", Blob: NewIdentifier(baseBytes), Size: int64(len(baseBytes)), Mimetype: "application/yaml"}}}
	baseRaw, baseID, _ := SerializeManifest(base)
	middle := Manifest{Version: ManifestVersion, Parent: baseID, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "site.yaml", Blob: NewIdentifier(oldBytes), Size: int64(len(oldBytes)), Mimetype: "application/yaml"}}}
	middleRaw, middleID, _ := SerializeManifest(middle)
	live := Manifest{Version: ManifestVersion, Parent: middleID, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "site.yaml", Blob: NewIdentifier(liveBytes), Size: int64(len(liveBytes)), Mimetype: "application/yaml"}}}
	liveRaw, liveID, _ := SerializeManifest(live)
	indexRaw, _ := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: liveID, History: []Identifier{liveID, middleID, baseID}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + liveID.Filename() + ".json":
			_, _ = w.Write(liveRaw)
		case "/.sync/manifests/" + middleID.Filename() + ".json":
			_, _ = w.Write(middleRaw)
		case "/.sync/blobs/" + NewIdentifier(liveBytes).Filename():
			_, _ = w.Write(liveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if err := os.MkdirAll(filepath.Join(checkout, ".hsync"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".hsync", "manifest.json"), baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckoutFile(checkout, "site.yaml", oldBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := PullWithOptions(context.Background(), checkout, SyncConfig{Remote: server.URL + "/.sync/latest.json"}, PullOptions{Client: server.Client()}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(checkout, "site.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(liveBytes) {
		t.Fatalf("site.yaml = %q, want %q", got, liveBytes)
	}
}

func TestPullFailureDoesNotAdvanceLocalBase(t *testing.T) {
	checkout := t.TempDir()
	before, after := []byte("before"), []byte("after")
	base := Manifest{Version: ManifestVersion, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(before), Size: int64(len(before)), Mimetype: "text/plain"}}}
	baseRaw, baseID, err := SerializeManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	live := Manifest{Version: ManifestVersion, Parent: baseID, SyncPath: base.SyncPath, Entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(after), Size: int64(len(after)), Mimetype: "text/plain"}}}
	liveRaw, liveID, err := SerializeManifest(live)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: liveID, History: []Identifier{liveID, baseID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + liveID.Filename() + ".json":
			_, _ = w.Write(liveRaw)
		case "/.sync/manifests/" + baseID.Filename() + ".json":
			_, _ = w.Write(baseRaw)
		case "/.sync/blobs/" + NewIdentifier(after).Filename():
			_, _ = w.Write([]byte("corrupt"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if err := os.MkdirAll(filepath.Join(checkout, ".hsync"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(checkout, LocalManifestPath)
	if err := os.WriteFile(manifestPath, baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckoutFile(checkout, "content/message.txt", before); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), checkout, SyncConfig{Remote: server.URL + "/.sync/latest.json"}, server.Client()); err == nil {
		t.Fatal("Pull() error = nil for corrupt blob")
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(baseRaw) {
		t.Fatalf("local base advanced after failed pull: %q", got)
	}
	contents, err := os.ReadFile(filepath.Join(checkout, "content", "message.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(before) {
		t.Fatalf("workspace changed after failed pull: %q", contents)
	}
}

func TestSafeCheckoutTargetRejectsSymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "content")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := safeWriteTarget(root, "content/page.txt")
	if err == nil {
		t.Fatal("safeWriteTarget() error = nil")
	}
	if _, err := os.Stat(filepath.Join(outside, "page.txt")); !os.IsNotExist(err) {
		t.Fatalf("materialization escaped checkout: %v", err)
	}
}

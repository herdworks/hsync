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

func writePullConfig(t *testing.T, checkout, remote string) string {
	t.Helper()
	configPath := filepath.Join(checkout, "pull.yaml")
	if err := os.WriteFile(configPath, []byte("sync:\n  remote: "+remote+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestPullUsesConcurrentDownloadProgress(t *testing.T) {
	checkout := t.TempDir()
	before, after := []byte("before"), []byte("after")
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(before), Size: int64(len(before)), Mimetype: "text/plain"}}}
	baseRaw, baseID, err := sealManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	head := Manifest{version: ManifestVersion, parent: baseID, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(after), Size: int64(len(after)), Mimetype: "text/plain"}}}
	headRaw, headID, err := sealManifest(head)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: headID, History: []Identifier{headID, baseID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(headID):
			_, _ = w.Write(headRaw)
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
	if err := writeRepositoryFile(checkout, "content/message.txt", before); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result, err := Pull(context.Background(), checkout, writePullConfig(t, checkout, server.URL+"/.sync/latest.json"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "updated" || result.Remote.Head.ID() != headID {
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
	base := Manifest{entries: []ManifestEntry{
		{Path: "conflict.txt", Blob: NewIdentifier([]byte("base"))},
		{Path: "local.txt", Blob: NewIdentifier([]byte("base"))},
		{Path: "remote.txt", Blob: NewIdentifier([]byte("base"))},
	}}
	head := Manifest{entries: []ManifestEntry{
		{Path: "conflict.txt", Blob: NewIdentifier([]byte("remote"))},
		{Path: "local.txt", Blob: NewIdentifier([]byte("base"))},
		{Path: "remote.txt", Blob: NewIdentifier([]byte("remote"))},
	}}
	workspace := Local{entries: map[string]ManifestEntry{
		"conflict.txt": {Path: "conflict.txt", Blob: NewIdentifier([]byte("local"))},
		"local.txt":    {Path: "local.txt", Blob: NewIdentifier([]byte("local"))},
		"remote.txt":   {Path: "remote.txt", Blob: NewIdentifier([]byte("base"))},
	}}
	resolved := Manifest{entries: []ManifestEntry{
		workspace.entries["conflict.txt"], workspace.entries["local.txt"], head.Entries()[2],
	}}
	reconciliation := Reconciliation{Diffs: []ReconciliationDiff{
		{Path: "conflict.txt", Base: manifestEntry(base.Entries()[0], true), Local: manifestEntry(workspace.entries["conflict.txt"], true), Head: manifestEntry(head.Entries()[0], true), Resolved: manifestEntry(resolved.Entries()[0], true)},
		{Path: "local.txt", Base: manifestEntry(base.Entries()[1], true), Local: manifestEntry(workspace.entries["local.txt"], true), Head: manifestEntry(head.Entries()[1], true), Resolved: manifestEntry(resolved.Entries()[1], true)},
		{Path: "remote.txt", Base: manifestEntry(base.Entries()[2], true), Local: manifestEntry(workspace.entries["remote.txt"], true), Head: manifestEntry(head.Entries()[2], true), Resolved: manifestEntry(resolved.Entries()[2], true)},
	}}
	plan := reconciliation.Plan()
	if len(plan.Retained) != 1 || plan.Retained[0] != "conflict.txt" {
		t.Fatalf("retained = %#v, want [conflict.txt]", plan.Retained)
	}
	if len(plan.Writes) != 1 || plan.Writes[0].Path != "remote.txt" {
		t.Fatalf("writes = %#v, want remote.txt", plan.Writes)
	}
}

func TestPullLogsDeletedFiles(t *testing.T) {
	checkout := t.TempDir()
	if err := writeRepositoryFile(checkout, "content/removed.txt", []byte("removed")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := materializeReconciliationPlan(context.Background(), checkout, ReconciliationPlan{Removes: []string{"content/removed.txt"}}, &GetOperation{Operation: Operation{Writer: &output}}); err != nil {
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
	baseBytes, oldBytes, headBytes := []byte("version: 1\n"), []byte("version: 2\n"), []byte("version: 10\n")
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "site.yaml", Blob: NewIdentifier(baseBytes), Size: int64(len(baseBytes)), Mimetype: "application/yaml"}}}
	baseRaw, baseID, _ := sealManifest(base)
	middle := Manifest{version: ManifestVersion, parent: baseID, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "site.yaml", Blob: NewIdentifier(oldBytes), Size: int64(len(oldBytes)), Mimetype: "application/yaml"}}}
	middleRaw, middleID, _ := sealManifest(middle)
	head := Manifest{version: ManifestVersion, parent: middleID, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "site.yaml", Blob: NewIdentifier(headBytes), Size: int64(len(headBytes)), Mimetype: "application/yaml"}}}
	headRaw, headID, _ := sealManifest(head)
	indexRaw, _ := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: headID, History: []Identifier{headID, middleID, baseID}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(headID):
			_, _ = w.Write(headRaw)
		case "/.sync/manifests/" + manifestFilename(middleID):
			_, _ = w.Write(middleRaw)
		case "/.sync/blobs/" + NewIdentifier(headBytes).Filename():
			_, _ = w.Write(headBytes)
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
	if err := writeRepositoryFile(checkout, "site.yaml", oldBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), checkout, writePullConfig(t, checkout, server.URL+"/.sync/latest.json"), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(checkout, "site.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(headBytes) {
		t.Fatalf("site.yaml = %q, want %q", got, headBytes)
	}
}

func TestPullFailureDoesNotAdvanceLocalBase(t *testing.T) {
	checkout := t.TempDir()
	before, after := []byte("before"), []byte("after")
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(before), Size: int64(len(before)), Mimetype: "text/plain"}}}
	baseRaw, baseID, err := sealManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	head := Manifest{version: ManifestVersion, parent: baseID, indexPath: base.IndexPath(), entries: []ManifestEntry{{Path: "content/message.txt", Blob: NewIdentifier(after), Size: int64(len(after)), Mimetype: "text/plain"}}}
	headRaw, headID, err := sealManifest(head)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := json.Marshal(Index{Version: ManifestVersion, UpdatedAt: time.Now().UTC(), Published: headID, History: []Identifier{headID, baseID}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(headID):
			_, _ = w.Write(headRaw)
		case "/.sync/manifests/" + manifestFilename(baseID):
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
	if err := writeRepositoryFile(checkout, "content/message.txt", before); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), checkout, writePullConfig(t, checkout, server.URL+"/.sync/latest.json"), nil); err == nil {
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

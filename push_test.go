package hsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/herdworks/htup"
)

type unavailableSecrets struct{}

func (unavailableSecrets) Name() string { return "unavailable" }

func (unavailableSecrets) Resolve(context.Context, string) (htup.SecretValue, error) {
	return nil, fmt.Errorf("%w", htup.ErrSecretNotFound)
}

const pushPayloadProviderKind = "hsync-push-payload-test"

var pushPayloadEngine = &capturingPushEngine{}

type capturingPushEngine struct {
	paths []string
	index []byte
	err   error
}

func init() { htup.RegisterEngine(pushPayloadProviderKind, pushPayloadEngine) }

func (*capturingPushEngine) Validate(*htup.HostingProvider) error { return nil }
func (*capturingPushEngine) Ready(context.Context, htup.SecretsProviders, *htup.HostingProvider) error {
	return nil
}
func (*capturingPushEngine) ValidateOperation(*htup.HostingProvider, htup.DeploymentOperation) error {
	return nil
}
func (engine *capturingPushEngine) PerformOperation(_ context.Context, _ htup.SecretsProviders, operation htup.DeploymentOperation, _ io.Writer) error {
	engine.paths = nil
	for _, file := range append(append([]*htup.File(nil), operation.Bulk...), operation.Enqueued...) {
		engine.paths = append(engine.paths, file.RelPath())
		if file.RelPath() == ".hsync/index.json" {
			contents, err := os.ReadFile(file.LocalPath())
			if err != nil {
				return err
			}
			engine.index = contents
		}
	}
	sort.Strings(engine.paths)
	return engine.err
}

func resetPushPayloadEngine() {
	pushPayloadEngine.paths = nil
	pushPayloadEngine.index = nil
	pushPayloadEngine.err = nil
}

func TestPushConfigRegistersHyperctlHostingEngines(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "providers.yaml")
	config := `providers:
  storage:
    kind: s3
    endpoint: https://example.r2.cloudflarestorage.com
    config:
      bucket: website-production
    secrets:
      - name: access_key_id
        key: AWS_ACCESS_KEY_ID
      - name: secret_access_key
        key: AWS_SECRET_ACCESS_KEY
  repository:
    kind: git
    endpoint: https://example.com/site.git
    config:
      branch: main
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := ReadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, provider := range loaded.Providers {
		if err := provider.Validate(); err != nil {
			t.Fatalf("provider %q validation = %v", name, err)
		}
	}
	if err := loaded.Providers["storage"].Ready(context.Background(), htup.SecretsProviders{unavailableSecrets{}}); !errors.Is(err, htup.ErrSecretNotFound) {
		t.Fatalf("storage readiness error = %v, want missing secret", err)
	}
}

func TestPushDeploysOnlyGeneratedSnapshotAndFinalizesInitialManifest(t *testing.T) {
	resetPushPayloadEngine()
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	writeFile(t, filepath.Join(workspace, ".git", "config"), "private")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.hsync/index.json" || len(pushPayloadEngine.index) == 0 {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(pushPayloadEngine.index)
	}))
	defer server.Close()
	configPath := filepath.Join(workspace, "site.yaml")
	config := "providers:\n  test:\n    kind: " + pushPayloadProviderKind + "\n    endpoint: https://provider.example\nenvironments:\n  hsync:\n    - test\nsync:\n  remote: " + server.URL + "\n  include:\n    paths:\n      - content/**\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Push(context.Background(), PushOptions{ConfigPath: configPath}); err != nil {
		t.Fatal(err)
	}
	for _, path := range pushPayloadEngine.paths {
		if path == ".git/config" || path == "content/page.txt" || path == "site.yaml" {
			t.Fatalf("checkout file %q was sent to provider: %#v", path, pushPayloadEngine.paths)
		}
	}
	if len(pushPayloadEngine.paths) == 0 {
		t.Fatal("provider received no generated snapshot files")
	}
	if _, err := os.Stat(filepath.Join(workspace, LocalManifestPath)); err != nil {
		t.Fatalf("initial manifest was not finalized: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(workspace, ".hsync", "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("snapshot staging entries = %#v, want none", entries)
	}
}

func TestPushRejectsMissingIncludeBeforeRemoteWrite(t *testing.T) {
	resetPushPayloadEngine()
	workspace := t.TempDir()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := filepath.Join(workspace, "site.yaml")
	config := "providers:\n  test:\n    kind: " + pushPayloadProviderKind + "\n    endpoint: https://provider.example\nenvironments:\n  hsync:\n    - test\nsync:\n  remote: " + server.URL + "\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Push(context.Background(), PushOptions{ConfigPath: configPath}); err == nil {
		t.Fatal("Push() error = nil without sync.include")
	}
	if len(pushPayloadEngine.paths) != 0 {
		t.Fatalf("provider received paths: %#v", pushPayloadEngine.paths)
	}
	if _, err := os.Stat(filepath.Join(workspace, LocalManifestPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local manifest state = %v, want absent", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".hsync")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local state directory = %v, want absent", err)
	}
}

func TestPushRejectsRemoteHeadWithoutLocalState(t *testing.T) {
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"manifest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	}))
	defer server.Close()
	configPath := filepath.Join(workspace, "site.yaml")
	config := "sync:\n  remote: " + server.URL + "\n  include:\n    paths:\n      - content/**\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Push(context.Background(), PushOptions{ConfigPath: configPath})
	if !errors.Is(err, ErrManualResolution) {
		t.Fatalf("Push() error = %v, want manual resolution", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".hsync")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local state directory = %v, want absent", err)
	}
}

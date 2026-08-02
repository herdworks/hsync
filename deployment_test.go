package hsync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	hyperproviders "github.com/herdworks/hyperproviders-go"
)

const facadeProviderKind = "hsync-deployment-test"

var facadeEngine = &deploymentTestEngine{}

func init() {
	hyperproviders.RegisterEngine(facadeProviderKind, facadeEngine)
}

type deploymentTestEngine struct {
	err   error
	calls int
}

func (*deploymentTestEngine) Validate(*hyperproviders.HostingProvider) error { return nil }
func (*deploymentTestEngine) Ready(context.Context, hyperproviders.SecretsProviders, *hyperproviders.HostingProvider) error {
	return nil
}
func (*deploymentTestEngine) ValidateOperation(*hyperproviders.HostingProvider, hyperproviders.DeploymentOperation) error {
	return nil
}
func (engine *deploymentTestEngine) PerformOperation(context.Context, hyperproviders.SecretsProviders, hyperproviders.DeploymentOperation, io.Writer) error {
	engine.calls++
	return engine.err
}

func resetFacadeEngine() {
	facadeEngine.err = nil
	facadeEngine.calls = 0
}

func facadeProvider() hyperproviders.HostingProviders {
	return hyperproviders.HostingProviders{"test": {Kind: facadeProviderKind, Endpoint: "https://provider.example"}}
}

func writeDeploymentConfig(t *testing.T, root, remote string, include []string) string {
	t.Helper()
	config := "sync:\n  remote: " + remote + "/.sync/latest.json\n"
	if len(include) != 0 {
		config += "  include:\n    paths:\n"
		for _, path := range include {
			config += "      - " + path + "\n"
		}
	}
	path := filepath.Join(root, "site.yaml")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeploymentPrepareBuildsProviderPlanAfterSync(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	repository := Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}},
	}
	if _, _, err := repository.Initialize("/.sync/latest.json"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	deployment := &Deployment{ConfigPath: configPath, PayloadPath: payload, Providers: facadeProvider(), Kind: hyperproviders.IncrementalDeployment, Now: fixedNow}
	if err := deployment.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if deployment.sync.Status.Status != StatusPrepared || deployment.sync.Changeset == nil {
		t.Fatalf("sync outcome = %#v, changeset = %#v", deployment.sync.Status, deployment.sync.Changeset)
	}
	if len(deployment.Instructions().Preconditions) != 1 {
		t.Fatalf("instructions = %#v", deployment.Instructions())
	}
	if len(deployment.Plan().Sequential) != 1 {
		t.Fatalf("plan = %#v", deployment.Plan())
	}
}

func TestDeploymentExecutesOrdinaryDeploymentAfterNoopSync(t *testing.T) {
	resetFacadeEngine()
	workspace := t.TempDir()
	payload := t.TempDir()
	writeFile(t, filepath.Join(payload, "index.html"), "page")
	manifest := Manifest{Version: Version, SyncPath: "/.sync/latest.json", Entries: []ManifestEntry{}}
	manifestRaw, manifestID, err := SerializeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(workspace, ".hsync", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + manifestID + `","history":["` + manifestID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.sync/latest.json":
			_, _ = w.Write(indexRaw)
		case "/.sync/manifests/" + manifestID.Filename() + ".json":
			_, _ = w.Write(manifestRaw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, nil)
	deployment := &Deployment{ConfigPath: configPath, PayloadPath: payload, Providers: facadeProvider(), Kind: hyperproviders.CompleteDeployment, Now: fixedNow}
	if err := deployment.Execute(context.Background(), nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || deployment.Status() != hyperproviders.DeploymentSucceeded {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, deployment.Status())
	}
	if deployment.sync.Status.Status != StatusNoop {
		t.Fatalf("sync status = %q, want noop", deployment.sync.Status.Status)
	}
}

func TestDeploymentUsesOneConfigSnapshotForSyncAndProviders(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, nil)
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("this is no longer valid configuration"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployment := &Deployment{ConfigPath: configPath, ConfigBytes: configBytes, PayloadPath: payload, Providers: facadeProvider(), Kind: hyperproviders.IncrementalDeployment, Now: fixedNow}
	if err := deployment.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if deployment.sync == nil || deployment.Status() != hyperproviders.DeploymentReady {
		t.Fatalf("sync = %#v, deployment status = %q", deployment.sync, deployment.Status())
	}
}

func TestDeploymentUsesEnvironmentFromConfig(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(payload, "index.html"), "page")
	configPath := filepath.Join(workspace, "site.yaml")
	config := "providers:\n  test:\n    kind: " + facadeProviderKind + "\n    endpoint: https://provider.example\nenvironments:\n  production:\n    - test\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	deployment := &Deployment{ConfigPath: configPath, PayloadPath: payload, Environment: "production", Kind: hyperproviders.CompleteDeployment}
	if err := deployment.Execute(context.Background(), nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || deployment.Status() != hyperproviders.DeploymentSucceeded {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, deployment.Status())
	}
}

func TestDeploymentFailureDoesNotFinalizePreparedSync(t *testing.T) {
	resetFacadeEngine()
	facadeEngine.err = errors.New("provider failed")
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	manifestPath := filepath.Join(workspace, ".hsync", "manifest.json")
	repository := Repository{Root: workspace, Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}}
	baseRaw, _, err := repository.Initialize("/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	deployment := &Deployment{ConfigPath: configPath, PayloadPath: payload, Providers: facadeProvider(), Kind: hyperproviders.IncrementalDeployment, Now: fixedNow}
	if err := deployment.Execute(context.Background(), nil, io.Discard); !errors.Is(err, facadeEngine.err) {
		t.Fatalf("Execute() error = %v", err)
	}
	if deployment.sync.Status.Status != StatusPrepared {
		t.Fatalf("sync status = %q, want prepared", deployment.sync.Status.Status)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(baseRaw) {
		t.Fatalf("local manifest advanced after failed deployment: %q", got)
	}
}

func TestDeploymentFinalizesPreparedSyncAfterProviderSuccess(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	repository := Repository{Root: workspace, Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}}
	baseRaw, _, err := repository.Initialize("/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	var deployedIndex []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.sync/latest.json" || deployedIndex == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(deployedIndex)
	}))
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	deployment := &Deployment{ConfigPath: configPath, PayloadPath: payload, Providers: facadeProvider(), Kind: hyperproviders.IncrementalDeployment, Now: fixedNow}
	if err := deployment.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	prepared := *deployment.sync.Changeset
	deployedIndex, err = json.Marshal(Index{Version: Version, UpdatedAt: fixedNow(), Published: prepared.ManifestID, History: []Identifier{prepared.ManifestID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.Execute(context.Background(), nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || deployment.Status() != hyperproviders.DeploymentSucceeded {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, deployment.Status())
	}
	got, err := os.ReadFile(filepath.Join(workspace, LocalManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(baseRaw) || string(got) != string(prepared.Manifest) {
		t.Fatalf("local manifest = %q, want finalized prepared bytes", got)
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }

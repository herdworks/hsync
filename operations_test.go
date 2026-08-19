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

	"github.com/herdworks/htup"
)

func TestPrepareInitialDeployment(t *testing.T) {
	workspace := t.TempDir()
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
	config := &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}
	repository := Repository{Root: workspace, Config: config}
	if _, err := repository.Initialize("/.sync/latest.json"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	operation := &PutOperation{Operation: Operation{Repository: repository, Remote: testRemote(t, server.URL, "/.sync/latest.json"), Now: func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) }}, Deployment: &htup.Deployment{PayloadPath: build}}
	if err := operation.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	manifest := operation.Changeset.Manifest
	if manifest.Parent() != "" || len(manifest.Entries()) != 1 || manifest.Entries()[0].Path != "content/post.md" {
		t.Fatalf("changeset manifest = %#v", manifest)
	}
	if operation.Status != StatusPrepared {
		t.Fatalf("status = %q", operation.Status)
	}
	if _, err := os.Stat(filepath.Join(build, ".sync", "latest.json")); err != nil {
		t.Fatalf("clean index: %v", err)
	}
}

func TestNewDeployment(t *testing.T) {
	provider := &htup.Deployment{}
	deployment := NewDeployment(provider)
	if deployment == nil || deployment.Deployment != provider {
		t.Fatalf("deployment = %#v, want provider deployment", deployment)
	}
}

func TestPrepareRemovesTrackedSymlinkFromNextManifest(t *testing.T) {
	workspace, build := t.TempDir(), t.TempDir()
	contents := []byte("tracked")
	entry := ManifestEntry{Path: "content/page.txt", Blob: NewIdentifier(contents), Size: int64(len(contents)), Mimetype: "text/plain"}
	base := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{entry}}
	baseRaw, baseID, err := sealManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(workspace, ".hsync", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "outside.txt"), filepath.Join(workspace, "content", "page.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + baseID + `","history":["` + baseID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(baseID):
			_, _ = writer.Write(baseRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	sync := &PutOperation{Operation: Operation{Repository: Repository{Root: workspace, Config: &SyncConfig{}}, Remote: testRemote(t, server.URL, base.IndexPath()), Now: fixedNow}, Deployment: &htup.Deployment{PayloadPath: build}}
	if err := sync.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sync.Changeset == nil {
		t.Fatal("Prepare() did not create a removal changeset")
	}
	manifest := sync.Changeset.Manifest
	if len(manifest.Entries()) != 0 {
		t.Fatalf("symlink remained in next manifest: %#v", manifest.Entries())
	}
}

func TestPrepareContinuationUsesExactObservedIndex(t *testing.T) {
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, LocalManifestPath)
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("local change"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}
	baseManifest := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}}
	baseRaw, _, err := sealManifest(baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, baseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	headRaw, headID, err := sealManifest(baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + headID + `","history":["` + headID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(headID):
			_, _ = writer.Write(headRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	sync := &PutOperation{Operation: Operation{Repository: Repository{Root: workspace, Config: config}, Remote: testRemote(t, server.URL, "/.sync/latest.json"), Now: func() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }}, Deployment: &htup.Deployment{PayloadPath: build}}
	if err := sync.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	manifest := sync.Changeset.Manifest
	if manifest.Parent() != headID {
		t.Fatalf("changeset parent = %s, want %s", manifest.Parent(), headID)
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
	manifestPath := filepath.Join(workspace, LocalManifestPath)
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("post"), 0o644); err != nil {
		t.Fatal(err)
	}
	blob := NewIdentifier([]byte("post"))
	config := &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}
	head := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/post.md", Blob: blob, Size: 4, Mimetype: "text/markdown; charset=utf-8"}}}
	headRaw, headID, err := sealManifest(head)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, headRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + headID + `","history":["` + headID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(headID):
			_, _ = writer.Write(headRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	sync := &PutOperation{Operation: Operation{Repository: Repository{Root: workspace, Config: config}, Remote: testRemote(t, server.URL, "/.sync/latest.json"), Now: func() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }}, Deployment: &htup.Deployment{PayloadPath: build}}
	if err := sync.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sync.Changeset != nil || sync.Status != StatusNoop {
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
	manifestPath := filepath.Join(workspace, LocalManifestPath)
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "content", "post.md"), []byte("post"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}
	baseRaw, baseID, err := sealManifest(Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}})
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
	head := Manifest{version: ManifestVersion, parent: baseID, indexPath: "/.sync/latest.json", entries: []ManifestEntry{{Path: "content/post.md", Blob: blob, Size: 4, Mimetype: "text/plain; charset=utf-8"}}}
	headRaw, headID, err := sealManifest(head)
	if err != nil {
		t.Fatal(err)
	}
	indexRaw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + headID + `","history":["` + headID + `"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.sync/latest.json":
			_, _ = writer.Write(indexRaw)
		case "/.sync/manifests/" + manifestFilename(headID):
			_, _ = writer.Write(headRaw)
		case "/.sync/manifests/" + manifestFilename(baseID):
			_, _ = writer.Write(baseRaw)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	sync := &PutOperation{Operation: Operation{Repository: Repository{Root: workspace, Config: config}, Remote: testRemote(t, server.URL, "/.sync/latest.json"), Now: func() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }}, Deployment: &htup.Deployment{PayloadPath: build}}
	if err := sync.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sync.Changeset != nil || sync.Status != StatusAdopted {
		t.Fatalf("unexpected sync outcome: changeset=%#v status=%#v", sync.Changeset, sync.Status)
	}
	localRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(localRaw) != string(headRaw) {
		t.Fatalf("local manifest bytes = %q, want exact head bytes %q", localRaw, headRaw)
	}
}

const facadeProviderKind = "hsync-deployment-test"

var facadeEngine = &deploymentTestEngine{}

func init() {
	htup.RegisterEngine(facadeProviderKind, facadeEngine)
}

type deploymentTestEngine struct {
	err   error
	calls int
}

func (*deploymentTestEngine) Validate(*htup.HostingProvider) error { return nil }
func (*deploymentTestEngine) Ready(context.Context, htup.SecretsProviders, *htup.HostingProvider) error {
	return nil
}
func (*deploymentTestEngine) ValidateOperation(*htup.HostingProvider, htup.DeploymentOperation) error {
	return nil
}
func (engine *deploymentTestEngine) PerformOperation(context.Context, htup.SecretsProviders, htup.DeploymentOperation, io.Writer) error {
	engine.calls++
	return engine.err
}

func resetFacadeEngine() {
	facadeEngine.err = nil
	facadeEngine.calls = 0
}

func facadeProvider() htup.HostingProviders {
	return htup.HostingProviders{"test": {Kind: facadeProviderKind, Endpoint: "https://provider.example"}}
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

func TestPutOperationPrepareReconcilesBeforeProviderPlan(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	repository := Repository{
		Root:   workspace,
		Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}},
	}
	if _, err := repository.Initialize("/.sync/latest.json"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	deployment := testPutOperation(configPath, payload, nil, facadeProvider(), "", htup.IncrementalDeployment, fixedNow)
	deployment.Writer = io.Discard
	if err := deployment.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := deployment.Prepare(context.Background()); err != nil {
		t.Fatalf("second Prepare() error = %v", err)
	}
	if deployment.Status != StatusPrepared || deployment.Changeset == nil {
		t.Fatalf("operation status = %#v, changeset = %#v", deployment.Status, deployment.Changeset)
	}
	if deployment.ProviderStatus() != htup.DeploymentPending {
		t.Fatalf("provider status = %q, want pending", deployment.ProviderStatus())
	}
	if err := deployment.prepareProvider(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(deployment.Instructions().Preconditions) != 1 {
		t.Fatalf("instructions = %#v", deployment.Instructions())
	}
	if len(deployment.Plan().Sequential) != 1 {
		t.Fatalf("plan = %#v", deployment.Plan())
	}
}

func TestOperationFromConfigRejectsRepositoryDeploymentDirectory(t *testing.T) {
	root := t.TempDir()
	configPath := writeDeploymentConfig(t, root, "https://example.test", []string{"content/**"})
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operationFromConfig(context.Background(), configPath, configBytes, root, nil, fixedNow()); err == nil {
		t.Fatal("operationFromConfig() error = nil for repository payload")
	}
}

func TestPutOperationExecutesAndPushOperationSkipsAfterNoopSync(t *testing.T) {
	resetFacadeEngine()
	workspace := t.TempDir()
	payload := t.TempDir()
	writeFile(t, filepath.Join(payload, "index.html"), "page")
	manifest := Manifest{version: ManifestVersion, indexPath: "/.sync/latest.json", entries: []ManifestEntry{}}
	manifestRaw, manifestID, err := sealManifest(manifest)
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
		case "/.sync/manifests/" + manifestFilename(manifestID):
			_, _ = w.Write(manifestRaw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	newOperation := func() *PutOperation {
		return testPutOperation(configPath, payload, nil, facadeProvider(), "", htup.CompleteDeployment, fixedNow)
	}
	deployment := newOperation()
	if err := executePutOperation(context.Background(), deployment); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || deployment.ProviderStatus() != htup.DeploymentSucceeded {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, deployment.ProviderStatus())
	}
	if deployment.Status != StatusNoop {
		t.Fatalf("operation status = %q, want noop", deployment.Status)
	}
	push := &PushOperation{PutOperation: *newOperation()}
	if err := push.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || push.ProviderStatus() != htup.DeploymentPending {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, push.ProviderStatus())
	}
	if push.Status != StatusNoop {
		t.Fatalf("push status = %q, want noop", push.Status)
	}
}

func TestDeploymentUsesOneConfigSnapshotForSyncAndProviders(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("this is no longer valid configuration"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployment := testPutOperation(configPath, payload, configBytes, facadeProvider(), "", htup.IncrementalDeployment, fixedNow)
	deployment.Writer = io.Discard
	if err := deployment.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := deployment.prepareProvider(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deployment.ProviderStatus() != htup.DeploymentReady {
		t.Fatalf("deployment status = %q", deployment.ProviderStatus())
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
	deployment := testPutOperation(configPath, payload, nil, nil, "production", htup.CompleteDeployment, nil)
	if err := executePutOperation(context.Background(), deployment); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || deployment.ProviderStatus() != htup.DeploymentSucceeded {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, deployment.ProviderStatus())
	}
}

func TestDeploymentUsesHsyncEnvironmentByDefault(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(payload, "index.html"), "page")
	configPath := filepath.Join(workspace, "site.yaml")
	config := "providers:\n  test:\n    kind: " + facadeProviderKind + "\n    endpoint: https://provider.example\nenvironments:\n  hsync:\n    - test\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	deployment := testPutOperation(configPath, payload, nil, nil, "", htup.CompleteDeployment, nil)
	if err := executePutOperation(context.Background(), deployment); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", facadeEngine.calls)
	}
}

func TestDeploymentFailureDoesNotFinalizePreparedSync(t *testing.T) {
	resetFacadeEngine()
	facadeEngine.err = errors.New("provider failed")
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	manifestPath := filepath.Join(workspace, ".hsync", "manifest.json")
	repository := Repository{Root: workspace, Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}}
	base, err := repository.Initialize("/.sync/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configPath := writeDeploymentConfig(t, workspace, server.URL, []string{"content/**"})
	deployment := testPutOperation(configPath, payload, nil, facadeProvider(), "", htup.IncrementalDeployment, fixedNow)
	if err := executePutOperation(context.Background(), deployment); !errors.Is(err, facadeEngine.err) {
		t.Fatalf("Execute() error = %v", err)
	}
	if deployment.Status != StatusPrepared {
		t.Fatalf("operation status = %q, want prepared", deployment.Status)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(base.Raw()) {
		t.Fatalf("local manifest advanced after failed deployment: %q", got)
	}
}

func TestDeploymentFinalizesPreparedSyncAfterProviderSuccess(t *testing.T) {
	resetFacadeEngine()
	workspace, payload := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "content", "page.txt"), "page")
	repository := Repository{Root: workspace, Config: &SyncConfig{Include: Rules{Paths: []string{"content/**"}}}}
	base, err := repository.Initialize("/.sync/latest.json")
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
	deployment := testPutOperation(configPath, payload, nil, facadeProvider(), "", htup.IncrementalDeployment, fixedNow)
	deployment.Writer = io.Discard
	if err := deployment.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	prepared := *deployment.Changeset
	deployedIndex, err = json.Marshal(Index{Version: ManifestVersion, UpdatedAt: fixedNow(), Published: prepared.Manifest.ID(), History: []Identifier{prepared.Manifest.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := executePutOperation(context.Background(), deployment); err != nil {
		t.Fatal(err)
	}
	if facadeEngine.calls != 1 || deployment.ProviderStatus() != htup.DeploymentSucceeded {
		t.Fatalf("calls = %d, deployment status = %q", facadeEngine.calls, deployment.ProviderStatus())
	}
	got, err := os.ReadFile(filepath.Join(workspace, LocalManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(base.Raw()) || string(got) != string(prepared.Manifest.Raw()) {
		t.Fatalf("local manifest = %q, want finalized prepared bytes", got)
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC) }

func testPutOperation(configPath, payload string, configBytes []byte, providers htup.HostingProviders, environment string, kind htup.DeploymentKind, now func() time.Time) *PutOperation {
	return &PutOperation{
		Operation: Operation{Now: now},
		Deployment: &htup.Deployment{
			ConfigPath:  configPath,
			ConfigBytes: configBytes,
			PayloadPath: payload,
			Providers:   providers,
			Environment: environment,
			Kind:        kind,
		},
	}
}

func executePutOperation(ctx context.Context, operation *PutOperation) error {
	deployment := &Deployment{PutOperation: *operation}
	err := deployment.Run(ctx)
	*operation = deployment.PutOperation
	return err
}

package hsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	hyperproviders "github.com/herdworks/hyperproviders-go"
)

// Deployment composes optional synchronization preparation and finalization
// with one declarative HyperProviders deployment.
type Deployment struct {
	ConfigPath  string
	ConfigBytes []byte
	PayloadPath string
	Providers   hyperproviders.HostingProviders
	Environment string
	Kind        hyperproviders.DeploymentKind
	Client      *http.Client
	Now         func() time.Time

	provider *hyperproviders.Deployment
	sync     *Sync
}

// Prepare prepares synchronization first, then materializes the immutable
// provider plan. It performs no provider readiness checks or remote writes.
func (deployment *Deployment) Prepare(ctx context.Context, writer io.Writer) error {
	if deployment == nil {
		return fmt.Errorf("deployment is required")
	}
	if deployment.provider != nil {
		return fmt.Errorf("deployment is already prepared")
	}
	configBytes, err := deployment.configSnapshot()
	if err != nil {
		return err
	}
	sync, err := SyncFromConfig(ctx, deployment.ConfigPath, configBytes, deployment.PayloadPath, deployment.Client, deployment.now())
	if err != nil {
		return err
	}
	if sync != nil {
		if err := sync.Prepare(ctx); err != nil {
			return err
		}
	}
	provider := &hyperproviders.Deployment{ConfigPath: deployment.ConfigPath, ConfigBytes: configBytes, PayloadPath: deployment.PayloadPath, Providers: deployment.Providers, Environment: deployment.Environment, Kind: deployment.Kind}
	if err := provider.Prepare(ctx, writer); err != nil {
		return err
	}
	deployment.provider = provider
	deployment.sync = sync
	return nil
}

// Execute performs one deployment attempt and finalizes a prepared sync only
// after every provider operation has succeeded.
func (deployment *Deployment) Execute(ctx context.Context, secrets hyperproviders.SecretsProviders, writer io.Writer) error {
	if deployment == nil {
		return fmt.Errorf("deployment is required")
	}
	if deployment.provider == nil {
		if err := deployment.Prepare(ctx, writer); err != nil {
			return err
		}
	}
	if err := deployment.provider.Execute(ctx, secrets, writer); err != nil {
		return err
	}
	if deployment.sync != nil && deployment.sync.Changeset != nil {
		if err := deployment.sync.Finalize(ctx); err != nil {
			return fmt.Errorf("finalize synchronization: %w", err)
		}
	}
	return nil
}

func (deployment *Deployment) configSnapshot() ([]byte, error) {
	if deployment.ConfigBytes != nil {
		if err := hyperproviders.ValidateDeploymentConfigPath(deployment.ConfigPath); err != nil {
			return nil, err
		}
		return append([]byte(nil), deployment.ConfigBytes...), nil
	}
	return hyperproviders.ReadDeploymentConfig(deployment.ConfigPath)
}

func (deployment *Deployment) now() time.Time {
	if deployment.Now != nil {
		return deployment.Now().UTC()
	}
	return time.Now().UTC()
}

// SyncFromConfig constructs a synchronization unit from a repository-root
// configuration snapshot. It returns nil when the configuration omits sync.
func SyncFromConfig(ctx context.Context, configPath string, configBytes []byte, payloadPath string, client *http.Client, now time.Time) (*Sync, error) {
	config, err := ParseConfigDocument(configPath, configBytes)
	if err != nil {
		return nil, err
	}
	if config.Sync == nil {
		return nil, nil
	}
	if payloadPath == "" {
		return nil, errors.New("deployment payload path is required")
	}
	if err := config.Sync.Validate(); err != nil {
		return nil, err
	}
	root := filepath.Dir(configPath)
	remoteURL, err := config.Sync.RemoteURL()
	if err != nil {
		return nil, err
	}
	remote := Remote{BaseURL: &url.URL{Scheme: remoteURL.Scheme, Host: remoteURL.Host}, Client: client, SyncPath: remoteURL.Path}
	if err := ensureLocalBase(ctx, root, remote, now); err != nil {
		return nil, err
	}
	repository := Repository{Root: root, Config: config.Sync}
	return &Sync{Repository: repository, Payload: Payload{Root: payloadPath}, Remote: remote, Now: func() time.Time { return now }}, nil
}

func ensureLocalBase(ctx context.Context, root string, remote Remote, now time.Time) error {
	repository := Repository{Root: root}
	_, err := repository.manifestTarget(false)
	if err == nil {
		_, _, manifest, err := repository.LoadManifest()
		if err != nil {
			return fmt.Errorf("load sync manifest: %w", err)
		}
		if manifest.SyncPath != remote.SyncPath {
			return fmt.Errorf("sync manifest sync_path %q differs from configured index path %q", manifest.SyncPath, remote.SyncPath)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect sync manifest: %w", err)
	}
	indexRaw, err := remote.ReadIndex(ctx)
	if errors.Is(err, ErrNotFound) {
		_, _, err := repository.Initialize(remote.SyncPath)
		return err
	}
	if err != nil {
		return fmt.Errorf("read configured synchronization index: %w", err)
	}
	index, err := ParseIndex(indexRaw)
	if err != nil {
		return err
	}
	if err := index.PendingError(now); err != nil {
		return err
	}
	manifestRaw, err := remote.ReadManifest(ctx, index.Published)
	if err != nil {
		return err
	}
	manifest, err := ParseManifest(manifestRaw, index.Published)
	if err != nil {
		return err
	}
	if manifest.SyncPath != remote.SyncPath {
		return fmt.Errorf("published manifest sync_path %q differs from configured index path %q", manifest.SyncPath, remote.SyncPath)
	}
	return repository.Finalize(Changeset{ManifestID: index.Published, Manifest: manifestRaw}, indexRaw, now)
}

// Instructions returns a snapshot of the nested provider declarations.
func (deployment *Deployment) Instructions() hyperproviders.DeploymentInstructions {
	if deployment == nil || deployment.provider == nil {
		return hyperproviders.DeploymentInstructions{}
	}
	return deployment.provider.Instructions()
}

// Plan returns a snapshot of the nested provider plan.
func (deployment *Deployment) Plan() hyperproviders.DeploymentPlan {
	if deployment == nil || deployment.provider == nil {
		return hyperproviders.DeploymentPlan{}
	}
	return deployment.provider.Plan()
}

// Status reports the nested provider status.
func (deployment *Deployment) Status() hyperproviders.DeploymentStatus {
	if deployment == nil || deployment.provider == nil {
		return hyperproviders.DeploymentPending
	}
	return deployment.provider.Status()
}

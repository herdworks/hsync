package hsync

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	hyperproviders "github.com/herdworks/hyperproviders-go"
	envsecrets "github.com/herdworks/hyperproviders-go/providers/env"
	_ "github.com/herdworks/hyperproviders-go/providers/git"
	_ "github.com/herdworks/hyperproviders-go/providers/s3"
)

// PushOptions selects providers from a generic deployment configuration.
type PushOptions struct {
	ConfigPath, Environment string
	Providers               []string
	Secrets                 hyperproviders.SecretsProviders
}

// Push reconciles and publishes the repository containing its configuration
// without requiring an SSG.
func Push(ctx context.Context, options PushOptions) error {
	if options.ConfigPath == "" {
		return fmt.Errorf("deployment config is required")
	}
	if options.Environment != "" && len(options.Providers) != 0 {
		return fmt.Errorf("environment and providers are mutually exclusive")
	}
	config, configBytes, err := ReadConfig(options.ConfigPath)
	if err != nil {
		return err
	}
	if config.Sync == nil {
		return fmt.Errorf("push requires sync configuration")
	}
	if err := config.Sync.Validate(); err != nil {
		return err
	}
	if err := preflightInitialPush(ctx, config.Directory(), *config.Sync); err != nil {
		return err
	}
	var providers hyperproviders.HostingProviders
	if len(options.Providers) != 0 {
		providers, err = config.Providers.Select(options.Providers)
		if err != nil {
			return err
		}
	}
	secrets := options.Secrets
	if secrets == nil {
		secrets = hyperproviders.SecretsProviders{envsecrets.New()}
	}
	environment := options.Environment
	if environment == "" && len(providers) == 0 {
		environment = DefaultEnvironment
	}
	payload, err := snapshotPayload(config.Directory())
	if err != nil {
		return err
	}
	defer os.RemoveAll(payload)
	return (&Deployment{ConfigPath: config.Path, ConfigBytes: configBytes, PayloadPath: payload, Providers: providers, Environment: environment, Kind: hyperproviders.IncrementalDeployment}).Execute(ctx, secrets, nil)
}

// preflightInitialPush refuses to create even temporary local state for a
// checkout with no local base when the remote has already been initialized.
// Sync.Prepare repeats this check as part of its normal race-safe workflow.
func preflightInitialPush(ctx context.Context, root string, config SyncConfig) error {
	_, _, _, err := (Repository{Root: root}).LoadManifest()
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read local synchronization base: %w", err)
	}
	policy, err := config.Policy(reservedPaths...)
	if err != nil {
		return err
	}
	if !policy.includePaths.hasPositive() {
		return fmt.Errorf("%w: initial synchronization requires a positive sync.include path rule", ErrInvalidState)
	}
	remoteURL, err := config.RemoteURL()
	if err != nil {
		return err
	}
	remote := Remote{BaseURL: &url.URL{Scheme: remoteURL.Scheme, Host: remoteURL.Host}, SyncPath: remoteURL.Path}
	if _, err := remote.ReadIndex(ctx); err == nil {
		return fmt.Errorf("%w: remote index exists but this repository has no local manifest", ErrManualResolution)
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("read live index: %w", err)
	}
	return nil
}

func snapshotPayload(root string) (string, error) {
	state := filepath.Join(root, ".hsync")
	info, err := os.Lstat(state)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(state, 0o755); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("snapshot state directory must be a regular directory: %s", state)
	}
	parent := filepath.Join(state, "snapshot")
	info, err = os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(parent, 0o755); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("snapshot parent must be a regular directory: %s", parent)
	}
	return os.MkdirTemp(parent, "")
}

package hsync

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/herdworks/htup"
	envsecrets "github.com/herdworks/htup/providers/env"
	_ "github.com/herdworks/htup/providers/git"
	_ "github.com/herdworks/htup/providers/s3"
)

// PushOperation carries the process-specific workflow for a PutOperation.
type PushOperation struct {
	PutOperation
}

// Push reconciles and publishes the repository containing its configuration
// without requiring an SSG, then returns the completed operation.
func Push(ctx context.Context, directory, configPath, environment string, providers []string, secrets htup.SecretsProviders) (*PushOperation, error) {
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if configPath == "" {
		var err error
		configPath, err = DiscoverConfig(directory)
		if err != nil {
			return nil, err
		}
	}
	if configPath == "" {
		return nil, fmt.Errorf("deployment config is required")
	}
	config, configBytes, err := ReadConfig(configPath)
	if err != nil {
		return nil, err
	}
	if config.Sync == nil {
		return nil, fmt.Errorf("push requires sync configuration")
	}
	if err := config.Sync.Validate(); err != nil {
		return nil, err
	}
	if err := preflightInitialPush(ctx, config.Directory(), *config.Sync); err != nil {
		return nil, err
	}
	if secrets == nil {
		secrets = htup.SecretsProviders{envsecrets.New()}
	}
	if environment == "" && len(providers) == 0 {
		environment = DefaultEnvironment
	}
	operation := &PushOperation{PutOperation: PutOperation{
		Operation: Operation{
			ID:         uuid.NewString(),
			Repository: Repository{Root: config.Directory(), Config: config.Sync},
		},
		Deployment: &htup.Deployment{
			ConfigPath:    config.Path,
			ConfigBytes:   configBytes,
			ProviderNames: providers,
			Environment:   environment,
			Kind:          htup.IncrementalDeployment,
		},
		Secrets: secrets,
	}}
	if operation.TempDir, err = operation.Repository.prepare(operation.ID); err != nil {
		return operation, err
	}
	defer operation.Operation.cleanup()
	operation.Deployment.PayloadPath = operation.TempDir
	return operation, operation.Run(ctx)
}

func (operation *PushOperation) Run(ctx context.Context) error {
	if operation == nil || operation.Deployment == nil {
		return fmt.Errorf("push operation is incomplete")
	}
	if operation.Deployment.Status() == htup.DeploymentPending {
		if err := operation.Prepare(ctx); err != nil {
			return err
		}
	}
	if err := operation.Deployment.Execute(ctx, operation.Secrets, operation.Writer); err != nil {
		return err
	}
	if operation.Changeset == nil {
		return nil
	}
	if err := operation.finalize(ctx); err != nil {
		return fmt.Errorf("finalize synchronization: %w", err)
	}
	return nil
}

// preflightInitialPush refuses to create even temporary local state for a
// repository with no local base when the remote has already been initialized.
// Operation.Prepare repeats this check as part of its normal race-safe workflow.
func preflightInitialPush(ctx context.Context, root string, config SyncConfig) error {
	repository := Repository{Root: root}
	err := repository.LoadBase()
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
	remote := Remote{IndexURL: remoteURL}
	if _, err := remote.ReadIndex(ctx); err == nil {
		return fmt.Errorf("%w: remote index exists but this repository has no local manifest", ErrManualResolution)
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("read head index: %w", err)
	}
	return nil
}

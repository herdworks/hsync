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

// Run prepares a changeset and publishes it only when reconciliation finds
// local changes that the remote does not already contain.
func (operation *PushOperation) Run(ctx context.Context) error {
	if operation == nil || operation.Deployment == nil {
		return fmt.Errorf("push operation is incomplete")
	}
	if err := operation.PutOperation.Prepare(ctx); err != nil {
		return err
	}
	if operation.Status == StatusNoop || operation.Status == StatusAdopted {
		return nil
	}
	if operation.ProviderStatus() == htup.DeploymentPending {
		if err := operation.prepareProvider(ctx); err != nil {
			return err
		}
	}
	return operation.PutOperation.put(ctx)
}

// Push reconciles and publishes the repository containing its configuration
// without requiring an SSG, then returns the completed operation.
func Push(ctx context.Context, directory, configPath, environment string, providers []string, secrets htup.SecretsProviders) (*PushOperation, error) {
	config, err := (DefaultConfigManager{}).Read(directory, configPath)
	if err != nil {
		return nil, err
	}
	return PushWithConfig(ctx, config, environment, providers, secrets)
}

// PushWithConfig reconciles and publishes the repository configured by config.
func PushWithConfig(ctx context.Context, config RepositoryConfig, environment string, providers []string, secrets htup.SecretsProviders) (*PushOperation, error) {
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
			ProviderNames: providers,
			Environment:   environment,
			Kind:          htup.IncrementalDeployment,
		},
		Secrets: secrets,
	}}
	tempDir, err := operation.Repository.prepare(operation.ID)
	if err != nil {
		return operation, err
	}
	operation.TempDir = tempDir
	defer operation.Operation.cleanup()
	operation.Deployment.PayloadPath = operation.TempDir
	return operation, operation.Run(ctx)
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

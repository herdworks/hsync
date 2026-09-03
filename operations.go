package hsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/herdworks/htup"
)

// Operation holds the shared repository, remote, and transient execution state
// for hsync work.
type Operation struct {
	ID         string
	Repository Repository
	Remote     Remote
	TempDir    string
	Writer     io.Writer
	Status     OperationStatus
	Now        func() time.Time
}

// GetOperation reads a remote revision into a repository.
type GetOperation struct {
	Operation
}

type getBlob struct {
	once     sync.Once
	contents []byte
	err      error
}

// PutOperation reconciles a Changeset for a declarative htup deployment.
type PutOperation struct {
	Operation
	Changeset      *Changeset
	Deployment     *htup.Deployment
	Secrets        htup.SecretsProviders
	resolvedConfig *RepositoryConfig
	prepared       bool
}

// ResolveRepository resolves a local repository root without requiring it to exist.
func ResolveRepository(root string) (Repository, error) {
	if root == "" {
		return Repository{}, fmt.Errorf("repository root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Repository{}, err
	}
	return Repository{Root: root}, nil
}

// ResolveOperationEndpoints resolves the repository and remote inputs for an operation.
func ResolveOperationEndpoints(ctx context.Context, repositoryPath, remote string, client *http.Client) (Operation, error) {
	repository, err := ResolveRepository(repositoryPath)
	if err != nil {
		return Operation{}, err
	}
	resolvedRemote, err := ResolveRemote(ctx, remote, client)
	if err != nil {
		return Operation{}, err
	}
	return Operation{Repository: repository, Remote: resolvedRemote}, nil
}

// OperationStatus describes the hsync-side result of an operation. Provider
// deployment state remains owned by htup.
type OperationStatus string

const (
	StatusPrepared   OperationStatus = "prepared"
	StatusNoop       OperationStatus = "noop"
	StatusAdopted    OperationStatus = "adopted"
	StatusFinalized  OperationStatus = "finalized"
	StatusDownloaded OperationStatus = "downloaded"
	StatusUpdated    OperationStatus = "updated"
	StatusReset      OperationStatus = "reset"
)

const getWorkers = 8

// getEntries fetches and verifies entries concurrently before materializing
// their blobs below root.
func (operation *GetOperation) getEntries(ctx context.Context, root string, entries []ManifestEntry) error {
	downloads := make(map[Identifier]*getBlob)
	for _, entry := range entries {
		if downloads[entry.Blob] == nil {
			downloads[entry.Blob] = &getBlob{}
		}
	}
	var logMu sync.Mutex
	log := func(format string, arguments ...any) error {
		logMu.Lock()
		defer logMu.Unlock()
		return operation.log(format, arguments...)
	}
	group, groupCtx := errgroup.WithContext(ctx)
	jobs := make(chan ManifestEntry)
	group.Go(func() error {
		defer close(jobs)
		for _, entry := range entries {
			select {
			case jobs <- entry:
			case <-groupCtx.Done():
				return groupCtx.Err()
			}
		}
		return nil
	})
	workers := min(getWorkers, len(entries))
	for range workers {
		group.Go(func() error {
			for {
				select {
				case entry, ok := <-jobs:
					if !ok {
						return nil
					}
					if err := log("file %s downloading", entry.Path); err != nil {
						return err
					}
					contents, err := downloads[entry.Blob].read(groupCtx, operation.Remote, entry.Blob)
					if err != nil {
						return fmt.Errorf("read blob %s: %w", entry.Blob, err)
					}
					if err := groupCtx.Err(); err != nil {
						return err
					}
					if int64(len(contents)) != entry.Size {
						return fmt.Errorf("%w: blob %s size differs from manifest", ErrInvalidState, entry.Blob)
					}
					if err := writeRepositoryFile(root, entry.Path, contents); err != nil {
						return err
					}
					if err := log("file %s downloaded", entry.Path); err != nil {
						return err
					}
				case <-groupCtx.Done():
					return groupCtx.Err()
				}
			}
		})
	}
	return group.Wait()
}

func (download *getBlob) read(ctx context.Context, remote Remote, id Identifier) ([]byte, error) {
	download.once.Do(func() {
		download.contents, download.err = remote.ReadBlob(ctx, id)
	})
	return download.contents, download.err
}

// Prepare validates and reconciles an hsync-enabled provider deployment. It
// does not materialize or execute a provider deployment plan.
func (operation *PutOperation) Prepare(ctx context.Context) error {
	if operation == nil {
		return fmt.Errorf("put operation is required")
	}
	if operation.Deployment == nil {
		return fmt.Errorf("put operation deployment is required")
	}
	if operation.prepared {
		return nil
	}
	configBytes, err := operation.prepareReconciliation(ctx)
	if err != nil {
		return err
	}
	operation.Deployment.ConfigBytes = configBytes
	operation.prepared = true
	return nil
}

func (operation *PutOperation) put(ctx context.Context) error {
	if !operation.prepared || operation.Deployment.Status() != htup.DeploymentReady {
		return fmt.Errorf("put operation provider deployment is not ready")
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

func (operation *PutOperation) prepareReconciliation(ctx context.Context) ([]byte, error) {
	configBytes, err := operation.configBytes()
	if err != nil {
		return nil, err
	}
	configuration := operation.Deployment
	var resolved *Operation
	if operation.resolvedConfig != nil {
		resolved, err = operationFromRepositoryConfig(ctx, *operation.resolvedConfig, configuration.PayloadPath, operation.Remote.Client, operation.now())
	} else {
		resolved, err = operationFromConfig(ctx, configuration.ConfigPath, configBytes, configuration.PayloadPath, operation.Remote.Client, operation.now())
	}
	if err != nil {
		return nil, err
	}
	if resolved != nil {
		id, tempDir, writer := operation.ID, operation.TempDir, operation.Writer
		operation.Operation = *resolved
		operation.Operation.ID = id
		operation.Operation.TempDir = tempDir
		operation.Operation.Writer = writer
		operation.Operation.Now = operation.Now
		if err := operation.prepare(ctx); err != nil {
			return nil, err
		}
	}
	return configBytes, nil
}

func (operation *PutOperation) prepareProvider(ctx context.Context) error {
	if !operation.prepared {
		return fmt.Errorf("put operation is not prepared")
	}
	if operation.Deployment.Status() != htup.DeploymentPending {
		return fmt.Errorf("put operation provider deployment is already prepared")
	}
	configuration := operation.Deployment
	if configuration.Environment == "" && len(configuration.Providers) == 0 {
		configuration.Environment = DefaultEnvironment
	}
	if err := configuration.Prepare(ctx, operation.Writer); err != nil {
		return err
	}
	return nil
}

// Instructions returns a copy of the nested provider declarations.
func (operation *PutOperation) Instructions() htup.DeploymentInstructions {
	if operation == nil || operation.Deployment == nil {
		return htup.DeploymentInstructions{}
	}
	return operation.Deployment.Instructions()
}

// Plan returns a copy of the nested provider plan.
func (operation *PutOperation) Plan() htup.DeploymentPlan {
	if operation == nil || operation.Deployment == nil {
		return htup.DeploymentPlan{}
	}
	return operation.Deployment.Plan()
}

// ProviderStatus reports the nested provider deployment status.
func (operation *PutOperation) ProviderStatus() htup.DeploymentStatus {
	if operation == nil || operation.Deployment == nil {
		return htup.DeploymentPending
	}
	return operation.Deployment.Status()
}

func (operation *PutOperation) configBytes() ([]byte, error) {
	if operation.Deployment.ConfigBytes != nil {
		if err := htup.ValidateDeploymentConfigPath(operation.Deployment.ConfigPath); err != nil {
			return nil, err
		}
		return append([]byte(nil), operation.Deployment.ConfigBytes...), nil
	}
	return htup.ReadDeploymentConfig(operation.Deployment.ConfigPath)
}

// Prepare reconciles Repository with authoritative remote state when required,
// then materializes a non-empty changeset in the deployment directory. It
// performs no provider writes.
//
// Repository.Config controls optional discovery; without it, synchronization
// uses only the files already tracked by the local manifest.
func (operation *PutOperation) prepare(ctx context.Context) error {
	if operation == nil {
		return fmt.Errorf("%w: operation is required", ErrInvalidState)
	}
	now := operation.now()
	base, err := operation.Repository.RefreshBase()
	if errors.Is(err, os.ErrNotExist) {
		return operation.prepareInitial(ctx, now)
	}
	if err != nil {
		return fmt.Errorf("load local manifest: %w", err)
	}
	baseID := base.ID()
	if operation.Remote.IndexURL == nil || operation.Remote.IndexURL.Path != base.IndexPath() {
		return fmt.Errorf("%w: remote index path differs from local manifest %q", ErrManualResolution, base.IndexPath())
	}
	if err := operation.Repository.Snapshot(); err != nil {
		return err
	}

	observedIndex, err := operation.Remote.ReadIndex(ctx)
	if errors.Is(err, ErrNotFound) {
		if base.Parent() != "" {
			return fmt.Errorf("%w: local base has a parent but head index is absent", ErrManualResolution)
		}
		changeset, err := initialChangeset(base.IndexPath(), operation.Repository.Local)
		if err != nil {
			return err
		}
		prepared, err := completeChangeset(changeset, nil, now)
		if err != nil {
			return err
		}
		if err := prepared.Apply(operation.Deployment.PayloadPath); err != nil {
			return err
		}
		operation.Changeset = prepared
		operation.Status = StatusPrepared
		return nil
	}
	if err != nil {
		return fmt.Errorf("read head index: %w", err)
	}
	index, err := ParseIndex(observedIndex)
	if err != nil {
		return err
	}
	if err := index.PendingError(now); err != nil {
		return err
	}
	lineage, err := ReadLineage(ctx, operation.Remote, baseID, index.Published)
	if err != nil {
		return fmt.Errorf("read published manifest lineage: %w", err)
	}
	head := lineage.Revisions[0]
	operation.Remote.Head = head
	reconciliation, err := operation.Repository.Reconcile(index.Published, lineage)
	if err != nil {
		return err
	}
	changeset, err := reconciliation.Changeset()
	if err != nil {
		return err
	}
	if changesetMatchesHead(changeset, head) {
		if baseID != index.Published {
			headChangeset := Changeset{Manifest: head}
			if err := operation.Repository.Finalize(headChangeset, observedIndex, now); err != nil {
				return fmt.Errorf("adopt head manifest: %w", err)
			}
			operation.Status = StatusAdopted
		} else {
			operation.Status = StatusNoop
		}
		return nil
	}
	prepared, err := completeChangeset(changeset, observedIndex, now)
	if err != nil {
		return err
	}
	if err := prepared.Apply(operation.Deployment.PayloadPath); err != nil {
		return err
	}
	operation.Changeset = prepared
	operation.Status = StatusPrepared
	return nil
}

// Finalize verifies a clean deployed index and advances the local repository.
func (operation *PutOperation) finalize(ctx context.Context) error {
	if operation == nil || operation.Changeset == nil {
		return fmt.Errorf("%w: prepared changeset is required", ErrInvalidState)
	}
	headIndex, err := operation.Remote.ReadIndex(ctx)
	if err != nil {
		return fmt.Errorf("read deployed synchronization index: %w", err)
	}
	if err := operation.Repository.Finalize(*operation.Changeset, headIndex, operation.now()); err != nil {
		return err
	}
	operation.Status = StatusFinalized
	return nil
}

func (operation *PutOperation) prepareInitial(ctx context.Context, now time.Time) error {
	observedIndex, err := operation.Remote.ReadIndex(ctx)
	if err == nil {
		if _, err := ParseIndex(observedIndex); err != nil {
			return err
		}
		return fmt.Errorf("%w: remote index exists but this repository has no local manifest", ErrManualResolution)
	}
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("read head index: %w", err)
	}
	if err := operation.Repository.Snapshot(); err != nil {
		return err
	}
	changeset, err := initialChangeset(operation.Remote.IndexURL.Path, operation.Repository.Local)
	if err != nil {
		return err
	}
	prepared, err := completeChangeset(changeset, nil, now)
	if err != nil {
		return err
	}
	if err := prepared.Apply(operation.Deployment.PayloadPath); err != nil {
		return err
	}
	operation.Changeset = prepared
	operation.Status = StatusPrepared
	return nil
}

func (operation Operation) now() time.Time {
	if operation.Now != nil {
		return operation.Now().UTC()
	}
	return time.Now().UTC()
}

func (operation *Operation) cleanup() {
	if operation == nil || operation.TempDir == "" {
		return
	}
	_ = os.RemoveAll(operation.TempDir)
	operation.TempDir = ""
}

func (operation Operation) log(format string, arguments ...any) error {
	if operation.Writer == nil {
		return nil
	}
	_, err := fmt.Fprintf(operation.Writer, format+"\n", arguments...)
	return err
}

// operationFromConfig constructs an operation from a repository-root
// configuration bytes. It returns nil when the configuration omits sync.
func operationFromConfig(ctx context.Context, configPath string, configBytes []byte, payloadPath string, client *http.Client, now time.Time) (*Operation, error) {
	config, err := ParseConfig(configBytes)
	if err != nil {
		return nil, err
	}
	config.Path = configPath
	return operationFromRepositoryConfig(ctx, config, payloadPath, client, now)
}

func operationFromRepositoryConfig(ctx context.Context, config RepositoryConfig, payloadPath string, client *http.Client, now time.Time) (*Operation, error) {
	if config.Sync == nil {
		return nil, nil
	}
	if err := config.Sync.Validate(); err != nil {
		return nil, err
	}
	if err := htup.ValidateDeploymentPayloadPath(config.Path, payloadPath); err != nil {
		return nil, err
	}
	root := config.Directory()
	remoteURL, err := config.Sync.RemoteURL()
	if err != nil {
		return nil, err
	}
	remote := Remote{IndexURL: remoteURL, Client: client}
	repository := Repository{Root: root, Config: config.Sync}
	return &Operation{Repository: repository, Remote: remote, Now: func() time.Time { return now }}, nil
}

func initialChangeset(indexPath string, local Local) (Changeset, error) {
	entries := local.Entries()
	manifest, err := NewSealedManifest(indexPath, "", entries)
	if err != nil {
		return Changeset{}, err
	}
	return Changeset{Manifest: manifest, Blobs: local.Blobs}, nil
}

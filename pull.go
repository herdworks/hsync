package hsync

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// PullOperation carries the process-specific workflow for a GetOperation.
type PullOperation struct {
	GetOperation
}

// Pull loads or discovers synchronization configuration, constructs and runs a
// PullOperation, then returns it.
func Pull(ctx context.Context, directory, configPath string, writer io.Writer) (*PullOperation, error) {
	config, err := LoadSyncConfig(directory, configPath)
	if err != nil {
		return nil, err
	}
	return PullWithConfig(ctx, config, writer)
}

// PullWithConfig incorporates remote changes using config.
func PullWithConfig(ctx context.Context, config RepositoryConfig, writer io.Writer) (*PullOperation, error) {
	if config.Sync == nil {
		return nil, fmt.Errorf("pull requires sync configuration")
	}
	repository, remote, err := openConfiguredRepository(config.Directory(), *config.Sync, nil)
	if err != nil {
		return nil, err
	}
	repository.Config = config.Sync
	operation := &PullOperation{GetOperation: GetOperation{Operation: Operation{Repository: repository, Remote: remote, Writer: writer}}}
	return operation, operation.Run(ctx)
}

// Run incorporates the latest clean revision from Remote into Repository.
func (operation *PullOperation) Run(ctx context.Context) error {
	if operation == nil {
		return fmt.Errorf("get operation is required")
	}
	repository, remote := &operation.Repository, &operation.Remote
	base, err := repository.RefreshBase()
	if err != nil {
		return err
	}
	baseID := base.ID()
	indexRaw, err := remote.ReadIndex(ctx)
	if err != nil {
		return err
	}
	index, err := ParseIndex(indexRaw)
	if err != nil {
		return err
	}
	if err := index.PendingError(time.Now().UTC()); err != nil {
		return err
	}
	if index.Published == baseID {
		remote.Head = base
		operation.Status = StatusNoop
		return nil
	}
	lineage, err := ReadLineage(ctx, remote, baseID, index.Published)
	if err != nil {
		return err
	}
	head := lineage.Revisions[0]
	remote.Head = head
	if err := repository.Snapshot(); err != nil {
		return err
	}
	reconciliation, err := repository.Reconcile(index.Published, lineage)
	if err != nil {
		return err
	}
	plan := reconciliation.Plan()
	if err := operation.log("downloading %d/%d files (%s)", len(plan.Writes), len(head.Entries()), humanSize(entriesSize(plan.Writes))); err != nil {
		return err
	}
	for _, path := range plan.Retained {
		if err := operation.log("file %s skipped", path); err != nil {
			return err
		}
	}
	downloadStarted := time.Now()
	if err := materializeReconciliationPlan(ctx, repository.Root, plan, &operation.GetOperation); err != nil {
		return err
	}
	if err := operation.log("downloaded %d files in %.3f seconds", len(plan.Writes), time.Since(downloadStarted).Seconds()); err != nil {
		return err
	}
	if err := repository.Finalize(Changeset{Manifest: head}, indexRaw, time.Now().UTC()); err != nil {
		return err
	}
	operation.Status = StatusUpdated
	return nil
}

func materializeReconciliationPlan(ctx context.Context, root string, plan ReconciliationPlan, operation *GetOperation) error {
	if err := operation.getEntries(ctx, root, plan.Writes); err != nil {
		return err
	}
	for _, path := range plan.Removes {
		filename, err := safeWriteTarget(root, path)
		if err != nil {
			return err
		}
		if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := operation.log("file %s deleted", path); err != nil {
			return err
		}
	}
	return nil
}

func entriesSize(entries []ManifestEntry) int64 {
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}
	return total
}

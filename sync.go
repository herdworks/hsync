package hsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Sync is one hsync unit of work. Prepare reads repository state and, when the
// local base and remote live manifests differ, performs repository
// reconciliation before applying a non-empty changeset to Payload. Finalize
// advances Repository only after its caller has deployed that payload
// successfully.
type Sync struct {
	Repository Repository
	Payload    Payload
	Changeset  *Changeset
	Status     SyncStatus
	Remote     Remote
	Now        func() time.Time
}

// SyncStatus is the local lifecycle outcome of a Sync and leaves room for
// future caller-owned deployment status without claiming provider transaction
// state.
type SyncStatus struct {
	Status Status
}

// Status describes the completed stage of a Sync. Provider outcomes remain
// external because hsync performs no provider writes.
type Status string

const (
	StatusPrepared  Status = "prepared"
	StatusNoop      Status = "noop"
	StatusAdopted   Status = "adopted"
	StatusFinalized Status = "finalized"
)

// Prepare reconciles Repository with authoritative remote state when required,
// then writes a non-empty changeset beneath Payload. It performs no provider
// writes.
//
// Repository.Config controls optional discovery; without it, synchronization
// uses only the files already tracked by the local manifest.
func (sync *Sync) Prepare(ctx context.Context) error {
	if sync == nil {
		return fmt.Errorf("%w: sync is required", ErrInvalidState)
	}
	now := sync.now()
	_, baseID, base, err := sync.Repository.LoadManifest()
	if err != nil {
		return fmt.Errorf("load local manifest: %w", err)
	}
	if sync.Remote.SyncPath != base.SyncPath {
		return fmt.Errorf("%w: remote sync_path %q differs from local manifest %q", ErrManualResolution, sync.Remote.SyncPath, base.SyncPath)
	}
	local, err := sync.Repository.snapshot(&base)
	if err != nil {
		return err
	}

	observedIndex, err := sync.Remote.ReadIndex(ctx)
	if errors.Is(err, ErrNotFound) {
		if base.Parent != "" {
			return fmt.Errorf("%w: local base has a parent but live index is absent", ErrManualResolution)
		}
		changeset, err := initialChangeset(base, local)
		if err != nil {
			return err
		}
		prepared, err := completeChangeset(changeset, nil, now)
		if err != nil {
			return err
		}
		if err := prepared.Apply(sync.Payload); err != nil {
			return err
		}
		sync.Changeset = prepared
		sync.Status = SyncStatus{Status: StatusPrepared}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read live index: %w", err)
	}
	index, err := ParseIndex(observedIndex)
	if err != nil {
		return err
	}
	if err := index.PendingError(now); err != nil {
		return err
	}
	continuation, err := ReadContinuation(ctx, sync.Remote, baseID, index.Published)
	if err != nil {
		return fmt.Errorf("read published continuation: %w", err)
	}
	liveRaw, live := continuation.LiveRaw, continuation.Revisions[0]
	changeset, err := sync.Repository.Reconcile(base, index.Published, continuation, local)
	if err != nil {
		return err
	}
	if changesetMatchesLive(changeset, live) {
		if baseID != index.Published {
			liveChangeset := Changeset{ManifestID: index.Published, Manifest: liveRaw}
			if err := sync.Repository.Finalize(liveChangeset, observedIndex, now); err != nil {
				return fmt.Errorf("adopt live manifest: %w", err)
			}
			sync.Status = SyncStatus{Status: StatusAdopted}
		} else {
			sync.Status = SyncStatus{Status: StatusNoop}
		}
		return nil
	}
	prepared, err := completeChangeset(changeset, observedIndex, now)
	if err != nil {
		return err
	}
	if err := prepared.Apply(sync.Payload); err != nil {
		return err
	}
	sync.Changeset = prepared
	sync.Status = SyncStatus{Status: StatusPrepared}
	return nil
}

// Finalize verifies a clean deployed index and advances the local repository.
func (sync *Sync) Finalize(ctx context.Context) error {
	if sync == nil || sync.Changeset == nil {
		return fmt.Errorf("%w: prepared changeset is required", ErrInvalidState)
	}
	liveIndex, err := sync.Remote.ReadIndex(ctx)
	if err != nil {
		return fmt.Errorf("read deployed synchronization index: %w", err)
	}
	if err := sync.Repository.Finalize(*sync.Changeset, liveIndex, sync.now()); err != nil {
		return err
	}
	sync.Status = SyncStatus{Status: StatusFinalized}
	return nil
}

func (sync Sync) now() time.Time {
	if sync.Now != nil {
		return sync.Now().UTC()
	}
	return time.Now().UTC()
}

func initialChangeset(base Manifest, snapshot Snapshot) (Changeset, error) {
	entries := make([]ManifestEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })
	manifest := Manifest{Version: Version, SyncPath: base.SyncPath, Entries: entries}
	raw, id, err := SerializeManifest(manifest)
	if err != nil {
		return Changeset{}, err
	}
	return Changeset{ManifestID: id, Manifest: raw, Blobs: snapshot.Blobs, syncPath: base.SyncPath, manifest: manifest}, nil
}

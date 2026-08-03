package hsync

import (
	"context"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

// PullResult describes incorporation of a newer published revision.
type PullResult struct {
	IndexURL   string
	ManifestID Identifier
	Status     string
}

// PullOptions controls optional pull behavior.
type PullOptions struct {
	Client *http.Client
	Output io.Writer
}

type remoteSelection struct {
	path   string
	live   ManifestEntry
	liveOK bool
}

type pullPlan struct {
	materialize []remoteSelection
	skipped     []string
}

// Pull reconciles a configured checkout with the current clean remote revision.
// Remote-selected changes are materialized while genuinely divergent local
// workspace revisions retain local-wins behavior.
func Pull(ctx context.Context, directory string, config SyncConfig, client *http.Client) (PullResult, error) {
	return PullWithOptions(ctx, directory, config, PullOptions{Client: client})
}

// PullWithOptions reconciles a configured checkout with the current clean
// remote revision. When Output is non-nil, it receives download progress lines.
func PullWithOptions(ctx context.Context, directory string, config SyncConfig, options PullOptions) (PullResult, error) {
	repository, remote, err := OpenCheckout(directory, config, options.Client)
	if err != nil {
		return PullResult{}, err
	}
	_, baseID, base, err := repository.LoadManifest()
	if err != nil {
		return PullResult{}, err
	}
	indexRaw, err := remote.ReadIndex(ctx)
	if err != nil {
		return PullResult{}, err
	}
	index, err := ParseIndex(indexRaw)
	if err != nil {
		return PullResult{}, err
	}
	if err := index.PendingError(time.Now().UTC()); err != nil {
		return PullResult{}, err
	}
	if index.Published == baseID {
		return PullResult{IndexURL: remote.BaseURL.String() + remote.SyncPath, ManifestID: baseID, Status: "noop"}, nil
	}
	continuation, err := ReadContinuation(ctx, remote, baseID, index.Published)
	if err != nil {
		return PullResult{}, err
	}
	liveRaw, live := continuation.LiveRaw, continuation.Revisions[0]
	repository.Config = &config
	workspace, err := repository.snapshot(&base)
	if err != nil {
		return PullResult{}, err
	}
	changeset, err := repository.Reconcile(base, index.Published, continuation, workspace)
	if err != nil {
		return PullResult{}, err
	}
	plan := remoteSelections(base, live, workspace, changeset.manifest)
	downloads := make([]ManifestEntry, 0, len(plan.materialize))
	for _, selection := range plan.materialize {
		if selection.liveOK {
			downloads = append(downloads, selection.live)
		}
	}
	if err := writeDownloadProgress(options.Output, "downloading %d/%d files (%s)", len(downloads), len(live.Entries), humanSize(entriesSize(downloads))); err != nil {
		return PullResult{}, err
	}
	for _, path := range plan.skipped {
		if err := writeDownloadProgress(options.Output, "file %s skipped", path); err != nil {
			return PullResult{}, err
		}
	}
	downloadStarted := time.Now()
	if err := materializeRemoteSelections(ctx, directory, downloads, plan.materialize, remote, options.Output); err != nil {
		return PullResult{}, err
	}
	if err := writeDownloadProgress(options.Output, "downloaded %d files in %.3f seconds", len(downloads), time.Since(downloadStarted).Seconds()); err != nil {
		return PullResult{}, err
	}
	if err := repository.Finalize(Changeset{ManifestID: index.Published, Manifest: liveRaw}, indexRaw, time.Now().UTC()); err != nil {
		return PullResult{}, err
	}
	return PullResult{IndexURL: remote.BaseURL.String() + remote.SyncPath, ManifestID: index.Published, Status: "updated"}, nil
}

func remoteSelections(base, live Manifest, workspace Snapshot, resolved Manifest) pullPlan {
	baseEntries, liveEntries := entriesByPath(base.Entries), entriesByPath(live.Entries)
	paths := map[string]struct{}{}
	for name := range baseEntries {
		paths[name] = struct{}{}
	}
	for name := range liveEntries {
		paths[name] = struct{}{}
	}
	for name := range workspace.Entries {
		paths[name] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	plan := pullPlan{materialize: make([]remoteSelection, 0, len(ordered))}
	for _, name := range ordered {
		baseEntry, baseOK := baseEntries[name]
		workspaceEntry, workspaceOK := workspace.Entries[name]
		liveEntry, liveOK := liveEntries[name]
		resolvedEntry, resolvedOK := entriesByPath(resolved.Entries)[name]
		workspaceChanged := !entriesEqual(workspaceEntry, workspaceOK, baseEntry, baseOK)
		liveChanged := !entriesEqual(liveEntry, liveOK, baseEntry, baseOK)
		if entriesEqual(resolvedEntry, resolvedOK, workspaceEntry, workspaceOK) {
			if workspaceChanged && liveChanged && !entriesEqual(workspaceEntry, workspaceOK, liveEntry, liveOK) {
				plan.skipped = append(plan.skipped, name)
			}
			continue
		}
		plan.materialize = append(plan.materialize, remoteSelection{path: name, live: resolvedEntry, liveOK: resolvedOK})
	}
	return plan
}

func materializeRemoteSelections(ctx context.Context, root string, downloads []ManifestEntry, selections []remoteSelection, remote Remote, output io.Writer) error {
	if err := downloadEntries(ctx, root, remote, downloads, output); err != nil {
		return err
	}
	for _, selection := range selections {
		if selection.liveOK {
			continue
		}
		filename, err := safeWriteTarget(root, selection.path)
		if err != nil {
			return err
		}
		if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := writeDownloadProgress(output, "file %s deleted", selection.path); err != nil {
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

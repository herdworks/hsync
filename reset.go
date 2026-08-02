package hsync

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ResetOptions controls optional reset behavior.
type ResetOptions struct {
	Output      io.Writer
	Input       io.Reader
	Interactive bool
}

// ResetResult describes restoration of files from the repository's local base.
type ResetResult struct {
	ManifestID Identifier
	Status     string
	Files      int
}

// Reset restores missing or modified files tracked by the local manifest. It
// never reads the remote index, changes the local manifest, or removes files
// absent from the manifest.
func (repository Repository) Reset(ctx context.Context, remote Remote, options ResetOptions) (ResetResult, error) {
	if repository.Root == "" {
		return ResetResult{}, fmt.Errorf("%w: repository root is required", ErrInvalidState)
	}
	_, manifestID, manifest, err := repository.LoadManifest()
	if err != nil {
		return ResetResult{}, err
	}
	candidates, err := repository.resetCandidates(manifest.Entries)
	if err != nil {
		return ResetResult{}, err
	}
	if len(candidates) == 0 {
		return ResetResult{ManifestID: manifestID, Status: "noop"}, nil
	}
	accepted, err := selectResetEntries(candidates, options)
	if err != nil {
		return ResetResult{}, err
	}
	if len(accepted) == 0 {
		return ResetResult{ManifestID: manifestID, Status: "noop"}, nil
	}
	if err := writeDownloadProgress(options.Output, "downloading %d files (%s)", len(accepted), humanSize(entriesSize(accepted))); err != nil {
		return ResetResult{}, err
	}
	staging, err := os.MkdirTemp("", "hsync-reset-")
	if err != nil {
		return ResetResult{}, err
	}
	defer os.RemoveAll(staging)
	started := time.Now()
	if err := downloadEntries(ctx, staging, remote, accepted, options.Output); err != nil {
		return ResetResult{}, err
	}
	targets := make(map[string]string, len(accepted))
	for _, entry := range accepted {
		target, err := safeWriteTarget(repository.Root, entry.Path)
		if err != nil {
			return ResetResult{}, err
		}
		targets[entry.Path] = target
	}
	for _, entry := range accepted {
		contents, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(entry.Path)))
		if err != nil {
			return ResetResult{}, err
		}
		if err := writeResetFile(repository.Root, targets[entry.Path], contents); err != nil {
			return ResetResult{}, err
		}
	}
	if err := writeDownloadProgress(options.Output, "downloaded %d files in %.3f seconds", len(accepted), time.Since(started).Seconds()); err != nil {
		return ResetResult{}, err
	}
	return ResetResult{ManifestID: manifestID, Status: "reset", Files: len(accepted)}, nil
}

func (repository Repository) resetCandidates(entries []ManifestEntry) ([]ManifestEntry, error) {
	candidates := make([]ManifestEntry, 0, len(entries))
	for _, entry := range entries {
		filename := filepath.Join(repository.Root, filepath.FromSlash(entry.Path))
		info, err := os.Lstat(filename)
		switch {
		case errors.Is(err, os.ErrNotExist):
			candidates = append(candidates, entry)
			continue
		case err != nil:
			return nil, err
		case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
			candidates = append(candidates, entry)
			continue
		}
		contents, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		if NewIdentifier(contents) != entry.Blob {
			candidates = append(candidates, entry)
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].Path < candidates[right].Path })
	return candidates, nil
}

func selectResetEntries(candidates []ManifestEntry, options ResetOptions) ([]ManifestEntry, error) {
	if !options.Interactive {
		return candidates, nil
	}
	input := options.Input
	if input == nil {
		input = os.Stdin
	}
	output := options.Output
	if output == nil {
		output = os.Stdout
	}
	reader := bufio.NewReader(input)
	accepted := make([]ManifestEntry, 0, len(candidates))
	for _, entry := range candidates {
		if _, err := fmt.Fprintf(output, "file %s reset? [y/N] ", entry.Path); err != nil {
			return nil, err
		}
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
			accepted = append(accepted, entry)
			continue
		}
		if _, err := fmt.Fprintf(output, "file %s skipped\n", entry.Path); err != nil {
			return nil, err
		}
	}
	return accepted, nil
}

func writeResetFile(root, target string, contents []byte) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	target, err = safeWriteTarget(root, filepath.ToSlash(relative))
	if err != nil {
		return err
	}
	parent := filepath.Dir(target)
	temporary, err := os.CreateTemp(parent, ".reset-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, target)
}

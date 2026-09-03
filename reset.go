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

// ResetOperation restores selected entries from a repository's local base
// manifest without changing that base or the remote index.
type ResetOperation struct {
	GetOperation
	Reader      io.Reader
	Interactive bool
	Files       int
}

// Run restores selected files tracked by the repository's local base manifest.
// It never reads the remote index, changes the local manifest, or removes
// files absent from that manifest.
func (operation *ResetOperation) Run(ctx context.Context) error {
	if operation == nil || operation.Repository.Root == "" {
		return fmt.Errorf("%w: repository root is required", ErrInvalidState)
	}
	if err := operation.Repository.LoadBase(); err != nil {
		return err
	}
	entries, err := operation.Repository.resetCandidates(operation.Repository.Base.Entries())
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		operation.Status = StatusNoop
		return nil
	}
	entries, err = selectResetEntries(entries, operation.Reader, operation.Interactive, operation.Writer)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		operation.Status = StatusNoop
		return nil
	}
	if err := operation.log("downloading %d files (%s)", len(entries), humanSize(entriesSize(entries))); err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "hsync-reset-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	started := time.Now()
	if err := operation.getEntries(ctx, staging, entries); err != nil {
		return err
	}
	targets, err := resetTargets(ctx, operation.Repository.Root, entries)
	if err != nil {
		return err
	}
	if err := writeResetEntries(ctx, operation.Repository.Root, staging, entries, targets); err != nil {
		return err
	}
	if err := operation.log("downloaded %d files in %.3f seconds", len(entries), time.Since(started).Seconds()); err != nil {
		return err
	}
	operation.Files = len(entries)
	operation.Status = StatusReset
	return nil
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

func resetTargets(ctx context.Context, root string, entries []ManifestEntry) (targets map[string]string, err error) {
	targets = make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target, err := safeWriteTarget(root, entry.Path)
		if err != nil {
			return nil, err
		}
		targets[entry.Path] = target
	}
	return targets, nil
}

func writeResetEntries(ctx context.Context, root, staging string, entries []ManifestEntry, targets map[string]string) (err error) {
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(entry.Path)))
		if err != nil {
			return err
		}
		if err := writeResetFile(root, targets[entry.Path], contents); err != nil {
			return err
		}
	}
	return nil
}

// Reset loads or discovers synchronization configuration, constructs and runs
// a ResetOperation, then returns it.
func Reset(ctx context.Context, directory, configPath string, writer io.Writer, input io.Reader, interactive bool) (*ResetOperation, error) {
	config, err := LoadSyncConfig(directory, configPath)
	if err != nil {
		return nil, err
	}
	return ResetWithConfig(ctx, config, writer, input, interactive)
}

// ResetWithConfig restores selected entries using config.
func ResetWithConfig(ctx context.Context, config RepositoryConfig, writer io.Writer, input io.Reader, interactive bool) (*ResetOperation, error) {
	if config.Sync == nil {
		return nil, fmt.Errorf("reset requires sync configuration")
	}
	repository, remote, err := openConfiguredRepository(config.Directory(), *config.Sync, nil)
	if err != nil {
		return nil, err
	}
	operation := &ResetOperation{GetOperation: GetOperation{
		Operation: Operation{Repository: repository, Remote: remote, Writer: writer},
	}, Reader: input, Interactive: interactive}
	return operation, operation.Run(ctx)
}

func selectResetEntries(candidates []ManifestEntry, input io.Reader, interactive bool, writer io.Writer) ([]ManifestEntry, error) {
	if !interactive {
		return candidates, nil
	}
	if input == nil {
		input = os.Stdin
	}
	if writer == nil {
		writer = os.Stdout
	}
	reader := bufio.NewReader(input)
	accepted := make([]ManifestEntry, 0, len(candidates))
	for _, entry := range candidates {
		if _, err := fmt.Fprintf(writer, "file %s reset? [y/N] ", entry.Path); err != nil {
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
		if _, err := fmt.Fprintf(writer, "file %s skipped\n", entry.Path); err != nil {
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

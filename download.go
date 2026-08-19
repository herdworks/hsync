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
	"strings"
	"time"

	"github.com/google/uuid"
)

// DownloadOperation holds the transient state for one download operation.
type DownloadOperation struct {
	GetOperation
}

// Run creates a verified local repository from the resolved remote revision.
func (operation *DownloadOperation) Run(ctx context.Context) (err error) {
	if operation != nil && operation.ID == "" {
		operation.ID = uuid.NewString()
	}
	if operation == nil || operation.ID == "" || operation.Remote.IndexURL == nil || operation.Repository.Root == "" {
		return fmt.Errorf("download operation is incomplete")
	}
	if err := operation.Remote.RefreshHead(ctx); err != nil {
		return err
	}
	if err := operation.destinationReadiness(); err != nil {
		return err
	}
	if operation.TempDir, err = operation.Repository.prepare(operation.ID, stagedFilesPath); err != nil {
		return err
	} else {
		defer operation.Operation.cleanup() // remove the operation temp dir on completion
	}
	if err := operation.log("downloading %d files (%s)", len(operation.Remote.Head.Entries()), humanSize(manifestSize(operation.Remote.Head))); err != nil {
		return err
	}
	downloadStarted := time.Now()
	if err := operation.getEntries(ctx, filepath.Join(operation.TempDir, stagedFilesPath), operation.Remote.Head.Entries()); err != nil {
		return err
	}
	if err := operation.log("downloaded %d files in %.3f seconds", len(operation.Remote.Head.Entries()), time.Since(downloadStarted).Seconds()); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(operation.TempDir, "manifest.json"), operation.Remote.Head.Raw(), 0o644); err != nil {
		return err
	}
	if empty, err := operation.Repository.isEmpty(); err != nil {
		return err
	} else if !empty {
		return fmt.Errorf("destination %q is not empty", operation.Repository.Root)
	}
	if err := operation.Repository.commit(operation.TempDir); err != nil {
		return err
	}
	operation.Status = StatusDownloaded
	return nil
}

// destinationReadiness ensures download will not overwrite existing files.
func (operation *DownloadOperation) destinationReadiness() (err error) {
	// does the destination path exist?
	if exists, err := operation.Repository.exists(); err != nil {
		return err
	} else if !exists {
		return nil
	}
	// is it a directory?
	if directory, err := operation.Repository.isDirectory(); err != nil {
		return err
	} else if !directory {
		return fmt.Errorf("destination %q is not a directory", operation.Repository.Root)
	}
	// is it empty?
	if empty, err := operation.Repository.isEmpty(); err != nil {
		return err
	} else if !empty {
		return fmt.Errorf("destination %q is not empty", operation.Repository.Root)
	}
	return nil
}

// Download resolves source, constructs and runs a DownloadOperation, then
// returns it.
func Download(ctx context.Context, source *url.URL, destination string, client *http.Client, writer io.Writer) (*DownloadOperation, error) {
	if source == nil {
		return nil, fmt.Errorf("source is required")
	}
	operation, err := ResolveOperationEndpoints(ctx, destination, source.String(), client)
	if err != nil {
		return nil, err
	}
	operation.Writer = writer
	download := &DownloadOperation{GetOperation: GetOperation{Operation: operation}}
	return download, download.Run(ctx)
}

// OpenRepository loads a local repository. Its configuration is a repository
// file and is resolved separately by the caller.
func OpenRepository(directory string) (Repository, error) {
	if directory == "" {
		return Repository{}, fmt.Errorf("repository directory is required")
	}
	repository := Repository{Root: directory}
	if err := repository.LoadBase(); err != nil {
		return Repository{}, err
	}
	return repository, nil
}

// openConfiguredRepository loads a repository and remote reader from its
// required SyncConfig.
// A manifest records its index path but not a remote host.
func openConfiguredRepository(directory string, config SyncConfig, client *http.Client) (Repository, Remote, error) {
	repository, err := OpenRepository(directory)
	if err != nil {
		return Repository{}, Remote{}, err
	}
	remoteURL, err := config.RemoteURL()
	if err != nil {
		return Repository{}, Remote{}, err
	}
	if remoteURL.Path != repository.Base.IndexPath() {
		return Repository{}, Remote{}, fmt.Errorf("%w: configured remote differs from local manifest", ErrInvalidState)
	}
	return repository, Remote{IndexURL: remoteURL, Client: client}, nil
}

func manifestSize(manifest Manifest) int64 {
	var total int64
	for _, entry := range manifest.Entries() {
		total += entry.Size
	}
	return total
}

func humanSize(size int64) string {
	const (
		kilobyte = 1024
		megabyte = kilobyte * 1024
		gigabyte = megabyte * 1024
	)
	switch {
	case size >= gigabyte:
		return fmt.Sprintf("%.1f GB", float64(size)/gigabyte)
	case size >= megabyte:
		return fmt.Sprintf("%.1f MB", float64(size)/megabyte)
	default:
		return fmt.Sprintf("%.1f KB", float64(size)/kilobyte)
	}
}

func writeRepositoryFile(root, relative string, contents []byte) error {
	filename, err := safeWriteTarget(root, relative)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".repository-*")
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
	return os.Rename(temporaryName, filename)
}

// safeWriteTarget returns a regular-file target contained by root. hsync
// does not support symlinks in a local repository, so neither a root, ancestor, nor an
// existing target may be one.
func safeWriteTarget(root, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("write root must be a regular directory: %s", root)
	}
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("write target has unsafe ancestor: %s", current)
		}
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	info, err = os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return target, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("write target must be a regular file or absent: %s", target)
	}
	return target, nil
}

// safeReadTarget returns an existing regular file contained by root without
// following a symlink at the root, an ancestor, or the target itself.
func safeReadTarget(root, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("read root must be a regular directory: %s", root)
	}
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("read target has unsafe ancestor: %s", current)
		}
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	info, err = os.Lstat(target)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("read target must be a regular file: %s", target)
	}
	return target, nil
}

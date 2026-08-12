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
	"sync"
	"time"

	"github.com/herdworks/hq"
	"golang.org/x/sync/errgroup"
)

// DownloadResult describes a verified public checkout.
type DownloadResult struct {
	IndexURL   *url.URL
	ManifestID Identifier
	Status     string
}

// DownloadOptions controls optional download behavior.
type DownloadOptions struct {
	Client *http.Client
	Output io.Writer
}

type blobDownload struct {
	once     sync.Once
	contents []byte
	err      error
}

const downloadWorkers = 8

func (download *blobDownload) read(ctx context.Context, remote Remote, id Identifier) ([]byte, error) {
	download.once.Do(func() {
		download.contents, download.err = remote.ReadBlob(ctx, id)
	})
	return download.contents, download.err
}

// OpenRepository loads a local checkout. Its configuration is a repository
// file and is resolved separately by the caller.
func OpenRepository(directory string) (Repository, error) {
	if directory == "" {
		return Repository{}, fmt.Errorf("checkout directory is required")
	}
	repository := Repository{Root: directory}
	if _, _, _, err := repository.LoadManifest(); err != nil {
		return Repository{}, err
	}
	return repository, nil
}

// OpenCheckout loads a checkout and remote reader from its required SyncConfig.
// A manifest records its sync path but not a remote host.
func OpenCheckout(directory string, config SyncConfig, client *http.Client) (Repository, Remote, error) {
	repository, err := OpenRepository(directory)
	if err != nil {
		return Repository{}, Remote{}, err
	}
	remoteURL, err := config.RemoteURL()
	if err != nil {
		return Repository{}, Remote{}, err
	}
	_, _, manifest, err := repository.LoadManifest()
	if err != nil {
		return Repository{}, Remote{}, err
	}
	if remoteURL.Path != manifest.SyncPath {
		return Repository{}, Remote{}, fmt.Errorf("%w: configured remote differs from local manifest", ErrInvalidState)
	}
	return repository, Remote{BaseURL: &url.URL{Scheme: remoteURL.Scheme, Host: remoteURL.Host}, Client: client, SyncPath: remoteURL.Path}, nil
}

// Download creates a verified local checkout of the clean revision published
// by source, which may be either a direct synchronization index or a webpage
// declaring a single rel=https://hsync.sh link.
func Download(ctx context.Context, source *url.URL, destination string, client *http.Client) (DownloadResult, error) {
	return DownloadWithOptions(ctx, source, destination, DownloadOptions{Client: client})
}

// DownloadWithOptions creates a verified local checkout of the clean revision
// published by source. When Output is non-nil, it receives progress lines.
func DownloadWithOptions(ctx context.Context, source *url.URL, destination string, options DownloadOptions) (DownloadResult, error) {
	if source == nil || destination == "" {
		return DownloadResult{}, fmt.Errorf("source and destination are required")
	}
	existingDestination, err := emptyDestination(destination)
	if err != nil {
		return DownloadResult{}, err
	}
	indexURL, remote, index, manifestRaw, manifest, err := resolvePublished(ctx, source, options.Client)
	if err != nil {
		return DownloadResult{}, err
	}
	if err := writeDownloadProgress(options.Output, "downloading %d files (%s)", len(manifest.Entries), humanSize(manifestSize(manifest))); err != nil {
		return DownloadResult{}, err
	}
	parent := filepath.Dir(filepath.Clean(destination))
	staging, err := os.MkdirTemp(parent, ".hsync-download-")
	if err != nil {
		return DownloadResult{}, err
	}
	defer os.RemoveAll(staging)
	downloadStarted := time.Now()
	if err := downloadEntries(ctx, staging, remote, manifest.Entries, options.Output); err != nil {
		return DownloadResult{}, err
	}
	if err := writeDownloadProgress(options.Output, "downloaded %d files in %.3f seconds", len(manifest.Entries), time.Since(downloadStarted).Seconds()); err != nil {
		return DownloadResult{}, err
	}
	control := filepath.Join(staging, ".hsync")
	if err := os.MkdirAll(control, 0o755); err != nil {
		return DownloadResult{}, err
	}
	if err := os.WriteFile(filepath.Join(control, "manifest.json"), manifestRaw, 0o644); err != nil {
		return DownloadResult{}, err
	}
	if existingDestination {
		// Rename cannot replace a directory on all supported platforms. Recheck
		// immediately before removing it so a download never replaces content
		// created while its verified checkout was being staged.
		if exists, err := emptyDestination(destination); err != nil || !exists {
			if err != nil {
				return DownloadResult{}, err
			}
			return DownloadResult{}, fmt.Errorf("destination %q changed while downloading", destination)
		}
		if err := os.Remove(destination); err != nil {
			return DownloadResult{}, err
		}
	}
	if err := os.Rename(staging, destination); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{IndexURL: indexURL, ManifestID: index.Published, Status: "downloaded"}, nil
}

func downloadEntries(ctx context.Context, root string, remote Remote, entries []ManifestEntry, output io.Writer) error {
	downloads := make(map[Identifier]*blobDownload)
	for _, entry := range entries {
		if downloads[entry.Blob] == nil {
			downloads[entry.Blob] = &blobDownload{}
		}
	}
	var outputMu sync.Mutex
	progress := func(format string, arguments ...any) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return writeDownloadProgress(output, format, arguments...)
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
	workers := min(downloadWorkers, len(entries))
	for range workers {
		group.Go(func() error {
			for {
				select {
				case entry, ok := <-jobs:
					if !ok {
						return nil
					}
					if err := progress("file %s downloading", entry.Path); err != nil {
						return err
					}
					contents, err := downloads[entry.Blob].read(groupCtx, remote, entry.Blob)
					if err != nil {
						return fmt.Errorf("read blob %s: %w", entry.Blob, err)
					}
					if int64(len(contents)) != entry.Size {
						return fmt.Errorf("%w: blob %s size differs from manifest", ErrInvalidState, entry.Blob)
					}
					if err := writeCheckoutFile(root, entry.Path, contents); err != nil {
						return err
					}
					if err := progress("file %s downloaded", entry.Path); err != nil {
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

func manifestSize(manifest Manifest) int64 {
	var total int64
	for _, entry := range manifest.Entries {
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

func writeDownloadProgress(output io.Writer, format string, arguments ...any) error {
	if output == nil {
		return nil
	}
	_, err := fmt.Fprintf(output, format+"\n", arguments...)
	return err
}

// emptyDestination reports whether destination already exists and, when it
// does, requires it to be an empty regular directory rather than a symlink.
func emptyDestination(destination string) (bool, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("destination %q is not a regular directory", destination)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return false, err
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("destination %q is not empty", destination)
	}
	return true, nil
}

func writeCheckoutFile(root, relative string, contents []byte) error {
	filename, err := safeWriteTarget(root, relative)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".checkout-*")
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
// does not support symlinks in a checkout, so neither a root, ancestor, nor an
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
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", err
			}
			continue
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

func resolvePublished(ctx context.Context, source *url.URL, client *http.Client) (*url.URL, Remote, Index, []byte, Manifest, error) {
	if source == nil || (source.Scheme != "http" && source.Scheme != "https") || source.Host == "" {
		return nil, Remote{}, Index{}, nil, Manifest{}, fmt.Errorf("source must be an HTTP(S) URL")
	}
	if source.Path != "" && source.Path != "/" {
		remote, index, raw, manifest, err := readPublished(ctx, source, client)
		if err != nil {
			return nil, Remote{}, Index{}, nil, Manifest{}, err
		}
		return source, remote, index, raw, manifest, nil
	}
	indexURL := &url.URL{Scheme: source.Scheme, Host: source.Host, Path: DefaultIndexPath}
	if remote, index, raw, manifest, err := readPublished(ctx, indexURL, client); err == nil {
		return indexURL, remote, index, raw, manifest, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return nil, Remote{}, Index{}, nil, Manifest{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, Remote{}, Index{}, nil, Manifest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, Remote{}, Index{}, nil, Manifest{}, fmt.Errorf("read discovery document: HTTP %s", response.Status)
	}
	links, err := hq.DiscoverSyncEndpoints(source.String(), response.Body)
	if err != nil {
		return nil, Remote{}, Index{}, nil, Manifest{}, err
	}
	if len(links) != 1 {
		return nil, Remote{}, Index{}, nil, Manifest{}, fmt.Errorf("expected one sync endpoint, found %d", len(links))
	}
	resolved, err := url.Parse(links[0])
	if err != nil {
		return nil, Remote{}, Index{}, nil, Manifest{}, err
	}
	remote, index, raw, manifest, err := readPublished(ctx, resolved, client)
	return resolved, remote, index, raw, manifest, err
}

func readPublished(ctx context.Context, indexURL *url.URL, client *http.Client) (Remote, Index, []byte, Manifest, error) {
	if indexURL == nil || (indexURL.Scheme != "http" && indexURL.Scheme != "https") || indexURL.Host == "" || indexURL.RawQuery != "" || indexURL.Fragment != "" || strings.Contains(indexURL.Path, "//") {
		return Remote{}, Index{}, nil, Manifest{}, fmt.Errorf("invalid synchronization index URL")
	}
	remote := Remote{BaseURL: &url.URL{Scheme: indexURL.Scheme, Host: indexURL.Host}, Client: client, SyncPath: indexURL.Path}
	raw, err := remote.ReadIndex(ctx)
	if err != nil {
		return Remote{}, Index{}, nil, Manifest{}, err
	}
	index, err := ParseIndex(raw)
	if err != nil {
		return Remote{}, Index{}, nil, Manifest{}, err
	}
	if err := index.PendingError(time.Now().UTC()); err != nil {
		return Remote{}, Index{}, nil, Manifest{}, err
	}
	manifestRaw, err := remote.ReadManifest(ctx, index.Published)
	if err != nil {
		return Remote{}, Index{}, nil, Manifest{}, err
	}
	manifest, err := ParseManifest(manifestRaw, index.Published)
	if err != nil {
		return Remote{}, Index{}, nil, Manifest{}, err
	}
	if manifest.SyncPath != indexURL.Path {
		return Remote{}, Index{}, nil, Manifest{}, fmt.Errorf("%w: published manifest sync_path differs from index URL", ErrInvalidState)
	}
	return remote, index, manifestRaw, manifest, nil
}

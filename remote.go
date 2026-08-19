package hsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/herdworks/hq"
)

// Remote retrieves publicly served state using ordinary HTTP GET requests.
// It never performs provider write requests.
type Remote struct {
	IndexURL *url.URL
	Client   *http.Client
	Head     Manifest
}

// ResolveRemote parses and resolves a public synchronization endpoint.
func ResolveRemote(ctx context.Context, source string, client *http.Client) (Remote, error) {
	url, err := url.Parse(source)
	if err != nil {
		return Remote{}, err
	}
	return ResolveRemoteURL(ctx, url, client)
}

// ResolveRemoteURL resolves either a direct index URL or a website discovery URL.
func ResolveRemoteURL(ctx context.Context, source *url.URL, client *http.Client) (Remote, error) {
	if source == nil || (source.Scheme != "http" && source.Scheme != "https") || source.Host == "" {
		return Remote{}, fmt.Errorf("source must be an HTTP(S) URL")
	}
	remote := Remote{IndexURL: source, Client: client}
	if source.Path != "" && source.Path != "/" {
		return remote, nil
	}
	indexURL := &url.URL{Scheme: source.Scheme, Host: source.Host, Path: DefaultIndexPath}
	remote.IndexURL = indexURL
	if _, err := remote.ReadIndex(ctx); err == nil {
		return remote, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Remote{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return Remote{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return Remote{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Remote{}, fmt.Errorf("read discovery document: HTTP %s", response.Status)
	}
	links, err := hq.DiscoverSyncEndpoints(source.String(), response.Body)
	if err != nil {
		return Remote{}, err
	}
	if len(links) != 1 {
		return Remote{}, fmt.Errorf("expected one sync endpoint, found %d", len(links))
	}
	indexURL, err = url.Parse(links[0])
	if err != nil {
		return Remote{}, err
	}
	return Remote{IndexURL: indexURL, Client: client}, nil
}

// ErrNotFound reports an absent remote synchronization object.
var ErrNotFound = errors.New("synchronization object not found")

// ReadIndex returns the raw mutable index bytes, or ErrNotFound when no site has
// initialized remote synchronization state.
func (reader Remote) ReadIndex(ctx context.Context) ([]byte, error) {
	indexPath, err := reader.indexPath()
	if err != nil {
		return nil, err
	}
	return reader.get(ctx, indexPath)
}

// RefreshHead refreshes this remote's sealed published manifest.
func (remote *Remote) RefreshHead(ctx context.Context) error {
	raw, err := remote.ReadIndex(ctx)
	if err != nil {
		return err
	}
	index, err := ParseIndex(raw)
	if err != nil {
		return err
	}
	return remote.loadHead(ctx, index)
}

// ReadManifest returns a sealed manifest after hash-verifying its exact bytes.
func (reader Remote) ReadManifest(ctx context.Context, id Identifier) (Manifest, error) {
	if err := id.Validate(); err != nil {
		return Manifest{}, err
	}
	indexPath, err := reader.indexPath()
	if err != nil {
		return Manifest{}, err
	}
	syncRoot := path.Dir(indexPath)
	raw, err := reader.get(ctx, path.Join(syncRoot, "manifests", manifestFilename(id)))
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(raw, id)
}

// ReadBlob returns and hash-verifies the exact immutable bytes for id.
func (reader Remote) ReadBlob(ctx context.Context, id Identifier) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	indexPath, err := reader.indexPath()
	if err != nil {
		return nil, err
	}
	syncRoot := path.Dir(indexPath)
	raw, err := reader.get(ctx, path.Join(syncRoot, "blobs", id.Filename()))
	if err != nil {
		return nil, err
	}
	if NewIdentifier(raw) != id {
		return nil, fmt.Errorf("%w: blob %s has mismatched bytes", ErrInvalidState, id)
	}
	return raw, nil
}

func (remote *Remote) loadHead(ctx context.Context, index Index) error {
	if err := index.PendingError(time.Now().UTC()); err != nil {
		return err
	}
	manifest, err := remote.ReadManifest(ctx, index.Published)
	if err != nil {
		return err
	}
	indexPath, err := remote.indexPath()
	if err != nil {
		return err
	}
	if manifest.IndexPath() != indexPath {
		return fmt.Errorf("%w: published manifest index path differs from index URL", ErrInvalidState)
	}
	remote.Head = manifest
	return nil
}

func (reader Remote) get(ctx context.Context, requestPath string) ([]byte, error) {
	if _, err := reader.indexPath(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(requestPath, "/") {
		return nil, fmt.Errorf("%w: HTTP request path must be absolute", ErrInvalidState)
	}
	target := *reader.IndexURL
	// The index path is an absolute URL path, independent of any path component in
	// the site canonical base URL.
	target.Path = requestPath
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	client := reader.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("read %s: HTTP %s", requestPath, response.Status)
	}
	return io.ReadAll(response.Body)
}

func (reader Remote) indexPath() (string, error) {
	if reader.IndexURL == nil || (reader.IndexURL.Scheme != "http" && reader.IndexURL.Scheme != "https") || reader.IndexURL.Host == "" || reader.IndexURL.RawQuery != "" || reader.IndexURL.Fragment != "" {
		return "", fmt.Errorf("%w: remote requires an HTTP(S) index URL", ErrInvalidState)
	}
	if err := validateIndexPath(reader.IndexURL.Path); err != nil {
		return "", err
	}
	return reader.IndexURL.Path, nil
}

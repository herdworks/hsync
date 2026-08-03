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
)

// Remote retrieves publicly served state using ordinary HTTP GET requests.
// It never performs provider write requests.
type Remote struct {
	BaseURL  *url.URL
	Client   *http.Client
	SyncPath string
}

// ManifestReader retrieves exact immutable manifest bytes by Identifier.
type ManifestReader interface {
	ReadManifest(context.Context, Identifier) ([]byte, error)
}

// Continuation holds the verified remote manifests needed for repository
// reconciliation from the local base to the live revision. Revisions begins
// with live and ends with the direct child of base when they differ.
type Continuation struct {
	LiveRaw   []byte
	Revisions []Manifest
}

// ErrNotFound reports an absent live state object.
var ErrNotFound = errors.New("synchronization object not found")

// ReadIndex returns the raw mutable index bytes, or ErrNotFound when no site has
// initialized remote synchronization state.
func (reader Remote) ReadIndex(ctx context.Context) ([]byte, error) {
	return reader.get(ctx, reader.SyncPath)
}

// ReadManifest returns and hash-verifies exact bytes for id.
func (reader Remote) ReadManifest(ctx context.Context, id Identifier) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	syncRoot := path.Dir(reader.SyncPath)
	raw, err := reader.get(ctx, path.Join(syncRoot, "manifests", id.Filename()+".json"))
	if err != nil {
		return nil, err
	}
	if _, err := ParseManifest(raw, id); err != nil {
		return nil, err
	}
	return raw, nil
}

// ReadBlob returns and hash-verifies the exact immutable bytes for id.
func (reader Remote) ReadBlob(ctx context.Context, id Identifier) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	syncRoot := path.Dir(reader.SyncPath)
	raw, err := reader.get(ctx, path.Join(syncRoot, "blobs", id.Filename()))
	if err != nil {
		return nil, err
	}
	if NewIdentifier(raw) != id {
		return nil, fmt.Errorf("%w: blob %s has mismatched bytes", ErrInvalidState, id)
	}
	return raw, nil
}

func (reader Remote) get(ctx context.Context, requestPath string) ([]byte, error) {
	if reader.BaseURL == nil || !strings.HasPrefix(reader.SyncPath, "/") {
		return nil, fmt.Errorf("%w: remote requires base URL and absolute sync_path", ErrInvalidState)
	}
	if !strings.HasPrefix(requestPath, "/") {
		return nil, fmt.Errorf("%w: HTTP request path must be absolute", ErrInvalidState)
	}
	target := *reader.BaseURL
	// sync_path is an absolute URL path, independent of any path component in
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

// ReadContinuation proves that localID is an immutable ancestor of liveID and
// retains every newer verified revision from base to live for the lineage-based
// three-way divergence algorithm used during repository reconciliation.
func ReadContinuation(ctx context.Context, reader ManifestReader, localID, liveID Identifier) (Continuation, error) {
	if err := localID.Validate(); err != nil {
		return Continuation{}, err
	}
	if err := liveID.Validate(); err != nil {
		return Continuation{}, err
	}
	if localID == liveID {
		raw, err := reader.ReadManifest(ctx, liveID)
		if err != nil {
			return Continuation{}, err
		}
		manifest, err := ParseManifest(raw, liveID)
		if err != nil {
			return Continuation{}, err
		}
		return Continuation{LiveRaw: raw, Revisions: []Manifest{manifest}}, nil
	}
	continuation := Continuation{}
	visited := map[Identifier]struct{}{}
	for current := liveID; current != ""; {
		if current == localID {
			return continuation, nil
		}
		if _, seen := visited[current]; seen {
			return Continuation{}, fmt.Errorf("%w: manifest ancestry loop at %s", ErrInvalidState, current)
		}
		visited[current] = struct{}{}
		raw, err := reader.ReadManifest(ctx, current)
		if err != nil {
			return Continuation{}, err
		}
		manifest, err := ParseManifest(raw, current)
		if err != nil {
			return Continuation{}, err
		}
		if len(continuation.Revisions) == 0 {
			continuation.LiveRaw = raw
		}
		continuation.Revisions = append(continuation.Revisions, manifest)
		current = manifest.Parent
	}
	return Continuation{}, fmt.Errorf("%w: local manifest %s is not reachable from live %s", ErrManualResolution, localID, liveID)
}

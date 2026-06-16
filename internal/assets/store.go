package assets

import (
	"context"
	"net/http"

	"github.com/herdworks/hypersync-go/internal/store"
)

type AssetRef struct {
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

func Put(ctx context.Context, objects store.ObjectStore, body []byte) (AssetRef, error) {
	hash := Hash(body)
	contentType := http.DetectContentType(body)
	_, err := objects.Put(ctx, ShardedKey(hash), body, store.PutOptions{IfNoneMatch: true, ContentType: contentType})
	if err != nil && err != store.ErrPreconditionFailed {
		return AssetRef{}, err
	}
	return AssetRef{Hash: hash, Size: int64(len(body)), ContentType: contentType}, nil
}

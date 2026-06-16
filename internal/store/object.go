package store

import (
	"context"
	"errors"
)

var (
	ErrNotFound           = errors.New("object not found")
	ErrPreconditionFailed = errors.New("object precondition failed")
)

type ObjectStore interface {
	Get(ctx context.Context, key string) (Object, error)
	Put(ctx context.Context, key string, body []byte, opts PutOptions) (Object, error)
	Delete(ctx context.Context, key string, opts DeleteOptions) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

type Object struct {
	Key         string
	Body        []byte
	ETag        string
	ContentType string
}

type ObjectInfo struct {
	Key  string
	ETag string
	Size int64
}

type PutOptions struct {
	IfMatch     string
	IfNoneMatch bool
	ContentType string
}

type DeleteOptions struct {
	IfMatch string
}

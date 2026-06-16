package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FS struct {
	Root string
}

func NewFS(root string) FS {
	return FS{Root: root}
}

func (fs FS) Get(_ context.Context, key string) (Object, error) {
	path, err := fs.path(key)
	if err != nil {
		return Object{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Object{}, ErrNotFound
		}
		return Object{}, fmt.Errorf("read object %q: %w", key, err)
	}
	return Object{Key: key, Body: body, ETag: etag(body)}, nil
}

func (fs FS) Put(_ context.Context, key string, body []byte, opts PutOptions) (Object, error) {
	path, err := fs.path(key)
	if err != nil {
		return Object{}, err
	}
	existing, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return Object{}, fmt.Errorf("read existing object %q: %w", key, readErr)
	}
	if opts.IfNoneMatch && exists {
		return Object{}, ErrPreconditionFailed
	}
	if opts.IfMatch != "" {
		if !exists || etag(existing) != opts.IfMatch {
			return Object{}, ErrPreconditionFailed
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Object{}, fmt.Errorf("create object directory: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return Object{}, fmt.Errorf("write object %q: %w", key, err)
	}
	return Object{Key: key, Body: append([]byte(nil), body...), ETag: etag(body), ContentType: opts.ContentType}, nil
}

func (fs FS) Delete(_ context.Context, key string, opts DeleteOptions) error {
	path, err := fs.path(key)
	if err != nil {
		return err
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return ErrNotFound
		}
		return fmt.Errorf("read existing object %q: %w", key, readErr)
	}
	if opts.IfMatch != "" && etag(existing) != opts.IfMatch {
		return ErrPreconditionFailed
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete object %q: %w", key, err)
	}
	return nil
}

func (fs FS) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	root := fs.Root
	var out []ObjectInfo
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return out, nil
	}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, ObjectInfo{Key: key, ETag: etag(body), Size: info.Size()})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func (fs FS) path(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return filepath.Join(fs.Root, clean), nil
}

func etag(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

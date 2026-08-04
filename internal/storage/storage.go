// Package storage defines the backend interface used by the filesystem layer.
// Concrete implementations (e.g. S3-compatible object storage) satisfy Backend.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("not found")

type Meta struct {
	Size    int64
	ModTime time.Time
	IsDir   bool
}

type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// Backend is the storage abstraction used by the FUSE filesystem.
// It is modeled on the operations of an S3-compatible object store; other
// backends (e.g. MEGA) implement it on top of their native API.
type Backend interface {
	Stat(ctx context.Context, path string) (*Meta, error)
	GetRange(ctx context.Context, path string, off, size int64) (io.ReadCloser, int64, error)
	GetFull(ctx context.Context, path string) (io.ReadCloser, int64, error)
	List(ctx context.Context, path string) ([]Entry, error)
	ListRecursive(ctx context.Context, path string) ([]string, error)
	Remove(ctx context.Context, path string) error
	RemovePlaceholder(ctx context.Context, path string) error
	Copy(ctx context.Context, src, dst string) error
	CopyPlaceholder(ctx context.Context, src, dst string) error
	Put(ctx context.Context, path string, r io.Reader, size int64, partSize int64) error
	PutPlaceholder(ctx context.Context, path string) error
}

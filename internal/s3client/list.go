package s3client

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Yuan720/vmount/internal/storage"
	"github.com/minio/minio-go/v7"
)

func (c *Client) Stat(ctx context.Context, path string) (*storage.Meta, error) {
	if c.negHit(path) {
		debugf("Stat %q -> negcache miss", path)
		return nil, storage.ErrNotFound
	}
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	k := c.key(path)
	info, err := c.cli.StatObject(ctx, c.bucket, k, minio.StatObjectOptions{})
	if err == nil {
		c.negRemove(path)
		return &storage.Meta{Size: info.Size, ModTime: info.LastModified}, nil
	}
	var er minio.ErrorResponse
	if !errors.As(err, &er) || er.Code != "NoSuchKey" {
		debugf("Stat %q statobj err: %v", path, err)
		return nil, err
	}

	entries, err := c.List(ctx, path)
	if err != nil {
		debugf("Stat %q list err: %v", path, err)
		return nil, err
	}
	if len(entries) > 0 {
		c.negRemove(path)
		var newest time.Time
		for _, e := range entries {
			if e.ModTime.After(newest) {
				newest = e.ModTime
			}
		}
		return &storage.Meta{Size: 0, ModTime: newest, IsDir: true}, nil
	}

	if c.usePlaceholder {
		placeholder, err := c.cli.StatObject(ctx, c.bucket, c.dirPrefix(path), minio.StatObjectOptions{})
		if err == nil {
			c.negRemove(path)
			return &storage.Meta{Size: 0, ModTime: placeholder.LastModified, IsDir: true}, nil
		}
	}
	if errors.As(err, &er) && er.Code == "NoSuchKey" {
		c.negSet(path)
	}
	debugf("Stat %q -> not found", path)
	return nil, storage.ErrNotFound
}

func (c *Client) GetRange(ctx context.Context, path string, off, size int64) (io.ReadCloser, int64, error) {
	var obj *minio.Object
	var err error
	ctx, _ = c.ctx(ctx)
	debugf("GetRange %q off=%d size=%d", path, off, size)
	err = retry(func() error {
		opts := minio.GetObjectOptions{}
		opts.SetRange(off, off+size-1)
		obj, err = c.cli.GetObject(ctx, c.bucket, c.key(path), opts)
		return err
	}, 3)
	if err != nil {
		debugf("GetRange %q err: %v", path, err)
		return nil, 0, err
	}
	return obj, size, nil
}

func (c *Client) GetFull(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	var obj *minio.Object
	var err error
	ctx, _ = c.ctx(ctx)
	err = retry(func() error {
		obj, err = c.cli.GetObject(ctx, c.bucket, c.key(path), minio.GetObjectOptions{})
		return err
	}, 3)
	if err != nil {
		return nil, 0, err
	}
	info, ierr := obj.Stat()
	if ierr != nil {
		obj.Close()
		return nil, 0, ierr
	}
	return obj, info.Size, nil
}

func (c *Client) List(ctx context.Context, path string) ([]storage.Entry, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()

	prefix := c.dirPrefix(path)
	ch := c.cli.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	})

	seen := map[string]bool{}
	var entries []storage.Entry
	for info := range ch {
		if info.Err != nil {
			return nil, info.Err
		}
		name := strings.TrimPrefix(info.Key, prefix)
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, "/") {
			name = strings.TrimSuffix(name, "/")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			mt := info.LastModified
			if mt.IsZero() {
				mt = time.Now()
			}
			entries = append(entries, storage.Entry{Name: name, IsDir: true, ModTime: mt})
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, storage.Entry{Name: name, IsDir: false, Size: info.Size, ModTime: info.LastModified})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func (c *Client) ListRecursive(ctx context.Context, path string) ([]string, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	prefix := c.dirPrefix(path)
	ch := c.cli.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	var paths []string
	for info := range ch {
		if info.Err != nil {
			return nil, info.Err
		}
		rel := strings.TrimPrefix(info.Key, prefix)
		paths = append(paths, rel)
	}
	return paths, nil
}

func (c *Client) Remove(ctx context.Context, path string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	if err := retry(func() error {
		return c.cli.RemoveObject(ctx, c.bucket, c.key(path), minio.RemoveObjectOptions{})
	}, 3); err != nil {
		return err
	}
	c.negRemove(path)
	return nil
}

func (c *Client) RemovePlaceholder(ctx context.Context, path string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	return retry(func() error {
		return c.cli.RemoveObject(ctx, c.bucket, c.dirPrefix(path), minio.RemoveObjectOptions{})
	}, 3)
}

func (c *Client) Copy(ctx context.Context, src, dst string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	return retry(func() error {
		_, err := c.cli.CopyObject(ctx,
			minio.CopyDestOptions{Bucket: c.bucket, Object: c.key(dst)},
			minio.CopySrcOptions{Bucket: c.bucket, Object: c.key(src)})
		return err
	}, 3)
}

func (c *Client) CopyPlaceholder(ctx context.Context, src, dst string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	return retry(func() error {
		_, err := c.cli.CopyObject(ctx,
			minio.CopyDestOptions{Bucket: c.bucket, Object: c.dirPrefix(dst)},
			minio.CopySrcOptions{Bucket: c.bucket, Object: c.dirPrefix(src)})
		return err
	}, 3)
}

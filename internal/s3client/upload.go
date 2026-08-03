package s3client

import (
	"context"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
)

func (c *Client) Put(ctx context.Context, path string, r io.Reader, size int64, partSize int64) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	return retry(func() error {
		_, err := c.cli.PutObject(ctx, c.bucket, c.key(path), r, size, minio.PutObjectOptions{
			PartSize:               uint64(partSize),
			ConcurrentStreamParts:  true,
			NumThreads:             4,
		})
		return err
	}, 3)
}

func (c *Client) PutPlaceholder(ctx context.Context, path string) error {
	return retry(func() error {
		_, err := c.cli.PutObject(ctx, c.bucket, c.dirPrefix(path), strings.NewReader(""), 0, minio.PutObjectOptions{})
		return err
	}, 3)
}

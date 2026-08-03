package s3client

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	ErrNotFound = errors.New("not found")
)

type Meta struct {
	Size       int64
	ModTime    time.Time
	IsDir      bool
}

type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

type Client struct {
	cli    *minio.Client
	bucket string
	prefix string
	timeout time.Duration
}

func New(endpoint, bucket, prefix, accessKey, secretKey string, useTLS bool, timeout time.Duration) (*Client, error) {
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useTLS,
	})
	if err != nil {
		return nil, err
	}
	prefix = strings.Trim(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &Client{
		cli:     cli,
		bucket:  bucket,
		prefix:  prefix,
		timeout: timeout,
	}, nil
}

func (c *Client) key(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return c.prefix
	}
	return c.prefix + path
}

func (c *Client) dirPrefix(path string) string {
	k := c.key(path)
	if k != "" {
		k += "/"
	}
	return k
}

func (c *Client) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, c.timeout)
}

func retry(fn func() error, attempts int) error {
	var err error
	backoff := 200 * time.Millisecond
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return err
}

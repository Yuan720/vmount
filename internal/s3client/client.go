package s3client

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Yuan720/vmount/internal/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const negTTL = 10 * time.Second

type Client struct {
	cli      *minio.Client
	bucket   string
	prefix   string
	timeout  time.Duration
	negMu    sync.Mutex
	negCache map[string]time.Time
}

var _ storage.Backend = (*Client)(nil)

func New(endpoint, bucket, prefix, accessKey, secretKey string, useTLS bool, timeout time.Duration, region string) (*Client, error) {
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimSuffix(endpoint, "/")
	if region == "" {
		region = "us-east-1"
	}
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useTLS,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	prefix = strings.Trim(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &Client{
		cli:      cli,
		bucket:   bucket,
		prefix:   prefix,
		timeout:  timeout,
		negCache: map[string]time.Time{},
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

func (c *Client) negHit(path string) bool {
	c.negMu.Lock()
	defer c.negMu.Unlock()
	t, ok := c.negCache[path]
	if !ok {
		return false
	}
	if time.Since(t) > negTTL {
		delete(c.negCache, path)
		return false
	}
	return true
}

func (c *Client) negSet(path string) {
	c.negMu.Lock()
	defer c.negMu.Unlock()
	c.negCache[path] = time.Now()
}

func (c *Client) negRemove(path string) {
	c.negMu.Lock()
	defer c.negMu.Unlock()
	delete(c.negCache, path)
}

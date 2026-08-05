package s3client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Yuan720/vmount/internal/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// uaTransport overrides the User-Agent so gateways that route by client
// type (e.g. the Hugging Face S3 gateway returns 302 redirects to CDN for
// unknown clients, but proxies directly for botocore-style clients) serve
// data without redirects that minio-go does not follow. It also injects a
// valid Last-Modified header when the gateway proxy omits it, since
// minio-go fails to parse an empty Last-Modified on GetObject responses.
type uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.ua != "" {
		r.Header.Set("User-Agent", t.ua)
	}
	// Some gateways (e.g. Hugging Face) reject the quoted, URL-encoded
	// x-amz-copy-source header that minio-go produces (AWS accepts it).
	fixCopySource(r)
	resp, err := t.base.RoundTrip(r)
	if err == nil && resp.Header.Get("Last-Modified") == "" {
		resp.Header.Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	}
	return resp, err
}

func fixCopySource(r *http.Request) {
	src := r.Header.Get("x-amz-copy-source")
	if src == "" {
		return
	}
	debugf("copy-source before: [%s]", src)
	src = strings.Trim(src, `"`)
	if dec, err := url.PathUnescape(src); err == nil {
		src = dec
	}
	if !strings.HasPrefix(src, "/") {
		src = "/" + src
	}
	r.Header.Set("x-amz-copy-source", src)
	debugf("copy-source after: [%s]", src)
}

const negTTL = 30 * time.Second

type Client struct {
	cli            *minio.Client
	bucket         string
	prefix         string
	timeout        time.Duration
	usePlaceholder bool
	negMu          sync.Mutex
	negCache       map[string]time.Time
}

var _ storage.Backend = (*Client)(nil)

func New(endpoint, bucket, prefix, accessKey, secretKey string, useTLS bool, timeout time.Duration, region, userAgent string, usePlaceholder bool) (*Client, error) {
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimSuffix(endpoint, "/")
	if region == "" {
		region = "us-east-1"
	}
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:    useTLS,
		Region:    region,
		Transport: &uaTransport{base: http.DefaultTransport, ua: userAgent},
	})
	if err != nil {
		return nil, err
	}
	prefix = strings.Trim(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &Client{
		cli:            cli,
		bucket:         bucket,
		prefix:         prefix,
		timeout:        timeout,
		usePlaceholder: usePlaceholder,
		negCache:       map[string]time.Time{},
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

// negHit reports whether path is in the negative cache and whether that
// entry is stale (older than negTTL). A stale hit may still be served
// immediately while a background re-check refreshes it.
func (c *Client) negHit(path string) (bool, bool) {
	c.negMu.Lock()
	defer c.negMu.Unlock()
	t, ok := c.negCache[path]
	if !ok {
		return false, false
	}
	return true, time.Since(t) > negTTL
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

// negRefresh re-checks a stale negative cache entry in the background.
func (c *Client) negRefresh(ctx context.Context, path string) {
	k := c.key(path)
	_, err := c.cli.StatObject(ctx, c.bucket, k, minio.StatObjectOptions{})
	if err == nil {
		c.negRemove(path)
	} else {
		var er minio.ErrorResponse
		if errors.As(err, &er) && er.Code == "NoSuchKey" {
			c.negSet(path)
		} else {
			c.negRemove(path)
		}
	}
}

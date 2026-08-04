package cache

import (
	"strings"
	"sync"
	"time"

	"github.com/Yuan720/vmount/internal/storage"
)

type DirCache struct {
	mu     sync.Mutex
	dirs   map[string]dirEntry
	ttl    time.Duration
}

type dirEntry struct {
	entries   []storage.Entry
	fetchedAt time.Time
}

func NewDirCache(ttl time.Duration) *DirCache {
	return &DirCache{dirs: map[string]dirEntry{}, ttl: ttl}
}

// Get returns cached entries. ok reports whether the path is cached at all;
// stale reports whether the entry is older than the TTL (caller may return
// the stale data immediately and refresh in the background).
func (d *DirCache) Get(path string) ([]storage.Entry, bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.dirs[path]
	if !ok {
		return nil, false, false
	}
	return e.entries, true, d.ttl > 0 && time.Since(e.fetchedAt) > d.ttl
}

func (d *DirCache) Set(path string, entries []storage.Entry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirs[path] = dirEntry{entries: entries, fetchedAt: time.Now()}
}

func (d *DirCache) Invalidate(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.dirs, path)
	if path == "" {
		return
	}
	prefix := path + "/"
	for p := range d.dirs {
		if strings.HasPrefix(p, prefix) {
			delete(d.dirs, p)
		}
	}
}

func (d *DirCache) InvalidateAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirs = map[string]dirEntry{}
}

type MetaCache struct {
	mu   sync.Mutex
	meta map[string]metaEntry
	ttl  time.Duration
}

type metaEntry struct {
	meta      storage.Meta
	fetchedAt time.Time
}

func NewMetaCache(ttl time.Duration) *MetaCache {
	return &MetaCache{meta: map[string]metaEntry{}, ttl: ttl}
}

// Get returns cached meta. ok reports whether the path is cached at all;
// stale reports whether the entry is older than the TTL.
func (m *MetaCache) Get(path string) (storage.Meta, bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.meta[path]
	if !ok {
		return storage.Meta{}, false, false
	}
	return e.meta, true, m.ttl > 0 && time.Since(e.fetchedAt) > m.ttl
}

func (m *MetaCache) Set(path string, meta storage.Meta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meta[path] = metaEntry{meta: meta, fetchedAt: time.Now()}
}

func (m *MetaCache) Invalidate(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.meta, path)
}

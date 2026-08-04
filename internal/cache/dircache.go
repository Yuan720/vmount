package cache

import (
	"encoding/json"
	"os"
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
	Entries   []storage.Entry `json:"entries"`
	FetchedAt time.Time       `json:"fetched_at"`
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
	return e.Entries, true, d.ttl > 0 && time.Since(e.FetchedAt) > d.ttl
}

func (d *DirCache) Set(path string, entries []storage.Entry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirs[path] = dirEntry{Entries: entries, FetchedAt: time.Now()}
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

func (d *DirCache) Save(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := json.Marshal(d.dirs)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (d *DirCache) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return json.Unmarshal(data, &d.dirs)
}

type MetaCache struct {
	mu   sync.Mutex
	meta map[string]metaEntry
	ttl  time.Duration
}

type metaEntry struct {
	Meta      storage.Meta `json:"meta"`
	FetchedAt time.Time    `json:"fetched_at"`
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
	return e.Meta, true, m.ttl > 0 && time.Since(e.FetchedAt) > m.ttl
}

func (m *MetaCache) Set(path string, meta storage.Meta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meta[path] = metaEntry{Meta: meta, FetchedAt: time.Now()}
}

func (m *MetaCache) Invalidate(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.meta, path)
}

func (m *MetaCache) Save(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := json.Marshal(m.meta)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (m *MetaCache) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Unmarshal(data, &m.meta)
}

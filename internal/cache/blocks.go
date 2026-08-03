package cache

import (
	"sync"
	"time"
)

type BlockCache struct {
	mu       sync.Mutex
	entries  map[string]blockEntry
	curBytes int64
	maxBytes int64
}

type blockEntry struct {
	data []byte
	mod  time.Time
}

func NewBlockCache(maxBytes int64) *BlockCache {
	if maxBytes <= 0 {
		maxBytes = 0
	}
	return &BlockCache{
		entries:  map[string]blockEntry{},
		maxBytes: maxBytes,
	}
}

func (b *BlockCache) Get(path string, mod time.Time) ([]byte, bool) {
	if b.maxBytes == 0 {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[path]
	if !ok {
		return nil, false
	}
	if !e.mod.Equal(mod) {
		delete(b.entries, path)
		b.curBytes -= int64(len(e.data))
		return nil, false
	}
	return e.data, true
}

func (b *BlockCache) Put(path string, mod time.Time, data []byte) {
	if b.maxBytes == 0 || len(data) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.entries[path]; ok {
		b.curBytes -= int64(len(old.data))
	}
	b.entries[path] = blockEntry{data: data, mod: mod}
	b.curBytes += int64(len(data))
	for b.curBytes > b.maxBytes {
		for p := range b.entries {
			delete(b.entries, p)
		}
		b.curBytes = 0
		break
	}
}

func (b *BlockCache) Invalidate(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.entries[path]; ok {
		b.curBytes -= int64(len(e.data))
		delete(b.entries, path)
	}
}

func (b *BlockCache) InvalidatePrefix(prefix string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for p := range b.entries {
		if p == prefix || (len(p) > len(prefix) && p[:len(prefix)] == prefix && p[len(prefix)] == '#') {
			b.curBytes -= int64(len(b.entries[p].data))
			delete(b.entries, p)
		}
	}
}

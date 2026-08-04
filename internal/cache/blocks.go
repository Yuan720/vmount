package cache

import (
	"container/list"
	"sync"
	"time"
)

type BlockCache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	lru      *list.List
	curBytes int64
	maxBytes int64
}

type blockEntry struct {
	key  string
	data []byte
	mod  time.Time
}

func NewBlockCache(maxBytes int64) *BlockCache {
	if maxBytes <= 0 {
		maxBytes = 0
	}
	return &BlockCache{
		entries:  map[string]*list.Element{},
		lru:      list.New(),
		maxBytes: maxBytes,
	}
}

func (b *BlockCache) Get(path string, mod time.Time) ([]byte, bool) {
	if b.maxBytes == 0 {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	el, ok := b.entries[path]
	if !ok {
		return nil, false
	}
	e := el.Value.(*blockEntry)
	if !e.mod.Equal(mod) {
		b.removeElement(el)
		return nil, false
	}
	b.lru.MoveToFront(el)
	return e.data, true
}

func (b *BlockCache) Put(path string, mod time.Time, data []byte) {
	if b.maxBytes == 0 || len(data) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if el, ok := b.entries[path]; ok {
		b.removeElement(el)
	}
	e := &blockEntry{key: path, data: data, mod: mod}
	b.entries[path] = b.lru.PushFront(e)
	b.curBytes += int64(len(data))
	for b.curBytes > b.maxBytes {
		back := b.lru.Back()
		if back == nil {
			break
		}
		if back == b.entries[path] {
			b.removeElement(back)
			break
		}
		b.removeElement(back)
	}
}

func (b *BlockCache) removeElement(el *list.Element) {
	e := el.Value.(*blockEntry)
	delete(b.entries, e.key)
	b.lru.Remove(el)
	b.curBytes -= int64(len(e.data))
}

func (b *BlockCache) Invalidate(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if el, ok := b.entries[path]; ok {
		b.removeElement(el)
	}
}

func (b *BlockCache) InvalidatePrefix(prefix string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for p, el := range b.entries {
		if p == prefix || (len(p) > len(prefix) && p[:len(prefix)] == prefix && p[len(prefix)] == '#') {
			b.removeElement(el)
		}
	}
}

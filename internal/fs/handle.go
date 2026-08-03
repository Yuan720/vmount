package fs

import (
	"sync"
)

type handle struct {
	path    string
	write   bool
	spool   bool
}

type handleTable struct {
	mu       sync.Mutex
	next     uint64
	handles  map[uint64]*handle
}

func newHandleTable() *handleTable {
	return &handleTable{next: 1, handles: map[uint64]*handle{}}
}

func (t *handleTable) Add(h *handle) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	fh := t.next
	t.next++
	t.handles[fh] = h
	return fh
}

func (t *handleTable) Get(fh uint64) *handle {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.handles[fh]
}

func (t *handleTable) Remove(fh uint64) *handle {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.handles[fh]
	delete(t.handles, fh)
	return h
}

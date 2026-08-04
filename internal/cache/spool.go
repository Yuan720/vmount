package cache

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Spool struct {
	dir  string
	mu   sync.Mutex
	open map[string]*spoolEntry
}

type spoolEntry struct {
	mu      sync.Mutex
	file    *os.File
	size    int64
	refs    int
	created time.Time
}

func NewSpool(dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Spool{dir: dir, open: map[string]*spoolEntry{}}, nil
}

func (s *Spool) pathFor(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:]))
}

func (s *Spool) Open(key string) (*spoolEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.open[key]; ok {
		e.refs++
		return e, nil
	}
	p := s.pathFor(key)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	e := &spoolEntry{file: f, size: info.Size(), refs: 1, created: time.Now()}
	s.open[key] = e
	return e, nil
}

func (e *spoolEntry) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.file.WriteAt(p, e.size)
	if n > 0 {
		e.size += int64(n)
	}
	return n, err
}

func (e *spoolEntry) WriteAt(p []byte, off int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.file.WriteAt(p, off)
	if off+int64(n) > e.size {
		e.size = off + int64(n)
	}
	return n, err
}

func (e *spoolEntry) ReadAt(p []byte, off int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.file.ReadAt(p, off)
}

func (e *spoolEntry) Truncate(size int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.file.Truncate(size); err != nil {
		return err
	}
	e.size = size
	return nil
}

func (e *spoolEntry) Size() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.size
}

func (e *spoolEntry) SeekReader() (io.ReadSeeker, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return e.file, nil
}

func (s *Spool) Close(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.open[key]
	if !ok {
		return nil
	}
	e.refs--
	if e.refs <= 0 {
		delete(s.open, key)
		return e.file.Close()
	}
	return nil
}

func (s *Spool) Exists(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.open[key]; ok {
		return true
	}
	_, err := os.Stat(s.pathFor(key))
	return err == nil
}

func (s *Spool) SizeOf(key string) (int64, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.open[key]; ok {
		e.mu.Lock()
		size := e.size
		e.mu.Unlock()
		return size, e.created, true
	}
	info, err := os.Stat(s.pathFor(key))
	if err != nil {
		return 0, time.Time{}, false
	}
	return info.Size(), info.ModTime(), true
}

func (s *Spool) Remove(key string) error {
	s.mu.Lock()
	if _, ok := s.open[key]; ok {
		s.mu.Unlock()
		return fmt.Errorf("spool %s still open", key)
	}
	s.mu.Unlock()
	return os.Remove(s.pathFor(key))
}

func (s *Spool) Move(oldKey, newKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.open[oldKey]; ok {
		delete(s.open, oldKey)
		s.open[newKey] = e
		return nil
	}
	return os.Rename(s.pathFor(oldKey), s.pathFor(newKey))
}

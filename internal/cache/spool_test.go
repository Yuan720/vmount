package cache

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestSpool(t *testing.T) *Spool {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "spool")
	s, err := NewSpool(dir)
	if err != nil {
		t.Fatalf("NewSpool: %v", err)
	}
	return s
}

func TestSpoolWriteRead(t *testing.T) {
	s := newTestSpool(t)
	e, err := s.Open("spool:k1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := e.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if _, err := e.WriteAt([]byte("world"), 5); err != nil {
		t.Fatalf("WriteAt2: %v", err)
	}
	if got := e.Size(); got != 10 {
		t.Fatalf("Size = %d, want 10", got)
	}
	buf := make([]byte, 10)
	if _, err := e.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf) != "helloworld" {
		t.Fatalf("ReadAt = %q", buf)
	}
	if err := s.Close("spool:k1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSpoolReopenPersists(t *testing.T) {
	s := newTestSpool(t)
	e, _ := s.Open("spool:k2")
	e.WriteAt([]byte("data"), 0)
	s.Close("spool:k2")
	e2, err := s.Open("spool:k2")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := e2.Size(); got != 4 {
		t.Fatalf("reopened Size = %d, want 4", got)
	}
	buf := make([]byte, 4)
	e2.ReadAt(buf, 0)
	if string(buf) != "data" {
		t.Fatalf("reopened content = %q", buf)
	}
	s.Close("spool:k2")
}

func TestSpoolTruncate(t *testing.T) {
	s := newTestSpool(t)
	e, _ := s.Open("spool:k3")
	e.WriteAt([]byte("0123456789"), 0)
	if err := e.Truncate(4); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	buf := make([]byte, 4)
	e.ReadAt(buf, 0)
	if string(buf) != "0123" {
		t.Fatalf("after truncate = %q", buf)
	}
	if err := e.Truncate(8); err != nil {
		t.Fatalf("Truncate up: %v", err)
	}
	buf2 := make([]byte, 8)
	e.ReadAt(buf2, 0)
	if string(buf2[:4]) != "0123" {
		t.Fatalf("prefix lost after truncate-up: %q", buf2)
	}
	for _, b := range buf2[4:] {
		if b != 0 {
			t.Fatalf("pad not zero: %q", buf2)
		}
	}
	s.Close("spool:k3")
}

func TestSpoolRemoveOpenFails(t *testing.T) {
	s := newTestSpool(t)
	_, _ = s.Open("spool:k4")
	if err := s.Remove("spool:k4"); err == nil {
		t.Fatalf("Remove of open spool should fail")
	}
	s.Close("spool:k4")
	if err := s.Remove("spool:k4"); err != nil {
		t.Fatalf("Remove after close: %v", err)
	}
	if _, err := os.Stat(s.pathFor("spool:k4")); !os.IsNotExist(err) {
		t.Fatalf("file should be removed")
	}
}

func TestSpoolSeekReader(t *testing.T) {
	s := newTestSpool(t)
	e, _ := s.Open("spool:k5")
	e.WriteAt([]byte("seekdata"), 0)
	rs, err := e.SeekReader()
	if err != nil {
		t.Fatalf("SeekReader: %v", err)
	}
	buf, err := io.ReadAll(rs)
	if err != nil || string(buf) != "seekdata" {
		t.Fatalf("read all = %q, %v", buf, err)
	}
	s.Close("spool:k5")
}

func TestSpoolConcurrentWrites(t *testing.T) {
	s := newTestSpool(t)
	e, _ := s.Open("spool:k6")
	const size = 64 * 1024
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			off := int64(g * 1024)
			b := make([]byte, 1024)
			for i := range b {
				b[i] = byte(g)
			}
			e.WriteAt(b, off)
		}(g)
	}
	wg.Wait()
	if got := e.Size(); got != size {
		t.Fatalf("Size = %d, want %d", got, size)
	}
	buf := make([]byte, size)
	if _, err := e.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	for g := 0; g < 8; g++ {
		for i := 0; i < 1024; i++ {
			if buf[g*1024+i] != byte(g) {
				t.Fatalf("region %d corrupted at %d", g, i)
			}
		}
	}
	s.Close("spool:k6")
}

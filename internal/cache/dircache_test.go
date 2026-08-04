package cache

import (
	"testing"
	"time"

	"github.com/Yuan720/vmount/internal/storage"
)

func TestDirCacheSetGet(t *testing.T) {
	d := NewDirCache(time.Minute)
	entries := []storage.Entry{{Name: "a"}}
	d.Set("", entries)
	got, ok := d.Get("")
	if !ok || len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("Get = %v,%v", got, ok)
	}
}

func TestDirCacheTTL(t *testing.T) {
	d := NewDirCache(time.Millisecond)
	d.Set("x", []storage.Entry{})
	time.Sleep(5 * time.Millisecond)
	if _, ok := d.Get("x"); ok {
		t.Fatalf("expired entry should miss")
	}
}

func TestDirCacheInvalidateRootOnly(t *testing.T) {
	d := NewDirCache(time.Minute)
	d.Set("", []storage.Entry{})
	d.Set("a", []storage.Entry{})
	d.Set("a/b", []storage.Entry{})
	d.Invalidate("")
	if _, ok := d.Get(""); ok {
		t.Fatalf("root entry should be invalidated")
	}
	if _, ok := d.Get("a"); !ok {
		t.Fatalf("subtree should survive root invalidation")
	}
	if _, ok := d.Get("a/b"); !ok {
		t.Fatalf("deep subtree should survive root invalidation")
	}
}

func TestDirCacheInvalidateSubtree(t *testing.T) {
	d := NewDirCache(time.Minute)
	d.Set("a", []storage.Entry{})
	d.Set("a/b", []storage.Entry{})
	d.Set("ab", []storage.Entry{})
	d.Set("a/c", []storage.Entry{})
	d.Invalidate("a")
	if _, ok := d.Get("a"); ok {
		t.Fatalf("a should be invalidated")
	}
	if _, ok := d.Get("a/b"); ok {
		t.Fatalf("a/b should be invalidated")
	}
	if _, ok := d.Get("a/c"); ok {
		t.Fatalf("a/c should be invalidated")
	}
	if _, ok := d.Get("ab"); !ok {
		t.Fatalf("ab should survive (not a child of a)")
	}
}

func TestDirCacheInvalidateAll(t *testing.T) {
	d := NewDirCache(time.Minute)
	d.Set("", []storage.Entry{})
	d.Set("x", []storage.Entry{})
	d.InvalidateAll()
	if _, ok := d.Get(""); ok {
		t.Fatalf("root should be gone")
	}
	if _, ok := d.Get("x"); ok {
		t.Fatalf("x should be gone")
	}
}

func TestMetaCacheTTL(t *testing.T) {
	m := NewMetaCache(0)
	m.Set("f", storage.Meta{Size: 10})
	got, ok := m.Get("f")
	if !ok || got.Size != 10 {
		t.Fatalf("Get = %v,%v", got, ok)
	}
}

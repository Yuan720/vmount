package cache

import (
	"testing"
	"time"
)

func TestBlockCachePutGet(t *testing.T) {
	b := NewBlockCache(1024)
	mod := time.Now()
	b.Put("a#0", mod, []byte("hello"))
	got, ok := b.Get("a#0", mod)
	if !ok || string(got) != "hello" {
		t.Fatalf("Get = %q,%v", got, ok)
	}
}

func TestBlockCacheDisabled(t *testing.T) {
	b := NewBlockCache(0)
	b.Put("a#0", time.Now(), []byte("x"))
	if _, ok := b.Get("a#0", time.Now()); ok {
		t.Fatalf("disabled cache should not hit")
	}
}

func TestBlockCacheEvictLRU(t *testing.T) {
	b := NewBlockCache(200)
	mod := time.Now()
	b.Put("k1", mod, make([]byte, 100))
	b.Put("k2", mod, make([]byte, 100))
	if _, ok := b.Get("k2", mod); !ok {
		t.Fatalf("k2 should be cached")
	}
	b.Put("k3", mod, make([]byte, 100))
	if _, ok := b.Get("k1", mod); ok {
		t.Fatalf("k1 (least recently used) should be evicted")
	}
	if _, ok := b.Get("k2", mod); !ok {
		t.Fatalf("k2 (recently used) should survive")
	}
	if _, ok := b.Get("k3", mod); !ok {
		t.Fatalf("k3 should survive")
	}
}

func TestBlockCacheGetRefreshesLRU(t *testing.T) {
	b := NewBlockCache(300)
	mod := time.Now()
	b.Put("k1", mod, make([]byte, 100))
	b.Put("k2", mod, make([]byte, 100))
	b.Put("k3", mod, make([]byte, 100))
	b.Get("k1", mod)
	b.Put("k4", mod, make([]byte, 100))
	if _, ok := b.Get("k2", mod); ok {
		t.Fatalf("k2 should be evicted (k1 was touched)")
	}
	if _, ok := b.Get("k1", mod); !ok {
		t.Fatalf("k1 was recently used and should survive")
	}
}

func TestBlockCacheModChange(t *testing.T) {
	b := NewBlockCache(1024)
	b.Put("a#0", time.Now(), []byte("old"))
	if _, ok := b.Get("a#0", time.Now().Add(time.Second)); ok {
		t.Fatalf("mod mismatch should miss")
	}
	if _, ok := b.Get("a#0", time.Now()); ok {
		t.Fatalf("stale entry should have been removed")
	}
}

func TestBlockCacheSingleBigBlock(t *testing.T) {
	b := NewBlockCache(100)
	mod := time.Now()
	b.Put("big", mod, make([]byte, 500))
	if _, ok := b.Get("big", mod); ok {
		t.Fatalf("block larger than cache should not be cached")
	}
	b.Put("small", mod, make([]byte, 50))
	if _, ok := b.Get("small", mod); !ok {
		t.Fatalf("small block should be cached")
	}
}

func TestBlockCacheInvalidate(t *testing.T) {
	b := NewBlockCache(1024)
	mod := time.Now()
	b.Put("a#0", mod, []byte("1"))
	b.Put("a#1048576", mod, []byte("2"))
	b.Put("other#0", mod, []byte("3"))
	b.InvalidatePrefix("a")
	if _, ok := b.Get("a#0", mod); ok {
		t.Fatalf("a#0 should be invalidated")
	}
	if _, ok := b.Get("a#1048576", mod); ok {
		t.Fatalf("a#1048576 should be invalidated")
	}
	if _, ok := b.Get("other#0", mod); !ok {
		t.Fatalf("other#0 should survive")
	}
	b.Invalidate("other#0")
	if _, ok := b.Get("other#0", mod); ok {
		t.Fatalf("other#0 should be invalidated")
	}
}

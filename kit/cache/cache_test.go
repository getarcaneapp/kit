package cache

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

func age(c *Cache[string, int], key string, by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.items[key].Value.(*entry[string, int])
	e.fresh = e.fresh.Add(-by)
	e.dead = e.dead.Add(-by)
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := &Cache[string, int]{
		Capacity: 2,
	}
	c.Set(Entry[string, int]{Key: "a", Value: 1}, Entry[string, int]{Key: "b", Value: 2})
	if _, found, _ := c.Get("a"); !found {
		t.Fatal("a missing before eviction")
	}
	c.Set(Entry[string, int]{Key: "c", Value: 3})

	if _, found := c.Peek("b"); found {
		t.Fatal("b should be evicted as least recently used")
	}
	if got := c.Keys(); !slices.Equal(got, []string{"c", "a"}) {
		t.Fatalf("keys = %v, want [c a]", got)
	}
}

func TestCacheExpiry(t *testing.T) {
	tests := []struct {
		name     string
		cacheTTL time.Duration
		entryTTL time.Duration
	}{
		{name: "cache ttl", cacheTTL: 10 * time.Millisecond},
		{name: "entry ttl overrides cache ttl", cacheTTL: time.Hour, entryTTL: 10 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache[string, int]{
				TTL: tt.cacheTTL,
			}
			c.Set(Entry[string, int]{
				Key:   "k",
				Value: 1,
				TTL:   tt.entryTTL,
			})
			time.Sleep(20 * time.Millisecond)

			if got, found := c.Peek("k"); !found || got != 1 {
				t.Fatalf("Peek = %d, %t; want stale 1, true", got, found)
			}
			if len(c.Keys()) != 0 || len(c.Values()) != 0 {
				t.Fatal("Keys and Values must skip expired entries")
			}
			if _, found, _ := c.Get("k"); found {
				t.Fatal("Get returned an expired entry")
			}
			if _, found := c.Peek("k"); found {
				t.Fatal("Get should remove the expired entry")
			}
		})
	}
}

func TestCacheJanitorSweepsUntilClosed(t *testing.T) {
	c := &Cache[string, int]{
		TTL:             5 * time.Millisecond,
		JanitorInterval: time.Millisecond,
	}
	c.Set(Entry[string, int]{Key: "k", Value: 1})
	eventually(t, func() bool { return c.Len() == 0 })

	c.Close()
	c.Close()
	c.Set(Entry[string, int]{Key: "k", Value: 1})
	time.Sleep(20 * time.Millisecond)
	if c.Len() != 1 {
		t.Fatal("janitor kept sweeping after Close")
	}
}

func TestCacheCollapsesConcurrentLoads(t *testing.T) {
	var loads atomic.Int32
	release := make(chan struct{})
	c := &Cache[string, string]{
		Load: func(key string) (string, bool, error) {
			loads.Add(1)
			<-release
			return key + "-value", true, nil
		},
	}

	var wg sync.WaitGroup
	results := make([]string, 20)
	for i := range results {
		wg.Go(func() {
			results[i], _, _ = c.Get("k")
		})
	}
	eventually(t, func() bool { return loads.Load() == 1 })
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()

	if loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", loads.Load())
	}
	for _, got := range results {
		if got != "k-value" {
			t.Fatalf("result = %q, want k-value", got)
		}
	}
}

func TestCacheDoesNotCacheLoadErrors(t *testing.T) {
	var loads atomic.Int32
	c := &Cache[string, int]{
		Load: func(string) (int, bool, error) {
			loads.Add(1)
			return 0, false, errors.New("boom")
		},
	}
	for range 2 {
		if _, _, err := c.Get("k"); err == nil {
			t.Fatal("expected load error")
		}
	}
	if loads.Load() != 2 {
		t.Fatalf("loads = %d, want 2", loads.Load())
	}
}

func TestCacheRevalidation(t *testing.T) {
	tests := []struct {
		reloadErr error
		name      string
		wantValue int
		wantLoads int32
		separate  bool
	}{
		{name: "reload replaces stale value", wantValue: 2, wantLoads: 2},
		{name: "failed reload keeps stale value", reloadErr: errors.New("boom"), wantValue: 1, wantLoads: 2},
		{name: "revalidate loader replaces stale value", separate: true, wantValue: 100, wantLoads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loads atomic.Int32
			var revalidate func(string) (int, bool, error)
			if tt.separate {
				revalidate = func(string) (int, bool, error) {
					return 100, true, nil
				}
			}
			c := &Cache[string, int]{
				TTL:             time.Hour,
				RevalidateAfter: time.Hour,
				Load: func(string) (int, bool, error) {
					n := loads.Add(1)
					if n > 1 && tt.reloadErr != nil {
						return 0, false, tt.reloadErr
					}
					return int(n), true, nil
				},
				Revalidate: revalidate,
			}

			if got, _, _ := c.Get("k"); got != 1 {
				t.Fatalf("first Get = %d, want 1", got)
			}
			age(c, "k", 90*time.Minute)
			if got, found, err := c.Get("k"); got != 1 || !found || err != nil {
				t.Fatalf("stale Get = %d, %t, %v; want 1, true, nil", got, found, err)
			}
			eventually(t, func() bool {
				c.mu.Lock()
				defer c.mu.Unlock()
				return len(c.inflight) == 0
			})
			if got, found, _ := c.Get("k"); got != tt.wantValue || !found {
				t.Fatalf("Get after reload = %d, %t; want %d, true", got, found, tt.wantValue)
			}
			if loads.Load() != tt.wantLoads {
				t.Fatalf("loads = %d, want %d: a kept or reloaded entry must be fresh again", loads.Load(), tt.wantLoads)
			}
		})
	}
}

func TestCacheFailedRevalidationSurvivesExpiry(t *testing.T) {
	release := make(chan struct{})
	c := &Cache[string, int]{
		Revalidate: func(string) (int, bool, error) {
			<-release
			return 0, false, errors.New("boom")
		},
		TTL:             time.Hour,
		JanitorInterval: time.Millisecond,
		RevalidateAfter: time.Hour,
	}
	defer c.Close()
	c.Set(Entry[string, int]{Key: "k", Value: 1})
	age(c, "k", 90*time.Minute)
	if got, _, _ := c.Get("k"); got != 1 {
		t.Fatalf("stale Get = %d, want 1", got)
	}
	age(c, "k", time.Hour)
	time.Sleep(20 * time.Millisecond)
	if _, found := c.Peek("k"); !found {
		t.Fatal("expiry dropped an entry with a reload in flight")
	}

	close(release)
	eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.inflight) == 0
	})
	if got, found, _ := c.Get("k"); got != 1 || !found {
		t.Fatalf("Get after failed reload = %d, %t; want 1, true", got, found)
	}
}

func TestCacheMisses(t *testing.T) {
	tests := []struct {
		name        string
		cacheMisses bool
		wantLoads   int32
	}{
		{name: "misses reload", wantLoads: 2},
		{name: "misses cached", cacheMisses: true, wantLoads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loads atomic.Int32
			c := &Cache[string, int]{
				TTL:         time.Hour,
				CacheMisses: tt.cacheMisses,
				Load: func(string) (int, bool, error) {
					loads.Add(1)
					return 7, false, nil
				},
			}
			for range 2 {
				if got, found, err := c.Get("k"); got != 0 || found || err != nil {
					t.Fatalf("Get = %d, %t, %v; want 0, false, nil", got, found, err)
				}
			}
			if loads.Load() != tt.wantLoads {
				t.Fatalf("loads = %d, want %d", loads.Load(), tt.wantLoads)
			}
			if _, found := c.Peek("k"); found {
				t.Fatal("Peek must not report a cached miss")
			}
		})
	}
}

func TestCacheCloneIsolatesCallers(t *testing.T) {
	c := &Cache[string, []int]{
		Clone: slices.Clone[[]int],
	}
	stored := []int{1, 2}
	c.Set(Entry[string, []int]{Key: "k", Value: stored})
	stored[0] = 99

	got, _, _ := c.Get("k")
	got[1] = 99
	peeked, _ := c.Peek("k")
	if !slices.Equal(peeked, []int{1, 2}) {
		t.Fatalf("cached value = %v, want [1 2]", peeked)
	}
}

func TestCacheCloneMayUseCache(t *testing.T) {
	var c *Cache[string, int]
	c = &Cache[string, int]{
		Clone: func(v int) int {
			c.Len()
			return v
		},
	}
	c.Set(Entry[string, int]{Key: "k", Value: 1})
	if got, _, _ := c.Get("k"); got != 1 {
		t.Fatalf("Get = %d, want 1", got)
	}
}

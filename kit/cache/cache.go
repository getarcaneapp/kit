// Package cache provides a generic in-memory LRU cache with TTL expiry,
// single-flight loading, and stale-while-revalidate reads.
package cache

import (
	"cmp"
	"container/list"
	"errors"
	"sync"
	"time"
)

type (
	// Cache is an LRU cache configured by its exported fields. Build it with a
	// keyed literal and always use it through a pointer. The fields must not
	// change after first use; replace the whole cache to change its config.
	Cache[K comparable, V any] struct {
		// Load fills misses for Get. The bool reports whether the key exists.
		Load func(K) (V, bool, error)
		// Revalidate reloads stale entries. Nil uses the loader of the read that found the entry.
		Revalidate func(K) (V, bool, error)
		// Clone copies values on the way in and out, so callers never share cached memory.
		Clone func(V) V

		items    map[K]*list.Element
		order    *list.List
		inflight map[K]*call[V]
		stop     chan struct{}
		stopped  chan struct{}

		// Capacity bounds the number of entries, evicting the least recently used first. Zero means unbounded.
		Capacity int
		// TTL is how long an entry stays fresh. Zero means entries never expire.
		TTL time.Duration
		// JanitorInterval sweeps expired entries in the background at this interval. Zero disables the sweep.
		JanitorInterval time.Duration
		// RevalidateAfter keeps an entry this long past its TTL. Reads in that window return the
		// stale value and reload it in the background; a failed reload keeps the stale value.
		RevalidateAfter time.Duration
		// CacheMisses remembers keys a loader reported as not found until their TTL passes.
		CacheMisses bool

		once      sync.Once
		closeOnce sync.Once
		mu        sync.Mutex
	}

	// Entry is a value written with Cache.Set. A zero TTL uses Cache.TTL.
	Entry[K comparable, V any] struct {
		Key   K
		Value V
		TTL   time.Duration
	}

	entry[K comparable, V any] struct {
		fresh time.Time
		dead  time.Time
		key   K
		value V
		found bool
	}

	call[V any] struct {
		value V
		err   error
		done  chan struct{}
		found bool
	}
)

var errLoaderPanicked = errors.New("cache: loader panicked")

// Get returns the value for key, filling a miss with Load when it is set.
func (c *Cache[K, V]) Get(key K) (V, bool, error) {
	return c.GetOrLoad(key, c.Load)
}

// GetOrLoad returns the value for key, filling a miss with load. Concurrent misses
// for one key share a single load. A nil load only reads the cache.
func (c *Cache[K, V]) GetOrLoad(key K, load func(K) (V, bool, error)) (V, bool, error) {
	c.init()
	now := time.Now()
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry[K, V])
		if !e.expired(now) {
			c.order.MoveToFront(el)
			reload := c.Revalidate
			if reload == nil {
				reload = load
			}
			if _, busy := c.inflight[key]; reload != nil && !busy && e.stale(now) {
				go c.run(key, reload, c.startLocked(key), e)
			}
			c.mu.Unlock()
			return c.read(e.value, e.found), e.found, nil
		}
		if _, busy := c.inflight[key]; !busy {
			c.removeLocked(el)
		}
	}
	if load == nil {
		c.mu.Unlock()
		var zero V
		return zero, false, nil
	}
	cl, busy := c.inflight[key]
	if !busy {
		cl = c.startLocked(key)
	}
	c.mu.Unlock()
	if !busy {
		c.run(key, load, cl, nil)
	}
	<-cl.done
	if cl.err != nil {
		var zero V
		return zero, false, cl.err
	}
	return c.read(cl.value, cl.found), cl.found, nil
}

// Set stores entries, evicting the least recently used ones past Capacity.
func (c *Cache[K, V]) Set(entries ...Entry[K, V]) {
	c.init()
	values := make([]V, len(entries))
	for i, e := range entries {
		values[i] = c.read(e.Value, true)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, e := range entries {
		c.setLocked(e.Key, values[i], true, cmp.Or(e.TTL, c.TTL))
	}
}

// Peek returns the value for key without loading, refreshing its recency, or checking expiry.
func (c *Cache[K, V]) Peek(key K) (V, bool) {
	c.init()
	c.mu.Lock()
	el, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	e := el.Value.(*entry[K, V])
	c.mu.Unlock()
	return c.read(e.value, e.found), e.found
}

// Delete removes keys from the cache.
func (c *Cache[K, V]) Delete(keys ...K) {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keys {
		if el, ok := c.items[key]; ok {
			c.removeLocked(el)
		}
	}
}

// Purge removes every entry.
func (c *Cache[K, V]) Purge() {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.items)
	c.order.Init()
}

// Keys returns the keys of unexpired values, most recently used first.
func (c *Cache[K, V]) Keys() []K {
	live := c.live()
	keys := make([]K, 0, len(live))
	for _, e := range live {
		keys = append(keys, e.key)
	}
	return keys
}

// Values returns the unexpired values, most recently used first.
func (c *Cache[K, V]) Values() []V {
	live := c.live()
	values := make([]V, 0, len(live))
	for _, e := range live {
		values = append(values, c.read(e.value, true))
	}
	return values
}

// Range calls f for each unexpired value, most recently used first, until f returns false.
// It iterates a snapshot, so f may use the cache.
func (c *Cache[K, V]) Range(f func(K, V) bool) {
	for _, e := range c.live() {
		if !f(e.key, c.read(e.value, true)) {
			return
		}
	}
}

// Len returns the number of entries, including cached misses and expired entries not yet swept.
func (c *Cache[K, V]) Len() int {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// Close stops the janitor and waits for it to exit. The cache stays usable without it.
func (c *Cache[K, V]) Close() {
	c.init()
	c.closeOnce.Do(func() {
		close(c.stop)
	})
	<-c.stopped
}

func (c *Cache[K, V]) init() {
	c.once.Do(func() {
		c.items = make(map[K]*list.Element)
		c.order = list.New()
		c.inflight = make(map[K]*call[V])
		c.stop = make(chan struct{})
		c.stopped = make(chan struct{})
		if c.JanitorInterval > 0 {
			go c.janitor()
		} else {
			close(c.stopped)
		}
	})
}

func (c *Cache[K, V]) startLocked(key K) *call[V] {
	cl := &call[V]{
		done: make(chan struct{}),
		err:  errLoaderPanicked,
	}
	c.inflight[key] = cl
	return cl
}

// run loads key into cl and stores the result. When a reload of stale fails, the
// stale entry gets a fresh TTL unless it was replaced or removed meanwhile. Expiry
// skips keys with a load in flight, so only Set, Delete, Purge, or eviction drop it.
func (c *Cache[K, V]) run(key K, load func(K) (V, bool, error), cl *call[V], stale *entry[K, V]) {
	defer func() {
		c.mu.Lock()
		el, ok := c.items[key]
		switch {
		case cl.err != nil:
			if ok && el.Value.(*entry[K, V]) == stale {
				c.setLocked(key, stale.value, stale.found, c.TTL)
			}
		case cl.found:
			c.setLocked(key, cl.value, true, c.TTL)
		case c.CacheMisses:
			var zero V
			c.setLocked(key, zero, false, c.TTL)
		case ok:
			c.removeLocked(el)
		}
		delete(c.inflight, key)
		c.mu.Unlock()
		close(cl.done)
	}()
	value, found, err := load(key)
	cl.value, cl.found, cl.err = c.read(value, found), found, err
}

func (c *Cache[K, V]) setLocked(key K, value V, found bool, ttl time.Duration) {
	var fresh, dead time.Time
	if ttl > 0 {
		fresh = time.Now().Add(ttl)
		dead = fresh.Add(c.RevalidateAfter)
	}
	e := &entry[K, V]{
		key:   key,
		value: value,
		found: found,
		fresh: fresh,
		dead:  dead,
	}
	if el, ok := c.items[key]; ok {
		el.Value = e
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(e)
	if c.Capacity > 0 && c.order.Len() > c.Capacity {
		c.removeLocked(c.order.Back())
	}
}

func (c *Cache[K, V]) removeLocked(el *list.Element) {
	delete(c.items, c.order.Remove(el).(*entry[K, V]).key)
}

func (c *Cache[K, V]) live() []*entry[K, V] {
	c.init()
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	live := make([]*entry[K, V], 0, c.order.Len())
	for el := c.order.Front(); el != nil; el = el.Next() {
		if e := el.Value.(*entry[K, V]); e.found && !e.expired(now) {
			live = append(live, e)
		}
	}
	return live
}

// read copies a found value with Clone and returns the zero value for misses.
func (c *Cache[K, V]) read(value V, found bool) V {
	if !found {
		var zero V
		return zero
	}
	if c.Clone == nil {
		return value
	}
	return c.Clone(value)
}

func (c *Cache[K, V]) janitor() {
	defer close(c.stopped)
	ticker := time.NewTicker(c.JanitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-ticker.C:
			c.mu.Lock()
			for el := c.order.Back(); el != nil; {
				prev := el.Prev()
				e := el.Value.(*entry[K, V])
				if _, busy := c.inflight[e.key]; !busy && e.expired(now) {
					c.removeLocked(el)
				}
				el = prev
			}
			c.mu.Unlock()
		}
	}
}

func (e *entry[K, V]) expired(now time.Time) bool {
	return !e.dead.IsZero() && now.After(e.dead)
}

func (e *entry[K, V]) stale(now time.Time) bool {
	return !e.fresh.IsZero() && now.After(e.fresh)
}

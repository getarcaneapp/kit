package cgroup

import (
	"sync"
	"time"
)

// Cache memoizes the result of cgroup limit detection for a configurable TTL.
// A single in-flight refresh is shared across concurrent callers (singleflight
// semantics via the write lock), so a stampede of samplers never duplicates
// the work.
type Cache struct {
	mu        sync.RWMutex
	value     *Limits
	detected  bool
	timestamp time.Time
	ttl       time.Duration
	detector  func() (*Limits, error)
}

// NewCache returns a cache that caches detection results for ttl using the
// default DetectLimits detector.
func NewCache(ttl time.Duration) *Cache {
	return NewCacheWithDetector(ttl, DetectLimits)
}

// NewCacheWithDetector returns a cache backed by a custom detector. Useful when
// callers (e.g. tests) need to control detection behaviour or simulate failures.
func NewCacheWithDetector(ttl time.Duration, detector func() (*Limits, error)) *Cache {
	return &Cache{ttl: ttl, detector: detector}
}

// Get returns the cached cgroup limits, refreshing if the entry is older than the TTL.
// Returns nil when no cgroup limits have been detected (host is not running under cgroups).
func (c *Cache) Get() *Limits {
	c.mu.RLock()
	value, detected, fresh := c.value, c.detected, time.Since(c.timestamp) < c.ttl
	c.mu.RUnlock()
	if fresh {
		if detected {
			return value
		}
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.timestamp) < c.ttl {
		if c.detected {
			return c.value
		}
		return nil
	}

	limits, err := c.detector()
	c.timestamp = time.Now()
	if err != nil {
		c.value = nil
		c.detected = false
		return nil
	}
	c.value = limits
	c.detected = true
	return c.value
}

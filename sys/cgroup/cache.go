package cgroup

import (
	"time"

	"github.com/samber/hot"
)

// Cache memoizes the result of cgroup limit detection for a configurable TTL.
// A single in-flight refresh is shared across concurrent callers, so a
// stampede of samplers never duplicates the work.
type Cache struct {
	cache    *hot.HotCache[struct{}, *Limits]
	ttl      time.Duration
	detector func() (*Limits, error)
}

// NewCache returns a cache that caches detection results for ttl using the
// default DetectLimits detector.
func NewCache(ttl time.Duration) *Cache {
	return NewCacheWithDetector(ttl, DetectLimits)
}

// NewCacheWithDetector returns a cache backed by a custom detector. Useful when
// callers (e.g. tests) need to control detection behaviour or simulate failures.
func NewCacheWithDetector(ttl time.Duration, detector func() (*Limits, error)) *Cache {
	cache := &Cache{ttl: ttl, detector: detector}
	if ttl > 0 {
		cache.cache = hot.NewHotCache[struct{}, *Limits](hot.LRU, 1).
			WithTTL(ttl).
			Build()
	}
	return cache
}

// Get returns the cached cgroup limits, refreshing if the entry is older than the TTL.
// Returns nil when no cgroup limits have been detected (host is not running under cgroups).
func (c *Cache) Get() *Limits {
	if c.ttl <= 0 {
		limits, err := c.detector()
		if err != nil {
			return nil
		}
		return limits
	}

	limits, _, err := c.cache.GetWithLoaders(struct{}{}, func(_ []struct{}) (map[struct{}]*Limits, error) {
		detected, detectErr := c.detector()
		if detectErr != nil {
			return map[struct{}]*Limits{{}: nil}, nil
		}
		return map[struct{}]*Limits{{}: detected}, nil
	})
	if err != nil {
		return nil
	}
	return limits
}

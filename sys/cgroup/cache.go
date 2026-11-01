package cgroup

import (
	"time"

	"github.com/samber/hot"
)

// Cache memoizes the result of cgroup limit detection for a configurable TTL.
// A single in-flight refresh is shared across concurrent callers, so a
// stampede of samplers never duplicates the work. The zero Cache is usable and
// runs DetectLimits on every Get.
type Cache struct {
	cache    *hot.HotCache[struct{}, *Limits]
	detector func() (*Limits, error)
	ttl      time.Duration
}

// CacheConfig configures a Cache. The zero value detects on every call with DetectLimits.
type CacheConfig struct {
	// Detector replaces DetectLimits, e.g. in tests that simulate failures. Nil uses DetectLimits.
	Detector func() (*Limits, error)
	// TTL is how long a detection result is reused. Zero or negative disables caching.
	TTL time.Duration
}

// NewCache returns a cache configured by cfg.
func NewCache(cfg CacheConfig) *Cache {
	return &Cache{
		cache: func() *hot.HotCache[struct{}, *Limits] {
			if cfg.TTL <= 0 {
				return nil
			}
			return hot.NewHotCache[struct{}, *Limits](hot.LRU, 1).
				WithTTL(cfg.TTL).
				Build()
		}(),
		detector: cfg.Detector,
		ttl:      cfg.TTL,
	}
}

// Get returns the cached cgroup limits, refreshing if the entry is older than the TTL.
// Returns nil when no cgroup limits have been detected (host is not running under cgroups).
func (c *Cache) Get() *Limits {
	if c.ttl <= 0 {
		limits, err := c.detect()
		if err != nil {
			return nil
		}
		return limits
	}

	limits, _, err := c.cache.GetWithLoaders(struct{}{}, func(_ []struct{}) (map[struct{}]*Limits, error) {
		detected, detectErr := c.detect()
		if detectErr != nil {
			// Cache the miss: a host without detectable cgroup limits should
			// not re-run detection on every call.
			detected = nil
		}
		return map[struct{}]*Limits{{}: detected}, nil
	})
	if err != nil {
		return nil
	}
	return limits
}

// detect runs the configured detector, falling back to DetectLimits for the zero Cache.
func (c *Cache) detect() (*Limits, error) {
	if c.detector == nil {
		return DetectLimits()
	}
	return c.detector()
}

package cgroup

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCache_DeduplicatesRefreshAndCachesErrors(t *testing.T) {
	var calls atomic.Int32
	cache := NewCacheWithDetector(time.Minute, func() (*Limits, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &Limits{CPUCount: 2}, nil
	})

	const goroutines = 8
	var wg sync.WaitGroup
	results := make(chan *Limits, goroutines)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.Get()
		}()
	}
	wg.Wait()
	close(results)

	// Singleflight: concurrent callers share one detector run.
	require.Equal(t, int32(1), calls.Load())
	for limits := range results {
		require.NotNil(t, limits)
		require.Equal(t, 2, limits.CPUCount)
	}

	// A failing detector yields nil, and the failure is cached for the TTL.
	failing := NewCacheWithDetector(time.Minute, func() (*Limits, error) {
		calls.Add(1)
		return nil, errors.New("no cgroup")
	})
	calls.Store(0)
	require.Nil(t, failing.Get())
	require.Nil(t, failing.Get())
	require.Equal(t, int32(1), calls.Load())
}

package concurrency

import (
	"sync"
	"testing"
	"time"
)

// The whole point of the reference count: releasing a key must not remove an
// entry another goroutine is waiting on, or the two end up on different mutexes
// and both enter the critical section.
func TestKeyedMutexSerializesSameKey(t *testing.T) {
	var locks KeyedMutex

	inside := 0
	maxInside := 0
	var guard sync.Mutex

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			defer locks.Lock("image-1")()

			guard.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			guard.Unlock()

			guard.Lock()
			inside--
			guard.Unlock()
		})
	}
	wg.Wait()

	if maxInside != 1 {
		t.Fatalf("two goroutines held the same key at once: max inside %d", maxInside)
	}
	if len(locks.locks) != 0 {
		t.Fatalf("released keys must not accumulate: %d left", len(locks.locks))
	}
}

func TestKeyedMutexTryLockDoesNotBlockOnHeldKey(t *testing.T) {
	var locks KeyedMutex

	unlock := locks.Lock("a")
	if _, ok := locks.TryLock("a"); ok {
		t.Fatal("TryLock acquired a key that is already held")
	}

	// A different key is unaffected.
	otherUnlock, ok := locks.TryLock("b")
	if !ok {
		t.Fatal("TryLock failed on an unrelated key")
	}
	otherUnlock()

	unlock()
	retaken, ok := locks.TryLock("a")
	if !ok {
		t.Fatal("key must be acquirable once released")
	}
	retaken()
	if len(locks.locks) != 0 {
		t.Fatalf("released keys must not accumulate: %d left", len(locks.locks))
	}
}

func TestKeyedMutexReadLocksShareKeyAndBlockWriter(t *testing.T) {
	var locks KeyedMutex

	firstReadUnlock := locks.RLock("volume")
	secondReadUnlock := locks.RLock("volume")
	writerAcquired := make(chan struct{})
	writerReleased := make(chan struct{})
	go func() {
		defer close(writerReleased)
		defer locks.Lock("volume")()
		close(writerAcquired)
	}()

	select {
	case <-writerAcquired:
		t.Fatal("writer acquired a key while readers held it")
	case <-time.After(50 * time.Millisecond):
	}

	firstReadUnlock()
	select {
	case <-writerAcquired:
		t.Fatal("writer acquired a key before every reader released it")
	case <-time.After(50 * time.Millisecond):
	}

	secondReadUnlock()
	select {
	case <-writerAcquired:
	case <-time.After(time.Second):
		t.Fatal("writer never acquired the released key")
	}
	<-writerReleased
	if len(locks.locks) != 0 {
		t.Fatalf("released keys must not accumulate: %d left", len(locks.locks))
	}
}

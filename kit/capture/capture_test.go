package capture

import (
	"sync"
	"testing"
)

func TestCaptureStoresUpToLimit(t *testing.T) {
	t.Parallel()

	capture := New(8)

	n, err := capture.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if got := capture.String(); got != "hello" {
		t.Fatalf("String = %q, want %q", got, "hello")
	}
	if capture.Truncated() {
		t.Fatal("Truncated = true before the limit was reached")
	}

	n, err = capture.Write([]byte("world"))
	if n != 5 || err != nil {
		t.Fatalf("Write past limit = (%d, %v), want (5, nil)", n, err)
	}
	if got := capture.String(); got != "hellowor" {
		t.Fatalf("String = %q, want %q", got, "hellowor")
	}
	if !capture.Truncated() {
		t.Fatal("Truncated = false after output was dropped")
	}
}

func TestCaptureNonPositiveLimitCapturesNothing(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1} {
		capture := New(limit)
		if n, err := capture.Write(nil); n != 0 || err != nil || capture.Truncated() {
			t.Fatalf("limit %d: empty Write = (%d, %v), truncated %t; want (0, nil), false", limit, n, err, capture.Truncated())
		}
		if n, err := capture.Write([]byte("dropped")); n != 7 || err != nil {
			t.Fatalf("limit %d: Write = (%d, %v), want (7, nil)", limit, n, err)
		}
		if got := capture.String(); got != "" {
			t.Fatalf("limit %d: String = %q, want empty", limit, got)
		}
		if !capture.Truncated() {
			t.Fatalf("limit %d: Truncated = false after dropping output", limit)
		}
	}
}

func TestCaptureConcurrentWrites(t *testing.T) {
	t.Parallel()

	capture := New(64)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, _ = capture.Write([]byte("chunk"))
		})
	}
	wg.Wait()

	if got := len(capture.String()); got != 64 {
		t.Fatalf("len(String) = %d, want 64", got)
	}
	if !capture.Truncated() {
		t.Fatal("Truncated = false after writes exceeding the limit")
	}
}

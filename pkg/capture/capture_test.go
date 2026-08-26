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

	capture := New(0)
	if n, err := capture.Write([]byte("dropped")); n != 7 || err != nil {
		t.Fatalf("Write = (%d, %v), want (7, nil)", n, err)
	}
	if got := capture.String(); got != "" {
		t.Fatalf("String = %q, want empty", got)
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

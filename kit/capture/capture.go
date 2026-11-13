// Package capture provides a bounded, concurrency-safe capture buffer for
// streamed output such as build or process logs.
package capture

import (
	"bytes"
	"sync"
)

type (
	// Capture stores written output up to a maximum byte size.
	//
	// It implements io.Writer so it can be used with io.MultiWriter. Writers may
	// run on multiple goroutines (e.g. a progress display alongside a message
	// stream), so access is guarded. Writes never fail: once the limit is reached
	// the remainder is dropped and the capture is marked truncated.
	Capture struct {
		buf       bytes.Buffer
		mu        sync.Mutex
		maxBytes  int
		truncated bool
	}
)

// New returns a Capture that stores at most maxBytes of written output.
// A non-positive maxBytes captures nothing.
func New(maxBytes int) *Capture {
	return &Capture{maxBytes: maxBytes}
}

func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	remaining := max(c.maxBytes-c.buf.Len(), 0)
	_, _ = c.buf.Write(p[:min(len(p), remaining)])
	c.truncated = c.truncated || len(p) > remaining

	return len(p), nil
}

// String returns the captured output.
func (c *Capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Truncated reports whether any written output was dropped.
func (c *Capture) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.truncated
}

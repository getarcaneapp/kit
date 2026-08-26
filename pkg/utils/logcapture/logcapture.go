// Package logcapture provides a bounded, concurrency-safe capture buffer for
// streamed output such as build or process logs.
package logcapture

import (
	"bytes"
	"sync"
)

// Capture stores written output up to a maximum byte size.
//
// It implements io.Writer so it can be used with io.MultiWriter. Writers may
// run on multiple goroutines (e.g. a progress display alongside a message
// stream), so access is guarded. Writes never fail: once the limit is reached
// the remainder is dropped and the capture is marked truncated.
type Capture struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

// New returns a Capture that stores at most maxBytes of written output.
// A non-positive maxBytes captures nothing.
func New(maxBytes int) *Capture {
	return &Capture{maxBytes: maxBytes}
}

func (c *Capture) Write(p []byte) (int, error) {
	if c.maxBytes <= 0 {
		return len(p), nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	remaining := c.maxBytes - c.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = c.buf.Write(p)
		} else {
			_, _ = c.buf.Write(p[:remaining])
			c.truncated = true
		}
	} else {
		c.truncated = true
	}

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

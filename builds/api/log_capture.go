package api

import (
	"bytes"
	"sync"

	"go.getarcane.app/builds/types"
)

// logCapture stores build output up to a max byte size.
// It implements io.Writer so it can be used with io.MultiWriter. Build output
// renderers may write from multiple goroutines (e.g. the BuildKit progress
// display alongside the jsonmessage stream), so access is guarded.
type logCapture struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

func NewLogCapture(maxBytes int) types.LogCapture {
	return &logCapture{maxBytes: maxBytes}
}

func (l *logCapture) Write(p []byte) (int, error) {
	if l.maxBytes <= 0 {
		return len(p), nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	remaining := l.maxBytes - l.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = l.buf.Write(p)
		} else {
			_, _ = l.buf.Write(p[:remaining])
			l.truncated = true
		}
	} else {
		l.truncated = true
	}

	return len(p), nil
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *logCapture) Truncated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}

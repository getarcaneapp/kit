package agg

import (
	"context"
	"sync"
)

// Hub deduplicates poll loops across stream subscribers: the first subscriber
// for a key starts the runner, later subscribers attach to it, and the runner
// stops when the last subscriber detaches. Events fan out to each subscriber
// through a latest-wins mailbox, so events must be states (snapshots, errors)
// rather than deltas — a slow consumer skips straight to the newest one and
// can never stall the runner or its peers.
type Hub[T any] struct {
	mu      sync.Mutex
	entries map[string]*hubEntry[T]
}

type hubEntry[T any] struct {
	cancel      context.CancelFunc
	subscribers map[*hubSubscriber[T]]struct{}
	last        T
	hasLast     bool
}

type hubSubscriber[T any] struct {
	mu     sync.Mutex
	latest T
	has    bool
	wake   chan struct{}
}

// NewHub returns an empty Hub.
func NewHub[T any]() *Hub[T] {
	return &Hub[T]{entries: map[string]*hubEntry[T]{}}
}

// Subscribe attaches to the shared runner for key, starting run when no
// runner exists yet, and blocks until ctx is done or deliver returns false.
// run receives a context tied to the runner's lifetime — detached from any
// single subscriber — and a publish callback for its events. publish never
// blocks. A subscriber attaching after events were published immediately
// receives the most recent one.
func (h *Hub[T]) Subscribe(ctx context.Context, key string, run func(ctx context.Context, publish func(T)), deliver func(T) bool) {
	sub := &hubSubscriber[T]{wake: make(chan struct{}, 1)}

	h.mu.Lock()
	entry, ok := h.entries[key]
	if !ok {
		runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		entry = &hubEntry[T]{
			cancel:      cancel,
			subscribers: map[*hubSubscriber[T]]struct{}{},
		}
		h.entries[key] = entry
		go run(runCtx, func(event T) { h.publish(entry, event) })
	}
	entry.subscribers[sub] = struct{}{}
	if entry.hasLast {
		sub.set(entry.last)
	}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(entry.subscribers, sub)
		if len(entry.subscribers) == 0 {
			entry.cancel()
			delete(h.entries, key)
		}
		h.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.wake:
			event, ok := sub.take()
			if ok && !deliver(event) {
				return
			}
		}
	}
}

func (h *Hub[T]) publish(entry *hubEntry[T], event T) {
	h.mu.Lock()
	entry.last = event
	entry.hasLast = true
	subscribers := make([]*hubSubscriber[T], 0, len(entry.subscribers))
	for sub := range entry.subscribers {
		subscribers = append(subscribers, sub)
	}
	h.mu.Unlock()

	for _, sub := range subscribers {
		sub.set(event)
	}
}

func (sub *hubSubscriber[T]) set(event T) {
	sub.mu.Lock()
	sub.latest = event
	sub.has = true
	sub.mu.Unlock()

	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (sub *hubSubscriber[T]) take() (T, bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if !sub.has {
		var zero T
		return zero, false
	}
	event := sub.latest
	sub.has = false
	return event, true
}

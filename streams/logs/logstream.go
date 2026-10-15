// Package logs captures slog output into a bounded in-memory ring buffer and
// fans it out to live subscribers.
package logs

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultRingCapacity     = 1000
	defaultSubscriberBuffer = 256
)

// Entry is a single captured log record.
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Option configures a Broadcaster.
type Option func(*Broadcaster)

// WithSubscriberBuffer sets the per-subscriber channel buffer.
func WithSubscriberBuffer(size int) Option {
	return func(b *Broadcaster) {
		if size >= 0 {
			b.subscriberBuffer = size
		}
	}
}

// Broadcaster keeps a bounded ring buffer of recent log entries and fans new
// entries out to active subscribers.
type Broadcaster struct {
	historyMu        sync.Mutex
	deliveryMu       sync.RWMutex
	buf              []Entry
	start            int
	size             int
	capN             int
	subscriberBuffer int
	subscribers      atomic.Pointer[logSubscriberSnapshot]
}

type logSubscriberSnapshot struct {
	subscribers []chan Entry
}

// New returns a Broadcaster retaining up to capacity recent entries.
func New(capacity int, opts ...Option) *Broadcaster {
	if capacity <= 0 {
		capacity = defaultRingCapacity
	}
	b := &Broadcaster{
		buf:              make([]Entry, capacity),
		capN:             capacity,
		subscriberBuffer: defaultSubscriberBuffer,
	}
	b.subscribers.Store(&logSubscriberSnapshot{})
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Append records an entry in the ring buffer and delivers it to subscribers.
func (b *Broadcaster) Append(e Entry) {
	b.historyMu.Lock()

	if b.size < b.capN {
		b.buf[(b.start+b.size)%b.capN] = e
		b.size++
	} else {
		b.buf[b.start] = e
		b.start = (b.start + 1) % b.capN
	}
	b.historyMu.Unlock()

	snapshot := b.subscribers.Load()
	if snapshot == nil || len(snapshot.subscribers) == 0 {
		return
	}

	b.deliveryMu.RLock()
	snapshot = b.subscribers.Load()
	for _, subscription := range snapshot.subscribers {
		select {
		case subscription <- e:
		default:
		}
	}
	b.deliveryMu.RUnlock()
}

// Recent returns buffered entries in chronological order.
func (b *Broadcaster) Recent() []Entry {
	b.historyMu.Lock()
	defer b.historyMu.Unlock()

	out := make([]Entry, b.size)
	for i := range b.size {
		out[i] = b.buf[(b.start+i)%b.capN]
	}
	return out
}

// Subscribe registers a live subscriber.
func (b *Broadcaster) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, b.subscriberBuffer)
	for {
		current := b.loadSubscribers()
		next := &logSubscriberSnapshot{
			subscribers: append(append([]chan Entry(nil), current.subscribers...), ch),
		}
		if b.subscribers.CompareAndSwap(current, next) {
			break
		}
	}

	return ch, func() {
		for {
			current := b.loadSubscribers()
			index := slices.Index(current.subscribers, ch)
			if index < 0 {
				return
			}

			next := &logSubscriberSnapshot{
				subscribers: append(
					append(make([]chan Entry, 0, len(current.subscribers)-1), current.subscribers[:index]...),
					current.subscribers[index+1:]...,
				),
			}
			if b.subscribers.CompareAndSwap(current, next) {
				b.deliveryMu.Lock()
				close(ch)
				b.deliveryMu.Unlock()
				return
			}
		}
	}
}

func (b *Broadcaster) loadSubscribers() *logSubscriberSnapshot {
	for {
		current := b.subscribers.Load()
		if current != nil {
			return current
		}

		initial := &logSubscriberSnapshot{}
		if b.subscribers.CompareAndSwap(nil, initial) {
			return initial
		}
	}
}

// slogHandler wraps a base slog.Handler and appends handled records to a
// Broadcaster.
type slogHandler struct {
	base  slog.Handler
	b     *Broadcaster
	attrs []boundAttrs
	group []string
}

type boundAttrs struct {
	groups []string
	attrs  []slog.Attr
}

// NewSlogHandler returns a slog.Handler that tees records to base and to b.
func NewSlogHandler(base slog.Handler, b *Broadcaster) slog.Handler {
	return &slogHandler{base: base, b: b}
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	for _, bound := range h.attrs {
		addAttrsToMap(attrs, bound.groups, bound.attrs)
	}
	recordAttrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		recordAttrs = append(recordAttrs, a)
		return true
	})
	addAttrsToMap(attrs, h.group, recordAttrs)
	if len(attrs) == 0 {
		attrs = nil
	}

	h.b.Append(Entry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   attrs,
	})
	return h.base.Handle(ctx, r)
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{
		base: h.base.WithAttrs(attrs),
		b:    h.b,
		attrs: func() []boundAttrs {
			if len(h.attrs) == 0 {
				return append([]boundAttrs(nil), boundAttrs{
					groups: slices.Clone(h.group),
					attrs:  slices.Clone(attrs),
				})
			}
			copied := make([]boundAttrs, 0, len(h.attrs))
			for _, bound := range h.attrs {
				copied = append(copied, boundAttrs{
					groups: slices.Clone(bound.groups),
					attrs:  slices.Clone(bound.attrs),
				})
			}
			return append(copied, boundAttrs{
				groups: slices.Clone(h.group),
				attrs:  slices.Clone(attrs),
			})
		}(),
		group: slices.Clone(h.group),
	}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	return &slogHandler{
		base: h.base.WithGroup(name),
		b:    h.b,
		attrs: func() []boundAttrs {
			if len(h.attrs) == 0 {
				return nil
			}
			copied := make([]boundAttrs, 0, len(h.attrs))
			for _, bound := range h.attrs {
				copied = append(copied, boundAttrs{
					groups: slices.Clone(bound.groups),
					attrs:  slices.Clone(bound.attrs),
				})
			}
			return copied
		}(),
		group: func() []string {
			if name == "" {
				return slices.Clone(h.group)
			}
			return append(slices.Clone(h.group), name)
		}(),
	}
}

func addAttrsToMap(dst map[string]any, groups []string, attrs []slog.Attr) {
	target := dst
	for _, group := range groups {
		existing, _ := target[group].(map[string]any)
		if existing == nil {
			existing = make(map[string]any)
			target[group] = existing
		}
		target = existing
	}
	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}
		target[attr.Key] = normalizeAttrValue(attr.Value)
	}
}

func normalizeAttrValue(value slog.Value) any {
	value = value.Resolve()

	switch value.Kind() {
	case slog.KindAny:
		return normalizeAnyValue(value.Any())
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindGroup:
		return normalizeGroupAttrs(value.Group())
	case slog.KindInt64:
		return value.Int64()
	case slog.KindLogValuer:
		return value.String()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time()
	case slog.KindUint64:
		return value.Uint64()
	default:
		return value.String()
	}
}

func normalizeAnyValue(value any) any {
	switch typed := value.(type) {
	case error:
		return typed.Error()
	case slog.Attr:
		return map[string]any{typed.Key: normalizeAttrValue(typed.Value)}
	case []slog.Attr:
		return normalizeGroupAttrs(typed)
	case slog.Value:
		return normalizeAttrValue(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeAnyValue(item)
		}
		return out
	}

	rv := reflect.ValueOf(value)
	if rv.IsValid() && rv.Kind() == reflect.Slice {
		out := make([]any, rv.Len())
		for i := range rv.Len() {
			out[i] = normalizeAnyValue(rv.Index(i).Interface())
		}
		return out
	}

	return value
}

func normalizeGroupAttrs(attrs []slog.Attr) map[string]any {
	out := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}
		out[attr.Key] = normalizeAttrValue(attr.Value)
	}
	return out
}

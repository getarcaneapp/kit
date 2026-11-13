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

	"kit.getarcane.app"

	"kit.getarcane.app/docker/streams/internal/buffer"
)

type (
	// Entry is a single captured log record.
	Entry struct {
		Time    time.Time      `json:"time"`
		Attrs   map[string]any `json:"attrs,omitempty"`
		Level   string         `json:"level"`
		Message string         `json:"message"`
	}

	// Config configures a Broadcaster. The zero value uses the defaults.
	Config struct {
		// SubscriberBuffer is the per-subscriber channel buffer. Nil or negative
		// uses the default of 256; new(0) makes subscriber channels unbuffered.
		SubscriberBuffer *int
		// Capacity is the number of recent entries retained. Zero or negative uses
		// the default of 1000.
		Capacity int
	}

	// Broadcaster keeps a bounded ring buffer of recent log entries and fans new
	// entries out to active subscribers.
	Broadcaster struct {
		subscribers      atomic.Pointer[logSubscriberSnapshot]
		buf              []Entry
		start            int
		size             int
		capN             int
		subscriberBuffer int
		deliveryMu       sync.RWMutex
		historyMu        sync.Mutex
	}

	logSubscriberSnapshot struct {
		subscribers []chan Entry
	}

	// slogHandler wraps a base slog.Handler and appends handled records to a
	// Broadcaster.
	slogHandler struct {
		base  slog.Handler
		b     *Broadcaster
		attrs []boundAttrs
		group []string
	}

	boundAttrs struct {
		groups []string
		attrs  []slog.Attr
	}
)

const (
	defaultRingCapacity     = 1000
	defaultSubscriberBuffer = 256
)

// New returns a Broadcaster configured by cfg.
func New(cfg Config) *Broadcaster {
	capacity := kit.Ternary(cfg.Capacity > 0, cfg.Capacity, defaultRingCapacity)
	b := &Broadcaster{
		buf:              make([]Entry, capacity),
		capN:             capacity,
		subscriberBuffer: buffer.Size(cfg.SubscriberBuffer, defaultSubscriberBuffer),
	}
	b.subscribers.Store(&logSubscriberSnapshot{})
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

// NewSlogHandler returns a slog.Handler that tees records to base and to b.
func NewSlogHandler(base slog.Handler, b *Broadcaster) slog.Handler {
	return &slogHandler{base: base, b: b}
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.b.Append(Entry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs: func() map[string]any {
			if len(h.attrs) == 0 && r.NumAttrs() == 0 {
				return nil
			}
			attrs := make(map[string]any)
			for _, bound := range h.attrs {
				addAttrsToMap(attrs, bound.groups, bound.attrs)
			}
			target := groupMap(attrs, h.group)
			r.Attrs(func(a slog.Attr) bool {
				setAttr(target, a)
				return true
			})
			return kit.Ternary(len(attrs) == 0, nil, attrs)
		}(),
	})
	return h.base.Handle(ctx, r)
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{
		base: h.base.WithAttrs(attrs),
		b:    h.b,
		attrs: func() []boundAttrs {
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

// groupMap returns the map nested under groups in dst, creating it as needed.
func groupMap(dst map[string]any, groups []string) map[string]any {
	target := dst
	for _, group := range groups {
		existing, _ := target[group].(map[string]any)
		if existing == nil {
			existing = make(map[string]any)
			target[group] = existing
		}
		target = existing
	}
	return target
}

func addAttrsToMap(dst map[string]any, groups []string, attrs []slog.Attr) {
	target := groupMap(dst, groups)
	for _, attr := range attrs {
		setAttr(target, attr)
	}
}

// setAttr stores attr in dst, inlining the attrs of an empty-key group as slog
// requires and dropping other empty-key attrs.
func setAttr(dst map[string]any, attr slog.Attr) {
	if attr.Key != "" {
		dst[attr.Key] = normalizeAttrValue(attr.Value)
		return
	}
	if value := attr.Value.Resolve(); value.Kind() == slog.KindGroup {
		for _, member := range value.Group() {
			setAttr(dst, member)
		}
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
		setAttr(out, attr)
	}
	return out
}

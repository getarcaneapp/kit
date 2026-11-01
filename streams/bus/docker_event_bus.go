// Package bus provides typed in-process pub/sub fan-out primitives.
package bus

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/moby/moby/api/types/events"

	"go.getarcane.app/streams/internal/buffer"
)

const defaultSubscriberBuffer = 16

// Config configures a DockerEventBus. The zero value is valid.
type Config struct {
	// OnDrop observes messages that could not be delivered to a full
	// subscriber channel. Nil ignores drops.
	OnDrop func(events.Message)
}

// SubscribeOptions configures one subscription. The zero value is valid.
type SubscribeOptions struct {
	// Buffer is the subscription channel buffer. Nil or negative uses the
	// default of 16; new(0) makes the channel unbuffered.
	Buffer *int
}

// DockerEventBus is an in-process fan-out point for Docker daemon events.
type DockerEventBus struct {
	state      atomic.Pointer[dockerEventBusState]
	onDrop     func(events.Message)
	deliveryMu sync.RWMutex
}

type dockerEventBusState struct {
	subscribers map[events.Type][]chan events.Message
	closed      bool
}

// NewDockerEventBus creates an empty Docker event bus configured by cfg.
func NewDockerEventBus(cfg Config) *DockerEventBus {
	b := &DockerEventBus{onDrop: cfg.OnDrop}
	b.state.Store(&dockerEventBusState{
		subscribers: make(map[events.Type][]chan events.Message),
	})
	return b
}

// Subscribe registers for messages of eventType. Delivery is non-blocking.
func (b *DockerEventBus) Subscribe(eventType events.Type, opts SubscribeOptions) (<-chan events.Message, func()) {
	ch := make(chan events.Message, buffer.Size(opts.Buffer, defaultSubscriberBuffer))
	if b == nil {
		close(ch)
		return ch, func() {}
	}

	for {
		current := b.loadState()
		if current.closed {
			close(ch)
			return ch, func() {}
		}

		subscribers := maps.Clone(current.subscribers)
		subscribers[eventType] = append(append([]chan events.Message(nil), subscribers[eventType]...), ch)
		if b.state.CompareAndSwap(current, &dockerEventBusState{subscribers: subscribers}) {
			break
		}
	}

	return ch, func() {
		for {
			current := b.loadState()
			subscriptions := current.subscribers[eventType]
			index := slices.Index(subscriptions, ch)
			if index < 0 {
				return
			}

			subscribers := maps.Clone(current.subscribers)
			if len(subscriptions) == 1 {
				delete(subscribers, eventType)
			} else {
				next := make([]chan events.Message, 0, len(subscriptions)-1)
				next = append(next, subscriptions[:index]...)
				next = append(next, subscriptions[index+1:]...)
				subscribers[eventType] = next
			}

			if b.state.CompareAndSwap(current, &dockerEventBusState{closed: current.closed, subscribers: subscribers}) {
				b.deliveryMu.Lock()
				close(ch)
				b.deliveryMu.Unlock()
				return
			}
		}
	}
}

// Publish fans out msg to subscribers of msg.Type.
func (b *DockerEventBus) Publish(msg events.Message) {
	if b == nil {
		return
	}

	state := b.state.Load()
	if state == nil || state.closed || len(state.subscribers[msg.Type]) == 0 {
		return
	}

	b.deliveryMu.RLock()
	state = b.state.Load()
	if state == nil || state.closed {
		b.deliveryMu.RUnlock()
		return
	}
	dropped := 0
	for _, subscriber := range state.subscribers[msg.Type] {
		select {
		case subscriber <- msg:
		default:
			dropped++
		}
	}
	onDrop := b.onDrop
	b.deliveryMu.RUnlock()

	if onDrop != nil {
		for range dropped {
			onDrop(msg)
		}
	}
}

// Close closes every subscription. It is safe to call more than once.
func (b *DockerEventBus) Close() {
	if b == nil {
		return
	}

	for {
		current := b.loadState()
		if current.closed {
			return
		}

		if !b.state.CompareAndSwap(current, &dockerEventBusState{closed: true}) {
			continue
		}

		b.deliveryMu.Lock()
		for _, subscribers := range current.subscribers {
			for _, subscriber := range subscribers {
				close(subscriber)
			}
		}
		b.deliveryMu.Unlock()
		return
	}
}

func (b *DockerEventBus) loadState() *dockerEventBusState {
	for {
		state := b.state.Load()
		if state != nil {
			return state
		}

		initial := &dockerEventBusState{
			subscribers: make(map[events.Type][]chan events.Message),
		}
		if b.state.CompareAndSwap(nil, initial) {
			return initial
		}
	}
}

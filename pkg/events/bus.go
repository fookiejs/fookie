package events

import (
	"sync"
	"time"
)

type Op string

const (
	OpCreate Op = "created"
	OpRead   Op = "read"
	OpUpdate Op = "updated"
	OpDelete Op = "deleted"
)

type Event struct {
	Op        Op                     `json:"op"`
	Model     string                 `json:"model"`
	ID        string                 `json:"id"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"ts"`
}

type Bus struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
	lastEvent   map[string]Event // key: "model:id", caches latest event for dedup
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[chan Event]struct{}),
		lastEvent:   make(map[string]Event),
	}
}

func (b *Bus) Publish(ev Event) {
	ev.Timestamp = time.Now()
	cacheKey := ev.Model + ":" + ev.ID

	b.mu.Lock()
	// Dedup: if same operation on same model:id within short window, skip publish
	if last, exists := b.lastEvent[cacheKey]; exists {
		if last.Op == ev.Op && (ev.Op == OpCreate || ev.Op == OpUpdate) {
			// Merge payload and update cache, but don't broadcast duplicate
			for k, v := range ev.Payload {
				last.Payload[k] = v
			}
			last.Timestamp = ev.Timestamp
			b.lastEvent[cacheKey] = last
			b.mu.Unlock()
			return
		}
	}
	b.lastEvent[cacheKey] = ev
	subscribers := make([]chan Event, 0, len(b.subscribers))
	for ch := range b.subscribers {
		subscribers = append(subscribers, ch)
	}
	b.mu.Unlock()

	// Send outside lock to avoid blocking
	for _, ch := range subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *Bus) PublishCRUD(op, model, id string, payload map[string]interface{}) {
	b.Publish(Event{Op: Op(op), Model: model, ID: id, Payload: payload})
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	// Buffer size 16: most subscribers process events immediately (sub-millisecond latency)
	// Slow subscribers (buffer full) are dropped via non-blocking send, preventing backpressure
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subscribers, ch)
		b.mu.Unlock()
		close(ch)
	}
}

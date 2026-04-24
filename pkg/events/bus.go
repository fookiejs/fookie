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
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[chan Event]struct{}),
	}
}

func (b *Bus) Publish(ev Event) {
	ev.Timestamp = time.Now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
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
	ch := make(chan Event, 64)
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

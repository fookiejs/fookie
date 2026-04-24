package events

import (
	"sync"
)

type RoomBus struct {
	mu    sync.RWMutex
	rooms map[string][]chan interface{}
}

func NewRoomBus() *RoomBus {
	return &RoomBus{rooms: make(map[string][]chan interface{})}
}

func (rb *RoomBus) Subscribe(roomID string) (ch chan interface{}, cancel func()) {
	ch = make(chan interface{}, 32)
	rb.mu.Lock()
	rb.rooms[roomID] = append(rb.rooms[roomID], ch)
	rb.mu.Unlock()
	cancel = func() {
		rb.mu.Lock()
		defer rb.mu.Unlock()
		subs := rb.rooms[roomID]
		out := subs[:0]
		for _, c := range subs {
			if c != ch {
				out = append(out, c)
			}
		}
		if len(out) == 0 {
			delete(rb.rooms, roomID)
		} else {
			rb.rooms[roomID] = out
		}
		close(ch)
	}
	return ch, cancel
}

func (rb *RoomBus) Publish(roomID string, msg map[string]interface{}) {
	rb.mu.RLock()
	subs := append([]chan interface{}(nil), rb.rooms[roomID]...)
	rb.mu.RUnlock()
	for _, c := range subs {
		select {
		case c <- msg:
		default:
		}
	}
}

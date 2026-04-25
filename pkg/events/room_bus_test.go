package events

import (
	"testing"
	"time"
)

func TestRoomBusSubscriberMetrics(t *testing.T) {
	rb := NewRoomBus()

	if rb.RoomCount() != 0 {
		t.Errorf("expected 0 rooms initially, got %d", rb.RoomCount())
	}

	// Subscribe to room-1
	_, unsub1 := rb.Subscribe("room-1")
	if rb.RoomCount() != 1 {
		t.Errorf("expected 1 room after first subscribe, got %d", rb.RoomCount())
	}
	if rb.SubscriberCount("room-1") != 1 {
		t.Errorf("expected 1 subscriber in room-1, got %d", rb.SubscriberCount("room-1"))
	}

	// Second subscriber to same room
	_, unsub2 := rb.Subscribe("room-1")
	if rb.RoomCount() != 1 {
		t.Errorf("expected 1 room (same), got %d", rb.RoomCount())
	}
	if rb.SubscriberCount("room-1") != 2 {
		t.Errorf("expected 2 subscribers in room-1, got %d", rb.SubscriberCount("room-1"))
	}

	// Subscribe to different room
	_, unsub3 := rb.Subscribe("room-2")
	if rb.RoomCount() != 2 {
		t.Errorf("expected 2 rooms, got %d", rb.RoomCount())
	}
	if rb.SubscriberCount("room-2") != 1 {
		t.Errorf("expected 1 subscriber in room-2, got %d", rb.SubscriberCount("room-2"))
	}

	// Unsubscribe one from room-1
	unsub1()
	if rb.RoomCount() != 2 {
		t.Errorf("expected 2 rooms (room-1 still has ch2), got %d", rb.RoomCount())
	}
	if rb.SubscriberCount("room-1") != 1 {
		t.Errorf("expected 1 subscriber in room-1 after unsub, got %d", rb.SubscriberCount("room-1"))
	}

	// Unsubscribe last from room-1
	unsub2()
	if rb.RoomCount() != 1 {
		t.Errorf("expected 1 room (room-1 should be cleaned up), got %d", rb.RoomCount())
	}
	if rb.SubscriberCount("room-1") != 0 {
		t.Errorf("expected 0 subscribers in room-1 (cleaned), got %d", rb.SubscriberCount("room-1"))
	}

	// Cleanup
	unsub3()
	if rb.RoomCount() != 0 {
		t.Errorf("expected 0 rooms after all unsub, got %d", rb.RoomCount())
	}
}

func TestRoomBusPublishLocal(t *testing.T) {
	rb := NewRoomBus()
	ch, unsub := rb.Subscribe("room-1")
	defer unsub()

	msg := map[string]interface{}{"text": "hello"}
	rb.Publish("room-1", msg)

	select {
	case received := <-ch:
		if m, ok := received.(map[string]interface{}); !ok || m["text"] != "hello" {
			t.Errorf("expected hello message, got %v", received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no message received")
	}
}

func TestRoomBusUnsubscribeCleansUp(t *testing.T) {
	rb := NewRoomBus()

	// Create and immediately unsubscribe
	_, unsub := rb.Subscribe("room-temp")
	unsub()

	// Room should be gone
	if rb.RoomCount() != 0 {
		t.Errorf("expected room to be cleaned up, got %d rooms", rb.RoomCount())
	}
}

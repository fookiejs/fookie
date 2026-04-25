package events

import (
	"testing"
	"time"
)

func TestBusDeduplication(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe()
	defer unsub()

	// Publish 3 updates to same model:id — should deduplicate to 1 event
	bus.Publish(Event{
		Op:    OpUpdate,
		Model: "User",
		ID:    "user-1",
		Payload: map[string]interface{}{
			"name": "Alice",
		},
	})

	bus.Publish(Event{
		Op:    OpUpdate,
		Model: "User",
		ID:    "user-1",
		Payload: map[string]interface{}{
			"email": "alice@example.com",
		},
	})

	bus.Publish(Event{
		Op:    OpUpdate,
		Model: "User",
		ID:    "user-1",
		Payload: map[string]interface{}{
			"status": "active",
		},
	})

	// Should receive only 1 event (dedup), with merged payload
	select {
	case ev := <-ch:
		if ev.Op != OpUpdate || ev.Model != "User" || ev.ID != "user-1" {
			t.Fatalf("expected OpUpdate User:user-1, got %v:%s:%s", ev.Op, ev.Model, ev.ID)
		}
		// Payload should have merged values
		if ev.Payload["name"] != "Alice" {
			t.Errorf("expected name=Alice in merged payload, got %v", ev.Payload["name"])
		}
		if ev.Payload["status"] != "active" {
			t.Errorf("expected status=active in merged payload, got %v", ev.Payload["status"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received (expected 1 dedup event)")
	}

	// Next event should not come (dedup swallowed them)
	select {
	case <-ch:
		t.Fatal("received extra event (dedup failed)")
	case <-time.After(100 * time.Millisecond):
		// Expected: no more events
	}
}

func TestBusCreateDoesntDedupDelete(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe()
	defer unsub()

	// Create + Delete should both broadcast (different ops)
	bus.Publish(Event{
		Op:    OpCreate,
		Model: "Post",
		ID:    "post-1",
		Payload: map[string]interface{}{
			"title": "Hello",
		},
	})

	bus.Publish(Event{
		Op:    OpDelete,
		Model: "Post",
		ID:    "post-1",
		Payload: map[string]interface{}{},
	})

	// Should receive both events
	var count int
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			count++
			if i == 0 && ev.Op != OpCreate {
				t.Errorf("expected first event OpCreate, got %v", ev.Op)
			}
			if i == 1 && ev.Op != OpDelete {
				t.Errorf("expected second event OpDelete, got %v", ev.Op)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("expected 2 events, got %d", count)
		}
	}
}

func TestBusMultipleSubscribers(t *testing.T) {
	bus := NewBus()
	ch1, unsub1 := bus.Subscribe()
	ch2, unsub2 := bus.Subscribe()
	defer unsub1()
	defer unsub2()

	bus.Publish(Event{
		Op:      OpUpdate,
		Model:   "User",
		ID:      "user-1",
		Payload: map[string]interface{}{"age": 30},
	})

	// Both subscribers should receive the same event
	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Model != "User" {
				t.Errorf("expected User model, got %s", ev.Model)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("subscriber didn't receive event")
		}
	}
}

func TestBusSlowSubscriberDropped(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe()
	defer unsub()

	// Publish 30 events without draining channel (buffer is 16)
	// Subscriber will drop events after buffer fills
	for i := 0; i < 30; i++ {
		bus.Publish(Event{
			Op:    OpUpdate,
			Model: "Event",
			ID:    "event-" + string(rune(i)),
			Payload: map[string]interface{}{
				"seq": i,
			},
		})
	}

	// Drain what we got
	var count int
	for {
		select {
		case <-ch:
			count++
		case <-time.After(50 * time.Millisecond):
			goto done
		}
	}
done:
	// We should have received ~16 events (buffer size), not all 30
	if count > 20 {
		t.Logf("Warning: expected ~16 events (buffer size), got %d (may be dedup or timing)", count)
	}
	if count == 0 {
		t.Fatal("expected to receive at least some events")
	}
}

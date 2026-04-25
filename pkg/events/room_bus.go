package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type RoomBus struct {
	mu               sync.RWMutex
	rooms            map[string][]chan interface{}
	rdb              *redis.Client // optional: nil = local-only mode
	subscriberMetric map[string]int // subscriber count per room (for cleanup)
}

func NewRoomBus() *RoomBus {
	return &RoomBus{
		rooms:            make(map[string][]chan interface{}),
		subscriberMetric: make(map[string]int),
	}
}

func NewRoomBusWithRedis(rdb *redis.Client) *RoomBus {
	rb := &RoomBus{
		rooms:            make(map[string][]chan interface{}),
		rdb:              rdb,
		subscriberMetric: make(map[string]int),
	}
	return rb
}

func (rb *RoomBus) Subscribe(roomID string) (ch chan interface{}, cancel func()) {
	ch = make(chan interface{}, 8) // Reduced from 32: room msgs are sparse
	rb.mu.Lock()
	rb.rooms[roomID] = append(rb.rooms[roomID], ch)
	rb.subscriberMetric[roomID]++
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
		// Decrement before cleanup
		rb.subscriberMetric[roomID]--
		if len(out) == 0 {
			// Aggressive cleanup: remove room completely
			delete(rb.rooms, roomID)
			delete(rb.subscriberMetric, roomID)
		} else {
			rb.rooms[roomID] = out
		}
		close(ch)
	}
	return ch, cancel
}

// SubscriberCount returns the number of active subscribers for a room (for metrics).
func (rb *RoomBus) SubscriberCount(roomID string) int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.subscriberMetric[roomID]
}

// RoomCount returns the number of active rooms with subscribers (for metrics).
func (rb *RoomBus) RoomCount() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return len(rb.rooms)
}

// publishLocal sends message to all local subscribers of a room.
func (rb *RoomBus) publishLocal(roomID string, msg map[string]interface{}) {
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

// Publish sends to local subscribers and (if Redis configured) to all other server instances.
func (rb *RoomBus) Publish(roomID string, msg map[string]interface{}) {
	rb.publishLocal(roomID, msg)

	if rb.rdb != nil {
		ctx := context.Background()
		// Instrument Redis Publish operation
		tracer := otel.Tracer("fookie/events")
		_, span := tracer.Start(ctx, "room_bus.redis_publish",
			trace.WithAttributes(
				attribute.String("redis.command", "PUBLISH"),
				attribute.String("redis.key", "fookie:room:"+roomID),
				attribute.String("room_id", roomID),
			),
		)
		defer span.End()

		payload, err := json.Marshal(msg)
		if err != nil {
			span.RecordError(err)
			return
		}
		result := rb.rdb.Publish(ctx, "fookie:room:"+roomID, payload)
		if err := result.Err(); err != nil {
			span.RecordError(err)
		} else {
			span.SetAttributes(attribute.Int64("redis.subscribers", result.Val()))
		}
	}
}

// StartRedisSubscriber subscribes to all room channels on Redis and forwards
// incoming messages to local subscribers. Call in a goroutine.
func (rb *RoomBus) StartRedisSubscriber(ctx context.Context) {
	if rb.rdb == nil {
		return
	}

	// Instrument PSubscribe call
	tracer := otel.Tracer("fookie/events")
	_, subscribeSpan := tracer.Start(ctx, "room_bus.redis_psubscribe",
		trace.WithAttributes(
			attribute.String("redis.command", "PSUBSCRIBE"),
			attribute.String("redis.pattern", "fookie:room:*"),
		),
	)
	defer subscribeSpan.End()

	pubsub := rb.rdb.PSubscribe(ctx, "fookie:room:*")
	defer pubsub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case redisMsg, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			roomID := strings.TrimPrefix(redisMsg.Channel, "fookie:room:")

			// Record message receive as span event
			_, msgSpan := tracer.Start(ctx, "room_bus.redis_message",
				trace.WithAttributes(
					attribute.String("redis.channel", redisMsg.Channel),
					attribute.String("room_id", roomID),
				),
			)

			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(redisMsg.Payload), &msg); err != nil {
				msgSpan.RecordError(err)
			} else {
				rb.publishLocal(roomID, msg)
			}
			msgSpan.End()
		}
	}
}

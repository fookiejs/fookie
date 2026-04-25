package events

// Redis patterns for Seçenek 2 (separate WebSocket server architecture).
// All pub/sub, streams, and queues coordinating WS server ↔ fookie-server ↔ PostgreSQL.

const (
	// PubSub: Room events — all subscribers in room get published events.
	// Format: room:{roomId}:events
	RoomEventPattern = "room:%s:events"

	// PubSub: Entity updates — broadcast to all subscribers.
	// Format: entity:updates
	EntityUpdateChannel = "entity:updates"

	// Stream: Room movement sync — stateful (replay last N messages).
	// Format: room:{roomId}:movements
	// Each message: {"player_id":"p1","x":100,"y":200,"ts":1234567890}
	RoomMovementPattern = "room:%s:movements"

	// List (queue): Mutations from WS server → fookie-server processes.
	// Format: mutations:queue
	// Each item: JSON-encoded GraphQL mutation request
	MutationsQueue = "mutations:queue"

	// PubSub: Mutation results — WS server listens for responses.
	// Format: mutation:result:{requestId}
	MutationResultPattern = "mutation:result:%s"
)

// ChannelName builds a room event channel name.
func ChannelName(roomID string) string {
	return "room:" + roomID + ":events"
}

// StreamName builds a room movement stream name.
func StreamName(roomID string) string {
	return "room:" + roomID + ":movements"
}

// ResultChannel builds a mutation result channel name.
func ResultChannel(requestID string) string {
	return "mutation:result:" + requestID
}

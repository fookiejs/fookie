package fookiegql

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fookiejs/fookie/pkg/events"
	"github.com/graphql-go/graphql"
)

func entityEventToMap(e events.Event) map[string]interface{} {
	// Use pre-computed cached payload JSON (computed at publish time)
	payloadJSON := e.CachedPayloadJSON
	if payloadJSON == "" && e.Payload != nil {
		// Fallback: compute if not cached (shouldn't happen in normal path)
		b, err := json.Marshal(e.Payload)
		if err == nil {
			payloadJSON = string(b)
		}
	}
	return map[string]interface{}{
		"op":           string(e.Op),
		"model":        e.Model,
		"id":           e.ID,
		"payload_json": payloadJSON,
		"ts":           e.Timestamp.UTC().Format(time.RFC3339Nano),
	}
}

func attachSubscriptions(cfg *graphql.SchemaConfig, eventBus *events.Bus, roomBus *events.RoomBus) {
	if eventBus == nil && roomBus == nil {
		return
	}

	fields := graphql.Fields{}

	if eventBus != nil {
		entityType := graphql.NewObject(graphql.ObjectConfig{
			Name: "EntityEvent",
			Fields: graphql.Fields{
				"op": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: fieldResolver("op"),
				},
				"model": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: fieldResolver("model"),
				},
				"id": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.ID),
					Resolve: fieldResolver("id"),
				},
				"payload_json": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: fieldResolver("payload_json"),
				},
				"ts": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: fieldResolver("ts"),
				},
			},
		})

		eb := eventBus
		fields["entity_events"] = &graphql.Field{
			Type: graphql.NewNonNull(entityType),
			Args: graphql.FieldConfigArgument{
				"model": &graphql.ArgumentConfig{Type: graphql.String},
			},
			Subscribe: func(p graphql.ResolveParams) (interface{}, error) {
				evCh, unsub := eb.Subscribe()
				out := make(chan interface{}, 64)
				modelArg, _ := p.Args["model"].(string)
				go func() {
					defer close(out)
					defer unsub()
					for {
						select {
						case <-p.Context.Done():
							return
						case ev, ok := <-evCh:
							if !ok {
								return
							}
							if modelArg != "" && ev.Model != modelArg {
								continue
							}
							select {
							case out <- entityEventToMap(ev):
							case <-p.Context.Done():
								return
							}
						}
					}
				}()
				return out, nil
			},
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				return p.Source, nil
			},
		}
	}

	if roomBus != nil {
		payloadType := graphql.NewObject(graphql.ObjectConfig{
			Name: "RoomGraphQLMessagePayload",
			Fields: graphql.Fields{
				"query": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						if m, ok := p.Source.(map[string]interface{}); ok {
							if q, ok := m["query"].(string); ok {
								return q, nil
							}
						}
						return nil, nil
					},
				},
				"body": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						if m, ok := p.Source.(map[string]interface{}); ok {
							if b, ok := m["body"].(string); ok {
								return b, nil
							}
						}
						return nil, nil
					},
				},
			},
		})

		msgType := graphql.NewObject(graphql.ObjectConfig{
			Name: "RoomGraphQLMessage",
			Fields: graphql.Fields{
				"room_id": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.ID),
					Resolve: fieldResolver("room_id"),
				},
				"method": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: fieldResolver("method"),
				},
				"model": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						if m, ok := p.Source.(map[string]interface{}); ok {
							return m["model"], nil
						}
						return nil, nil
					},
				},
				"record_id": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						if m, ok := p.Source.(map[string]interface{}); ok {
							return m["record_id"], nil
						}
						return nil, nil
					},
				},
				"payload": &graphql.Field{
					Type: payloadType,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						if m, ok := p.Source.(map[string]interface{}); ok {
							if pl, ok := m["payload"].(map[string]interface{}); ok {
								return pl, nil
							}
						}
						return map[string]interface{}{}, nil
					},
				},
			},
		})

		rb := roomBus
		fields["room_graphql_message"] = &graphql.Field{
			Type: graphql.NewNonNull(msgType),
			Args: graphql.FieldConfigArgument{
				"room_id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Subscribe: func(p graphql.ResolveParams) (interface{}, error) {
				rid, _ := p.Args["room_id"].(string)
				if rid == "" {
					return nil, fmt.Errorf("room_id is required")
				}
				ch, unsub := rb.Subscribe(rid)
				go func() {
					<-p.Context.Done()
					unsub()
				}()
				return ch, nil
			},
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				return p.Source, nil
			},
		}
	}

	cfg.Subscription = graphql.NewObject(graphql.ObjectConfig{
		Name:   "Subscription",
		Fields: fields,
	})
}

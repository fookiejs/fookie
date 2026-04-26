package fookiegql

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/graphql-go/graphql"
)

type gqlRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

func decodeGQLRequest(r *http.Request) (gqlRequest, error) {
	var params gqlRequest
	if r.Method == http.MethodGet {
		params.Query = r.URL.Query().Get("query")
		params.OperationName = r.URL.Query().Get("operationName")
		if v := r.URL.Query().Get("variables"); v != "" {
			if err := json.Unmarshal([]byte(v), &params.Variables); err != nil {
				return params, err
			}
		}
		return params, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return params, err
	}
	return params, nil
}

func writeGQLError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"errors": []map[string]string{{"message": msg}},
	})
}

func NewHandler(executor *runtime.Executor, schema graphql.Schema, idem *runtime.IdempotencyStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params, err := decodeGQLRequest(r)
		if err != nil {
			writeGQLError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(params.Query) == "" {
			writeGQLError(w, http.StatusBadRequest, "missing query")
			return
		}

		ctx := r.Context()
		propagator := propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		)
		ctx = propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))
		ctx = WithExecutor(ctx, executor)

		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			ctx = WithToken(ctx, token)
		}
		if ak := strings.TrimSpace(r.Header.Get("X-Fookie-Admin-Key")); ak != "" {
			ctx = WithAdminKey(ctx, ak)
		}

		isSub, err := IsSubscriptionOperation(params.Query, params.OperationName)
		if err != nil {
			writeGQLError(w, http.StatusBadRequest, err.Error())
			return
		}
		if isSub {
			if schema.SubscriptionType() == nil {
				writeGQLError(w, http.StatusNotImplemented, "GraphQL subscriptions are not enabled for this server")
				return
			}
			serveSubscriptionNDJSON(w, ctx, schema, params)
			return
		}

		idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idem != nil && idemKey != "" {
			ir, err := idem.Begin(ctx, idemKey)
			if err != nil {
				writeGQLError(w, http.StatusConflict, err.Error())
				return
			}
			if ir.Replayed {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Idempotency-Replayed", "true")
				if len(ir.Response) > 0 {
					_, _ = w.Write(ir.Response)
				} else {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{})
				}
				return
			}

			result := graphql.Do(graphql.Params{
				Schema:         schema,
				RequestString:  params.Query,
				VariableValues: params.Variables,
				OperationName:  params.OperationName,
				Context:        ctx,
			})

			_ = idem.Commit(ctx, idemKey, result)

			w.Header().Set("Content-Type", "application/json")
			if sc := trace.SpanContextFromContext(ctx); sc.IsValid() && sc.HasTraceID() {
				w.Header().Set("X-Trace-ID", sc.TraceID().String())
			}
			_ = json.NewEncoder(w).Encode(result)
			return
		}

		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  params.Query,
			VariableValues: params.Variables,
			OperationName:  params.OperationName,
			Context:        ctx,
		})

		w.Header().Set("Content-Type", "application/json")
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() && sc.HasTraceID() {
			w.Header().Set("X-Trace-ID", sc.TraceID().String())
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}

func serveSubscriptionNDJSON(w http.ResponseWriter, ctx context.Context, schema graphql.Schema, params gqlRequest) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	flusher, _ := w.(http.Flusher)
	ch := graphql.Subscribe(graphql.Params{
		Schema:         schema,
		RequestString:  params.Query,
		VariableValues: params.Variables,
		OperationName:  params.OperationName,
		Context:        ctx,
	})
	enc := json.NewEncoder(w)
	for res := range ch {
		if err := enc.Encode(res); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

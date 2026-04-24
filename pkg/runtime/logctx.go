package runtime

import (
	"context"

	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel/trace"
)

type ctxKey int

const (
	rootRequestIDKey ctxKey = iota
	rootDepthKey
)

func RootRequestIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(rootRequestIDKey).(string); ok {
		return v
	}
	return ""
}

func rootDepthFromCtx(ctx context.Context) int {
	if v, ok := ctx.Value(rootDepthKey).(int); ok {
		return v
	}
	return 0
}

func withRootRequest(ctx context.Context, id string, depth int) context.Context {
	ctx = context.WithValue(ctx, rootRequestIDKey, id)
	ctx = context.WithValue(ctx, rootDepthKey, depth)
	return ctx
}

func newRootRequestID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() && sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ulid.Make().String()
}

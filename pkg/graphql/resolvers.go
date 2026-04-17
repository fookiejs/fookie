package fookiegql

import (
	"context"

	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/graphql-go/graphql"
)

type contextKey string

const (
	executorKey contextKey = "executor"
	tokenKey   contextKey = "token"
)

func WithExecutor(ctx context.Context, exec *runtime.Executor) context.Context {
	return context.WithValue(ctx, executorKey, exec)
}

func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}

func executorFromCtx(ctx context.Context) *runtime.Executor {
	return ctx.Value(executorKey).(*runtime.Executor)
}

func tokenFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(tokenKey).(string); ok {
		return v
	}
	return ""
}

func injectToken(ctx context.Context, input map[string]interface{}) {
	if token := tokenFromCtx(ctx); token != "" {
		input["token"] = token
	}
}

func resolveCreate(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		input := p.Args["input"].(map[string]interface{})
		injectToken(p.Context, input)
		return exec.Create(p.Context, modelName, input)
	}
}

func resolveRead(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		input := map[string]interface{}{}
		if w, ok := p.Args["where"]; ok && w != nil {
			input["where"] = w.(map[string]interface{})
		}
		injectToken(p.Context, input)
		return exec.Read(p.Context, modelName, input)
	}
}

func resolveAggregateRead(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		input := map[string]interface{}{}
		if w, ok := p.Args["where"]; ok && w != nil {
			input["where"] = w.(map[string]interface{})
		}
		injectToken(p.Context, input)
		rows, err := exec.Read(p.Context, modelName, input)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return rows[0], nil
	}
}

func resolveUpdate(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		id := p.Args["id"].(string)
		input := p.Args["input"].(map[string]interface{})
		injectToken(p.Context, input)
		return exec.Update(p.Context, modelName, id, input)
	}
}

func resolveDelete(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		id := p.Args["id"].(string)
		input := map[string]interface{}{}
		injectToken(p.Context, input)
		err := exec.Delete(p.Context, modelName, id, input)
		if err != nil {
			return false, err
		}
		return true, nil
	}
}

func resolveUpdateMany(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		where := p.Args["where"].(map[string]interface{})
		input := p.Args["input"].(map[string]interface{})
		injectToken(p.Context, input)
		n, err := exec.UpdateMany(p.Context, modelName, where, input)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"count": int(n)}, nil
	}
}

func resolveDeleteMany(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		where := p.Args["where"].(map[string]interface{})
		input := map[string]interface{}{}
		injectToken(p.Context, input)
		n, err := exec.DeleteMany(p.Context, modelName, where, input)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"count": int(n)}, nil
	}
}

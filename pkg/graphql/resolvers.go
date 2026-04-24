package fookiegql

import (
	"context"

	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/graphql-go/graphql"
)

type contextKey string

const (
	executorKey contextKey = "executor"
	tokenKey    contextKey = "token"
	adminKeyKey contextKey = "fookie_admin_key"
)

func WithExecutor(ctx context.Context, exec *runtime.Executor) context.Context {
	return context.WithValue(ctx, executorKey, exec)
}

func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}

func WithAdminKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, adminKeyKey, key)
}

func adminKeyFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(adminKeyKey).(string); ok {
		return v
	}
	return ""
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

func injectTokenCtx(ctx context.Context, req map[string]interface{}) {
	if token := tokenFromCtx(ctx); token != "" {
		req["token"] = token
	}
}

func injectAdminKey(ctx context.Context, req map[string]interface{}) {
	if req == nil {
		return
	}
	if _, exists := req["admin_key"]; exists {
		return
	}
	if k := adminKeyFromCtx(ctx); k != "" {
		req["admin_key"] = k
	}
}

func stripClientSystemFromBody(body map[string]interface{}) {
	if body == nil {
		return
	}
	delete(body, "__system")
}

func resolveCreate(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		body := p.Args["body"].(map[string]interface{})
		stripClientSystemFromBody(body)
		req := map[string]interface{}{"body": body}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Create(p.Context, modelName, req)
	}
}

func resolveRead(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if w, ok := p.Args["filter"]; ok && w != nil {
			req["filter"] = w.(map[string]interface{})
		}
		if c, ok := p.Args["cursor"]; ok && c != nil {
			req["cursor"] = c.(map[string]interface{})
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Read(p.Context, modelName, req)
	}
}

func resolveAggregateRead(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if w, ok := p.Args["filter"]; ok && w != nil {
			req["filter"] = w.(map[string]interface{})
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		rows, err := exec.Read(p.Context, modelName, req)
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
		body := p.Args["body"].(map[string]interface{})
		stripClientSystemFromBody(body)
		req := map[string]interface{}{"body": body}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Update(p.Context, modelName, id, req)
	}
}

func resolveDelete(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		id := p.Args["id"].(string)
		req := map[string]interface{}{}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		err := exec.Delete(p.Context, modelName, id, req)
		if err != nil {
			return false, err
		}
		return true, nil
	}
}

func resolveUpdateMany(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		filter := p.Args["filter"].(map[string]interface{})
		body := p.Args["body"].(map[string]interface{})
		stripClientSystemFromBody(body)
		req := map[string]interface{}{"filter": filter, "body": body}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		n, err := exec.UpdateMany(p.Context, modelName, req)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"count": int(n)}, nil
	}
}

func relatedObjectResolver(fkColumn, relatedModelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		parent, ok := p.Source.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		fkVal := parent[fkColumn]
		if fkVal == nil {
			fkVal = parent[compiler.SnakeCase(fkColumn)]
		}
		if fkVal == nil {
			return nil, nil
		}
		req := map[string]interface{}{
			"filter": map[string]interface{}{
				"id": map[string]interface{}{"eq": fkVal},
			},
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		rows, err := exec.Read(p.Context, relatedModelName, req)
		if err != nil || len(rows) == 0 {
			return nil, err
		}
		return rows[0], nil
	}
}

func hasManyResolver(childModelName, fkColumn string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		parent, ok := p.Source.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		var parentID interface{} = parent["id"]
		if parentID == nil {
			return nil, nil
		}

		filter := map[string]interface{}{
			fkColumn: map[string]interface{}{"eq": parentID},
		}
		if userFilter, ok := p.Args["filter"].(map[string]interface{}); ok {
			for k, v := range userFilter {
				filter[k] = v
			}
		}
		req := map[string]interface{}{"filter": filter}
		if c, ok := p.Args["cursor"]; ok && c != nil {
			req["cursor"] = c.(map[string]interface{})
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Read(p.Context, childModelName, req)
	}
}

func resolveDeleteMany(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{
			"filter": p.Args["filter"].(map[string]interface{}),
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		n, err := exec.DeleteMany(p.Context, modelName, req)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"count": int(n)}, nil
	}
}

func resolveSum(modelName, field string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if userFilter, ok := p.Args["filter"].(map[string]interface{}); ok && len(userFilter) > 0 {
			req["filter"] = userFilter
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Sum(p.Context, modelName, field, req)
	}
}

func resolveCount(modelName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if userFilter, ok := p.Args["filter"].(map[string]interface{}); ok && len(userFilter) > 0 {
			req["filter"] = userFilter
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Count(p.Context, modelName, req)
	}
}

func resolveAvg(modelName, field string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if userFilter, ok := p.Args["filter"].(map[string]interface{}); ok && len(userFilter) > 0 {
			req["filter"] = userFilter
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Avg(p.Context, modelName, field, req)
	}
}

func resolveMin(modelName, field string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if userFilter, ok := p.Args["filter"].(map[string]interface{}); ok && len(userFilter) > 0 {
			req["filter"] = userFilter
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Min(p.Context, modelName, field, req)
	}
}

func resolveMax(modelName, field string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		exec := executorFromCtx(p.Context)
		req := map[string]interface{}{}
		if userFilter, ok := p.Args["filter"].(map[string]interface{}); ok && len(userFilter) > 0 {
			req["filter"] = userFilter
		}
		injectTokenCtx(p.Context, req)
		injectAdminKey(p.Context, req)
		return exec.Max(p.Context, modelName, field, req)
	}
}

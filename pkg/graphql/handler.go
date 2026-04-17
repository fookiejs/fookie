package fookiegql

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/graphql-go/graphql"
)

func NewHandler(executor *runtime.Executor, schema graphql.Schema) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params struct {
			Query         string                 `json:"query"`
			OperationName string                 `json:"operationName"`
			Variables     map[string]interface{} `json:"variables"`
		}

		if r.Method == http.MethodGet {
			params.Query = r.URL.Query().Get("query")
			params.OperationName = r.URL.Query().Get("operationName")
			if v := r.URL.Query().Get("variables"); v != "" {
				json.Unmarshal([]byte(v), &params.Variables)
			}
		} else {
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"errors": []map[string]string{{"message": "invalid request body"}},
				})
				return
			}
		}

		ctx := WithExecutor(r.Context(), executor)
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			ctx = WithToken(ctx, token)
		}

		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  params.Query,
			VariableValues: params.Variables,
			OperationName:  params.OperationName,
			Context:        ctx,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
}

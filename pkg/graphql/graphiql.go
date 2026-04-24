package fookiegql

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed graphiql.html
var graphiqlHTML string

func GraphiQLWrapper(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantsGraphiQL(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(graphiqlHTML))
			return
		}
		api.ServeHTTP(w, r)
	})
}

func wantsGraphiQL(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Query().Get("query") != "" {
		return false
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

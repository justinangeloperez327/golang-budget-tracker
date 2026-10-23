// Package app assembles the Group 1 HTTP bootstrap.
// Chi routing and middleware are introduced in Group 2.
package app

import (
	"encoding/json"
	"io"
	"net/http"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"service": "golang-budget-tracker",
		})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Golang Budget Tracker</title></head><body><main><h1>Golang Budget Tracker</h1><p>Group 1 bootstrap is running. templ and HTMX arrive in Group 5.</p></main></body></html>")
	})
	return mux
}

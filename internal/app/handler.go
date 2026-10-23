// Package app assembles the HTTP router and middleware for the application.
package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/httpx"
)

type statusResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version,omitempty"`
}

func NewHandler(logger *slog.Logger, cfg config.Config) http.Handler {
	r := chi.NewRouter()
	r.Use(requestID)
	r.Use(accessLog(logger))
	r.Use(recoverer(logger))
	r.Use(securityHeaders)
	r.Use(limitBody(cfg.MaxBodyBytes))
	// Timeout cancels the request context. Business handlers must honor ctx.Done().
	r.Use(chimiddleware.Timeout(cfg.RequestTimeout))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, http.StatusNotFound, "not_found", "Resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	})
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		writeStatus(w, statusResponse{Status: "ok", Service: "golang-budget-tracker"})
	})
	// Group 2 readiness is process-only; PostgreSQL checks arrive in Group 3.
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		writeStatus(w, statusResponse{Status: "ready", Service: "golang-budget-tracker"})
	})
	r.Get("/api/v1/status", func(w http.ResponseWriter, req *http.Request) {
		writeStatus(w, statusResponse{Status: "ok", Service: "golang-budget-tracker", Version: "v1"})
	})
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Golang Budget Tracker</title></head><body><main><h1>Golang Budget Tracker</h1><p>Group 2 HTTP infrastructure is running. templ and HTMX arrive in Group 5.</p></main></body></html>")
	})

	return r
}

func writeStatus(w http.ResponseWriter, value statusResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

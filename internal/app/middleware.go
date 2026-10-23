package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/httpx"
)

type requestIDContextKey struct{}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			httpx.WriteError(w, req, http.StatusInternalServerError, "internal_error", "Internal server error")
			return
		}
		value := hex.EncodeToString(id[:])
		w.Header().Set("X-Request-ID", value)
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), requestIDContextKey{}, value)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			wrapped := chimiddleware.NewWrapResponseWriter(w, req.ProtoMajor)
			started := time.Now()
			next.ServeHTTP(wrapped, req)
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := ""
			if routeContext := chi.RouteContext(req.Context()); routeContext != nil {
				route = routeContext.RoutePattern()
			}
			if route == "" {
				route = "unmatched"
			}
			logger.Info("http_request",
				"request_id", requestIDFrom(req.Context()),
				"method", req.Method,
				"route", route,
				"status", status,
				"bytes_written", wrapped.BytesWritten(),
				"duration_ms", time.Since(started).Milliseconds(),
			)
		})
	}
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			defer func() {
				if value := recover(); value != nil {
					logger.Error("http_handler_panic",
						"request_id", requestIDFrom(req.Context()),
						"panic_type", fmt.Sprintf("%T", value),
						"stack", string(debug.Stack()),
					)
					// Headers may already be sent for streaming responses.
					if status, ok := w.(interface{ Written() bool }); ok && status.Written() {
						return
					}
					httpx.WriteError(w, req, http.StatusInternalServerError, "internal_error", "Internal server error")
				}
			}()
			next.ServeHTTP(w, req)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, req)
	})
}

func limitBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if maxBytes > 0 {
				if req.ContentLength > maxBytes {
					httpx.WriteError(w, req, http.StatusRequestEntityTooLarge, "body_too_large", "Request body is too large")
					return
				}
				if req.Body != nil {
					req.Body = http.MaxBytesReader(w, req.Body, maxBytes)
				}
			}
			next.ServeHTTP(w, req)
		})
	}
}

package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecovererDoesNotExposePanic(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("never expose panic detail")
	})
	handler := requestID(accessLog(logger)(recoverer(logger)(panicHandler)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "never expose panic detail") {
		t.Fatal("panic text exposed to client")
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatal("expected structured internal error")
	}
	if !strings.Contains(logs.String(), "http_handler_panic") {
		t.Fatal("expected panic log")
	}
}

func TestUnknownLengthBodyIsBounded(t *testing.T) {
	reader := io.NopCloser(strings.NewReader("123456789"))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = reader
	req.ContentLength = -1
	var tooLarge bool
	handler := limitBody(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var maxErr *http.MaxBytesError
		tooLarge = errors.As(err, &maxErr)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !tooLarge {
		t.Fatal("expected body reader to reject oversized unknown-length stream")
	}
}

func TestRequestIDPropagatesInContext(t *testing.T) {
	handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIDFrom(r.Context()) == "" {
			t.Fatal("missing request ID context")
		}
		if requestIDFrom(r.Context()) != w.Header().Get("X-Request-ID") {
			t.Fatal("request ID mismatch")
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRequestIDFromEmptyContext(t *testing.T) {
	if requestIDFrom(context.Background()) != "" {
		t.Fatal("expected blank request ID")
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
)

func testHandler(logs *bytes.Buffer) http.Handler {
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	return NewHandler(logger, config.Config{RequestTimeout: 2 * time.Second, MaxBodyBytes: 128})
}

func TestHealthAndReadiness(t *testing.T) {
	for _, tc := range []struct{
		path, status, version string
	}{
		{"/healthz", "ok", ""},
		{"/readyz", "ready", ""},
		{"/api/v1/status", "ok", "v1"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var logs bytes.Buffer
			rec := httptest.NewRecorder()
			testHandler(&logs).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("content-type: %q", got)
			}
			var body statusResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Status != tc.status || body.Service != "golang-budget-tracker" || body.Version != tc.version {
				t.Fatalf("body = %+v", body)
			}
			if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(rec.Header().Get("X-Request-ID")) {
				t.Fatal("missing or invalid request ID")
			}
			if !strings.Contains(logs.String(), "http_request") {
				t.Fatal("expected structured access log")
			}
		})
	}
}

func TestHomepageAndSecurityHeaders(t *testing.T) {
	var logs bytes.Buffer
	rec := httptest.NewRecorder()
	testHandler(&logs).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Golang Budget Tracker") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, key := range []string{"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Content-Security-Policy", "Cache-Control"} {
		if rec.Header().Get(key) == "" {
			t.Errorf("missing security header %s", key)
		}
	}
}

func TestErrorsAreJSON(t *testing.T) {
	for _, tc := range []struct{
		method, path string
		status int
		code string
	}{
		{"GET", "/does-not-exist", 404, "not_found"},
		{"POST", "/healthz", 405, "method_not_allowed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var logs bytes.Buffer
			rec := httptest.NewRecorder()
			testHandler(&logs).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status = %d", rec.Code)
			}
			var body struct {
				Error struct{
					Code string `json:"code"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code || body.Error.RequestID == "" {
				t.Fatalf("body = %+v", body)
			}
		})
	}
}

func TestUntrustedRequestIDIsReplaced(t *testing.T) {
	var logs bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "attacker")
	rec := httptest.NewRecorder()
	testHandler(&logs).ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") == "attacker" {
		t.Fatal("must generate trusted ID")
	}
}

func TestBodyLimitRejectsOversizeContentLength(t *testing.T) {
	var logs bytes.Buffer
	req := httptest.NewRequest(http.MethodPost, "/healthz", strings.NewReader(strings.Repeat("a", 300)))
	rec := httptest.NewRecorder()
	testHandler(&logs).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}

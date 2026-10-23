//go:build integration

package integration

import (
	"fmt"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/app"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
)

func uiFixture(t *testing.T) http.Handler {
	t.Helper()
	db := openDB(t)
	cfg := config.Config{
		RequestTimeout: 4 * time.Second,
		MaxBodyBytes: 8192,
		Database: config.Database{ReadyTimeout: time.Second},
		Auth: config.Auth{PublicOrigin: "http://localhost:8080", SessionTTL: 7 * 24 * time.Hour},
	}
	return app.NewHandlerWithAuth(slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg, db.Ready, auth.NewStore(db.Pool(), cfg.Auth.SessionTTL))
}

func formRequest(handler http.Handler, method, path string, values url.Values, cookie *http.Cookie, hx bool, origin string) *httptest.ResponseRecorder {
	body := ""
	if values != nil {
		body = values.Encode()
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestFrontendDocumentAndAssets(t *testing.T) {
	handler := uiFixture(t)
	for _, path := range []string{"/login", "/register"} {
		rec := formRequest(handler, "GET", path, nil, nil, false, "")
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		for _, text := range []string{"<main", "hx-post=", "/assets/app.css", "/assets/htmx.min.js", "name=\"email\""} {
			if !strings.Contains(rec.Body.String(), text) {
				t.Fatalf("GET %s missing %q", path, text)
			}
		}
	}
	for _, path := range []string{"/assets/app.css", "/assets/app.js", "/assets/htmx.min.js"} {
		rec := formRequest(handler, "GET", path, nil, nil, false, "")
		if rec.Code != 200 || rec.Body.Len() < 100 {
			t.Fatalf("asset %s status %d size %d", path, rec.Code, rec.Body.Len())
		}
	}
}

func TestHTMLAuthenticationAndIsolation(t *testing.T) {
	handler := uiFixture(t)
	email := fmt.Sprintf("ui-%d@example.test", time.Now().UnixNano())
	values := url.Values{"email": {email}, "password": {"a-good-unique-password-2026"}}
	if rec := formRequest(handler, "GET", "/dashboard", nil, nil, false, ""); rec.Code != 303 {
		t.Fatalf("anonymous dashboard status %d", rec.Code)
	}
	if rec := formRequest(handler, "POST", "/register", values, nil, false, ""); rec.Code != 403 {
		t.Fatalf("missing origin status %d", rec.Code)
	}
	rec := formRequest(handler, "POST", "/register", values, nil, false, "http://localhost:8080")
	if rec.Code != 303 || rec.Header().Get("Location") != "/dashboard" {
		t.Fatalf("register %d, redirect %q, body %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("registration did not set a session cookie")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe cookie flags")
	}
	dashboard := formRequest(handler, "GET", "/dashboard", nil, cookie, false, "")
	if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), "Your dashboard") ||
		!strings.Contains(dashboard.Body.String(), email) || !strings.Contains(dashboard.Body.String(), "csrf_token") {
		t.Fatalf("dashboard %d %s", dashboard.Code, dashboard.Body.String())
	}
	partial := formRequest(handler, "GET", "/dashboard/activity", nil, cookie, true, "")
	if partial.Code != 200 || !strings.Contains(partial.Body.String(), "Recent activity") ||
		strings.Contains(partial.Body.String(), "<!DOCTYPE") {
		t.Fatalf("fragment %d %s", partial.Code, partial.Body.String())
	}
	if rec := formRequest(handler, "POST", "/logout", url.Values{"csrf_token": {"bad"}}, cookie, false, "http://localhost:8080"); rec.Code != 403 {
		t.Fatalf("logout without CSRF %d", rec.Code)
	}
	csrfRec := formRequest(handler, "GET", "/api/v1/auth/csrf", nil, cookie, false, "")
	if csrfRec.Code != 200 {
		t.Fatalf("get csrf = %d", csrfRec.Code)
	}
	var token struct{ CSRF string `json:"csrf_token"` }
	if err := json.Unmarshal(csrfRec.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	if rec := formRequest(handler, "POST", "/logout", url.Values{"csrf_token": {token.CSRF}}, cookie, false, "https://evil.invalid"); rec.Code != 403 {
		t.Fatalf("cross-origin logout %d", rec.Code)
	}
	if rec := formRequest(handler, "POST", "/logout", url.Values{"csrf_token": {token.CSRF}}, cookie, false, "http://localhost:8080"); rec.Code != 303 {
		t.Fatalf("logout status %d", rec.Code)
	}
	if rec := formRequest(handler, "GET", "/dashboard", nil, cookie, false, ""); rec.Code != 303 {
		t.Fatalf("revoked dashboard status %d", rec.Code)
	}
}

func TestHTMXValidationAndRedirect(t *testing.T) {
	handler := uiFixture(t)
	bad := url.Values{"email": {"invalid"}, "password": {"short"}}
	rec := formRequest(handler, "POST", "/register", bad, nil, true, "http://localhost:8080")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=\"auth-feedback\"") ||
		strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("htmx fragment error %d %s", rec.Code, rec.Body.String())
	}
	email := fmt.Sprintf("hx-%d@example.test", time.Now().UnixNano())
	good := url.Values{"email": {email}, "password": {"a-long-unique-password-for-htmx"}}
	rec = formRequest(handler, "POST", "/register", good, nil, true, "http://localhost:8080")
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("htmx success %d HX-Redirect=%q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

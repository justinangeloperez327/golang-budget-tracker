//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/app"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
)

type authReply struct {
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
	CSRFToken   string `json:"csrf_token"`
	Email       string `json:"email"`
}

func authRequest(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	// Test-local loopback identity prevents independent integration tests from
	// consuming one another's persistent 15-minute registration rate limits.
	hash := sha256.Sum256([]byte(t.Name()))
	req.RemoteAddr = fmt.Sprintf("127.10.%d.%d:12345", hash[0], hash[1])
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func registration(t *testing.T, handler http.Handler, email string) (authReply, *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "very secure test password!"})
	rec := authRequest(t, handler, "POST", "/api/v1/auth/register", string(body), nil, "", "http://localhost:8080")
	if rec.Code != 201 {
		t.Fatalf("registration %s: %d %s", email, rec.Code, rec.Body.String())
	}
	var result authReply
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.UserID == "" || result.WorkspaceID == "" || len(result.CSRFToken) != 64 {
		t.Fatalf("invalid registration payload: %+v", result)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "budget_session" {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
				t.Fatal("insecure session cookie")
			}
			return result, cookie
		}
	}
	t.Fatal("no session cookie")
	return authReply{}, nil
}

func TestAuthenticationLifecycle(t *testing.T) {
	db := openDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{RequestTimeout: 3 * time.Second, MaxBodyBytes: 4096,
		Database: config.Database{ReadyTimeout: time.Second},
		Auth:     config.Auth{PublicOrigin: "http://localhost:8080", SessionTTL: 7 * 24 * time.Hour}}
	handler := app.NewHandlerWithAuth(logger, cfg, db.Ready, auth.NewStore(db.Pool(), cfg.Auth.SessionTTL))
	base := fmt.Sprintf("group4-%d", time.Now().UnixNano())
	alice, aliceCookie := registration(t, handler, base+"-alice@example.test")
	bob, bobCookie := registration(t, handler, base+"-bob@example.test")

	if x := authRequest(t, handler, "GET", "/api/v1/auth/me", "", nil, "", ""); x.Code != 401 {
		t.Fatalf("unauthenticated /me status=%d", x.Code)
	}
	if x := authRequest(t, handler, "GET", "/api/v1/auth/me", "", aliceCookie, "", ""); x.Code != 200 ||
		strings.Contains(x.Body.String(), "csrf_token") || !strings.Contains(x.Body.String(), alice.UserID) {
		t.Fatalf("authenticated /me=%d %s", x.Code, x.Body.String())
	}
	if x := authRequest(t, handler, "GET", "/api/v1/auth/csrf", "", aliceCookie, "", ""); x.Code != 200 ||
		!strings.Contains(x.Body.String(), alice.CSRFToken) {
		t.Fatalf("csrf token retrieval: %d %s", x.Code, x.Body.String())
	}
	if x := authRequest(t, handler, "GET", "/api/v1/workspaces/"+alice.WorkspaceID, "", aliceCookie, "", ""); x.Code != 200 {
		t.Fatalf("own workspace=%d", x.Code)
	}
	if x := authRequest(t, handler, "GET", "/api/v1/workspaces/"+bob.WorkspaceID, "", aliceCookie, "", ""); x.Code != 404 {
		t.Fatalf("cross-owner workspace=%d", x.Code)
	}
	if x := authRequest(t, handler, "GET", "/api/v1/workspaces/"+bob.WorkspaceID, "", bobCookie, "", ""); x.Code != 200 {
		t.Fatalf("bob workspace=%d", x.Code)
	}
	if x := authRequest(t, handler, "POST", "/api/v1/auth/logout", "", aliceCookie, "", "http://localhost:8080"); x.Code != 403 {
		t.Fatalf("logout without csrf=%d", x.Code)
	}
	if x := authRequest(t, handler, "POST", "/api/v1/auth/logout", "", aliceCookie, alice.CSRFToken, "https://attacker.invalid"); x.Code != 403 {
		t.Fatalf("cross-origin logout=%d", x.Code)
	}
	if x := authRequest(t, handler, "POST", "/api/v1/auth/logout", "", aliceCookie, alice.CSRFToken, "http://localhost:8080"); x.Code != 204 {
		t.Fatalf("logout=%d %s", x.Code, x.Body.String())
	}
	if x := authRequest(t, handler, "GET", "/api/v1/auth/me", "", aliceCookie, "", ""); x.Code != 401 {
		t.Fatalf("logged-out session still valid=%d", x.Code)
	}

	bad, _ := json.Marshal(map[string]string{"email": alice.Email, "password": "incorrect-password"})
	if x := authRequest(t, handler, "POST", "/api/v1/auth/login", string(bad), nil, "", "http://localhost:8080"); x.Code != 401 {
		t.Fatalf("incorrect login status=%d", x.Code)
	}
	creds, _ := json.Marshal(map[string]string{"email": alice.Email, "password": "very secure test password!"})
	if x := authRequest(t, handler, "POST", "/api/v1/auth/login", string(creds), nil, "", "http://localhost:8080"); x.Code != 200 {
		t.Fatalf("login=%d %s", x.Code, x.Body.String())
	}
	if x := authRequest(t, handler, "POST", "/api/v1/auth/register", string(creds), nil, "", "http://localhost:8080"); x.Code != 409 {
		t.Fatalf("duplicate email registration=%d", x.Code)
	}
	if x := authRequest(t, handler, "POST", "/api/v1/auth/login", "{} EXTRA", nil, "", "http://localhost:8080"); x.Code != 400 {
		t.Fatalf("malformed JSON status=%d", x.Code)
	}
	if x := authRequest(t, handler, "POST", "/api/v1/auth/login", string(creds), nil, "", ""); x.Code != 403 {
		t.Fatalf("missing Origin status=%d", x.Code)
	}
}

func TestRegistrationLimiter(t *testing.T) {
	db := openDB(t)
	store := auth.NewStore(db.Pool(), 7*24*time.Hour)
	identity := fmt.Sprintf("limit-test-%d", time.Now().UnixNano())
	for i := 0; i < 6; i++ {
		if err := store.LimitRegistration(context.Background(), identity); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := store.LimitRegistration(context.Background(), identity); err != auth.ErrLimited {
		t.Fatalf("expected rate limit, got %v", err)
	}
}

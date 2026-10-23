package auth

import (
	"net/http"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
)

// SessionCookie and ClearSessionCookie expose the same cookie policy to the
// browser forms and JSON API. The raw session token must never be logged.
func SessionCookie(cfg config.Auth, token string, expires time.Time) *http.Cookie {
	return sessionCookie(cfg, token, expires)
}

func ClearSessionCookie(cfg config.Auth) *http.Cookie {
	return clearSessionCookie(cfg)
}

// ValidOrigin rejects absent/mismatched Origin and cross-site Fetch Metadata.
// It must guard every browser-facing state-changing endpoint.
func ValidOrigin(req *http.Request, cfg config.Auth) bool {
	if req.Header.Get("Origin") != cfg.PublicOrigin {
		return false
	}
	site := req.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin" || site == "none"
}

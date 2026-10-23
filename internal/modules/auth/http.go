package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/httpx"
)

type actorContextKey struct{}

func ActorFromContext(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	return actor, ok
}

func CookieName(cfg config.Auth) string {
	if cfg.CookieSecure {
		return "__Host-budget_session"
	}
	return "budget_session"
}

func sessionCookie(cfg config.Auth, token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name: CookieName(cfg), Value: token, Path: "/",
		HttpOnly: true, Secure: cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: expires, MaxAge: int(cfg.SessionTTL.Seconds()),
	}
}

func clearSessionCookie(cfg config.Auth) *http.Cookie {
	return &http.Cookie{
		Name: CookieName(cfg), Value: "", Path: "/", HttpOnly: true,
		Secure: cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: time.Unix(1, 0), MaxAge: -1,
	}
}

func requireOrigin(cfg config.Auth, w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != cfg.PublicOrigin {
		httpx.WriteError(w, r, http.StatusForbidden, "csrf_failed", "Request origin rejected")
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		httpx.WriteError(w, r, http.StatusForbidden, "csrf_failed", "Request origin rejected")
		return false
	}
	return true
}

func readCredentials(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		httpx.WriteError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "JSON required")
		return "", "", false
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Request body too large")
		} else {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_json", "Invalid request")
		}
		return "", "", false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_json", "Invalid request")
		return "", "", false
	}
	return body.Email, body.Password, true
}

func respondJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func authError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrInvalidPassword):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_input", "Invalid account details")
	case errors.Is(err, ErrCredentials), errors.Is(err, ErrUnauthenticated):
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
	case errors.Is(err, ErrConflict):
		httpx.WriteError(w, r, http.StatusConflict, "account_unavailable", "Account cannot be created")
	case errors.Is(err, ErrLimited):
		w.Header().Set("Retry-After", "900")
		httpx.WriteError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests")
	default:
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "temporarily_unavailable", "Please retry later")
	}
}

func requireSession(store *Store, cfg config.Auth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(CookieName(cfg))
			if err != nil {
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in required")
				return
			}
			actor, err := store.Authenticate(r.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					http.SetCookie(w, clearSessionCookie(cfg))
					httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in required")
				} else {
					httpx.WriteError(w, r, http.StatusServiceUnavailable, "temporarily_unavailable", "Please retry later")
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor)))
		})
	}
}

func csrfProtection(cfg config.Auth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if !requireOrigin(cfg, w, r) {
					return
				}
				actor, ok := ActorFromContext(r.Context())
				csrf := r.Header.Get("X-CSRF-Token")
				if !ok || len(csrf) != 64 || subtle.ConstantTimeCompare([]byte(csrf), []byte(actor.CSRFToken)) != 1 {
					httpx.WriteError(w, r, http.StatusForbidden, "csrf_failed", "Invalid CSRF token")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RegisterRoutes mounts JSON API routes only; templ forms are Group 5.
func RegisterRoutes(r chi.Router, store *Store, cfg config.Auth, logger *slog.Logger) {
	r.Route("/api/v1/auth", func(api chi.Router) {
		api.Post("/register", func(w http.ResponseWriter, r *http.Request) {
			if !requireOrigin(cfg, w, r) {
				return
			}
			email, password, ok := readCredentials(w, r)
			if !ok {
				return
			}
			if err := store.LimitRegistration(r.Context(), RemoteIP(r.RemoteAddr)); err != nil {
				authError(w, r, err)
				return
			}
			s, err := store.Register(r.Context(), email, password)
			if err != nil {
				logger.Warn("registration rejected", "error_type", errorCategory(err))
				authError(w, r, err)
				return
			}
			http.SetCookie(w, sessionCookie(cfg, s.Token, s.ExpiresAt))
			respondJSON(w, http.StatusCreated, map[string]any{"user_id": s.Actor.UserID, "workspace_id": s.Actor.WorkspaceID, "email": s.Actor.Email, "csrf_token": s.Actor.CSRFToken})
		})
		api.Post("/login", func(w http.ResponseWriter, r *http.Request) {
			if !requireOrigin(cfg, w, r) {
				return
			}
			email, password, ok := readCredentials(w, r)
			if !ok {
				return
			}
			s, err := store.Login(r.Context(), email, password, RemoteIP(r.RemoteAddr))
			if err != nil {
				logger.Warn("login rejected", "error_type", errorCategory(err))
				authError(w, r, err)
				return
			}
			http.SetCookie(w, sessionCookie(cfg, s.Token, s.ExpiresAt))
			respondJSON(w, http.StatusOK, map[string]any{"user_id": s.Actor.UserID, "workspace_id": s.Actor.WorkspaceID, "email": s.Actor.Email, "csrf_token": s.Actor.CSRFToken})
		})
		api.Group(func(protected chi.Router) {
			protected.Use(requireSession(store, cfg))
			protected.Use(csrfProtection(cfg))
			protected.Get("/me", func(w http.ResponseWriter, r *http.Request) {
				actor, _ := ActorFromContext(r.Context())
				respondJSON(w, http.StatusOK, actor)
			})
			protected.Get("/csrf", func(w http.ResponseWriter, r *http.Request) {
				actor, _ := ActorFromContext(r.Context())
				respondJSON(w, http.StatusOK, map[string]string{"csrf_token": actor.CSRFToken})
			})
			protected.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
				cookie, _ := r.Cookie(CookieName(cfg))
				if err := store.Logout(r.Context(), cookie.Value); err != nil {
					authError(w, r, err)
					return
				}
				http.SetCookie(w, clearSessionCookie(cfg))
				w.WriteHeader(http.StatusNoContent)
			})
		})
	})
	r.Group(func(protected chi.Router) {
		protected.Use(requireSession(store, cfg))
		protected.Use(csrfProtection(cfg))
		protected.Get("/api/v1/workspaces/{workspaceID}", func(w http.ResponseWriter, r *http.Request) {
			actor, _ := ActorFromContext(r.Context())
			id := chi.URLParam(r, "workspaceID")
			name, err := store.WorkspaceName(r.Context(), actor, id)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					httpx.WriteError(w, r, http.StatusNotFound, "not_found", "Resource not found")
				} else {
					authError(w, r, err)
				}
				return
			}
			respondJSON(w, http.StatusOK, map[string]string{"id": id, "name": name})
		})
	})
}

func errorCategory(err error) string {
	switch {
	case errors.Is(err, ErrCredentials):
		return "invalid_credentials"
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrInvalidPassword):
		return "invalid_input"
	case errors.Is(err, ErrConflict):
		return "duplicate"
	case errors.Is(err, ErrLimited):
		return "rate_limited"
	default:
		return "database_or_internal"
	}
}

package app

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func renderPage(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = component.Render(r.Context(), w)
}

func hasHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func formFailure(w http.ResponseWriter, r *http.Request, mode, email, message string, status int) {
	if hasHTMX(r) {
		// htmx defaults to not swapping 4xx responses. Validated form errors
		// are an HTTP 200 partial; native forms receive the real error status.
		renderPage(w, r, http.StatusOK, views.Feedback(message))
		return
	}
	renderPage(w, r, status, views.AuthPage(mode, email, message))
}

func currentActor(w http.ResponseWriter, r *http.Request, store *auth.Store, cfg config.Auth) (auth.Actor, bool) {
	cookie, err := r.Cookie(auth.CookieName(cfg))
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return auth.Actor{}, false
	}
	actor, err := store.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			http.SetCookie(w, auth.ClearSessionCookie(cfg))
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		} else {
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
		}
		return auth.Actor{}, false
	}
	return actor, true
}

func registerUI(r chi.Router, store *auth.Store, cfg config.Auth, logger *slog.Logger) {
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		if cookie, err := req.Cookie(auth.CookieName(cfg)); err == nil {
			if _, err := store.Authenticate(req.Context(), cookie.Value); err == nil {
				http.Redirect(w, req, "/dashboard", http.StatusSeeOther)
				return
			}
		}
		http.Redirect(w, req, "/login", http.StatusSeeOther)
	})
	for _, route := range []struct{ path, mode string }{
		{"/login", "login"}, {"/register", "register"},
	} {
		path, mode := route.path, route.mode
		r.Get(path, func(w http.ResponseWriter, req *http.Request) {
			if cookie, err := req.Cookie(auth.CookieName(cfg)); err == nil {
				if _, err := store.Authenticate(req.Context(), cookie.Value); err == nil {
					http.Redirect(w, req, "/dashboard", http.StatusSeeOther)
					return
				}
			}
			renderPage(w, req, http.StatusOK, views.AuthPage(mode, "", ""))
		})
		r.Post(path, func(w http.ResponseWriter, req *http.Request) {
			if !auth.ValidOrigin(req, cfg) {
				formFailure(w, req, mode, "", "This request could not be verified. Reload and try again.", http.StatusForbidden)
				return
			}
			kind, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil || kind != "application/x-www-form-urlencoded" {
				formFailure(w, req, mode, "", "Please submit the form from this page.", http.StatusUnsupportedMediaType)
				return
			}
			if err := req.ParseForm(); err != nil {
				formFailure(w, req, mode, "", "The form is invalid or too large.", http.StatusBadRequest)
				return
			}
			email := strings.TrimSpace(req.PostFormValue("email"))
			password := req.PostFormValue("password")
			if len(email) > 254 || len(password) > 128 || len(email) == 0 || len(password) == 0 {
				formFailure(w, req, mode, email, "Check the email and password fields.", http.StatusUnprocessableEntity)
				return
			}
			var session auth.Session
			if mode == "register" {
				err = store.LimitRegistration(req.Context(), auth.RemoteIP(req.RemoteAddr))
				if err == nil {
					session, err = store.Register(req.Context(), email, password)
				}
			} else {
				session, err = store.Login(req.Context(), email, password, auth.RemoteIP(req.RemoteAddr))
			}
			if err != nil {
				message, status := authMessage(mode, err)
				if status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "900")
				}
				logger.Warn("browser_auth_rejected", "route", mode, "status", status)
				formFailure(w, req, mode, email, message, status)
				return
			}
			http.SetCookie(w, auth.SessionCookie(cfg, session.Token, session.ExpiresAt))
			if hasHTMX(req) {
				w.Header().Set("HX-Redirect", "/dashboard")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Redirect(w, req, "/dashboard", http.StatusSeeOther)
		})
	}
	r.Get("/dashboard", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, store, cfg)
		if !ok {
			return
		}
		renderPage(w, req, http.StatusOK, views.DashboardPage(actor.Email, actor.CSRFToken))
	})
	r.Get("/dashboard/activity", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := currentActor(w, req, store, cfg); !ok {
			return
		}
		renderPage(w, req, http.StatusOK, views.ActivityPanel())
	})
	r.Post("/logout", func(w http.ResponseWriter, req *http.Request) {
		if !auth.ValidOrigin(req, cfg) {
			http.Error(w, "Request origin rejected", http.StatusForbidden)
			return
		}
		cookie, err := req.Cookie(auth.CookieName(cfg))
		if err != nil {
			http.Redirect(w, req, "/login", http.StatusSeeOther)
			return
		}
		actor, err := store.Authenticate(req.Context(), cookie.Value)
		if err != nil {
			http.SetCookie(w, auth.ClearSessionCookie(cfg))
			http.Redirect(w, req, "/login", http.StatusSeeOther)
			return
		}
		if kind, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || kind != "application/x-www-form-urlencoded" {
			http.Error(w, "Invalid form", http.StatusUnsupportedMediaType)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		token := req.PostFormValue("csrf_token")
		if len(token) != 64 || subtle.ConstantTimeCompare([]byte(token), []byte(actor.CSRFToken)) != 1 {
			http.Error(w, "Invalid CSRF token", http.StatusForbidden)
			return
		}
		if err := store.Logout(req.Context(), cookie.Value); err != nil {
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		http.SetCookie(w, auth.ClearSessionCookie(cfg))
		http.Redirect(w, req, "/login", http.StatusSeeOther)
	})
	registerAccountsUI(r, store, cfg, logger)
	registerTransactionsUI(r, store, cfg, logger)
	registerTransfersUI(r, store, cfg, logger)
	registerBudgetsUI(r, store, cfg, logger)
	registerRecurringUI(r, store, cfg, logger)
	registerDebtsUI(r, store, cfg, logger)
	registerSavingsUI(r, store, cfg, logger)
	registerRemittancesUI(r, store, cfg, logger)
	registerContributionsUI(r, store, cfg, logger)
}

func authMessage(mode string, err error) (string, int) {
	switch {
	case errors.Is(err, auth.ErrLimited):
		return "Too many attempts. Please try again later.", http.StatusTooManyRequests
	case errors.Is(err, auth.ErrInvalidInput), errors.Is(err, auth.ErrInvalidPassword):
		return "Enter a valid email and a password of at least 12 characters.", http.StatusUnprocessableEntity
	case errors.Is(err, auth.ErrConflict):
		return "That account cannot be created. Try signing in.", http.StatusConflict
	case errors.Is(err, auth.ErrCredentials):
		return "Email or password is incorrect.", http.StatusUnauthorized
	default:
		return "We could not complete the request. Try again later.", http.StatusServiceUnavailable
	}
}

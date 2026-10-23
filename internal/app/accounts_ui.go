package app

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"mime"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func accountFormAuthorized(w http.ResponseWriter, r *http.Request, actor auth.Actor, cfg config.Auth) bool {
	if !auth.ValidOrigin(r, cfg) {
		http.Error(w, "Request origin rejected", http.StatusForbidden)
		return false
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected a form submission", http.StatusUnsupportedMediaType)
		return false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Malformed or oversized form", http.StatusBadRequest)
		return false
	}
	token := r.PostFormValue("csrf_token")
	if len(token) != 64 || subtle.ConstantTimeCompare([]byte(token), []byte(actor.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func registerAccountsUI(r chi.Router, identity *auth.Store, cfg config.Auth, logger *slog.Logger) {
	store := accounts.NewStore(identity.DB)
	show := func(w http.ResponseWriter, req *http.Request, message string, status int) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok { return }
		items, err := store.List(req.Context(), actor.WorkspaceID)
		if err != nil {
			logger.Error("accounts_list_unavailable", "type", "database")
			http.Error(w, "Accounts temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		key, err := accounts.NewSetupKey()
		if err != nil {
			http.Error(w, "Accounts temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		renderPage(w, req, status, views.AccountsPage(items, actor.CSRFToken, key, message))
	}
	r.Get("/accounts", func(w http.ResponseWriter, req *http.Request) {
		show(w, req, "", http.StatusOK)
	})
	r.Get("/accounts/rows", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok { return }
		items, err := store.List(req.Context(), actor.WorkspaceID)
		if err != nil {
			http.Error(w, "Accounts temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		renderPage(w, req, http.StatusOK, views.AccountList(items, actor.CSRFToken))
	})
	r.Post("/accounts", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok { return }
		if !accountFormAuthorized(w, req, actor, cfg) { return }
		input := accounts.CreateInput{
			Name: req.PostFormValue("name"),
			Kind: req.PostFormValue("kind"),
			Currency: req.PostFormValue("currency"),
			Opening: req.PostFormValue("opening"),
			SetupKey: req.PostFormValue("setup_key"),
		}
		_, err := store.Create(req.Context(), actor.WorkspaceID, input)
		if err != nil {
			message := "Unable to save the account. Please retry."
			status := http.StatusServiceUnavailable
			switch {
			case errors.Is(err, accounts.ErrInvalid):
				message, status = "Check the account name, type, currency and opening amount.", http.StatusUnprocessableEntity
			case errors.Is(err, accounts.ErrDuplicate):
				message, status = "This account submission was already used. Reload to start a new account.", http.StatusConflict
			default:
				logger.Error("account_create_failed", "cause", "database")
			}
			if hasHTMX(req) {
				renderPage(w, req, http.StatusOK, views.AccountFeedback(message))
				return
			}
			show(w, req, message, status)
			return
		}
		if hasHTMX(req) {
			w.Header().Set("HX-Redirect", "/accounts")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, req, "/accounts", http.StatusSeeOther)
	})
	r.Post("/accounts/{id}/archive", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok { return }
		if !accountFormAuthorized(w, req, actor, cfg) { return }
		err := store.Archive(req.Context(), actor.WorkspaceID, chi.URLParam(req, "id"))
		if err != nil {
			if errors.Is(err, accounts.ErrNotFound) {
				http.Error(w, "Account not found", http.StatusNotFound)
				return
			}
			logger.Error("account_archive_failed", "cause", "database")
			http.Error(w, "Accounts temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		if hasHTMX(req) {
			w.Header().Set("HX-Redirect", "/accounts")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, req, "/accounts", http.StatusSeeOther)
	})
}

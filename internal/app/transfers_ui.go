package app

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func registerTransfersUI(r chi.Router, identity *auth.Store, cfg config.Auth, logger *slog.Logger) {
	store := transfers.NewStore(identity.DB)
	walletStore := accounts.NewStore(identity.DB)
	r.Get("/transfers", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok {
			return
		}
		wallets, e := walletStore.List(req.Context(), actor.WorkspaceID)
		if e != nil {
			http.Error(w, "Accounts temporarily unavailable", 503)
			return
		}
		movements, e := store.Transfers(req.Context(), actor.WorkspaceID)
		if e != nil {
			http.Error(w, "Transfer history temporarily unavailable", 503)
			return
		}
		checks, e := store.Reconciliations(req.Context(), actor.WorkspaceID)
		if e != nil {
			http.Error(w, "Reconciliation history temporarily unavailable", 503)
			return
		}
		moveKey, e := accounts.NewSetupKey()
		if e != nil {
			http.Error(w, "Service temporarily unavailable", 503)
			return
		}
		reconcileKey, e := accounts.NewSetupKey()
		if e != nil {
			http.Error(w, "Service temporarily unavailable", 503)
			return
		}
		renderPage(w, req, http.StatusOK, views.TransfersPage(wallets, movements, checks, actor.CSRFToken,
			moveKey, reconcileKey, time.Now().UTC().Format("2006-01-02"), ""))
	})
	r.Post("/transfers", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok {
			return
		}
		if !accountFormAuthorized(w, req, actor, cfg) {
			return
		}
		in := transfers.Input{
			FromID: req.PostFormValue("from_id"), ToID: req.PostFormValue("to_id"),
			Amount: req.PostFormValue("amount"), Received: req.PostFormValue("received"),
			Fee: req.PostFormValue("fee"), Rate: req.PostFormValue("rate"),
			Key: req.PostFormValue("request_key"), Date: req.PostFormValue("occurred_on"),
			Note: req.PostFormValue("note"),
		}
		if _, err := store.Transfer(req.Context(), actor.WorkspaceID, in); err != nil {
			movementFailure(w, req, err, logger)
			return
		}
		movementRedirect(w, req)
	})
	r.Post("/reconciliations", func(w http.ResponseWriter, req *http.Request) {
		actor, ok := currentActor(w, req, identity, cfg)
		if !ok {
			return
		}
		if !accountFormAuthorized(w, req, actor, cfg) {
			return
		}
		in := transfers.ReconcileInput{
			AccountID: req.PostFormValue("account_id"), Observed: req.PostFormValue("observed"),
			Key: req.PostFormValue("request_key"), Date: req.PostFormValue("occurred_on"),
			Note: req.PostFormValue("note"),
		}
		if _, err := store.Reconcile(req.Context(), actor.WorkspaceID, in); err != nil {
			movementFailure(w, req, err, logger)
			return
		}
		movementRedirect(w, req)
	})
}
func movementFailure(w http.ResponseWriter, r *http.Request, err error, logger *slog.Logger) {
	status := http.StatusServiceUnavailable
	message := "Unable to save. Please retry."
	switch {
	case errors.Is(err, transfers.ErrInvalid):
		status = http.StatusUnprocessableEntity
		message = "Check accounts, amounts, FX rate, fee, date and statement value."
	case errors.Is(err, transfers.ErrOverflow):
		status = http.StatusUnprocessableEntity
		message = "The resulting financial amount exceeds supported limits."
	case errors.Is(err, transfers.ErrNotFound):
		status = http.StatusNotFound
		message = "The selected account is not available."
	case errors.Is(err, transfers.ErrConflict):
		status = http.StatusConflict
		message = "This request was already submitted with different details. Reload the page."
	default:
		logger.Error("account_movement_failed", "cause", "persistence")
	}
	if hasHTMX(r) {
		renderPage(w, r, http.StatusOK, views.MovementFeedback(message))
		return
	}
	http.Error(w, message, status)
}
func movementRedirect(w http.ResponseWriter, r *http.Request) {
	if hasHTMX(r) {
		w.Header().Set("HX-Redirect", "/transfers")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

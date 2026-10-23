package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/httpx"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

// Group 7 exposes read-only ledger observations. Groups 8–9 will own
// authorized, domain-validated mutation APIs for expenses and transfers.
func registerLedgerAPI(r chi.Router, identity *auth.Store, cfg config.Auth) {
	s := ledger.NewStore(identity.DB)
	r.Route("/api/v1/ledger", func(api chi.Router) {
		check := func(w http.ResponseWriter, req *http.Request) (auth.Actor, bool) {
			cookie, err := req.Cookie(auth.CookieName(cfg))
			if err != nil {
				httpx.WriteError(w, req, http.StatusUnauthorized, "unauthenticated", "Sign in required")
				return auth.Actor{}, false
			}
			actor, err := identity.Authenticate(req.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, auth.ErrUnauthenticated) {
					httpx.WriteError(w, req, http.StatusUnauthorized, "unauthenticated", "Sign in required")
				} else {
					httpx.WriteError(w, req, http.StatusServiceUnavailable, "temporarily_unavailable", "Please retry later")
				}
				return auth.Actor{}, false
			}
			return actor, true
		}
		api.Get("/balances", func(w http.ResponseWriter, req *http.Request) {
			actor, ok := check(w, req)
			if !ok {
				return
			}
			balances, err := s.Balances(req.Context(), actor.WorkspaceID)
			if err != nil {
				httpx.WriteError(w, req, http.StatusServiceUnavailable, "temporarily_unavailable", "Please retry later")
				return
			}
			writeLedgerJSON(w, map[string]any{"balances": balances})
		})
		api.Get("/journals", func(w http.ResponseWriter, req *http.Request) {
			actor, ok := check(w, req)
			if !ok {
				return
			}
			history, err := s.Recent(req.Context(), actor.WorkspaceID, 50)
			if err != nil {
				httpx.WriteError(w, req, http.StatusServiceUnavailable, "temporarily_unavailable", "Please retry later")
				return
			}
			writeLedgerJSON(w, map[string]any{"journals": history})
		})
	})
}

func writeLedgerJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

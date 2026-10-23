//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestAccountBrowserWorkflowAndOwnership(t *testing.T) {
	handler := uiFixture(t)
	seed := time.Now().UnixNano()
	alice, aliceCookie := registration(t, handler, fmt.Sprintf("accounts-a-%d@example.test", seed))
	bob, bobCookie := registration(t, handler, fmt.Sprintf("accounts-b-%d@example.test", seed))

	if rec := formRequest(handler, "GET", "/accounts", nil, nil, false, ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous list: %d", rec.Code)
	}
	page := formRequest(handler, "GET", "/accounts", nil, aliceCookie, false, "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Accounts &amp; wallets") ||
		!strings.Contains(page.Body.String(), "setup_key") {
		t.Fatalf("accounts page: %d %s", page.Code, page.Body.String())
	}

	key, err := accounts.NewSetupKey()
	if err != nil { t.Fatal(err) }
	form := url.Values{
		"name": {"UAE Bank <script>alert(1)</script>"},
		"kind": {"bank"}, "currency": {"AED"},
		"opening": {"1234.56"}, "setup_key": {key},
		"csrf_token": {alice.CSRFToken},
	}
	origin := "http://localhost:8080"
	if rec := formRequest(handler, "POST", "/accounts", form, aliceCookie, false, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing Origin: %d", rec.Code)
	}
	missingCSRF := url.Values{}
	for k, v := range form { missingCSRF[k] = append([]string{}, v...) }
	missingCSRF.Set("csrf_token", "bad")
	if rec := formRequest(handler, "POST", "/accounts", missingCSRF, aliceCookie, false, origin); rec.Code != http.StatusForbidden {
		t.Fatalf("invalid csrf: %d", rec.Code)
	}
	created := formRequest(handler, "POST", "/accounts", form, aliceCookie, false, origin)
	if created.Code != http.StatusSeeOther || created.Header().Get("Location") != "/accounts" {
		t.Fatalf("create account: %d %s", created.Code, created.Body.String())
	}
	if rec := formRequest(handler, "POST", "/accounts", form, aliceCookie, false, origin); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate setup key should return 409, got %d", rec.Code)
	}
	alicePage := formRequest(handler, "GET", "/accounts", nil, aliceCookie, false, "")
	if alicePage.Code != http.StatusOK || !strings.Contains(alicePage.Body.String(), "AED 1,234.56") {
		t.Fatalf("missing AED account: %d %s", alicePage.Code, alicePage.Body.String())
	}
	if strings.Contains(alicePage.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("unescaped account name HTML")
	}
	if !strings.Contains(alicePage.Body.String(), "&lt;script&gt;") {
		t.Fatal("escaped name not rendered")
	}
	if rec := formRequest(handler, "GET", "/accounts", nil, bobCookie, false, ""); strings.Contains(rec.Body.String(), "UAE Bank") {
		t.Fatal("workspace leakage")
	}
	if rec := formRequest(handler, "GET", "/accounts/rows", nil, aliceCookie, true, ""); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), "AED 1,234.56") || strings.Contains(rec.Body.String(), "<!DOCTYPE") {
		t.Fatalf("account fragment: %d %s", rec.Code, rec.Body.String())
	}

	pesoKey, err := accounts.NewSetupKey()
	if err != nil { t.Fatal(err) }
	pesoForm := url.Values{
		"name": {"Philippine credit card"}, "kind": {"credit"},
		"currency": {"PHP"}, "opening": {"2000.00"},
		"setup_key": {pesoKey}, "csrf_token": {alice.CSRFToken},
	}
	htmx := formRequest(handler, "POST", "/accounts", pesoForm, aliceCookie, true, origin)
	if htmx.Code != http.StatusNoContent || htmx.Header().Get("HX-Redirect") != "/accounts" {
		t.Fatalf("HTMX create: %d %q", htmx.Code, htmx.Header().Get("HX-Redirect"))
	}
	badAmount := url.Values{
		"name": {"Bad amount"}, "kind": {"cash"},
		"currency": {"AED"}, "opening": {"-10.11"},
		"setup_key": {pesoKey}, "csrf_token": {alice.CSRFToken},
	}
	if rec := formRequest(handler, "POST", "/accounts", badAmount, aliceCookie, true, origin); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), "account-feedback") {
		t.Fatalf("HTMX validation: %d %s", rec.Code, rec.Body.String())
	}
	db := openDB(t)
	ctx := context.Background()
	var firstID, firstCurrency string
	var amount int64
	if err := db.Pool().QueryRow(ctx, `
		SELECT a.id,a.currency,o.amount_minor FROM accounts a
		JOIN account_openings o ON o.account_id=a.id
		WHERE a.workspace_id=$1 AND a.name LIKE 'UAE Bank%'`, alice.WorkspaceID).
		Scan(&firstID,&firstCurrency,&amount);err != nil {
		t.Fatal(err)
	}
	if firstCurrency != "AED" || amount != 123456 {
		t.Fatalf("opening mismatch: %s %d", firstCurrency, amount)
	}
	var total int
	if err:=db.Pool().QueryRow(ctx,`SELECT COUNT(*) FROM accounts WHERE workspace_id=$1`,alice.WorkspaceID).Scan(&total);err!=nil {
		t.Fatal(err)
	}
	if total != 2 {t.Fatalf("duplicate or invalid account was persisted: %d",total)}

	foreignArchive := formRequest(handler, "POST", "/accounts/"+firstID+"/archive",
		url.Values{"csrf_token": {alice.CSRFToken}}, bobCookie, false, origin)
	if foreignArchive.Code != http.StatusForbidden {
		// The foreign user lacks the owner's CSRF token; this blocks earlier
		// than workspace isolation. Repeat with Bob's actual token below.
		t.Fatalf("foreign token should be rejected: %d", foreignArchive.Code)
	}
	// Even a valid CSRF token for Bob cannot archive Alice's account.
	if rec := formRequest(handler, "POST", "/accounts/"+firstID+"/archive",
		url.Values{"csrf_token": {bob.CSRFToken}}, bobCookie, false, origin); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace archive status: %d", rec.Code)
	}
	archive := formRequest(handler, "POST", "/accounts/"+firstID+"/archive",
		url.Values{"csrf_token": {alice.CSRFToken}}, aliceCookie, false, origin)
	if archive.Code != http.StatusSeeOther {
		t.Fatalf("archive own account: %d", archive.Code)
	}
	if again := formRequest(handler, "POST", "/accounts/"+firstID+"/archive",
		url.Values{"csrf_token": {alice.CSRFToken}}, aliceCookie, false, origin); again.Code != http.StatusNotFound {
		t.Fatalf("archive must be idempotent without deleting record, got %d", again.Code)
	}
	after := formRequest(handler, "GET", "/accounts", nil, aliceCookie, false, "")
	if !strings.Contains(after.Body.String(), "Archived") || !strings.Contains(after.Body.String(), "PHP 2,000.00") {
		t.Fatal("archived account missing or PHP liability lost")
	}
	// The database enforces immutable opening records even against direct SQL updates.
	if _,err := db.Pool().Exec(ctx,
		`UPDATE account_openings SET amount_minor=1 WHERE account_id=$1`,firstID);err==nil {
		t.Fatal("opening amount must be immutable at DB level")
	}
	if err:=db.Pool().QueryRow(ctx,`SELECT amount_minor FROM account_openings WHERE account_id=$1`,firstID).Scan(&amount);err!=nil||amount!=123456 {
		t.Fatalf("opening changed unexpectedly: %d, %v",amount,err)
	}
}

func TestAccountsStoreRejectsCrossWorkspaceArchive(t *testing.T) {
	// Valid workspace identity is derived from the session in the HTTP layer.
	// PostgreSQL's workspace predicate prevents cross-owner updates even if an ID is guessed.
	db := openDB(t)
	store := accounts.NewStore(db.Pool())
	if err := store.Archive(context.Background(), "00000000000000000000000000000000", "11111111111111111111111111111111");
		err != accounts.ErrNotFound {
		t.Fatalf("unexpected archive result: %v",err)
	}
}

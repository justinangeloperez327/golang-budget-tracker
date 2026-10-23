package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
)

func TestOriginGuard(t *testing.T) {
	cfg:=config.Auth{PublicOrigin:"http://localhost:8080"}
	test:=func(origin, fetch string,want int) {
		req:=httptest.NewRequest(http.MethodPost,"/",nil)
		req.Header.Set("Origin",origin)
		if fetch!="" {req.Header.Set("Sec-Fetch-Site",fetch)}
		w:=httptest.NewRecorder()
		ok:=requireOrigin(cfg,w,req)
		if ok!=(want==200) {t.Fatalf("origin=%q fetch=%q accepted=%v",origin,fetch,ok)}
		if !ok&&w.Code!=403 {t.Fatalf("status=%d",w.Code)}
	}
	test("http://localhost:8080","same-origin",200)
	test("http://attacker.invalid","cross-site",403)
	test("http://localhost:8080","cross-site",403)
	test("","",403)
}

func TestSessionCookieFlags(t *testing.T) {
	dev:=config.Auth{SessionTTL:7*24*time.Hour}
	if CookieName(dev)!="budget_session" || sessionCookie(dev,"abc",time.Now()).Secure {t.Fatal("development cookie incorrect")}
	prod:=config.Auth{CookieSecure:true,SessionTTL:dev.SessionTTL}
	cookie:=sessionCookie(prod,"abc",time.Now())
	if CookieName(prod)!="__Host-budget_session" || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite!=http.SameSiteLaxMode || cookie.Path!="/" {t.Fatal("production cookie insecure")}
}

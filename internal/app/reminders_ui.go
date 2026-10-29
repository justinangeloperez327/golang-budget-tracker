package app

import (
 "errors"
 "html/template"
 "log/slog"
 "net/http"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/config"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/reminders"
)

var remindersPage = template.Must(template.New("reminders").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Reminders</title><link rel="stylesheet" href="/assets/app.css"></head><body><main class="mx-auto max-w-3xl p-6"><nav><a href="/dashboard">Dashboard</a> / Reminders</nav><h1 class="text-2xl font-bold my-5">Outstanding reminders</h1><p>Reminders are informational. Dismissing one never marks a bill paid.</p>{{if not .Items}}<p>No outstanding reminders within the supported window.</p>{{end}}{{range .Items}}<section class="border rounded p-4 my-3"><strong>{{.Title}}</strong> — {{.Currency}} — {{.Status}}<p>Due {{.DueOn}} · {{.AmountMinor}} minor units</p><form method="post" action="/reminders/dismiss"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="kind" value="{{.Kind}}"><input type="hidden" name="resource_id" value="{{.ResourceID}}"><input type="hidden" name="due_on" value="{{.DueOn}}"><button type="submit">Dismiss reminder</button></form></section>{{end}}</main></body></html>`))

func registerRemindersUI(r chi.Router, identity *auth.Store,cfg config.Auth,logger *slog.Logger) {
 store:=reminders.NewStore(identity.DB)
 r.Get("/reminders",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg);if !ok{return}
  items,err:=store.List(req.Context(),actor.WorkspaceID,time.Now().UTC())
  if err!=nil{logger.Error("reminder_read_failed");http.Error(w,"Reminders unavailable",503);return}
  w.Header().Set("Content-Type","text/html; charset=utf-8");w.Header().Set("Cache-Control","no-store")
  _=remindersPage.Execute(w,struct{CSRF string;Items []reminders.Item}{actor.CSRFToken,items})
 })
 r.Post("/reminders/dismiss",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg);if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  err:=store.Dismiss(req.Context(),actor.WorkspaceID,req.PostFormValue("kind"),req.PostFormValue("resource_id"),req.PostFormValue("due_on"),time.Now().UTC())
  if err!=nil {
   status:=http.StatusServiceUnavailable
   if errors.Is(err,reminders.ErrInvalid){status=422}else if errors.Is(err,reminders.ErrNotFound){status=404}
   http.Error(w,"Reminder cannot be dismissed",status);return
  }
  http.Redirect(w,req,"/reminders",http.StatusSeeOther)
 })
}

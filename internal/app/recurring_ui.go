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
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func registerRecurringUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=recurring.NewStore(identity.DB)
 accountsStore:=accounts.NewStore(identity.DB)
 financeStore:=transactions.NewStore(identity.DB)
 r.Get("/recurring",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  schedules,err:=store.Schedules(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Schedules unavailable",http.StatusServiceUnavailable);return}
  occurrences,err:=store.Upcoming(req.Context(),actor.WorkspaceID,time.Now().UTC())
  if err!=nil{http.Error(w,"Scheduled occurrences unavailable",http.StatusServiceUnavailable);return}
  for i:=range occurrences{
   occurrences[i].PayKey,err=accounts.NewSetupKey()
   if err!=nil{http.Error(w,"Service unavailable",503);return}
   occurrences[i].SkipKey,err=accounts.NewSetupKey()
   if err!=nil{http.Error(w,"Service unavailable",503);return}
  }
  history,err:=store.Payments(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Payment history unavailable",503);return}
  wallets,err:=accountsStore.List(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Accounts unavailable",503);return}
  categories,err:=financeStore.Categories(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Categories unavailable",503);return}
  expenses,err:=financeStore.List(req.Context(),actor.WorkspaceID,transactions.Filter{Kind:"expense"})
  if err!=nil{http.Error(w,"Expense history unavailable",503);return}
  key,err:=accounts.NewSetupKey()
  if err!=nil{http.Error(w,"Service unavailable",503);return}
  renderPage(w,req,http.StatusOK,views.RecurringPage(schedules,occurrences,history,
   wallets,categories,expenses,actor.CSRFToken,key,time.Now().UTC().Format("2006-01-02"),""))
 })
 r.Post("/recurring",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,err:=store.Create(req.Context(),actor.WorkspaceID,recurring.NewSchedule{
   Name:req.PostFormValue("name"),Kind:req.PostFormValue("kind"),
   Currency:req.PostFormValue("currency"),AccountID:req.PostFormValue("account_id"),
   CategoryID:req.PostFormValue("category_id"),Amount:req.PostFormValue("amount"),
   Frequency:req.PostFormValue("frequency"),StartOn:req.PostFormValue("start_on"),
   EndOn:req.PostFormValue("end_on"),Key:req.PostFormValue("request_key"),
  })
  if err!=nil{recurringFailure(w,req,logger,err);return}
  recurringRedirect(w,req)
 })
 r.Post("/recurring/record",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,err:=store.Record(req.Context(),actor.WorkspaceID,recurring.NewAction{
   ScheduleID:req.PostFormValue("schedule_id"),DueOn:req.PostFormValue("due_on"),
   Action:req.PostFormValue("action"),TransactionID:req.PostFormValue("transaction_id"),
   Key:req.PostFormValue("request_key"),
  })
  if err!=nil{recurringFailure(w,req,logger,err);return}
  recurringRedirect(w,req)
 })
 r.Post("/recurring/{id}/archive",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  if err:=store.Archive(req.Context(),actor.WorkspaceID,chi.URLParam(req,"id"));err!=nil{
   recurringFailure(w,req,logger,err)
   return
  }
  recurringRedirect(w,req)
 })
}
func recurringFailure(w http.ResponseWriter,r *http.Request,logger *slog.Logger,err error){
 status:=http.StatusServiceUnavailable
 message:="Unable to save. Please retry."
 switch{
 case errors.Is(err,recurring.ErrInvalid):
  status=http.StatusUnprocessableEntity
  message="Check your schedule, account, currency, category, dates, amount and matching expense."
 case errors.Is(err,recurring.ErrNotFound):
  status=http.StatusNotFound
  message="The selected schedule or expense is not available."
 case errors.Is(err,recurring.ErrConflict),errors.Is(err,recurring.ErrRecorded):
  status=http.StatusConflict
  message="This occurrence or expense has already been recorded, or the request key was used."
 default:
  logger.Error("recurring_write_failure","kind","persistence")
 }
 if hasHTMX(r){
  renderPage(w,r,http.StatusOK,views.RecurringFeedback(message))
  return
 }
 http.Error(w,message,status)
}
func recurringRedirect(w http.ResponseWriter,r *http.Request){
 if hasHTMX(r){
  w.Header().Set("HX-Redirect","/recurring")
  w.WriteHeader(http.StatusNoContent)
  return
 }
 http.Redirect(w,r,"/recurring",http.StatusSeeOther)
}

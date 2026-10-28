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
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/savings"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)
func registerSavingsUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=savings.NewStore(identity.DB)
 ac:=accounts.NewStore(identity.DB)
 r.Get("/savings",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  goals,e:=store.Goals(req.Context(),actor.WorkspaceID,time.Now().UTC())
  if e!=nil{http.Error(w,"Savings goals unavailable",503);return}
  for i:=range goals{
   goals[i].ContributeKey,e=accounts.NewSetupKey()
   if e!=nil{http.Error(w,"Service unavailable",503);return}
   goals[i].WithdrawKey,e=accounts.NewSetupKey()
   if e!=nil{http.Error(w,"Service unavailable",503);return}
  }
  movements,e:=store.Movements(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Movement history unavailable",503);return}
  wallets,e:=ac.List(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Accounts unavailable",503);return}
  key,e:=accounts.NewSetupKey()
  if e!=nil{http.Error(w,"Service unavailable",503);return}
  renderPage(w,req,http.StatusOK,views.SavingsPage(goals,movements,wallets,
    actor.CSRFToken,key,time.Now().UTC().Format("2006-01-02"),""))
 })
 r.Post("/savings",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,e:=store.Create(req.Context(),actor.WorkspaceID,savings.CreateInput{
   Name:req.PostFormValue("name"),SavingsAccountID:req.PostFormValue("savings_account_id"),
   TargetAmount:req.PostFormValue("target_amount"),TargetOn:req.PostFormValue("target_on"),
   Key:req.PostFormValue("request_key"),
  })
  if e!=nil{savingsFailure(w,req,logger,e);return}
  savingsRedirect(w,req)
 })
 r.Post("/savings/movements",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,e:=store.Move(req.Context(),actor.WorkspaceID,savings.MovementInput{
   GoalID:req.PostFormValue("goal_id"),OtherAccountID:req.PostFormValue("other_account_id"),
   Direction:req.PostFormValue("direction"),Amount:req.PostFormValue("amount"),
   OccurredOn:req.PostFormValue("occurred_on"),Key:req.PostFormValue("request_key"),
  })
  if e!=nil{savingsFailure(w,req,logger,e);return}
  savingsRedirect(w,req)
 })
 r.Post("/savings/{id}/archive",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  if e:=store.Archive(req.Context(),actor.WorkspaceID,chi.URLParam(req,"id"));e!=nil{
   savingsFailure(w,req,logger,e);return
  }
  savingsRedirect(w,req)
 })
}
func savingsFailure(w http.ResponseWriter,r *http.Request,logger *slog.Logger,e error){
 status:=http.StatusServiceUnavailable
 message:="Unable to save savings goal or transfer. Please retry."
 switch{
 case errors.Is(e,savings.ErrInvalid):
  status=http.StatusUnprocessableEntity
  message="Check goal, accounts, dates, currency, and positive amount."
 case errors.Is(e,savings.ErrNotFound):
  status=http.StatusNotFound
  message="The selected goal or account is unavailable."
 case errors.Is(e,savings.ErrConflict):
  status=http.StatusConflict
  message="This request key was already used with different details. Reload the page."
 case errors.Is(e,savings.ErrAllocated):
  status=http.StatusConflict
  message="That savings account is already assigned to a goal or cannot be used as another goal's funding source."
 case errors.Is(e,savings.ErrFunds):
  status=http.StatusUnprocessableEntity
  message="Insufficient account balance to transfer this amount."
 case errors.Is(e,savings.ErrOverflow):
  status=http.StatusUnprocessableEntity
  message="Transfer would exceed the supported monetary limit."
 default:
  logger.Error("savings_goal_write_failed","kind","persistence")
 }
 if hasHTMX(r){renderPage(w,r,http.StatusOK,views.SavingsFeedback(message));return}
 http.Error(w,message,status)
}
func savingsRedirect(w http.ResponseWriter,r *http.Request){
 if hasHTMX(r){w.Header().Set("HX-Redirect","/savings");w.WriteHeader(http.StatusNoContent);return}
 http.Redirect(w,r,"/savings",http.StatusSeeOther)
}

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
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/remittances"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func registerRemittancesUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=remittances.NewStore(identity.DB)
 ac:=accounts.NewStore(identity.DB)
 r.Get("/remittances",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  orders,e:=store.Orders(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Remittance history unavailable",http.StatusServiceUnavailable);return}
  for i:=range orders{
   orders[i].SettleKey,e=accounts.NewSetupKey()
   if e!=nil{http.Error(w,"Service unavailable",503);return}
   orders[i].CancelKey,e=accounts.NewSetupKey()
   if e!=nil{http.Error(w,"Service unavailable",503);return}
  }
  wallets,e:=ac.List(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Accounts unavailable",503);return}
  key,e:=accounts.NewSetupKey()
  if e!=nil{http.Error(w,"Service unavailable",503);return}
  renderPage(w,req,http.StatusOK,views.RemittancesPage(orders,wallets,actor.CSRFToken,
   key,time.Now().UTC().Format("2006-01-02"),""))
 })
 r.Post("/remittances",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,e:=store.Create(req.Context(),actor.WorkspaceID,remittances.CreateInput{
   SourceAccountID:req.PostFormValue("source_account_id"),
   DestinationAccountID:req.PostFormValue("destination_account_id"),
   Send:req.PostFormValue("send"),Fee:req.PostFormValue("fee"),
   Receive:req.PostFormValue("receive"),Rate:req.PostFormValue("rate"),
   RequestedOn:req.PostFormValue("requested_on"),Note:req.PostFormValue("note"),
   Key:req.PostFormValue("request_key"),
  })
  if e!=nil{remittanceFailure(w,req,logger,e);return}
  remittanceRedirect(w,req)
 })
 r.Post("/remittances/resolve",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,e:=store.Resolve(req.Context(),actor.WorkspaceID,remittances.ResolveInput{
   OrderID:req.PostFormValue("order_id"),Action:req.PostFormValue("action"),
   Date:req.PostFormValue("settled_on"),Key:req.PostFormValue("request_key"),
  })
  if e!=nil{remittanceFailure(w,req,logger,e);return}
  remittanceRedirect(w,req)
 })
}
func remittanceFailure(w http.ResponseWriter,r *http.Request,logger *slog.Logger,e error){
 status:=http.StatusServiceUnavailable
 message:="Unable to save remittance. Please retry."
 switch{
 case errors.Is(e,remittances.ErrInvalid):
  status=http.StatusUnprocessableEntity
  message="Check AED/PHP accounts, quoted rate, exact received amount, fees and dates."
 case errors.Is(e,remittances.ErrNotFound):
  status=http.StatusNotFound
  message="The remittance or selected account is not available."
 case errors.Is(e,remittances.ErrConflict),errors.Is(e,remittances.ErrResolved):
  status=http.StatusConflict
  message="This remittance was already resolved or the request key was reused. Reload the page."
 case errors.Is(e,remittances.ErrFunds):
  status=http.StatusUnprocessableEntity
  message="Insufficient AED funds for the send amount plus the fee."
 case errors.Is(e,remittances.ErrOverflow):
  status=http.StatusUnprocessableEntity
  message="The requested amount or effective FX calculation exceeds the supported limit."
 default:
  logger.Error("remittance_persistence_failed","kind","storage")
 }
 if hasHTMX(r){renderPage(w,r,http.StatusOK,views.RemittanceFeedback(message));return}
 http.Error(w,message,status)
}
func remittanceRedirect(w http.ResponseWriter,r *http.Request){
 if hasHTMX(r){
  w.Header().Set("HX-Redirect","/remittances")
  w.WriteHeader(http.StatusNoContent)
  return
 }
 http.Redirect(w,r,"/remittances",http.StatusSeeOther)
}

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
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/contributions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func registerContributionsUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=contributions.NewStore(identity.DB)
 ac:=accounts.NewStore(identity.DB)
 finance:=transactions.NewStore(identity.DB)
 movements:=transfers.NewStore(identity.DB)
 r.Get("/contributions",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  now:=time.Now().UTC()
  obligations,e:=store.Obligations(req.Context(),actor.WorkspaceID,now)
  if e!=nil{http.Error(w,"Contributions unavailable",http.StatusServiceUnavailable);return}
  for i:=range obligations{
   obligations[i].PayKey,e=accounts.NewSetupKey()
   if e!=nil{http.Error(w,"Service unavailable",503);return}
   obligations[i].SkipKey,e=accounts.NewSetupKey()
   if e!=nil{http.Error(w,"Service unavailable",503);return}
  }
  history,e:=store.History(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Contribution history unavailable",503);return}
  categories,e:=finance.Categories(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Categories unavailable",503);return}
  wallets,e:=ac.List(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Accounts unavailable",503);return}
  expenses,e:=finance.List(req.Context(),actor.WorkspaceID,transactions.Filter{Kind:"expense"})
  if e!=nil{http.Error(w,"Expense history unavailable",503);return}
  transfersList,e:=movements.Transfers(req.Context(),actor.WorkspaceID)
  if e!=nil{http.Error(w,"Transfer history unavailable",503);return}
  key,e:=accounts.NewSetupKey()
  if e!=nil{http.Error(w,"Service unavailable",503);return}
  renderPage(w,req,http.StatusOK,views.ContributionsPage(obligations,history,categories,
   wallets,expenses,transfersList,actor.CSRFToken,key,now.Format("2006-01-02"),now.Format("2006-01"),""))
 })
 r.Post("/contributions",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,e:=store.Create(req.Context(),actor.WorkspaceID,contributions.NewObligation{
   Scheme:req.PostFormValue("scheme"),Period:req.PostFormValue("period"),
   DueOn:req.PostFormValue("due_on"),Amount:req.PostFormValue("amount"),
   ExpenseCategoryID:req.PostFormValue("expense_category_id"),
   MP2AccountID:req.PostFormValue("mp2_account_id"),
   Note:req.PostFormValue("note"),Key:req.PostFormValue("request_key"),
  })
  if e!=nil{contributionFailure(w,req,logger,e);return}
  contributionRedirect(w,req)
 })
 r.Post("/contributions/resolve",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,e:=store.Resolve(req.Context(),actor.WorkspaceID,contributions.NewResolution{
   ObligationID:req.PostFormValue("obligation_id"),Action:req.PostFormValue("action"),
   ExpenseID:req.PostFormValue("expense_id"),TransferID:req.PostFormValue("transfer_id"),
   ResolvedOn:req.PostFormValue("resolved_on"),Note:req.PostFormValue("note"),
   Key:req.PostFormValue("request_key"),
  })
  if e!=nil{contributionFailure(w,req,logger,e);return}
  contributionRedirect(w,req)
 })
}
func contributionFailure(w http.ResponseWriter,req *http.Request,logger *slog.Logger,e error){
 status:=http.StatusServiceUnavailable
 message:="Unable to record the contribution. Please retry."
 switch{
 case errors.Is(e,contributions.ErrInvalid):
  status=http.StatusUnprocessableEntity
  message="Check the PHP period, amount, category or MP2 savings account, and linked expense or transfer."
 case errors.Is(e,contributions.ErrNotFound):
  status=http.StatusNotFound
  message="The contribution, category or payment is not available."
 case errors.Is(e,contributions.ErrResolved),errors.Is(e,contributions.ErrConflict):
  status=http.StatusConflict
  message="This period or payment is already recorded, or this request key was used for different details."
 default:
  logger.Error("contribution_write_failed","kind","persistence")
 }
 if hasHTMX(req){renderPage(w,req,http.StatusOK,views.ContributionFeedback(message));return}
 http.Error(w,message,status)
}
func contributionRedirect(w http.ResponseWriter,req *http.Request){
 if hasHTMX(req){
  w.Header().Set("HX-Redirect","/contributions")
  w.WriteHeader(http.StatusNoContent)
  return
 }
 http.Redirect(w,req,"/contributions",http.StatusSeeOther)
}

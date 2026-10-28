package app

import (
 "errors"
 "log/slog"
 "net/http"
 "strconv"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/config"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/debts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)
func registerDebtsUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=debts.NewStore(identity.DB)
 accountStore:=accounts.NewStore(identity.DB)
 r.Get("/debts",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  plans,err:=store.Plans(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Loan plans temporarily unavailable",503);return}
  installments,err:=store.Installments(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Installments temporarily unavailable",503);return}
  for k:=range installments{
   installments[k].PayKey,err=accounts.NewSetupKey()
   if err!=nil{http.Error(w,"Service unavailable",503);return}
  }
  history,err:=store.Payments(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Payment history unavailable",503);return}
  wallets,err:=accountStore.List(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Accounts unavailable",503);return}
  key,err:=accounts.NewSetupKey()
  if err!=nil{http.Error(w,"Service unavailable",503);return}
  renderPage(w,req,http.StatusOK,views.DebtsPage(plans,installments,history,wallets,
   actor.CSRFToken,key,time.Now().UTC().Format("2006-01-02"),""))
 })
 r.Post("/debts",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  months,err:=strconv.Atoi(req.PostFormValue("months"))
  if err!=nil{debtFailure(w,req,logger,debts.ErrInvalid);return}
  _,err=store.Create(req.Context(),actor.WorkspaceID,debts.NewPlan{
   Name:req.PostFormValue("name"),LoanAccountID:req.PostFormValue("loan_account_id"),
   FundingAccountID:req.PostFormValue("funding_account_id"),APR:req.PostFormValue("apr"),
   FirstDue:req.PostFormValue("first_due_on"),Key:req.PostFormValue("request_key"),Months:months,
  })
  if err!=nil{debtFailure(w,req,logger,err);return}
  debtRedirect(w,req)
 })
 r.Post("/debts/payments",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,err:=store.Pay(req.Context(),actor.WorkspaceID,debts.PayInput{
   PlanID:req.PostFormValue("plan_id"),InstallmentID:req.PostFormValue("installment_id"),
   PaidOn:req.PostFormValue("paid_on"),Key:req.PostFormValue("request_key"),
  })
  if err!=nil{debtFailure(w,req,logger,err);return}
  debtRedirect(w,req)
 })
}
func debtFailure(w http.ResponseWriter,req *http.Request,logger *slog.Logger,err error){
 status:=http.StatusServiceUnavailable
 message:="Unable to save loan or payment. Please retry."
 switch{
 case errors.Is(err,debts.ErrInvalid):
  status=http.StatusUnprocessableEntity
  message="Check loan, funding account, interest rate, due date and installment order."
 case errors.Is(err,debts.ErrNotFound):
  status=http.StatusNotFound
  message="The selected loan or installment is unavailable."
 case errors.Is(err,debts.ErrConflict),errors.Is(err,debts.ErrRecorded):
  status=http.StatusConflict
  message="This payment, loan or request key has already been recorded."
 case errors.Is(err,debts.ErrFunds):
  status=http.StatusUnprocessableEntity
  message="Insufficient funding account balance for the full installment."
 case errors.Is(err,debts.ErrBalance):
  status=http.StatusConflict
  message="Loan balance differs from the plan; reconcile before paying."
 case errors.Is(err,debts.ErrOverflow):
  status=http.StatusUnprocessableEntity
  message="The scheduled amount exceeds the supported limit."
 default:
  logger.Error("debt_write_failed","cause","persistence")
 }
 if hasHTMX(req){renderPage(w,req,http.StatusOK,views.DebtsFeedback(message));return}
 http.Error(w,message,status)
}
func debtRedirect(w http.ResponseWriter,req *http.Request){
 if hasHTMX(req){w.Header().Set("HX-Redirect","/debts");w.WriteHeader(http.StatusNoContent);return}
 http.Redirect(w,req,"/debts",http.StatusSeeOther)
}

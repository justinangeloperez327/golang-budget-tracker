package app

import (
 "errors"
 "log/slog"
 "net/http"
 "strconv"
 "strings"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/config"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func transactionFilter(r *http.Request)(transactions.Filter,error){
 query:=r.URL.Query()
 page:=0
 if query.Has("page"){
  p,e:=strconv.Atoi(query.Get("page"))
  if e!=nil{return transactions.Filter{},transactions.ErrInvalid}
  page=p
 }
 return transactions.ValidateFilter(transactions.Filter{
  Kind:query.Get("kind"),AccountID:query.Get("account"),Query:query.Get("q"),
  From:query.Get("from"),To:query.Get("to"),Page:page,
 })
}

func registerTransactionsUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=transactions.NewStore(identity.DB)
 wallets:=accounts.NewStore(identity.DB)
 show:=func(w http.ResponseWriter,req *http.Request,message string,status int){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  filter,err:=transactionFilter(req)
  if err!=nil{http.Error(w,"Invalid search filters",http.StatusBadRequest);return}
  history,err:=store.List(req.Context(),actor.WorkspaceID,filter)
  if err!=nil{http.Error(w,"Transactions temporarily unavailable",http.StatusServiceUnavailable);return}
  if hasHTMX(req){
   renderPage(w,req,http.StatusOK,views.TransactionsList(history,filter))
   return
  }
  categories,err:=store.Categories(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Categories temporarily unavailable",http.StatusServiceUnavailable);return}
  accountList,err:=wallets.List(req.Context(),actor.WorkspaceID)
  if err!=nil{http.Error(w,"Accounts temporarily unavailable",http.StatusServiceUnavailable);return}
  expenses,err:=store.List(req.Context(),actor.WorkspaceID,transactions.Filter{Kind:"expense"})
  if err!=nil{http.Error(w,"Transactions temporarily unavailable",http.StatusServiceUnavailable);return}
  key,err:=accounts.NewSetupKey()
  if err!=nil{http.Error(w,"Service temporarily unavailable",http.StatusServiceUnavailable);return}
  renderPage(w,req,status,views.TransactionsPage(accountList,categories,history,expenses,filter,actor.CSRFToken,key,time.Now().UTC().Format("2006-01-02"),message))
 }
 r.Get("/transactions",func(w http.ResponseWriter,req *http.Request){show(w,req,"",http.StatusOK)})
 r.Post("/transactions",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  ids:=req.PostForm["category_id"]
  amounts:=req.PostForm["split_amount"]
  input:=transactions.Input{
   Kind:req.PostFormValue("kind"),AccountID:req.PostFormValue("account_id"),
   RefundOf:req.PostFormValue("refund_of"),Note:req.PostFormValue("note"),
   OccurredOn:req.PostFormValue("occurred_on"),Key:req.PostFormValue("request_key"),
  }
  if len(ids)!=len(amounts)||len(ids)>8{
   transactionError(w,req,"Provide matching category and amount fields.",http.StatusUnprocessableEntity)
   return
  }
  for i:=range ids{
   category:=strings.TrimSpace(ids[i])
   amount:=strings.TrimSpace(amounts[i])
   if category==""&&amount==""{continue}
   input.Splits=append(input.Splits,transactions.SplitInput{CategoryID:category,Amount:amount})
  }
  _,err:=store.Create(req.Context(),actor.WorkspaceID,input)
  if err!=nil{
   status:=http.StatusServiceUnavailable
   msg:="Unable to save transaction. Please retry."
   switch{
   case errors.Is(err,transactions.ErrInvalid):
    status=http.StatusUnprocessableEntity
    msg="Check account, category currency, date, amount and splits."
   case errors.Is(err,transactions.ErrNotFound):
    status=http.StatusNotFound
    msg="The selected account, category or expense is unavailable."
   case errors.Is(err,transactions.ErrRefundLimit):
    status=http.StatusUnprocessableEntity
    msg="Refund exceeds the amount remaining for a category."
   case errors.Is(err,transactions.ErrConflict):
    status=http.StatusConflict
    msg="This request key was already used for different details. Reload and retry."
   default:
    logger.Error("finance_transaction_failure","kind","persistence")
   }
   transactionError(w,req,msg,status)
   return
  }
  transactionRedirect(w,req)
 })
 r.Post("/categories",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  _,err:=store.CreateCategory(req.Context(),actor.WorkspaceID,
   req.PostFormValue("kind"),req.PostFormValue("currency"),req.PostFormValue("name"))
  if err!=nil{
   if errors.Is(err,transactions.ErrInvalid){
    http.Error(w,"Invalid category",http.StatusUnprocessableEntity)
   }else if errors.Is(err,transactions.ErrConflict){
    http.Error(w,"Category already exists",http.StatusConflict)
   }else{
    logger.Error("finance_category_failure","kind","persistence")
    http.Error(w,"Unable to save category",http.StatusServiceUnavailable)
   }
   return
  }
  transactionRedirect(w,req)
 })
 r.Post("/categories/{id}/archive",func(w http.ResponseWriter,req *http.Request){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if !accountFormAuthorized(w,req,actor,cfg){return}
  err:=store.ArchiveCategory(req.Context(),actor.WorkspaceID,chi.URLParam(req,"id"))
  if err!=nil{
   if errors.Is(err,transactions.ErrNotFound){
    http.Error(w,"Category not found",http.StatusNotFound)
   }else{
    http.Error(w,"Unable to archive category",http.StatusServiceUnavailable)
   }
   return
  }
  transactionRedirect(w,req)
 })
}
func transactionError(w http.ResponseWriter,req *http.Request,message string,status int){
 if hasHTMX(req){
  renderPage(w,req,http.StatusOK,views.TransactionsFeedback(message))
  return
 }
 http.Error(w,message,status)
}
func transactionRedirect(w http.ResponseWriter,req *http.Request){
 if hasHTMX(req){
  w.Header().Set("HX-Redirect","/transactions")
  w.WriteHeader(http.StatusNoContent)
  return
 }
 http.Redirect(w,req,"/transactions",http.StatusSeeOther)
}

package app

import (
 "errors"
 "log/slog"
 "net/http"
 "net/url"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/config"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/budgets"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)

func budgetURL(month,currency string)string{
 query:=url.Values{"month":{month},"currency":{currency}}
 return "/budgets?"+query.Encode()
}
func registerBudgetsUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=budgets.NewStore(identity.DB)
 show:=func(w http.ResponseWriter,req *http.Request,month,currency,message string,status int){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  if _,err:=budgets.ValidateMonth(month);err!=nil||!budgets.ValidCurrency(currency){
   http.Error(w,"Invalid budget month or currency",http.StatusBadRequest)
   return
  }
  overview,categories,err:=store.Overview(req.Context(),actor.WorkspaceID,month,currency)
  if err!=nil{
   logger.Error("budget_read_failed","cause","persistence")
   http.Error(w,"Budget data temporarily unavailable",http.StatusServiceUnavailable)
   return
  }
  allocationKey,err:=accounts.NewSetupKey()
  if err!=nil{http.Error(w,"Service temporarily unavailable",503);return}
  policyKey,err:=accounts.NewSetupKey()
  if err!=nil{http.Error(w,"Service temporarily unavailable",503);return}
  if hasHTMX(req){
   renderPage(w,req,status,views.BudgetMain(overview,categories,actor.CSRFToken,
    allocationKey,policyKey,message))
   return
  }
  renderPage(w,req,status,views.BudgetsPage(overview,categories,actor.CSRFToken,
   allocationKey,policyKey,message))
 }
 r.Get("/budgets",func(w http.ResponseWriter,req *http.Request){
  month:=req.URL.Query().Get("month")
  if month==""{month=time.Now().UTC().Format("2006-01")}
  currency:=req.URL.Query().Get("currency")
  if currency==""{currency="AED"}
  show(w,req,month,currency,"",http.StatusOK)
 })
 for _,item:=range []struct{path,action string}{
  {"/budgets/allocations","allocation"},
  {"/budgets/policy","policy"},
 }{
  path,action:=item.path,item.action
  r.Post(path,func(w http.ResponseWriter,req *http.Request){
   actor,ok:=currentActor(w,req,identity,cfg)
   if !ok{return}
   if !accountFormAuthorized(w,req,actor,cfg){return}
   input:=budgets.Update{
    Month:req.PostFormValue("month"),Currency:req.PostFormValue("currency"),
    Action:action,CategoryID:req.PostFormValue("category_id"),
    Amount:req.PostFormValue("amount"),Policy:req.PostFormValue("policy"),
    Key:req.PostFormValue("request_key"),
   }
   _,err:=store.Set(req.Context(),actor.WorkspaceID,input)
   if err!=nil{
    status:=http.StatusServiceUnavailable
    message:="Unable to save budget. Please retry."
    switch {
    case errors.Is(err,budgets.ErrInvalid):
     status=http.StatusUnprocessableEntity
     message="Check the month, currency, expense category, amount and rollover option."
    case errors.Is(err,budgets.ErrNotFound):
     status=http.StatusNotFound
     message="The selected expense category is unavailable."
    case errors.Is(err,budgets.ErrConflict):
     status=http.StatusConflict
     message="This request key was already used for different budget details. Reload the page."
    default:
     logger.Error("budget_write_failed","cause","persistence")
    }
    if hasHTMX(req){
     renderPage(w,req,http.StatusOK,views.BudgetFeedback(message))
     return
    }
    if _,e:=budgets.ValidateMonth(input.Month);e==nil&&budgets.ValidCurrency(input.Currency){
     show(w,req,input.Month,input.Currency,message,status)
    }else{
     http.Error(w,message,status)
    }
    return
   }
   location:=budgetURL(input.Month,input.Currency)
   if hasHTMX(req){
    w.Header().Set("HX-Redirect",location)
    w.WriteHeader(http.StatusNoContent)
    return
   }
   http.Redirect(w,req,location,http.StatusSeeOther)
  })
 }
}

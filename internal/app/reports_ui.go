package app

import (
 "fmt"
 "log/slog"
 "net/http"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/config"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/reports"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/views"
)
func registerReportsUI(r chi.Router,identity *auth.Store,cfg config.Auth,logger *slog.Logger){
 store:=reports.NewStore(identity.DB)
 handler:=func(w http.ResponseWriter,req *http.Request,mode string){
  actor,ok:=currentActor(w,req,identity,cfg)
  if !ok{return}
  period:=req.URL.Query().Get("period")
  currency:=req.URL.Query().Get("currency")
  if period==""{period=time.Now().UTC().Format("2006-01")}
  if currency==""{currency="AED"}
  query:=reports.Query{Period:period,Currency:currency}
  if _,err:=reports.Parse(query);err!=nil{
   http.Error(w,"Invalid report period or currency",http.StatusBadRequest)
   return
  }
  report,err:=store.Load(req.Context(),actor.WorkspaceID,query)
  if err!=nil{
   logger.Error("financial_report_read_failed","category","report")
   http.Error(w,"Financial report temporarily unavailable",http.StatusServiceUnavailable)
   return
  }
  w.Header().Set("Cache-Control","no-store")
  switch mode{
  case "csv","pdf":
   var data []byte
   if mode=="csv"{data,err=reports.Csv(report)}else{data,err=reports.Pdf(report)}
   if err!=nil{
    http.Error(w,"Unable to prepare report export",http.StatusServiceUnavailable)
    return
   }
   kind:="text/csv; charset=utf-8"
   if mode=="pdf"{kind="application/pdf"}
   w.Header().Set("Content-Type",kind)
   w.Header().Set("X-Content-Type-Options","nosniff")
   w.Header().Set("Content-Disposition",fmt.Sprintf("attachment; filename=\"budget-report-%s-%s.%s\"",currency,period,mode))
   w.Header().Set("Content-Length",fmt.Sprintf("%d",len(data)))
   _,_ = w.Write(data)
  case "summary":
   renderPage(w,req,http.StatusOK,views.ReportsSummary(report))
  default:
   renderPage(w,req,http.StatusOK,views.ReportsPage(actor.Email,actor.CSRFToken,report))
  }
 }
 r.Get("/reports",func(w http.ResponseWriter,req *http.Request){handler(w,req,"page")})
 r.Get("/reports/summary",func(w http.ResponseWriter,req *http.Request){handler(w,req,"summary")})
 r.Get("/reports/export.csv",func(w http.ResponseWriter,req *http.Request){handler(w,req,"csv")})
 r.Get("/reports/export.pdf",func(w http.ResponseWriter,req *http.Request){handler(w,req,"pdf")})
}

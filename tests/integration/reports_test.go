//go:build integration

package integration

import (
 "bytes"
 "context"
 "encoding/csv"
 "fmt"
 "net/http"
 "strings"
 "testing"
 "time"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/budgets"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/reports"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)
func TestReportsMoneyCurrencyCategoriesAndBalanceHistory(t *testing.T){
 ctx:=context.Background()
 _,_,ac,ws,_:=ledgerFixture(t)
 db:=openDB(t)
 finance:=transactions.NewStore(db.Pool())
 now:=time.Now().UTC()
 month:=now.Format("2006-01")
 year:=now.Format("2006")
 day:=now.Format("2006-01-02")
 bank:=newAccount(t,ctx,ac,ws,"AED payroll bank","bank","AED","100.00")
 vault:=newAccount(t,ctx,ac,ws,"AED vault","savings","AED","0.00")
 phpBank:=newAccount(t,ctx,ac,ws,"PHP family account","bank","PHP","250.00")
 if phpBank==""{t.Fatal("missing PHP bank")}
 incomeCategory:=financeCategory(t,finance,ws,"income","AED","Salary")
 expenseCategory:=financeCategory(t,finance,ws,"expense","AED","Food")
 _=financeCategory(t,finance,ws,"expense","PHP","PH groceries")
 if _,e:=finance.Create(ctx,ws,financeInput(t,"income",bank,day,
  []transactions.SplitInput{{CategoryID:incomeCategory,Amount:"60.00"}}));e!=nil{t.Fatal(e)}
 expense,e:=finance.Create(ctx,ws,financeInput(t,"expense",bank,day,
  []transactions.SplitInput{{CategoryID:expenseCategory,Amount:"20.00"}}))
 if e!=nil{t.Fatal(e)}
 refund:=financeInput(t,"refund",bank,day,[]transactions.SplitInput{{CategoryID:expenseCategory,Amount:"5.00"}})
 refund.RefundOf=expense
 if _,e:=finance.Create(ctx,ws,refund);e!=nil{t.Fatal(e)}
 transfer:=transferInput(t,bank,vault,"10.00")
 transfer.Date=day;transfer.Fee="1.00"
 if _,e:=transfers.NewStore(db.Pool()).Transfer(ctx,ws,transfer);e!=nil{t.Fatal(e)}
 if _,e:=budgets.NewStore(db.Pool()).Set(ctx,ws,budgets.Update{
  Month:month,Currency:"AED",Action:"allocation",CategoryID:expenseCategory,
  Amount:"50.00",Key:financeKey(t),
 });e!=nil{t.Fatal(e)}
 store:=reports.NewStore(db.Pool())
 report,e:=store.Load(ctx,ws,reports.Query{Period:month,Currency:"AED"})
 if e!=nil{t.Fatal(e)}
 if report.IncomeMinor!=6000||report.ExpensesMinor!=2000||
  report.RefundMinor!=500||report.NetSpendingMinor!=1500||
  report.FeesMinor!=100||report.OperatingNetMinor!=4400{
  t.Fatalf("report aggregates incorrect %+v",report)
 }
 if report.OpeningNetWorthMinor!=0||report.ClosingAssetsMinor!=14400||
  report.ClosingNetWorthMinor!=14400{
  t.Fatalf("journal balance history incorrect %+v",report)
 }
 if len(report.Accounts)!=2||len(report.Categories)!=2{
  t.Fatalf("missing category/account grouping %+v",report)
 }
 var food *reports.Category
 for i:=range report.Categories{if report.Categories[i].Name=="Food"{food=&report.Categories[i]}}
 if food==nil||food.GrossMinor!=2000||food.RefundMinor!=500||food.NetMinor!=1500{
  t.Fatalf("refund-aware category %+v",food)
 }
 var bankRow *reports.AccountMovement
 for i:=range report.Accounts{if report.Accounts[i].ID==bank{bankRow=&report.Accounts[i]}}
 if bankRow==nil||bankRow.DebitsMinor!=16500||bankRow.CreditsMinor!=3100||
  bankRow.ClosingMinor!=13400{
  t.Fatalf("cash account movement %+v",bankRow)
 }
 if !report.BudgetConfigured||report.BudgetPlannedMinor!=5000||
  report.BudgetActualMinor!=1500||report.BudgetRemainingMinor!=3500{
  t.Fatalf("budget vs actual incorrect %+v",report)
 }
 if len(report.Monthly)!=1||report.Monthly[0].NetMinor!=4400{
  t.Fatalf("monthly comparison incorrect %+v",report.Monthly)
 }
 php,e:=store.Load(ctx,ws,reports.Query{Period:month,Currency:"PHP"})
 if e!=nil{t.Fatal(e)}
 if php.ClosingAssetsMinor!=25000||php.ClosingNetWorthMinor!=25000||
  php.IncomeMinor!=0||php.NetSpendingMinor!=0||php.FeesMinor!=0{
  t.Fatalf("cross-currency leakage %+v",php)
 }
 annual,e:=store.Load(ctx,ws,reports.Query{Period:year,Currency:"AED"})
 if e!=nil{t.Fatal(e)}
 if len(annual.Monthly)!=12||annual.IncomeMinor!=6000||
  annual.NetSpendingMinor!=1500||annual.BudgetPlannedMinor!=5000||
  annual.ClosingNetWorthMinor!=14400{
  t.Fatalf("annual rollup incorrect %+v",annual)
 }
 var total int64
 for _,m:=range annual.Monthly{total+=m.NetMinor}
 if total!=annual.OperatingNetMinor{t.Fatalf("monthly vs annual drift %d != %d",total,annual.OperatingNetMinor)}
 // Past reporting periods do not silently substitute today's balances.
 earlier:=fmt.Sprintf("%04d",now.Year()-1)
 historic,e:=store.Load(ctx,ws,reports.Query{Period:earlier,Currency:"AED"})
 if e!=nil{t.Fatal(e)}
 if historic.ClosingNetWorthMinor!=0||historic.IncomeMinor!=0{
  t.Fatalf("historic journal snapshot leaked future activity %+v",historic)
 }
}
func TestReportsAuthenticatedCSVPDFAndHTMX(t *testing.T){
 handler:=uiFixture(t)
 now:=time.Now().UTC()
 month:=now.Format("2006-01")
 actor,cookie:=registration(t,handler,fmt.Sprintf("report17-%d@example.test",time.Now().UnixNano()))
 _,otherCookie:=registration(t,handler,fmt.Sprintf("report17-other-%d@example.test",time.Now().UnixNano()))
 db:=openDB(t)
 ac:=accounts.NewStore(db.Pool())
 _=newAccount(t,context.Background(),ac,actor.WorkspaceID,"Priv@te_(report)","bank","AED","123.45")
 route:="/reports?period="+month+"&currency=AED"
 page:=formRequest(handler,"GET",route,nil,cookie,false,"")
 if page.Code!=http.StatusOK||!strings.Contains(page.Body.String(),"Reports &amp; exports")||
  !strings.Contains(page.Body.String(),"Priv@te_(report)")||
  !strings.Contains(page.Body.String(),"Download CSV"){
  t.Fatalf("report page %d %s",page.Code,page.Body.String())
 }
 partial:=formRequest(handler,"GET","/reports/summary?period="+month+"&currency=AED",nil,cookie,true,"")
 if partial.Code!=200||!strings.Contains(partial.Body.String(),"report-summary")||
  strings.Contains(partial.Body.String(),"<html"){
  t.Fatalf("HTMX partial %d %s",partial.Code,partial.Body.String())
 }
 csvRec:=formRequest(handler,"GET","/reports/export.csv?period="+month+"&currency=AED",nil,cookie,false,"")
 if csvRec.Code!=200||!strings.HasPrefix(csvRec.Header().Get("Content-Type"),"text/csv")||
  !strings.Contains(csvRec.Header().Get("Content-Disposition"),"attachment")||
  csvRec.Header().Get("Cache-Control")!="no-store"{
  t.Fatalf("CSV headers %d %+v",csvRec.Code,csvRec.Header())
 }
 csvRows,e:=csv.NewReader(bytes.NewReader(csvRec.Body.Bytes())).ReadAll()
 if e!=nil{t.Fatal(e)}
 var found bool
 for _,row:=range csvRows{if len(row)>0&&row[0]=="Priv@te_(report)"{found=true}}
 if !found{t.Fatal("PDF/CSV missing account-specific detail")}
 pdfRec:=formRequest(handler,"GET","/reports/export.pdf?period="+month+"&currency=AED",nil,cookie,false,"")
 if pdfRec.Code!=200||pdfRec.Header().Get("Content-Type")!="application/pdf"||
  !bytes.HasPrefix(pdfRec.Body.Bytes(),[]byte("%PDF-1.4"))||
  !bytes.Contains(pdfRec.Body.Bytes(),[]byte("Priv@te_\\(report\\)"))||
  !bytes.Contains(pdfRec.Body.Bytes(),[]byte("%%EOF")){
  t.Fatalf("PDF response malformed %d %s",pdfRec.Code,pdfRec.Body.String()[:min(150,pdfRec.Body.Len())])
 }
 for _,path:=range []string{
  "/reports?period=2026-13&currency=AED",
  "/reports/export.csv?period=2026-10&currency=USD",
  "/reports/export.pdf?period=1999&currency=PHP",
 }{
  bad:=formRequest(handler,"GET",path,nil,cookie,false,"")
  if bad.Code!=400{t.Fatalf("invalid filter accepted %s => %d",path,bad.Code)}
 }
 for _,path:=range []string{route,"/reports/summary?period="+month+"&currency=AED",
  "/reports/export.csv?period="+month+"&currency=AED",
  "/reports/export.pdf?period="+month+"&currency=AED"}{
  unauthorized:=formRequest(handler,"GET",path,nil,nil,false,"")
  if unauthorized.Code!=303{t.Fatalf("anonymous report accepted %s => %d",path,unauthorized.Code)}
 }
 other:=formRequest(handler,"GET",route,nil,otherCookie,false,"")
 if other.Code!=200||strings.Contains(other.Body.String(),"Priv@te_(report)"){
  t.Fatalf("cross-workspace report leak: %d",other.Code)
 }
 otherExport:=formRequest(handler,"GET","/reports/export.csv?period="+month+"&currency=AED",nil,otherCookie,false,"")
 if otherExport.Code!=200||strings.Contains(otherExport.Body.String(),"Priv@te_(report)"){
  t.Fatalf("cross-workspace export leak: %d",otherExport.Code)
 }
}

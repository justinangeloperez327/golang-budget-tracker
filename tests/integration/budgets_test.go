//go:build integration

package integration

import (
 "context"
 "errors"
 "fmt"
 "net/url"
 "strings"
 "sync"
 "testing"
 "time"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/budgets"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

func budgetFixture(t *testing.T)(*budgets.Store,*transactions.Store,*accounts.Store,string){
 t.Helper()
 _,_,_,ws,_:=ledgerFixture(t)
 db:=openDB(t)
 return budgets.NewStore(db.Pool()),transactions.NewStore(db.Pool()),
  accounts.NewStore(db.Pool()),ws
}
func budgetKey(t *testing.T)string{
 t.Helper()
 key,err:=accounts.NewSetupKey()
 if err!=nil{t.Fatal(err)}
 return key
}
func budgetAllocate(t *testing.T,s *budgets.Store,ws,month,currency,category,amount string)string{
 t.Helper()
 id,err:=s.Set(context.Background(),ws,budgets.Update{
  Month:month,Currency:currency,Action:"allocation",CategoryID:category,
  Amount:amount,Key:budgetKey(t),
 })
 if err!=nil{t.Fatal(err)}
 return id
}
func budgetPolicy(t *testing.T,s *budgets.Store,ws,month,currency,policy string){
 t.Helper()
 _,err:=s.Set(context.Background(),ws,budgets.Update{
  Month:month,Currency:currency,Action:"policy",Policy:policy,Key:budgetKey(t),
 })
 if err!=nil{t.Fatal(err)}
}
func TestMonthlyBudgetActualsRefundsCarryAndOverspending(t *testing.T){
 b,finance,accountStore,workspace:=budgetFixture(t)
 ctx:=context.Background()
 bank:=newAccount(t,ctx,accountStore,workspace,"AED bank","bank","AED","500.00")
 savings:=newAccount(t,ctx,accountStore,workspace,"Savings","savings","AED","0")
 card:=newAccount(t,ctx,accountStore,workspace,"Credit","credit","AED","0")
 category:=financeCategory(t,finance,workspace,"expense","AED","Groceries")
 otherCategory:=financeCategory(t,finance,workspace,"expense","AED","Transport")
 phpCategory:=financeCategory(t,finance,workspace,"expense","PHP","Food")
 budgetAllocate(t,b,workspace,"2026-09","AED",category,"100.00")
 budgetPolicy(t,b,workspace,"2026-10","AED","carry_positive")
 budgetAllocate(t,b,workspace,"2026-10","AED",category,"50.00")
 budgetAllocate(t,b,workspace,"2026-11","AED",category,"30.00")
 budgetPolicy(t,b,workspace,"2026-11","AED","carry_positive")
 sep:=financeInput(t,"expense",bank,"2026-09-24",[]transactions.SplitInput{
  {CategoryID:category,Amount:"40"},
 })
 if _,err:=finance.Create(ctx,workspace,sep);err!=nil{t.Fatal(err)}
 oct:=financeInput(t,"expense",card,"2026-10-04",[]transactions.SplitInput{
  {CategoryID:category,Amount:"90"},
 })
 original,err:=finance.Create(ctx,workspace,oct)
 if err!=nil{t.Fatal(err)}
 refund:=financeInput(t,"refund",card,"2026-10-20",[]transactions.SplitInput{
  {CategoryID:category,Amount:"20"},
 })
 refund.RefundOf=original
 if _,err:=finance.Create(ctx,workspace,refund);err!=nil{t.Fatal(err)}
 november:=financeInput(t,"expense",bank,"2026-11-03",[]transactions.SplitInput{
  {CategoryID:category,Amount:"90"},
 })
 if _,err:=finance.Create(ctx,workspace,november);err!=nil{t.Fatal(err)}
 movement:=transferInput(t,bank,savings,"25")
 movement.Date="2026-10-26"
 db:=openDB(t)
 if _,err:=transfers.NewStore(db.Pool()).Transfer(ctx,workspace,movement);err!=nil{t.Fatal(err)}
 o,cats,err:=b.Overview(ctx,workspace,"2026-10","AED")
 if err!=nil{t.Fatal(err)}
 if len(cats)!=2||!o.Configured||o.Policy!="carry_positive"||o.CarriedMinor!=6000||
  o.PlannedMinor!=5000||o.ActualMinor!=7000||o.RemainingMinor!=4000||
  o.Overspent{
  t.Fatalf("october budget incorrect: %+v categories=%+v",o,cats)
 }
 o,_,err=b.Overview(ctx,workspace,"2026-11","AED")
 if err!=nil||o.CarriedMinor!=4000||o.ActualMinor!=9000||
  o.RemainingMinor!=-2000||!o.Overspent{
  t.Fatalf("november overspend incorrect %+v %v",o,err)
 }
 p,_,err:=b.Overview(ctx,workspace,"2026-10","PHP")
 if err!=nil||p.PlannedMinor!=0||p.ActualMinor!=0||p.RemainingMinor!=0{
  t.Fatalf("AED spending leaked into PHP: %+v %v",p,err)
 }
 if _,err:=b.Set(ctx,workspace,budgets.Update{
  Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:phpCategory,
  Amount:"10",Key:budgetKey(t),
 });!errors.Is(err,budgets.ErrInvalid){
  t.Fatalf("foreign-currency budget accepted: %v",err)
 }
 if _,err:=b.Set(ctx,workspace,budgets.Update{
  Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:otherCategory,
  Amount:"20",Key:budgetKey(t),
 });err!=nil{t.Fatal(err)}
 o,_,err=b.Overview(ctx,workspace,"2026-10","AED")
 if err!=nil||o.PlannedMinor!=7000||o.RemainingMinor!=6000{
  t.Fatalf("allocation revision not reflected %+v %v",o,err)
 }
}

func TestBudgetIdempotencyAuditAndWorkspaceIsolation(t *testing.T){
 b,finance,_,ws:=budgetFixture(t)
 ctx:=context.Background()
 cat:=financeCategory(t,finance,ws,"expense","AED","Food")
 key:=budgetKey(t)
 input:=budgets.Update{Month:"2026-10",Currency:"AED",Action:"allocation",
  CategoryID:cat,Amount:"200.00",Key:key}
 var wg sync.WaitGroup
 ids:=make(chan string,8)
 errs:=make(chan error,8)
 for i:=0;i<8;i++{
  wg.Add(1)
  go func(){
   defer wg.Done()
   id,err:=b.Set(context.Background(),ws,input)
   ids<-id;errs<-err
  }()
 }
 wg.Wait();close(ids);close(errs)
 first:=""
 for err:=range errs{if err!=nil{t.Fatal(err)}}
 for id:=range ids{
  if first==""{first=id}
  if id!=first{t.Fatalf("different IDs on duplicate request: %s %s",first,id)}
 }
 changed:=input
 changed.Amount="201"
 if _,err:=b.Set(ctx,ws,changed);!errors.Is(err,budgets.ErrConflict){
  t.Fatalf("reused key changed amount: %v",err)
 }
 archivedKey:=budgetKey(t)
 edit:=input
 edit.Amount="250.00"
 edit.Key=archivedKey
 if _,err:=b.Set(ctx,ws,edit);err!=nil{t.Fatal(err)}
 o,_,err:=b.Overview(ctx,ws,"2026-10","AED")
 if err!=nil||o.PlannedMinor!=25000{t.Fatalf("latest revision not applied %+v %v",o,err)}
 db:=openDB(t)
 var count int
 if err:=db.Pool().QueryRow(ctx,`SELECT COUNT(*) FROM monthly_budget_events WHERE workspace_id=$1`,ws).Scan(&count);
  err!=nil||count!=2{t.Fatalf("idempotent event count %d %v",count,err)}
 if _,err:=db.Pool().Exec(ctx,`UPDATE monthly_budget_events SET amount_minor=1 WHERE id=$1`,first);err==nil{
  t.Fatal("immutable budget event updated")
 }
 if _,err:=db.Pool().Exec(ctx,`DELETE FROM monthly_budget_events WHERE id=$1`,first);err==nil{
  t.Fatal("immutable budget event deleted")
 }
 other,_,_,otherWS:=budgetFixture(t)
 if _,err:=other.Set(ctx,otherWS,budgets.Update{
  Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:cat,
  Amount:"100",Key:budgetKey(t),
 });!errors.Is(err,budgets.ErrNotFound){
  t.Fatalf("cross-workspace allocation accepted %v",err)
 }
 o,_,err=other.Overview(ctx,otherWS,"2026-10","AED")
 if err!=nil||o.PlannedMinor!=0||len(o.Rows)!=0{t.Fatalf("budget leaked %+v %v",o,err)}
 if err:=finance.ArchiveCategory(ctx,ws,cat);err!=nil{t.Fatal(err)}
 if _,err:=b.Set(ctx,ws,budgets.Update{
  Month:"2026-11",Currency:"AED",Action:"allocation",CategoryID:cat,
  Amount:"100",Key:budgetKey(t),
 });!errors.Is(err,budgets.ErrInvalid){
  t.Fatalf("archived category allocation accepted %v",err)
 }
}

func TestBudgetBrowserCSRFHTMXAndValidation(t *testing.T){
 handler:=uiFixture(t)
 actor,cookie:=registration(t,handler,fmt.Sprintf("budget-%d@example.test",time.Now().UnixNano()))
 ctx:=context.Background()
 db:=openDB(t)
 finance:=transactions.NewStore(db.Pool())
 cat:=financeCategory(t,finance,actor.WorkspaceID,"expense","AED","Groceries")
 page:=formRequest(handler,"GET","/budgets?month=2026-10&currency=AED",nil,cookie,false,"")
 if page.Code!=200||!strings.Contains(page.Body.String(),"Monthly budgets"){
  t.Fatalf("GET budgets %d %s",page.Code,page.Body.String())
 }
 if rec:=formRequest(handler,"GET","/budgets",nil,nil,false,"");rec.Code!=303{
  t.Fatalf("anonymous budget access %d",rec.Code)
 }
 if rec:=formRequest(handler,"GET","/budgets?month=2026-88",nil,cookie,false,"");rec.Code!=400{
  t.Fatalf("invalid month accepted %d",rec.Code)
 }
 form:=url.Values{"csrf_token":{actor.CSRFToken},"request_key":{budgetKey(t)},
  "month":{"2026-10"},"currency":{"AED"},"category_id":{cat},"amount":{"120.50"}}
 if rec:=formRequest(handler,"POST","/budgets/allocations",form,cookie,false,"");rec.Code!=403{
  t.Fatalf("missing origin accepted %d",rec.Code)
 }
 bad:=url.Values{}
 for key,value:=range form{bad[key]=append([]string{},value...)}
 bad.Set("csrf_token","invalid")
 if rec:=formRequest(handler,"POST","/budgets/allocations",bad,cookie,false,"http://localhost:8080");rec.Code!=403{
  t.Fatalf("invalid CSRF accepted %d",rec.Code)
 }
 created:=formRequest(handler,"POST","/budgets/allocations",form,cookie,true,"http://localhost:8080")
 if created.Code!=204||!strings.HasPrefix(created.Header().Get("HX-Redirect"),"/budgets?"){
  t.Fatalf("HTMX allocation %d %s",created.Code,created.Body.String())
 }
 policy:=url.Values{"csrf_token":{actor.CSRFToken},"request_key":{budgetKey(t)},
  "month":{"2026-10"},"currency":{"AED"},"policy":{"carry_positive"}}
 if rec:=formRequest(handler,"POST","/budgets/policy",policy,cookie,false,"http://localhost:8080");rec.Code!=303{
  t.Fatalf("native policy POST %d %s",rec.Code,rec.Body.String())
 }
 fragment:=formRequest(handler,"GET","/budgets?month=2026-10&currency=AED",nil,cookie,true,"")
 if fragment.Code!=200||!strings.Contains(fragment.Body.String(),"AED 120.50")||
  !strings.Contains(fragment.Body.String(),"Groceries")||strings.Contains(fragment.Body.String(),"<!DOCTYPE"){
  t.Fatalf("budget fragment %d %s",fragment.Code,fragment.Body.String())
 }
 invalid:=url.Values{"csrf_token":{actor.CSRFToken},"request_key":{budgetKey(t)},
  "month":{"2026-10"},"currency":{"AED"},"category_id":{cat},"amount":{"-10"}}
 rec:=formRequest(handler,"POST","/budgets/allocations",invalid,cookie,true,"http://localhost:8080")
 if rec.Code!=200||!strings.Contains(rec.Body.String(),"budget-feedback"){
  t.Fatalf("invalid amount feedback %d %s",rec.Code,rec.Body.String())
 }
 s:=budgets.NewStore(db.Pool())
 overview,_,err:=s.Overview(ctx,actor.WorkspaceID,"2026-10","AED")
 if err!=nil||overview.PlannedMinor!=12050||overview.Policy!="carry_positive"{
  t.Fatalf("persisted budget %+v %v",overview,err)
 }
}

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
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/remittances"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

func remittanceFixture(t *testing.T)(*remittances.Store,*accounts.Store,*ledger.Store,string){
 t.Helper()
 _,_,_,ws,_:=ledgerFixture(t)
 db:=openDB(t)
 return remittances.NewStore(db.Pool()),accounts.NewStore(db.Pool()),ledger.NewStore(db.Pool()),ws
}
func remittanceKey(t *testing.T)string{
 t.Helper()
 k,e:=accounts.NewSetupKey()
 if e!=nil{t.Fatal(e)}
 return k
}
func remittanceDraft(t *testing.T,source,destination string)remittances.CreateInput{
 t.Helper()
 return remittances.CreateInput{
  SourceAccountID:source,DestinationAccountID:destination,
  Send:"1000.00",Fee:"5.00",Rate:"15.50000000",Receive:"15500.00",
  RequestedOn:"2026-10-28",Note:"Family transfer",Key:remittanceKey(t),
 }
}
func TestRemittanceSettleOnceAndAccounting(t *testing.T){
 s,a,l,ws:=remittanceFixture(t)
 ctx:=context.Background()
 from:=newAccount(t,ctx,a,ws,"UAE Bank","bank","AED","5000.00")
 to:=newAccount(t,ctx,a,ws,"PH Bank","bank","PHP","0.00")
 input:=remittanceDraft(t,from,to)
 id,e:=s.Create(ctx,ws,input)
 if e!=nil{t.Fatal(e)}
 again,e:=s.Create(ctx,ws,input)
 if e!=nil||again!=id{t.Fatalf("pending replay %s %v",again,e)}
 adjusted:=input;adjusted.Fee="5.01"
 if _,e:=s.Create(ctx,ws,adjusted);!errors.Is(e,remittances.ErrConflict){
  t.Fatalf("different quote accepted %v",e)
 }
 prior:=balancesByAccount(t,ctx,l,ws)
 if prior[from]!=500000||prior[to]!=0{t.Fatalf("pending unexpectedly posted %+v",prior)}
 orders,e:=s.Orders(ctx,ws)
 if e!=nil||len(orders)!=1||orders[0].Status!="pending"||
  orders[0].RateUnits!=1550000000||orders[0].EffectiveRateUnits==orders[0].RateUnits{
  t.Fatalf("incorrect pending rate/state %+v %v",orders,e)
 }
 settle:=remittances.ResolveInput{OrderID:id,Action:"settled",Date:"2026-10-29",Key:remittanceKey(t)}
 const parallel=8
 ids:=make(chan string,parallel)
 errs:=make(chan error,parallel)
 var wg sync.WaitGroup
 for i:=0;i<parallel;i++{
  wg.Add(1)
  go func(){
   defer wg.Done()
   record,e:=s.Resolve(context.Background(),ws,settle)
   ids<-record;errs<-e
  }()
 }
 wg.Wait();close(ids);close(errs)
 for e:=range errs{if e!=nil{t.Fatalf("concurrent settle %v",e)}}
 expected:=""
 for r:=range ids{
  if expected==""{expected=r}
  if r!=expected{t.Fatalf("non-idempotent settlement %s != %s",r,expected)}
 }
 if expected==""{t.Fatal("settlement ID missing")}
 balances:=balancesByAccount(t,ctx,l,ws)
 if balances[from]!=399500||balances[to]!=1550000{
  t.Fatalf("settlement balances incorrect %+v",balances)
 }
 db:=openDB(t)
 var transferCount,financeCount int
 e=db.Pool().QueryRow(ctx,`SELECT COUNT(*) FROM transfer_operations WHERE workspace_id=$1`,ws).Scan(&transferCount)
 if e!=nil||transferCount!=1{t.Fatalf("Group 9 transfer counted %d %v",transferCount,e)}
 e=db.Pool().QueryRow(ctx,`SELECT COUNT(*) FROM finance_transactions WHERE workspace_id=$1`,ws).Scan(&financeCount)
 if e!=nil||financeCount!=0{t.Fatalf("double counted remittance as income/expense %d %v",financeCount,e)}
 var feeTotal int64
 e=db.Pool().QueryRow(ctx,`SELECT COALESCE(SUM(l.debit_minor-l.credit_minor),0)
  FROM ledger_lines l JOIN ledger_accounts a ON a.id=l.ledger_account_id
  WHERE l.workspace_id=$1 AND a.system_key='transfer_fee'`,ws).Scan(&feeTotal)
 if e!=nil||feeTotal!=500{t.Fatalf("fee ledger %d %v",feeTotal,e)}
 var journals int
 e=db.Pool().QueryRow(ctx,`SELECT COUNT(*) FROM ledger_journals WHERE workspace_id=$1
 AND idempotency_key IN ($2,$3)`,ws,"transfer:"+id,"transfer-target:"+id).Scan(&journals)
 if e!=nil||journals!=2{t.Fatalf("expected exactly two FX journals: %d %v",journals,e)}
 for _,currency:=range []string{"AED","PHP"}{
  var debit,credit int64
  e=db.Pool().QueryRow(ctx,`SELECT COALESCE(SUM(l.debit_minor),0),
   COALESCE(SUM(l.credit_minor),0) FROM ledger_lines l
   WHERE l.workspace_id=$1 AND l.currency=$2 AND l.journal_id IN (
    SELECT id FROM ledger_journals WHERE workspace_id=$1 AND idempotency_key IN ($3,$4))`,
   ws,currency,"transfer:"+id,"transfer-target:"+id).Scan(&debit,&credit)
  if e!=nil||debit!=credit||debit==0{t.Fatalf("unbalanced %s: %d %d %v",currency,debit,credit,e)}
 }
 orders,e=s.Orders(ctx,ws)
 if e!=nil||orders[0].Status!="settled"||orders[0].ResolvedOn!="2026-10-29"||
  orders[0].TransferID==""{t.Fatalf("missing resolution %+v %v",orders,e)}
 otherKey:=settle;otherKey.Key=remittanceKey(t)
 if _,e:=s.Resolve(ctx,ws,otherKey);!errors.Is(e,remittances.ErrResolved){
  t.Fatalf("settled twice %v",e)
 }
 conflict:=settle;conflict.Action="cancelled"
 if _,e:=s.Resolve(ctx,ws,conflict);!errors.Is(e,remittances.ErrConflict){
  t.Fatalf("conflicting action replay %v",e)
 }
 if _,e:=db.Pool().Exec(ctx,`UPDATE remittance_orders SET send_minor=1 WHERE id=$1`,id);e==nil{
  t.Fatal("remittance order mutable")
 }
 if _,e:=db.Pool().Exec(ctx,`DELETE FROM remittance_resolutions WHERE id=$1`,expected);e==nil{
  t.Fatal("remittance settlement mutable")
 }
 // Database trigger rejects falsely linking a different, unrelated transfer.
 other:=remittanceDraft(t,from,to);other.Send="10.00";other.Fee="0";other.Receive="155.00"
 next,e:=s.Create(ctx,ws,other)
 if e!=nil{t.Fatal(e)}
 if _,e:=db.Pool().Exec(ctx,`
  INSERT INTO remittance_resolutions(id,workspace_id,order_id,action,
   transfer_id,settled_on,idempotency_key,fingerprint)
  VALUES($1,$2,$3,'settled',$4,$5,$6,$7)`,
  remittanceKey(t),ws,next,orders[0].TransferID,"2026-10-29",remittanceKey(t),make([]byte,32));e==nil{
  t.Fatal("database accepted mismatched transfer")
 }
}

func TestRemittanceCancellationInsufficientFundsAndIsolation(t *testing.T){
 s,a,l,ws:=remittanceFixture(t)
 ctx:=context.Background()
 from:=newAccount(t,ctx,a,ws,"AED funding","bank","AED","20")
 to:=newAccount(t,ctx,a,ws,"PHP recipient","bank","PHP","0")
 intent:=remittanceDraft(t,from,to)
 id,e:=s.Create(ctx,ws,intent)
 if e!=nil{t.Fatal(e)}
 settle:=remittances.ResolveInput{OrderID:id,Action:"settled",Date:"2026-10-28",Key:remittanceKey(t)}
 if _,e:=s.Resolve(ctx,ws,settle);!errors.Is(e,remittances.ErrFunds){t.Fatalf("underfunded settled %v",e)}
 before:=balancesByAccount(t,ctx,l,ws)
 if before[from]!=2000||before[to]!=0{t.Fatalf("rejected settlement posted %+v",before)}
 cancel:=remittances.ResolveInput{OrderID:id,Action:"cancelled",Date:"2026-10-29",Key:remittanceKey(t)}
 first,e:=s.Resolve(ctx,ws,cancel)
 if e!=nil{t.Fatal(e)}
 again,e:=s.Resolve(ctx,ws,cancel)
 if e!=nil||again!=first{t.Fatalf("cancel replay %s %v",again,e)}
 if _,e:=s.Resolve(ctx,ws,settle);!errors.Is(e,remittances.ErrResolved){t.Fatalf("cancelled order settled %v",e)}
 if after:=balancesByAccount(t,ctx,l,ws);after[from]!=before[from]||after[to]!=before[to]{
  t.Fatalf("cancel changed balances %+v",after)
 }
 outside,otherA,_,foreignWS:=remittanceFixture(t)
 foreignAED:=newAccount(t,ctx,otherA,foreignWS,"Foreign AED","bank","AED","100")
 foreignPHP:=newAccount(t,ctx,otherA,foreignWS,"Foreign PHP","bank","PHP","0")
 bad:=remittanceDraft(t,foreignAED,to)
 if _,e:=s.Create(ctx,ws,bad);!errors.Is(e,remittances.ErrNotFound){
  t.Fatalf("foreign funding accepted %v",e)
 }
 bad=remittanceDraft(t,from,foreignPHP)
 if _,e:=s.Create(ctx,ws,bad);!errors.Is(e,remittances.ErrNotFound){
  t.Fatalf("foreign recipient accepted %v",e)
 }
 if _,e:=outside.Resolve(ctx,foreignWS,cancel);!errors.Is(e,remittances.ErrNotFound){
  t.Fatalf("foreign resolution accepted %v",e)
 }
 reverse:=remittanceDraft(t,to,from)
 if _,e:=s.Create(ctx,ws,reverse);!errors.Is(e,remittances.ErrInvalid){
  t.Fatalf("PHP->AED remittance accepted %v",e)
 }
 credit:=newAccount(t,ctx,a,ws,"Credit Card","credit","AED","0")
 bad=remittanceDraft(t,credit,to)
 if _,e:=s.Create(ctx,ws,bad);!errors.Is(e,remittances.ErrInvalid){
  t.Fatalf("credit funding accepted %v",e)
 }
}
func TestRemittanceBrowserSecurityAndHTMX(t *testing.T){
 handler:=uiFixture(t)
 actor,cookie:=registration(t,handler,fmt.Sprintf("remit-%d@example.test",time.Now().UnixNano()))
 db:=openDB(t)
 ac:=accounts.NewStore(db.Pool())
 ctx:=context.Background()
 from:=newAccount(t,ctx,ac,actor.WorkspaceID,"Bank UAE","bank","AED","1000")
 to:=newAccount(t,ctx,ac,actor.WorkspaceID,"Bank Philippines","bank","PHP","0")
 page:=formRequest(handler,"GET","/remittances",nil,cookie,false,"")
 if page.Code!=200||!strings.Contains(page.Body.String(),"AED")||!strings.Contains(page.Body.String(),"remittances"){
  t.Fatalf("page %d %s",page.Code,page.Body.String())
 }
 if unauth:=formRequest(handler,"GET","/remittances",nil,nil,false,"");unauth.Code!=303{
  t.Fatalf("anonymous GET allowed %d",unauth.Code)
 }
 form:=url.Values{
  "csrf_token":{actor.CSRFToken},"request_key":{remittanceKey(t)},
  "source_account_id":{from},"destination_account_id":{to},
  "send":{"100.00"},"fee":{"2.00"},"receive":{"1550.00"},
  "rate":{"15.50"},"requested_on":{"2026-10-28"},
 }
 if missing:=formRequest(handler,"POST","/remittances",form,cookie,false,"");missing.Code!=403{
  t.Fatalf("Origin missing %d",missing.Code)
 }
 bad:=url.Values{}
 for key,value:=range form{bad[key]=append([]string{},value...)}
 bad.Set("csrf_token","invalid")
 if rec:=formRequest(handler,"POST","/remittances",bad,cookie,false,"http://localhost:8080");rec.Code!=403{
  t.Fatalf("CSRF accepted %d",rec.Code)
 }
 created:=formRequest(handler,"POST","/remittances",form,cookie,true,"http://localhost:8080")
 if created.Code!=204||created.Header().Get("HX-Redirect")!="/remittances"{
  t.Fatalf("HTMX create %d %s",created.Code,created.Body.String())
 }
 orders,e:=remittances.NewStore(db.Pool()).Orders(ctx,actor.WorkspaceID)
 if e!=nil||len(orders)!=1||orders[0].Status!="pending"{t.Fatalf("orders %+v %v",orders,e)}
 settlement:=url.Values{
  "csrf_token":{actor.CSRFToken},"request_key":{remittanceKey(t)},
  "order_id":{orders[0].ID},"action":{"settled"},"settled_on":{"2026-10-28"},
 }
 rec:=formRequest(handler,"POST","/remittances/resolve",settlement,cookie,false,"http://localhost:8080")
 if rec.Code!=303{t.Fatalf("settlement native form %d %s",rec.Code,rec.Body.String())}
 history:=formRequest(handler,"GET","/remittances",nil,cookie,false,"")
 if history.Code!=200||!strings.Contains(history.Body.String(),"settled")||
  !strings.Contains(history.Body.String(),"1550.00"){
  t.Fatalf("history missing %d %s",history.Code,history.Body.String())
 }
 invalid:=url.Values{}
 for key,value:=range settlement{invalid[key]=append([]string{},value...)}
 invalid.Set("request_key",remittanceKey(t))
 feedback:=formRequest(handler,"POST","/remittances/resolve",invalid,cookie,true,"http://localhost:8080")
 if feedback.Code!=200||!strings.Contains(feedback.Body.String(),"remittance-feedback"){
  t.Fatalf("HTMX feedback %d %s",feedback.Code,feedback.Body.String())
 }
}

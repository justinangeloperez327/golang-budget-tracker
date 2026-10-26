package budgets

import (
 "errors"
 "testing"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestBudgetInputAndCurrency(t *testing.T){
 id,_:=accounts.NewSetupKey()
 key,_:=accounts.NewSetupKey()
 valid:=Update{Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:id,Amount:"125.50",Key:key}
 n,e:=normalize(valid)
 if e!=nil||n.AmountMinor!=12550{t.Fatalf("valid allocation %+v %v",n,e)}
 revised:=valid
 revised.Amount="125.51"
 m,_:=normalize(revised)
 if m.Hash==n.Hash{t.Fatal("amount must change idempotency fingerprint")}
 for _,bad:=range []Update{
  {Month:"2026-13",Currency:"AED",Action:"allocation",CategoryID:id,Amount:"1",Key:key},
  {Month:"2026-10",Currency:"USD",Action:"allocation",CategoryID:id,Amount:"1",Key:key},
  {Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:"bad",Amount:"1",Key:key},
  {Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:id,Amount:"-1",Key:key},
  {Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:id,Amount:"1.001",Key:key},
  {Month:"2026-10",Currency:"AED",Action:"allocation",CategoryID:id,Amount:"1",Key:"bad"},
  {Month:"2026-10",Currency:"AED",Action:"policy",Policy:"carry_negative",Key:key},
  {Month:"2026-10",Currency:"AED",Action:"policy",Policy:"none",CategoryID:id,Key:key},
  {Month:"2026-10",Currency:"AED",Action:"unknown",Key:key},
 } {
  if _,e:=normalize(bad);!errors.Is(e,ErrInvalid){t.Fatalf("accepted %+v %v",bad,e)}
 }
 policy,e:=normalize(Update{Month:"2026-10",Currency:"PHP",Action:"policy",Policy:"carry_positive",Key:key})
 if e!=nil||policy.Policy!="carry_positive"{t.Fatalf("policy %+v %v",policy,e)}
 for _,month:=range []string{"1999-12","2101-01","2026-00","2026-1","2026-10-01","invalid"} {
  if _,e:=ValidateMonth(month);!errors.Is(e,ErrInvalid){t.Fatalf("accepted month %q",month)}
 }
}

func TestPositiveRolloverAndRefunds(t *testing.T){
 id,_:=accounts.NewSetupKey()
 categories:=[]Category{{ID:id,Name:"Groceries"}}
 plans:=map[string]*monthPlan{
  "2026-09":{exists:true,policy:"none",allocations:map[string]int64{id:10000}},
  "2026-10":{exists:true,policy:"carry_positive",allocations:map[string]int64{id:5000}},
  "2026-11":{exists:true,policy:"carry_positive",allocations:map[string]int64{id:3000}},
  "2026-12":{exists:true,policy:"none",allocations:map[string]int64{id:1000}},
 }
 // September 4,000 spent => 6,000 carried into October. October
 // expense less partial refund = 7,000 net spent => 4,000 remaining.
 actual:=map[string]map[string]int64{
  "2026-09":{id:4000},
  "2026-10":{id:7000},
  "2026-11":{id:9000},
 }
 october,e:=Calculate("2026-10","AED",plans,actual,categories)
 if e!=nil||october.CarriedMinor!=6000||october.ActualMinor!=7000||
  october.RemainingMinor!=4000||october.Overspent{
  t.Fatalf("unexpected october %+v %v",october,e)
 }
 november,e:=Calculate("2026-11","AED",plans,actual,categories)
 if e!=nil||november.CarriedMinor!=4000||november.RemainingMinor != -2000||
  !november.Overspent||len(november.Rows)!=1||!november.Rows[0].Overspent{
  t.Fatalf("unexpected overspend %+v %v",november,e)
 }
 december,e:=Calculate("2026-12","AED",plans,actual,categories)
 if e!=nil||december.CarriedMinor!=0||december.RemainingMinor!=1000{
  t.Fatalf("none must reset rollover %+v %v",december,e)
 }
 // Missing October plan must break November's rollover chain.
 delete(plans,"2026-10")
 november,e=Calculate("2026-11","AED",plans,actual,categories)
 if e!=nil||november.CarriedMinor!=0{t.Fatalf("rollover crossed month gap %+v %v",november,e)}
 // Negative net actual due to a refund is distinct from income.
 plans["2026-10"]=&monthPlan{exists:true,policy:"none",allocations:map[string]int64{id:5000}}
 actual["2026-10"][id]=-1000
 october,e=Calculate("2026-10","AED",plans,actual,categories)
 if e!=nil||october.ActualMinor!=-1000||october.RemainingMinor!=6000{
  t.Fatalf("refund-aware negative actual %+v %v",october,e)
 }
 php,e:=Calculate("2026-10","PHP",map[string]*monthPlan{},map[string]map[string]int64{},categories)
 if e!=nil||php.ActualMinor!=0||php.Currency!="PHP"{t.Fatal("PHP plan should be independent")}
}
func TestOverflowAndUnbudgetedActuals(t *testing.T){
 id,_:=accounts.NewSetupKey()
 cat:=[]Category{{ID:id,Name:"Expense"}}
 o,e:=Calculate("2026-10","AED",nil,map[string]map[string]int64{"2026-10":{id:1500}},cat)
 if e!=nil||o.PlannedMinor!=0||o.RemainingMinor!=-1500||!o.Overspent{
  t.Fatalf("unbudgeted spending %+v %v",o,e)
 }
 p:=map[string]*monthPlan{"2026-10":{exists:true,allocations:map[string]int64{id:MaxMinor},policy:"none"}}
 if _,e:=Calculate("2026-10","AED",p,map[string]map[string]int64{
  "2026-10":{id:-10},
 },cat);!errors.Is(e,ErrOverflow){t.Fatalf("overflow not rejected %v",e)}
}

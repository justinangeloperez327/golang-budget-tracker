package contributions

import (
 "errors"
 "testing"
 "time"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func key(t *testing.T)string{
 t.Helper()
 v,e:=accounts.NewSetupKey()
 if e!=nil{t.Fatal(e)}
 return v
}
func TestContributionNormalization(t *testing.T){
 cat:=key(t)
 mp2:=key(t)
 k:=key(t)
 input:=NewObligation{Scheme:" SSS ",Period:"2026-10",DueOn:"2026-10-31",
  Amount:"1,000.00",ExpenseCategoryID:cat,Key:k,Note:"  Monthly  "}
 // Amounts are validated as strict decimal strings, without display separators.
 if _,e:=normalizeObligation(input);!errors.Is(e,ErrInvalid){
  t.Fatalf("accepted grouped amount %v",e)
 }
 input.Amount="1000.00"
 normalized,e:=normalizeObligation(input)
 if e!=nil||normalized.Scheme!="sss"||normalized.AmountMinor!=100000||
  normalized.PeriodOn!="2026-10-01"||normalized.Note!="Monthly"{
  t.Fatalf("normalized %+v %v",normalized,e)
 }
 copy:=input;copy.Amount="1000.01"
 changed,e:=normalizeObligation(copy)
 if e!=nil||changed.Hash==normalized.Hash{t.Fatal("amount missing in request fingerprint")}
 mp2Input:=NewObligation{Scheme:"mp2",Period:"2026-10",Amount:"500.50",
  MP2AccountID:mp2,Key:k}
 result,e:=normalizeObligation(mp2Input)
 if e!=nil||result.MP2AccountID!=mp2||result.AmountMinor!=50050{t.Fatalf("mp2 %+v %v",result,e)}
 for _,bad:=range []NewObligation{
  {Scheme:"sss",Period:"2026-10",Amount:"100",Key:k},
  {Scheme:"mp2",Period:"2026-10",Amount:"100",ExpenseCategoryID:cat,Key:k},
  {Scheme:"philhealth",Period:"2026-13",Amount:"100",ExpenseCategoryID:cat,Key:k},
  {Scheme:"philhealth",Period:"2026-10",DueOn:"2026-09-30",Amount:"100",ExpenseCategoryID:cat,Key:k},
  {Scheme:"pagibig",Period:"2026-10",Amount:"0",ExpenseCategoryID:cat,Key:k},
  {Scheme:"other",Period:"2026-10",Amount:"100",ExpenseCategoryID:cat,Key:k},
 }{
  if _,e:=normalizeObligation(bad);!errors.Is(e,ErrInvalid){t.Fatalf("accepted invalid %+v %v",bad,e)}
 }
}
func TestResolutionNormalizationAndStatuses(t *testing.T){
 id:=key(t)
 reference:=key(t)
 k:=key(t)
 input:=NewResolution{ObligationID:id,Action:"paid",ExpenseID:reference,
  ResolvedOn:"2026-10-28",Key:k}
 n,e:=normalizeResolution(input)
 if e!=nil{t.Fatal(e)}
 other:=input;other.ExpenseID="";other.TransferID=reference
 x,e:=normalizeResolution(other)
 if e!=nil||n.Hash==x.Hash{t.Fatal("expense vs MP2 transfer must fingerprint differently")}
 for _,bad:=range []NewResolution{
  {ObligationID:id,Action:"paid",ResolvedOn:"2026-10-28",Key:k},
  {ObligationID:id,Action:"paid",ExpenseID:reference,TransferID:reference,ResolvedOn:"2026-10-28",Key:k},
  {ObligationID:id,Action:"skipped",ExpenseID:reference,ResolvedOn:"2026-10-28",Key:k},
  {ObligationID:id,Action:"paid",ExpenseID:reference,ResolvedOn:"2026-02-31",Key:k},
 }{
  if _,e:=normalizeResolution(bad);!errors.Is(e,ErrInvalid){t.Fatalf("accepted invalid %+v %v",bad,e)}
 }
 now:=time.Date(2026,10,28,0,0,0,0,time.UTC)
 for _,tc:=range []struct{due,action,want string}{
  {"","","pending"},{"2026-10-27","","overdue"},
  {"2026-10-28","","due"},{"2026-11-01","","upcoming"},
  {"2026-10-27","paid","paid"},{"2026-10-27","skipped","skipped"},
 }{
  if got:=Status(tc.due,tc.action,now);got!=tc.want{
   t.Fatalf("Status(%s,%s)=%s want %s",tc.due,tc.action,got,tc.want)
  }
 }
 for _,tc:=range []struct{scheme,label string}{
  {"sss","SSS"},{"philhealth","PhilHealth"},{"pagibig","Pag-IBIG"},{"mp2","Pag-IBIG MP2"},
 }{
  if SchemeLabel(tc.scheme)!=tc.label{t.Fatalf("scheme label %+v",tc)}
 }
}

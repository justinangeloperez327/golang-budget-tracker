package reports

import (
 "bytes"
 "encoding/csv"
 "errors"
 "math"
 "strings"
 "testing"
)
func TestPeriodsValidation(t *testing.T){
 for _,tc:=range []struct{period,currency,start,end string;annual bool}{
  {"2026-10","AED","2026-10-01","2026-11-01",false},
  {"2026","PHP","2026-01-01","2027-01-01",true},
  {"2100-12","PHP","2100-12-01","2101-01-01",false},
 }{
  r,e:=Parse(Query{Period:tc.period,Currency:tc.currency})
  if e!=nil||r.Start!=tc.start||r.End!=tc.end||r.Annual!=tc.annual{
   t.Fatalf("period %+v -> %+v, %v",tc,r,e)
  }
 }
 for _,q:=range []Query{
  {Period:"2026-13",Currency:"AED"},
  {Period:"2026-00",Currency:"AED"},
  {Period:"2026-2",Currency:"PHP"},
  {Period:"2026-02-31",Currency:"AED"},
  {Period:"1999",Currency:"AED"},
  {Period:"2101",Currency:"PHP"},
  {Period:"2026",Currency:"USD"},
  {Period:"2026",Currency:""},
 }{
  if _,e:=Parse(q);!errors.Is(e,ErrInvalid){t.Fatalf("invalid report accepted %+v %v",q,e)}
 }
}
func TestSignedMath(t *testing.T){
 if _,e:=add(math.MaxInt64,1);!errors.Is(e,ErrOverflow){t.Fatal("sum overflow accepted")}
 if _,e:=add(math.MinInt64,-1);!errors.Is(e,ErrOverflow){t.Fatal("sum underflow accepted")}
 if _,e:=sub(0,math.MinInt64);!errors.Is(e,ErrOverflow){t.Fatal("sub underflow accepted")}
 if x,e:=sub(-12,5);e!=nil||x!=-17{t.Fatalf("sub %d %v",x,e)}
 if _,e:=parse("9876543210987654321098");!errors.Is(e,ErrOverflow){t.Fatal("large SQL sum accepted")}
}
func TestExportsAndCSVFormulaDefense(t *testing.T){
 r:=Report{
  Query:Query{Period:"2026",Currency:"PHP"},
  Period:Range{Label:"2026",Start:"2026-01-01",End:"2027-01-01",Annual:true},
  IncomeMinor:20000,ExpensesMinor:14000,RefundMinor:1000,NetSpendingMinor:13000,
  ClosingNetWorthMinor:6500,
  Categories:[]Category{{Name:" =SUM(1,2)",Kind:"expense",GrossMinor:14000,RefundMinor:1000,NetMinor:13000}},
  Accounts:[]AccountMovement{{Name:"@private",Kind:"bank",ClosingMinor:6500}},
  Monthly:[]Month{{Period:"2026-01",IncomeMinor:20000,ExpensesMinor:14000,RefundMinor:1000,NetMinor:7000}},
 }
 raw,e:=Csv(r)
 if e!=nil{t.Fatal(e)}
 rows,e:=csv.NewReader(bytes.NewReader(raw)).ReadAll()
 if e!=nil{t.Fatal(e)}
 hasCategory,hasAccount:=false,false
 for _,row:=range rows{
  if len(row)>0&&strings.HasPrefix(row[0],"' =SUM"){hasCategory=true}
  if len(row)>0&&row[0]=="'@private"{hasAccount=true}
 }
 if !hasCategory||!hasAccount{t.Fatalf("CSV formula escaping category=%v account=%v",hasCategory,hasAccount)}
 if SafeCell("\t+CMD")!="'\t+CMD"{t.Fatal("tab formula not protected")}
 if SafeCell("normal")!="normal"{t.Fatal("normal cell modified")}
 pdf,e:=Pdf(r)
 if e!=nil{t.Fatal(e)}
 if !bytes.HasPrefix(pdf,[]byte("%PDF-1.4"))||!bytes.Contains(pdf,[]byte("xref"))||
  !bytes.Contains(pdf,[]byte("startxref"))||!bytes.Contains(pdf,[]byte("%%EOF"))||
  !bytes.Contains(pdf,[]byte("/Type /Page"))||!bytes.Contains(pdf,[]byte("PHP")){
  t.Fatal("PDF missing basic structure")
 }
 // Large category listings paginate and preserve page references.
 for i:=0;i<160;i++{r.Categories=append(r.Categories,Category{Name:"Long category",Kind:"expense",GrossMinor:100})}
 pdf,e=Pdf(r)
 if e!=nil||!bytes.Contains(pdf,[]byte("Page 2 of"))||!bytes.Contains(pdf,[]byte("/Count 4")){
  t.Fatalf("PDF pagination missing %v",e)
 }
}

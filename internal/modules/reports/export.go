package reports

import (
 "bytes"
 "encoding/csv"
 "fmt"
 "strconv"
 "strings"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

// SafeCell protects spreadsheet consumers against formula injection even when
// an account or category name begins with a formula sigil or leading spaces.
func SafeCell(raw string)string{
 value:=strings.ReplaceAll(strings.ReplaceAll(raw,"\r"," "),"\n"," ")
 trimmed:=strings.TrimLeft(value," \t")
 if len(trimmed)>0&&strings.ContainsRune("=+-@\t",rune(trimmed[0])){
  return "'"+value
 }
 return value
}
func money(n int64)string{return accounts.FormatSignedMinor(n)}
func Csv(report Report)([]byte,error){
 var b bytes.Buffer
 w:=csv.NewWriter(&b)
 emit:=func(cells ...string){for i:=range cells{cells[i]=SafeCell(cells[i])};for len(cells)<7{cells=append(cells,"")};_ = w.Write(cells)}
 emit("Budget Tracker — Financial Report")
 emit("Currency",report.Query.Currency)
 emit("Period",report.Period.Label)
 emit("Basis","Transactions: occurred date; account balances and fees: journal posting date")
 emit("")
 emit("Summary","Amount in "+report.Query.Currency)
 for _,x:=range [][2]string{
  {"Income",money(report.IncomeMinor)},{"Gross expense",money(report.ExpensesMinor)},
  {"Refunds",money(report.RefundMinor)},{"Net categorized spending",money(report.NetSpendingMinor)},
  {"Transfer fees",money(report.FeesMinor)},{"Loan interest",money(report.InterestMinor)},
  {"Operating net (not bank cashflow)",money(report.OperatingNetMinor)},
  {"Opening assets",money(report.OpeningAssetsMinor)},
  {"Opening liabilities",money(report.OpeningLiabilitiesMinor)},
  {"Opening net worth",money(report.OpeningNetWorthMinor)},
  {"Closing assets",money(report.ClosingAssetsMinor)},
  {"Closing liabilities",money(report.ClosingLiabilitiesMinor)},
  {"Closing net worth",money(report.ClosingNetWorthMinor)},
 }{emit(x[0],x[1])}
 emit("")
 emit("Category","Type","Gross","Refunds","Net")
 for _,row:=range report.Categories{emit(row.Name,row.Kind,money(row.GrossMinor),money(row.RefundMinor),money(row.NetMinor))}
 emit("")
 emit("Month","Income","Gross expenses","Refunds","Transfer fees","Loan interest","Operating net")
 for _,row:=range report.Monthly{emit(row.Period,money(row.IncomeMinor),money(row.ExpensesMinor),money(row.RefundMinor),money(row.FeesMinor),money(row.InterestMinor),money(row.NetMinor))}
 emit("")
 emit("Account","Type","Opening balance","Debits","Credits","Net posting movement","Closing balance")
 for _,row:=range report.Accounts{
  emit(row.Name,row.Kind,money(row.OpeningMinor),money(row.DebitsMinor),money(row.CreditsMinor),money(row.NetMovementMinor),money(row.ClosingMinor))
 }
 emit("")
 emit("Budget category","Planned","Carried","Actual","Remaining")
 for _,row:=range report.Budgets{emit(row.Name,money(row.PlannedMinor),money(row.CarriedMinor),money(row.ActualMinor),money(row.RemainingMinor))}
 emit("TOTAL",""+money(report.BudgetPlannedMinor),money(report.BudgetCarriedMinor),money(report.BudgetActualMinor),money(report.BudgetRemainingMinor))
 w.Flush()
 if e:=w.Error();e!=nil{return nil,e}
 return b.Bytes(),nil
}

// Pdf creates a paginated, printable text PDF in standard PDF 1.4 using only
// built-in Go APIs and the base-14 Helvetica font. Long rows wrap, and text
// is escaped (PDF operator injection is not possible).
func Pdf(report Report)([]byte,error){
 lines:=[]string{
  "BUDGET TRACKER - FINANCIAL REPORT",
  "Currency: "+report.Query.Currency+" | Period: "+report.Period.Label,
  "Basis: Transactions on occurred date; balances and costs on journal posting date",
  "",
  "FINANCIAL SUMMARY",
 }
 for _,item:=range [][2]string{
  {"Income",money(report.IncomeMinor)},
  {"Expenses before refunds",money(report.ExpensesMinor)},
  {"Refunds",money(report.RefundMinor)},
  {"Net categorized spending",money(report.NetSpendingMinor)},
  {"Transfer fees",money(report.FeesMinor)},
  {"Loan interest",money(report.InterestMinor)},
  {"Operating net (not bank cashflow)",money(report.OperatingNetMinor)},
  {"Opening assets",money(report.OpeningAssetsMinor)},
  {"Opening liabilities",money(report.OpeningLiabilitiesMinor)},
  {"Opening net worth",money(report.OpeningNetWorthMinor)},
  {"Closing assets",money(report.ClosingAssetsMinor)},
  {"Closing liabilities",money(report.ClosingLiabilitiesMinor)},
  {"Closing net worth",money(report.ClosingNetWorthMinor)},
 }{lines=append(lines,item[0]+": "+report.Query.Currency+" "+item[1])}
 lines=append(lines,"","CATEGORIES (Gross / Refunds / Net)")
 for _,v:=range report.Categories{lines=append(lines,fmt.Sprintf("%s [%s]: %s / %s / %s",v.Name,v.Kind,money(v.GrossMinor),money(v.RefundMinor),money(v.NetMinor)))}
 lines=append(lines,"","MONTH-TO-MONTH OPERATING COMPARISON")
 for _,v:=range report.Monthly{lines=append(lines,fmt.Sprintf("%s: Income %s | Expense %s | Refund %s | Fees %s | Interest %s | Net %s",v.Period,money(v.IncomeMinor),money(v.ExpensesMinor),money(v.RefundMinor),money(v.FeesMinor),money(v.InterestMinor),money(v.NetMinor)))}
 lines=append(lines,"","ACCOUNT MOVEMENTS (as-of journal posting times)")
 for _,v:=range report.Accounts{lines=append(lines,fmt.Sprintf("%s [%s]: Opening %s | Dr %s | Cr %s | Closing %s",v.Name,v.Kind,money(v.OpeningMinor),money(v.DebitsMinor),money(v.CreditsMinor),money(v.ClosingMinor)))}
 lines=append(lines,"","BUDGET VERSUS ACTUAL")
 if !report.BudgetConfigured{lines=append(lines,"No budget configured for this reporting period.")}
 for _,v:=range report.Budgets{lines=append(lines,fmt.Sprintf("%s: Planned %s | Carry %s | Actual %s | Left %s",v.Name,money(v.PlannedMinor),money(v.CarriedMinor),money(v.ActualMinor),money(v.RemainingMinor)))}
 lines=append(lines,fmt.Sprintf("Totals: Planned %s | Carry %s | Actual %s | Left %s",money(report.BudgetPlannedMinor),money(report.BudgetCarriedMinor),money(report.BudgetActualMinor),money(report.BudgetRemainingMinor)))
 lines=append(lines,"","Opening and closing balances use posted journal timestamps, not bank statement dates.","Transfers and loan principal change account balances but are not operating income/expenses.")
 // Helvetica supports printable 7-bit ASCII. Replace unsupported rune values.
 var printable []string
 for _,line:=range lines{
  var clean strings.Builder
  for _,r:=range line{if r>=32&&r<=126{clean.WriteRune(r)}else{clean.WriteByte('?')}}
  s:=clean.String()
  if len(s)==0{printable=append(printable,"");continue}
  for len(s)>98{
   cut:=98
   if x:=strings.LastIndexByte(s[:98],' ');x>45{cut=x}
   printable=append(printable,s[:cut]);s=strings.TrimLeft(s[cut:]," ")
  }
  printable=append(printable,s)
 }
 const linesPerPage=56
 pageCount:=(len(printable)+linesPerPage-1)/linesPerPage
 if pageCount==0{pageCount=1}
 var b bytes.Buffer
 b.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
 offsets:=[]int{0}
 writeObj:=func(id int,value string){
  for len(offsets)<=id{offsets=append(offsets,0)}
  offsets[id]=b.Len()
  fmt.Fprintf(&b,"%d 0 obj\n%s\nendobj\n",id,value)
 }
 // 1=Catalog; 2=Pages; 3=font; pages 4,6,...; content streams 5,7,...
 kids:=make([]string,0,pageCount)
 for i:=0;i<pageCount;i++{kids=append(kids,fmt.Sprintf("%d 0 R",4+2*i))}
 writeObj(1,"<< /Type /Catalog /Pages 2 0 R >>")
 writeObj(2,fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",strings.Join(kids," "),pageCount))
 writeObj(3,"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
 for i:=0;i<pageCount;i++{
  pageID,contentID:=4+2*i,5+2*i
  writeObj(pageID,fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>",contentID))
  var stream strings.Builder
  limit:=(i+1)*linesPerPage
  if limit>len(printable){limit=len(printable)}
  for j,line:=range printable[i*linesPerPage:limit]{
   esc:=strings.NewReplacer("\\","\\\\","(","\\(",")","\\)").Replace(line)
   fmt.Fprintf(&stream,"BT /F1 9 Tf 38 %d Td (%s) Tj ET\n",742-j*12,esc)
  }
  fmt.Fprintf(&stream,"BT /F1 8 Tf 38 34 Td (Page %d of %d) Tj ET\n",i+1,pageCount)
  body:=stream.String()
  writeObj(contentID,"<< /Length "+strconv.Itoa(len(body))+" >>\nstream\n"+body+"endstream")
 }
 start:=b.Len()
 count:=3+2*pageCount
 fmt.Fprintf(&b,"xref\n0 %d\n0000000000 65535 f \n",count+1)
 for i:=1;i<=count;i++{fmt.Fprintf(&b,"%010d 00000 n \n",offsets[i])}
 fmt.Fprintf(&b,"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",count+1,start)
 return b.Bytes(),nil
}

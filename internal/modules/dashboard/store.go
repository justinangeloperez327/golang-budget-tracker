// Package dashboard composes existing workspace-owned, ledger-backed views.
// It never creates or adjusts balances or accounting entries.
package dashboard

import (
 "context"
 "errors"
 "math"
 "strconv"
 "time"

 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/budgets"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/contributions"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/debts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/savings"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)

var ErrInvalid = errors.New("invalid dashboard workspace or month")
var ErrOverflow = errors.New("dashboard total exceeds supported precision")

type Currency struct {
 Code string
 AssetsMinor,LiabilitiesMinor,NetWorthMinor int64
 IncomeMinor,ExpensesMinor,RefundsMinor,SpendingMinor,OtherCostsMinor,NetFlowMinor int64
 Budget budgets.Overview
 Accounts []accounts.Account
}
type Snapshot struct {
 Month string
 UpdatedOn string
 Currencies []Currency
 Goals []savings.Goal
 Loans []debts.Plan
 Bills []recurring.Occurrence
 Contributions []contributions.Obligation
 Installments []debts.Installment
 Recent []transactions.Record
}
type Store struct{ db *pgxpool.Pool }
func NewStore(db *pgxpool.Pool)*Store{return &Store{db:db}}

func Add(a,b int64)(int64,error){
 if (b>0&&a>math.MaxInt64-b)||(b<0&&a<math.MinInt64-b){return 0,ErrOverflow}
 return a+b,nil
}
func Sub(a,b int64)(int64,error){
 if b==math.MinInt64{return 0,ErrOverflow}
 return Add(a,-b)
}
func parseAmount(s string)(int64,error){
 n,e:=strconv.ParseInt(s,10,64)
 if e!=nil{return 0,ErrOverflow}
 return n,nil
}
func isAsset(kind string)bool{
 return kind=="bank"||kind=="cash"||kind=="savings"||kind=="ewallet"
}
func isLiability(kind string)bool{return kind=="loan"||kind=="credit"}
func classify(accountsList []accounts.Account,currency string)(Currency,error){
 result:=Currency{Code:currency,Accounts:make([]accounts.Account,0)}
 var err error
 for _,a:=range accountsList{
  if a.Currency!=currency{continue}
  result.Accounts=append(result.Accounts,a) // archived accounts still have money/debt
  if isAsset(a.Kind){
   result.AssetsMinor,err=Add(result.AssetsMinor,a.LedgerMinor)
  }else if isLiability(a.Kind){
   result.LiabilitiesMinor,err=Add(result.LiabilitiesMinor,a.LedgerMinor)
  }
  if err!=nil{return Currency{},err}
 }
 result.NetWorthMinor,err=Sub(result.AssetsMinor,result.LiabilitiesMinor)
 return result,err
}
func (s *Store) monthlyTotals(ctx context.Context,ws,start,end string,out *[2]Currency)error{
 rows,e:=s.db.Query(ctx,`
  SELECT currency,kind,SUM(total_minor::numeric)::text
  FROM finance_transactions
  WHERE workspace_id=$1 AND occurred_on >= $2::date AND occurred_on < $3::date
  GROUP BY currency,kind`,ws,start,end)
 if e!=nil{return e}
 for rows.Next(){
  var curr,kind,raw string
  if e=rows.Scan(&curr,&kind,&raw);e!=nil{rows.Close();return e}
  n,parseErr:=parseAmount(raw)
  if parseErr!=nil{rows.Close();return parseErr}
  i:=0
  if curr=="PHP"{i=1}else if curr!="AED"{rows.Close();return ErrInvalid}
  switch kind{
  case "income":out[i].IncomeMinor=n
  case "expense":out[i].ExpensesMinor=n
  case "refund":out[i].RefundsMinor=n
  default:rows.Close();return ErrInvalid
  }
 }
 e=rows.Err()
 rows.Close()
 if e!=nil{return e}
 // Dedicated transfer fees and loan interest are genuine expenses, separate
 // from category-split spending. Their event month is journal creation time,
 // not the manually backdated transfer/payment date.
 rows,e=s.db.Query(ctx,`
  SELECT a.currency,COALESCE(SUM(l.debit_minor::numeric-l.credit_minor::numeric),0)::text
  FROM ledger_lines l
  JOIN ledger_accounts a ON a.id=l.ledger_account_id AND a.workspace_id=l.workspace_id
  JOIN ledger_journals j ON j.id=l.journal_id AND j.workspace_id=l.workspace_id
  WHERE l.workspace_id=$1 AND a.system_key IN ('transfer_fee','loan_interest')
   AND j.created_at >= $2::date AND j.created_at < $3::date
  GROUP BY a.currency`,ws,start,end)
 if e!=nil{return e}
 for rows.Next(){
  var curr,raw string
  if e=rows.Scan(&curr,&raw);e!=nil{rows.Close();return e}
  n,parseErr:=parseAmount(raw)
  if parseErr!=nil{rows.Close();return parseErr}
  i:=0
  if curr=="PHP"{i=1}else if curr!="AED"{rows.Close();return ErrInvalid}
  out[i].OtherCostsMinor=n
 }
 e=rows.Err()
 rows.Close()
 if e!=nil{return e}
 for i:=range out{
  out[i].SpendingMinor,e=Sub(out[i].ExpensesMinor,out[i].RefundsMinor)
  if e!=nil{return e}
  var remainder int64
  remainder,e=Sub(out[i].IncomeMinor,out[i].SpendingMinor)
  if e!=nil{return e}
  out[i].NetFlowMinor,e=Sub(remainder,out[i].OtherCostsMinor)
  if e!=nil{return e}
 }
 return nil
}
// Load never combines AED and PHP in a single total. Source values are
// authoritative module records, not a second mutable dashboard balance.
func(s *Store)Load(ctx context.Context,workspace,month string,now time.Time)(Snapshot,error){
 if !accounts.IsValidKey(workspace){return Snapshot{},ErrInvalid}
 if _,e:=budgets.ValidateMonth(month);e!=nil{return Snapshot{},ErrInvalid}
 date,e:=time.Parse("2006-01",month)
 if e!=nil{return Snapshot{},ErrInvalid}
 start:=date.Format("2006-01-02")
 end:=date.AddDate(0,1,0).Format("2006-01-02")
 accountList,e:=accounts.NewStore(s.db).List(ctx,workspace)
 if e!=nil{return Snapshot{},e}
 var data [2]Currency
 for i,code:=range []string{"AED","PHP"}{
  data[i],e=classify(accountList,code)
  if e!=nil{return Snapshot{},e}
  data[i].Budget,_,e=budgets.NewStore(s.db).Overview(ctx,workspace,month,code)
  if e!=nil{return Snapshot{},e}
 }
 if e=s.monthlyTotals(ctx,workspace,start,end,&data);e!=nil{return Snapshot{},e}
 snap:=Snapshot{Month:month,UpdatedOn:now.UTC().Format("2006-01-02 15:04 UTC"),
  Currencies:[]Currency{data[0],data[1]}}
 snap.Goals,e=savings.NewStore(s.db).Goals(ctx,workspace,now)
 if e!=nil{return Snapshot{},e}
 snap.Loans,e=debts.NewStore(s.db).Plans(ctx,workspace)
 if e!=nil{return Snapshot{},e}
 snap.Installments,e=debts.NewStore(s.db).Installments(ctx,workspace)
 if e!=nil{return Snapshot{},e}
 occurrences,e:=recurring.NewStore(s.db).Upcoming(ctx,workspace,now)
 if e!=nil{return Snapshot{},e}
 // Show only actionable due / overdue bills in the next 30 days.
 cutoff:=now.UTC().AddDate(0,0,30).Format("2006-01-02")
 for _,b:=range occurrences{
  if (b.Status=="overdue"||b.Status=="due"||b.Status=="upcoming")&&
   b.DueOn<=cutoff&&len(snap.Bills)<8{
   snap.Bills=append(snap.Bills,b)
  }
 }
 obligations,e:=contributions.NewStore(s.db).Obligations(ctx,workspace,now)
 if e!=nil{return Snapshot{},e}
 for _,c:=range obligations{
  if (c.Status=="overdue"||c.Status=="due")&&len(snap.Contributions)<8{
   snap.Contributions=append(snap.Contributions,c)
  }
 }
 snap.Recent,e=transactions.NewStore(s.db).List(ctx,workspace,transactions.Filter{})
 if e!=nil{return Snapshot{},e}
 if len(snap.Recent)>8{snap.Recent=snap.Recent[:8]}
 return snap,nil
}

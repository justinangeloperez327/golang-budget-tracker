// Package debts models existing loan liabilities and immutable repayments.
package debts

import (
 "crypto/sha256"
 "encoding/json"
 "errors"
 "math/big"
 "strconv"
 "strings"
 "time"
 "unicode/utf8"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

const MaxMinor int64=99999999999999
var (
 ErrInvalid=errors.New("invalid loan or installment")
 ErrNotFound=errors.New("loan, account or installment not found")
 ErrConflict=errors.New("idempotency key reused with different details")
 ErrRecorded=errors.New("installment already paid or loan already planned")
 ErrFunds=errors.New("insufficient balance to pay installment")
 ErrBalance=errors.New("loan ledger balance does not match remaining principal")
 ErrOverflow=errors.New("loan exceeds supported monetary limits")
)
type NewPlan struct {
 Name,LoanAccountID,FundingAccountID,APR,FirstDue,Key string
 Months int
}
type PayInput struct {
 PlanID,InstallmentID,PaidOn,Key string
}
type Plan struct{
 ID,Name,Currency,LoanAccountID,FundingAccountID,LoanName,FundingName,FirstDue string
 PrincipalMinor,OutstandingMinor int64
 AnnualRateBps,Months,Completed int
}
type Installment struct{
 ID,PlanID,DueOn,Currency,Status,PayKey string
 Number int
 PrincipalMinor,InterestMinor,AmountMinor int64
}
type Payment struct{
 ID,PlanID,PlanName,Currency,PaidOn string
 Number int
 PrincipalMinor,InterestMinor,AmountMinor int64
}
type normalizedPlan struct {
 Name,LoanAccountID,FundingAccountID,FirstDue,Key string
 AnnualRateBps,Months int
 Hash [32]byte
}
type normalizedPay struct {
 PlanID,InstallmentID,PaidOn,Key string
 Hash [32]byte
}
func validDate(s string)bool{
 d,e:=time.Parse("2006-01-02",s)
 return e==nil && d.Format("2006-01-02")==s && d.Year()>=2000 && d.Year()<=2100
}
func hash(v any)[32]byte{
 data,_:=json.Marshal(v)
 return sha256.Sum256(data)
}
func ParseAPR(s string)(int,error){
 n,e:=accounts.ParseMinor(s)
 if e!=nil||n>100000{return 0,ErrInvalid}
 return int(n),nil
}
func normalizePlan(p NewPlan)(normalizedPlan,error){
 n:=normalizedPlan{
  Name:strings.TrimSpace(p.Name),LoanAccountID:p.LoanAccountID,
  FundingAccountID:p.FundingAccountID,FirstDue:p.FirstDue,
  Key:p.Key,Months:p.Months,
 }
 if !accounts.IsValidKey(n.Key)||!accounts.IsValidKey(n.LoanAccountID)||
  !accounts.IsValidKey(n.FundingAccountID)||n.LoanAccountID==n.FundingAccountID||
  !validDate(n.FirstDue)||n.Months<1||n.Months>360||
  utf8.RuneCountInString(n.Name)<1||utf8.RuneCountInString(n.Name)>100||
  strings.ContainsAny(n.Name,"\x00\r\n"){return normalizedPlan{},ErrInvalid}
 rate,err:=ParseAPR(p.APR)
 if err!=nil{return normalizedPlan{},err}
 n.AnnualRateBps=rate
 n.Hash=hash(struct{
  Name,Loan,Funding,Due string
  APR,Months int
 }{n.Name,n.LoanAccountID,n.FundingAccountID,n.FirstDue,n.AnnualRateBps,n.Months})
 return n,nil
}
func normalizePay(p PayInput)(normalizedPay,error){
 n:=normalizedPay{PlanID:p.PlanID,InstallmentID:p.InstallmentID,PaidOn:p.PaidOn,Key:p.Key}
 if !accounts.IsValidKey(n.PlanID)||!accounts.IsValidKey(n.InstallmentID)||
  !accounts.IsValidKey(n.Key)||!validDate(n.PaidOn){return normalizedPay{},ErrInvalid}
 n.Hash=hash(struct{PlanID,InstallmentID,PaidOn string}{
  n.PlanID,n.InstallmentID,n.PaidOn,
 })
 return n,nil
}
func DueDate(start string,index int)string{
 first,_:=time.Parse("2006-01-02",start)
 target:=time.Date(first.Year(),first.Month(),1,0,0,0,0,time.UTC).AddDate(0,index,0)
 maxDay:=target.AddDate(0,1,-1).Day()
 day:=first.Day()
 if day>maxDay{day=maxDay}
 return time.Date(target.Year(),target.Month(),day,0,0,0,0,time.UTC).Format("2006-01-02")
}
type Calculation struct{
 DueOn string
 Number int
 PrincipalMinor,InterestMinor,AmountMinor int64
}
// Schedule uses equal-principal payments and declining-balance annual simple
// interest calculated each month: round_half_up(outstanding * APRbps / 120000).
// No floating point or compound interest. Final principal pays the remainder.
func Schedule(principal int64,aprBps,months int,firstDue string)([]Calculation,error){
 if principal<=0||principal>MaxMinor||months<1||months>360||
  principal<int64(months)||aprBps<0||aprBps>100000||!validDate(firstDue){
  return nil,ErrInvalid
 }
 remaining:=principal
 base:=principal/int64(months)
 result:=make([]Calculation,0,months)
 for i:=0;i<months;i++{
  portion:=base
  if i==months-1{portion=remaining}
  interestBig:=new(big.Int).Mul(big.NewInt(remaining),big.NewInt(int64(aprBps)))
  interestBig.Add(interestBig,big.NewInt(60000))
  interestBig.Div(interestBig,big.NewInt(120000))
  if !interestBig.IsInt64(){return nil,ErrOverflow}
  interest:=interestBig.Int64()
  if interest>MaxMinor-portion{return nil,ErrOverflow}
  result=append(result,Calculation{DueOn:DueDate(firstDue,i),Number:i+1,
   PrincipalMinor:portion,InterestMinor:interest,AmountMinor:portion+interest})
  remaining-=portion
 }
 if remaining!=0{return nil,ErrInvalid}
 return result,nil
}
func parseMinorSQL(v string)(int64,error){
 n,e:=strconv.ParseInt(v,10,64)
 if e!=nil||n>MaxMinor||n < -MaxMinor{return 0,ErrOverflow}
 return n,nil
}

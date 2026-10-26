// Package budgets computes workspace-scoped, currency-specific monthly plans
// from immutable revisions and immutable finance transaction allocations.
package budgets

import (
 "crypto/sha256"
 "encoding/json"
 "errors"
 "math"
 "strconv"
 "strings"
 "time"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

const MaxMinor int64=99999999999999

var (
 ErrInvalid=errors.New("invalid budget")
 ErrNotFound=errors.New("expense category not found")
 ErrConflict=errors.New("idempotency key was reused for different budget details")
 ErrOverflow=errors.New("budget amount exceeds supported precision")
)

type Update struct {
 Month string
 Currency string
 Action string
 CategoryID string
 Amount string
 Policy string
 Key string
}
type normalized struct {
 Month string
 Currency string
 Action string
 CategoryID string
 AmountMinor int64
 Policy string
 Key string
 Hash [32]byte
}
type Category struct {
 ID string
 Name string
 Archived bool
}
type Row struct {
 CategoryID string
 Name string
 AllocatedMinor int64
 CarriedMinor int64
 ActualMinor int64
 RemainingMinor int64
 Overspent bool
}
type Overview struct {
 Month string
 Currency string
 Policy string
 Configured bool
 Rows []Row
 PlannedMinor int64
 CarriedMinor int64
 ActualMinor int64
 RemainingMinor int64
 Overspent bool
}

func ValidateMonth(raw string)(string,error){
 if len(raw)!=7||raw[4]!='-'{return "",ErrInvalid}
 parsed,err:=time.Parse("2006-01",raw)
 if err!=nil||parsed.Year()<2000||parsed.Year()>2100||parsed.Format("2006-01")!=raw {
  return "",ErrInvalid
 }
 return raw,nil
}
func ValidCurrency(currency string)bool{return currency=="AED"||currency=="PHP"}

func normalize(v Update)(normalized,error){
 n:=normalized{Month:v.Month,Currency:v.Currency,Action:v.Action,
  CategoryID:v.CategoryID,Key:v.Key,Policy:v.Policy}
 if _,err:=ValidateMonth(n.Month);err!=nil{return normalized{},err}
 if !ValidCurrency(n.Currency)||!accounts.IsValidKey(n.Key){return normalized{},ErrInvalid}
 switch n.Action{
 case "allocation":
  if !accounts.IsValidKey(n.CategoryID)||n.Policy!=""{return normalized{},ErrInvalid}
  value,err:=accounts.ParseMinor(v.Amount)
  if err!=nil{return normalized{},ErrInvalid}
  n.AmountMinor=value
 case "policy":
  if n.CategoryID!=""||v.Amount!=""||(n.Policy!="none"&&n.Policy!="carry_positive"){
   return normalized{},ErrInvalid
  }
 default:
  return normalized{},ErrInvalid
 }
 payload:=struct{
  Month,Currency,Action,CategoryID,Policy string
  Amount int64
 }{n.Month,n.Currency,n.Action,n.CategoryID,n.Policy,n.AmountMinor}
 bytes,_:=json.Marshal(payload)
 n.Hash=sha256.Sum256(bytes)
 return n,nil
}
func monthDate(month string)string{return month+"-01"}
func monthBefore(month string)string{
 date,_:=time.Parse("2006-01",month)
 return date.AddDate(0,-1,0).Format("2006-01")
}
func safeAdd(a,b int64)(int64,error){
 if (b>0&&a>math.MaxInt64-b)||(b<0&&a<math.MinInt64-b){return 0,ErrOverflow}
 result:=a+b
 if result>MaxMinor||result < -MaxMinor{return 0,ErrOverflow}
 return result,nil
}
func numericMinor(raw string)(int64,error){
 n,e:=strconv.ParseInt(strings.TrimSpace(raw),10,64)
 if e!=nil||n>MaxMinor||n < -MaxMinor{return 0,ErrOverflow}
 return n,nil
}
type monthPlan struct {
 exists bool
 policy string
 allocations map[string]int64
}
func planFor(m map[string]*monthPlan,month string)*monthPlan{
 current:=m[month]
 if current==nil{
  current=&monthPlan{policy:"none",allocations:map[string]int64{}}
  m[month]=current
 }
 return current
}

// Calculate recalculates positive unused allocations month-by-month. Carryover
// requires an immediately previous configured month and carry_positive on the
// destination month. Missing months break the chain.
func Calculate(target,currency string,plans map[string]*monthPlan,
 actuals map[string]map[string]int64,categories []Category)(Overview,error){
 if _,err:=ValidateMonth(target);err!=nil||!ValidCurrency(currency){return Overview{},ErrInvalid}
 o:=Overview{Month:target,Currency:currency,Policy:"none",Rows:make([]Row,0)}
 // Re-evaluate from the first month of the supported budget calendar so a
 // retroactive expense or refund changes the next month's available carry.
 previousAvailable:=map[string]int64{}
 previousConfigured:=false
 var currentAllocated,currentCarried,currentSpent map[string]int64
 period,_:=time.Parse("2006-01","2000-01")
 end,_:=time.Parse("2006-01",target)
 for !period.After(end){
  month:=period.Format("2006-01")
  p:=plans[month]
  allocations:=map[string]int64{}
  policy:="none"
  configured:=p!=nil&&p.exists
  if p!=nil{
   policy=p.policy
   for key,value:=range p.allocations{allocations[key]=value}
  }
  carried:=map[string]int64{}
  if policy=="carry_positive" && previousConfigured{
   for key,value:=range previousAvailable{
    if value>0{carried[key]=value}
   }
  }
  spent:=actuals[month]
  available:=map[string]int64{}
  categoriesSeen:=map[string]bool{}
  for key:=range allocations{categoriesSeen[key]=true}
  for key:=range carried{categoriesSeen[key]=true}
  for key:=range spent{categoriesSeen[key]=true}
  for category:=range categoriesSeen{
   base,e:=safeAdd(allocations[category],carried[category])
   if e!=nil{return Overview{},e}
   remainder,e:=safeAdd(base,-spent[category])
   if e!=nil{return Overview{},e}
   if remainder>0{available[category]=remainder}
  }
  if month==target{
   o.Policy=policy
   o.Configured=configured
   currentAllocated,currentCarried,currentSpent=allocations,carried,spent
  }
  previousAvailable=available
  previousConfigured=configured
  period=period.AddDate(0,1,0)
 }
 for _,category:=range categories{
  a,c,v:=currentAllocated[category.ID],currentCarried[category.ID],currentSpent[category.ID]
  if a==0&&c==0&&v==0{continue}
  combined,e:=safeAdd(a,c)
  if e!=nil{return Overview{},e}
  remaining,e:=safeAdd(combined,-v)
  if e!=nil{return Overview{},e}
  row:=Row{CategoryID:category.ID,Name:category.Name,AllocatedMinor:a,
   CarriedMinor:c,ActualMinor:v,RemainingMinor:remaining,Overspent:remaining<0}
  o.Rows=append(o.Rows,row)
  o.PlannedMinor,e=safeAdd(o.PlannedMinor,a);if e!=nil{return Overview{},e}
  o.CarriedMinor,e=safeAdd(o.CarriedMinor,c);if e!=nil{return Overview{},e}
  o.ActualMinor,e=safeAdd(o.ActualMinor,v);if e!=nil{return Overview{},e}
 }
 combined,e:=safeAdd(o.PlannedMinor,o.CarriedMinor)
 if e!=nil{return Overview{},e}
 o.RemainingMinor,e=safeAdd(combined,-o.ActualMinor)
 if e!=nil{return Overview{},e}
 o.Overspent=o.RemainingMinor<0
 return o,nil
}

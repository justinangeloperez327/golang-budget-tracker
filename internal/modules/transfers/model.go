// Package transfers manages money movement and reconciliation using immutable
// ledger journals. All values are exact integer minor units.
package transfers

import (
 "crypto/sha256"
 "encoding/json"
 "errors"
 "math/big"
 "regexp"
 "strconv"
 "strings"
 "time"
 "unicode/utf8"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

var (
 ErrInvalid=errors.New("invalid transfer or reconciliation")
 ErrNotFound=errors.New("account not available")
 ErrConflict=errors.New("idempotency key already used for different content")
 ErrOverflow=errors.New("monetary amount out of range")
)
const scale int64=100000000
var ratePattern=regexp.MustCompile(`^[0-9]{1,7}(\.[0-9]{1,8})?$`)

type Input struct{
 FromID string
 ToID string
 Amount string
 Received string
 Fee string
 Rate string
 Key string
 Date string
 Note string
}
type normalized struct{
 FromID,ToID,Key,Date,Note string
 Send,Receive,Fee,RateUnits int64
 Hash [32]byte
}
type ReconcileInput struct{
 AccountID,Observed,Key,Date,Note string
}
type normalizedReconcile struct{
 AccountID,Key,Date,Note string
 Observed int64
 Hash [32]byte
}
type Transfer struct{
 ID string
 FromName string
 ToName string
 FromCurrency string
 ToCurrency string
 SendMinor int64
 ReceiveMinor int64
 FeeMinor int64
 RateUnits int64
 Note string
 Date string
}
type Reconciliation struct{
 ID string
 AccountName string
 Currency string
 BeforeMinor int64
 ObservedMinor int64
 DeltaMinor int64
 Note string
 Date string
}
func parseAmount(v string,allowZero bool)(int64,error){
 amount,err:=accounts.ParseMinor(v)
 if err!=nil||(!allowZero&&amount==0){return 0,ErrInvalid}
 return amount,nil
}
func ParseRate(raw string)(int64,error){
 raw=strings.TrimSpace(raw)
 if !ratePattern.MatchString(raw){return 0,ErrInvalid}
 parts:=strings.SplitN(raw,".",2)
 i,err:=strconv.ParseInt(parts[0],10,64)
 if err!=nil{return 0,ErrInvalid}
 n:=i*scale
 if len(parts)==2{
  p:=parts[1]+strings.Repeat("0",8-len(parts[1]))
  f,err:=strconv.ParseInt(p,10,64)
  if err!=nil{return 0,ErrInvalid}
  n+=f
 }
 if n==0{return 0,ErrInvalid}
 return n,nil
}
func RateText(units int64)string {
 if units<=0{return "—"}
 whole:=strconv.FormatInt(units/scale,10)
 frac:=strconv.FormatInt(units%scale+scale,10)[1:]
 tail:=strings.TrimRight(frac,"0")
 if tail==""{return whole}
 return whole+"."+tail
}
func ConvertedMinor(source,units int64)(int64,error){
 if source<=0||source>ledger.MaxMinor||units<=0{return 0,ErrInvalid}
 n:=new(big.Int).Mul(big.NewInt(source),big.NewInt(units))
 n.Add(n,big.NewInt(scale/2))
 n.Div(n,big.NewInt(scale))
 if !n.IsInt64()||n.Sign()==0||n.Int64()>ledger.MaxMinor{return 0,ErrOverflow}
 return n.Int64(),nil
}
func validateIdentity(key,day,note string)error {
 if !accounts.IsValidKey(key)||utf8.RuneCountInString(note)>500||
  strings.ContainsAny(note,"\x00\r\n"){return ErrInvalid}
 parsed,err:=time.Parse("2006-01-02",day)
 if err!=nil||parsed.Year()<2000||parsed.Year()>2100{return ErrInvalid}
 return nil
}
func hashPayload(v any)[32]byte{
 body,_:=json.Marshal(v)
 return sha256.Sum256(body)
}
func normalizeTransfer(v Input)(normalized,error){
 n:=normalized{FromID:v.FromID,ToID:v.ToID,Key:v.Key,Date:v.Date,Note:strings.TrimSpace(v.Note)}
 if !accounts.IsValidKey(n.FromID)||!accounts.IsValidKey(n.ToID)||n.FromID==n.ToID||
  validateIdentity(n.Key,n.Date,n.Note)!=nil{return normalized{},ErrInvalid}
 var err error
 n.Send,err=parseAmount(v.Amount,false)
 if err!=nil{return normalized{},err}
 fee:=strings.TrimSpace(v.Fee)
 if fee==""{fee="0"}
 n.Fee,err=parseAmount(fee,true)
 if err!=nil||n.Fee>ledger.MaxMinor-n.Send{return normalized{},ErrInvalid}
 if v.Rate!=""{
  n.RateUnits,err=ParseRate(v.Rate)
  if err!=nil{return normalized{},err}
  n.Receive,err=parseAmount(v.Received,false)
  if err!=nil{return normalized{},err}
  converted,err:=ConvertedMinor(n.Send,n.RateUnits)
  if err!=nil{return normalized{},err}
  if n.Receive!=converted{return normalized{},ErrInvalid}
 }else{
  if v.Received!="" && v.Received!=v.Amount{return normalized{},ErrInvalid}
  n.Receive=n.Send
 }
 n.Hash=hashPayload(struct{
  From,To,Date,Note string
  Send,Receive,Fee,Rate int64
 }{n.FromID,n.ToID,n.Date,n.Note,n.Send,n.Receive,n.Fee,n.RateUnits})
 return n,nil
}
func normalizeReconcile(v ReconcileInput)(normalizedReconcile,error){
 n:=normalizedReconcile{AccountID:v.AccountID,Key:v.Key,Date:v.Date,Note:strings.TrimSpace(v.Note)}
 if !accounts.IsValidKey(n.AccountID)||validateIdentity(n.Key,n.Date,n.Note)!=nil{return normalizedReconcile{},ErrInvalid}
 value,err:=parseAmount(v.Observed,true)
 if err!=nil{return normalizedReconcile{},err}
 n.Observed=value
 n.Hash=hashPayload(struct{
  AccountID,Date,Note string
  Observed int64
 }{n.AccountID,n.Date,n.Note,n.Observed})
 return n,nil
}

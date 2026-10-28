package remittances

import (
 "errors"
 "testing"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)
func TestEffectiveFXAndLimits(t *testing.T){
 n,e:=effectiveRate(10000,200,155000)
 if e!=nil||n!=1519607843{t.Fatalf("fee adjusted rate %d %v",n,e)}
 zero,e:=effectiveRate(10000,0,155000)
 if e!=nil||zero!=1550000000{t.Fatalf("rate without fee %d %v",zero,e)}
 for _,tc:=range []struct{send,fee,receive int64}{
  {0,0,100},{100,-1,100},{100,0,0},{maxMinor,1,100},
 }{
  if _,e:=effectiveRate(tc.send,tc.fee,tc.receive);!errors.Is(e,ErrInvalid){
   t.Fatalf("invalid effective rate accepted %+v %v",tc,e)
  }
 }
}
func TestNormalizeRemittanceAndIdempotency(t *testing.T){
 source,_:=accounts.NewSetupKey()
 destination,_:=accounts.NewSetupKey()
 key,_:=accounts.NewSetupKey()
 v:=CreateInput{
  SourceAccountID:source,DestinationAccountID:destination,
  Send:"100.00",Fee:"2.00",Rate:"15.5",Receive:"1550.00",
  RequestedOn:"2026-10-28",Note:"  Family  ",Key:key,
 }
 n,e:=normalizeCreate(v)
 if e!=nil||n.SendMinor!=10000||n.FeeMinor!=200||n.ReceiveMinor!=155000||
  n.RateUnits!=1550000000||n.EffectiveRateUnits!=1519607843||n.Note!="Family"{
  t.Fatalf("normalized %+v %v",n,e)
 }
 same:=v;same.Rate="15.50000000";same.Note="Family"
 other,e:=normalizeCreate(same)
 if e!=nil||other.Hash!=n.Hash{t.Fatalf("canonical request drift %+v %v",other,e)}
 altered:=v;altered.Fee="3.00"
 other,e=normalizeCreate(altered)
 if e!=nil||other.Hash==n.Hash{t.Fatal("fee not in fingerprint")}
 for _,bad:=range []CreateInput{
  {SourceAccountID:source,DestinationAccountID:source,Send:"100",Fee:"0",Rate:"15",Receive:"1500",RequestedOn:v.RequestedOn,Key:key},
  {SourceAccountID:source,DestinationAccountID:destination,Send:"100",Fee:"0",Rate:"15",Receive:"1400",RequestedOn:v.RequestedOn,Key:key},
  {SourceAccountID:source,DestinationAccountID:destination,Send:"100",Fee:"-1",Rate:"15",Receive:"1500",RequestedOn:v.RequestedOn,Key:key},
  {SourceAccountID:source,DestinationAccountID:destination,Send:"100",Fee:"0",Rate:"15",Receive:"1500",RequestedOn:"2026-02-31",Key:key},
  {SourceAccountID:source,DestinationAccountID:destination,Send:"100",Fee:"0",Rate:"0",Receive:"0",RequestedOn:v.RequestedOn,Key:key},
 }{
  if _,e:=normalizeCreate(bad);!errors.Is(e,ErrInvalid){t.Fatalf("accepted %+v %v",bad,e)}
 }
}
func TestResolutionValidation(t *testing.T){
 order,_:=accounts.NewSetupKey()
 key,_:=accounts.NewSetupKey()
 v:=ResolveInput{OrderID:order,Action:"settled",Date:"2026-10-28",Key:key}
 n,e:=normalizeResolution(v)
 if e!=nil{t.Fatal(e)}
 other:=v;other.Action="cancelled"
 changed,e:=normalizeResolution(other)
 if e!=nil||changed.Hash==n.Hash{t.Fatal("resolution action omitted from fingerprint")}
 v.Action="paid"
 if _,e:=normalizeResolution(v);!errors.Is(e,ErrInvalid){t.Fatal("unsupported action accepted")}
 v.Action="settled";v.Date="2026-10-32"
 if _,e:=normalizeResolution(v);!errors.Is(e,ErrInvalid){t.Fatal("invalid date accepted")}
}

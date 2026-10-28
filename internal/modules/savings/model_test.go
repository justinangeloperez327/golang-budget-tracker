package savings

import (
 "errors"
 "testing"
 "time"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)
func TestSavingsInputsAndFingerprints(t *testing.T){
 savingsID,_:=accounts.NewSetupKey()
 fundingID,_:=accounts.NewSetupKey()
 key,_:=accounts.NewSetupKey()
 create:=CreateInput{Name:"  Emergency Fund  ",SavingsAccountID:savingsID,
  TargetAmount:"5000.00",TargetOn:"2027-05-31",Key:key}
 norm,err:=normalizeCreate(create)
 if err!=nil||norm.Name!="Emergency Fund"||norm.TargetMinor!=500000{t.Fatalf("normalized %+v %v",norm,err)}
 altered:=create;altered.TargetAmount="5000.01"
 other,err:=normalizeCreate(altered)
 if err!=nil||other.Hash==norm.Hash{t.Fatalf("hash insensitive to amount %v",err)}
 for _,input:=range []CreateInput{
  {Name:"",SavingsAccountID:savingsID,TargetAmount:"100",Key:key},
  {Name:"New",SavingsAccountID:"wrong",TargetAmount:"100",Key:key},
  {Name:"New",SavingsAccountID:savingsID,TargetAmount:"0",Key:key},
  {Name:"New",SavingsAccountID:savingsID,TargetAmount:"-1",Key:key},
  {Name:"New",SavingsAccountID:savingsID,TargetAmount:"1.000",Key:key},
  {Name:"New",SavingsAccountID:savingsID,TargetAmount:"1",TargetOn:"2026-02-31",Key:key},
 }{
  if _,err:=normalizeCreate(input);!errors.Is(err,ErrInvalid){t.Fatalf("accepted %+v %v",input,err)}
 }
 movement:=MovementInput{GoalID:savingsID,OtherAccountID:fundingID,Direction:"contribution",
  Amount:"5.25",OccurredOn:"2026-10-28",Key:key}
 m,err:=normalizeMovement(movement)
 if err!=nil||m.AmountMinor!=525{t.Fatalf("movement %+v %v",m,err)}
 moved:=movement;moved.Direction="withdrawal"
 n,err:=normalizeMovement(moved)
 if err!=nil||m.Hash==n.Hash{t.Fatalf("direction missing from fingerprint %v",err)}
 moved.Direction="refund"
 if _,err:=normalizeMovement(moved);!errors.Is(err,ErrInvalid){t.Fatal("unsupported direction accepted")}
 moved=movement;moved.Amount="0"
 if _,err:=normalizeMovement(moved);!errors.Is(err,ErrInvalid){t.Fatal("zero value accepted")}
}
func TestSavingsProgressAccuracy(t *testing.T){
 for _,tc:=range []struct{balance,target,remain int64; percent int}{
  {0,10000,10000,0},
  {2500,10000,7500,2500},
  {10000,10000,0,10000},
  {12500,10000,-2500,10000},
  {-500,10000,10500,0},
 }{
  remain,bps,err:=Progress(tc.balance,tc.target)
  if err!=nil||remain!=tc.remain||bps!=tc.percent{
   t.Fatalf("%+v resulted %d %d %v",tc,remain,bps,err)
  }
 }
 if _,_,err:=Progress(MaxMinor,1);err!=nil{t.Fatalf("overgoal balance can be shown: %v",err)}
 if _,_,err:=Progress(1,0);!errors.Is(err,ErrInvalid){t.Fatalf("zero target: %v",err)}
 now:=time.Date(2026,10,28,0,0,0,0,time.UTC)
 if Status(1000,2000,"2026-10-27",false,now)!="past target"{
  t.Fatal("past target status missing")
 }
 if Status(3000,2000,"2026-10-27",false,now)!="achieved"{
  t.Fatal("achieved status ignored")
 }
 if Status(3000,2000,"2026-10-27",true,now)!="archived"{
  t.Fatal("archive status ignored")
 }
}

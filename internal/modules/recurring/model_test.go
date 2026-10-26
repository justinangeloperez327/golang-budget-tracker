package recurring

import (
 "testing"
 "time"

 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestScheduleAnchors(t *testing.T){
 for _,tc:=range []struct{start,frequency string;index int;want string}{
  {"2026-01-31","monthly",1,"2026-02-28"},
  {"2026-01-31","monthly",2,"2026-03-31"},
  {"2024-02-29","yearly",1,"2025-02-28"},
  {"2024-02-29","yearly",4,"2028-02-29"},
  {"2026-01-31","quarterly",1,"2026-04-30"},
  {"2026-10-26","weekly",2,"2026-11-09"},
  {"2026-10-26","daily",3,"2026-10-29"},
 }{
  start,_:=time.Parse("2006-01-02",tc.start)
  got:=occurrenceAt(start,tc.frequency,tc.index).Format("2006-01-02")
  if got!=tc.want{t.Errorf("start=%s freq=%s index=%d got %s want %s",tc.start,tc.frequency,tc.index,got,tc.want)}
  schedule:=Schedule{StartOn:tc.start,Frequency:tc.frequency}
  if !IsDue(schedule,tc.want){t.Errorf("not recognized as due: %+v %s",tc,tc.want)}
 }
}
func TestInvalidScheduleAndAction(t *testing.T){
 key,_:=accounts.NewSetupKey()
 account,_:=accounts.NewSetupKey()
 cat,_:=accounts.NewSetupKey()
 original:=NewSchedule{
  Name:"Internet",Kind:"subscription",Currency:"AED",AccountID:account,
  CategoryID:cat,Amount:"125.25",Frequency:"monthly",StartOn:"2026-01-31",Key:key,
 }
 a,e:=normalizeSchedule(original)
 if e!=nil||a.AmountMinor!=12525{t.Fatalf("valid schedule failed %+v %v",a,e)}
 next:=original;next.Amount="125.26"
 b,e:=normalizeSchedule(next)
 if e!=nil||a.Hash==b.Hash{t.Fatal("money must contribute to fingerprint")}
 for _,v:=range []NewSchedule{
  {Name:"",Kind:"subscription",Currency:"AED",AccountID:account,CategoryID:cat,Amount:"1",Frequency:"monthly",StartOn:"2026-01-31",Key:key},
  {Name:"rent",Kind:"bill",Currency:"USD",AccountID:account,CategoryID:cat,Amount:"1",Frequency:"monthly",StartOn:"2026-01-31",Key:key},
  {Name:"rent",Kind:"bill",Currency:"AED",AccountID:account,CategoryID:cat,Amount:"0",Frequency:"monthly",StartOn:"2026-01-31",Key:key},
  {Name:"rent",Kind:"bill",Currency:"AED",AccountID:account,CategoryID:cat,Amount:"1",Frequency:"biweekly",StartOn:"2026-01-31",Key:key},
  {Name:"rent",Kind:"bill",Currency:"AED",AccountID:account,CategoryID:cat,Amount:"1",Frequency:"monthly",StartOn:"2026-02-31",Key:key},
  {Name:"rent",Kind:"bill",Currency:"AED",AccountID:account,CategoryID:cat,Amount:"1",Frequency:"monthly",StartOn:"2026-05-01",EndOn:"2026-04-30",Key:key},
 }{
  if _,err:=normalizeSchedule(v);err==nil{t.Fatalf("invalid schedule accepted %+v",v)}
 }
 action:=NewAction{ScheduleID:account,DueOn:"2026-10-26",Action:"skipped",Key:key}
 if _,err:=normalizeAction(action);err!=nil{t.Fatal(err)}
 action.Action="paid"
 if _,err:=normalizeAction(action);err==nil{t.Fatal("paid must have expense link")}
 action.TransactionID=cat
 if _,err:=normalizeAction(action);err!=nil{t.Fatal(err)}
 action.Action="skipped"
 if _,err:=normalizeAction(action);err==nil{t.Fatal("skipped cannot have expense link")}
}
func TestOccurrenceStatusesAndEndDate(t *testing.T){
 s:=Schedule{ID:"schedule",StartOn:"2026-10-01",EndOn:"2026-12-01",
  Frequency:"monthly",Name:"Water",Currency:"AED",AmountMinor:5000}
 actions:=map[string]Payment{"2026-10-01":{Action:"paid",TransactionID:"expense"}}
 rows:=project(s,"2026-09-01","2027-02-01","2026-11-15",actions)
 if len(rows)!=3||rows[0].Status!="paid"||rows[1].Status!="overdue"||rows[2].Status!="upcoming"{
  t.Fatalf("bad statuses %+v",rows)
 }
 s.Archived=true
 rows=project(s,"2026-09-01","2027-02-01","2026-11-15",actions)
 if len(rows)!=1||rows[0].Status!="paid"{t.Fatalf("archived schedule forecast appeared %+v",rows)}
}

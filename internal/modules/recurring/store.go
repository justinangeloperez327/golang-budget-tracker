package recurring

import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "sort"
 "time"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgconn"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

type Store struct{db *pgxpool.Pool}
func NewStore(db *pgxpool.Pool)*Store{return &Store{db:db}}

func lock(ctx context.Context,tx pgx.Tx,workspace,key string)error{
 _,err:=tx.Exec(ctx,`SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`,workspace,key)
 return err
}
func missing(err error)bool{return errors.Is(err,pgx.ErrNoRows)}
func conflictError(err error)error{
 var p *pgconn.PgError
 if errors.As(err,&p){
  if p.Code=="23505"{return ErrRecorded}
  if p.Code=="23503"{return ErrNotFound}
  if p.Code=="23514"{return ErrInvalid}
 }
 return err
}
func nullable(v string)any{if v==""{return nil};return v}

func (s *Store) Create(ctx context.Context,workspace string,v NewSchedule)(string,error){
 if !accounts.IsValidKey(workspace){return "",ErrInvalid}
 n,err:=normalizeSchedule(v)
 if err!=nil{return "",err}
 var id string
 err=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  if err:=lock(ctx,tx,workspace,"recurring-create:"+n.Key);err!=nil{return err}
  var previous []byte
  err:=tx.QueryRow(ctx,`SELECT id,fingerprint FROM recurring_schedules
   WHERE workspace_id=$1 AND idempotency_key=$2`,workspace,n.Key).Scan(&id,&previous)
  if err==nil{
   if !bytes.Equal(previous,n.Hash[:]){return ErrConflict}
   return nil
  }
  if !missing(err){return err}
  var currency,kind string
  var archived bool
  err=tx.QueryRow(ctx,`SELECT currency,kind,archived_at IS NOT NULL FROM accounts
   WHERE workspace_id=$1 AND id=$2 FOR SHARE`,workspace,n.AccountID).
   Scan(&currency,&kind,&archived)
  if missing(err){return ErrNotFound}
  if err!=nil{return err}
  if archived||currency!=n.Currency||kind=="loan"{return ErrInvalid}
  var categoryCurrency,categoryKind string
  err=tx.QueryRow(ctx,`SELECT currency,kind,archived_at IS NOT NULL
   FROM finance_categories WHERE workspace_id=$1 AND id=$2 FOR SHARE`,
   workspace,n.CategoryID).Scan(&categoryCurrency,&categoryKind,&archived)
  if missing(err){return ErrNotFound}
  if err!=nil{return err}
  if archived||categoryCurrency!=n.Currency||categoryKind!="expense"{return ErrInvalid}
  newID,err:=accounts.NewSetupKey()
  if err!=nil{return err}
  id=newID
  _,err=tx.Exec(ctx,`
   INSERT INTO recurring_schedules(id,workspace_id,idempotency_key,fingerprint,
    name,kind,currency,account_id,category_id,amount_minor,frequency,start_on,end_on)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13::date)`,
   id,workspace,n.Key,n.Hash[:],n.Name,n.Kind,n.Currency,n.AccountID,
   n.CategoryID,n.AmountMinor,n.Frequency,n.StartOn,nullable(n.EndOn))
  return err
 })
 return id,conflictError(err)
}

func (s *Store) Archive(ctx context.Context,workspace,id string)error{
 if !accounts.IsValidKey(workspace)||!accounts.IsValidKey(id){return ErrInvalid}
 tag,err:=s.db.Exec(ctx,`UPDATE recurring_schedules SET archived_at=now()
  WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL`,workspace,id)
 if err!=nil{return err}
 if tag.RowsAffected()==0{return ErrNotFound}
 return nil
}

func (s *Store) Record(ctx context.Context,workspace string,v NewAction)(string,error){
 if !accounts.IsValidKey(workspace){return "",ErrInvalid}
 n,err:=normalizeAction(v)
 if err!=nil{return "",err}
 var id string
 err=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  if err:=lock(ctx,tx,workspace,"recurring-action:"+n.Key);err!=nil{return err}
  var previous []byte
  err:=tx.QueryRow(ctx,`SELECT id,fingerprint FROM recurring_occurrence_actions
   WHERE workspace_id=$1 AND idempotency_key=$2`,workspace,n.Key).Scan(&id,&previous)
  if err==nil{
   if !bytes.Equal(previous,n.Hash[:]){return ErrConflict}
   return nil
  }
  if !missing(err){return err}
  if err:=lock(ctx,tx,workspace,"recurring-due:"+n.ScheduleID+":"+n.DueOn);err!=nil{return err}
  var schedule Schedule
  err=tx.QueryRow(ctx,`
   SELECT currency,account_id,category_id,amount_minor,frequency,
    start_on::text,COALESCE(end_on::text,''),archived_at IS NOT NULL
   FROM recurring_schedules WHERE workspace_id=$1 AND id=$2 FOR SHARE`,
   workspace,n.ScheduleID).
   Scan(&schedule.Currency,&schedule.AccountID,&schedule.CategoryID,&schedule.AmountMinor,
    &schedule.Frequency,&schedule.StartOn,&schedule.EndOn,&schedule.Archived)
  if missing(err){return ErrNotFound}
  if err!=nil{return err}
  if schedule.Archived||!IsDue(schedule,n.DueOn){return ErrInvalid}
  var exists string
  err=tx.QueryRow(ctx,`SELECT id FROM recurring_occurrence_actions
   WHERE workspace_id=$1 AND schedule_id=$2 AND due_on=$3::date`,
   workspace,n.ScheduleID,n.DueOn).Scan(&exists)
  if err==nil{return ErrRecorded}
  if !missing(err){return err}
  if n.Action=="paid"{
   var matched bool
   err=tx.QueryRow(ctx,`
    SELECT EXISTS(SELECT 1 FROM finance_transactions t
     JOIN finance_transaction_splits sp ON sp.transaction_id=t.id
      AND sp.workspace_id=t.workspace_id
     WHERE t.workspace_id=$1 AND t.id=$2 AND t.currency=$3
      AND t.account_id=$4 AND t.kind='expense'
      AND sp.category_id=$5 AND sp.amount_minor=$6)`,
    workspace,n.TransactionID,schedule.Currency,schedule.AccountID,
    schedule.CategoryID,schedule.AmountMinor).Scan(&matched)
   if err!=nil{return err}
   if !matched{return ErrInvalid}
  }
  newID,err:=accounts.NewSetupKey()
  if err!=nil{return err}
  id=newID
  _,err=tx.Exec(ctx,`
   INSERT INTO recurring_occurrence_actions(id,workspace_id,schedule_id,currency,
     due_on,action,expense_transaction_id,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5::date,$6,$7,$8,$9)`,
   id,workspace,n.ScheduleID,schedule.Currency,n.DueOn,n.Action,
   nullable(n.TransactionID),n.Key,n.Hash[:])
  return err
 })
 return id,conflictError(err)
}

func (s *Store) Schedules(ctx context.Context,workspace string)([]Schedule,error){
 if !accounts.IsValidKey(workspace){return nil,ErrInvalid}
 rows,err:=s.db.Query(ctx,`
  SELECT s.id,s.name,s.kind,s.currency,s.account_id,s.category_id,
   a.name,c.name,s.amount_minor,s.frequency,s.start_on::text,
   COALESCE(s.end_on::text,''),s.archived_at IS NOT NULL
  FROM recurring_schedules s
  JOIN accounts a ON a.id=s.account_id AND a.workspace_id=s.workspace_id
  JOIN finance_categories c ON c.id=s.category_id AND c.workspace_id=s.workspace_id
  WHERE s.workspace_id=$1
  ORDER BY (s.archived_at IS NOT NULL),s.created_at DESC,s.id DESC LIMIT 100`,workspace)
 if err!=nil{return nil,err}
 defer rows.Close()
 items:=make([]Schedule,0)
 for rows.Next(){
  var item Schedule
  if err:=rows.Scan(&item.ID,&item.Name,&item.Kind,&item.Currency,&item.AccountID,
   &item.CategoryID,&item.AccountName,&item.CategoryName,&item.AmountMinor,
   &item.Frequency,&item.StartOn,&item.EndOn,&item.Archived);err!=nil{return nil,err}
  items=append(items,item)
 }
 return items,rows.Err()
}

func (s *Store) Payments(ctx context.Context,workspace string)([]Payment,error){
 if !accounts.IsValidKey(workspace){return nil,ErrInvalid}
 rows,err:=s.db.Query(ctx,`
  SELECT x.schedule_id,s.name,x.currency,x.due_on::text,x.action,
   COALESCE(x.expense_transaction_id,''),s.amount_minor
  FROM recurring_occurrence_actions x
  JOIN recurring_schedules s ON s.id=x.schedule_id AND s.workspace_id=x.workspace_id
  WHERE x.workspace_id=$1
  ORDER BY x.created_at DESC,x.id DESC LIMIT 100`,workspace)
 if err!=nil{return nil,err}
 defer rows.Close()
 items:=make([]Payment,0)
 for rows.Next(){
  var p Payment
  if err:=rows.Scan(&p.ScheduleID,&p.ScheduleName,&p.Currency,&p.DueOn,
   &p.Action,&p.TransactionID,&p.AmountMinor);err!=nil{return nil,err}
  items=append(items,p)
 }
 return items,rows.Err()
}

// Upcoming evaluates a bounded window rather than writing future expenses.
// Recorded actions remain visible, while upcoming/overdue statuses are live.
func (s *Store) Upcoming(ctx context.Context,workspace string,now time.Time)([]Occurrence,error){
 schedules,err:=s.Schedules(ctx,workspace)
 if err!=nil{return nil,err}
 start:=now.UTC().Truncate(24*time.Hour)
 from:=start.AddDate(0,0,-30).Format("2006-01-02")
 to:=start.AddDate(0,0,90).Format("2006-01-02")
 today:=start.Format("2006-01-02")
 rows,err:=s.db.Query(ctx,`
  SELECT schedule_id,due_on::text,action,COALESCE(expense_transaction_id,'')
  FROM recurring_occurrence_actions
  WHERE workspace_id=$1 AND due_on BETWEEN $2::date AND $3::date`,workspace,from,to)
 if err!=nil{return nil,err}
 actions:=map[string]map[string]Payment{}
 for rows.Next(){
  var key,day string
  var p Payment
  if err:=rows.Scan(&key,&day,&p.Action,&p.TransactionID);err!=nil{
   rows.Close();return nil,err
  }
  if actions[key]==nil{actions[key]=map[string]Payment{}}
  actions[key][day]=p
 }
 err=rows.Err()
 rows.Close()
 if err!=nil{return nil,err}
 list:=make([]Occurrence,0)
 for _,s:=range schedules{
  list=append(list,project(s,from,to,today,actions[s.ID])...)
 }
 sort.Slice(list,func(i,j int)bool{
  if list[i].DueOn==list[j].DueOn{return list[i].ScheduleID<list[j].ScheduleID}
  return list[i].DueOn<list[j].DueOn
 })
 // Return the complete bounded window, up to 100 schedules * 121 days.
 if len(list)>500{list=list[:500]}
 return list,nil
}

func (s *Store) ValidateMigration(ctx context.Context)error{
 var ready bool
 err:=s.db.QueryRow(ctx,`SELECT EXISTS(SELECT 1 FROM app_metadata WHERE key='schema_marker' AND value='group11')`).Scan(&ready)
 if err!=nil{return err}
 if !ready{return fmt.Errorf("recurring migrations unavailable")}
 return nil
}

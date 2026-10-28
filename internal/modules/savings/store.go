package savings

import (
 "bytes"
 "context"
 "errors"
 "strconv"
 "time"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgconn"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

type Store struct{db *pgxpool.Pool}
func NewStore(db *pgxpool.Pool)*Store{return &Store{db:db}}

func lockKey(ctx context.Context,tx pgx.Tx,ws,key string)error{
 _,e:=tx.Exec(ctx,`SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`,ws,key)
 return e
}
func dbError(e error)error{
 var p *pgconn.PgError
 if errors.As(e,&p){
  switch p.Code{
  case "23505":return ErrAllocated
  case "23503":return ErrNotFound
  case "23514":return ErrInvalid
  }
 }
 return e
}
func optionalDate(d string)any{if d==""{return nil};return d}
type accountRow struct{ID,Kind,Currency string;Archived bool}
func lockAccounts(ctx context.Context,tx pgx.Tx,ws string,ids ...string)(map[string]accountRow,error){
 rows,e:=tx.Query(ctx,`
  SELECT id,kind,currency,archived_at IS NOT NULL FROM accounts
  WHERE workspace_id=$1 AND id=ANY($2::text[]) ORDER BY id FOR UPDATE`,ws,ids)
 if e!=nil{return nil,e}
 found:=make(map[string]accountRow,len(ids))
 for rows.Next(){
  var a accountRow
  if e:=rows.Scan(&a.ID,&a.Kind,&a.Currency,&a.Archived);e!=nil{rows.Close();return nil,e}
  found[a.ID]=a
 }
 e=rows.Err()
 rows.Close()
 if e!=nil{return nil,e}
 if len(found)!=len(ids){return nil,ErrNotFound}
 for _,id:=range ids{if found[id].Archived{return nil,ErrInvalid}}
 return found,nil
}
func balance(ctx context.Context,tx pgx.Tx,ws,accountID string)(int64,error){
 var raw string
 e:=tx.QueryRow(ctx,`
  SELECT COALESCE(SUM(l.debit_minor::numeric-l.credit_minor::numeric),0)::text
  FROM ledger_lines l WHERE l.workspace_id=$1 AND l.ledger_account_id=$2`,ws,accountID).Scan(&raw)
 if e!=nil{return 0,e}
 n,e:=strconv.ParseInt(raw,10,64)
 if e!=nil||n>MaxMinor||n< -MaxMinor{return 0,ErrOverflow}
 return n,nil
}

// Create assigns one EXISTING savings account to one goal. The existing
// opening balance is immediately visible as progress, without new journals.
func(s *Store)Create(ctx context.Context,ws string,input CreateInput)(string,error){
 if !accounts.IsValidKey(ws){return "",ErrInvalid}
 n,e:=normalizeCreate(input);if e!=nil{return "",e}
 var id string
 e=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  if e:=lockKey(ctx,tx,ws,"savings-create:"+n.Key);e!=nil{return e}
  var previous []byte
  e:=tx.QueryRow(ctx,`SELECT id,fingerprint FROM savings_goals
   WHERE workspace_id=$1 AND idempotency_key=$2`,ws,n.Key).Scan(&id,&previous)
  if e==nil{
   if !bytes.Equal(previous,n.Hash[:]){return ErrConflict}
   return nil
  }
  if !errors.Is(e,pgx.ErrNoRows){return e}
  aa,e:=lockAccounts(ctx,tx,ws,n.SavingsAccountID)
  if e!=nil{return e}
  if aa[n.SavingsAccountID].Kind!="savings"{return ErrInvalid}
  id,e=accounts.NewSetupKey()
  if e!=nil{return e}
  _,e=tx.Exec(ctx,`
   INSERT INTO savings_goals(id,workspace_id,currency,savings_account_id,
     name,target_minor,target_on,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5,$6,$7::date,$8,$9)`,
   id,ws,aa[n.SavingsAccountID].Currency,n.SavingsAccountID,
   n.Name,n.TargetMinor,optionalDate(n.TargetOn),n.Key,n.Hash[:])
  return e
 })
 if e!=nil{return "",dbError(e)}
 return id,nil
}

// Archive preserves all historical movements; the account is not closed and
// may not be silently reallocated to another goal.
func(s *Store)Archive(ctx context.Context,ws,id string)error{
 if !accounts.IsValidKey(ws)||!accounts.IsValidKey(id){return ErrInvalid}
 tag,e:=s.db.Exec(ctx,`UPDATE savings_goals SET archived_at=now()
  WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL`,ws,id)
 if e!=nil{return e}
 if tag.RowsAffected()==0{return ErrNotFound}
 return nil
}

// Move transfers between owned asset accounts, in one transaction with
// the immutable goal movement and ledger journal. No income/expense is posted.
func(s *Store)Move(ctx context.Context,ws string,input MovementInput)(string,error){
 if !accounts.IsValidKey(ws){return "",ErrInvalid}
 n,e:=normalizeMovement(input);if e!=nil{return "",e}
 var id string
 e=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  if e:=lockKey(ctx,tx,ws,"savings-move:"+n.Key);e!=nil{return e}
  var previous []byte
  e:=tx.QueryRow(ctx,`SELECT id,fingerprint FROM savings_goal_movements
   WHERE workspace_id=$1 AND idempotency_key=$2`,ws,n.Key).Scan(&id,&previous)
  if e==nil{
   if !bytes.Equal(previous,n.Hash[:]){return ErrConflict}
   return nil
  }
  if !errors.Is(e,pgx.ErrNoRows){return e}
  var goalAccount,currency string
  var archived bool
  e=tx.QueryRow(ctx,`SELECT savings_account_id,currency,archived_at IS NOT NULL
    FROM savings_goals WHERE workspace_id=$1 AND id=$2 FOR SHARE`,ws,n.GoalID).
    Scan(&goalAccount,&currency,&archived)
  if errors.Is(e,pgx.ErrNoRows){return ErrNotFound}
  if e!=nil{return e}
  if archived||goalAccount==n.OtherAccountID{return ErrInvalid}
  aa,e:=lockAccounts(ctx,tx,ws,goalAccount,n.OtherAccountID)
  if e!=nil{return e}
  asset:=func(kind string)bool{
   return kind=="bank"||kind=="cash"||kind=="savings"||kind=="ewallet"
  }
  if aa[goalAccount].Kind!="savings"||!asset(aa[n.OtherAccountID].Kind)||
   aa[n.OtherAccountID].Currency!=currency||aa[goalAccount].Currency!=currency{
   return ErrInvalid
  }
  // Prevent a transfer from silently reducing another active or archived
  // goal while recording a contribution only for this one.
  var dedicated bool
  e=tx.QueryRow(ctx,`SELECT EXISTS(SELECT 1 FROM savings_goals
   WHERE workspace_id=$1 AND savings_account_id=$2)`,ws,n.OtherAccountID).Scan(&dedicated)
  if e!=nil{return e}
  if dedicated{return ErrAllocated}
  source,target:=n.OtherAccountID,goalAccount
  if n.Direction=="withdrawal"{source,target=goalAccount,n.OtherAccountID}
  available,e:=balance(ctx,tx,ws,source)
  if e!=nil{return e}
  if available<n.AmountMinor{return ErrFunds}
  destinationBalance,e:=balance(ctx,tx,ws,target)
  if e!=nil{return e}
  if destinationBalance>MaxMinor-n.AmountMinor{return ErrOverflow}
  journalID,e:=ledger.PostInTx(ctx,tx,ws,currency,"goal:"+n.Key,[]ledger.Line{
   {AccountID:source,CreditMinor:n.AmountMinor},
   {AccountID:target,DebitMinor:n.AmountMinor},
  })
  if e!=nil{return e}
  id,e=accounts.NewSetupKey()
  if e!=nil{return e}
  _,e=tx.Exec(ctx,`
   INSERT INTO savings_goal_movements(id,workspace_id,goal_id,currency,
    other_account_id,direction,amount_minor,occurred_on,journal_id,
    idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8::date,$9,$10,$11)`,
   id,ws,n.GoalID,currency,n.OtherAccountID,n.Direction,n.AmountMinor,
   n.OccurredOn,journalID,n.Key,n.Hash[:])
  return e
 })
 if e!=nil{return "",dbError(e)}
 return id,nil
}
func(s *Store)Goals(ctx context.Context,ws string,now time.Time)([]Goal,error){
 if !accounts.IsValidKey(ws){return nil,ErrInvalid}
 rows,e:=s.db.Query(ctx,`
  SELECT g.id,g.name,g.savings_account_id,a.name,g.currency,g.target_minor,
    COALESCE(g.target_on::text,''),g.archived_at IS NOT NULL,
    COALESCE(SUM(l.debit_minor::numeric-l.credit_minor::numeric),0)::text
  FROM savings_goals g JOIN accounts a ON a.id=g.savings_account_id
   AND a.workspace_id=g.workspace_id
  LEFT JOIN ledger_lines l ON l.ledger_account_id=g.savings_account_id
   AND l.workspace_id=g.workspace_id AND l.currency=g.currency
  WHERE g.workspace_id=$1
  GROUP BY g.id,a.id
  ORDER BY g.created_at DESC,g.id DESC LIMIT 100`,ws)
 if e!=nil{return nil,e}
 defer rows.Close()
 out:=make([]Goal,0)
 for rows.Next(){
  var g Goal
  var raw string
  if e:=rows.Scan(&g.ID,&g.Name,&g.SavingsAccountID,&g.SavingsAccountName,
   &g.Currency,&g.TargetMinor,&g.TargetOn,&g.Archived,&raw);e!=nil{return nil,e}
  g.BalanceMinor,e=strconv.ParseInt(raw,10,64)
  if e!=nil||g.BalanceMinor>MaxMinor||g.BalanceMinor< -MaxMinor{return nil,ErrOverflow}
  g.RemainingMinor,g.ProgressBps,e=Progress(g.BalanceMinor,g.TargetMinor)
  if e!=nil{return nil,e}
  g.Status=Status(g.BalanceMinor,g.TargetMinor,g.TargetOn,g.Archived,now)
  out=append(out,g)
 }
 return out,rows.Err()
}
func(s *Store)Movements(ctx context.Context,ws string)([]Movement,error){
 if !accounts.IsValidKey(ws){return nil,ErrInvalid}
 rows,e:=s.db.Query(ctx,`
  SELECT m.id,m.goal_id,g.name,a.name,m.direction,m.currency,
    m.amount_minor,m.occurred_on::text
  FROM savings_goal_movements m
  JOIN savings_goals g ON g.id=m.goal_id AND g.workspace_id=m.workspace_id
  JOIN accounts a ON a.id=m.other_account_id AND a.workspace_id=m.workspace_id
  WHERE m.workspace_id=$1 ORDER BY m.created_at DESC,m.id DESC LIMIT 100`,ws)
 if e!=nil{return nil,e}
 defer rows.Close()
 out:=make([]Movement,0)
 for rows.Next(){
  var m Movement
  if e:=rows.Scan(&m.ID,&m.GoalID,&m.GoalName,&m.OtherAccountName,
    &m.Direction,&m.Currency,&m.AmountMinor,&m.OccurredOn);e!=nil{return nil,e}
  out=append(out,m)
 }
 return out,rows.Err()
}

package transfers

import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "strconv"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

type Store struct{ db *pgxpool.Pool }
func NewStore(pool *pgxpool.Pool)*Store {return &Store{db:pool}}

type accountRow struct {
 ID,Currency,Kind string
 Archived bool
}
func lockedAccounts(ctx context.Context,tx pgx.Tx,workspace string,ids ...string)(map[string]accountRow,error){
 rows,err:=tx.Query(ctx,`
  SELECT id,currency,kind,archived_at IS NOT NULL FROM accounts
  WHERE workspace_id=$1 AND id=ANY($2::text[])
  ORDER BY id FOR UPDATE`,workspace,ids)
 if err!=nil{return nil,err}
 result:=make(map[string]accountRow,len(ids))
 for rows.Next(){
  var a accountRow
  if err:=rows.Scan(&a.ID,&a.Currency,&a.Kind,&a.Archived);err!=nil{rows.Close();return nil,err}
  result[a.ID]=a
 }
 err=rows.Err()
 rows.Close()
 if err!=nil{return nil,err}
 if len(result)!=len(ids){return nil,ErrNotFound}
 for _,id:=range ids{
  if result[id].Archived{return nil,ErrInvalid}
 }
 return result,nil
}
func lockKey(ctx context.Context,tx pgx.Tx,workspace,key string)error{
 _,err:=tx.Exec(ctx,`SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`,workspace,key)
 return err
}
func existing(ctx context.Context,tx pgx.Tx,table,workspace,key string,fingerprint [32]byte)(string,error){
 var id string
 var stored []byte
 var query string
 switch table {
 case "transfer_operations":
  query=`SELECT id,fingerprint FROM transfer_operations WHERE workspace_id=$1 AND idempotency_key=$2`
 case "account_reconciliations":
  query=`SELECT id,fingerprint FROM account_reconciliations WHERE workspace_id=$1 AND idempotency_key=$2`
 default:return "",ErrInvalid
 }
 err:=tx.QueryRow(ctx,query,workspace,key).Scan(&id,&stored)
 if errors.Is(err,pgx.ErrNoRows){return "",nil}
 if err!=nil{return "",err}
 if !bytes.Equal(stored,fingerprint[:]){return "",ErrConflict}
 return id,nil
}
func systemAccount(ctx context.Context,tx pgx.Tx,workspace,currency,kind,key string)(string,error){
 id,err:=accounts.NewSetupKey()
 if err!=nil{return "",err}
 _,err=tx.Exec(ctx,`
  INSERT INTO ledger_accounts(id,workspace_id,currency,kind,system_key)
  VALUES($1,$2,$3,$4,$5)
  ON CONFLICT(workspace_id,currency,system_key) DO NOTHING`,id,workspace,currency,kind,key)
 if err!=nil{return "",err}
 err=tx.QueryRow(ctx,`
  SELECT id FROM ledger_accounts WHERE workspace_id=$1 AND currency=$2 AND system_key=$3
  `,workspace,currency,key).Scan(&id)
 return id,err
}
func post(ctx context.Context,tx pgx.Tx,workspace,currency,key string,lines []ledger.Line)(string,error){
 id,err:=ledger.PostInTx(ctx,tx,workspace,currency,key,lines)
 if errors.Is(err,ledger.ErrInvalid){return "",ErrInvalid}
 if errors.Is(err,ledger.ErrConflict){return "",ErrConflict}
 if errors.Is(err,ledger.ErrNotFound){return "",ErrNotFound}
 return id,err
}

// Transfer posts an internal movement without creating income/expense except
// an explicit fee. FX uses two balanced, currency-local clearing journals.
func (s *Store) Transfer(ctx context.Context,workspace string,input Input)(string,error){
 if !accounts.IsValidKey(workspace){return "",ErrInvalid}
 n,err:=normalizeTransfer(input)
 if err!=nil{return "",err}
 var id string
 err=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  if err:=lockKey(ctx,tx,workspace,"transfer:"+n.Key);err!=nil{return err}
  prior,err:=existing(ctx,tx,"transfer_operations",workspace,n.Key,n.Hash)
  if err!=nil{return err}
  if prior!=""{id=prior;return nil}
  aa,err:=lockedAccounts(ctx,tx,workspace,n.FromID,n.ToID)
  if err!=nil{return err}
  source,target:=aa[n.FromID],aa[n.ToID]
  if source.Kind=="credit"||source.Kind=="loan"||target.Kind=="loan"{
   return ErrInvalid
  }
  fx:=source.Currency!=target.Currency
  if fx!=(n.RateUnits!=0){return ErrInvalid}
  if !fx && n.Send!=n.Receive{return ErrInvalid}
  debitTo:=ledger.Line{AccountID:n.ToID,DebitMinor:n.Receive}
  creditFrom:=ledger.Line{AccountID:n.FromID,CreditMinor:n.Send+n.Fee}
  var sourceLines,targetLines []ledger.Line
  if fx{
   bridge,e:=systemAccount(ctx,tx,workspace,source.Currency,"equity","fx_clearing")
   if e!=nil{return e}
   sourceLines=[]ledger.Line{creditFrom,{AccountID:bridge,DebitMinor:n.Send}}
   bridgeTarget,e:=systemAccount(ctx,tx,workspace,target.Currency,"equity","fx_clearing")
   if e!=nil{return e}
   targetLines=[]ledger.Line{debitTo,{AccountID:bridgeTarget,CreditMinor:n.Receive}}
  }else{
   sourceLines=[]ledger.Line{creditFrom,debitTo}
  }
  if n.Fee>0{
   feeAccount,e:=systemAccount(ctx,tx,workspace,source.Currency,"expense","transfer_fee")
   if e!=nil{return e}
   sourceLines=append(sourceLines,ledger.Line{AccountID:feeAccount,DebitMinor:n.Fee})
  }
  sourceJournal,e:=post(ctx,tx,workspace,source.Currency,"transfer:"+n.Key,sourceLines)
  if e!=nil{return e}
  var targetJournal any
  if fx{
   v,e:=post(ctx,tx,workspace,target.Currency,"transfer-target:"+n.Key,targetLines)
   if e!=nil{return e}
   targetJournal=v
  }
  id,e=accounts.NewSetupKey()
  if e!=nil{return e}
  var rate any
  if fx{rate=n.RateUnits}
  _,e=tx.Exec(ctx,`
   INSERT INTO transfer_operations
   (id,workspace_id,idempotency_key,fingerprint,source_account_id,target_account_id,
    source_currency,target_currency,send_minor,receive_minor,fee_minor,rate_units,
    note,occurred_on,source_journal_id,target_journal_id)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
   id,workspace,n.Key,n.Hash[:],n.FromID,n.ToID,source.Currency,target.Currency,
   n.Send,n.Receive,n.Fee,rate,n.Note,n.Date,sourceJournal,targetJournal)
  return e
 })
 return id,err
}

func balanceInTx(ctx context.Context,tx pgx.Tx,workspace,id string,liability bool)(int64,error){
 var value string
 err:=tx.QueryRow(ctx,`
  SELECT COALESCE(SUM(CASE WHEN $3::boolean
   THEN l.credit_minor::numeric-l.debit_minor::numeric
   ELSE l.debit_minor::numeric-l.credit_minor::numeric END),0)::text
  FROM ledger_lines l
  WHERE l.workspace_id=$1 AND l.ledger_account_id=$2`,workspace,id,liability).Scan(&value)
 if err!=nil{return 0,err}
 n,err:=strconv.ParseInt(value,10,64)
 if err!=nil||n > ledger.MaxMinor || n < -ledger.MaxMinor{return 0,ErrOverflow}
 return n,nil
}

// Reconcile posts the difference to a dedicated equity adjustment account.
// Account row locking serializes observations with transfers and with other
// reconciliations, preserving the previous_balance snapshot.
func (s *Store) Reconcile(ctx context.Context,workspace string,input ReconcileInput)(string,error){
 if !accounts.IsValidKey(workspace){return "",ErrInvalid}
 n,err:=normalizeReconcile(input)
 if err!=nil{return "",err}
 var id string
 err=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  if err:=lockKey(ctx,tx,workspace,"reconcile:"+n.Key);err!=nil{return err}
  prior,err:=existing(ctx,tx,"account_reconciliations",workspace,n.Key,n.Hash)
  if err!=nil{return err}
  if prior!=""{id=prior;return nil}
  aa,err:=lockedAccounts(ctx,tx,workspace,n.AccountID)
  if err!=nil{return err}
  a:=aa[n.AccountID]
  current,err:=balanceInTx(ctx,tx,workspace,n.AccountID,a.Kind=="credit"||a.Kind=="loan")
  if err!=nil{return err}
  // Avoid overflow in the subtraction when an account is already negative.
  if current<0 && n.Observed > ledger.MaxMinor+current{return ErrOverflow}
  delta:=n.Observed-current
  if delta>ledger.MaxMinor || delta < -ledger.MaxMinor{return ErrOverflow}
  var journal any
  if delta!=0{
   equity,e:=systemAccount(ctx,tx,workspace,a.Currency,"equity","reconciliation_equity")
   if e!=nil{return e}
   amount:=delta
   if amount<0{amount=-amount}
   accountLine:=ledger.Line{AccountID:n.AccountID}
   equityLine:=ledger.Line{AccountID:equity}
   increasing:=delta>0
   liability:=a.Kind=="credit"||a.Kind=="loan"
   if increasing==liability {
    accountLine.CreditMinor=amount
    equityLine.DebitMinor=amount
   }else{
    accountLine.DebitMinor=amount
    equityLine.CreditMinor=amount
   }
   posted,e:=post(ctx,tx,workspace,a.Currency,"reconcile:"+n.Key,[]ledger.Line{accountLine,equityLine})
   if e!=nil{return e}
   journal=posted
  }
  id,e:=accounts.NewSetupKey()
  if e!=nil{return e}
  _,e=tx.Exec(ctx,`
   INSERT INTO account_reconciliations
   (id,workspace_id,idempotency_key,fingerprint,account_id,currency,
    previous_minor,observed_minor,delta_minor,note,occurred_on,journal_id)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
   id,workspace,n.Key,n.Hash[:],n.AccountID,a.Currency,current,n.Observed,delta,n.Note,n.Date,journal)
  return e
 })
 return id,err
}
func (s *Store) Transfers(ctx context.Context,workspace string)([]Transfer,error){
 if !accounts.IsValidKey(workspace){return nil,ErrInvalid}
 rows,err:=s.db.Query(ctx,`
  SELECT t.id,a.name,b.name,t.source_currency,t.target_currency,
   t.send_minor,t.receive_minor,t.fee_minor,COALESCE(t.rate_units,0),
   t.note,t.occurred_on::text
  FROM transfer_operations t JOIN accounts a ON a.id=t.source_account_id
  JOIN accounts b ON b.id=t.target_account_id WHERE t.workspace_id=$1
  ORDER BY t.occurred_on DESC,t.created_at DESC,t.id DESC LIMIT 50`,workspace)
 if err!=nil{return nil,err}
 defer rows.Close()
 result:=make([]Transfer,0)
 for rows.Next(){
  var v Transfer
  if err:=rows.Scan(&v.ID,&v.FromName,&v.ToName,&v.FromCurrency,&v.ToCurrency,&v.SendMinor,
   &v.ReceiveMinor,&v.FeeMinor,&v.RateUnits,&v.Note,&v.Date);err!=nil{return nil,err}
  result=append(result,v)
 }
 return result,rows.Err()
}
func (s *Store) Reconciliations(ctx context.Context,workspace string)([]Reconciliation,error){
 if !accounts.IsValidKey(workspace){return nil,ErrInvalid}
 rows,err:=s.db.Query(ctx,`
  SELECT r.id,a.name,r.currency,r.previous_minor,r.observed_minor,r.delta_minor,
   r.note,r.occurred_on::text
  FROM account_reconciliations r JOIN accounts a ON a.id=r.account_id
  WHERE r.workspace_id=$1 ORDER BY r.occurred_on DESC,r.created_at DESC,r.id DESC LIMIT 50`,workspace)
 if err!=nil{return nil,err}
 defer rows.Close()
 out:=make([]Reconciliation,0)
 for rows.Next(){
  var v Reconciliation
  if err:=rows.Scan(&v.ID,&v.AccountName,&v.Currency,&v.BeforeMinor,
   &v.ObservedMinor,&v.DeltaMinor,&v.Note,&v.Date);err!=nil{return nil,err}
  out=append(out,v)
 }
 return out,rows.Err()
}

func diagnostic(err error)error{
 if err==nil{return nil}
 return fmt.Errorf("finance settlement: %w",err)
}

package transactions

import (
 "bytes"
 "context"
 "crypto/sha256"
 "errors"
 "fmt"
 "strings"
 "time"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgconn"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

type Store struct { db *pgxpool.Pool }
func NewStore(db *pgxpool.Pool)*Store{return &Store{db:db}}

func (s *Store) CreateCategory(ctx context.Context,workspace,kind,currency,name string)(string,error){
 name=strings.TrimSpace(name)
 if !ValidID(workspace)||(kind!="income"&&kind!="expense")||
  (currency!="AED"&&currency!="PHP")||len(name)==0||len([]rune(name))>80||
  strings.ContainsAny(name,"\x00\r\n"){return "",ErrInvalid}
 id,err:=accounts.NewSetupKey()
 if err!=nil{return "",err}
 err=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  _,e:=tx.Exec(ctx,`INSERT INTO ledger_accounts(id,workspace_id,currency,kind,system_key)
   VALUES($1,$2,$3,$4,$5)`,id,workspace,currency,kind,"category:"+id)
  if e!=nil{return e}
  _,e=tx.Exec(ctx,`INSERT INTO finance_categories(id,workspace_id,name,kind,currency,ledger_account_id)
   VALUES($1,$2,$3,$4,$5,$1)`,id,workspace,name,kind,currency)
  return e
 })
 if err!=nil{
  var pgerr *pgconn.PgError
  if errors.As(err,&pgerr) && pgerr.Code=="23505"{return "",ErrConflict}
  return "",fmt.Errorf("create category: %w",err)
 }
 return id,nil
}

func (s *Store) Categories(ctx context.Context,workspace string)([]Category,error){
 if !ValidID(workspace){return nil,ErrInvalid}
 rows,err:=s.db.Query(ctx,`
 SELECT id,name,kind,currency,archived_at IS NOT NULL
 FROM finance_categories WHERE workspace_id=$1
 ORDER BY kind,currency,name,id`,workspace)
 if err!=nil{return nil,err}
 defer rows.Close()
 result:=make([]Category,0)
 for rows.Next(){
  var c Category
  if err:=rows.Scan(&c.ID,&c.Name,&c.Kind,&c.Currency,&c.Archived);err!=nil{return nil,err}
  result=append(result,c)
 }
 return result,rows.Err()
}

func (s *Store) ArchiveCategory(ctx context.Context,workspace,id string)error{
 if !ValidID(workspace)||!ValidID(id){return ErrNotFound}
 tag,err:=s.db.Exec(ctx,`
  UPDATE finance_categories SET archived_at=now()
  WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL`,workspace,id)
 if err!=nil{return err}
 if tag.RowsAffected()==0{return ErrNotFound}
 return nil
}

// Create atomically persists immutable metadata, category allocations and a
// balanced ledger journal. Matching retries never double-post.
func (s *Store) Create(ctx context.Context,workspace string,in Input)(string,error){
 if !ValidID(workspace){return "",ErrInvalid}
 n,err:=normalize(in)
 if err!=nil{return "",err}
 var result string
 err=pgx.BeginFunc(ctx,s.db,func(tx pgx.Tx)error{
  var previous []byte
  err:=tx.QueryRow(ctx,`SELECT id,request_hash FROM finance_transactions
   WHERE workspace_id=$1 AND idempotency_key=$2`,workspace,n.Key).
   Scan(&result,&previous)
  if err==nil{
   if !bytes.Equal(previous,n.Hash[:]){return ErrConflict}
   return nil
  }
  if !errors.Is(err,pgx.ErrNoRows){return err}
  var currency,accountKind string
  var archived bool
  err=tx.QueryRow(ctx,`
   SELECT currency,kind,archived_at IS NOT NULL FROM accounts
   WHERE id=$1 AND workspace_id=$2 FOR SHARE`,n.AccountID,workspace).
   Scan(&currency,&accountKind,&archived)
  if errors.Is(err,pgx.ErrNoRows){return ErrNotFound}
  if err!=nil{return err}
  if archived||accountKind=="loan"||(n.Kind=="income"&&accountKind=="credit"){return ErrInvalid}
  categoryKind:="expense"
  if n.Kind=="income"{categoryKind="income"}
  lines:=make([]ledger.Line,0,len(n.Splits)+1)
  accountLine:=ledger.Line{AccountID:n.AccountID}
  if n.Kind=="expense"{accountLine.CreditMinor=n.Total
  }else{accountLine.DebitMinor=n.Total}
  lines=append(lines,accountLine)
  for _,allocation:=range n.Splits{
   var kind,cur string
   var disabled bool
   err=tx.QueryRow(ctx,`
    SELECT kind,currency,archived_at IS NOT NULL FROM finance_categories
    WHERE workspace_id=$1 AND id=$2 FOR SHARE`,workspace,allocation.CategoryID).
    Scan(&kind,&cur,&disabled)
   if errors.Is(err,pgx.ErrNoRows){return ErrNotFound}
   if err!=nil{return err}
   if kind!=categoryKind||cur!=currency||(n.Kind!="refund"&&disabled){return ErrInvalid}
   line:=ledger.Line{AccountID:allocation.CategoryID}
   if n.Kind=="expense"{line.DebitMinor=allocation.Minor
   }else{line.CreditMinor=allocation.Minor}
   lines=append(lines,line)
  }
  if n.Kind=="refund"{
   var originalAccount,originalKind string
   err=tx.QueryRow(ctx,`
    SELECT account_id,kind FROM finance_transactions
    WHERE workspace_id=$1 AND id=$2 AND currency=$3 FOR UPDATE`,
    workspace,n.RefundOf,currency).Scan(&originalAccount,&originalKind)
   if errors.Is(err,pgx.ErrNoRows){return ErrNotFound}
   if err!=nil{return err}
   if originalKind!="expense"||originalAccount!=n.AccountID{return ErrInvalid}
   for _,allocation:=range n.Splits{
    var original,prior int64
    err=tx.QueryRow(ctx,`
     SELECT COALESCE((SELECT amount_minor FROM finance_transaction_splits
       WHERE transaction_id=$1 AND category_id=$2),0),
       COALESCE((SELECT SUM(s.amount_minor) FROM finance_transaction_splits s
       JOIN finance_transactions t ON t.id=s.transaction_id
       WHERE t.workspace_id=$3 AND t.refund_of=$1 AND s.category_id=$2),0)`,
       n.RefundOf,allocation.CategoryID,workspace).Scan(&original,&prior)
    if err!=nil{return err}
    if original==0||prior>original||allocation.Minor>original-prior{return ErrRefundLimit}
   }
  }
  keyHash:=sha256.Sum256([]byte(n.Key))
  posted,e:=ledger.PostInTx(ctx,tx,workspace,currency,"finance:"+fmt.Sprintf("%x",keyHash),lines)
  if e!=nil{
   if errors.Is(e,ledger.ErrConflict){return ErrConflict}
   if errors.Is(e,ledger.ErrNotFound){return ErrNotFound}
   return e
  }
  err=tx.QueryRow(ctx,`SELECT id,request_hash FROM finance_transactions
   WHERE workspace_id=$1 AND journal_id=$2`,workspace,posted).
   Scan(&result,&previous)
  if err==nil{
   if !bytes.Equal(previous,n.Hash[:]){return ErrConflict}
   return nil
  }
  if !errors.Is(err,pgx.ErrNoRows){return err}
  id,e:=accounts.NewSetupKey()
  if e!=nil{return e}
  result=id
  _,err=tx.Exec(ctx,`
   INSERT INTO finance_transactions
   (id,workspace_id,account_id,currency,kind,total_minor,journal_id,
    idempotency_key,request_hash,refund_of,note,occurred_on)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
   id,workspace,n.AccountID,currency,n.Kind,n.Total,posted,n.Key,n.Hash[:],
   nullable(n.RefundOf),n.Note,n.Date)
  if err!=nil{return err}
  for _,allocation:=range n.Splits{
   _,err=tx.Exec(ctx,`
    INSERT INTO finance_transaction_splits
    (transaction_id,workspace_id,currency,category_id,amount_minor)
    VALUES($1,$2,$3,$4,$5)`,id,workspace,currency,allocation.CategoryID,allocation.Minor)
   if err!=nil{return err}
  }
  return nil
 })
 if err!=nil{
  var pgerr *pgconn.PgError
  if errors.As(err,&pgerr) && pgerr.Code=="23514"{return "",ErrInvalid}
  return "",err
 }
 return result,nil
}
func nullable(v string)any{if v==""{return nil};return v}

func (s *Store) List(ctx context.Context,workspace string,filter Filter)([]Record,error){
 if !ValidID(workspace){return nil,ErrInvalid}
 f,err:=ValidateFilter(filter)
 if err!=nil{return nil,err}
 rows,err:=s.db.Query(ctx,`
 SELECT t.id,t.kind,t.account_id,a.name,t.currency,t.total_minor,t.note,
        t.occurred_on::text,COALESCE(t.refund_of,''),
        COALESCE((SELECT string_agg(c.name,', ' ORDER BY c.name)
          FROM finance_transaction_splits sp
          JOIN finance_categories c ON c.id=sp.category_id
          WHERE sp.transaction_id=t.id),'')
 FROM finance_transactions t
 JOIN accounts a ON a.id=t.account_id AND a.workspace_id=t.workspace_id
 WHERE t.workspace_id=$1
  AND ($2='' OR t.kind=$2)
  AND ($3='' OR t.account_id=$3)
  AND ($4='' OR t.note ILIKE '%'||$4||'%' OR a.name ILIKE '%'||$4||'%'
       OR EXISTS (SELECT 1 FROM finance_transaction_splits sp
         JOIN finance_categories c ON c.id=sp.category_id
         WHERE sp.transaction_id=t.id AND c.name ILIKE '%'||$4||'%'))
  AND ($5='' OR t.occurred_on >= NULLIF($5,'')::date)
  AND ($6='' OR t.occurred_on <= NULLIF($6,'')::date)
 ORDER BY t.occurred_on DESC,t.created_at DESC,t.id DESC
 LIMIT 25 OFFSET $7`,
 workspace,f.Kind,f.AccountID,f.Query,f.From,f.To,f.Page*25)
 if err!=nil{return nil,err}
 defer rows.Close()
 result:=make([]Record,0)
 for rows.Next(){
  var r Record
  var day string
  err:=rows.Scan(&r.ID,&r.Kind,&r.AccountID,&r.AccountName,&r.Currency,
   &r.TotalMinor,&r.Note,&day,&r.RefundOf,&r.Categories)
  if err!=nil{return nil,err}
  r.OccurredOn,err=time.Parse("2006-01-02",day)
  if err!=nil{return nil,err}
  result=append(result,r)
 }
 return result,rows.Err()
}

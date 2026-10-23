package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureOpeningTx is called inside the *same transaction* that creates a Group
// 6 account. Historical Group 6 accounts are safely imported by SyncOpenings.
func EnsureOpeningTx(ctx context.Context,tx pgx.Tx,workspaceID,accountID string) error {
	var currency,kind string
	var amount int64
	err:=tx.QueryRow(ctx,`
		SELECT a.currency,a.kind,o.amount_minor FROM account_openings o
		JOIN accounts a ON a.id=o.account_id AND a.workspace_id=o.workspace_id
		WHERE o.account_id=$1 AND o.workspace_id=$2 FOR UPDATE OF o`,
		accountID,workspaceID).Scan(&currency,&kind,&amount)
	if errors.Is(err,pgx.ErrNoRows){return ErrNotFound}
	if err!=nil{return err}
	var prior string
	err=tx.QueryRow(ctx,`
		SELECT account_id FROM ledger_opening_imports
		WHERE account_id=$1 AND workspace_id=$2`,accountID,workspaceID).Scan(&prior)
	if err==nil{return nil}
	if !errors.Is(err,pgx.ErrNoRows){return err}
	nature:="asset"
	if kind=="credit"||kind=="loan"{nature="liability"}
	if _,err:=tx.Exec(ctx,`
		INSERT INTO ledger_accounts(id,workspace_id,currency,kind,source_account_id)
		VALUES ($1,$2,$3,$4,$1)
		ON CONFLICT(id) DO NOTHING`,accountID,workspaceID,currency,nature);err!=nil{return err}
	var journalID any
	if amount>0 {
		equityID,err:=newID()
		if err!=nil{return err}
		if _,err:=tx.Exec(ctx,`
			INSERT INTO ledger_accounts(id,workspace_id,currency,kind,system_key)
			VALUES($1,$2,$3,'equity','opening_equity')
			ON CONFLICT(workspace_id,currency,system_key) DO NOTHING`,
			equityID,workspaceID,currency);err!=nil{return err}
		if err:=tx.QueryRow(ctx,`
			SELECT id FROM ledger_accounts WHERE workspace_id=$1 AND currency=$2
			AND system_key='opening_equity'`,workspaceID,currency).Scan(&equityID);err!=nil{return err}
		lines:=[]Line{
			{AccountID:accountID,DebitMinor:amount},
			{AccountID:equityID,CreditMinor:amount},
		}
		if nature=="liability"{
			lines=[]Line{
				{AccountID:equityID,DebitMinor:amount},
				{AccountID:accountID,CreditMinor:amount},
			}
		}
		journal,err:=postTx(ctx,tx,workspaceID,currency,"opening:"+accountID,"opening","",lines)
		if err!=nil{return err}
		journalID=journal
	}
	_,err=tx.Exec(ctx,`
		INSERT INTO ledger_opening_imports(account_id,workspace_id,journal_id)
		VALUES($1,$2,$3)`,accountID,workspaceID,journalID)
	return err
}

// SyncOpenings is repeatable and safe across competing server starts: each
// account's opening row is locked, and the import marker is unique. This runs
// before accepting HTTP requests and fails startup if any account is unsynced.
func SyncOpenings(ctx context.Context,pool *pgxpool.Pool)error{
	rows,err:=pool.Query(ctx,`
		SELECT o.workspace_id,o.account_id FROM account_openings o
		LEFT JOIN ledger_opening_imports i ON i.account_id=o.account_id
		WHERE i.account_id IS NULL ORDER BY o.account_id`)
	if err!=nil{return err}
	type record struct{ workspace,account string }
	var pending []record
	for rows.Next(){
		var item record
		if err:=rows.Scan(&item.workspace,&item.account);err!=nil{
			rows.Close()
			return err
		}
		pending=append(pending,item)
	}
	err=rows.Err()
	rows.Close()
	if err!=nil{return err}
	for _,item:=range pending {
		if err:=pgx.BeginFunc(ctx,pool,func(tx pgx.Tx)error{
			return EnsureOpeningTx(ctx,tx,item.workspace,item.account)
		});err!=nil{
			return fmt.Errorf("import opening account %s: %w",item.account,err)
		}
	}
	return nil
}

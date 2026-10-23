//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/auth"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

func ledgerFixture(t *testing.T)(context.Context,*ledger.Store,*accounts.Store,string,string){
	t.Helper()
	db:=openDB(t)
	ctx:=context.Background()
	user,err:=auth.NewStore(db.Pool(),time.Hour).Register(ctx,
		fmt.Sprintf("ledger-%d@example.test",time.Now().UnixNano()),
		"unique-strong-integration-password")
	if err!=nil{t.Fatal(err)}
	return ctx,ledger.NewStore(db.Pool()),accounts.NewStore(db.Pool()),user.Actor.WorkspaceID,user.Actor.UserID
}

func newAccount(t *testing.T,ctx context.Context,s *accounts.Store,workspace,name,kind,currency,opening string) string{
	t.Helper()
	key,err:=accounts.NewSetupKey()
	if err!=nil{t.Fatal(err)}
	id,err:=s.Create(ctx,workspace,accounts.CreateInput{
		Name:name,Kind:kind,Currency:currency,Opening:opening,SetupKey:key,
	})
	if err!=nil{t.Fatal(err)}
	return id
}

func balancesByAccount(t *testing.T,ctx context.Context,s *ledger.Store,workspace string)map[string]int64{
	t.Helper()
	balances,err:=s.Balances(ctx,workspace)
	if err!=nil{t.Fatal(err)}
	result:=make(map[string]int64)
	for _,b:=range balances{result[b.AccountID]=b.Minor}
	return result
}

func TestLedgerOpeningBalancesCurrencyAndSign(t *testing.T){
	ctx,s,accountsStore,workspace,_:=ledgerFixture(t)
	asset:=newAccount(t,ctx,accountsStore,workspace,"AED bank","bank","AED","1000.00")
	debt:=newAccount(t,ctx,accountsStore,workspace,"PHP credit","credit","PHP","200.25")
	zero:=newAccount(t,ctx,accountsStore,workspace,"Empty wallet","cash","AED","0.00")
	b:=balancesByAccount(t,ctx,s,workspace)
	if b[asset]!=100000||b[debt]!=20025||b[zero]!=0{t.Fatalf("bad opening balances: %+v",b)}
	history,err:=s.Recent(ctx,workspace,50)
	if err!=nil{t.Fatal(err)}
	if len(history)!=2{t.Fatalf("zero opening must not create journal: %+v",history)}
	for _,h:=range history{
		if h.Kind!="opening"||len(h.Lines)!=2 {t.Fatalf("invalid opening journal: %+v",h)}
		var debit,credit int64
		for _,l:=range h.Lines{debit+=l.DebitMinor;credit+=l.CreditMinor}
		if debit!=credit{t.Fatalf("unbalanced opening: %+v",h)}
		if h.Currency!="AED"&&h.Currency!="PHP"{t.Fatal("invalid currency")}
	}
	items,err:=accountsStore.List(ctx,workspace)
	if err!=nil{t.Fatal(err)}
	for _,item:=range items{
		if item.LedgerMinor!=b[item.ID] {t.Fatalf("UI balance mismatch: %+v",item)}
	}
}

func TestLedgerAdjustmentsConcurrencyIdempotencyReversal(t *testing.T){
	ctx,s,a,workspace,_:=ledgerFixture(t)
	cash:=newAccount(t,ctx,a,workspace,"Cash","cash","AED","1000.00")
	bank:=newAccount(t,ctx,a,workspace,"Bank","bank","AED","0.00")
	lines:=[]ledger.Line{
		{AccountID:bank,DebitMinor:2500},
		{AccountID:cash,CreditMinor:2500},
	}
	id,err:=s.PostAdjustment(ctx,workspace,"AED","transfer-test",lines)
	if err!=nil{t.Fatal(err)}
	if id==""{t.Fatal("journal id is empty")}
	replay,err:=s.PostAdjustment(ctx,workspace,"AED","transfer-test",
		[]ledger.Line{lines[1],lines[0]})
	if err!=nil||replay!=id{t.Fatalf("idempotent replay: %s %v",replay,err)}
	if _,err:=s.PostAdjustment(ctx,workspace,"AED","transfer-test",
		[]ledger.Line{{AccountID:bank,DebitMinor:1},{AccountID:cash,CreditMinor:1}});
		!errors.Is(err,ledger.ErrConflict){t.Fatalf("key reuse must conflict: %v",err)}
	var wg sync.WaitGroup
	ids:=make(chan string,12)
	errs:=make(chan error,12)
	for i:=0;i<12;i++{
		wg.Add(1)
		go func(){
			defer wg.Done()
			got,err:=s.PostAdjustment(context.Background(),workspace,"AED","concurrent-transfer",
				[]ledger.Line{{AccountID:bank,DebitMinor:100},{AccountID:cash,CreditMinor:100}})
			ids<-got
			errs<-err
		}()
	}
	wg.Wait()
	close(ids);close(errs)
	var parallelID string
	for err:=range errs{if err!=nil{t.Fatalf("concurrent post: %v",err)}}
	for posted:=range ids{
		if parallelID==""{parallelID=posted}
		if posted!=parallelID{t.Fatalf("duplicate concurrent journals %s != %s",parallelID,posted)}
	}
	b:=balancesByAccount(t,ctx,s,workspace)
	if b[cash]!=97400||b[bank]!=2600{t.Fatalf("double posting or wrong transfer: %+v",b)}
	reversal,err:=s.Reverse(ctx,workspace,id,"undo-transfer-test")
	if err!=nil{t.Fatal(err)}
	if reversal==""||reversal==id{t.Fatal("reversal must have its own ID")}
	again,err:=s.Reverse(ctx,workspace,id,"undo-transfer-test")
	if err!=nil||again!=reversal{t.Fatalf("idempotent reversal: %s %v",again,err)}
	if _,err:=s.Reverse(ctx,workspace,id,"different-reversal-key");!errors.Is(err,ledger.ErrReversed){
		t.Fatalf("double reversal should fail: %v",err)
	}
	if _,err:=s.Reverse(ctx,workspace,reversal,"reverse-a-reversal");!errors.Is(err,ledger.ErrInvalid){
		t.Fatalf("reversal of reversal should be rejected: %v",err)
	}
	b=balancesByAccount(t,ctx,s,workspace)
	if b[cash]!=99900||b[bank]!=100{t.Fatalf("reversal did not restore funds: %+v",b)}
	history,err:=s.Recent(ctx,workspace,30)
	if err!=nil{t.Fatal(err)}
	found:=false
	for _,h:=range history{
		if h.ID==reversal && h.Kind=="reversal"&&h.ReversesID!=nil&&*h.ReversesID==id {
			found=true
		}
	}
	if !found{t.Fatal("immutable linked reversal is missing")}
}

func TestLedgerWorkspaceCurrencyAndDatabaseGuards(t *testing.T){
	ctx,s,a,workspace,_:=ledgerFixture(t)
	asset:=newAccount(t,ctx,a,workspace,"AED savings","savings","AED","40.00")
	php:=newAccount(t,ctx,a,workspace,"PHP savings","savings","PHP","10.00")
	otherWorkspaceCtx,_,otherAccounts,otherWorkspace,_:=ledgerFixture(t)
	foreign:=newAccount(t,otherWorkspaceCtx,otherAccounts,otherWorkspace,"Other bank","bank","AED","12.00")
	baseline:=balancesByAccount(t,ctx,s,workspace)
	for _,tc:=range []struct {
		currency,key string
		lines []ledger.Line
	}{
		{"AED","mixed-currency",[]ledger.Line{{AccountID:asset,DebitMinor:50},{AccountID:php,CreditMinor:50}}},
		{"AED","foreign-workspace",[]ledger.Line{{AccountID:asset,DebitMinor:50},{AccountID:foreign,CreditMinor:50}}},
	}{
		if _,err:=s.PostAdjustment(ctx,workspace,tc.currency,tc.key,tc.lines);!errors.Is(err,ledger.ErrNotFound){
			t.Fatalf("cross-currency/workspace posting accepted or wrong error: %v",err)
		}
	}
	if _,err:=s.Reverse(ctx,workspace,"00000000000000000000000000000000","missing-reversal");!errors.Is(err,ledger.ErrNotFound){
		t.Fatalf("missing reversal: %v",err)
	}
	if _,err:=s.Recent(ctx,otherWorkspace,10);err!=nil{t.Fatal(err)}
	if len(baseline)!=2{t.Fatalf("unusual asset list %+v",baseline)}
	if balance:=balancesByAccount(t,ctx,s,workspace);balance[asset]!=baseline[asset]||balance[php]!=baseline[php]{
		t.Fatal("failed transaction changed balances")
	}
	db:=openDB(t)
	// An unsealed journal without balanced lines cannot commit.
	err:=pgx.BeginFunc(ctx,db.Pool(),func(tx pgx.Tx)error{
		_,e:=tx.Exec(ctx,`INSERT INTO ledger_journals
			(id,workspace_id,currency,idempotency_key,kind,fingerprint)
			VALUES($1,$2,'AED','direct-unbalanced','adjustment',$3)`,
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",workspace,make([]byte,32))
		return e
	})
	if err==nil{t.Fatal("database committed empty/unsealed journal")}
	// Cannot append a line to an already sealed journal.
	var postedID string
	if err:=db.Pool().QueryRow(ctx,`
		SELECT id FROM ledger_journals WHERE workspace_id=$1 AND kind='opening'
		AND currency='AED' LIMIT 1`,workspace).Scan(&postedID);err!=nil{t.Fatal(err)}
	if _,err:=db.Pool().Exec(ctx,`
		INSERT INTO ledger_lines(journal_id,ledger_account_id,workspace_id,currency,debit_minor)
		VALUES($1,$2,$3,'AED',1)`,postedID,asset,workspace);err==nil{
		t.Fatal("database allowed appending to a sealed journal")
	}
	if _,err:=db.Pool().Exec(ctx,`UPDATE ledger_journals SET kind='adjustment' WHERE id=$1`,postedID);err==nil{
		t.Fatal("database allowed journal rewrite")
	}
	if _,err:=db.Pool().Exec(ctx,`DELETE FROM ledger_lines WHERE journal_id=$1`,postedID);err==nil{
		t.Fatal("database allowed deleting journal lines")
	}
}

func TestLedgerBackfillGroup6OpeningsIsRepeatable(t *testing.T){
	ctx,s,_,workspace,_:=ledgerFixture(t)
	db:=openDB(t)
	id,key:=fmt.Sprintf("%032x",time.Now().UnixNano()),fmt.Sprintf("%032x",time.Now().UnixNano()+1)
	_,err:=db.Pool().Exec(ctx,`
		INSERT INTO accounts(id,workspace_id,name,kind,currency,setup_key)
		VALUES($1,$2,'Legacy savings','savings','PHP',$3)`,id,workspace,key)
	if err!=nil{t.Fatal(err)}
	_,err=db.Pool().Exec(ctx,`
		INSERT INTO account_openings(account_id,workspace_id,currency,amount_minor)
		VALUES($1,$2,'PHP',8000)`,id,workspace)
	if err!=nil{t.Fatal(err)}
	for i:=0;i<2;i++{
		if err:=ledger.SyncOpenings(ctx,db.Pool());err!=nil{t.Fatalf("sync #%d: %v",i,err)}
	}
	if b:=balancesByAccount(t,ctx,s,workspace);b[id]!=8000{
		t.Fatalf("opening sync missing or duplicated: %+v",b)
	}
	var count int
	if err:=db.Pool().QueryRow(ctx,`SELECT COUNT(*) FROM ledger_opening_imports WHERE account_id=$1`,id).Scan(&count);err!=nil||count!=1 {
		t.Fatalf("opening import count %d %v",count,err)
	}
}

func TestLedgerReadEndpointsRequireSessionAndScope(t *testing.T){
	handler:=uiFixture(t)
	alice,cookie:=registration(t,handler,fmt.Sprintf("ledger-api-%d@example.test",time.Now().UnixNano()))
	if rec:=formRequest(handler,"GET","/api/v1/ledger/balances",nil,nil,false,"");rec.Code!=http.StatusUnauthorized {
		t.Fatalf("anonymous ledger balance access: %d",rec.Code)
	}
	db:=openDB(t)
	_,err:=accounts.NewStore(db.Pool()).Create(context.Background(),alice.WorkspaceID,
		accounts.CreateInput{Name:"Bank",Kind:"bank",Currency:"AED",Opening:"45.00",SetupKey:fmt.Sprintf("%032x",time.Now().UnixNano())})
	if err!=nil{t.Fatal(err)}
	for _,route:=range []string{"/api/v1/ledger/balances","/api/v1/ledger/journals"} {
		rec:=formRequest(handler,"GET",route,nil,cookie,false,"")
		if rec.Code!=http.StatusOK||!strings.Contains(rec.Body.String(),"AED"){
			t.Fatalf("ledger read endpoint %s: %d %s",route,rec.Code,rec.Body.String())
		}
	}
}

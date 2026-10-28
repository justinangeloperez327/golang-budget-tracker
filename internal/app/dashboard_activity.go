package app

import (
 "context"

 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)
func dashboardRecent(ctx context.Context,db *pgxpool.Pool,ws string)([]transactions.Record,error){
 records,e:=transactions.NewStore(db).List(ctx,ws,transactions.Filter{})
 if e!=nil{return nil,e}
 if len(records)>8{records=records[:8]}
 return records,nil
}

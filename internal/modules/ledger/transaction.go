package ledger

import (
 "context"
 "github.com/jackc/pgx/v5"
)

// PostInTx shares the caller's transaction boundary with a domain record.
// It never commits independently: failure of either record rolls back both.
func PostInTx(ctx context.Context, tx pgx.Tx, workspace, currency, key string, lines []Line) (string, error) {
 return postTx(ctx, tx, workspace, currency, key, "adjustment", "", lines)
}

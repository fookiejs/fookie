package runtime

import (
	"context"
	"database/sql"
)

type ctxTxKey int

const txKey ctxTxKey = 1

func withTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, txKey, tx)
}

func txFromCtx(ctx context.Context) *sql.Tx {
	if v, ok := ctx.Value(txKey).(*sql.Tx); ok {
		return v
	}
	return nil
}

type dbExecer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func (e *Executor) execer(ctx context.Context) dbExecer {
	if tx := txFromCtx(ctx); tx != nil {
		return tx
	}
	return e.db
}

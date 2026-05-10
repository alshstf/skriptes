package importer

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// txQuerier адаптирует pgx.Tx под общий интерфейс querier.
// Возвращаемый Exec упаковывает pgconn.CommandTag в pgconnTag (см. upsert.go).
type txQuerier struct{ tx pgx.Tx }

func (q txQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.tx.QueryRow(ctx, sql, args...)
}

func (q txQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconnTag, error) {
	tag, err := q.tx.Exec(ctx, sql, args...)
	return ctagWrap(tag), err
}

type ctagWrap pgconn.CommandTag

func (c ctagWrap) String() string { return pgconn.CommandTag(c).String() }

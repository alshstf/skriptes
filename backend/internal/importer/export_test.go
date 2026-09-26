package importer

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// Двухуровневый кэш — внешним тестам (importer_test): id из откаченной транзакции
// записи не должен попадать в общий кэш.
type TestCaches = cacheSet

func NewTestCaches() *TestCaches { return newCaches() }

func (c *cacheSet) EnsureAuthorTx(ctx context.Context, tx pgx.Tx, a inpx.Author) (int64, error) {
	return c.ensureAuthor(ctx, txQuerier{tx}, a)
}

func (c *cacheSet) CommitStaged() { c.commitStaged() }
func (c *cacheSet) DropStaged()   { c.dropStaged() }

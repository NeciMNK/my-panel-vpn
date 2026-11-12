package database

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	txConflictAttempts = 5
	txConflictBackoff  = 25 * time.Millisecond
)

// IsTxConflict reports a transaction Postgres aborted only because a concurrent
// one won (deadlock 40P01, serialization failure 40001): rerunning it is safe.
func IsTxConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}

// RetryTxConflict reruns tx, which must open and commit its own transaction,
// while Postgres aborts it as a conflict with another writer (e.g. a second master).
func RetryTxConflict(tx func() error) error {
	var err error
	for attempt := 1; ; attempt++ {
		if err = tx(); !IsTxConflict(err) || attempt == txConflictAttempts {
			return err
		}
		jitter := time.Duration(rand.Int64N(int64(txConflictBackoff)))
		time.Sleep(time.Duration(attempt)*txConflictBackoff + jitter)
	}
}

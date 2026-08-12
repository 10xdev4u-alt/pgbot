// Package collect runs the read-only diagnostic SQL and turns it into a
// model.Context. Counters (pg_stat_database, pg_stat_wal, IO) are sampled twice
// — each sample in its own short transaction — and rate-computed; gauges
// (activity, locks, tables, indexes, settings, replication) are read once.
package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgrundev/pgbot/internal/conn"
)

//go:embed sql/health.sql
var sqlHealth string

//go:embed sql/activity.sql
var sqlActivity string

//go:embed sql/locks.sql
var sqlLocks string

//go:embed sql/queries.sql
var sqlQueries string

//go:embed sql/tables.sql
var sqlTables string

//go:embed sql/indexes.sql
var sqlIndexes string

//go:embed sql/wal.sql
var sqlWAL string

//go:embed sql/io_bgwriter.sql
var sqlIOBgwriter string

//go:embed sql/io_checkpointer.sql
var sqlIOCheckpointer string

//go:embed sql/settings.sql
var sqlSettings string

//go:embed sql/replication.sql
var sqlReplication string

// Raw counter samples. Each is read twice; the runner keeps sample A and B and
// hands both to the assembler, which computes rates via the rate package.

type healthSample struct {
	Numbackends  int32      `db:"numbackends"`
	XactCommit   int64      `db:"xact_commit"`
	XactRollback int64      `db:"xact_rollback"`
	BlksRead     int64      `db:"blks_read"`
	BlksHit      int64      `db:"blks_hit"`
	TupReturned  int64      `db:"tup_returned"`
	TupFetched   int64      `db:"tup_fetched"`
	TupInserted  int64      `db:"tup_inserted"`
	TupUpdated   int64      `db:"tup_updated"`
	TupDeleted   int64      `db:"tup_deleted"`
	Deadlocks    int64      `db:"deadlocks"`
	TempBytes    int64      `db:"temp_bytes"`
	StatsReset   *time.Time `db:"stats_reset"`
}

type walSample struct {
	WalRecords     int64 `db:"wal_records"`
	WalBytes       int64 `db:"wal_bytes"`
	WalBuffersFull int64 `db:"wal_buffers_full"`
}

type ioSample struct {
	CheckpointsTimed int64 `db:"checkpoints_timed"`
	CheckpointsReq   int64 `db:"checkpoints_req"`
	BuffersWritten   int64 `db:"buffers_written"`
	BackendFsyncs    int64 `db:"backend_fsyncs"`
}

// queryRow / tableRow / indexRow / replRow / activityRow / lockRow are the
// gauge shapes scanned straight from their SQL.

type activityRow struct {
	State         string  `db:"state"`
	WaitEventType string  `db:"wait_event_type"`
	N             int     `db:"n"`
	MaxXactAgeS   float64 `db:"max_xact_age_s"`
	MaxActiveAgeS float64 `db:"max_active_age_s"`
}

type lockRow struct {
	BlockedPID    int32   `db:"blocked_pid"`
	BlockingPIDs  []int32 `db:"blocking_pids"`
	WaitEventType string  `db:"wait_event_type"`
	BlockedWaitS  float64 `db:"blocked_wait_s"`
	BlockedQuery  string  `db:"blocked_query"`
}

type queryRow struct {
	QueryID        int64   `db:"queryid"`
	Query          string  `db:"query"`
	Calls          int64   `db:"calls"`
	TotalMS        float64 `db:"total_ms"`
	MeanMS         float64 `db:"mean_ms"`
	MaxMS          float64 `db:"max_ms"`
	Rows           int64   `db:"rows"`
	SharedBlksHit  int64   `db:"shared_blks_hit"`
	SharedBlksRead int64   `db:"shared_blks_read"`
	WalBytes       int64   `db:"wal_bytes"`
}

type tableRow struct {
	Schema           string     `db:"schema"`
	Table            string     `db:"table"`
	TotalBytes       int64      `db:"total_bytes"`
	LiveTuples       int64      `db:"live_tuples"`
	DeadTuples       int64      `db:"dead_tuples"`
	SeqScans         int64      `db:"seq_scans"`
	IndexScans       int64      `db:"index_scans"`
	ModsSinceAnalyze int64      `db:"mods_since_analyze"`
	LastVacuum       *time.Time `db:"last_vacuum"`
	LastAutovacuum   *time.Time `db:"last_autovacuum"`
}

type indexRow struct {
	Schema     string `db:"schema"`
	Table      string `db:"table"`
	Index      string `db:"index"`
	Scans      int64  `db:"scans"`
	Bytes      int64  `db:"bytes"`
	Definition string `db:"definition"`
	IsPrimary  bool   `db:"is_primary"`
	IsUnique   bool   `db:"is_unique"`
}

type settingRow struct {
	Name    string `db:"name"`
	Setting string `db:"setting"`
}

type replRow struct {
	ClientAddr string `db:"client_addr"`
	State      string `db:"state"`
	SyncState  string `db:"sync_state"`
	WriteLagB  int64  `db:"write_lag_bytes"`
	FlushLagB  int64  `db:"flush_lag_bytes"`
	ReplayLagB int64  `db:"replay_lag_bytes"`
}

// queryOne runs a single-row query in its own read-only transaction.
func queryOne[T any](ctx context.Context, t *conn.Target, sql string, args ...any) (T, error) {
	var out T
	err := t.ReadOnlyTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		out, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByNameLax[T])
		return err
	})
	return out, err
}

// queryMany runs a multi-row query in its own read-only transaction.
func queryMany[T any](ctx context.Context, t *conn.Target, sql string, args ...any) ([]T, error) {
	var out []T
	err := t.ReadOnlyTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
		return err
	})
	return out, err
}

// scalar reads a single scalar in its own read-only transaction.
func scalar[T any](ctx context.Context, t *conn.Target, sql string, args ...any) (T, error) {
	var out T
	err := t.ReadOnlyTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, sql, args...).Scan(&out)
	})
	return out, err
}

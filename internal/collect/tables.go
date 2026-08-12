package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/tables.sql
var sqlTables string

// tables = pg_stat_user_tables plus the total database size.
type tablesCollector struct{}

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

type tablesSample struct {
	Rows   []tableRow
	DBSize int64
}

func (tablesCollector) Name() string                     { return "tables" }
func (tablesCollector) Kind() Kind                       { return KindGauge }
func (tablesCollector) Available(conn.Capabilities) bool { return true }

func (tablesCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	rows, err := queryMany[tableRow](ctx, t, sqlTables)
	if err != nil {
		return nil, err
	}
	size, err := scalar[int64](ctx, t, `SELECT pg_database_size(current_database())`)
	if err != nil {
		return nil, err
	}
	return tablesSample{Rows: rows, DBSize: size}, nil
}

func (tablesCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	ts, ok := s.A.(tablesSample)
	if s.Err != nil || !ok {
		c.Tables = &model.Tables{Section: unavail(s.Err, "pg_stat_user_tables unavailable")}
		return
	}
	tbl := &model.Tables{Section: model.Section{Exactness: model.ExactnessScraped}, DBSizeBytes: ts.DBSize}
	for _, r := range ts.Rows {
		dead := 0.0
		if tot := r.LiveTuples + r.DeadTuples; tot > 0 {
			dead = float64(r.DeadTuples) / float64(tot)
		}
		tbl.Top = append(tbl.Top, model.TableStat{
			Schema: r.Schema, Name: r.Table, TotalBytes: r.TotalBytes,
			LiveTuples: r.LiveTuples, DeadTuples: r.DeadTuples, DeadRatio: round4(dead),
			SeqScans: r.SeqScans, IndexScans: r.IndexScans, ModsSinceAnalyze: r.ModsSinceAnalyze,
			LastVacuum: r.LastVacuum, LastAutovac: r.LastAutovacuum,
		})
	}
	c.Tables = tbl
}

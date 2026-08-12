package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/indexes.sql
var sqlIndexes string

// indexes = pg_stat_user_indexes. Zero-scan, non-trivial indexes that don't
// back a primary key or unique constraint become the unused-index finding.
type indexesCollector struct{}

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

func (indexesCollector) Name() string                     { return "indexes" }
func (indexesCollector) Kind() Kind                       { return KindGauge }
func (indexesCollector) Available(conn.Capabilities) bool { return true }

func (indexesCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[indexRow](ctx, t, sqlIndexes)
}

func (indexesCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]indexRow)
	if s.Err != nil || !ok {
		c.Indexes = &model.Indexes{Section: unavail(s.Err, "pg_stat_user_indexes unavailable")}
		return
	}
	idx := &model.Indexes{Section: model.Section{Exactness: model.ExactnessScraped}, Total: len(rows)}
	for _, r := range rows {
		st := model.IndexStat{Schema: r.Schema, Table: r.Table, Name: r.Index, Scans: r.Scans, Bytes: r.Bytes, Definition: r.Definition}
		if r.Scans == 0 && r.Bytes > 16384 && !r.IsPrimary && !r.IsUnique && len(idx.Unused) < 50 {
			idx.Unused = append(idx.Unused, st)
		}
		if len(idx.Largest) < 10 {
			idx.Largest = append(idx.Largest, st) // rows arrive largest-first
		}
	}
	c.Indexes = idx
}

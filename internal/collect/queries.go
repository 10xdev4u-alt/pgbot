package collect

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/rate"
)

//go:embed sql/queries.sql
var sqlQueries string

// queries = top of pg_stat_statements, cumulative since stats reset. The
// temporal view comes from the baseline diff, not a short in-process sample, so
// this is a single read (KindGauge) even though the columns are counters.
type queriesCollector struct{}

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

func (queriesCollector) Name() string { return "queries" }
func (queriesCollector) Kind() Kind   { return KindGauge }
func (queriesCollector) Available(caps conn.Capabilities) bool {
	return caps.HasStatStatements
}

func (queriesCollector) Sample(ctx context.Context, t *conn.Target, caps conn.Capabilities) (any, error) {
	// The version-appropriate total-time column comes from a fixed allowlist
	// (never user input); %% in the SQL escapes the ILIKE literal.
	return queryMany[queryRow](ctx, t, fmt.Sprintf(sqlQueries, caps.StatStatementsTotalCol()))
}

func (queriesCollector) Assemble(c *model.Context, caps conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	if !caps.HasStatStatements {
		// A large fraction of first runs (especially RDS/Aurora) land here, so make
		// it a first-class, provider-specific instruction rather than a dead end.
		c.Queries = &model.Queries{Enabled: false, Section: model.Section{
			Exactness: model.ExactnessUnavailable,
			Reason:    "pg_stat_statements not enabled — " + caps.Provider.PgssInstructions(),
		}}
		return
	}
	rows, ok := s.A.([]queryRow)
	if s.Err != nil || !ok {
		c.Queries = &model.Queries{Enabled: true, Section: unavail(s.Err, "pg_stat_statements read failed")}
		return
	}
	q := &model.Queries{Enabled: true, Section: model.Section{Exactness: model.ExactnessCumulative}}
	for _, r := range rows {
		item := model.QueryStat{
			QueryID: r.QueryID, Query: r.Query, Calls: r.Calls,
			TotalMS: round2(r.TotalMS), MeanMS: round4(r.MeanMS), MaxMS: round2(r.MaxMS),
			Rows: r.Rows, WALBytes: r.WalBytes,
		}
		if tot := r.SharedBlksHit + r.SharedBlksRead; tot > 0 {
			item.CacheHit = round4p(rate.Ptr(float64(r.SharedBlksHit) / float64(tot)))
		}
		q.Top = append(q.Top, item)
	}
	c.Queries = q
}

package collect

import (
	"context"
	"strings"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

// progress = the pg_stat_progress_* views unioned into one list of in-flight
// maintenance operations. Live truth (a vacuum 60% through heap-scan), not
// inference — and the companion to index_invalid: a CREATE INDEX CONCURRENTLY
// that shows here is still running, not failed. Gauge; views are version-gated.
type progressCollector struct{}

type progressRow struct {
	PID       int      `db:"pid"`
	Operation string   `db:"operation"`
	Relation  string   `db:"relation"`
	Phase     string   `db:"phase"`
	Pct       *float64 `db:"pct"`
}

func (progressCollector) Name() string                     { return "progress" }
func (progressCollector) Kind() Kind                       { return KindGauge }
func (progressCollector) Available(conn.Capabilities) bool { return true } // vacuum progress is PG9.6+

func (progressCollector) Sample(ctx context.Context, t *conn.Target, caps conn.Capabilities) (any, error) {
	// Each SELECT is normalized to (pid, operation, relation, phase, pct). relid
	// becomes a schema-qualified name (no data); 0 → NULL. Version gates match when
	// each progress view was introduced.
	parts := []string{
		`SELECT pid, 'vacuum' AS operation, nullif(relid,0)::regclass::text AS relation, phase,
		   CASE WHEN heap_blks_total>0 THEN round(100.0*heap_blks_scanned/heap_blks_total,1) ELSE NULL END AS pct
		 FROM pg_stat_progress_vacuum`,
	}
	if caps.VersionNum >= 120000 {
		parts = append(parts,
			`SELECT pid, 'create_index', nullif(relid,0)::regclass::text, phase,
			   CASE WHEN blocks_total>0 THEN round(100.0*blocks_done/blocks_total,1) ELSE NULL END
			 FROM pg_stat_progress_create_index`,
			`SELECT pid, 'cluster', nullif(relid,0)::regclass::text, phase,
			   CASE WHEN heap_blks_total>0 THEN round(100.0*heap_blks_scanned/heap_blks_total,1) ELSE NULL END
			 FROM pg_stat_progress_cluster`)
	}
	if caps.VersionNum >= 130000 {
		parts = append(parts,
			`SELECT pid, 'analyze', nullif(relid,0)::regclass::text, phase,
			   CASE WHEN sample_blks_total>0 THEN round(100.0*sample_blks_scanned/sample_blks_total,1) ELSE NULL END
			 FROM pg_stat_progress_analyze`,
			`SELECT pid, 'basebackup', NULL::text, phase,
			   CASE WHEN backup_total>0 THEN round(100.0*backup_streamed/backup_total,1) ELSE NULL END
			 FROM pg_stat_progress_basebackup`)
	}
	if caps.VersionNum >= 140000 {
		parts = append(parts,
			`SELECT pid, 'copy', nullif(relid,0)::regclass::text, command::text,
			   CASE WHEN bytes_total>0 THEN round(100.0*bytes_processed/bytes_total,1) ELSE NULL END
			 FROM pg_stat_progress_copy`)
	}
	return queryMany[progressRow](ctx, t, strings.Join(parts, "\nUNION ALL\n"))
}

func (progressCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]progressRow)
	if s.Err != nil || !ok {
		c.Progress = &model.Progress{Section: unavail(s.Err, "pg_stat_progress_* unavailable")}
		return
	}
	p := &model.Progress{Section: model.Section{Exactness: model.ExactnessScraped}}
	for _, r := range rows {
		p.Operations = append(p.Operations, model.ProgressOp{
			PID: r.PID, Operation: r.Operation, Relation: r.Relation, Phase: r.Phase, Pct: r.Pct,
		})
	}
	c.Progress = p
}

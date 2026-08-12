package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/locks.sql
var sqlLocks string

// locks = current blocking chains. blocked_query is RAW SQL and is scrubbed of
// literals before entering the Context (unless --raw-query-text opts out).
type locksCollector struct{}

type lockRow struct {
	BlockedPID    int32   `db:"blocked_pid"`
	BlockingPIDs  []int32 `db:"blocking_pids"`
	WaitEventType string  `db:"wait_event_type"`
	BlockedWaitS  float64 `db:"blocked_wait_s"`
	BlockedQuery  string  `db:"blocked_query"`
}

func (locksCollector) Name() string                     { return "locks" }
func (locksCollector) Kind() Kind                       { return KindGauge }
func (locksCollector) Available(conn.Capabilities) bool { return true }

func (locksCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[lockRow](ctx, t, sqlLocks)
}

func (locksCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, opts Options) {
	rows, ok := s.A.([]lockRow)
	if s.Err != nil || !ok {
		c.Locks = &model.Locks{Section: unavail(s.Err, "pg_locks unavailable")}
		return
	}
	l := &model.Locks{Section: model.Section{Exactness: model.ExactnessScraped}, BlockedCount: len(rows)}
	for _, r := range rows {
		pids := make([]int64, len(r.BlockingPIDs))
		for i, p := range r.BlockingPIDs {
			pids[i] = int64(p)
		}
		q := r.BlockedQuery
		if !opts.RawQueryText {
			q = conn.ScrubQueryText(q)
		}
		l.Chains = append(l.Chains, model.BlockingRow{
			BlockedPID:   int(r.BlockedPID),
			BlockingPIDs: pids,
			WaitEvent:    r.WaitEventType,
			WaitSeconds:  round2(r.BlockedWaitS),
			BlockedQuery: q,
		})
	}
	c.Locks = l
}

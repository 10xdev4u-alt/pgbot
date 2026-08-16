package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/standby.sql
var sqlStandby string

// standby = recovery-conflict counters, only meaningful on a hot standby. Gated
// on Standby() (A15-0): it never runs on a primary.
type standbyCollector struct{}

type standbyRow struct {
	ConflTablespace int64 `db:"confl_tablespace"`
	ConflLock       int64 `db:"confl_lock"`
	ConflSnapshot   int64 `db:"confl_snapshot"`
	ConflBufferpin  int64 `db:"confl_bufferpin"`
	ConflDeadlock   int64 `db:"confl_deadlock"`
}

func (standbyCollector) Name() string                          { return "standby" }
func (standbyCollector) Kind() Kind                            { return KindGauge }
func (standbyCollector) Available(caps conn.Capabilities) bool { return caps.Standby() }

func (standbyCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryOne[standbyRow](ctx, t, sqlStandby)
}

func (standbyCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	r, ok := s.A.(standbyRow)
	if s.Err != nil || !ok {
		c.Standby = &model.StandbyStatus{Section: unavail(s.Err, "recovery conflicts unavailable")}
		return
	}
	c.Standby = &model.StandbyStatus{
		Section:         model.Section{Exactness: model.ExactnessCumulative},
		ConflTablespace: r.ConflTablespace, ConflLock: r.ConflLock, ConflSnapshot: r.ConflSnapshot,
		ConflBufferpin: r.ConflBufferpin, ConflDeadlock: r.ConflDeadlock,
	}
}

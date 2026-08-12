package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/rate"
)

//go:embed sql/wal.sql
var sqlWAL string

// wal = pg_stat_wal (PG14+), double-sampled for byte/record rates.
type walCollector struct{}

type walSample struct {
	WalRecords     int64 `db:"wal_records"`
	WalBytes       int64 `db:"wal_bytes"`
	WalBuffersFull int64 `db:"wal_buffers_full"`
}

func (walCollector) Name() string                          { return "wal" }
func (walCollector) Kind() Kind                            { return KindCounter }
func (walCollector) Available(caps conn.Capabilities) bool { return caps.HasStatWAL() }

func (walCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryOne[walSample](ctx, t, sqlWAL)
}

func (walCollector) Assemble(c *model.Context, caps conn.Capabilities, s sampled, dt time.Duration, _ Options) {
	if !caps.HasStatWAL() {
		c.WAL = &model.WAL{Section: model.Section{Exactness: model.ExactnessUnavailable, Reason: "pg_stat_wal requires PostgreSQL 14+"}}
		return
	}
	a, aok := s.A.(walSample)
	b, bok := s.B.(walSample)
	if s.Err != nil || !aok || !bok {
		c.WAL = &model.WAL{Section: unavail(s.Err, "pg_stat_wal unavailable")}
		return
	}
	w := &model.WAL{Section: model.Section{Exactness: model.ExactnessSampled}, BuffersFull: b.WalBuffersFull}
	if bp, ok := rate.PerSecond(a.WalBytes, b.WalBytes, dt); ok {
		w.BytesPerSec = round2p(bp)
	} else {
		w.Section = model.Section{Exactness: model.ExactnessReset, Reason: "wal counter reset between samples"}
	}
	if rp, ok := rate.PerSecond(a.WalRecords, b.WalRecords, dt); ok {
		w.RecordsPerSec = round2p(rp)
	}
	c.WAL = w
}

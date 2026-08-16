package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/horizon.sql
var sqlHorizon string

// horizon = what pins the xmin/vacuum horizon (long-open transaction, standby
// feedback, replication slot, or prepared transaction). Point-in-time; feeds the
// vacuum_horizon_blocked finding, which explains WHY table_bloat isn't clearing.
type horizonCollector struct{}

type horizonRow struct {
	Source  string  `db:"source"`
	Holder  string  `db:"holder"`
	XminAge int64   `db:"xmin_age"`
	AgeSec  float64 `db:"age_s"`
	Detail  string  `db:"detail"`
}

func (horizonCollector) Name() string                     { return "horizon" }
func (horizonCollector) Kind() Kind                       { return KindGauge }
func (horizonCollector) Available(conn.Capabilities) bool { return true }

func (horizonCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[horizonRow](ctx, t, sqlHorizon)
}

func (horizonCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]horizonRow)
	if s.Err != nil || !ok {
		c.Horizon = &model.VacuumHorizon{Section: unavail(s.Err, "xmin horizon unavailable")}
		return
	}
	h := &model.VacuumHorizon{Section: model.Section{Exactness: model.ExactnessScraped}}
	for _, r := range rows {
		h.Holders = append(h.Holders, model.HorizonHolder{
			Source: r.Source, Holder: r.Holder, XminAge: r.XminAge, AgeSec: round2(r.AgeSec), Detail: r.Detail,
		})
	}
	c.Horizon = h
}

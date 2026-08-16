package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/limits.sql
var sqlLimits string

// limits = cluster-wide saturation gauges (connections vs max, xid wraparound
// age). Point-in-time, one row. Feeds the connection_saturation and
// txid_wraparound findings.
type limitsCollector struct{}

type limitsRow struct {
	ConnUsed   int   `db:"conn_used"`
	ConnMax    int   `db:"conn_max"`
	MaxXIDAge  int64 `db:"max_xid_age"`
	MaxMXIDAge int64 `db:"max_mxid_age"`
}

func (limitsCollector) Name() string                     { return "limits" }
func (limitsCollector) Kind() Kind                       { return KindGauge }
func (limitsCollector) Available(conn.Capabilities) bool { return true }

func (limitsCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryOne[limitsRow](ctx, t, sqlLimits)
}

func (limitsCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	row, ok := s.A.(limitsRow)
	if s.Err != nil || !ok {
		c.Limits = &model.Limits{Section: unavail(s.Err, "connection/xid limits unavailable")}
		return
	}
	c.Limits = &model.Limits{
		Section:         model.Section{Exactness: model.ExactnessScraped},
		ConnectionsUsed: row.ConnUsed,
		ConnectionsMax:  row.ConnMax,
		MaxXIDAge:       row.MaxXIDAge,
		MaxMXIDAge:      row.MaxMXIDAge,
	}
}

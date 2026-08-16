package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/checksums.sql
var sqlChecksums string

// checksums = data-checksum failures cluster-wide (pg_stat_database, PG12+). A
// wake-the-on-call signal. Empty list = healthy.
type checksumsCollector struct{}

type checksumRow struct {
	Database    string     `db:"database"`
	Failures    int64      `db:"checksum_failures"`
	LastFailure *time.Time `db:"checksum_last_failure"`
}

func (checksumsCollector) Name() string { return "checksums" }
func (checksumsCollector) Kind() Kind   { return KindGauge }
func (checksumsCollector) Available(caps conn.Capabilities) bool {
	return caps.VersionNum >= 120000 // checksum_failures is PG12+
}

func (checksumsCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[checksumRow](ctx, t, sqlChecksums)
}

func (checksumsCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]checksumRow)
	if s.Err != nil || !ok {
		c.Checksums = &model.Checksums{Section: unavail(s.Err, "checksum stats unavailable")}
		return
	}
	ck := &model.Checksums{Section: model.Section{Exactness: model.ExactnessScraped}}
	for _, r := range rows {
		ck.Failures = append(ck.Failures, model.ChecksumFailure{
			Database: r.Database, Count: r.Failures, LastFailure: r.LastFailure,
		})
	}
	c.Checksums = ck
}

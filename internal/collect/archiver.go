package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/archiver.sql
var sqlArchiver string

// archiver = WAL archiving health (pg_stat_archiver). Archiving is a primary
// concern, so this stays off on a confirmed standby (A15-0). Point-in-time.
type archiverCollector struct{}

type archiverRow struct {
	ArchivedCount     int64      `db:"archived_count"`
	LastArchivedWAL   string     `db:"last_archived_wal"`
	LastArchivedTime  *time.Time `db:"last_archived_time"`
	FailedCount       int64      `db:"failed_count"`
	LastFailedWAL     string     `db:"last_failed_wal"`
	LastFailedTime    *time.Time `db:"last_failed_time"`
	StatsReset        *time.Time `db:"stats_reset"`
	HasArchiveCommand bool       `db:"has_archive_command"`
}

func (archiverCollector) Name() string { return "archiver" }
func (archiverCollector) Kind() Kind   { return KindGauge }
func (archiverCollector) Available(caps conn.Capabilities) bool {
	return !caps.Standby() // primary or unknown; a standby doesn't drive archiving
}

func (archiverCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryOne[archiverRow](ctx, t, sqlArchiver)
}

func (archiverCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	r, ok := s.A.(archiverRow)
	if s.Err != nil || !ok {
		c.Archiver = &model.Archiver{Section: unavail(s.Err, "pg_stat_archiver unavailable")}
		return
	}
	c.Archiver = &model.Archiver{
		Section:           model.Section{Exactness: model.ExactnessScraped},
		ArchivedCount:     r.ArchivedCount,
		LastArchivedWAL:   r.LastArchivedWAL,
		LastArchivedTime:  r.LastArchivedTime,
		FailedCount:       r.FailedCount,
		LastFailedWAL:     r.LastFailedWAL,
		LastFailedTime:    r.LastFailedTime,
		StatsReset:        r.StatsReset,
		HasArchiveCommand: r.HasArchiveCommand,
	}
}

package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/activity.sql
var sqlActivity string

// activity = a point-in-time read of pg_stat_activity.
type activityCollector struct{}

type activityRow struct {
	State         string  `db:"state"`
	WaitEventType string  `db:"wait_event_type"`
	N             int     `db:"n"`
	MaxXactAgeS   float64 `db:"max_xact_age_s"`
	MaxActiveAgeS float64 `db:"max_active_age_s"`
}

func (activityCollector) Name() string                     { return "activity" }
func (activityCollector) Kind() Kind                       { return KindGauge }
func (activityCollector) Available(conn.Capabilities) bool { return true }

func (activityCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[activityRow](ctx, t, sqlActivity)
}

func (activityCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]activityRow)
	if s.Err != nil || !ok {
		c.Activity = &model.Activity{Section: unavail(s.Err, "pg_stat_activity unavailable")}
		return
	}
	act := &model.Activity{ByState: map[string]int{}, WaitEvents: map[string]int{}, Section: model.Section{Exactness: model.ExactnessScraped}}
	for _, r := range rows {
		act.Total += r.N
		act.ByState[r.State] += r.N
		switch r.State {
		case "active":
			act.Active += r.N
		case "idle":
			act.Idle += r.N
		case "idle in transaction", "idle in transaction (aborted)":
			act.IdleInTransaction += r.N
		}
		if r.WaitEventType != "" {
			act.Waiting += r.N
			act.WaitEvents[r.WaitEventType] += r.N
		}
		if r.MaxXactAgeS > act.LongestXactSec {
			act.LongestXactSec = round2(r.MaxXactAgeS)
		}
		if r.MaxActiveAgeS > act.LongestActiveSec {
			act.LongestActiveSec = round2(r.MaxActiveAgeS)
		}
	}
	if len(act.WaitEvents) == 0 {
		act.WaitEvents = nil
	}
	c.Activity = act
}

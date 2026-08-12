package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/settings.sql
var sqlSettings string

// settings = parameters whose active value differs from the compiled-in default.
type settingsCollector struct{}

type settingRow struct {
	Name    string `db:"name"`
	Setting string `db:"setting"`
}

func (settingsCollector) Name() string                     { return "settings" }
func (settingsCollector) Kind() Kind                       { return KindGauge }
func (settingsCollector) Available(conn.Capabilities) bool { return true }

func (settingsCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[settingRow](ctx, t, sqlSettings)
}

func (settingsCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]settingRow)
	if s.Err != nil || !ok {
		c.Settings = &model.Settings{Section: unavail(s.Err, "pg_settings unavailable")}
		return
	}
	set := &model.Settings{Section: model.Section{Exactness: model.ExactnessScraped}, Overrides: map[string]string{}}
	for _, r := range rows {
		set.Overrides[r.Name] = r.Setting
	}
	c.Settings = set
}

package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/sequences.sql
var sqlSequences string

// sequences = per-sequence exhaustion headroom, using the owning column's type
// ceiling (an int4 column wraps at 2^31 regardless of the sequence's max_value).
// PG10+ (pg_sequences).
type sequencesCollector struct{}

type sequenceRow struct {
	Schema    string `db:"schema"`
	Name      string `db:"sequence"`
	LastValue int64  `db:"last_value"`
	Ceiling   int64  `db:"ceiling"`
	OwnedBy   string `db:"owned_by"`
}

func (sequencesCollector) Name() string { return "sequences" }
func (sequencesCollector) Kind() Kind   { return KindGauge }
func (sequencesCollector) Available(caps conn.Capabilities) bool {
	return caps.VersionNum >= 100000 // pg_sequences
}

func (sequencesCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[sequenceRow](ctx, t, sqlSequences)
}

func (sequencesCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]sequenceRow)
	if s.Err != nil || !ok {
		c.Sequences = &model.Sequences{Section: unavail(s.Err, "sequence usage unavailable")}
		return
	}
	seq := &model.Sequences{Section: model.Section{Exactness: model.ExactnessScraped}}
	for _, r := range rows {
		if r.Ceiling <= 0 {
			continue
		}
		seq.Items = append(seq.Items, model.SequenceUsage{
			Schema: r.Schema, Name: r.Name, LastValue: r.LastValue, Ceiling: r.Ceiling,
			PctUsed: round4(float64(r.LastValue) / float64(r.Ceiling)), OwnedBy: r.OwnedBy,
		})
	}
	c.Sequences = seq
}

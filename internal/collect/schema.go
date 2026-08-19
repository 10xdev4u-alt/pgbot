package collect

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/schema.sql
var sqlSchema string

// schema = a fingerprint of the catalog (tables, columns, indexes, constraints,
// extensions, sequences). Stored separately and diffed against the previous run
// to derive schema.* events (T7). Reads pg_catalog only — no new grants.
type schemaCollector struct{}

type schemaRow struct {
	Kind       string `db:"kind"`
	Identity   string `db:"identity"`
	Definition string `db:"definition"`
	Invalid    bool   `db:"invalid"`
	Ready      bool   `db:"ready"` // indexes: pg_index.indisready (false → not maintained on writes)
	Live       bool   `db:"live"`  // indexes: pg_index.indislive (false → being dropped)
	Bytes      int64  `db:"bytes"` // indexes: pg_relation_size, invalid ones only
}

func (schemaCollector) Name() string                     { return "schema" }
func (schemaCollector) Kind() Kind                       { return KindGauge }
func (schemaCollector) Available(conn.Capabilities) bool { return true }

func (schemaCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	return queryMany[schemaRow](ctx, t, sqlSchema)
}

func (schemaCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rows, ok := s.A.([]schemaRow)
	if s.Err != nil || !ok {
		return // schema fingerprint absent → no events this run; not a rendered section
	}
	fp := &model.SchemaFingerprint{Objects: make([]model.SchemaObject, 0, len(rows))}
	for _, r := range rows {
		sum := sha256.Sum256([]byte(r.Kind + "\x00" + r.Identity + "\x00" + r.Definition))
		fp.Objects = append(fp.Objects, model.SchemaObject{
			Kind:           r.Kind,
			Identity:       r.Identity,
			Definition:     r.Definition,
			DefinitionHash: hex.EncodeToString(sum[:8]),
			Invalid:        r.Invalid,
			IndexReady:     r.Ready,
			IndexLive:      r.Live,
			Bytes:          r.Bytes,
		})
	}
	c.Schema = fp
}

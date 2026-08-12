package collect

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

// A zero-scan index that backs ANY constraint (PK, unique, exclusion, or a FK's
// referenced key) must never be reported as unused — dropping it breaks the
// constraint. Verified through the collector's Unused filter (T9.3).
func TestIndexes_constraintBackedNeverUnused(t *testing.T) {
	rows := []indexRow{
		{Schema: "public", Table: "t", Index: "t_pkey", Scans: 0, Bytes: 1 << 20, IsPrimary: true, BacksConstraint: true},
		{Schema: "public", Table: "t", Index: "t_email_key", Scans: 0, Bytes: 1 << 20, IsUnique: true, BacksConstraint: true},
		{Schema: "public", Table: "t", Index: "t_no_overlap", Scans: 0, Bytes: 1 << 20, IsExclusion: true, BacksConstraint: true},
		{Schema: "public", Table: "t", Index: "t_fk_ref", Scans: 0, Bytes: 1 << 20, BacksConstraint: true}, // FK's referenced unique index
		{Schema: "public", Table: "t", Index: "t_plain_idx", Scans: 0, Bytes: 4 << 20},                     // the only genuinely droppable one
	}
	c := &model.Context{}
	indexesCollector{}.Assemble(c, conn.Capabilities{}, sampled{A: rows}, 0, Options{})

	if c.Indexes == nil {
		t.Fatal("indexes section missing")
	}
	if len(c.Indexes.Unused) != 1 {
		t.Fatalf("only the plain index should be unused, got %d: %+v", len(c.Indexes.Unused), c.Indexes.Unused)
	}
	if c.Indexes.Unused[0].Name != "t_plain_idx" {
		t.Errorf("wrong index flagged unused: %s", c.Indexes.Unused[0].Name)
	}
}

func TestIndexes_partialExpressionFlagsCarried(t *testing.T) {
	rows := []indexRow{
		{Schema: "public", Table: "t", Index: "p", Scans: 0, Bytes: 4 << 20, IsPartial: true},
		{Schema: "public", Table: "t", Index: "e", Scans: 0, Bytes: 4 << 20, IsExpression: true},
	}
	c := &model.Context{}
	indexesCollector{}.Assemble(c, conn.Capabilities{}, sampled{A: rows}, 0, Options{})
	if len(c.Indexes.Unused) != 2 {
		t.Fatalf("both should be unused, got %d", len(c.Indexes.Unused))
	}
	if !c.Indexes.Unused[0].Partial || !c.Indexes.Unused[1].Expression {
		t.Errorf("partial/expression flags should carry to the finding, got %+v", c.Indexes.Unused)
	}
}

package findings

import (
	"strings"
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestRedundantIndexes(t *testing.T) {
	if has(Compute(&model.Context{Indexes: &model.Indexes{}}), "redundant_indexes") != nil {
		t.Error("no redundant indexes must not fire")
	}
	c := &model.Context{Indexes: &model.Indexes{Redundant: []model.RedundantIndex{
		{Schema: "public", Table: "t", Name: "idx_x", CoveredBy: "idx_x_y", Bytes: 100 << 20},
	}}}
	f := has(Compute(c), "redundant_indexes")
	if f == nil || f.Impact.Dimension != model.DimStorage {
		t.Fatalf("expected a storage finding, got %+v", f)
	}
	if !strings.Contains(f.Evidence[0], "idx_x") || !strings.Contains(f.Evidence[0], "idx_x_y") {
		t.Errorf("evidence should name both indexes: %v", f.Evidence)
	}
}

func TestUnindexedForeignKeys(t *testing.T) {
	if has(Compute(&model.Context{Indexes: &model.Indexes{}}), "fk_unindexed") != nil {
		t.Error("no unindexed FKs must not fire")
	}
	c := &model.Context{Indexes: &model.Indexes{UnindexedFKs: []model.UnindexedFK{
		{Schema: "public", Table: "line_items", Constraint: "fk_order", Columns: "order_id", ChildBytes: 2 << 30},
	}}}
	f := has(Compute(c), "fk_unindexed")
	if f == nil || f.Impact.Dimension != model.DimLatency {
		t.Fatalf("expected a latency finding, got %+v", f)
	}
	if !strings.Contains(f.Evidence[0], "order_id") {
		t.Errorf("evidence should name the FK columns: %v", f.Evidence)
	}
}

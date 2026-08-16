package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestIndexInvalid_caveatedWhenBuilding(t *testing.T) {
	schema := &model.SchemaFingerprint{Objects: []model.SchemaObject{
		{Kind: "index", Identity: "public.orders_idx", Invalid: true},
	}}
	// No build running → critical, confidence 1.
	f := has(Compute(&model.Context{Schema: schema}), "index_invalid")
	if f == nil || f.Severity != model.SeverityCritical || f.Confidence != 1.0 {
		t.Fatalf("expected critical/1.0, got %+v", f)
	}
	// A CREATE INDEX CONCURRENTLY in progress → warn + caveat, don't cry wolf.
	building := &model.Context{Schema: schema, Progress: &model.Progress{Operations: []model.ProgressOp{
		{Operation: "create_index", Relation: "public.orders_idx"},
	}}}
	f = has(Compute(building), "index_invalid")
	if f == nil || f.Severity != model.SeverityWarn || len(f.Caveats) == 0 {
		t.Errorf("expected warn + caveat while a build is running, got %+v", f)
	}
}

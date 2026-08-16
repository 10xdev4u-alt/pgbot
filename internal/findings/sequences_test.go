package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestSequenceExhaustion(t *testing.T) {
	ok := &model.Context{Sequences: &model.Sequences{Items: []model.SequenceUsage{
		{Schema: "public", Name: "s1", LastValue: 1000, Ceiling: 2147483647, PctUsed: 0.0000005},
	}}}
	if has(Compute(ok), "sequence_exhaustion") != nil {
		t.Error("a fresh sequence must not fire")
	}
	warn := &model.Context{Sequences: &model.Sequences{Items: []model.SequenceUsage{
		{Schema: "public", Name: "orders_id_seq", LastValue: 1_800_000_000, Ceiling: 2_147_483_647, PctUsed: 0.838, OwnedBy: "orders.id"},
	}}}
	f := has(Compute(warn), "sequence_exhaustion")
	if f == nil || f.Severity != model.SeverityWarn || f.Impact.Dimension != model.DimRisk {
		t.Fatalf("expected warn/risk, got %+v", f)
	}
	crit := &model.Context{Sequences: &model.Sequences{Items: []model.SequenceUsage{
		{Schema: "public", Name: "s", LastValue: 2_000_000_000, Ceiling: 2_147_483_647, PctUsed: 0.93},
	}}}
	if f := has(Compute(crit), "sequence_exhaustion"); f == nil || f.Severity != model.SeverityCritical {
		t.Errorf("expected critical, got %+v", f)
	}
}

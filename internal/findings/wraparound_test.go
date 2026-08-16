package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestMxidWraparound(t *testing.T) {
	// Healthy multixact age — no finding.
	ok := &model.Context{Limits: &model.Limits{MaxMXIDAge: 50_000_000}}
	if has(Compute(ok), "mxid_wraparound") != nil {
		t.Error("a healthy multixact age must not fire")
	}
	// Warn threshold.
	warn := &model.Context{Limits: &model.Limits{MaxMXIDAge: xidWraparoundWarn + 1}}
	f := has(Compute(warn), "mxid_wraparound")
	if f == nil || f.Severity != model.SeverityWarn || f.Impact.Dimension != model.DimRisk {
		t.Fatalf("expected warn/risk, got %+v", f)
	}
	// Critical threshold.
	crit := &model.Context{Limits: &model.Limits{MaxMXIDAge: xidWraparoundCrit + 1}}
	if f := has(Compute(crit), "mxid_wraparound"); f == nil || f.Severity != model.SeverityCritical {
		t.Errorf("expected critical, got %+v", f)
	}
}

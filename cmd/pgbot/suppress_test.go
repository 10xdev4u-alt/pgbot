package main

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

// Suppressed findings of ANY severity must not affect the exit code (B2-2 DoD).
func TestExitCode_suppressedNeverContributes(t *testing.T) {
	cases := []struct {
		name string
		fs   []model.Finding
		want int
	}{
		{"suppressed critical → clean", []model.Finding{
			{ID: "checksum_failures", Severity: model.SeverityCritical, Suppressed: true},
		}, exitClean},
		{"suppressed warn → clean", []model.Finding{
			{ID: "unused_indexes", Severity: model.SeverityWarn, Suppressed: true},
		}, exitClean},
		{"live warn beside suppressed critical → warn", []model.Finding{
			{ID: "checksum_failures", Severity: model.SeverityCritical, Suppressed: true},
			{ID: "table_bloat", Severity: model.SeverityWarn},
		}, exitWarn},
		{"live critical → critical", []model.Finding{
			{ID: "checksum_failures", Severity: model.SeverityCritical},
		}, exitCritical},
	}
	for _, tc := range cases {
		if got := exitCode(tc.fs, "warn"); got != tc.want {
			t.Errorf("%s: exitCode = %d, want %d", tc.name, got, tc.want)
		}
	}
}

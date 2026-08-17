package main

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestExitCode_failOn(t *testing.T) {
	fs := []model.Finding{
		{Severity: model.SeverityWarn},
		{Severity: model.SeverityInfo},
	}
	crit := append(fs, model.Finding{Severity: model.SeverityCritical})
	cases := []struct {
		name   string
		fs     []model.Finding
		failOn string
		want   int
	}{
		{"warn default: warn present", fs, "warn", exitWarn},
		{"fail-on=critical: warn ignored", fs, "critical", exitClean},
		{"fail-on=critical: crit present", crit, "critical", exitCritical},
		{"fail-on=info: info counts", fs, "info", exitWarn},
		{"fail-on=none: never fails", crit, "none", exitClean},
		{"suppressed critical ignored", []model.Finding{{Severity: model.SeverityCritical, Suppressed: true}}, "warn", exitClean},
	}
	for _, c := range cases {
		if got := exitCode(c.fs, c.failOn); got != c.want {
			t.Errorf("%s: exitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

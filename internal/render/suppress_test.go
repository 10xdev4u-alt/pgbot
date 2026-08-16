package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func suppressCtx() *model.Context {
	return &model.Context{
		SchemaVersion: model.SchemaVersion,
		CollectedAt:   time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
		Server:        model.ServerInfo{VersionNum: 180000, Database: "app", HasPgMonitor: true},
		Window:        model.Window{SampleSeconds: 1.0},
		Findings: []model.Finding{
			{ID: "checksum_failures", Severity: model.SeverityCritical, Title: "data-checksum failures on 2 pages",
				Suppressed: true, SuppressionReason: "known bad disk, replacement scheduled", SuppressionRule: "checksum_failures object=*",
				Impact: model.Impact{Score: 95, Dimension: model.DimRisk}},
			{ID: "unused_indexes", Severity: model.SeverityWarn, Title: "3 unused indexes",
				Suppressed: true, SuppressionReason: "quarterly export", SuppressionRule: "unused_indexes object=*",
				Impact: model.Impact{Score: 60, Dimension: model.DimStorage}},
		},
	}
}

// A suppressed CRITICAL must still render (visibly marked); a suppressed WARN is
// hidden from the list and only counted in the footer (B2-2).
func TestTerminal_suppressedCriticalStillRenders(t *testing.T) {
	var buf bytes.Buffer
	if err := Terminal(&buf, suppressCtx(), Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "data-checksum failures on 2 pages") {
		t.Error("a suppressed critical must still appear in the report")
	}
	if !strings.Contains(out, "suppressed by config") {
		t.Error("the suppressed critical must be visibly marked with its reason")
	}
	if strings.Contains(out, "3 unused indexes") {
		t.Error("a suppressed warning must NOT be listed in the default view")
	}
	if !strings.Contains(out, "1 finding(s) suppressed by config") {
		t.Errorf("expected a footer counting the hidden warning, got:\n%s", out)
	}
}

// --full lists suppressed findings in a dimmed section, reason inline.
func TestTerminal_fullShowsSuppressedSection(t *testing.T) {
	var buf bytes.Buffer
	if err := Terminal(&buf, suppressCtx(), Options{Color: false, Full: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "SUPPRESSED (1)") {
		t.Errorf("--full must show a suppressed section, got:\n%s", out)
	}
	if !strings.Contains(out, "quarterly export") {
		t.Error("the suppressed warning's reason must render in --full")
	}
}

// Config warnings surface at the top of the report.
func TestTerminal_configWarningsRender(t *testing.T) {
	c := suppressCtx()
	c.ConfigWarnings = []string{`[severity] "typo_finding" is not a known finding id`}
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "config warning") || !strings.Contains(buf.String(), "typo_finding") {
		t.Errorf("config warnings must render at the top, got:\n%s", buf.String())
	}
}

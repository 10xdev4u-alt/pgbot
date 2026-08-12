package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func sampleContext() *model.Context {
	tps := 1200.0
	hit := 0.994
	return &model.Context{
		SchemaVersion: model.SchemaVersion,
		CollectedAt:   time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		Server:        model.ServerInfo{VersionNum: 170010, Database: "app", HasPgMonitor: true},
		Window:        model.Window{SampleSeconds: 1.0},
		Health:        &model.Health{Section: model.Section{Exactness: model.ExactnessSampled}, Connections: 24, TPS: &tps, CacheHitRatio: &hit},
		Activity:      &model.Activity{Section: model.Section{Exactness: model.ExactnessScraped}, Total: 24, Active: 6},
		Findings: []model.Finding{
			{ID: "unused_indexes", Severity: model.SeverityWarn, Title: "1 unused index",
				Impact: model.Impact{Score: 60, Dimension: model.DimStorage, Estimate: "≈4.2 GiB reclaimable"}},
		},
	}
}

func TestTerminal_dashboardIsDefault(t *testing.T) {
	var buf bytes.Buffer
	if err := Terminal(&buf, sampleContext(), Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile("\x1b\\[").MatchString(buf.String()) {
		t.Error("no-color output must contain no ANSI escapes")
	}
	out := buf.String()
	// Dashboard header + a vital meter (cache hit) + the finding meter (idle idx)
	// + its bar + the "checked" line + the --full pointer. No section tables.
	for _, want := range []string{"connected", "postgres 17", "cache hit", "[", "idle idx", "review", "checked", "--full"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard output missing %q", want)
		}
	}
	if strings.Contains(out, "HEALTH") || strings.Contains(out, "ACTIVITY") {
		t.Error("default dashboard must not print section tables")
	}
}

func TestTerminal_fullShowsSectionsAndCaveats(t *testing.T) {
	c := sampleContext()
	c.Findings = []model.Finding{{
		ID: "unused_indexes", Severity: model.SeverityWarn, Title: "3 unused indexes",
		Confidence: 0.4, Caveats: []string{"replication is active — per-node counts only"},
		Impact: model.Impact{Score: 50, Dimension: model.DimStorage, Estimate: "≈43 GB"},
	}}
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false, Full: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// --full carries the detail the dashboard omits: sections, caveats inline,
	// and the low-confidence "(possible)" marker.
	for _, want := range []string{"HEALTH", "cache hit", "3 unused indexes", "replication is active", "(possible)"} {
		if !strings.Contains(out, want) {
			t.Errorf("--full output missing %q", want)
		}
	}
}

func TestTerminal_cleanDashboardNamesPassedChecks(t *testing.T) {
	c := sampleContext()
	c.Findings = nil
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Healthy: the cache-hit vital reads "ok", and the checked line names what
	// was examined and passed.
	for _, want := range []string{"cache hit", "ok", "checked", "connections"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean dashboard missing %q", want)
		}
	}
}

func TestSparkline(t *testing.T) {
	if s := sparkline(nil); s != "" {
		t.Errorf("empty series -> empty spark, got %q", s)
	}
	s := sparkline([]float64{1, 2, 3, 4, 5})
	if len([]rune(s)) != 5 {
		t.Errorf("expected 5 spark runes, got %q", s)
	}
}

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
			{ID: "unused_indexes", Severity: model.SeverityWarn, Title: "1 unused index"},
		},
	}
}

func TestTerminal_noColorHasNoANSI(t *testing.T) {
	var buf bytes.Buffer
	if err := Terminal(&buf, sampleContext(), Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile("\x1b\\[").MatchString(buf.String()) {
		t.Error("no-color output must contain no ANSI escapes")
	}
	out := buf.String()
	// Default is the concise summary: the header, the attention finding, the
	// pointer to --full. Section tables are NOT here.
	for _, want := range []string{"app", "PG 17", "1 unused index", "need attention", "--full"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary output missing %q", want)
		}
	}
	// Section headers (which only appear in --full) must be absent. ("cache hit"
	// is intentionally not checked here — it doubles as a passed-check label.)
	if strings.Contains(out, "HEALTH") || strings.Contains(out, "ACTIVITY") {
		t.Error("default summary must not print section tables")
	}
}

func TestTerminal_fullShowsSections(t *testing.T) {
	var buf bytes.Buffer
	if err := Terminal(&buf, sampleContext(), Options{Color: false, Full: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"HEALTH", "cache hit", "1 unused index"} {
		if !strings.Contains(out, want) {
			t.Errorf("--full output missing %q", want)
		}
	}
}

func TestTerminal_cleanReportNamesPassedChecks(t *testing.T) {
	c := sampleContext()
	c.Findings = nil
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "nothing needs attention") {
		t.Error("a clean report should say nothing needs attention")
	}
	// The ✓ line must NAME checks that were examined and passed.
	for _, want := range []string{"cache hit", "connections"} {
		if !strings.Contains(out, want) {
			t.Errorf("passed-checks line missing %q", want)
		}
	}
}

func TestTerminal_summaryKeepsCaveatsInline(t *testing.T) {
	c := sampleContext()
	c.Findings = []model.Finding{{
		ID: "unused_indexes", Severity: model.SeverityWarn, Title: "3 unused indexes",
		Confidence: 0.4, Caveats: []string{"replication is active — per-node counts only"},
		Impact: model.Impact{Score: 50, Dimension: model.DimStorage, Estimate: "≈43 GB"},
	}}
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "replication is active") {
		t.Error("caveats must render inline in the summary, not be dropped")
	}
	if !strings.Contains(out, "(possible)") {
		t.Error("a <0.5 confidence finding should render as possible")
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

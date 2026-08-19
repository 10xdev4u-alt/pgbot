package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func ptr(s string) *string { return &s }

// a finding carrying a precondition guard (DROP INDEX) and a prohibition guard.
func guardedContext() *model.Context {
	return &model.Context{
		SchemaVersion: model.SchemaVersion,
		Findings: []model.Finding{
			{
				ID: "unused_indexes", Severity: model.SeverityWarn, Title: "1 unused index",
				Remediation: "Drop with DROP INDEX CONCURRENTLY after confirming the caveats.",
				Safety: &model.Safety{BlockingCaveats: []model.SafetyGuard{
					{ID: "unused_index.per_node", Kind: model.GuardPrecondition, Action: model.ActionDropIndex,
						Text: "zero scans on this node only", Verify: ptr("the index is unused on every replica")},
				}},
			},
			{
				ID: "checksum_failures", Severity: model.SeverityCritical, Title: "corruption",
				Remediation: "Restore from backup.",
				Safety: &model.Safety{BlockingCaveats: []model.SafetyGuard{
					{ID: "checksum.no_vacuum_full", Kind: model.GuardProhibition, Action: model.ActionVacuumFull,
						Text: "rewriting pages destroys the evidence"},
				}},
			},
		},
	}
}

// Step 6: the structured guard must survive into --json, keyed on its stable ID.
func TestSafety_inJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, guardedContext()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`"blocking_caveats"`, `"unused_index.per_node"`, `"checksum.no_vacuum_full"`, `"DROP INDEX"`, `"VACUUM FULL"`, `"precondition"`, `"prohibition"`} {
		if !strings.Contains(out, want) {
			t.Errorf("--json output is missing %s — the structured guard was dropped", want)
		}
	}
}

// Step 6: SARIF (GitHub Security tab) must carry the guard text, not just the
// remediation — the original bug was that it silently dropped every caveat.
func TestSafety_inSARIF(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, guardedContext()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"DROP INDEX", "the index is unused on every replica", "VACUUM FULL", "rewriting pages destroys the evidence"} {
		if !strings.Contains(out, want) {
			t.Errorf("SARIF output is missing %q — a destructive guard was dropped", want)
		}
	}
}

// Step 7 guard: the grouped (default) and full terminal views render the guard.
func TestSafety_inTerminal(t *testing.T) {
	for _, full := range []bool{false, true} {
		var buf bytes.Buffer
		if err := Terminal(&buf, guardedContext(), Options{Full: full, Width: 100}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "DROP INDEX") || !strings.Contains(out, "VACUUM FULL") {
			t.Errorf("terminal(full=%v) dropped a destructive guard:\n%s", full, out)
		}
	}
}

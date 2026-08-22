package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestExplainFindingTool(t *testing.T) {
	out, err := explainFindingTool(context.Background(), json.RawMessage(`{"id":"fsync_off"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# fsync_off") || !strings.Contains(out, "## How to fix it") {
		t.Errorf("explain_finding should return the catalogue page, got:\n%s", out[:min(200, len(out))])
	}
	if strings.HasPrefix(out, "---") {
		t.Error("front-matter should be stripped from the served page")
	}
	if _, err := explainFindingTool(context.Background(), json.RawMessage(`{"id":"nope"}`)); err == nil {
		t.Error("unknown finding id should error")
	}
}

// explain_plan must refuse anything but a single plain SELECT before it ever
// connects — same guard as the advisor.
func TestExplainPlanTool_refusesNonSelect(t *testing.T) {
	for _, q := range []string{"DELETE FROM t", "SELECT 1; DROP TABLE t", "WITH w AS (INSERT ...) SELECT 1"} {
		args, _ := json.Marshal(map[string]string{"query": q, "connection_string": "postgres://x@127.0.0.1:1/x"})
		if _, err := explainPlanTool(context.Background(), args); err == nil || !strings.Contains(err.Error(), "single plain SELECT") {
			t.Errorf("query %q should be refused as not-a-SELECT, got %v", q, err)
		}
	}
}

// The diagnose prompt must never re-print the connection string it was given:
// the rendered prompt lands in agent transcripts and logs, and a postgres://
// URL carries the password in cleartext.
func TestDiagnosePrompt_neverEchoesDSN(t *testing.T) {
	prompts := pgbotPrompts()
	if len(prompts) != 1 || prompts[0].Name != "diagnose" {
		t.Fatalf("expected the single diagnose prompt, got %+v", prompts)
	}
	dsn := "postgres://appuser:s3cret@db.internal:5432/appdb"
	msgs, err := prompts[0].Build(context.Background(), map[string]string{"connection_string": dsn})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected one prompt message, got %d", len(msgs))
	}
	if strings.Contains(msgs[0].Text, "s3cret") || strings.Contains(msgs[0].Text, dsn) {
		t.Errorf("the rendered prompt must not contain the DSN or its password:\n%s", msgs[0].Text)
	}
	if !strings.Contains(msgs[0].Text, "inspect") {
		t.Errorf("the prompt must still direct the agent to call inspect:\n%s", msgs[0].Text)
	}
	// Without a DSN the prompt still renders and stays DSN-free.
	msgs, err = prompts[0].Build(context.Background(), map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msgs[0].Text, "connection_string \"") {
		t.Errorf("no DSN given, nothing about one should be mentioned:\n%s", msgs[0].Text)
	}
}

// Live: explain_plan plans a real SELECT and schema_of returns real metadata.
func TestIntegration_mcpTools(t *testing.T) {
	dsn := os.Getenv("PGBOT_TEST_SUPERUSER_DSN")
	if dsn == "" {
		t.Skip("set PGBOT_TEST_SUPERUSER_DSN to run the MCP tool integration test")
	}
	ctx := context.Background()

	// explain_plan on a real SELECT returns a plan and the estimate label.
	args, _ := json.Marshal(map[string]string{"query": "SELECT count(*) FROM pg_class WHERE relname = 'pg_class'", "connection_string": dsn})
	out, err := explainPlanTool(ctx, args)
	if err != nil {
		t.Fatalf("explain_plan: %v", err)
	}
	if !strings.Contains(out, `"Plan"`) || !strings.Contains(out, `"exactness": "estimate"`) {
		t.Errorf("explain_plan should return a plan + estimate label:\n%s", out)
	}

	// schema_of returns columns/indexes for a catalog table, no data.
	args, _ = json.Marshal(map[string]string{"table": "pg_catalog.pg_class", "connection_string": dsn})
	out, err = schemaOfTool(ctx, args)
	if err != nil {
		t.Fatalf("schema_of: %v", err)
	}
	if !strings.Contains(out, `"columns"`) || !strings.Contains(out, "relname") || !strings.Contains(out, `"estimated_rows"`) {
		t.Errorf("schema_of should return columns + row estimate:\n%s", out[:min(400, len(out))])
	}
}

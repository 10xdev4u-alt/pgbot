package findings

import (
	"strings"
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

// Issue #11: an invalid index left by a CREATE INDEX CONCURRENTLY that failed
// BEFORE the index was marked ready (indisready = false, typically 0 bytes) is
// not maintained on writes — PostgreSQL ignores it for INSERT/UPDATE. Calling
// that "critical write overhead" overstates it above real operational problems.
// Only an invalid index that is still indisready carries the write cost.

func invalidCtx(objs ...model.SchemaObject) *model.Context {
	return &model.Context{Schema: &model.SchemaFingerprint{Objects: objs}}
}

func TestIndexInvalid_notReadyZeroBytes_isDebrisNotCritical(t *testing.T) {
	f := has(Compute(invalidCtx(
		model.SchemaObject{Kind: "index", Identity: "public.example.example_invalid_index",
			Invalid: true, IndexReady: false, IndexLive: true, Bytes: 0},
	)), "index_invalid")
	if f == nil {
		t.Fatal("an invalid index must still fire index_invalid (it needs cleanup)")
	}
	if f.Severity != model.SeverityWarn {
		t.Errorf("failed-build debris (indisready=false, 0 B) must be warn, got %s", f.Severity)
	}
	if strings.Contains(strings.ToLower(f.Detail), "maintained on every write") {
		t.Errorf("must not claim write maintenance for an indisready=false index: %q", f.Detail)
	}
	if !strings.Contains(strings.ToLower(f.Detail), "not maintained") {
		t.Errorf("detail should state the index is NOT maintained on writes: %q", f.Detail)
	}
	if len(f.Evidence) != 1 || !strings.Contains(f.Evidence[0], "indisready = false") || !strings.Contains(f.Evidence[0], "0 B") {
		t.Errorf("evidence should carry the catalog state and size: %v", f.Evidence)
	}
	if f.Impact.Score >= 85 {
		t.Errorf("debris impact must rank below a maintained invalid index (85), got %v", f.Impact.Score)
	}
	// Still cleanup guidance + the drop guard: the index name is occupied and a
	// build may be running.
	if !strings.Contains(f.Remediation, "DROP INDEX CONCURRENTLY") {
		t.Errorf("remediation should still say how to clean up: %q", f.Remediation)
	}
	if f.Safety == nil || len(f.Safety.BlockingCaveats) == 0 {
		t.Error("the destructive-action guard must remain")
	}
}

func TestIndexInvalid_readyIsMaintained_staysCritical(t *testing.T) {
	f := has(Compute(invalidCtx(
		model.SchemaObject{Kind: "index", Identity: "public.orders.orders_created_idx",
			Invalid: true, IndexReady: true, IndexLive: true, Bytes: 128 << 20},
	)), "index_invalid")
	if f == nil {
		t.Fatal("expected index_invalid")
	}
	if f.Severity != model.SeverityCritical {
		t.Errorf("an invalid index that is indisready IS maintained on every write → critical, got %s", f.Severity)
	}
	if !strings.Contains(f.Detail, "maintained on every write") {
		t.Errorf("detail should state the write cost for a ready index: %q", f.Detail)
	}
	if len(f.Evidence) != 1 || !strings.Contains(f.Evidence[0], "indisready = true") || !strings.Contains(f.Evidence[0], "128.0 MiB") {
		t.Errorf("evidence should carry the catalog state and size: %v", f.Evidence)
	}
	if f.Impact.Score != 85 {
		t.Errorf("maintained invalid index keeps impact 85, got %v", f.Impact.Score)
	}
}

func TestIndexInvalid_notReadyNonzeroBytes_isWarnStorageOnly(t *testing.T) {
	// A build that failed mid-way (or a REINDEX CONCURRENTLY leftover) can have
	// written pages: storage cost, still no write cost.
	f := has(Compute(invalidCtx(
		model.SchemaObject{Kind: "index", Identity: "public.t.t_ccnew",
			Invalid: true, IndexReady: false, IndexLive: true, Bytes: 40 << 20},
	)), "index_invalid")
	if f == nil || f.Severity != model.SeverityWarn {
		t.Fatalf("not-ready index with data is warn (storage, no write cost), got %+v", f)
	}
	if strings.Contains(strings.ToLower(f.Detail), "maintained on every write") {
		t.Errorf("must not claim write maintenance: %q", f.Detail)
	}
	if !strings.Contains(f.Evidence[0], "40.0 MiB") {
		t.Errorf("evidence should show the wasted size: %v", f.Evidence)
	}
}

func TestIndexInvalid_notLive_isBeingDropped(t *testing.T) {
	f := has(Compute(invalidCtx(
		model.SchemaObject{Kind: "index", Identity: "public.t.t_old_idx",
			Invalid: true, IndexReady: false, IndexLive: false, Bytes: 0},
	)), "index_invalid")
	if f == nil || f.Severity != model.SeverityWarn {
		t.Fatalf("indislive=false is ignored for all purposes → warn, got %+v", f)
	}
	if !strings.Contains(f.Evidence[0], "indislive = false") {
		t.Errorf("evidence should name the indislive state: %v", f.Evidence)
	}
}

func TestIndexInvalid_mixed_isCriticalAndNamesEachState(t *testing.T) {
	f := has(Compute(invalidCtx(
		model.SchemaObject{Kind: "index", Identity: "public.a.a_idx", Invalid: true, IndexReady: true, IndexLive: true, Bytes: 1 << 20},
		model.SchemaObject{Kind: "index", Identity: "public.b.b_idx", Invalid: true, IndexReady: false, IndexLive: true, Bytes: 0},
		model.SchemaObject{Kind: "index", Identity: "public.c.c_pkey", Invalid: false, IndexReady: true, IndexLive: true}, // valid: ignored
	)), "index_invalid")
	if f == nil {
		t.Fatal("expected index_invalid")
	}
	if f.Severity != model.SeverityCritical {
		t.Errorf("one maintained invalid index makes the finding critical, got %s", f.Severity)
	}
	if len(f.Evidence) != 2 {
		t.Fatalf("evidence should list both invalid indexes: %v", f.Evidence)
	}
	joined := strings.Join(f.Evidence, "\n")
	if !strings.Contains(joined, "a_idx") || !strings.Contains(joined, "indisready = true") ||
		!strings.Contains(joined, "b_idx") || !strings.Contains(joined, "indisready = false") {
		t.Errorf("each index should carry its own state: %v", f.Evidence)
	}
	if !strings.Contains(f.Detail, "1 maintained") || !strings.Contains(f.Detail, "1 ") {
		t.Errorf("mixed detail should count both classes: %q", f.Detail)
	}
	if !strings.Contains(f.Title, "2 invalid index") {
		t.Errorf("title counts all invalid indexes: %q", f.Title)
	}
}

// A running CREATE INDEX CONCURRENTLY still downgrades everything to warn with
// the do-not-drop guard, whatever the ready state (unchanged behaviour).
func TestIndexInvalid_buildingStillWarnsWithProhibition(t *testing.T) {
	c := invalidCtx(model.SchemaObject{Kind: "index", Identity: "public.orders.orders_idx", Invalid: true, IndexReady: true, IndexLive: true, Bytes: 1 << 20})
	c.Progress = &model.Progress{Operations: []model.ProgressOp{{Operation: "create_index", Relation: "public.orders_idx"}}}
	f := has(Compute(c), "index_invalid")
	if f == nil || f.Severity != model.SeverityWarn || f.Confidence != 0.5 || len(f.Caveats) == 0 {
		t.Fatalf("building → warn/0.5/caveat, got %+v", f)
	}
	if f.Safety == nil || len(f.Safety.BlockingCaveats) == 0 || f.Safety.BlockingCaveats[0].Kind != model.GuardProhibition {
		t.Errorf("building keeps the prohibition guard, got %+v", f.Safety)
	}
}

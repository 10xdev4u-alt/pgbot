package correlate

import (
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func f64(v float64) *float64 { return &v }

// ctxWith builds a minimal Context with the given unused indexes and a long
// (non-cold) stats window.
func ctxWith(unused []model.IndexStat, mut func(*model.Context)) *model.Context {
	c := &model.Context{
		Fingerprint: "abc123",
		Indexes:     &model.Indexes{Unused: unused},
	}
	c.Window.StatsWindowDays = f64(45)
	c.Window.StatsResetAt = &time.Time{}
	if mut != nil {
		mut(c)
	}
	return c
}

func find(r Report, name string) *IndexReport {
	for i := range r.Indexes {
		if r.Indexes[i].Index == name {
			return &r.Indexes[i]
		}
	}
	return nil
}

func TestSearchTerms_allFourCaseVariants(t *testing.T) {
	terms := searchTermsFor([]string{"externalIdNormalized"})
	want := []string{"externalIdNormalized", "external_id_normalized", "ExternalIdNormalized", "EXTERNAL_ID_NORMALIZED"}
	for _, w := range want {
		if !contains(terms, w) {
			t.Errorf("search terms %v missing %q (all four cases must be emitted)", terms, w)
		}
	}
}

func TestSearchTerms_fromSnakeAndSingleWord(t *testing.T) {
	// snake_case input must still yield camelCase and PascalCase.
	terms := searchTermsFor([]string{"customer_id"})
	for _, w := range []string{"customerId", "customer_id", "CustomerId", "CUSTOMER_ID"} {
		if !contains(terms, w) {
			t.Errorf("snake input: %v missing %q", terms, w)
		}
	}
	// single word still gets a Pascal + CONSTANT form.
	terms = searchTermsFor([]string{"status"})
	for _, w := range []string{"status", "Status", "STATUS"} {
		if !contains(terms, w) {
			t.Errorf("single word: %v missing %q", terms, w)
		}
	}
}

func TestClassify_plainBtreeIsNeedsCodeCheck(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "Job", Name: "Job_externalIdNormalized_idx", Method: "btree",
			Columns: []string{"externalIdNormalized"}, Bytes: 41943040},
	}, nil)
	r := Build(c, nil)
	ix := find(r, "Job_externalIdNormalized_idx")
	if ix == nil || ix.Confidence != NeedsCodeCheck {
		t.Fatalf("plain btree zero-scan must be needs_code_check, got %+v", ix)
	}
	if len(ix.SearchTerms) == 0 || ix.Instruction == "" || ix.IfFound == "" || ix.IfNotFound == "" {
		t.Errorf("needs_code_check must carry search terms + instruction + if_found/if_not_found: %+v", ix)
	}
	if ix.DoNotDrop {
		t.Errorf("needs_code_check must not set do_not_drop")
	}
}

func TestInstruction_statesWhereVsSelect(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "t_a_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
	}, nil)
	ix := find(Build(c, nil), "t_a_idx")
	if ix == nil {
		t.Fatal("missing entry")
	}
	if !strings.Contains(ix.Instruction, "WHERE") || !strings.Contains(ix.Instruction, "SELECT") {
		t.Errorf("instruction must state the WHERE-vs-SELECT distinction: %q", ix.Instruction)
	}
}

func TestClassify_ginExpressionPartialAreInconclusive(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "gin_idx", Method: "gin", Columns: []string{"tags"}, Bytes: 1 << 21},
		{Schema: "public", Table: "t", Name: "expr_idx", Method: "btree", Expression: true, Bytes: 1 << 21},
		{Schema: "public", Table: "t", Name: "partial_idx", Method: "btree", Columns: []string{"c"}, Partial: true, Bytes: 1 << 21},
	}, nil)
	r := Build(c, nil)
	for _, name := range []string{"gin_idx", "expr_idx", "partial_idx"} {
		ix := find(r, name)
		if ix == nil || ix.Confidence != Inconclusive {
			t.Errorf("%s must be inconclusive, got %+v", name, ix)
			continue
		}
		if !ix.DoNotDrop || !strings.Contains(ix.Note, "Do not DROP INDEX on this evidence") {
			t.Errorf("%s (inconclusive) must carry the do-not-drop wording: %+v", name, ix)
		}
		if len(ix.SearchTerms) != 0 {
			t.Errorf("%s (inconclusive) must not emit search terms", name)
		}
	}
}

func TestInconclusive_neverPromotedByVerdict(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "gin_idx", Method: "gin", Columns: []string{"tags"}, Bytes: 1 << 21},
	}, nil)
	// A "not found in code" verdict must NOT turn an inconclusive index actionable.
	verdicts := map[string]PriorVerdict{
		"public.gin_idx": {Verdict: "not_found_in_code", CheckedAt: time.Unix(1_700_000_000, 0)},
	}
	ix := find(Build(c, verdicts), "gin_idx")
	if ix == nil || ix.Confidence != Inconclusive {
		t.Fatalf("inconclusive must stay inconclusive even with a not_found verdict, got %+v", ix)
	}
	if !ix.DoNotDrop {
		t.Errorf("still do-not-drop")
	}
}

func TestColdWindow_demotesToInconclusive(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "t_a_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		// A very recent stats reset makes the window cold.
		c.Window.StatsWindowDays = f64(0.001)
		age := int64(60) // 1 minute — below the 900s cold threshold
		c.Window.WindowAgeSeconds = &age
	})
	r := Build(c, nil)
	if !r.ColdWindow {
		t.Fatal("expected cold window")
	}
	ix := find(r, "t_a_idx")
	if ix == nil || ix.Confidence != Inconclusive {
		t.Fatalf("cold window must demote to inconclusive, got %+v", ix)
	}
}

func TestCatalogProven_noStatsCaveat_andRedundantWins(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "dup_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		c.Indexes.Redundant = []model.RedundantIndex{
			{Schema: "public", Table: "t", Name: "dup_idx", CoveredBy: "t_a_b_idx", Bytes: 1 << 21},
		}
	})
	ix := find(Build(c, nil), "dup_idx")
	if ix == nil || ix.Confidence != CatalogProven {
		t.Fatalf("a zero-scan index that is also redundant must be catalog_proven, got %+v", ix)
	}
	if ix.DoNotDrop {
		t.Errorf("catalog_proven must not be do-not-drop")
	}
	if len(ix.SearchTerms) != 0 {
		t.Errorf("catalog_proven needs no code search")
	}
	if !strings.Contains(ix.Reason, "t_a_b_idx") {
		t.Errorf("reason should name the covering index: %q", ix.Reason)
	}
}

func TestInvalidIndex_isCatalogProven(t *testing.T) {
	c := ctxWith(nil, func(c *model.Context) {
		c.Schema = &model.SchemaFingerprint{Objects: []model.SchemaObject{
			{Kind: "index", Identity: "public.orders.orders_bad_idx", Invalid: true},
		}}
	})
	ix := find(Build(c, nil), "orders_bad_idx")
	if ix == nil || ix.Confidence != CatalogProven {
		t.Fatalf("invalid index must be catalog_proven, got %+v", ix)
	}
	if ix.Table != "orders" || ix.Schema != "public" {
		t.Errorf("identity should parse to schema/table: %+v", ix)
	}
}

// forbiddenPhrases must never appear in any correlation output, at any confidence.
var forbiddenPhrases = []string{"safe to drop", "confirmed unused", "you can now remove", "you can remove", "go ahead and drop", "ok to drop"}

func assertNoAuthorization(t *testing.T, ix *IndexReport) {
	t.Helper()
	blob := strings.ToLower(ix.Reason + " " + ix.Note + " " + ix.IfFound + " " + ix.IfNotFound)
	if ix.Safety != nil {
		for _, g := range ix.Safety.BlockingCaveats {
			blob += " " + strings.ToLower(g.Text)
			if g.Verify != nil {
				blob += " " + strings.ToLower(*g.Verify)
			}
		}
	}
	for _, p := range forbiddenPhrases {
		if strings.Contains(blob, p) {
			t.Errorf("output for %s contains authorization phrase %q — must never read as a go-ahead", ix.Index, p)
		}
	}
}

// B2/B3: a FRESH not_found verdict corroborates (states observation continued,
// shows window growth) but never authorizes, and the precondition guard persists.
func TestVerdict_freshCorroboratesNeverAuthorizes(t *testing.T) {
	checked := time.Unix(1_700_000_000, 0)
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "t_a_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		c.Window.StatsWindowDays = f64(45)
		c.CollectedAt = checked.Add(20 * 24 * time.Hour) // 20d old vs 45d window → fresh
	})
	verdicts := map[string]PriorVerdict{
		"public.t_a_idx": {Verdict: "not_found_in_code", RepoRef: "abc123", CheckedAt: checked, StatsWindowDays: f64(12)},
	}
	ix := find(Build(c, verdicts), "t_a_idx")
	if ix == nil || ix.PriorVerdict == nil {
		t.Fatalf("prior verdict should be attached, got %+v", ix)
	}
	if ix.PriorVerdict.Stale {
		t.Error("a 20-day-old verdict against a 45-day window must not be stale")
	}
	if !strings.Contains(ix.Note, "12d to 45d") || !strings.Contains(ix.Note, "corroboration") {
		t.Errorf("fresh note should show growth + read as corroboration: %q", ix.Note)
	}
	if ix.Safety == nil || ix.Safety.BlockingCaveats[0].Kind != model.GuardPrecondition {
		t.Errorf("precondition guard must persist through the verdict: %+v", ix.Safety)
	}
	assertNoAuthorization(t, ix)
}

// B2: a verdict older than the window is STALE — stated in output, no strengthening.
func TestVerdict_staleStatedAndDoesNotStrengthen(t *testing.T) {
	checked := time.Unix(1_700_000_000, 0)
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "t_a_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		c.Window.StatsWindowDays = f64(30)
		c.CollectedAt = checked.Add(47 * 24 * time.Hour) // 47d old vs 30d window → stale
	})
	verdicts := map[string]PriorVerdict{
		"public.t_a_idx": {Verdict: "not_found_in_code", RepoRef: "abc123", CheckedAt: checked, StatsWindowDays: f64(12)},
	}
	ix := find(Build(c, verdicts), "t_a_idx")
	if ix == nil || ix.PriorVerdict == nil || !ix.PriorVerdict.Stale {
		t.Fatalf("verdict older than the window must be stale: %+v", ix.PriorVerdict)
	}
	if ix.PriorVerdict.AgeDays != 47 {
		t.Errorf("age = %dd, want 47", ix.PriorVerdict.AgeDays)
	}
	if !strings.Contains(ix.Note, "47 days old") || !strings.Contains(strings.ToLower(ix.Note), "may have changed") {
		t.Errorf("staleness must be stated in output: %q", ix.Note)
	}
	if strings.Contains(ix.Note, "corroboration,") || strings.Contains(ix.Note, "grown") {
		t.Errorf("a stale verdict must not strengthen: %q", ix.Note)
	}
	assertNoAuthorization(t, ix)
}

// B4 hard invariant: inconclusive is NEVER promoted, at the extreme — 365-day
// window + not_found verdict → still inconclusive, prohibition guard intact.
func TestVerdict_inconclusiveNeverPromoted_365d(t *testing.T) {
	checked := time.Unix(1_700_000_000, 0)
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "gin_idx", Method: "gin", Columns: []string{"tags"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		c.Window.StatsWindowDays = f64(365)
		c.CollectedAt = checked.Add(10 * 24 * time.Hour)
	})
	verdicts := map[string]PriorVerdict{
		"public.gin_idx": {Verdict: "not_found_in_code", CheckedAt: checked, StatsWindowDays: f64(360)},
	}
	ix := find(Build(c, verdicts), "gin_idx")
	if ix == nil || ix.Confidence != Inconclusive {
		t.Fatalf("inconclusive must stay inconclusive at 365d with a not_found verdict, got %+v", ix)
	}
	if !ix.DoNotDrop {
		t.Error("still do-not-drop")
	}
	if ix.Safety == nil || ix.Safety.BlockingCaveats[0].Kind != model.GuardProhibition {
		t.Errorf("prohibition guard must remain: %+v", ix.Safety)
	}
	assertNoAuthorization(t, ix)
}

func TestSort_catalogFirstInconclusiveLast(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "gin_idx", Method: "gin", Columns: []string{"tags"}, Bytes: 1 << 21},
		{Schema: "public", Table: "t", Name: "plain_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
		{Schema: "public", Table: "t", Name: "dup_idx", Method: "btree", Columns: []string{"b"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		c.Indexes.Redundant = []model.RedundantIndex{{Schema: "public", Table: "t", Name: "dup_idx", CoveredBy: "x"}}
	})
	r := Build(c, nil)
	if len(r.Indexes) != 3 {
		t.Fatalf("want 3, got %d", len(r.Indexes))
	}
	if r.Indexes[0].Confidence != CatalogProven || r.Indexes[len(r.Indexes)-1].Confidence != Inconclusive {
		t.Errorf("order should be catalog_proven..inconclusive, got %v/%v",
			r.Indexes[0].Confidence, r.Indexes[len(r.Indexes)-1].Confidence)
	}
}

// TestSafety_correlateGuards asserts every correlation entry carries a structured
// drop guard, keyed on a stable ID — including the catalog_proven redundant entry,
// which must carry the per-node/INCLUDE guard (Step 5) that lives on the finding.
func TestSafety_correlateGuards(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "plain", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
		{Schema: "public", Table: "t", Name: "gin", Method: "gin", Columns: []string{"tags"}, Bytes: 1 << 21},
	}, func(c *model.Context) {
		c.Indexes.Redundant = []model.RedundantIndex{{Schema: "public", Table: "t", Name: "dup", CoveredBy: "cov"}}
	})
	r := Build(c, nil)
	check := func(name, guardID, kind string) {
		ix := find(r, name)
		if ix == nil || ix.Safety == nil || len(ix.Safety.BlockingCaveats) == 0 {
			t.Fatalf("%s is missing a structured safety guard: %+v", name, ix)
		}
		g := ix.Safety.BlockingCaveats[0]
		if g.ID != guardID {
			t.Errorf("%s guard id = %q, want %q", name, g.ID, guardID)
		}
		if g.Action != model.ActionDropIndex {
			t.Errorf("%s guard action = %q, want DROP INDEX", name, g.Action)
		}
		if g.Kind != kind {
			t.Errorf("%s guard kind = %q, want %q", name, g.Kind, kind)
		}
	}
	check("plain", "correlate.needs_code_check", model.GuardPrecondition)
	check("gin", "correlate.inconclusive", model.GuardProhibition)
	check("dup", "correlate.redundant_covering", model.GuardPrecondition)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

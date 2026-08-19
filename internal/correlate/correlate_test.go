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

func TestVerdict_strengthensNeedsCodeCheck(t *testing.T) {
	c := ctxWith([]model.IndexStat{
		{Schema: "public", Table: "t", Name: "t_a_idx", Method: "btree", Columns: []string{"a"}, Bytes: 1 << 21},
	}, nil)
	verdicts := map[string]PriorVerdict{
		"public.t_a_idx": {Verdict: "not_found_in_code", RepoRef: "abc123", CheckedAt: time.Unix(1_700_000_000, 0), StatsWindowDays: f64(12)},
	}
	ix := find(Build(c, verdicts), "t_a_idx")
	if ix == nil || ix.PriorVerdict == nil {
		t.Fatalf("prior verdict should be attached, got %+v", ix)
	}
	if !strings.Contains(ix.Note, "strengthened") || !strings.Contains(ix.Note, "12d") || !strings.Contains(ix.Note, "45d") {
		t.Errorf("note should show window growth 12d -> 45d: %q", ix.Note)
	}
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

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

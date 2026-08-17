package advisor

import (
	"context"
	"strings"
)

// Planner is the minimal database surface the advisor drives. Implemented over a
// pgx READ ONLY transaction in the command; mocked in tests. Every method is a
// plan-only or hypothetical operation — none executes the inspected query or
// writes anything.
type Planner interface {
	// GenericPlan returns EXPLAIN (GENERIC_PLAN, FORMAT JSON) <query> as raw JSON.
	// GENERIC_PLAN (PG16+) plans a normalized $N query without values or execution.
	GenericPlan(ctx context.Context, query string) ([]byte, error)
	// CreateHypoIndex creates a hypothetical index from a CREATE INDEX statement
	// and returns its planner-visible name (hypopg_create_index). Nothing is built.
	CreateHypoIndex(ctx context.Context, ddl string) (name string, err error)
	// ResetHypo drops every hypothetical index in this session (hypopg_reset).
	ResetHypo(ctx context.Context) error
}

// QueryInput is one candidate-for-improvement slow query.
type QueryInput struct {
	QueryID  int64
	Text     string  // RAW normalized ($1) SQL, used only to plan — never stored
	Scrubbed string  // display form (scrubbed), safe for output
	Calls    int64
	SharePct float64 // % of total DB exec time
}

// Options tunes the advisor.
type Options struct {
	MinImprovement float64 // required fractional cost drop, e.g. 0.5 = 50%
}

// Stats reports what the advisor examined, for an honest "nothing found" message.
type Stats struct {
	QueriesConsidered int
	QueriesPlanned    int // successfully planned (GENERIC_PLAN worked)
	CandidatesTested  int
}

// Advise runs the deterministic-candidate → hypopg-validation loop over the given
// queries and returns only planner-confirmed recommendations. The caller is
// responsible for running this inside a single READ ONLY transaction (hypopg
// state is per-connection) and for the final ResetHypo — but Advise also resets
// between every candidate so plans never compound.
func Advise(ctx context.Context, p Planner, queries []QueryInput, opts Options) ([]Recommendation, Stats) {
	if opts.MinImprovement <= 0 {
		opts.MinImprovement = 0.5
	}
	var recs []Recommendation
	var st Stats

	for _, q := range queries {
		st.QueriesConsidered++
		clean := sanitizeQuery(q.Text)
		if clean == "" {
			continue
		}
		baseJS, err := p.GenericPlan(ctx, clean)
		if err != nil {
			continue // can't plan it (temp tables, unsupported params) — skip, don't fail
		}
		base, err := parsePlan(baseJS)
		if err != nil {
			continue
		}
		st.QueriesPlanned++
		costBefore := planCost(base)
		if costBefore <= 0 {
			continue
		}
		for _, cand := range candidatesFromPlan(base) {
			st.CandidatesTested++
			if rec, ok := validate(ctx, p, q, cand, clean, costBefore, opts.MinImprovement); ok {
				recs = append(recs, rec)
			}
		}
	}
	return dedupeRecommendations(recs), st
}

// validate creates one hypothetical index, re-plans, and confirms the planner
// switches to it with a real cost drop. It always resets hypopg afterward so the
// next candidate is tested in isolation.
func validate(ctx context.Context, p Planner, q QueryInput, cand Candidate, query string, costBefore, minImpr float64) (Recommendation, bool) {
	name, err := p.CreateHypoIndex(ctx, cand.DDL())
	if err != nil || name == "" {
		return Recommendation{}, false // bad DDL / nonexistent column — hypopg rejected it
	}
	defer p.ResetHypo(ctx) //nolint:errcheck // reset is best-effort; the tx is read-only

	afterJS, err := p.GenericPlan(ctx, query)
	if err != nil {
		return Recommendation{}, false
	}
	after, err := parsePlan(afterJS)
	if err != nil {
		return Recommendation{}, false
	}
	costAfter := planCost(after)

	// Two independent gates: the planner must actually PICK the hypothetical index
	// (name match), AND the estimated total cost must fall by the threshold. Either
	// alone is too weak — cost can wobble, and an unused index proves nothing.
	if !usesIndex(after, name) || costAfter >= costBefore*(1-minImpr) {
		return Recommendation{}, false
	}
	return Recommendation{
		Candidate: cand, IndexDDL: cand.DDL(),
		Schema: cand.Schema, Table: cand.Table, Columns: cand.Columns,
		QueryID: q.QueryID, QueryText: q.Scrubbed, Calls: q.Calls, SharePct: q.SharePct,
		CostBefore: costBefore, CostAfter: costAfter,
	}, true
}

// sanitizeQuery trims a single normalized statement for EXPLAIN: it drops a
// trailing semicolon (EXPLAIN takes one statement) and rejects anything that
// isn't a plain SELECT — no WITH (which can hide a writable CTE), no DML, no
// utility statement (whose pgss text can carry literals). Belt-and-braces on top
// of the READ ONLY transaction.
func sanitizeQuery(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "; \t\n\r")
	if s == "" {
		return ""
	}
	if !startsWithWord(s, "select") {
		return ""
	}
	if strings.Contains(s, ";") {
		return "" // a second statement snuck in — refuse
	}
	return s
}

// startsWithWord reports whether s begins with word followed by a non-identifier
// char (so "select" matches but "selection" doesn't), case-insensitively.
func startsWithWord(s, word string) bool {
	if len(s) < len(word) {
		return false
	}
	if !strings.EqualFold(s[:len(word)], word) {
		return false
	}
	if len(s) == len(word) {
		return true
	}
	c := s[len(word)]
	return !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
}

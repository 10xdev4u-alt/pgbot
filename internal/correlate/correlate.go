// Package correlate turns pgbot's index observations into an actionable,
// confidence-graded payload an agent can act on WITHOUT pgbot ever reading the
// repository. pgbot knows the database side (which index, which columns, whether
// it's provable from the catalog alone); the agent has the code. This package
// emits what to search for and exactly how to interpret the result.
//
// Three confidence levels, and the boundary between them is load-bearing:
//
//   - catalog_proven  — provable from the catalog alone (invalid or
//     redundant/duplicate). No stats window, no code check; the catalog justifies
//     the drop, still subject to the guard (covering-index / no-running-build).
//   - needs_code_check — zero scans, plain btree over bare columns. Actionable
//     ONLY in combination with the code: grep the search terms, apply if_found /
//     if_not_found.
//   - inconclusive    — GIN/GiST/BRIN, expression, partial, or a cold window. A
//     query shape that simply hasn't run can be served by these, so an empty code
//     search proves nothing. NEVER promoted to actionable.
package correlate

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pgrundev/pgbot/internal/model"
)

// Confidence is the strength of the "this index is droppable" claim.
type Confidence string

const (
	CatalogProven  Confidence = "catalog_proven"
	NeedsCodeCheck Confidence = "needs_code_check"
	Inconclusive   Confidence = "inconclusive"
)

// The instruction and outcome strings are fixed wording — the WHERE-vs-SELECT
// distinction in particular is the single most important line in the payload:
// without it an agent finds a column in a SELECT list and wrongly reports "used".
const (
	instruction = "Search the repository for these identifiers in filtering positions only: " +
		"WHERE clauses, JOIN ... ON conditions, ORDER BY, GROUP BY, and ORM equivalents " +
		"(where, orderBy, groupBy, findUnique, findFirst, findMany filters, cursor). " +
		"An appearance in SELECT lists, return types, model definitions, serializers, or " +
		"migration files does NOT indicate index usage."
	ifFound        = "Index is used by an infrequent path not captured in the stats window. Do not drop."
	inconclusiveIf = "The identifier appears inside dynamically constructed SQL (string concatenation, " +
		"raw query builders, dynamic filter maps). No conclusion possible from static search."
	doNotDrop = "Do not DROP INDEX on this evidence."
)

// PriorVerdict is a code-search result recorded on an earlier run (from the
// index_verdicts store). Carrying it forward is what turns a one-off grep into a
// compounding asset: the same verdict against a growing stats window gets stronger.
type PriorVerdict struct {
	Verdict         string    `json:"verdict"`
	Source          string    `json:"source,omitempty"`
	RepoRef         string    `json:"repo_ref,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
	StatsWindowDays *float64  `json:"stats_window_days_then,omitempty"`
	// Stale is set when the verdict is older than the current stats window — the
	// code may have changed since, so it is no longer the fresher signal. A stale
	// verdict is kept as context but must never increase confidence.
	Stale   bool `json:"stale"`
	AgeDays int  `json:"age_days"`
}

// IndexReport is one index's correlation entry.
type IndexReport struct {
	Index           string     `json:"index"`
	Table           string     `json:"table,omitempty"`
	Schema          string     `json:"schema,omitempty"`
	Columns         []string   `json:"columns,omitempty"`
	Kind            string     `json:"kind,omitempty"` // access method
	IsUnique        bool       `json:"is_unique"`
	IsPartial       bool       `json:"is_partial"`
	IsExpression    bool       `json:"is_expression"`
	SizeBytes       int64      `json:"size_bytes"`
	Scans           int64      `json:"scans"`
	StatsWindowDays *float64   `json:"stats_window_days,omitempty"`
	StatsResetAt    *time.Time `json:"stats_reset_at,omitempty"`

	Confidence Confidence `json:"confidence"`
	Reason     string     `json:"reason"`

	// Populated only for needs_code_check.
	SearchTerms    []string `json:"search_terms,omitempty"`
	Instruction    string   `json:"instruction,omitempty"`
	IfFound        string   `json:"if_found,omitempty"`
	IfNotFound     string   `json:"if_not_found,omitempty"`
	InconclusiveIf string   `json:"inconclusive_if,omitempty"`

	// DoNotDrop is set on every inconclusive entry (never on the others).
	DoNotDrop bool `json:"do_not_drop,omitempty"`
	// Safety carries the deterministic drop guard as structured data (the same
	// contract as model.Finding.Safety) so a consumer of the correlation payload
	// can't lose it — DROP INDEX is destructive on every entry here.
	Safety       *model.Safety `json:"safety,omitempty"`
	PriorVerdict *PriorVerdict `json:"prior_verdict,omitempty"`
	Note         string        `json:"note,omitempty"`
}

// Report is the whole correlation payload.
type Report struct {
	Fingerprint       string        `json:"fingerprint,omitempty"`
	ColdWindow        bool          `json:"cold_window"`
	ReplicationActive bool          `json:"replication_active"`
	StatsWindowDays   *float64      `json:"stats_window_days,omitempty"`
	Indexes           []IndexReport `json:"indexes"`
	Note              string        `json:"note"`
}

const reportNote = "pgbot never reads your repository and never drops anything. " +
	"catalog_proven = the catalog alone justifies the drop, still subject to the guard. " +
	"needs_code_check = search the terms per the instruction, then apply if_found/if_not_found, subject to the guard. " +
	"inconclusive = do NOT drop on this evidence, and an empty code search does not change that."

func key(schema, name string) string { return schema + "." + name }

// Build assembles the correlation report from a collected Context. verdicts is
// the prior code-search verdicts for this database (keyed by "schema.index"),
// loaded from the store by the caller — pass nil if none.
func Build(c *model.Context, verdicts map[string]PriorVerdict) Report {
	r := Report{Note: reportNote}
	if c == nil {
		return r
	}
	r.Fingerprint = c.Fingerprint
	r.ColdWindow = c.Window.ColdWindow()
	r.ReplicationActive = replicationActive(c)
	winDays := c.Window.StatsWindowDays
	r.StatsWindowDays = winDays
	resetAt := c.Window.StatsResetAt
	if c.Indexes == nil {
		return r
	}

	invalid := invalidIndexKeys(c) // key -> table
	redundant := map[string]model.RedundantIndex{}
	for _, rd := range c.Indexes.Redundant {
		redundant[key(rd.Schema, rd.Name)] = rd
	}
	// A by-name lookup so redundant/invalid entries that aren't in the zero-scan
	// set can still be enriched with columns/kind if we happened to observe them.
	stByKey := map[string]model.IndexStat{}
	for _, s := range c.Indexes.Largest {
		stByKey[key(s.Schema, s.Name)] = s
	}
	for _, s := range c.Indexes.Unused {
		stByKey[key(s.Schema, s.Name)] = s
	}

	seen := map[string]bool{}
	var out []IndexReport

	// 1) Zero-scan unused indexes — the main correlation surface.
	for _, ix := range c.Indexes.Unused {
		k := key(ix.Schema, ix.Name)
		seen[k] = true
		ir := base(ix, winDays, resetAt)
		switch {
		case invalid[k] != "":
			markCatalog(&ir, "invalid index (indisvalid = false — likely a failed CREATE INDEX CONCURRENTLY); provable from the catalog alone, no code check needed", invalidSafetyGuard())
		case redundant[k].CoveredBy != "":
			markCatalog(&ir, redundantReason(redundant[k]), redundantSafety(redundant[k]))
		default:
			classifyStats(&ir, r.ColdWindow, winDays)
		}
		attachVerdict(&ir, verdicts, k, winDays, c.CollectedAt)
		out = append(out, ir)
	}
	// 2) Redundant indexes not already emitted (they may have scans).
	for _, rd := range c.Indexes.Redundant {
		k := key(rd.Schema, rd.Name)
		if seen[k] {
			continue
		}
		seen[k] = true
		ir := IndexReport{Index: rd.Name, Schema: rd.Schema, Table: rd.Table, SizeBytes: rd.Bytes, StatsWindowDays: winDays, StatsResetAt: resetAt}
		if s, ok := stByKey[k]; ok {
			fillFromStat(&ir, s)
		}
		markCatalog(&ir, redundantReason(rd), redundantSafety(rd))
		attachVerdict(&ir, verdicts, k, winDays, c.CollectedAt)
		out = append(out, ir)
	}
	// 3) Invalid indexes not already emitted.
	for k, table := range invalid {
		if seen[k] {
			continue
		}
		seen[k] = true
		schema, name := splitKey(k)
		ir := IndexReport{Index: name, Schema: schema, Table: table, StatsWindowDays: winDays, StatsResetAt: resetAt}
		if s, ok := stByKey[k]; ok {
			fillFromStat(&ir, s)
		}
		markCatalog(&ir, "invalid index (indisvalid = false — likely a failed CREATE INDEX CONCURRENTLY); it is not enforcing anything and can be dropped and rebuilt", invalidSafetyGuard())
		out = append(out, ir)
	}

	sortReports(out)
	r.Indexes = out
	return r
}

func base(ix model.IndexStat, winDays *float64, resetAt *time.Time) IndexReport {
	return IndexReport{
		Index: ix.Name, Table: ix.Table, Schema: ix.Schema,
		Columns: ix.Columns, Kind: ix.Method,
		IsUnique: ix.Unique, IsPartial: ix.Partial, IsExpression: ix.Expression,
		SizeBytes: ix.Bytes, Scans: ix.Scans,
		StatsWindowDays: winDays, StatsResetAt: resetAt,
	}
}

func fillFromStat(ir *IndexReport, s model.IndexStat) {
	ir.Columns, ir.Kind = s.Columns, s.Method
	ir.IsUnique, ir.IsPartial, ir.IsExpression = s.Unique, s.Partial, s.Expression
	ir.Scans = s.Scans
	if ir.SizeBytes == 0 {
		ir.SizeBytes = s.Bytes
	}
}

func markCatalog(ir *IndexReport, reason string, guards ...model.SafetyGuard) {
	ir.Confidence = CatalogProven
	ir.Reason = reason
	ir.Safety = &model.Safety{BlockingCaveats: guards}
}

// guardProhibition / guardPrecondition mirror findings' helpers — DROP INDEX is
// destructive on every correlation entry, so each carries a structured guard.
func guardProhibition(id, action, text string) model.SafetyGuard {
	return model.SafetyGuard{ID: id, Kind: model.GuardProhibition, Action: action, Text: text}
}

func guardPrecondition(id, action, text, verify string) model.SafetyGuard {
	return model.SafetyGuard{ID: id, Kind: model.GuardPrecondition, Action: action, Text: text, Verify: &verify}
}

func redundantReason(rd model.RedundantIndex) string {
	return fmt.Sprintf("redundant: its columns are a leading prefix of (or identical to) %s, "+
		"which already serves these lookups; provable from the catalog alone", rd.CoveredBy)
}

// redundantSafety is the drop guard for a redundant index. Redundancy is a catalog
// fact, but "prefix" can hide a difference the covering index doesn't actually
// cover (extra INCLUDE columns, a different opclass/collation), and index CHOICE is
// per-node — so the covering index must be confirmed before the drop.
func redundantSafety(rd model.RedundantIndex) model.SafetyGuard {
	return guardPrecondition("correlate.redundant_covering", model.ActionDropIndex,
		"A leading-prefix match can hide a difference the covering index does not cover (extra INCLUDE columns, a different opclass or collation), and index choice is per-node.",
		fmt.Sprintf("the covering index (%s) has the same INCLUDE columns, opclass, and collation, and is the one used on any replicas, then DROP INDEX CONCURRENTLY", rd.CoveredBy))
}

func invalidSafetyGuard() model.SafetyGuard {
	return guardPrecondition("correlate.invalid_no_build", model.ActionDropIndex,
		"An invalid index is likely a stopped CREATE INDEX CONCURRENTLY, but a build may still be running.",
		"no CREATE INDEX build is running, then DROP INDEX CONCURRENTLY and rebuild")
}

// classifyStats grades a zero-scan index that is neither invalid nor redundant.
// A cold window or any non-btree/expression/partial index is inconclusive and
// stays inconclusive regardless of any code search. Only a plain btree over bare
// columns earns needs_code_check and a correlation payload.
func classifyStats(ir *IndexReport, cold bool, winDays *float64) {
	switch {
	case cold:
		inconclusive(ir, "cold statistics window — scan counts just started from zero, so 'unused' is not yet meaningful")
	case ir.Kind != "" && ir.Kind != "btree":
		inconclusive(ir, ir.Kind+" index — may serve a query shape (containment, similarity, range) that a plain scan count cannot rule out")
	case ir.IsExpression:
		inconclusive(ir, "expression index — serves a computed expression, not a bare column; a code search for column names cannot confirm it is unused")
	case ir.IsPartial:
		inconclusive(ir, "partial index — may serve a narrow but critical path that runs rarely")
	default:
		ir.Confidence = NeedsCodeCheck
		ir.Reason = "zero scans in the observed window; plain btree over bare columns — actionable only after confirming the columns are absent from query filters in the code"
		ir.SearchTerms = searchTermsFor(ir.Columns)
		ir.Instruction = instruction
		ir.IfFound = ifFound
		ir.IfNotFound = ifNotFound(winDays)
		ir.InconclusiveIf = inconclusiveIf
		ir.Safety = &model.Safety{BlockingCaveats: []model.SafetyGuard{guardPrecondition(
			"correlate.needs_code_check", model.ActionDropIndex,
			"Zero scans in one window on one node; a code search is only half the evidence.",
			"the columns are absent from query FILTERS (WHERE/JOIN/ORDER BY/GROUP BY), not just SELECT lists — and that no cron/analytics/BI job or other repository uses it (this covered one repo at one commit)",
		)}}
	}
}

func inconclusive(ir *IndexReport, reason string) {
	ir.Confidence = Inconclusive
	ir.Reason = reason
	ir.DoNotDrop = true
	ir.Note = doNotDrop
	ir.Safety = &model.Safety{BlockingCaveats: []model.SafetyGuard{guardProhibition(
		"correlate.inconclusive", model.ActionDropIndex,
		doNotDrop+" "+reason+". An empty code search does not change that.",
	)}}
}

func ifNotFound(winDays *float64) string {
	return fmt.Sprintf("Two independent signals agree (zero scans + absent from code filters), but neither is proof: "+
		"(1) the stats window is %s; (2) a MONTHLY, QUARTERLY, or ANNUAL job — billing cycles, compliance exports, "+
		"year-end batches — will not appear in it; a 40-day window does not see a quarterly job; (3) cron, analytics, "+
		"or BI tools, or a repository not searched here, may still use it. The precondition to drop still applies.",
		windowStr(winDays))
}

func windowStr(winDays *float64) string {
	if winDays == nil {
		return "of unknown length"
	}
	if *winDays < 1 {
		return "under a day"
	}
	return fmt.Sprintf("%.0f days", *winDays)
}

// attachVerdict carries a stored code-search verdict forward. A verdict records
// what one repository looked like at one commit, at one moment: it is STALE once
// older than the current stats window (the observation is now the fresher signal),
// and a stale verdict must never increase confidence — it only ever adds context.
// The strengthened wording states that observation continued; it never authorizes
// a drop, and the precondition guard stays attached regardless.
func attachVerdict(ir *IndexReport, verdicts map[string]PriorVerdict, k string, winDays *float64, now time.Time) {
	v, ok := verdicts[k]
	if !ok {
		return
	}
	ageDays := now.Sub(v.CheckedAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	vv := v
	vv.AgeDays = int(ageDays + 0.5)
	// Stale once older than the current window. With an unknown window we cannot
	// establish that the verdict is the fresher signal, so treat it as stale.
	vv.Stale = winDays == nil || ageDays > *winDays
	ir.PriorVerdict = &vv

	// Only a fresh "not found in code" verdict on a needs_code_check index adds
	// corroboration. Everything else just carries the structured verdict.
	if ir.Confidence != NeedsCodeCheck || v.Verdict != "not_found_in_code" {
		return
	}
	if vv.Stale {
		ir.Note = stalenessNote(v, vv.AgeDays)
		return
	}
	ir.Note = corroborationNote(v, winDays)
}

// stalenessNote states the verdict's age and that the repo may have changed —
// explicitly, in output, never as clearance.
func stalenessNote(v PriorVerdict, ageDays int) string {
	ref := ""
	if v.RepoRef != "" {
		ref = " (commit " + v.RepoRef + ")"
	}
	return fmt.Sprintf("A prior code search found no filter references, but that check is %d days old%s — "+
		"the repository may have changed since, so it is no longer the fresher signal. It stays as context, not corroboration.",
		ageDays, ref)
}

// corroborationNote states that observation continued and the code check still
// holds — corroboration, not authorization. Must not contain any go-ahead phrasing.
func corroborationNote(v PriorVerdict, winDays *float64) string {
	ref := ""
	if v.RepoRef != "" {
		ref = ", commit " + v.RepoRef
	}
	grew := ""
	if v.StatsWindowDays != nil && winDays != nil && *winDays > *v.StatsWindowDays {
		grew = fmt.Sprintf(" over a window that has grown from %.0fd to %.0fd", *v.StatsWindowDays, *winDays)
	}
	return fmt.Sprintf("A prior code search (recorded %s%s) found no filter references, and the index has stayed "+
		"zero-scan since%s. This is corroboration, not clearance — the precondition below still applies.",
		v.CheckedAt.Format("2006-01-02"), ref, grew)
}

// searchTermsFor emits, for each bare column, the camelCase / snake_case /
// PascalCase / CONSTANT_CASE variants plus the raw name, deduped — pgbot knows
// the database name; the agent shouldn't have to guess the codebase convention.
func searchTermsFor(cols []string) []string {
	var out []string
	seen := map[string]bool{}
	push := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, col := range cols {
		push(col)
		for _, v := range caseVariants(splitWords(col)) {
			push(v)
		}
	}
	return out
}

// splitWords breaks an identifier into lowercase words on underscores/dashes and
// at camelCase boundaries (including ACRONYM→Word transitions).
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0:
			prev := rs[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				flush() // fooBar -> foo | Bar
			} else if unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1]) {
				flush() // HTTPServer -> HTTP | Server
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

func caseVariants(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	camel := words[0]
	pascal := title(words[0])
	for _, w := range words[1:] {
		camel += title(w)
		pascal += title(w)
	}
	snake := strings.Join(words, "_")
	constant := strings.ToUpper(snake)
	return []string{camel, snake, pascal, constant}
}

func title(w string) string {
	if w == "" {
		return ""
	}
	rs := []rune(w)
	return strings.ToUpper(string(rs[0])) + string(rs[1:])
}

func replicationActive(c *model.Context) bool {
	return c.Replication != nil && (len(c.Replication.Replicas) > 0 || c.Replication.IsReplica)
}

// invalidIndexKeys returns "schema.index" -> table for every invalid index in the
// schema fingerprint. Identity is "schema.table.index".
func invalidIndexKeys(c *model.Context) map[string]string {
	out := map[string]string{}
	if c.Schema == nil {
		return out
	}
	for _, o := range c.Schema.Objects {
		if o.Kind != "index" || !o.Invalid {
			continue
		}
		schema, table, name := splitIdentity(o.Identity)
		if name == "" {
			continue
		}
		out[key(schema, name)] = table
	}
	return out
}

func splitIdentity(id string) (schema, table, name string) {
	parts := strings.Split(id, ".")
	switch {
	case len(parts) >= 3:
		return parts[0], strings.Join(parts[1:len(parts)-1], "."), parts[len(parts)-1]
	case len(parts) == 2:
		return parts[0], "", parts[1]
	case len(parts) == 1:
		return "", "", parts[0]
	}
	return "", "", ""
}

func splitKey(k string) (schema, name string) {
	if i := strings.Index(k, "."); i >= 0 {
		return k[:i], k[i+1:]
	}
	return "", k
}

func confidenceRank(c Confidence) int {
	switch c {
	case CatalogProven:
		return 0
	case NeedsCodeCheck:
		return 1
	default:
		return 2
	}
}

func sortReports(rs []IndexReport) {
	sort.SliceStable(rs, func(i, j int) bool {
		ri, rj := confidenceRank(rs[i].Confidence), confidenceRank(rs[j].Confidence)
		if ri != rj {
			return ri < rj
		}
		return rs[i].SizeBytes > rs[j].SizeBytes
	})
}

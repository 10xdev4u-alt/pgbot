package render

import (
	"fmt"
	"strings"

	"github.com/pgrundev/pgbot/internal/model"
)

// The default view is a vital-signs dashboard: a few headline gauges as bar
// meters, then the named checks that passed. It's still findings-first — the
// meters ARE the findings, visualized — just quicker to scan than sentences.

type statusKind int

const (
	kOK statusKind = iota
	kWatch
	kBad
	kInfo
)

type meter struct {
	label  string
	fill   float64 // 0..1 bar fill
	value  string  // right-hand magnitude, e.g. "99.2%" or "43 GiB"
	status string  // disposition word, e.g. "ok" / "watch" / "query 4f2a"
	kind   statusKind
}

func renderDashboard(b *strings.Builder, st styler, c *model.Context, width int) {
	meters := buildMeters(c)
	if len(meters) == 0 {
		fmt.Fprintln(b, st.good("✓ all clear — nothing needs attention"))
	} else {
		for _, m := range meters {
			renderMeter(b, st, m)
		}
	}
	fmt.Fprintln(b)

	// The "checked ·" line: name what pgbot examined and found clean — minus the
	// vitals we already showed as meters, to avoid saying the same thing twice.
	shownAsMeter := map[string]bool{}
	for _, m := range meters {
		switch m.label {
		case "cache hit":
			shownAsMeter["cache hit"] = true
		case "rollbacks":
			shownAsMeter["rollbacks"] = true
		case "lock wait":
			shownAsMeter["lock waits"] = true
		}
	}
	var passed []string
	for _, p := range passedChecks(c) {
		if !shownAsMeter[p] {
			passed = append(passed, p)
		}
	}
	if len(passed) > 0 {
		lines := wrapText(strings.Join(passed, " · "), width-12)
		for i, line := range lines {
			prefix := st.good("checked · ")
			if i > 0 {
				prefix = "          " // align under the names
			}
			fmt.Fprintf(b, "%s%s\n", prefix, st.dim(line))
		}
		fmt.Fprintln(b)
	}

	fmt.Fprintln(b, st.dim("Details: pgbot inspect --full   Machine-readable: --json"))
	fmt.Fprintln(b, st.dim(`Ask it: pgbot ask "why is it slow?"`))
}

func renderMeter(b *strings.Builder, st styler, m meter) {
	bar := bar20(m.fill)
	paint := statusColor(st, m.kind)
	fmt.Fprintf(b, "  %-10s %s  %-7s  %s\n", m.label, paint(bar), m.value, paint(m.status))
}

// bar20 draws a 20-cell filled/empty meter in brackets.
func bar20(fill float64) string {
	const n = 20
	if fill < 0 {
		fill = 0
	}
	if fill > 1 {
		fill = 1
	}
	filled := int(fill*float64(n) + 0.5)
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", n-filled) + "]"
}

func statusColor(st styler, k statusKind) func(string) string {
	switch k {
	case kOK:
		return st.good
	case kWatch:
		return st.warn
	case kBad:
		return st.crit
	default:
		return st.dim
	}
}

// buildMeters selects the headline gauges: the vital ratios (cache hit, lock
// wait, rollbacks) shown always when their data is present, then one row per
// warn/critical finding not already covered by a vital.
func buildMeters(c *model.Context) []meter {
	var ms []meter
	covered := map[string]bool{
		"low_cache_hit": true, "wait_lock_contention": true, "wait_io_bound": true,
		"wait_lwlock_pressure": true, "high_rollback_ratio": true,
	}

	if c.Health != nil && c.Health.CacheHitRatio != nil {
		r := *c.Health.CacheHitRatio
		k, s := kOK, "ok"
		if r < 0.90 {
			k, s = kWatch, "low"
		}
		ms = append(ms, meter{"cache hit", r, pct(r), s, k})
	}

	if w := c.WaitProfile; w != nil && w.Available && !w.Thin() {
		if lock := bucketShare(w, "Lock"); lock > 0 {
			k, s := kOK, "ok"
			if q := topLockQuery(w); q != nil && q.LockShare > 0.30 {
				k, s = kBad, "query "+queryTag(q.QueryID)
			}
			ms = append(ms, meter{"lock wait", lock, pct(lock), s, k})
		}
	}

	if c.Health != nil && c.Health.RollbackRatio != nil {
		r := *c.Health.RollbackRatio
		k, s := kOK, "ok"
		if r >= 0.10 {
			k, s = kWatch, "watch"
		}
		ms = append(ms, meter{"rollbacks", r, pct(r), s, k})
	}

	for _, f := range c.Findings {
		if covered[f.ID] || (f.Severity != model.SeverityWarn && f.Severity != model.SeverityCritical) {
			continue
		}
		k, s := kWatch, "review"
		if f.Severity == model.SeverityCritical {
			k, s = kBad, "fail"
		}
		ms = append(ms, meter{shortLabel(f.ID), f.Impact.Score / 100, leadingMagnitude(f.Impact.Estimate), s, k})
		if len(ms) >= 7 {
			break
		}
	}
	return ms
}

func bucketShare(w *model.WaitProfile, typ string) float64 {
	for _, b := range w.Buckets {
		if b.Type == typ {
			return b.Share
		}
	}
	return 0
}

// shortLabel maps a finding id to a compact meter label.
func shortLabel(id string) string {
	switch id {
	case "unused_indexes":
		return "idle idx"
	case "table_bloat":
		return "bloat"
	case "seq_scan_heavy":
		return "seq scan"
	case "index_invalid":
		return "bad idx"
	case "blocking_chains":
		return "locks"
	case "idle_in_transaction":
		return "idle txn"
	case "long_running_transaction":
		return "long txn"
	default:
		if i := strings.IndexByte(id, '_'); i > 0 {
			return id[:i]
		}
		return id
	}
}

// leadingMagnitude pulls the compact magnitude out of an Impact.Estimate, e.g.
// "≈43.0 GiB reclaimable" → "43.0 GiB", "1 session(s), longest 0s" → "1 session".
func leadingMagnitude(estimate string) string {
	s := strings.TrimPrefix(strings.TrimSpace(estimate), "≈")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	out := strings.TrimRight(fields[0], ",")
	if len(fields) > 1 {
		out += " " + strings.TrimRight(fields[1], ",")
	}
	return out
}

// pgLower renders "postgres 16.3" for the header.
func pgLower(num int) string {
	if num == 0 {
		return "postgres"
	}
	return fmt.Sprintf("postgres %d.%d", num/10000, num%100)
}

---
id: query_slowdown
severity: warn
critical_when: ""
dimension: latency
object: cluster
scope: history
requires: [pg_stat_statements, a baseline snapshot]
thresholds: []
related: [stale_statistics]
---

# query_slowdown

**Severity:** warn · **Dimension:** latency · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** pg_stat_statements, a baseline snapshot

## What pgbot observed

Comparing this run against a stored **baseline snapshot**, a query's mean execution time
regressed sharply. pgbot scans the `query.mean_ms` deltas and picks the worst one meeting
both bars: the factor `After/Before` is **at least 2×** (`querySlowdownFactor = 2.0`) *and*
the new mean is **at least 10 ms** (`querySlowdownMinMs = 10.0`), so trivially fast queries
that double from 0.1 ms to 0.2 ms aren't flagged as noise. The mean itself is
`total_exec_time / calls` from `pg_stat_statements` for that normalized query.

This finding is **temporal** — it needs a prior run to diff against, which is exactly what a
one-shot stats read cannot produce. If `pg_stat_statements` is currently evicting entries,
pgbot attaches a load-bearing caveat and drops confidence from `0.8` to `0.4`, because the
query may have been evicted and re-entered (its counters reset to zero), making the
"regression" an artifact rather than a real change.

## Why it matters

A query that suddenly runs 2×+ slower is the single most actionable signal in a diagnostic:
something *changed*. The usual culprits are a plan flip after the table grew past a planner
threshold, an index that was dropped or invalidated (a failed `CREATE INDEX CONCURRENTLY`),
or statistics that went stale so the planner now misestimates row counts. Because it's a
per-query regression rather than a cluster average, it points you straight at the statement
to investigate.

## How to verify it yourself

Read the current mean directly from `pg_stat_statements` and compare it against the mean
you remember (or a snapshot you keep):

```sql
-- READ-ONLY. Current mean execution time per normalized query, slowest first.
SELECT queryid,
       calls,
       round(mean_exec_time::numeric, 2)  AS mean_ms,
       round(total_exec_time::numeric, 2) AS total_ms,
       left(query, 80)                    AS query
FROM pg_stat_statements
WHERE calls > 0
  AND mean_exec_time >= 10          -- pgbot's 10 ms floor
ORDER BY mean_exec_time DESC
LIMIT 20;
```

Then inspect *why* it's slower: run `EXPLAIN (ANALYZE, BUFFERS)` on the statement and check
whether the plan changed, whether an expected index is missing or `indisvalid = false`, and
when the table was last analyzed:

```sql
SELECT relname, last_analyze, last_autoanalyze, n_live_tup, n_mod_since_analyze
FROM pg_stat_user_tables
ORDER BY n_mod_since_analyze DESC;
```

## How to fix it

The regression is a symptom; find the plan change behind it:

1. **Re-check statistics.** Run `ANALYZE <table>` (or raise the table's
   `default_statistics_target`) so the planner's row estimates match reality — stale stats
   are the most common cause of a plan flip. See [stale_statistics](stale_statistics.md).
2. **Restore the missing index.** If a `CREATE INDEX CONCURRENTLY` failed it leaves an
   `INVALID` index the planner ignores; drop and rebuild it. Confirm the index the fast
   plan relied on still exists and is valid.
3. **Compare the plans.** `EXPLAIN (ANALYZE, BUFFERS)` now vs. what you expect; a switch
   from Index Scan to Seq Scan (or a nested-loop→hash-join flip) after the table grew is the
   classic pattern.
4. If eviction is churning `pg_stat_statements`, treat the caveat seriously and fix that
   first — see [pgss_entries_evicted](pgss_entries_evicted.md) — before trusting the delta.

## When to ignore it

The regression is explained and accepted — e.g. a deliberate schema change traded per-query
latency for something else, or the baseline was captured during an unrepresentative quiet
period:

```toml
[[ignore]]
finding = "query_slowdown"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- It compares **cumulative means** (`total_exec_time/calls` since the counters were last
  reset), not individual executions. A change in the *mix* of parameters behind one
  normalized query — cheap calls one run, expensive ones the next — moves the mean without
  any plan actually regressing.
- When `pg_stat_statements` is at capacity and evicting, the "before" and "after" may not
  be the same accumulation of calls — a query evicted and re-entered starts its counters at
  zero, so a low "before" mean can manufacture a fake regression. That is exactly why pgbot
  attaches its caveat and drops confidence to 0.4 in that case (see
  [pgss_entries_evicted](pgss_entries_evicted.md)).
- It sees that the mean rose, not *why*: it cannot capture the plan, the parameters, or the
  data distribution that caused the flip. The `EXPLAIN` step above is how you get the cause.
- Without a baseline snapshot it emits nothing at all — a fresh install or a wiped history
  has no "before" to diff against.

## Related

- [stale_statistics](stale_statistics.md) — stale planner statistics are the most common
  reason a query's plan flips and its mean time regresses.

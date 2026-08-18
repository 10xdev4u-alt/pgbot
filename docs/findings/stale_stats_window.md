---
id: stale_stats_window
severity: info
critical_when: ""
dimension: cost
object: cluster
scope: workload
requires: []
thresholds: []
related: [query_slowdown]
---

# stale_stats_window

**Severity:** info · **Dimension:** cost · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The cumulative statistics window is very old. pgbot computes `StatsWindowDays` as
`now − stats_reset` (the age of the `pg_stat_database.stats_reset` timestamp, or the
postmaster start if never reset) and fires when it is **at least 30 days**
(`staleStatsWarnDays = 30`). Every rate and ratio derived from the cumulative counters has
been averaging over that entire span.

## Why it matters

Counter-derived metrics — cache-hit ratio, commit/rollback ratio, `pg_stat_statements`
totals, sequential-vs-index scan counts — are cumulative since the last reset. Averaged over
a month or a year, they are dominated by history and go nearly flat: a real regression that
started yesterday is diluted into weeks of healthy numbers and effectively disappears. A
sudden spike in disk reads today barely moves a ratio computed over 300 days. The counters
aren't *wrong*, they're just too coarse to reveal recent change — which is exactly the
change you usually care about.

## How to verify it yourself

Read the reset timestamp and its age directly:

```sql
-- READ-ONLY. Age of the cumulative stats window per database.
SELECT datname,
       stats_reset,
       round(extract(epoch FROM now() - stats_reset) / 86400.0, 1) AS window_days
FROM pg_stat_database
WHERE stats_reset IS NOT NULL
ORDER BY stats_reset ASC;   -- oldest (stalest) window first
```

A `window_days` of 30+ (and often hundreds) is what pgbot flags. `stats_reset` is set by
`pg_stat_reset()` / `pg_stat_statements_reset()`, and is otherwise the value from when the
counters last started accumulating.

## How to fix it

This is an `info` observation about *interpretation*, not a defect to repair — pick the
approach that fits how you read the numbers:

1. **Reset the baseline** so ratios reflect recent behavior:
   ```sql
   SELECT pg_stat_reset();               -- cumulative table/db/index counters
   SELECT pg_stat_statements_reset();    -- per-query stats, if the extension is present
   ```
   Do this knowingly — it discards the history other tools may rely on, and cache-hit and
   similar ratios will read as noisy until the counters re-accumulate.
2. **Prefer windowed/delta signals** for "what changed recently." pgbot's own cross-run
   deltas (and [query_slowdown](query_slowdown.md)) are computed between snapshots, so they
   isolate recent change regardless of how old the cumulative window is.
3. **Adopt a periodic reset cadence** (e.g. monthly) if you routinely reason from cumulative
   ratios, so the averaging window stays meaningful.

## When to ignore it

You intentionally keep counters running long-term (e.g. for lifetime totals or capacity
trending) and read recent change from deltas instead of the cumulative ratios:

```toml
[[ignore]]
finding = "stale_stats_window"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- It knows the **age** of the window, not what the counters would look like over a shorter,
  more relevant span — it can't retroactively re-slice cumulative totals into recent-only
  values.
- It reads `stats_reset` from `pg_stat_database`, which tracks the block/tuple/transaction
  counters; other subsystems reset independently (`pg_stat_statements` has its own reset
  time, `pg_stat_bgwriter`/`pg_stat_checkpointer` another), so different metrics can span
  different windows than the one reported here.
- On scale-to-zero serverless the opposite risk applies: the window can be *too young* (a
  cold window under 900 s), which pgbot handles separately by suppressing counter-based
  findings.

## Related

- [query_slowdown](query_slowdown.md) — the delta-based signal to trust for recent change
  when the cumulative window is too old to reveal it.

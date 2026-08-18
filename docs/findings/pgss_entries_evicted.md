---
id: pgss_entries_evicted
severity: warn
critical_when: ""
dimension: throughput
object: cluster
scope: history
requires: [pg_stat_statements]
thresholds: []
related: [pg_stat_statements_missing]
---

# pgss_entries_evicted

**Severity:** warn · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** pg_stat_statements

## What pgbot observed

`pg_stat_statements` is discarding entries. pgbot reads
`pg_stat_statements_info.dealloc` — the running count of times the extension evicted
least-used entries to make room — together with the current entry count against the
`pg_stat_statements.max` ceiling. The literal condition (`pgssEvicting`) is
`PgssDealloc > 0` **or** (`PgssMax > 0 && PgssCount >= PgssMax`): the tracker has evicted at
least once, or it is sitting full at its `max`. This is a **trust** finding — it also stamps
the queries section's reason so any consumer knows the top-queries list is now a biased
sample.

## Why it matters

`pg_stat_statements` keeps a fixed number of normalized-query slots (`max`, default 5000).
When the distinct-query population exceeds that, it evicts the least-used entries to admit
new ones. Two things break as a result: the **top-queries view becomes a biased sample** —
low-frequency-but-expensive statements never survive long enough to accumulate, so you
can't see them — and a query that was evicted and later re-entered **resets its counters to
zero**, which makes any cross-run delta (like [query_slowdown](query_slowdown.md)) compare
against a phantom baseline. In short, the numbers you'd tune from are no longer trustworthy.

## How to verify it yourself

Read the eviction counter and how close the tracker is to full:

```sql
-- READ-ONLY. Evictions to date, and current fill vs the configured ceiling.
SELECT (SELECT dealloc FROM pg_stat_statements_info)                        AS deallocations,
       (SELECT count(*) FROM pg_stat_statements)                            AS entries,
       current_setting('pg_stat_statements.max')::int                       AS max_entries,
       round(100.0 * (SELECT count(*) FROM pg_stat_statements)
             / current_setting('pg_stat_statements.max')::numeric, 1)       AS pct_full;
```

A non-zero, *growing* `deallocations` across two reads a while apart confirms active churn
(a single non-zero value could be old history). `dealloc` and the reset timestamp live in
`pg_stat_statements_info`; `pg_stat_statements_reset()` zeroes them.

## How to fix it

1. **Raise `pg_stat_statements.max`** so the whole working set of distinct queries fits —
   e.g. from the default 5000 to 10000. It is set in `shared_preload_libraries`-loaded
   memory, so the change **requires a restart**. Size it to comfortably exceed the number
   of distinct normalized queries the app issues.
2. **Reduce the distinct-query population.** Churn is often driven by queries that *should*
   normalize to one entry but don't — literals that should be parameters, generated
   `IN (...)` lists of varying length, or unparameterized DDL. Parameterize them so many
   executions collapse into a single slot.
3. If neither is possible, **accept that low-frequency queries won't appear** and lean on
   pgbot's own deltas rather than the pg_stat_statements top list for those.

## When to ignore it

The eviction count is stale history from before a fix (and no longer growing), or the churn
is inherent to a workload you don't tune from the top-queries list:

```toml
[[ignore]]
finding = "pgss_entries_evicted"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- `dealloc` is a **cumulative counter since the last `pg_stat_statements_reset()`** — a
  large value can be entirely historical. pgbot can't tell, from one read, whether eviction
  is happening *now*; the two-read check above is how you confirm it's active.
- It cannot see *which* entries were evicted — that's the whole problem. The evicted
  statements are gone, so neither pgbot nor you can enumerate what's missing from the top
  list.
- It reads the count and the ceiling, not the memory cost; the right `max` depends on the
  distinct-query cardinality of the application, which pgbot infers rather than measures.

## Related

- [pg_stat_statements_missing](pg_stat_statements_missing.md) — the extension not being
  installed at all; this finding is its "installed but overflowing" counterpart.

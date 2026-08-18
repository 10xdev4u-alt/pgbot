---
id: stale_statistics
severity: warn
critical_when: ""
dimension: latency
object: relation
scope: workload
requires: []
thresholds: []
related: [query_slowdown, never_analyzed]
---

# stale_statistics

**Severity:** warn · **Dimension:** latency · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

On a table above the **10,000**-tuple floor (`deadRatioTableMinRows`) that *has* been
analyzed at least once, the number of rows changed since the last analyze
(`n_mod_since_analyze`) is **≥ 2×** the table's autoanalyze trigger. pgbot computes
the trigger the way Postgres does — `autovacuum_analyze_threshold +
autovacuum_analyze_scale_factor × n_live_tup` (defaults **50** and **0.1**) — and
honours **per-table reloption overrides** of both. The condition is
`n_mod_since_analyze ≥ 2 × (threshold + scale × n_live_tup)`. (It uses `n_live_tup`,
not `reltuples`, because `reltuples` is `-1` for never-analyzed tables on PG 14+.) A
table that has *never* been analyzed is reported separately as
[never_analyzed](never_analyzed.md), not here.

## Why it matters

The planner chooses join orders, index-vs-seqscan, and row estimates from the
statistics `ANALYZE` collects. When the table has changed far more than the analyze
threshold since those stats were gathered, the planner is reasoning about an
out-of-date shape of the data — an old row count, an old distribution, old
most-common-values. That's the classic setup for a **plan flip**: a query that used a
good index-driven plan yesterday suddenly picks a nested loop over millions of rows,
or a sequential scan it should never have chosen. Nothing errors; a query just gets
dramatically slower for no change in the SQL. The 2× multiplier means pgbot waits
until the drift is comfortably past the point where autoanalyze *should* have fired.

## How to verify it yourself

```sql
-- Tables whose modifications since analyze are past 2x their analyze trigger.
-- Uses the GLOBAL threshold/scale; substitute reloption overrides if a table sets
-- its own (pgbot does):
SELECT s.schemaname || '.' || s.relname                              AS table,
       s.n_mod_since_analyze,
       (current_setting('autovacuum_analyze_threshold')::numeric
        + current_setting('autovacuum_analyze_scale_factor')::numeric
          * s.n_live_tup)                                            AS trigger,
       s.last_analyze,
       s.last_autoanalyze
FROM pg_stat_user_tables s
WHERE s.n_live_tup >= 10000
  AND (s.last_analyze IS NOT NULL OR s.last_autoanalyze IS NOT NULL)
  AND s.n_mod_since_analyze >= 2 * (current_setting('autovacuum_analyze_threshold')::numeric
        + current_setting('autovacuum_analyze_scale_factor')::numeric * s.n_live_tup)
ORDER BY s.n_mod_since_analyze DESC;
```

## How to fix it

1. **Refresh the stats now:**
   ```sql
   ANALYZE public.orders;
   ```
   This is cheap (a sample, not a full scan) and takes only a `SHARE UPDATE
   EXCLUSIVE` lock, so it's safe on a live table.
2. **If it keeps going stale, make autoanalyze fire sooner** — globally or, better,
   on the hot table:
   ```sql
   ALTER TABLE public.orders SET (autovacuum_analyze_scale_factor = 0.02);
   ```
3. **For a skewed or fast-changing column** the planner keeps misjudging, raise its
   sample resolution so the refreshed stats are sharper:
   ```sql
   ALTER TABLE public.orders ALTER COLUMN status SET STATISTICS 1000;  -- then ANALYZE
   ```

## When to ignore it

The churn is a bulk load you're about to `ANALYZE` by hand, or the drift is on columns
the planner never uses for estimation so the stale stats don't actually mislead any
plan. Scope the rule to that table; a bare `finding = "stale_statistics"` mutes it for
every table and hides the next plan-flip waiting to happen:

```toml
[[ignore]]
finding = "stale_statistics"
object  = "public.orders"
reason  = "nightly bulk load; ANALYZE runs at the end of the job, plans unaffected"
expires = "2027-01-01"
```

## What pgbot cannot see

- `n_mod_since_analyze` is a **row-change count, not a distribution-change measure**.
  A million updates that don't move the column distributions won't hurt any plan;
  pgbot can't tell those from a million that shift the data's shape completely.
- It cannot connect stale stats to a *specific* slow query. When
  [query_slowdown](query_slowdown.md) also fires this run, pgbot notes the two travel
  together but explicitly cannot prove the slow query touches these tables — it can't
  map a normalized query back to its relations.
- The counter is zeroed by a statistics reset, which can make a genuinely-drifted
  table momentarily look current.

## Related

- [query_slowdown](query_slowdown.md) — the latency symptom stale stats most often
  cause via a plan flip; if both fire, check whether the slow query hits these tables.
- [never_analyzed](never_analyzed.md) — the stronger case: a table with *no* stats at
  all, reported on its own so it isn't mistaken for mere drift.

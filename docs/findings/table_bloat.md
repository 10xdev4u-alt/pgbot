---
id: table_bloat
severity: warn
critical_when: ""
dimension: storage
object: relation
scope: workload
requires: []
thresholds: [dead_ratio_warn]
related: [low_hot_update_ratio, vacuum_horizon_blocked, autovacuum_starved]
---

# table_bloat

**Severity:** warn · **Dimension:** storage · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

On a table with at least **10,000** total tuples (`deadRatioTableMinRows`), the
dead-tuple ratio was **≥ 20%** (`deadRatioWarn`, the `dead_ratio_warn` key in
`[thresholds]`). The ratio is `n_dead_tup / (n_live_tup + n_dead_tup)` from
`pg_stat_user_tables`. pgbot ranks the offenders by *dead bytes* —
`dead_ratio × table size` — so the "worst" table is the one wasting the most disk,
not merely the highest percentage. If no autovacuum has run recently on the worst
table (`last_autovacuum` is null), pgbot treats the bloat as *not being kept in
check*; if autovacuum has run recently, it discounts the score, since the bloat is
likely transient churn that will be reclaimed.

## Why it matters

Postgres never overwrites a row in place: an `UPDATE` or `DELETE` leaves the old
version behind as a **dead tuple**, and only `VACUUM` marks that space reusable.
Until it does, the dead versions inflate the table's on-disk size, so every
sequential scan reads more pages, every index-less lookup wanders through more
heap, and the buffer cache holds dead bytes where live rows should be. A ratio
that stays high means vacuum is losing the race against your write rate — the
table only grows, and reclaimed space is reused in place rather than returned to
the OS, so it rarely shrinks on its own.

## How to verify it yourself

```sql
-- The ratio pgbot computes, worst first. Matches deadRatioWarn (0.20) and the
-- 10k-row floor:
SELECT schemaname || '.' || relname                                      AS table,
       n_live_tup,
       n_dead_tup,
       round(100.0 * n_dead_tup / nullif(n_live_tup + n_dead_tup, 0), 1)  AS dead_pct,
       last_vacuum,
       last_autovacuum
FROM pg_stat_user_tables
WHERE n_live_tup + n_dead_tup >= 10000
  AND n_dead_tup::numeric / nullif(n_live_tup + n_dead_tup, 0) >= 0.20
ORDER BY dead_pct DESC;
```

For a physical (not estimated) measurement of wasted space, install `pgstattuple`
and read the real free-space fraction of a specific table — it scans the heap, so
run it off-peak:

```sql
CREATE EXTENSION IF NOT EXISTS pgstattuple;
SELECT * FROM pgstattuple('public.orders');   -- dead_tuple_percent, free_percent
```

## How to fix it

1. **Reclaim now.** `VACUUM public.orders;` marks the dead space reusable in place
   (no exclusive lock, safe on a live table). This does not shrink the file, but it
   stops the growth and lets new rows fill the freed space.
2. **If it keeps coming back, autovacuum isn't keeping pace.** Make it more
   aggressive on the hot table so it triggers sooner and runs faster:
   ```sql
   ALTER TABLE public.orders SET (
     autovacuum_vacuum_scale_factor = 0.02,   -- default 0.2: trigger at 2% dead
     autovacuum_vacuum_cost_limit   = 2000    -- let the worker do more per cycle
   );
   ```
   See [autovacuum_starved](autovacuum_starved.md) if dead tuples are already past
   the trigger with no worker in sight.
3. **To actually shrink the file** (only when a one-off spike left it oversized):
   `pg_repack` rewrites the table online with no long lock; `VACUUM FULL` reclaims
   the most space but takes an `ACCESS EXCLUSIVE` lock for the duration — a
   maintenance-window operation only.

**First rule out [vacuum_horizon_blocked](vacuum_horizon_blocked.md):** if a
long-running transaction, an abandoned prepared xact, or a stale replication slot
is pinning the xmin horizon, VACUUM *runs but reclaims nothing*, because the dead
rows are still visible to that old snapshot. Vacuuming harder won't help until the
horizon is released.

## When to ignore it

The table is churn-heavy by nature (a queue, a session store) and the dead space
is steady-state — reused as fast as it's created, never growing — and you've
confirmed the file size is stable. Scope the rule to that table; a bare
`finding = "table_bloat"` mutes the check for **every** table, hiding the next one
that genuinely runs away:

```toml
[[ignore]]
finding = "table_bloat"
object  = "public.orders"
reason  = "high-churn queue table; dead space is reused in place, file size stable"
expires = "2027-01-01"
```

## What pgbot cannot see

- `n_dead_tup` is an **estimate** maintained by the cumulative statistics system,
  not a page-level count. A stats reset zeroes it, and heavy churn between
  autovacuum runs can make it lag the true figure in either direction — use
  `pgstattuple` (above) for the measured number.
- It measures *heap* dead-tuple ratio only. It does not see **index** bloat, nor
  whether the "dead" space is reclaimable free space already available for reuse
  versus space that will need a rewrite to return to the OS.
- It cannot tell churn-driven steady-state bloat from a one-off spike — both show
  the same ratio at a single moment.

## Related

- [low_hot_update_ratio](low_hot_update_ratio.md) — non-HOT updates are a leading
  source of the dead tuples this finding reports; fixing the HOT ratio slows the
  bloat at its source.
- [vacuum_horizon_blocked](vacuum_horizon_blocked.md) — if the xmin horizon is
  pinned, VACUUM cannot reclaim these dead rows no matter how often it runs; check
  this first.
- [autovacuum_starved](autovacuum_starved.md) — the same tables often show dead
  tuples piling up past the autovacuum trigger with no worker keeping pace.

---
id: autovacuum_starved
severity: warn
critical_when: ""
dimension: storage
object: relation
scope: workload
requires: []
thresholds: []
related: [table_bloat, autovacuum_saturated]
---

# autovacuum_starved

**Severity:** warn · **Dimension:** storage · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

On a table above the **10,000**-tuple floor (`deadRatioTableMinRows`), the dead-tuple
count is **≥ 1.5×** the table's autovacuum trigger, yet **no autovacuum is recorded**
(`last_autovacuum` is null). pgbot computes the trigger exactly as Postgres does —
`autovacuum_vacuum_threshold + autovacuum_vacuum_scale_factor × n_live_tup` (defaults
**50** and **0.2**) — and it honours **per-table reloption overrides** of both
`autovacuum_vacuum_threshold` and `autovacuum_vacuum_scale_factor` when they're set.
Because the table has been vacuumed *manually* at least once (otherwise it would be
[table_never_vacuumed](table_never_vacuumed.md) instead), the null `last_autovacuum`
means the automatic side, specifically, is falling behind. The condition is
`n_dead_tup ≥ 1.5 × (threshold + scale × n_live_tup)`.

## Why it matters

This is autovacuum being **outrun**, not blocked. The dead tuples are well past the
point where a worker *should* have fired, but none has — the cluster doesn't have the
worker count or the cost budget to reach this table in time. Left alone it becomes
[table_bloat](table_bloat.md): the table grows, scans slow, and the backlog only gets
harder to clear. The 1.5× multiplier is deliberate — it waits until the table is
comfortably past its own trigger before complaining, so this fires on a real deficit,
not on a table that's merely due for its next routine pass.

## How to verify it yourself

```sql
-- Tables whose dead tuples are past 1.5x their autovacuum trigger with no
-- autovacuum recorded. Uses the GLOBAL threshold/scale; see the reloption note.
SELECT s.schemaname || '.' || s.relname                            AS table,
       s.n_dead_tup,
       (current_setting('autovacuum_vacuum_threshold')::numeric
        + current_setting('autovacuum_vacuum_scale_factor')::numeric
          * s.n_live_tup)                                          AS trigger,
       s.last_autovacuum
FROM pg_stat_user_tables s
WHERE s.n_live_tup >= 10000
  AND s.last_autovacuum IS NULL
  AND s.n_dead_tup >= 1.5 * (current_setting('autovacuum_vacuum_threshold')::numeric
        + current_setting('autovacuum_vacuum_scale_factor')::numeric * s.n_live_tup)
ORDER BY s.n_dead_tup DESC;
```

If a table sets its own `autovacuum_vacuum_scale_factor` / `..._threshold` in
reloptions, substitute those values — pgbot does. Check for overrides with:

```sql
SELECT reloptions FROM pg_class WHERE oid = 'public.orders'::regclass;
```

## How to fix it

Two levers — give autovacuum more capacity, and clear the current backlog:

1. **Widen the fleet or its budget** (cluster-wide, in `postgresql.conf`):
   ```
   autovacuum_max_workers      = 5      # default 3: more tables serviced at once
   autovacuum_vacuum_cost_limit = 3000  # default 200: each worker does more per cycle
   autovacuum_vacuum_cost_delay = 2ms   # default 2ms; lower it to throttle less
   ```
   `autovacuum_max_workers` needs a restart; the cost settings reload with
   `SELECT pg_reload_conf();`.
2. **Make the hot table trigger sooner** so it doesn't fall this far behind again:
   ```sql
   ALTER TABLE public.orders SET (autovacuum_vacuum_scale_factor = 0.02);
   ```
3. **Clear the current deficit now**, without waiting for a worker:
   ```sql
   VACUUM (VERBOSE) public.orders;
   ```

If **every** worker is already busy, adding tables to the queue won't help until the
fleet has room — see [autovacuum_saturated](autovacuum_saturated.md), which is the
cluster-wide view of the same shortage.

## When to ignore it

The backlog is a known one-off — a bulk delete or load you're about to `VACUUM` by
hand — and steady state is healthy. Scope the rule to that table; a bare
`finding = "autovacuum_starved"` mutes it for every table and blinds you to the next
one that falls behind for real:

```toml
[[ignore]]
finding = "autovacuum_starved"
object  = "public.orders"
reason  = "one-off backfill left dead tuples; manual VACUUM scheduled, steady state fine"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads a **snapshot**. If an autovacuum worker is running on this table *right
  now*, pgbot can't see it clearing the dead tuples — `last_autovacuum` only updates
  when the worker finishes.
- `n_dead_tup` is the statistics estimate, not a page count, and it's zeroed by a
  stats reset — a reset can make a genuinely-starved table momentarily look fine.
- It infers "outrun" from the counter shape; it does not directly measure worker
  contention or cost-limit throttling. [autovacuum_saturated](autovacuum_saturated.md)
  is the closest signal to the actual worker shortage.

## Related

- [table_bloat](table_bloat.md) — the outcome if the deficit isn't closed; these two
  frequently name the same tables.
- [autovacuum_saturated](autovacuum_saturated.md) — the cluster-wide cause: if all
  workers are pinned, tables queue behind them and starve.

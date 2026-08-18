---
id: autovacuum_saturated
severity: warn
critical_when: "point-in-time; confirm it holds across runs"
dimension: throughput
object: cluster
scope: workload
requires: []
thresholds: []
related: [autovacuum_starved, autovacuum_long_running]
---

# autovacuum_saturated

**Severity:** warn (critical when point-in-time; confirm it holds across runs) · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

At the moment pgbot sampled `pg_stat_activity`, the number of running autovacuum
workers was **at or above** `autovacuum_max_workers` — every worker slot was in use.
The condition is `AutovacuumWorkers ≥ autovacuum_max_workers` (default cap **3**).
This is a **single-moment count**, so pgbot reports it with modest confidence (0.5)
and attaches a caveat: one glance can catch a normal, transient peak. It matters only
if it *holds* across several runs.

## Why it matters

Autovacuum has a fixed number of worker slots. When all of them are busy, any table
that crosses its trigger has to **wait in line** — no worker can start on it until one
frees up. If the saturation is momentary, that's fine; the queue drains. If it's
sustained, vacuum falls progressively behind across the whole cluster: dead tuples and
bloat grow on the waiting tables ([autovacuum_starved](autovacuum_starved.md)), and
transaction-id age creeps up everywhere at once. A permanently-full worker pool is a
throughput ceiling — the database simply cannot vacuum faster than three (or however
many) tables at a time, no matter how much work is queued.

## How to verify it yourself

```sql
-- Live worker count vs the cap — pgbot's exact comparison. Re-run it a few times
-- (or watch it) to tell a transient peak from sustained saturation:
SELECT count(*)                                        AS active_workers,
       current_setting('autovacuum_max_workers')::int  AS max_workers,
       count(*) >= current_setting('autovacuum_max_workers')::int AS saturated
FROM pg_stat_activity
WHERE backend_type = 'autovacuum worker';
```

To see *what* the workers are pinned on — a few huge tables, or genuine
cluster-wide demand — list them with what they're vacuuming:

```sql
SELECT pid, now() - xact_start AS running_for, query
FROM pg_stat_activity
WHERE backend_type = 'autovacuum worker'
ORDER BY xact_start;
```

## How to fix it

Only act after confirming the saturation is **sustained**, not a single-sample blip.

1. **Add worker slots** (requires a restart):
   ```
   autovacuum_max_workers = 5      # default 3
   ```
   Note the throttle budget is *shared* across workers: `autovacuum_vacuum_cost_limit`
   is divided among all active workers, so doubling the workers without raising the
   cost limit just makes each one slower. Raise both together.
2. **Make each worker finish faster** so slots free up sooner — reduce per-worker
   throttling with a higher `autovacuum_vacuum_cost_limit` and/or lower
   `autovacuum_vacuum_cost_delay`. These reload with `SELECT pg_reload_conf();`.
3. **Give very large tables their own budget** so one giant vacuum doesn't hold a slot
   for hours — see [autovacuum_long_running](autovacuum_long_running.md).

## When to ignore it

You've looked and the saturation is a normal, brief peak (a nightly batch window, a
post-migration catch-up) that drains on its own, or you've deliberately sized the
worker pool small and accept the throughput ceiling. This is a cluster-scoped finding
— there's no object to scope to, so the suppression is wholesale; use a near-term
`expires` so it can't silently hide a *later* pool that stays pinned:

```toml
[[ignore]]
finding = "autovacuum_saturated"
reason  = "brief nightly catch-up window saturates workers, drains within the hour"
expires = "2027-01-01"
```

## What pgbot cannot see

- It's a **point-in-time** count. pgbot cannot tell a fleeting peak from hours of
  sustained saturation from one sample — that's why the caveat says to confirm across
  runs, and why this finding never asserts sustained pressure on its own.
- It sees the worker *count*, not the **queue behind it**: how many tables are past
  their trigger and waiting for a slot. A full pool with an empty queue is harmless; a
  full pool with a long queue is the real problem, and that depth isn't in
  `pg_stat_activity`.
- It cannot see whether the shared cost-limit budget, rather than the slot count, is
  the actual bottleneck.

## Related

- [autovacuum_starved](autovacuum_starved.md) — the per-table symptom of sustained
  saturation: tables past their trigger with no worker reaching them.
- [autovacuum_long_running](autovacuum_long_running.md) — one worker pinned for a long
  time on a huge table is a common reason the pool stays full.

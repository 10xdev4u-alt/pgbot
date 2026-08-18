---
id: autovacuum_long_running
severity: info
critical_when: ""
dimension: throughput
object: cluster
scope: workload
requires: []
thresholds: []
related: [autovacuum_saturated]
---

# autovacuum_long_running

**Severity:** info · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

An autovacuum worker's transaction has been open for **≥ 3600 seconds** — one hour
(`autovacuumLongRunSec`). pgbot takes the longest-running autovacuum worker's
transaction age (`AutovacuumMaxAgeSec`, from `pg_stat_activity`) and reports it when
it crosses the hour mark. This is **info**, not a warning: a long autovacuum is
usually a large table catching up, which is exactly what autovacuum is supposed to do.

## Why it matters

Most of the time this is healthy — a big table's first thorough vacuum, or an
aggressive anti-wraparound freeze, simply takes a long time, and interrupting it would
only make the next attempt start over. But a worker running for hours has two costs
worth knowing about: it **ties up one of the limited worker slots** for the whole
duration (contributing to [autovacuum_saturated](autovacuum_saturated.md)), and on the
rare occasion it's genuinely *stuck* — blocked on a lock, or crawling because of tight
cost throttling — it will never finish on its own. The finding exists so a long vacuum
is *visible*, not so you cancel it: look before you leap.

## How to verify it yourself

```sql
-- Longest-running autovacuum workers and how long each has been going. pgbot fires
-- when the longest crosses 3600s (1h):
SELECT pid,
       now() - xact_start AS running_for,
       query               -- e.g. 'autovacuum: VACUUM public.orders'
FROM pg_stat_activity
WHERE backend_type = 'autovacuum worker'
ORDER BY xact_start
LIMIT 10;
```

Before deciding it's stuck, read its **progress** — how far along it is and which
phase — from `pg_stat_progress_vacuum` (PG 9.6+):

```sql
SELECT p.pid, p.relid::regclass AS table, p.phase,
       p.heap_blks_scanned, p.heap_blks_total,
       round(100.0 * p.heap_blks_scanned / nullif(p.heap_blks_total, 0), 1) AS pct
FROM pg_stat_progress_vacuum p
JOIN pg_stat_activity v ON v.pid = p.pid
WHERE v.backend_type = 'autovacuum worker';
```

## How to fix it

Usually: **nothing** — let it finish. If it's making progress (the `pct` above is
climbing between checks), leave it; cancelling only wastes the work done so far.

- **If it's genuinely stuck** (progress isn't advancing, or it's blocked — check
  `pg_stat_activity.wait_event_type`), you can cancel that one worker with
  `SELECT pg_cancel_backend(<pid>);`. Autovacuum will pick the table up again on its
  next cycle.
- **If long vacuums on one huge table are chronic**, give that table a looser throttle
  so its worker finishes faster instead of holding a slot for hours:
  ```sql
  ALTER TABLE public.big_events SET (
    autovacuum_vacuum_cost_limit = 3000,
    autovacuum_vacuum_cost_delay = 0     -- no throttle for this table's vacuums
  );
  ```
- Consider whether the table should be **partitioned** so each vacuum is bounded to
  one partition rather than the whole table.

## When to ignore it

Routinely. A long autovacuum on a known-large table is expected behaviour, and this is
an `info` finding precisely so it doesn't gate anything. This is cluster-scoped —
there's no object to attach, so suppression is wholesale; keep a near-term `expires`
so a *new* long vacuum on a different table still surfaces later:

```toml
[[ignore]]
finding = "autovacuum_long_running"
reason  = "big_events vacuum routinely runs several hours; expected, monitored"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the worker's **transaction age**, not its **progress**. It cannot tell a
  vacuum that's 90% done from one that's wedged — only `pg_stat_progress_vacuum` (the
  verification query) shows the phase and how far along it is.
- It cannot see *why* the vacuum is slow: aggressive anti-wraparound work, cost-limit
  throttling, waiting on a buffer pin or a lock, or slow I/O all look the same from a
  single age number.
- It reports the single longest worker, not the full set — several
  moderately-long vacuums can add up to the same slot pressure without any one of them
  crossing the hour.

## Related

- [autovacuum_saturated](autovacuum_saturated.md) — a worker pinned for hours is a
  frequent reason the worker pool stays full; these two often appear together.

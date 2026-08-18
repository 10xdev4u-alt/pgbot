---
id: checkpoints_forced
severity: warn
critical_when: ""
dimension: throughput
object: setting
scope: workload
requires: []
thresholds: []
related: [wait_lwlock_pressure]
---

# checkpoints_forced

**Severity:** warn · **Dimension:** throughput · **Object identity:** `setting:max_wal_size` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

Of the checkpoints since the last stats reset, too many were **forced by WAL volume**
rather than fired on the timed interval. Two conditions must both hold: at least
**10** checkpoints total (`tuneForcedCheckpointMin`, enough to judge a ratio), and the
forced fraction `checkpoints_req / (checkpoints_req + checkpoints_timed)` at or above
**0.30** (`tuneForcedCheckpointFrac`). The counts come from `pg_stat_checkpointer`
(`num_requested` / `num_timed`) on PostgreSQL 17+, or `pg_stat_bgwriter`
(`checkpoints_req` / `checkpoints_timed`) before that.

## Why it matters

A *timed* checkpoint fires every `checkpoint_timeout` and spreads its writes out; a
*requested* (forced) checkpoint fires early because WAL hit `max_wal_size`. A high
forced fraction means WAL is being written faster than the checkpoint spacing expects,
so checkpoints bunch up into **I/O spikes**, and each one resets the full-page-write
cycle — the first write to every page after a checkpoint logs a full 8 KB image,
inflating WAL further and feeding the loop. The symptom is periodic latency stalls
under write load.

## How to verify it yourself

```sql
-- PostgreSQL 17+:
SELECT num_timed,
       num_requested,
       round(100.0 * num_requested
             / nullif(num_timed + num_requested, 0), 1) AS forced_pct
FROM pg_stat_checkpointer;

-- PostgreSQL 16 and earlier:
SELECT checkpoints_timed,
       checkpoints_req,
       round(100.0 * checkpoints_req
             / nullif(checkpoints_timed + checkpoints_req, 0), 1) AS forced_pct
FROM pg_stat_bgwriter;

SHOW max_wal_size;
SHOW checkpoint_timeout;
```

## How to fix it

Give WAL more room so checkpoints are paced by time, not volume. `max_wal_size` is
reloadable (SIGHUP) — no restart:

```sql
ALTER SYSTEM SET max_wal_size = '4GB';   -- raise from the 1GB default as needed
SELECT pg_reload_conf();
```

Aim for a `max_wal_size` large enough that most checkpoints are timed. The trade-off
is disk: a larger WAL ceiling uses more space and can lengthen crash recovery, so size
it to your storage and recovery-time goals. `checkpoint_completion_target` already
defaults to `0.9` (writes spread across 90% of the interval), so raising
`max_wal_size` is usually the lever, not the completion target.

## When to ignore it

A temporary bulk-load or migration window legitimately generates abnormal WAL, so
forced checkpoints during it are expected and transient — or the cumulative counters
are still dominated by such a past burst. Suppression is the clean per-object case,
scoped to this one setting:

```toml
[[ignore]]
finding = "checkpoints_forced"
object  = "setting:max_wal_size"
reason  = "counters skewed by the one-time data migration on 2026-08-10; steady state is fine"
expires = "2027-01-01"
```

## What pgbot cannot see

- The counts are **cumulative** since the last `pg_stat_reset_shared` (or server
  start), so a past bulk load can skew the forced fraction long after it ended.
- It sees the ratio, not the actual WAL write rate, so it can't distinguish a
  legitimate one-off migration from a chronically undersized `max_wal_size` — the
  cross-check against your workload pattern is yours to make.

## Related

- [wait_lwlock_pressure](wait_lwlock_pressure.md) — checkpoint I/O bursts drive
  lightweight-lock contention (e.g. `WALWrite`, buffer-mapping); the two often
  surface together under write pressure.

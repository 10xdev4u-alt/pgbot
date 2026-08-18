---
id: wait_lwlock_pressure
severity: warn
critical_when: ""
dimension: throughput
object: cluster
scope: workload
requires: [ASH sampling (ash-hz>0)]
thresholds: []
related: [checkpoints_forced]
---

# wait_lwlock_pressure

**Severity:** warn · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** ASH sampling (ash-hz>0)

## What pgbot observed

Sampling `pg_stat_activity` many times over the collection window (active-session history,
at `--ash-hz`, default **10 Hz**), pgbot found a single **lightweight lock** event
concentrating the window. The literal condition is `dominantLWLock(w).share > waitLWLockShare`
where `waitLWLockShare = 0.30`: the largest individual `LWLock:<event>` (e.g.
`BufferMapping`, `WALWrite`, `LockManager`, `WALInsert`) accounted for **more than 30%** of
all active-session samples.

Gated on `model.WaitMinSamples = 20` like every wait finding: fewer than 20 total samples
is `Thin()` and nothing fires. The share is a fraction of *sampled moments*, not a measured
lock-wait time.

## Why it matters

LWLocks are Postgres's *internal* short-term latches protecting shared-memory structures —
the buffer mapping table, WAL buffers, the lock manager's partitions. Unlike a `Lock:*`
wait (a user waiting on another user's row lock), heavy `LWLock` waiting means backends are
contending with *each other* inside the engine. It is a scalability wall: adding
connections makes it worse, because more backends fight over the same latch. The specific
event names the subsystem — `BufferMapping` points at an undersized buffer cache,
`WALWrite`/`WALInsert` at write/commit concurrency and checkpoint pressure,
`LockManager` at a very high rate of lock acquisitions.

## How to verify it yourself

The profile is a *sample*, so reproduce it by polling `pg_stat_activity` repeatedly and
tallying which `LWLock` event dominates active backends:

```sql
-- READ-ONLY. Run repeatedly (e.g. \watch 0.1) over a minute and aggregate:
-- the fraction of active samples on one LWLock event is what pgbot compares to 30%.
-- One snapshot is not a profile — sample many times.
SELECT wait_event AS lwlock_event,
       count(*)   AS sampled
FROM pg_stat_activity
WHERE state = 'active'
  AND wait_event_type = 'LWLock'
  AND pid <> pg_backend_pid()
GROUP BY wait_event
ORDER BY sampled DESC;
```

Then corroborate the likely cause. For WAL/checkpoint-flavored LWLocks, check how often
checkpoints are being forced by WAL volume rather than time:

```sql
SELECT num_timed, num_requested, write_time, sync_time, buffers_written
FROM pg_stat_checkpointer;   -- PG17+; older: pg_stat_bgwriter (checkpoints_timed/_req)
```

## How to fix it

The dominant event is the diagnosis — fix *that* subsystem, not LWLocks in general:

- **`BufferMapping` / `BufferContent`** → the buffer cache is churning. Raise
  `shared_buffers`/RAM so hot pages stop being evicted and re-mapped; reduce the scan
  volume that thrashes it (see [wait_io_bound](wait_io_bound.md)).
- **`WALWrite` / `WALInsert` / `WALBufferLock`** → commit/write concurrency. Tune
  checkpoints so they don't storm: raise `max_wal_size` and `checkpoint_timeout` so
  checkpoints fire on time, not on volume ([checkpoints_forced](checkpoints_forced.md)),
  and consider larger `wal_buffers` and commit batching.
- **`LockManager`** → a very high rate of lock acquisitions, often from queries touching
  huge numbers of partitions; prune partitions in the plan or reduce per-statement lock
  churn.
- Across the board, **fewer, busier connections** (a pooler) usually beats many idle ones,
  which reduces contention on every partitioned LWLock.

## When to ignore it

The pressure is inherent to a known, accepted workload phase — e.g. a bulk load or index
build that intentionally saturates WAL for a bounded window:

```toml
[[ignore]]
finding = "wait_lwlock_pressure"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- A wait profile is a **distribution over sampled moments**, never an exact latch-wait
  time. The finding carries its sample count; a 30%+ share off a thin sample is
  suggestive, not measured — confidence scales with the total (20 thin, 200 solid).
- LWLocks are held for microseconds; between samples pgbot sees nothing, so a genuinely
  high but very short-lived contention rate can be under- or over-represented by the luck
  of when samples landed.
- It reports the *event*, not the shared-memory address or the specific buffer/partition
  in contention — enough to name the subsystem, not to pinpoint the object.

## Related

- [checkpoints_forced](checkpoints_forced.md) — WAL-driven checkpoints are a frequent
  source of `WALWrite`/`WALInsert` LWLock pressure; tuning them often clears this finding.

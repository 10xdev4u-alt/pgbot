---
id: wait_lock_contention
severity: warn
critical_when: ""
dimension: latency
object: cluster
scope: workload
requires: [ASH sampling (ash-hz>0)]
thresholds: []
related: [blocking_chains]
---

# wait_lock_contention

**Severity:** warn · **Dimension:** latency · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** ASH sampling (ash-hz>0)

## What pgbot observed

While sampling `pg_stat_activity` many times over the collection window (active-session
history, at `--ash-hz`, default **10 Hz**), pgbot found a single normalized query
whose samples were dominated by heavyweight-lock waits. The literal condition is
`q.Count >= waitLockQueryMinSamples && q.LockShare > waitLockContentionShare`: the query
was caught active in **at least 5** samples (`waitLockQueryMinSamples`) and **more than
30%** (`waitLockContentionShare = 0.30`) of *its own* samples had
`wait_event_type = 'Lock'` — i.e. it was blocked on a row, transaction, or relation lock
held by someone else, not doing work.

The whole wait family is gated on `model.WaitMinSamples = 20`: a profile with fewer than
20 total active-session samples is treated as noise (`WaitProfile.Thin()`) and **no** wait
finding fires. The share is a fraction of *sampled moments*, not a measured lock-wait time.

## Why it matters

Time a query spends parked on a `Lock:*` wait is latency your users feel while the query
does nothing. Sustained lock contention usually means a serialization point in the
workload — a long-held transaction, a hot row every session updates, or a coarse
table-level lock — and it tends to compound: the blocked sessions pile up, hold their own
locks, and lengthen the queue behind them. Left alone it degrades into the visible
blocking chains and connection saturation that take an application down.

## How to verify it yourself

The wait profile is a *sample*, so reproduce it the way pgbot does: poll
`pg_stat_activity` repeatedly (a few times a second for a minute) and tally where active
backends are waiting. On PG14+ you can attribute it per `query_id`:

```sql
-- READ-ONLY. Run this many times in a loop (e.g. \watch 0.1) and aggregate:
-- the fraction of an active query's samples on wait_event_type='Lock' is what
-- pgbot's LockShare measures. A single snapshot is not enough — sample repeatedly.
SELECT query_id,
       wait_event_type,
       wait_event,
       count(*) AS sampled
FROM pg_stat_activity
WHERE state = 'active'
  AND backend_type = 'client backend'
  AND pid <> pg_backend_pid()
GROUP BY query_id, wait_event_type, wait_event
ORDER BY sampled DESC;
```

To see *who* is blocking *whom* right now, join through `pg_locks` /
`pg_blocking_pids()`:

```sql
SELECT blocked.pid          AS blocked_pid,
       blocked.query        AS blocked_query,
       unnest(pg_blocking_pids(blocked.pid)) AS blocking_pid
FROM pg_stat_activity blocked
WHERE cardinality(pg_blocking_pids(blocked.pid)) > 0;
```

## How to fix it

The wait profile points straight at the bottleneck: it is *lock* contention, so reduce it
at the source rather than throwing hardware at it.

1. **Shorten the holding transaction.** The most common cause is one session sitting in a
   long or idle-in-transaction state holding a lock. Find it (`idle_in_transaction`,
   `long_running_transaction`), tighten the app's transaction scope, and consider
   `idle_in_transaction_session_timeout`.
2. **Break up hot-row updates.** If every session updates the same counter/row, that row
   is a serialization point — batch the updates, move the counter out of the hot path, or
   shard it across N rows.
3. **Lower lock granularity.** A statement taking an `ACCESS EXCLUSIVE`/table lock (DDL, an
   un-`CONCURRENTLY` index build, `LOCK TABLE`) blocks everyone; schedule it off-peak and
   use the `CONCURRENTLY` / `SET lock_timeout` variants.
4. **Reorder to avoid deadlock-prone patterns** — have all sessions acquire locks in a
   consistent order.

## When to ignore it

The contention is on a known, accepted serialization point (e.g. a nightly batch job that
intentionally locks a table for a short window), or the sample was captured during a
one-off migration:

```toml
[[ignore]]
finding = "wait_lock_contention"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- A wait profile is a **distribution over sampled moments**, never an exact lock-wait
  time. The finding always carries its sample count (`q.Count` and the total); a share
  computed off few samples is suggestive, not proof, which is why confidence scales with
  the sample total (20 is thin, 200 is solid).
- It names the blocked *query*, but not the transaction holding the lock, nor the line of
  application code that opened it. The blocking join above closes that gap live.
- Between samples it sees nothing: a lock held and released faster than the sampling
  interval is invisible, and a burst that ends before collection starts leaves no trace.

## Related

- [blocking_chains](blocking_chains.md) — the point-in-time view of which session blocks
  which; wait_lock_contention is the sampled-over-time symptom of the same problem.

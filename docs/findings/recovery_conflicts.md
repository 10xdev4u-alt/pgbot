---
id: recovery_conflicts
severity: warn
critical_when: ""
dimension: risk
object: cluster
scope: infra
requires: [standby]
thresholds: []
related: [replica_lag_time]
---

# recovery_conflicts

**Severity:** warn · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** standby

## What pgbot observed

This node is a standby and the sum of its recovery-conflict counters in
`pg_stat_database_conflicts` is **greater than zero** (`Total() > 0`). pgbot adds the
five conflict classes together —
`confl_snapshot + confl_lock + confl_bufferpin + confl_deadlock + confl_tablespace` —
and reports the total, with the per-class breakdown in the evidence. There is no
tunable threshold: any recovered conflict at all trips the finding, because these are
cumulative counts of queries that were **cancelled**, not slowed.

## Why it matters

Recovery is single-threaded and always wins. When WAL replay needs to apply a change
that would invalidate a snapshot, lock, or buffer a read query on this standby is
holding, Postgres **cancels the query** ("terminating connection due to conflict with
recovery") so replay can proceed. Frequent conflicts make this standby an unreliable
place to run reads: queries fail intermittently for reasons the application can't
predict, and the failures cluster exactly when the primary is busiest (the most WAL to
replay). It is the direct tension behind [replica_lag_time](replica_lag_time.md) — the
usual "fix" for conflicts (a higher `max_standby_streaming_delay`) buys query survival
by letting replay fall behind.

## How to verify it yourself

Run on the **standby** itself (the counters live on the node where recovery runs):

```sql
SELECT datname,
       confl_snapshot,
       confl_lock,
       confl_bufferpin,
       confl_deadlock,
       confl_tablespace,
       confl_snapshot + confl_lock + confl_bufferpin
         + confl_deadlock + confl_tablespace AS total   -- pgbot's number
FROM   pg_stat_database_conflicts
WHERE  confl_snapshot + confl_lock + confl_bufferpin
         + confl_deadlock + confl_tablespace > 0
ORDER  BY total DESC;
```

The biggest class points at the cause: `confl_snapshot` is by far the most common
(old snapshots on the standby vs. vacuum cleanup replayed from the primary);
`confl_lock` is replay waiting on a lock a query holds; `confl_bufferpin` is a pinned
buffer; `confl_tablespace` shows up with `DROP TABLESPACE` replay.

## How to fix it

There is no free lunch — every option trades query survival against replay lag or
primary bloat. Pick per workload:

1. **Give read queries more slack on the standby.** Raise `max_standby_streaming_delay`
   (and `max_standby_archive_delay`) so replay waits longer before cancelling a query.
   The cost is exactly [replica_lag_time](replica_lag_time.md): the standby falls
   behind while it waits.
2. **Stop vacuum from yanking the rug — `hot_standby_feedback = on`.** This tells the
   primary to hold back cleanup of rows the standby's queries still need, which
   eliminates most `confl_snapshot` cancellations. The cost moves to the primary:
   held-back cleanup means bloat and a pinned xmin horizon — see
   `vacuum_horizon_blocked`. Pair it with an `idle_in_transaction_session_timeout` on
   the standby so a forgotten transaction there can't pin the primary indefinitely.
3. **Move the offending queries.** Long analytical reads are the usual victims; running
   them against a dedicated, lag-tolerant replica (or during quiet replay periods)
   sidesteps the conflict entirely.

## When to ignore it

A standby you use only for HA failover and never for reads will still accumulate a few
conflicts harmlessly — no query is being cancelled that you care about. This is
cluster-scoped, so the ignore covers all conflict classes on the node; use it when
reads on this standby are simply not part of your workload:

```toml
[[ignore]]
finding = "recovery_conflicts"
reason  = "HA-only standby; no application reads run here, so cancellations are inconsequential"
expires = "2027-01-01"
```

## What pgbot cannot see

- The counters are **cumulative since the last stats reset**; a large total may be
  months of history, not a current storm. pgbot reports the count, not the rate — a
  recent `pg_stat_reset()` can make a chronic problem look clean.
- It cannot see *which* queries were cancelled or how many succeeded; only that some
  were killed. The per-class breakdown hints at the cause but not the culprit query.
- It reads the standby's own view; it cannot correlate a conflict to the specific
  primary-side vacuum or DDL that triggered it.

## Related

- [replica_lag_time](replica_lag_time.md) — the direct trade-off: raising
  `max_standby_streaming_delay` to stop conflicts is precisely what lets replay lag
  grow.

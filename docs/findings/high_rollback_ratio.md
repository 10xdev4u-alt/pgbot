---
id: high_rollback_ratio
severity: warn
critical_when: ""
dimension: throughput
object: cluster
scope: workload
requires: []
thresholds: []
related: []
---

# high_rollback_ratio

**Severity:** warn · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

Over the sample window, a high share of transactions **rolled back** rather than
committed. pgbot computes `rollbackRatio = xact_rollback / (xact_commit +
xact_rollback)` from the delta in `pg_stat_database` and fires when it is at least
**`rollbackRatioWarn` = 0.10 (10%)**. To keep the ratio meaningful it is gated on
volume: it fires only when the window saw enough transactions —
`TPS × sampleSeconds >= rollbackMinTxns` where **`rollbackMinTxns` = 20** — because
2 rollbacks out of 4 reads as 50% and means nothing. Severity is `warn`, confidence
`0.5`, and the impact score is capped low (`min(35, ratio×100)`): this is a signal
worth confirming, not an outage.

## Why it matters

Every rollback is work the database did and then threw away — a transaction was
opened, statements ran, WAL may have been written, and none of it counted. A
sustained double-digit rollback rate usually points at one of a few things:
application error handling that aborts transactions on a routine path, failed
constraint checks (unique violations, check/foreign-key failures) that the app
retries, serialization or deadlock failures under contention, or statements hitting
`statement_timeout`. Occasionally it is benign — a health check or advisory-lock
probe that deliberately rolls back. Either way it is throughput spent on transactions
that produced nothing, and a *rising* ratio is often the first cheap signal of a new
bug or a contention problem.

## How to verify it yourself

```sql
SELECT datname,
       xact_commit,
       xact_rollback,
       round(100.0 * xact_rollback
             / nullif(xact_commit + xact_rollback, 0), 2) AS rollback_pct
FROM pg_stat_database
WHERE xact_commit + xact_rollback > 0
ORDER BY rollback_pct DESC;
```

Note this shows the ratio **cumulative since the last stats reset**, whereas pgbot
computes it over its sample *window* from the counter deltas — a chronically-bad
week can mask a spike that just started (and vice versa). To watch the live rate,
snapshot the counters, wait, and diff:

```sql
-- run twice, a minute apart, and subtract:
SELECT sum(xact_commit) AS commits, sum(xact_rollback) AS rollbacks
FROM pg_stat_database;
```

## How to fix it

1. **Find where the rollbacks come from.** Check the server log for the errors that
   precede an aborted transaction (set `log_min_error_statement`/`log_statement`
   appropriately if it isn't already logging them) — unique violations, deadlocks,
   serialization failures, and timeouts each leave a distinct message.
2. **Fix the dominant cause.** If it is constraint violations on a hot path, fix the
   application logic (or use `INSERT ... ON CONFLICT` instead of insert-then-catch).
   If it is deadlocks or serialization failures, reduce contention and add bounded
   retries. If it is `statement_timeout`, the queries are too slow — tune them.
3. **Distinguish intentional rollbacks.** If a framework or health check opens a
   transaction only to `ROLLBACK`, confirm that's deliberate; it inflates the ratio
   without indicating a problem.

## When to ignore it

The rollback rate is **expected and understood** — for example a Serializable
workload that retries on serialization failures by design, or a probe that rolls
back on purpose. Because the ratio is a single cluster-wide counter derivative with
no per-object identity (no relation, query, or PID to name), the suppression is
cluster-scoped:

```toml
[[ignore]]
finding = "high_rollback_ratio"
reason  = "Serializable workload retries on 40001; elevated rollback rate is by design, OPS-7040"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees the **ratio**, not the **cause**. `pg_stat_database` counts commits and
  rollbacks but not *why* a transaction aborted — that detail lives only in the
  server log.
- It measures over its **sample window** via counter deltas. A short window right
  after a stats reset, or an unusually quiet one, can make the number jumpy — which
  is exactly why the `rollbackMinTxns` = 20 volume gate exists.
- The counter is a **cluster/database-wide** aggregate: it can't attribute the
  rollbacks to a particular query or session. That is why the finding is
  `cluster`-scoped and its `[[ignore]]` has no `object` line — there is no durable
  object to suppress, so it is all-or-nothing, by design.

## Related

- This finding travels alone — a high rollback ratio is a symptom whose cause is
  application- or contention-specific rather than tied to another pgbot check.

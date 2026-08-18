---
id: long_running_transaction
severity: warn
critical_when: ""
dimension: risk
object: cluster
scope: workload
requires: []
thresholds: []
related: [idle_in_transaction, vacuum_horizon_blocked]
---

# long_running_transaction

**Severity:** warn · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The oldest open transaction had been running for at least **`longXactWarnSec` = 300
seconds (5 minutes)** when pgbot sampled `pg_stat_activity`
(`c.Activity.LongestXactSec >= longXactWarnSec`). Age is measured from `xact_start`,
so it counts the whole transaction — every statement since `BEGIN` — not just the
current one. Severity is `warn`; the impact score rises with age
(`min(85, 55 + longestXactSec÷60)`), and the basis is the longest `xact_start` age
in `pg_stat_activity`.

## Why it matters

A long-running transaction **holds back the `xmin` horizon**. From the moment it
began, its snapshot must stay valid, so `VACUUM` cannot remove any row version that
became dead after it started — anywhere in the cluster, not just in the tables this
transaction touches. Dead tuples pile up, tables and indexes bloat, index-only
scans lose their edge, and if the transaction lives long enough, transaction-id age
marches toward wraparound. A transaction that is also taking locks (a long-running
DDL, a batch `UPDATE`) can additionally block other sessions and seed a
[blocking chain](blocking_chains.md). Unlike an idle transaction, this one is doing
real work — but the cost it imposes on vacuum is identical.

## How to verify it yourself

```sql
SELECT pid, usename, application_name, state,
       now() - xact_start   AS xact_age,
       now() - query_start  AS current_stmt_age,
       left(query, 80)      AS query
FROM pg_stat_activity
WHERE xact_start IS NOT NULL
  AND now() - xact_start > interval '5 minutes'
ORDER BY xact_start
LIMIT 20;
```

The largest `xact_age` here is pgbot's `LongestXactSec`. Note `state` too: if it is
`active`, the transaction is genuinely working; if it is `idle in transaction`, it
is stuck between statements — see [idle_in_transaction](idle_in_transaction.md).

## How to fix it

1. **Identify and end it.** If it is a runaway query, `SELECT pg_cancel_backend(<pid>)`
   cancels the current statement; if it is holding the transaction open with nothing
   running, `SELECT pg_terminate_backend(<pid>)` ends the whole backend.
2. **Shorten the scope in the application.** Long-lived read transactions (report
   builders, exports, ORMs that wrap a whole request in one transaction) are the
   usual offenders. Break the work into smaller transactions, or move heavy read-only
   analytics to a **replica** so they can't pin the primary's vacuum horizon.
3. **Put a ceiling on it.** Set `statement_timeout` to bound individual statements
   and `idle_in_transaction_session_timeout` to bound the gaps between them, so no
   single transaction can hold the horizon open indefinitely. `transaction_timeout`
   (PostgreSQL 17+) bounds the whole transaction directly.

## When to ignore it

A **known, necessary** long transaction — a large scheduled maintenance job, a
logical-dump export, or a migration — whose xmin-pinning cost you have accounted for
and timed for a quiet window. Because the transaction is identified only by an
ephemeral **PID**, there is no stable name to scope to, so the suppression is
cluster-wide:

```toml
[[ignore]]
finding = "long_running_transaction"
reason  = "nightly pg_dump runs ~20 min; vacuum-horizon impact understood, tracked in OPS-3312"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees a **snapshot** of `pg_stat_activity`, not the transaction's history — it
  knows the age, not what the transaction has done or how much longer it will run.
- It cannot tell a **legitimate** long job apart from a leaked one; both look like a
  large `xact_start` age. Only you know whether the workload is expected.
- The transaction is keyed by **PID**, an ephemeral identifier gone by the next run.
  That is why the finding is `cluster`-scoped and its `[[ignore]]` carries no
  `object` line — there is nothing durable to suppress, so it is all-or-nothing by
  design, keeping a genuinely new offender visible tomorrow.

## Related

- [idle_in_transaction](idle_in_transaction.md) — the same horizon hazard from a
  transaction that is *idle* rather than actively working.
- [vacuum_horizon_blocked](vacuum_horizon_blocked.md) — when this transaction is the
  oldest xmin holder, it is what pins the vacuum horizon that finding reports.

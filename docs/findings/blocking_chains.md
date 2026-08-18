---
id: blocking_chains
severity: critical
critical_when: ""
dimension: risk
object: cluster
scope: workload
requires: []
thresholds: []
related: [long_running_transaction, idle_in_transaction]
---

# blocking_chains

**Severity:** critical · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

At the instant pgbot sampled the lock graph, **at least one session was waiting on
a lock held by another session** (`c.Locks.BlockedCount > 0`). There is no
threshold to cross and nothing to override in `[thresholds]` — a single blocked
session trips it, and the severity is always `critical` at confidence `1.0`,
because it is happening *right now*. The impact score climbs with both how many
sessions are blocked and how long the longest one has waited
(`min(100, 80 + blocked×3 + maxWait÷10)`), and the evidence names each blocked PID
and its wait in seconds.

## Why it matters

Lock waits cascade. A session that is itself blocked still holds every lock it has
already taken, so a second session can queue behind it, a third behind that, and a
short stall becomes a growing chain. If the head of the chain is holding a heavy
lock — an `ACCESS EXCLUSIVE` from a DDL statement, or a `ROW EXCLUSIVE`/`SHARE`
conflict from a hot-row update — every reader and writer that needs the object
piles up behind it. The deadlock detector only breaks genuine *cycles*; a
one-directional chain is not a deadlock and will wait until the holder finishes or
you intervene. Meanwhile application threads block on their connection-pool
checkouts, and what began as one slow transaction turns into a cluster-wide stall.

## How to verify it yourself

`pg_blocking_pids()` (PostgreSQL 9.6+) resolves the full set of PIDs blocking each
waiter — this is exactly the blocked/blocking relationship pgbot counts:

```sql
SELECT a.pid                                   AS blocked_pid,
       a.usename, a.application_name,
       a.wait_event_type, a.wait_event,
       now() - a.state_change                  AS waiting_for,
       pg_blocking_pids(a.pid)                  AS blocked_by,
       left(a.query, 80)                        AS blocked_query
FROM pg_stat_activity a
WHERE cardinality(pg_blocking_pids(a.pid)) > 0
ORDER BY waiting_for DESC;
```

To see the actual locks and objects behind a wait, join `pg_locks` (still read-only):

```sql
SELECT l.pid, l.locktype, l.mode, l.granted,
       l.relation::regclass AS relation
FROM pg_locks l
WHERE NOT l.granted
ORDER BY l.pid;
```

## How to fix it

1. **Find the head of the chain** — the PID that blocks others but is not itself in
   `pg_blocking_pids()` of anyone. That transaction is the root cause; everything
   else is queued behind it.
2. **Let it finish, or end it.** If it is a real query that will commit soon, wait.
   If it is stuck (often idle-in-transaction — see [idle_in_transaction](idle_in_transaction.md)),
   cancel its current statement with `SELECT pg_cancel_backend(<pid>)`, or, if it is
   holding a transaction open with no running statement, terminate the whole backend
   with `SELECT pg_terminate_backend(<pid>)` (cancel does nothing to a session that
   isn't executing a statement).
3. **Prevent recurrence.** Set a `lock_timeout` so a statement gives up rather than
   queuing indefinitely, keep transactions short, run DDL with `CONCURRENTLY` or in
   low-traffic windows, and set `idle_in_transaction_session_timeout` so a forgotten
   transaction can't hold locks forever.

## When to ignore it

Rarely — this is a live event, and a suppressed `critical` still renders in the
report (visibly marked) and only drops out of the exit code. The legitimate case is
a **planned migration or maintenance window** where brief blocking is expected and
you don't want CI to fail on it while it runs. Because the finding keys on PIDs,
which don't survive to the next run, there is nothing stable to scope to — the
suppression is cluster-wide, by design:

```toml
[[ignore]]
finding = "blocking_chains"
reason  = "migration window 2027-01-01: brief ACCESS EXCLUSIVE waits expected; tracked in OPS-1234"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees a **single snapshot** of the lock graph, not the whole history. A chain
  that formed and cleared between samples is invisible; conversely one it reports
  may already have cleared by the time you look. It is a "right now" signal.
- It cannot see the **application** holding the transaction — which code path opened
  it, whether a client is mid-request or has walked away.
- The blocked and blocking identifiers are **PIDs**: ephemeral session identifiers
  that are gone next run. That is precisely why this finding is scoped to `cluster`
  and its `[[ignore]]` carries no `object` line — there is no durable name to
  suppress, so suppression is wholesale or not at all, deliberately.

## Related

- [long_running_transaction](long_running_transaction.md) — the transaction at the
  head of a chain is often simply a long one that took a lock and never let go.
- [idle_in_transaction](idle_in_transaction.md) — an idle-in-transaction session is
  the classic stuck lock holder: it runs no query yet blocks everyone behind it.

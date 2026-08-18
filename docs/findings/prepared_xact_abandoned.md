---
id: prepared_xact_abandoned
severity: warn
critical_when: "a prepared (2PC) transaction open over an hour"
dimension: risk
object: cluster
scope: infra
requires: []
thresholds: []
related: [vacuum_horizon_blocked]
---

# prepared_xact_abandoned

**Severity:** warn (critical when a prepared (2PC) transaction open over an hour) · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

A **prepared (two-phase-commit) transaction** has been sitting in the
`PREPARED` state, neither committed nor rolled back. pgbot reads the prepared-xact
holders the horizon collector gathers (from `pg_prepared_xacts`) and fires when one
has been open at least **`preparedXactWarnSec` = 300 seconds (5 minutes)** as
`warn`, escalating to `critical` once it has been open **`preparedXactCritSec` = 3600
seconds (1 hour)**. Score is `55` (warn) or `78` (critical), confidence `0.9`, basis
`pg_prepared_xacts`. The evidence names the transaction's `gid` and how far behind
its `xmin` sits.

## Why it matters

An abandoned prepared transaction is uniquely dangerous because it is **invisible in
`pg_stat_activity` and never times out**. It is not a session — after `PREPARE
TRANSACTION` the backend disconnects, and the prepared state is persisted on disk
(`pg_twophase`), so it **survives server restarts** and is untouched by
`idle_in_transaction_session_timeout`, `statement_timeout`, or `transaction_timeout`.
While it exists it holds locks and, critically, **pins the `xmin` horizon
indefinitely**: `VACUUM` cannot reclaim any row version dead since it was prepared,
so bloat grows and transaction-id age creeps toward wraparound — with no session for
an operator to notice. It is the quietest way a Postgres cluster slides into a
wraparound emergency.

## How to verify it yourself

```sql
SELECT gid,
       database,
       owner,
       prepared,
       now() - prepared      AS open_for,
       age(transaction)      AS xid_age
FROM pg_prepared_xacts
ORDER BY prepared;
```

Any row is a prepared transaction awaiting resolution; `open_for` is the age that
drives pgbot's warn/critical thresholds, and `age(transaction)` is how many
transactions of vacuum horizon it is holding back. If you don't run a distributed
transaction manager (XA/JTA) at all, any row here is almost certainly a leak — and
note `max_prepared_transactions` must be non-zero for these to exist in the first
place.

## How to fix it

1. **Confirm the transaction manager is finished** with this `gid`. If a coordinator
   (an XA/JTA resource manager, a distributed-transaction framework) is mid-recovery,
   let it complete the two-phase commit itself — resolving it by hand underneath a
   live coordinator can violate the atomicity the protocol exists to guarantee.
2. **Resolve it by hand only once you're sure it's abandoned.** Either finish it or
   discard it, by `gid`:
   ```sql
   COMMIT PREPARED   'the_gid';   -- if its work should land
   ROLLBACK PREPARED 'the_gid';   -- if it should be discarded (the usual case)
   ```
   Only a superuser or the transaction's original owner may run these. The moment it
   is resolved, the locks release and the vacuum horizon it pinned moves forward.
3. **Prevent recurrence.** If nothing legitimately uses 2PC, set
   `max_prepared_transactions = 0` so prepared transactions can't be created. If you
   do use 2PC, monitor `pg_prepared_xacts` age and make sure the coordinator reliably
   completes or aborts every branch.

## When to ignore it

A distributed transaction manager legitimately keeps prepared transactions open
briefly during its commit protocol, and you accept the short-lived horizon cost.
Because a prepared transaction is identified by an application-chosen `gid` on an
ephemeral, cluster-wide list — not a stable database object — the suppression is
cluster-scoped:

```toml
[[ignore]]
finding = "prepared_xact_abandoned"
reason  = "XA coordinator holds sub-minute prepared branches during commit; understood, OPS-6120"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees the **prepared-xact list**, not the **coordinator**. It cannot tell an
  in-flight two-phase commit apart from a truly abandoned one — only the age. Whether
  the transaction manager still intends to finish this `gid` is knowable only outside
  Postgres.
- It sees a **snapshot**; a prepared transaction that appears and is resolved between
  samples won't be reported.
- The transaction is identified by a `gid` on the cluster-wide `pg_prepared_xacts`
  list — there is no stable per-object identity to attach the finding to. That is why
  it is `cluster`-scoped and its `[[ignore]]` has no `object` line: suppression is
  wholesale, deliberately, so a new abandoned prepared transaction still surfaces.

## Related

- [vacuum_horizon_blocked](vacuum_horizon_blocked.md) — when a prepared transaction
  is the oldest xmin holder, it is exactly what pins the vacuum horizon that finding
  reports; the two often fire together and are cured by the same `COMMIT`/`ROLLBACK
  PREPARED`.

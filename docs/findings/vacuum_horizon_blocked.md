---
id: vacuum_horizon_blocked
severity: warn
critical_when: "the pinned XID age grows large (dimension escalates from storage to risk)"
dimension: storage
object: cluster
scope: infra
requires: []
thresholds: []
related: [idle_in_transaction, long_running_transaction, prepared_xact_abandoned, replication_slot_inactive]
---

# vacuum_horizon_blocked

**Severity:** warn (critical when the pinned XID age grows large (dimension escalates from storage to risk)) · **Dimension:** storage · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

Something is holding the cluster's **xmin horizon** in the past. pgbot gathers every
candidate holder of an old `xmin`, orders them oldest-first, and looks at the oldest
one: its `xmin` is at least **1,000,000** transactions behind the current
transaction id (the `vacuumHorizonWarnXIDs` constant), so the finding fires at
`warn` with dimension `storage`. The holder is one of exactly four kinds, whichever
is oldest:

- **`backend`** — an open transaction in `pg_stat_activity` with a non-null
  `backend_xmin` (a long-running query or an idle-in-transaction session), named by
  pid.
- **`replication_slot`** — a slot in `pg_replication_slots` whose `xmin` or
  `catalog_xmin` is set, named by slot name.
- **`standby_feedback`** — a replica's `hot_standby_feedback`, which arrives on the
  primary as a `backend_xmin` in `pg_stat_replication`, named by client address.
- **`prepared_xact`** — a prepared (2PC) transaction in `pg_prepared_xacts`, named
  by gid. This one is invisible in `pg_stat_activity` and never times out.

The finding **escalates to `critical` and its dimension changes from `storage` to
`risk`** once the oldest holder's `xmin` is **1,000,000,000** transactions behind
(`xidWraparoundWarn`) — at that point the pinned horizon is no longer just a bloat
problem, it is actively feeding [txid_wraparound](txid_wraparound.md). Neither
threshold is overridable in `[thresholds]`.

## Why it matters

VACUUM can only remove a dead row version that is invisible to **every** snapshot
that might still need it. The oldest `xmin` in use defines that cutoff — the
horizon — and vacuum reclaims nothing newer than it, on any table in the cluster, no
matter how much dead tuple churn has piled up. So a single forgotten transaction or
a disconnected replication slot can freeze reclamation database-wide: dead tuples
accumulate, tables and indexes bloat, and query plans degrade as heaps fill with
invisible rows. Left long enough it stops being a storage problem and becomes a
correctness deadline — because the same held-back horizon also prevents *freezing*,
so `age(datfrozenxid)` climbs and the cluster marches toward the wraparound wall.

## How to verify it yourself

pgbot's number is the oldest holder's `xmin` age in transactions. Reproduce it, and
name every holder, with these four read-only queries — one per source. The largest
`xmin_age` among them is the one pgbot reports:

```sql
-- (1) Backends: long-running or idle-in-transaction sessions holding an old xmin.
SELECT pid,
       age(backend_xmin)                        AS xmin_age,
       state,
       round(extract(epoch FROM now() - xact_start)) AS xact_age_s
FROM pg_stat_activity
WHERE backend_xmin IS NOT NULL
  AND pid <> pg_backend_pid()
ORDER BY xmin_age DESC;

-- (2) Replication slots pinning the horizon via xmin / catalog_xmin.
SELECT slot_name, active,
       age(xmin)         AS xmin_age,
       age(catalog_xmin) AS catalog_xmin_age
FROM pg_replication_slots
WHERE xmin IS NOT NULL OR catalog_xmin IS NOT NULL
ORDER BY greatest(coalesce(age(xmin), 0), coalesce(age(catalog_xmin), 0)) DESC;

-- (3) Standby hot_standby_feedback, arriving as backend_xmin on the primary.
SELECT coalesce(host(client_addr), application_name, 'standby') AS standby,
       age(backend_xmin) AS xmin_age
FROM pg_stat_replication
WHERE backend_xmin IS NOT NULL
ORDER BY xmin_age DESC;

-- (4) Prepared (2PC) transactions — invisible in pg_stat_activity, never time out.
SELECT gid,
       age(transaction)                         AS xmin_age,
       round(extract(epoch FROM now() - prepared)) AS prepared_age_s
FROM pg_prepared_xacts
ORDER BY xmin_age DESC;
```

## How to fix it

Identify the oldest holder from the queries above, then **end it** — the right move
depends on which of the four it is:

- **`backend` (open transaction).** Find the pid and end the transaction. A stuck
  idle-in-transaction session can be closed with `SELECT pg_terminate_backend(<pid>)`;
  a genuinely needed long query should be allowed to finish or be cancelled with
  `pg_cancel_backend(<pid>)`. Set `idle_in_transaction_session_timeout` so it cannot
  recur silently. See [idle_in_transaction](idle_in_transaction.md) and
  [long_running_transaction](long_running_transaction.md).
- **`replication_slot`.** If the slot's consumer is gone for good, drop it:
  `SELECT pg_drop_replication_slot('<slot_name>')`. If the consumer is merely behind,
  let it catch up (or fix why it stalled) so its `xmin` advances. See
  [replication_slot_inactive](replication_slot_inactive.md). **Do not drop a slot a
  live standby or logical subscriber still depends on** — that breaks its replication.
- **`standby_feedback`.** A replica's `hot_standby_feedback` is deliberately holding
  the horizon to protect its queries. Shorten the long queries on that standby, or —
  weighing the trade-off — turn `hot_standby_feedback` off there, which stops the
  pinning at the cost of possible query cancellations on the standby.
- **`prepared_xact`.** An abandoned prepared transaction blocks vacuum forever and
  will not time out. Once you have confirmed its transaction manager is truly done,
  resolve it: `COMMIT PREPARED '<gid>'` or `ROLLBACK PREPARED '<gid>'`. See
  [prepared_xact_abandoned](prepared_xact_abandoned.md).

Releasing the holder is the whole fix — the horizon advances immediately and the
next (auto)vacuum reclaims the backlog. There is no need to `VACUUM FULL`; a normal
vacuum will now clean what it previously could not.

## When to ignore it

When the holder is deliberate and understood — a maintenance job, a base backup, or
a logical-replication slot you actively depend on — and the resulting bloat is
acceptable for its duration. It is safe to silence *only while the age stays modest*;
if it climbs toward wraparound the finding re-escalates to `critical`/`risk`, and you
want that. The finding is cluster-scoped, so the block carries **no** `object` line:

```toml
[[ignore]]
finding = "vacuum_horizon_blocked"
reason  = "nightly logical-dump slot pins the horizon during the backup window; expected"
expires = "2027-01-01"
```

Because there is no object, this mutes the whole check — including a *different*
holder that appears tomorrow. Keep `expires` short and revisit, rather than leaving
the cluster's horizon unwatched.

## What pgbot cannot see

- It names the holder by pid, slot, client address, or gid — **not** by application.
  It cannot tell you which service opened the transaction or which consumer owns the
  slot; that requires correlating the pid/slot with your own deployment.
- It reads `xmin` ages at one instant. A transaction that is about to commit and one
  that has been idle for hours both show the same way; the `state` and `xact_age_s`
  columns above distinguish them.
- It shows the *oldest* holder that trips the threshold and lists up to ten, but the
  underlying query is `LIMIT 10` ordered by age — a very large fleet of holders could
  have more beyond that.
- It reports the horizon, not the resulting bloat. How much dead tuple space is
  actually trapped behind it is a separate, per-table measurement.

## Related

- [long_running_transaction](long_running_transaction.md) — a long-open transaction
  is the most common `backend` holder of the horizon.
- [idle_in_transaction](idle_in_transaction.md) — an idle-in-transaction session
  pins `xmin` while doing no work at all; a prime cause.
- [prepared_xact_abandoned](prepared_xact_abandoned.md) — the `prepared_xact` holder
  that never times out and blocks vacuum indefinitely.
- [replication_slot_inactive](replication_slot_inactive.md) — an inactive slot pins
  both the horizon and WAL; often the `replication_slot` holder here.

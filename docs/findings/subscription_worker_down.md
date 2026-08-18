---
id: subscription_worker_down
severity: warn
critical_when: ""
dimension: risk
object: sub
scope: infra
requires: [logical replication]
thresholds: []
related: [replica_disconnected]
---

# subscription_worker_down

**Severity:** warn · **Dimension:** risk · **Object identity:** `sub:<name>` (see [configuration](../configuration.md)) · **Requires:** logical replication

## What pgbot observed

A logical-replication subscription exists on this node but its **apply worker is not
running** (`WorkerRunning = false`, derived from the subscription having no live worker
row in `pg_stat_subscription`). The subscription is defined, but nothing is currently
pulling and applying changes from the publisher — so this subscriber has stopped
tracking the source. There is no threshold: a defined subscription with no running
apply worker fires the finding.

## Why it matters

Logical replication is silent when it stalls. The subscription still exists, the tables
are still there, queries against them still succeed — they just return **increasingly
stale data** because no new changes from the publisher are being applied. There is no
error to the application and no gap in `pg_stat_replication` on the subscriber. Worse,
a stalled subscription usually means the publisher's replication slot is **inactive and
retaining WAL** on the *other* side (see [replication_slot_inactive](replication_slot_inactive.md)) —
so a down apply worker here can quietly fill the publisher's disk. The longer it's
down, the further this subscriber drifts and the more WAL the publisher must keep.

## How to verify it yourself

Run on the **subscriber**. A subscription with no row in `pg_stat_subscription` that
has a non-null `pid` has no running apply worker — that's pgbot's condition:

```sql
SELECT s.subname,
       s.subenabled,                       -- was it disabled, or crashed while enabled?
       st.pid,                             -- NULL / no row  ⇒  apply worker not running
       st.received_lsn,
       st.latest_end_lsn,
       st.latest_end_time,
       now() - st.latest_end_time AS since_last_progress
FROM   pg_subscription s
LEFT   JOIN pg_stat_subscription st
       ON st.subid = s.oid AND st.relid IS NULL   -- relid IS NULL = the main apply worker
ORDER  BY s.subname;
```

A subscription whose `pid` is `NULL` (no main-worker row) is the one pgbot flagged.
`subenabled = false` tells you it was disabled; `subenabled = true` with no `pid` means
the worker died or is crash-looping — check the subscriber's server log for apply
errors.

## How to fix it

Work out *why* the worker isn't running, in this order:

1. **Is it simply disabled?** If `subenabled = false`, re-enable it:
   ```sql
   ALTER SUBSCRIPTION orders_sync ENABLE;
   ```
2. **Is it crash-looping on an apply error?** A conflicting row (duplicate key, missing
   column, type mismatch) makes the apply worker error out, restart, and hit the same
   change again. Find the error in the subscriber log, resolve the conflict on the
   subscriber, and let it retry — or, on PG15+, skip the offending transaction with
   `ALTER SUBSCRIPTION … SKIP (lsn = '…')` (or set `disable_on_error` so it stops
   instead of looping).
3. **Does the publisher's slot still exist?** If the publisher's replication slot was
   dropped, the subscriber can't resume; recreate the slot or, if too much has been
   lost, re-copy the tables. Check the `subslotname` and confirm it's present on the
   publisher.
4. **Confirm progress resumes.** After the fix, `received_lsn` / `latest_end_time`
   should start advancing again in the query above.

## When to ignore it

This finding is **subscription-scoped** (`object = "sub:<name>"`). Ignore only a
*specific* subscription you have deliberately paused — for example a one-off migration
subscription you disabled after cutover but haven't dropped yet:

```toml
[[ignore]]
finding = "subscription_worker_down"
object  = "sub:orders_sync"
reason  = "migration cutover complete; subscription intentionally disabled pending teardown, OPS-1234"
expires = "2027-01-01"
```

**Do not omit `object`.** A bare `finding = "subscription_worker_down"` mutes the check
for *every* subscription, so a different subscription's worker crashing later would go
unnoticed while your data silently drifts.

## What pgbot cannot see

- It sees that the worker **isn't running**, not **why** — disabled, crashed, or
  blocked on an apply conflict all present the same way. The log is where the reason
  lives.
- It cannot measure how far this subscriber has **drifted** from the publisher, only
  that application has stopped; the lag/backlog lives on the publisher's slot.
- The publisher side is a separate node pgbot may not be scanning; the retained-WAL
  consequence there is invisible from the subscriber unless pgbot also runs on the
  publisher.
- A worker that is running but making no *progress* (stuck mid-transaction) is not this
  finding — the worker exists, so `WorkerRunning` is true.

## Related

- [replica_disconnected](replica_disconnected.md) — the physical-replication analogue;
  both are silent losses of a downstream consumer that point-in-time views miss.
- [replication_slot_inactive](replication_slot_inactive.md) — the publisher-side
  consequence of this finding: the slot feeding this subscription goes inactive and
  pins WAL.

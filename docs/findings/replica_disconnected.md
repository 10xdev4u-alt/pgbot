---
id: replica_disconnected
severity: warn
critical_when: ""
dimension: risk
object: cluster
scope: history
requires: [replication]
thresholds: []
related: [sync_rep_degraded, replication_slot_inactive]
---

# replica_disconnected

**Severity:** warn · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** replication

## What pgbot observed

A standby that was present in `pg_stat_replication` at the **last run** is absent
**now**. pgbot detects this from the run-over-run diff (the `replication.standby_gone`
delta between the stored baseline and the current scan) and names the standby that
vanished. This is inherently a temporal finding: it needs a prior baseline, because a
disconnected standby simply isn't in `pg_stat_replication` anymore — there is no
counter or row that says "a standby used to be here." Only comparing against history
sees the absence.

## Why it matters

A standby that stops connecting is invisible to any point-in-time tool: the primary
keeps running, `pg_stat_replication` just has one fewer row, and nothing errors. But
your HA posture has quietly degraded — you have fewer failover targets than you think,
and if the vanished standby was a **synchronous** one, commits may now be blocking (see
[sync_rep_degraded](sync_rep_degraded.md)). If the standby fed a replication slot, that
slot is now **inactive and pinning WAL**, so the disk starts filling from the moment it
disconnected (see [replication_slot_inactive](replication_slot_inactive.md)). The
danger is precisely that it's silent until one of those second-order effects bites.

## How to verify it yourself

Run on the **primary** and compare against the set of standbys you expect to be
connected:

```sql
SELECT application_name,
       client_addr,
       state,
       sync_state,
       backend_start,      -- when this standby's current connection began
       now() - backend_start AS connected_for
FROM   pg_stat_replication
ORDER  BY backend_start;
```

A standby you know should be streaming but is missing from this result is the one
pgbot flagged. If it reconnected between pgbot's scan and your query, `backend_start`
will be very recent — evidence it dropped and came back. Cross-check the primary's log
for `walsender` disconnect messages around pgbot's run time, and check
`pg_replication_slots` for the slot that standby used (now `active = false`).

## How to fix it

1. **Is the standby down, or just unreachable?** Check whether the standby host is up
   and its Postgres is running. If it's up, the break is in the path — firewall,
   security group, `listen_addresses`, or a `primary_conninfo` that no longer resolves.
2. **Was it reconfigured or decommissioned on purpose?** A planned teardown is a
   legitimate cause — but if so, **drop its replication slot** on the primary
   (`SELECT pg_drop_replication_slot('…')`), or it will silently retain WAL forever.
3. **Did its slot get dropped out from under it?** If the primary's slot was removed
   while the standby was away, the standby can no longer resume from where it left off
   and will need a rebuild (`pg_basebackup`) if its required WAL has since been
   recycled.
4. **Confirm sync status after recovery.** If this was a sync standby, re-check
   [sync_rep_degraded](sync_rep_degraded.md) once it's back — being connected isn't the
   same as being back in `sync` state.

## When to ignore it

You intentionally removed a standby (scaling down, migrating hosts) and have already
dealt with its slot. This is cluster-scoped, so the ignore covers *any* standby
disappearing, not just the one you retired — keep the window short so a genuinely
unexpected disconnect later still surfaces:

```toml
[[ignore]]
finding = "replica_disconnected"
reason  = "decommissioned reporting standby s3 on 2026-08-17; its slot was dropped, WAL not pinned"
expires = "2027-01-01"
```

## What pgbot cannot see

- Detection is **baseline-dependent**. With no prior run (first scan, or a wiped
  baseline) a standby that is already gone is simply never seen — pgbot can't miss what
  it never recorded.
- It sees the disconnect, not the **reason**: down host, network break, dropped slot,
  and planned teardown all look identical from `pg_stat_replication`.
- A standby that dropped and reconnected entirely between two runs leaves no trace in
  the diff — the flap is invisible unless it was absent during a scan.
- It cannot see the standby's own logs or health; the "why" investigation above is
  manual.

## Related

- [sync_rep_degraded](sync_rep_degraded.md) — if the vanished standby was synchronous,
  commits may now be blocking or the durability guarantee silently relaxed.
- [replication_slot_inactive](replication_slot_inactive.md) — a disconnected standby
  that used a slot leaves that slot pinning WAL, filling the primary's disk.

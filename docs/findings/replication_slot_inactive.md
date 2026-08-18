---
id: replication_slot_inactive
severity: warn
critical_when: "retained WAL past the critical size"
dimension: risk
object: slot
scope: infra
requires: []
thresholds: []
related: [vacuum_horizon_blocked, replica_disconnected]
---

# replication_slot_inactive

**Severity:** warn (critical when retained WAL past the critical size) · **Dimension:** risk · **Object identity:** `slot:<name>` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

A replication slot in `pg_replication_slots` is **inactive** (`active = false` — no
consumer connected) *and* it is holding back a material amount of WAL: at least
**512 MiB** retained (`slotRetainWarnBytes = 512 << 20`) trips `warn`. It escalates to
`critical` when either the retained WAL reaches **8 GiB**
(`slotRetainCritBytes = 8 << 30`) **or** the slot's `wal_status` is `lost`, which means
the WAL the slot needed has *already been removed* — the slot is broken and its
consumer can never resume from it. An active, healthy slot (`active = true`,
`wal_status ≠ lost`) is skipped, and an inactive slot retaining less than 512 MiB is
treated as a brief reconnection gap and skipped too. pgbot reports the retained size as
`pg_current_wal_lsn() - restart_lsn`.

## Why it matters

An inactive slot is a WAL ratchet. Postgres will not recycle any WAL past the slot's
`restart_lsn` until the slot's consumer reconnects and advances it — so the moment the
consumer disappears, `pg_wal` starts growing and **never stops on its own**. This is one
of the most common ways to fill a Postgres data disk and take the **primary** down hard:
not a slow leak, but unbounded growth pinned by a slot nobody is reading. A slot at
`wal_status = lost` is worse in a subtle way — the disk pressure is relieved because the
WAL was force-removed, but the standby or logical subscriber that depended on it is now
unrecoverable and needs a full rebuild.

## How to verify it yourself

Run on the **primary** (where `pg_current_wal_lsn()` is valid). This reproduces
pgbot's per-slot retained size and its active/lost state:

```sql
SELECT slot_name,
       slot_type,
       database,
       active,
       wal_status,
       pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)                 AS retained_bytes,
       pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS retained_pretty
FROM   pg_replication_slots
ORDER  BY retained_bytes DESC NULLS LAST;
```

A row with `active = false` and `retained_bytes ≥ 536870912` (512 MiB) is a `warn`;
`≥ 8589934592` (8 GiB), or any `wal_status = 'lost'`, is a `critical`. Cross-check the
on-disk reality with `SELECT pg_size_pretty(sum(size)) FROM pg_ls_waldir();`.

## How to fix it

Decide whether the consumer is coming back, then act — do not just wait:

1. **Consumer gone for good → drop the slot.** A standby you decommissioned or a
   logical subscriber you deleted leaves an orphan slot that will fill the disk. Drop
   it:
   ```sql
   SELECT pg_drop_replication_slot('wal2json_prod');
   ```
   This immediately frees the retained WAL for recycling. This is the single most
   common fix, and the most commonly forgotten step when tearing down a replica.
2. **Consumer should be connected → reconnect it.** Restart the standby / logical
   subscriber (or fix its network path) so it re-attaches and advances `restart_lsn`.
   Once active, retention drains on the next checkpoint.
3. **Cap the blast radius — `max_slot_wal_keep_size`.** Set a ceiling on how much WAL
   *any* slot may pin. When a slot exceeds it, Postgres invalidates the slot
   (`wal_status = lost`) rather than let it fill the disk — you lose that consumer but
   save the primary. This converts an unbounded outage into a bounded, recoverable one
   and is worth setting cluster-wide as a safety net.

## When to ignore it

This finding is **slot-scoped** (`object = "slot:<name>"`). Ignore only a *specific*
slot whose retention you understand and accept — for example a slot for a consumer that
is briefly offline for a known maintenance window:

```toml
[[ignore]]
finding = "replication_slot_inactive"
object  = "slot:wal2json_prod"
reason  = "CDC consumer paused for planned upgrade; reconnecting before disk pressure, tracked in OPS-1234"
expires = "2027-01-01"
```

**Do not omit `object`.** A bare `finding = "replication_slot_inactive"` mutes the
check for *every* slot, including a new orphan you create tomorrow — which is exactly
how an unbounded WAL fill goes unnoticed until the primary's disk is full.

## What pgbot cannot see

- The retained size is a **point-in-time** read of `restart_lsn` vs. the current LSN;
  on a fast-writing primary it grows between the scan and your check.
- It cannot tell you *why* the consumer disconnected — down host, dropped standby, or
  paused CDC pipeline all present as `active = false`.
- It reports retention against the current WAL position; it can't know whether the
  consumer intends to return, so a legitimately-paused slot and a truly-orphaned one
  look the same until you investigate.
- On a managed provider, some slots may be created and managed by the platform's own
  replication/backup machinery.

## Related

- [vacuum_horizon_blocked](vacuum_horizon_blocked.md) — a logical/physical slot also
  pins the xmin horizon, so an inactive slot can block vacuum cleanup as well as
  retain WAL.
- [replica_disconnected](replica_disconnected.md) — the usual cause: the standby that
  fed this slot stopped connecting, leaving the slot inactive.

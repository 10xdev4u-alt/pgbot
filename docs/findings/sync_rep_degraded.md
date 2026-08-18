---
id: sync_rep_degraded
severity: critical
critical_when: ""
dimension: risk
object: cluster
scope: infra
requires: [replication, synchronous_standby_names set]
thresholds: []
related: [replica_lag_time, replica_disconnected]
---

# sync_rep_degraded

**Severity:** critical · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** replication, synchronous_standby_names set

## What pgbot observed

`synchronous_standby_names` requires **N** sync standbys, `synchronous_commit` is
one of `on` / `remote_write` / `remote_apply` (so commits actually wait for them),
but fewer than **N** standbys are connected in `sync` or `quorum` state right now.
pgbot parses the required count out of `synchronous_standby_names` the way Postgres
does: `ANY N (...)` or `FIRST N (...)` → **N**, a bare leading `N (...)` → **N**, a
plain list of names → **1**, and an empty value → **0** (sync rep not in use, so this
never fires). It then counts replicas in `pg_stat_replication` whose `sync_state` is
`sync` or `quorum` and compares: the finding fires when `got < required`. There is no
overridable threshold — the requirement is whatever your own `synchronous_standby_names`
declares.

## Why it matters

This is the one that hurts silently, and it cuts two ways depending on the exact
`synchronous_commit` mode. If a required sync standby is missing, every commit that
must wait for a synchronous acknowledgement **blocks** until one appears — a write
stall that looks like a hang, not an error. If instead someone quietly relaxed
`synchronous_commit` (or the standby is present but never reaches `sync` state), the
cluster keeps committing at full speed while the durability guarantee everyone
believes in has **stopped applying** — you think you have zero-data-loss failover, and
you don't. Either way the RPO you designed for is not the RPO you have.

## How to verify it yourself

Run this on the **primary**. It shows what the config asks for and how many
`sync`/`quorum` standbys are actually connected — reproduce pgbot's `got` vs
`required`:

```sql
-- What the durability contract asks for:
SHOW synchronous_standby_names;   -- required N is parsed from ANY/FIRST N, or 1 for a bare list
SHOW synchronous_commit;          -- must be on / remote_write / remote_apply to wait at all

-- How many standbys count toward it right now (pgbot's "got"):
SELECT sync_state, count(*)
FROM   pg_stat_replication
WHERE  sync_state IN ('sync', 'quorum')
GROUP  BY sync_state;

-- The individual standbys and their priority/state:
SELECT application_name, client_addr, state, sync_state, sync_priority
FROM   pg_stat_replication
ORDER  BY sync_priority, application_name;
```

If the `sync`/`quorum` row count is below the number in `synchronous_standby_names`,
the finding is correct.

## How to fix it

1. **Find the missing standby.** Compare the `application_name`s listed in
   `synchronous_standby_names` against what is actually in `pg_stat_replication`. A
   name in the config with no matching row is the one that vanished; a row stuck in
   `state = 'catchup'` (never reaching `streaming`/`sync`) is present but not yet
   counting.
2. **Reconnect or rebuild it.** If the standby is up but disconnected, fix the network
   path / `primary_conninfo` and let it re-attach. If its WAL is too far behind to
   catch up (or its slot was dropped and required WAL is gone), rebuild it with
   `pg_basebackup` and let it stream back into `sync` state.
3. **Reconcile the config with reality.** If the missing standby is gone for good,
   either bring a replacement into `sync_state = sync` or edit
   `synchronous_standby_names` (and `SELECT pg_reload_conf()`) so it names the
   standbys that actually run — otherwise commits keep waiting for a ghost. Consider
   `ANY 1 (s1, s2)` quorum commit so the loss of any single standby doesn't stall
   writes.

Do not "fix" a write stall by blindly turning `synchronous_commit = off` under
pressure — that trades the hang for silent data-loss risk, which is the other half of
this same finding.

## When to ignore it

Rare, and only when you have deliberately chosen availability over the sync guarantee
for a known, time-boxed window (e.g. a planned standby rebuild). This is
cluster-scoped, so the suppression covers the whole durability contract — do not leave
it in place:

```toml
[[ignore]]
finding = "sync_rep_degraded"
reason  = "planned rebuild of standby s2; commits intentionally on the remaining sync standby until 2027-01-01"
expires = "2027-01-01"
```

A suppressed `critical` still renders in the report (marked); the ignore only removes
it from the exit code, so a stale entry here quietly hides a real durability gap.

## What pgbot cannot see

- It reports a **point-in-time** count. A standby that flaps in and out of `sync`
  state is only caught if it is disconnected during the scan.
- It reads `sync_state` from the primary's view; it cannot confirm the standby is
  actually persisting WAL to durable storage, only that Postgres considers it
  synchronous.
- It infers the required count by parsing `synchronous_standby_names`; an exotic or
  malformed value could be counted differently than the running Postgres interprets
  it.
- On a managed provider, the platform may manage a hidden sync standby of its own that
  is not visible in `pg_stat_replication`.

## Related

- [replica_lag_time](replica_lag_time.md) — a standby can be connected and counted as
  sync yet still lag in replay; the two together tell the full failover-RPO story.
- [replica_disconnected](replica_disconnected.md) — the run-over-run detection of a
  standby that was streaming last run and is now gone, often the root cause here.

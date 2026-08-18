---
id: checksum_failures
severity: critical
critical_when: ""
dimension: risk
object: db
scope: infra
requires: [PG12+, data_checksums=on]
thresholds: []
related: [ignore_checksum_failure_on, checksums_disabled]
---

# checksum_failures

**Severity:** critical · **Dimension:** risk · **Object identity:** `db:<database>` (see [configuration](../configuration.md)) · **Requires:** PostgreSQL 12+, `data_checksums = on`

## What pgbot observed

`pg_stat_database.checksum_failures` is greater than zero on one or more databases:
Postgres read at least one page whose stored checksum did not match the page's
contents. This is a **data-integrity** signal, not a performance one.

## Why it matters

A checksum mismatch means a page on disk is not what Postgres wrote there —
storage corruption, failing memory, or a filesystem/volume that acknowledged a
write it did not actually persist. It **does not heal itself**, reads of the
affected page return wrong data, and the damage can spread if the page is copied
or rewritten. Treat it as a live incident.

## How to verify it yourself

```sql
SELECT datname, checksum_failures, checksum_last_failure
FROM pg_stat_database
WHERE checksum_failures > 0
ORDER BY checksum_failures DESC;
```

The server log holds the detail the counter doesn't — grep it for the exact block
and file, which names the corrupt relation:

```
grep -i 'checksum' $PGDATA/log/*.log
# WARNING: page verification failed, calculated checksum ... but expected ...
# in file "base/16384/24576" block 12345
```

## How to fix it

Preserve evidence, then recover from a good copy. In order:

1. **Take the affected relation out of write service** — stop the workload writing
   to it, or fail over reads to a healthy replica, so nothing overwrites the
   evidence or acts on wrong data.
2. **Identify the affected relation** from the server-log message: map the
   `base/<db>/<relfilenode>` in the WARNING to a relation with
   `SELECT relname FROM pg_class WHERE relfilenode = <n>;` (or `pg_filenode_relation`).
3. **Investigate the hardware.** Check `dmesg`, the storage device's SMART data,
   and ECC-memory logs. A checksum failure is a symptom; the failing component is
   the disease, and it will corrupt more pages until fixed.
4. **Restore the affected data from a known-good backup** — point-in-time recovery
   to just before the corruption appeared, or restore the relation from a verified
   dump. On a managed provider, open a support case and use their PITR.

**Do not** run `VACUUM FULL`, `REINDEX`, `CLUSTER`, or `pg_repack` on the affected
relation. These rewrite the pages: they **destroy the evidence** you need to scope
the damage and can **propagate** the corruption into indexes and new heap pages.
The instinct to "rebuild it" is the single most common way to turn a recoverable
incident into an unrecoverable one.

## When to ignore it

Effectively never for a genuine failure. A suppressed `critical` **still renders**
(visibly marked) and only drops out of the exit code — so an `[[ignore]]` here can
stop a *known, already-handled* incident from failing CI while you close it out,
but it can never make the signal disappear from the report:

```toml
# A suppressed critical still shows in the report — this only removes it from the
# exit code. Never use it to hide a live corruption signal. Scope it to the one
# database you've handled; other databases still trip the exit code.
[[ignore]]
finding = "checksum_failures"
object  = "db:app"
reason  = "incident 2026-08-14: restored via PITR, counter is residual; tracked in OPS-1234"
expires = "2026-09-14"
```

## What pgbot cannot see

- It sees the **counter**, not which relation or block failed — that is only in the
  server log (see the verification step).
- It cannot distinguish permanent corruption from a transient controller/cable
  glitch that has since cleared; both increment the counter, and both warrant the
  hardware investigation.
- On a managed provider it cannot see the provider's own integrity checks or
  whether a replica has a clean copy.

## Related

- [ignore_checksum_failure_on](ignore_checksum_failure_on.md) — the
  `ignore_checksum_failure` setting makes Postgres return corrupt pages instead of
  erroring; if that is on, this counter may under-report.
- [checksums_disabled](checksums_disabled.md) — without data checksums, this class
  of corruption is silent; there is no counter to trip.

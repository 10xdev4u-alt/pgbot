---
id: ignore_checksum_failure_on
severity: critical
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: []
thresholds: []
related: [checksum_failures, checksums_disabled]
---

# ignore_checksum_failure_on

**Severity:** critical · **Dimension:** risk · **Object identity:** `setting:ignore_checksum_failure` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `ignore_checksum_failure` GUC reads exactly `on` — pgbot's literal test is
`settingParam(c, "ignore_checksum_failure") == "on"`, which fires this `critical`
unconditionally. It is a boolean, so there is no threshold to tune.

## Why it matters

Normally, when a page fails its data checksum Postgres raises an **error** and aborts
the read — a loud, catchable signal that the page on disk is corrupt. With
`ignore_checksum_failure = on`, Postgres instead logs a warning and **returns the
corrupt page's contents as if they were valid**. That silently converts a caught
integrity failure into live bad data: the corruption flows into query results, into
new pages when the data is rewritten, and into logical dumps and backups. This GUC
exists only as a last-resort recovery tool for deliberately extracting rows from an
already-damaged page — leaving it on in normal operation defeats the entire point of
having checksums.

## How to verify it yourself

```sql
SHOW ignore_checksum_failure;    -- should be 'off'
-- and where the value came from, plus who can set it:
SELECT setting, source, context FROM pg_settings
WHERE name = 'ignore_checksum_failure';
```

## How to fix it

Turn it back **off immediately**, then investigate the underlying corruption. It is a
superuser, per-session GUC and is reloadable (no restart) — but if it is on via
`ALTER SYSTEM` or `postgresql.conf`, clear that global default:

```sql
ALTER SYSTEM SET ignore_checksum_failure = off;
SELECT pg_reload_conf();
```

Setting it back to `off` is only step one. The reason someone turned it on is a page
that failed its checksum — so follow the corruption runbook: find the affected
relation from the server log's checksum WARNING, investigate the hardware (storage
SMART data, ECC memory, `dmesg`), and restore the affected data from a known-good
backup. Do **not** `VACUUM FULL` or `REINDEX` the relation — that destroys the
evidence and can propagate the damage.

## When to ignore it

Essentially never on a running system. Like [checksum_failures](checksum_failures.md),
a suppressed `critical` **still renders** in the report (visibly marked) and only drops
out of the exit code — so an `[[ignore]]` here can keep CI green during a *deliberate,
supervised* recovery session where you knowingly enabled the GUC to salvage rows, but
it can never make the signal disappear. Scope it to this one setting with a short
expiry:

```toml
# A suppressed critical still shows in the report — this only removes it from the exit
# code. Use only during a controlled recovery, never to hide it in normal operation.
[[ignore]]
finding = "ignore_checksum_failure_on"
object  = "setting:ignore_checksum_failure"
reason  = "controlled recovery to extract rows from a damaged page; tracked in OPS-XXXX"
expires = "2026-09-01"
```

## What pgbot cannot see

- It reads the current setting, not **why** it is on — a deliberate, supervised
  recovery looks identical to an accident left in place.
- It cannot see whether corrupt pages have already propagated into dumps, backups, or
  replicas while the flag was on — the damage may extend beyond the live cluster.
- It sees this GUC's value, not the checksum failures it is masking; with it on, the
  `checksum_failures` counter may under-report.

## Related

- [checksum_failures](checksum_failures.md) — the actual corruption signal this
  setting silences; with `ignore_checksum_failure` on, that counter can under-report.
- [checksums_disabled](checksums_disabled.md) — if data checksums are off entirely,
  there is nothing for this flag to ignore, and corruption is undetectable.

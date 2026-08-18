---
id: statement_timeout_unset
severity: info
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: []
thresholds: []
related: [long_running_transaction, idle_in_transaction]
---

# statement_timeout_unset

**Severity:** info · **Dimension:** risk · **Object identity:** `setting:statement_timeout` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `statement_timeout` GUC reads exactly `0` — pgbot's literal test is
`settingParam(c, "statement_timeout") == "0"`, where `0` means *disabled*: no
statement is ever cancelled for running too long. There is no threshold to tune; the
finding is `info`, a nudge rather than a defect.

## Why it matters

With no `statement_timeout`, a runaway or pathological query can run **indefinitely** —
holding locks other sessions queue behind, and pinning the transaction `xmin` horizon
so vacuum can't reclaim dead tuples anywhere in the cluster (feeding bloat). A generous
cluster-wide cap is a cheap safety net: it bounds the blast radius of a bad query
without affecting normal ones, and the few genuinely long jobs can raise the limit
locally.

## How to verify it yourself

```sql
SHOW statement_timeout;    -- '0' means no limit
-- or the effective value, its unit, and where it came from:
SELECT setting, unit, source FROM pg_settings WHERE name = 'statement_timeout';
```

## How to fix it

Set a generous cluster default and override upward where you know you need to.
`statement_timeout` is reloadable (no restart) and applies to sessions started after
the reload:

```sql
ALTER SYSTEM SET statement_timeout = '60s';
SELECT pg_reload_conf();
```

Pick a value comfortably above your slowest *normal* query so it only catches
genuinely stuck ones. Raise it per-role or per-session for known long jobs —
`ALTER ROLE reporting SET statement_timeout = '30min'`, or `SET statement_timeout = 0`
inside a maintenance session — rather than leaving the whole cluster uncapped.

## When to ignore it

An analytics or warehouse cluster where long queries are the norm and a global cap
would cancel legitimate work, or where timeouts are already enforced at the pooler or
application layer. Being `info`, it barely affects the exit code — but you can still
mute it cleanly, scoped to this one setting:

```toml
[[ignore]]
finding = "statement_timeout_unset"
object  = "setting:statement_timeout"
reason  = "OLAP cluster; per-query timeouts enforced in the application, global cap intentionally off"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the **cluster default** only. It cannot see per-role or per-session
  `statement_timeout` overrides (a session may set its own), so an unset global does
  not prove that no protection exists anywhere.
- It cannot see timeouts enforced outside Postgres — at PgBouncer, a proxy, or in the
  application's query layer.

## Related

- [long_running_transaction](long_running_transaction.md) — the concrete harm an
  uncapped statement can cause: a transaction that runs long enough to hold the
  `xmin` horizon and block vacuum.
- [idle_in_transaction](idle_in_transaction.md) — a related bound is
  `idle_in_transaction_session_timeout`, which caps sessions that hold a transaction
  open while doing nothing.

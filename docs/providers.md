# Provider compatibility matrix

How pgbot behaves on each managed-Postgres platform: what it can see, what it
can't, and the exact steps to unblock the degraded paths.

> **Status — verification pending.** The rows below are seeded from pgbot's
> built-in provider detection and remediation logic (`internal/conn/provider.go`),
> which encodes the enable-steps pgbot prints at runtime. They have **not yet
> been confirmed against a live connection to each provider.** Per the honesty
> rule for this matrix, unverified rows are marked ⏳; do not treat them as
> tested until someone has run `pgbot inspect` against a real instance and
> checked the boxes in [Verification checklist](#verification-checklist) below.
> An honest matrix with two "partial" rows beats six unearned green checks.

## Summary

| Provider | `pg_monitor` grantable | `pg_stat_statements` | Default DSN pooled | Stats survive idle | Status |
|---|---|---|---|---|---|
| Amazon RDS | ⏳ yes, via `rds_superuser` | ⏳ preload + reboot | ⏳ no | ⏳ yes | ⏳ unverified |
| Amazon Aurora | ⏳ yes, via `rds_superuser` | ⏳ preload + reboot | ⏳ no | ⏳ yes | ⏳ unverified |
| Google Cloud SQL | ⏳ yes, via `cloudsqlsuperuser` | ⏳ instance flag | ⏳ no | ⏳ yes | ⏳ unverified |
| Azure Flexible Server | ⏳ yes, via `azure_pg_admin` | ⏳ two server params + restart | ⏳ no | ⏳ yes | ⏳ unverified |
| Supabase | ⏳ limited (no superuser) | ⏳ preloaded | ⏳ **yes** (`:6543`) | ⏳ yes | ⏳ unverified |
| Neon | ⏳ limited (no superuser) | ⏳ preloaded | ⏳ **yes** (`-pooler` host) | ❌ **no** (scale-to-zero) | ⏳ unverified |

Legend: ✅ confirmed live · ⏳ expected, not yet verified · ❌ not supported / notable limitation.

pgbot detects the provider from the host, `version()` text, and provider-specific
`pg_settings` markers (`rds.*`, `cloudsql.*`, `azure.*`, `aurora_version()`), so
detection works even when the host is a bare IP or sits behind a proxy.

---

## Amazon RDS / Aurora

- **`pg_monitor`:** grant as the master user (`rds_superuser`): `GRANT pg_monitor TO <role>;`. RDS does not expose OS-superuser, but `pg_monitor` is fully grantable, which is all pgbot needs.
- **`pg_stat_statements`:** add `pg_stat_statements` to `shared_preload_libraries` in the **DB parameter group**, **reboot** the instance, then `CREATE EXTENSION pg_stat_statements;`. This is exactly the string pgbot prints on the degraded path.
- **Pooler:** the default endpoint is a direct connection. RDS Proxy is opt-in; when used it is a transaction pooler and pgbot will note it (rates stay correct).
- **Idle/stats:** always-on instance; cumulative stats persist normally.
- **Capability-gated:** `pg_stat_io` requires PG 16+; `pg_stat_wal` requires PG 14+. Aurora reports storage differently from community Postgres — WAL/IO sections may read differently and need live confirmation.

## Google Cloud SQL

- **`pg_monitor`:** grant via the `cloudsqlsuperuser` role: `GRANT pg_monitor TO <role>;`.
- **`pg_stat_statements`:** set the `cloudsql.enable_pg_stat_statements` instance flag, then `CREATE EXTENSION pg_stat_statements;`.
- **Pooler:** default endpoint is direct; the Cloud SQL Auth Proxy is a TCP proxy, not a statement pooler.
- **Idle/stats:** always-on; stats persist.

## Azure Database for PostgreSQL — Flexible Server

- **`pg_monitor`:** grant as a member of `azure_pg_admin`.
- **`pg_stat_statements`:** add `pg_stat_statements` to **both** the `azure.extensions` and `shared_preload_libraries` server parameters (a restart applies the latter), then `CREATE EXTENSION pg_stat_statements;`.
- **Pooler:** built-in PgBouncer is opt-in on a separate port; the default endpoint is direct.
- **Idle/stats:** always-on; stats persist.

## Supabase

- **`pg_monitor`:** Supabase does not grant superuser; `pg_monitor` availability to a custom role needs live confirmation. The default `postgres` role has broad but not unlimited access.
- **`pg_stat_statements`:** preloaded — just `CREATE EXTENSION pg_stat_statements;`.
- **Pooler:** the **default pooled connection string uses port `:6543`** (Supavisor / PgBouncer transaction mode). pgbot detects this endpoint and proceeds with a note; rates stay correct because each counter is sampled in its own transaction against cluster-wide shared memory. Use the direct `:5432` endpoint (`--strict-pooler` will insist on it) if you want session-scoped certainty.
- **Idle/stats:** paid tiers are always-on; free tier pauses after inactivity — treat the first run after a pause like a cold window (pgbot's T2 handling applies).

## Neon

- **`pg_monitor`:** no superuser; grantability to a custom role needs live confirmation.
- **`pg_stat_statements`:** preloaded — just `CREATE EXTENSION pg_stat_statements;`.
- **Pooler:** the pooled endpoint uses a **`-pooler` host suffix** (PgBouncer transaction mode). pgbot detects it and proceeds with a note.
- **Idle/stats:** **scale-to-zero.** After the compute suspends, cumulative statistics are discarded; the first run after a wake sees a near-zero stats window. This is the canonical case pgbot's cold-window detection (T2) exists for — counter-based findings are suppressed until the window exceeds 15 minutes, and the wait-event profile (ASH) may be the only usable signal.

---

## Verification checklist

For each provider, connect a real instance and confirm, replacing the ⏳ marks
above with ✅ or ❌ and recording the exact commands that worked:

- [ ] `pg_monitor` can be granted to a non-admin role, and by which admin role
- [ ] `pg_stat_statements` enable steps above are exact and sufficient
- [ ] whether the **default** connection string routes through a pooler (and which port/host)
- [ ] whether pgbot's pooler detection fires on that default endpoint
- [ ] stats retention across an idle/suspend cycle (scale-to-zero behaviour)
- [ ] which capability-gated sections (`pg_stat_io` PG16+, `pg_stat_wal` PG14+, replication) are unavailable and why
- [ ] paste the working, copy-pasteable setup commands

Run `pgbot inspect "<dsn>" --json | jq .server` to capture the detected provider,
version, and capabilities for the record.

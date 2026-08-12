# pgbot

**In-database observability for PostgreSQL.** One static binary connects
read-only, reads Postgres's own statistics views, and prints a findings-first
health report — plus what changed since last time. No agent, no external
service, no write privilege anywhere in the path.

```
curl -fsSL https://pgbot.dev/install | sh
pgbot inspect "postgres://pgbot_ro@host:5432/db"
```

```
pgbot · app · PostgreSQL 17.10 · 2026-08-12 10:00 UTC

2 finding(s) · 1 warning
  ⚠ 3 unused index(es) · 4.2 GiB
     These indexes have zero scans since stats began...
     → Reclaims 4.2 GiB. 1 is on a write-heavy table, where it also taxes every INSERT/UPDATE.
  · pg_stat_statements not enabled

HEALTH  sampled
  TPS 1.2k   cache hit 99.4%   connections 24   rollbacks 0.2%
  ▁▂▃▅▇  tps, recent runs

CHANGES since 09:14
  · queryid 4471  2.1 → 8.4  (+300%)  mean execution time per call
  · public.orders  1.3k → 5.0k  (+284%)  sequential scans surged
```

Why it's not just another stats reader: **pgbot remembers.** Every run writes a
local baseline, so from the third run on it can tell you *what changed and why
it matters* — a query that got slower, a table that started sequential-scanning,
an index that stopped being used.

## Install

| Method | Command |
|---|---|
| Script (verifies checksum) | `curl -fsSL https://pgbot.dev/install \| sh` |
| Go | `go install github.com/pgrundev/pgbot/cmd/pgbot@latest` |
| Docker | `docker run --rm ghcr.io/pgrundev/pgbot inspect "$DATABASE_URL"` |
| Homebrew | `brew install pgrundev/tap/pgbot` |

Some security teams won't pipe `curl` to `sh` — every alternative above installs
the same verified binary. Releases ship SHA256 checksums signed with cosign.

## Setup — a read-only role with `pg_monitor`

The read-only guarantee is **the role**, not a flag. Create a login role that
holds `pg_monitor` (so it can see the full statistics views) and has no write
grants:

```sql
CREATE ROLE pgbot_ro LOGIN PASSWORD '...';
GRANT pg_monitor TO pgbot_ro;
GRANT CONNECT ON DATABASE yourdb TO pgbot_ro;
```

Without `pg_monitor`, a non-superuser sees only its own sessions in
`pg_stat_activity` and can't read several views fully — pgbot detects this at
connect time and tells you exactly which GRANT to run rather than silently
reporting partial data.

pgbot additionally pins every session read-only (`default_transaction_read_only`,
`statement_timeout=15s`, `lock_timeout=2s`) and wraps each query in a
`BEGIN READ ONLY … ROLLBACK`. Those are defence in depth; the role is the boundary.

## Usage

```
pgbot inspect <connection-string>   # URL or libpq DSN, or set $DATABASE_URL
  --json                 emit the versioned, PII-free Context (the agent/script contract)
  --interval 1s          gap between the two counter samples (min 500ms)
  --no-store             don't read or write the local baseline
  --no-color             disable ANSI (also honors NO_COLOR and non-TTY)

pgbot baselines list                # what's stored locally, per database
pgbot baselines prune <fingerprint> # delete a database's snapshots
pgbot baselines export <fingerprint># dump stored snapshots as JSON
```

**Exit codes** (for CI): `0` clean · `1` warnings · `2` critical findings ·
`3` connection/execution failure.

The baseline store lives at `$XDG_STATE_HOME/pgbot/baselines.db` (7 days at full
resolution, hourly rollups to 90 days, 100 MB cap). It's yours — inspect and
delete it with `pgbot baselines`.

## What it collects

All from SQL — connections, cache-hit ratio, TPS and rollback ratio, WAL and IO
rates, checkpoints, locks and blocking chains, replication lag, top queries
(`pg_stat_statements`), table/index sizes, dead tuples and vacuum activity,
unused and missing indexes, and non-default settings. Counters
(`pg_stat_database`, `pg_stat_wal`, IO) are **double-sampled** to produce live
rates; the rest are point-in-time reads trended against the baseline.

Every section in `--json` carries an `exactness` label — `sampled`,
`cumulative`, `scraped`, or `unavailable` — so a consumer never mistakes a
cumulative total for a live rate.

## Version support

Collectors degrade rather than fail when a capability is absent:

| Feature | From | Fallback |
|---|---|---|
| `pg_stat_wal` (WAL rates) | PG 14 | section marked unavailable |
| `pg_stat_io` (buffers written) | PG 16 | `pg_stat_bgwriter` |
| `pg_stat_checkpointer` | PG 17 | `pg_stat_bgwriter` |
| `stats_fetch_consistency` | PG 15 | separate per-sample transactions |
| `pg_stat_statements` | extension | queries section unavailable + install hint |

Tested against PostgreSQL 13–18.

## Not in scope (yet)

Slice 1 is honest about its edges:

- **Host OS metrics** (CPU, disk IOPS, free memory) are **not** reachable over a
  SQL connection. On managed databases they live behind the provider's own API;
  on your own hardware, a future agent-on-host will read them.
- **No AI yet.** The deterministic findings here are the foundation; natural-
  language explanations (`pgbot ask` / `pgbot why`) come next, consuming the same
  `Context` + `Deltas`.
- **pgbot never writes.** It recommends indexes; it doesn't create them.

## Privacy

In agent/direct use, only the `--json` Context ever leaves the machine, and it
is PII-free by construction: `pg_stat_statements` text is normalized (`$1`
placeholders), and the one raw-SQL source (`pg_stat_activity` for blocking
chains) is scrubbed of string/numeric literals, emails, and UUIDs before it can
enter the Context. Connection strings are redacted in every log, error, and
output. This holds for a reader of the source, not just as a claim.

## License

Apache-2.0.

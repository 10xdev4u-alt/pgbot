# pgbot

**In-database observability for PostgreSQL.** One static binary connects
read-only, reads Postgres's own statistics views, and prints a findings-first
health report — plus what changed since last time. No agent, no external
service, no write privilege anywhere in the path.

![pgbot inspect — a read-only vital-signs read: headline gauges with a status, then the checks that came back clean](docs/img/dashboard.png)

```
curl -fsSL https://pgbot.dev/install | sh
pgbot inspect "postgres://pgbot_ro@host:5432/db"
```

```
connected · db.example.com · postgres 17.4 · read-only · 6h20m window

Database health: 82/100

CRITICAL
● transaction-id age 1.8B — 84% toward wraparound

WARNING
● orders queries 3.2× slower (8 → 26 ms mean)
● 3 unused indexes consume 18 GB
● connection usage reached 87%

GOOD
● cache hit ratio 99.4%
● replication healthy
● no deadlocks

Details: pgbot inspect --full   ·   Machine-readable: --json
Ask it: pgbot ask "what's wrong?"
```

The default report is a **graded read**: a health score, findings bucketed
CRITICAL / WARNING / NOTE, then a GOOD list naming the healthy subsystems with
their values (a tool that names what it verified reads like a colleague who
looked, not an alarm). `pgbot inspect --full` adds a subsystem status board plus
the section tables and per-finding caveats; `pgbot indexes` drills into zero-scan
indexes; `pgbot ask "…"` and `pgbot explain` put a plain-language AI reading on
top of the same findings. `--json` is the complete, versioned contract for agents
and scripts.

```
$ pgbot ask "what's wrong?"

Your database is mostly healthy.

1 critical issue:
orders queries became 3.2× slower in the last 6 hours.

Likely cause:
sequential scans increased after the orders table grew 18%.

Recommended:
review an index on customer_id + created_at.
```

Why it's not just another stats reader: **pgbot remembers.** Every run writes a
local baseline, so from the third run on it can tell you *what changed and why
it matters* — a query that got slower, a table that started sequential-scanning,
an index that stopped being used.

## See it

**`pgbot inspect --full`** — a subsystem status board (one row per subsystem,
colored ok / warn / fail), followed by the detailed section tables.

![pgbot inspect --full — a box-drawing subsystem status board](docs/img/full.png)

**`pgbot indexes`** — zero-scan indexes with sizes, and the caveat that matters:
on a primary those scan counts are per-node, so a replica may still be using an
index that looks unused here. It tells you what *not* to drop.

![pgbot indexes — zero-scan indexes and what not to drop](docs/img/indexes.png)

**`pgbot queries`** — the top statements from `pg_stat_statements`, ranked by
total execution time (the query quietly eating your database) with a `share`
column for each query's slice of total time. Add `--by-calls` to rank by call
count instead — a cheap query run a million times can outweigh an expensive one
run twice. Transaction-control and session-`SET` noise is filtered out.

```
$ pgbot queries "$DATABASE_URL"
  total  share  calls  mean       query
  4h11m  61.0%  812.4k 18.55 ms   SELECT * FROM orders WHERE user_id = $1 AND …
  22m3s  17.8%  1.3k   1.02 s     SELECT count(*) FROM events WHERE created_at …
  15m2s  12.0%  99.8k  9.04 ms    INSERT INTO audit_log (actor, action, …) VAL …
```

**`pgbot vacuum`** — autovacuum health per table: dead tuples, dead-tuple ratio,
when autovacuum last ran, and a computed `due?` — whether the table's dead tuples
have passed Postgres' default autovacuum trigger (`50 + 20%` of live rows). Rising
dead tuples with `due? yes` and no recent run is autovacuum falling behind, the
early signal for bloat and, eventually, wraparound risk.

```
$ pgbot vacuum "$DATABASE_URL"
  table               live   dead   dead%  last autovacuum  due?
  public.demo_events  42.9k  33.8k  44.1%  4m ago           yes
  public.churny       5.0k   10.0k  66.7%  never            yes
```

**`pgbot ask "why is it slow?"`** — a plain-language reading of the *same*
deterministic findings. It leads with the lock contention and refuses to
recommend dropping the indexes because replication is active — the caveat is
carried into the advice, not lost.

![pgbot ask — an AI reading of pgbot's findings, with caveats carried](docs/img/ask.png)

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
`statement_timeout=15s`, `lock_timeout=2s`) and wraps each query in its own
`BEGIN READ ONLY … COMMIT`. It **commits** those read-only probes rather than
rolling them back — a read-only transaction writes nothing either way, but a
rollback would inflate the `xact_rollback` counter pgbot itself reports. Those
are defence in depth; the role is the boundary.

## Connecting to managed providers

pgbot is a **client** — it connects over the Postgres wire protocol like `psql`.
You never install anything on the database; run pgbot from your laptop, a bastion,
CI, or an instance in the same network. Grant `pg_monitor` to your role (above)
and connect. Provider-specific notes:

### AWS RDS / Aurora

You can't install on the RDS/Aurora instance itself — it's managed, no OS access.
Run pgbot from a **client that can reach it**:

- **Private RDS (recommended for prod):** run pgbot from a small **EC2 in the same
  VPC**. It reaches the private endpoint over AWS's internal network — no public
  access, no SSH tunnel, no IP allow-listing. The only rule is the RDS security
  group allowing `5432` from the EC2's security group.
- **Publicly accessible RDS:** allow your IP in the RDS security group and connect
  straight from your laptop.

```bash
# on the EC2 (or your laptop for a public instance):
curl -fsSL https://pgbot.dev/install | sh
pgbot inspect "postgres://pgbot_ro@mydb.abc123.us-east-1.rds.amazonaws.com:5432/appdb?sslmode=require"
```
Grant `pg_monitor` as the master (`rds_superuser`) role. **Caveat:** host metrics
(CPU / memory / disk IOPS) live in CloudWatch, not Postgres, so they're out of
reach over a connection string — everything else works.

### Neon

```bash
pgbot inspect "postgres://user:pass@ep-xxx.region.aws.neon.tech/dbname?sslmode=require"
```
- The **pooled** endpoint has a `-pooler` host suffix (transaction mode). pgbot
  detects it and proceeds — rates stay correct — or use the direct (non-pooler)
  host for session-scoped certainty.
- Neon's default string ships `channel_binding=require`; pgbot **ignores it
  automatically** (the driver can't do channel binding; TLS from `sslmode` still
  applies) instead of erroring.
- `pg_stat_statements` is preloaded — just `CREATE EXTENSION pg_stat_statements;`.
- **Scale-to-zero:** after idle, Neon suspends the compute and discards stats. The
  first run after a wake is a *cold window* — pgbot suppresses counter-based
  findings until the window is old enough, so a reset never reads as a −99% regression.

### Supabase

```bash
# direct endpoint (session-scoped, best for pgbot):
pgbot inspect "postgres://postgres:pass@db.<ref>.supabase.co:5432/postgres?sslmode=require"
# or the pooled endpoint (:6543, transaction mode) — pgbot notes it and proceeds:
pgbot inspect "postgres://postgres.<ref>:pass@aws-0-<region>.pooler.supabase.com:6543/postgres?sslmode=require"
```
- The default pooled connection string uses port **`:6543`** (Supavisor, transaction
  mode). pgbot detects the pooler and proceeds with a note; prefer the direct
  `:5432` endpoint when you can.
- `pg_stat_statements` is preloaded — `CREATE EXTENSION pg_stat_statements;`.
- Supabase doesn't hand out superuser; the built-in `postgres` role already has
  broad read access, or grant `pg_monitor` to a dedicated role where allowed.

### Postgres in Docker

The connection string depends on **where pgbot runs relative to the container.**

**pgbot on the host, container with a published port.** Read the `PORTS` column of
`docker ps` — `0.0.0.0:6433->5432/tcp` means host port `6433` maps to the
container's `5432`. Connect to the **host** port:

```bash
docker port mypg 5432                    # → 0.0.0.0:6433  (find the host port)
pgbot inspect "postgres://postgres:pw@127.0.0.1:6433/postgres?sslmode=disable"
```

Use `127.0.0.1`, not `localhost`: `localhost` resolves to IPv6 (`::1`) first, which
Docker Desktop doesn't forward, so the connect stalls ~10s before falling back to
IPv4. Local containers usually have no TLS → `sslmode=disable`. Find the
credentials with `docker exec mypg env | grep POSTGRES`.

**pgbot as a container reaching a DB container.** `localhost` would mean pgbot's
own container — join the DB's network and use the **container name** + internal
port `5432`:

```bash
docker run --rm --network <that-network> ghcr.io/pgrundev/pgbot \
  inspect "postgres://postgres:pw@mypg:5432/postgres?sslmode=disable"
```

**pgbot as a container reaching a DB on the host.** Use `host.docker.internal`
(add `--add-host=host.docker.internal:host-gateway` on Linux).

> Rule of thumb: same-network containers address each other by **container name +
> internal port `5432`**; the host reaches a container by **`127.0.0.1` + the
> published host port**. A container with no `->` mapping in `docker ps` isn't
> reachable from the host at all — publish it with `-p`, or connect from inside
> its network.

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

pgbot indexes <connection-string>   # zero-scan indexes + what NOT to drop
pgbot queries <connection-string>   # top pg_stat_statements by total time (--by-calls to re-rank)
pgbot vacuum <connection-string>    # autovacuum health per table — dead tuples + whether it's due
pgbot tune <connection-string>      # config-tuning recommendations from the workload
pgbot explain <connection-string>   # inspect, then have an AI explain the findings
pgbot ask "why is it slow?"         # AI answer grounded on the findings ($DATABASE_URL)
  --yes                  skip the "this sends data to Google" confirmation
pgbot mcp                           # run as an MCP server over stdio (for AI agents)
```

### MCP — use pgbot as an agent tool

`pgbot mcp` speaks the [Model Context Protocol](https://modelcontextprotocol.io)
on stdio, so an AI agent can call pgbot as a read-only tool. It exposes
**deterministic** tools only — `inspect` (full findings as JSON), `unused_indexes`,
`top_queries` (pg_stat_statements ranked by total time, with each query's share),
and `vacuum_health` (autovacuum health per table, with a computed "due" flag) —
and lets the *connected model* do the explaining. Every tool is read-only,
returns a stable JSON shape, and never exposes a raw connection string or query
literals to the model. No Gemini key involved: the agent reasons over the same
findings the CLI computes.

Add it to any MCP client (Claude Desktop/Code, Cursor, …):

```json
{
  "mcpServers": {
    "pgbot": {
      "command": "pgbot",
      "args": ["mcp"],
      "env": { "DATABASE_URL": "postgres://pgbot_ro@host:5432/db" }
    }
  }
}
```

With `DATABASE_URL` set, the agent calls `inspect` with no arguments; or it can
pass `connection_string` per call to reach several databases. pgbot never writes,
so there's nothing an agent can break through it.

It also exposes a **`diagnose` prompt** (a one-click "inspect and give me a
prioritized diagnosis" workflow) and a **`pgbot://baselines` resource** (the
databases pgbot has local history for) — so tools, prompts, and resources are all
available to the agent.

### `explain` — optional AI layer

`pgbot explain` runs the exact same read-only inspection, prints the
deterministic report unchanged, then asks a model to **explain and prioritize**
the findings in plain language. The findings are still computed locally in Go —
the model only interprets them, it never invents them, and it's instructed to
carry every caveat into any recommendation. The AI text is printed below a
labeled rule (`🤖 generated by … — verify before acting`); if the model errors
or the key is unset, the deterministic report still stands.

This is the **only** command that sends data off the machine — the same PII-free
Context you can see with `inspect --json`. It works with **OpenAI or Google
Gemini**, and the key is always read from the environment (never a flag). pgbot
picks the provider automatically: `OPENAI_API_KEY` → OpenAI, `GEMINI_API_KEY` (or
`GOOGLE_API_KEY`) → Gemini. Set `PGBOT_AI_PROVIDER=openai|gemini` to force one when
both are present.

```
# OpenAI
export OPENAI_API_KEY=sk-…
pgbot explain "$DATABASE_URL"          # gpt-4o-mini by default

# …or Google Gemini
export GEMINI_API_KEY=…                # from Google AI Studio
pgbot explain "$DATABASE_URL"
```

Override the model or endpoint per provider: `PGBOT_OPENAI_MODEL` /
`PGBOT_OPENAI_URL` (any OpenAI-compatible endpoint works — Azure OpenAI,
OpenRouter, a local server) and `PGBOT_GEMINI_MODEL` / `PGBOT_GEMINI_URL`.

**Exit codes** (for CI): `0` clean · `1` warnings · `2` critical findings ·
`3` connection/execution failure.

> **Local Docker gotcha:** with a database in Docker Desktop, connect via
> `127.0.0.1`, not `localhost`. `localhost` resolves to IPv6 (`::1`) first, which
> Docker Desktop doesn't forward, so the connect stalls for ~10s before falling
> back to IPv4. Managed hosts (RDS, Supabase, Neon…) aren't affected.

The baseline store lives at `$XDG_STATE_HOME/pgbot/baselines.db` (7 days at full
resolution, hourly rollups to 90 days, 100 MB cap). It's yours — inspect and
delete it with `pgbot baselines`.

## What it collects

All from SQL — connections, cache-hit ratio, TPS and rollback ratio, WAL and IO
rates, checkpoints, locks and blocking chains, replication lag, replication-slot
WAL retention and logical-subscription health, top queries
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

### Managed providers

pgbot detects the platform (RDS, Aurora, Cloud SQL, Azure Flexible Server,
Supabase, Neon) and prints the provider-specific steps to enable
`pg_stat_statements` when it's missing. Supabase (`:6543`) and Neon (`-pooler`)
default to a pooled endpoint, which pgbot notes without degrading its rates;
Neon's scale-to-zero discards stats, which pgbot handles as a cold window. Full
per-provider notes and the live-verification checklist are in
[`docs/providers.md`](docs/providers.md).

## Serverless Postgres (Neon, scale-to-zero)

Scale-to-zero databases (Neon, Databricks Lakebase, and similar) **discard
in-memory statistics when the compute suspends** — by default after ~5 minutes
idle. After each wake, `pg_stat_statements` history, cache-hit counters and
index-scan counts all start again from zero.

pgbot detects this and **degrades rather than lies**:

- If the statistics were reset (or the server restarted) since the last run, the
  entire `deltas` section is suppressed with a reason — a counter going from 40M
  to 12k is a wake, not a −99.97% change.
- On a cold window (younger than 15 minutes), counter-based findings — unused
  indexes, cache-hit, sequential-scan-heavy — are suppressed, because they'd be
  meaningless or actively dangerous. Gauges (blocking chains, idle-in-transaction,
  replication lag, invalid indexes) are valid immediately and still reported.
- The report header states the window age plainly.

If you want continuous history, disable scale-to-zero or raise the suspend
timeout so the statistics survive between runs.

## Not in scope (yet)

Slice 1 is honest about its edges:

- **Host OS metrics** (CPU, disk IOPS, free memory) are **not** reachable over a
  SQL connection. On managed databases they live behind the provider's own API;
  on your own hardware, a future agent-on-host will read them.
- **AI is optional and explain-only.** `pgbot explain` can put a plain-language
  explanation on top of the findings (see above), but the findings themselves are
  always computed deterministically in Go — no model ever generates one. Deeper
  correlation (`pgbot why`) is still future work.
- **pgbot never writes.** It recommends indexes; it doesn't create them.

## Privacy

Nothing leaves the machine unless you ask for it: `inspect` and its `--json` are
entirely local. The one command that makes an outbound call is `pgbot explain`,
which sends the same PII-free Context to your configured model (and says so, with
a confirmation prompt).

That Context is PII-free by construction: `pg_stat_statements` text is normalized
(`$1` placeholders), and the one raw-SQL source (`pg_stat_activity` for blocking
chains) is scrubbed of string/numeric literals, emails, and UUIDs before it can
enter the Context. Connection strings are redacted in every log, error, and
output. This holds for a reader of the source, not just as a claim.

## License

Apache-2.0.

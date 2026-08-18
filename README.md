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
the section tables and per-finding caveats; focused commands (`indexes`,
`queries`, `tables`, `vacuum`) each drill into one signal; `pgbot ask "…"` and
`pgbot explain` put a plain-language AI reading on top of the same findings.
`--json` is the complete, versioned contract for agents and scripts.

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

**`pgbot tables`** — the largest tables by total size (heap + indexes + TOAST),
each with row count, dead-tuple ratio, and sequential-vs-index scan counts. It's
storage accounting *and* a missing-index radar: a large table with heavy `seq
scans` and few `idx scans` is a likely index candidate.

```
$ pgbot tables "$DATABASE_URL"
  size      rows   dead%  seq scans  idx scans  table
  38.7 GiB  19.7M  8.3%   1.5k       112.3M     public.performance_events
  20.0 GiB  1.3M   10.8%  2.5M       121.6M     public.events        ← 2.5M seq scans
  7.1 GiB   5.6M   0.0%   5.0k       46.6M      public.log_entries
```

**`pgbot ask "why is it slow?"`** — a plain-language reading of the *same*
deterministic findings. It leads with the lock contention and refuses to
recommend dropping the indexes because replication is active — the caveat is
carried into the advice, not lost.

![pgbot ask — an AI reading of pgbot's findings, with caveats carried](docs/img/ask.png)

## Install

| Method | Command |
|---|---|
| npx (no install) | `npx pgbot inspect "$DATABASE_URL"` |
| Script (cosign signature + checksum) | `curl -fsSL https://pgbot.dev/install \| sh` |
| Homebrew | `brew install pgrundev/tap/pgbot` |
| Go | `go install github.com/pgrundev/pgbot/cmd/pgbot@latest` |
| Docker | `docker run --rm ghcr.io/pgrundev/pgbot inspect "$DATABASE_URL"` |
| Windows / manual | download the archive for your OS/arch from [Releases](https://github.com/pgrundev/pgbot/releases) (Linux/macOS `.tar.gz`, Windows `.zip`) |

`npx pgbot` fetches the prebuilt binary for your platform from npm (shipped as an
`optionalDependency`, so only the matching one installs) and runs it — nothing to
install, works with `npm ci --ignore-scripts`.

**What each path verifies.** npm is the *convenient* path: the packages carry
registry integrity hashes and npm **provenance**, a verifiable link to the GitHub
Actions workflow that built them — that attests *where* the package came from, not
that the artifact was signed. `install.sh` is the *verified* path: releases ship
SHA256 checksums signed with **cosign** (keyless, via GitHub Actions OIDC), and the
script verifies that signature when `cosign` is on your `PATH` and always verifies
the checksum. For the strongest guarantee, require the signature:

```bash
PGBOT_REQUIRE_SIGNATURE=1 curl -fsSL https://pgbot.dev/install | sh
```

`PGBOT_REQUIRE_SIGNATURE=1` hard-fails if `cosign` is missing or the check doesn't
pass. To verify a release by hand:

```bash
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github.com/pgrundev/pgbot/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```

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

## Point pgbot at your database

Pass the connection string as an argument — a URL or a libpq DSN:

```bash
pgbot inspect "postgres://pgbot_ro:secret@host:5432/db?sslmode=require"

# the libpq keyword/value DSN form works too:
pgbot inspect "host=host port=5432 dbname=db user=pgbot_ro sslmode=require"
```

Or set it once in the environment and omit the argument — convenient for a shell
session or CI, and it keeps the password out of your shell history and `ps`
output:

```bash
export DATABASE_URL="postgres://pgbot_ro:secret@host:5432/db?sslmode=require"

pgbot inspect
pgbot queries     # every command takes the connection the same way
pgbot diff --since 24h
```

pgbot resolves the connection in this order: the argument first, then
`$DATABASE_URL`, then `$PGBOT_DATABASE_URL`. Add `?sslmode=require` (or stricter)
for any database reached over a network.

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
pgbot tables  <connection-string>   # largest tables + row counts + seq-vs-index scan pattern
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
**deterministic** tools only and lets the *connected model* do the explaining:

- `inspect` — full findings as JSON
- `unused_indexes`, `top_queries`, `vacuum_health` — the CLI's focused views
- `suggest_indexes` — planner-validated index recommendations (hypopg)
- `explain_plan` — the planner's plan for a SELECT (plain EXPLAIN, never executed)
- `schema_of` — a table's columns/indexes/constraints + row estimate, **no data**
- `compare_to_baseline` — the `diff`, with its interval-honesty and reset caveats
- `explain_finding` — pgbot's catalogue page for a finding, so the agent explains
  a recommendation in pgbot's words instead of inventing them

Every tool is read-only, returns a stable JSON shape carrying its `exactness`
label, honors `.pgbot.toml` suppression, and never exposes a raw connection
string or query literals to the model. The agent reasons over the same findings
the CLI computes.

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

**Pair it with the skill.** MCP gives the agent the *tools*; the
[`postgres-diagnostics` skill](skills/postgres-diagnostics/SKILL.md) gives it the
*playbook* — respect caveats, never `EXPLAIN ANALYZE`, prioritize by impact,
never write. Drop it in `~/.claude/skills/` (see [`skills/`](skills/)) and your
agent asks the right pgbot command and reads the results the way pgbot intends.

### Claude Code plugin

[Claude Code](https://claude.com/claude-code) users can install the tools, the
skill, and the commands in one shot — the repo is its own plugin marketplace:

```bash
claude plugin marketplace add pgrundev/pgbot
claude plugin install pgbot@pgbot
```

That registers the pgbot **MCP tools**, the **`postgres-diagnostics` skill**, and
three slash commands — **`/pg-health`**, **`/pg-slow`**, **`/pg-indexes`** — each
of which carries the pgbot judgment (caveats intact, impact-first, never writes).
The plugin drives the `pgbot` binary, so install that first (`curl -fsSL
https://pgbot.dev/install | sh`); set `DATABASE_URL` or pass a connection string
per call, then ask *"is my Postgres healthy?"*

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

**Exit codes** (a stable contract for CI): `0` clean · `1` warnings · `2` critical
findings · `3` connection/execution failure · `64` usage error (bad flags/args).
Suppressed findings never contribute to the exit code.

### `advise` — index suggestions the planner validates

`pgbot advise` finds missing indexes without guessing. It reads the slowest
queries from `pg_stat_statements`, derives candidate indexes from the planner's
own sequential-scan filters (deterministically, in Go — never an LLM), and then
**validates each one**: it creates the index *hypothetically* with
[hypopg](https://github.com/HypoPG/hypopg), re-plans the query, and only reports
it if the planner actually switches to it and the estimated cost drops.

```
$ pgbot advise "$DATABASE_URL"
index advisor · app · postgres 17 · hypopg validation — nothing was built

1 validated recommendation(s):

⚑ public.orders
  CREATE INDEX ON public.orders (customer_id, status);
  helps: SELECT count(*) FROM orders WHERE customer_id = $1 AND status = $2
         60 calls · 68% of DB time
  planner confirmed: cost 4653 → 4.1 (−99.9%)
  ↳ nothing was created. Review, then build off-peak: add CONCURRENTLY.
```

Nothing is ever built — the hypothetical indexes live in backend memory and are
discarded. Everything runs in a READ ONLY transaction; pgbot only *plans* your
query (`EXPLAIN (GENERIC_PLAN)`), it never executes it and never uses the
executing form of EXPLAIN. Requires **hypopg**, **pg_stat_statements**, and
**PostgreSQL 16+**; when any is missing it prints exactly what to enable and does
nothing else. `--json` gives structured recommendations for agents (also exposed
as the MCP `suggest_indexes` tool).

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

### Supported versions

| Tier | Versions | In CI |
|---|---|---|
| **Supported** | PostgreSQL 16, 17, 18 | every PR + push |
| **Best-effort** | PostgreSQL 14, 15 | every PR + push |
| Unsupported | PostgreSQL 13 and older | — (13 is [end-of-life](https://www.postgresql.org/support/versioning/)) |

New features may target 16+ without a backward path. Everything degrades rather
than errors on an older or capability-limited server (see the table above).

### Managed providers

pgbot detects the platform (RDS, Aurora, Cloud SQL, Azure Flexible Server,
Supabase, Neon) and prints the provider-specific steps to enable
`pg_stat_statements` when it's missing. Supabase (`:6543`) and Neon (`-pooler`)
default to a pooled endpoint, which pgbot notes without degrading its rates;
Neon's scale-to-zero discards stats, which pgbot handles as a cold window. Full
per-provider notes and the live-verification checklist are in
[`docs/providers.md`](docs/providers.md).

## Configuration & suppression

An optional `.pgbot.toml` (committed to your repo) overrides thresholds, remaps a
finding's severity, and suppresses specific findings so noise never trains people
to ignore the severity column:

```toml
schema = 1
[severity]
checksums_disabled = "info"        # can't change it on this managed provider
[[ignore]]
finding = "unused_indexes"
object  = "public.idx_legacy_*"    # glob; omit to mute the whole finding
reason  = "backs the quarterly export"
expires = "2026-12-31"
```

Suppression is always **visible** — suppressed findings stay in `--json` (with
`suppressed`/`suppression_reason`), never affect the exit code, and a suppressed
**critical still renders** (a config must not hide `checksum_failures`). pgbot
refuses to read any credential-shaped key from the file, flags rules that have
gone stale, and ships `pgbot config check` / `explain` / `init`. Full contract —
including the per-finding object-identity table — in
[`docs/configuration.md`](docs/configuration.md).

### `diff` — what changed since last time

`pgbot diff [--since 24h]` compares the two most relevant baseline snapshots from
the local store — no connection needed. It's honest about what it compared:

```
$ pgbot diff --since 24h
diff · prod · a1b2c3d4e5f6
2026-08-16 09:00 → 2026-08-17 16:00  ·  31h elapsed
note: you asked for ~24h back, but the nearest older snapshot is 31h back — comparing that.
```

It prints the interval it *actually* used (the nearest snapshot to `--since`, not
a silent substitution), warns up front when a **stats reset** or
**pg_stat_statements eviction** between the snapshots makes specific deltas
untrustworthy, and refuses to compare two different databases (pass
`--fingerprint` when the store holds more than one).

> **Whole cluster:** `pgbot inspect "$DATABASE_URL" --all-databases` inspects every
> connectable database on the server. Cluster-wide findings (settings, replication,
> archiving, wraparound) are reported once; per-database findings appear per
> database. Serial by default (`--parallel N` to fan out).

## CI integration

pgbot is built to run in a pipeline. `--fail-on` decouples the exit code from the
default severity map, and `--format` emits machine-readable reports:

```bash
pgbot inspect "$DATABASE_URL" --fail-on=critical --format=sarif > pgbot.sarif
```

`--format=sarif` produces [SARIF 2.1.0](https://sarifweb.azurewebsites.net/);
upload it with `github/codeql-action/upload-sarif` and every finding lands in your
repo's **Security** tab, linked to its catalogue page. `--format=junit` feeds
Jenkins/GitLab test panes. Suppressed findings stay visible (a SARIF suppression /
a JUnit `skipped`) and never affect the exit code.

### GitHub Action

```yaml
- uses: pgrundev/pgbot@v1
  with:
    dsn: ${{ secrets.PGBOT_DSN }}
    fail-on: critical
```

That runs the check and uploads SARIF to the Security tab. **The DSN must be a
`pg_monitor` role with no data access — never a superuser.** Create one:

```sql
CREATE ROLE pgbot_ci LOGIN PASSWORD '…';
GRANT pg_monitor TO pgbot_ci;
GRANT CONNECT ON DATABASE yourdb TO pgbot_ci;
```

`pg_monitor` grants read access to the statistics views pgbot needs and nothing
else — no table data. The job that uploads SARIF needs `security-events: write`.

### Prometheus

`--format=prometheus` writes the [node_exporter textfile
format](https://github.com/prometheus/node_exporter#textfile-collector): every
finding as a `pgbot_finding{id,severity,dimension,object}` series plus the gauges
behind them (`pgbot_cache_hit_ratio`, `pgbot_xid_age`, `pgbot_connections_used`,
`pgbot_replica_lag_seconds`, …), so an alert can fire on a trend before a finding
crosses its threshold. Suppressed findings are exported with `suppressed="true"`,
not dropped — a muted config stays visible in your metrics.

pgbot has **no daemon** — that is deliberate. Point it at a textfile collector on a
cron or systemd timer:

```bash
pgbot inspect "$DATABASE_URL" --format=prometheus > /var/lib/node_exporter/pgbot.prom.$$
mv /var/lib/node_exporter/pgbot.prom.$$ /var/lib/node_exporter/pgbot.prom   # atomic
```

Under `--all-databases`, each database's series carry a `database="…"` label.

## The findings catalogue

Every finding pgbot emits has a reference page — what it observed, why it matters,
a **read-only query to verify it yourself**, how to fix it, when to ignore it (with
a pasteable `[[ignore]]` block), and what pgbot cannot see. Browse them by symptom
in [`docs/findings/`](docs/findings/README.md), or read one offline straight from
the binary:

```
pgbot explain-finding low_hot_update_ratio
```

Every line of a report tells you its id, so `pgbot explain-finding <id>` always
has the page.

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

Nothing leaves the machine unless you ask for it: every command except the AI
layer is entirely local. The only commands that make an outbound call are `pgbot
explain` and `pgbot ask`, which send the same PII-free Context to your configured
model — OpenAI or Gemini (and say so, with a confirmation prompt).

That Context is PII-free by construction: `pg_stat_statements` text is normalized
(`$1` placeholders), and the one raw-SQL source (`pg_stat_activity` for blocking
chains) is scrubbed of string/numeric literals, emails, and UUIDs before it can
enter the Context. Connection strings are redacted in every log, error, and
output. This holds for a reader of the source, not just as a claim.

## License

Apache-2.0.

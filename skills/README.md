# pgbot skills

Agent **skills** that teach an AI assistant how to use pgbot *well* — the
judgment layer on top of the tools: respect caveats, never execute a user's
query to diagnose it, prioritize by impact, never write to the database.

A skill pairs naturally with the pgbot **MCP server** (`pgbot mcp`): MCP gives the
agent the read-only tools, the skill gives it the playbook.

## `postgres-diagnostics`

Turns "why is my database slow / what should I optimize / which indexes can I
drop" into a prioritized, caveat-aware plan backed by pgbot's deterministic
findings.

### Install

Works in **Claude Code, Cursor, and Codex** (and any Skills-aware agent). The
[`skills` CLI](https://github.com/vercel-labs/skills) detects which agents you
use and installs to the right place for each:

```bash
npx skills add pgrundev/pgbot
```

Or the pgbot-style one-liner (Claude Code / Skills-aware clients):

```bash
curl -fsSL https://pgbot.dev/skill | sh
```

It drops the skill at `~/.claude/skills/postgres-diagnostics/SKILL.md`
(override the target with `PGBOT_SKILL_DIR`, e.g. `.claude/skills` for a
project-scoped install). Or fetch the raw file directly:

```bash
mkdir -p ~/.claude/skills/postgres-diagnostics
curl -fsSL https://raw.githubusercontent.com/pgrundev/pgbot/main/skills/postgres-diagnostics/SKILL.md \
  -o ~/.claude/skills/postgres-diagnostics/SKILL.md
```

Pair it with the MCP server so the agent has both the tools and the playbook:

```bash
claude mcp add pgbot -e DATABASE_URL="postgres://pgbot_ro:…@host:5432/db?sslmode=require" -- pgbot mcp
```

Then ask your agent *"is my Postgres healthy?"* — it invokes the skill, calls
pgbot's tools, and gives you a prioritized diagnosis with the caveats intact.

> The `SKILL.md` format is a small frontmatter block (`name`, `description`)
> followed by the instructions. `description` is what the agent matches on to
> decide the skill is relevant, so it names the situations pgbot is for.

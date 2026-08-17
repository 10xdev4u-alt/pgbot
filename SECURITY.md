# Security policy

pgbot connects to production PostgreSQL databases. We take reports seriously.

## Reporting a vulnerability

**Please do not open a public issue for a security problem.** Instead, use GitHub's
private vulnerability reporting: on the repository, go to **Security → Report a
vulnerability**. That opens a private advisory only maintainers can see.

Include what you found, how to reproduce it, and the impact. We'll acknowledge
within a few days and keep you updated through the advisory.

## Scope worth a report

pgbot's security posture is that it is **read-only** and **never executes an
inspected query**. A report that breaks either invariant is high priority:

- Any path where pgbot writes to, or executes a statement against, the target
  database (it pins `default_transaction_read_only`, runs everything in
  `BEGIN READ ONLY`, and only ever `EXPLAIN`s — never `EXPLAIN ANALYZE`).
- PII leaking into a `model.Context`, `--json`, the baseline store, or the AI
  payload (query text is scrubbed; connection strings are redacted).
- A credential read from `.pgbot.toml` (it refuses credential-shaped keys).
- A dependency vulnerability `govulncheck` should have caught.

## Supported versions

Fixes land on the latest release. See the version-support table in the README for
which PostgreSQL versions are supported.

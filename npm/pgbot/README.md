# pgbot

**In-database observability for PostgreSQL.** One static binary connects
read-only, reads Postgres's own statistics views, and prints a findings-first
health report.

```bash
npx pgbot inspect "postgres://pgbot_ro@host:5432/db"
```

No prior install. `npx` downloads the wrapper and the prebuilt binary for your
platform (shipped as an `optionalDependency`, so npm installs only the one that
matches your OS/CPU) and runs it. Point it at your database with an argument or
`$DATABASE_URL`; use a role holding `pg_monitor` with no write grants.

## What npm verifies (and what it doesn't)

The npm packages carry registry integrity hashes and npm **provenance** — a
verifiable link to the GitHub Actions workflow that built them. That is a
*different and weaker* guarantee than the release's **cosign** signature: it
attests where the package came from, not that the artifact was signed.

For the verified path, install the binary with the script and require the
signature:

```bash
PGBOT_REQUIRE_SIGNATURE=1 curl -fsSL https://pgbot.dev/install | sh
```

Full documentation: <https://github.com/pgrundev/pgbot#readme>

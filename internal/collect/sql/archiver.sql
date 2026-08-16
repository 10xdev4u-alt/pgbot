-- WAL archiving health from pg_stat_archiver (cluster-wide, one row). A silently
-- failing archive_command produces no client error and no symptom until a restore
-- is attempted — the canonical "we had no backups for six weeks" incident.
-- has_archive_command records only WHETHER archive_command / archive_library is
-- set — never the VALUE, which routinely carries bucket paths, tokens, credentials.
SELECT archived_count,
       coalesce(last_archived_wal, '') AS last_archived_wal,
       last_archived_time,
       failed_count,
       coalesce(last_failed_wal, '')   AS last_failed_wal,
       last_failed_time,
       stats_reset,
       (current_setting('archive_command') <> ''
          OR coalesce(current_setting('archive_library', true), '') <> '') AS has_archive_command
FROM pg_stat_archiver;

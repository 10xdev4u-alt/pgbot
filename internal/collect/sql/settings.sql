-- Two sets in one scan:
--   'override' — parameters set away from their compiled-in default (human-set knobs).
--   'tuning'   — a fixed whitelist of tuning-relevant parameters, ALWAYS, with their
--                display values, so the tuning rules can name the current setting.
SELECT name, setting AS value, 'override' AS kind
FROM pg_settings
WHERE setting IS DISTINCT FROM boot_val
UNION ALL
SELECT name, current_setting(name) AS value, 'tuning' AS kind
FROM pg_settings
WHERE name IN (
  'work_mem', 'maintenance_work_mem', 'max_wal_size', 'max_connections',
  'shared_buffers', 'effective_cache_size', 'autovacuum_max_workers',
  'autovacuum_vacuum_scale_factor', 'random_page_cost', 'track_io_timing',
  'fsync', 'full_page_writes', 'autovacuum', 'statement_timeout',
  'archive_mode', 'archive_timeout', 'wal_level'
)
ORDER BY 1;

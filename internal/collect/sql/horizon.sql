-- What pins the vacuum/xmin horizon: dead tuples cannot be reclaimed past the
-- oldest xmin held by (1) a long-open transaction, (2) a replica's
-- hot_standby_feedback (arrives as backend_xmin on the primary), (3) a
-- replication slot's xmin / catalog_xmin, or (4) a prepared (2PC) transaction
-- (invisible in pg_stat_activity). One row per candidate holder, with the age of
-- its xmin in transactions. NO query text (PII) — the holder is named by
-- pid/slot/gid/client only; the human can look up the pid.
SELECT source, holder, xmin_age::bigint AS xmin_age, detail
FROM (
  SELECT 'backend'            AS source,
         pid::text            AS holder,
         age(backend_xmin)    AS xmin_age,
         btrim(coalesce(state, '') ||
           CASE WHEN xact_start IS NOT NULL
                THEN ', xact ' || round(extract(epoch FROM now() - xact_start))::text || 's'
                ELSE '' END, ', ') AS detail
  FROM pg_stat_activity
  WHERE backend_xmin IS NOT NULL AND pid <> pg_backend_pid()

  UNION ALL
  SELECT 'replication_slot', slot_name,
         greatest(coalesce(age(xmin), 0), coalesce(age(catalog_xmin), 0)),
         CASE WHEN active THEN 'active' ELSE 'inactive' END
  FROM pg_replication_slots
  WHERE xmin IS NOT NULL OR catalog_xmin IS NOT NULL

  UNION ALL
  SELECT 'standby_feedback',
         coalesce(host(client_addr), application_name, 'standby'),
         age(backend_xmin),
         'hot_standby_feedback'
  FROM pg_stat_replication
  WHERE backend_xmin IS NOT NULL

  UNION ALL
  SELECT 'prepared_xact', gid, age("transaction"),
         'prepared ' || round(extract(epoch FROM now() - prepared))::text || 's ago'
  FROM pg_prepared_xacts
) h
WHERE xmin_age IS NOT NULL AND xmin_age > 0
ORDER BY xmin_age DESC
LIMIT 10;

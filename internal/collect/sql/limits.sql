-- Cluster-wide saturation gauges: current vs configured connections, and the
-- oldest transaction-id age across all databases (transaction-ID wraparound
-- risk). age(datfrozenxid) climbs toward the ~2.1-billion wall past which
-- Postgres refuses writes; a healthy cluster stays well under autovacuum's
-- 200M freeze trigger.
-- max_mxid_age tracks multixact-ID wraparound (mxid_age(datminmxid)); multixacts
-- are consumed by SELECT ... FOR SHARE / FOR UPDATE and FK-check workloads and
-- exhaust toward the same ~2.1B wall as regular transaction ids.
-- conn_used excludes pgbot's own pool connections (pid <> pg_backend_pid() is
-- rewritten by Target.ExcludeSelf to drop all of them) so the monitor doesn't
-- inflate the very connection-saturation percentage it reports.
SELECT (SELECT count(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid())::int AS conn_used,
       current_setting('max_connections')::int                    AS conn_max,
       (SELECT max(age(datfrozenxid)) FROM pg_database)::bigint    AS max_xid_age,
       (SELECT max(mxid_age(datminmxid)) FROM pg_database)::bigint AS max_mxid_age;

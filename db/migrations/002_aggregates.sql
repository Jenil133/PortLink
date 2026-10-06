-- One row per vessel per bucket. last(x, ts) = the most recent value inside the bucket.
-- timescaledb.materialized_only = false turns on real-time aggregation, so queries also see
-- rows that are newer than the last refresh.

-- samples_1s has as many rows as the raw table at 1 Hz, so it gets the raw table's 1 hour chunks.
-- The default is 10 hours, and since retention drops whole chunks that would keep rows far longer.
CREATE MATERIALIZED VIEW IF NOT EXISTS samples_1s
WITH (timescaledb.continuous, timescaledb.materialized_only = false, timescaledb.chunk_interval = '1 hour') AS
SELECT mmsi,
       time_bucket(INTERVAL '1 second', ts) AS bucket,
       last(lat, ts)            AS lat,
       last(lon, ts)            AS lon,
       last(sog, ts)            AS sog,
       last(cog, ts)            AS cog,
       last(heading, ts)        AS heading,
       avg(engine_rpm)::REAL    AS engine_rpm,
       avg(fuel_rate_lph)::REAL AS fuel_rate_lph,
       max(engine_temp_c)       AS engine_temp_c,
       last(fuel_level_pct, ts) AS fuel_level_pct
FROM vessel_samples
GROUP BY mmsi, bucket
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS samples_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT mmsi,
       time_bucket(INTERVAL '1 minute', ts) AS bucket,
       last(lat, ts) AS lat, last(lon, ts) AS lon,
       last(sog, ts) AS sog, last(cog, ts) AS cog, last(heading, ts) AS heading,
       avg(engine_rpm)::REAL AS engine_rpm, avg(fuel_rate_lph)::REAL AS fuel_rate_lph,
       max(engine_temp_c) AS engine_temp_c, last(fuel_level_pct, ts) AS fuel_level_pct
FROM vessel_samples
GROUP BY mmsi, bucket
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS samples_10m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT mmsi,
       time_bucket(INTERVAL '10 minutes', ts) AS bucket,
       last(lat, ts) AS lat, last(lon, ts) AS lon,
       last(sog, ts) AS sog, last(cog, ts) AS cog, last(heading, ts) AS heading,
       avg(engine_rpm)::REAL AS engine_rpm, avg(fuel_rate_lph)::REAL AS fuel_rate_lph,
       max(engine_temp_c) AS engine_temp_c, last(fuel_level_pct, ts) AS fuel_level_pct
FROM vessel_samples
GROUP BY mmsi, bucket
WITH NO DATA;

-- Refresh policies. start_offset (8 h) covers the 6 h backfill and stays inside the
-- 12 h raw retention, so a refresh never wipes buckets whose raw rows were already dropped.
SELECT add_continuous_aggregate_policy('samples_1s',
    start_offset => INTERVAL '8 hours', end_offset => INTERVAL '10 seconds',
    schedule_interval => INTERVAL '30 seconds', if_not_exists => TRUE);
SELECT add_continuous_aggregate_policy('samples_1m',
    start_offset => INTERVAL '8 hours', end_offset => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute', if_not_exists => TRUE);
SELECT add_continuous_aggregate_policy('samples_10m',
    start_offset => INTERVAL '8 hours', end_offset => INTERVAL '10 minutes',
    schedule_interval => INTERVAL '5 minutes', if_not_exists => TRUE);

-- The coarser aggregates are small, keep them a full day. samples_1s has as many rows as the
-- raw table at 1 Hz, so it keeps the raw table's 12 hours, which is as far as history reaches.
SELECT add_retention_policy('samples_1s',  INTERVAL '12 hours', schedule_interval => INTERVAL '1 hour', if_not_exists => TRUE);
SELECT add_retention_policy('samples_1m',  INTERVAL '24 hours', if_not_exists => TRUE);
SELECT add_retention_policy('samples_10m', INTERVAL '24 hours', if_not_exists => TRUE);

-- Retention jobs default to a daily schedule, which would let 1 Hz data pile up for a day
-- past its limit. Check the raw table hourly instead.
SELECT alter_job(job_id, schedule_interval => INTERVAL '1 hour')
FROM timescaledb_information.jobs
WHERE proc_name = 'policy_retention' AND hypertable_name = 'vessel_samples';

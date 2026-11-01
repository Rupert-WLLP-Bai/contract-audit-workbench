-- CLM delivery and global metrics use a database-scoped, schema-specific
-- NOLOGIN maintenance owner. Go migration preflight creates and validates the
-- role and grants temporary-purpose membership only to the migrator identity.
DO $policies$
DECLARE
    maintenance_role text := 'caw_clm_maint_' || md5(current_database() || ':' || current_schema());
BEGIN
    EXECUTE format('CREATE POLICY clm_maintenance_all ON %I.clm_outbox_delivery FOR ALL TO %I USING (true) WITH CHECK (true)', current_schema(), maintenance_role);
    EXECUTE format('CREATE POLICY clm_maintenance_read ON %I.clm_business_events FOR SELECT TO %I USING (true)', current_schema(), maintenance_role);
    EXECUTE format('CREATE POLICY clm_maintenance_read ON %I.clm_approval_rounds FOR SELECT TO %I USING (true)', current_schema(), maintenance_role);
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', current_schema(), maintenance_role);
    EXECUTE format('GRANT CREATE ON SCHEMA %I TO %I', current_schema(), maintenance_role);
    EXECUTE format('GRANT SELECT (tenant_id,event_id,available_at,claim_token,claimed_until,broker_acked_at,attempt_count) ON %I.clm_outbox_delivery TO %I', current_schema(), maintenance_role);
    EXECUTE format('GRANT UPDATE (available_at,claim_token,claimed_until,broker_acked_at,attempt_count) ON %I.clm_outbox_delivery TO %I', current_schema(), maintenance_role);
    EXECUTE format('GRANT SELECT (tenant_id,event_id,event_type,occurred_at) ON %I.clm_business_events TO %I', current_schema(), maintenance_role);
    EXECUTE format('GRANT SELECT (state,started_at,ended_at) ON %I.clm_approval_rounds TO %I', current_schema(), maintenance_role);
END
$policies$;

CREATE FUNCTION {{SCHEMA}}.clm_outbox_claim(p_limit integer, p_lease_seconds bigint)
RETURNS TABLE (
    tenant_id text,
    event_id uuid,
    event_type text,
    occurred_at timestamptz,
    claim_token bigint,
    claimed_until timestamptz,
    message_id text
)
LANGUAGE plpgsql
SECURITY DEFINER
STRICT
SET search_path = pg_catalog, pg_temp
SET statement_timeout = '2s'
AS $function$
BEGIN
    IF p_limit < 1 OR p_limit > 128 OR p_lease_seconds < 1 OR p_lease_seconds > 300 THEN
        RAISE EXCEPTION 'invalid CLM outbox claim bounds' USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS now),
    candidates AS MATERIALIZED (
        SELECT delivery.tenant_id, delivery.event_id
        FROM {{SCHEMA}}.clm_outbox_delivery AS delivery CROSS JOIN instant
        WHERE delivery.broker_acked_at IS NULL
          AND delivery.available_at <= instant.now
          AND (delivery.claimed_until IS NULL OR delivery.claimed_until <= instant.now)
        ORDER BY delivery.available_at, delivery.tenant_id, delivery.event_id
        FOR UPDATE OF delivery SKIP LOCKED
        LIMIT p_limit
    ),
    claimed AS (
        UPDATE {{SCHEMA}}.clm_outbox_delivery AS delivery
        SET claim_token = delivery.claim_token + 1,
            attempt_count = delivery.attempt_count + 1,
            claimed_until = clock_timestamp() + p_lease_seconds * interval '1 second'
        FROM candidates
        WHERE delivery.tenant_id = candidates.tenant_id AND delivery.event_id = candidates.event_id
        RETURNING delivery.tenant_id, delivery.event_id, delivery.claim_token, delivery.claimed_until
    )
    SELECT claimed.tenant_id, claimed.event_id, event.event_type,
        CASE WHEN isfinite(event.occurred_at) THEN event.occurred_at ELSE NULL::timestamptz END,
        claimed.claim_token, claimed.claimed_until,
        encode(convert_to(claimed.tenant_id, 'UTF8'), 'hex') || ':' || claimed.event_id::text
    FROM claimed
    JOIN {{SCHEMA}}.clm_business_events AS event
      ON event.tenant_id = claimed.tenant_id AND event.event_id = claimed.event_id;
END
$function$;

CREATE FUNCTION {{SCHEMA}}.clm_outbox_ack(p_tenant_id text, p_event_id uuid, p_claim_token bigint)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
STRICT
SET search_path = pg_catalog, pg_temp
SET statement_timeout = '2s'
AS $function$
DECLARE
    affected integer;
BEGIN
    UPDATE {{SCHEMA}}.clm_outbox_delivery AS delivery
    SET broker_acked_at = clock_timestamp(), claimed_until = NULL
    WHERE delivery.tenant_id = p_tenant_id AND delivery.event_id = p_event_id
      AND delivery.claim_token = p_claim_token AND delivery.broker_acked_at IS NULL
      AND delivery.claimed_until > clock_timestamp();
    GET DIAGNOSTICS affected = ROW_COUNT;
    IF affected = 1 THEN
        RETURN true;
    END IF;
    RETURN EXISTS (
        SELECT 1 FROM {{SCHEMA}}.clm_outbox_delivery AS delivery
        WHERE delivery.tenant_id = p_tenant_id AND delivery.event_id = p_event_id
          AND delivery.claim_token = p_claim_token AND delivery.broker_acked_at IS NOT NULL
    );
END
$function$;

CREATE FUNCTION {{SCHEMA}}.clm_outbox_release(p_tenant_id text, p_event_id uuid, p_claim_token bigint, p_retry_microseconds bigint)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
STRICT
SET search_path = pg_catalog, pg_temp
SET statement_timeout = '2s'
AS $function$
DECLARE
    affected integer;
BEGIN
    IF p_retry_microseconds < 0 OR p_retry_microseconds > 600000000 THEN
        RAISE EXCEPTION 'invalid CLM outbox retry delay' USING ERRCODE = '22023';
    END IF;
    UPDATE {{SCHEMA}}.clm_outbox_delivery AS delivery
    SET available_at = clock_timestamp() + p_retry_microseconds * interval '1 microsecond',
        claimed_until = NULL
    WHERE delivery.tenant_id = p_tenant_id AND delivery.event_id = p_event_id
      AND delivery.claim_token = p_claim_token AND delivery.broker_acked_at IS NULL
      AND delivery.claimed_until > clock_timestamp();
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected = 1;
END
$function$;

CREATE FUNCTION {{SCHEMA}}.clm_metrics_snapshot(p_aged_after_microseconds bigint)
RETURNS TABLE (
    captured_at timestamptz,
    pending_count bigint,
    pending_age_available boolean,
    oldest_pending_ms double precision,
    approved_count bigint,
    approved_valid boolean,
    approved_p50_ms double precision,
    approved_p95_ms double precision,
    returned_count bigint,
    returned_valid boolean,
    returned_p50_ms double precision,
    returned_p95_ms double precision,
    withdrawn_count bigint,
    withdrawn_valid boolean,
    withdrawn_p50_ms double precision,
    withdrawn_p95_ms double precision,
    terminated_count bigint,
    terminated_valid boolean,
    terminated_p50_ms double precision,
    terminated_p95_ms double precision,
    aged_in_review_count bigint
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET statement_timeout = '2s'
AS $function$
BEGIN
    IF p_aged_after_microseconds IS NOT NULL AND
        (p_aged_after_microseconds <= 0 OR p_aged_after_microseconds > 31536000000000) THEN
        RAISE EXCEPTION 'invalid CLM aged-in-review threshold' USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS now),
    pending AS MATERIALIZED (
        SELECT event.occurred_at
        FROM {{SCHEMA}}.clm_outbox_delivery AS delivery
        JOIN {{SCHEMA}}.clm_business_events AS event
          ON event.tenant_id = delivery.tenant_id AND event.event_id = delivery.event_id
        WHERE delivery.broker_acked_at IS NULL
    ), pending_summary AS (
        SELECT count(*)::bigint AS count,
            COALESCE(bool_and(isfinite(pending.occurred_at) AND pending.occurred_at <= instant.now), false) AS times_valid,
            min(pending.occurred_at) AS oldest
        FROM pending CROSS JOIN instant
    ), rounds AS MATERIALIZED (
        SELECT approval_round.state,
            CASE WHEN approval_round.ended_at IS NOT NULL
                AND isfinite(approval_round.started_at) AND isfinite(approval_round.ended_at)
                AND approval_round.started_at <= instant.now AND approval_round.ended_at <= instant.now
                AND approval_round.ended_at >= approval_round.started_at
                THEN (EXTRACT(EPOCH FROM (approval_round.ended_at - approval_round.started_at)) * 1000)::double precision
                ELSE NULL::double precision END AS duration_ms,
            approval_round.ended_at IS NOT NULL
                AND isfinite(approval_round.started_at) AND isfinite(approval_round.ended_at)
                AND approval_round.started_at <= instant.now AND approval_round.ended_at <= instant.now
                AND approval_round.ended_at >= approval_round.started_at AS interval_valid
        FROM {{SCHEMA}}.clm_approval_rounds AS approval_round CROSS JOIN instant
        WHERE approval_round.state IN ('approved','returned','withdrawn','terminated')
    ), latency AS (
        SELECT state, count(*)::bigint AS count,
            bool_and(interval_valid) AS intervals_valid,
            percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE interval_valid) AS p50,
            percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE interval_valid) AS p95
        FROM rounds GROUP BY state
    ), outcomes AS (
        SELECT required.state, COALESCE(latency.count,0)::bigint AS count,
            COALESCE(latency.intervals_valid, false) AS intervals_valid,
            latency.p50, latency.p95
        FROM (VALUES ('approved'),('returned'),('withdrawn'),('terminated')) AS required(state)
        LEFT JOIN latency ON latency.state=required.state
    ), aged AS (
        SELECT CASE WHEN p_aged_after_microseconds IS NULL
                OR COALESCE(bool_or(approval_round.state='in_review'
                    AND (NOT isfinite(approval_round.started_at) OR approval_round.started_at > instant.now)),false)
            THEN NULL::bigint
            ELSE count(*) FILTER (WHERE approval_round.state='in_review'
                AND approval_round.started_at <= instant.now - p_aged_after_microseconds * interval '1 microsecond')::bigint END AS count
        FROM {{SCHEMA}}.clm_approval_rounds AS approval_round CROSS JOIN instant
    )
    SELECT instant.now,
        pending_summary.count,
        pending_summary.count > 0 AND pending_summary.times_valid,
        CASE WHEN pending_summary.count > 0 AND pending_summary.times_valid
            THEN GREATEST(EXTRACT(EPOCH FROM (instant.now-pending_summary.oldest))*1000,0)::double precision
            ELSE NULL::double precision END,
        approved.count, approved.count > 0 AND approved.intervals_valid, approved.p50, approved.p95,
        returned.count, returned.count > 0 AND returned.intervals_valid, returned.p50, returned.p95,
        withdrawn.count, withdrawn.count > 0 AND withdrawn.intervals_valid, withdrawn.p50, withdrawn.p95,
        terminated.count, terminated.count > 0 AND terminated.intervals_valid, terminated.p50, terminated.p95,
        aged.count
    FROM instant CROSS JOIN pending_summary CROSS JOIN aged
    JOIN outcomes AS approved ON approved.state='approved'
    JOIN outcomes AS returned ON returned.state='returned'
    JOIN outcomes AS withdrawn ON withdrawn.state='withdrawn'
    JOIN outcomes AS terminated ON terminated.state='terminated';
END
$function$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_outbox_claim(integer,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_outbox_ack(text,uuid,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_outbox_release(text,uuid,bigint,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_metrics_snapshot(bigint) FROM PUBLIC;

DO $ownership$
DECLARE
    maintenance_role text := 'caw_clm_maint_' || md5(current_database() || ':' || current_schema());
BEGIN
    EXECUTE format('ALTER FUNCTION %I.clm_outbox_claim(integer,bigint) OWNER TO %I', current_schema(), maintenance_role);
    EXECUTE format('ALTER FUNCTION %I.clm_outbox_ack(text,uuid,bigint) OWNER TO %I', current_schema(), maintenance_role);
    EXECUTE format('ALTER FUNCTION %I.clm_outbox_release(text,uuid,bigint,bigint) OWNER TO %I', current_schema(), maintenance_role);
    EXECUTE format('ALTER FUNCTION %I.clm_metrics_snapshot(bigint) OWNER TO %I', current_schema(), maintenance_role);
    EXECUTE format('REVOKE CREATE ON SCHEMA %I FROM %I', current_schema(), maintenance_role);
END
$ownership$;

SET LOCAL ROLE {{CLM_OWNER}};
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_outbox_claim(integer,bigint) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_outbox_ack(text,uuid,bigint) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_outbox_release(text,uuid,bigint,bigint) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.clm_metrics_snapshot(bigint) FROM SESSION_USER;
RESET ROLE;

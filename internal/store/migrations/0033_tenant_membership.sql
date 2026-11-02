-- Tenant membership is the only production source of tenant/org/role/grant
-- authority. The enterprise directory proves issuer+subject and supplies a
-- profile; it does not authorize a membership.
CREATE TABLE {{SCHEMA}}.identity_jurisdictions (
    jurisdiction_id text PRIMARY KEY CHECK (btrim(jurisdiction_id) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    status text NOT NULL CHECK (status IN ('active','inactive')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE {{SCHEMA}}.identity_tenants (
    tenant_id text PRIMARY KEY CHECK (btrim(tenant_id) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    jurisdiction_id text REFERENCES {{SCHEMA}}.identity_jurisdictions(jurisdiction_id),
    status text NOT NULL CHECK (status IN ('active','suspended','closed')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE {{SCHEMA}}.identity_org_units (
    tenant_id text NOT NULL REFERENCES {{SCHEMA}}.identity_tenants(tenant_id),
    org_unit_id text NOT NULL CHECK (btrim(org_unit_id) <> ''),
    parent_org_unit_id text,
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    status text NOT NULL CHECK (status IN ('active','inactive')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    PRIMARY KEY (tenant_id, org_unit_id),
    FOREIGN KEY (tenant_id, parent_org_unit_id)
        REFERENCES {{SCHEMA}}.identity_org_units(tenant_id, org_unit_id),
    CHECK (parent_org_unit_id IS NULL OR parent_org_unit_id <> org_unit_id)
);

CREATE TABLE {{SCHEMA}}.identity_principals (
    principal_id uuid PRIMARY KEY,
    actor_id text NOT NULL UNIQUE CHECK (btrim(actor_id) <> ''),
    issuer text NOT NULL CHECK (btrim(issuer) <> ''),
    subject text NOT NULL CHECK (btrim(subject) <> ''),
    status text NOT NULL CHECK (status IN ('active','disabled')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (issuer, subject)
);

CREATE TABLE {{SCHEMA}}.identity_memberships (
    membership_id uuid PRIMARY KEY,
    principal_id uuid NOT NULL REFERENCES {{SCHEMA}}.identity_principals(principal_id),
    tenant_id text NOT NULL REFERENCES {{SCHEMA}}.identity_tenants(tenant_id),
    org_unit_id text NOT NULL,
    role_code text NOT NULL CHECK (role_code IN ('admin','clerk','reviewer','viewer')),
    grants jsonb NOT NULL CHECK (jsonb_typeof(grants) = 'array'),
    status text NOT NULL CHECK (status IN ('active','suspended','revoked')),
    effective_from timestamptz,
    effective_until timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, org_unit_id)
        REFERENCES {{SCHEMA}}.identity_org_units(tenant_id, org_unit_id),
    UNIQUE (principal_id, tenant_id, org_unit_id),
    UNIQUE (membership_id, principal_id, tenant_id, org_unit_id),
    CHECK (effective_from IS NULL OR effective_until IS NULL OR effective_until > effective_from)
);
CREATE INDEX identity_memberships_principal_active
    ON {{SCHEMA}}.identity_memberships (principal_id, status, tenant_id, org_unit_id);

CREATE TABLE {{SCHEMA}}.identity_pending_auth (
    token_digest text PRIMARY KEY CHECK (token_digest ~ '^[0-9a-f]{64}$'),
    principal_id uuid NOT NULL REFERENCES {{SCHEMA}}.identity_principals(principal_id),
    audience text NOT NULL CHECK (audience IN ('client','admin')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes'),
    CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE INDEX identity_pending_auth_expiry ON {{SCHEMA}}.identity_pending_auth (expires_at);

ALTER TABLE {{SCHEMA}}.sessions
    ADD COLUMN principal_id uuid,
    ADD COLUMN membership_id uuid,
    ADD CONSTRAINT sessions_membership_identity_check CHECK
        ((principal_id IS NULL AND membership_id IS NULL) OR
         (principal_id IS NOT NULL AND membership_id IS NOT NULL)),
    ADD CONSTRAINT sessions_membership_identity_fk
        FOREIGN KEY (membership_id, principal_id, tenant_id, org_id)
        REFERENCES {{SCHEMA}}.identity_memberships(membership_id, principal_id, tenant_id, org_unit_id);

-- Identity rows have one non-login definer owner. Ordinary services receive no
-- direct access; Core can call only bounded identity and ticket resolution APIs.
GRANT USAGE, CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (jurisdiction_id,display_name,status,revision) ON {{SCHEMA}}.identity_jurisdictions TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,display_name,jurisdiction_id,status,revision) ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_unit_id,display_name,status,revision) ON {{SCHEMA}}.identity_org_units TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (principal_id,actor_id,issuer,subject,status,revision) ON {{SCHEMA}}.identity_principals TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (membership_id,principal_id,tenant_id,org_unit_id,role_code,grants,status,effective_from,effective_until,revision)
    ON {{SCHEMA}}.identity_memberships TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (token_digest,principal_id,audience,expires_at,consumed_at), INSERT (token_digest,principal_id,audience,created_at,expires_at),
    UPDATE (consumed_at) ON {{SCHEMA}}.identity_pending_auth TO {{AUTH_FENCE_OWNER}};
GRANT INSERT (token, actor_id, expires_at, issuer, subject, tenant_id, org_id, audience, principal_id, membership_id)
    ON {{SCHEMA}}.sessions TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (token, actor_id, issuer, subject, tenant_id, org_id, audience, expires_at, principal_id, membership_id)
    ON {{SCHEMA}}.sessions TO {{AUTH_FENCE_OWNER}};
GRANT USAGE ON SEQUENCE {{SCHEMA}}.sessions_seq_seq TO {{AUTH_FENCE_OWNER}};

-- A random, short-lived Document intake ticket binds one committed operation
-- to the session and exact upload bytes. Document never reads this table.
CREATE TABLE {{SCHEMA}}.intake_authority_tickets (
    ticket_digest text PRIMARY KEY CHECK (ticket_digest ~ '^[0-9a-f]{64}$'),
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    session_digest text NOT NULL CHECK (session_digest ~ '^sha256:[0-9a-f]{64}$'),
    principal_id uuid,
    membership_id uuid,
    actor_id text NOT NULL,
    org_id text NOT NULL,
    case_id text NOT NULL,
    upload_key text NOT NULL,
    filename text NOT NULL CHECK (filename <> ''),
    media_type text NOT NULL CHECK (media_type <> ''),
    body_sha256 text NOT NULL CHECK (body_sha256 ~ '^[0-9a-f]{64}$'),
    action text NOT NULL CHECK (action='create_primary_contract'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    FOREIGN KEY (tenant_id,operation_id) REFERENCES {{SCHEMA}}.case_intake_operations(tenant_id,operation_id),
    FOREIGN KEY (membership_id,principal_id,tenant_id,org_id)
        REFERENCES {{SCHEMA}}.identity_memberships(membership_id,principal_id,tenant_id,org_unit_id),
    CHECK ((principal_id IS NULL AND membership_id IS NULL) OR (principal_id IS NOT NULL AND membership_id IS NOT NULL)),
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes')
);
REVOKE ALL ON TABLE {{SCHEMA}}.intake_authority_tickets FROM PUBLIC;
ALTER TABLE {{SCHEMA}}.intake_authority_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.intake_authority_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.intake_authority_tickets
    USING (tenant_id = current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id',true));
GRANT SELECT (ticket_digest,tenant_id,operation_id,session_digest,principal_id,membership_id,actor_id,org_id,case_id,upload_key,filename,media_type,body_sha256,action,expires_at)
    ON {{SCHEMA}}.intake_authority_tickets TO {{AUTH_FENCE_OWNER}};
CREATE POLICY intake_ticket_owner_read ON {{SCHEMA}}.intake_authority_tickets
    FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);

CREATE TABLE {{SCHEMA}}.operation_authority_tickets (
 ticket_digest text PRIMARY KEY CHECK(ticket_digest ~ '^[0-9a-f]{64}$'),
 tenant_id text NOT NULL, org_id text NOT NULL, session_digest text NOT NULL,
 principal_id uuid, membership_id uuid,
 scope jsonb NOT NULL CHECK(jsonb_typeof(scope)='object'),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT statement_timestamp()+interval '5 minutes',
 FOREIGN KEY (membership_id,principal_id,tenant_id,org_id) REFERENCES {{SCHEMA}}.identity_memberships(membership_id,principal_id,tenant_id,org_unit_id),
 CHECK ((principal_id IS NULL AND membership_id IS NULL) OR (principal_id IS NOT NULL AND membership_id IS NOT NULL)),
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '5 minutes')
);
REVOKE ALL ON TABLE {{SCHEMA}}.operation_authority_tickets FROM PUBLIC;
ALTER TABLE {{SCHEMA}}.operation_authority_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.operation_authority_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.operation_authority_tickets
 USING(tenant_id=current_setting('app.tenant_id',true)) WITH CHECK(tenant_id=current_setting('app.tenant_id',true));

-- A review job may carry the exact membership that admitted its attempt. The
-- AUTH_FENCE owner can read only the claim and case fields needed to recheck a
-- live frozen attempt; service roles retain their existing table ownership.
CREATE TABLE {{SCHEMA}}.worker_claim_identities (
    tenant_id text NOT NULL,
    job_id text NOT NULL REFERENCES {{SCHEMA}}.worker_jobs(id),
    org_id text NOT NULL,
    case_id text NOT NULL,
    run_id text NOT NULL,
    attempt_id text NOT NULL,
    actor_id text NOT NULL,
    principal_id uuid,
    membership_id uuid,
    membership_org_id text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,job_id),
    FOREIGN KEY (membership_id,principal_id,tenant_id,membership_org_id)
        REFERENCES {{SCHEMA}}.identity_memberships(membership_id,principal_id,tenant_id,org_unit_id),
    CHECK ((principal_id IS NULL AND membership_id IS NULL AND membership_org_id IS NULL) OR (principal_id IS NOT NULL AND membership_id IS NOT NULL AND membership_org_id IS NOT NULL))
);
REVOKE ALL ON TABLE {{SCHEMA}}.worker_claim_identities FROM PUBLIC;
ALTER TABLE {{SCHEMA}}.worker_claim_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.worker_claim_identities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.worker_claim_identities
    USING (tenant_id=current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
CREATE POLICY worker_claim_identity_owner_read ON {{SCHEMA}}.worker_claim_identities
    FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);
CREATE POLICY worker_claim_identity_owner_insert ON {{SCHEMA}}.worker_claim_identities
    FOR INSERT TO {{AUTH_FENCE_OWNER}} WITH CHECK (true);
GRANT SELECT (tenant_id,job_id,org_id,case_id,run_id,attempt_id,actor_id,principal_id,membership_id,membership_org_id)
    ON {{SCHEMA}}.worker_claim_identities TO {{AUTH_FENCE_OWNER}};
GRANT INSERT (tenant_id,job_id,org_id,case_id,run_id,attempt_id,actor_id,principal_id,membership_id,membership_org_id)
    ON {{SCHEMA}}.worker_claim_identities TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (id,tenant_id,org_id,case_id,run_id,attempt_id,actor_id,worker_id,generation,state,lease_until,kind)
    ON {{SCHEMA}}.worker_jobs TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,id,owner_id,member_ids,archived_at)
    ON {{SCHEMA}}.cases TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,case_id,id,state)
    ON {{SCHEMA}}.review_runs TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,run_id,id,history_kind,state,input_hash)
    ON {{SCHEMA}}.review_attempts TO {{AUTH_FENCE_OWNER}};
CREATE POLICY worker_identity_claim_owner_read ON {{SCHEMA}}.worker_jobs
    FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);
CREATE POLICY case_identity_claim_owner_read ON {{SCHEMA}}.cases
    FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);
CREATE POLICY run_identity_claim_owner_read ON {{SCHEMA}}.review_runs
    FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);
CREATE POLICY attempt_identity_claim_owner_read ON {{SCHEMA}}.review_attempts
    FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);

ALTER TABLE {{SCHEMA}}.identity_jurisdictions ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_jurisdictions FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_owner_all ON {{SCHEMA}}.identity_jurisdictions TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.identity_tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_owner_all ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.identity_org_units ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_org_units FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_owner_all ON {{SCHEMA}}.identity_org_units TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.identity_principals ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_principals FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_owner_all ON {{SCHEMA}}.identity_principals TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.identity_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_owner_all ON {{SCHEMA}}.identity_memberships TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.identity_pending_auth ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_pending_auth FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_owner_all ON {{SCHEMA}}.identity_pending_auth TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
REVOKE ALL ON {{SCHEMA}}.identity_jurisdictions, {{SCHEMA}}.identity_tenants, {{SCHEMA}}.identity_org_units,
    {{SCHEMA}}.identity_principals, {{SCHEMA}}.identity_memberships, {{SCHEMA}}.identity_pending_auth FROM PUBLIC;
DROP POLICY tenant_boundary ON {{SCHEMA}}.sessions;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.sessions USING
    (tenant_id = current_setting('app.tenant_id',true) OR token = current_setting('app.session_token',true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id',true) AND
        (current_user <> {{AUTH_FENCE_OWNER_LITERAL}} OR
         (org_id = current_setting('app.org_id',true) AND actor_id = current_setting('app.actor_id',true))));

CREATE FUNCTION {{SCHEMA}}.tenant_identity_oidc_login(
    p_issuer text, p_subject text, p_audience text,
    p_pending_digest text, p_session_digest text, p_session_expires_at text
)
RETURNS TABLE (
    pending boolean,
    principal_id uuid, actor_id text, issuer text, subject text,
    membership_id uuid, tenant_id text, tenant_name text, jurisdiction_id text,
    org_unit_id text, org_unit_name text, role_code text, grants jsonb,
    principal_revision bigint, tenant_revision bigint, membership_revision bigint
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    principal_row record;
    principal_key uuid;
    eligible_count integer;
    resolved record;
    expiry timestamptz;
    challenge_created_at timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
    IF p_issuer IS NULL OR btrim(p_issuer)='' OR p_subject IS NULL OR btrim(p_subject)='' OR
       p_audience NOT IN ('client','admin') OR p_pending_digest !~ '^[0-9a-f]{64}$' OR
       p_session_digest !~ '^sha256:[0-9a-f]{64}$' THEN
        RAISE EXCEPTION 'invalid verified identity request' USING ERRCODE='22023';
    END IF;
    expiry := p_session_expires_at::timestamptz;
    IF expiry <= clock_timestamp() OR expiry > clock_timestamp()+interval '7 days' THEN
        RAISE EXCEPTION 'invalid session expiry' USING ERRCODE='22023';
    END IF;
    SELECT identity.principal_id,identity.actor_id,identity.issuer,identity.subject,identity.revision INTO principal_row
      FROM {{SCHEMA}}.identity_principals AS identity
     WHERE identity.issuer=p_issuer AND identity.subject=p_subject AND identity.status='active'
     ;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'verified identity has no active principal' USING ERRCODE='28000';
    END IF;
    principal_key := principal_row.principal_id;
    SELECT count(*) INTO eligible_count
      FROM {{SCHEMA}}.identity_memberships membership
      JOIN {{SCHEMA}}.identity_tenants tenant USING (tenant_id)
      JOIN {{SCHEMA}}.identity_org_units unit USING (tenant_id,org_unit_id)
     WHERE membership.principal_id=principal_key AND membership.status='active'
       AND tenant.status='active' AND unit.status='active'
       AND (membership.effective_from IS NULL OR membership.effective_from <= clock_timestamp())
       AND (membership.effective_until IS NULL OR membership.effective_until > clock_timestamp())
       AND (p_audience='client' OR membership.role_code='admin');
    IF eligible_count=0 THEN
        RAISE EXCEPTION 'verified identity has no eligible active membership' USING ERRCODE='28000';
    ELSIF eligible_count>1 THEN
        challenge_created_at := clock_timestamp();
        INSERT INTO {{SCHEMA}}.identity_pending_auth(token_digest,principal_id,audience,created_at,expires_at)
            VALUES(p_pending_digest,principal_key,p_audience,challenge_created_at,challenge_created_at+interval '5 minutes');
        RETURN QUERY SELECT true, principal_row.principal_id,principal_row.actor_id,principal_row.issuer,principal_row.subject,
            membership.membership_id,tenant.tenant_id,tenant.display_name,tenant.jurisdiction_id,
            unit.org_unit_id,unit.display_name,membership.role_code,membership.grants,
            principal_row.revision,tenant.revision,membership.revision
          FROM {{SCHEMA}}.identity_memberships membership
          JOIN {{SCHEMA}}.identity_tenants tenant USING (tenant_id)
          JOIN {{SCHEMA}}.identity_org_units unit USING (tenant_id,org_unit_id)
         WHERE membership.principal_id=principal_key AND membership.status='active'
           AND tenant.status='active' AND unit.status='active'
           AND (membership.effective_from IS NULL OR membership.effective_from <= clock_timestamp())
           AND (membership.effective_until IS NULL OR membership.effective_until > clock_timestamp())
           AND (p_audience='client' OR membership.role_code='admin')
         ORDER BY tenant.tenant_id,unit.org_unit_id,membership.membership_id;
        RETURN;
    END IF;
    SELECT p.principal_id,p.actor_id,p.issuer,p.subject,
        membership.membership_id,tenant.tenant_id,tenant.display_name AS tenant_name,
        tenant.jurisdiction_id,unit.org_unit_id,unit.display_name AS org_unit_name,
        membership.role_code,membership.grants,p.revision AS principal_revision,
        tenant.revision AS tenant_revision,membership.revision AS membership_revision
      INTO resolved
      FROM {{SCHEMA}}.identity_principals p
      JOIN {{SCHEMA}}.identity_memberships membership USING (principal_id)
      JOIN {{SCHEMA}}.identity_tenants tenant USING (tenant_id)
      JOIN {{SCHEMA}}.identity_org_units unit USING (tenant_id,org_unit_id)
     WHERE p.principal_id=principal_key AND membership.principal_id=p.principal_id AND membership.status='active'
       AND tenant.status='active' AND unit.status='active'
       AND (membership.effective_from IS NULL OR membership.effective_from <= clock_timestamp())
       AND (membership.effective_until IS NULL OR membership.effective_until > clock_timestamp())
       AND (p_audience='client' OR membership.role_code='admin')
     ;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'verified identity membership changed during login' USING ERRCODE='28000';
    END IF;
    PERFORM set_config('app.tenant_id',resolved.tenant_id,true);
    PERFORM set_config('app.org_id',resolved.org_unit_id,true);
    PERFORM set_config('app.actor_id',resolved.actor_id,true);
    PERFORM set_config('app.actor_role','api',true);
    INSERT INTO {{SCHEMA}}.sessions(token,actor_id,expires_at,issuer,subject,tenant_id,org_id,audience,principal_id,membership_id)
        VALUES(p_session_digest,resolved.actor_id,to_char(expiry AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
            resolved.issuer,resolved.subject,resolved.tenant_id,resolved.org_unit_id,p_audience,resolved.principal_id,resolved.membership_id);
    RETURN QUERY SELECT false,resolved.principal_id,resolved.actor_id,resolved.issuer,resolved.subject,
        resolved.membership_id,resolved.tenant_id,resolved.tenant_name,resolved.jurisdiction_id,
        resolved.org_unit_id,resolved.org_unit_name,resolved.role_code,resolved.grants,
        resolved.principal_revision,resolved.tenant_revision,resolved.membership_revision;
END
$function$;

CREATE FUNCTION {{SCHEMA}}.tenant_identity_list_pending(p_token_digest text, p_audience text)
RETURNS TABLE (
    actor_id text, issuer text, subject text,
    membership_id uuid, tenant_id text, tenant_name text, jurisdiction_id text,
    org_unit_id text, org_unit_name text, role_code text
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
    RETURN QUERY
    SELECT principal.actor_id,principal.issuer,principal.subject,
        membership.membership_id, tenant.tenant_id, tenant.display_name, tenant.jurisdiction_id,
        unit.org_unit_id, unit.display_name, membership.role_code
    FROM {{SCHEMA}}.identity_pending_auth AS pending
    JOIN {{SCHEMA}}.identity_principals AS principal USING (principal_id)
    JOIN {{SCHEMA}}.identity_memberships AS membership USING (principal_id)
    JOIN {{SCHEMA}}.identity_tenants AS tenant USING (tenant_id)
    JOIN {{SCHEMA}}.identity_org_units AS unit USING (tenant_id, org_unit_id)
    WHERE pending.token_digest=p_token_digest AND pending.audience=p_audience AND p_audience IN ('client','admin') AND pending.consumed_at IS NULL
      AND pending.expires_at > clock_timestamp() AND principal.status='active'
      AND membership.status='active' AND tenant.status='active' AND unit.status='active'
      AND (membership.effective_from IS NULL OR membership.effective_from <= clock_timestamp())
      AND (membership.effective_until IS NULL OR membership.effective_until > clock_timestamp())
      AND (pending.audience='client' OR membership.role_code='admin')
    ORDER BY tenant.tenant_id, unit.org_unit_id, membership.membership_id;
END
$function$;

CREATE FUNCTION {{SCHEMA}}.tenant_identity_consume_pending(
    p_token_digest text, p_membership_id uuid, p_session_digest text, p_audience text, p_expires_at text
)
RETURNS TABLE (
    principal_id uuid, actor_id text, issuer text, subject text,
    membership_id uuid, tenant_id text, tenant_name text, jurisdiction_id text,
    org_unit_id text, org_unit_name text, role_code text, grants jsonb,
    principal_revision bigint, tenant_revision bigint, membership_revision bigint
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    pending_row record;
    candidate record;
    resolved record;
    expiry timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
    IF p_audience NOT IN ('client','admin') OR p_token_digest !~ '^[0-9a-f]{64}$' OR p_session_digest !~ '^sha256:[0-9a-f]{64}$' THEN
        RAISE EXCEPTION 'invalid pending authentication input' USING ERRCODE='22023';
    END IF;
    expiry := p_expires_at::timestamptz;
    IF expiry <= clock_timestamp() OR expiry > clock_timestamp()+interval '7 days' THEN
        RAISE EXCEPTION 'invalid session expiry' USING ERRCODE='22023';
    END IF;
    -- First discover the lock keys without taking locks, then use the stable
    -- tenant -> membership -> challenge order and repeat every predicate.
    SELECT pending_auth.principal_id,pending_auth.audience,membership.tenant_id
      INTO candidate
      FROM {{SCHEMA}}.identity_pending_auth AS pending_auth
      JOIN {{SCHEMA}}.identity_memberships AS membership ON membership.membership_id=p_membership_id
        AND membership.principal_id=pending_auth.principal_id
     WHERE pending_auth.token_digest=p_token_digest AND pending_auth.audience=p_audience
       AND pending_auth.consumed_at IS NULL AND pending_auth.expires_at>clock_timestamp();
    IF NOT FOUND THEN
        RAISE EXCEPTION 'pending identity expired or already consumed' USING ERRCODE='28000';
    END IF;
    SELECT principal.principal_id,principal.actor_id,principal.issuer,principal.subject,
        membership.membership_id,tenant.tenant_id,tenant.display_name AS tenant_name,
        tenant.jurisdiction_id,unit.org_unit_id,unit.display_name AS org_unit_name,
        membership.role_code,membership.grants,principal.revision AS principal_revision,
        tenant.revision AS tenant_revision,membership.revision AS membership_revision
      INTO resolved
      FROM {{SCHEMA}}.identity_memberships membership
      JOIN {{SCHEMA}}.identity_principals principal USING (principal_id)
      JOIN {{SCHEMA}}.identity_tenants tenant USING (tenant_id)
      JOIN {{SCHEMA}}.identity_org_units unit USING (tenant_id,org_unit_id)
     WHERE membership.membership_id=p_membership_id AND membership.principal_id=candidate.principal_id
       AND principal.status='active' AND membership.status='active' AND tenant.status='active' AND unit.status='active'
       AND (membership.effective_from IS NULL OR membership.effective_from<=clock_timestamp())
       AND (membership.effective_until IS NULL OR membership.effective_until>clock_timestamp())
       AND (p_audience='client' OR membership.role_code='admin')
     ;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'selected membership is inactive or unavailable' USING ERRCODE='28000';
    END IF;
    SELECT pending_auth.principal_id,pending_auth.audience INTO pending_row
      FROM {{SCHEMA}}.identity_pending_auth
     AS pending_auth WHERE pending_auth.token_digest=p_token_digest AND pending_auth.audience=p_audience AND pending_auth.consumed_at IS NULL AND pending_auth.expires_at > clock_timestamp()
     FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'pending identity expired or already consumed' USING ERRCODE='28000';
    END IF;
    UPDATE {{SCHEMA}}.identity_pending_auth SET consumed_at=clock_timestamp()
     WHERE token_digest=p_token_digest AND consumed_at IS NULL AND expires_at > clock_timestamp();
    IF NOT FOUND THEN
        RAISE EXCEPTION 'pending identity expired or already consumed' USING ERRCODE='28000';
    END IF;
    PERFORM set_config('app.tenant_id',resolved.tenant_id,true);
    PERFORM set_config('app.org_id',resolved.org_unit_id,true);
    PERFORM set_config('app.actor_id',resolved.actor_id,true);
    PERFORM set_config('app.actor_role','api',true);
    INSERT INTO {{SCHEMA}}.sessions(token,actor_id,expires_at,issuer,subject,tenant_id,org_id,audience,principal_id,membership_id)
        VALUES(p_session_digest,resolved.actor_id,to_char(expiry AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
            resolved.issuer,resolved.subject,resolved.tenant_id,resolved.org_unit_id,pending_row.audience,resolved.principal_id,resolved.membership_id);
    RETURN QUERY SELECT resolved.principal_id,resolved.actor_id,resolved.issuer,resolved.subject,
        resolved.membership_id,resolved.tenant_id,resolved.tenant_name,resolved.jurisdiction_id,
        resolved.org_unit_id,resolved.org_unit_name,resolved.role_code,resolved.grants,
        resolved.principal_revision,resolved.tenant_revision,resolved.membership_revision;
END
$function$;

CREATE FUNCTION {{SCHEMA}}.tenant_identity_session_user(p_token_digest text)
RETURNS TABLE (
    principal_id uuid, actor_id text, issuer text, subject text, audience text,
    membership_id uuid, tenant_id text, tenant_name text, jurisdiction_id text,
    org_unit_id text, org_unit_name text, role_code text, grants jsonb,
    expires_at text
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
    IF p_token_digest IS NULL OR p_token_digest !~ '^sha256:[0-9a-f]{64}$' THEN
        RAISE EXCEPTION 'invalid session lookup' USING ERRCODE='22023';
    END IF;
    -- This GUC only enables the existing exact-token RLS predicate. The
    -- persistent row and high-entropy digest remain the credential check.
    PERFORM set_config('app.session_token',p_token_digest,true);
    RETURN QUERY
    SELECT principal.principal_id,principal.actor_id,principal.issuer,principal.subject,
        session.audience,membership.membership_id,tenant.tenant_id,tenant.display_name,tenant.jurisdiction_id,
        unit.org_unit_id,unit.display_name,membership.role_code,membership.grants,session.expires_at
    FROM {{SCHEMA}}.sessions AS session
    JOIN {{SCHEMA}}.identity_principals AS principal ON principal.principal_id=session.principal_id
    JOIN {{SCHEMA}}.identity_memberships AS membership ON membership.membership_id=session.membership_id
        AND membership.principal_id=principal.principal_id AND membership.tenant_id=session.tenant_id
        AND membership.org_unit_id=session.org_id
    JOIN {{SCHEMA}}.identity_tenants AS tenant ON tenant.tenant_id=membership.tenant_id
    JOIN {{SCHEMA}}.identity_org_units AS unit ON unit.tenant_id=membership.tenant_id AND unit.org_unit_id=membership.org_unit_id
    WHERE session.token=p_token_digest AND session.expires_at::timestamptz>clock_timestamp()
      AND principal.status='active' AND membership.status='active' AND tenant.status='active' AND unit.status='active'
      AND (membership.effective_from IS NULL OR membership.effective_from<=clock_timestamp())
      AND (membership.effective_until IS NULL OR membership.effective_until>clock_timestamp())
      AND (session.audience='client' OR (session.audience='admin' AND membership.role_code='admin'));
    IF NOT FOUND THEN
        RAISE EXCEPTION 'session identity is unavailable' USING ERRCODE='28000';
    END IF;
END
$function$;

CREATE FUNCTION {{SCHEMA}}.tenant_identity_intake_ticket(p_ticket_digest text)
RETURNS TABLE (
    tenant_id text, operation_id text, session_digest text, principal_id uuid, membership_id uuid,
    actor_id text, org_id text, case_id text, upload_key text, filename text, media_type text,
    body_sha256 text, action text, expires_at timestamptz
)
LANGUAGE sql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
    SELECT ticket.tenant_id,ticket.operation_id,ticket.session_digest,ticket.principal_id,ticket.membership_id,
        ticket.actor_id,ticket.org_id,ticket.case_id,ticket.upload_key,ticket.filename,ticket.media_type,ticket.body_sha256,ticket.action,ticket.expires_at
    FROM {{SCHEMA}}.intake_authority_tickets AS ticket
    WHERE ticket.ticket_digest=p_ticket_digest
      AND p_ticket_digest ~ '^[0-9a-f]{64}$'
      AND ticket.expires_at>clock_timestamp()
$function$;

CREATE FUNCTION {{SCHEMA}}.tenant_identity_claim_user(
    p_tenant_id text,p_org_id text,p_job_id text,p_worker_id text,p_generation bigint,
    p_case_id text,p_run_id text,p_attempt_id text,p_phase text
)
RETURNS TABLE (
    principal_id uuid,actor_id text,issuer text,subject text,membership_id uuid,
    tenant_id text,org_id text,membership_org_id text,role_code text,grants jsonb,
    case_owner_id text,case_member_ids text
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
    RETURN QUERY SELECT principal.principal_id,COALESCE(principal.actor_id,job.actor_id),principal.issuer,principal.subject,
        membership.membership_id,job.tenant_id,job.org_id,membership.org_unit_id,membership.role_code,
        membership.grants,case_row.owner_id,case_row.member_ids
    FROM {{SCHEMA}}.worker_jobs AS job
    JOIN {{SCHEMA}}.worker_claim_identities AS claim ON claim.tenant_id=job.tenant_id AND claim.job_id=job.id
        AND claim.org_id=job.org_id AND claim.case_id=job.case_id AND claim.run_id=COALESCE(job.run_id,'')
        AND claim.attempt_id=COALESCE(job.attempt_id,'') AND claim.actor_id=job.actor_id
    LEFT JOIN {{SCHEMA}}.review_runs AS run ON run.tenant_id=job.tenant_id AND run.org_id=job.org_id
        AND run.case_id=job.case_id AND run.id=job.run_id
    LEFT JOIN {{SCHEMA}}.review_attempts AS attempt ON attempt.tenant_id=job.tenant_id AND attempt.org_id=job.org_id
        AND attempt.run_id=job.run_id AND attempt.id=job.attempt_id
    JOIN {{SCHEMA}}.cases AS case_row ON case_row.tenant_id=job.tenant_id AND case_row.org_id=job.org_id AND case_row.id=job.case_id
    LEFT JOIN {{SCHEMA}}.identity_principals AS principal ON principal.principal_id=claim.principal_id AND principal.actor_id=job.actor_id
    LEFT JOIN {{SCHEMA}}.identity_memberships AS membership ON membership.membership_id=claim.membership_id
        AND membership.principal_id=principal.principal_id AND membership.tenant_id=job.tenant_id
        AND membership.org_unit_id=claim.membership_org_id
    LEFT JOIN {{SCHEMA}}.identity_tenants AS tenant ON tenant.tenant_id=membership.tenant_id
    LEFT JOIN {{SCHEMA}}.identity_org_units AS unit ON unit.tenant_id=membership.tenant_id AND unit.org_unit_id=membership.org_unit_id
    WHERE job.tenant_id=p_tenant_id AND job.org_id=p_org_id AND job.id=p_job_id AND job.worker_id=p_worker_id
      AND job.generation=p_generation AND job.case_id=p_case_id AND COALESCE(job.run_id,'')=p_run_id AND COALESCE(job.attempt_id,'')=p_attempt_id
      AND job.state='running' AND job.lease_until>clock_timestamp()
      AND p_phase IN ('worker_start','tool_read')
      AND ((job.kind='review' AND run.state IN ('accepted','running') AND attempt.state IN ('accepted','running')
           AND (p_phase<>'tool_read' OR (run.state='running' AND attempt.state='running' AND attempt.history_kind='frozen' AND attempt.input_hash IS NOT NULL))
           AND attempt.history_kind IN ('preparing','frozen') AND (attempt.history_kind='preparing' OR attempt.input_hash IS NOT NULL))
        OR (job.kind='experience_distill' AND p_phase='worker_start' AND p_run_id='' AND p_attempt_id=''))
      AND case_row.archived_at IS NULL
      AND ((claim.principal_id IS NULL AND claim.membership_id IS NULL) OR
        (principal.status='active' AND membership.status='active' AND tenant.status='active' AND unit.status='active'
         AND (membership.effective_from IS NULL OR membership.effective_from<=clock_timestamp())
         AND (membership.effective_until IS NULL OR membership.effective_until>clock_timestamp())));
END
$function$;

-- Bind an Execution-admitted job from the exact live browser session. Execution
-- supplies only the opaque session digest; all identity and Review authority
-- are re-read under the same identity lock after the job/attempt rows exist.
CREATE FUNCTION {{SCHEMA}}.tenant_identity_bind_admission(p_session_digest text,p_job_id text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    bound record;
    existing record;
    now_at timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
    now_at := clock_timestamp();
    IF p_session_digest IS NULL OR p_session_digest !~ '^sha256:[0-9a-f]{64}$' OR p_job_id IS NULL OR p_job_id='' THEN
        RAISE EXCEPTION 'invalid admission binding' USING ERRCODE='22023';
    END IF;
    PERFORM set_config('app.session_token',p_session_digest,true);
    SELECT job.tenant_id,job.org_id,job.id,job.case_id,job.run_id,job.attempt_id,job.actor_id,
        session.principal_id,session.membership_id,session.issuer,session.subject,
        membership.org_unit_id AS membership_org_id,membership.role_code,membership.grants,principal.actor_id AS principal_actor,
        principal.issuer AS principal_issuer,principal.subject AS principal_subject,
        case_row.owner_id,case_row.member_ids
    INTO bound
    FROM {{SCHEMA}}.sessions AS session
    JOIN {{SCHEMA}}.identity_principals AS principal ON principal.principal_id=session.principal_id
    JOIN {{SCHEMA}}.identity_memberships AS membership ON membership.membership_id=session.membership_id
        AND membership.principal_id=principal.principal_id AND membership.tenant_id=session.tenant_id
        AND membership.org_unit_id=session.org_id
    JOIN {{SCHEMA}}.identity_tenants AS tenant ON tenant.tenant_id=membership.tenant_id
    JOIN {{SCHEMA}}.identity_org_units AS unit ON unit.tenant_id=membership.tenant_id AND unit.org_unit_id=membership.org_unit_id
    JOIN {{SCHEMA}}.worker_jobs AS job ON job.tenant_id=session.tenant_id
        AND job.actor_id=principal.actor_id AND job.id=p_job_id
    LEFT JOIN {{SCHEMA}}.review_runs AS run ON run.tenant_id=job.tenant_id AND run.org_id=job.org_id
        AND run.case_id=job.case_id AND run.id=job.run_id
    LEFT JOIN {{SCHEMA}}.review_attempts AS attempt ON attempt.tenant_id=job.tenant_id AND attempt.org_id=job.org_id
        AND attempt.run_id=job.run_id AND attempt.id=job.attempt_id
    JOIN {{SCHEMA}}.cases AS case_row ON case_row.tenant_id=job.tenant_id AND case_row.org_id=job.org_id AND case_row.id=job.case_id
    WHERE session.token=p_session_digest AND session.audience='client' AND session.expires_at::timestamptz>now_at
      AND principal.status='active' AND membership.status='active' AND tenant.status='active' AND unit.status='active'
      AND session.issuer=principal.issuer AND session.subject=principal.subject
      AND membership.role_code IN ('admin','clerk','reviewer','viewer')
      AND (membership.effective_from IS NULL OR membership.effective_from<=now_at)
      AND (membership.effective_until IS NULL OR membership.effective_until>now_at)
      AND ((job.kind='review' AND run.state IN ('accepted','running') AND attempt.state IN ('accepted','running')
        AND attempt.history_kind IN ('preparing','frozen') AND (attempt.history_kind='preparing' OR attempt.input_hash IS NOT NULL))
        OR (job.kind='experience_distill' AND job.run_id IS NULL AND job.attempt_id IS NULL))
      AND case_row.archived_at IS NULL
      AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(membership.grants) AS grants(value)
        WHERE grants.value->>'role' IN ('case_owner','reviewer')
          AND (NULLIF(grants.value->>'expires_at','') IS NULL OR grants.value->>'expires_at'='0001-01-01T00:00:00Z' OR (grants.value->>'expires_at')::timestamptz>now_at)
          AND ((grants.value->>'scope'='self' AND case_row.owner_id=principal.actor_id)
            OR (grants.value->>'scope'='case' AND grants.value->>'case_id'=job.case_id)
            OR (grants.value->>'scope'='org' AND grants.value->>'org_id'=job.org_id)
            OR (grants.value->>'scope'='org_subtree' AND COALESCE(grants.value->'resolved_org_ids','[]'::jsonb) ? job.org_id)
            OR grants.value->>'scope'='tenant')
      );
    IF NOT FOUND THEN
        -- Explicit locally issued demonstration sessions retain their existing
        -- development-only path. Enterprise sessions can never enter it.
        SELECT job.tenant_id,job.org_id,job.id,job.case_id,job.run_id,job.attempt_id,job.actor_id,
            NULL::uuid AS principal_id,NULL::uuid AS membership_id,session.issuer,session.subject,
            NULL::text AS membership_org_id,NULL::text AS role_code,NULL::jsonb AS grants,job.actor_id AS principal_actor,
            ''::text AS principal_issuer,''::text AS principal_subject,case_row.owner_id,case_row.member_ids
        INTO bound
        FROM {{SCHEMA}}.sessions session JOIN {{SCHEMA}}.worker_jobs job
          ON job.actor_id=session.actor_id AND job.tenant_id=session.tenant_id AND job.org_id=session.org_id
        JOIN {{SCHEMA}}.cases case_row ON case_row.id=job.case_id AND case_row.tenant_id=job.tenant_id AND case_row.org_id=job.org_id
        LEFT JOIN {{SCHEMA}}.review_runs run ON run.id=job.run_id AND run.tenant_id=job.tenant_id AND run.org_id=job.org_id
        LEFT JOIN {{SCHEMA}}.review_attempts attempt ON attempt.id=job.attempt_id AND attempt.run_id=run.id AND attempt.tenant_id=job.tenant_id AND attempt.org_id=job.org_id
        WHERE session.token=p_session_digest AND session.audience='client' AND session.expires_at::timestamptz>now_at
          AND session.principal_id IS NULL AND session.membership_id IS NULL AND COALESCE(session.issuer,'')='' AND COALESCE(session.subject,'')=''
          AND job.tenant_id IN ('local-demo','local-demo-2') AND job.id=p_job_id AND case_row.archived_at IS NULL
          AND ((job.kind='review' AND run.state IN ('accepted','running') AND attempt.state IN ('accepted','running') AND attempt.history_kind IN ('preparing','frozen') AND (attempt.history_kind='preparing' OR attempt.input_hash IS NOT NULL))
            OR (job.kind='experience_distill' AND job.run_id IS NULL AND job.attempt_id IS NULL));
        IF NOT FOUND THEN RAISE EXCEPTION 'current session cannot admit this review job' USING ERRCODE='42501'; END IF;
    END IF;
    IF bound.principal_actor<>bound.actor_id OR bound.issuer<>bound.principal_issuer OR bound.subject<>bound.principal_subject THEN
        RAISE EXCEPTION 'current session cannot admit this review job' USING ERRCODE='42501';
    END IF;
    INSERT INTO {{SCHEMA}}.worker_claim_identities
        (tenant_id,job_id,org_id,case_id,run_id,attempt_id,actor_id,principal_id,membership_id,membership_org_id)
    VALUES (bound.tenant_id,bound.id,bound.org_id,bound.case_id,COALESCE(bound.run_id,''),COALESCE(bound.attempt_id,''),
        bound.actor_id,bound.principal_id,bound.membership_id,bound.membership_org_id)
    ON CONFLICT (tenant_id,job_id) DO NOTHING;
    SELECT tenant_id,job_id,org_id,case_id,run_id,attempt_id,actor_id,principal_id,membership_id,membership_org_id
    INTO existing FROM {{SCHEMA}}.worker_claim_identities
    WHERE tenant_id=bound.tenant_id AND job_id=bound.id;
    IF NOT FOUND OR existing.org_id IS DISTINCT FROM bound.org_id OR existing.case_id IS DISTINCT FROM bound.case_id
       OR existing.run_id IS DISTINCT FROM COALESCE(bound.run_id,'') OR existing.attempt_id IS DISTINCT FROM COALESCE(bound.attempt_id,'')
       OR existing.actor_id IS DISTINCT FROM bound.actor_id OR existing.principal_id IS DISTINCT FROM bound.principal_id
       OR existing.membership_id IS DISTINCT FROM bound.membership_id OR existing.membership_org_id IS DISTINCT FROM bound.membership_org_id THEN
        RAISE EXCEPTION 'review job identity binding conflicts with its immutable record' USING ERRCODE='23505';
    END IF;
END
$function$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_oidc_login(text,text,text,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_list_pending(text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_consume_pending(text,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_session_user(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_intake_ticket(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_claim_user(text,text,text,text,bigint,text,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_bind_admission(text,text) FROM PUBLIC;

DO $ownership$
DECLARE
    owner_role text := 'caw_auth_fence_' || md5(current_database() || ':' || current_schema());
BEGIN
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_oidc_login(text,text,text,text,text,text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_list_pending(text,text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_consume_pending(text,uuid,text,text,text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_session_user(text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_intake_ticket(text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_claim_user(text,text,text,text,bigint,text,text,text,text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('ALTER FUNCTION %I.tenant_identity_bind_admission(text,text) OWNER TO %I', current_schema(), owner_role);
    EXECUTE format('REVOKE CREATE ON SCHEMA %I FROM %I', current_schema(), owner_role);
END
$ownership$;

SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_oidc_login(text,text,text,text,text,text) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_list_pending(text,text) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_consume_pending(text,uuid,text,text,text) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_session_user(text) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_intake_ticket(text) FROM SESSION_USER;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_identity_bind_admission(text,text) FROM SESSION_USER;
RESET ROLE;

-- Case-only mutations do not change authority rows. Shared locks let them run
-- concurrently while still conflicting with identity changes and logout.
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
CREATE FUNCTION {{SCHEMA}}.tenant_case_context_fence(p_digest text,p_version bigint,p_revision text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE hint record; actual record;
BEGIN
 PERFORM set_config('app.session_token',p_digest,true);
 SELECT s.token,s.tenant_id,s.membership_id INTO hint FROM {{SCHEMA}}.sessions s WHERE s.token=p_digest;
 IF NOT FOUND OR p_version IS NULL OR p_version<1 THEN RAISE EXCEPTION 'session_invalid' USING ERRCODE='28000'; END IF;
 PERFORM t.tenant_id FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id=hint.tenant_id FOR SHARE;
 PERFORM m.membership_id FROM {{SCHEMA}}.identity_memberships m WHERE m.membership_id=hint.membership_id FOR SHARE;
 PERFORM s.token FROM {{SCHEMA}}.sessions s WHERE s.token=p_digest FOR SHARE;
 SELECT * INTO actual FROM {{SCHEMA}}.tenant_context_info(p_digest);
 IF NOT FOUND THEN RAISE EXCEPTION 'session_invalid' USING ERRCODE='28000'; END IF;
 IF actual.context_version<>p_version OR p_revision IS NULL OR p_revision<>actual.authority_revision THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001'; END IF;
 PERFORM set_config('app.command_digest',p_digest,true); PERFORM set_config('app.command_version',p_version::text,true);
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_case_context_fence(text,bigint,text) FROM PUBLIC;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};

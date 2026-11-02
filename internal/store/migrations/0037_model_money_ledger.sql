-- Exact USD micro accounting for model calls. Price snapshots are installed by
-- an explicitly trusted platform principal; absence means unconfigured.
CREATE TABLE {{SCHEMA}}.model_price_snapshots (
    snapshot_id text PRIMARY KEY,
    provider_config_id text NOT NULL,
    model_ref text NOT NULL,
    currency char(3) NOT NULL CHECK (currency='USD'),
    minor_unit_scale smallint NOT NULL CHECK (minor_unit_scale=6),
    input_micro_usd_per_million numeric(30,0) NOT NULL CHECK (input_micro_usd_per_million>=0),
    output_micro_usd_per_million numeric(30,0) NOT NULL CHECK (output_micro_usd_per_million>=0),
    published_by text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    UNIQUE(provider_config_id,model_ref,snapshot_id)
);
CREATE INDEX model_price_snapshots_lookup ON {{SCHEMA}}.model_price_snapshots(provider_config_id,model_ref,published_at DESC);
REVOKE ALL ON {{SCHEMA}}.model_price_snapshots FROM PUBLIC;

CREATE TABLE {{SCHEMA}}.model_money_limits (
    scope text NOT NULL CHECK (scope IN ('global','tenant')),
    tenant_id text,
    day_limit_minor numeric(30,0) CHECK (day_limit_minor>=0),
    month_limit_minor numeric(30,0) CHECK (month_limit_minor>=0),
    revision bigint NOT NULL CHECK (revision>0),
    timezone text NOT NULL DEFAULT 'UTC' CHECK (timezone='UTC'),
    updated_by text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((scope='global' AND tenant_id IS NULL) OR (scope='tenant' AND tenant_id IS NOT NULL AND tenant_id<>'')),
    UNIQUE NULLS NOT DISTINCT(scope,tenant_id)
);
ALTER TABLE {{SCHEMA}}.model_money_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.model_money_limits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.model_money_limits
    USING (tenant_id=current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
REVOKE ALL ON {{SCHEMA}}.model_money_limits FROM PUBLIC;

CREATE TABLE {{SCHEMA}}.model_money_ledger (
    event_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    lease_id text NOT NULL,
    event_kind text NOT NULL CHECK(event_kind IN ('reserve','release','estimated_cost','observed_cost','unknown_hold','adjustment','reconciliation')),
    period_day date NOT NULL,
    period_month date NOT NULL,
    currency char(3) NOT NULL CHECK(currency='USD'),
    minor_unit_scale smallint NOT NULL CHECK(minor_unit_scale=6),
    amount_minor numeric(60,0) NOT NULL,
    price_snapshot_id text,
    policy_revision bigint NOT NULL DEFAULT 0,
    source_event_id text NOT NULL,
    payload_sha256 text NOT NULL CHECK(payload_sha256 ~ '^[0-9a-f]{64}$'),
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(tenant_id,lease_id,event_kind,source_event_id)
);
ALTER TABLE {{SCHEMA}}.model_money_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.model_money_ledger FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.model_money_ledger USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
CREATE INDEX model_money_ledger_tenant_day ON {{SCHEMA}}.model_money_ledger(tenant_id,period_day);
CREATE INDEX model_money_ledger_tenant_month ON {{SCHEMA}}.model_money_ledger(tenant_id,period_month);
REVOKE ALL ON {{SCHEMA}}.model_money_ledger FROM PUBLIC;
CREATE OR REPLACE FUNCTION {{SCHEMA}}.reject_model_money_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $function$
BEGIN RAISE EXCEPTION 'model money ledger and prices are immutable' USING ERRCODE='55000'; END
$function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.reject_model_money_mutation() FROM PUBLIC;
CREATE TRIGGER model_money_ledger_immutable BEFORE UPDATE OR DELETE ON {{SCHEMA}}.model_money_ledger FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.reject_model_money_mutation();
CREATE TRIGGER model_price_snapshots_immutable BEFORE UPDATE OR DELETE ON {{SCHEMA}}.model_price_snapshots FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.reject_model_money_mutation();

ALTER TABLE {{SCHEMA}}.model_call_leases
    ADD COLUMN money_reserved_minor numeric(60,0),
    ADD COLUMN money_price_snapshot_id text,
    ADD COLUMN money_provider_config_id text,
    ADD COLUMN money_currency char(3) NOT NULL DEFAULT 'USD' CHECK(money_currency='USD'),
    ADD COLUMN money_minor_unit_scale smallint NOT NULL DEFAULT 6 CHECK(money_minor_unit_scale=6),
    ADD COLUMN money_timezone text NOT NULL DEFAULT 'UTC' CHECK(money_timezone='UTC'),
    ADD COLUMN money_period_day date,
    ADD COLUMN money_period_month date,
    ADD COLUMN money_policy_revision bigint NOT NULL DEFAULT 0;
ALTER TABLE {{SCHEMA}}.model_usage
    ADD COLUMN money_cost_minor numeric(60,0),
    ADD COLUMN money_reserved_minor numeric(60,0),
    ADD COLUMN money_price_snapshot_id text,
    ADD COLUMN money_provider_config_id text,
    ADD COLUMN money_currency char(3) NOT NULL DEFAULT 'USD' CHECK(money_currency='USD'),
    ADD COLUMN money_minor_unit_scale smallint NOT NULL DEFAULT 6 CHECK(money_minor_unit_scale=6),
    ADD COLUMN money_timezone text NOT NULL DEFAULT 'UTC' CHECK(money_timezone='UTC'),
    ADD COLUMN money_period_day date,
    ADD COLUMN money_period_month date,
    ADD COLUMN money_cost_state text NOT NULL DEFAULT 'unconfigured'
      CHECK (money_cost_state IN ('unconfigured','estimated','unknown_hold','pre_dispatch_free'));

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_money_reserve() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $function$
DECLARE
  v_now timestamptz := clock_timestamp(); v_price record; v_cost numeric;
  v_global_day numeric; v_global_month numeric; v_tenant_day numeric; v_tenant_month numeric;
  v_global_day_limit numeric; v_global_month_limit numeric; v_tenant_day_limit numeric; v_tenant_month_limit numeric;
  v_revision bigint := 0; v_configured boolean := false; v_tenant_revision bigint := 0;
  v_provider text := coalesce(nullif(current_setting('app.model_provider_identity',true),''),'');
  v_day date; v_month date;
BEGIN
  -- All money accounting takes the same fixed lock order after the existing
  -- global model quota lock: global window, then tenant window.
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:global',0));
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:tenant:'||NEW.tenant_id,0));
  v_day := (v_now AT TIME ZONE 'UTC')::date; v_month := date_trunc('month',v_now AT TIME ZONE 'UTC')::date;
  NEW.money_period_day := v_day; NEW.money_period_month := v_month;
  NEW.money_provider_config_id := nullif(v_provider,'');
  SELECT * INTO v_price FROM {{SCHEMA}}.model_price_snapshots p
    WHERE p.provider_config_id=v_provider AND p.model_ref=NEW.model_ref
    ORDER BY p.published_at DESC,p.snapshot_id DESC LIMIT 1;
  SELECT day_limit_minor,month_limit_minor,revision INTO v_global_day_limit,v_global_month_limit,v_revision
    FROM {{SCHEMA}}.model_money_limits WHERE scope='global' AND tenant_id IS NULL;
  SELECT day_limit_minor,month_limit_minor,revision INTO v_tenant_day_limit,v_tenant_month_limit,v_tenant_revision
    FROM {{SCHEMA}}.model_money_limits WHERE scope='tenant' AND tenant_id=NEW.tenant_id;
  v_configured := v_global_day_limit IS NOT NULL OR v_global_month_limit IS NOT NULL OR
      v_tenant_day_limit IS NOT NULL OR v_tenant_month_limit IS NOT NULL;
  IF NOT v_configured AND v_price.snapshot_id IS NULL THEN
    -- No price and no hard amount cap: admit under existing token controls,
    -- but explicitly retain an unconfigured cost state.
    NEW.money_policy_revision := 0;
    RETURN NEW;
  END IF;
  IF v_price.snapshot_id IS NULL THEN
    RAISE EXCEPTION 'trusted model price is unavailable under configured money limit' USING ERRCODE='P7M02';
  END IF;
  v_cost := ceil((NEW.reserved_input_tokens::numeric*v_price.input_micro_usd_per_million+
      NEW.reserved_output_tokens::numeric*v_price.output_micro_usd_per_million)/1000000);
  SELECT coalesce(sum(amount_minor),0) INTO v_global_day FROM {{SCHEMA}}.model_money_ledger WHERE period_day=v_day;
  SELECT coalesce(sum(amount_minor),0) INTO v_global_month FROM {{SCHEMA}}.model_money_ledger WHERE period_month=v_month;
  SELECT coalesce(sum(amount_minor),0) INTO v_tenant_day FROM {{SCHEMA}}.model_money_ledger WHERE tenant_id=NEW.tenant_id AND period_day=v_day;
  SELECT coalesce(sum(amount_minor),0) INTO v_tenant_month FROM {{SCHEMA}}.model_money_ledger WHERE tenant_id=NEW.tenant_id AND period_month=v_month;
  IF (v_global_day_limit IS NOT NULL AND v_global_day+v_cost>v_global_day_limit) OR
     (v_global_month_limit IS NOT NULL AND v_global_month+v_cost>v_global_month_limit) OR
     (v_tenant_day_limit IS NOT NULL AND v_tenant_day+v_cost>v_tenant_day_limit) OR
     (v_tenant_month_limit IS NOT NULL AND v_tenant_month+v_cost>v_tenant_month_limit) THEN
    RAISE EXCEPTION 'model money budget is exhausted' USING ERRCODE='P7M03';
  END IF;
  NEW.money_reserved_minor:=v_cost; NEW.money_price_snapshot_id:=v_price.snapshot_id;
  NEW.money_policy_revision:=greatest(coalesce(v_revision,0),coalesce(v_tenant_revision,0));
  INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,price_snapshot_id,policy_revision,source_event_id,payload_sha256,reason)
  VALUES('mmr_'||NEW.lease_id,NEW.tenant_id,NEW.lease_id,'reserve',v_day,v_month,'USD',6,v_cost,v_price.snapshot_id,NEW.money_policy_revision,'reserve',repeat('0',64),'conservative token reserve');
  RETURN NEW;
END $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_money_reserve() FROM PUBLIC;
CREATE TRIGGER model_money_reserve BEFORE INSERT ON {{SCHEMA}}.model_call_leases
  FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.model_money_reserve();

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_money_settle() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $function$
DECLARE
  v_lease record; v_cost numeric; v_reserved numeric; v_snapshot text; v_provider text; v_period_day date; v_period_month date;
  v_state text := 'unknown_hold'; v_day date; v_month date;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:global',0));
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:tenant:'||NEW.tenant_id,0));
  SELECT * INTO v_lease FROM {{SCHEMA}}.model_call_leases WHERE lease_id=NEW.lease_id AND tenant_id=NEW.tenant_id;
  IF NOT FOUND THEN
    -- Expired-lease conversion is performed inside the reserve function and
    -- may have deleted the source lease; preserve its unresolved hold.
    NEW.money_period_day:=coalesce((NEW.reserved_at AT TIME ZONE 'UTC')::date,(NEW.occurred_at AT TIME ZONE 'UTC')::date);
    NEW.money_period_month:=date_trunc('month',NEW.money_period_day)::date;
    SELECT l.amount_minor,l.price_snapshot_id,l.period_day,l.period_month,p.provider_config_id INTO v_reserved,v_snapshot,v_period_day,v_period_month,v_provider
      FROM {{SCHEMA}}.model_money_ledger l LEFT JOIN {{SCHEMA}}.model_price_snapshots p ON p.snapshot_id=l.price_snapshot_id
      WHERE l.tenant_id=NEW.tenant_id AND l.lease_id=NEW.lease_id AND l.event_kind='reserve' LIMIT 1;
    IF FOUND THEN NEW.money_reserved_minor:=v_reserved; NEW.money_price_snapshot_id:=v_snapshot; NEW.money_provider_config_id:=v_provider; NEW.money_period_day:=v_period_day; NEW.money_period_month:=v_period_month; END IF;
    NEW.money_cost_state:='unknown_hold'; RETURN NEW;
  END IF;
  v_day:=v_lease.money_period_day; v_month:=v_lease.money_period_month;
  NEW.money_period_day:=v_day; NEW.money_period_month:=v_month;
  NEW.money_reserved_minor:=v_lease.money_reserved_minor;
  NEW.money_price_snapshot_id:=v_lease.money_price_snapshot_id;
  NEW.money_provider_config_id:=v_lease.money_provider_config_id;
  IF NEW.budget_released THEN
    v_state:='pre_dispatch_free'; v_cost:=0;
    IF v_lease.money_reserved_minor IS NOT NULL THEN
      INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,price_snapshot_id,source_event_id,payload_sha256,reason)
      VALUES('mmf_'||NEW.lease_id,NEW.tenant_id,NEW.lease_id,'release',v_day,v_month,'USD',6,-v_lease.money_reserved_minor,v_lease.money_price_snapshot_id,'pre-dispatch',NEW.settlement_fingerprint,'proven pre-dispatch failure');
    END IF;
  ELSIF v_lease.money_reserved_minor IS NOT NULL AND NEW.input_tokens IS NOT NULL AND NEW.output_tokens IS NOT NULL THEN
    SELECT ceil((NEW.input_tokens::numeric*p.input_micro_usd_per_million+NEW.output_tokens::numeric*p.output_micro_usd_per_million)/1000000)
      INTO v_cost FROM {{SCHEMA}}.model_price_snapshots p WHERE p.snapshot_id=v_lease.money_price_snapshot_id;
    IF v_cost IS NOT NULL THEN
      v_state:='estimated';
      INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,price_snapshot_id,source_event_id,payload_sha256,reason)
      VALUES('mmf_'||NEW.lease_id,NEW.tenant_id,NEW.lease_id,'release',v_day,v_month,'USD',6,-v_lease.money_reserved_minor,v_lease.money_price_snapshot_id,'settle',NEW.settlement_fingerprint,'release unused reserve');
      INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,price_snapshot_id,source_event_id,payload_sha256,reason)
      VALUES('mmc_'||NEW.lease_id,NEW.tenant_id,NEW.lease_id,'estimated_cost',v_day,v_month,'USD',6,v_cost,v_lease.money_price_snapshot_id,'settle',NEW.settlement_fingerprint,'provider usage multiplied by frozen price');
    END IF;
  END IF;
  NEW.money_cost_minor:=v_cost; NEW.money_cost_state:=v_state;
  RETURN NEW;
END $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_money_settle() FROM PUBLIC;
CREATE TRIGGER model_money_settle BEFORE INSERT ON {{SCHEMA}}.model_usage
  FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.model_money_settle();

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_money_publish_price(
  p_snapshot_id text,p_provider_config_id text,p_model_ref text,p_input_rate numeric,p_output_rate numeric,
  p_payload_sha256 text,p_actor text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $function$
BEGIN
  IF current_setting('app.platform_operator_actor',true) IS NULL OR current_setting('app.platform_operator_actor',true)<>p_actor OR p_actor='' OR
     p_snapshot_id='' OR p_provider_config_id='' OR p_model_ref='' OR p_input_rate<0 OR p_output_rate<0 OR
     p_payload_sha256 !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION 'platform operator authorization or price is invalid' USING ERRCODE='42501';
  END IF;
  INSERT INTO {{SCHEMA}}.model_price_snapshots(snapshot_id,provider_config_id,model_ref,currency,minor_unit_scale,
      input_micro_usd_per_million,output_micro_usd_per_million,published_by,payload_sha256)
  VALUES(p_snapshot_id,p_provider_config_id,p_model_ref,'USD',6,p_input_rate,p_output_rate,p_actor,p_payload_sha256);
END $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_money_publish_price(text,text,text,numeric,numeric,text,text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_money_publish_global(
  p_expected_revision bigint,p_day_limit numeric,p_month_limit numeric,p_actor text) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $function$
DECLARE v_revision bigint;
BEGIN
  IF current_setting('app.platform_operator_actor',true) IS NULL OR current_setting('app.platform_operator_actor',true)<>p_actor OR p_actor='' OR
     p_expected_revision<0 OR p_day_limit<0 OR p_month_limit<0 THEN
    RAISE EXCEPTION 'platform operator authorization or global cap is invalid' USING ERRCODE='42501';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:global',0));
  INSERT INTO {{SCHEMA}}.model_money_limits(scope,tenant_id,day_limit_minor,month_limit_minor,revision,timezone,updated_by)
    VALUES('global',NULL,p_day_limit,p_month_limit,1,'UTC',p_actor)
    ON CONFLICT(scope,tenant_id) DO UPDATE SET day_limit_minor=EXCLUDED.day_limit_minor,month_limit_minor=EXCLUDED.month_limit_minor,
      revision={{SCHEMA}}.model_money_limits.revision+1,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()
    WHERE {{SCHEMA}}.model_money_limits.revision=p_expected_revision RETURNING revision INTO v_revision;
  IF v_revision IS NULL THEN RAISE EXCEPTION 'global money policy revision conflict' USING ERRCODE='P7M04'; END IF;
  RETURN v_revision;
END $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_money_publish_global(bigint,numeric,numeric,text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_money_global_control()
RETURNS TABLE(revision bigint,day_limit_minor text,month_limit_minor text,price_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $function$
BEGIN
  IF current_setting('app.platform_operator_actor',true) IS NULL OR current_setting('app.platform_operator_actor',true)='' THEN
    RAISE EXCEPTION 'platform operator authorization is required' USING ERRCODE='42501';
  END IF;
  RETURN QUERY SELECT coalesce(l.revision,0),l.day_limit_minor::text,l.month_limit_minor::text,
    (SELECT count(*) FROM {{SCHEMA}}.model_price_snapshots)
    FROM (SELECT 1) x LEFT JOIN {{SCHEMA}}.model_money_limits l ON l.scope='global' AND l.tenant_id IS NULL;
END $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_money_global_control() FROM PUBLIC;

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_money_adjust_tenant(
  p_tenant text,p_lease text,p_kind text,p_amount numeric,p_key text,p_digest text,p_reason text,p_actor text)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $function$
DECLARE v_day date; v_month date; v_existing record; v_event text := 'mma_'||md5(p_tenant||':'||p_lease||':'||p_kind||':'||p_key);
        v_open_reserve numeric; v_estimated numeric; v_observed numeric; v_delta numeric; v_cost_state text; v_has_observed boolean;
BEGIN
  IF p_tenant IS NULL OR p_tenant='' OR p_tenant<>current_setting('app.tenant_id',true) OR
     p_actor IS NULL OR p_actor='' OR p_actor<>current_setting('app.actor_id',true) OR
     p_kind NOT IN ('adjustment','reconciliation') OR p_lease IS NULL OR p_lease='' OR
     p_key IS NULL OR p_key='' OR length(p_key)>160 OR p_reason IS NULL OR length(btrim(p_reason))<8 OR length(p_reason)>1000 OR
     p_digest !~ '^[0-9a-f]{64}$' OR p_amount IS NULL THEN
    RAISE EXCEPTION 'invalid tenant money adjustment' USING ERRCODE='22023';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:global',0));
  PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:tenant:'||p_tenant,0));
  SELECT period_day,period_month INTO v_day,v_month FROM {{SCHEMA}}.model_money_ledger
    WHERE tenant_id=p_tenant AND lease_id=p_lease AND event_kind='reserve' LIMIT 1;
  IF v_day IS NULL OR v_month IS NULL THEN RAISE EXCEPTION 'original priced model call is unavailable' USING ERRCODE='P7M05'; END IF;
  SELECT * INTO v_existing FROM {{SCHEMA}}.model_money_ledger WHERE tenant_id=p_tenant AND lease_id=p_lease AND event_kind=p_kind AND source_event_id=p_key;
  IF NOT FOUND AND p_kind='reconciliation' THEN
    SELECT * INTO v_existing FROM {{SCHEMA}}.model_money_ledger WHERE tenant_id=p_tenant AND lease_id=p_lease AND event_kind='observed_cost' AND source_event_id=p_key;
  END IF;
  IF FOUND THEN
    IF v_existing.payload_sha256=p_digest THEN RETURN v_existing.event_id; END IF;
    RAISE EXCEPTION 'money adjustment idempotency key conflicts' USING ERRCODE='P7M06';
  END IF;
  IF p_kind='reconciliation' THEN
    SELECT coalesce(sum(amount_minor) FILTER(WHERE event_kind IN ('reserve','release')),0),
      coalesce(sum(amount_minor) FILTER(WHERE event_kind='estimated_cost'),0),
      coalesce(sum(amount_minor) FILTER(WHERE event_kind IN ('observed_cost','reconciliation')),0),
      coalesce(bool_or(event_kind='observed_cost'),false)
      INTO v_open_reserve,v_estimated,v_observed,v_has_observed FROM {{SCHEMA}}.model_money_ledger WHERE tenant_id=p_tenant AND lease_id=p_lease;
    SELECT money_cost_state INTO v_cost_state FROM {{SCHEMA}}.model_usage WHERE tenant_id=p_tenant AND lease_id=p_lease;
    IF v_cost_state='unknown_hold' AND v_open_reserve>0 THEN
      INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,source_event_id,payload_sha256,reason)
        VALUES('mmr_'||md5(v_event||':release'),p_tenant,p_lease,'release',v_day,v_month,'USD',6,-v_open_reserve,'reconcile-release:'||p_key,p_digest,'release unknown hold against trusted billing evidence');
    END IF;
    IF NOT v_has_observed THEN
      IF v_estimated<>0 THEN
        INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,price_snapshot_id,source_event_id,payload_sha256,reason)
          VALUES('mme_'||md5(v_event||':estimate'),p_tenant,p_lease,'estimated_cost',v_day,v_month,'USD',6,-v_estimated,NULL,'reconcile-estimate:'||p_key,p_digest,'reverse estimate replaced by trusted billed cost');
      END IF;
      INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,source_event_id,payload_sha256,reason)
        VALUES('mmo_'||md5(v_event||':observed'),p_tenant,p_lease,'observed_cost',v_day,v_month,'USD',6,p_amount,p_key,p_digest,'trusted billing evidence');
    ELSE
      v_delta:=p_amount-v_observed;
      INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,source_event_id,payload_sha256,reason)
        VALUES(v_event,p_tenant,p_lease,'reconciliation',v_day,v_month,'USD',6,v_delta,p_key,p_digest,btrim(p_reason));
    END IF;
    RETURN v_event;
  END IF;
  INSERT INTO {{SCHEMA}}.model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,source_event_id,payload_sha256,reason)
    VALUES(v_event,p_tenant,p_lease,p_kind,v_day,v_month,'USD',6,p_amount,p_key,p_digest,btrim(p_reason));
  RETURN v_event;
END $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_money_adjust_tenant(text,text,text,numeric,text,text,text,text) FROM PUBLIC;

DO $acl$
DECLARE owner_name text;
BEGIN
  SELECT r.rolname INTO owner_name FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner
    WHERE n.nspname=current_schema() AND p.proname='model_budget_reserve' LIMIT 1;
  IF owner_name IS NULL THEN RAISE EXCEPTION 'model budget accounting owner is unavailable'; END IF;
  EXECUTE format('DROP POLICY IF EXISTS model_money_accounting_authority ON %I.model_money_ledger',current_schema());
  EXECUTE format('CREATE POLICY model_money_accounting_authority ON %I.model_money_ledger USING (current_user=%L) WITH CHECK (current_user=%L)',current_schema(),owner_name,owner_name);
  EXECUTE format('DROP POLICY IF EXISTS model_money_accounting_authority ON %I.model_money_limits',current_schema());
  EXECUTE format('CREATE POLICY model_money_accounting_authority ON %I.model_money_limits USING (current_user=%L) WITH CHECK (current_user=%L)',current_schema(),owner_name,owner_name);
END $acl$;

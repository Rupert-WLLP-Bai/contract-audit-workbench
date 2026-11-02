#!/usr/bin/env bash
# Create the isolated review database and the non-superuser model role used by
# the database and browser test suites. Idempotent: safe to run repeatedly.
set -euo pipefail

: "${PGUSER:?PGUSER is required}"
: "${PGPASSWORD:?PGPASSWORD is required}"
PGHOST="${PGHOST:-127.0.0.1}"
PGPORT="${PGPORT:-5432}"
TEST_DB="${TEST_DB:-contract_audit_test}"
MODEL_ROLE="${MODEL_ROLE:-contract_audit_model}"
MODEL_PASSWORD="${MODEL_PASSWORD:-$PGPASSWORD}"
export PGPASSWORD PGUSER PGHOST PGPORT

validate_identifier() {
  local value="$1" label="$2"
  if [[ ! "$value" =~ ^[a-z_][a-z0-9_]*$ ]]; then
    echo "invalid ${label}: must match [a-z_][a-z0-9_]*" >&2
    exit 2
  fi
}

validate_identifier "$TEST_DB" "TEST_DB"
validate_identifier "$MODEL_ROLE" "MODEL_ROLE"

psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d postgres -v ON_ERROR_STOP=1 \
  -v test_db="$TEST_DB" -v model_role="$MODEL_ROLE" -v model_secret="$MODEL_PASSWORD" <<'SQL'
SELECT format('CREATE DATABASE %I', :'test_db')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'test_db') \gexec
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE',
              :'model_role', :'model_secret')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'model_role') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'test_db', :'model_role') \gexec
SQL

echo "provisioned database ${TEST_DB} and role ${MODEL_ROLE}"

# Exercise the supported offline AUTH_FENCE CLI against a new disposable
# database, not an application's schema or pre-existing owner. The fast gate
# invokes this script only after its database=true plan has prepared Go.
(
  smoke_repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
  smoke_suffix="$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
  smoke_db="caw_auth_smoke_${smoke_suffix}_test"
  smoke_schema="caw_auth_${smoke_suffix}"
  smoke_migrator="caw_auth_mig_${smoke_suffix}"
  smoke_password="$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
  smoke_owner="$(python3 -c 'import hashlib,sys; print("caw_auth_fence_"+hashlib.md5((sys.argv[1]+":"+sys.argv[2]).encode()).hexdigest())' "$smoke_db" "$smoke_schema")"
  smoke_clm_owner="$(python3 -c 'import hashlib,sys; print("caw_clm_maint_"+hashlib.md5((sys.argv[1]+":"+sys.argv[2]).encode()).hexdigest())' "$smoke_db" "$smoke_schema")"
  validate_identifier "$smoke_db" "scratch database"
  validate_identifier "$smoke_schema" "scratch schema"
  validate_identifier "$smoke_migrator" "scratch migrator"
  validate_identifier "$smoke_owner" "scratch owner"
  occupied="$(psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d postgres -At -v ON_ERROR_STOP=1 \
    -v smoke_db="$smoke_db" -v smoke_migrator="$smoke_migrator" -v smoke_owner="$smoke_owner" -v smoke_clm_owner="$smoke_clm_owner" <<'SQL'
SELECT (SELECT count(*) FROM pg_database WHERE datname=:'smoke_db')+
       (SELECT count(*) FROM pg_roles WHERE rolname IN (:'smoke_migrator',:'smoke_owner',:'smoke_clm_owner'));
SQL
  )"
  if [[ "$occupied" != "0" ]]; then
    echo "refusing AUTH_FENCE CLI smoke: scratch identity already exists" >&2
    exit 2
  fi
  cleanup_auth_smoke() {
    smoke_status=$?
    trap - EXIT
    # Every name below was proved absent before this subshell created it.
    psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d postgres -v ON_ERROR_STOP=1 \
      -v smoke_db="$smoke_db" -v smoke_owner="$smoke_owner" -v smoke_migrator="$smoke_migrator" -v smoke_clm_owner="$smoke_clm_owner" <<'SQL' >/dev/null
SELECT format('DROP DATABASE IF EXISTS %I WITH (FORCE)', :'smoke_db') \gexec
SELECT format('DROP ROLE IF EXISTS %I', :'smoke_clm_owner') \gexec
SELECT format('DROP ROLE IF EXISTS %I', :'smoke_owner') \gexec
SELECT format('DROP ROLE IF EXISTS %I', :'smoke_migrator') \gexec
SQL
    cleanup_status=$?
    if [[ "$cleanup_status" != "0" ]]; then
      echo "AUTH_FENCE CLI scratch cleanup failed" >&2
      exit 1
    fi
    exit "$smoke_status"
  }
  trap cleanup_auth_smoke EXIT
  psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d postgres -v ON_ERROR_STOP=1 \
    -v smoke_db="$smoke_db" -v smoke_migrator="$smoke_migrator" -v smoke_secret="$smoke_password" <<'SQL' >/dev/null
SELECT format('CREATE DATABASE %I', :'smoke_db') \gexec
SQL
  # URI components are encoded by Python. Credentials remain private process
  # environment and are never echoed or added to the command arguments.
  export CAW_AUTH_SMOKE_DB="$smoke_db" CAW_AUTH_SMOKE_MIGRATOR="$smoke_migrator" CAW_AUTH_SMOKE_PASSWORD="$smoke_password"
  auth_admin_dsn="$(python3 - <<'PY'
import os,urllib.parse
q=lambda s:urllib.parse.quote(s,safe='')
host=os.environ['PGHOST']
if ':' in host and not host.startswith('['): host='['+host+']'
print('postgres://'+q(os.environ['PGUSER'])+':'+q(os.environ['PGPASSWORD'])+'@'+host+':'+os.environ['PGPORT']+'/'+q(os.environ['CAW_AUTH_SMOKE_DB'])+'?sslmode=disable')
PY
  )"
  auth_migrator_dsn="$(python3 - <<'PY'
import os,urllib.parse
q=lambda s:urllib.parse.quote(s,safe='')
host=os.environ['PGHOST']
if ':' in host and not host.startswith('['): host='['+host+']'
print('postgres://'+q(os.environ['CAW_AUTH_SMOKE_MIGRATOR'])+':'+q(os.environ['CAW_AUTH_SMOKE_PASSWORD'])+'@'+host+':'+os.environ['PGPORT']+'/'+q(os.environ['CAW_AUTH_SMOKE_DB'])+'?sslmode=disable')
PY
  )"
  export APP_ENV=test DATABASE_SCHEMA="$smoke_schema" DEV_DATABASE_URL= PROD_DATABASE_URL= \
    TEST_DATABASE_URL="$auth_admin_dsn" AUTH_ADMIN_DATABASE_URL="$auth_admin_dsn" CLM_ADMIN_DATABASE_URL="$auth_admin_dsn" MIGRATOR_DATABASE_URL="$auth_migrator_dsn"
  for smoke_service in CORE DOCUMENT EXECUTION MODEL TOOL; do
    smoke_runtime_dsn="$(CAW_AUTH_SMOKE_SERVICE="$smoke_service" python3 - <<'PYDSN'
import os,urllib.parse
u=urllib.parse.urlsplit(os.environ['MIGRATOR_DATABASE_URL'])
name='caw_'+os.environ['CAW_AUTH_SMOKE_SERVICE'].lower()+'_'+os.environ['CAW_AUTH_SMOKE_MIGRATOR'].removeprefix('caw_auth_mig_')
print(urllib.parse.urlunsplit((u.scheme,urllib.parse.quote(name,safe='')+':'+urllib.parse.quote(os.environ['CAW_AUTH_SMOKE_PASSWORD'],safe='')+'@'+u.netloc.rsplit('@',1)[1],u.path,u.query,u.fragment)))
PYDSN
    )"
    export "${smoke_service}_DATABASE_URL=$smoke_runtime_dsn"
  done
  go -C "$smoke_repo_root" run ./cmd/migrate provision-offline-migrator
  go -C "$smoke_repo_root" run ./cmd/migrate provision-clm-maintenance
  go -C "$smoke_repo_root" run ./cmd/migrate provision-tenant-identity
  echo "verified offline migrator and both bounded owner provisioning CLIs in an isolated scratch database"
)

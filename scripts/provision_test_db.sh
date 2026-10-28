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
export PGPASSWORD

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

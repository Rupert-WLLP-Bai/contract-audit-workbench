#!/usr/bin/env python3
"""Back up one running local source environment and restore it into a NEW,
isolated local destination (new database name, new object bucket and/or new
private object root).

The source is never written and nothing is ever dropped. The destination is a
fresh database inside the same PostgreSQL cluster -- so the migrator and the
five dedicated service roles already exist at cluster level and grants, RLS
policies, append-only triggers, ownership and sequence state restore faithfully
-- plus a fresh private bucket and/or directory that holds exactly the objects
the dumped snapshot references, under their frozen keys.

Consistency: an exported read-only REPEATABLE READ snapshot is held open for the
whole source capture. `pg_dump --snapshot` and every source comparison query use
that one snapshot, so the source baseline and the dump describe the same
instant even while the source keeps running. Object references are enumerated
from that same snapshot; only those objects are copied. pg_dump itself fails
loudly when the connection role cannot bypass FORCE RLS, so an incomplete dump
is impossible; a dedicated migrator/BYPASSRLS authority is sufficient, a
superuser is not required.

Nothing starts for the destination: no worker, no relay, no model call. Only
psql/pg_dump/pg_restore plus an S3 or filesystem copy run. Verification compares
source and destination identity by identity (row count plus a streamed SHA-256
over canonical rows in primary-key order per table), relationship joins,
referenced original bytes under their frozen IDs, recomputed report digests, and
a read-only exercise of the restored service-role grants and FORCE RLS in the
fresh database.

Honest limits are recorded in the manifest: this proves local BACKUP_RESTORE
only. It is not RPO/RTO, not quiesced-cluster backup, not multi-node HA/DR.
"""
from __future__ import annotations

import argparse
import binascii
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import select
import shlex
import shutil
import stat
import struct
import subprocess
import sys
import time
import urllib.parse
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
CONFIRM_TOKEN = "restore-into-new-local-database-and-object-root"
DEFAULT_POSTGRES_IMAGE = "postgres:18.6-bookworm"
DEFAULT_AWSCLI_IMAGE = "amazon/aws-cli:2.31.14"
DEFAULT_USER = "contractaudit"
DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = "5432"
RESERVED_DATABASES = {
    "development": "contract_audit_dev",
    "test": "contract_audit_test",
    "production": "contract_audit_prod",
}

# Frozen identity and relationship tables of the current schema. Tables absent
# from an environment are recorded as skipped, never assumed.
TABLES = (
    "sessions", "cases", "documents", "parsed_documents", "review_runs",
    "review_attempts", "review_attempt_parse_selections", "review_attempt_inputs",
    "execution_events", "findings", "constraint_evaluations", "decisions",
    "run_events", "activity", "idempotency", "review_run_snapshots", "audit_events",
    "worker_jobs", "outbox_events", "tool_call_ledger",
    "business_generations", "business_remediations", "business_remediation_revisions",
    "business_reports", "business_archives", "business_run_events",
    "business_audit_events", "document_activity", "execution_activity",
    "model_call_leases", "model_usage", "service_database_roles",
    "execution_queue_policies", "execution_queue_cursor", "execution_queue_tenants",
)

# The representative source a restore may claim anything about. A journey that
# only created a case is not enough: the frozen attempt, rule configuration,
# decision, remediation, report, archive and original bytes must all exist.
REQUIRED_NONEMPTY = (
    "cases", "documents", "parsed_documents", "review_runs", "review_attempts",
    "review_attempt_parse_selections", "review_attempt_inputs", "findings",
    "constraint_evaluations", "decisions", "business_remediations",
    "business_reports", "business_archives",
)
REQUIRED_QUERIES = {
    "frozen rule configuration": (
        "SELECT count(*) FROM {s}.review_attempts "
        "WHERE rule_pack_hash <> '' AND rule_pack_snapshot_digest <> '' AND rule_pack_compiler_ref <> ''"
    ),
}

# Destination orphan joins that must equal the source snapshot result. The
# frozen schema scopes foreign keys by (tenant_id, org_id).
ORPHAN_CHECKS = {
    "parsed_documents->documents": (
        "SELECT count(*) FROM {s}.parsed_documents p LEFT JOIN {s}.documents d "
        "ON d.id=p.document_id AND d.tenant_id=p.tenant_id AND d.org_id=p.org_id WHERE d.id IS NULL"
    ),
    "review_attempts->review_runs": (
        "SELECT count(*) FROM {s}.review_attempts a LEFT JOIN {s}.review_runs r "
        "ON r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.org_id=a.org_id WHERE r.id IS NULL"
    ),
    "review_attempt_parse_selections->attempts": (
        "SELECT count(*) FROM {s}.review_attempt_parse_selections x LEFT JOIN {s}.review_attempts a "
        "ON a.id=x.attempt_id AND a.tenant_id=x.tenant_id AND a.org_id=x.org_id WHERE a.id IS NULL"
    ),
    "review_attempt_parse_selections->parsed_documents": (
        "SELECT count(*) FROM {s}.review_attempt_parse_selections x LEFT JOIN {s}.parsed_documents p "
        "ON p.id=x.parsed_document_id AND p.tenant_id=x.tenant_id AND p.org_id=x.org_id WHERE p.id IS NULL"
    ),
    "review_attempt_inputs->attempts": (
        "SELECT count(*) FROM {s}.review_attempt_inputs i LEFT JOIN {s}.review_attempts a "
        "ON a.id=i.attempt_id AND a.tenant_id=i.tenant_id AND a.org_id=i.org_id WHERE a.id IS NULL"
    ),
    "findings->review_runs": (
        "SELECT count(*) FROM {s}.findings f LEFT JOIN {s}.review_runs r "
        "ON r.id=f.run_id AND r.tenant_id=f.tenant_id AND r.org_id=f.org_id WHERE r.id IS NULL"
    ),
    "decisions->findings": (
        "SELECT count(*) FROM {s}.decisions d LEFT JOIN {s}.findings f "
        "ON f.id=d.finding_id AND f.tenant_id=d.tenant_id AND f.org_id=d.org_id WHERE f.id IS NULL"
    ),
    "business_generations->documents": (
        "SELECT count(*) FROM {s}.business_generations g LEFT JOIN {s}.documents d "
        "ON d.id=g.document_id AND d.tenant_id=g.tenant_id AND d.org_id=g.org_id AND d.case_id=g.case_id "
        "WHERE d.id IS NULL"
    ),
    "business_reports->review_attempts": (
        "SELECT count(*) FROM {s}.business_reports b LEFT JOIN {s}.review_attempts a "
        "ON a.id=b.attempt_id AND a.tenant_id=b.tenant_id AND a.org_id=b.org_id AND a.run_id=b.run_id "
        "WHERE a.id IS NULL"
    ),
    "business_archives->business_reports": (
        "SELECT count(*) FROM {s}.business_archives x LEFT JOIN {s}.business_reports r "
        "ON r.id=x.report_id AND r.tenant_id=x.tenant_id AND r.org_id=x.org_id AND r.case_id=x.case_id "
        "WHERE r.id IS NULL"
    ),
    "business_remediation_revisions->remediations": (
        "SELECT count(*) FROM {s}.business_remediation_revisions v LEFT JOIN {s}.business_remediations r "
        "ON r.id=v.remediation_id AND r.tenant_id=v.tenant_id AND r.org_id=v.org_id AND r.case_id=v.case_id "
        "WHERE r.id IS NULL"
    ),
    "worker_jobs->cases": (
        "SELECT count(*) FROM {s}.worker_jobs j LEFT JOIN {s}.cases c "
        "ON c.id=j.case_id AND c.tenant_id=j.tenant_id AND c.org_id=j.org_id WHERE c.id IS NULL"
    ),
    "outbox_events->worker_jobs": (
        "SELECT count(*) FROM {s}.outbox_events o LEFT JOIN {s}.worker_jobs j "
        "ON j.id=o.job_id AND j.tenant_id=o.tenant_id AND j.org_id=o.org_id WHERE j.id IS NULL"
    ),
    "model_usage->review_attempts": (
        "SELECT count(*) FROM {s}.model_usage u "
        "LEFT JOIN {s}.review_runs r ON r.id=u.run_id AND r.tenant_id=u.tenant_id AND r.case_id=u.case_id "
        "LEFT JOIN {s}.review_attempts a ON a.id=u.attempt_id AND a.run_id=r.id "
        "AND a.tenant_id=r.tenant_id AND a.org_id=r.org_id "
        "WHERE u.attempt_id <> '' AND a.id IS NULL"
    ),
}


class Failure(RuntimeError):
    """A guard, command or verification step did not hold."""


class Recorder:
    """Runs subprocesses and appends redacted, timed command evidence."""

    def __init__(self, path: Path | None, secrets_to_redact: set[str]) -> None:
        self.path = path
        self.secrets = {value for value in secrets_to_redact if value and len(value) >= 8}
        self.records: list[dict[str, object]] = []

    def redact(self, argv: list[str]) -> list[str]:
        redacted = []
        for argument in argv:
            text = argument
            for secret in self.secrets:
                text = text.replace(secret, "<redacted>")
            if re.fullmatch(r"(?i).*(password|secret|_key|token)=.+", text):
                text = text.split("=", 1)[0] + "=<redacted>"
            redacted.append(text)
        return redacted

    def redact_text(self, value: str) -> str:
        for secret in self.secrets:
            value = value.replace(secret, "<redacted>")
        return value

    def record_raw(self, argv: list[str], code: int | None, seconds: float, stdout: str, stderr: str) -> None:
        self.records.append({
            "argv": self.redact(argv),
            "exit": code,
            "seconds": round(seconds, 3),
            "stdout_tail": self.redact_text(stdout[-2000:]),
            "stderr_tail": self.redact_text(stderr[-2000:]),
        })

    def run(self, argv: list[str], *, stdout: object | None = None, stdin: object | None = None,
            env: dict[str, str] | None = None, timeout: float = 900,
            capture: bool = True) -> subprocess.CompletedProcess:
        started = time.monotonic()
        try:
            process = subprocess.run(
                argv, cwd=ROOT,
                stdout=stdout if stdout is not None else (subprocess.PIPE if capture else subprocess.DEVNULL),
                stderr=subprocess.PIPE, stdin=stdin, env=env, timeout=timeout,
            )
        except subprocess.TimeoutExpired as error:
            self.record_raw(argv, None, time.monotonic() - started, "", f"timeout after {timeout}s: {error}")
            raise Failure(f"command timed out after {timeout}s: {shlex.join(self.redact(argv))}") from error
        seconds = time.monotonic() - started
        stderr = (process.stderr or b"").decode("utf-8", "replace")
        stdout_text = ""
        if capture and isinstance(process.stdout, (bytes, bytearray)):
            stdout_text = bytes(process.stdout).decode("utf-8", "replace")
        process.stdout_text = stdout_text  # type: ignore[attr-defined]
        self.record_raw(argv, process.returncode, seconds, stdout_text, stderr)
        return process

    def flush(self) -> None:
        if self.path is None:
            return
        with self.path.open("w", encoding="utf-8") as stream:
            for record in self.records:
                stream.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")


def load_private_env(path: Path) -> dict[str, str]:
    """Read a dotenv or flat/nested JSON environment file that must be private."""
    if not path.is_file():
        raise Failure(f"private source environment {path} does not exist")
    mode = stat.S_IMODE(path.stat().st_mode)
    if os.name == "posix" and mode & 0o077:
        raise Failure(f"{path} is readable by group/other (mode {mode:04o}); keep the private environment at 0600")
    text = path.read_text(encoding="utf-8")
    if text.lstrip().startswith("{"):
        try:
            data = json.loads(text)
        except json.JSONDecodeError as error:
            raise Failure(f"{path} is not valid JSON: {error}") from error
        if not isinstance(data, dict):
            raise Failure(f"{path} must contain a flat JSON object")
        values: dict[str, str] = {}

        def flatten(node: object, prefix: str) -> None:
            if isinstance(node, dict):
                for key, value in node.items():
                    flatten(value, str(key))
                return
            if isinstance(node, list):
                raise Failure(f"{path}: lists are not supported in the private environment")
            key = prefix
            if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
                raise Failure(f"{path}: unexpected nested key {key!r}")
            if key in values:
                raise Failure(f"{path}: duplicate key {key}")
            values[key] = "true" if node is True else ("false" if node is False else str(node))

        flatten(data, "")
        return values
    values = {}
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise Failure(f"invalid environment syntax at {path}:{number}")
        if key in values:
            raise Failure(f"duplicate key {key} in {path}")
        if len(value) >= 2 and value[0] in "'\"" and value[-1] == value[0]:
            value = value[1:-1]
        values[key] = value
    if not values:
        raise Failure(f"{path} contains no configuration")
    return values


def parse_identity(key: str, raw: str) -> tuple[urllib.parse.SplitResult, str, str, str]:
    """Mirror internal/config.ParseDSN: reject URLs with identity overrides."""
    url = urlsplit(raw.strip())
    if url.scheme not in ("postgres", "postgresql") or url.fragment:
        raise Failure(f"{key} must be a plain PostgreSQL URL without a fragment")
    try:
        query = urllib.parse.parse_qs(url.query, strict_parsing=False)
    except ValueError as error:
        raise Failure(f"{key} has an unparsable query string") from error
    for name in query:
        if name.lower() in ("dbname", "database", "host", "hostaddr", "port", "service", "servicefile"):
            raise Failure(f"{key} must not carry an identity override query parameter ({name})")
    name = url.path.strip("/")
    host = url.hostname or ""
    port = url.port and str(url.port) or DEFAULT_PORT.split(":")[0]
    if not host or not name or "/" in name or "," in host:
        raise Failure(f"{key} must define exactly one host and one database name")
    return url, host, port, name


def select_source(config: dict[str, str]) -> dict[str, object]:
    """Mirror internal/config.Select to derive the selected environment identity."""
    raw_mode = (config.get("APP_ENV") or "").strip().lower()
    mode = {"": "development", "dev": "development", "development": "development",
            "test": "test", "production": "production", "prod": "production"}.get(raw_mode)
    if mode is None:
        raise Failure(f"APP_ENV={raw_mode!r} is not a known application environment")
    if mode == "production":
        raise Failure("refusing to run the local restore against APP_ENV=production")
    identities: dict[str, tuple[str, str, str]] = {}
    for name, key in (("development", "DEV_DATABASE_URL"), ("test", "TEST_DATABASE_URL"), ("production", "PROD_DATABASE_URL")):
        raw = (config.get(key) or "").strip()
        if raw:
            _url, host, port, database = parse_identity(key, raw)
            identities[name] = (host, port, database)
        else:
            identities[name] = (DEFAULT_HOST, DEFAULT_PORT, RESERVED_DATABASES[name])
    selected_host, selected_port, selected_name = identities[mode]
    for other in ("development", "test", "production"):
        if other != mode and selected_name == identities[other][2]:
            raise Failure(f"selected database {selected_name!r} is reserved for {other}")
    if selected_name in ("postgres", "template0", "template1"):
        raise Failure(f"selected database {selected_name!r} is a system database")
    dsn = (config.get("PROD_DATABASE_URL") if mode == "production" else
           config.get("TEST_DATABASE_URL") if mode == "test" else config.get("DEV_DATABASE_URL")) or ""
    if mode != "production" and not dsn.strip():
        dsn = f"postgres://{DEFAULT_USER}:{DEFAULT_USER}@{selected_host}:{selected_port}/{selected_name}?sslmode=disable"
    return {
        "mode": mode,
        "dsn": dsn,
        "host": selected_host,
        "port": selected_port,
        "name": selected_name,
        "identities": identities,
    }


def service_connections(config: dict[str, str], selected: dict[str, object]) -> dict[str, str]:
    """Every configured service/MIGRATOR DSN must select the same identity."""
    service_keys = ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL",
                    "MODEL_DATABASE_URL", "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL")
    checked: dict[str, str] = {}
    for key in service_keys:
        raw = (config.get(key) or "").strip()
        if not raw:
            continue
        _url, host, port, database = parse_identity(key, raw)
        if (host, port, database) != (selected["host"], selected["port"], selected["name"]):
            raise Failure(
                f"{key} selects {host}:{port}/{database}, which is not the selected identity "
                f"{selected['host']}:{selected['port']}/{selected['name']}"
            )
        checked[key] = database
    if "MIGRATOR_DATABASE_URL" not in checked:
        raise Failure("MIGRATOR_DATABASE_URL is required to name the migration identity")
    return checked


def safe_schema(config: dict[str, str]) -> str:
    schema = (config.get("DATABASE_SCHEMA") or "public").strip() or "public"
    if not re.fullmatch(r"[a-z_][a-z0-9_]*", schema):
        raise Failure(f"unsafe DATABASE_SCHEMA: {schema!r}")
    return schema


def docker_cli() -> str:
    docker = os.environ.get("DOCKER_BIN", "docker")
    if not shutil.which(docker):
        raise Failure("Docker CLI is unavailable; the local restore needs Docker or local PostgreSQL clients")
    return docker


class PgAccess:
    """psql/pg_dump/pg_restore access resolved without downloading anything."""

    def __init__(self, recorder: Recorder, docker: str, container: str | None, image_runner: bool,
                 image: str, host: str, port: str, user: str, password: str) -> None:
        self.recorder = recorder
        self.docker = docker
        self.container = container
        self.image_runner = image_runner
        self.image = image
        self.host = host
        self.port = port
        self.user = user
        self.password = password
        self.env = dict(os.environ)
        if password:
            self.env["PGPASSWORD"] = password

    @property
    def mode(self) -> str:
        return "container" if self.container else ("image" if self.image_runner else "host")

    def _agent(self) -> list[str]:
        if self.mode == "container":
            # -e PGPASSWORD (no value) copies the value from this process, so
            # the secret never appears in any command line.
            return [self.docker, "exec", "-i", "-e", "PGPASSWORD", str(self.container)]
        if self.mode == "image":
            return [self.docker, "run", "--rm", "--network", "host", "-e", "PGPASSWORD", self.image]
        return []

    def _connection(self, database: str) -> list[str]:
        if self.mode == "container":
            return ["-U", self.user, "-d", database]
        return ["-h", self.host, "-p", self.port, "-U", self.user, "-d", database]

    def argv(self, program: str, arguments: list[str], database: str) -> list[str]:
        return self._agent() + [program, *arguments, "--no-password", *self._connection(database)]

    def sql(self, database: str, query: str, timeout: float = 120) -> list[str]:
        argv = self.argv("psql", ["-X", "-q", "-t", "-A", "-F", "|", "-v", "ON_ERROR_STOP=1", "-c", query], database)
        process = self.recorder.run(argv, env=self.env, timeout=timeout)
        if process.returncode != 0:
            raise Failure(f"psql failed on {database}: {(process.stderr or b'').decode('utf-8', 'replace').strip()[-400:]}")
        return str(process.stdout_text or "").splitlines()

    def scalar(self, database: str, query: str, timeout: float = 120) -> str:
        lines = [line for line in self.sql(database, query, timeout) if line != ""]
        if len(lines) != 1 or "|" in lines[0]:
            raise Failure(f"expected exactly one scalar from {query!r}, got {lines!r}")
        return lines[0]

    def count(self, database: str, schema: str, table: str, where: str = "") -> int:
        query = f"SELECT count(*) FROM {schema}.{table}"
        if where:
            query += " WHERE " + where
        return int(self.scalar(database, query))

    def table_exists(self, database: str, schema: str, table: str) -> bool:
        return self.scalar(database, f"SELECT to_regclass('{schema}.{table}') IS NOT NULL") == "t"

    def primary_key(self, database: str, schema: str, table: str) -> str:
        """Return quoted PK columns in attnum order, or '' for a fallback sort."""
        query = (
            "SELECT string_agg(quote_ident(a.attname), ', ' ORDER BY a.attnum) "
            "FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid "
            "WHERE i.indrelid = '" + schema + "." + table + "'::regclass AND i.indisprimary "
            "AND a.attnum = ANY (string_to_array(i.indkey::text, ' ')::int2[])"
        )
        try:
            value = self.scalar(database, query)
        except Failure:
            return ""
        return value

    def hash_rows(self, database: str, schema: str, table: str, snapshot: str | None,
                  transaction: bool = True) -> tuple[int, str]:
        """Stream canonical rows through SHA-256 without materialising the table."""
        order = self.primary_key(database, schema, table) or "to_jsonb(t)::text"
        copy_sql = (
            f"COPY (SELECT to_jsonb(t)::text AS r FROM (SELECT * FROM {schema}.{table}) t "
            f"ORDER BY {order}) TO STDOUT"
        )
        if transaction:
            prelude = ["BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;"]
            if snapshot:
                prelude.append(f"SET TRANSACTION SNAPSHOT '{snapshot}';")
            prelude.append("SET LOCAL row_security = off;")
            query = " ".join(prelude) + " " + copy_sql + "; COMMIT"
        else:
            query = f"BEGIN READ ONLY; SET LOCAL row_security = off; {copy_sql}; COMMIT"
        argv = self.argv("psql", ["-X", "-q", "-t", "-A", "-v", "ON_ERROR_STOP=1", "-c", query], database)
        started = time.monotonic()
        hasher = hashlib.sha256()
        count = 0
        process = subprocess.Popen(argv, cwd=ROOT, env=self.env, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, stdin=subprocess.DEVNULL)
        assert process.stdout is not None
        deadline = time.monotonic() + 600
        while True:
            ready, _, _ = select.select([process.stdout], [], [], 30)
            if not ready:
                if time.monotonic() > deadline:
                    process.kill()
                    raise Failure(f"row hashing did not finish for {schema}.{table}")
                continue
            chunk = os.read(process.stdout.fileno(), 1 << 20)
            if not chunk:
                break
            hasher.update(chunk)
            count += chunk.count(b"\n")
        stderr = process.stderr.read() if process.stderr else b""
        code = process.wait(timeout=60)
        self.recorder.record_raw(argv, code, time.monotonic() - started, "", stderr.decode("utf-8", "replace"))
        if code != 0:
            raise Failure(
                f"row hashing failed for {schema}.{table}: {stderr.decode('utf-8', 'replace').strip()[-300:]}"
            )
        return count, hasher.hexdigest()

    def dump(self, database: str, destination: Path, snapshot: str) -> None:
        argv = self.argv("pg_dump", ["--format=custom", "--compress=6", "--snapshot", snapshot], database)
        with destination.open("wb") as stream:
            process = self.recorder.run(argv, stdout=stream, env=self.env, timeout=1800)
        if process.returncode != 0:
            raise Failure(f"pg_dump failed: {(process.stderr or b'').decode('utf-8', 'replace').strip()[-400:]}")

    def restore(self, database: str, source: Path) -> None:
        argv = self.argv("pg_restore", ["--exit-on-error", "--single-transaction"], database)
        with source.open("rb") as stream:
            process = self.recorder.run(argv, stdin=stream, env=self.env, timeout=1800)
        if process.returncode != 0:
            raise Failure(f"pg_restore failed: {(process.stderr or b'').decode('utf-8', 'replace').strip()[-400:]}")


class Snapshot:
    """Holds one exported read-only REPEATABLE READ snapshot for the capture."""

    def __init__(self, pg: PgAccess, database: str) -> None:
        self.pg = pg
        self.database = database
        self.process: subprocess.Popen | None = None
        self.snapshot_id = ""

    def __enter__(self) -> "Snapshot":
        argv = self.pg.argv("psql", ["-X", "-q", "-t", "-A", "-v", "ON_ERROR_STOP=1"], self.database)
        self.process = subprocess.Popen(argv, cwd=ROOT, env=self.pg.env, stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert self.process.stdin and self.process.stdout
        script = (
            "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\n"
            "SET LOCAL row_security = off;\n"
            "SELECT 'SNAPSHOT:' || pg_export_snapshot();\n"
        )
        self.process.stdin.write(script.encode())
        self.process.stdin.flush()
        ready, _, _ = select.select([self.process.stdout], [], [], 60)
        if not ready:
            self.close()
            raise Failure("timed out exporting a source snapshot")
        line = self.process.stdout.readline().decode("utf-8", "replace").strip()
        if not line.startswith("SNAPSHOT:"):
            self.close()
            raise Failure(f"could not export a source snapshot: {line[:200]!r}")
        self.snapshot_id = line.split(":", 1)[1].strip()
        if not re.fullmatch(r"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{8}-[0-9]+", self.snapshot_id):
            self.close()
            raise Failure(f"unexpected snapshot identifier {self.snapshot_id!r}")
        self.pg.recorder.record_raw(argv, 0, 0.0, f"exported snapshot {self.snapshot_id}", "")
        return self

    def close(self) -> None:
        if self.process is None:
            return
        try:
            if self.process.stdin:
                self.process.stdin.write(b"COMMIT;\n\\q\n")
                self.process.stdin.flush()
                self.process.stdin.close()
            self.process.wait(timeout=30)
        except (OSError, subprocess.TimeoutExpired):
            self.process.kill()
            self.process.wait(timeout=10)
        finally:
            self.process = None

    def __exit__(self, *exc_info: object) -> None:
        self.close()


class LocalObjects:
    """Content-addressed originals under one DATA_DIR.

    Blobs live at <DATA_DIR>/blobs/<digest>. When the source stack keeps that
    directory in a container volume, reads go through `docker exec cat` so no
    user directory is ever mounted, copied or deleted.
    """

    def __init__(self, root: Path, recorder: Recorder | None = None, docker: str = "",
                 container: str = "", container_root: str = "") -> None:
        self.root = root
        self.recorder = recorder
        self.docker = docker
        self.container = container
        self.container_root = container_root

    def describe(self) -> str:
        if self.container:
            return f"docker:{self.container}:{self.container_root}/blobs"
        return str(self.root)

    def read(self, digest: str) -> bytes:
        if not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise Failure(f"refusing to read a non content-addressed original name {digest!r}")
        if self.container:
            argv = [self.docker, "exec", self.container, "cat", f"{self.container_root}/blobs/{digest}"]
            started = time.monotonic()
            process = subprocess.run(argv, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                     stdin=subprocess.DEVNULL, timeout=120)
            if self.recorder is not None:
                self.recorder.records.append({
                    "argv": self.recorder.redact(argv), "exit": process.returncode,
                    "seconds": round(time.monotonic() - started, 3), "stdout_tail": "<binary>",
                    "stderr_tail": process.stderr.decode("utf-8", "replace")[-500:],
                })
            if process.returncode != 0:
                raise Failure(
                    f"could not read {digest} from {self.container}: "
                    f"{process.stderr.decode('utf-8', 'replace').strip()[-200:]}")
            return process.stdout
        return (self.root / digest).read_bytes()


class S3Objects:
    """Private S3-compatible bucket reached through the pinned AWS CLI image."""

    def __init__(self, recorder: Recorder, docker: str, image: str, endpoint: str, access_key: str,
                 secret_key: str, region: str) -> None:
        self.recorder = recorder
        self.docker = docker
        self.image = image
        self.endpoint = endpoint.rstrip("/")
        self.region = region
        self.env = dict(os.environ)
        self.env.update({
            "AWS_ACCESS_KEY_ID": access_key,
            "AWS_SECRET_ACCESS_KEY": secret_key,
            "AWS_DEFAULT_REGION": region,
            "AWS_EC2_METADATA_DISABLED": "true",
            "AWS_MAX_ATTEMPTS": "1",
            "AWS_PAGER": "",
        })

    def _aws(self, arguments: list[str]) -> list[str]:
        return [
            self.docker, "run", "--rm", "--network", "host",
            "-e", "AWS_ACCESS_KEY_ID", "-e", "AWS_SECRET_ACCESS_KEY", "-e", "AWS_DEFAULT_REGION",
            "-e", "AWS_EC2_METADATA_DISABLED", "-e", "AWS_MAX_ATTEMPTS", "-e", "AWS_PAGER",
            self.image, "--endpoint-url", self.endpoint,
            "--cli-connect-timeout", "5", "--cli-read-timeout", "60", *arguments,
        ]

    def bucket_exists(self, bucket: str) -> bool:
        process = self.recorder.run(self._aws(["s3api", "head-bucket", "--bucket", bucket]),
                                    env=self.env, timeout=120)
        return process.returncode == 0

    def create_bucket(self, bucket: str) -> None:
        process = self.recorder.run(self._aws(["s3api", "create-bucket", "--bucket", bucket]),
                                    env=self.env, timeout=120)
        if process.returncode != 0:
            raise Failure(f"could not create destination bucket {bucket}: "
                          f"{(process.stderr or b'').decode('utf-8', 'replace').strip()[-300:]}")

    def copy_key(self, source_bucket: str, destination_bucket: str, key: str) -> None:
        process = self.recorder.run(
            self._aws(["s3", "cp", f"s3://{source_bucket}/{key}", f"s3://{destination_bucket}/{key}"]),
            env=self.env, timeout=300,
        )
        if process.returncode != 0:
            raise Failure(f"could not copy {key} into {destination_bucket}: "
                          f"{(process.stderr or b'').decode('utf-8', 'replace').strip()[-300:]}")

    def list_keys(self, bucket: str) -> list[str]:
        process = self.recorder.run(
            self._aws(["s3api", "list-objects-v2", "--bucket", bucket, "--query", "Contents[].Key",
                       "--output", "text"]),
            env=self.env, timeout=300,
        )
        if process.returncode != 0:
            raise Failure(f"could not list bucket {bucket}: "
                          f"{(process.stderr or b'').decode('utf-8', 'replace').strip()[-300:]}")
        return [key for key in str(process.stdout_text or "").split() if key]

    def read(self, bucket: str, key: str) -> bytes:
        argv = self._aws(["s3", "cp", f"s3://{bucket}/{key}", "-", "--only-show-errors"])
        started = time.monotonic()
        process = subprocess.run(argv, cwd=ROOT, env=self.env, stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, stdin=subprocess.DEVNULL, timeout=300)
        self.recorder.record_raw(argv, process.returncode, time.monotonic() - started, "",
                                 process.stderr.decode("utf-8", "replace"))
        if process.returncode != 0:
            raise Failure(f"could not read s3://{bucket}/{key}: "
                          f"{process.stderr.decode('utf-8', 'replace').strip()[-300:]}")
        return process.stdout


def report_digest(json_bytes: bytes, html_bytes: bytes) -> str:
    hasher = hashlib.sha256()
    hasher.update(struct.pack(">Q", len(json_bytes)))
    hasher.update(json_bytes)
    hasher.update(struct.pack(">Q", len(html_bytes)))
    hasher.update(html_bytes)
    return hasher.hexdigest()


def snapshot_documents(pg: PgAccess, database: str, schema: str, snapshot: str) -> list[tuple[str, str, str, int, str]]:
    """Enumerate the referenced originals from the held snapshot itself."""
    query = (
        "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; "
        f"SET TRANSACTION SNAPSHOT '{snapshot}'; SET LOCAL row_security = off; "
        f"SELECT tenant_id || '|' || case_id || '|' || digest || '|' || size || '|' || storage_backend "
        f"FROM {schema}.documents ORDER BY id; COMMIT"
    )
    rows: list[tuple[str, str, str, int, str]] = []
    for line in pg.sql(database, query, timeout=300):
        if not line:
            continue
        fields = line.split("|")
        if len(fields) != 5:
            raise Failure(f"unexpected documents row in the snapshot: {line!r}")
        tenant, case, digest, size, backend = fields
        if backend not in ("local", "remote"):
            raise Failure(f"unknown storage backend {backend!r} in the snapshot")
        rows.append((tenant, case, digest, int(size), backend))
    return rows


def require_representative(pg: PgAccess, database: str, schema: str, snapshot: str) -> dict[str, int]:
    """Refuse an incomplete journey: the frozen attempt, rule config, decision,
    remediation, report, archive and originals must all exist in the snapshot."""
    counts: dict[str, int] = {}
    missing: list[str] = []
    for table in REQUIRED_NONEMPTY:
        if not pg.table_exists(database, schema, table):
            missing.append(f"{table} (table absent)")
            continue
        count = int(pg.scalar(
            database,
            f"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TRANSACTION SNAPSHOT '{snapshot}'; "
            f"SET LOCAL row_security = off; SELECT count(*) FROM {schema}.{table}; COMMIT",
        ))
        counts[table] = count
        if count == 0:
            missing.append(table)
    for label, template in REQUIRED_QUERIES.items():
        value = int(pg.scalar(
            database,
            f"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TRANSACTION SNAPSHOT '{snapshot}'; "
            f"SET LOCAL row_security = off; {template.format(s=schema)}; COMMIT",
        ))
        counts[label] = value
        if value == 0:
            missing.append(label)
    if missing:
        raise Failure("refusing to count an incomplete journey as proof: the source has no " + ", ".join(missing))
    return counts


def probe_authority(pg: PgAccess, database: str, schema: str) -> None:
    """pg_dump and every comparison run with row_security=off; prove it is allowed.

    A dedicated migrator or BYPASSRLS role is sufficient; a superuser is not
    required, but a role subject to FORCE RLS must fail here instead of quietly
    dumping or comparing a scoped subset.
    """
    query = (
        "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET LOCAL row_security = off; "
        f"SELECT count(*) FROM {schema}.sessions; COMMIT"
    )
    try:
        pg.scalar(database, query)
    except Failure as error:
        raise Failure(
            "the source identity cannot read through FORCE row level security with row_security=off; "
            "use the dedicated migrator/BYPASSRLS authority: " + str(error)[:200]
        ) from error


def hash_table_set(pg: PgAccess, database: str, schema: str, snapshot: str | None) -> dict[str, object]:
    result: dict[str, object] = {}
    for table in TABLES:
        if not pg.table_exists(database, schema, table):
            result[table] = {"absent": True}
            continue
        count, digest = pg.hash_rows(database, schema, table, snapshot)
        result[table] = {"rows": count, "sha256": digest}
    return result


def compare_identities(source: dict[str, object], destination: dict[str, object]) -> list[str]:
    failures: list[str] = []
    for table in sorted(set(source) | set(destination)):
        left = source.get(table, {})
        right = destination.get(table, {})
        if left.get("absent") or right.get("absent"):
            if left.get("absent") != right.get("absent"):
                failures.append(f"table {table} exists on only one side")
            continue
        if left.get("rows") != right.get("rows") or left.get("sha256") != right.get("sha256"):
            failures.append(
                f"table {table} differs (source rows {left.get('rows')} digest {str(left.get('sha256'))[:12]}, "
                f"destination rows {right.get('rows')} digest {str(right.get('sha256'))[:12]})"
            )
    return failures


def compare_relationships(source_scalars: dict[str, int], destination_scalars: dict[str, int]) -> list[str]:
    failures: list[str] = []
    for name in sorted(set(source_scalars) | set(destination_scalars)):
        left = source_scalars.get(name, -1)
        right = destination_scalars.get(name, -1)
        if left != right:
            failures.append(f"orphan count changed for {name}: {left}->{right}")
    return failures


def evaluate_relationships(pg: PgAccess, database: str, schema: str, snapshot: str | None,
                           transaction: bool) -> dict[str, int]:
    result: dict[str, int] = {}
    for name, template in ORPHAN_CHECKS.items():
        if snapshot and transaction:
            query = (f"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TRANSACTION SNAPSHOT '{snapshot}'; "
                     f"SET LOCAL row_security = off; {template.format(s=schema)}; COMMIT")
        else:
            query = f"BEGIN READ ONLY; SET LOCAL row_security = off; {template.format(s=schema)}; COMMIT"
        result[name] = int(pg.scalar(database, query, timeout=300))
    return result


def remote_object_key(tenant: str, case: str, digest: str) -> str:
    """Match internal/store.objectKey for immutable originals."""
    return f"{tenant}/{case}/{digest}"


def copy_objects(referenced: list[tuple[str, str, str, int, str]], local_objects: LocalObjects | None,
                 s3: S3Objects | None, source_bucket: str, destination_bucket: str,
                 destination_root: Path) -> dict[str, object]:
    """Copy exactly the snapshot's referenced originals under their frozen IDs."""
    local_report: dict[str, object] = {"copied": 0, "distinct": 0, "failures": []}
    remote_report: dict[str, object] = {"copied": 0, "distinct": 0, "failures": []}
    local_failures: list[str] = local_report["failures"]  # type: ignore[assignment]
    remote_failures: list[str] = remote_report["failures"]  # type: ignore[assignment]
    seen_local: set[str] = set()
    seen_remote: set[str] = set()
    for tenant, case, digest, size, backend in referenced:
        if backend == "local":
            if digest in seen_local:
                continue
            seen_local.add(digest)
            if local_objects is None:
                local_failures.append(f"{digest}: no DATA_DIR is configured")
                continue
            try:
                data = local_objects.read(digest)
            except OSError as error:
                local_failures.append(f"{digest}: {error}")
                continue
            if len(data) != size or hashlib.sha256(data).hexdigest() != digest:
                local_failures.append(f"{digest}: source bytes do not match digest/size")
                continue
            target = destination_root / "blobs" / digest
            target.parent.mkdir(parents=True, exist_ok=True)
            if target.exists():
                if hashlib.sha256(target.read_bytes()).hexdigest() != digest:
                    local_failures.append(f"{digest}: destination copy already exists with different bytes")
                    continue
            else:
                with target.open("xb") as stream:
                    stream.write(data)
            local_report["copied"] = int(local_report["copied"]) + 1
        else:
            key = remote_object_key(tenant, case, digest)
            if key in seen_remote:
                continue
            seen_remote.add(key)
            if s3 is None:
                remote_failures.append(f"{key}: no S3 source is configured")
                continue
            try:
                data = s3.read(source_bucket, key)
            except Failure as error:
                remote_failures.append(f"{key}: {error}")
                continue
            if len(data) != size or hashlib.sha256(data).hexdigest() != digest:
                remote_failures.append(f"{key}: source bytes do not match digest/size")
                continue
            s3.copy_key(source_bucket, destination_bucket, key)
            remote_report["copied"] = int(remote_report["copied"]) + 1
    local_report["distinct"] = len(seen_local)
    remote_report["distinct"] = len(seen_remote)
    return {"local": local_report, "remote": remote_report}


def verify_object_layout(referenced: list[tuple[str, str, str, int, str]], destination_root: Path | None,
                         s3: S3Objects | None, destination_bucket: str) -> tuple[list[str], dict[str, object]]:
    """The destination object namespace must hold exactly the frozen references."""
    failures: list[str] = []
    report: dict[str, object] = {}
    expected_local = {digest for _t, _c, digest, _s, backend in referenced if backend == "local"}
    expected_remote = {remote_object_key(tenant, case, digest) for tenant, case, digest, _s, backend in referenced
                       if backend == "remote"}
    if expected_local:
        blobs = destination_root / "blobs" if destination_root else None
        actual = {path.name for path in blobs.iterdir() if path.is_file()} if blobs and blobs.is_dir() else set()
        report["local"] = {"expected": len(expected_local), "present": len(actual),
                           "missing": sorted(expected_local - actual)[:10],
                           "unexpected": sorted(actual - expected_local)[:10]}
        failures += [f"local original {digest} missing under {blobs}" for digest in sorted(expected_local - actual)[:5]]
        failures += [f"unexpected local object {name} under {blobs}" for name in sorted(actual - expected_local)[:5]]
    if expected_remote:
        actual_remote = set(s3.list_keys(destination_bucket)) if s3 else set()
        report["remote"] = {"expected": len(expected_remote), "present": len(actual_remote),
                            "missing": sorted(expected_remote - actual_remote)[:10],
                            "unexpected": sorted(actual_remote - expected_remote)[:10]}
        failures += [f"destination object {key} missing" for key in sorted(expected_remote - actual_remote)[:5]]
        failures += [f"unexpected destination object {key}" for key in sorted(actual_remote - expected_remote)[:5]]
    return failures, report


def verify_destination_bytes(pg: PgAccess, database: str, schema: str, destination_root: Path | None,
                             s3: S3Objects | None, destination_bucket: str,
                             limits: int = 500) -> tuple[list[str], dict[str, object]]:
    rows = pg.sql(database, f"SELECT tenant_id, case_id, digest, size, storage_backend FROM {schema}.documents ORDER BY id")
    failures: list[str] = []
    checked = {"local": 0, "remote": 0}
    for line in rows:
        if not line:
            continue
        tenant, case, digest, size, backend = line.split("|")
        if backend == "local":
            if destination_root is None:
                failures.append(f"{digest}: no destination root")
                continue
            path = destination_root / "blobs" / digest
            try:
                data = path.read_bytes()
            except OSError as error:
                failures.append(f"{digest}: {error}")
                continue
        else:
            if s3 is None:
                failures.append(f"{digest}: no destination bucket client")
                continue
            try:
                data = s3.read(destination_bucket, remote_object_key(tenant, case, digest))
            except Failure as error:
                failures.append(f"{tenant}/{case}/{digest}: {error}")
                continue
        if len(data) != int(size) or hashlib.sha256(data).hexdigest() != digest:
            failures.append(f"{digest}: destination bytes do not match the restored digest/size")
        else:
            checked[backend] = checked.get(backend, 0) + 1
        if len(failures) >= limits:
            failures.append("more failures suppressed")
            break
    return failures, checked


def verify_reports(pg: PgAccess, database: str, schema: str) -> tuple[list[str], dict[str, int]]:
    lines = pg.sql(
        database,
        f"SELECT id, digest, encode(json_bytes,'hex'), encode(html_bytes,'hex') FROM {schema}.business_reports ORDER BY id",
        timeout=300,
    )
    failures: list[str] = []
    verified = 0
    for line in lines:
        if not line:
            continue
        fields = line.split("|")
        if len(fields) != 4:
            failures.append(f"unexpected business_reports row: {line[:120]!r}")
            continue
        identifier, digest, json_hex, html_hex = fields
        try:
            json_bytes = binascii.unhexlify(json_hex)
            html_bytes = binascii.unhexlify(html_hex)
        except binascii.Error:
            failures.append(f"report {identifier} has non-hex bytes in the restored row")
            continue
        if report_digest(json_bytes, html_bytes) != digest:
            failures.append(f"report {identifier} digest does not match its restored bytes")
        else:
            verified += 1
    return failures, {"rows": len(lines), "verified": verified}


def psql_probe(pg: PgAccess, database: str, script: str, timeout: float = 120) -> tuple[int, str, str]:
    process = pg.recorder.run(
        pg.argv("psql", ["-X", "-q", "-t", "-A", "-v", "ON_ERROR_STOP=0", "-c", script], database),
        env=pg.env, timeout=timeout,
    )
    return process.returncode, str(process.stdout_text or ""), (process.stderr or b"").decode("utf-8", "replace")


def exercise_permissions(pg: PgAccess, database: str, schema: str) -> tuple[list[str], dict[str, object]]:
    """Read-only exercise of the restored grants and FORCE RLS with real roles.

    Everything runs inside BEGIN ... ROLLBACK: a denied statement must be denied,
    an allowed read must see the tenant scope, and nothing is written.
    """
    failures: list[str] = []
    report: dict[str, object] = {}
    roles = [line for line in pg.sql(
        database, f"SELECT role_name || '|' || service FROM {schema}.service_database_roles ORDER BY role_name") if line]
    if len(roles) != 5:
        failures.append(f"expected the five service roles in the destination, found {len(roles)}")
    for line in roles:
        role, service = line.split("|")
        if not re.fullmatch(r"[a-z_][a-z0-9_]*", role):
            failures.append(f"unexpected restored role name {role!r}")
            continue
        tenant = pg.scalar(database, f"SELECT tenant_id FROM {schema}.cases ORDER BY seq LIMIT 1")
        org = pg.scalar(database, f"SELECT org_id FROM {schema}.cases ORDER BY seq LIMIT 1")
        total = int(pg.scalar(database, f"SELECT count(*) FROM {schema}.cases"))
        scoped_expected = int(pg.scalar(
            database, f"SELECT count(*) FROM {schema}.cases WHERE tenant_id = {quote(tenant)}"))
        entry: dict[str, object] = {"service": service, "total_cases": total, "tenant_cases": scoped_expected}

        code, output, stderr = psql_probe(pg, database, (
            "BEGIN; "
            f"SET LOCAL ROLE {role}; "
            f"SELECT 'UNSCOPED:' || count(*) FROM {schema}.cases; "
            f"SELECT set_config('app.tenant_id','{tenant}',true), set_config('app.org_id','{org}',true); "
            f"SELECT 'SCOPED:' || count(*) FROM {schema}.cases; "
            "ROLLBACK"
        ))
        unscoped = next((int(row.split(":", 1)[1]) for row in output.splitlines() if row.startswith("UNSCOPED:")), -1)
        scoped = next((int(row.split(":", 1)[1]) for row in output.splitlines() if row.startswith("SCOPED:")), -1)
        entry.update({"unscoped_visible": unscoped, "scoped_visible": scoped})
        if code != 0:
            failures.append(f"role {role} read probe failed: {stderr.strip()[-200:]}")
        if unscoped != 0:
            failures.append(f"role {role} saw {unscoped} cases without a tenant scope; FORCE RLS is not enforced")
        if scoped != scoped_expected:
            failures.append(
                f"role {role} saw {scoped} of {scoped_expected} cases for the tenant scope {tenant!r}"
            )

        _, _, ddl_stderr = psql_probe(pg, database, (
            "BEGIN; " f"SET LOCAL ROLE {role}; " "CREATE TABLE restore_probe_ddl (id text); " "ROLLBACK"
        ))
        entry["ddl_denied"] = "permission denied" in ddl_stderr
        if not entry["ddl_denied"]:
            failures.append(f"role {role} was able to run DDL in the destination")

        candidates = pg.sql(database, (
            "SELECT c.relname, (SELECT a.attname FROM pg_attribute a WHERE a.attrelid=c.oid "
            "AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 1) "
            "FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
            f"WHERE n.nspname='{schema}' AND c.relkind='r' "
            f"AND NOT has_table_privilege('{role}', c.oid, 'UPDATE') ORDER BY c.relname LIMIT 1"
        ))
        if candidates and candidates[0]:
            table, column = candidates[0].split("|")
            if re.fullmatch(r"[a-z_][a-z0-9_]*", table) and re.fullmatch(r"[a-z_][a-z0-9_]*", column):
                _, _, write_stderr = psql_probe(pg, database, (
                    "BEGIN; " f"SET LOCAL ROLE {role}; "
                    f"UPDATE {schema}.{table} SET {column}={column} WHERE false; " "ROLLBACK"
                ))
                entry["foreign_write_denied"] = "permission denied" in write_stderr
                entry["foreign_write_probe"] = f"{table}.{column}"
                if not entry["foreign_write_denied"]:
                    failures.append(f"role {role} could write {table}, which it does not own")
        report[role] = entry
    return failures, report


def image_present(recorder: Recorder, docker: str, image: str) -> bool:
    process = recorder.run([docker, "image", "inspect", "--format", "{{.Id}}", image], timeout=60)
    return process.returncode == 0


def discover_pg_container(recorder: Recorder, docker: str, port: str) -> str | None:
    process = recorder.run([docker, "ps", "--format", "{{.ID}}\t{{.Names}}\t{{.Ports}}"], timeout=60)
    if process.returncode != 0:
        return None
    matches: list[str] = []
    for line in str(process.stdout_text or "").splitlines():
        fields = line.split("\t")
        if len(fields) != 3:
            continue
        identifier, _name, ports = fields
        if f":{port}->" in ports and "5432/tcp" in ports:
            matches.append(identifier)
    if len(matches) > 1:
        raise Failure(f"several running containers publish port {port}; pass --pg-container explicitly")
    return matches[0] if matches else None


def resolve_pg(recorder: Recorder, host: str, port: str, user: str, password: str,
               pg_container: str | None, postgres_image: str, docker: str) -> PgAccess:
    if not user:
        raise Failure("the migrator DSN has no database user")
    if pg_container:
        return PgAccess(recorder, docker, pg_container, False, postgres_image, host, port, user, password)
    if host in ("127.0.0.1", "localhost", "::1"):
        container = discover_pg_container(recorder, docker, port)
        if container:
            return PgAccess(recorder, docker, container, False, postgres_image, host, port, user, password)
    if all(shutil.which(program) for program in ("psql", "pg_dump", "pg_restore")):
        return PgAccess(recorder, docker, None, False, postgres_image, host, port, user, password)
    if image_present(recorder, docker, postgres_image):
        return PgAccess(recorder, docker, None, True, postgres_image, host, port, user, password)
    raise Failure(
        "no PostgreSQL client is available: install the client binaries, start the database container, "
        f"or pull the pinned {postgres_image} image (nothing is downloaded automatically)"
    )


def container_env_value(container: str, key: str) -> str:
    """Read one environment value from a container without recording secrets."""
    process = subprocess.run(
        [os.environ.get("DOCKER_BIN", "docker"), "inspect", "--format",
         f"{{{{range .Config.Env}}}}{{{{println .}}}}{{{{end}}}}", container],
        cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, stdin=subprocess.DEVNULL, timeout=60)
    if process.returncode != 0:
        return ""
    for line in process.stdout.decode("utf-8", "replace").splitlines():
        name, separator, value = line.partition("=")
        if separator and name == key:
            return value
    return ""


def paths_overlap(left: Path, right: Path) -> bool:
    return left == right or left in right.parents or right in left.parents


def ensure_destination_root_isolated(destination_root: Path, local_objects: LocalObjects | None,
                                     data_dir: str, recorder: Recorder, docker: str) -> None:
    """Refuse an object destination that shares the host source directory or volume."""
    if local_objects is None:
        return
    target_root = destination_root.resolve()
    if not local_objects.container:
        if not data_dir:
            return
        source_root = Path(data_dir).resolve()
        if paths_overlap(target_root, source_root):
            raise Failure("the local restore root must be isolated from the source DATA_DIR")
        return

    resolved = recorder.run(
        [docker, "exec", local_objects.container, "realpath", "-e", local_objects.container_root], timeout=60)
    if resolved.returncode != 0:
        raise Failure(f"cannot resolve the source DATA_DIR inside {local_objects.container}")
    container_root = Path(str(resolved.stdout_text or "").strip())
    if not container_root.is_absolute():
        raise Failure("the source container returned a non-absolute DATA_DIR path")
    inspected = recorder.run(
        [docker, "inspect", "--format", "{{json .Mounts}}", local_objects.container], timeout=60)
    if inspected.returncode != 0:
        raise Failure(f"cannot inspect source volume mounts for {local_objects.container}")
    try:
        mounts = json.loads(str(inspected.stdout_text or ""))
    except json.JSONDecodeError as error:
        raise Failure("Docker returned malformed source volume metadata") from error
    if not isinstance(mounts, list) or not all(isinstance(mount, dict) for mount in mounts):
        raise Failure("Docker returned an invalid source volume list")
    source_volumes: list[Path] = []
    for mount in mounts:
        destination = str(mount.get("Destination", ""))
        source = str(mount.get("Source", ""))
        if (not destination.startswith("/") or not source.startswith("/")
                or (container_root != Path(destination) and Path(destination) not in container_root.parents
                    and container_root not in Path(destination).parents)):
            continue
        source_volumes.append(Path(source).resolve())
    for source_volume in source_volumes:
        if paths_overlap(target_root, source_volume):
            raise Failure(f"the restore root overlaps source container volume {source_volume}")


def build_local_objects(config: dict[str, str], args, recorder: Recorder, docker: str) -> tuple[LocalObjects | None, str]:
    container = (args.source_data_container or "").strip()
    data_dir = (args.source_data_dir or config.get("DATA_DIR") or "").strip()
    if container:
        container_root = (args.source_data_path or container_env_value(container, "DATA_DIR") or "").strip()
        if not container_root.startswith("/"):
            raise Failure(
                f"cannot derive the DATA_DIR inside {container}; pass --source-data-path with the absolute path"
            )
        return LocalObjects(Path(data_dir or "."), recorder, docker, container, container_root), \
            f"docker:{container}:{container_root}"
    if not data_dir:
        return None, ""
    return LocalObjects(Path(data_dir) / "blobs", recorder), data_dir


def build_s3_client(recorder: Recorder, docker: str, config: dict[str, str], args) -> tuple[S3Objects | None, str]:
    endpoint = (args.source_endpoint or config.get("S3_ENDPOINT") or "").strip()
    bucket = (args.source_bucket or config.get("S3_BUCKET") or "").strip()
    access = (config.get("S3_ACCESS_KEY") or config.get("RUSTFS_ACCESS_KEY") or "").strip()
    secret = (config.get("S3_SECRET_KEY") or config.get("RUSTFS_SECRET_KEY") or "").strip()
    image = (args.awscli_image or config.get("AWSCLI_IMAGE") or DEFAULT_AWSCLI_IMAGE).strip()
    if not (endpoint and bucket and access and secret):
        return None, ""
    if not image_present(recorder, docker, image):
        raise Failure(f"the pinned AWS CLI image {image} is not present locally; pull it explicitly first")
    return S3Objects(recorder, docker, image, endpoint, access, secret,
                     (config.get("S3_REGION") or "us-east-1").strip()), bucket


def sha256_file(path: Path) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def write_manifest(path: Path, manifest: dict[str, object]) -> None:
    temporary = path.with_suffix(".json.tmp")
    temporary.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temporary.replace(path)


def create_database(pg: PgAccess, destination_db: str) -> None:
    existing = pg.scalar("postgres", f"SELECT count(*) FROM pg_database WHERE datname = {quote(destination_db)}")
    if existing != "0":
        raise Failure(f"destination database {destination_db} already exists; refusing to overwrite")
    owner = pg.scalar("postgres", "SELECT current_user")
    pg.sql("postgres", f'CREATE DATABASE {quote_identifier(destination_db)} OWNER {quote_identifier(owner)}')


def quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"

def quote_identifier(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-env", default="",
                        help="private source environment (dotenv or flat/nested JSON, mode 0600)")
    parser.add_argument("--source-db", default="", help="the named source database (must be the selected identity)")
    parser.add_argument("--confirm", default="", help="must repeat --source-db")
    parser.add_argument("--source-bucket", default="", help="override S3_BUCKET from the source environment")
    parser.add_argument("--source-data-dir", default="", help="override DATA_DIR from the source environment")
    parser.add_argument("--source-data-container", default="",
                        help="container that holds the source DATA_DIR volume; blobs are read through docker exec")
    parser.add_argument("--source-data-path", default="",
                        help="absolute DATA_DIR inside --source-data-container (default: its DATA_DIR env)")
    parser.add_argument("--source-endpoint", default="", help="override S3_ENDPOINT for a host-network client")
    parser.add_argument("--destination-db", default="", help="default: restore_<utcstamp>_<hex>")
    parser.add_argument("--destination-bucket", default="", help="default: contract-audit-restore-<suffix>")
    parser.add_argument("--destination-root", default="", help="default: <backup-dir>/originals")
    parser.add_argument("--backup-dir", default="", help="default: artifacts/restore-local/<suffix>")
    parser.add_argument("--pg-container", default="", help="explicit running PostgreSQL container id or name")
    parser.add_argument("--postgres-image", default="", help="pinned image for a one-off client")
    parser.add_argument("--awscli-image", default="", help="pinned AWS CLI image")
    parser.add_argument("--verify-only", default="", help="re-verify a previous destination against its manifest")
    args = parser.parse_args(argv)

    if os.environ.get("RESTORE_LOCAL_CONFIRM") != CONFIRM_TOKEN:
        print(f"REFUSED: set RESTORE_LOCAL_CONFIRM={CONFIRM_TOKEN} to run a local backup/restore", file=sys.stderr)
        return 2
    if args.verify_only:
        return run_verify_only(Path(args.verify_only), args)
    if not args.source_env or not args.source_db:
        print("REFUSED: --source-env and --source-db are required", file=sys.stderr)
        return 2
    if args.confirm != args.source_db:
        print(f"REFUSED: --confirm must repeat the source database name {args.source_db!r}", file=sys.stderr)
        return 2
    return run_restore(args)


def run_verify_only(manifest_path: Path, args) -> int:
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        print(f"REFUSED: cannot read manifest {manifest_path}: {error}", file=sys.stderr)
        return 2
    source = manifest.get("source", {})
    destination = manifest.get("destination", {})
    snapshot = source.get("snapshot", {})
    objects = manifest.get("objects", {})
    if not source.get("env") or not source.get("dsn") or not destination.get("db") or not snapshot.get("tables"):
        print("REFUSED: the manifest does not carry a complete capture", file=sys.stderr)
        return 2

    verify_dir = manifest_path.parent / (
        "verify_" + dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S") + "_" + secrets.token_hex(3))
    recorder: Recorder | None = None
    failures: list[str] = []
    checked: dict[str, object] = {}
    reports: dict[str, int] = {}
    permissions: dict[str, object] = {}
    destination_db = str(destination["db"])
    try:
        verify_dir.mkdir(mode=0o700)
        config = load_private_env(Path(str(source["env"])))
        selected = select_source(config)
        expected = source.get("selected_identity", {})
        if (not isinstance(expected, dict) or selected["host"] != expected.get("host")
                or selected["port"] != str(expected.get("port")) or selected["name"] != expected.get("name")):
            raise Failure("the current private source profile does not match the captured source identity")
        recorded_dsn = source["dsn"]
        if isinstance(recorded_dsn, dict):
            recorded_host = str(recorded_dsn.get("host", ""))
            recorded_port = str(recorded_dsn.get("port", ""))
            recorded_database = str(recorded_dsn.get("database", ""))
        elif isinstance(recorded_dsn, str):
            try:
                parsed = urlsplit(recorded_dsn)
                recorded_host = parsed.hostname or ""
                recorded_port = str(parsed.port or DEFAULT_PORT.split(":")[0])
                recorded_database = parsed.path.strip("/")
            except ValueError as error:
                raise Failure("the legacy captured source identity is malformed") from error
        else:
            raise Failure("the captured source identity is malformed")
        if (recorded_host != selected["host"] or recorded_port != selected["port"]
                or recorded_database != selected["name"]):
            raise Failure("the captured DSN identity does not match the current private source profile")
        if (not re.fullmatch(r"[a-z][a-z0-9_]{0,62}", destination_db)
                or destination_db in ("postgres", "template0", "template1", selected["name"])):
            raise Failure("the captured destination database name is unsafe or equals the source")

        schema = safe_schema(config)
        recorder = Recorder(verify_dir / "commands.jsonl", set(config.values()))
        docker = docker_cli()
        postgres_image = args.postgres_image or config.get("POSTGRES_IMAGE") or DEFAULT_POSTGRES_IMAGE
        _raw, host, port, user, password = dsn_parts(str(selected["dsn"]))
        pg = resolve_pg(recorder, host, port, user, password, args.pg_container or None, postgres_image, docker)
        tables = hash_table_set(pg, destination_db, schema, None)
        failures += compare_identities(dict(snapshot["tables"]), tables)
        relationships = evaluate_relationships(pg, destination_db, schema, None, False)
        for name, current in relationships.items():
            recorded = snapshot.get("relationships", {}).get(name)
            if recorded != current:
                failures.append(f"orphan count drifted for {name}: {recorded}->{current}")
        s3, _bucket = build_s3_client(recorder, docker, config, args)
        destination_root = Path(str(destination.get("root", ""))) if destination.get("root") else None
        expected_local = set(objects.get("referenced", {}).get("local", []))
        expected_remote = set(objects.get("referenced", {}).get("remote", []))
        byte_failures, byte_stats = verify_destination_bytes(pg, destination_db, schema, destination_root, s3,
                                                             str(destination.get("bucket", "")))
        failures += byte_failures
        expected_objects: list[tuple[str, str, str, int, str]] = []
        for key in sorted(expected_remote):
            parts = key.split("/")
            if len(parts) != 3:
                failures.append(f"malformed frozen object key {key!r} in the manifest")
                continue
            expected_objects.append((parts[0], parts[1], parts[2], 0, "remote"))
        expected_objects += [("", "", digest, 0, "local") for digest in sorted(expected_local)]
        layout_failures, layout = verify_object_layout(expected_objects, destination_root, s3,
                                                       str(destination.get("bucket", "")))
        failures += layout_failures
        report_failures, reports = verify_reports(pg, destination_db, schema)
        failures += report_failures
        permission_failures, permissions = exercise_permissions(pg, destination_db, schema)
        failures += permission_failures
        checked = {"bytes": byte_stats, "layout": layout, "tables_compared": len(tables)}
    except (Failure, OSError, subprocess.SubprocessError, ValueError) as error:
        failures.append(str(error))
        print(f"FAIL: {error}", file=sys.stderr)
    finally:
        if recorder is not None:
            recorder.flush()

    result = {"verify_only": True, "destination_db": destination_db, "checked": checked,
              "reports": reports, "permissions": permissions,
              "failures": failures, "status": "FAIL" if failures else "PASS"}
    try:
        verify_dir.mkdir(mode=0o700, exist_ok=True)
        (verify_dir / "verify-only.json").write_text(
            json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    except OSError as error:
        failures.append(f"could not write verification evidence: {error}")
        result["failures"] = failures
        result["status"] = "FAIL"
    for failure in failures:
        print(f"FAIL: {failure}", file=sys.stderr)
    print(json.dumps({"verify_only": True, "destination_db": destination_db,
                      "failures": len(failures), "artifact_dir": str(verify_dir),
                      "status": "FAIL" if failures else "PASS"}, ensure_ascii=False, sort_keys=True))
    return 1 if failures else 0


def dsn_parts(raw: str) -> tuple[str, str, str, str, str]:
    url = urlsplit(raw)
    return (raw, url.hostname or DEFAULT_HOST, str(url.port or 5432),
            urllib.parse.unquote(url.username or ""), urllib.parse.unquote(url.password or ""))


def run_restore(args) -> int:
    try:
        config = load_private_env(Path(args.source_env))
        selected = select_source(config)
        if selected["name"] != args.source_db:
            raise Failure(
                f"APP_ENV={selected['mode']} selects database {selected['name']!r}, not the named source {args.source_db!r}"
            )
        connections = service_connections(config, selected)
        schema = safe_schema(config)
    except Failure as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2

    started = dt.datetime.now(dt.timezone.utc)
    suffix = started.strftime("%Y%m%d%H%M%S") + "_" + secrets.token_hex(3)
    destination_db = args.destination_db or f"restore_{suffix}"
    destination_bucket = args.destination_bucket or f"contract-audit-restore-{suffix.replace('_', '-')}"
    backup_dir = Path(args.backup_dir) if args.backup_dir else ROOT / "artifacts" / "restore-local" / suffix
    destination_root = Path(args.destination_root) if args.destination_root else backup_dir / "originals"
    if not re.fullmatch(r"[a-z][a-z0-9_]{0,62}", destination_db) or destination_db in ("postgres", "template0", "template1"):
        print(f"REFUSED: unsafe destination database name {destination_db!r}", file=sys.stderr)
        return 2
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,61}[a-z0-9]", destination_bucket):
        print(f"REFUSED: unsafe destination bucket name {destination_bucket!r}", file=sys.stderr)
        return 2
    if backup_dir.exists():
        print(f"REFUSED: backup directory {backup_dir} already exists; choose another --backup-dir", file=sys.stderr)
        return 2
    if args.destination_root and Path(args.destination_root).exists():
        print(f"REFUSED: destination root {args.destination_root} already exists", file=sys.stderr)
        return 2

    recorder = Recorder(None, set(config.values()))
    failures: list[str] = []
    manifest: dict[str, object] = {"schema_version": 3, "created_at_utc": started.isoformat(),
                                  "failures": failures}
    status: dict[str, object] = {}
    exit_code = 1
    try:
        backup_dir.mkdir(parents=True, mode=0o700)
        recorder = Recorder(backup_dir / "commands.jsonl", set(config.values()))
        docker = docker_cli()
        postgres_image = args.postgres_image or config.get("POSTGRES_IMAGE") or DEFAULT_POSTGRES_IMAGE
        _raw, host, port, user, password = dsn_parts(str(selected["dsn"]))
        pg = resolve_pg(recorder, host, port, user, password, args.pg_container or None, postgres_image, docker)
        probe_authority(pg, selected["name"], schema)

        role_facts = pg.sql("postgres", "SELECT current_user, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user")
        manifest["source"] = {
            "env": os.path.abspath(args.source_env),
            "app_env": config.get("APP_ENV", ""),
            "selected_identity": {"host": selected["host"], "port": selected["port"], "name": selected["name"]},
            "service_dsns_checked": sorted(connections),
            "schema": schema,
            "dsn": {"host": selected["host"], "port": selected["port"], "database": selected["name"]},
            "role": (role_facts[0].split("|") if role_facts else ["", "", ""]),
            "server_version": pg.scalar("postgres", "SHOW server_version"),
        }

        local_objects, data_dir = build_local_objects(config, args, recorder, docker)
        s3, source_bucket = build_s3_client(recorder, docker, config, args)
        ensure_destination_root_isolated(destination_root, local_objects, data_dir, recorder, docker)
        manifest["objects"] = {"source_data_dir": data_dir, "source_bucket": source_bucket,
                               "destination_root": str(destination_root), "destination_bucket": destination_bucket,
                               "runtime_mapping": {
                                   "DATA_DIR": str(destination_root),
                                   "S3_BUCKET": destination_bucket,
                                   "note": "frozen object IDs are preserved; only the destination root/bucket changes",
                               }}

        with Snapshot(pg, selected["name"]) as snapshot:
            representative = require_representative(pg, selected["name"], schema, snapshot.snapshot_id)
            referenced = snapshot_documents(pg, selected["name"], schema, snapshot.snapshot_id)
            if not referenced:
                raise Failure("the snapshot references no originals; refusing to count an empty object set as proof")
            dump_path = backup_dir / "source.dump"
            pg.dump(selected["name"], dump_path, snapshot.snapshot_id)
            source_tables = hash_table_set(pg, selected["name"], schema, snapshot.snapshot_id)
            source_relationships = evaluate_relationships(pg, selected["name"], schema, snapshot.snapshot_id, True)
            manifest["source"]["representative"] = representative
            manifest["source"]["snapshot"] = {
                "id": snapshot.snapshot_id,
                "tables": source_tables,
                "relationships": source_relationships,
                "dump": {"path": str(dump_path), "sha256": sha256_file(dump_path),
                         "bytes": dump_path.stat().st_size},
            }
            manifest["objects"]["referenced"] = {
                "local": sorted({digest for _t, _c, digest, _s, backend in referenced if backend == "local"}),
                "remote": sorted({f"{tenant}/{case}/{digest}" for tenant, case, digest, _s, backend in referenced
                                  if backend == "remote"}),
            }

        remote_references = [item for item in referenced if item[4] == "remote"]
        if remote_references:
            if s3 is None:
                raise Failure("the snapshot references remote originals but no source/destination S3 client is configured")
            if source_bucket == destination_bucket:
                raise Failure("the destination bucket must be different from the source bucket")
            if s3.bucket_exists(destination_bucket):
                raise Failure(f"destination bucket {destination_bucket} already exists; refusing to overwrite it")
        create_database(pg, destination_db)
        manifest["destination"] = {"db": destination_db, "bucket": destination_bucket, "root": str(destination_root),
                                   "created_utc": dt.datetime.now(dt.timezone.utc).isoformat()}
        if remote_references:
            s3.create_bucket(destination_bucket)
        pg.restore(destination_db, dump_path)

        copy_report = copy_objects(referenced, local_objects, s3, source_bucket, destination_bucket, destination_root)
        manifest["objects"]["copy"] = copy_report
        local_failures = list(copy_report["local"]["failures"])  # type: ignore[index]
        remote_failures = list(copy_report["remote"]["failures"])  # type: ignore[index]
        if local_failures or remote_failures:
            raise Failure("not every referenced original could be copied: " +
                          "; ".join((local_failures + remote_failures)[:3]))

        destination_tables = hash_table_set(pg, destination_db, schema, None)
        destination_relationships = evaluate_relationships(pg, destination_db, schema, None, False)
        failures += compare_identities(source_tables, destination_tables)
        failures += compare_relationships(source_relationships, destination_relationships)
        byte_failures, byte_stats = verify_destination_bytes(pg, destination_db, schema, destination_root, s3, destination_bucket)
        failures += byte_failures
        layout_failures, layout = verify_object_layout(referenced, destination_root, s3, destination_bucket)
        failures += layout_failures
        report_failures, report_stats = verify_reports(pg, destination_db, schema)
        failures += report_failures
        permission_failures, permission_report = exercise_permissions(pg, destination_db, schema)
        failures += permission_failures
        status = {"bytes": byte_stats, "layout": layout, "reports": report_stats,
                  "permissions": permission_report, "tables_compared": len(destination_tables)}
        manifest["objects"]["layout"] = layout

        manifest["verification"] = status
        manifest["failures"] = failures
        manifest["capability"] = {
            "BACKUP_RESTORE": "PASS" if not failures else "FAIL",
            "MULTI_NODE_HA": "NOT_RUN (single host; no cluster)",
            "DISASTER_RECOVERY": "NOT_RUN (no cross-failure-domain restore)",
            "RPO_RTO": "NOT_RUN (local snapshot only; no WAL/PITR)",
        }
        manifest["limits"] = [
            "One exported REPEATABLE READ snapshot pins pg_dump and every source comparison; the source keeps running.",
            "Originals are copied only for the snapshot's referenced keys, under their frozen IDs; nothing is rewritten to a new prefix.",
            "No worker, relay or external model call is started for the destination; never attach services to it.",
            "Same-cluster restore proves local BACKUP_RESTORE only: not RPO/RTO, not quiesced backup, not multi-node isolation.",
            "The restored service-role exercise switches identity with SET ROLE, so the migrator connection must be a "
            "superuser, the database owner, or a member of the restored service roles.",
        ]
        manifest["destination"]["handoff"] = (
            "No service may be pointed at this destination. If it is ever promoted, set APP_READ_ONLY=true and start no worker/relay."
        )
        write_manifest(backup_dir / "manifest.json", manifest)
        exit_code = 1 if failures else 0
        for failure in failures:
            print(f"FAIL: {failure}", file=sys.stderr)
        print(json.dumps({"source_db": args.source_db, "destination_db": destination_db,
                          "backup_dir": str(backup_dir), "failures": len(failures),
                          "status": "FAIL" if failures else "PASS"}, ensure_ascii=False, sort_keys=True))
    except (Failure, OSError, subprocess.SubprocessError, ValueError) as error:
        failures.append(str(error))
        manifest["failures"] = failures
        if backup_dir.exists():
            write_manifest(backup_dir / "manifest.json", manifest)
        print(f"FAIL: {error}", file=sys.stderr)
        exit_code = 1
    finally:
        recorder.flush()
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())

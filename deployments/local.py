#!/usr/bin/env python3
"""Bootstrap private distributed settings and operate the local Compose profile."""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import subprocess
import sys
import time
from urllib.parse import urlsplit
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
ROOT = Path(__file__).resolve().parents[1]
ENV_PATH = Path(os.environ.get("INFRA_ENV_PATH") or (ROOT / ".env")).expanduser().resolve()
# Keep distributed credentials beside a custom infrastructure profile unless
# the caller explicitly selects another private file.
if os.environ.get("DISTRIBUTED_ENV_PATH"):
	DISTRIBUTED_ENV_PATH = Path(os.environ["DISTRIBUTED_ENV_PATH"]).expanduser().resolve()
elif ENV_PATH != ROOT / ".env":
	DISTRIBUTED_ENV_PATH = ENV_PATH.parent / ".env.distributed"
else:
	DISTRIBUTED_ENV_PATH = ROOT / ".env.distributed"
FIXTURE_CHAT_MODEL = "fixture-chat"
FIXTURE_OCR_MODEL = "fixture-ocr"
FIXTURE_MODEL_GATEWAY_URL = "http://model-gateway-fixture:8084/v1"
DATABASE_TARGETS = {"development": "DEV_DATABASE_URL", "test": "TEST_DATABASE_URL"}
APP_SERVICES = (
	"health-monitor",
	"gateway", "core-api", "document-service", "audit-execution", "audit-worker",
	"model-gateway", "model-gateway-fixture", "tool-gateway", "client", "admin", "portal",
)
# Observation follows applications so infrastructure can run independently.
OBSERVATION_SERVICES = ("otel-collector", "tempo", "prometheus", "grafana")

SUBPROCESS_RECORDS: list[dict[str, object]] = []
PREFLIGHT_RESULTS: dict[str, object] = {}
BUSINESS_JOURNEYS: list[dict[str, object]] = []


def compose_project_name() -> str:
	if ENV_PATH == ROOT / ".env":
		name = os.environ.get("COMPOSE_PROJECT_NAME", "").strip()
		if name and not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,62}", name):
			raise ValueError("COMPOSE_PROJECT_NAME must be a lowercase Compose project name")
		return name
	name = os.environ.get("COMPOSE_PROJECT_NAME", "").strip()
	profile = re.sub(r"[^a-z0-9_-]+", "-", ENV_PATH.parent.name.lower()).strip("-_")[:35] or "private"
	digest = hashlib.sha256(str(ENV_PATH).encode()).hexdigest()[:8]
	derived = f"contract-audit-{profile}-{digest}"
	if name and name != derived:
		raise ValueError("COMPOSE_PROJECT_NAME must match the isolated profile path")
	return derived


def database_name(infra: dict[str, str], key: str) -> str:
	dsn = urlsplit(infra.get(key, ""))
	database = dsn.path.lstrip("/")
	if not re.fullmatch(r"contract_audit_(?:dev|test)(?:_[a-f0-9]{8})?", database):
		raise ValueError(f"{key} does not name a private local database")
	return database


def database_target_key() -> str:
	target = os.environ.get("EV02_DATABASE_TARGET", "development").strip() or "development"
	try:
		return DATABASE_TARGETS[target]
	except KeyError as error:
		raise ValueError("EV02_DATABASE_TARGET must be development or test") from error


def application_database_slot(infra: dict[str, str]) -> str:
	mode = infra.get("APP_ENV", "development").strip().lower() or "development"
	if mode in {"development", "dev"}:
		return "DEV_DATABASE_URL"
	if mode == "test":
		return "TEST_DATABASE_URL"
	if mode == "production":
		return "PROD_DATABASE_URL"
	raise ValueError("APP_ENV must be development, test, or production for migration database selection")


def application_database_name(infra: dict[str, str]) -> str:
	slot = application_database_slot(infra)
	if slot in {"DEV_DATABASE_URL", "TEST_DATABASE_URL"}:
		name = database_name(infra, slot)
		prefix = "contract_audit_dev" if slot == "DEV_DATABASE_URL" else "contract_audit_test"
		if name != prefix and not re.fullmatch(re.escape(prefix) + r"_[a-f0-9]{8}", name):
			raise ValueError(f"{slot} must select its matching isolated local database")
		return name
	prod_dsn = urlsplit(infra.get(slot, ""))
	name = prod_dsn.path.lstrip("/")
	if (prod_dsn.scheme not in {"postgres", "postgresql"} or not prod_dsn.hostname or
			not prod_dsn.username or not prod_dsn.password or name != "contract_audit_prod"):
		raise ValueError("APP_ENV=production requires a private PROD_DATABASE_URL naming contract_audit_prod")
	return name

def read_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or not key.replace("_", "").isalnum() or not key[0].isalpha():
            raise ValueError(f"Invalid dotenv syntax at {path}:{number}")
        if key in values:
            raise ValueError(f"Duplicate environment key {key} in {path}")
        values[key] = value
    return values


def _atomic_update_env(path: Path, updates: dict[str, str]) -> None:
	"""Replace only named private profile values without rotating other secrets."""
	original = path.read_bytes()
	lines = original.decode("utf-8").splitlines()
	seen: set[str] = set()
	result: list[str] = []
	for raw in lines:
		stripped = raw.strip()
		key, separator, _ = stripped.partition("=")
		if separator and key in updates:
			result.append(f"{key}={updates[key]}")
			seen.add(key)
		else:
			result.append(raw)
	for key, value in updates.items():
		if key not in seen:
			result.append(f"{key}={value}")
	temporary = path.with_name(path.name + f".{os.getpid()}.tmp")
	if path.read_bytes() != original:
		raise RuntimeError(f"{path} changed during the private migrator profile upgrade; no update was applied")
	fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
	try:
		with os.fdopen(fd, "w", encoding="utf-8") as stream:
			stream.write("\n".join(result) + "\n")
			stream.flush()
			os.fsync(stream.fileno())
		if path.read_bytes() != original:
			raise RuntimeError(f"{path} changed during the private migrator profile upgrade; no update was applied")
		os.replace(temporary, path)
		os.chmod(path, 0o600)
	finally:
		if temporary.exists():
			temporary.unlink()


def infrastructure_report_path(action: str) -> Path:
	return ENV_PATH.parent / f"infra-{action}-{time.time_ns()}.json"


def infra_bootstrap() -> None:
	report_path = infrastructure_report_path("bootstrap")
	run([sys.executable, str(ROOT / "deploy/scripts/infra.py"), "bootstrap"],
		environment={"INFRA_EVIDENCE_PATH": str(report_path)})


def observation_services(values: dict[str, str]) -> tuple[str, ...]:
	if values.get("OBSERVE", "").strip().lower() in {"1", "true", "yes"}:
		return OBSERVATION_SERVICES
	return ()


def compose_prefix(distributed_env_path: Path | None = None) -> list[str]:
	binary = os.environ.get("COMPOSE_BIN")
	if binary:
		command = [binary]
	else:
		docker = os.environ.get("DOCKER_BIN", "docker")
		if not shutil.which(docker):
			raise RuntimeError("Docker CLI is unavailable; install Docker Engine/Desktop with Compose v2")
		command = [docker, "compose"]
	project = compose_project_name()
	if project:
		command.extend(["-p", project])
	command.extend([
		"--project-directory", str(ROOT),
		"--env-file", str(ENV_PATH),
		"--env-file", str(distributed_env_path or DISTRIBUTED_ENV_PATH),
		"-f", str(ROOT / "compose.yaml"),
		"-f", str(ROOT / "compose.app.yaml"),
	])
	values = {**read_env(ENV_PATH), **read_env(distributed_env_path or DISTRIBUTED_ENV_PATH), **os.environ}
	if values.get("IDENTITY_DIRECTORY_ROOT", "").strip():
		command.extend(["-f", str(ROOT / "deployments/compose.identity.yaml")])
	elif values.get("OIDC_ISSUER", "").strip():
		raise RuntimeError("Compose OIDC requires IDENTITY_DIRECTORY_ROOT and IDENTITY_DIRECTORY_GID")
	if observation_services(values):
		command.extend(["-f", str(ROOT / "compose.observe.yaml"), "--profile", "otel"])
	return command


def generated_values(infra: dict[str, str]) -> dict[str, str]:
	postgres_password = infra["POSTGRES_PASSWORD"]
	database = application_database_name(infra)
	selected_slot = application_database_slot(infra)
	migrator_dsn = _dsn("audit_offline_migrator", database, secrets.token_urlsafe(48))
	edge_port = infra.get("EDGE_PORT", "8080")
	client_port = infra.get("CLIENT_PORT", "18080")
	admin_port = infra.get("ADMIN_PORT", "18081")
	portal_port = infra.get("PORTAL_PORT", "18082")
	values: dict[str, str] = {
		"CORE_DATABASE_URL": _dsn("core_api", database, secrets.token_hex(32)),
		"DOCUMENT_DATABASE_URL": _dsn("document_service", database, secrets.token_hex(32)),
		"EXECUTION_DATABASE_URL": _dsn("audit_execution", database, secrets.token_hex(32)),
		"MODEL_DATABASE_URL": _dsn("model_gateway", database, secrets.token_hex(32)),
		"TOOL_DATABASE_URL": _dsn("tool_gateway", database, secrets.token_hex(32)),
		"MIGRATOR_DATABASE_URL": migrator_dsn,
		"DEV_DATABASE_URL": migrator_dsn if selected_slot == "DEV_DATABASE_URL" else "",
		"TEST_DATABASE_URL": migrator_dsn if selected_slot == "TEST_DATABASE_URL" else "",
		"PROD_DATABASE_URL": migrator_dsn if selected_slot == "PROD_DATABASE_URL" else "",
		# Superuser identity is used only by short-lived offline provisioning.
		"AUTH_ADMIN_DATABASE_URL": _dsn("contractaudit", database, postgres_password),
		"GATEWAY_CORE_TOKEN": secrets.token_urlsafe(48),
		"GATEWAY_DOCUMENT_TOKEN": secrets.token_urlsafe(48),
		"GATEWAY_EXECUTION_TOKEN": secrets.token_urlsafe(48),
		"GATEWAY_TOOL_TOKEN": secrets.token_urlsafe(48),
		"WORKER_DOCUMENT_TOKEN": secrets.token_urlsafe(48),
		"WORKER_MODEL_TOKEN": secrets.token_urlsafe(48),
		"WORKER_TOOL_TOKEN": secrets.token_urlsafe(48),
		"WORKER_CORE_TOKEN": secrets.token_urlsafe(48),
		"CORE_DOCUMENT_TOKEN": secrets.token_urlsafe(48),
		"TOOL_CORE_AUTHORITY_TOKEN": secrets.token_urlsafe(48),
		"CORE_MODEL_AUTHORITY_TOKEN": secrets.token_urlsafe(48),
		"DOCUMENT_MODEL_TOKEN": secrets.token_urlsafe(48),
		"CORE_URLS": "http://core-api:8081",
		"DOCUMENT_URL": "http://document-service:8082",
		"EXECUTION_URL": "http://audit-execution:8083",
		"TOOL_URLS": "http://tool-gateway:8085",
		"DOCUMENT_SERVICE_URL": "http://document-service:8082",
		"CORE_SERVICE_URL": "http://core-api:8081",
		"CORE_AUTHORITY_URL": _core_authority_url(infra),
		"CORE_AUTHORITY_CA_FILE": infra.get("CORE_AUTHORITY_CA_FILE", "").strip(),
		"CORE_AUTHORITY_ALLOW_INSECURE_DEV": _core_authority_dev_flag(infra),
		"MODEL_GATEWAY_URL": "http://model-gateway:8084/v1",
		"NATS_URL": "nats://nats:4222",
		"LLM_BASE_URL": _container_model_url(infra.get("LLM_BASE_URL", "http://127.0.0.1:3425/v1")),
		"EDGE_PORT": edge_port,
		"CLIENT_PORT": client_port,
		"ADMIN_PORT": admin_port,
		"PORTAL_PORT": portal_port,
		"CORS_ORIGIN": ",".join(f"http://127.0.0.1:{port}" for port in (client_port, admin_port, portal_port)),
	}
	return values




def _dsn(role: str, database: str, password: str) -> str:
    return f"postgres://{role}:{password}@db:5432/{database}?sslmode=disable"


def _container_model_url(value: str) -> str:
    for host in ("127.0.0.1", "localhost"):
        prefix = f"http://{host}:"
        if value.startswith(prefix):
            return "http://host.docker.internal:" + value[len(prefix):]
    return value


def _core_authority_url(infra: dict[str, str]) -> str:
	app_env = infra.get("APP_ENV", "development").strip().lower() or "development"
	if app_env in {"development", "dev", "test"}:
		return "http://core-api:8081"
	return infra.get("CORE_AUTHORITY_URL", "https://REPLACE-ME-CORE-AUTH-ENDPOINT").strip()


def _core_authority_dev_flag(infra: dict[str, str]) -> str:
	app_env = infra.get("APP_ENV", "development").strip().lower() or "development"
	return "true" if app_env in {"development", "dev", "test"} else "false"


def validate_core_authority(values: dict[str, str]) -> None:
	app_env = values.get("APP_ENV", "development").strip().lower() or "development"
	if app_env not in {"development", "dev", "test", "production"}:
		raise ValueError("APP_ENV must be development, dev, test, or production for Core authority callback")
	raw_flag = values.get("CORE_AUTHORITY_ALLOW_INSECURE_DEV", "false").strip()
	if raw_flag not in {"true", "false"}:
		raise ValueError("CORE_AUTHORITY_ALLOW_INSECURE_DEV must be true or false")
	allow_dev = raw_flag == "true"
	if allow_dev and app_env == "production":
		raise ValueError("CORE_AUTHORITY_ALLOW_INSECURE_DEV is forbidden in production")
	endpoint = values.get("CORE_AUTHORITY_URL", "").strip()
	try:
		parsed = urlsplit(endpoint)
		port = parsed.port
		if (parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username is not None or
				parsed.password is not None or parsed.query or parsed.fragment or parsed.path not in {"", "/"}):
			raise ValueError("invalid URL")
	except ValueError as error:
		raise ValueError("CORE_AUTHORITY_URL must be an absolute origin without credentials, path, query, or fragment") from error
	host = parsed.hostname.lower().rstrip(".")
	if (host.startswith("replace-me") or host in {"example.com", "example.net", "example.org"} or
			host.endswith((".example.com", ".example.net", ".example.org")) or ".example." in host):
		raise ValueError("CORE_AUTHORITY_URL still contains an operator placeholder")
	ca_file = values.get("CORE_AUTHORITY_CA_FILE", "").strip()
	if ca_file:
		ca_path = Path(ca_file).expanduser()
		if (not ca_path.is_absolute() or not ca_path.is_file() or not os.access(ca_path, os.R_OK) or
				ca_path.stat().st_size == 0):
			raise ValueError("CORE_AUTHORITY_CA_FILE must be a readable, nonempty absolute PEM path mounted at the same container path")
	if parsed.scheme == "https":
		return
	if not allow_dev or app_env not in {"development", "dev", "test"}:
		raise ValueError("Core authority callback requires HTTPS outside the explicit development profile")
	if host == "core-api" and port == 8081:
		return
	if port is not None and (host == "localhost" or host in {"127.0.0.1", "::1"}):
		return
	raise ValueError("development HTTP callback is limited to http://core-api:8081 or explicit loopback origins")


def validate_experience_delivery(values: dict[str, str]) -> None:
	validate_core_authority(values)
	auth_admin_dsn = urlsplit(values.get("AUTH_ADMIN_DATABASE_URL", "").strip())
	migrator_dsn = urlsplit(values.get("MIGRATOR_DATABASE_URL", "").strip())
	if (auth_admin_dsn.scheme not in {"postgres", "postgresql"} or not auth_admin_dsn.hostname or
			not auth_admin_dsn.username or not auth_admin_dsn.password or
			migrator_dsn.scheme not in {"postgres", "postgresql"} or not migrator_dsn.hostname or
			not migrator_dsn.username or not migrator_dsn.password):
		raise ValueError("AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL must be configured privately")
	if (auth_admin_dsn.path != migrator_dsn.path or auth_admin_dsn.hostname != migrator_dsn.hostname or
			(auth_admin_dsn.port or 5432) != (migrator_dsn.port or 5432) or
			auth_admin_dsn.username == migrator_dsn.username or migrator_dsn.username != "audit_offline_migrator"):
		raise ValueError("AUTH_ADMIN_DATABASE_URL must be a distinct login for the same local database as MIGRATOR_DATABASE_URL")
	authority_credential = values.get("TOOL_CORE_AUTHORITY_TOKEN", "").strip()
	if len(authority_credential) < 32:
		raise ValueError("TOOL_CORE_AUTHORITY_TOKEN must contain at least 32 characters")
	for name in (
		"GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
		"WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN",
		"CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN",
	):
		if authority_credential == values.get(name, "").strip():
			raise ValueError(f"TOOL_CORE_AUTHORITY_TOKEN must differ from {name}")
	model_authority_credential = values.get("CORE_MODEL_AUTHORITY_TOKEN", "").strip()
	if len(model_authority_credential) < 32:
		raise ValueError("CORE_MODEL_AUTHORITY_TOKEN must contain at least 32 characters")
	for name in (
		"GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
		"WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN",
		"CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN",
	):
		if model_authority_credential == values.get(name, "").strip():
			raise ValueError(f"CORE_MODEL_AUTHORITY_TOKEN must differ from {name}")
	endpoint = values.get("CORE_SERVICE_URL", "").strip()
	credential = values.get("WORKER_CORE_TOKEN", "").strip()
	try:
		parsed = urlsplit(endpoint)
		if (parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username is not None or
				parsed.query or parsed.fragment):
			raise ValueError("invalid endpoint")
		parsed.port  # Reject malformed or out-of-range ports before invoking Compose.
	except ValueError as error:
		raise ValueError("CORE_SERVICE_URL must be an HTTP(S) service URL without credentials, query, or fragment") from error
	if len(credential) < 32:
		raise ValueError("WORKER_CORE_TOKEN must contain at least 32 characters")
	for name in ("GATEWAY_CORE_TOKEN", "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN"):
		if credential == values.get(name, "").strip():
			raise ValueError(f"WORKER_CORE_TOKEN must differ from {name}")



def ensure_private_environment() -> dict[str, str]:
	infra_bootstrap()
	infra = read_env(ENV_PATH)
	expected = generated_values(infra)
	if DISTRIBUTED_ENV_PATH.exists():
		current = read_env(DISTRIBUTED_ENV_PATH)
		missing = set(expected) - current.keys()
		allowed_upgrade_keys = {
			"AUTH_ADMIN_DATABASE_URL", "CORE_AUTHORITY_URL", "CORE_AUTHORITY_CA_FILE",
			"CORE_AUTHORITY_ALLOW_INSECURE_DEV", "CORE_MODEL_AUTHORITY_TOKEN",
			"TOOL_CORE_AUTHORITY_TOKEN", "DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL",
		}
		unexpected_missing = sorted(missing - allowed_upgrade_keys)
		if unexpected_missing:
			raise RuntimeError(
				f"{DISTRIBUTED_ENV_PATH} already exists and is missing unsupported keys {', '.join(unexpected_missing)}; "
				"it was left untouched. Add those values manually or keep a separate copy before regenerating."
			)
		if DISTRIBUTED_ENV_PATH.stat().st_mode & 0o077:
			raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} must have mode 0600")
		expected_database = application_database_name(infra)
		for key in ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL", "AUTH_ADMIN_DATABASE_URL"):
			if key in current and urlsplit(current[key]).path.lstrip("/") != expected_database:
				raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} targets a different database than selected APP_ENV; select a matching profile file")
		selected = application_database_slot(infra)
		current_migrator = urlsplit(current["MIGRATOR_DATABASE_URL"])
		if "AUTH_ADMIN_DATABASE_URL" in missing and current_migrator.username == "audit_offline_migrator":
			raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} is missing AUTH_ADMIN_DATABASE_URL and its legacy administrator DSN cannot be inferred safely")
		new_migrator = (current["MIGRATOR_DATABASE_URL"] if current_migrator.username == "audit_offline_migrator"
					else expected["MIGRATOR_DATABASE_URL"])
		updates: dict[str, str] = {}
		for key in missing:
			if key == "AUTH_ADMIN_DATABASE_URL":
				updates[key] = current["MIGRATOR_DATABASE_URL"]
			elif key in {"DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL"}:
				updates[key] = new_migrator if key == selected else expected[key]
			else:
				updates[key] = expected[key]
		if current_migrator.username != "audit_offline_migrator":
			updates["MIGRATOR_DATABASE_URL"] = new_migrator
			updates[selected] = new_migrator
		elif current.get(selected, new_migrator) != new_migrator:
			raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} must route {selected} to the dedicated offline migrator")
		for slot in {"DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL"} - {selected}:
			if slot in current and current[slot] != expected[slot]:
				raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} has an inconsistent {slot} selection for APP_ENV; it was left unchanged")
		if updates:
			_atomic_update_env(DISTRIBUTED_ENV_PATH, updates)
			current.update(updates)
		for slot in ("DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL"):
			if slot == selected:
				if current.get(slot) != current["MIGRATOR_DATABASE_URL"]:
					raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} must route {selected} to the dedicated offline migrator")
			elif current.get(slot, "") != expected[slot]:
				raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} has an inconsistent {slot} selection for APP_ENV; it was left unchanged")
		for key in ("EDGE_PORT", "CLIENT_PORT", "ADMIN_PORT", "PORTAL_PORT", "CORS_ORIGIN"):
			if current[key] != expected[key]:
				raise RuntimeError(f"{DISTRIBUTED_ENV_PATH} has a {key} value that does not match {ENV_PATH}")
		validate_experience_delivery({**infra, **current, **os.environ})
		return current
	values = expected
	validate_experience_delivery({**infra, **values, **os.environ})
	body = "# Private local distributed profile. Keep mode 0600; never commit.\n"
	body += "".join(f"{key}={value}\n" for key, value in values.items())
	DISTRIBUTED_ENV_PATH.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
	descriptor = os.open(DISTRIBUTED_ENV_PATH, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
	with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
		stream.write(body)
	return values


def _sensitive_values() -> list[str]:
    sensitive: set[str] = set()
    for path in (ENV_PATH, DISTRIBUTED_ENV_PATH):
        if not path.is_file():
            continue
        for key, value in read_env(path).items():
            if any(marker in key for marker in ("PASSWORD", "SECRET", "TOKEN", "API_KEY", "STATE_KEY", "DATABASE_URL", "ACCESS_KEY")):
                if value:
                    sensitive.add(value)
    return sorted(sensitive, key=len, reverse=True)


def _redact(text: str) -> str:
    for value in _sensitive_values():
        text = text.replace(value, "<redacted>")
    return text


def run(command: list[str], *, input_text: str | None = None, quiet: bool = False,
        timeout: int = 900, environment: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    started = time.monotonic()
    process_environment = {**os.environ, **environment} if environment else None
    try:
        process = subprocess.run(command, cwd=ROOT, input=input_text, text=True, capture_output=True,
                                 timeout=timeout, env=process_environment)
    except subprocess.TimeoutExpired as error:
        SUBPROCESS_RECORDS.append({"argv": command, "returncode": None, "timed_out": True,
                                   "timeout_seconds": timeout, "seconds": round(time.monotonic() - started, 3),
                                   "environment_override_keys": sorted(environment or {})})
        raise error
    stdout = process.stdout or ""
    stderr = process.stderr or ""
    record: dict[str, object] = {
        "argv": command,
        "returncode": process.returncode,
        "seconds": round(time.monotonic() - started, 3),
        "stdout_bytes": len(stdout.encode()),
        "stdout_sha256": hashlib.sha256(stdout.encode()).hexdigest(),
        "stderr_bytes": len(stderr.encode()),
        "stderr_sha256": hashlib.sha256(stderr.encode()).hexdigest(),
        "environment_override_keys": sorted(environment or {}),
    }
    if environment and "INFRA_EVIDENCE_PATH" in environment:
        record["public_environment_overrides"] = {"INFRA_EVIDENCE_PATH": environment["INFRA_EVIDENCE_PATH"]}
    SUBPROCESS_RECORDS.append(record)
    if not quiet or process.returncode:
        if stdout:
            sys.stdout.write(_redact(stdout))
        if stderr:
            sys.stderr.write(_redact(stderr))
    if process.returncode:
        raise subprocess.CalledProcessError(process.returncode, command, stdout, stderr)
    if any(argument.endswith("journey-local.py") for argument in command):
        for line in stdout.splitlines():
            try:
                summary = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(summary, dict) and {"case_id", "run_id", "job_id"}.issubset(summary):
                BUSINESS_JOURNEYS.append(summary)
                break
    return process

def ensure_databases() -> None:
	infra = read_env(ENV_PATH)
	databases = (database_name(infra, "DEV_DATABASE_URL"), database_name(infra, "TEST_DATABASE_URL"))
	statements = []
	for database in databases:
		statements.append(
			f"SELECT format('CREATE DATABASE %I OWNER contractaudit', '{database}') "
			f"WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '{database}')\n\\gexec"
		)
	sql = "\n".join(statements) + "\n"
	run(compose_prefix() + ["exec", "-T", "db", "psql", "-U", "contractaudit", "-d", "postgres", "-v", "ON_ERROR_STOP=1"], input_text=sql)

def model_provider_preflight(values: dict[str, str]) -> dict[str, object]:
	mode = values.get("AGENT_PLANNER_MODE", "deterministic").strip() or "deterministic"
	base = values.get("LLM_BASE_URL", "").strip().rstrip("/")
	chat_model = values.get("LLM_MODEL", "").strip()
	ocr_model = values.get("OCR_MODEL", "").strip()
	if mode == "fixture":
		return {"status": "NOT_REQUIRED_FIXTURE", "planner_mode": mode,
				"live_planner_call": "NOT_RUN"}
	if not base:
		return {"status": "NOT_CONFIGURED", "planner_mode": mode,
				"live_planner_call": "NOT_RUN"}
	try:
		parsed = urlsplit(base)
		if parsed.scheme not in {"http", "https"} or not parsed.hostname:
			raise ValueError("invalid_endpoint")
		port = f":{parsed.port}" if parsed.port else ""
		safe_endpoint = f"{parsed.scheme}://{parsed.hostname}{port}{parsed.path}"
	except ValueError:
		return {"status": "BLOCKED", "planner_mode": mode, "reason": "invalid_endpoint",
				"live_planner_call": "NOT_RUN"}
	request = Request(base + "/models", headers={"Accept": "application/json"}, method="GET")
	api_key = values.get("LLM_API_KEY", "").strip()
	if api_key:
		request.add_header("Authorization", f"Bearer {api_key}")
	try:
		with urlopen(request, timeout=5) as response:
			catalog = json.load(response)
			status_code = response.status
	except HTTPError as error:
		return {"status": "BLOCKED", "planner_mode": mode, "endpoint": safe_endpoint,
				"http_status": error.code, "reason": "provider_http_error", "live_planner_call": "NOT_RUN"}
	except URLError as error:
		reason = "provider_timeout" if isinstance(error.reason, TimeoutError) else "provider_unreachable"
		return {"status": "BLOCKED", "planner_mode": mode, "endpoint": safe_endpoint,
				"reason": reason, "live_planner_call": "NOT_RUN"}
	except (TimeoutError, OSError):
		return {"status": "BLOCKED", "planner_mode": mode, "endpoint": safe_endpoint,
				"reason": "provider_unreachable", "live_planner_call": "NOT_RUN"}
	except ValueError:
		return {"status": "BLOCKED", "planner_mode": mode, "endpoint": safe_endpoint,
				"reason": "invalid_catalog_response", "live_planner_call": "NOT_RUN"}
	if not isinstance(catalog, dict) or not isinstance(catalog.get("data"), list):
		return {"status": "BLOCKED", "planner_mode": mode, "endpoint": safe_endpoint,
				"http_status": status_code, "reason": "invalid_catalog_response", "live_planner_call": "NOT_RUN"}
	model_ids = {item.get("id") for item in catalog["data"] if isinstance(item, dict) and isinstance(item.get("id"), str)}
	chat_present = bool(chat_model and chat_model in model_ids)
	ocr_present = bool(ocr_model and ocr_model in model_ids)
	return {"status": "PASS" if chat_present and ocr_present else "BLOCKED",
			"planner_mode": mode, "endpoint": safe_endpoint, "http_status": status_code,
			"configured_models": {"chat": chat_model, "ocr": ocr_model},
			"catalog": {"chat_present": chat_present, "ocr_present": ocr_present},
			"reason": None if chat_present and ocr_present else "configured_model_missing_from_catalog",
			"live_planner_call": "NOT_RUN"}
def preflight() -> None:
	infra = read_env(ENV_PATH)
	app = read_env(DISTRIBUTED_ENV_PATH)
	run(compose_prefix() + ["config", "--quiet"], quiet=True)
	ports = {
		"POSTGRES_PORT": "db",
		"RUSTFS_PORT": "rustfs",
		"RUSTFS_CONSOLE_PORT": "rustfs",
		"KEYCLOAK_PORT": "keycloak",
		"EDGE_PORT": "gateway",
		"CLIENT_PORT": "client",
		"ADMIN_PORT": "admin",
		"PORTAL_PORT": "portal",
	}
	port_results: dict[str, dict[str, object]] = {}
	conflicts: list[str] = []
	for key, service in ports.items():
		value = app.get(key, infra.get(key, ""))
		if not value:
			continue
		port = int(value)
		probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
		try:
			probe.bind(("127.0.0.1", port))
			port_results[key] = {"port": port, "status": "free"}
		except OSError:
			docker = os.environ.get("DOCKER_BIN", "docker")
			owners = run([docker, "ps", "--filter", f"publish={port}", "--format", "{{.Names}}"], quiet=True)
			names = owners.stdout.splitlines()
			owned = any(name.startswith(f"{compose_project_name()}-{service}-") for name in names)
			port_results[key] = {"port": port, "status": "owned_by_profile" if owned else "conflict",
							"service": service if owned else None}
			if not owned:
				conflicts.append(key)
		finally:
			probe.close()

	dev_database = database_name(infra, "DEV_DATABASE_URL")
	test_database = database_name(infra, "TEST_DATABASE_URL")
	query = (
		"SELECT datname FROM pg_database WHERE datname IN "
		f"('{dev_database}', '{test_database}') ORDER BY datname;"
	)
	database_result = run(compose_prefix() + [
		"exec", "-T", "db", "psql", "-U", "contractaudit", "-d", "postgres",
		"-v", "ON_ERROR_STOP=1", "-At", "-c", query,
	], quiet=True)
	available_databases = database_result.stdout.splitlines()
	if available_databases != sorted((dev_database, test_database)):
		raise RuntimeError("preflight did not find both isolated development and test databases")
	run(compose_prefix() + ["--profile", "tools", "run", "--rm", "--no-deps", "--pull", "never", "s3-smoke"])

	image_report = f"artifacts/c1c/ev-02/preflight-images-{time.time_ns()}.json"
	run([sys.executable, str(ROOT / "deployments/scripts/package-app-images.py"),
		 "--preflight", "--report", image_report], quiet=True)
	issuer = infra.get("OIDC_ISSUER", "").strip()
	if issuer:
		if not infra.get("IDENTITY_DIRECTORY_ROOT", "").strip() or not infra.get("IDENTITY_DIRECTORY_GID", "").strip():
			raise RuntimeError("OIDC issuer is configured without the required private identity directory mount")
		with urlopen(issuer.rstrip("/") + "/.well-known/openid-configuration", timeout=5) as response:
			discovery = json.load(response)
		if discovery.get("issuer") != issuer:
			raise RuntimeError("OIDC discovery issuer did not match the configured issuer")
		oidc = {"status": "READY", "issuer": issuer}
	else:
		oidc = {"status": "NOT_CONFIGURED", "reason": "local development account mode; no OIDC issuer supplied"}
	PREFLIGHT_RESULTS.update({
		"model_provider": model_provider_preflight({**infra, **app, **os.environ}),
		"status": "PASS",
		"ports": port_results,
		"databases": {"status": "PASS", "names": sorted(available_databases)},
		"object_store": {"status": "PASS", "bucket": infra.get("S3_BUCKET", "")},
		"oidc": oidc,
		"frontend_artifacts": {"status": "PASS", "image_preflight_report": image_report},
		"model_mode_before_app_start": "NOT_STARTED",
		"topology": {"infrastructure": "Docker Compose containers", "nats": "Docker Compose container",
					 "applications": "not started by preflight", "frontends": "three Compose containers"},
	})
	if conflicts:
		PREFLIGHT_RESULTS["status"] = "BLOCKED"
		PREFLIGHT_RESULTS["port_conflicts"] = conflicts
		raise RuntimeError("preflight found host port conflicts: " + ", ".join(conflicts))


def start_infrastructure(identity: bool) -> None:
    command = [sys.executable, str(ROOT / "deploy/scripts/infra.py"), "up"]
    if identity:
        command.append("--identity")
    report_path = infrastructure_report_path("up")
    run(command, environment={"INFRA_EVIDENCE_PATH": str(report_path)})
    run(compose_prefix() + ["up", "-d", "--wait", "--wait-timeout", "120", "nats"])
    ensure_databases()


def start_apps(fixture: bool, offline_images: bool, planner_mode: str | None) -> None:
	infra = read_env(ENV_PATH)
	if fixture and infra.get("APP_ENV", "development") == "production":
		raise RuntimeError("The deterministic model fixture is forbidden in APP_ENV=production")
	if fixture:
		# The fixture answers only its own two model identifiers, and the run
		# signature identity must agree across Execution, Worker, and Document.
		os.environ["LLM_MODEL"] = FIXTURE_CHAT_MODEL
		os.environ["OCR_MODEL"] = FIXTURE_OCR_MODEL
		# The fixture profile runs its own gateway service; without this the
		# default http://model-gateway:8084/v1 host does not exist and every
		# model call fails before reaching the fixture.
		os.environ["MODEL_GATEWAY_URL"] = FIXTURE_MODEL_GATEWAY_URL
		# The worker still takes the advisory/gateway path and records the
		# frozen suggestion trace; only the gateway answers deterministically.
		# Keep the worker and gateway model identities identical.
		os.environ["TYPESAFE_JEV_MODE"] = "advisory"
		os.environ["TYPESAFE_JEV_MODEL"] = "jev-fixture"
		os.environ["TYPESAFE_JEV_PROVIDER"] = "model-gateway"
		os.environ["TYPESAFE_JEV_EGRESS"] = "minimized"
		os.environ["TYPESAFE_JEV_FEATURES"] = "knowledge_rerank"
	if planner_mode:
		os.environ["AGENT_PLANNER_MODE"] = planner_mode
	if offline_images:
		verification_report = f"artifacts/c1c/ev-02/image-verification-{time.time_ns()}.json"
		run([sys.executable, str(ROOT / "deployments/scripts/package-app-images.py"),
			 "--verify", "--report", verification_report], quiet=True)
	up_options = ["--no-build", "--pull", "never"] if offline_images else ["--build"]
	migration_options = ["--pull", "never"] if offline_images else ["--build"]
	# Create/validate a dedicated non-super migration login, then provision
	# maintenance owners before ordinary migrations. Admin DSN is one-shot only.
	for service in ("migrate-offline-migrator", "migrate-clm-owner", "migrate-auth-owner"):
		run(compose_prefix() + ["--profile", "migration", "run", "--rm", *migration_options, service], timeout=900)
	run(compose_prefix() + ["--profile", "migration", "run", "--rm", *migration_options, "migrate"], timeout=900)
	profile = "model-fixture" if fixture else "real-model"
	model_service = "model-gateway-fixture" if fixture else "model-gateway"
	values = {**infra, **read_env(DISTRIBUTED_ENV_PATH), **os.environ}
	run(compose_prefix() + [
		"--profile", profile, "up", "-d", *up_options,
		"--wait", "--wait-timeout", "180",
		"gateway", "core-api", "document-service", "audit-execution", "audit-worker",
		model_service, "tool-gateway", "client", "admin", "portal",
		"health-monitor",
		*observation_services(values),
	], timeout=300)


def run_journey(confirmation: str, smoke: bool) -> None:
	infra = read_env(ENV_PATH)
	target_key = database_target_key()
	database = database_name(infra, target_key)
	target = "test" if target_key == "TEST_DATABASE_URL" else "development"
	if confirmation != database:
		raise ValueError(f"journey confirmation must name this isolated {target} database")
	command = [sys.executable, str(ROOT / "deployments/scripts/journey-local.py"),
			   "--env", str(DISTRIBUTED_ENV_PATH), "--database-target", target, "--confirm", confirmation]
	if smoke:
		command.append("--smoke")
	run(command)

def stop_apps() -> None:
    values = {**read_env(ENV_PATH), **read_env(DISTRIBUTED_ENV_PATH), **os.environ}
    run(compose_prefix() + ["stop", *APP_SERVICES, *observation_services(values)])


def stop_infrastructure(identity: bool) -> None:
    run(compose_prefix() + ["stop", "nats"])
    command = [sys.executable, str(ROOT / "deploy/scripts/infra.py"), "stop"]
    if identity:
        command.append("--identity")
    report_path = infrastructure_report_path("stop")
    run(command, environment={"INFRA_EVIDENCE_PATH": str(report_path)})


def deployment_profile_summary(action: str) -> dict[str, object]:
	infra = read_env(ENV_PATH)
	app = read_env(DISTRIBUTED_ENV_PATH) if DISTRIBUTED_ENV_PATH.is_file() else {}
	values = {**infra, **app, **os.environ}
	ports = {key: values[key] for key in ("POSTGRES_PORT", "RUSTFS_PORT", "RUSTFS_CONSOLE_PORT",
												"KEYCLOAK_PORT", "EDGE_PORT", "CLIENT_PORT", "ADMIN_PORT", "PORTAL_PORT") if key in values}
	databases = {}
	for key, name in (("DEV_DATABASE_URL", "development"), ("TEST_DATABASE_URL", "test")):
		if key in infra:
			databases[name] = database_name(infra, key)
	database_key = database_target_key()
	database_target = "test" if database_key == "TEST_DATABASE_URL" else "development"
	active_database = database_name(infra, database_key)
	fixture = action == "apps-up-fixture"
	issuer = values.get("OIDC_ISSUER", "").strip()
	return {
		"private_profile_paths": {"infrastructure_env": str(ENV_PATH), "distributed_env": str(DISTRIBUTED_ENV_PATH)},
		"compose_project": compose_project_name(),
		"app_env": values.get("APP_ENV", "development"),
		"ports": ports,
		"database_target": database_target,
		"active_database": active_database,
		"databases": databases,
		"object_bucket": values.get("S3_BUCKET", ""),
		"oidc": {"status": "CONFIGURED" if issuer else "NOT_CONFIGURED", "issuer": issuer or None},
		"model": {"configured_mode": "fixture" if fixture else "openai-compatible" if action == "apps-up" else "not_started",
				  "planner_mode": values.get("AGENT_PLANNER_MODE", "deterministic"),
				  "llm_model": values.get("LLM_MODEL", ""), "ocr_model": values.get("OCR_MODEL", ""),
				  "connectivity": "NOT_PROVED_BY_LAUNCHER"},
		"topology": {"infrastructure": "Docker Compose containers", "nats": "Docker Compose container",
					 "applications": "Docker Compose containers", "frontends": "Client/Admin/Portal Compose containers",
					 "host_published_ports": "loopback", "model_connection": "fixture container" if fixture else "external configured endpoint",
					 "oidc_connection": "configured issuer" if issuer else "no OIDC issuer; local development account mode"},
	}


def write_deployment_evidence(action: str, exit_code: int, error: BaseException | None) -> None:
	path_value = os.environ.get("EV02_DEPLOYMENT_REPORT", "").strip()
	if not path_value:
		return
	path = Path(path_value).expanduser()
	if not path.is_absolute():
		path = ROOT / path
	try:
		sys.path.insert(0, str(ROOT / "scripts"))
		from source_identity import source_identity
		identity = source_identity(ROOT)
	except (OSError, ValueError) as identity_error:
		identity = {"error": str(identity_error)}
	report: dict[str, object] = {
		"schema_version": "ev02-launcher-invocation/v1",
		"task_id": "EV-02",
		"action": action,
		"status": "PASS" if exit_code == 0 else "FAIL",
		"parent_argv": [sys.executable, *sys.argv],
		"parent_exit_code": exit_code,
		"recorded_at": dt.datetime.now(dt.timezone.utc).isoformat(),
		"source_identity": identity,
		"profile": deployment_profile_summary(action),
		"subprocesses": SUBPROCESS_RECORDS,
		"business_journeys": BUSINESS_JOURNEYS,
	}
	if PREFLIGHT_RESULTS:
		report["preflight"] = PREFLIGHT_RESULTS
	if error is not None:
		report["error"] = _redact(str(error))
	path.parent.mkdir(parents=True, exist_ok=True)
	descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
	with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
		json.dump(report, stream, ensure_ascii=False, indent=2)
		stream.write("\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("bootstrap", "config", "preflight", "infra-up", "infra-down",
                                            "apps-up", "apps-up-fixture", "apps-down", "journey"))
    parser.add_argument("--identity", action="store_true", help="Also start or stop the optional Keycloak identity sandbox")
    parser.add_argument("--offline-images", action="store_true", help="Use only verified current-source local app images")
    parser.add_argument("--planner-mode", choices=("fixed_chain", "deterministic", "fixture", "live"),
                        help="Override the frozen review planner mode for this app startup")
    parser.add_argument("--database-target", choices=("development", "test"),
                        default=os.environ.get("EV02_DATABASE_TARGET", "development"),
                        help="Select the isolated database for generated app credentials and journey writes")
    parser.add_argument("--confirm", default="", help="Isolated database name required for journey")
    parser.add_argument("--smoke", action="store_true", help="Use the bounded journey path without retry steps")
    args = parser.parse_args(argv)
    os.environ["EV02_DATABASE_TARGET"] = args.database_target
    exit_code = 0
    error: BaseException | None = None
    try:
        ensure_private_environment()
        if args.action == "bootstrap":
            print("private local environment is ready; existing .env files were preserved")
        elif args.action == "config":
            run(compose_prefix() + ["config", "--quiet"])
        elif args.action == "preflight":
            preflight()
        elif args.action == "infra-up":
            start_infrastructure(args.identity)
        elif args.action == "infra-down":
            stop_infrastructure(args.identity)
        elif args.action == "apps-up":
            start_apps(False, args.offline_images, args.planner_mode)
        elif args.action == "apps-up-fixture":
            start_apps(True, args.offline_images, args.planner_mode)
        elif args.action == "apps-down":
            stop_apps()
        elif args.action == "journey":
            run_journey(args.confirm, args.smoke)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as caught:
        error = caught
        exit_code = 1
        print(f"FAIL: {_redact(str(caught))}", file=sys.stderr)
    write_deployment_evidence(args.action, exit_code, error)
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())

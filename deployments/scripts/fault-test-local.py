#!/usr/bin/env python3
"""D2-8 dual-replica fault/recovery harness for the isolated local stack.

This harness never decides success by itself and never fabricates coverage: it
observes real containers through real HTTP and real PostgreSQL, and every
scenario status is derived from measured assertions on the resources it names.

Isolation: the private distributed profile and the named test database must
match before anything runs (reusing the same guard as journey-local.py), every
fault target belongs to the confirmed stack, and each target is checked to
actually point at the named test database. Only clearly named test resources are
killed or stopped.

Prerequisite gate: the six frozen prerequisites of the approved plan (section
eleven) must be recorded with evidence against the current source identity, and
the live facts that can be machine-checked (service roles, FORCE RLS, shared
sessions, job claim columns and unique constraints, hot-entity tables) must
pass. Anything missing reports NOT_RUN and exits 3; nothing is hardcoded PASS.

Environment control: --control compose (default) drives the confirmed compose
project with `compose up -d --no-deps --scale`. --control host drives the
runtime owner's host processes through --control-file, a JSON document:

  {"services": {
     "core-api":    {"instances": [{"pid": 1, "start": "...", "log": "...",
                                    "health": "http://127.0.0.1:18081/ready"}],
                     "additional": [{"start": "...", "log": "...", "health": "..."}]},
     "audit-worker": {...}, "tool-gateway": {...}}}

Every instance carries its exact start command and log file. Before a fault, the harness verifies that the live instance targets the confirmed test database. It reads `/proc/<pid>/environ`; if the platform denies that read, it may use only the private declared command after it verifies the same PID start time, owner UID, argv and executable digest. It starts or restarts replicas only by running the declared start command: no fabricated processes, no process it did not name.

Scenarios (each with measured results, timing, exit codes and raw logs):
  1. baseline and two Core/Worker/Tool replicas, including shared-session proof;
  2. trusted owner/org checks, cross-tenant denial and direct FORCE RLS reads;
  3. weighted two-tenant queue fairness, durable wait and retry/backoff observations;
  4. Core replica loss and Worker lease takeover, stale-generation rejection,
     one terminal attempt, contiguous official events and recovery;
  5. Tool and Model Gateway replica loss, plus Worker OTLP 503 isolation;
  6. SSE resume/revocation and NATS disconnect with PostgreSQL backlog evidence;
  7. simultaneous two-tenant quota contention, reservation settlement and null usage.

All model activity uses the explicitly configured local fixture. No real-model,
production, multi-node HA, disaster recovery, or SLO claim is made.

Capability labels are explicit: PROCESS_RECOVERY and MULTI_REPLICA are claimed
only from measurements on this single host. MULTI_NODE_HA, DISASTER_RECOVERY
and RPO/RTO stay NOT_RUN; HPA stays SPEC_ONLY.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import signal
import hashlib
import http.client
import http.server
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import select
import secrets
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
import urllib.request
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from deployments.local import DISTRIBUTED_ENV_PATH, ENV_PATH, compose_prefix as local_compose_prefix  # noqa: E402

# The six frozen prerequisites, exactly as approved (plan section eleven).
ITEM_DEFINITIONS = {
    "cross_owner_write_guards": "P0-04 six cross-owner writes removed and the guard bypass closed",
    "role_rls_migration": "role migration with RLS complete; dedicated service roles provisioned",
    "four_hot_entities": "four hot entities (sessions, review_attempts and their events, worker_jobs, cases) "
                         "entity transactions, versions, idempotency keys, unique constraints and narrow writes",
    "shared_authoritative_reads": "request path does not read a process-local old snapshot; authoritative shared reads",
    "h1_h2_h3_collaborative_cancel": "H1 late model event, H2 cross-process collaborative cancel and H3 generation "
                                     "CAS rows-affected checks are fixed",
    "shared_session_revocation": "session and revocation state shared across replicas",
}
HOT_TABLES = ("sessions", "review_attempts", "worker_jobs", "cases")
RLS_TABLES = (
    "sessions", "cases", "documents", "parsed_documents", "review_runs", "review_attempts",
    "review_attempt_parse_selections", "review_attempt_inputs", "execution_events", "findings",
    "constraint_evaluations", "decisions", "run_events", "activity", "idempotency",
    "review_run_snapshots", "audit_events", "worker_jobs", "outbox_events", "tool_call_ledger",
    "business_generations", "business_remediations", "business_remediation_revisions",
    "business_reports", "business_archives", "business_run_events", "business_audit_events",
    "document_activity", "execution_activity", "model_call_leases", "model_usage",
)
SERVICE_DEFAULTS = (("core-api", 2), ("audit-worker", 2), ("tool-gateway", 2))
BASELINE_DEFAULTS = (("core-api", 1), ("audit-worker", 1), ("tool-gateway", 1))
DEMO_CREDENTIALS = {"username": "demo-admin", "password": "admin-demo", "audience": "client"}
# A confirmed this finite, test-only store test as the stale-generation hook:
# it claims with a 1ns lease, takes the job over with a second worker
# (generation+1), and exits non-zero unless the superseded heartbeat and
# completion are rejected. It creates and drops only its own test schema.
DEFAULT_STALE_INJECTION_COMMAND = (
    "go test -json -count=1 -timeout=120s ./internal/store -run '^TestStaleGenerationCompletionIsRejected$'"
)


class Failure(RuntimeError):
    """A guard, command or measured assertion did not hold."""


def load_journey():
    """Reuse the frozen journey client instead of writing a second one."""
    spec = importlib.util.spec_from_file_location(
        "journey_local", ROOT / "deployments" / "scripts" / "journey-local.py")
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module

def load_oidc_module():
    spec = importlib.util.spec_from_file_location(
        "oidc_local", ROOT / "deployments" / "scripts" / "oidc-local.py")
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def load_oidc_setup(path: Path, recorder) -> dict[str, object]:
    if not path.is_file() or stat.S_IMODE(path.stat().st_mode) & 0o077:
        raise Failure("the OIDC host environment must be an existing private 0600 file")
    values: dict[str, str] = {}
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key) or key in values:
            raise Failure(f"invalid or duplicate OIDC host environment key at {path}:{number}")
        values[key] = value
    required = ("OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_ADMIN_CLIENT_ID",
                "OIDC_ADMIN_CLIENT_SECRET", "OIDC_REDIRECT_URL", "OIDC_STATE_KEY", "IDENTITY_DIRECTORY_FILE")
    if any(not values.get(key) for key in required):
        raise Failure("the OIDC host environment is missing required identity configuration")
    directory_path = Path(values["IDENTITY_DIRECTORY_FILE"])
    credentials_path = path.parent / "identity-credentials.json"
    for private_file in (directory_path, credentials_path):
        if not private_file.is_file() or stat.S_IMODE(private_file.stat().st_mode) & 0o077:
            raise Failure(f"required OIDC identity file is missing or not private: {private_file.name}")
    try:
        directory = json.loads(directory_path.read_text(encoding="utf-8"))
        credentials = json.loads(credentials_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise Failure("the private OIDC directory or credentials file is invalid JSON") from error
    if not isinstance(directory, dict) or not isinstance(credentials, dict):
        raise Failure("the private OIDC directory and credentials must be JSON objects")
    oidc = load_oidc_module()
    try:
        oidc.validate_directory(directory)
    except oidc.Failure as error:
        raise Failure(f"the trusted OIDC directory is invalid: {error}") from error
    issuer = values["OIDC_ISSUER"]
    if credentials.get("issuer") != issuer or any(user.get("issuer") != issuer for user in directory["users"]):
        raise Failure("the OIDC issuer differs between host environment, directory and credentials")
    credential_users = credentials.get("users")
    if not isinstance(credential_users, list):
        raise Failure("the private OIDC credentials file has no users list")
    users = {str(user.get("id")): user for user in credential_users if isinstance(user, dict)}
    directory_users = {str(user["id"]): user for user in directory["users"]}
    if not {"demo-admin", "demo-reviewer", "demo-clerk", "demo-viewer", "d2-tenant-b-clerk"}.issubset(users):
        raise Failure("the private OIDC credentials do not contain both synthetic tenant identities")
    for user_id, credential in users.items():
        trusted = directory_users.get(user_id)
        if (trusted is None or not credential.get("username") or not credential.get("password")
                or credential.get("tenant_id") != trusted["tenant_id"]
                or credential.get("role") != trusted["role"]):
            raise Failure(f"the private OIDC credentials do not match trusted user {user_id}")
        recorder.secrets.add(str(credential["password"]))
    recorder.secrets.update(str(values[key]) for key in required if "SECRET" in key or "KEY" in key)
    callback = urlsplit(values["OIDC_REDIRECT_URL"])
    origin = f"{callback.scheme}://{callback.netloc}"
    if callback.path != "/api/auth/oidc/callback" or callback.scheme != "http":
        raise Failure("the OIDC callback must be an HTTP auth-code callback URL")
    try:
        oidc.provider_wait_ready(issuer)
    except oidc.Failure as error:
        raise Failure(f"the configured local issuer is not ready: {error}") from error
    return {"env": values, "directory": directory, "users": users, "issuer": issuer,
            "origin": origin, "credentials_path": str(credentials_path),
            "directory_path": str(directory_path), "oidc_module": oidc}


def login_with_oidc(client, setup: dict[str, object], user_id: str, recorder) -> dict[str, object]:
    users = setup["users"]
    user = users.get(user_id)
    if not isinstance(user, dict):
        raise Failure(f"no private synthetic OIDC credentials exist for {user_id}")
    oidc = setup["oidc_module"]
    try:
        step, opener = oidc.oidc_login_flow(str(setup["origin"]), str(setup["issuer"]), "client",
                                            str(user["username"]), str(user["password"]), recorder)
    except oidc.Failure as error:
        raise Failure(f"OIDC login failed for trusted user {user_id}: {error}") from error
    actor = step.get("actor", {})
    if (not isinstance(actor, dict) or step.get("session_status") != 200
            or step.get("session_audience") != "client" or actor.get("id") != user_id
            or actor.get("tenant_id") != user.get("tenant_id") or actor.get("role") != user.get("role")):
        raise Failure(f"OIDC login did not establish the trusted client identity for {user_id}: {step!r}")
    client.base = str(setup["origin"])
    client.opener = opener
    return actor


def authenticate_client(client, context: dict, user_id: str = "demo-admin") -> dict[str, object]:
    setup = context.get("oidc")
    if setup is None:
        client.call("POST", "/api/session", DEMO_CREDENTIALS, expected={200})
        return {}
    return login_with_oidc(client, setup, user_id, context["recorder"])


def source_identity_module():
    spec = importlib.util.spec_from_file_location("source_identity", ROOT / "scripts" / "source_identity.py")
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class Recorder:
    """Runs subprocesses and appends redacted, timed command evidence."""

    def __init__(self, path: Path, secrets_to_redact: set[str]) -> None:
        self.path = path
        self.secrets = {value for value in secrets_to_redact if value and len(value) >= 8}
        self.records: list[dict[str, object]] = []

    def note(self, message: str) -> None:
        self.records.append({"argv": ["<internal>"], "exit": 0, "seconds": 0,
                             "stdout_tail": self.redact_text(message[-2000:]), "stderr_tail": ""})

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

    def run(self, argv: list[str], *, timeout: float = 600, capture: bool = True,
            env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
        started = time.monotonic()
        process = subprocess.run(argv, cwd=ROOT, env=env, timeout=timeout,
                                 stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
                                 stderr=subprocess.PIPE, stdin=subprocess.DEVNULL)
        self.records.append({
            "argv": self.redact(argv),
            "exit": process.returncode,
            "seconds": round(time.monotonic() - started, 3),
            "stdout_tail": self.redact_text(str((process.stdout or b"").decode("utf-8", "replace")[-2000:])),
            "stderr_tail": self.redact_text(str((process.stderr or b"").decode("utf-8", "replace")[-2000:])),
        })
        return process

    def flush(self) -> None:
        with self.path.open("w", encoding="utf-8") as stream:
            for record in self.records:
                stream.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")


def compose_prefix_for(env_path: Path, database_target: str = "development") -> list[str]:
    if env_path.expanduser().resolve() != DISTRIBUTED_ENV_PATH.expanduser().resolve():
        raise Failure("--env must match the distributed profile bound to the active infrastructure/project profile")
    if database_target not in ("development", "test"):
        raise Failure("database target must be development or test")
    return local_compose_prefix(env_path)

def database_identity(raw: str) -> tuple[str, int, str] | None:
    try:
        parsed = urlsplit(raw.strip())
        query = urllib.parse.parse_qs(parsed.query, strict_parsing=False)
        database = urllib.parse.unquote(parsed.path.lstrip("/"))
        port = parsed.port or 5432
    except (ValueError, UnicodeError):
        return None
    identity_overrides = {"dbname", "database", "host", "hostaddr", "port", "service", "servicefile"}
    if (parsed.scheme not in ("postgres", "postgresql") or parsed.fragment or not parsed.hostname
            or not database or "/" in database or "," in parsed.hostname
            or any(name.lower() in identity_overrides for name in query)):
        return None
    return parsed.hostname.lower(), port, database


class Stack:
    """The explicit named test resources this harness may scale and fault.

    compose mode controls containers of the confirmed compose project. host mode
    controls explicitly declared host processes (the runtime owner's stack):
    every slot is named by an exact start command, a log file and an optional
    health URL, with the currently running pid. Nothing is invented or faked:
    each instance must prove it targets the confirmed test database before the
    harness kills or stops it.
    """

    def __init__(self, recorder: Recorder, env_path: Path, confirm: str, app_env: dict[str, str],
                 control: str = "compose", control_file: Path | None = None,
                 database_target: str = "development") -> None:
        self.recorder = recorder
        self.control_file: Path | None = None
        self.control_document: dict | None = None
        self.control_services: dict | None = None
        self.env_path = env_path
        self.database_target = database_target
        self.confirm = confirm
        self.app_env = app_env
        self.docker = os.environ.get("DOCKER_BIN", "docker")
        self.expected_database_identity = database_identity(app_env.get("MIGRATOR_DATABASE_URL", ""))
        if not self.expected_database_identity or self.expected_database_identity[2] != confirm:
            raise Failure("the confirmed database does not match the isolated migrator connection identity")
        self.migrator_user = urllib.parse.unquote(
            urlsplit(app_env.get("MIGRATOR_DATABASE_URL", "")).username or "contractaudit")
        self.control = control
        self.services = ("core-api", "audit-worker", "tool-gateway")
        self.slots: dict[str, list[dict]] = {service: [] for service in self.services}
        self.last_edge_upstreams: dict[str, str] = {}
        if control == "host":
            if control_file is None or not control_file.is_file():
                raise Failure("host control needs --control-file naming the running instances")
            try:
                document = json.loads(control_file.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError) as error:
                raise Failure(f"cannot read host control file {control_file}: {error}") from error
            if not isinstance(document, dict):
                raise Failure("host control file must be a JSON object")
            services = document.get("services", document)
            if not isinstance(services, dict):
                raise Failure("host control services must be a JSON object")
            self.control_file = control_file
            self.control_document = document
            self.control_services = services
            self.slots = {}
            for service, spec in services.items():
                if service == "broker":
                    continue
                if not isinstance(service, str) or not isinstance(spec, dict):
                    raise Failure("each declared host service must have an object specification")
                instances = spec.get("instances", [])
                additional = spec.get("additional", [])
                if not isinstance(instances, list) or not isinstance(additional, list):
                    raise Failure(f"host control instances/additional for {service} must be arrays")
                if service in self.services and not instances and not additional:
                    raise Failure(f"host control file must declare slots for required service {service}")
                slots = [self._host_slot(service, entry, running=True, group="instances") for entry in instances]
                slots.extend(self._host_slot(service, entry, running=False, group="additional") for entry in additional)
                self.slots[service] = slots
            for service in self.services:
                if service not in self.slots:
                    raise Failure(f"host control file is missing the required service {service}")
            listeners: dict[tuple[str, int], str] = {}
            for service, slots in self.slots.items():
                for slot in slots:
                    parsed = urlsplit(slot["spec"]["health"])
                    listener = (parsed.hostname or "", parsed.port or 0)
                    if listener in listeners:
                        raise Failure(f"host control assigns the same listener to {listeners[listener]} and {service}")
                    listeners[listener] = service

    def _host_slot(self, service: str, entry: object, *, running: bool, group: str) -> dict:
        if not isinstance(entry, dict):
            raise Failure(f"host control slot for {service} must be an object")
        raw_pid = entry.get("pid")
        if running and (isinstance(raw_pid, bool) or not isinstance(raw_pid, int) or raw_pid <= 0):
            raise Failure(f"host control instance for {service} has no positive pid")
        command = entry.get("start", entry.get("command"))
        if not isinstance(command, str) or not command.strip():
            raise Failure(f"host control slot for {service} has no start command")
        Stack._parse_host_start(command)
        log = entry.get("log")
        if not isinstance(log, str) or not Path(log).is_absolute():
            raise Failure(f"host control slot for {service} needs an absolute log path")
        health = entry.get("health")
        try:
            parsed = urlsplit(str(health or ""))
            port = parsed.port
        except ValueError as error:
            raise Failure(f"host control slot for {service} has an invalid health URL") from error
        if (parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost", "::1")
                or port is None):
            raise Failure(f"host control slot for {service} needs a loopback HTTP health URL with an explicit port")
        pid = raw_pid if running else None
        start_ticks = self._host_process_start_ticks(pid) if pid is not None else None
        recorded_ticks = entry.get("pid_start_ticks")
        if recorded_ticks is not None and str(recorded_ticks) != str(start_ticks):
            raise Failure(f"host control PID identity changed for {service} process {pid}")
        return {"spec": entry, "pid": pid, "group": group, "start_ticks": start_ticks}

    def _persist_host_control(self) -> None:
        if self.control != "host" or self.control_file is None or self.control_services is None:
            raise Failure("host control persistence requires host mode")
        try:
            mode = stat.S_IMODE(self.control_file.stat().st_mode)
            if mode & 0o077:
                raise Failure("host control file is readable by group/other; refusing to rewrite it")
            for service, slots in self.slots.items():
                service_spec = self.control_services.get(service)
                if not isinstance(service_spec, dict):
                    raise Failure(f"host control lost the {service} service record")
                instances, additional = [], []
                for slot in slots:
                    spec = slot["spec"]
                    if slot["group"] == "instances":
                        if slot["pid"] is not None:
                            spec["pid"] = int(slot["pid"])
                            spec["pid_start_ticks"] = int(slot["start_ticks"])
                        else:
                            spec.pop("pid", None)
                            spec.pop("pid_start_ticks", None)
                        instances.append(spec)
                    elif slot["group"] == "additional":
                        spec.pop("pid", None)
                        spec.pop("pid_start_ticks", None)
                        additional.append(spec)
                    else:
                        raise Failure(f"host control has an invalid slot group for {service}")
                service_spec["instances"] = instances
                service_spec["additional"] = additional
            descriptor, temporary = tempfile.mkstemp(prefix=f".{self.control_file.name}.",
                                                     dir=self.control_file.parent)
            try:
                with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
                    json.dump(self.control_document, stream, ensure_ascii=False, indent=2, sort_keys=True)
                    stream.write("\n")
                    stream.flush()
                    os.fsync(stream.fileno())
                os.chmod(temporary, mode)
                os.replace(temporary, self.control_file)
            except Exception:
                try:
                    os.unlink(temporary)
                except OSError:
                    pass
                raise
        except OSError as error:
            raise Failure(f"could not persist host control file: {error}") from error

    @staticmethod
    def _start_command_with_environment(command: str, additions: dict[str, str]) -> str:
        environment, executable = Stack._parse_host_start(command)
        for key, value in additions.items():
            if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
                raise Failure(f"invalid host environment key {key!r}")
            environment[key] = value
        arguments = ["env", "-i", *(f"{key}={value}" for key, value in sorted(environment.items())), *executable]
        return shlex.join(arguments)

    def _wait_host_slot(self, slot: dict, timeout: float = 180) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self._host_health(slot):
                return
            time.sleep(0.5)
        raise Failure(f"host service did not become healthy at {slot['spec']['health']} within {timeout}s")

    def configure_host_environment(self, service_environment: dict[str, dict[str, str]]) -> dict[str, object]:
        """Apply a private identity configuration and restart declared host processes."""
        if self.control != "host":
            raise Failure("trusted-directory rollout is supported only for explicitly controlled host processes")
        if not service_environment:
            raise Failure("trusted-directory rollout has no service environment")
        original_commands: dict[int, str] = {}
        modified_slots: list[tuple[str, dict]] = []
        restart_slots: set[int] = set()
        restarted: list[tuple[str, dict]] = []
        current: tuple[str, dict] | None = None
        try:
            for service, additions in service_environment.items():
                slots = self.slots.get(service)
                if not slots:
                    raise Failure(f"host control has no slots for identity-dependent service {service}")
                for slot in slots:
                    original_commands[id(slot)] = str(slot["spec"].get("start", slot["spec"].get("command", "")))
                    modified_slots.append((service, slot))
                    slot["spec"]["start"] = self._start_command_with_environment(
                        original_commands[id(slot)], additions)
                    if slot["group"] != "instances":
                        continue
                    pid = slot["pid"]
                    if pid is None or not self.alive(int(pid)):
                        raise Failure(f"cannot roll identity configuration through stopped {service} instance")
                    if any(self.env_value(str(pid), key) != value for key, value in additions.items()):
                        restart_slots.add(id(slot))
            self._persist_host_control()

            for service in service_environment:
                for slot in self.slots[service]:
                    if id(slot) not in restart_slots:
                        continue
                    current = (service, slot)
                    self._stop_pid(int(slot["pid"]))
                    slot["pid"] = None
                    self._start_slot(service, slot)
                    self._wait_host_slot(slot)
                    restarted.append(current)
                    current = None
                    self._persist_host_control()
                    if service in self.services:
                        self.assert_targets_confirmed_database(service)
            return {"updated_start_commands": sorted(service_environment),
                    "restarted": [{"service": service, "pid": slot["pid"],
                                   "health": slot["spec"]["health"]} for service, slot in restarted],
                    "already_configured": len(restart_slots) == 0}
        except Exception as error:
            rollback = ([current] if current else []) + list(reversed(restarted))
            rollback_failures: list[str] = []
            active_rollback_ids = {id(slot) for _, slot in rollback}
            for service, slot in modified_slots:
                if id(slot) not in active_rollback_ids:
                    slot["spec"]["start"] = original_commands[id(slot)]
            for service, slot in rollback:
                try:
                    if slot["pid"] is not None and self.alive(int(slot["pid"])):
                        self._stop_pid(int(slot["pid"]))
                    slot["pid"] = None
                    slot["spec"]["start"] = original_commands[id(slot)]
                    self._start_slot(service, slot)
                    self._wait_host_slot(slot)
                except Exception as rollback_error:
                    rollback_failures.append(f"{service}: {rollback_error}")
            try:
                self._persist_host_control()
            except Failure as persist_error:
                rollback_failures.append(str(persist_error))
            detail = f"host environment rollout failed: {error}"
            if rollback_failures:
                detail += "; rollback failures: " + "; ".join(rollback_failures)
            raise Failure(detail) from error

    @staticmethod
    def _parse_host_start(command: str) -> tuple[dict[str, str], list[str]]:
        try:
            arguments = shlex.split(command, posix=True)
        except ValueError as error:
            raise Failure("host control start command has invalid shell quoting") from error
        if arguments[:1] == ["exec"]:
            arguments = arguments[1:]
        if len(arguments) < 4 or arguments[:2] != ["env", "-i"]:
            raise Failure("host control start command must be a literal env -i command")
        environment: dict[str, str] = {}
        index = 2
        while index < len(arguments):
            key, separator, value = arguments[index].partition("=")
            if not separator:
                break
            if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key) or key in environment:
                raise Failure("host control start command has invalid or duplicate environment keys")
            environment[key] = value
            index += 1
        executable = arguments[index:]
        if not executable or not Path(executable[0]).is_absolute():
            raise Failure("host control start command must name an absolute executable")
        return environment, executable

    # --- shared helpers -------------------------------------------------

    def compose(self, *arguments: str, timeout: float = 600) -> subprocess.CompletedProcess:
        return self.recorder.run(compose_prefix_for(self.env_path, self.database_target) + list(arguments), timeout=timeout)

    @staticmethod
    def alive(pid: int) -> bool:
        """A host process counts as alive only when it is neither gone nor a zombie."""
        if os.name == "posix" and Path("/proc").is_dir():
            try:
                stat = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
            except OSError:
                return False
            close = stat.rfind(")")
            if close < 0 or len(stat) < close + 3:
                return False
            return stat[close + 2] not in ("Z", "X")
        try:
            os.kill(pid, 0)
            return True
        except (ProcessLookupError, PermissionError):
            return False

    @staticmethod
    def _host_process_start_ticks(pid: int) -> int:
        try:
            raw = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
        except OSError as error:
            raise Failure(f"cannot read process identity for controlled pid {pid}: {error}") from error
        close = raw.rfind(")")
        fields = raw[close + 2:].split() if close >= 0 else []
        if len(fields) <= 19 or not fields[19].isdigit():
            raise Failure(f"cannot parse process start time for controlled pid {pid}")
        return int(fields[19])

    def _is_host(self, instance: str) -> bool:
        """True only for a pid this harness tracks as a host slot."""
        if self.control != "host" or not instance.isdigit():
            return False
        for slots in self.slots.values():
            for slot in slots:
                if slot["pid"] is not None and str(slot["pid"]) == instance:
                    return True
        return False

    def _slot_for(self, instance: str) -> dict:
        for slots in self.slots.values():
            for slot in slots:
                if slot["pid"] is not None and str(slot["pid"]) == instance:
                    return slot
        raise Failure(f"{instance} is not a tracked host instance")

    def _host_service_of(self, instance: str) -> str:
        for service, slots in self.slots.items():
            for slot in slots:
                if slot["pid"] is not None and str(slot["pid"]) == instance:
                    return service
        raise Failure(f"{instance} is not a tracked host instance")

    def containers(self, service: str) -> list[str]:
        if self.control == "host":
            return [str(slot["pid"]) for slot in self.slots.get(service, [])
                    if slot["pid"] is not None and self.alive(slot["pid"])]
        process = self.compose("ps", "-q", service, timeout=120)
        if process.returncode != 0:
            raise Failure(f"compose ps failed for {service}: {process.stderr.decode('utf-8', 'replace')[-200:]}")
        output = process.stdout.decode() if isinstance(process.stdout, bytes) else process.stdout
        return [line for line in str(output).split() if line]

    def _host_health(self, slot: dict) -> bool:
        if slot["pid"] is None or not self.alive(slot["pid"]):
            return False
        address = str(slot["spec"].get("health", ""))
        if not address:
            return True
        parsed = urlsplit(address)
        try:
            connection = http.client.HTTPConnection(parsed.hostname, parsed.port or 80, timeout=5)
            connection.request("GET", parsed.path or "/ready")
            response = connection.getresponse()
            response.read()
            return response.status == 200
        except (OSError, http.client.HTTPException):
            return False

    def healthy(self, instances: list[str]) -> bool:
        for instance in instances:
            if self._is_host(instance):
                if not self._host_health(self._slot_for(instance)):
                    return False
                continue
            process = self.recorder.run(
                [self.docker, "inspect", "--format",
                 "{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}", instance],
                timeout=60)
            state = str(process.stdout.decode() if isinstance(process.stdout, bytes) else process.stdout).strip()
            if state != "running|healthy":
                return False
        return True

    def wait_healthy(self, service: str, count: int, timeout: float = 180) -> list[str]:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            containers = self.containers(service)
            if len(containers) == count and self.healthy(containers):
                return containers
            time.sleep(2)
        raise Failure(f"{service} did not reach {count} healthy replicas within {timeout}s")

    def scale(self, counts: dict[str, int], timeout: float = 900) -> None:
        if self.control == "host":
            self._scale_host(counts, timeout)
            if {"core-api", "tool-gateway"}.intersection(counts):
                self.configure_host_edge_upstreams()
            return
        arguments: list[str] = ["up", "-d", "--no-deps", "--wait", "--wait-timeout", "180"]
        services: list[str] = []
        for service, count in counts.items():
            arguments += ["--scale", f"{service}={count}"]
            services.append(service)
        process = self.compose(*arguments, *services, timeout=timeout)
        if process.returncode != 0:
            raise Failure(f"compose scale failed: {process.stderr.decode('utf-8', 'replace')[-300:]}")

    def configure_host_edge_upstreams(self) -> dict[str, str]:
        if self.control != "host":
            return {}
        if len(self.containers("edge")) != 1:
            raise Failure("host runtime must declare exactly one controlled Edge process")
        routes: dict[str, str] = {}
        for service, key in (("core-api", "CORE_URLS"), ("tool-gateway", "TOOL_URLS")):
            instances = self.containers(service)
            if not instances:
                raise Failure(f"host Edge cannot route {service}: no live replicas are declared")
            endpoints = []
            for instance in instances:
                address, port = self.instance_addr(instance)
                if not address or not port:
                    raise Failure(f"host Edge cannot route {service} instance {instance}: no live listener")
                endpoints.append(f"http://{address}:{port}")
            routes[key] = ",".join(endpoints)
        self.configure_host_environment({"edge": routes})
        edge = self.containers("edge")[0]
        observed = {key: self.env_value(edge, key) for key in routes}
        if observed != routes:
            raise Failure("the controlled Edge did not adopt the declared Core and Tool replica topology")
        self.last_edge_upstreams = routes
        self.recorder.note("Edge upstreams match every live Core and Tool replica")
        return routes

    def _start_slot(self, service: str, slot: dict) -> None:
        command = str(slot["spec"].get("start", slot["spec"].get("command", "")))
        if not command:
            raise Failure(f"the {service} instance has no recorded start command")
        environment, executable = self._parse_host_start(command)
        health = urlsplit(slot["spec"]["health"])
        host = health.hostname or "127.0.0.1"
        listen_address = f"[{host}]:{health.port}" if ":" in host else f"{host}:{health.port}"
        if service == "audit-worker":
            environment["HEALTH_ADDR"] = listen_address
        else:
            environment["ADDR"] = listen_address
        log_path = Path(slot["spec"]["log"])
        if not log_path.parent.is_dir():
            raise Failure(f"the {service} log directory does not exist")
        try:
            with log_path.open("a", encoding="utf-8") as log:
                process = subprocess.Popen(executable, cwd=ROOT, env=environment,
                                           stdout=log, stderr=subprocess.STDOUT,
                                           stdin=subprocess.DEVNULL, start_new_session=True)
        except OSError as error:
            raise Failure(f"could not start the declared {service} executable: {error}") from error
        self.recorder.records.append({
            "argv": ["host-start", service, executable[0]],
            "command_sha256": hashlib.sha256(command.encode("utf-8")).hexdigest(),
            "exit": None,
            "seconds": 0,
            "stdout_tail": f"started {service} pid {process.pid} at {listen_address}",
            "stderr_tail": "",
        })
        slot["pid"] = process.pid
        slot["start_ticks"] = self._host_process_start_ticks(process.pid)
        if slot["group"] == "instances":
            slot["spec"]["pid"] = process.pid
            slot["spec"]["pid_start_ticks"] = slot["start_ticks"]

    def _scale_host(self, counts: dict[str, int], timeout: float) -> None:
        """Host scaling is real process control through the declared start commands."""
        for service, desired in counts.items():
            slots = self.slots[service]
            if desired > len(slots):
                raise Failure(
                    f"host control declares {len(slots)} {service} slots but {desired} replicas were requested; "
                    "the runtime owner must hand over that topology")
            for index, slot in enumerate(slots):
                if index < desired:
                    slot["group"] = "instances"
                    if slot["pid"] is None or not self.alive(slot["pid"]):
                        self._start_slot(service, slot)
                else:
                    if slot["pid"] is not None and self.alive(slot["pid"]):
                        self._stop_pid(slot["pid"])
                    slot["pid"] = None
                    slot["start_ticks"] = None
                    slot["group"] = "additional"
            self._persist_host_control()
            deadline = time.monotonic() + min(timeout, 180)
            while time.monotonic() < deadline:
                running = self.containers(service)
                if len(running) == desired and self.healthy(running):
                    break
                time.sleep(2)
            else:
                raise Failure(f"{service} did not reach {desired} healthy host instances")

    def _stop_pid(self, pid: int) -> None:
        os.kill(pid, 15)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and self.alive(pid):
            time.sleep(0.5)
        stopped = not self.alive(pid)
        self.recorder.records.append({"argv": ["kill", "-15", str(pid)], "exit": 0 if stopped else 1,
                                      "seconds": 30 if not stopped else 0,
                                      "stdout_tail": f"SIGTERM {pid}" if stopped else f"still alive {pid}",
                                      "stderr_tail": ""})
        if not stopped:
            raise Failure(f"host process {pid} did not stop after SIGTERM")

    # --- per-instance facts --------------------------------------------

    def _host_environ(self, instance: str) -> dict[str, str]:
        try:
            raw = Path(f"/proc/{instance}/environ").read_bytes()
        except PermissionError:
            return self._host_environ_from_verified_start(instance)
        except OSError as error:
            raise Failure(f"cannot read the environment of controlled process {instance}: {error}") from error
        values: dict[str, str] = {}
        for entry in raw.split(b"\0"):
            key, separator, value = entry.decode("utf-8", "replace").partition("=")
            if separator and key:
                values[key] = value
        return values

    def _host_environ_from_verified_start(self, instance: str) -> dict[str, str]:
        pid = int(instance)
        slot = self._slot_for(instance)
        expected_ticks = slot.get("start_ticks")
        proc = Path(f"/proc/{pid}")
        try:
            if not self.alive(pid) or expected_ticks is None or self._host_process_start_ticks(pid) != expected_ticks:
                raise Failure(f"controlled process identity changed while reading pid {pid}")
            owner_uid = proc.stat().st_uid
            status = (proc / "status").read_text(encoding="utf-8")
            uid_match = re.search(r"^Uid:\s+(.*)$", status, re.MULTILINE)
            if owner_uid != os.getuid() or not uid_match or any(
                    int(value) != os.getuid() for value in uid_match.group(1).split()[:4]):
                raise Failure(f"controlled process {pid} is not owned by the current runtime user")
            environment, executable = self._parse_host_start(
                str(slot["spec"].get("start", slot["spec"].get("command", ""))))
            actual_argv = [value.decode("utf-8", "replace") for value in (proc / "cmdline").read_bytes().split(b"\0")
                           if value]
            if actual_argv != executable:
                raise Failure(f"controlled process argv no longer matches its declared start command for pid {pid}")
            declared_path = Path(executable[0]).resolve(strict=True)
            running_path = os.readlink(proc / "exe")
            if running_path != str(declared_path):
                raise Failure(f"controlled process executable no longer matches its declared start command for pid {pid}")
            running_hash = hashlib.sha256()
            with (proc / "exe").open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    running_hash.update(chunk)
            declared_hash = hashlib.sha256()
            with declared_path.open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    declared_hash.update(chunk)
            expected_hash = str(slot["spec"].get("binary_sha256", ""))
            if running_hash.digest() != declared_hash.digest() or (expected_hash and running_hash.hexdigest() != expected_hash):
                raise Failure(f"controlled process binary identity changed for pid {pid}")
        except (OSError, ValueError, IndexError) as error:
            raise Failure(f"cannot verify the declared environment for controlled pid {pid}") from error
        self.recorder.note(f"read environment for pid {pid} from its verified private start command after /proc denial")
        return environment

    def target_database(self, instance: str) -> tuple[str, int, str] | None:
        """Return one consistent connection identity without exposing credentials."""
        if self._is_host(instance):
            values = self._host_environ(instance)
        else:
            process = subprocess.run(
                [self.docker, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", instance],
                cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
            if process.returncode != 0:
                raise Failure(f"cannot inspect {instance}")
            values = {}
            for line in process.stdout.decode("utf-8", "replace").splitlines():
                key, separator, value = line.partition("=")
                if separator:
                    values[key] = value
        identities = [database_identity(value) for key, value in values.items()
                      if key.endswith("_DATABASE_URL") and value]
        if not identities or any(identity is None for identity in identities):
            return None
        first = identities[0]
        return first if all(identity == first for identity in identities) else None

    def assert_targets_confirmed_database(self, service: str) -> list[str]:
        instances = self.containers(service)
        if not instances:
            raise Failure(f"{service} has no running instance")
        for instance in instances:
            identity = self.target_database(instance)
            if identity != self.expected_database_identity:
                observed = (f"{identity[0]}:{identity[1]}/{identity[2]}" if identity else "missing/inconsistent DSNs")
                expected = self.expected_database_identity
                raise Failure(f"refusing to fault {instance}: it targets {observed}, not the confirmed "
                              f"{expected[0]}:{expected[1]}/{expected[2]}")
        return instances

    def service_role(self, instance: str) -> str:
        return self.env_value(instance, "SERVICE_ROLE")

    def env_value(self, instance: str, key: str) -> str:
        if self._is_host(instance):
            return self._host_environ(instance).get(key, "")
        process = subprocess.run(
            [self.docker, "inspect", "--format", f"{{{{range .Config.Env}}}}{{{{println .}}}}{{{{end}}}}", instance],
            cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        for line in process.stdout.decode("utf-8", "replace").splitlines():
            name, separator, value = line.partition("=")
            if separator and name == key:
                return value
        return ""

    def process_facts(self, service: str) -> list[dict[str, object]]:
        facts: list[dict[str, object]] = []
        for instance in self.containers(service):
            if self._is_host(instance):
                pid = int(instance)
                executable = Path(f"/proc/{pid}/exe")
                try:
                    executable_path = os.readlink(executable)
                    running_hasher = hashlib.sha256()
                    with executable.open("rb") as stream:
                        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                            running_hasher.update(chunk)
                    slot = self._slot_for(instance)
                    _, command = self._parse_host_start(str(slot["spec"].get("start", slot["spec"].get("command", ""))))
                    declared_executable = Path(command[0]).resolve(strict=True)
                    disk_hasher = hashlib.sha256()
                    with declared_executable.open("rb") as stream:
                        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                            disk_hasher.update(chunk)
                except (OSError, IndexError) as error:
                    raise Failure(f"cannot identify host executable for {service} pid {pid}: {error}") from error
                expected = slot["spec"].get("binary_sha256")
                actual = running_hasher.hexdigest()
                declared_hash = disk_hasher.hexdigest()
                facts.append({"service": service, "pid": pid, "executable": executable_path,
                              "declared_executable": str(declared_executable),
                              "binary_sha256": actual, "declared_binary_sha256": declared_hash,
                              "declared_hash_matches_running": actual == declared_hash,
                              "recorded_binary_sha256": expected,
                              "recorded_hash_matches": actual == expected if expected else None,
                              "service_role": self.env_value(instance, "SERVICE_ROLE"),
                              "health": slot["spec"]["health"]})
            else:
                image = self.recorder.run(
                    [self.docker, "inspect", "--format", "{{.Image}}", instance], timeout=60)
                if image.returncode != 0:
                    raise Failure(f"cannot identify container image for {service} {instance}")
                facts.append({"service": service, "container": instance,
                              "image_id": image.stdout.decode("utf-8", "replace").strip(),
                              "service_role": self.env_value(instance, "SERVICE_ROLE")})
        return facts

    def instance_hostname(self, instance: str) -> str:
        if self._is_host(instance):
            environment = self._host_environ(instance)
            return environment.get("WORKER_ID") or environment.get("HOSTNAME") or f"pid-{instance}"
        process = self.recorder.run([self.docker, "exec", instance, "hostname"], timeout=60)
        return str(process.stdout.decode() if isinstance(process.stdout, bytes) else process.stdout).strip()

    def instance_addr(self, instance: str) -> tuple[str, int]:
        """The host/port this instance actually listens on, from its own config."""
        address = self.env_value(instance, "ADDR")
        if not address:
            return "", 0
        host, _, port = address.rpartition(":")
        if not port.isdigit():
            return "", 0
        if self._is_host(instance):
            if host in ("0.0.0.0", "::", ""):
                host = "127.0.0.1"
            return host, int(port)
        process = self.recorder.run(
            [self.docker, "inspect", "--format",
             "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", instance], timeout=60)
        output = str(process.stdout.decode() if isinstance(process.stdout, bytes) else process.stdout)
        container_ip = output.split()[0] if output.split() else ""
        return container_ip, int(port)

    def instance_logs(self, instance: str, tail: int = 200) -> str:
        if self._is_host(instance):
            log = str(self._slot_for(instance)["spec"].get("log", ""))
            if not log or not Path(log).is_file():
                return ""
            lines = Path(log).read_text(encoding="utf-8", errors="replace").splitlines()
            return "\n".join(lines[-tail:])
        process = self.recorder.run([self.docker, "logs", "--tail", str(tail), instance], timeout=60)
        stdout = process.stdout.decode("utf-8", "replace") if isinstance(process.stdout, bytes) else str(process.stdout)
        stderr = process.stderr.decode("utf-8", "replace") if process.stderr else ""
        return stdout + stderr

    def instance_label(self, instance: str) -> str:
        """The bounded startup instance label B landed, for per-replica attribution."""
        for line in self.instance_logs(instance).splitlines():
            marker = line.find("instance ")
            if marker >= 0:
                return line[marker:].strip()[:160]
        return ""

    # --- faults ---------------------------------------------------------

    def kill(self, instance: str) -> None:
        if self._is_host(instance):
            pid = int(instance)
            os.kill(pid, 9)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline and self.alive(pid):
                time.sleep(0.1)
            self.recorder.records.append({"argv": ["kill", "-9", instance], "exit": 0, "seconds": 0,
                                          "stdout_tail": f"SIGKILL {instance}", "stderr_tail": ""})
            return
        self.recorder.run([self.docker, "kill", instance], timeout=120)

    def stop(self, instance: str) -> None:
        if self._is_host(instance):
            self._stop_pid(int(instance))
            return
        self.recorder.run([self.docker, "stop", "--time", "20", instance], timeout=180)

    def pause(self, instance: str) -> None:
        if self._is_host(instance):
            os.kill(int(instance), signal.SIGSTOP)
            action = ["kill", "-STOP", instance]
        else:
            process = self.recorder.run([self.docker, "pause", instance], timeout=120)
            if process.returncode != 0:
                raise Failure(f"could not pause named instance {instance}")
            action = [self.docker, "pause", instance]
        self.recorder.records.append({"argv": action, "exit": 0, "seconds": 0,
                                      "stdout_tail": f"SIGSTOP {instance}", "stderr_tail": ""})

    def resume(self, instance: str) -> None:
        if self._is_host(instance):
            os.kill(int(instance), signal.SIGCONT)
            action = ["kill", "-CONT", instance]
        else:
            process = self.recorder.run([self.docker, "unpause", instance], timeout=120)
            if process.returncode != 0:
                raise Failure(f"could not resume named instance {instance}")
            action = [self.docker, "unpause", instance]
        self.recorder.records.append({"argv": action, "exit": 0, "seconds": 0,
                                      "stdout_tail": f"SIGCONT {instance}", "stderr_tail": ""})

    def start(self, instance: str) -> None:
        if self._is_host(instance):
            service = self._host_service_of(instance)
            self._start_slot(service, self._slot_for(instance))
            return
        self.recorder.run([self.docker, "start", instance], timeout=180)

    # --- database read access (the database stays the compose db container) ---

    def psql_rows(self, query: str, timeout: float = 120) -> list[str]:
        process = self.compose(
            "exec", "-T", "db", "psql", "-U", self.migrator_user, "-d", self.confirm,
            "-t", "-A", "-F", "|", "-v", "ON_ERROR_STOP=1", "-c", query, timeout=timeout)
        if process.returncode != 0:
            raise Failure(f"psql failed: {process.stderr.decode('utf-8', 'replace').strip()[-300:]}")
        output = process.stdout.decode() if isinstance(process.stdout, bytes) else process.stdout
        return [line for line in str(output).splitlines() if line]

    def psql_scalar(self, query: str, timeout: float = 120) -> str:
        rows = self.psql_rows(query, timeout)
        if len(rows) != 1 or "|" in rows[0]:
            raise Failure(f"expected one scalar from {query!r}, got {rows!r}")
        return rows[0]

    def psql_service_rows(self, service: str, query: str, timeout: float = 120) -> list[str]:
        key = service.upper() + "_DATABASE_URL"
        dsn = self.app_env.get(key, "")
        parts = urlsplit(dsn)
        user = urllib.parse.unquote(parts.username or "")
        password = urllib.parse.unquote(parts.password or "")
        if (service not in {"core", "document", "execution", "model", "tool"}
                or parts.scheme not in {"postgres", "postgresql"}
                or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", user) or not password):
            raise Failure(f"{key} lacks a usable dedicated service login")
        self.recorder.secrets.add(password)
        process = self.compose(
            "exec", "-T", "-e", f"PGPASSWORD={password}", "db", "psql", "-h", "127.0.0.1",
            "-p", str(parts.port or 5432), "-U", user, "-d", self.confirm,
            "-q", "-t", "-A", "-F", "|", "-v", "ON_ERROR_STOP=1", "-c", query, timeout=timeout)
        if process.returncode != 0:
            raise Failure(f"dedicated {service} role query failed: "
                          f"{process.stderr.decode('utf-8', 'replace').strip()[-300:]}")
        output = process.stdout.decode() if isinstance(process.stdout, bytes) else process.stdout
        return [line for line in str(output).splitlines() if line]



def validate_current_regression(document: dict[str, object], current_identity: dict[str, object],
                                failures: list[str]) -> None:
    declared = document.get("current_regression")
    if not isinstance(declared, dict) or not isinstance(declared.get("report"), str):
        failures.append("current_regression.report must name the latest Store/HTTP regression report")
        return
    report_path = Path(str(declared["report"]))
    if report_path.is_absolute() or ".." in report_path.parts:
        failures.append("current_regression.report must be a workspace-relative path")
        return
    try:
        report = json.loads((ROOT / report_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        failures.append(f"cannot read current regression report {report_path}: {error}")
        return
    if not isinstance(report, dict):
        failures.append("current regression report must be a JSON object")
        return

    expected_command = ["go", "test", "-race", "-count=1", "-timeout=240s", "-json",
                        "./internal/store", "./internal/httpapi"]
    complete_go_command = ["go", "test", "-race", "-count=1", "-json", "-timeout=300s",
                           "-covermode=atomic", "-coverpkg=./internal/...",
                           "-coverprofile=artifacts/go-coverage.out", "./..."]
    if report.get("command") not in (expected_command, complete_go_command):
        failures.append("current regression report must record the full Store/HTTP or complete Go race command")
    roots = ["internal/store", "internal/httpapi"]
    if report.get("source_roots") != roots:
        failures.append("current regression report must cover exactly the Store and HTTP source roots")


    selected = report.get("selected_source_sha256")
    current_files = current_identity.get("files")
    if not isinstance(selected, dict) or not isinstance(current_files, list):
        failures.append("current regression report or source manifest has no complete SHA-256 file map")
    else:
        expected = {
            str(record.get("path")): str(record.get("sha256"))
            for record in current_files if isinstance(record, dict)
            and any(str(record.get("path", "")) == root or
                    str(record.get("path", "")).startswith(root.rstrip("/") + "/") for root in roots)
        }
        if not expected:
            failures.append("current regression source_roots match no files in the current source manifest")
        if selected != expected:
            missing = sorted(set(expected) - set(selected))
            extra = sorted(set(selected) - set(expected))
            changed = sorted(path for path in set(expected) & set(selected) if expected[path] != selected[path])
            failures.append(
                "current regression source hashes do not match the current source manifest "
                f"(missing={len(missing)}, extra={len(extra)}, changed={len(changed)})"
            )

    pass_count = report.get("passed_events")
    if not isinstance(pass_count, int) or isinstance(pass_count, bool) or pass_count <= 0:
        failures.append("current regression report must declare a positive pass-event count")
    if report.get("exit") != 0 or report.get("failed") != [] or report.get("skipped") != []:
        failures.append("current regression report is not a complete zero-failure, zero-skip pass")
    if report.get("selected_source_unchanged") is not True:
        failures.append("current regression report does not prove selected source stayed unchanged")

    log_name = report.get("log")
    if not isinstance(log_name, str) or not log_name:
        failures.append("current regression report must name its JSONL event log")
        return
    log_path = Path(log_name)
    if log_path.is_absolute() or ".." in log_path.parts:
        failures.append("current regression log must be a workspace-relative path")
        return
    try:
        log_bytes = (ROOT / log_path).read_bytes()
        log_text = log_bytes.decode("utf-8")
        events = [json.loads(line) for line in log_text.splitlines() if line]
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        failures.append(f"cannot read current regression event log {log_path}: {error}")
        return
    if any(not isinstance(event, dict) for event in events):
        failures.append("current regression JSONL contains a non-object event")
        events = [event for event in events if isinstance(event, dict)]
    expected_log_digest = report.get("log_sha256")
    if not isinstance(expected_log_digest, str) or not re.fullmatch(r"[0-9a-f]{64}", expected_log_digest):
        failures.append("current regression report must carry the JSONL SHA-256")
    elif hashlib.sha256(log_bytes).hexdigest() != expected_log_digest:
        failures.append("current regression JSONL does not match its recorded SHA-256")
    actual_passes = sum(1 for event in events if event.get("Action") == "pass")
    named_passes = sum(1 for event in events if event.get("Action") == "pass" and event.get("Test"))
    actual_failures = sum(1 for event in events if event.get("Action") == "fail")
    actual_skips = sum(1 for event in events if event.get("Action") == "skip")
    passed_packages = {event.get("Package") for event in events
                       if event.get("Action") == "pass" and not event.get("Test")}
    if not {"contractaudit/internal/store", "contractaudit/internal/httpapi"}.issubset(passed_packages):
        failures.append("current regression log must include completed Store and HTTP package passes")
    if isinstance(pass_count, int) and actual_passes != pass_count:
        failures.append(f"current regression report says {pass_count} passes but its log has {actual_passes}")
    named_pass_count = report.get("named_pass_events")
    if not isinstance(named_pass_count, int) or isinstance(named_pass_count, bool) or named_pass_count != named_passes:
        failures.append(
            f"current regression report named-pass count {named_pass_count!r} does not match its log ({named_passes})")
    if actual_failures or actual_skips:
        failures.append(f"current regression event log has {actual_failures} failures and {actual_skips} skips")

def load_prerequisite_records(path: Path) -> tuple[dict[str, object], list[str]]:
    failures: list[str] = []
    if not path.is_file():
        return {}, [f"prerequisite evidence {path} is missing"]
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        return {}, [f"prerequisite evidence {path} is not valid JSON: {error}"]
    if not isinstance(document, dict):
        return {}, ["prerequisite evidence must be a JSON object"]
    items = document.get("items", {})
    if not isinstance(items, dict):
        failures.append("prerequisite evidence has no items object")
        items = {}
    for key, description in ITEM_DEFINITIONS.items():
        entry = items.get(key)
        if not isinstance(entry, dict):
            failures.append(f"prerequisite {key} is missing ({description})")
            continue
        if str(entry.get("status", "")).upper() != "PASS":
            failures.append(f"prerequisite {key} is not PASS ({entry.get('status')!r})")
        evidence = entry.get("evidence")
        if not isinstance(evidence, list) or not evidence or not all(isinstance(item, str) and item.strip() for item in evidence):
            failures.append(f"prerequisite {key} has no evidence")
            continue
        for reference in evidence:
            reference_path = Path(reference)
            candidate = ROOT / reference_path
            if (not reference_path.is_absolute() and ".." not in reference_path.parts
                    and not candidate.is_symlink() and candidate.is_file()):
                continue
            if (not reference_path.is_absolute() and ".." not in reference_path.parts
                    and re.fullmatch(r"[0-9a-fA-F]{7,64}", reference.strip())):
                continue
            failures.append(f"prerequisite {key} evidence {reference!r} is neither a workspace file nor a commit id")
    identity = document.get("source_identity", {})
    if not isinstance(identity, dict):
        failures.append("source_identity must be an object")
        identity = {}
    manifest_ref = identity.get("manifest")
    manifest_path = Path(manifest_ref) if isinstance(manifest_ref, str) else None
    current = source_identity_module().source_identity(ROOT)
    if (manifest_path is None or manifest_path.is_absolute() or not manifest_path.parts
            or ".." in manifest_path.parts):
        failures.append("source_identity.manifest must be a workspace-relative recorded identity manifest")
    else:
        manifest_path = ROOT / manifest_path
        if not manifest_path.is_file():
            failures.append("source_identity.manifest must reference the recorded identity manifest")
        else:
            try:
                recorded = json.loads(manifest_path.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError) as error:
                failures.append(f"cannot read source identity manifest: {error}")
                recorded = None
            if not isinstance(recorded, dict):
                failures.append("source identity manifest must be a JSON object")
            else:
                recorded_digest = recorded.get("digest")
                if not isinstance(recorded_digest, str) or not re.fullmatch(r"[0-9a-f]{64}", recorded_digest):
                    failures.append("source identity manifest must carry its SHA-256 digest")
                elif recorded_digest != current["digest"]:
                    failures.append(
                        "the prerequisites were recorded against source identity "
                        f"{recorded_digest[:12]} but the worktree is now {current['digest'][:12]}; "
                        "re-validate the six prerequisites against the current source identity"
                    )
                if identity.get("digest") != recorded_digest:
                    failures.append("source_identity.digest does not match its manifest file")
    validate_current_regression(document, current, failures)
    return document, failures


def live_prerequisites(stack: Stack, journey, base_url: str, auth_context: dict) -> tuple[list[str], dict[str, object]]:
    """Machine-checkable prerequisites; everything else must be evidenced."""
    failures: list[str] = []
    measured: dict[str, object] = {}
    model_gateway_service, _ = select_model_gateway_service(stack)
    expected_roles = {
        "core-api": "core", "document-service": "document", "audit-execution": "execution",
        "audit-worker": "worker", model_gateway_service: "model", "tool-gateway": "tool",
    }
    process_facts = {service: stack.process_facts(service) for service in expected_roles}
    measured["process_identity"] = process_facts
    for service, expected_role in expected_roles.items():
        if not process_facts[service]:
            failures.append(f"{service} has no identifiable running process")
        for fact in process_facts[service]:
            if fact.get("service_role") != expected_role:
                failures.append(f"{service} instance {fact.get('pid', fact.get('container'))} has service role "
                                f"{fact.get('service_role')!r}, expected {expected_role!r}")
            if "pid" in fact:
                if fact.get("declared_hash_matches_running") is not True:
                    failures.append(f"{service} pid {fact['pid']} does not match its declared executable on disk")
                if fact.get("recorded_hash_matches") is False:
                    failures.append(f"{service} pid {fact['pid']} differs from its pinned binary SHA-256")
    current_source = source_identity_module().source_identity(ROOT)
    measured["source_identity"] = {"algorithm": current_source["algorithm"], "digest": current_source["digest"]}
    if stack.control == "compose":
        image_ids = sorted({str(fact["image_id"]) for facts in process_facts.values()
                            for fact in facts if fact.get("image_id")})
        tagged_image = stack.recorder.run(
            [stack.docker, "image", "inspect", "--format", "{{.Id}}", "contract-audit-backend:local"],
            timeout=60)
        tagged_image_id = tagged_image.stdout.decode("utf-8", "replace").strip()
        measured["backend_image"] = {"running_image_ids": image_ids, "local_tag_image_id": tagged_image_id}
        if tagged_image.returncode != 0 or not tagged_image_id or image_ids != [tagged_image_id]:
            failures.append("running backend services do not all use the current contract-audit-backend:local image")
    roles = stack.psql_rows("SELECT service FROM service_database_roles ORDER BY service")
    measured["service_roles"] = roles
    if sorted(roles) != ["core", "document", "execution", "model", "tool"]:
        failures.append(f"service_database_roles does not bind the five service roles: {roles!r}")
    if len(roles) != 5:
        failures.append(f"expected five service roles, found {len(roles)}")

    missing_rls = stack.psql_rows(
        "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
        "WHERE n.nspname='public' AND c.relkind='r' AND c.relname = ANY (ARRAY["
        + ",".join("'" + table + "'" for table in RLS_TABLES) + "]) "
        "AND (NOT c.relrowsecurity OR NOT c.relforcerowsecurity)")
    measured["rls_without_force"] = missing_rls
    if missing_rls:
        failures.append(f"tables without ENABLE+FORCE RLS: {missing_rls!r}")
    absent_rls = stack.psql_rows(
        "SELECT name FROM (VALUES " + ",".join(f"('{table}')" for table in RLS_TABLES) + ") v(name) "
        "WHERE to_regclass('public.' || name) IS NULL")
    measured["rls_tables_absent"] = absent_rls
    if absent_rls:
        failures.append(f"RLS tables absent: {absent_rls!r}")

    absent = stack.psql_rows(
        "SELECT name FROM (VALUES " + ",".join(f"('{table}')" for table in HOT_TABLES) + ") v(name) "
        "WHERE to_regclass('public.' || name) IS NULL")
    measured["hot_tables_absent"] = absent
    if absent:
        failures.append(f"hot entity tables absent: {absent!r}")

    indexes = stack.psql_rows(
        "SELECT indexname FROM pg_indexes WHERE schemaname='public' AND "
        "(tablename='worker_jobs' OR tablename='idempotency' OR tablename='run_events') "
        "AND indexdef ILIKE '%UNIQUE%'")
    measured["unique_indexes"] = indexes
    for needle in ("attempt_id", "idem", "run_events_identity"):
        if not any(needle in name for name in indexes):
            failures.append(f"no unique index containing {needle!r} was found")

    claim_columns = stack.psql_rows(
        "SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name='worker_jobs' "
        "AND column_name IN ('generation','worker_id','lease_until','heartbeat_at','attempt_id')")
    measured["worker_job_columns"] = claim_columns
    if len(claim_columns) != 5:
        failures.append(f"worker_jobs claim/CAS columns missing: {claim_columns!r}")

    client = journey.Journey(base_url, time.monotonic() + 60)
    before = int(stack.psql_scalar("SELECT count(*) FROM sessions"))
    authenticate_client(client, auth_context)
    after = int(stack.psql_scalar("SELECT count(*) FROM sessions"))
    measured["session_rows"] = {"before": before, "after": after}
    if after != before + 1:
        failures.append("a session created through the edge is not visible in shared PostgreSQL state")
    client.call("DELETE", "/api/session", expected={200, 204})
    after_logout = int(stack.psql_scalar("SELECT count(*) FROM sessions"))
    measured["session_rows"]["after_logout"] = after_logout
    if after_logout != before:
        failures.append("logout did not remove the shared session row")
    return failures, measured


class SseStream:
    """Minimal event-stream reader with a bounded read timeout."""

    def __init__(self, base: str, path: str, cookie: str, last_event_id: int | None = None,
                 read_timeout: float = 2.0) -> None:
        parts = urlsplit(base)
        self.connection = http.client.HTTPConnection(parts.hostname, parts.port or 80, timeout=10)
        headers = {"Accept": "text/event-stream", "Cookie": cookie}
        if last_event_id is not None:
            headers["Last-Event-ID"] = str(last_event_id)
        self.connection.request("GET", path, headers=headers)
        self.response = self.connection.getresponse()
        if self.response.status != 200:
            raise Failure(f"event stream answered HTTP {self.response.status}")
        if self.connection.sock is not None:
            self.connection.sock.settimeout(read_timeout)
        self.buffer = ""

    def read(self, seconds: float, max_events: int = 2000) -> tuple[list[int], bool]:
        events: list[int] = []
        last_id = 0
        started = time.monotonic()
        while time.monotonic() - started < seconds:
            try:
                chunk = self.response.read1(4096)
            except (socket.timeout, TimeoutError):
                continue
            except (OSError, http.client.HTTPException):
                return events, True
            if not chunk:
                return events, True
            self.buffer += chunk.decode("utf-8", "replace")
            while "\n" in self.buffer:
                line, self.buffer = self.buffer.split("\n", 1)
                line = line.rstrip("\r")
                if line.startswith("id: "):
                    try:
                        last_id = int(line[4:])
                    except ValueError:
                        last_id = 0
                elif line.startswith("data: ") and last_id:
                    events.append(last_id)
                    if len(events) >= max_events:
                        return events, False
        return events, False

    def wait_closed(self, seconds: float) -> bool:
        events, closed = self.read(seconds)
        del events
        return closed

    def close(self) -> None:
        try:
            self.connection.close()
        except OSError:
            pass


def cookie_header(client) -> str:
    for handler in client.opener.handlers:
        if isinstance(handler, urllib.request.HTTPCookieProcessor):
            return "; ".join(f"{cookie.name}={cookie.value}" for cookie in handler.cookiejar)
    raise Failure("authenticated HTTP client has no cookie processor")


def direct_tool_list(stack: Stack, instance: str, cookie: str, service_key: str,
                     listener: tuple[str, int] | None = None) -> str:
    address, port = listener if listener is not None else stack.instance_addr(instance)
    if not address or not port:
        return "no listener address configured"
    metadata = {
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientCapabilities": {},
        "io.modelcontextprotocol/clientInfo": {"name": "d2-8-replica-probe", "version": "1"},
    }
    body = json.dumps({"jsonrpc": "2.0", "id": "replica-probe", "method": "tools/list",
                       "params": {"_meta": metadata}}).encode("utf-8")
    headers = {
        "Accept": "application/json, text/event-stream",
        "Content-Type": "application/json",
        "MCP-Protocol-Version": "2026-07-28",
        "Mcp-Method": "tools/list",
        "Cookie": cookie,
        "X-Contract-Service-Key": service_key,
    }
    connection = http.client.HTTPConnection(address, port, timeout=10)
    try:
        connection.request("POST", "/mcp", body=body, headers=headers)
        response = connection.getresponse()
        raw = response.read()
        if response.status != 200:
            return f"http {response.status}"
        try:
            result = json.loads(raw)
        except json.JSONDecodeError:
            return "invalid JSON response"
        tools = result.get("result", {}).get("tools") if isinstance(result, dict) else None
        if not isinstance(tools, list) or "error" in result:
            return "MCP tools/list result was malformed"
        return f"http 200; tools={len(tools)}"
    except (OSError, http.client.HTTPException) as error:
        return f"unreachable: {error}"
    finally:
        connection.close()



def select_model_gateway_service(stack: Stack) -> tuple[str, list[str]]:
    for service in ("model-gateway", "model-gateway-fixture"):
        instances = stack.containers(service)
        if instances:
            return service, instances
    return "", []


def worker_gateway_routes(stack: Stack, workers: list[str], service: str,
                          gateways: list[str]) -> tuple[bool, dict[str, object]]:
    targets: set[tuple[str, int]] = set()
    ports: set[int] = set()
    for gateway in gateways:
        address, port = stack.instance_addr(gateway)
        if address and port:
            targets.add((address, port))
            ports.add(port)
    routes: dict[str, object] = {}
    matched = bool(workers and gateways and ports)
    for worker in workers:
        endpoint = stack.env_value(worker, "MODEL_GATEWAY_URL")
        parsed = urlsplit(endpoint)
        host = (parsed.hostname or "").lower()
        port = parsed.port or (443 if parsed.scheme == "https" else 80)
        service_host = host in {service, "model-gateway", "model-gateway-fixture"}
        address_host = any(host == target or (host == "localhost" and target == "127.0.0.1")
                           for target, target_port in targets if target_port == port)
        route_matches = bool(endpoint and port in ports and (service_host or address_host))
        routes[worker] = {"host": host, "port": port, "matches_gateway": route_matches}
        matched = matched and route_matches
    return matched, routes
def frozen_prefix(journey, client, label: str) -> dict[str, object]:
    """The frozen journey steps before review acceptance, reusing its texts."""
    _, case, _ = client.call(
        "POST", "/api/cases",
        {"title": f"D2-8 {label}", "contract_type": "procurement_ict"}, expected={201})
    case_id = str(case["id"])  # type: ignore[index]
    procurement = client.upload(case_id, "procurement", "purchase.txt", "text/plain", journey.PROCUREMENT_TEXT)
    draft = client.upload(case_id, "draft_contract", "draft.txt", "text/plain", journey.DRAFT_TEXT)
    for document in (procurement, draft):
        client.call("POST", f"/api/cases/{case_id}/documents/{document}/parse", {}, expected={200})
    return {"case_id": case_id, "documents": [procurement, draft]}


def submit_review(client, case_id: str) -> dict:
    status, review, _ = client.call("POST", f"/api/cases/{case_id}/reviews", {}, expected={200, 201, 202})
    run_id = str(review["id"])  # type: ignore[index]
    return {"run_id": run_id, "job_id": str(review.get("job_id") or ""), "status": status}  # type: ignore[union-attr]


def complete_review(journey, client, case_id: str, run_id: str, label: str) -> dict:
    workspace = client.poll_run(case_id, run_id, label)
    run = workspace["run"]
    if run.get("state") == "failed":
        raise Failure(f"{label}: review failed: {run.get('failure_message')!r}")
    findings = workspace.get("findings") or []
    return {"state": run.get("state"), "findings": len(findings),
            "rule_conflicts": sum(1 for finding in findings
                                  if finding.get("origin") == "rule" and finding.get("relation") == "conflict")}


def event_integrity(stack: Stack, run_id: str) -> dict[str, object]:
    """Official events must be contiguous and unique, never silently replayed."""
    result: dict[str, object] = {}
    for table, column in (("run_events", "event_seq"), ("audit_events", "seq")):
        rows = stack.psql_rows(
            f"SELECT count(*), COALESCE(max({column}),0), count(DISTINCT {column}) FROM {table} "
            f"WHERE run_id='{run_id}'")
        count, maximum, distinct = (int(value) for value in rows[0].split("|"))
        result[table] = {"count": count, "max": maximum, "distinct": distinct}
        if not (count == maximum == distinct):
            raise Failure(f"{table} for run {run_id} is not a contiguous unique sequence: {result[table]}")
    return result

def sql_literal(value: str) -> str:
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", value):
        raise Failure("an API or database identity used an unexpected SQL key format")
    return f"'{value}'"


def scenario_owner_scope(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    setup = context.get("oidc")
    if not setup:
        return "BLOCKED_MISSING_HOOK", {}, "real two-tenant OIDC identities are required for owner and RLS checks"
    trusted_users = {str(user["id"]): user for user in setup["directory"]["users"]}
    owner_user = trusted_users["demo-clerk"]
    same_tenant_user = trusted_users["demo-admin"]
    other_tenant_user = trusted_users["d2-tenant-b-clerk"]
    if (owner_user["tenant_id"] != same_tenant_user["tenant_id"]
            or owner_user["tenant_id"] == other_tenant_user["tenant_id"]):
        raise Failure("trusted identity directory does not provide same-tenant and cross-tenant principals")

    owner_client = journey.Journey(context["base_url"], time.monotonic() + 300)
    owner_actor = authenticate_client(owner_client, context, "demo-clerk")
    forged_status, _, _ = owner_client.call(
        "POST", "/api/cases",
        {"title": "D2-8 rejected identity override", "contract_type": "procurement_ict",
         "tenant_id": other_tenant_user["tenant_id"], "owner_org_id": other_tenant_user["org_id"],
         "owner_id": other_tenant_user["id"]}, expected={422})
    _, created, _ = owner_client.call(
        "POST", "/api/cases",
        {"title": "D2-8 owner and RLS isolation", "contract_type": "procurement_ict"}, expected={201})
    case_id = str(created["id"])
    case_sql = sql_literal(case_id)
    owner_tenant = str(owner_user["tenant_id"])
    owner_org = str(owner_user["org_id"])
    other_tenant = str(other_tenant_user["tenant_id"])
    other_org = str(other_tenant_user["org_id"])
    owner_row = stack.psql_rows(
        "SELECT tenant_id,org_id,owner_id,member_ids FROM cases WHERE id=" + case_sql)
    if len(owner_row) != 1:
        raise Failure("the owner-created case was not found in the shared database")
    row_fields = owner_row[0].split("|", 3)
    try:
        member_ids = json.loads(row_fields[3])
    except (IndexError, json.JSONDecodeError) as error:
        raise Failure("the owner-created case has invalid persisted members") from error

    same_tenant_client = journey.Journey(context["base_url"], time.monotonic() + 60)
    authenticate_client(same_tenant_client, context, "demo-admin")
    same_tenant_status, _, _ = same_tenant_client.call(
        "GET", f"/api/cases/{case_id}", expected={403, 404})
    other_tenant_client = journey.Journey(context["base_url"], time.monotonic() + 60)
    authenticate_client(other_tenant_client, context, "d2-tenant-b-clerk")
    cross_tenant_status, _, _ = other_tenant_client.call(
        "GET", f"/api/cases/{case_id}", expected={403, 404})
    cross_write_status, _, _ = other_tenant_client.call(
        "POST", f"/api/cases/{case_id}/members", {"actor_id": other_tenant_user["id"]},
        expected={403, 404})

    core_role = stack.psql_scalar("SELECT role_name FROM service_database_roles WHERE service='core'")
    core_user = urllib.parse.unquote(urlsplit(stack.app_env["CORE_DATABASE_URL"]).username or "")
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", core_role) or core_user != core_role:
        raise Failure("the registered Core database role does not match its dedicated database login")
    sql = (
        f"BEGIN; SELECT set_config('app.tenant_id',{sql_literal(owner_tenant)},true),"
        f"set_config('app.org_id',{sql_literal(owner_org)},true); "
        f"SELECT count(*) FROM cases WHERE id={case_sql}; "
        f"SELECT set_config('app.tenant_id',{sql_literal(other_tenant)},true),"
        f"set_config('app.org_id',{sql_literal(other_org)},true); "
        f"SELECT count(*) FROM cases WHERE id={case_sql}; ROLLBACK"
    )
    rls_rows = stack.psql_service_rows("core", sql)
    measured = {
        "case_id": case_id,
        "owner_actor_id": owner_actor.get("id"),
        "persisted_owner_scope": {"tenant_id": row_fields[0], "org_id": row_fields[1],
                                  "owner_id": row_fields[2], "member_ids": member_ids},
        "requested_identity_override": {"tenant_id": other_tenant, "org_id": other_org,
                                        "owner_id": other_tenant_user["id"]},
        "forged_identity_request_status": forged_status,
        "same_tenant_non_owner_detail_status": same_tenant_status,
        "cross_tenant_detail_status": cross_tenant_status,
        "cross_tenant_member_write_status": cross_write_status,
        "core_role": core_role,
        "rls_visibility_rows": rls_rows,
    }
    if (row_fields[:3] != [owner_tenant, owner_org, str(owner_actor.get("id"))]
            or not isinstance(member_ids, list) or str(owner_actor.get("id")) not in member_ids):
        return "FAIL", measured, "case creation did not derive tenant, organization and ownership from the trusted actor"
    if same_tenant_status not in (403, 404):
        return "FAIL", measured, "a same-tenant non-owner could read the owner's case"
    if cross_tenant_status not in (403, 404) or cross_write_status not in (403, 404):
        return "FAIL", measured, "a cross-tenant principal could read or modify the owner's case"
    if len(rls_rows) != 4 or rls_rows[1] != "1" or rls_rows[3] != "0":
        return "FAIL", measured, "the dedicated Core role did not observe the case only under its owning tenant RLS context"
    return "PASS", measured, "trusted owner fields persisted; same-tenant non-owner and cross-tenant reads/writes were denied; Core FORCE RLS hid the row"


def simulate_queue_schedule(tenants: list[str], weights: dict[str, int], cursor: str,
                            deficits: dict[str, int], jobs_per_tenant: dict[str, int]) -> list[str]:
    remaining = dict(jobs_per_tenant)
    balance = {tenant: deficits.get(tenant, 0) for tenant in tenants}
    scheduled: list[str] = []
    while any(count > 0 for count in remaining.values()):
        eligible = [tenant for tenant in tenants if remaining[tenant] > 0]
        tenant = min(eligible, key=lambda item: (
            0 if item == cursor and balance[item] > 0 else 1 if item > cursor else 2, item))
        if balance[tenant] == 0:
            balance[tenant] = weights.get(tenant, 1)
        balance[tenant] -= 1
        remaining[tenant] -= 1
        scheduled.append(tenant)
        cursor = tenant
        if remaining[tenant] == 0:
            balance[tenant] = 0
    return scheduled
def validate_recorded_queue_policy(policy: object) -> dict[str, int]:
    fields = {
        "version", "global_inflight", "tenant_inflight", "global_pending", "tenant_pending",
        "max_claims", "max_age_seconds", "backoff_base_ms", "backoff_max_ms", "weights",
    }
    if not isinstance(policy, dict) or set(policy) != fields:
        raise Failure("the recorded queue policy does not match the trusted QueuePolicy JSON schema")
    version = policy["version"]
    if not isinstance(version, str) or not version.strip() or len(version.encode("utf-8")) > 80:
        raise Failure("the recorded queue policy has an invalid version")

    def integer(key: str) -> int:
        value = policy[key]
        if type(value) is not int:
            raise Failure(f"the recorded queue policy field {key} is not an integer")
        return value

    global_inflight = integer("global_inflight")
    tenant_inflight = integer("tenant_inflight")
    global_pending = integer("global_pending")
    tenant_pending = integer("tenant_pending")
    max_claims = integer("max_claims")
    max_age = integer("max_age_seconds")
    backoff_base = integer("backoff_base_ms")
    backoff_max = integer("backoff_max_ms")
    if (not 1 <= global_inflight <= 1024 or not 1 <= tenant_inflight <= global_inflight
            or not global_inflight <= global_pending <= 100000
            or not tenant_inflight <= tenant_pending <= global_pending
            or not 1 <= max_claims <= 16 or not 1 <= max_age <= 86400
            or not 1 <= backoff_base <= backoff_max <= 60000):
        raise Failure("the recorded queue policy violates trusted startup bounds")

    raw_weights = policy["weights"]
    if raw_weights is None:
        return {}
    if not isinstance(raw_weights, dict) or len(raw_weights) > 1000:
        raise Failure("the recorded queue policy has invalid tenant weights")
    weights: dict[str, int] = {}
    for tenant, weight in raw_weights.items():
        if (not isinstance(tenant, str) or not tenant or tenant.strip() != tenant
                or len(tenant.encode("utf-8")) > 200 or type(weight) is not int or not 1 <= weight <= 100):
            raise Failure("the recorded queue policy has invalid tenant weights")
        weights[tenant] = weight
    return weights





def expected_queue_retry_delay_ms(job_id: str, claim_count: int, policy: dict[str, object]) -> int:
    base = int(policy["backoff_base_ms"])
    maximum = int(policy["backoff_max_ms"])
    delay = base
    for _ in range(1, claim_count):
        if delay >= maximum:
            break
        delay = min(delay * 2, maximum)
    hash_value = 14695981039346656037
    for byte in job_id.encode("utf-8"):
        hash_value = ((hash_value ^ byte) * 1099511628211) & 0xFFFFFFFFFFFFFFFF
    jitter = hash_value % max(delay // 4, 1)
    return min(delay + jitter, maximum)
def scenario_queue_fairness(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    setup = context.get("oidc")
    if not setup:
        return "BLOCKED_MISSING_HOOK", {}, "real two-tenant OIDC identities are required for queue fairness"
    trusted_users = {str(user["id"]): user for user in setup["directory"]["users"]}
    user_ids = ("demo-clerk", "d2-tenant-b-clerk")
    tenant_for_user = {user_id: str(trusted_users[user_id]["tenant_id"]) for user_id in user_ids}
    if len(set(tenant_for_user.values())) != 2:
        raise Failure("the trusted identity directory does not contain two distinct queue tenants")
    workers = stack.assert_targets_confirmed_database("audit-worker")
    if len(workers) < 2:
        raise Failure("the queue fairness scenario needs two confirmed Worker replicas")
    active_before = int(stack.psql_scalar(
        "SELECT count(*) FROM worker_jobs WHERE state IN ('queued','running')"))
    if active_before:
        raise Failure(f"queue fairness requires an empty active queue; found {active_before} jobs")

    workload: list[tuple[object, str, str, str]] = []
    stopped_workers: list[str] = []
    expected_schedule: list[str] = []
    observed_rows: list[str] = []
    cursor_before = ""
    policy: dict[str, object] = {}
    scheduler_state: dict[str, int] = {}
    restored_workers: list[str] = []
    try:
        for worker in workers:
            stack.stop(worker)
            stopped_workers.append(worker)
        if stack.containers("audit-worker"):
            raise Failure("a Worker remained live while the fairness queue was being prepared")

        for index in range(6):
            user_id = user_ids[index % 2]
            tenant_id = tenant_for_user[user_id]
            client = journey.Journey(context["base_url"], time.monotonic() + 900)
            authenticate_client(client, context, user_id)
            case_id = str(frozen_prefix(journey, client, f"fair-{index}")["case_id"])
            handle = submit_review(client, case_id)
            run_id = str(handle["run_id"])
            workload.append((client, case_id, run_id, tenant_id))

        runs_sql = ",".join(sql_literal(run_id) for _, _, run_id, _ in workload)
        queued = stack.psql_rows(
            "SELECT count(*) FILTER(WHERE state='queued'),count(*),count(DISTINCT tenant_id) "
            f"FROM worker_jobs WHERE run_id IN ({runs_sql})")
        queued_count, total_count, queued_tenants = (int(value) for value in queued[0].split("|"))
        if queued_count != len(workload) or total_count != len(workload) or queued_tenants != 2:
            raise Failure(f"the two-tenant workload did not remain durably queued: {queued!r}")

        cursor_before = stack.psql_scalar(
            "SELECT last_tenant FROM execution_queue_cursor WHERE tenant_id='__scheduler__'")
        tenant_sql = ",".join(sql_literal(tenant) for tenant in sorted(tenant_for_user.values()))
        scheduler_state = {
            row.split("|")[0]: int(row.split("|")[1])
            for row in stack.psql_rows(
                f"SELECT tenant_id,deficit FROM execution_queue_tenants WHERE tenant_id IN ({tenant_sql})")
        }
        policies = stack.psql_rows(
            "SELECT DISTINCT j.policy_hash,encode(convert_to(p.policy_json,'UTF8'),'hex') "
            "FROM worker_jobs j JOIN execution_queue_policies p ON p.policy_hash=j.policy_hash "
            f"WHERE j.run_id IN ({runs_sql})")
        if len(policies) != 1:
            raise Failure(f"fairness workload does not share one recorded queue policy: {policies!r}")
        policy_hash, encoded_policy = policies[0].split("|", 1)
        policy = json.loads(bytes.fromhex(encoded_policy).decode("utf-8"))
        weights = validate_recorded_queue_policy(policy)
        job_counts = {tenant: sum(1 for _, _, _, item_tenant in workload if item_tenant == tenant)
                      for tenant in sorted(tenant_for_user.values())}
        if set(scheduler_state) != set(job_counts):
            raise Failure("the persisted scheduler state does not contain both active tenant queues")
        for tenant, deficit in scheduler_state.items():
            effective_weight = weights.get(tenant, 1)
            if deficit < 0 or deficit >= effective_weight:
                raise Failure(f"the persisted deficit is outside the trusted scheduler range for tenant {tenant!r}")
        expected_schedule = simulate_queue_schedule(
            list(sorted(job_counts)), weights, cursor_before, scheduler_state, job_counts)

        stack.scale({"audit-worker": 1})
        stack.wait_healthy("audit-worker", 1)
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(workload)) as executor:
            completions = [executor.submit(client.poll_run, case_id, run_id, f"fair-{index}")
                           for index, (client, case_id, run_id, _) in enumerate(workload)]
            for future in completions:
                future.result()

        observed_rows = stack.psql_rows(
            "SELECT j.tenant_id,j.run_id,coalesce(j.worker_id,''),j.claim_count,j.state,"
            "CASE WHEN a.started_at IS NULL THEN 'NULL' ELSE "
            "round(EXTRACT(EPOCH FROM (a.started_at::timestamptz-a.accepted_at::timestamptz))::numeric,3)::text END "
            "FROM worker_jobs j JOIN review_attempts a ON a.id=j.attempt_id "
            f"WHERE j.run_id IN ({runs_sql}) ORDER BY a.started_at::timestamptz,j.run_id")
    finally:
        stack.scale({"audit-worker": len(workers)})
        restored_workers = stack.wait_healthy("audit-worker", len(workers), timeout=180)

    observed_schedule = [row.split("|", 5)[0] for row in observed_rows]
    queue_waits: dict[str, list[float]] = {tenant: [] for tenant in tenant_for_user.values()}
    terminal_states: dict[str, str] = {}
    for row in observed_rows:
        tenant_id, run_id, worker_id, claim_count, state, wait = row.split("|", 5)
        terminal_states[run_id] = state
        if wait != "NULL":
            queue_waits.setdefault(tenant_id, []).append(float(wait))
        if not worker_id or int(claim_count) != 1:
            raise Failure(f"a queued workload was not claimed exactly once by a Worker: {row!r}")
    measured = {
        "workers_stopped": stopped_workers,
        "workers_restored": restored_workers,
        "queued_jobs_by_tenant": job_counts,
        "queue_policy_hash": policy_hash,
        "queue_policy": policy,
        "scheduler_cursor_before_claim": cursor_before,
        "tenant_deficits_before_claim": scheduler_state,
        "expected_weighted_schedule": expected_schedule,
        "observed_weighted_schedule": observed_schedule,
        "queue_wait_seconds_by_tenant": queue_waits,
        "terminal_states": terminal_states,
    }
    if len(observed_rows) != len(workload) or len(terminal_states) != len(workload):
        return "FAIL", measured, "not every durable two-tenant job produced one terminal scheduling record"
    if observed_schedule != expected_schedule:
        return "FAIL", measured, "the observed queue order differs from the recorded weighted deficit-round-robin policy"
    if len(set(observed_schedule[:2])) != 2:
        return "FAIL", measured, "one tenant did not receive a claim while the other tenant still had queued work"
    if any(state not in journey.TERMINAL_RUN_STATES for state in terminal_states.values()):
        return "FAIL", measured, "one fairness workload run did not reach a terminal state"
    return "PASS", measured, "two tenant queues followed the persisted weighted scheduler order and all six jobs reached one terminal claim"

def scenario_baseline(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    stack.scale(dict(BASELINE_DEFAULTS))
    for service, count in BASELINE_DEFAULTS:
        stack.wait_healthy(service, count)
    client = journey.Journey(context["base_url"], time.monotonic() + 300)
    client.call("GET", "/health", expected={200})
    authenticate_client(client, context)
    journey_case = frozen_prefix(journey, client, "baseline")
    case_id = str(journey_case["case_id"])
    handle = submit_review(client, case_id)
    summary = complete_review(journey, client, case_id, handle["run_id"], "baseline")
    events = event_integrity(stack, handle["run_id"])
    attempts = stack.psql_rows(
        f"SELECT count(*), count(*) FILTER (WHERE state IN ('completed','partial','failed')) "
        f"FROM review_attempts WHERE run_id='{handle['run_id']}'")
    leases = stack.psql_rows(
        "SELECT count(*), count(*) FILTER (WHERE caller_identity='worker'), "
        "count(*) FILTER (WHERE caller_identity='document') FROM model_call_leases "
        f"WHERE run_id='{handle['run_id']}'")
    usage = stack.psql_rows(
        "SELECT count(*), count(*) FILTER (WHERE input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL) "
        f"FROM model_usage WHERE run_id='{handle['run_id']}'")
    return "PASS", {
        "run_state": summary["state"], "findings": summary["findings"],
        "rule_conflicts": summary["rule_conflicts"], "events": events,
        "attempts_total_terminal": attempts[0],
        "open_leases_worker_document": leases[0],
        "usage_rows_unknown_tokens": usage[0],
        "note": "single-replica reference; recovery may legitimately add events or calls, so this is not used as an equality assertion",
    }, "baseline captured with one replica per service"


def scenario_dual_replicas(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    wanted = context["scale"]
    stack.scale(wanted)
    measured: dict[str, object] = {}
    containers: dict[str, list[str]] = {}
    for service, count in wanted.items():
        containers[service] = stack.wait_healthy(service, count)
        stack.assert_targets_confirmed_database(service)
    measured["containers"] = containers
    measured["worker_ids"] = {instance: stack.instance_hostname(instance)
                              for instance in containers["audit-worker"]}
    measured["service_roles"] = {instance: stack.service_role(instance)
                                 for service, items in containers.items() for instance in items}
    measured["instance_labels"] = {
        service: {container: stack.instance_label(container) for container in service_containers}
        for service, service_containers in containers.items()
    }

    expected_roles = {"core-api": "core", "audit-worker": "worker", "tool-gateway": "tool"}
    role_failures = {instance: measured["service_roles"][instance]
                     for service, instances in containers.items() for instance in instances
                     if measured["service_roles"][instance] != expected_roles[service]}
    worker_ids = list(measured["worker_ids"].values())
    if len(worker_ids) != 2 or any(not worker_id for worker_id in worker_ids) or len(set(worker_ids)) != 2:
        role_failures["worker_id"] = worker_ids
    measured["role_failures"] = role_failures

    client = journey.Journey(context["base_url"], time.monotonic() + 120)
    authenticate_client(client, context)
    cookie = cookie_header(client)
    reachability: dict[str, str] = {}
    for instance in containers["core-api"]:
        address, port = stack.instance_addr(instance)
        if not address or not port:
            reachability[instance] = "no address/port in the instance configuration"
            continue
        connection = None
        try:
            connection = http.client.HTTPConnection(address, port, timeout=5)
            connection.request("GET", "/api/cases", headers={
                "Cookie": cookie,
                "X-Contract-Service-Key": stack.env_value(instance, "GATEWAY_ROLE_TOKEN"),
            })
            response = connection.getresponse()
            response.read()
            reachability[instance] = f"http {response.status}"
        except (OSError, http.client.HTTPException) as error:
            reachability[instance] = f"unreachable: {error}"
        finally:
            if connection is not None:
                connection.close()
    measured["direct_core_replica_reachability"] = reachability
    tool_reachability = {
        instance: direct_tool_list(stack, instance, cookie, stack.env_value(instance, "GATEWAY_ROLE_TOKEN"))
        for instance in containers["tool-gateway"]
    }
    measured["direct_tool_replica_reachability"] = tool_reachability
    measured["direct_note"] = ("the same shared session reads through both Core replicas and lists tools through both "
                                "Tool Gateway replicas; requests are not required to use a sticky route")
    if role_failures:
        return "FAIL", measured, f"service identity or worker ID checks failed: {role_failures!r}"
    failed_core = [instance for instance, state in reachability.items() if state != "http 200"]
    failed_tool = [instance for instance, state in tool_reachability.items()
                   if not state.startswith("http 200; tools=") or state.endswith("tools=0")]
    if failed_core or failed_tool:
        return "FAIL", measured, f"direct replica requests failed: core={failed_core!r}, tool={failed_tool!r}"
    return "PASS", measured, "two Core, Worker and Tool replicas have unique identities; shared-session reads and tools/list reach both API surfaces"


def scenario_core_fault(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    containers = stack.assert_targets_confirmed_database("core-api")
    if len(containers) < 2:
        raise Failure("the core fault scenario needs at least two replicas")
    client = journey.Journey(context["base_url"], time.monotonic() + 180)
    authenticate_client(client, context)
    target = containers[0]
    started = time.monotonic()
    stack.kill(target)
    successes = 0
    failures = 0
    down_samples = 0
    window_end = time.monotonic() + 20
    while time.monotonic() < window_end:
        if target not in stack.containers("core-api"):
            down_samples += 1
        try:
            client.call("GET", "/api/cases", expected={200})
            successes += 1
        except (journey.Failure, OSError):
            failures += 1
        time.sleep(1)
    recovery_started = time.monotonic()
    stack.scale(context["scale"])
    restored = stack.wait_healthy("core-api", context["scale"]["core-api"], timeout=180)
    recovery_seconds = round(time.monotonic() - recovery_started, 2)
    stack.assert_targets_confirmed_database("core-api")
    _, _, _ = client.call("GET", "/api/cases", expected={200})
    measured = {
        "target": target,
        "fault_duration_seconds": round(time.monotonic() - started - recovery_seconds, 2),
        "observed_down_samples": down_samples,
        "requests_ok_during_fault": successes,
        "requests_failed_during_fault": failures,
        "recovery_seconds_after_restart": recovery_seconds,
        "restored_instances": restored,
        "target_replaced": target not in restored,
    }
    if down_samples == 0:
        return "FAIL", measured, "the targeted Core process was not observed down during the fault window"
    if successes == 0:
        return "FAIL", measured, "no edge request succeeded while the replica was faulted"
    if len(restored) != context["scale"]["core-api"] or target in restored:
        return "FAIL", measured, "the killed Core replica was not replaced into the declared topology"
    return "PASS", measured, "the edge served the shared session during the fault and a new Core process recovered on the same database"


def scenario_worker_takeover(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    containers = stack.assert_targets_confirmed_database("audit-worker")
    if len(containers) < 2:
        raise Failure("the worker takeover scenario needs at least two replicas")
    hostnames = {instance: stack.instance_hostname(instance) for instance in containers}
    lease = stack.env_value(containers[0], "WORKER_LEASE_DURATION") or "30s"
    heartbeat = stack.env_value(containers[0], "WORKER_HEARTBEAT_INTERVAL") or "10s"

    client = journey.Journey(context["base_url"], time.monotonic() + 300)
    authenticate_client(client, context)
    journey_case = frozen_prefix(journey, client, "takeover")
    case_id = str(journey_case["case_id"])
    handle = submit_review(client, case_id)
    run_id = handle["run_id"]

    gateway_service, gateways = select_model_gateway_service(stack)
    if not gateway_service or not gateways:
        return "BLOCKED_MISSING_HOOK", {}, "a controlled Model Gateway is required to hold an observable Worker claim"
    gateways = stack.assert_targets_confirmed_database(gateway_service)
    gateway_modes = {instance: stack.env_value(instance, "MODEL_GATEWAY_MODE") for instance in gateways}
    if any(mode != "fixture" for mode in gateway_modes.values()):
        return "BLOCKED_MISSING_HOOK", {"model_gateway_modes": gateway_modes}, (
            "the deterministic Worker takeover scenario requires the local fixture gateway; no real-model call is allowed")
    gateway_routes_ok, gateway_routes = worker_gateway_routes(stack, containers, gateway_service, gateways)
    if not gateway_routes_ok:
        return "BLOCKED_MISSING_HOOK", {"worker_gateway_routes": gateway_routes}, (
            "the confirmed Worker processes do not route Model Gateway calls to the controlled fixture instances")

    claim: dict[str, object] = {}
    finished_without_claim = False
    takeover: dict[str, object] | None = None
    holder = ""
    holder_evidence = ""
    holder_paused = False
    paused_gateways: list[str] = []
    claim_window_started = time.monotonic()
    stopped_at: float | None = None
    gateway_recovered_at: float | None = None
    try:
        for gateway in gateways:
            stack.pause(gateway)
            paused_gateways.append(gateway)

        sql_run_id = sql_literal(run_id)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            rows = stack.psql_rows(
                "SELECT id,worker_id,generation,state,claim_count,max_claims,policy_hash,"
                "COALESCE(round(EXTRACT(EPOCH FROM (lease_until-clock_timestamp()))::numeric,2),0) "
                f"FROM worker_jobs WHERE run_id={sql_run_id}")
            if rows:
                job_id, worker_id, generation, state, claim_count, max_claims, policy_hash, lease_left = rows[0].split("|")
                if state == "running":
                    claim = {"job_id": job_id, "worker_id": worker_id, "generation": int(generation),
                             "claim_count": int(claim_count), "max_claims": int(max_claims),
                             "policy_hash": policy_hash, "lease_seconds_left": float(lease_left)}
                    break
                if state in ("completed", "partial", "failed", "cancelled"):
                    finished_without_claim = True
                    break
            time.sleep(0.05)
        if not claim:
            if finished_without_claim:
                return "INCONCLUSIVE", {"run_id": run_id, "model_gateway_modes": gateway_modes}, (
                    "the fixture run reached terminal state without a live worker claim, even with the gateway paused")
            raise Failure("no worker claim became visible while the fixture gateway was paused")

        holder = next((instance for instance, hostname in hostnames.items() if hostname == claim["worker_id"]), "")
        holder_evidence = "instance hostname or WORKER_ID matches the claim worker_id"
        if not holder:
            for instance in containers:
                if f"worker {claim['worker_id']} claiming" in stack.instance_logs(instance):
                    holder = instance
                    holder_evidence = "the claiming worker_id appears in this replica's startup log"
                    break
        if not holder:
            raise Failure(f"the claiming worker {claim['worker_id']!r} does not match any running replica")
        stack.pause(holder)
        holder_paused = True
        stopped_at = time.monotonic()

        takeover_deadline = time.monotonic() + 180
        job_sql = sql_literal(str(claim["job_id"]))
        while time.monotonic() < takeover_deadline:
            rows = stack.psql_rows(
                "SELECT worker_id,generation,state,claim_count,max_claims,policy_hash,"
                "round(EXTRACT(EPOCH FROM (ready_at-lease_until))*1000)::int "
                f"FROM worker_jobs WHERE id={job_sql}")
            if rows:
                worker_id, generation, state, count, max_claims, policy_hash, backoff_ms = rows[0].split("|")
                if worker_id != claim["worker_id"] and int(generation) > int(claim["generation"]):
                    takeover = {
                        "worker_id": worker_id, "generation": int(generation), "state": state,
                        "claim_count": int(count), "max_claims": int(max_claims),
                        "policy_hash": policy_hash, "observed_backoff_ms": int(backoff_ms),
                        "seconds_after_holder_pause": round(time.monotonic() - stopped_at, 2),
                    }
                    break
            time.sleep(0.2)
        if takeover is None:
            raise Failure("no other replica reclaimed the paused job within 180s")

        policy_rows = stack.psql_rows(
            "SELECT encode(convert_to(policy_json,'UTF8'),'hex') FROM execution_queue_policies "
            f"WHERE policy_hash={sql_literal(str(takeover['policy_hash']))}")
        if len(policy_rows) != 1:
            raise Failure("the takeover job's immutable queue policy record is missing")
        retry_policy = json.loads(bytes.fromhex(policy_rows[0]).decode("utf-8"))
        takeover["expected_backoff_ms"] = expected_queue_retry_delay_ms(
            str(claim["job_id"]), int(takeover["claim_count"]), retry_policy)
        takeover["backoff_delta_ms"] = int(takeover["observed_backoff_ms"]) - int(takeover["expected_backoff_ms"])

        stack.kill(holder)
        holder_paused = False
        for gateway in reversed(paused_gateways):
            stack.resume(gateway)
            paused_gateways.remove(gateway)
        gateway_recovered_at = time.monotonic()
    finally:
        cleanup_failures: list[str] = []
        if holder_paused and holder:
            try:
                stack.kill(holder)
            except (Failure, OSError, subprocess.SubprocessError) as error:
                cleanup_failures.append(f"holder termination: {error}")
        for gateway in reversed(paused_gateways):
            try:
                stack.resume(gateway)
            except (Failure, OSError, subprocess.SubprocessError) as error:
                cleanup_failures.append(f"gateway resume: {error}")
        try:
            stack.scale({"audit-worker": len(containers)})
            stack.wait_healthy("audit-worker", len(containers), timeout=180)
        except (Failure, OSError, subprocess.SubprocessError) as error:
            cleanup_failures.append(f"worker recovery: {error}")
        if cleanup_failures:
            raise Failure("Worker takeover cleanup failed: " + "; ".join(cleanup_failures))
    if takeover is None:
        raise Failure("Worker takeover did not produce a second fencing generation")

    workspace = client.poll_run(case_id, run_id, "takeover")
    run_state = workspace["run"].get("state")
    findings = workspace.get("findings") or []
    rule_conflicts = sum(1 for finding in findings if finding.get("origin") == "rule" and finding.get("relation") == "conflict")
    events = event_integrity(stack, run_id)
    attempts = stack.psql_rows(
        f"SELECT count(*), count(*) FILTER (WHERE state IN ('completed','partial','failed')) "
        f"FROM review_attempts WHERE run_id='{run_id}'")
    database_findings = int(stack.psql_scalar(f"SELECT count(*) FROM findings WHERE run_id='{run_id}'"))

    stack.scale(context["scale"])
    for service, count in context["scale"].items():
        stack.wait_healthy(service, count)

    stale_status = "NOT_RUN"
    stale_detail = "no stale-generation injection command was configured"
    stale_test_actions: list[str] = []
    stale_exit: int | None = None
    if context["stale_cmd"]:
        environment = dict(os.environ)
        environment.update({key: value for key, value in context["app_env"].items()})
        for key in ("TEST_DATABASE_URL", "MIGRATOR_DATABASE_URL"):
            dsn = environment.get(key, "")
            if not dsn or urlsplit(dsn).path.lstrip("/") != stack.confirm:
                raise Failure(f"{key} must point at the confirmed isolated database for stale-generation proof")
        try:
            command = shlex.split(str(context["stale_cmd"]))
        except ValueError as error:
            raise Failure(f"stale-generation command could not be parsed: {error}") from error
        test_name = "TestStaleGenerationCompletionIsRejected"
        if (len(command) < 3 or Path(command[0]).name != "go" or command[1] != "test"
                or not any(test_name in item for item in command)):
            raise Failure(f"stale-generation command must run the named Go test {test_name}")
        if "-json" not in command[2:]:
            command.insert(2, "-json")
        process = context["recorder"].run(command, timeout=600, env=environment)
        stale_exit = process.returncode
        output = process.stdout.decode("utf-8", "replace") if isinstance(process.stdout, bytes) else str(process.stdout)
        for secret in context["recorder"].secrets:
            output = output.replace(secret, "<redacted>")
        record = context["recorder"].records[-1]
        record["stdout_tail"] = output[-2000:]
        for key in ("stdout_tail", "stderr_tail"):
            for secret in context["recorder"].secrets:
                record[key] = str(record.get(key, "")).replace(secret, "<redacted>")
        for line in output.splitlines():
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if event.get("Test") == test_name:
                stale_test_actions.append(str(event.get("Action", "")))
        passed = process.returncode == 0 and stale_test_actions.count("pass") == 1 and "skip" not in stale_test_actions
        stale_status = "PASS" if passed else "FAIL"
        stale_detail = f"store test exit {process.returncode}; test actions {stale_test_actions!r}"

    measured = {
        "claim": claim, "stopped_container": holder, "holder_evidence": holder_evidence, "takeover": takeover,
        "configured_lease": lease, "configured_heartbeat": heartbeat,
        "model_gateway_service": gateway_service, "model_gateway_modes": gateway_modes,
        "worker_gateway_routes": gateway_routes,
        "gateway_paused_seconds": (round(gateway_recovered_at - claim_window_started, 2)
                                   if gateway_recovered_at is not None else None),
        "queue_retry_policy": retry_policy,
        "run_state": run_state, "findings_api": len(findings), "findings_database": database_findings,
        "rule_conflicts": rule_conflicts, "events": events,
        "attempts_total_terminal": attempts[0],
        "stale_injection": {"status": stale_status, "exit": stale_exit,
                            "test_actions": stale_test_actions, "detail": stale_detail},
    }
    problems: list[str] = []
    if run_state not in journey.TERMINAL_RUN_STATES:
        problems.append(f"the run did not reach a terminal state ({run_state!r})")
    if len(findings) != database_findings:
        problems.append("the API findings and the PostgreSQL findings disagree")
    total, terminal = (int(value) for value in attempts[0].split("|"))
    if total != 1 or terminal != 1:
        problems.append(f"expected exactly one terminal attempt for the run, saw {total} attempts / {terminal} terminal")
    if rule_conflicts < 1:
        problems.append("the required rule finding is missing after recovery")
    if (int(claim.get("claim_count", 0)) != 1 or int(takeover["claim_count"]) != 2
            or int(takeover["generation"]) <= int(claim["generation"])):
        problems.append("the abandoned claim did not advance exactly one fencing generation")
    if abs(int(takeover["backoff_delta_ms"])) > 25:
        problems.append("ready_at did not encode the configured exponential retry delay and stable jitter")
    expected_window = float(claim["lease_seconds_left"]) + int(takeover["expected_backoff_ms"]) / 1000
    if abs(float(takeover["seconds_after_holder_pause"]) - expected_window) > 3:
        problems.append("the observed takeover time did not include the lease and persisted retry backoff")
    if stale_status == "FAIL":
        problems.append(stale_detail)
    if problems:
        return ("BLOCKED_MISSING_HOOK" if stale_status == "NOT_RUN" and len(problems) == 1 else "FAIL"), measured, "; ".join(problems)
    if stale_status == "NOT_RUN":
        return "BLOCKED_MISSING_HOOK", measured, ("takeover and official-result integrity passed, but the "
                                                  "stale-generation submission rejection has no configured injection command")
    return "PASS", measured, "another replica took over after the lease window; one terminal attempt; official events contiguous"


def scenario_sse(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    client = journey.Journey(context["base_url"], time.monotonic() + 300)
    authenticate_client(client, context)
    journey_case = frozen_prefix(journey, client, "sse")
    case_id = str(journey_case["case_id"])
    handle = submit_review(client, case_id)
    run_id = handle["run_id"]
    complete_review(journey, client, case_id, run_id, "sse")
    cookie = cookie_header(client)
    path = f"/api/cases/{case_id}/reviews/{run_id}/events"

    first_events: list[int] = []
    stream = SseStream(context["base_url"], path, cookie)
    try:
        first_events, _ = stream.read(5)
    finally:
        stream.close()
    if not first_events:
        return "FAIL", {"first_events": first_events}, "the event stream did not deliver the persisted events"
    last = max(first_events)

    containers = stack.containers("core-api")
    killed = containers[0] if len(containers) > 1 else ""
    if killed:
        stack.kill(killed)
        stack.scale(context["scale"])
        stack.wait_healthy("core-api", context["scale"]["core-api"], timeout=180)

    for document in journey_case["documents"]:
        client.call("POST", f"/api/cases/{case_id}/documents/{document}/parse", {}, expected={200})
    _, retried, _ = client.call("POST", f"/api/cases/{case_id}/reviews/{run_id}/retry", {}, expected={200, 201, 202})
    retry_run = str(retried.get("id") or run_id)  # type: ignore[union-attr]
    if retry_run != run_id:
        client.poll_run(case_id, retry_run, "sse-retry")

    resumed: list[int] = []
    closed = False
    resumed_stream = SseStream(context["base_url"], path, cookie, last_event_id=last)
    try:
        resumed, closed = resumed_stream.read(15)
    finally:
        resumed_stream.close()
    if not resumed:
        return "FAIL", {"first_events": first_events, "resumed": resumed, "closed": closed}, \
            "no events were delivered after Last-Event-ID"
    if resumed != sorted(resumed) or len(set(resumed)) != len(resumed):
        return "FAIL", {"first_events": first_events, "resumed": resumed}, "the resumed stream is not strictly increasing"
    if min(resumed) <= last:
        return "FAIL", {"first_events": first_events, "resumed": resumed}, "the resumed stream replayed an already consumed event"

    revocation_stream = SseStream(context["base_url"], path, cookie)
    revocation_stream.read(1)
    status, _, _ = client.call("DELETE", "/api/session", expected={200, 204})
    del status
    revoked = revocation_stream.wait_closed(10)
    revocation_stream.close()
    try:
        client.call("GET", "/api/cases", expected={401, 403})
        unauthorized = True
    except journey.Failure:
        unauthorized = False

    measured = {"first_events": len(first_events), "last_event_id": last, "killed_replica": killed,
                "resumed_events": len(resumed), "resumed_min": min(resumed),
                "revocation_closed_stream": revoked, "revoked_session_rejected": unauthorized}
    if not revoked:
        return "FAIL", measured, "the open stream was not closed by session revocation"
    if not unauthorized:
        return "FAIL", measured, "the revoked session still passed authorization"
    return "PASS", measured, "resume continued strictly after Last-Event-ID and revocation closed the live stream"



def scenario_tool_gateway_fault(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    instances = stack.assert_targets_confirmed_database("tool-gateway")
    if len(instances) < 2:
        return "INCONCLUSIVE", {"replicas": len(instances)}, "Tool Gateway fault continuity requires two live replicas"
    client = journey.Journey(context["base_url"], time.monotonic() + 180)
    authenticate_client(client, context)
    cookie = cookie_header(client)
    before = {instance: direct_tool_list(stack, instance, cookie, stack.env_value(instance, "GATEWAY_ROLE_TOKEN"))
              for instance in instances}
    target = instances[-1]
    target_address = stack.instance_addr(target)
    target_credential = stack.env_value(target, "GATEWAY_ROLE_TOKEN")
    target_probe = "NOT_RUN"
    survivor_probes: dict[str, str] = {}
    down = False
    fault_started = time.monotonic()
    recovery_started: float | None = None
    restored: list[str] = []
    after: dict[str, str] = {}
    try:
        stack.stop(target)
        target_probe = direct_tool_list(stack, target, cookie, target_credential, target_address)
        down = target not in stack.containers("tool-gateway")
        survivor_probes = {
            instance: direct_tool_list(stack, instance, cookie, stack.env_value(instance, "GATEWAY_ROLE_TOKEN"))
            for instance in instances if instance != target
        }
    finally:
        recovery_started = time.monotonic()
        stack.scale({"tool-gateway": len(instances)})
        restored = stack.wait_healthy("tool-gateway", len(instances), timeout=180)
    after = {instance: direct_tool_list(stack, instance, cookie, stack.env_value(instance, "GATEWAY_ROLE_TOKEN"))
             for instance in restored}
    measured = {
        "target": target, "replica_count": len(instances), "before": before,
        "target_probe_while_stopped": target_probe, "target_absent_while_stopped": down,
        "survivor_probes": survivor_probes,
        "fault_seconds": round(recovery_started - fault_started, 2),
        "restored_instances": restored, "after_recovery": after,
        "recovery_seconds": round(time.monotonic() - recovery_started, 2),
        "target_replaced": target not in restored if stack.control == "host" else None,
    }
    if not down or target_probe.startswith("http 200"):
        return "FAIL", measured, "the stopped Tool Gateway did not leave the live topology"
    if any(not value.startswith("http 200; tools=") or value.endswith("tools=0") for value in before.values()):
        return "FAIL", measured, "a Tool Gateway replica failed the pre-fault tools/list request"
    if any(not value.startswith("http 200; tools=") or value.endswith("tools=0")
           for value in survivor_probes.values()):
        return "FAIL", measured, "the surviving Tool Gateway replica did not serve tools/list during the fault"
    if len(restored) != len(instances) or any(
            not value.startswith("http 200; tools=") or value.endswith("tools=0") for value in after.values()):
        return "FAIL", measured, "Tool Gateway did not recover the declared replica count and tools/list service"
    if stack.control == "host" and target in restored:
        return "FAIL", measured, "the stopped host Tool Gateway process was not replaced"
    return "PASS", measured, "one Tool Gateway process was stopped; its peer served MCP tools/list and the stopped replica recovered"


def scenario_model_gateway_fault(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    service, gateways = select_model_gateway_service(stack)
    if not service or not gateways:
        return "BLOCKED_MISSING_HOOK", {}, "no controlled Model Gateway is declared"
    gateways = stack.assert_targets_confirmed_database(service)
    modes = {instance: stack.env_value(instance, "MODEL_GATEWAY_MODE") for instance in gateways}
    models = {instance: {"chat": stack.env_value(instance, "LLM_MODEL"),
                         "ocr": stack.env_value(instance, "OCR_MODEL")} for instance in gateways}
    if any(mode != "fixture" for mode in modes.values()):
        return "BLOCKED_MISSING_HOOK", {"modes": modes, "models": models}, (
            "the fault run requires the isolated fixture gateway; real-model execution is not authorized")
    workers = stack.assert_targets_confirmed_database("audit-worker")
    routes_ok, routes = worker_gateway_routes(stack, workers, service, gateways)
    if not routes_ok:
        return "BLOCKED_MISSING_HOOK", {"worker_gateway_routes": routes}, (
            "Workers do not route Model Gateway calls to the controlled instances")

    client = journey.Journey(context["base_url"], time.monotonic() + 900)
    authenticate_client(client, context)
    case_id = str(frozen_prefix(journey, client, "model-gateway-down")["case_id"])
    started = time.monotonic()
    down_instances: list[str] = []
    down_handle: dict[str, object] = {}
    down_workspace: dict[str, object] = {}
    down_usage = ["0|0"]
    down_model_events = ["0|0|0"]
    down_open_leases = "0"
    restored: list[str] = []
    recovery_started = time.monotonic()
    try:
        for gateway in gateways:
            stack.stop(gateway)
        down_instances = stack.containers(service)
        down_handle = submit_review(client, case_id)
        down_workspace = client.poll_run(case_id, str(down_handle["run_id"]), "model-gateway-down")
        down_usage = stack.psql_rows(
            "SELECT count(*),count(*) FILTER(WHERE call_status='failed') FROM model_usage WHERE run_id="
            + sql_literal(str(down_handle["run_id"])))
        down_model_events = stack.psql_rows(
            "SELECT count(*) FILTER(WHERE e.node_kind='model_call' AND e.event_type='model_failed'),"
            "count(*) FILTER(WHERE e.node_kind='model_call' AND e.event_type='model_failed' "
            "AND e.status='failed'),"
            "count(*) FILTER(WHERE e.node_kind='model_call' AND e.event_type='model_completed') "
            "FROM execution_events e JOIN review_attempts a ON a.id=e.attempt_id WHERE a.run_id="
            + sql_literal(str(down_handle["run_id"])))
        down_open_leases = stack.psql_scalar(
            "SELECT count(*) FROM model_call_leases WHERE run_id=" + sql_literal(str(down_handle["run_id"])))
    finally:
        recovery_started = time.monotonic()
        stack.scale({service: len(gateways)})
        restored = stack.wait_healthy(service, len(gateways), timeout=180)
        stack.assert_targets_confirmed_database(service)
    recovery_seconds = round(time.monotonic() - recovery_started, 2)

    down_state = down_workspace["run"].get("state")
    down_findings = down_workspace.get("findings") or []
    down_conflicts = sum(1 for finding in down_findings
                         if finding.get("origin") == "rule" and finding.get("relation") == "conflict")
    down_usage_count, failed_down_calls = (int(value) for value in down_usage[0].split("|"))
    down_failure_events, durable_model_failures, down_completed_events = (
        int(value) for value in down_model_events[0].split("|"))
    recovery_client = journey.Journey(context["base_url"], time.monotonic() + 900)
    authenticate_client(recovery_client, context)
    recovery_case = str(frozen_prefix(journey, recovery_client, "model-gateway-recovered")["case_id"])
    recovery_handle = submit_review(recovery_client, recovery_case)
    recovery_workspace = recovery_client.poll_run(
        recovery_case, str(recovery_handle["run_id"]), "model-gateway-recovered")
    recovered_state = recovery_workspace["run"].get("state")
    recovered_usage = stack.psql_rows(
        "SELECT count(*),count(*) FILTER(WHERE call_status='completed'),"
        "count(*) FILTER(WHERE call_status='failed') FROM model_usage WHERE run_id="
        + sql_literal(str(recovery_handle["run_id"])))
    recovered_open_leases = stack.psql_scalar(
        "SELECT count(*) FROM model_call_leases WHERE run_id=" + sql_literal(str(recovery_handle["run_id"])))
    measured = {
        "gateway_service": service, "gateway_replicas": len(gateways), "gateway_modes": modes,
        "fixture_models": models, "worker_gateway_routes": routes,
        "down_run_id": down_handle["run_id"], "down_run_state": down_state,
        "down_run_rule_conflicts": down_conflicts, "down_run_model_usage": down_usage[0],
        "down_run_model_usage_rows": down_usage_count, "down_run_failed_model_calls": failed_down_calls,
        "down_run_worker_model_failed_events": down_failure_events,
        "down_run_durable_worker_model_failures": durable_model_failures,
        "down_run_worker_model_completed_events": down_completed_events,
        "down_run_open_leases_after_terminal": down_open_leases,
        "gateways_absent_during_run": not down_instances,
        "fault_seconds": round(time.monotonic() - started - recovery_seconds, 2),
        "restored_gateways": restored, "recovery_seconds": recovery_seconds,
        "recovery_run_id": recovery_handle["run_id"], "recovery_run_state": recovered_state,
        "recovery_model_usage": recovered_usage[0], "recovery_open_leases_after_terminal": recovered_open_leases,
    }
    if down_instances:
        return "FAIL", measured, "a Model Gateway instance remained live during the injected outage"
    if down_state != "partial" or down_conflicts < 1:
        return "FAIL", measured, "model outage did not preserve required rule conflicts in a partial run"
    if durable_model_failures < 1:
        return "FAIL", measured, "the Worker did not durably record its failed model request"
    if down_usage_count != 0 or failed_down_calls != 0:
        return "FAIL", measured, "a model call was charged even though every controlled gateway was absent"
    if down_completed_events != 0:
        return "FAIL", measured, "the outage recorded a completed model call while every controlled gateway was absent"
    if int(down_open_leases) != 0:
        return "FAIL", measured, "model outage left an unsettled reservation after the run became terminal"
    if len(restored) != len(gateways) or recovered_state not in journey.TERMINAL_RUN_STATES:
        return "FAIL", measured, "the fixture Model Gateway or a post-recovery review did not recover"
    recovered_rows, completed_calls, failed_calls = (int(value) for value in recovered_usage[0].split("|"))
    if recovered_rows < 1 or completed_calls < 1 or failed_calls != 0 or int(recovered_open_leases) != 0:
        return "FAIL", measured, "the recovered fixture gateway did not settle a real Worker model call"
    return "PASS", measured, "the outage retained a durable Worker failure event and partial rules without billing a call; recovery settled a new fixture call"


def scenario_worker_exporter_fault(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    if stack.control != "host":
        return "BLOCKED_MISSING_HOOK", {}, "the isolated loopback 503 collector needs explicitly controlled host Workers"
    workers = stack.assert_targets_confirmed_database("audit-worker")
    endpoints = {stack.env_value(worker, "OTEL_EXPORTER_OTLP_ENDPOINT") for worker in workers}
    if len(endpoints) != 1:
        raise Failure("Worker replicas do not share one OTLP exporter endpoint setting")
    previous_endpoint = next(iter(endpoints))
    if previous_endpoint:
        context["recorder"].secrets.add(previous_endpoint)

    records: list[dict[str, object]] = []
    record_lock = threading.Lock()

    response_body = b'{"error":"controlled 503"}'
    class CollectorHandler(http.server.BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length)
            with record_lock:
                records.append({"path": self.path, "status": 503, "content_type": self.headers.get("Content-Type", ""),
                                "payload_bytes": len(body), "payload_sha256": hashlib.sha256(body).hexdigest(),
                                "contains_procurement_text": journey.PROCUREMENT_TEXT.encode("utf-8") in body,
                                "contains_draft_text": journey.DRAFT_TEXT.encode("utf-8") in body,
                                "contains_recorded_secret": any(secret.encode("utf-8") in body
                                                                 for secret in context["recorder"].secrets)})
            self.send_response(503)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response_body)))
            self.end_headers()
            self.wfile.write(response_body)

        def log_message(self, format: str, *args) -> None:
            del format, args

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), CollectorHandler)
    server.daemon_threads = True
    endpoint = f"http://127.0.0.1:{server.server_port}"
    server_thread = threading.Thread(target=server.serve_forever, name="otlp-503-collector", daemon=True)
    server_thread.start()
    configured = False
    worker_before = list(workers)
    first_client = journey.Journey(context["base_url"], time.monotonic() + 900)
    case_id = ""
    run_id = ""
    run_state = ""
    official_events: dict[str, object] = {}
    terminal_attempts = ""
    restore_error = ""
    try:
        authenticate_client(first_client, context)
        case_id = str(frozen_prefix(journey, first_client, "otlp-503")["case_id"])
        context["recorder"].note(f"local OTLP collector listening at 127.0.0.1:{server.server_port}, returning HTTP 503")
        stack.configure_host_environment({"audit-worker": {"OTEL_EXPORTER_OTLP_ENDPOINT": endpoint}})
        configured = True
        handle = submit_review(first_client, case_id)
        run_id = str(handle["run_id"])
        workspace = first_client.poll_run(case_id, run_id, "otlp-503")
        run_state = str(workspace["run"].get("state", ""))
        official_events = event_integrity(stack, run_id)
        terminal_attempts = stack.psql_scalar(
            "SELECT count(*) FROM review_attempts WHERE run_id=" + sql_literal(run_id)
            + " AND state IN ('completed','partial','failed')")
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            with record_lock:
                if records:
                    break
            time.sleep(0.2)
    finally:
        if configured:
            try:
                stack.configure_host_environment({
                    "audit-worker": {"OTEL_EXPORTER_OTLP_ENDPOINT": previous_endpoint}})
            except (Failure, OSError, subprocess.SubprocessError) as error:
                restore_error = f"first restore attempt: {error}"
                try:
                    stack.configure_host_environment({
                        "audit-worker": {"OTEL_EXPORTER_OTLP_ENDPOINT": previous_endpoint}})
                except (Failure, OSError, subprocess.SubprocessError) as retry_error:
                    restore_error += f"; retry: {retry_error}"
        server.shutdown()
        server.server_close()
        server_thread.join(timeout=5)

    workers_after = stack.wait_healthy("audit-worker", len(worker_before), timeout=180)
    restored_endpoints = {stack.env_value(worker, "OTEL_EXPORTER_OTLP_ENDPOINT") for worker in workers_after}
    if restored_endpoints != {previous_endpoint}:
        restore_error = (restore_error + "; " if restore_error else "") + "live Worker endpoint differs from its original value"
    with record_lock:
        captured = list(records)
    if restore_error and restored_endpoints != {previous_endpoint}:
        return "FAIL", {"requests": captured, "restore_error": restore_error,
                        "workers_after_reset": workers_after}, "Worker exporter endpoint restoration failed"
    recovery_client = journey.Journey(context["base_url"], time.monotonic() + 900)
    authenticate_client(recovery_client, context)
    recovery_case = str(frozen_prefix(journey, recovery_client, "otlp-reset")["case_id"])
    recovery_handle = submit_review(recovery_client, recovery_case)
    recovery_workspace = recovery_client.poll_run(recovery_case, str(recovery_handle["run_id"]), "otlp-reset")
    recovery_state = recovery_workspace["run"].get("state")
    with record_lock:
        captured = list(records)
    measured = {
        "collector": "127.0.0.1", "response_status": 503, "requests": captured,
        "worker_run_id": run_id, "worker_run_state": run_state,
        "official_event_integrity": official_events, "terminal_attempt_count": terminal_attempts,
        "worker_pids_before": worker_before, "worker_pids_after_reset": workers_after,
        "endpoint_restored": not restore_error, "post_reset_run_state": recovery_state,
        "restore_error": restore_error,
    }
    if restore_error:
        return "FAIL", measured, "the original Worker exporter endpoint could not be restored: " + restore_error
    if not captured or any(record["status"] != 503 or record["path"] != "/v1/traces" for record in captured):
        return "FAIL", measured, "Worker telemetry did not reach the isolated collector and receive HTTP 503"
    if any(record["contains_procurement_text"] or record["contains_draft_text"] or record["contains_recorded_secret"]
           for record in captured):
        return "FAIL", measured, "the exporter payload contained contract text or a recorded secret"
    if run_state not in journey.TERMINAL_RUN_STATES or terminal_attempts != "1":
        return "FAIL", measured, "the exporter 503 prevented durable business state from reaching one terminal attempt"
    if recovery_state not in journey.TERMINAL_RUN_STATES:
        return "FAIL", measured, "a fresh review did not complete after restoring the Worker exporter endpoint"
    return "PASS", measured, "OTLP 503 was observed with no contract text or secret; durable review and post-reset review both reached terminal state"
BROKER_REQUIRED_KEYS = ("pid", "binary_sha256", "argv", "log", "data_dir", "health")


def normalize_broker_target(document: object) -> dict[str, object]:
    """Normalize an explicit broker record or the broker section of control.json."""
    if not isinstance(document, dict):
        raise Failure("the broker target must be a JSON object")
    raw = document.get("broker", document)
    if not isinstance(raw, dict):
        raise Failure("the broker target must contain a broker object")
    target = dict(raw)
    if "binary_sha256" not in target:
        target["binary_sha256"] = target.get("sha256")
    if "health" not in target:
        target["health"] = target.get("monitor_url")
    missing = [key for key in BROKER_REQUIRED_KEYS if key not in target]
    if missing:
        raise Failure(f"the broker target is missing {missing}")
    pid = target["pid"]
    if isinstance(pid, bool) or not isinstance(pid, int) or pid <= 0:
        raise Failure("the broker target must record a positive process id")
    if not isinstance(target["argv"], list) or not target["argv"] or any(
            not isinstance(item, str) or not item or "\0" in item for item in target["argv"]):
        raise Failure("the broker target must record the exact argv")
    target["argv"] = list(target["argv"])
    if not re.fullmatch(r"[0-9a-f]{64}", str(target["binary_sha256"])):
        raise Failure("the broker target must record the executable SHA-256")
    log = Path(str(target["log"]))
    if not log.is_absolute() or not log.is_file():
        raise Failure(f"the broker log {target['log']} is not an existing absolute file")
    data_dir = Path(str(target["data_dir"]))
    if not data_dir.is_absolute() or not data_dir.is_dir():
        raise Failure(f"the broker data directory {target['data_dir']} is not an existing absolute directory")
    health = str(target["health"])
    health_url = urlsplit(health)
    if (health_url.scheme != "http" or health_url.hostname not in ("127.0.0.1", "localhost", "::1")
            or health_url.port is None):
        raise Failure("the broker health URL must use an explicit loopback HTTP address")
    start_argv = target.get("start_argv", target.get("restart_argv"))
    if start_argv is not None:
        if not isinstance(start_argv, list) or not start_argv or any(
                not isinstance(item, str) or not item or "\0" in item for item in start_argv):
            raise Failure("the broker restart argv must be a non-empty string array")
        target["start_argv"] = list(start_argv)
    elif not isinstance(target.get("start_command"), str) or not str(target["start_command"]).strip():
        raise Failure("the broker target must record start_command or restart_argv")
    return target


def load_broker_target(path: Path) -> dict[str, object]:
    """Load an explicit target or the broker section of a control file."""
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise Failure(f"cannot read the broker target file {path}: {error}") from error
    return normalize_broker_target(document)


def load_control_broker_target(path: Path) -> dict[str, object] | None:
    """Use an identity-checked broker declared beside the host service slots."""
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise Failure(f"cannot read the host control file {path}: {error}") from error
    if not isinstance(document, dict):
        raise Failure("the host control file must be a JSON object")
    if "broker" not in document:
        return None
    return normalize_broker_target(document["broker"])


def broker_facts(recorder: Recorder, docker: str, target: dict[str, object]) -> dict[str, object]:
    """Verify the declared host broker: pid, executable hash, argv, data dir, health."""
    pid = int(target["pid"])
    facts: dict[str, object] = {"pid": pid, "binary_sha256": target["binary_sha256"]}
    if not Path(f"/proc/{pid}").exists():
        raise Failure(f"the declared broker pid {pid} is not running")
    try:
        executable = Path(f"/proc/{pid}/exe").resolve()
        actual_hash = hashlib.sha256(executable.read_bytes()).hexdigest()
    except OSError as error:
        raise Failure(f"cannot hash the broker executable: {error}") from error
    facts["executable"] = str(executable)
    facts["executable_sha256"] = actual_hash
    if actual_hash != target["binary_sha256"]:
        raise Failure(f"the running broker executable does not match the recorded SHA-256 ({actual_hash[:12]}…)")
    try:
        raw = Path(f"/proc/{pid}/cmdline").read_bytes()
    except OSError as error:
        raise Failure(f"cannot read the broker command line: {error}") from error
    argv = [item.decode("utf-8", "replace") for item in raw.split(b"\0") if item]
    facts["argv"] = argv
    recorded = [str(item) for item in target["argv"]]
    if argv != recorded:
        raise Failure(f"the running broker argv does not match the recorded argv: {argv!r}")
    data_dir = str(target["data_dir"])
    try:
        environ = Path(f"/proc/{pid}/environ").read_bytes().decode("utf-8", "replace")
    except OSError:
        environ = ""
    if data_dir not in argv and data_dir not in environ:
        raise Failure(f"the running broker does not declare the recorded data dir {data_dir}")
    facts["data_dir"] = data_dir
    status, _, _ = http_get(str(target["health"]))
    facts["health_status"] = status
    if status != 200:
        raise Failure(f"the declared broker health endpoint answered HTTP {status}")
    return facts


def broker_health_up(target: dict[str, object], attempts: int = 30, delay: float = 1.0) -> bool:
    for _ in range(attempts):
        try:
            status, _, _ = http_get(str(target["health"]))
            if status == 200:
                return True
        except (OSError, subprocess.SubprocessError):
            pass
        time.sleep(delay)
    return False


def http_get(url: str, timeout: float = 3) -> tuple[int, str, bytes]:
    request = urllib.request.Request(url, headers={"Accept": "application/json"}, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, "ok", response.read()
    except urllib.error.HTTPError as error:
        return error.code, "http error", error.read()


def start_host_broker(target: dict[str, object], environment: dict[str, str], recorder: Recorder) -> int:
    """Launch the declared broker detached and append its output to the owned log."""
    start_argv = target.get("start_argv")
    command = ([str(item) for item in start_argv] if isinstance(start_argv, list)
               else ["/bin/sh", "-c", str(target["start_command"])])
    started = time.monotonic()
    try:
        with Path(str(target["log"])).open("ab") as log_stream:
            process = subprocess.Popen(command, cwd=ROOT, env=environment, stdin=subprocess.DEVNULL,
                                       stdout=log_stream, stderr=subprocess.STDOUT, start_new_session=True)
    except OSError as error:
        recorder.records.append({"argv": recorder.redact(command), "exit": None,
                                 "seconds": round(time.monotonic() - started, 3),
                                 "stdout_tail": "", "stderr_tail": str(error)[:500]})
        raise Failure(f"could not launch the declared broker: {error}") from error
    recorder.records.append({"argv": recorder.redact(command), "exit": None,
                             "seconds": round(time.monotonic() - started, 3),
                             "stdout_tail": f"started broker launcher pid {process.pid}", "stderr_tail": ""})
    return process.pid


def outbox_delivery_snapshot(stack: Stack, job_id: str) -> dict[str, object]:
    rows = stack.psql_rows(
        "SELECT count(*),count(*) FILTER(WHERE state='pending'),"
        "count(*) FILTER(WHERE state='delivered'),coalesce(string_agg(DISTINCT state,','),''),"
        "count(*) FILTER(WHERE delivered_at IS NOT NULL) FROM outbox_events WHERE job_id="
        + sql_literal(job_id))
    if len(rows) != 1:
        raise Failure(f"outbox delivery query returned an unexpected row set for job {job_id}")
    total, pending, delivered, states, delivered_at = rows[0].split("|", 4)
    return {"total": int(total), "pending": int(pending), "delivered": int(delivered),
            "states": states, "delivered_at_rows": int(delivered_at)}


def wait_for_outbox_delivery(stack: Stack, job_id: str, timeout: float = 120) -> tuple[dict[str, object], float]:
    started = time.monotonic()
    deadline = started + timeout
    latest: dict[str, object] = {"total": 0, "pending": 0, "delivered": 0, "states": "", "delivered_at_rows": 0}
    while time.monotonic() < deadline:
        latest = outbox_delivery_snapshot(stack, job_id)
        if (int(latest["total"]) > 0 and int(latest["pending"]) == 0
                and int(latest["delivered"]) == int(latest["total"])
                and int(latest["delivered_at_rows"]) == int(latest["total"])):
            return latest, round(time.monotonic() - started, 2)
        time.sleep(1)
    return latest, round(time.monotonic() - started, 2)


def scenario_nats_host(context: dict) -> tuple[str, dict[str, object], str]:
    """Stop the declared host broker, prove PG recovery, then restore the broker."""
    journey = context["journey"]
    stack: Stack = context["stack"]
    recorder: Recorder = context["recorder"]
    target = context["nats_broker"]
    facts = broker_facts(recorder, stack.docker, target)
    client = journey.Journey(context["base_url"], time.monotonic() + 300)
    authenticate_client(client, context)
    facts = broker_facts(recorder, stack.docker, target)

    pid = int(target["pid"])
    stopped_at = time.monotonic()
    os.kill(pid, 15)
    recorder.records.append({"argv": ["kill", "-15", str(pid)], "exit": 0, "seconds": 0,
                             "stdout_tail": f"SIGTERM broker {pid}", "stderr_tail": ""})
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline and Path(f"/proc/{pid}").exists():
        time.sleep(0.5)
    broker_down = not Path(f"/proc/{pid}").exists()

    run_down: dict[str, object] = {}
    down_error = ""
    outbox_down: dict[str, object] = {}
    outbox_after_recovery: dict[str, object] = {}
    outbox_recovery_seconds: float | None = None
    handle: dict[str, object] = {}
    recovery_started: float | None = None
    recovery_error = ""
    recovery_launcher_pid: int | None = None
    recovered = False
    recovered_at: float | None = None
    recovered_facts: dict[str, object] = {}
    after_recovery: dict[str, object] = {}
    if broker_down:
        try:
            journey_case = frozen_prefix(journey, client, "nats")
            case_id = str(journey_case["case_id"])
            handle = submit_review(client, case_id)
            try:
                run_down = complete_review(journey, client, case_id, str(handle["run_id"]), "nats-down")
            except journey.Failure as error:
                down_error = str(error)
            if handle.get("job_id"):
                outbox_down = outbox_delivery_snapshot(stack, str(handle["job_id"]))
            else:
                down_error = "the accepted review has no durable job id for outbox recovery proof"
        except (Failure, journey.Failure, OSError, subprocess.SubprocessError, ValueError) as error:
            down_error = str(error)
        finally:
            recovery_started = time.monotonic()
            environment = dict(os.environ)
            environment.update(context["app_env"])
            try:
                recovery_launcher_pid = start_host_broker(target, environment, recorder)
                recovered = broker_health_up(target)
                if recovered:
                    recovered_at = time.monotonic()
                if recovered:
                    recovered_pid = new_broker_pid(target)
                    recovered_facts = broker_facts(recorder, stack.docker, {**target, "pid": recovered_pid})
                    if stack.control_document is not None and isinstance(stack.control_document.get("broker"), dict):
                        stack.control_document["broker"]["pid"] = recovered_pid
                        stack._persist_host_control()
            except (Failure, OSError, subprocess.SubprocessError) as error:
                recovery_error = str(error)
    else:
        down_error = f"the declared broker pid {pid} did not stop within 30s"
        recovered = True
        recovered_facts = facts

    if broker_down and recovered and not recovery_error:
        try:
            if handle.get("job_id"):
                outbox_after_recovery, outbox_recovery_seconds = wait_for_outbox_delivery(
                    stack, str(handle["job_id"]), timeout=120)
            client2 = journey.Journey(context["base_url"], time.monotonic() + 300)
            authenticate_client(client2, context)
            second_case = str(frozen_prefix(journey, client2, "nats-up")["case_id"])
            second = submit_review(client2, second_case)
            after_recovery = complete_review(journey, client2, second_case, str(second["run_id"]), "nats-up")
        except (Failure, journey.Failure, OSError, subprocess.SubprocessError, ValueError) as error:
            recovery_error = f"post-recovery journey failed: {error}"

    broker_down_seconds = (round(recovered_at - stopped_at, 2) if recovered_at is not None
                           else round(time.monotonic() - stopped_at, 2))
    measured = {
        "target": {"pid": pid, "binary_sha256": facts.get("executable_sha256"), "argv": facts.get("argv"),
                   "data_dir": facts.get("data_dir"), "health": str(target["health"]), "log": str(target["log"])},
        "broker_down_observed": broker_down, "broker_down_seconds": broker_down_seconds,
        "run_while_down": run_down, "run_while_down_error": down_error,
        "outbox_during_outage": outbox_down,
        "outbox_after_recovery": outbox_after_recovery,
        "outbox_recovery_seconds": outbox_recovery_seconds,
        "recovery_launcher_pid": recovery_launcher_pid, "recovered": recovered,
        "recovery_seconds": (round(recovered_at - recovery_started, 2)
                             if recovered_at is not None and recovery_started is not None else None),
        "recovery_error": recovery_error, "recovered_facts": recovered_facts,
        "run_after_recovery": after_recovery,
    }
    if not broker_down:
        return "FAIL", measured, down_error
    if down_error or run_down.get("state") not in journey.TERMINAL_RUN_STATES:
        return "FAIL", measured, "the accepted run did not complete while the host broker was stopped: " + down_error
    if int(outbox_down.get("total", 0)) == 0:
        return "FAIL", measured, "the durable outbox rows are missing during the broker outage"
    if (int(outbox_down.get("pending", 0)) != int(outbox_down.get("total", 0))
            or int(outbox_down.get("delivered", 0)) != 0):
        return "FAIL", measured, "outbox notifications were not retained pending while NATS was unavailable"
    if not recovered:
        return "FAIL", measured, "the declared broker did not recover through its recorded start command"
    if recovery_error or recovered_facts.get("verification_error"):
        return "FAIL", measured, recovery_error or str(recovered_facts["verification_error"])
    if int(outbox_after_recovery.get("total", 0)) == 0 or (
            int(outbox_after_recovery.get("delivered", 0)) != int(outbox_after_recovery.get("total", 0))
            or int(outbox_after_recovery.get("delivered_at_rows", 0)) != int(outbox_after_recovery.get("total", 0))):
        return "FAIL", measured, "the restarted JetStream broker did not receive and durably acknowledge the PG outbox backlog"
    if not after_recovery or after_recovery.get("state") not in journey.TERMINAL_RUN_STATES:
        return "FAIL", measured, "a new PG-backed review did not complete after broker recovery"
    return "PASS", measured, "the PG review completed during NATS outage; the relay retained and later acknowledged its outbox event"

def new_broker_pid(target: dict[str, object]) -> int:
    """The recovery command starts a new process; find it by recorded argv."""
    recorded = [str(item) for item in target["argv"]]
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            try:
                raw = (entry / "cmdline").read_bytes()
            except OSError:
                continue
            argv = [item.decode("utf-8", "replace") for item in raw.split(b"\0") if item]
            if argv == recorded:
                return int(entry.name)
        time.sleep(0.5)
    raise Failure("the recovered broker process could not be found by its recorded argv")


def scenario_nats(context: dict) -> tuple[str, dict[str, object], str]:
    if context.get("nats_broker"):
        return scenario_nats_host(context)
    stack: Stack = context["stack"]
    if not stack.containers("nats"):
        return "BLOCKED_MISSING_HOOK", {"nats": "no declared broker target"}, (
            "no host broker target or project nats container was declared; pass --control-file with a broker entry "
            "or --nats-broker-file with the runtime owner's process identity")
    return scenario_nats_container(context)


def scenario_nats_container(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    nats = stack.containers("nats")[0]
    client = journey.Journey(context["base_url"], time.monotonic() + 300)
    authenticate_client(client, context)

    stack.stop(nats)
    stopped_at = time.monotonic()
    run_down: dict[str, object] = {}
    handle: dict[str, object] = {}
    outbox_down: dict[str, object] = {}
    outbox_after_recovery: dict[str, object] = {}
    outbox_recovery_seconds: float | None = None
    down_error = ""
    recovery_error = ""
    recovery_started = time.monotonic()
    recovered_at: float | None = None
    healthy = False
    second_summary: dict[str, object] = {}
    try:
        try:
            journey_case = frozen_prefix(journey, client, "nats")
            case_id = str(journey_case["case_id"])
            handle = submit_review(client, case_id)
            try:
                run_down = complete_review(journey, client, case_id, str(handle["run_id"]), "nats-down")
            except journey.Failure as error:
                down_error = str(error)
            if handle.get("job_id"):
                outbox_down = outbox_delivery_snapshot(stack, str(handle["job_id"]))
            else:
                down_error = "the accepted review has no durable job id for outbox recovery proof"
        except (Failure, journey.Failure, OSError, subprocess.SubprocessError, ValueError) as error:
            down_error = str(error)
    finally:
        recovery_started = time.monotonic()
        try:
            stack.start(nats)
            restarted = stack.compose("up", "-d", "--wait", "--wait-timeout", "120", "nats", timeout=180)
            if restarted.returncode != 0:
                raise Failure(f"Compose could not restart NATS: {restarted.stderr.decode('utf-8', 'replace')[-300:]}")
            healthy = stack.healthy(stack.containers("nats"))
            if healthy:
                recovered_at = time.monotonic()
                if handle.get("job_id"):
                    outbox_after_recovery, outbox_recovery_seconds = wait_for_outbox_delivery(
                        stack, str(handle["job_id"]), timeout=120)
                client2 = journey.Journey(context["base_url"], time.monotonic() + 300)
                authenticate_client(client2, context)
                second_case = str(frozen_prefix(journey, client2, "nats-up")["case_id"])
                second = submit_review(client2, second_case)
                second_summary = complete_review(journey, client2, second_case,
                                                 str(second["run_id"]), "nats-up")
        except (Failure, journey.Failure, OSError, subprocess.SubprocessError, ValueError) as error:
            recovery_error = str(error)

    measured = {
        "nats_container": nats,
        "broker_down_seconds": round((recovered_at or time.monotonic()) - stopped_at, 2),
        "run_while_down": run_down, "run_while_down_error": down_error,
        "outbox_during_outage": outbox_down,
        "outbox_after_recovery": outbox_after_recovery,
        "outbox_recovery_seconds": outbox_recovery_seconds,
        "run_after_restart": second_summary, "nats_restarted_healthy": healthy,
        "recovery_seconds": round(recovered_at - recovery_started, 2) if recovered_at is not None else None,
        "recovery_error": recovery_error,
    }
    if down_error or run_down.get("state") not in journey.TERMINAL_RUN_STATES:
        return "FAIL", measured, "the accepted run did not complete while NATS was stopped: " + down_error
    if int(outbox_down.get("total", 0)) == 0:
        return "FAIL", measured, "the durable outbox rows are missing during the broker outage"
    if (int(outbox_down.get("pending", 0)) != int(outbox_down.get("total", 0))
            or int(outbox_down.get("delivered", 0)) != 0):
        return "FAIL", measured, "outbox notifications were not retained pending while NATS was unavailable"
    if not healthy:
        return "FAIL", measured, recovery_error or "NATS did not return to a healthy state after restart"
    if recovery_error:
        return "FAIL", measured, recovery_error
    if int(outbox_after_recovery.get("total", 0)) == 0 or (
            int(outbox_after_recovery.get("delivered", 0)) != int(outbox_after_recovery.get("total", 0))
            or int(outbox_after_recovery.get("delivered_at_rows", 0)) != int(outbox_after_recovery.get("total", 0))):
        return "FAIL", measured, "the restarted JetStream broker did not receive and durably acknowledge the PG outbox backlog"
    if second_summary.get("state") not in journey.TERMINAL_RUN_STATES:
        return "FAIL", measured, "a new PG-backed review did not complete after NATS recovery"
    return "PASS", measured, "the PG review completed during NATS outage; the relay retained and later acknowledged its outbox event"


class PsqlQuerySession:
    """Reuse one confirmed-DB psql connection for tightly paced lease samples."""

    def __init__(self, stack: Stack) -> None:
        self.stack = stack
        self.process: subprocess.Popen | None = None
        self.started_at: float | None = None

    def _start(self) -> None:
        argv = compose_prefix_for(self.stack.env_path, self.stack.database_target) + [
            "exec", "-T", "db", "psql", "-X", "-qAt", "-F", "|", "-v", "ON_ERROR_STOP=1",
            "-U", self.stack.migrator_user, "-d", self.stack.confirm,
        ]
        self.started_at = time.monotonic()
        try:
            self.process = subprocess.Popen(
                argv, cwd=ROOT, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, bufsize=1)
        except OSError as error:
            raise Failure(f"could not start the bounded quota reservation sampler: {error}") from error

    def query(self, statement: str, timeout: float = 10) -> str:
        if self.process is None:
            self._start()
        process = self.process
        if process is None or process.poll() is not None or process.stdin is None or process.stdout is None:
            raise Failure("the persistent quota reservation sampler stopped unexpectedly")
        process.stdin.write(statement.rstrip().rstrip(";") + ";\n")
        process.stdin.flush()
        readable, _, _ = select.select([process.stdout], [], [], timeout)
        if not readable:
            raise Failure("the persistent quota reservation sampler query exceeded its bound")
        row = process.stdout.readline()
        if not row:
            raise Failure("the persistent quota reservation sampler returned no row")
        return row.rstrip("\r\n")

    def close(self) -> None:
        process = self.process
        if process is None:
            return
        self.process = None
        if process.poll() is None and process.stdin is not None:
            try:
                process.stdin.write("\\q\n")
                process.stdin.flush()
                process.stdin.close()
            except (BrokenPipeError, OSError, ValueError):
                pass
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        stderr = process.stderr.read() if process.stderr is not None else ""
        elapsed = round(time.monotonic() - self.started_at, 3) if self.started_at is not None else 0
        self.stack.recorder.records.append({
            "argv": ["compose", "exec", "-T", "db", "psql", "quota-reservation-sampler"],
            "exit": process.returncode,
            "seconds": elapsed,
            "stdout_tail": "persistent reservation sampling session closed",
            "stderr_tail": self.stack.recorder.redact_text(stderr[-300:]),
        })
        if process.returncode not in (0, None):
            raise Failure("the persistent quota reservation sampler did not exit cleanly")


def scenario_quota_with_delayed_fixture(context: dict) -> tuple[str, dict[str, object], str]:
    """Hold real gateway reservations at an owned loopback provider, never a real model."""
    stack: Stack = context["stack"]
    if stack.control != "host":
        return scenario_quota(context)
    service, gateways = select_model_gateway_service(stack)
    if not gateways or len(stack.slots.get(service, [])) < 2:
        return "BLOCKED_MISSING_HOOK", {}, "quota contention needs two declared Model Gateway slots"
    if any(stack.env_value(instance, "MODEL_GATEWAY_MODE") != "fixture" for instance in gateways):
        return "BLOCKED_MISSING_HOOK", {}, "only the declared local fixture may enter the delayed-provider profile"
    workers = stack.assert_targets_confirmed_database("audit-worker")
    if len(workers) != 2:
        return "BLOCKED_MISSING_HOOK", {}, "quota contention needs the two declared Workers"
    chat_model = stack.env_value(gateways[0], "LLM_MODEL")
    ocr_model = stack.env_value(gateways[0], "OCR_MODEL")
    content = stack.env_value(gateways[0], "MODEL_GATEWAY_FIXTURE_CHAT_CONTENT")
    originals = [(name, slot, str(slot["spec"]["start"]))
                 for name in (service, "audit-worker") for slot in stack.slots[name]]
    stopped = threading.Event()
    provider_calls: list[str] = []
    calls_lock = threading.Lock()

    class FixtureProvider(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args) -> None:
            pass

        def answer(self, status: int, value: object) -> None:
            payload = json.dumps(value, ensure_ascii=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self) -> None:
            if self.path != "/v1/models":
                self.answer(404, {"error": "unknown fixture operation"})
                return
            self.answer(200, {"data": [{"id": chat_model}, {"id": ocr_model}]})

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            if self.path != "/v1/chat/completions" or not 0 < length <= 1 << 20:
                self.answer(400, {"error": "invalid bounded fixture request"})
                return
            request = json.loads(self.rfile.read(length))
            if not isinstance(request, dict) or request.get("model") not in (chat_model, ocr_model):
                self.answer(400, {"error": "unknown fixture model"})
                return
            with calls_lock:
                provider_calls.append(str(request["model"]))
            stopped.wait(5)
            self.answer(200, {"id": "quota-local-fixture", "object": "chat.completion",
                              "model": "local-fixture/" + str(request["model"]),
                              "choices": [{"index": 0, "finish_reason": "stop",
                                           "message": {"role": "assistant", "content": content}}]})

    provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FixtureProvider)
    provider_thread = threading.Thread(target=provider.serve_forever, name="quota-local-fixture", daemon=True)
    provider_thread.start()
    provider_url = f"http://127.0.0.1:{provider.server_address[1]}/v1"

    def restart_slot(name: str, slot: dict, command: str) -> None:
        pid = slot["pid"]
        if pid is not None and stack.alive(int(pid)):
            stack._stop_pid(int(pid))
        slot["pid"] = None
        slot["spec"]["start"] = command
        if slot["group"] == "instances":
            stack._start_slot(name, slot)
            stack._wait_host_slot(slot)
        stack._persist_host_control()

    result: tuple[str, dict[str, object], str] | None = None
    try:
        stack.scale({service: 2})
        stack.configure_host_environment({service: {"MODEL_GATEWAY_MODE": "openai-compatible",
                                                   "LLM_BASE_URL": provider_url, "LLM_API_KEY": ""}})
        replicas = stack.assert_targets_confirmed_database(service)
        for index, slot in enumerate(stack.slots["audit-worker"]):
            if slot["group"] != "instances":
                continue
            address, port = stack.instance_addr(replicas[index % len(replicas)])
            command = stack._start_command_with_environment(str(slot["spec"]["start"]),
                {"MODEL_GATEWAY_URL": f"http://{address}:{port}/v1"})
            restart_slot("audit-worker", slot, command)
        result = scenario_quota({**context, "owned_quota_fixture_url": provider_url})
        with calls_lock:
            calls = list(provider_calls)
        result[1]["delayed_local_provider"] = {
            "data_class": "synthetic fixture only; no real model", "bind_host": "127.0.0.1",
            "response_hold_seconds": 5, "calls": len(calls), "models": sorted(set(calls)),
            "gateway_replicas": len(replicas), "distinct_worker_gateway_routes": 2,
            "application_limits_changed": False,
        }
        if len(calls) != int(context["quota_runs"]) or len(replicas) != 2:
            result = ("FAIL", result[1], "the delayed local provider did not exercise both gateways with all quota runs")
    finally:
        restore_errors = []
        for name, slot, command in originals:
            try:
                restart_slot(name, slot, command)
            except (Failure, OSError) as error:
                restore_errors.append(f"{name}: {error}")
        try:
            stack.scale({service: len(gateways)})
        except (Failure, OSError) as error:
            restore_errors.append(f"{service} replica count: {error}")
        stopped.set()
        provider.shutdown()
        provider.server_close()
        provider_thread.join(timeout=5)
        if restore_errors or provider_thread.is_alive():
            raise Failure("bounded quota profile cleanup failed: " + "; ".join(restore_errors))
    if result is None:
        raise Failure("the delayed local quota workload produced no result")
    result[1]["original_fixture_profile_restored"] = all(
        stack.env_value(instance, "MODEL_GATEWAY_MODE") == "fixture"
        for instance in stack.containers(service))
    if not result[1]["original_fixture_profile_restored"]:
        return "FAIL", result[1], "the quota workload did not restore the original fixture profile"
    return result


def scenario_quota(context: dict) -> tuple[str, dict[str, object], str]:
    journey = context["journey"]
    stack: Stack = context["stack"]
    gateway_service, gateway = select_model_gateway_service(stack)
    if not gateway:
        raise Failure("no model gateway replica is running")
    gateway = stack.assert_targets_confirmed_database(gateway_service)
    limits: dict[str, dict[str, str]] = {
        instance: {
            "tenant": stack.env_value(instance, "MODEL_TENANT_MAX_INFLIGHT"),
            "global": stack.env_value(instance, "MODEL_GLOBAL_MAX_INFLIGHT"),
        }
        for instance in gateway
    }
    if any(not value["tenant"] or not value["global"] for value in limits.values()):
        return "INCONCLUSIVE", {"gateway_replicas": len(gateway), "gateway_limits": limits}, (
            "every model gateway must declare both shared in-flight limits before reservation assertions")
    if len({(value["tenant"], value["global"]) for value in limits.values()}) != 1:
        return "FAIL", {"gateway_replicas": len(gateway), "gateway_limits": limits}, (
            "model gateway replicas have inconsistent in-flight limits")
    first_limits = next(iter(limits.values()))
    tenant_limit, global_limit = int(first_limits["tenant"]), int(first_limits["global"])
    if (tenant_limit, global_limit) != (2, 4):
        return "FAIL", {"tenant_limit": tenant_limit, "global_limit": global_limit}, (
            "the bounded quota profile requires the existing tenant-2/global-4 limits")
    gateway_modes = {instance: stack.env_value(instance, "MODEL_GATEWAY_MODE") for instance in gateway}
    owned_fixture = context.get("owned_quota_fixture_url")
    local_provider = (isinstance(owned_fixture, str) and urlsplit(owned_fixture).hostname == "127.0.0.1"
                      and all(mode == "openai-compatible" and stack.env_value(instance, "LLM_BASE_URL") == owned_fixture
                              for instance, mode in gateway_modes.items()))
    if any(mode != "fixture" for mode in gateway_modes.values()) and not local_provider:
        return "BLOCKED_MISSING_HOOK", {"gateway_modes": gateway_modes}, (
            "quota contention requires the explicitly configured local fixture; real-model calls are not authorized")
    workers = stack.assert_targets_confirmed_database("audit-worker")
    if len(workers) < 2:
        return "INCONCLUSIVE", {"worker_replicas": len(workers)}, (
            "two controlled Worker processes are required for simultaneous target-run reservations")
    routes_ok, worker_routes = worker_gateway_routes(stack, workers, gateway_service, gateway)
    if not routes_ok:
        return "BLOCKED_MISSING_HOOK", {"worker_gateway_routes": worker_routes}, (
            "Workers do not route model calls to the controlled fixture gateway")
    schemas = {stack.env_value(instance, "DATABASE_SCHEMA").strip() or "public" for instance in gateway}
    if len(schemas) != 1:
        return "FAIL", {"gateway_database_schemas": sorted(schemas)}, (
            "Model Gateway replicas do not share one model database schema")
    model_schema = next(iter(schemas))
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", model_schema):
        return "BLOCKED_MISSING_HOOK", {"gateway_database_schema": model_schema}, (
            "the Model Gateway schema is not a safe identifier for the bounded quota profile")
    model_usage_table = f'"{model_schema}"."model_usage"'
    model_leases_table = f'"{model_schema}"."model_call_leases"'
    runs_requested = int(context["quota_runs"])
    if runs_requested < 4 or runs_requested > 24 or runs_requested % 2:
        raise Failure("two-tenant quota contention requires an even number of 4 to 24 concurrent runs")
    setup = context.get("oidc")
    if not setup:
        return "BLOCKED_MISSING_HOOK", {"requested_runs": runs_requested}, (
            "two trusted OIDC tenant identities are required for cross-tenant budget assertions")
    trusted_users = {str(user["id"]): user for user in setup["directory"]["users"]}
    user_ids = ("demo-clerk", "d2-tenant-b-clerk")
    tenant_for_user = {user_id: str(trusted_users[user_id]["tenant_id"]) for user_id in user_ids}
    if len(set(tenant_for_user.values())) != 2:
        raise Failure("the trusted identity directory does not contain two quota tenants")
    active_before = int(stack.psql_scalar(
        "SELECT count(*) FROM worker_jobs WHERE state IN ('queued','running')"))
    if active_before:
        raise Failure(f"quota contention requires an empty active queue; found {active_before} jobs")

    deadline = time.monotonic() + 600
    cases: list[tuple[object, str, str]] = []
    for index in range(runs_requested):
        user_id = user_ids[index % len(user_ids)]
        tenant_id = tenant_for_user[user_id]
        client = journey.Journey(context["base_url"], deadline)
        authenticate_client(client, context, user_id)
        case_id = str(frozen_prefix(journey, client, f"quota-{index}")["case_id"])
        cases.append((client, case_id, tenant_id))

    case_list = ",".join(sql_literal(case_id) for _, case_id, _ in cases)
    tenant_rows = stack.psql_rows(f"SELECT DISTINCT tenant_id FROM cases WHERE id IN ({case_list}) ORDER BY tenant_id")
    tenant_ids = sorted(tenant_for_user.values())
    if tenant_rows != tenant_ids:
        raise Failure(f"quota run cases did not resolve to the two trusted tenants: {tenant_rows!r}")

    samples: list[dict[str, object]] = []
    run_ids: set[str] = set()
    sample_interval_seconds = 0.002
    sample_limit = 30000
    sample_errors: list[str] = []
    shared_lock = threading.Lock()
    stop = threading.Event()
    sample_started = time.monotonic()

    sampler_ready = threading.Event()

    def sample() -> None:
        tenant_counts_sql = ", ".join(
            "count(*) FILTER (WHERE tenant_id=" + sql_literal(tenant) + ")" for tenant in tenant_ids)
        target_predicate = f"case_id IN ({case_list})"
        target_tenant_counts_sql = ", ".join(
            "count(*) FILTER (WHERE tenant_id=" + sql_literal(tenant) + f" AND {target_predicate})"
            for tenant in tenant_ids)
        query = (
            "SELECT " + tenant_counts_sql + ", " + target_tenant_counts_sql + ", "
            f"count(*) FILTER (WHERE {target_predicate}), count(*) "
            f"FROM {model_leases_table} WHERE expires_at > clock_timestamp()"
        )
        session = PsqlQuerySession(stack)
        try:
            if session.query("SELECT 1", timeout=15) != "1":
                raise Failure("the persistent quota reservation sampler failed its connection check")
            sampler_ready.set()
            while not stop.is_set():
                with shared_lock:
                    if len(samples) >= sample_limit:
                        raise Failure("the persistent quota sampler reached its bounded sample limit")
                row = session.query(query, timeout=10)
                values = [int(value) for value in row.split("|")]
                count = len(tenant_ids)
                if len(values) != count * 2 + 2:
                    raise Failure("the persistent quota reservation sampler returned an unexpected row shape")
                tenant_counts = dict(zip(tenant_ids, values[:count]))
                target_tenant_counts = dict(zip(tenant_ids, values[count:count * 2]))
                target_count, global_count = values[-2:]
                with shared_lock:
                    samples.append({
                        "elapsed_seconds": round(time.monotonic() - sample_started, 4),
                        "tenant_inflight_by_id": tenant_counts,
                        "target_tenant_inflight_by_id": target_tenant_counts,
                        "tenant_inflight": max(tenant_counts.values(), default=0),
                        "target_inflight": target_count,
                        "global_inflight": global_count,
                    })
                stop.wait(sample_interval_seconds)
        except (Failure, IndexError, ValueError) as error:
            with shared_lock:
                sample_errors.append(str(error))
        finally:
            sampler_ready.set()
            try:
                session.close()
            except Failure as error:
                with shared_lock:
                    sample_errors.append(str(error))

    sampler = threading.Thread(target=sample, name="quota-reservation-sampler", daemon=True)
    handles: list[tuple[object, str, str, dict]] = []
    completion_errors: list[str] = []
    queued_snapshot: dict[str, int] = {}

    def submit_one(client, case_id: str, tenant_id: str) -> tuple[object, str, str, dict]:
        handle = submit_review(client, case_id)
        run_id = str(handle.get("run_id", ""))
        sql_literal(run_id)
        with shared_lock:
            run_ids.add(run_id)
        return client, case_id, tenant_id, handle

    worker_stop_started = False
    sampler_started = False
    queued_before_resume = False
    restored_workers: list[str] = []
    worker_routes_after: dict[str, object] = {}
    quota_profile: dict[str, object] = {
        "method": "stop the two existing Workers, queue the requested runs, then restart the same bounded replica count",
        "worker_replicas_before": len(workers),
        "sampler": "persistent psql session with 2ms target pacing and exact target-tenant lease counts",
        "sampler_interval_target_seconds": sample_interval_seconds,
        "sampler_sample_limit": sample_limit,
        "application_limits_changed": False,
    }

    def restore_workers() -> None:
        nonlocal worker_stop_started, restored_workers, worker_routes_after
        stack.scale({"audit-worker": len(workers)})
        restored_workers = stack.wait_healthy("audit-worker", len(workers), timeout=180)
        stack.assert_targets_confirmed_database("audit-worker")
        restored_routes_ok, worker_routes_after = worker_gateway_routes(
            stack, restored_workers, gateway_service, gateway)
        if not restored_routes_ok:
            raise Failure("restored quota Workers do not retain their fixture-gateway route")
        worker_stop_started = False

    try:
        worker_stop_started = True
        for worker in workers:
            stack.stop(worker)
        if stack.containers("audit-worker"):
            raise Failure("a Worker remained live while quota runs were being queued")

        with concurrent.futures.ThreadPoolExecutor(max_workers=min(8, len(cases))) as executor:
            submissions = [executor.submit(submit_one, client, case_id, tenant_id)
                           for client, case_id, tenant_id in cases]
            for future in submissions:
                try:
                    handles.append(future.result())
                except Exception as error:
                    completion_errors.append(f"review submission: {error}")
        if len(handles) != runs_requested:
            completion_errors.append("quota profile did not accept the full requested run count")

        if run_ids:
            runs_sql = ",".join(sql_literal(run_id) for run_id in sorted(run_ids))
            queued = stack.psql_rows(
                "SELECT count(*) FILTER(WHERE state='queued'),count(*),count(DISTINCT tenant_id) "
                f"FROM worker_jobs WHERE run_id IN ({runs_sql})")
            queued_count, queue_count, queue_tenants = (int(value) for value in queued[0].split("|"))
            queued_snapshot = {"queued": queued_count, "total": queue_count, "distinct_tenants": queue_tenants}
            queued_before_resume = (queued_count == len(handles) == runs_requested
                                    and queue_count == len(handles) and queue_tenants == 2)
            if not queued_before_resume:
                completion_errors.append("quota runs were not all durably queued for the two trusted tenants")

        if handles:
            sampler.start()
            sampler_started = True
            if not sampler_ready.wait(15):
                completion_errors.append("persistent quota sampler did not become ready within 15 seconds")
            with shared_lock:
                if sample_errors:
                    completion_errors.append("persistent quota sampler failed before Worker restart")

        try:
            restore_workers()
        except Failure as error:
            completion_errors.append(f"quota Worker restart: {error}")
            try:
                restore_workers()
            except Failure as retry_error:
                completion_errors.append(f"quota Worker restart retry: {retry_error}")

        if handles and not worker_stop_started:
            with concurrent.futures.ThreadPoolExecutor(max_workers=min(8, len(handles))) as executor:
                completions = {
                    executor.submit(complete_review, journey, client, case_id, handle["run_id"],
                                   f"quota-{case_id[-4:]}"): case_id
                    for client, case_id, _, handle in handles
                }
                for future, case_id in completions.items():
                    try:
                        future.result()
                    except Exception as error:
                        completion_errors.append(f"run {case_id}: {error}")
    finally:
        if worker_stop_started:
            try:
                restore_workers()
            except Failure as error:
                completion_errors.append(f"quota Worker cleanup: {error}")
        if sampler_started:
            stop.set()
            sampler.join(timeout=10)
    quota_profile["queued_before_resume"] = queued_before_resume
    quota_profile["queued_snapshot"] = queued_snapshot
    quota_profile["worker_replicas_after"] = len(restored_workers)
    sample_times = [float(sample["elapsed_seconds"]) for sample in samples]
    quota_profile["maximum_sample_gap_seconds"] = max(
        (later - earlier for earlier, later in zip(sample_times, sample_times[1:])), default=None)
    quota_profile["restored_same_worker_count"] = len(restored_workers) == len(workers)
    quota_profile["worker_gateway_routes_after"] = worker_routes_after

    selected_runs = sorted(run_ids)
    if not selected_runs:
        return "FAIL", {"case_count": len(cases), "submission_failures": completion_errors,
                         "quota_contention_profile": quota_profile}, (
            "no quota workload review was accepted")
    runs_sql = ",".join(sql_literal(run_id) for run_id in selected_runs)
    target_usage = stack.psql_rows(
        "SELECT count(*), count(DISTINCT lease_id), "
        "count(*) FILTER (WHERE input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL), "
        "count(*) FILTER (WHERE input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL "
        "AND NOT budget_released AND charged_tokens=reserved_input_tokens+reserved_output_tokens), "
        "count(*) FILTER (WHERE call_status='failed') "
        f"FROM {model_usage_table} WHERE run_id IN ({runs_sql})")
    open_leases = stack.psql_rows(
        f"SELECT count(*) FROM {model_leases_table} WHERE run_id IN ({runs_sql})")
    duplicates = stack.psql_rows(
        f"SELECT count(*) FROM (SELECT lease_id FROM {model_usage_table} "
        f"WHERE run_id IN ({runs_sql}) GROUP BY lease_id HAVING count(*) > 1) d")
    queue_rows = stack.psql_rows(
        "SELECT j.tenant_id,j.run_id,coalesce(j.worker_id,''),j.state, "
        "CASE WHEN a.started_at IS NULL THEN 'NULL' ELSE "
        "round(EXTRACT(EPOCH FROM (a.started_at::timestamptz-a.accepted_at::timestamptz))::numeric,3)::text END "
        "FROM worker_jobs j JOIN review_attempts a ON a.id=j.attempt_id "
        f"WHERE j.run_id IN ({runs_sql}) ORDER BY a.accepted_at,j.run_id")
    peaks_by_tenant = {
        tenant: max((int(sample["tenant_inflight_by_id"].get(tenant, 0)) for sample in samples), default=0)
        for tenant in tenant_ids
    }
    target_peaks_by_tenant = {
        tenant: max((int(sample["target_tenant_inflight_by_id"].get(tenant, 0)) for sample in samples), default=0)
        for tenant in tenant_ids
    }
    peaks = {
        "tenant_inflight": max(peaks_by_tenant.values(), default=0),
        "tenant_inflight_by_id": peaks_by_tenant,
        "target_tenant_inflight_by_id": target_peaks_by_tenant,
        "target_inflight": max((int(sample["target_inflight"]) for sample in samples), default=0),
        "global_inflight": max((int(sample["global_inflight"]) for sample in samples), default=0),
        "simultaneous_tenants_with_leases": max(
            (sum(1 for value in sample["tenant_inflight_by_id"].values() if int(value) > 0)
             for sample in samples), default=0),
        "simultaneous_target_tenants_with_leases": max(
            (sum(1 for value in sample["target_tenant_inflight_by_id"].values() if int(value) > 0)
             for sample in samples), default=0),
    }
    usage_by_tenant = stack.psql_rows(
        f"SELECT tenant_id,count(*) FROM {model_usage_table} WHERE run_id IN ({runs_sql}) "
        "GROUP BY tenant_id ORDER BY tenant_id")
    usage_count, unique_leases, unknown_usage, unknown_charged_at_reservation, failed_calls = (
        target_usage[0].split("|")
    )
    measured = {
        "requested_runs": runs_requested,
        "accepted_runs": len(handles),
        "requested_runs_by_tenant": {tenant: sum(1 for _, _, item_tenant in cases if item_tenant == tenant)
                                      for tenant in tenant_ids},
        "gateway_replicas": len(gateway),
        "gateway_limits": limits,
        "gateway_modes": gateway_modes,
        "worker_replicas": len(workers),
        "worker_gateway_routes": worker_routes,
        "quota_contention_profile": quota_profile,
        "tenant_ids": tenant_ids,
        "tenant_limit": tenant_limit,
        "global_limit": global_limit,
        "reservation_samples": samples,
        "peak_reservations": peaks,
        "sampler_errors": sample_errors,
        "submission_or_completion_errors": completion_errors,
        "model_usage_rows": usage_count,
        "model_usage_by_tenant": usage_by_tenant,
        "distinct_usage_leases": unique_leases,
        "unknown_usage_rows": unknown_usage,
        "unknown_usage_charged_at_full_reservation": unknown_charged_at_reservation,
        "failed_model_calls": failed_calls,
        "open_target_leases_after_load": open_leases[0],
        "duplicate_settlements": duplicates[0],
        "worker_queue_rows": queue_rows,
        "fairness": "measured in the separate queue-fairness scenario against the persisted weighted schedule",
        "worker_retry_backoff": "measured in the worker-takeover scenario against ready_at and the persisted policy",
    }
    if sampler.is_alive():
        return "FAIL", measured, "reservation sampler did not stop after the workload"
    if sample_errors or completion_errors or len(handles) != runs_requested:
        return "FAIL", measured, "quota workload or reservation sampling had errors"
    if not samples or len(queue_rows) != len(handles):
        return "FAIL", measured, "reservation or durable queue-wait evidence is incomplete"
    if any(any(int(value) > tenant_limit for value in sample["tenant_inflight_by_id"].values())
           or int(sample["global_inflight"]) > global_limit for sample in samples):
        return "FAIL", measured, "sampled reservations exceeded a tenant or shared global in-flight limit"
    if int(open_leases[0]) != 0:
        return "FAIL", measured, "the completed quota runs retained unsettled model-call reservations"
    if int(duplicates[0]) != 0 or int(usage_count) != int(unique_leases):
        return "FAIL", measured, "model call reservations did not settle one-to-one"
    if int(unknown_usage) == 0 or int(unknown_charged_at_reservation) == 0:
        return "FAIL", measured, "the fixture did not preserve null usage with the full reservation charged"
    if peaks["target_inflight"] < 2:
        return "INCONCLUSIVE", measured, "the workload did not expose two simultaneous target-run reservations"
    if peaks["simultaneous_target_tenants_with_leases"] < 2:
        return "INCONCLUSIVE", measured, "the workload did not expose target-run reservations for both tenants at the same time"
    return "PASS", measured, ("two tenant workloads stayed within each tenant and shared global reservation limits, "
                              "settled every lease once, preserved null usage at its reserved upper bound, "
                              "and recorded durable queue wait")


SCENARIOS = (
    ("baseline-single-replica", scenario_baseline),
    ("dual-replicas-and-session-sharing", scenario_dual_replicas),
    ("owner-scope-and-forced-rls", scenario_owner_scope),
    ("weighted-two-tenant-queue-fairness", scenario_queue_fairness),
    ("core-replica-fault", scenario_core_fault),
    ("worker-takeover-generation-backoff", scenario_worker_takeover),
    ("tool-gateway-replica-fault", scenario_tool_gateway_fault),
    ("model-gateway-fault-recovery", scenario_model_gateway_fault),
    ("worker-otlp-503-isolation", scenario_worker_exporter_fault),
    ("sse-resume-and-revocation", scenario_sse),
    ("nats-disconnect", scenario_nats),
    ("two-tenant-quota-not-multiplied", scenario_quota_with_delayed_fixture),
)


def parse_scale(raw: str) -> dict[str, int]:
    counts: dict[str, int] = {}
    for item in raw.split(","):
        name, _, value = item.partition("=")
        name = name.strip()
        if name not in ("core-api", "audit-worker", "tool-gateway"):
            raise Failure(f"unknown service in --scale: {name!r}")
        try:
            count = int(value)
        except ValueError as error:
            raise Failure(f"invalid replica count {item!r}") from error
        if count < 1 or count > 4:
            raise Failure(f"replica count out of the supported range: {item!r}")
        counts[name] = count
    return counts


def select_scenarios(names: list[str] | None, scenarios=SCENARIOS):
    selected = set(names or [name for name, _ in scenarios])
    unknown = selected - {name for name, _ in scenarios}
    if unknown:
        raise ValueError(f"unknown scenario name(s): {', '.join(sorted(unknown))}")
    return [(name, function) for name, function in scenarios if name in selected]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", default=str(DISTRIBUTED_ENV_PATH), help="private dotenv profile of the isolated stack")
    parser.add_argument("--confirm", default="", help="the isolated database every stack service must target")
    parser.add_argument("--database-target", choices=("development", "test"), default="development",
                        help="which isolated database target every stack service must use")
    parser.add_argument("--prerequisites", default=str(ROOT / "artifacts" / "d2-8" / "prerequisites.json"))
    parser.add_argument("--artifact-dir", default="")
    parser.add_argument("--scale", default="core-api=2,audit-worker=2,tool-gateway=2")
    parser.add_argument("--control", choices=("compose", "host"), default="compose",
                        help="how the three scaled services are controlled: compose containers or the runtime owner's host processes")
    parser.add_argument("--control-file", default="",
                        help="host control: JSON naming each instance's pid, start command, log file and health URL")
    parser.add_argument("--quota-runs", type=int, default=6)
    parser.add_argument("--oidc-env-file", default="",
                        help="private host.env with trusted identity directory and client credentials")
    parser.add_argument("--nats-broker-file", default="",
                        help="host broker target JSON: pid/binary_sha256/argv/log/data_dir/health/start_command")
    parser.add_argument("--stale-injection-cmd", default="",
                        help="override the default stale-generation rejection command (A's store test)")
    parser.add_argument("--plan", action="store_true", help="evaluate the gate and print the plan only")
    parser.add_argument("--run", action="store_true", help="execute the gated scenarios")
    parser.add_argument("--scenario", action="append", dest="selected_scenarios",
                        help="run only this named scenario; repeat to select several (default: all)")
    parser.add_argument("--print-prerequisites-template", action="store_true",
                        help="print the six-item evidence skeleton bound to the current source identity")
    parser.add_argument("--print-control-template", action="store_true",
                        help="print the host control-file skeleton (pid, start, log, health per instance)")
    args = parser.parse_args(argv)

    try:
        selected_scenarios = select_scenarios(args.selected_scenarios)
    except ValueError as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2

    journey = load_journey()
    if args.print_control_template:
        template = {"services": {
            service: {
                "instances": [
                    {"pid": 0, "start": f"<exact command that (re)starts one {service} instance with the profile env>",
                     "log": f"<absolute log path of this {service} instance>",
                     "health": f"<http://127.0.0.1:<port>/<ready path> for this instance>"},
                ],
                "additional": [
                    {"start": f"<exact command for a second {service} instance if the run should scale up>",
                     "log": f"<absolute log path of the second {service} instance>",
                     "health": f"<http://127.0.0.1:<port>/<ready path> for the second instance>"},
                ],
            }
            for service in ("core-api", "audit-worker", "tool-gateway")
        }}
        print(json.dumps(template, ensure_ascii=False, indent=2, sort_keys=True))
        return 0
    if args.print_prerequisites_template:
        identity = source_identity_module().source_identity(ROOT)
        skeleton = {
            "source_identity": {"manifest": "artifacts/d2-8/source_identity.json", "digest": identity["digest"]},
            "items": {key: {"status": "NOT_RUN", "evidence": [f"<path or commit id for: {description}>"]}
                      for key, description in ITEM_DEFINITIONS.items()},
            "hooks": {"stale_generation_injection_command": ""},
        }
        print(json.dumps(skeleton, ensure_ascii=False, indent=2, sort_keys=True))
        return 0
    env_path = Path(args.env)
    if not args.plan and not args.run:
        args.plan = True
    if not args.confirm:
        print("REFUSED: pass --confirm <database> naming the isolated test database", file=sys.stderr)
        return 2
    if not env_path.is_file():
        print(f"REFUSED: missing private distributed profile at {env_path}", file=sys.stderr)
        return 2
    if env_path.read_text(encoding="utf-8").lstrip().startswith("{"):
        print("REFUSED: --env must be the dotenv profile compose consumes; the flat JSON secret file is not enough",
              file=sys.stderr)
        return 2

    if args.run and not args.oidc_env_file:
        print("REFUSED: distributed fault runs require --oidc-env-file and real trusted OIDC identities",
              file=sys.stderr)
        return 2
    delivery_root = Path(os.environ.get("CAW_DELIVERY_DIR") or (ROOT.parent / "contract-audit-delivery"))
    artifact_dir = Path(args.artifact_dir) if args.artifact_dir else (
        delivery_root / "fault-runs" /
        (dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S") + "_" + secrets.token_hex(3)))
    try:
        artifact_dir.mkdir(parents=True)
    except FileExistsError:
        print(f"REFUSED: artifact directory already exists; refusing to overwrite it: {artifact_dir}", file=sys.stderr)
        return 2
    except OSError as error:
        print(f"REFUSED: cannot create artifact directory {artifact_dir}: {error}", file=sys.stderr)
        return 2

    try:
        infra, app = journey.require_isolated_stack(env_path, args.confirm, args.database_target)
    except journey.Failure as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2
    recorder = Recorder(artifact_dir / "commands.jsonl", set(infra.values()) | set(app.values()))
    oidc_setup: dict[str, object] | None = None
    if args.oidc_env_file:
        try:
            oidc_setup = load_oidc_setup(Path(args.oidc_env_file), recorder)
        except (Failure, OSError) as error:
            print(f"REFUSED: {error}", file=sys.stderr)
            recorder.flush()
            return 2
    try:
        stack = Stack(recorder, env_path, args.confirm, app, args.control,
                      Path(args.control_file) if args.control_file else None, args.database_target)
    except Failure as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2
    scale = parse_scale(args.scale)
    nats_broker: dict[str, object] | None = None
    nats_broker_source = "not declared"
    try:
        if args.nats_broker_file:
            nats_broker = load_broker_target(Path(args.nats_broker_file))
            nats_broker_source = f"--nats-broker-file:{args.nats_broker_file}"
        elif args.control_file:
            nats_broker = load_control_broker_target(Path(args.control_file))
            if nats_broker:
                nats_broker_source = f"{args.control_file}#broker"
    except Failure as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2
    if args.quota_runs < 4 or args.quota_runs > 24 or args.quota_runs % 2:
        print("REFUSED: --quota-runs must be an even number between 4 and 24 for two tenants", file=sys.stderr)
        return 2

    document, prerequisite_failures = load_prerequisite_records(Path(args.prerequisites))
    hook_command = str(document.get("hooks", {}).get("stale_generation_injection_command", ""))
    stale_cmd = args.stale_injection_cmd or hook_command or DEFAULT_STALE_INJECTION_COMMAND
    stale_source = ("cli" if args.stale_injection_cmd else
                    "prerequisites hook" if hook_command else "A-provided default store test")
    summary: dict[str, object] = {
        "harness": "d2-8",
        "created_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "confirm_database": args.confirm,
        "env": str(env_path),
        "control": {"mode": args.control, "file": args.control_file},
        "scale": scale,
        "stale_generation_injection": {"command": stale_cmd, "source": stale_source},
        "nats_broker_target": nats_broker_source,
        "prerequisites": {"file": str(args.prerequisites), "failures": prerequisite_failures},
        "scenarios_planned": [name for name, _ in SCENARIOS],
    }
    print(json.dumps({"mode": "plan" if args.plan and not args.run else "run",
                      "prerequisites_failures": prerequisite_failures,
                      "scenarios": summary["scenarios_planned"]}, ensure_ascii=False, sort_keys=True))
    if args.plan and not args.run:
        (artifact_dir / "summary.json").write_text(
            json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        recorder.flush()
        return 3 if prerequisite_failures else 0
    if prerequisite_failures:
        print("NOT_RUN: the frozen six prerequisites are not evidenced for the current source identity",
              file=sys.stderr)
        (artifact_dir / "summary.json").write_text(
            json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        recorder.flush()
        return 3

    if not shutil.which(os.environ.get("DOCKER_BIN", "docker")):
        print("REFUSED: Docker CLI is unavailable", file=sys.stderr)
        return 2
    port = app.get("EDGE_PORT") or infra.get("EDGE_PORT") or "8080"
    base_url = str(oidc_setup["origin"])
    identity_keys = ("OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_ADMIN_CLIENT_ID",
                     "OIDC_ADMIN_CLIENT_SECRET", "OIDC_REDIRECT_URL", "OIDC_STATE_KEY")
    directory_environment = {"IDENTITY_DIRECTORY_FILE": str(oidc_setup["env"]["IDENTITY_DIRECTORY_FILE"])}
    identity_environment = {key: str(oidc_setup["env"][key]) for key in identity_keys}
    model_gateway_service, _ = select_model_gateway_service(stack)
    identity_services = ("core-api", "document-service", "audit-execution", "audit-worker",
                         model_gateway_service, "tool-gateway")
    try:
        if args.control == "host":
            service_environment = {service: dict(directory_environment) for service in identity_services}
            service_environment["core-api"].update(identity_environment)
            identity_rollout = stack.configure_host_environment(service_environment)
        else:
            measured_services: dict[str, list[str]] = {}
            directory_paths: dict[str, str] = {}
            for service in identity_services:
                instances = stack.containers(service)
                if not instances:
                    raise Failure(f"identity-dependent Compose service {service} has no running instance")
                measured_services[service] = instances
                for instance in instances:
                    path = stack.env_value(instance, "IDENTITY_DIRECTORY_FILE")
                    if not path or not Path(path).is_absolute():
                        raise Failure(f"Compose {service} instance {instance} lacks an absolute identity directory path")
                    directory_paths[instance] = path
                    if service == "core-api":
                        mismatches = [key for key, value in identity_environment.items()
                                      if stack.env_value(instance, key) != value]
                        if mismatches:
                            raise Failure(f"Compose Core instance {instance} lacks matching OIDC settings: "
                                          + ", ".join(mismatches))
            if len(set(directory_paths.values())) != 1:
                raise Failure("business services do not share one consistent identity directory path")
            identity_rollout = {"verified_services": measured_services,
                                "container_directory_paths": directory_paths, "already_configured": True}
        summary["trusted_identity_rollout"] = {"status": "PASS", "measured": identity_rollout}
    except (Failure, OSError, subprocess.SubprocessError, ValueError) as error:
        summary["trusted_identity_rollout"] = {"status": "NOT_RUN", "detail": str(error)}
        print(f"NOT_RUN: trusted identity runtime is not ready: {error}", file=sys.stderr)
        (artifact_dir / "summary.json").write_text(
            json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        recorder.flush()
        return 3
    live_failures: list[str] = []
    live_measured: dict[str, object] = {}
    try:
        live_failures, live_measured = live_prerequisites(
            stack, journey, base_url, {"oidc": oidc_setup, "recorder": recorder})
    except (Failure, journey.Failure, OSError, subprocess.SubprocessError) as error:
        live_failures = [f"live prerequisite check could not run: {error}"]
    summary["live_prerequisites"] = {"failures": live_failures, "measured": live_measured}
    if live_failures:
        print(json.dumps({"status": "NOT_RUN", "live_prerequisites_failures": live_failures},
                         ensure_ascii=False, sort_keys=True))
        (artifact_dir / "summary.json").write_text(
            json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        recorder.flush()
        return 3
    if nats_broker:
        try:
            broker_preflight = broker_facts(recorder, stack.docker, nats_broker)
        except (Failure, OSError, subprocess.SubprocessError) as error:
            summary["nats_broker_preflight"] = {"status": "NOT_RUN", "detail": str(error)}
            print(f"NOT_RUN: the declared broker identity could not be verified: {error}", file=sys.stderr)
            (artifact_dir / "summary.json").write_text(
                json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
            recorder.flush()
            return 3
        summary["nats_broker_preflight"] = {"status": "PASS", "measured": broker_preflight}

    context = {"journey": journey, "stack": stack, "base_url": base_url, "scale": scale,
               "nats_broker": nats_broker, "oidc": oidc_setup,
               "stale_cmd": stale_cmd, "app_env": app, "recorder": recorder,
               "quota_runs": args.quota_runs}
    results = []
    for name, function in selected_scenarios:
        service_scale_at_start = dict(scale)
        started = time.monotonic()
        try:
            status, measured, detail = function(context)
        except (Failure, journey.Failure, OSError, subprocess.SubprocessError, ValueError) as error:
            status, measured, detail = "FAIL", {}, str(error)
        results.append({"name": name, "status": status, "seconds": round(time.monotonic() - started, 2),
                        "measured": measured, "detail": detail, "scale": service_scale_at_start})
        print(json.dumps({"scenario": name, "status": status, "detail": detail}, ensure_ascii=False, sort_keys=True))

    failures = [result for result in results if result["status"] == "FAIL"]
    blocked = [result for result in results if result["status"] not in ("PASS", "FAIL")]
    by_name = {result["name"]: result["status"] for result in results}
    summary["results"] = results
    summary["nats_broker_target"] = (
        {"source": nats_broker_source, "pid": nats_broker.get("pid"),
          "binary_sha256": nats_broker.get("binary_sha256"), "health": nats_broker.get("health")}
        if nats_broker else "NOT_DECLARED (no broker entry in --control-file, --nats-broker-file or project)")
    summary["capabilities"] = {
        "PROCESS_RECOVERY": by_name.get("core-replica-fault", "NOT_RUN"),
        "MULTI_REPLICA": by_name.get("dual-replicas-and-session-sharing", "NOT_RUN"),
        "OWNER_SCOPE_AND_FORCED_RLS": by_name.get("owner-scope-and-forced-rls", "NOT_RUN"),
        "WEIGHTED_TENANT_QUEUE_FAIRNESS": by_name.get("weighted-two-tenant-queue-fairness", "NOT_RUN"),
        "WORKER_TAKEOVER_AND_FENCING": by_name.get("worker-takeover-generation-backoff", "NOT_RUN"),
        "TOOL_GATEWAY_RECOVERY": by_name.get("tool-gateway-replica-fault", "NOT_RUN"),
        "MODEL_GATEWAY_RECOVERY": by_name.get("model-gateway-fault-recovery", "NOT_RUN"),
        "WORKER_EXPORTER_503_ISOLATION": by_name.get("worker-otlp-503-isolation", "NOT_RUN"),
        "SSE_RESUME_AND_REVOCATION": by_name.get("sse-resume-and-revocation", "NOT_RUN"),
        "NATS_DISCONNECT_RECOVERY": by_name.get("nats-disconnect", "NOT_RUN"),
        "SHARED_QUOTA": by_name.get("two-tenant-quota-not-multiplied", "NOT_RUN"),
        "BACKUP_RESTORE": "NOT_RUN (run deployments/scripts/restore-local.py)",
        "MULTI_NODE_HA": "NOT_RUN (single host; no cluster, PDB or node loss)",
        "DISASTER_RECOVERY": "NOT_RUN (no cross-failure-domain)",
        "HPA": "SPEC_ONLY (no metric producer)",
    }
    summary["missing_hooks"] = [
        "The harness records unique startup instance identities and probes each replica directly; per-request process "
        "attribution still requires a metrics endpoint or response header.",
        "The NATS scenario proves durable PostgreSQL outbox retention and broker-acknowledged replay after recovery; "
        "it does not claim exactly-once notification consumption, because PostgreSQL remains the worker authority.",
        "A's store test (TestStaleGenerationCompletionIsRejected) is the default stale-generation injection; "
        "the harness requires its explicit Go test pass event, so a skipped or unmatched test cannot pass.",
    ]
    (artifact_dir / "summary.json").write_text(
        json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    recorder.flush()
    print(json.dumps({"status": "FAIL" if failures else ("NOT_RUN" if blocked else "PASS"),
                      "failures": [result["name"] for result in failures],
                      "not_passed": [result["name"] for result in blocked],
                      "artifact_dir": str(artifact_dir)}, ensure_ascii=False, sort_keys=True))
    return 1 if failures else (3 if blocked else 0)


if __name__ == "__main__":
    raise SystemExit(main())

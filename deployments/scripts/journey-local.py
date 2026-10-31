#!/usr/bin/env python3
"""Run the frozen distributed journey through the edge against an isolated stack.

The script never talks to PostgreSQL directly for business writes: every business
step goes through the edge gateway, exactly like the browser client, so the run
exercises edge routing, per-service credentials, the independent document
service, the execution service that accepts a persistent task, and a separate
worker that performs the attempt. PostgreSQL is read only through `psql` inside
the compose database container to make the durable backlog visible.

Isolation is mandatory and checked before any request: every service credential
must target the selected isolated development or test database, and the operator
must confirm its exact name. The journey refuses the default demo database.
"""
from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPCookieProcessor, Request, build_opener

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from deployments.local import DATABASE_TARGETS, DISTRIBUTED_ENV_PATH, ENV_PATH, compose_prefix, database_name, read_env  # noqa: E402

TERMINAL_RUN_STATES = {"completed", "awaiting_human", "partial", "failed"}
REQUIRED_DATABASES = (
    "CORE_DATABASE_URL",
    "DOCUMENT_DATABASE_URL",
    "EXECUTION_DATABASE_URL",
    "MODEL_DATABASE_URL",
    "TOOL_DATABASE_URL",
    "MIGRATOR_DATABASE_URL",
)
PROCUREMENT_TEXT = "采购文件要求质保36个月。"
DRAFT_TEXT = "拟签合同约定质保24个月。"


class Failure(RuntimeError):
    """A journey step did not produce the result the frozen contract requires."""


class Journey:
    """Minimal cookie-aware client for the edge."""

    def __init__(self, base: str, deadline: float) -> None:
        self.base = base
        parsed = urlsplit(base)
        self.origin = f"{parsed.scheme}://{parsed.netloc}"
        self.deadline = deadline
        self.opener = build_opener(HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(
        self,
        method: str,
        path: str,
        payload: object | None = None,
        *,
        body: bytes | None = None,
        content_type: str = "application/json",
        expected: set[int],
    ) -> tuple[int, object | None, bytes]:
        if time.monotonic() > self.deadline:
            raise Failure(f"journey deadline exceeded before {method} {path}")
        data = body
        headers = {"Accept": "application/json", "Origin": self.origin}
        if payload is not None:
            data = json.dumps(payload).encode("utf-8")
        if data is not None:
            headers["Content-Type"] = content_type
        request = Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                status, raw = response.status, response.read()
        except HTTPError as error:
            status, raw = error.code, error.read()
        except (OSError, URLError) as error:
            raise Failure(f"{method} {path} could not reach the edge: {error}") from error
        if status not in expected:
            raise Failure(f"{method} {path} returned HTTP {status}: {raw[:400]!r}")
        if not raw:
            return status, None, raw
        try:
            return status, json.loads(raw), raw
        except json.JSONDecodeError as error:
            raise Failure(f"{method} {path} answered non-JSON: {raw[:200]!r}") from error

    def upload(self, case_id: str, role: str, filename: str, media: str, text: str) -> str:
        boundary = "journey" + secrets.token_hex(12)
        body = b"".join([
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="role"\r\n\r\n{role}\r\n'.encode(),
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="file"; filename="{filename}"\r\n'.encode(),
            f"Content-Type: {media}\r\n\r\n".encode(),
            text.encode("utf-8"),
            f"\r\n--{boundary}--\r\n".encode(),
        ])
        _, document, _ = self.call(
            "POST",
            f"/api/cases/{case_id}/documents",
            body=body,
            content_type=f"multipart/form-data; boundary={boundary}",
            expected={201},
        )
        if not isinstance(document, dict) or not document.get("id"):
            raise Failure("document upload did not return an id")
        return str(document["id"])

    def poll_run(self, case_id: str, run_id: str, label: str) -> dict:
        while True:
            if time.monotonic() > self.deadline:
                raise Failure(f"{label}: run {run_id} did not reach a terminal state in time")
            _, workspace, _ = self.call(
                "GET", f"/api/cases/{case_id}/reviews/{run_id}", expected={200}
            )
            if not isinstance(workspace, dict) or not isinstance(workspace.get("run"), dict):
                raise Failure(f"{label}: workspace has no run view")
            state = workspace["run"].get("state")
            if state in TERMINAL_RUN_STATES:
                return workspace
            time.sleep(2)


def require_isolated_stack(env_path: Path, confirmation: str,
					   database_target: str) -> tuple[dict[str, str], dict[str, str]]:
	"""Require every service credential to use the confirmed isolated database."""
	if not confirmation:
		raise Failure("Pass --confirm <database> naming the isolated database the journey may write to")
	database_key = DATABASE_TARGETS.get(database_target)
	if not database_key:
		raise Failure("database target must be development or test")
	infra = read_env(ENV_PATH)
	if infra.get("APP_ENV", "").strip().lower() == "production":
		raise Failure("refusing to run the journey with APP_ENV=production")
	if not env_path.exists():
		raise Failure(f"missing private distributed profile at {env_path}; pass --env after `make app-bootstrap`")
	app = read_env(env_path)
	configured_database = database_name(infra, database_key)
	if confirmation != configured_database:
		raise Failure(f"refusing to run: --confirm must match this private {database_target} database {configured_database!r}")
	if confirmation in ("contract_audit_dev", "contract_audit_test", "contract_audit_prod", "postgres"):
		raise Failure(f"refusing to run against {confirmation!r}")
	for key in REQUIRED_DATABASES:
		dsn = app.get(key, "")
		if not dsn:
			raise Failure(f"{key} is missing from {env_path}")
		database = urlsplit(dsn).path.lstrip("/")
		if database != configured_database:
			raise Failure(f"refusing to run: {key} targets {database!r}, not the confirmed {configured_database!r}")
	return infra, app


def require_running_stack() -> None:
    docker = os.environ.get("DOCKER_BIN", "docker")
    if not shutil.which(docker):
        raise Failure("Docker CLI is unavailable; the isolated stack must be running")
    output = subprocess.run(
        compose_prefix() + ["ps", "-q", "gateway"], cwd=ROOT, text=True, capture_output=True
    )
    if output.returncode != 0 or not output.stdout.strip():
        raise Failure("the gateway container is not running; start the stack with make apps-up-fixture")


def counts(app: dict[str, str], tables: tuple[str, ...]) -> dict[str, int]:
    dsn = urlsplit(app["MIGRATOR_DATABASE_URL"])
    database = dsn.path.lstrip("/")
    user = dsn.username or "contractaudit"
    query = " UNION ALL ".join(
        f"SELECT '{table}', count(*) FROM {table}" for table in tables
    )
    command = compose_prefix() + [
        "exec", "-T", "db", "psql", "-U", user, "-d", database, "-t", "-A", "-F", "|",
        "-v", "ON_ERROR_STOP=1", "-c", query,
    ]
    output = subprocess.run(command, cwd=ROOT, text=True, capture_output=True)
    if output.returncode != 0:
        raise Failure(f"psql sampling failed: {output.stderr.strip()}")
    sampled: dict[str, int] = {}
    for line in output.stdout.splitlines():
        name, _, value = line.strip().partition("|")
        if name and value:
            sampled[name] = int(value)
    return sampled


def run_journey(smoke: bool, env_path: Path, confirmation: str, database_target: str) -> int:
    infra, app = require_isolated_stack(env_path, confirmation, database_target)
    require_running_stack()
    port = app.get("EDGE_PORT") or infra.get("EDGE_PORT") or "8080"
    journey = Journey(f"http://127.0.0.1:{port}", time.monotonic() + (120 if smoke else 300))

    journey.call("GET", "/health", expected={200})
    journey.call("GET", "/ready", expected={200})
    _, session, _ = journey.call(
        "POST",
        "/api/session",
        {"username": "demo-admin", "password": "admin-demo", "audience": "client"},
        expected={200},
    )
    if not isinstance(session, dict) or session.get("audience") != "client":
        raise Failure(f"session did not return the client audience: {session!r}")

    _, case, _ = journey.call(
        "POST",
        "/api/cases",
        {"title": "分布式旅程-质保", "contract_type": "procurement_ict"},
        expected={201},
    )
    case_id = str(case["id"])  # type: ignore[index]

    procurement = journey.upload(case_id, "procurement", "purchase.txt", "text/plain", PROCUREMENT_TEXT)
    draft = journey.upload(case_id, "draft_contract", "draft.txt", "text/plain", DRAFT_TEXT)
    for document in (procurement, draft):
        journey.call("POST", f"/api/cases/{case_id}/documents/{document}/parse", {}, expected={200})

    status, review, _ = journey.call(
        "POST", f"/api/cases/{case_id}/reviews", {}, expected={200, 201, 202}
    )
    run_id = str(review["id"])  # type: ignore[index]
    job_id = str(review.get("job_id") or "")  # type: ignore[union-attr]
    if status == 202 and not job_id:
        raise Failure("a durable acceptance must return the persistent job id")

    workspace = journey.poll_run(case_id, run_id, "review")
    run = workspace["run"]
    if run.get("state") == "failed":
        raise Failure(f"review failed: {run.get('failure_message')!r}")
    findings = workspace.get("findings") or []
    rule_conflicts = [
        finding for finding in findings
        if finding.get("origin") == "rule" and finding.get("relation") == "conflict"
    ]
    if not rule_conflicts:
        raise Failure(f"deterministic rule findings are missing: {findings!r}")
    located = sum(
        1 for finding in findings for anchor in finding.get("evidence") or []
        if anchor.get("status") == "located"
    )
    documents = workspace.get("documents") or []

    execution = workspace.get("execution")
    attempt = execution.get("attempt") if isinstance(execution, dict) else None
    if not isinstance(attempt, dict) or not attempt.get("id"):
        raise Failure("terminal review workspace has no persisted attempt identity")
    attempt_state = attempt.get("state")
    if attempt_state not in TERMINAL_RUN_STATES:
        raise Failure(f"terminal review workspace has non-terminal attempt state: {attempt_state!r}")
    agent_review = execution.get("agent_review")

    backlog = counts(app, ("worker_jobs", "review_attempts", "outbox_events", "audit_events"))
    summary = {
        "case_id": case_id,
        "run_id": run_id,
        "attempt_id": str(attempt["id"]),
        "attempt_state": attempt_state,
        "job_id": job_id,
        "acceptance_status": status,
        "run_state": run.get("state"),
        "partial_reason": run.get("partial_reason"),
        "model_ref": run.get("model_ref"),
        "agent_review_present": agent_review is not None,
        "documents": len(documents),
        "findings": len(findings),
        "rule_conflicts": len(rule_conflicts),
        "located_anchors": located,
        "backlog": backlog,
        "smoke": smoke,
    }

    if not smoke:
        for document in (procurement, draft):
            journey.call("POST", f"/api/cases/{case_id}/documents/{document}/parse", {}, expected={200})
        status, retried, _ = journey.call(
            "POST", f"/api/cases/{case_id}/reviews/{run_id}/retry", {}, expected={200, 201, 202}
        )
        retry_run = str(retried.get("id") or run_id)  # type: ignore[union-attr]
        if status in (200, 201, 202) and retry_run != run_id:
            journey.poll_run(case_id, retry_run, "retry")
        summary["retry_status"] = status
        summary["retry_run_id"] = retry_run
        summary["retried_parse"] = True

    print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
    print(f"PASS: distributed journey via edge (smoke={smoke})")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--smoke", action="store_true", help="Fast path: no reparse/retry steps")
    parser.add_argument(
        "--env", default=str(DISTRIBUTED_ENV_PATH),
        help="Private distributed profile to read (default: .env.distributed; a private path such as /tmp/... is allowed)",
    )
    parser.add_argument("--database-target", choices=("development", "test"), default="development",
                        help="Isolated database named by --confirm")
    parser.add_argument(
        "--confirm", default="",
        help="The isolated database every service in --env must target; the journey refuses anything else",
    )
    args = parser.parse_args(argv)
    try:
        return run_journey(args.smoke, Path(args.env), args.confirm, args.database_target)
    except (Failure, OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

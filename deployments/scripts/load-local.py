#!/usr/bin/env python3
"""Bounded local load measurement for the three frozen telecom scenarios (D2-7e).

Approved shape:
- the frozen dataset testdata/telecom-c1b-v1.json is the single input and its
  recorded SHA-256 is verified before anything runs;
- default shape is concurrency 1 and 2, three repeats per scenario, so at most
  18 new cases; every repetition creates its own case and never overwrites,
  resets or deletes seed or user data;
- the workload mirrors scripts/seed_telecom.py: generation from the dataset
  fields, every material plus the scenario draft_override (uploaded as
  draft_contract), explicit parses, review acceptance and polling until the run
  leaves the pending states; the three expected_relations are read back from the
  workspace evaluations keyed by rule_id, so a repetition cannot silently drift
  away from the intended scenario;
- polling is authoritative: a run that reaches awaiting_human has finished its
  machine phase, so the poll stops on the pending-state set instead of waiting
  for a human decision;
- every HTTP request is measured exactly once (one wrapper around the existing
  Journey.call, which also covers multipart uploads); sleeps and loop overhead
  are never counted as request latency;
- PostgreSQL accepted/started/finished timestamps give queue wait and
  completion statistics; throughput, refused/failed/partial counts and
  nearest-rank p50/p95/p99 are reported overall and per concurrency level,
  scenario and level x scenario, with the sample count and the raw rows;
- retry and model-call usage counts separate known and unknown usage (unknown
  tokens and cost are null, never zero);
- hardware, kernel, runtime, model and dataset identity are recorded; the load
  runs only when a controlled model gateway proves MODEL_GATEWAY_MODE=fixture
  with the frozen fixture model identities; CPU/RSS of the controlled processes
  are sampled from /proc (host control) or docker stats (compose control) and
  are null when a sample cannot be read.

Nothing is started or restarted here and no metrics service, schema or
framework is added. PostgreSQL is only read, through the same `compose exec db
psql` pattern the other local scripts use. `--plan` runs the guards, verifies
the dataset and prints the plan without any HTTP or database call; real load
evidence exists only after `--run` against a handed-over environment.

Small n is reported as small n: these numbers are local single-host samples, not
production capacity, p95 or RPO/RTO claims.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import threading
import time

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from deployments.local import DISTRIBUTED_ENV_PATH  # noqa: E402

DEFAULT_DATASET = ROOT / "testdata" / "telecom-c1b-v1.json"
FIXTURE_CHAT_MODEL = "fixture-chat"
FIXTURE_OCR_MODEL = "fixture-ocr"
IMAGE_CASE_CAP = 36
# Same pending set as scripts/seed_telecom.py; startup asserts the modules agree.
# A run outside this set has finished its machine phase, so awaiting_human counts
# as finished and is never waited on as a human decision.
PENDING_STATES = {"accepted", "queued", "running"}
FINAL_STATES = {"completed", "partial", "awaiting_human", "failed"}
CLOCK_TICKS = os.sysconf("SC_CLK_TCK") if hasattr(os, "sysconf") else 100


class Failure(RuntimeError):
    """A guard, measurement or parsing step did not hold."""


def load_module(name: str, relative: str):
    spec = importlib.util.spec_from_file_location(name, ROOT / relative)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module




def parse_int_list(raw: str, *, minimum: int, maximum: int, label: str) -> list[int]:
    values: list[int] = []
    for item in raw.split(","):
        item = item.strip()
        if not item:
            continue
        if not item.isdigit():
            raise Failure(f"{label} must be a comma-separated list of integers")
        value = int(item)
        if value < minimum or value > maximum:
            raise Failure(f"{label} value {value} is outside {minimum}..{maximum}")
        values.append(value)
    if not values:
        raise Failure(f"{label} must not be empty")
    return sorted(set(values))


def proc_environ(pid: int) -> dict[str, str]:
    try:
        raw = Path(f"/proc/{pid}/environ").read_bytes()
    except OSError as error:
        raise Failure(f"cannot read the environment of pid {pid}: {error}") from error
    values: dict[str, str] = {}
    for entry in raw.split(b"\0"):
        key, separator, value = entry.decode("utf-8", "replace").partition("=")
        if separator and key:
            values[key] = value
    return values


def control_instances(path: Path) -> list[dict[str, object]]:
    """Every service instance the control file declares, with its pid and spec."""
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise Failure(f"cannot read the control file {path}: {error}") from error
    services = document.get("services", document)
    if not isinstance(services, dict):
        raise Failure("the control file must contain a services object")
    instances: list[dict[str, object]] = []
    for service, spec in services.items():
        if not isinstance(spec, dict):
            continue
        for instance in spec.get("instances", []):
            if isinstance(instance, dict) and int(instance.get("pid", 0)) > 0:
                instances.append({"service": service, "pid": int(instance["pid"]), "spec": instance})
    if not instances:
        raise Failure("the control file declares no running instance")
    return instances


def proc_cpu_ticks(pid: int) -> int | None:
    try:
        stat = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
    except OSError:
        return None
    close = stat.rfind(")")
    if close < 0:
        return None
    fields = stat[close + 2:].split()
    if len(fields) < 13:
        return None
    try:
        return int(fields[11]) + int(fields[12])
    except ValueError:
        return None


def proc_rss_bytes(pid: int) -> int | None:
    try:
        status = Path(f"/proc/{pid}/status").read_text(encoding="utf-8")
    except OSError:
        return None
    for line in status.splitlines():
        if line.startswith("VmRSS:"):
            parts = line.split()
            if len(parts) >= 2 and parts[1].isdigit():
                return int(parts[1]) * 1024
    return None


def container_stats(recorder, containers: list[str]) -> dict[str, tuple[float | None, int | None]]:
    """CPU percent and RSS bytes per container through the existing docker CLI.

    docker stats prints short ids, so results are keyed by both the short id and
    the full id of every requested container. An unreadable sample stays null
    instead of silently becoming zero.
    """
    if not containers:
        return {}
    process = recorder.run(
        ["docker", "stats", "--no-stream", "--format", "{{.ID}}|{{.CPUPerc}}|{{.MemUsage}}"] + containers,
        timeout=120)
    if process.returncode != 0:
        return {}
    parsed: dict[str, tuple[float | None, int | None]] = {}
    for line in process.stdout.decode("utf-8", "replace").splitlines():
        fields = line.split("|")
        if len(fields) != 3:
            continue
        identifier, cpu, memory = (field.strip() for field in fields)
        percent: float | None = None
        match = re.fullmatch(r"([0-9.]+)%", cpu)
        if match:
            try:
                percent = float(match.group(1))
            except ValueError:
                percent = None
        rss: int | None = None
        memory_match = re.match(r"([0-9.]+)\s*([KMGT]?i?B)", memory)
        if memory_match:
            factors = {"B": 1, "KB": 1000, "MB": 1000 ** 2, "GB": 1000 ** 3, "TB": 1000 ** 4,
                       "KiB": 1024, "MiB": 1024 ** 2, "GiB": 1024 ** 3, "TiB": 1024 ** 4}
            rss = int(float(memory_match.group(1)) * factors[memory_match.group(2)])
        parsed[identifier] = (percent, rss)
        for container in containers:
            if container.startswith(identifier):
                parsed[container] = (percent, rss)
    return parsed


class Sampler(threading.Thread):
    """Samples CPU/RSS of the controlled processes while the load runs."""

    def __init__(self, recorder, interval: float, targets: list[dict[str, object]]) -> None:
        super().__init__(daemon=True)
        self.recorder = recorder
        self.interval = interval
        self.targets = targets
        self.samples: list[dict[str, object]] = []
        self.stop_event = threading.Event()

    def run(self) -> None:
        previous: dict[int, tuple[int, float]] = {}
        while not self.stop_event.is_set():
            now = time.monotonic()
            for target in self.targets:
                if target["kind"] == "pid":
                    pid = int(target["id"])
                    ticks = proc_cpu_ticks(pid)
                    cpu: float | None = None
                    if ticks is not None and pid in previous:
                        last_ticks, last_time = previous[pid]
                        elapsed = now - last_time
                        if elapsed > 0:
                            cpu = round((ticks - last_ticks) / CLOCK_TICKS / elapsed * 100, 2)
                    if ticks is not None:
                        previous[pid] = (ticks, now)
                    self.samples.append({"at": dt.datetime.now(dt.timezone.utc).isoformat(),
                                         "service": target["service"], "id": str(pid),
                                         "cpu_percent": cpu, "rss_bytes": proc_rss_bytes(pid)})
                else:
                    percent, rss = container_stats(self.recorder, [str(target["id"])]).get(
                        str(target["id"]), (None, None))
                    self.samples.append({"at": dt.datetime.now(dt.timezone.utc).isoformat(),
                                         "service": target["service"], "id": str(target["id"]),
                                         "cpu_percent": percent, "rss_bytes": rss})
            self.stop_event.wait(self.interval)


def nearest_rank(values: list[float], fraction: float) -> float | None:
    """Nearest-rank percentile, the same method used by the decision comparison."""
    if not values:
        return None
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, int(-(-fraction * len(ordered) // 1)) - 1))
    return round(ordered[index], 6)


def stats(values: list[float]) -> dict[str, object]:
    return {"n": len(values), "p50": nearest_rank(values, 0.5),
            "p95": nearest_rank(values, 0.95), "p99": nearest_rank(values, 0.99)}


def parse_stamp(value: str) -> dt.datetime | None:
    if not value:
        return None
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def request_kind(method: str, path: str) -> str:
    if path == "/api/session":
        return "session"
    if path == "/api/cases" and method == "POST":
        return "case"
    if path.startswith("/api/cases/") and method == "GET" and path.endswith("/reviews/") is False and "/reviews/" not in path:
        return "case-read"
    if path.endswith("/generations"):
        return "generation"
    if path.endswith("/documents"):
        return "upload"
    if path.endswith("/parse"):
        return "parse"
    if re.search(r"/reviews/[^/]+$", path) and method == "GET":
        return "poll"
    if path.endswith("/reviews"):
        return "review-accept"
    if path.endswith("/templates"):
        return "templates"
    return "request"


def make_measured_journey(journey, rows: list[dict[str, object]], context: dict[str, object]):
    """One measured subclass so every actual request is recorded exactly once."""

    class MeasuredJourney(journey.Journey):
        def call(self, method, path, payload=None, *, body=None, content_type="application/json",
                 expected=frozenset({200})):
            started = time.monotonic()
            try:
                status, parsed, raw = super().call(method, path, payload, body=body,
                                                   content_type=content_type, expected=expected)
            except Exception as error:
                code = 0
                match = re.search(r"returned HTTP (\d{3})", str(error))
                if match:
                    code = int(match.group(1))
                rows.append({**context, "kind": request_kind(method, path), "method": method, "path": path,
                             "status": code, "seconds": round(time.monotonic() - started, 6),
                             "error": str(error)[:200]})
                raise
            rows.append({**context, "kind": request_kind(method, path), "method": method, "path": path,
                         "status": status, "seconds": round(time.monotonic() - started, 6)})
            return status, parsed, raw

    return MeasuredJourney


def await_review(client, case_id: str, run_id: str, deadline: float) -> dict:
    """Poll until the run leaves the pending states; awaiting_human is finished."""
    while True:
        _, workspace, _ = client.call("GET", f"/api/cases/{case_id}/reviews/{run_id}", expected={200})
        state = str((workspace or {}).get("run", {}).get("state", ""))
        if state not in PENDING_STATES:
            return workspace
        if time.monotonic() > deadline:
            raise Failure(f"review {run_id} remained {state!r} beyond the repetition deadline")


def observed_relations(scenario: dict, workspace: dict) -> tuple[dict[str, str | None], bool]:
    """Read the three expected rule relations from the workspace evaluations."""
    evaluations = workspace.get("evaluations", [])
    observed: dict[str, str | None] = {}
    match = True
    for rule_id, expected in scenario["expected_relations"].items():
        matches = [item for item in evaluations if item.get("rule_id") == rule_id]
        relation = matches[0].get("relation") if len(matches) == 1 else None
        observed[rule_id] = relation
        if relation != expected:
            match = False
    return observed, match


def run_repetition(journey, base_url: str, dataset: dict, scenario: dict, level: int, repeat: int,
                   timeout: float, rows: list[dict[str, object]], authenticate) -> dict[str, object]:
    """One create-to-machine-finished repetition following the seed workflow."""
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%S") + "-" + secrets.token_hex(3)
    context = {"scenario": scenario["id"], "level": level, "repeat": repeat}
    record: dict[str, object] = {**context, "title": f"{scenario['title']} · load {stamp}",
                                 "terminal": False, "run_state": "", "attempt_state": "",
                                 "relations_match": False, "refused": 0, "case_id": "", "run_id": "",
                                 "job_id": "", "attempt_id": "", "accepted_client_ts": "", "terminal_client_ts": ""}
    deadline = time.monotonic() + timeout
    client = make_measured_journey(journey, rows, context)(base_url, deadline)
    try:
        authenticate(client)
        _, case, _ = client.call("POST", "/api/cases",
                                 {"title": record["title"], "contract_type": "service",
                                  "business_line": "ENT", "scenario": "telecom_dedicated_line"},
                                 expected={201})
        case_id = str(case["id"])
        record["case_id"] = case_id
        _, generation, _ = client.call("POST", f"/api/cases/{case_id}/generations",
                                       {"template_id": dataset["template_id"], "fields": scenario["fields"]},
                                       expected={201})
        record["generation_id"] = str((generation or {}).get("id", ""))
        documents = []
        for material in scenario["materials"]:
            documents.append(client.upload(case_id, material["role"], material["filename"], "text/plain",
                                           material["text"]))
        override = scenario.get("draft_override")
        if override:
            documents.append(client.upload(case_id, "draft_contract", override["filename"], "text/plain",
                                           override["text"]))
            record["draft_override_uploaded"] = True
        else:
            record["draft_override_uploaded"] = False
        for document in documents:
            client.call("POST", f"/api/cases/{case_id}/documents/{document}/parse", {}, expected={200})
        status, review, _ = client.call("POST", f"/api/cases/{case_id}/reviews", {}, expected={200, 201, 202})
        record["accepted_client_ts"] = dt.datetime.now(dt.timezone.utc).isoformat()
        if status == 202 and not (review or {}).get("job_id"):
            raise Failure("durable review acceptance returned HTTP 202 without a persistent job id")
        run_id = str((review or {})["id"])
        record["run_id"] = run_id
        record["job_id"] = str((review or {}).get("job_id") or "")
        workspace = await_review(client, case_id, run_id, deadline)
        record["terminal_client_ts"] = dt.datetime.now(dt.timezone.utc).isoformat()
        run = workspace.get("run", {})
        execution = workspace.get("execution") or {}
        attempt = execution.get("attempt") or {}
        record["run_state"] = str(run.get("state", ""))
        record["attempt_state"] = str(attempt.get("state", ""))
        record["attempt_id"] = str((review or {}).get("attempt_id") or attempt.get("id") or "")
        record["terminal"] = record["run_state"] in FINAL_STATES
        findings = workspace.get("findings") or []
        record["findings"] = len(findings)
        observed, match = observed_relations(scenario, workspace)
        record["observed_relations"] = observed
        record["relations_match"] = match
    except Exception as error:  # keep every created id and timing, even on failure
        record["error"] = str(error)
    record["refused"] = sum(1 for row in rows if int(row["status"]) >= 400)
    record["transport_errors"] = sum(1 for row in rows if int(row["status"]) == 0)
    record["requests"] = len(rows)
    return record

def measured_repetition(journey, base_url: str, dataset: dict, scenario: dict, level: int, repeat: int,
                        timeout: float, authenticate) -> tuple[dict[str, object], list[dict[str, object]]]:
    request_rows: list[dict[str, object]] = []
    job = run_repetition(journey, base_url, dataset, scenario, level, repeat, timeout, request_rows, authenticate)
    return job, request_rows


def id_list(values: list[str]) -> str:
    for value in values:
        if not re.fullmatch(r"[A-Za-z0-9._:-]{1,128}", value):
            raise Failure(f"unexpected identifier shape in measurement: {value!r}")
    return ",".join(quote(value) for value in values)


def postgres_pass(stack, run_ids: list[str], case_ids: list[str]):
    """Read accepted/started/finished plus call usage for the measured runs."""
    if not run_ids or not case_ids:
        return [], [], {"rows": [], "note": "no accepted job reached PostgreSQL"}, 0
    attempt_rows = stack.psql_rows(
        "SELECT r.case_id, r.id, r.state, a.id, a.ordinal, a.state, "
        "COALESCE(a.accepted_at,''), COALESCE(a.started_at,''), COALESCE(a.finished_at,'') "
        "FROM review_runs r JOIN review_attempts a ON a.run_id = r.id "
        f"WHERE r.case_id IN ({id_list(case_ids)}) ORDER BY r.case_id, a.ordinal")
    job_rows = stack.psql_rows(
        "SELECT run_id, attempt_id, state, generation, COALESCE(worker_id,''), "
        "COALESCE(created_at,''), COALESCE(updated_at,''), "
        "COALESCE(round(EXTRACT(EPOCH FROM (lease_until - clock_timestamp()))::numeric, 3), 0) "
        f"FROM worker_jobs WHERE run_id IN ({id_list(run_ids)}) ORDER BY run_id")
    usage_rows = stack.psql_rows(
        "SELECT caller_identity, call_status, error_code, COALESCE(usage_source,''), count(*), "
        "count(*) FILTER (WHERE input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL) "
        f"FROM model_usage WHERE run_id IN ({id_list(run_ids)}) GROUP BY 1,2,3,4 ORDER BY 1,2,3,4")
    leases = int(stack.psql_scalar(f"SELECT count(*) FROM model_call_leases WHERE run_id IN ({id_list(run_ids)})"))

    attempts: list[dict[str, object]] = []
    for line in attempt_rows:
        case_id, run_id, run_state, attempt_id, ordinal, attempt_state, accepted, started_at, finished = line.split("|")
        accepted_ts, started_ts, finished_ts = parse_stamp(accepted), parse_stamp(started_at), parse_stamp(finished)
        attempts.append({
            "case_id": case_id, "run_id": run_id, "run_state": run_state, "attempt_id": attempt_id,
            "ordinal": int(ordinal), "attempt_state": attempt_state,
            "accepted_at": accepted, "started_at": started_at, "finished_at": finished,
            "queue_wait_seconds": round((started_ts - accepted_ts).total_seconds(), 6) if accepted_ts and started_ts else None,
            "service_seconds": round((finished_ts - started_ts).total_seconds(), 6) if started_ts and finished_ts else None,
            "end_to_end_seconds": round((finished_ts - accepted_ts).total_seconds(), 6) if accepted_ts and finished_ts else None,
        })
    measured_jobs: list[dict[str, object]] = []
    for line in job_rows:
        run_id, attempt_id, state, generation, worker_id, created, updated, lease_left = line.split("|")
        measured_jobs.append({"run_id": run_id, "attempt_id": attempt_id, "state": state,
                              "generation": int(generation), "worker_id": worker_id,
                              "created_at": created, "updated_at": updated,
                              "lease_seconds_left": float(lease_left)})
    usage = {
        "rows": [{"caller_identity": line.split("|")[0], "call_status": line.split("|")[1],
                  "error_code": line.split("|")[2], "usage_source": line.split("|")[3],
                  "calls": int(line.split("|")[4]), "unknown_usage_calls": int(line.split("|")[5])}
                 for line in usage_rows],
        "retries": {"attempts_beyond_first": sum(1 for attempt in attempts if attempt["ordinal"] > 1),
                    "jobs_claimed_more_than_once": sum(1 for job in measured_jobs if job["generation"] > 1)},
        "unknown_cost": None,
        "note": "an unknown usage call is a retained row with null tokens; cost is null, never zero",
    }
    return attempts, measured_jobs, usage, leases


def bucket_stats(rows: list[dict[str, object]], key) -> dict[str, object]:
    grouped: dict[str, list[dict[str, object]]] = {}
    for row in rows:
        grouped.setdefault(str(key(row)), []).append(row)
    result: dict[str, object] = {}
    for name, bucket in grouped.items():
        durations = [float(row["seconds"]) for row in bucket if row.get("seconds") is not None]
        result[name] = {"requests": len(bucket),
                        "refused": sum(1 for row in bucket if int(row["status"]) >= 400),
                        "transport_errors": sum(1 for row in bucket if int(row["status"]) == 0),
                        "duration_seconds": stats(durations)}
    return result


def request_stats(rows: list[dict[str, object]], window: float) -> dict[str, object]:
    durations = [float(row["seconds"]) for row in rows if row.get("seconds") is not None]
    return {
        "total": len(rows),
        "refused": sum(1 for row in rows if int(row["status"]) >= 400),
        "transport_errors": sum(1 for row in rows if int(row["status"]) == 0),
        "throughput_per_second": round(len(rows) / window, 4) if window > 0 else None,
        "duration_seconds": stats(durations),
        "by_kind": bucket_stats(rows, lambda row: row["kind"]),
        "by_level": bucket_stats(rows, lambda row: row["level"]),
        "by_scenario": bucket_stats(rows, lambda row: row["scenario"]),
        "by_level_scenario": bucket_stats(rows, lambda row: f"{row['level']}:{row['scenario']}"),
        "method": "one measured request per HTTP call (uploads and each poll included); sleeps are not latency",
    }


def job_stats(jobs: list[dict[str, object]], window: float, attempts: list[dict[str, object]],
              tags: dict[str, tuple[str, int]]) -> dict[str, object]:
    def bucket_name(job: dict[str, object]) -> str:
        if not job.get("terminal"):
            return "unfinished"
        return str(job.get("run_state", "") or "unknown")

    counts: dict[str, int] = {}
    for job in jobs:
        name = bucket_name(job)
        counts[name] = counts.get(name, 0) + 1
    machine_finished = sum(count for name, count in counts.items() if name not in ("unfinished",))
    completed = counts.get("completed", 0)
    partial = counts.get("partial", 0)
    failed = counts.get("failed", 0)
    awaiting = counts.get("awaiting_human", 0)
    per_group: dict[str, dict[str, object]] = {}
    for job in jobs:
        key = f"{job.get('level', 0)}:{job.get('scenario', 'unknown')}"
        entry = per_group.setdefault(key, {"jobs": 0, "completed": 0, "partial": 0, "failed": 0,
                                           "awaiting_human": 0, "unfinished": 0, "refused_requests": 0,
                                           "transport_errors": 0, "relations_mismatches": 0,
                                           "queue_wait_seconds": []})
        entry["jobs"] = int(entry["jobs"]) + 1
        name = bucket_name(job)
        if name in entry:
            entry[name] = int(entry[name]) + 1
        entry["refused_requests"] = int(entry["refused_requests"]) + int(job.get("refused", 0))
        entry["transport_errors"] = int(entry["transport_errors"]) + int(job.get("transport_errors", 0))
        if job.get("terminal") and job.get("relations_match") is False:
            entry["relations_mismatches"] = int(entry["relations_mismatches"]) + 1
    for attempt in attempts:
        scenario, level = tags.get(str(attempt["run_id"]), ("unknown", 0))
        key = f"{level}:{scenario}"
        entry = per_group.setdefault(key, {"jobs": 0, "completed": 0, "partial": 0, "failed": 0,
                                           "awaiting_human": 0, "unfinished": 0, "refused_requests": 0,
                                           "transport_errors": 0, "relations_mismatches": 0,
                                           "queue_wait_seconds": []})
        if attempt["queue_wait_seconds"] is not None:
            entry["queue_wait_seconds"].append(float(attempt["queue_wait_seconds"]))
    for entry in per_group.values():
        entry["queue_wait_seconds"] = stats(list(entry["queue_wait_seconds"]))  # type: ignore[arg-type]
    return {
        "jobs": len(jobs),
        "machine_finished": machine_finished,
        "completed": completed,
        "partial": partial,
        "failed": failed,
        "awaiting_human": awaiting,
        "unfinished": counts.get("unfinished", 0),
        "state_counts": counts,
        "refused_requests": sum(int(job.get("refused", 0)) for job in jobs),
        "transport_errors": sum(int(job.get("transport_errors", 0)) for job in jobs),
        "relations_mismatches": sum(1 for job in jobs if job.get("terminal") and job.get("relations_match") is False),
        "machine_finished_throughput_per_second": round(machine_finished / window, 4) if window > 0 else None,
        "completed_throughput_per_second": round(completed / window, 4) if window > 0 else None,
        "per_level_scenario": per_group,
    }


def queue_stats(attempts: list[dict[str, object]], tags: dict[str, tuple[str, int]]) -> dict[str, object]:
    queue = [float(attempt["queue_wait_seconds"]) for attempt in attempts if attempt["queue_wait_seconds"] is not None]
    service = [float(attempt["service_seconds"]) for attempt in attempts if attempt["service_seconds"] is not None]
    end_to_end = [float(attempt["end_to_end_seconds"]) for attempt in attempts if attempt["end_to_end_seconds"] is not None]
    grouped: dict[str, dict[str, list[float]]] = {}
    for attempt in attempts:
        scenario, level = tags.get(str(attempt["run_id"]), ("unknown", 0))
        entry = grouped.setdefault(f"{level}:{scenario}", {"queue": [], "end_to_end": []})
        if attempt["queue_wait_seconds"] is not None:
            entry["queue"].append(float(attempt["queue_wait_seconds"]))
        if attempt["end_to_end_seconds"] is not None:
            entry["end_to_end"].append(float(attempt["end_to_end_seconds"]))
    per_group = {key: {"queue_wait_seconds": stats(value["queue"]),
                       "end_to_end_seconds": stats(value["end_to_end"])}
                 for key, value in grouped.items()}
    return {
        "attempts": len(attempts),
        "queue_wait_seconds": stats(queue),
        "service_seconds": stats(service),
        "end_to_end_seconds": stats(end_to_end),
        "per_level_scenario": per_group,
        "method": "nearest-rank percentiles over the measured rows; small n is reported as small n",
    }


def resource_stats(samples: list[dict[str, object]]) -> dict[str, object]:
    grouped: dict[str, dict[str, list[float]]] = {}
    observations: dict[str, int] = {}
    for sample in samples:
        key = f"{sample['service']}#{sample['id']}"
        bucket = grouped.setdefault(key, {"cpu": [], "rss": []})
        observations[key] = observations.get(key, 0) + 1
        if sample.get("cpu_percent") is not None:
            bucket["cpu"].append(float(sample["cpu_percent"]))
        if sample.get("rss_bytes") is not None:
            bucket["rss"].append(float(sample["rss_bytes"]))
    result: dict[str, object] = {}
    for key, bucket in grouped.items():
        result[key] = {
            "samples": observations[key],
            "cpu_percent": {"min": round(min(bucket["cpu"]), 2), "max": round(max(bucket["cpu"]), 2),
                            "mean": round(sum(bucket["cpu"]) / len(bucket["cpu"]), 2)} if bucket["cpu"] else None,
            "rss_bytes": {"min": int(min(bucket["rss"])), "max": int(max(bucket["rss"])),
                          "mean": int(sum(bucket["rss"]) / len(bucket["rss"]))} if bucket["rss"] else None,
        }
    return {"per_process": result, "sample_count": len(samples),
            "note": "unreadable samples stay null and are excluded from the min/max/mean ranges"}


def hardware_identity() -> dict[str, object]:
    cpu = ""
    cores = 0
    try:
        for line in Path("/proc/cpuinfo").read_text(encoding="utf-8").splitlines():
            if line.startswith("model name") and not cpu:
                cpu = line.split(":", 1)[1].strip()
            if line.startswith("processor"):
                cores += 1
    except OSError:
        pass
    memory = 0
    try:
        for line in Path("/proc/meminfo").read_text(encoding="utf-8").splitlines():
            if line.startswith("MemTotal:"):
                memory = int(line.split()[1]) * 1024
    except (OSError, ValueError, IndexError):
        pass
    uname = os.uname()
    return {"cpu": cpu, "cores": cores, "memory_bytes": memory,
            "kernel": f"{uname.sysname} {uname.release} {uname.machine}", "hostname": uname.nodename}


def runtime_identity(app: dict[str, str], dataset: dict) -> dict[str, object]:
    identity: dict[str, object] = {
        "app_env": app.get("APP_ENV", ""),
        "edge_port": app.get("EDGE_PORT") or os.environ.get("EDGE_PORT", ""),
        "dataset_id": dataset.get("dataset_id"),
        "dataset_sha256": dataset.get("sha256"),
    }
    try:
        process = subprocess.run(["docker", "version", "--format", "{{.Server.Version}}"], cwd=ROOT,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        identity["docker_server"] = process.stdout.decode().strip()
    except (OSError, subprocess.SubprocessError):
        identity["docker_server"] = ""
    try:
        module = load_module("source_identity", "scripts/source_identity.py")
        identity["source_identity_digest"] = module.source_identity(ROOT)["digest"]
    except Exception as error:
        raise Failure(f"cannot establish the load source identity: {error}") from error
    return identity


def prove_fixture_gateway(stack, args) -> dict[str, object]:
    """Every gateway selected for Worker calls must prove the frozen fixture identity."""
    observations: list[dict[str, str]] = []
    if args.control == "host":
        if not args.control_file:
            raise Failure("host control needs --control-file")
        for instance in control_instances(Path(args.control_file)):
            environment = proc_environ(int(instance["pid"]))
            if environment.get("SERVICE_ROLE") == "model":
                observations.append({
                    "source": f"{instance['service']} pid {instance['pid']}",
                    "mode": environment.get("MODEL_GATEWAY_MODE", ""),
                    "chat": environment.get("LLM_MODEL", ""),
                    "ocr": environment.get("OCR_MODEL", ""),
                })
    else:
        service, containers = fault.select_model_gateway_service(stack)
        for container in containers:
            observations.append({
                "source": f"{service} {container}",
                "mode": stack.env_value(container, "MODEL_GATEWAY_MODE"),
                "chat": stack.env_value(container, "LLM_MODEL"),
                "ocr": stack.env_value(container, "OCR_MODEL"),
            })
    if not observations:
        raise Failure("no controlled model gateway instance is available to prove fixture mode")
    invalid = [item for item in observations if item["mode"] != "fixture"
               or item["chat"] != FIXTURE_CHAT_MODEL or item["ocr"] != FIXTURE_OCR_MODEL]
    if invalid:
        raise Failure(
            f"fixture-only load refused: controlled Model Gateway instances report {invalid!r}, expected "
            f"mode=fixture with {FIXTURE_CHAT_MODEL}/{FIXTURE_OCR_MODEL}")
    return {"instances": observations, "mode": "fixture", "chat": FIXTURE_CHAT_MODEL, "ocr": FIXTURE_OCR_MODEL}


def sampling_targets(stack, args) -> list[dict[str, object]]:
    targets: list[dict[str, object]] = []
    if args.control == "host":
        for instance in control_instances(Path(args.control_file)):
            targets.append({"kind": "pid", "service": instance["service"], "id": instance["pid"]})
        return targets
    for service in ("core-api", "audit-worker", "document-service", "audit-execution",
                    "model-gateway", "model-gateway-fixture", "tool-gateway"):
        try:
            containers = stack.containers(service)
        except Exception:
            continue
        for container in containers:
            targets.append({"kind": "container", "service": service, "id": container})
    return targets


def summary_skeleton(args, dataset, scenarios, levels, total_cases, identity) -> dict[str, object]:
    return {
        "schema_version": "d2-7e-bounded-load/v2",
        "created_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "status": "RUNNING",
        "shape": {"scenarios": [scenario["id"] for scenario in scenarios], "concurrency_levels": levels,
                  "repeats": args.repeats, "max_cases": total_cases},
        "identity": identity,
        "coverage": {
            "sampled": "controlled service processes only",
            "not_sampled": ["PostgreSQL (compose database container)", "object store (RustFS container)"],
            "claims": "local single-host samples with small n; not production capacity, p95 or RPO/RTO claims",
        },
        "caps": {"max_cases": IMAGE_CASE_CAP, "read_only_database": True, "no_seed_overwrite": True},
    }


def write_artifacts(directory: Path, sink: dict[str, object], requests: list[dict[str, object]],
                    jobs: list[dict[str, object]], samples: list[dict[str, object]]) -> None:
    def dump(name: str, rows: list[dict[str, object]]) -> None:
        with (directory / name).open("w", encoding="utf-8") as stream:
            for row in rows:
                stream.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")

    dump("requests.jsonl", requests)
    dump("jobs.jsonl", jobs)
    dump("resource-samples.jsonl", samples)
    dump("pg-attempts.jsonl", list(sink.get("pg_attempts", []) or []))
    (directory / "summary.json").write_text(
        json.dumps(sink, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", default=str(DISTRIBUTED_ENV_PATH),
                        help="private dotenv profile of the isolated stack")
    parser.add_argument("--confirm", default="", help="the isolated database every stack service must target")
    parser.add_argument("--dataset", default=str(DEFAULT_DATASET))
    parser.add_argument("--scenarios", default="", help="default: every scenario in the frozen dataset")
    parser.add_argument("--concurrency", default="1,2", help="finite concurrency levels, default 1,2")
    parser.add_argument("--repeats", type=int, default=3, help="repeats per scenario and level, default 3")
    parser.add_argument("--timeout", type=float, default=300, help="per-repetition deadline in seconds")
    parser.add_argument("--control", choices=("compose", "host"), default="compose")
    parser.add_argument("--control-file", default="", help="host control: JSON naming each instance pid and log")
    parser.add_argument("--oidc-env-file", default="", help="private local provider profile for authorization-code login")
    parser.add_argument("--sample-interval", type=float, default=1.0)
    parser.add_argument("--artifact-dir", default="")
    parser.add_argument("--plan", action="store_true", help="guards and dataset only; no HTTP or database call")
    parser.add_argument("--run", action="store_true", help="execute the bounded load")
    args = parser.parse_args(argv)
    if not args.plan and not args.run:
        args.plan = True
    if not args.confirm:
        print("REFUSED: pass --confirm <database> naming the isolated test database", file=sys.stderr)
        return 2

    journey = load_module("journey_local", "deployments/scripts/journey-local.py")
    fault = load_module("fault_local", "deployments/scripts/fault-test-local.py")
    seed = load_module("seed_telecom", "scripts/seed_telecom.py")
    env_path = Path(args.env)
    if not env_path.is_file():
        print(f"REFUSED: missing private distributed profile at {env_path}", file=sys.stderr)
        return 2
    if env_path.read_text(encoding="utf-8").lstrip().startswith("{"):
        print("REFUSED: --env must be the dotenv profile compose consumes", file=sys.stderr)
        return 2
    try:
        infra, app = journey.require_isolated_stack(env_path, args.confirm)
    except journey.Failure as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2
    try:
        dataset = seed.load_dataset(Path(args.dataset))
        if set(seed.PENDING_STATES) != PENDING_STATES:
            raise Failure("the pending-state set changed in scripts/seed_telecom.py; update this harness to match")
        levels = parse_int_list(args.concurrency, minimum=1, maximum=4, label="--concurrency")
        if not math.isfinite(args.sample_interval) or args.sample_interval <= 0:
            raise Failure("--sample-interval must be a finite positive number")
        if args.repeats < 1 or args.repeats > 10:
            raise Failure("--repeats must be between 1 and 10")
        names = [item.strip() for item in args.scenarios.split(",") if item.strip()] or \
            [str(scenario["id"]) for scenario in dataset["scenarios"]]
        scenarios = []
        for name in names:
            scenario = next((item for item in dataset["scenarios"] if item.get("id") == name), None)
            if scenario is None:
                raise Failure(f"scenario {name!r} is not part of the frozen dataset")
            scenarios.append(scenario)
        total_cases = len(scenarios) * len(levels) * args.repeats
        if total_cases > IMAGE_CASE_CAP:
            raise Failure(f"the requested shape would create {total_cases} cases, above the cap of {IMAGE_CASE_CAP}")
    except (Failure, seed.SeedError) as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 2

    plan = {
        "mode": "plan" if args.plan and not args.run else "run",
        "database": args.confirm,
        "env": str(env_path),
        "dataset": {"path": str(args.dataset), "id": dataset.get("dataset_id"), "sha256": dataset.get("sha256")},
        "scenarios": [scenario["id"] for scenario in scenarios],
        "concurrency_levels": levels,
        "repeats": args.repeats,
        "max_cases": total_cases,
        "workload": {
            "generation": "dataset fields per scenario",
            "materials": "dataset materials plus the scenario draft_override uploaded as draft_contract",
            "terminal": f"poll stops outside the pending states {sorted(seed.PENDING_STATES)} (awaiting_human is finished)",
            "relations": "all three expected_relations read back from workspace evaluations keyed by rule_id",
        },
        "fixture_mode_proof": "controlled model gateway must report MODEL_GATEWAY_MODE=fixture with "
                              f"{FIXTURE_CHAT_MODEL}/{FIXTURE_OCR_MODEL}",
        "control": {"mode": args.control, "file": args.control_file},
        "authentication": "oidc" if args.oidc_env_file else "demo",
        "resource_coverage": {
            "sampled": "controlled service processes (host pids from the control file, or compose containers)",
            "not_sampled": ["PostgreSQL (compose database container)", "object store (RustFS container)"],
        },
        "caps": {"max_cases": IMAGE_CASE_CAP, "no_seed_overwrite": True, "read_only_database": True},
    }
    print(json.dumps(plan, ensure_ascii=False, sort_keys=True))
    if args.plan and not args.run:
        return 0

    artifact_dir = Path(args.artifact_dir) if args.artifact_dir else \
        ROOT / "artifacts" / "d2-7e" / (dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S") + "_" + secrets.token_hex(3))
    try:
        artifact_dir.mkdir(parents=True)
    except FileExistsError:
        print(f"REFUSED: artifact directory already exists; refusing to overwrite it: {artifact_dir}", file=sys.stderr)
        return 2
    except OSError as error:
        print(f"REFUSED: cannot create artifact directory {artifact_dir}: {error}", file=sys.stderr)
        return 2
    recorder = fault.Recorder(artifact_dir / "commands.jsonl", set(infra.values()) | set(app.values()))
    requests_log: list[dict[str, object]] = []
    jobs: list[dict[str, object]] = []

    try:
        oidc_setup = fault.load_oidc_setup(Path(args.oidc_env_file), recorder) if args.oidc_env_file else None
        stack = fault.Stack(recorder, env_path, args.confirm, app, args.control,
                            Path(args.control_file) if args.control_file else None)
        fixture_gateway = prove_fixture_gateway(stack, args)
        targets = sampling_targets(stack, args)
        processes = {service: stack.process_facts(service)
                     for service in sorted({str(target["service"]) for target in targets})}
        runtime = runtime_identity(app, dataset)
    except (Failure, fault.Failure, OSError) as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        (artifact_dir / "preflight-failure.json").write_text(
            json.dumps({"status": "REFUSED", "error": str(error), "plan": plan},
                       ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        recorder.flush()
        return 2

    identity = {"fixture_gateway": fixture_gateway, "sampled_targets": targets, "processes": processes,
                "hardware": hardware_identity(), "runtime": runtime,
                "authentication": {"mode": "oidc" if oidc_setup else "demo",
                                   "issuer": oidc_setup["issuer"] if oidc_setup else None,
                                   "request_latency_coverage": "business API requests; OIDC redirects excluded"}}
    sink = summary_skeleton(args, dataset, scenarios, levels, total_cases, identity)
    sink["coverage"]["sampled_services"] = sorted({str(target["service"]) for target in targets})
    sink["coverage"]["not_declared_note"] = ("services absent from the control file (host) or not running (compose) "
                                             "are not sampled; PostgreSQL and RustFS stay external")
    base_url = str(oidc_setup["origin"]) if oidc_setup else \
        f"http://127.0.0.1:{app.get('EDGE_PORT') or infra.get('EDGE_PORT') or '8080'}"

    def authenticate(client):
        return fault.authenticate_client(client, {"oidc": oidc_setup, "recorder": recorder})

    sampler: Sampler | None = None
    started: float | None = None
    window_end = 0.0
    try:
        preflight = journey.Journey(base_url, time.monotonic() + 30)
        preflight.call("GET", "/ready", expected={200})
        authenticate(preflight)
        _, templates, _ = preflight.call("GET", "/api/templates", expected={200})
        entries = [item for item in (templates or {}).get("items", []) if item.get("id") == dataset["template_id"]]
        if len(entries) != 1:
            raise Failure(f"template {dataset['template_id']} is absent or ambiguous")
        if entries[0].get("version") != dataset["template_version"]:
            raise Failure(f"template version changed: {entries[0].get('version')!r}")
        sampler = Sampler(recorder, args.sample_interval, targets)
        started = time.monotonic()
        sampler.start()
        for level in levels:
            with concurrent.futures.ThreadPoolExecutor(max_workers=level) as pool:
                futures = {}
                for scenario in scenarios:
                    for repeat in range(1, args.repeats + 1):
                        future = pool.submit(measured_repetition, journey, base_url, dataset, scenario, level,
                                             repeat, args.timeout, authenticate)
                        futures[future] = (scenario["id"], level, repeat)
                for future in concurrent.futures.as_completed(futures):
                    scenario_id, level_done, repeat = futures[future]
                    try:
                        job, request_rows = future.result()
                        jobs.append(job)
                        requests_log.extend(request_rows)
                    except Exception as error:  # context is preserved, never dropped
                        jobs.append({"scenario": scenario_id, "level": level_done, "repeat": repeat,
                                     "terminal": False, "refused": 0, "error": str(error), "run_state": ""})
    except (Failure, fault.Failure, journey.Failure, OSError) as error:
        print(f"FAIL: load window could not run: {error}", file=sys.stderr)
        sink["failures"] = [str(error)]
        sink["status"] = "FAIL"
        write_artifacts(artifact_dir, sink, requests_log, jobs, [])
        recorder.flush()
        return 1
    finally:
        window_end = time.monotonic()
        if sampler is not None:
            sampler.stop_event.set()
            sampler.join(timeout=30)
    assert started is not None
    window = window_end - started
    samples = sampler.samples if sampler else []

    try:
        run_ids = [str(job["run_id"]) for job in jobs if job.get("run_id")]
        case_ids = [str(job["case_id"]) for job in jobs if job.get("case_id")]
        pg_attempts, pg_jobs, usage, leases = postgres_pass(stack, run_ids, case_ids)
    except Failure as error:
        print(f"FAIL: PostgreSQL measurement pass failed: {error}", file=sys.stderr)
        sink["failures"] = [str(error)]
        sink["status"] = "FAIL"
        write_artifacts(artifact_dir, sink, requests_log, jobs, samples)
        recorder.flush()
        return 1

    tags = {str(job["run_id"]): (str(job.get("scenario", "unknown")), int(job.get("level", 0)))
            for job in jobs if job.get("run_id")}
    sink["window_seconds"] = round(window, 3)
    sink["requests"] = request_stats(requests_log, window)
    sink["jobs"] = job_stats(jobs, window, pg_attempts, tags)
    sink["queue"] = queue_stats(pg_attempts, tags)
    sink["pg_attempts"] = pg_attempts
    sink["pg_jobs"] = pg_jobs
    sink["usage"] = usage
    sink["leases_open"] = leases
    sink["resources"] = resource_stats(samples)
    load_failures = [job["error"] for job in jobs if "error" in job]
    if leases:
        load_failures.append(f"{leases} model-call reservation(s) remain after the load window")
    sink["failures"] = load_failures
    failed = [job for job in jobs
              if not job.get("terminal") or job.get("run_state") == "failed"
              or job.get("relations_match") is False]
    sink["status"] = "FAIL" if (failed or sink["failures"]) else "PASS"
    write_artifacts(artifact_dir, sink, requests_log, jobs, samples)
    recorder.flush()
    print(json.dumps({
        "artifact_dir": str(artifact_dir),
        "window_seconds": sink["window_seconds"],
        "requests": {key: sink["requests"][key] for key in
                     ("total", "refused", "transport_errors", "throughput_per_second")},
        "jobs": {key: sink["jobs"][key] for key in ("jobs", "machine_finished", "completed",
                                                       "partial", "awaiting_human", "failed", "unfinished",
                                                       "relations_mismatches")},
        "queue": sink["queue"]["queue_wait_seconds"],
        "usage_rows": len(usage["rows"]),
        "status": sink["status"],
        "leases_open": leases,
    }, ensure_ascii=False, sort_keys=True))
    return 1 if (failed or sink["failures"]) else 0


if __name__ == "__main__":
    raise SystemExit(main())

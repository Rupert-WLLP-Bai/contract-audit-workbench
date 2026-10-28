#!/usr/bin/env python3
"""Seed frozen synthetic telecom C1-B cases through the authenticated HTTP API."""

import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_DATASET = ROOT / "testdata" / "telecom-c1b-v1.json"
REQUIRED_FIELDS = {
    "circuit_id",
    "bandwidth_mbps",
    "sla_percent",
    "sla_period",
    "sla_exclusions",
    "amount_cny",
    "tax_basis",
    "prepayment_percent",
    "customer",
    "supplier",
}
PENDING_STATES = {"accepted", "queued", "running"}


class SeedError(RuntimeError):
    pass


def read_dotenv(path: Path) -> dict[str, str]:
    """Read KEY=value literals without shell evaluation or variable expansion."""
    if not path.exists():
        return {}
    result: dict[str, str] = {}
    for number, source in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = source.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export "):].lstrip()
        key, separator, value = line.partition("=")
        key = key.strip()
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise SeedError(f"invalid dotenv assignment at {path}:{number}")
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        result[key] = value
    return result


def canonical_json(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


def load_dataset(path: Path) -> dict:
    try:
        dataset = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SeedError(f"cannot read dataset {path}: {exc}") from exc
    scenarios = dataset.get("scenarios")
    if (
        dataset.get("dataset_id") != "telecom-c1b-v1"
        or not isinstance(scenarios, list)
        or not dataset.get("template_id")
        or not dataset.get("template_version")
    ):
        raise SeedError("dataset identity or scenario list is invalid")
    digest_input = {key: value for key, value in dataset.items() if key != "sha256"}
    actual = hashlib.sha256(canonical_json(digest_input)).hexdigest()
    if dataset.get("sha256") != actual:
        raise SeedError(f"dataset SHA-256 mismatch: expected {dataset.get('sha256')}, got {actual}")
    if {item.get("id") for item in scenarios} != {"compatible", "conflict", "insufficient"}:
        raise SeedError("dataset must contain the three frozen telecom scenarios")
    for item in scenarios:
        if set(item.get("fields", {})) != REQUIRED_FIELDS:
            raise SeedError(f"scenario {item.get('id')} does not contain the frozen template fields")
        relations = item.get("expected_relations", {})
        if set(relations) != {"ENT-TECH-BW-001", "ENT-TECH-SLA-001", "ENT-COM-ADV-001"}:
            raise SeedError(f"scenario {item.get('id')} has an incomplete expected relation map")
        if any(value not in {"compatible", "conflict", "insufficient", "not_applicable"} for value in relations.values()):
            raise SeedError(f"scenario {item.get('id')} contains an invalid relation")
    return dataset


class API:
    def __init__(self, base: str):
        self.base = base.rstrip("/")
        cookies = http.cookiejar.CookieJar()
        self.client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))

    def request(self, method: str, path: str, payload=None, content_type=None, timeout=30):
        raw = None
        headers = {"Accept": "application/json"}
        if isinstance(payload, bytes):
            raw = payload
        elif payload is not None:
            raw = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if content_type:
            headers["Content-Type"] = content_type
        request = urllib.request.Request(self.base + path, raw, headers=headers, method=method)
        try:
            with self.client.open(request, timeout=timeout) as response:
                body = response.read()
                return response.status, decode_json(body)
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            raise SeedError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
        except urllib.error.URLError as exc:
            raise SeedError(f"{method} {path} failed: {exc.reason}") from exc

    def upload(self, case_id: str, material: dict) -> dict:
        boundary = uuid.uuid4().hex
        content = material["text"].encode("utf-8")
        filename = material["filename"]
        role = material["role"]
        body = (
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"role\"\r\n\r\n{role}\r\n"
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
            "Content-Type: text/plain; charset=utf-8\r\n\r\n"
        ).encode("utf-8") + content + f"\r\n--{boundary}--\r\n".encode("ascii")
        _, result = self.request(
            "POST", f"/api/cases/{case_id}/documents", payload=body,
            content_type=f"multipart/form-data; boundary={boundary}", timeout=60,
        )
        return result or {}


def decode_json(body: bytes):
    if not body:
        return None
    try:
        return json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise SeedError(f"API returned non-JSON data: {exc}") from exc


def field_keys(template: dict) -> set[str]:
    return {item.get("key") for item in template.get("fields", [])}


def get_case(api: API, case_id: str) -> dict:
    _, detail = api.request("GET", f"/api/cases/{case_id}")
    return detail


def ensure_case(api: API, scenario: dict, existing: dict | None) -> tuple[str, dict]:
    if existing:
        case_id = existing.get("id")
        detail = get_case(api, case_id)
        if (detail.get("business_line"), detail.get("scenario")) != ("ENT", "telecom_dedicated_line"):
            raise SeedError(f"case title already exists with different business scope: {scenario['title']}")
        return case_id, detail
    _, created = api.request("POST", "/api/cases", {
        "title": scenario["title"],
        "contract_type": "service",
        "business_line": "ENT",
        "scenario": "telecom_dedicated_line",
    })
    case_id = created.get("id")
    if not case_id:
        raise SeedError("case creation returned no case id")
    return case_id, get_case(api, case_id)


def ensure_generation(api: API, case_id: str, detail: dict, template_id: str, fields: dict) -> dict:
    generations = detail.get("generations", [])
    if generations:
        matches = [item for item in generations if item.get("template_id") == template_id]
        if not matches:
            raise SeedError(f"case {case_id} already has a generation from another template")
        return matches[-1]
    _, generation = api.request("POST", f"/api/cases/{case_id}/generations", {
        "template_id": template_id,
        "fields": fields,
    }, timeout=60)
    if not generation.get("document_id") or generation.get("template_id") != template_id:
        raise SeedError(f"template generation response is incomplete for case {case_id}")
    return generation


def ensure_material(api: API, case_id: str, detail: dict, material: dict) -> tuple[dict, bool]:
    digest = hashlib.sha256(material["text"].encode("utf-8")).hexdigest()
    current = [item for item in detail.get("documents", []) if not item.get("superseded")]
    if any(item.get("role") == material["role"] and item.get("digest") == digest for item in current):
        return detail, False
    api.upload(case_id, material)
    return get_case(api, case_id), True


def poll_review(api: API, case_id: str, run_id: str, timeout: float) -> dict:
    deadline = time.monotonic() + timeout
    while True:
        _, workspace = api.request("GET", f"/api/cases/{case_id}/reviews/{run_id}", timeout=30)
        run = workspace.get("run", {})
        state = run.get("state", "")
        if state not in PENDING_STATES:
            return workspace
        if time.monotonic() >= deadline:
            raise SeedError(f"review {run_id} remained {state} beyond {timeout:g}s")
        time.sleep(0.5)


def observed_relations(scenario: dict, workspace: dict) -> dict[str, str]:
    evaluations = workspace.get("evaluations", [])
    output = {}
    for rule_id, expected in scenario["expected_relations"].items():
        matches = [item for item in evaluations if item.get("rule_id") == rule_id]
        if len(matches) != 1:
            raise SeedError(f"review returned {len(matches)} evaluations for {rule_id}")
        relation = matches[0].get("relation")
        if relation != expected:
            raise SeedError(f"scenario {scenario['id']} expected {rule_id}={expected}, got {relation}")
        output[rule_id] = relation
    return output


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", help="API base URL (or API_BASE_URL in the environment)")
    parser.add_argument("--username", help="login name (or DEMO_USERNAME; default demo-admin)")
    parser.add_argument("--env-file", type=Path, default=Path(".env"), help="read literal KEY=value entries; no shell evaluation")
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--template-id", help="template ID; defaults to the frozen dataset template")
    parser.add_argument("--scenario", action="append", choices=("compatible", "conflict", "insufficient"), help="scenario to seed; repeat to select several; defaults to all")
    parser.add_argument("--review-timeout", type=float, default=150)
    parser.add_argument("--business-at", default="2026-01-15T09:00:00+08:00",
                        help="timezone-aware review business time for governed cases")
    parser.add_argument("--out", type=Path, help="optional JSON record of actual API responses")
    args = parser.parse_args()

    try:
        settings = read_dotenv(args.env_file)
        settings.update(os.environ)
        dataset = load_dataset(args.dataset)
        base = args.base or settings.get("API_BASE_URL", "http://127.0.0.1:8080")
        username = args.username or settings.get("DEMO_USERNAME", "demo-admin")
        password = settings.get("DEMO_PASSWORD", "admin-demo")
        if not base.startswith(("http://", "https://")):
            raise SeedError("API base URL must use http or https")
        template_id = args.template_id or dataset["template_id"]
        selected = list(dict.fromkeys(args.scenario or [item["id"] for item in dataset["scenarios"]]))
        by_id = {item["id"]: item for item in dataset["scenarios"]}

        api = API(base)
        _, session = api.request("POST", "/api/session", {"username": username, "password": password})
        if not session or not session.get("actor", {}).get("id"):
            raise SeedError("login response did not contain a trusted actor")
        _, templates = api.request("GET", "/api/templates")
        matches = [item for item in templates.get("items", []) if item.get("id") == template_id]
        if len(matches) != 1:
            raise SeedError(f"template {template_id!r} is absent or ambiguous")
        template = matches[0]
        if template_id == dataset["template_id"] and template.get("version") != dataset["template_version"]:
            raise SeedError(f"template {template_id} version changed: expected {dataset['template_version']}, got {template.get('version')}")
        missing_fields = REQUIRED_FIELDS - field_keys(template)
        if missing_fields:
            raise SeedError(f"template {template_id} lacks fields: {', '.join(sorted(missing_fields))}")
        _, cases = api.request("GET", "/api/cases")
        existing_by_title = {item.get("title"): item for item in cases.get("items", [])}

        results = []
        for scenario_id in selected:
            scenario = by_id[scenario_id]
            case_id, detail = ensure_case(api, scenario, existing_by_title.get(scenario["title"]))
            generation = ensure_generation(api, case_id, detail, template_id, scenario["fields"])
            detail = get_case(api, case_id)
            uploads = []
            for material in scenario.get("materials", []):
                detail, created = ensure_material(api, case_id, detail, material)
                if created:
                    uploads.append(material["filename"])
            override = scenario.get("draft_override")
            if override:
                detail, created = ensure_material(api, case_id, detail, {
                    "role": "draft_contract",
                    "filename": override["filename"],
                    "text": override["text"],
                })
                if created:
                    uploads.append(override["filename"])

            _, accepted = api.request("POST", f"/api/cases/{case_id}/reviews",
                                      {"business_at": args.business_at}, timeout=60)
            run_id = accepted.get("id")
            if not run_id:
                raise SeedError(f"review request for {scenario_id} returned no run id")
            workspace = poll_review(api, case_id, run_id, args.review_timeout)
            actual_relations = observed_relations(scenario, workspace)
            execution = workspace.get("execution") or {}
            attempt = execution.get("attempt") or {}
            attempt_id = accepted.get("attempt_id") or attempt.get("id")
            results.append({
                "scenario": scenario_id,
                "case_id": case_id,
                "generation_id": generation.get("id"),
                "document_id": generation.get("document_id"),
                "run_id": run_id,
                "attempt_id": attempt_id,
                "run_state": workspace.get("run", {}).get("state"),
                "expected_relations": scenario["expected_relations"],
                "observed_relations": actual_relations,
                "observed_findings": [
                    {key: finding.get(key) for key in ("id", "domain", "relation", "claim")}
                    for finding in workspace.get("findings", [])
                ],
                "uploaded_this_run": uploads,
            })

        output = {
            "dataset_id": dataset["dataset_id"],
            "dataset_sha256": dataset["sha256"],
            "data_class": dataset["data_class"],
            "template_id": template_id,
            "template_version": template.get("version"),
            "api_actor": session["actor"].get("id"),
            "cases": results,
        }
        rendered = json.dumps(output, ensure_ascii=False, indent=2) + "\n"
        if args.out:
            args.out.parent.mkdir(parents=True, exist_ok=True)
            args.out.write_text(rendered, encoding="utf-8")
        sys.stdout.write(rendered)
        return 0
    except SeedError as exc:
        parser.exit(1, f"seed_telecom: {exc}\n")


if __name__ == "__main__":
    raise SystemExit(main())

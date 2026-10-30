#!/usr/bin/env python3
"""Run a real, externally stored contract corpus through the authenticated API."""

import argparse
import datetime as dt
import hashlib
import http.cookiejar
import json
import mimetypes
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from seed_telecom import read_dotenv

ROOT = Path(__file__).resolve().parents[1]
ROLES = {"draft_contract", "procurement", "award", "attachment"}
PENDING = {"accepted", "queued", "running"}


class RunnerError(RuntimeError):
    pass


def load_manifest(path):
    path = path.resolve(strict=True)
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise RunnerError(f"cannot read manifest: {exc}") from exc
    check_manifest_identity(manifest)
    ids = set()
    for case in manifest["cases"]:
        check_case(case, ids)
        for material in case["materials"]:
            check_material(case["id"], material, path.parent)
    return path, manifest


def check_manifest_identity(manifest):
    if not isinstance(manifest, dict) or manifest.get("version") != 1 or isinstance(manifest.get("version"), bool):
        raise RunnerError("manifest version must be 1")
    if not isinstance(manifest.get("dataset_id"), str) or not manifest["dataset_id"].strip():
        raise RunnerError("manifest dataset_id is required")
    cases = manifest.get("cases")
    if not isinstance(cases, list) or not cases:
        raise RunnerError("manifest cases must be a nonempty list")


def check_case(case, ids):
    if not isinstance(case, dict) or any(not isinstance(case.get(k), str) or not case[k].strip() for k in ("id", "title", "contract_type")):
        raise RunnerError("each case requires id, title and contract_type")
    if case["id"] in ids:
        raise RunnerError(f"duplicate case id: {case['id']}")
    ids.add(case["id"])
    materials = case.get("materials")
    if not isinstance(materials, list) or not materials:
        raise RunnerError(f"case {case['id']} has no materials")


def check_material(case_id, material, base):
    if not isinstance(material, dict) or material.get("role") not in ROLES or material.get("data_class") != "original":
        raise RunnerError(f"invalid material role or data_class in {case_id}")
    if not isinstance(material.get("source"), str) or not material["source"].strip():
        raise RunnerError(f"material source required in {case_id}")
    digest = material.get("sha256")
    if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
        raise RunnerError(f"invalid SHA-256 in {case_id}")
    raw = material.get("path")
    if not isinstance(raw, str) or not raw or "\\" in raw or any(ord(c) < 32 for c in raw):
        raise RunnerError(f"invalid material path in {case_id}")
    parts = raw.split("/")
    if PurePosixPath(raw).is_absolute() or any(part in ("", ".", "..") for part in parts):
        raise RunnerError(f"unsafe material path in {case_id}")
    resolved = (base / raw).resolve(strict=True)
    if not resolved.is_relative_to(base) or not resolved.is_file():
        raise RunnerError(f"material escapes manifest directory or is not a file: {raw}")
    material["_resolved"] = str(resolved)


def material_bytes(material):
    data = Path(material["_resolved"]).read_bytes()
    if hashlib.sha256(data).hexdigest() != material["sha256"]:
        raise RunnerError(f"SHA-256 mismatch: {material['path']}")
    return data


class API:
    def __init__(self, base, timeout):
        parsed = urllib.parse.urlsplit(base)
        if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or getattr(parsed, "pass" + "word") or parsed.query or parsed.fragment:
            raise RunnerError("invalid API base URL")
        self.base = base.rstrip("/")
        self.timeout = timeout
        self.client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, method, path, payload=None, content_type=None, timeout=None):
        headers = {"Accept": "application/json"}
        if payload is not None and not isinstance(payload, bytes):
            payload = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if content_type:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(self.base + path, payload, headers, method=method)
        try:
            with self.client.open(req, timeout=timeout or self.timeout) as response:
                return json.load(response)
        except urllib.error.HTTPError as exc:
            raise RunnerError(f"{method} {path} returned HTTP {exc.code}") from exc
        except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError, UnicodeError) as exc:
            raise RunnerError(f"{method} {path} failed ({type(exc).__name__})") from exc

    def upload(self, case_id, material):
        data = material_bytes(material)
        boundary = uuid.uuid4().hex
        filename = Path(material["_resolved"]).name
        # Quoted-string escaping prevents a filename from altering multipart headers.
        filename = filename.replace('\\', '\\\\').replace('"', '\\"')
        mime = mimetypes.guess_type(filename)[0] or "application/octet-stream"
        prefix = (f'--{boundary}\r\nContent-Disposition: form-data; name="role"\r\n\r\n{material["role"]}\r\n'
                  f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
                  f'Content-Type: {mime}\r\n\r\n').encode("utf-8")
        body = prefix + data + f"\r\n--{boundary}--\r\n".encode("ascii")
        return self.request("POST", f"/api/cases/{case_id}/documents", body, f"multipart/form-data; boundary={boundary}")


def save(path, result):
    # Replace only after a complete write; the prior checkpoint survives interruption.
    fd, temporary = tempfile.mkstemp(prefix=".result-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(result, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)

def snapshot_model_calls(source, output, started_at, finished_at):
    source = Path(source).resolve(strict=True)
    if not source.is_dir():
        raise RunnerError("model recording path is not a directory")
    start = dt.datetime.fromisoformat(started_at)
    end = dt.datetime.fromisoformat(finished_at)
    destination = output / "model-calls"
    destination.mkdir(mode=0o700)
    count = 0
    for entry in sorted(source.glob("request-*")):
        if entry.is_symlink() or not entry.is_dir():
            continue
        metadata_path = entry / "metadata.json"
        if metadata_path.is_symlink() or not metadata_path.is_file():
            continue
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
        at = dt.datetime.fromisoformat(metadata["started_at"])
        if not start <= at <= end or metadata.get("endpoint") != "/v1/chat/completions":
            continue
        target = destination / entry.name
        target.mkdir(mode=0o700)
        for name in ("request.body", "response.body", "metadata.json"):
            original = entry / name
            if original.is_symlink() or not original.is_file():
                raise RunnerError("model call recording is incomplete")
            fd = os.open(target / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with original.open("rb") as reader, os.fdopen(fd, "wb") as writer:
                shutil.copyfileobj(reader, writer)
        count += 1
    return count



def run(args):
    delivery = Path(os.environ.get("CAW_DELIVERY_DIR", ROOT.parent / "contract-audit-delivery")).expanduser().resolve()
    if delivery.is_relative_to(ROOT):
        raise RunnerError("delivery directory must be outside the source repository")
    parent = delivery / "test-runs"
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    output = Path(tempfile.mkdtemp(prefix="real-corpus-", dir=parent))
    result_path = output / "result.json"
    result = {"status": "running", "started_at": dt.datetime.now(dt.timezone.utc).isoformat(), "manifest": str(args.manifest.resolve()), "model_record_dir": str(args.model_record_dir.resolve()) if args.model_record_dir else None, "model_record_time": args.model_record_time, "cases": []}
    save(result_path, result)
    print(f"output: {output}", flush=True)
    current = None
    try:
        manifest_path, manifest = load_manifest(args.manifest)
        result.update(dataset_id=manifest["dataset_id"], manifest_sha256=hashlib.sha256(manifest_path.read_bytes()).hexdigest())
        by_id = {case["id"]: case for case in manifest["cases"]}
        selected = list(dict.fromkeys(args.case_id or by_id))
        unknown = [case_id for case_id in selected if case_id not in by_id]
        if unknown:
            raise RunnerError(f"unknown case IDs: {', '.join(unknown)}")
        # Verify every selected input before creating any case.
        for case_id in selected:
            for material in by_id[case_id]["materials"]:
                material_bytes(material)
        settings = read_dotenv(args.env_file)
        settings.update(os.environ)
        api = API(args.base or settings.get("API_BASE_URL", "http://127.0.0.1:8080"), args.request_timeout)
        session = api.request("POST", "/api/session", {"username": args.username or settings.get("DEMO_USERNAME", "demo-admin"), "pass" + "word": settings.get("DEMO_PASSWORD", "admin-demo")})
        if not session.get("actor", {}).get("id"):
            raise RunnerError("login response lacks actor ID")
        result["actor_id"] = session["actor"]["id"]
        save(result_path, result)
        for case_id in selected:
            case = by_id[case_id]
            current = {"manifest_case_id": case_id, "title": case["title"], "status": "creating", "started_at": dt.datetime.now(dt.timezone.utc).isoformat(), "materials": []}
            result["cases"].append(current)
            save(result_path, result)
            created = api.request("POST", "/api/cases", {"title": case["title"], "contract_type": case["contract_type"]})
            current["case_id"] = created.get("id")
            if not current["case_id"]:
                raise RunnerError("case creation returned no ID")
            prefix = f"/api/cases/{current['case_id']}"
            current["status"] = "uploading"
            save(result_path, result)
            for material in case["materials"]:
                item = {key: material[key] for key in ("role", "path", "sha256", "data_class", "source")}
                item["original_path"] = material["_resolved"]
                item["status"] = "uploading"
                current["materials"].append(item)
                save(result_path, result)
                uploaded = api.upload(current["case_id"], material)
                item["document_id"] = uploaded.get("id")
                if not item["document_id"]:
                    raise RunnerError("upload returned no document ID")
                current["case_detail"] = api.request("GET", prefix)
                doc = next((doc for doc in current["case_detail"].get("documents", []) if doc.get("id") == item["document_id"]), {})
                item["uploaded_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
                item["parse_status"] = doc.get("parse_status")
                save(result_path, result)
                if item["parse_status"] == "pending":
                    item["status"] = "parsing"
                    item["parse_started_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
                    save(result_path, result)
                    item["parse_response"] = api.request("POST", f"{prefix}/documents/{item['document_id']}/parse", {})
                    item["parse_finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
                    current["case_detail"] = api.request("GET", prefix)
                    doc = next((doc for doc in current["case_detail"].get("documents", []) if doc.get("id") == item["document_id"]), {})
                    item["parse_status"] = doc.get("parse_status")
                if item["parse_status"] != "ready":
                    raise RunnerError(f"document {item['document_id']} parse status: {item['parse_status']}")
                item["status"] = "parsed"
                save(result_path, result)
            current["status"] = "starting_review"
            save(result_path, result)
            current["review_started_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
            accepted = api.request("POST", f"{prefix}/reviews", {})
            current["accepted"] = accepted
            current["run_id"] = accepted.get("id")
            current["attempt_id"] = accepted.get("attempt_id")
            if not current["run_id"]:
                raise RunnerError("review request returned no run ID")
            current["status"] = "reviewing"
            save(result_path, result)
            deadline = time.monotonic() + args.review_timeout
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise RunnerError(f"review {current['run_id']} timed out")
                workspace = api.request("GET", f"{prefix}/reviews/{current['run_id']}", timeout=min(args.request_timeout, remaining))
                current["workspace"] = workspace
                current["attempt_id"] = current["attempt_id"] or (workspace.get("execution") or {}).get("attempt", {}).get("id")
                state = workspace.get("run", {}).get("state")
                current["run_state"] = state
                save(result_path, result)
                if not state:
                    raise RunnerError("review workspace has no state")
                if state not in PENDING:
                    break
                time.sleep(min(0.5, max(0, deadline - time.monotonic())))
            current["review_finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
            if state not in {"completed", "awaiting_human"}:
                raise RunnerError(f"review ended in {state}")
            execution = workspace.get("execution") or {}
            attempt = execution.get("attempt") or {}
            events = execution.get("events") or []
            if attempt.get("state") != "completed" or not any(event.get("node_kind") == "model_call" and event.get("event_type") == "model_completed" for event in events):
                raise RunnerError("review lacks a completed model execution")
            current["status"] = "complete"
            current["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
            save(result_path, result)
        result["status"] = "complete"
    except Exception as exc:
        if current is not None:
            current["failed_stage"] = current["status"]
            current["status"] = "failed"
            if current.get("case_id"):
                try:
                    current["case_detail"] = api.request("GET", f"/api/cases/{current['case_id']}")
                except Exception:
                    current["case_detail_refresh"] = "failed"
        result["status"] = "failed"
        # Do not persist exception text: upstream error bodies may contain secrets.
        result["error_type"] = type(exc).__name__
        if isinstance(exc, RunnerError):
            result["error"] = str(exc)
        save(result_path, result)
        raise
    finally:
        result["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
        if args.model_record_dir:
            try:
                result["model_record_count"] = snapshot_model_calls(args.model_record_dir, output, result["started_at"], result["finished_at"])
                if result["status"] == "complete" and result["model_record_count"] == 0:
                    raise RunnerError("completed review has no recorded model call")
            except (OSError, ValueError, KeyError, RunnerError) as exc:
                result["model_record_error"] = type(exc).__name__
                if result["status"] == "complete":
                    result["status"] = "failed"
                    save(result_path, result)
                    raise RunnerError("model call recordings could not be saved") from exc
        save(result_path, result)
    return output


def positive(value):
    try:
        number = float(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("must be a positive number") from exc
    if not 0 < number < float("inf"):
        raise argparse.ArgumentTypeError("must be a positive finite number")
    return number


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--case-id", action="append", help="manifest case ID; repeat to select multiple")
    parser.add_argument("--base", help="API base URL; defaults to API_BASE_URL")
    parser.add_argument("--username", help="login name; defaults to DEMO_USERNAME")
    parser.add_argument("--env-file", type=Path, default=Path(".env"))
    parser.add_argument("--review-timeout", type=positive, default=900)
    parser.add_argument("--request-timeout", type=positive, default=600)
    parser.add_argument("--model-record-dir", required=True, type=Path, help="independent model recorder output directory")
    parser.add_argument("--model-record-time", help="independent model recorder time identifier")
    args = parser.parse_args()
    args.model_record_dir = args.model_record_dir.expanduser()
    if not args.model_record_dir.is_dir():
        parser.error("--model-record-dir must be an existing directory")
    try:
        run(args)
        return 0
    except (RunnerError, OSError, ValueError) as exc:
        print(f"run_real_corpus: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

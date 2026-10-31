#!/usr/bin/env python3
"""Exercise case authorization against a running API using separate demo sessions."""
import argparse
import datetime as dt
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid

from run_real_corpus import RunnerError, load_manifest, material_bytes, save
from seed_telecom import SeedError, read_dotenv

ROOT = Path(__file__).resolve().parents[1]
# Local demo credentials are deliberately confined to requests and never recorded.
ACCOUNTS = {
    "demo-admin": ("admin-demo", "admin", "local-demo", "demo-org"),
    "demo-clerk": ("clerk-demo", "clerk", "local-demo", "demo-org"),
    "demo-reviewer": ("reviewer-demo", "reviewer", "local-demo", "demo-org"),
    "demo-viewer": ("viewer-demo", "viewer", "local-demo", "demo-org"),
    "demo-otherorg-clerk": ("otherorg-clerk-demo", "clerk", "local-demo", "demo-other-org"),
    "demo-tenant2-admin": ("tenant2-admin-demo", "admin", "local-demo-2", "demo-org-2"),
    "demo-tenant2-clerk": ("tenant2-clerk-demo", "clerk", "local-demo-2", "demo-org-2"),
    "demo-tenant2-reviewer": ("tenant2-reviewer-demo", "reviewer", "local-demo-2", "demo-org-2"),
    "demo-tenant2-viewer": ("tenant2-viewer-demo", "viewer", "local-demo-2", "demo-org-2"),
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, base):
        self.base = base
        parsed = urllib.parse.urlsplit(base)
        self.origin = f"{parsed.scheme}://{parsed.netloc}"
        self.opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, method, path, payload=None, content_type=None):
        headers = {"Accept": "application/json", "Origin": self.origin}
        if payload is not None and not isinstance(payload, bytes):
            payload = json.dumps(payload).encode()
            content_type = "application/json"
        if content_type:
            headers["Content-Type"] = content_type
        request = urllib.request.Request(self.base + path, payload, headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()


def multipart(data, role, filename):
    boundary = uuid.uuid4().hex
    filename = filename.replace('\\', '\\\\').replace('"', '\\"')
    if any(ord(char) < 32 for char in filename):
        raise RunnerError("invalid material filename")
    prefix = (f'--{boundary}\r\nContent-Disposition: form-data; name="role"\r\n\r\n{role}\r\n'
              f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
              'Content-Type: application/octet-stream\r\n\r\n').encode()
    return prefix + data + f"\r\n--{boundary}--\r\n".encode(), f"multipart/form-data; boundary={boundary}"


def json_object(body):
    try:
        value = json.loads(body)
    except (ValueError, UnicodeError):
        return {}
    return value if isinstance(value, dict) else {}


class Journey:
    def __init__(self, path):
        self.path = path
        self.result = {"status": "RUNNING", "started_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                       "cases": {}, "checks": []}
        self.checkpoint()

    def checkpoint(self):
        save(self.path, self.result)

    def request(self, label, client, method, path, expected, payload=None, content_type=None, predicate=None):
        try:
            status, body = client.request(method, path, payload, content_type)
        except (OSError, TimeoutError, urllib.error.URLError) as error:
            self.result["checks"].append({"name": label, "status": "FAIL", "http_status": None,
                                          "error": type(error).__name__})
            self.checkpoint()
            raise RunnerError(f"{label}: request failed ({type(error).__name__})") from error
        passed = status in expected and (predicate is None or predicate(body))
        self.result["checks"].append({"name": label, "status": "PASS" if passed else "FAIL",
                                      "http_status": status, "expected_http_status": sorted(expected)})
        self.checkpoint()
        return passed, body

    def require(self, *args, **kwargs):
        passed, body = self.request(*args, **kwargs)
        if not passed:
            raise RunnerError(f"{args[0]}: prerequisite failed")
        return json_object(body)


def valid_base(base):
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or getattr(parsed, "pass" + "word") or parsed.query or parsed.fragment:
        raise RunnerError("invalid API base URL")
    return base.rstrip("/")


def run(args):
    delivery = Path(os.environ.get("CAW_DELIVERY_DIR", ROOT.parent / "contract-audit-delivery")).expanduser().resolve()
    if delivery == ROOT or delivery.is_relative_to(ROOT):
        raise RunnerError("delivery directory must be outside the source repository")
    parent = delivery / "test-runs"
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    parent = parent.resolve()
    if parent == ROOT or parent.is_relative_to(ROOT):
        raise RunnerError("test run directory must be outside the source repository")
    output = Path(tempfile.mkdtemp(prefix="real-access-", dir=parent))
    journey = Journey(output / "result.json")
    print(f"output: {output}", flush=True)
    try:
        settings = read_dotenv(args.env_file)
        settings.update(os.environ)
        base = valid_base(args.base or settings.get("API_BASE_URL", "http://127.0.0.1:8080"))
        if args.manifest:
            _, manifest = load_manifest(args.manifest)
            material = manifest["cases"][0]["materials"][0]
            data = material_bytes(material)
            role, filename = material["role"], Path(material["_resolved"]).name
            journey.result["material"] = {"kind": "original", "sha256": material["sha256"]}
        else:
            data = b"Authorization verification material; no contract content.\n"
            role, filename = "attachment", "access-check.txt"
            journey.result["material"] = {"kind": "authorization fixture", "sha256": hashlib.sha256(data).hexdigest()}
        upload, content_type = multipart(data, role, filename)
        clients = {}
        for username, (secret, user_role, tenant, org) in ACCOUNTS.items():
            client = Client(base)
            client.username = username
            expected_actor = {"id": username, "role": user_role, "tenant_id": tenant, "org_id": org}
            journey.require(f"login:{username}", client, "POST", "/api/session", {200},
                            {"username": username, "pass" + "word": secret},
                            predicate=lambda body, expected_actor=expected_actor: all(
                                json_object(body).get("actor", {}).get(k) == v for k, v in expected_actor.items()))
            clients[username] = client
        admin = clients["demo-admin"]
        tenant2 = clients["demo-tenant2-admin"]
        for name, client in (("primary", admin), ("tenant2", tenant2)):
            created = journey.require(f"create:{name}", client, "POST", "/api/cases", {201},
                                      {"title": f"Access verification {name} {uuid.uuid4().hex}", "contract_type": "unspecified"},
                                      predicate=lambda body: isinstance(json_object(body).get("id"), str) and bool(json_object(body)["id"]))
            journey.result["cases"][name] = created["id"]
            journey.checkpoint()
        case_id = journey.result["cases"]["primary"]
        other_id = journey.result["cases"]["tenant2"]
        prefix = f"/api/cases/{case_id}"
        document = journey.require("upload:primary", admin, "POST", prefix + "/documents", {201}, upload, content_type,
                                   predicate=lambda body: isinstance(json_object(body).get("id"), str) and bool(json_object(body)["id"]))
        doc_id = document["id"]
        journey.result["document_id"] = doc_id
        journey.checkpoint()
        original = prefix + f"/documents/{doc_id}/original"
        tenant2_prefix = f"/api/cases/{other_id}"
        other_upload, other_type = multipart(b"Second tenant access verification.\n", "attachment", "tenant2-access.txt")
        other_document = journey.require("tenant2:upload", tenant2, "POST", tenant2_prefix + "/documents", {201}, other_upload, other_type,
                                        predicate=lambda body: isinstance(json_object(body).get("id"), str) and bool(json_object(body)["id"]))
        journey.result["tenant2_document_id"] = other_document["id"]
        journey.checkpoint()
        other_original = tenant2_prefix + f"/documents/{other_document['id']}/original"
        journey.request("tenant2:own-original", tenant2, "GET", other_original, {200},
                        predicate=lambda body: body == b"Second tenant access verification.\n")
        journey.request("owner:original", admin, "GET", original, {200}, predicate=lambda body: body == data)
        def listed(body, wanted, present):
            items = json_object(body).get("items")
            return isinstance(items, list) and all(isinstance(item, dict) for item in items) and ((wanted in [item.get("id") for item in items]) == present)
        def list_check(label, client, wanted, present):
            journey.request(label, client, "GET", "/api/cases", {200}, predicate=lambda body: listed(body, wanted, present))
        def blocked(label, client, target, orig=None):
            journey.request(label + ":case", client, "GET", target, {404})
            if orig:
                journey.request(label + ":original", client, "GET", orig, {404})
            journey.request(label + ":write", client, "POST", target + "/members", {403, 404}, {"actor_id": client.username})
        for username in ACCOUNTS:
            if username == "demo-admin":
                continue
            client = clients[username]
            list_check(f"before:{username}:list", client, case_id, False)
            blocked(f"before:{username}", client, prefix, original)
        for username in ("demo-clerk", "demo-reviewer", "demo-viewer"):
            journey.require(f"add:{username}", admin, "POST", prefix + "/members", {200}, {"actor_id": username})
        for username in ("demo-clerk", "demo-reviewer", "demo-viewer"):
            client = clients[username]
            list_check(f"member:{username}:list", client, case_id, True)
            journey.request(f"member:{username}:case", client, "GET", prefix, {200},
                            predicate=lambda body: json_object(body).get("id") == case_id)
            journey.request(f"member:{username}:original", client, "GET", original, {200}, predicate=lambda body: body == data)
            if username == "demo-clerk":
                journey.request("member:clerk:decision-write", client, "POST", prefix + "/remediations", {403}, {})
                fixture, fixture_type = multipart(b"Access verification attachment.\n", "attachment", "access-check-clerk.txt")
                journey.request("member:clerk:upload", client, "POST", prefix + "/documents", {201}, fixture, fixture_type)
            else:
                journey.request(f"member:{username}:upload-write", client, "POST", prefix + "/documents", {403}, upload, content_type)
                journey.request(f"member:{username}:member-write", client, "POST", prefix + "/members", {403}, {"actor_id": "demo-otherorg-clerk"})
        for username in ("demo-tenant2-admin", "demo-tenant2-clerk", "demo-tenant2-reviewer", "demo-tenant2-viewer"):
            journey.request(f"cross-tenant:add:{username}", admin, "POST", prefix + "/members", {422}, {"actor_id": username})
            list_check(f"cross-tenant:after-add:{username}:list", clients[username], case_id, False)
            blocked(f"cross-tenant:after-add:{username}", clients[username], prefix, original)
        for username in ("demo-admin", "demo-clerk", "demo-reviewer", "demo-viewer", "demo-otherorg-clerk"):
            list_check(f"other-case:{username}:list", clients[username], other_id, False)
            blocked(f"other-case:{username}", clients[username], tenant2_prefix, other_original)
        list_check("tenant2:own-list", tenant2, other_id, True)
        journey.request("tenant2:own-case", tenant2, "GET", f"/api/cases/{other_id}", {200},
                        predicate=lambda body: json_object(body).get("id") == other_id)
        journey.require("add:other-org", admin, "POST", prefix + "/members", {200}, {"actor_id": "demo-otherorg-clerk"})
        other_org = clients["demo-otherorg-clerk"]
        list_check("member:other-org:list", other_org, case_id, True)
        journey.request("member:other-org:original", other_org, "GET", original, {200}, predicate=lambda body: body == data)
        for username in ("demo-tenant2-clerk", "demo-tenant2-reviewer", "demo-tenant2-viewer"):
            journey.require(f"tenant2:add:{username}", tenant2, "POST", tenant2_prefix + "/members", {200}, {"actor_id": username})
            client = clients[username]
            list_check(f"tenant2:member:{username}:list", client, other_id, True)
            journey.request(f"tenant2:member:{username}:case", client, "GET", tenant2_prefix, {200},
                            predicate=lambda body: json_object(body).get("id") == other_id)
            journey.request(f"tenant2:member:{username}:original", client, "GET", other_original, {200},
                            predicate=lambda body: body == b"Second tenant access verification.\n")
            if username != "demo-tenant2-clerk":
                journey.request(f"tenant2:member:{username}:member-write", client, "POST", tenant2_prefix + "/members", {403}, {"actor_id": "demo-tenant2-admin"})
        journey.result["status"] = "FAIL" if any(c["status"] == "FAIL" for c in journey.result["checks"]) else "PASS"
    except (RunnerError, SeedError, OSError, ValueError, urllib.error.URLError) as error:
        journey.result["status"] = "FAIL"
        journey.result["error"] = str(error) if isinstance(error, (RunnerError, SeedError)) else type(error).__name__
    finally:
        journey.result["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
        journey.checkpoint()
    return journey.result["status"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", help="API base URL")
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    parser.add_argument("--manifest", type=Path, help="real corpus manifest; uploads its first original")
    args = parser.parse_args()
    try:
        status = run(args)
    except (RunnerError, OSError, ValueError) as error:
        print(f"access check failed: {type(error).__name__}", file=sys.stderr)
        return 1
    print(status)
    return 0 if status == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())

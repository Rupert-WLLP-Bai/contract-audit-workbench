#!/usr/bin/env python3
"""D2-7a real OIDC harness: an isolated Keycloak provider for either a
throwaway application validation or a synthetic local C2 host identity setup.

This harness never pretends a local signed httptest is a real provider. It runs
the real authorization-code + PKCE flow through the provider's own login page,
against the real application build, and records only what the provider and the
application actually answered:

- provider: an isolated Keycloak container started from the image pinned in the
  repository .env (KEYCLOAK_IMAGE), on its own loopback port, with its own
  generated admin password, realm, two confidential clients (client and admin)
  and two users (one platform admin, one viewer);
- application validation: a separate `cmd/server` process with APP_ENV=test, its own
  database `oidc_d2_7a_<run>` created in the local PostgreSQL container, its own
  DATA_DIR and a private 0600 environment file; the currently running stack and
  its environment files are never modified;
- host-provider mode: a loopback-only Keycloak realm, a private trusted directory
  preserving the four demo IDs plus one synthetic second-tenant clerk, and a
  private host.env fragment; it does not touch the running host processes;
- checks: authorization redirects for both audiences, real login through the
  provider form, server-side directory mapping (tenant/org/role from the
  operator directory, never from unverified claims), client/admin cookie
  separation and refusals, unknown-subject refusal, logout, and directory
  revocation reload.

Every secret is generated locally, written only to 0600 files under the run
directory, and never printed or recorded: the evidence log keeps cookie names
and redacted commands only.

Without a cached provider image the harness refuses with the exact missing
prerequisite instead of faking a provider:

  --plan                      print the isolated-app plan and prerequisites
  --run                       execute isolated-app validation (requires provider image)
  --provider-only             run a local issuer and emit host identities/config (no host changes)
  --stop --out-dir DIR        remove only this run's owned provider container
  --emit-config --out-dir DIR generate placeholder env + directory shape without running
"""
from __future__ import annotations

import argparse
import base64
import datetime as dt
import hashlib
from http.cookiejar import CookieJar, DefaultCookiePolicy
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
RUN_ROOT = Path("/tmp/contract-audit-oidc")
DEFAULT_PROVIDER_IMAGE = "quay.io/keycloak/keycloak:26.4.0"
DEFAULT_PG_CONTAINER = "contract-audit-local-db-1"
OWNED_LABEL = "contract-audit-oidc=owned"
CLIENT_ID = "contract-audit-client"
ADMIN_CLIENT_ID = "contract-audit-admin"


class Failure(RuntimeError):
    """A guard, provider, application or assertion step did not hold."""


def read_dotenv(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.is_file():
        return values
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise Failure(f"invalid dotenv syntax at {path}:{number}")
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        values[key] = value
    return values


class Recorder:
    """Command evidence with secrets redacted and never printed."""

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
            if re.fullmatch(r"(?i).*(password|secret|token|key)=.+", text):
                text = text.split("=", 1)[0] + "=<redacted>"
            redacted.append(text)
        return redacted

    def redact_text(self, text: str) -> str:
        for secret in self.secrets:
            text = text.replace(secret, "<redacted>")
        return text

    def run(self, argv: list[str], timeout: float = 300, env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
        started = time.monotonic()
        process = subprocess.run(argv, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                 stdin=subprocess.DEVNULL, timeout=timeout)
        self.records.append({"argv": self.redact(argv), "exit": process.returncode,
                             "seconds": round(time.monotonic() - started, 3),
                             "stdout_tail": self.redact_text(process.stdout.decode("utf-8", "replace"))[-1500:],
                             "stderr_tail": self.redact_text(process.stderr.decode("utf-8", "replace"))[-1500:]})
        return process

    def note(self, message: str) -> None:
        self.records.append({"argv": ["<internal>"], "exit": 0, "seconds": 0,
                             "stdout_tail": message[-1500:], "stderr_tail": ""})

    def flush(self) -> None:
        if self.path is None:
            return
        with self.path.open("w", encoding="utf-8") as stream:
            for record in self.records:
                stream.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")


def free_port() -> int:
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return int(probe.getsockname()[1])


def docker_image_present(recorder: Recorder, image: str) -> bool:
    return recorder.run(["docker", "image", "inspect", "--format", "{{.Id}}", image], timeout=60).returncode == 0


def container_owned(recorder: Recorder, container_id: str, run_id: str, name: str) -> bool:
    process = recorder.run([
        "docker", "inspect", "--format",
        '{{.Name}}|{{index .Config.Labels "contract-audit-oidc"}}|{{index .Config.Labels "oidc-run"}}',
        container_id,
    ], timeout=60)
    return process.returncode == 0 and process.stdout.decode().strip() == f"/{name}|owned|{run_id}"


def http(method: str, url: str, *, data: bytes | None = None, headers: dict[str, str] | None = None,
         opener: urllib.request.OpenerDirector | None = None, timeout: float = 20) -> tuple[int, dict[str, str], bytes]:
    request = urllib.request.Request(url, data, headers=headers or {}, method=method)
    client = opener or urllib.request.build_opener()
    try:
        with client.open(request, timeout=timeout) as response:
            return response.status, dict(response.headers), response.read()
    except urllib.error.HTTPError as error:
        return error.code, dict(error.headers), error.read()


def no_redirect_opener() -> urllib.request.OpenerDirector:
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None

    class LoopbackCookiePolicy(DefaultCookiePolicy):
        def return_ok_secure(self, cookie, request):
            # Keycloak marks loopback cookies Secure, as browsers treat this
            # host as a secure context. Keep normal policy for every other host.
            target = urllib.parse.urlsplit(request.full_url)
            if target.scheme == "http" and target.hostname == "127.0.0.1":
                return True
            return super().return_ok_secure(cookie, request)

    return urllib.request.build_opener(
        NoRedirect, urllib.request.HTTPCookieProcessor(CookieJar(policy=LoopbackCookiePolicy())))


def cookie_jar(opener: urllib.request.OpenerDirector) -> CookieJar:
    for handler in opener.handlers:
        if isinstance(handler, urllib.request.HTTPCookieProcessor):
            return handler.cookiejar
    raise Failure("opener has no cookie jar")


def cookie_header(opener: urllib.request.OpenerDirector) -> str:
    return "; ".join(f"{cookie.name}={cookie.value}" for cookie in cookie_jar(opener))


def cookies_named(opener: urllib.request.OpenerDirector) -> list[str]:
    return sorted({cookie.name for cookie in cookie_jar(opener)})


def build_directory(issuer: str, realm_users: list[dict[str, object]]) -> dict[str, object]:
    """The operator directory the application must trust for issuer+subject."""
    users = []
    for entry in realm_users:
        role = str(entry["role"])
        grant_role = "case_owner" if role in ("admin", "clerk") else role
        grant_scope = "tenant" if role in ("reviewer", "viewer") else "self"
        users.append({
            "id": entry["id"],
            "display_name": entry["display_name"],
            "role": role,
            "tenant_id": entry["tenant_id"],
            "org_id": entry["org_id"],
            "enabled": entry["enabled"],
            "grants": [{"role": grant_role, "scope": grant_scope}],
            "issuer": issuer,
            "subject": entry["subject"],
        })
    return {"users": users}


def validate_directory(document: dict) -> None:
    """Mirror the application's readDirectory rules before anything runs."""
    if set(document) != {"users"} or not isinstance(document["users"], list) or not document["users"]:
        raise Failure("directory must contain exactly one non-empty users array")
    seen_ids: set[str] = set()
    seen_subjects: set[tuple[str, str]] = set()
    allowed_roles = {"admin", "clerk", "reviewer", "viewer"}
    for user in document["users"]:
        if not isinstance(user, dict):
            raise Failure("directory users must be objects")
        required = {"id", "display_name", "role", "tenant_id", "org_id", "enabled", "grants", "issuer", "subject"}
        if set(user) - required or not required <= set(user):
            raise Failure(f"directory user has unexpected or missing fields: {sorted(user)}")
        if not all(isinstance(user[field], str) and user[field] for field in
                   ("id", "display_name", "role", "tenant_id", "org_id", "issuer", "subject")):
            raise Failure("directory user fields must be non-empty strings")
        if user["role"] not in allowed_roles:
            raise Failure(f"directory user role {user['role']!r} is not supported")
        if not isinstance(user["enabled"], bool):
            raise Failure("directory user enabled must be a boolean")
        if not isinstance(user["grants"], list) or not user["grants"]:
            raise Failure("directory user grants must be a non-empty list")
        allowed_grant_fields = {"role", "scope", "org_id", "case_id", "resolved_org_ids", "expires_at"}
        allowed_grant_roles = {"case_owner", "reviewer", "viewer", "exporter", "tenant_admin", "platform_admin"}
        for grant in user["grants"]:
            if not isinstance(grant, dict) or not grant or set(grant) - allowed_grant_fields:
                raise Failure(f"directory user {user['id']!r} has an invalid grant object")
            if (not isinstance(grant.get("role"), str) or grant["role"] not in allowed_grant_roles
                    or not isinstance(grant.get("scope"), str)):
                raise Failure(f"directory user {user['id']!r} has an invalid grant role or scope")
            scope = grant["scope"]
            if scope not in {"self", "tenant", "org", "org_subtree", "case"}:
                raise Failure(f"directory user {user['id']!r} has an unsupported grant scope")
            if scope == "case" and (not isinstance(grant.get("case_id"), str) or not grant["case_id"].strip()):
                raise Failure(f"directory user {user['id']!r} has an empty case grant")
            if scope == "org" and (not isinstance(grant.get("org_id"), str) or not grant["org_id"].strip()):
                raise Failure(f"directory user {user['id']!r} has an empty organization grant")
            if scope == "org_subtree" and (not isinstance(grant.get("resolved_org_ids"), list)
                    or not grant["resolved_org_ids"]
                    or any(not isinstance(value, str) or not value.strip() for value in grant["resolved_org_ids"])):
                raise Failure(f"directory user {user['id']!r} has an unresolved organization subtree grant")
            for field in ("org_id", "case_id"):
                if field in grant and (not isinstance(grant[field], str) or not grant[field].strip()):
                    raise Failure(f"directory user {user['id']!r} has an empty {field} grant key")
            if "resolved_org_ids" in grant and (not isinstance(grant["resolved_org_ids"], list)
                    or any(not isinstance(value, str) or not value.strip() for value in grant["resolved_org_ids"])):
                raise Failure(f"directory user {user['id']!r} has invalid resolved organization ids")
            if "expires_at" in grant:
                if not isinstance(grant["expires_at"], str):
                    raise Failure(f"directory user {user['id']!r} has an invalid grant expiry")
                try:
                    dt.datetime.fromisoformat(grant["expires_at"].replace("Z", "+00:00"))
                except ValueError as error:
                    raise Failure(f"directory user {user['id']!r} has an invalid grant expiry") from error
        if user["id"] in seen_ids:
            raise Failure("directory user ids must be unique")
        seen_ids.add(user["id"])
        key = (user["issuer"], user["subject"])
        if key in seen_subjects:
            raise Failure("directory issuer+subject pairs must be unique")
        seen_subjects.add(key)
    encoded = json.dumps(document, ensure_ascii=False).encode()
    if len(encoded) > 1 << 20:
        raise Failure("directory file would exceed the application's 1 MiB limit")


def generate_material(out_dir: Path, provider_admin_user: str) -> dict[str, object]:
    """Create the private env file, the placeholder directory and the secrets.

    Directory subjects are filled in after the provider users exist; the file is
    rewritten then. Everything is 0600 and never printed.
    """
    out_dir.mkdir(parents=True, exist_ok=True)
    run_id = out_dir.name
    state_key = base64.b64encode(secrets.token_bytes(32)).decode()
    material = {
        "run_id": run_id,
        "provider_admin_user": provider_admin_user,
        "provider_admin_password": secrets.token_urlsafe(24),
        "client_secret": secrets.token_urlsafe(32),
        "admin_client_secret": secrets.token_urlsafe(32),
        "admin_password": secrets.token_urlsafe(20),
        "viewer_password": secrets.token_urlsafe(20),
        "state_key": state_key,
    }
    return material


def write_private(path: Path, text: str) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        stream.write(text)
    path.chmod(0o600)


def build_app_env(material: dict[str, object], *, schema: str, postgres_dsn: str,
                  app_port: int, data_dir: Path, directory_path: Path, issuer: str) -> dict[str, str]:
    return {
        "APP_ENV": "test",
        "ADDR": f"127.0.0.1:{app_port}",
        "DATA_DIR": str(data_dir),
        "DATABASE_SCHEMA": schema,
        "APP_DATABASE_MODE": "embedded",
        "EXECUTION_MODE": "embedded",
        "COOKIE_SECURE": "false",
        "SERVICE_DATABASE_ROLES": "false",
        "TEST_DATABASE_URL": postgres_dsn,
        "MIGRATOR_DATABASE_URL": postgres_dsn,
        "OIDC_ISSUER": issuer,
        "OIDC_CLIENT_ID": CLIENT_ID,
        "OIDC_CLIENT_SECRET": str(material["client_secret"]),
        "OIDC_ADMIN_CLIENT_ID": ADMIN_CLIENT_ID,
        "OIDC_ADMIN_CLIENT_SECRET": str(material["admin_client_secret"]),
        "OIDC_REDIRECT_URL": f"http://127.0.0.1:{app_port}/api/auth/oidc/callback",
        "OIDC_STATE_KEY": str(material["state_key"]),
        "IDENTITY_DIRECTORY_FILE": str(directory_path),
        "LLM_BASE_URL": "",
        "LLM_MODEL": "fixture-chat",
        "OCR_MODEL": "fixture-ocr",
    }


def kcadm(recorder: Recorder, container: str, admin_user: str, admin_password: str,
          arguments: list[str], timeout: float = 120) -> subprocess.CompletedProcess:
    environment = dict(os.environ)
    environment["KC_ADMIN_USER"] = admin_user
    environment["KC_ADMIN_PASSWORD"] = admin_password
    argv = ["docker", "exec", "-e", "KC_ADMIN_USER", "-e", "KC_ADMIN_PASSWORD", container,
            "/opt/keycloak/bin/kcadm.sh", *arguments]
    return recorder.run(argv, timeout=timeout, env=environment)


def parse_form(html: str) -> tuple[str, dict[str, str]]:
    """Extract the provider login form action and its hidden inputs."""
    action = re.search(r'<form[^>]+id="kc-form-login"[^>]+action="([^"]+)"', html)
    if not action:
        action = re.search(r'<form[^>]+action="([^"]+)"', html)
    if not action:
        raise Failure("the provider login page has no form action")
    hidden = {name: value for name, value in re.findall(
        r'<input[^>]+type="hidden"[^>]*name="([^"]+)"[^>]*value="([^"]*)"', html)}
    hidden.update({name: value for name, value in re.findall(
        r'<input[^>]+name="([^"]+)"[^>]*type="hidden"[^>]*value="([^"]*)"', html)})
    return action.group(1).replace("&amp;", "&"), hidden


def provider_login(opener: urllib.request.OpenerDirector, authorization_url: str, username: str,
                   password: str) -> tuple[int, dict[str, str]]:
    """Walk the provider's own login page with a real form POST."""
    status, headers, body = http("GET", authorization_url, opener=opener)
    for _ in range(6):
        if status in (301, 302, 303, 307, 308) and headers.get("Location"):
            status, headers, body = http("GET", urllib.parse.urljoin(authorization_url, headers["Location"]),
                                         opener=opener)
            continue
        if status == 200:
            break
        raise Failure(f"provider login page returned HTTP {status}")
    action, hidden = parse_form(body.decode("utf-8", "replace"))
    form = dict(hidden)
    form["username"] = username
    form["password"] = password
    if "credentialId" in form and not form["credentialId"]:
        form.pop("credentialId")
    request = urllib.request.Request(action, urllib.parse.urlencode(form).encode(),
                                     headers={"Content-Type": "application/x-www-form-urlencoded",
                                              "Accept": "text/html"}, method="POST")
    try:
        with opener.open(request, timeout=20) as response:
            return response.status, dict(response.headers)
    except urllib.error.HTTPError as error:
        return error.code, dict(error.headers)


def oidc_login_flow(app: str, issuer: str, audience: str, username: str, password: str,
                    recorder: Recorder) -> tuple[dict[str, object], urllib.request.OpenerDirector]:
    """One real end-to-end login: app redirect, provider form, app callback."""
    opener = no_redirect_opener()
    step: dict[str, object] = {"audience": audience, "user": username}
    status, headers, _ = http("GET", f"{app}/api/auth/oidc/login?audience={audience}", opener=opener)
    step["login_status"] = status
    step["login_location_host"] = urllib.parse.urlsplit(headers.get("Location", "")).hostname or ""
    if status != 302 or not headers.get("Location"):
        step["error"] = "the application did not redirect to the provider"
        return step, opener
    step["state_cookie_present"] = any(cookie.name.startswith("oidc_state") for cookie in cookie_jar(opener))
    status, headers = provider_login(opener, headers["Location"], username, password)
    location = headers.get("Location", "")
    step["provider_form_status"] = status
    step["callback_location"] = urllib.parse.urlsplit(location).path
    if not location:
        step["error"] = "the provider did not redirect back to the application"
        return step, opener
    status, headers, _ = http("GET", location, opener=opener)
    step["callback_status"] = status
    step["callback_destination"] = headers.get("Location", "")
    step["cookies"] = cookies_named(opener)
    if status in (301, 302, 303):
        status, headers, _ = http("GET", urllib.parse.urljoin(app, headers.get("Location", "/")), opener=opener)
    audience_query = "" if audience == "client" else "?audience=admin"
    status, _, body = http("GET", f"{app}/api/session{audience_query}", opener=opener)
    step["session_status"] = status
    if status == 200:
        parsed = json.loads(body.decode("utf-8", "replace"))
        actor = parsed.get("actor", {})
        step["session_audience"] = parsed.get("audience")
        step["actor"] = {"id": actor.get("id"), "tenant_id": actor.get("tenant_id"),
                         "org_id": actor.get("org_id"), "role": actor.get("role")}
    recorder.note(f"oidc flow audience={audience} user={username} callback={step.get('callback_status')} "
                  f"session={step.get('session_status')} cookies={step.get('cookies')}")
    return step, opener


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--provider-image", default="")
    parser.add_argument("--pg-container", default=DEFAULT_PG_CONTAINER)
    parser.add_argument("--out-dir", default="")
    parser.add_argument("--plan", action="store_true")
    parser.add_argument("--run", action="store_true")
    parser.add_argument("--playwright", action="store_true", help="also run the real Keycloak Chromium functional journeys during --run")
    parser.add_argument("--provider-only", action="store_true")
    parser.add_argument("--redirect-url", default="http://localhost:18090/api/auth/oidc/callback")
    parser.add_argument("--stop", action="store_true")
    parser.add_argument("--emit-config", action="store_true")
    args = parser.parse_args(argv)
    if args.playwright and not args.run:
        parser.error("--playwright requires --run")
    modes = (args.plan, args.run, args.provider_only, args.stop, args.emit_config)
    if sum(modes) > 1:
        print("REFUSED: select exactly one OIDC operation", file=sys.stderr)
        return 2
    if not any(modes):
        args.plan = True

    infra_env = read_dotenv(ROOT / ".env")
    provider_image = args.provider_image or infra_env.get("KEYCLOAK_IMAGE") or DEFAULT_PROVIDER_IMAGE
    recorder = Recorder(None, set(infra_env.values()))
    if args.provider_only:
        if not docker_image_present(recorder, provider_image):
            print(f"REFUSED: the pinned provider image {provider_image} is not present locally; "
                  "no substitute issuer will be used", file=sys.stderr)
            return 2
        return run_host_provider(recorder, provider_image, args)

    if args.stop:
        if not args.out_dir:
            print("REFUSED: --stop needs --out-dir", file=sys.stderr)
            return 2
        owners = Path(args.out_dir) / "oidc-owners.tsv"
        if not owners.is_file():
            print(f"REFUSED: {owners} is missing; nothing is deleted by name", file=sys.stderr)
            return 2
        container_ids = []
        for line in owners.read_text(encoding="utf-8").splitlines():
            fields = line.split("\t")
            if len(fields) != 3:
                print("refusing malformed provider ownership record", file=sys.stderr)
                return 1
            run_id, name, container_id = fields
            if not run_id or not container_id or not container_owned(recorder, container_id, run_id, name):
                print(f"refusing to remove {name}: provider ownership does not match this record", file=sys.stderr)
                return 1
            container_ids.append(container_id)
        for container_id in container_ids:
            if recorder.run(["docker", "rm", "-f", container_id], timeout=120).returncode != 0:
                return 1
        print(f"removed {len(container_ids)} owned container(s)")
        return 0

    if args.emit_config:
        if not args.out_dir:
            print("REFUSED: --emit-config needs --out-dir", file=sys.stderr)
            return 2
        out_dir = Path(args.out_dir)
        if out_dir.exists() and any(out_dir.iterdir()):
            print(f"REFUSED: {out_dir} already holds files; choose a fresh --out-dir", file=sys.stderr)
            return 2
        issuer = "http://127.0.0.1:18899/realms/contract-audit-oidc-placeholder"
        material = generate_material(out_dir, infra_env.get("KEYCLOAK_ADMIN", "local-admin"))
        directory = build_directory(issuer, [
            {"id": "oidc-admin", "display_name": "OIDC 管理员", "role": "admin", "tenant_id": "tenant-oidc",
             "org_id": "org-oidc", "enabled": True, "subject": "placeholder-admin-subject"},
            {"id": "oidc-viewer", "display_name": "OIDC 只读", "role": "viewer", "tenant_id": "tenant-oidc",
             "org_id": "org-oidc", "enabled": True, "subject": "placeholder-viewer-subject"},
        ])
        validate_directory(directory)
        write_private(out_dir / "identity-directory.json", json.dumps(directory, ensure_ascii=False, indent=2) + "\n")
        env = build_app_env(material, schema="public",
                            postgres_dsn="postgres://contractaudit:<redacted>@127.0.0.1:5432/oidc_d2_7a_placeholder"
                                         "?sslmode=disable",
                            app_port=18898, data_dir=out_dir / "data",
                            directory_path=out_dir / "identity-directory.json", issuer=issuer)
        write_private(out_dir / "app.env", "".join(f"{key}={value}\n" for key, value in env.items()))
        report = {"directory_users": [user["id"] for user in directory["users"]],
                  "directory_bytes": (out_dir / "identity-directory.json").stat().st_size,
                  "env_keys": sorted(env),
                  "app_env_mode": oct((out_dir / "app.env").stat().st_mode & 0o777),
                  "directory_mode": oct((out_dir / "identity-directory.json").stat().st_mode & 0o777),
                  "placeholder": True,
                  "note": "the emitted DSN is a placeholder for shape validation; a real run writes the real DSN",
                  "validated": True}
        print(json.dumps(report, ensure_ascii=False, sort_keys=True))
        return 0

    plan = {
        "mode": "plan" if args.plan and not args.run else "run",
        "provider_image": provider_image,
        "provider_image_present": docker_image_present(recorder, provider_image),
        "pg_container": args.pg_container,
        "pg_container_present": recorder.run(["docker", "inspect", args.pg_container], timeout=60).returncode == 0,
        "steps": [
            "start the isolated provider container on its own loopback port with a generated admin password",
            "create realm, two confidential clients (client/admin, PKCE S256) and two users via kcadm",
            "create the isolated database and the private app env + directory file (0600)",
            "migrate and start one throwaway cmd/server instance on its own port",
            "real login: client audience as the mapped admin user, session actor from the directory",
            "admin audience login, client/admin cookie separation, viewer refused on admin",
            "unknown subject refused, logout clears the session, directory revocation is honoured",
        ],
        "isolation": {
            "containers": "owned label %s, unique names per run, --stop removes only recorded ids" % OWNED_LABEL,
            "database": "dedicated oidc_d2_7a_<run> database; the running stack is untouched",
            "files": "0600 files under the run out-dir; secrets are never printed",
        },
    }
    if args.plan and not args.run:
        print(json.dumps(plan, ensure_ascii=False, sort_keys=True))
        return 0

    if not docker_image_present(recorder, provider_image):
        print(
            f"REFUSED: the pinned provider image {provider_image} is not present locally. Exact missing "
            "prerequisite: either a locally cached Keycloak image (docker pull of that pinned ref, bounded) or an "
            "already-running real issuer to point this harness at. No local httptest is used as a substitute.",
            file=sys.stderr)
        return 2
    return run_harness(recorder, infra_env, provider_image, args)


def provider_wait_ready(issuer: str, timeout: float = 180) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            status, _, body = http("GET", issuer + "/.well-known/openid-configuration", timeout=5)
            if status == 200:
                metadata = json.loads(body.decode("utf-8", "replace"))
                if metadata.get("issuer") != issuer:
                    raise Failure("provider discovery returned a different issuer")
                return metadata
        except (OSError, ValueError, urllib.error.URLError):
            pass
        time.sleep(2)
    raise Failure("the isolated provider did not publish discovery in time")


def kcadm_json(process: subprocess.CompletedProcess) -> object:
    text = process.stdout.decode("utf-8", "replace").strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        match = re.search(r'"id"\s*:\s*"([^"]+)"', text)
        if match:
            return {"id": match.group(1)}
        raise Failure(f"kcadm did not return JSON: {text[-200:]!r}")


def kcadm_ok(recorder: Recorder, container: str, admin_user: str, admin_password: str, arguments: list[str]) -> None:
    process = kcadm(recorder, container, admin_user, admin_password, arguments)
    if process.returncode != 0:
        raise Failure(f"kcadm {arguments[:3]} failed: {process.stderr.decode('utf-8', 'replace')[-300:]}")


def bootstrap_provider(recorder: Recorder, material: dict, provider_image: str, provider_port: int,
                       realm: str, redirect_uri: str, owners: Path, *,
                       redirect_uris: list[str] | None = None,
                       realm_users: list[dict[str, object]] | None = None) -> dict:
    """Start the isolated provider and create a realm, clients and users."""
    run_id = str(material["run_id"])
    container = f"contract-audit-oidc-provider-{run_id}"
    issuer = f"http://127.0.0.1:{provider_port}/realms/{realm}"
    environment = dict(os.environ)
    environment["KC_BOOTSTRAP_ADMIN_USERNAME"] = str(material["provider_admin_user"])
    environment["KC_BOOTSTRAP_ADMIN_PASSWORD"] = str(material["provider_admin_password"])
    start = recorder.run(["docker", "run", "-d", "--name", container, "--restart", "no",
                          "-p", f"127.0.0.1:{provider_port}:8080",
                          "--label", OWNED_LABEL, "--label", f"oidc-run={run_id}",
                          "-e", "KC_BOOTSTRAP_ADMIN_USERNAME", "-e", "KC_BOOTSTRAP_ADMIN_PASSWORD",
                          provider_image, "start-dev", "--http-port=8080"], env=environment, timeout=180)
    if start.returncode != 0:
        raise Failure(f"could not start the isolated provider: {start.stderr.decode('utf-8', 'replace')[-300:]}")
    container_id = start.stdout.decode().strip()
    write_private(owners, f"{run_id}\t{container}\t{container_id}\n")
    provider_wait_ready(f"http://127.0.0.1:{provider_port}/realms/master")
    recorder.note(f"provider container {container} started as {container_id[:12]}")
    admin_user, admin_password = str(material["provider_admin_user"]), str(material["provider_admin_password"])
    kcadm_ok(recorder, container_id, admin_user, admin_password,
             ["config", "credentials", "--server", "http://127.0.0.1:8080", "--realm", "master",
              "--user", admin_user, "--password", admin_password])
    kcadm_ok(recorder, container_id, admin_user, admin_password,
             ["create", "realms", "-s", f"realm={realm}", "-s", "enabled=true"])
    clients = {"client": CLIENT_ID, "admin": ADMIN_CLIENT_ID}
    allowed_redirects = redirect_uris or [redirect_uri]
    redirect_json = json.dumps(allowed_redirects, separators=(",", ":"))
    for label, client_id in clients.items():
        secret = str(material["client_secret"] if label == "client" else material["admin_client_secret"])
        kcadm_ok(recorder, container_id, admin_user, admin_password,
                 ["create", "clients", "-r", realm,
                  "-s", f"clientId={client_id}", "-s", "enabled=true", "-s", "protocol=openid-connect",
                  "-s", "publicClient=false", "-s", "standardFlowEnabled=true",
                  "-s", "directAccessGrantsEnabled=false", "-s", f"secret={secret}",
                  "-s", f"redirectUris={redirect_json}",
                  "-s", 'attributes={"pkce.code.challenge.method":"S256"}'])
    if realm_users is None:
        users = {
            "admin": {"username": "oidc-admin", "password": str(material["admin_password"]),
                      "display_name": "OIDC 管理员", "role": "admin", "id": "oidc-admin"},
            "viewer": {"username": "oidc-viewer", "password": str(material["viewer_password"]),
                       "display_name": "OIDC 只读", "role": "viewer", "id": "oidc-viewer"},
            "unknown": {"username": "oidc-unknown", "password": secrets.token_urlsafe(20),
                        "display_name": "not in the directory", "role": "viewer", "id": "oidc-unknown"},
        }
    else:
        users = {str(entry["id"]): dict(entry) for entry in realm_users}
        if len(users) != len(realm_users) or not users:
            raise Failure("host provider users must have unique identities")
    for entry in users.values():
        username, password = str(entry["username"]), str(entry["password"])
        recorder.secrets.add(password)
        kcadm_ok(recorder, container_id, admin_user, admin_password,
                 ["create", "users", "-r", realm, "-s", f"username={username}", "-s", "enabled=true",
                  "-s", "firstName=OIDC", "-s", f"lastName={username}",
                  "-s", "emailVerified=true", "-s", f"email={username}@example.invalid"])
        kcadm_ok(recorder, container_id, admin_user, admin_password,
                 ["set-password", "-r", realm, "--username", username, "--new-password", password])
        listed = kcadm(recorder, container_id, admin_user, admin_password,
                       ["get", "users", "-r", realm, "-q", f"username={username}", "--fields", "id"])
        if listed.returncode != 0:
            raise Failure(f"could not read the provider subject for {username}")
        entry["subject"] = str(kcadm_json(listed)[0]["id"])  # type: ignore[index]
    return {"container": container, "container_id": container_id, "issuer": issuer, "realm": realm, "users": users}


def create_isolated_database(recorder: Recorder, pg_container: str, database: str) -> None:
    check = recorder.run(["docker", "exec", pg_container, "psql", "-U", "contractaudit", "-d", "postgres",
                          "-t", "-A", "-c", f"SELECT count(*) FROM pg_database WHERE datname='{database}'"],
                         timeout=60)
    if check.returncode != 0:
        raise Failure(f"cannot inspect the local PostgreSQL container: {check.stderr.decode()[-200:]}")
    if check.stdout.decode().strip() != "0":
        raise Failure(f"the isolated database {database} already exists; refusing to overwrite")
    created = recorder.run(["docker", "exec", pg_container, "psql", "-U", "contractaudit", "-d", "postgres",
                            "-v", "ON_ERROR_STOP=1", "-c", f'CREATE DATABASE "{database}" OWNER contractaudit'],
                           timeout=120)
    if created.returncode != 0:
        raise Failure(f"could not create the isolated database: {created.stderr.decode()[-200:]}")


def build_binary(recorder: Recorder, out_dir: Path, target: str, output: str) -> Path:
    path = out_dir / output
    process = recorder.run(["go", "build", "-o", str(path), target], timeout=900)
    if process.returncode != 0:
        raise Failure(f"could not build {target}: {process.stderr.decode('utf-8', 'replace')[-400:]}")
    return path


def wait_app_ready(app: str, timeout: float = 60) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            status, _, _ = http("GET", app + "/ready", timeout=3)
            if status == 200:
                return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(1)
    raise Failure("the throwaway application instance did not become ready")


def write_json(path: Path, document: object) -> None:
    write_private(path, json.dumps(document, ensure_ascii=False, indent=2, sort_keys=True) + "\n")


def run_host_provider(recorder: Recorder, provider_image: str, args) -> int:
    try:
        callback = urllib.parse.urlsplit(args.redirect_url)
        callback_port = callback.port
    except ValueError:
        print("REFUSED: the host callback URL is invalid", file=sys.stderr)
        return 2
    if (callback.scheme != "http" or callback.hostname not in ("localhost", "127.0.0.1")
            or callback_port is None or callback.path != "/api/auth/oidc/callback"
            or callback.query or callback.fragment or callback.username or callback.password):
        print("REFUSED: the host callback must be a loopback HTTP /api/auth/oidc/callback URL", file=sys.stderr)
        return 2
    run_id = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S") + "-" + secrets.token_hex(3)
    out_dir = Path(args.out_dir) if args.out_dir else RUN_ROOT / ("host-" + run_id)
    if out_dir.exists() and any(out_dir.iterdir()):
        print(f"REFUSED: {out_dir} already holds files; choose a fresh --out-dir", file=sys.stderr)
        return 2
    out_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    out_dir.chmod(0o700)
    recorder.path = out_dir / "commands.jsonl"
    owners = out_dir / "oidc-owners.tsv"
    write_private(owners, "")

    material = generate_material(out_dir, "contract-audit-local-admin")
    material["run_id"] = run_id
    recorder.secrets |= {str(value) for key, value in material.items() if key.endswith("password") or key.endswith("secret")}
    users: list[dict[str, object]] = []
    identities = (
        ("demo-admin", "admin", "local-demo", "demo-org", "演示管理员"),
        ("demo-reviewer", "reviewer", "local-demo", "demo-org", "演示审查员"),
        ("demo-clerk", "clerk", "local-demo", "demo-org", "演示经办"),
        ("demo-viewer", "viewer", "local-demo", "demo-org", "演示只读"),
        ("d2-tenant-b-clerk", "clerk", "d2-tenant-b", "d2-tenant-b-org", "D2合成租户经办"),
    )
    for user_id, role, tenant_id, org_id, display_name in identities:
        users.append({
            "id": user_id,
            "username": user_id,
            "password": secrets.token_urlsafe(24),
            "display_name": display_name,
            "role": role,
            "tenant_id": tenant_id,
            "org_id": org_id,
            "enabled": True,
        })
    recorder.secrets.update(str(user["password"]) for user in users)

    provider_port = free_port()
    realm = "contract-audit-host-" + run_id.split("-")[-1]
    report: dict[str, object] = {
        "run_id": run_id,
        "provider_image": provider_image,
        "callback_url": args.redirect_url,
        "status": "RUNNING",
    }
    try:
        provider = bootstrap_provider(recorder, material, provider_image, provider_port, realm,
                                      args.redirect_url, owners, realm_users=users)
        issuer = str(provider["issuer"])
        metadata = provider_wait_ready(issuer)
        image = recorder.run(["docker", "image", "inspect", "--format", "{{.Id}}", provider_image], timeout=60)
        if image.returncode != 0:
            raise Failure("the running provider image identity could not be recorded")
        directory = build_directory(issuer, list(provider["users"].values()))
        validate_directory(directory)
        directory_path = out_dir / "identity-directory.json"
        write_json(directory_path, directory)
        host_env = {
            "OIDC_ISSUER": issuer,
            "OIDC_CLIENT_ID": CLIENT_ID,
            "OIDC_CLIENT_SECRET": str(material["client_secret"]),
            "OIDC_ADMIN_CLIENT_ID": ADMIN_CLIENT_ID,
            "OIDC_ADMIN_CLIENT_SECRET": str(material["admin_client_secret"]),
            "OIDC_REDIRECT_URL": args.redirect_url,
            "OIDC_STATE_KEY": str(material["state_key"]),
            "IDENTITY_DIRECTORY_FILE": str(directory_path),
        }
        host_env_path = out_dir / "host.env"
        write_private(host_env_path, "".join(f"{key}={value}\n" for key, value in host_env.items()))
        credential_path = out_dir / "identity-credentials.json"
        write_json(credential_path, {
            "issuer": issuer,
            "users": [{key: user[key] for key in ("id", "username", "password", "role", "tenant_id", "org_id")}
                      for user in provider["users"].values()],
        })
        directory_bytes = directory_path.read_bytes()
        report.update({
            "status": "READY",
            "provider": {"container": provider["container"], "container_id": provider["container_id"],
                         "port": provider_port, "realm": realm, "issuer": issuer,
                         "image_id": image.stdout.decode().strip(),
                         "discovery_issuer": metadata.get("issuer")},
            "identity_directory": {"path": str(directory_path),
                                   "sha256": hashlib.sha256(directory_bytes).hexdigest(),
                                   "users": [{"id": user["id"], "role": user["role"],
                                              "tenant_id": user["tenant_id"], "org_id": user["org_id"]}
                                             for user in directory["users"]]},
            "host_env_path": str(host_env_path),
            "credential_path": str(credential_path),
        })
        print(json.dumps({"out_dir": str(out_dir), "status": report["status"],
                          "issuer": issuer, "callback_url": args.redirect_url,
                          "directory_users": [user["id"] for user in directory["users"]],
                          "host_env": str(host_env_path), "stop":
                              f"python3 deployments/scripts/oidc-local.py --stop --out-dir {out_dir}"},
                         ensure_ascii=False, sort_keys=True))
        return 0
    except Exception as error:
        report["status"] = "FAIL"
        report["error"] = recorder.redact_text(f"{type(error).__name__}: {error}")
        print(f"FAIL: {report['error']}", file=sys.stderr)
        return 1
    finally:
        write_json(out_dir / "oidc-report.json", report)
        recorder.flush()


def run_harness(recorder: Recorder, infra_env: dict[str, str], provider_image: str, args) -> int:
    run_id = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S") + "-" + secrets.token_hex(3)
    out_dir = Path(args.out_dir) if args.out_dir else RUN_ROOT / run_id
    if out_dir.exists() and any(out_dir.iterdir()):
        print(f"REFUSED: {out_dir} already holds files; choose a fresh --out-dir", file=sys.stderr)
        return 2
    out_dir.mkdir(parents=True, exist_ok=True)
    recorder.path = out_dir / "commands.jsonl"
    owners = out_dir / "oidc-owners.tsv"
    owners.write_text("", encoding="utf-8")

    provider_port, app_port = free_port(), free_port()
    realm = "contract-audit-oidc-" + run_id.split("-")[-1]
    database = "oidc_d2_7a_" + run_id.replace("-", "_").lower()
    postgres_password = infra_env.get("POSTGRES_PASSWORD", "")
    if not postgres_password:
        print("REFUSED: the local .env has no POSTGRES_PASSWORD; run deploy/scripts/infra.py bootstrap first",
              file=sys.stderr)
        return 2
    material = generate_material(out_dir, infra_env.get("KEYCLOAK_ADMIN", "local-admin"))
    material["run_id"] = run_id
    recorder.secrets |= {str(material[key]) for key in
                         ("provider_admin_password", "client_secret", "admin_client_secret",
                          "admin_password", "viewer_password", "state_key")}
    app_env: dict[str, str] = {}
    app_process: subprocess.Popen | None = None
    report: dict[str, object] = {"run_id": run_id, "provider_image": provider_image,
                                 "checks": {}, "status": "RUNNING"}
    try:
        provider = bootstrap_provider(recorder, material, provider_image, provider_port, realm,
                                      f"http://127.0.0.1:{app_port}/api/auth/oidc/callback", owners)
        report["provider"] = {"container": provider["container"], "realm": realm, "issuer": provider["issuer"]}
        metadata = provider_wait_ready(str(provider["issuer"]))
        report["checks"]["discovery"] = {"status": 200, "issuer": metadata.get("issuer"),
                                         "authorization_endpoint_host":
                                             urllib.parse.urlsplit(metadata.get("authorization_endpoint", "")).hostname}
        create_isolated_database(recorder, args.pg_container, database)
        directory_document = build_directory(str(provider["issuer"]), [
            {"id": "oidc-admin", "display_name": "OIDC 管理员", "role": "admin", "tenant_id": "tenant-oidc",
             "org_id": "org-oidc", "enabled": True, "subject": provider["users"]["admin"]["subject"]},
            {"id": "oidc-viewer", "display_name": "OIDC 只读", "role": "viewer", "tenant_id": "tenant-oidc",
             "org_id": "org-oidc", "enabled": True, "subject": provider["users"]["viewer"]["subject"]},
        ])
        validate_directory(directory_document)
        directory_path = out_dir / "identity-directory.json"
        write_json(directory_path, directory_document)
        dsn = (f"postgres://contractaudit:{urllib.parse.quote(postgres_password)}@127.0.0.1:5432/"
               f"{database}?sslmode=disable")
        app_env = build_app_env(material, schema="public", postgres_dsn=dsn,
                                app_port=app_port, data_dir=out_dir / "data", directory_path=directory_path,
                                issuer=str(provider["issuer"]))
        write_private(out_dir / "app.env", "".join(f"{key}={value}\n" for key, value in app_env.items()))
        migrate_binary = build_binary(recorder, out_dir, "./cmd/migrate", "migrate")
        server_binary = build_binary(recorder, out_dir, "./cmd/server", "server")
        migrated = recorder.run([str(migrate_binary)], timeout=600, env={**os.environ, **app_env})
        if migrated.returncode != 0:
            raise Failure(f"the isolated migration failed: {migrated.stderr.decode('utf-8', 'replace')[-400:]}")
        report["checks"]["migration"] = {"exit": 0, "database": database}
        log_path = out_dir / "app.log"
        log_stream = log_path.open("wb")
        app_process = subprocess.Popen([str(server_binary)], cwd=ROOT, env={**os.environ, **app_env},
                                       stdout=log_stream, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
        app = f"http://127.0.0.1:{app_port}"
        wait_app_ready(app)
        recorder.note(f"throwaway application pid {app_process.pid} ready on {app}")

        issuer = str(provider["issuer"])
        if args.playwright:
            browser_env = {
                **os.environ,
                "E2E_OIDC_APP": app, "E2E_OIDC_ISSUER": issuer,
                "E2E_OIDC_ADMIN_USERNAME": str(provider["users"]["admin"]["username"]),
                "E2E_OIDC_ADMIN_PASSWORD": str(material["admin_password"]),
                "E2E_OIDC_VIEWER_USERNAME": str(provider["users"]["viewer"]["username"]),
                "E2E_OIDC_VIEWER_PASSWORD": str(material["viewer_password"]),
            }
            browser = recorder.run(["node", str(ROOT / "web/node_modules/playwright/cli.js"), "test",
                                    "--config", str(ROOT / "web/playwright.oidc.config.ts")],
                                   timeout=240, env=browser_env)
            browser_report = ROOT / "artifacts/documents-functional/oidc-browser/results.json"
            report["checks"]["playwright"] = {"exit": browser.returncode, "report": str(browser_report.relative_to(ROOT))}
            if browser.returncode != 0 or not browser_report.is_file():
                raise Failure("real Keycloak Playwright journeys failed; see the retained browser report")
            browser_stats = json.loads(browser_report.read_text(encoding="utf-8")).get("stats", {})
            if browser_stats.get("expected", 0) != 3 or browser_stats.get("unexpected", 0) != 0 or browser_stats.get("skipped", 0) != 0:
                raise Failure("real Keycloak Playwright journeys did not execute all three required cases")
            report["checks"]["playwright"]["stats"] = browser_stats
        admin_step, admin_opener = oidc_login_flow(app, issuer, "client",
                                                   provider["users"]["admin"]["username"],
                                                   str(material["admin_password"]), recorder)
        report["checks"]["client_login"] = admin_step
        if admin_step.get("session_status") != 200:
            raise Failure("the mapped admin user could not open a client session")
        actor = admin_step.get("actor", {})
        mapped = actor.get("tenant_id") == "tenant-oidc" and actor.get("org_id") == "org-oidc"
        report["checks"]["directory_mapping"] = {"actor": actor, "from_directory": mapped,
                                                 "client_cookie": "session" in admin_step.get("cookies", [])}
        if not mapped or admin_step.get("session_audience") != "client":
            raise Failure("the session actor was not mapped from the trusted directory")

        admin_flow, admin_flow_opener = oidc_login_flow(app, issuer, "admin",
                                                       provider["users"]["admin"]["username"],
                                                       str(material["admin_password"]), recorder)
        report["checks"]["admin_login"] = admin_flow
        if admin_flow.get("session_status") != 200 or admin_flow.get("session_audience") != "admin":
            raise Failure("the admin audience login did not establish an admin session")
        if "admin_session" not in admin_flow.get("cookies", []):
            raise Failure("the admin audience did not use its own cookie")
        client_token = next(cookie.value for cookie in cookie_jar(admin_opener) if cookie.name == "session")
        admin_token = next(cookie.value for cookie in cookie_jar(admin_flow_opener) if cookie.name == "admin_session")
        isolation = {}
        for name, path, header, expected in (
            ("client_cookie_on_admin_route", "/api/admin/settings", f"session={client_token}", 401),
            ("admin_cookie_on_client_route", "/api/cases", f"admin_session={admin_token}", 401),
            ("renamed_client_token_on_admin_route", "/api/admin/settings", f"admin_session={client_token}", 403),
            ("renamed_admin_token_on_client_route", "/api/cases", f"session={admin_token}", 403),
        ):
            observed = http("GET", app + path, headers={"Cookie": header})[0]
            isolation[name] = observed
            report["checks"]["audience_isolation"] = isolation
            if observed != expected:
                raise Failure(f"{name} returned HTTP {observed}; expected {expected}")

        viewer_flow, _ = oidc_login_flow(app, issuer, "admin", provider["users"]["viewer"]["username"],
                                         str(material["viewer_password"]), recorder)
        report["checks"]["viewer_admin_refused"] = viewer_flow.get("callback_status")
        if viewer_flow.get("callback_status") != 401:
            raise Failure("a non-admin identity was not refused on the admin audience")

        unknown_flow, _ = oidc_login_flow(app, issuer, "client", provider["users"]["unknown"]["username"],
                                          str(provider["users"]["unknown"]["password"]), recorder)
        report["checks"]["unknown_subject_refused"] = unknown_flow.get("callback_status")
        if unknown_flow.get("callback_status") != 401:
            raise Failure("a subject absent from the directory was not refused")

        logout = http("DELETE", app + "/api/session?audience=client",
                      headers={"Cookie": cookie_header(admin_opener)})
        after = http("GET", app + "/api/session", headers={"Cookie": cookie_header(admin_opener)})
        report["checks"]["logout"] = {"logout_status": logout[0], "session_after": after[0]}
        if logout[0] != 204 or after[0] != 401:
            raise Failure(f"logout did not clear the session (logout {logout[0]}, after {after[0]})")

        revoked = dict(directory_document)
        revoked["users"] = [dict(user, enabled=False) if user["id"] == "oidc-admin" else user
                            for user in directory_document["users"]]
        write_json(directory_path, revoked)
        deadline = time.monotonic() + 10
        after_revocation = 0
        while time.monotonic() < deadline:
            after_revocation = http("GET", app + "/api/session?audience=admin",
                                    headers={"Cookie": cookie_header(admin_flow_opener)})[0]
            if after_revocation in (401, 403):
                break
            time.sleep(0.5)
        report["checks"]["directory_revocation"] = {"session_after_revocation": after_revocation}
        if after_revocation not in (401, 403):
            raise Failure("revoking the directory user did not refuse the existing session")

        report["status"] = "PASS"
        return 0
    except Exception as error:
        report["status"] = "FAIL"
        report["error"] = recorder.redact_text(f"{type(error).__name__}: {error}")
        print(f"FAIL: {report['error']}", file=sys.stderr)
        return 1
    finally:
        if app_process is not None:
            app_process.terminate()
            try:
                app_process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                app_process.kill()
                app_process.wait(timeout=15)
        write_json(out_dir / "oidc-report.json", report)
        recorder.flush()
        print(json.dumps({"out_dir": str(out_dir), "status": report["status"],
                          "checks": sorted(report.get("checks", {})),
                          "stop": f"python3 deployments/scripts/oidc-local.py --stop --out-dir {out_dir}"},
                         ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Local Docker infrastructure lifecycle. Never removes a volume or business data."""
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
import urllib.request
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
SECRET_KEYS = ("POSTGRES_PASSWORD", "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY",
               "KEYCLOAK_ADMIN_PASSWORD", "KEYCLOAK_DB_PASSWORD")



def configured_env_path(root=ROOT):
	value = os.environ.get("INFRA_ENV_PATH", "").strip()
	return Path(value).expanduser().resolve() if value else Path(root).resolve() / ".env"


def isolated_project_name(env_file, root=ROOT):
	env_file = Path(env_file).resolve()
	if env_file == (Path(root).resolve() / ".env"):
		name = os.environ.get("COMPOSE_PROJECT_NAME", "").strip()
		if name and not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,62}", name):
			raise ValueError("COMPOSE_PROJECT_NAME must be a lowercase Compose project name")
		return name
	name = os.environ.get("COMPOSE_PROJECT_NAME", "").strip()
	profile = re.sub(r"[^a-z0-9_-]+", "-", env_file.parent.name.lower()).strip("-_")[:35] or "private"
	digest = hashlib.sha256(str(env_file).encode()).hexdigest()[:8]
	derived = f"contract-audit-{profile}-{digest}"
	if name and name != derived:
		raise ValueError("COMPOSE_PROJECT_NAME must match the isolated profile path")
	return derived


def available_loopback_ports(count):
	reservations = []
	try:
		for _ in range(count):
			reservation = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
			reservation.bind(("127.0.0.1", 0))
			reservations.append(reservation)
		return [reservation.getsockname()[1] for reservation in reservations]
	finally:
		for reservation in reservations:
			reservation.close()


def replace_env_entries(template, replacements):
	lines = []
	found = set()
	for line in template.splitlines():
		key, separator, _ = line.partition("=")
		if separator and key in replacements:
			lines.append(f"{key}={replacements[key]}")
			found.add(key)
		else:
			lines.append(line)
	for key, value in replacements.items():
		if key not in found:
			lines.append(f"{key}={value}")
	return "\n".join(lines) + "\n"


def private_profile_values(postgres_password):
	suffix = secrets.token_hex(4)
	postgres, rustfs, console, keycloak, edge, client, admin, portal = available_loopback_ports(8)
	dev_database = f"contract_audit_dev_{suffix}"
	test_database = f"contract_audit_test_{suffix}"
	return {
		"POSTGRES_PORT": str(postgres),
		"DEV_DATABASE_URL": f"postgres://contractaudit:{postgres_password}@127.0.0.1:{postgres}/{dev_database}?sslmode=disable",
		"TEST_DATABASE_URL": f"postgres://contractaudit:{postgres_password}@127.0.0.1:{postgres}/{test_database}?sslmode=disable",
		"RUSTFS_PORT": str(rustfs),
		"RUSTFS_CONSOLE_PORT": str(console),
		"S3_ENDPOINT": f"http://127.0.0.1:{rustfs}",
		"S3_BUCKET": f"contract-audit-{suffix}",
		"KEYCLOAK_PORT": str(keycloak),
		"EDGE_PORT": str(edge),
		"CLIENT_PORT": str(client),
		"ADMIN_PORT": str(admin),
		"PORTAL_PORT": str(portal),
	}

def read_env(path):
    result = {}
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key):
            raise ValueError(f"Invalid literal dotenv syntax at line {number}")
        if key in result:
            raise ValueError(f"Duplicate configuration key: {key}")
        if value.startswith(("'", '"')):
            if len(value) < 2 or value[-1] != value[0]:
                raise ValueError(f"Unmatched quote at line {number}")
            value = value[1:-1]
        if "$" in value or "`" in value or "\x00" in value:
            raise ValueError(f"Shell interpolation is not supported at line {number}")
        result[key] = value
    return result


def validate_env(values):
	for key in SECRET_KEYS:
		value = values.get(key, "")
		if len(value) < 24 or "__GENERATE_" in value or value.lower() in {"rustfsadmin", "contractaudit"}:
			raise ValueError(f"{key} must be a generated or independently configured secret (24+ characters)")
	port_keys = ("POSTGRES_PORT", "RUSTFS_PORT", "RUSTFS_CONSOLE_PORT", "KEYCLOAK_PORT", "EDGE_PORT", "CLIENT_PORT", "ADMIN_PORT", "PORTAL_PORT")
	ports = []
	for name in port_keys:
		value = values.get(name, "")
		if not value and name in {"EDGE_PORT", "CLIENT_PORT", "ADMIN_PORT", "PORTAL_PORT"}:
			continue
		if not value.isdigit() or not 1 <= int(value) <= 65535:
			raise ValueError(f"Invalid port: {name}")
		ports.append(int(value))
	if len(set(ports)) != len(ports):
		raise ValueError("Local service ports must be distinct")
	dbnames = []
	for key, prefix in (("DEV_DATABASE_URL", "contract_audit_dev"), ("TEST_DATABASE_URL", "contract_audit_test")):
		url = urlsplit(values.get(key, ""))
		if url.scheme not in ("postgres", "postgresql") or url.hostname not in ("127.0.0.1", "localhost"):
			raise ValueError(f"Local infrastructure expects a loopback PostgreSQL URL: {key}")
		if (url.port or 5432) != int(values["POSTGRES_PORT"]):
			raise ValueError(f"{key} port must match POSTGRES_PORT")
		if url.username != "contractaudit" or url.password != values["POSTGRES_PASSWORD"]:
			raise ValueError(f"{key} must match the generated local database credentials")
		database = url.path.lstrip("/")
		if database in dbnames:
			raise ValueError("Use separate development and test databases")
		if not re.fullmatch(prefix + r"(?:_[a-f0-9]{8})?", database):
			raise ValueError(f"{key} must name the isolated {prefix} database")
		dbnames.append(database)
	if dbnames[0] == dbnames[1]:
		raise ValueError("Use separate development and test databases")
	if not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,61}[a-z0-9]", values.get("S3_BUCKET", "")):
		raise ValueError("S3_BUCKET must be a 3-63 character lowercase local bucket name")
	endpoint = urlsplit(values.get("S3_ENDPOINT", ""))
	if endpoint.scheme != "http" or endpoint.hostname not in ("127.0.0.1", "localhost") or endpoint.port != int(values["RUSTFS_PORT"]):
		raise ValueError("S3_ENDPOINT must use the configured loopback RustFS port")
	for key in ("POSTGRES_IMAGE", "RUSTFS_IMAGE", "AWSCLI_IMAGE", "KEYCLOAK_IMAGE"):
		image = values.get(key, "")
		if not image or ":latest" in image or (":" not in image.rsplit("/", 1)[-1] and "@sha256:" not in image):
			raise ValueError(f"Pin an explicit image version or digest: {key}")
	return values


def bootstrap(root=ROOT):
	root = Path(root).resolve()
	destination = configured_env_path(root)
	if destination.exists():
		validate_env(read_env(destination))
		return "reused existing .env without modification"
	template = (root / ".env.example").read_text(encoding="utf-8")
	generated = {key: secrets.token_hex(24) for key in SECRET_KEYS}
	for key, value in generated.items():
		template = template.replace(f"__GENERATE_{key}__", value)
	if "__GENERATE_" in template:
		raise ValueError("Unresolved configuration placeholder")
	if destination != root / ".env":
		template = replace_env_entries(template, private_profile_values(generated["POSTGRES_PASSWORD"]))
	destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
	descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
	with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
		stream.write(template)
	validate_env(read_env(destination))
	if destination != root / ".env":
		return f"created private isolated profile at {destination.parent} with random credentials, ports, database names and bucket"
	return "created private .env with local random credentials"


def compose_prefix(root, identity=False):
	binary = os.environ.get("COMPOSE_BIN")
	if binary:
		command = [binary]
	else:
		docker = os.environ.get("DOCKER_BIN", "docker")
		if not shutil.which(docker):
			raise RuntimeError("Docker CLI is unavailable; install Docker Engine/Desktop with Compose v2")
		command = [docker, "compose"]
	env_file = configured_env_path(root)
	project = isolated_project_name(env_file, root)
	if project:
		command.extend(["-p", project])
	command.extend(["--project-directory", str(root), "--env-file", str(env_file), "-f", str(Path(root) / "compose.yaml")])
	if identity:
		command.extend(["--profile", "identity"])
	return command


def sensitive_values():
	values = set()
	distributed_value = os.environ.get("DISTRIBUTED_ENV_PATH", "").strip()
	distributed_path = Path(distributed_value).expanduser() if distributed_value else configured_env_path().parent / ".env.distributed"
	paths = (configured_env_path(), distributed_path)
	for path in paths:
		if not path.is_file():
			continue
		for key, value in read_env(path).items():
			if any(marker in key for marker in ("PASSWORD", "SECRET", "TOKEN", "API_KEY", "STATE_KEY", "DATABASE_URL", "ACCESS_KEY")) and value:
				values.add(value)
	return sorted(values, key=len, reverse=True)


def redact(text):
	for value in sensitive_values():
		text = text.replace(value, "<redacted>")
	return text


def run(command, checks, name, timeout=300):
    started = time.monotonic()
    try:
        process = subprocess.run(command, cwd=ROOT, text=True, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired as error:
        checks.append({"name": name, "argv": command, "returncode": None, "timed_out": True,
                       "timeout_seconds": timeout, "seconds": round(time.monotonic() - started, 3)})
        raise error
    stdout = process.stdout or ""
    stderr = process.stderr or ""
    checks.append({"name": name, "argv": command, "returncode": process.returncode,
                   "seconds": round(time.monotonic() - started, 3),
                   "stdout_bytes": len(stdout.encode()), "stdout_sha256": hashlib.sha256(stdout.encode()).hexdigest(),
                   "stderr_bytes": len(stderr.encode()), "stderr_sha256": hashlib.sha256(stderr.encode()).hexdigest(),
                   "stdout": redact(stdout[-6000:]), "stderr": redact(stderr[-3000:])})
    if process.returncode:
        raise RuntimeError(f"{name} failed with exit {process.returncode}; inspect the local infrastructure report")
    return stdout


def oidc_ready(values, checks):
    base = f"http://127.0.0.1:{values['KEYCLOAK_PORT']}/realms/master"
    for attempt in range(45):
        try:
            with urllib.request.urlopen(base + "/.well-known/openid-configuration", timeout=3) as response:
                discovery = json.load(response)
            if discovery.get("issuer") != base:
                raise ValueError("OIDC issuer mismatch")
            checks.append({"name": "keycloak-discovery", "passed": True,
                           "scope": "identity sandbox only; no application authentication integration"})
            return
        except (OSError, ValueError):
            if attempt == 44:
                raise RuntimeError("Keycloak discovery did not become ready")
            time.sleep(2)


def main(argv=None):
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("action", choices=("bootstrap", "config", "up", "smoke", "stop"))
	parser.add_argument("--identity", action="store_true", help="Also start/check optional Keycloak and its PostgreSQL database")
	parser.add_argument("--out", default="artifacts/infra.json", help="Local JSON evidence, excluded from release until explicitly archived")
	args = parser.parse_args(argv)
	env_file = configured_env_path(ROOT)
	custom_profile = env_file != ROOT / ".env"
	evidence_path = os.environ.get("INFRA_EVIDENCE_PATH", "").strip()
	if evidence_path:
		output = Path(evidence_path).expanduser()
	elif custom_profile:
		output = env_file.parent / "infra.json"
	else:
		output = ROOT / args.out
	if not output.is_absolute():
		output = ROOT / output
	report = {"started_at": dt.datetime.now(dt.timezone.utc).isoformat(), "action": args.action,
			  "parent_argv": [sys.executable, *sys.argv], "identity_requested": args.identity,
			  "checks": [], "passed": False}
	try:
		if args.action == "bootstrap":
			report["message"] = bootstrap(ROOT)
		else:
			values = validate_env(read_env(env_file))
			prefix = compose_prefix(ROOT, args.identity)
			run(prefix + ["config", "--quiet"], report["checks"], "compose-config", timeout=30)
			if args.action == "up":
				services = ["db", "rustfs"] + (["identity-db", "keycloak"] if args.identity else [])
				run(prefix + ["up", "-d", "--wait", "--wait-timeout", "120", *services], report["checks"], "infra-start")
				run(prefix + ["run", "--rm", "--no-deps", "rustfs-init"], report["checks"], "s3-bucket-init")
				if args.identity:
					oidc_ready(values, report["checks"])
			elif args.action == "smoke":
				result = run(prefix + ["exec", "-T", "db", "psql", "-U", "contractaudit", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-Atc", "SELECT current_database(), 1+1;"], report["checks"], "postgres-query")
				if result.strip() != "postgres|2":
					raise RuntimeError("PostgreSQL result did not match the expected query")
				run(prefix + ["run", "--rm", "--no-deps", "s3-smoke"], report["checks"], "s3-roundtrip")
				if args.identity:
					oidc_ready(values, report["checks"])
			elif args.action == "stop":
				run(prefix + ["stop"], report["checks"], "infra-stop")
		report["passed"] = True
	except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
		report["error"] = redact(str(error))
		print(f"FAIL: {redact(str(error))}", file=sys.stderr)
	report["actual_exit_code"] = 0 if report["passed"] else 1
	report["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
	output.parent.mkdir(parents=True, exist_ok=True)
	serialized = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
	if custom_profile:
		descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
		os.fchmod(descriptor, 0o600)
		with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
			stream.write(serialized)
	else:
		output.write_text(serialized, encoding="utf-8")
	print(f"{'PASS' if report['passed'] else 'FAIL'}: {args.action}; evidence: {output}")
	return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

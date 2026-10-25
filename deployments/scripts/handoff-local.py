#!/usr/bin/env python3
"""Emit the runtime handoff files from the running host stack (private, 0600).

The runtime owner runs this at handoff time so the control file always names live
PIDs and tested start commands. Three outputs are written into ``--out-dir``:

* ``stack.env`` — dotenv profile with the DSNs, ``EDGE_PORT``, token keys and
  model-budget knobs the running stack uses, so a harness's
  ``docker compose --env-file .env --env-file <stack.env>`` prefix resolves to the
  running services' own credentials (a different token set would 401 every hop).
* ``control.json`` — keyed by the **canonical service names the harnesses and the
  Compose files use** (``core-api``, ``document-service``, ``audit-execution``,
  ``audit-worker``, ``model-gateway``, ``tool-gateway``): one running instance per
  service with pid/start command/log/health URL plus the ``additional`` second
  replica (distinct ``WORKER_ID``, own port and log).
* ``restore-source.env`` — ``stack.env`` plus the document instance's real
  ``S3_*`` values and ``DATA_DIR``, so a restore harness never has to guess them.

Nothing is printed beyond paths and counts; no value is invented.
"""
from __future__ import annotations

import argparse
import json
import os
import shlex
from pathlib import Path

# canonical service name -> (binary, role, port, health path)
SERVICES = {
    "core-api": ("server", "core", 18081, "/ready"),
    "document-service": ("documentservice", "document", 18082, "/health/ready"),
    "audit-execution": ("auditexecution", "execution", 18083, "/health/ready"),
    "model-gateway": ("modelgateway", "model", 18084, "/health/ready"),
    "tool-gateway": ("toolgateway", "tool", 18085, "/ready"),
    "audit-worker": ("worker", "worker", 18086, "/health/ready"),
}
EDGE_PORT = 18080
S3_KEYS = ("S3_ENDPOINT", "S3_BUCKET", "S3_REGION", "S3_ACCESS_KEY", "S3_SECRET_KEY",
           "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY")


def read_pairs(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        values[key.strip()] = value.strip()
    return values


def live_instance(pid: int | None, binary: Path, environment: dict[str, str]) -> tuple[int | None, Path, dict[str, str]]:
    if not isinstance(pid, int) or not Path(f"/proc/{pid}").exists():
        return None, binary, environment
    process = Path(f"/proc/{pid}")
    actual_binary = (process / "exe").resolve(strict=True)
    if actual_binary.name != binary.name:
        raise RuntimeError(f"PID {pid} is not the expected {binary.name} process")
    actual_environment = dict(
        item.split("=", 1) for item in (process / "environ").read_bytes().decode().split("\0") if "=" in item
    )
    return pid, actual_binary, actual_environment


def write_private(path: Path, text: str) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        stream.write(text)
    path.chmod(0o600)


def start_command(binary: Path, role: str, environment: dict[str, str], worker_id: str | None, port: int,
                  extra: tuple[str, ...] = ()) -> str:
    values = dict(environment)
    values.setdefault("APP_ENV", "test")
    values.setdefault("DATABASE_SCHEMA", "public")
    values.setdefault("DATA_DIR", "/tmp")
    if binary.name != "gateway":
        values["SERVICE_ROLE"] = role
        role_token = environment.get(f"GATEWAY_{role.upper()}_TOKEN")
        if role_token and not values.get("GATEWAY_ROLE_TOKEN"):
            values["GATEWAY_ROLE_TOKEN"] = role_token
    values.update(item.split("=", 1) for item in extra)
    if role == "worker":
        values.update(WORKER_LEASE_DURATION=environment.get("WORKER_LEASE_DURATION", "2m"),
                      WORKER_HEARTBEAT_INTERVAL=environment.get("WORKER_HEARTBEAT_INTERVAL", "10s"),
                      HEALTH_ADDR=f"127.0.0.1:{port}")
    if role in ("worker", "execution") and environment.get("NATS_URL"):
        values["NATS_URL"] = environment["NATS_URL"]
    if role != "worker":
        values["ADDR"] = f"127.0.0.1:{port}"
    if worker_id:
        values["WORKER_ID"] = worker_id
    return shlex.join(["env", "-i", *(f"{key}={value}" for key, value in values.items()), str(binary)])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--private-env", required=True, help="dotenv or JSON environment used by the running stack")
    parser.add_argument("--state", required=True, help="runtime state JSON holding the live pids")
    parser.add_argument("--bin-dir", required=True, help="directory holding the host-built binaries")
    parser.add_argument("--out-dir", required=True, help="directory to write the handoff files into")
    parser.add_argument("--broker-json", default="",
                        help="optional broker identity JSON (pid/binary/sha256/argv/ports) merged into control.json as `broker`")
    args = parser.parse_args(argv)
    private = Path(args.private_env)
    environment = ({k: str(v) for k, v in json.loads(private.read_text(encoding="utf-8")).items()}
                   if private.suffix == ".json" else read_pairs(private))
    state = json.loads(Path(args.state).read_text(encoding="utf-8"))
    pids = state.get("pids", {})
    bin_dir, out_dir = Path(args.bin_dir), Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    environment["EDGE_PORT"] = str(EDGE_PORT)
    environment.setdefault("MODEL_TENANT_MAX_INFLIGHT", "2")
    for key, default in {
        "GATEWAY_HEALTH_INTERVAL": "1s",
        "GATEWAY_HEALTH_TIMEOUT": "500ms",
        "GATEWAY_HEALTH_FAILURE_THRESHOLD": "2",
        "GATEWAY_HEALTH_RECOVERY_THRESHOLD": "2",
    }.items():
        environment.setdefault(key, default)

    write_private(out_dir / "stack.env",
                  "# Runtime handoff profile; tokens are the running stack's own values.\n"
                  + "".join(f"{key}={environment[key]}\n" for key in sorted(environment)))

    control: dict[str, object] = {}
    document_environment: dict[str, str] | None = None
    for service, (binary, role, port, health) in SERVICES.items():
        pid = pids.get({"core-api": "core", "document-service": "document", "audit-execution": "execution",
                        "model-gateway": "model", "tool-gateway": "tool", "audit-worker": "worker"}[service])
        pid, running_binary, running_environment = live_instance(pid, bin_dir / binary, environment)
        if service == "document-service" and pid is not None:
            document_environment = running_environment
        entry: dict[str, object] = {
            "start": start_command(running_binary, role, running_environment, None, port),
            "log": f"{role}.log",
            "health": f"http://127.0.0.1:{port}{health}",
        }
        if isinstance(pid, int) and Path(f"/proc/{pid}").exists():
            entry["pid"] = pid
        additional_port = port + 100
        additional = {
            "start": start_command(running_binary, role, running_environment,
                                   f"handoff-{role}-2" if role == "worker" else None, additional_port),
            "log": f"{role}-2.log",
            "health": f"http://127.0.0.1:{additional_port}{health}",
        }
        control[service] = {"instances": [entry] if "pid" in entry else [],
                            "additional": [additional]}

    if document_environment is None:
        raise RuntimeError("Cannot capture restore source without a running Document service")
    restore = dict(environment)
    for key in S3_KEYS + ("DATA_DIR",):
        restore.pop(key, None)
        if key in document_environment:
            restore[key] = document_environment[key]
    write_private(out_dir / "restore-source.env",
                  "# Restore-source profile: stack.env plus the document instance's S3 values and DATA_DIR.\n"
                  + "".join(f"{key}={restore[key]}\n" for key in sorted(restore)))

    # Preserve the live edge environment, including upstreams and browser origins.
    upstream_defaults = {"CORE_URLS": "http://127.0.0.1:18081", "DOCUMENT_URL": "http://127.0.0.1:18082",
                         "EXECUTION_URL": "http://127.0.0.1:18083", "TOOL_URLS": "http://127.0.0.1:18085"}
    edge_pid = pids.get("edge")
    edge_pid, edge_binary, edge_environment = live_instance(edge_pid, bin_dir / "gateway", environment)
    edge_extra = tuple(f"{key}={edge_environment.get(key) or default}" for key, default in upstream_defaults.items())
    edge_entry: dict[str, object] = {
        "start": start_command(edge_binary, "core", edge_environment, None, EDGE_PORT, edge_extra),
        "log": "edge.log",
        "health": f"http://127.0.0.1:{EDGE_PORT}/ready",
    }
    if isinstance(edge_pid, int) and Path(f"/proc/{edge_pid}").exists():
        edge_entry["pid"] = edge_pid
    control["edge"] = {"instances": [edge_entry] if "pid" in edge_entry else [], "additional": []}
    if args.broker_json and Path(args.broker_json).exists():
        broker = json.loads(Path(args.broker_json).read_text(encoding="utf-8"))
        control["broker"] = {k: broker[k] for k in ("pid", "binary", "sha256", "version", "argv", "client_url",
                                                   "monitor_url", "data_dir", "log", "restart_argv", "stop")
                             if k in broker}
    write_private(out_dir / "control.json", json.dumps(control, ensure_ascii=False, indent=2) + "\n")

    live = sum(1 for entry in control.values() if isinstance(entry, dict) and entry.get("instances"))
    print(f"wrote stack.env, control.json and restore-source.env into {out_dir} (0600; {live} live instances, "
          f"{len(control)} entries)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

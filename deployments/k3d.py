#!/usr/bin/env python3
"""Create and operate the isolated local k3d development cluster."""
from __future__ import annotations

import argparse
import base64
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[1]
PROFILE = os.environ.get("CAW_K3D_PROFILE", "light").strip().lower()
FULL = PROFILE == "full"
CLUSTER = os.environ.get("CAW_K3D_CLUSTER", "caw-local-full" if FULL else "caw-local")
CLUSTER_CONFIG = "cluster-5node.yaml" if FULL else "cluster.yaml"
APPS_OVERLAY = "apps-5node" if FULL else "apps"
API_PORT = "6446" if FULL else "6445"
SERVER_MEMORY = "1g" if FULL else "768m"
# 2 GiB agents leave room for ztunnel/CNI and a surge pod in the full profile.
AGENTS_MEMORY = "2g"
NAMESPACE = "contract-audit"
OBSERVE_IMAGES = (
    "otel/opentelemetry-collector-contrib:0.140.0",
    "grafana/tempo:2.10.10",
    "prom/prometheus:v3.15.1",
    "grafana/grafana:13.2.4",
)
IMAGE_TAGS = (
    "contract-audit-backend:local",
    "contract-audit-client:local",
    "contract-audit-admin:local",
    "contract-audit-portal:local",
    "postgres:18.6-bookworm",
    "rustfs/rustfs:1.0.3",
    "nats:2.11.8-alpine",
    "amazon/aws-cli:2.31.14",
)
DELIVERY = Path(os.environ.get("CAW_DELIVERY_DIR", str(ROOT.parent / "contract-audit-delivery"))).expanduser().resolve()
DEFAULT_ARTIFACT_DIR = "k3d-full" if FULL else "k3d"
ARTIFACTS = Path(os.environ.get("CAW_K3D_ARTIFACT_DIR", str(DELIVERY / DEFAULT_ARTIFACT_DIR))).expanduser().resolve()
KUBECONFIG = ARTIFACTS / "kubeconfig"
SECRETS = ARTIFACTS / "secrets.json"


def run(command: list[str], *, check: bool = True, timeout: int = 300) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, text=True, capture_output=True, timeout=timeout)
    if check and result.returncode:
        detail = result.stderr.strip() or result.stdout.strip() or f"exit {result.returncode}"
        raise RuntimeError(f"command failed ({result.returncode}): {' '.join(command)}: {detail}")
    return result


def private_dir() -> None:
    ARTIFACTS.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(ARTIFACTS, 0o700)


def cluster_inventory() -> list[dict[str, object]]:
    result = run(["k3d", "cluster", "list", "--output", "json"])
    inventory = json.loads(result.stdout)
    if isinstance(inventory, dict):
        inventory = inventory.get("clusters", [])
    if not isinstance(inventory, list):
        raise RuntimeError("k3d returned an unexpected cluster inventory")
    return inventory


def cluster_exists() -> bool:
    return any(isinstance(item, dict) and item.get("name") == CLUSTER for item in cluster_inventory())


def ensure_secrets() -> dict[str, str]:
    private_dir()
    if SECRETS.exists():
        if SECRETS.stat().st_mode & 0o077:
            raise RuntimeError(f"private credentials must have mode 0600: {SECRETS}")
        values = json.loads(SECRETS.read_text(encoding="utf-8"))
        required = {"POSTGRES_PASSWORD", "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY", "LLM_API_KEY",
                    "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL",
                    "MODEL_DATABASE_URL", "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL",
                    "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN",
                    "GATEWAY_TOOL_TOKEN", "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN",
                    "WORKER_TOOL_TOKEN", "DOCUMENT_MODEL_TOKEN"}
        if not isinstance(values, dict) or required - values.keys():
            raise RuntimeError(f"private credentials file is incomplete; left untouched: {SECRETS}")
        if "DEV_DATABASE_URL" not in values or "WORKER_CORE_TOKEN" not in values or \
                "CORE_DOCUMENT_TOKEN" not in values or "LLM_CONFIG_KEY" not in values:
            values.setdefault("DEV_DATABASE_URL", values["MIGRATOR_DATABASE_URL"])
            values.setdefault("WORKER_CORE_TOKEN", secrets.token_urlsafe(48))
            values.setdefault("CORE_DOCUMENT_TOKEN", secrets.token_urlsafe(48))
            values.setdefault("LLM_CONFIG_KEY", base64.b64encode(secrets.token_bytes(32)).decode("ascii"))
            temporary = SECRETS.with_suffix(".tmp")
            descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
                json.dump(values, stream, sort_keys=True)
                stream.write("\n")
            os.replace(temporary, SECRETS)
            os.chmod(SECRETS, 0o600)
        return values

    postgres_password = secrets.token_hex(32)
    values: dict[str, str] = {
        "POSTGRES_PASSWORD": postgres_password,
        "RUSTFS_ACCESS_KEY": "caw" + secrets.token_hex(20),
        "RUSTFS_SECRET_KEY": secrets.token_urlsafe(48),
        # Fixture mode never sends this generated value to a model provider.
        "LLM_API_KEY": secrets.token_urlsafe(48),
        # Seals admin-managed provider credentials; exactly 32 bytes, base64 encoded.
        "LLM_CONFIG_KEY": base64.b64encode(secrets.token_bytes(32)).decode("ascii"),
    }
    for key, role in (("CORE_DATABASE_URL", "core_api"), ("DOCUMENT_DATABASE_URL", "document_service"),
                      ("EXECUTION_DATABASE_URL", "audit_execution"), ("MODEL_DATABASE_URL", "model_gateway"),
                      ("TOOL_DATABASE_URL", "tool_gateway")):
        password = secrets.token_hex(32)
        values[key] = f"postgres://{role}:{quote(password, safe='')}@db:5432/contract_audit_dev?sslmode=disable"
    values["MIGRATOR_DATABASE_URL"] = (
        f"postgres://contractaudit:{quote(postgres_password, safe='')}@db:5432/contract_audit_dev?sslmode=disable"
    )
    values["DEV_DATABASE_URL"] = values["MIGRATOR_DATABASE_URL"]
    for key in ("GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
                "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN", "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN"):
        values[key] = secrets.token_urlsafe(48)
    descriptor = os.open(SECRETS, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        json.dump(values, stream, sort_keys=True)
        stream.write("\n")
    return values


def write_kubeconfig() -> None:
    private_dir()
    exported = run(["k3d", "kubeconfig", "get", CLUSTER]).stdout
    if not exported.strip():
        raise RuntimeError("k3d returned an empty kubeconfig")
    temporary = KUBECONFIG.with_suffix(".tmp")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        stream.write(exported)
    os.replace(temporary, KUBECONFIG)
    os.chmod(KUBECONFIG, 0o600)


def kubectl(*arguments: str, check: bool = True, timeout: int = 300) -> subprocess.CompletedProcess[str]:
    if not KUBECONFIG.is_file():
        raise RuntimeError(f"isolated kubeconfig not found: {KUBECONFIG}; run 'up' first")
    return run(["kubectl", "--kubeconfig", str(KUBECONFIG), *arguments], check=check, timeout=timeout)


def up() -> None:
    private_dir()
    ensure_secrets()
    if cluster_exists():
        print(f"Cluster {CLUSTER} already exists; reusing it without touching the default kubeconfig.")
    else:
        run([
            "k3d", "cluster", "create", "--config", str(ROOT / "deployments/k3d" / CLUSTER_CONFIG),
            "--api-port", "127.0.0.1:" + API_PORT, "--servers-memory", SERVER_MEMORY,
            "--agents-memory", AGENTS_MEMORY,
            "--kubeconfig-update-default=false", "--kubeconfig-switch-context=false",
            "--wait", "--timeout", "180s",
        ], timeout=240)
    write_kubeconfig()
    kubectl("wait", "--for=condition=Ready", "nodes", "--all", "--timeout=120s", timeout=150)
    agents = 4 if FULL else 2
    print(f"Cluster: {CLUSTER} (1 server, {agents} agents, profile={PROFILE}); kubeconfig: {KUBECONFIG}")


def import_images() -> None:
    docker = os.environ.get("DOCKER_BIN", "docker")
    if not shutil.which(docker):
        raise RuntimeError("Docker CLI is unavailable")
    tags = list(IMAGE_TAGS) + (list(OBSERVE_IMAGES) if FULL else [])
    missing: list[str] = []
    for image in tags:
        if run([docker, "image", "inspect", image], check=False, timeout=30).returncode:
            missing.append(image)
    if missing:
        raise RuntimeError("required packaged or cached platform images are missing: " + ", ".join(missing))
    run(["k3d", "image", "import", "--cluster", CLUSTER, *tags], timeout=600)


def apply_private_secret(name: str, keys: tuple[str, ...]) -> None:
    values = ensure_secrets()
    private_env = ARTIFACTS / f".{name}-{secrets.token_hex(6)}.env"
    descriptor = os.open(private_env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            for key in keys:
                stream.write(f"{key}={values[key]}\n")
        manifest = run([
            "kubectl", "--kubeconfig", str(KUBECONFIG), "create", "secret", "generic", name,
            "--namespace", NAMESPACE, f"--from-env-file={private_env}", "--dry-run=client", "-o", "yaml",
        ]).stdout
        applied = subprocess.run(
            ["kubectl", "--kubeconfig", str(KUBECONFIG), "apply", "-f", "-"],
            input=manifest, text=True, capture_output=True,
        )
        if applied.returncode:
            raise RuntimeError(f"failed applying private secret {name}: {applied.stderr.strip()}")
    finally:
        private_env.unlink(missing_ok=True)


def apply_phase(path: str) -> None:
    overlay = str(ROOT / "deployments/k3d" / path)
    manifest = run(["kubectl", "kustomize", overlay, "--load-restrictor", "LoadRestrictionsNone"], timeout=300).stdout
    applied = subprocess.run(
        ["kubectl", "--kubeconfig", str(KUBECONFIG), "apply", "-f", "-"],
        input=manifest, text=True, capture_output=True, timeout=300,
    )
    if applied.returncode:
        raise RuntimeError(f"failed applying local overlay {path}: {applied.stderr.strip()}")


def wait_platform() -> None:
    for selector in ("app.kubernetes.io/name=db", "app.kubernetes.io/name=rustfs", "app.kubernetes.io/name=nats"):
        kubectl("wait", "--for=condition=Ready", "pod", "-l", selector, "-n", NAMESPACE,
                "--timeout=240s", timeout=270)
    kubectl("wait", "--for=condition=complete", "job/rustfs-bucket-init", "-n", NAMESPACE,
            "--timeout=180s", timeout=210)


def apply_configmap(name: str, key: str, source: Path) -> None:
    manifest = run([
        "kubectl", "--kubeconfig", str(KUBECONFIG), "-n", NAMESPACE, "create", "configmap", name,
        f"--from-file={key}={source}", "--dry-run=client", "-o", "yaml",
    ]).stdout
    applied = subprocess.run(
        ["kubectl", "--kubeconfig", str(KUBECONFIG), "apply", "-f", "-"],
        input=manifest, text=True, capture_output=True, timeout=60,
    )
    if applied.returncode:
        raise RuntimeError(f"failed applying ConfigMap {name}: {applied.stderr.strip()}")


def apply_observe_configs() -> None:
    apply_configmap("otel-collector-config", "config.yaml", ROOT / "deploy/app/otel-collector.yaml")
    apply_configmap("tempo-config", "tempo.yaml", ROOT / "deploy/app/observe/tempo.yaml")
    apply_configmap("prometheus-config", "prometheus.yml", ROOT / "deploy/app/observe/prometheus.yml")
    apply_configmap("grafana-datasources", "datasources.yaml", ROOT / "deploy/app/observe/grafana-datasources.yaml")


def deploy() -> None:
    if not cluster_exists():
        raise RuntimeError(f"cluster {CLUSTER} does not exist; run 'up' first")
    if not KUBECONFIG.is_file():
        write_kubeconfig()
    ensure_secrets()
    import_images()
    kubectl("apply", "-f", str(ROOT / "deployments/k8s/base/namespace.yaml"))
    apply_private_secret("contract-audit-postgres", ("POSTGRES_PASSWORD",))
    apply_private_secret("contract-audit-db-urls", ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL",
                                                      "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL",
                                                      "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL", "DEV_DATABASE_URL"))
    apply_private_secret("contract-audit-service-tokens", (
        "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
        "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN", "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN"))
    apply_private_secret("contract-audit-rustfs", ("RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY"))
    apply_private_secret("contract-audit-model", ("LLM_API_KEY", "LLM_CONFIG_KEY"))
    kubectl("delete", "job/rustfs-bucket-init", "-n", NAMESPACE, "--ignore-not-found=true", check=False)
    apply_phase("bootstrap")
    wait_platform()
    kubectl("delete", "job/contract-audit-migrate", "-n", NAMESPACE, "--ignore-not-found=true", check=False)
    apply_phase("migrate")
    kubectl("wait", "--for=condition=complete", "job/contract-audit-migrate", "-n", NAMESPACE,
            "--timeout=900s", timeout=930)
    if FULL:
        apply_observe_configs()
    apply_phase(APPS_OVERLAY)
    kubectl("rollout", "restart", "deployment", "-l", "app.kubernetes.io/part-of=contract-audit",
            "-n", NAMESPACE)
    for deployment in ("gateway", "core-api", "document-service", "audit-execution", "audit-worker",
                       "model-gateway", "tool-gateway", "client", "admin", "portal", "health-monitor"):
        kubectl("rollout", "status", f"deployment/{deployment}", "-n", NAMESPACE, "--timeout=240s", timeout=270)
    print("Local resources applied; application and health-monitor images were imported from Docker.")


def status() -> None:
    if not cluster_exists():
        print(f"Cluster {CLUSTER}: absent")
        return
    if not KUBECONFIG.is_file():
        write_kubeconfig()
    print(kubectl("get", "nodes", "-o", "wide").stdout, end="")
    result = kubectl("get", "all", "-n", NAMESPACE, "-o", "wide", check=False)
    print(result.stdout, end="")
    print("Ports: client http://127.0.0.1:18080, admin http://127.0.0.1:18081, portal http://127.0.0.1:18082")
    print(f"kubectl --kubeconfig {KUBECONFIG} -n {NAMESPACE} port-forward svc/client 18080:80")
    print(f"kubectl --kubeconfig {KUBECONFIG} -n {NAMESPACE} port-forward svc/admin 18081:80")
    print(f"kubectl --kubeconfig {KUBECONFIG} -n {NAMESPACE} port-forward svc/portal 18082:80")


def smoke() -> None:
    # A restricted Job performs an HTTP request using cluster DNS.
    kubectl("delete", "job/service-dns-smoke", "-n", NAMESPACE, "--ignore-not-found=true", check=False)
    kubectl("apply", "-f", str(ROOT / "deployments/k3d/smoke-dns.yaml"))
    kubectl("wait", "--for=condition=complete", "job/service-dns-smoke", "-n", NAMESPACE, "--timeout=90s")
    kubectl("logs", "job/service-dns-smoke", "-n", NAMESPACE)
    kubectl("delete", "job/service-dns-smoke", "-n", NAMESPACE, "--wait=true")
    kubectl("exec", "statefulset/db", "-n", NAMESPACE, "--", "pg_isready", "-h", "db", "-U",
            "contractaudit", "-d", "contract_audit_dev")
    kubectl("wait", "--for=condition=Ready", "pod", "-l", "app.kubernetes.io/name=health-monitor",
            "-n", NAMESPACE, "--timeout=120s")
    # Recover one stateless frontend pod without touching other workloads or data.
    kubectl("delete", "pod", "-l", "app.kubernetes.io/name=client", "-n", NAMESPACE, "--wait=true")
    kubectl("wait", "--for=condition=Ready", "pod", "-l", "app.kubernetes.io/name=client", "-n", NAMESPACE,
            "--timeout=180s")
    if FULL:
        # The full profile owns a fixed three-replica worker topology. Scaling it
        # down here would silently turn the later distribution evidence into a
        # single-replica result.
        desired = kubectl("get", "deployment/audit-worker", "-n", NAMESPACE,
                          "-o", "jsonpath={.spec.replicas}").stdout.strip()
        ready = kubectl("get", "deployment/audit-worker", "-n", NAMESPACE,
                        "-o", "jsonpath={.status.readyReplicas}").stdout.strip()
        if desired != "3" or ready != "3":
            raise RuntimeError(f"full-profile audit-worker is not 3/3: desired={desired} ready={ready}")
    else:
        # Exercise the worker controller at 1 -> 2 -> 1 and wait for each desired state.
        kubectl("scale", "deployment/audit-worker", "-n", NAMESPACE, "--replicas=2")
        kubectl("rollout", "status", "deployment/audit-worker", "-n", NAMESPACE, "--timeout=180s")
        kubectl("scale", "deployment/audit-worker", "-n", NAMESPACE, "--replicas=1")
        kubectl("rollout", "status", "deployment/audit-worker", "-n", NAMESPACE, "--timeout=180s")
    # Keep this intentional restart scoped to the worker deployment.
    kubectl("rollout", "restart", "deployment/audit-worker", "-n", NAMESPACE)
    kubectl("rollout", "status", "deployment/audit-worker", "-n", NAMESPACE, "--timeout=180s")
    kubectl("get", "pods", "-n", NAMESPACE, "-o", "wide")
    print("Smoke sequence completed: Service DNS/HTTP, PostgreSQL DNS, pod recovery, worker 1->2->1, worker rolling restart.")


def main(argv: list[str] | None = None) -> int:
    if PROFILE not in ("light", "full"):
        print("BLOCKED: CAW_K3D_PROFILE must be light or full", file=sys.stderr)
        return 2
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("up", "deploy", "status", "smoke"))
    args = parser.parse_args(argv)
    try:
        if args.action == "up":
            up()
        elif args.action == "deploy":
            deploy()
        elif args.action == "status":
            status()
        else:
            smoke()
    except (OSError, subprocess.SubprocessError, ValueError, RuntimeError, KeyError) as error:
        print(f"BLOCKED: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

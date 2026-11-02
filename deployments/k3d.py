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
from urllib.parse import quote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
PROFILE = os.environ.get("CAW_K3D_PROFILE", "light").strip().lower()
FULL = PROFILE == "full"
CLUSTER = os.environ.get("CAW_K3D_CLUSTER", "caw-local-full" if FULL else "caw-local")
CLUSTER_CONFIG = "cluster-5node.yaml" if FULL else "cluster.yaml"
APPS_OVERLAY = "apps-5node" if FULL else "apps"


def port_setting(name: str, default: str) -> str:
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    if not raw.isascii() or not raw.isdigit() or not 1 <= int(raw) <= 65535:
        print(f"BLOCKED: {name} must be a TCP port between 1 and 65535, got {raw!r}", file=sys.stderr)
        raise SystemExit(2)
    return raw


API_PORT = port_setting("CAW_K3D_API_PORT", "6446" if FULL else "6445")
# Browser-facing host ports for the UI services; they only materialize through
# kubectl port-forward and must match CORS_ORIGIN and the OIDC redirect URLs.
DEFAULT_UI_PORTS = ("18080", "18081", "18082")
CLIENT_PORT = port_setting("CAW_K3D_CLIENT_PORT", DEFAULT_UI_PORTS[0])
ADMIN_PORT = port_setting("CAW_K3D_ADMIN_PORT", DEFAULT_UI_PORTS[1])
PORTAL_PORT = port_setting("CAW_K3D_PORTAL_PORT", DEFAULT_UI_PORTS[2])
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
# Frontends and backend are packaged by deployments/scripts/package-app-images.py.
# The default suffix keeps the shared :local tags the Compose stack uses; an
# isolated acceptance passes CAW_K3D_IMAGE_SUFFIX to deploy images that were
# packaged under its own tag (--tag-suffix) and never retags the shared ones.
IMAGE_SUFFIX = os.environ.get("CAW_K3D_IMAGE_SUFFIX", "").strip()
_ALLOWED_SUFFIX = frozenset("abcdefghijklmnopqrstuvwxyz0123456789_.-")
if IMAGE_SUFFIX and (not IMAGE_SUFFIX.isascii() or len(IMAGE_SUFFIX) > 49
                     or set(IMAGE_SUFFIX) - _ALLOWED_SUFFIX
                     or not IMAGE_SUFFIX[0].isalnum() or not IMAGE_SUFFIX[0].isascii()):
    print(f"BLOCKED: CAW_K3D_IMAGE_SUFFIX must match [a-z0-9][a-z0-9_.-]{{0,48}}, got {IMAGE_SUFFIX!r}",
          file=sys.stderr)
    raise SystemExit(2)
APP_IMAGES = ("backend", "client", "admin", "portal")


def app_image(name: str) -> str:
    return f"contract-audit-{name}:{IMAGE_SUFFIX or 'local'}"


IMAGE_TAGS = tuple(app_image(name) for name in APP_IMAGES) + (
    "postgres:18.6-bookworm",
    "rustfs/rustfs:1.0.3",
    "nats:2.15.1-alpine@sha256:7ae34c25dfd6fe2e01c6483eabef20d796690fdcac6c4d94ca780a6cf9877fde",
    "amazon/aws-cli:2.31.14",
)
DELIVERY = Path(os.environ.get("CAW_DELIVERY_DIR", str(ROOT.parent / "contract-audit-delivery"))).expanduser().resolve()
DEFAULT_ARTIFACT_DIR = "k3d-full" if FULL else "k3d"
ARTIFACTS = Path(os.environ.get("CAW_K3D_ARTIFACT_DIR", str(DELIVERY / DEFAULT_ARTIFACT_DIR))).expanduser().resolve()
KUBECONFIG = ARTIFACTS / "kubeconfig"
SECRETS = ARTIFACTS / "secrets.json"
SERVICE_TOKEN_KEYS = (
    "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
    "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN",
    "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN", "CORE_MODEL_AUTHORITY_TOKEN",
)


def validate_service_tokens(values: dict[str, str]) -> None:
    seen: dict[str, str] = {}
    for key in SERVICE_TOKEN_KEYS:
        value = values.get(key)
        if not isinstance(value, str) or len(value) < 32:
            raise RuntimeError(f"private credentials require a distinct token of at least 32 characters: {key}")
        previous = seen.get(value)
        if previous:
            raise RuntimeError(f"private service credentials must be distinct: {previous} and {key}")
        seen[value] = key
    admin = urlsplit(str(values.get("AUTH_ADMIN_DATABASE_URL", "")))
    migrator = urlsplit(str(values.get("MIGRATOR_DATABASE_URL", "")))
    prod_migrator = urlsplit(str(values.get("PROD_DATABASE_URL", "")))
    if (admin.scheme not in {"postgres", "postgresql"} or not admin.hostname or not admin.username or not admin.password or
            migrator.scheme not in {"postgres", "postgresql"} or not migrator.hostname or not migrator.username or not migrator.password or
            prod_migrator.scheme not in {"postgres", "postgresql"} or not prod_migrator.hostname or not prod_migrator.username or not prod_migrator.password or
            admin.path != migrator.path or admin.hostname != migrator.hostname or (admin.port or 5432) != (migrator.port or 5432) or
            admin.username == migrator.username or migrator.username != "audit_offline_migrator" or
            prod_migrator.username != "audit_offline_migrator" or prod_migrator.path != "/contract_audit_prod" or
            prod_migrator.hostname != migrator.hostname or (prod_migrator.port or 5432) != (migrator.port or 5432)):
        raise RuntimeError("AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL must be distinct private logins for the same database; migrator must use audit_offline_migrator")


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
        needs_upgrade = ("DEV_DATABASE_URL" not in values or "WORKER_CORE_TOKEN" not in values or
                         "CORE_DOCUMENT_TOKEN" not in values or "TOOL_CORE_AUTHORITY_TOKEN" not in values or
                         "CORE_MODEL_AUTHORITY_TOKEN" not in values or "AUTH_ADMIN_DATABASE_URL" not in values or
                         "PROD_DATABASE_URL" not in values or
                         "LLM_CONFIG_KEY" not in values or "CLM_DATA_KEY" not in values or
                         urlsplit(str(values.get("MIGRATOR_DATABASE_URL", ""))).username != "audit_offline_migrator" or
                         urlsplit(str(values.get("PROD_DATABASE_URL", ""))).username != "audit_offline_migrator")
        if needs_upgrade:
            previous_migrator = values["MIGRATOR_DATABASE_URL"]
            values.setdefault("AUTH_ADMIN_DATABASE_URL", previous_migrator)
            if urlsplit(previous_migrator).username != "audit_offline_migrator":
                old = urlsplit(previous_migrator)
                password = secrets.token_urlsafe(48)
                credentials = f"audit_offline_migrator:{quote(password, safe='')}@{old.hostname}:{old.port or 5432}"
                new_migrator = old._replace(netloc=credentials).geturl()
                values["MIGRATOR_DATABASE_URL"] = new_migrator
                values["DEV_DATABASE_URL"] = new_migrator
                values["PROD_DATABASE_URL"] = old._replace(netloc=credentials, path="/contract_audit_prod").geturl()
            else:
                values.setdefault("DEV_DATABASE_URL", previous_migrator)
            offline = urlsplit(values["MIGRATOR_DATABASE_URL"])
            values["PROD_DATABASE_URL"] = offline._replace(path="/contract_audit_prod").geturl()
            values.setdefault("WORKER_CORE_TOKEN", secrets.token_urlsafe(48))
            values.setdefault("CORE_DOCUMENT_TOKEN", secrets.token_urlsafe(48))
            values.setdefault("TOOL_CORE_AUTHORITY_TOKEN", secrets.token_urlsafe(48))
            values.setdefault("CORE_MODEL_AUTHORITY_TOKEN", secrets.token_urlsafe(48))
            values.setdefault("PROD_DATABASE_URL",
                              f"postgres://contractaudit:{quote(values['POSTGRES_PASSWORD'], safe='')}@db:5432/contract_audit_prod?sslmode=disable")
            values.setdefault("LLM_CONFIG_KEY", base64.b64encode(secrets.token_bytes(32)).decode("ascii"))
            values.setdefault("CLM_DATA_KEY", base64.b64encode(secrets.token_bytes(32)).decode("ascii"))
        validate_service_tokens(values)
        if needs_upgrade:
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
        # Seals contract restricted supplier fields; a distinct 32-byte key.
        "CLM_DATA_KEY": base64.b64encode(secrets.token_bytes(32)).decode("ascii"),
    }
    for key, role in (("CORE_DATABASE_URL", "core_api"), ("DOCUMENT_DATABASE_URL", "document_service"),
                      ("EXECUTION_DATABASE_URL", "audit_execution"), ("MODEL_DATABASE_URL", "model_gateway"),
                      ("TOOL_DATABASE_URL", "tool_gateway")):
        password = secrets.token_hex(32)
        values[key] = f"postgres://{role}:{quote(password, safe='')}@db:5432/contract_audit_dev?sslmode=disable"
    offline_password = quote(secrets.token_urlsafe(48), safe="")
    values["MIGRATOR_DATABASE_URL"] = (
        f"postgres://audit_offline_migrator:{offline_password}@db:5432/contract_audit_dev?sslmode=disable"
    )
    values["DEV_DATABASE_URL"] = values["MIGRATOR_DATABASE_URL"]
    # The superuser DSN is consumed only by short-lived offline provisioning.
    values["AUTH_ADMIN_DATABASE_URL"] = (
        f"postgres://contractaudit:{quote(postgres_password, safe='')}@db:5432/contract_audit_dev?sslmode=disable"
    )
    values["PROD_DATABASE_URL"] = (
        f"postgres://audit_offline_migrator:{offline_password}@db:5432/contract_audit_prod?sslmode=disable"
    )
    for key in SERVICE_TOKEN_KEYS:
        values[key] = secrets.token_urlsafe(48)
    validate_service_tokens(values)
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
            "k3d", "cluster", "create", CLUSTER, "--config", str(ROOT / "deployments/k3d" / CLUSTER_CONFIG),
            "--api-port", "127.0.0.1:" + API_PORT, "--servers-memory", SERVER_MEMORY,
            "--agents-memory", AGENTS_MEMORY,
            "--kubeconfig-update-default=false", "--kubeconfig-switch-context=false",
            "--wait", "--timeout", "180s",
        ], timeout=240)
    write_kubeconfig()
    kubectl("wait", "--for=condition=Ready", "nodes", "--all", "--timeout=120s", timeout=150)
    agents = 4 if FULL else 2
    print(f"Cluster: {CLUSTER} (1 server, {agents} agents, profile={PROFILE}, "
          f"images={IMAGE_SUFFIX or 'local'}); kubeconfig: {KUBECONFIG}")


def cluster_nodes() -> list[str]:
    """Server and agent container names of the isolated cluster."""
    result = run(["k3d", "node", "list", "--output", "json"])
    listing = json.loads(result.stdout)
    if isinstance(listing, dict):
        listing = listing.get("nodes", [])
    if not isinstance(listing, list):
        raise RuntimeError("k3d returned an unexpected node inventory")
    nodes: list[str] = []
    for item in listing:
        if not isinstance(item, dict) or item.get("role") not in ("server", "agent"):
            continue
        labels = item.get("runtimeLabels")
        owner = item.get("cluster") or (labels.get("k3d.cluster") if isinstance(labels, dict) else None)
        if owner == CLUSTER:
            nodes.append(str(item["name"]))
    return nodes


def import_images() -> None:
    """Load the packaged images into every node's k8s.io containerd namespace.

    ``k3d image import`` is not used: for manifest lists it runs
    ``ctr image import --all-platforms``, which fails with "content digest ...
    not found" on every node and still exits 0, leaving the pods to fall back to
    a registry pull (ImagePullBackOff in a network-restricted host). Saving the
    image locally and importing only the running platform is verifiable, and the
    final listing check fails the run instead of silently continuing.
    """
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
    nodes = cluster_nodes()
    if not nodes:
        raise RuntimeError(f"cluster {CLUSTER} has no server or agent nodes to import into")
    private_dir()
    archive = ARTIFACTS / ".image-import.tar"
    try:
        for image in tags:
            saved = subprocess.run([docker, "save", image, "-o", str(archive)], text=True, capture_output=True,
                                   timeout=1800)
            if saved.returncode:
                raise RuntimeError(f"docker save failed for {image}: {saved.stderr.strip()}")
            for node in nodes:
                with archive.open("rb") as stream:
                    imported = subprocess.run(
                        [docker, "exec", "-i", node, "ctr", "-n", "k8s.io", "images", "import",
                         "--platform", "linux/amd64", "-"],
                        stdin=stream, text=False, capture_output=True, timeout=1800)
                if imported.returncode:
                    detail = (imported.stderr or imported.stdout or b"").decode("utf-8", "replace").strip()
                    raise RuntimeError(f"ctr import failed for {image} on {node}: {detail}")
    finally:
        archive.unlink(missing_ok=True)
    for image in tags:
        reference = image if ":" in image.rsplit("/", 1)[-1] else image + ":latest"
        for node in nodes:
            listing = run([docker, "exec", node, "ctr", "-n", "k8s.io", "images", "ls", "-q"], timeout=120).stdout
            if not any(reference == line.strip() or line.strip().endswith("/" + reference)
                       for line in listing.splitlines()):
                raise RuntimeError(f"{image} is missing from node {node} after import")


def recover_stale_image_pulls() -> None:
    """Recreate owned pods still waiting on images imported into containerd."""
    document = json.loads(kubectl("get", "pods", "-n", NAMESPACE, "-o", "json").stdout)
    for pod in document.get("items", []):
        metadata = pod.get("metadata", {})
        name = metadata.get("name")
        if not name or not metadata.get("ownerReferences"):
            continue
        status = pod.get("status") or {}
        statuses = list(status.get("containerStatuses") or [])
        statuses.extend(status.get("initContainerStatuses") or [])
        conditions = {item.get("type"): item.get("status")
                      for item in status.get("conditions") or []}
        waiting_images: set[str] = set()
        for container in statuses:
            waiting = (container.get("state") or {}).get("waiting") or {}
            reason = waiting.get("reason")
            image = container.get("image")
            if image not in IMAGE_TAGS:
                continue
            if reason in {"ErrImagePull", "ImagePullBackOff"}:
                waiting_images.add(image)
            elif reason == "ContainerCreating" and conditions.get("PodReadyToStartContainers") == "False":
                waiting_images.add(image)
        if waiting_images:
            kubectl("delete", "pod", str(name), "-n", NAMESPACE, "--wait=true", timeout=120)
            print(f"Recreated {name} after importing {', '.join(sorted(waiting_images))}.")


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


def staged_overlay(overlay: Path) -> Path:
    """Point an overlay at the isolated application image tags.

    Kustomize only accepts resources below its own root, so the generated
    parent kustomization stays under the private artifact directory and refers
    back to the checked-in overlay with a relative path. Nothing is written
    inside the source tree, and the default suffix bypasses this entirely.
    """
    staged = ARTIFACTS / "kustomize" / overlay.name
    private_dir()
    staged.mkdir(parents=True, exist_ok=True, mode=0o700)
    lines = [
        "apiVersion: kustomize.config.k8s.io/v1beta1",
        "kind: Kustomization",
        "resources:",
        f"  - {os.path.relpath(overlay, staged)}",
        "images:",
    ]
    lines.extend(f"  - {{name: contract-audit-{name}, newTag: {IMAGE_SUFFIX}}}" for name in APP_IMAGES)
    (staged / "kustomization.yaml").write_text("\n".join(lines) + "\n", encoding="utf-8")
    return staged


def apply_phase(path: str) -> None:
    overlay = ROOT / "deployments/k3d" / path
    if IMAGE_SUFFIX:
        overlay = staged_overlay(overlay)
    manifest = run(["kubectl", "kustomize", str(overlay), "--load-restrictor", "LoadRestrictionsNone"], timeout=300).stdout
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


def apply_runtime_origin_override() -> None:
    # The in-repo config patch pins CORS/OIDC to the default UI ports; when the
    # isolated acceptance binds free ports, keep the runtime ConfigMap consistent
    # with the port-forward URLs instead of editing tracked manifests.
    patch = {"data": {
        "CORS_ORIGIN": ",".join(f"http://127.0.0.1:{port}" for port in (CLIENT_PORT, ADMIN_PORT, PORTAL_PORT)),
        "OIDC_REDIRECT_URL": f"http://127.0.0.1:{CLIENT_PORT}/api/auth/oidc/callback",
        "OIDC_ADMIN_REDIRECT_URL": f"http://127.0.0.1:{ADMIN_PORT}/api/auth/oidc/callback",
    }}
    applied = run(["kubectl", "--kubeconfig", str(KUBECONFIG), "-n", NAMESPACE, "patch",
                   "configmap", "contract-audit-runtime", "--type", "merge", "-p", json.dumps(patch)],
                  check=False)
    if applied.returncode:
        raise RuntimeError(f"failed applying runtime origin override: {applied.stderr.strip()}")


def deploy() -> None:
    if not cluster_exists():
        raise RuntimeError(f"cluster {CLUSTER} does not exist; run 'up' first")
    if not KUBECONFIG.is_file():
        write_kubeconfig()
    ensure_secrets()
    import_images()
    kubectl("apply", "-f", str(ROOT / "deployments/k8s/base/namespace.yaml"))
    recover_stale_image_pulls()
    apply_private_secret("contract-audit-postgres", ("POSTGRES_PASSWORD",))
    apply_private_secret("contract-audit-db-urls", ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL",
                                                      "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL",
                                                      "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL", "DEV_DATABASE_URL",
                                                      "AUTH_ADMIN_DATABASE_URL", "PROD_DATABASE_URL"))
    apply_private_secret("contract-audit-service-tokens", (
        "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
        "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN",
        "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN", "CORE_MODEL_AUTHORITY_TOKEN"))
    apply_private_secret("contract-audit-rustfs", ("RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY"))
    apply_private_secret("contract-audit-model", ("LLM_API_KEY", "LLM_CONFIG_KEY"))
    apply_private_secret("contract-audit-clm", ("CLM_DATA_KEY",))
    kubectl("delete", "job/rustfs-bucket-init", "-n", NAMESPACE, "--ignore-not-found=true", check=False)
    apply_phase("bootstrap")
    if (CLIENT_PORT, ADMIN_PORT, PORTAL_PORT) != DEFAULT_UI_PORTS:
        apply_runtime_origin_override()
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
    images = ", ".join(app_image(name) for name in APP_IMAGES)
    print(f"Local resources applied; imported application images: {images}.")


def status() -> None:
    if not cluster_exists():
        print(f"Cluster {CLUSTER}: absent")
        return
    if not KUBECONFIG.is_file():
        write_kubeconfig()
    print(kubectl("get", "nodes", "-o", "wide").stdout, end="")
    result = kubectl("get", "all", "-n", NAMESPACE, "-o", "wide", check=False)
    print(result.stdout, end="")
    print(f"Ports: client http://127.0.0.1:{CLIENT_PORT}, admin http://127.0.0.1:{ADMIN_PORT}, "
          f"portal http://127.0.0.1:{PORTAL_PORT}")
    print(f"kubectl --kubeconfig {KUBECONFIG} -n {NAMESPACE} port-forward svc/client {CLIENT_PORT}:80")
    print(f"kubectl --kubeconfig {KUBECONFIG} -n {NAMESPACE} port-forward svc/admin {ADMIN_PORT}:80")
    print(f"kubectl --kubeconfig {KUBECONFIG} -n {NAMESPACE} port-forward svc/portal {PORTAL_PORT}:80")


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

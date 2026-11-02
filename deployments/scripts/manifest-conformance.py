#!/usr/bin/env python3
"""Static conformance checks for the application deployment manifests.

Expectations are derived from the real source of truth, never counted from the
manifests: release entrypoints from the Dockerfile (built binaries and the copies
into the final backend stage), accepted roles and per-role database identity keys
from `internal/bootstrap/services.go`, the unified probe surface from
`internal/httpapi/health.go`, and the per-role credential/dependency contract
from the process entrypoints.

The Kustomize kustomizations are resolved so only *rendered* objects are
checked: the default base must contain zero HorizontalPodAutoscaler objects (the
autoscaling specs live in an unreferenced file), PodDisruptionBudgets stay in the
base, and the one-shot migration Job is applied through its own kustomization
before the base. ConfigMap references are resolved by name, Secrets against the
declared secret contract, and Services against the Deployments they select.

Nothing here contacts a cluster: a passing run is static evidence only, so
cluster behaviour stays NOT_RUN. `--self-test` mutates copies of the tree and
requires the checker to fail on each injected defect, so the checks are proven
to have teeth rather than hand-counted. Exits non-zero with every violation.
"""
from __future__ import annotations

import argparse
import hashlib
import re
import shutil
import sys
import tempfile
from pathlib import Path
from urllib.parse import urlsplit

try:
    import yaml
except ImportError:  # pragma: no cover - explicit, never silent
    print("FAIL: PyYAML is required: python3 -m pip install pyyaml", file=sys.stderr)
    raise SystemExit(1)


class UniqueKeyLoader(yaml.SafeLoader):
    """Reject duplicate explicit keys (including repeated YAML merge keys)."""

    def construct_mapping(self, node, deep=False):
        seen = set()
        for key_node, _ in node.value:
            key = "<<" if key_node.tag == "tag:yaml.org,2002:merge" else self.construct_object(key_node, deep=deep)
            if key in seen:
                raise yaml.constructor.ConstructorError(
                    "while constructing a mapping", node.start_mark,
                    f"duplicate YAML key {key!r}", key_node.start_mark,
                )
            seen.add(key)
        self.flatten_mapping(node)
        return super().construct_mapping(node, deep=deep)

# Secret names and the keys this release may reference. A reference outside this
# contract is a typo or an undeclared dependency and must fail.
SECRET_CONTRACT = {
    "contract-audit-db-urls": {
        "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL",
        "MODEL_DATABASE_URL", "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL",
        "AUTH_ADMIN_DATABASE_URL", "DEV_DATABASE_URL", "PROD_DATABASE_URL",
    },
    "contract-audit-service-tokens": {
        "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
        "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN",
        "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN",
        "CORE_MODEL_AUTHORITY_TOKEN",
    },
    "contract-audit-oidc": {"OIDC_CLIENT_SECRET", "OIDC_ADMIN_CLIENT_SECRET", "OIDC_STATE_KEY"},
    "contract-audit-rustfs": {"RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY"},
    "contract-audit-model": {"LLM_API_KEY", "LLM_CONFIG_KEY", "TYPESAFE_API_KEY"},
    "contract-audit-clm": {"CLM_DATA_KEY"},
}

REQUIRED = {
    "core": ("CORE_DATABASE_URL", "GATEWAY_ROLE_TOKEN", "WORKER_CORE_TOKEN", "CORE_DOCUMENT_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN", "MODEL_GATEWAY_URL"),
    "document": (
        "DOCUMENT_DATABASE_URL", "GATEWAY_ROLE_TOKEN", "MODEL_GATEWAY_URL",
        "DOCUMENT_MODEL_TOKEN", "WORKER_DOCUMENT_TOKEN", "CORE_DOCUMENT_TOKEN",
        "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY",
    ),
    "execution": ("EXECUTION_DATABASE_URL", "GATEWAY_ROLE_TOKEN"),
    "worker": (
        "EXECUTION_DATABASE_URL", "HEALTH_ADDR", "DOCUMENT_SERVICE_URL",
        "WORKER_DOCUMENT_TOKEN", "MODEL_GATEWAY_URL", "WORKER_MODEL_TOKEN",
        "TOOL_GATEWAY_URL", "WORKER_TOOL_TOKEN", "CORE_SERVICE_URL", "WORKER_CORE_TOKEN",
    ),
    "model": ("MODEL_DATABASE_URL", "WORKER_MODEL_TOKEN", "DOCUMENT_MODEL_TOKEN", "LLM_API_KEY"),
    "tool": ("TOOL_DATABASE_URL", "GATEWAY_ROLE_TOKEN", "WORKER_TOOL_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN"),
}
GATEWAY_ENV = (
    "CORE_URLS", "DOCUMENT_URL", "EXECUTION_URL", "TOOL_URLS",
    "GATEWAY_HEALTH_INTERVAL", "GATEWAY_HEALTH_TIMEOUT",
    "GATEWAY_HEALTH_FAILURE_THRESHOLD", "GATEWAY_HEALTH_RECOVERY_THRESHOLD",
    "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
)
IDENTITY_SERVICES = ("core", "document", "execution", "worker")
IDENTITY_KEYS = ("LLM_BASE_URL", "LLM_MODEL", "OCR_MODEL")
JEV_ADVISORY_KEYS = (
    "TYPESAFE_JEV_MODE", "TYPESAFE_JEV_EGRESS", "TYPESAFE_JEV_FEATURES",
    "TYPESAFE_JEV_MODEL", "TYPESAFE_JEV_PROVIDER",
)
JEV_MODEL_KEYS = JEV_ADVISORY_KEYS + ("TYPESAFE_JEV_BASE_URL",)
INTERNAL_TOKEN_KEYS = ("WORKER_MODEL_TOKEN", "WORKER_DOCUMENT_TOKEN", "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN", "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN", "TOOL_CORE_AUTHORITY_TOKEN", "CORE_MODEL_AUTHORITY_TOKEN")
PROBES = ("startupProbe", "readinessProbe", "livenessProbe")
HTTP_PORT_HINT = {"core": 8081, "document": 8082, "execution": 8083, "worker": 8086, "model": 8084, "tool": 8085}

failures: list[str] = []
notes: list[str] = []


def fail(message: str) -> None:
    failures.append(message)


def read_yaml_all(path: Path) -> list[dict]:
    try:
        return [doc for doc in yaml.load_all(path.read_text(encoding="utf-8"), Loader=UniqueKeyLoader) if doc]
    except (yaml.YAMLError, ValueError) as error:
        fail(f"invalid YAML in {path}: {error}")
        return []


def read_compose(path: Path) -> dict:
    documents = read_yaml_all(path)
    if len(documents) != 1 or not isinstance(documents[0], dict):
        fail(f"{path} must contain exactly one Compose mapping")
        return {}
    return documents[0]


def resolve_kustomization(root: Path, kustomization: Path) -> tuple[list[dict], list[Path]]:
    """Render the declared resources of one kustomization, recursively."""
    if not kustomization.exists():
        fail(f"missing kustomization {kustomization.relative_to(root)}")
        return [], []
    spec = yaml.safe_load(kustomization.read_text(encoding="utf-8")) or {}
    objects: list[dict] = []
    files: list[Path] = []
    for entry in spec.get("resources", []) or []:
        candidate = (kustomization.parent / str(entry)).resolve()
        if candidate.is_dir():
            nested, nested_files = resolve_kustomization(root, candidate / "kustomization.yaml")
            objects += nested
            files += nested_files
            continue
        if not candidate.exists():
            fail(f"{kustomization.relative_to(root)} references a missing resource {entry}")
            continue
        files.append(Path(candidate))
        objects += read_yaml_all(candidate)
    return objects, files


def dockerfile_expectations(root: Path) -> tuple[set[str], set[str], dict[str, str]]:
    """Backend runtime entrypoints, every built binary, and stage name map."""
    text = (root / "deploy/app/Dockerfile").read_text(encoding="utf-8")
    stages = set(re.findall(r"^FROM\s+\S+\s+AS\s+([A-Za-z0-9_.-]+)", text, re.MULTILINE))
    built: set[str] = set()
    for name in re.findall(r"-o\s+/out/([A-Za-z0-9_.-]+)", text):
        built.add(name)
        if not (root / "cmd" / name).is_dir():
            fail(f"deploy/app/Dockerfile builds ./cmd/{name}, which does not exist")
    for stage in re.findall(r"COPY --from=([A-Za-z0-9_.-]+)", text):
        if stage not in stages:
            fail(f"deploy/app/Dockerfile copies from undeclared stage {stage!r}")
    backend = re.search(r"^FROM\s+\S+\s+AS\s+backend$(.*?)(?=^FROM\s|\Z)", text, re.MULTILINE | re.DOTALL)
    if not backend:
        fail("deploy/app/Dockerfile declares no backend stage")
        return set(), built, stages
    copied = re.findall(r"COPY --from=go-build[^\n]*?/out/([A-Za-z0-9_.-]+)\s+/app/([A-Za-z0-9_.-]+)", backend.group(1))
    entrypoints = {f"/app/{target}" for source, target in copied if source == target}
    for source, target in copied:
        if source != target:
            fail(f"backend stage copies /out/{source} to a different name /app/{target}")
    for name in built:
        if f"/app/{name}" not in entrypoints:
            fail(f"the backend stage never copies /out/{name} into the image")
    return entrypoints, built, stages


def source_expectations(root: Path) -> tuple[set[str], set[str], dict[str, str], set[str]]:
    services = (root / "internal/bootstrap/services.go").read_text(encoding="utf-8")
    accepted: set[str] = set()
    open_service = re.search(r"func OpenService\([^)]*\).*?switch role\s*\{(.*?)\n\s*\}", services, re.DOTALL)
    if open_service:
        for group in re.findall(r'case ("[^"]+"(?:, "[^"]+")*):', open_service.group(1)):
            accepted |= set(re.findall(r'"([^"]+)"', group))
    if not accepted:
        fail("internal/bootstrap/services.go declares no accepted service role")
    dsn_line = next((line for line in services.splitlines() if "map[string]string{" in line and "_DATABASE_URL" in line), "")
    dsn_keys = dict(re.findall(r'"([a-z]+)":\s*"([A-Z_]+)"', dsn_line))
    if not dsn_keys:
        fail("internal/bootstrap/services.go declares no per-role database identity key")

    health = (root / "internal/httpapi/health.go").read_text(encoding="utf-8")
    match = re.search(r"HealthPaths\s*=\s*\[\]string\{([^}]*)\}", health)
    probes = set(re.findall(r'"([^"]+)"', match.group(1))) if match else set()
    if not probes:
        fail("internal/httpapi/health.go declares no HealthPaths surface")
    return accepted, set(), dsn_keys, probes


def compose_environment(environment: object) -> dict[str, str]:
    if isinstance(environment, dict):
        return {str(key): str(value) for key, value in environment.items()}
    values: dict[str, str] = {}
    if isinstance(environment, list):
        for item in environment:
            key, _, value = str(item).partition("=")
            values[key.strip()] = value.strip()
    return values

def valid_service_url(value: object) -> bool:
    try:
        parsed = urlsplit(str(value or "").strip())
        if parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username is not None or parsed.query or parsed.fragment:
            return False
        parsed.port
        return True
    except ValueError:
        return False


def is_operator_placeholder(value: object) -> bool:
    try:
        parsed = urlsplit(str(value or "").strip())
    except ValueError:
        return True
    host = (parsed.hostname or "").lower().rstrip(".")
    return (not host or host.startswith("replace-me") or host in {"example.com", "example.net", "example.org"} or
            host.endswith((".example.com", ".example.net", ".example.org")) or ".example." in host)


def check_core_authority_config(root: Path, mode: str) -> None:
    """Check the shared Core authority channel only at its actual consumers."""
    runtime_docs = read_yaml_all(root / "deployments/k8s/base/config.yaml")
    runtime = next((doc.get("data", {}) for doc in runtime_docs
                    if doc.get("kind") == "ConfigMap" and
                    (doc.get("metadata", {}).get("name") == "contract-audit-runtime")), {})
    required = ("CORE_AUTHORITY_URL", "CORE_AUTHORITY_CA_FILE",
                "CORE_AUTHORITY_ALLOW_INSECURE_DEV")
    for key in required:
        if key not in runtime:
            fail(f"production runtime ConfigMap is missing {key}")
    if runtime.get("APP_ENV") != "production":
        fail("production runtime ConfigMap must keep APP_ENV=production")
    if runtime.get("CORE_AUTHORITY_ALLOW_INSECURE_DEV") != "false":
        fail("production Core authority clients must set CORE_AUTHORITY_ALLOW_INSECURE_DEV=false")

    endpoint = str(runtime.get("CORE_AUTHORITY_URL", "")).strip()
    if mode == "template":
        if endpoint != "https://REPLACE-ME-CORE-AUTH-ENDPOINT":
            fail("production template must retain the explicit nondeployable HTTPS Core authority placeholder")
        notes.append("Core authority callback template: HTTPS operator placeholder; production preflight NOT_RUN")
    else:
        if is_operator_placeholder(endpoint):
            fail("production deployment preflight BLOCKED: replace CORE_AUTHORITY_URL with the trusted Core HTTPS route")
        if not valid_service_url(endpoint) or urlsplit(endpoint).scheme != "https":
            fail("production deployment preflight requires an absolute HTTPS CORE_AUTHORITY_URL origin")
        if urlsplit(endpoint).path not in {"", "/"}:
            fail("production deployment preflight requires CORE_AUTHORITY_URL to be an origin without a path")
        if not is_operator_placeholder(endpoint) and valid_service_url(endpoint) and urlsplit(endpoint).scheme == "https" and urlsplit(endpoint).path in {"", "/"}:
            notes.append("Core authority callback URL is a configured HTTPS origin; TLS network verification NOT_RUN")

    if runtime.get("CORE_AUTHORITY_CA_FILE", ""):
        ca_path = str(runtime["CORE_AUTHORITY_CA_FILE"]).strip()
        workloads = read_yaml_all(root / "deployments/k8s/base/workloads.yaml")
        for name in ("document-service", "audit-worker", "tool-gateway", "audit-execution", "model-gateway"):
            deployment = next((obj for obj in workloads if obj.get("kind") == "Deployment" and
                               obj.get("metadata", {}).get("name") == name), None)
            template = (deployment or {}).get("spec", {}).get("template", {}).get("spec", {})
            containers = template.get("containers", [])
            volume_map = {v.get("name"): v for v in template.get("volumes", [])}
            mounted = any((ca_path == mount.get("mountPath") or
                           ca_path.startswith(str(mount.get("mountPath", "")).rstrip("/") + "/")) and
                          volume_map.get(mount.get("name"), {}).get("configMap", {}).get("name") ==
                          "contract-audit-core-authority-ca"
                          for container in containers for mount in container.get("volumeMounts", []))
            if not mounted:
                fail(f"CORE_AUTHORITY_CA_FILE must name a file under the mounted trust ConfigMap in the {name} workload")

    compose = read_compose(root / "compose.app.yaml")
    expected_compose = {
        "CORE_AUTHORITY_URL": r"\$\{CORE_AUTHORITY_URL:\?[^}]*\}",
        "CORE_AUTHORITY_ALLOW_INSECURE_DEV": r"\$\{CORE_AUTHORITY_ALLOW_INSECURE_DEV:\?[^}]*\}",
        "CORE_AUTHORITY_CA_FILE": r"\$\{CORE_AUTHORITY_CA_FILE:-\}",
    }
    services = compose.get("services", {})
    ca_mount = {
        "type": "bind",
        "source": "${CORE_AUTHORITY_CA_FILE:-/dev/null}",
        "target": "${CORE_AUTHORITY_CA_FILE:-/run/contract-audit/core-authority-ca.pem}",
        "read_only": True,
        "bind": {"create_host_path": False},
    }
    authority_clients = {"document-service", "audit-worker", "tool-gateway", "audit-execution", "model-gateway", "model-gateway-fixture"}
    for name in sorted(authority_clients):
        service = services.get(name) or {}
        service_env = compose_environment(service.get("environment"))
        for key, pattern in expected_compose.items():
            if not re.fullmatch(pattern, service_env.get(key, "")):
                fail(f"compose {name} must explicitly pass {key} from the private profile")
        if ca_mount not in service.get("volumes", []):
            fail(f"compose {name} must mount the optional Core authority CA read-only at the configured path")
    authority_names = set(expected_compose)
    for name, service in services.items():
        if name in authority_clients:
            continue
        service_env = compose_environment(service.get("environment"))
        if authority_names & service_env.keys():
            fail(f"compose {name} must not receive unused Core authority client configuration")
        if ca_mount in service.get("volumes", []):
            fail(f"compose {name} must not mount the Core authority CA without an authority client")

    workloads = read_yaml_all(root / "deployments/k8s/base/workloads.yaml")
    for name in sorted(authority_clients - {"model-gateway-fixture"}):
        deployment = next((obj for obj in workloads if obj.get("kind") == "Deployment" and
                           obj.get("metadata", {}).get("name") == name), None)
        containers = ((deployment or {}).get("spec", {}).get("template", {}).get("spec", {})
                      .get("containers", []))
        uses_runtime = any(any((source.get("configMapRef") or {}).get("name") == "contract-audit-runtime"
                               for source in container.get("envFrom", [])) for container in containers)
        if not uses_runtime:
            fail(f"Kubernetes {name} must import the shared Core authority ConfigMap")

    k3d_docs = read_yaml_all(root / "deployments/k3d/config-patch.yaml")
    k3d = next((obj.get("data", {}) for obj in k3d_docs if obj.get("kind") == "ConfigMap" and
                obj.get("metadata", {}).get("name") == "contract-audit-runtime"), {})
    if (k3d.get("APP_ENV") != "development" or
            k3d.get("CORE_AUTHORITY_URL") != "http://core-api:8081" or
            k3d.get("CORE_AUTHORITY_ALLOW_INSECURE_DEV") != "true" or
            k3d.get("CORE_AUTHORITY_CA_FILE") != ""):
        fail("k3d development profile must use the exact bounded Core authority HTTP exception")


def check_experience_kubernetes(name: str, role: str, container: dict, env: dict[str, object], runtime: dict[str, object]) -> None:
    if role not in {"core", "worker"}:
        return
    source = env.get("WORKER_CORE_TOKEN")
    ref = source.get("secretKeyRef") if isinstance(source, dict) else None
    if not isinstance(ref, dict) or ref.get("name") != "contract-audit-service-tokens" or ref.get("key") != "WORKER_CORE_TOKEN" or ref.get("optional", False):
        fail(f"{name}: {role} must use the required WORKER_CORE_TOKEN secret key shared by core and worker")
    other_keys = ("GATEWAY_ROLE_TOKEN",) if role == "core" else ("WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN")
    for key in other_keys:
        other = env.get(key)
        other_ref = other.get("secretKeyRef") if isinstance(other, dict) else None
        if isinstance(other_ref, dict) and other_ref.get("name") == "contract-audit-service-tokens" and other_ref.get("key") == "WORKER_CORE_TOKEN":
            fail(f"{name}: WORKER_CORE_TOKEN must be distinct from {key}")
    if role != "worker":
        return
    if "CORE_SERVICE_URL" in env:
        source = env["CORE_SERVICE_URL"]
        ref = source.get("configMapKeyRef") if isinstance(source, dict) else None
        if not isinstance(ref, dict) or ref.get("name") != "contract-audit-runtime" or ref.get("key") != "CORE_SERVICE_URL" or ref.get("optional", False):
            fail(f"{name}: worker CORE_SERVICE_URL must use the runtime ConfigMap key")
    elif not any((item.get("configMapRef") or {}).get("name") == "contract-audit-runtime" and not (item.get("configMapRef") or {}).get("optional", False) for item in container.get("envFrom") or []):
        fail(f"{name}: worker must import CORE_SERVICE_URL from the runtime ConfigMap")
    if not valid_service_url(runtime.get("CORE_SERVICE_URL")):
        fail(f"{name}: runtime CORE_SERVICE_URL must be an HTTP(S) service URL without credentials, query, or fragment")


def check_experience_compose(name: str, role: str, environment: dict[str, str]) -> None:
    if role not in {"core", "worker"}:
        return
    if not re.fullmatch(r"\$\{WORKER_CORE_TOKEN:\?[^}]*\}", environment.get("WORKER_CORE_TOKEN", "")):
        fail(f"compose {name}: {role} must require the shared WORKER_CORE_TOKEN variable")
    other_keys = ("GATEWAY_ROLE_TOKEN",) if role == "core" else ("WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN")
    for key in other_keys:
        if re.match(r"\$\{WORKER_CORE_TOKEN(?=[:}])", environment.get(key, "")):
            fail(f"compose {name}: WORKER_CORE_TOKEN must be distinct from {key}")
    if role == "worker" and not re.fullmatch(r"\$\{CORE_SERVICE_URL:\?[^}]*\}", environment.get("CORE_SERVICE_URL", "")):
        fail(f"compose {name}: worker must require the CORE_SERVICE_URL variable")


def check_tool_authority_compose(name: str, role: str | None, environment: dict[str, str]) -> None:
    required = role in {"core", "tool"}
    value = environment.get("TOOL_CORE_AUTHORITY_TOKEN", "")
    if required:
        if not re.fullmatch(r"\$\{TOOL_CORE_AUTHORITY_TOKEN:\?[^}]*\}", value):
            fail(f"compose {name} must require TOOL_CORE_AUTHORITY_TOKEN from the private profile")
    elif value:
        fail(f"compose {name} must not receive the Core-to-Tool authority credential")


def check_tool_authority_kubernetes(name: str, role: str | None, env: dict[str, object]) -> None:
    required = role in {"core", "tool"}
    source = env.get("TOOL_CORE_AUTHORITY_TOKEN")
    ref = source.get("secretKeyRef") if isinstance(source, dict) else None
    if required:
        if (not isinstance(ref, dict) or ref.get("name") != "contract-audit-service-tokens" or
                ref.get("key") != "TOOL_CORE_AUTHORITY_TOKEN" or ref.get("optional", False)):
            fail(f"{name}: {role} must use the required TOOL_CORE_AUTHORITY_TOKEN service secret")
    elif source is not None:
        fail(f"{name}: {role or 'gateway'} must not receive TOOL_CORE_AUTHORITY_TOKEN")


def check_model_authority_compose(name: str, role: str | None, environment: dict[str, str]) -> None:
    required = role == "model" and name in {"model-gateway", "model-gateway-fixture"}
    allowed = required or role == "core"
    value = environment.get("CORE_MODEL_AUTHORITY_TOKEN", "")
    if required and not re.fullmatch(r"\$\{CORE_MODEL_AUTHORITY_TOKEN:\?[^}]*\}", value):
        fail("compose model-gateway must require CORE_MODEL_AUTHORITY_TOKEN from the private profile")
    elif role == "core" and not re.fullmatch(r"\$\{CORE_MODEL_AUTHORITY_TOKEN:\?[^}]*\}", value):
        fail("compose core must require CORE_MODEL_AUTHORITY_TOKEN for model authority mode")
    elif not allowed and value:
        fail(f"compose {name} must not receive CORE_MODEL_AUTHORITY_TOKEN")


def check_model_authority_kubernetes(name: str, role: str | None, env: dict[str, object]) -> None:
    required = role == "model" and name == "model-gateway"
    allowed = required or role == "core"
    source = env.get("CORE_MODEL_AUTHORITY_TOKEN")
    ref = source.get("secretKeyRef") if isinstance(source, dict) else None
    if required or role == "core":
        if (not isinstance(ref, dict) or ref.get("name") != "contract-audit-service-tokens" or
                ref.get("key") != "CORE_MODEL_AUTHORITY_TOKEN" or ref.get("optional", False)):
            fail(f"{name}: {role} must use the required CORE_MODEL_AUTHORITY_TOKEN service secret")
    elif source is not None:
        fail(f"{name}: {role or 'gateway'} must not receive CORE_MODEL_AUTHORITY_TOKEN")


def configmap_lookup(root: Path, objects: list[dict]) -> dict[str, set[str]]:
    configs: dict[str, set[str]] = {}
    for document in objects:
        if document.get("kind") == "ConfigMap":
            configs[document["metadata"]["name"]] = set((document.get("data") or {}).keys())
    return configs


def check_container_env(name: str, container: dict, role: str | None, configs: dict[str, set[str]], probes: set[str], surface: bool) -> None:
    env = {item["name"]: item.get("valueFrom", item.get("value")) for item in container.get("env", [])}
    if "AUTH_ADMIN_DATABASE_URL" in env:
        fail(f"{name}: AUTH_ADMIN_DATABASE_URL is reserved for the one-shot AUTH_FENCE provisioning command")
    config_from: set[str] = set()
    for source in container.get("envFrom") or []:
        reference = source.get("configMapRef") or source.get("secretRef") or {}
        if "configMapRef" in source:
            if reference.get("name") not in configs:
                fail(f"{name}: envFrom references unknown ConfigMap {reference.get('name')!r}")
            else:
                config_from |= configs[reference["name"]]
        elif reference.get("name") not in SECRET_CONTRACT:
            fail(f"{name}: envFrom references undeclared Secret {reference.get('name')!r}")
    for key, source in env.items():
        if not isinstance(source, dict):
            continue
        if "configMapKeyRef" in source:
            reference = source["configMapKeyRef"]
            if reference.get("name") not in configs or reference.get("key") not in configs[reference["name"]]:
                fail(f"{name}: env {key} references {reference.get('name')!r}/{reference.get('key')!r}, which no rendered ConfigMap declares")
        elif "secretKeyRef" in source:
            reference = source["secretKeyRef"]
            allowed = SECRET_CONTRACT.get(reference.get("name"), set())
            if reference.get("key") not in allowed:
                fail(f"{name}: env {key} references {reference.get('name')!r}/{reference.get('key')!r}, outside the declared secret contract")
    check_tool_authority_kubernetes(name, role, env)
    check_model_authority_kubernetes(name, role, env)
    ports = {entry.get("name") for entry in container.get("ports", [])} | {entry.get("containerPort") for entry in container.get("ports", [])}
    if surface and (role or name.endswith("gateway")):
        for probe_key in PROBES:
            probe = container.get(probe_key)
            http_get = (probe or {}).get("httpGet")
            if not http_get:
                fail(f"{name}: role {role or 'gateway'} declares no {probe_key}")
                continue
            if http_get.get("path") not in probes:
                fail(f"{name}: {probe_key} path {http_get.get('path')} is outside the unified health surface")
            if http_get.get("port") not in ports:
                fail(f"{name}: {probe_key} port {http_get.get('port')!r} is not a declared container port")
    elif surface:
        for key in PROBES:
            http_get = (container.get(key) or {}).get("httpGet")
            if http_get and http_get.get("port") not in ports:
                fail(f"{name}: {key} port {http_get.get('port')!r} is not a declared container port")


def check_gateway_env(name: str, env: dict[str, object], config_from: set[str], configs: dict[str, set[str]]) -> None:
    for key in GATEWAY_ENV:
        if key in env:
            continue
        if key.endswith("_TOKEN"):
            continue  # tokens are checked through their secret refs below
        if key in config_from or key in configs.get("contract-audit-runtime", set()):
            continue
        fail(f"{name}: the edge is missing {key}")
    for token in ("GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN"):
        source = env.get(token)
        if isinstance(source, dict):
            if source.get("secretKeyRef", {}).get("key") != token:
                fail(f"{name}: the edge must reference secret key {token}")
        elif not str(source or "").strip():
            fail(f"{name}: the edge must set {token}")
    leaked = sorted(set(env) & set(INTERNAL_TOKEN_KEYS))
    if leaked:
        fail(f"{name}: the edge must not hold internal credentials {leaked}")
    for key in env:
        if key in IDENTITY_KEYS or key == "GATEWAY_ROLE_TOKEN":
            fail(f"{name}: {key} is not part of the edge contract")


def check_jev_kubernetes(name: str, role: str, env: dict[str, object], config_from: set[str]) -> None:
    if role in {"core", "execution", "worker"}:
        for key in JEV_ADVISORY_KEYS:
            if key not in env and key not in config_from:
                fail(f"{name}: role {role} is missing Jev configuration {key}")
        if "TYPESAFE_API_KEY" in env:
            fail(f"{name}: only model-gateway may hold TYPESAFE_API_KEY")
    if role == "model":
        keys = JEV_ADVISORY_KEYS + (("TYPESAFE_JEV_BASE_URL",) if name == "model-gateway" else ())
        for key in keys:
            if key not in env and key not in config_from:
                fail(f"{name}: role model is missing Jev configuration {key}")
        source = env.get("TYPESAFE_API_KEY")
        reference = source.get("secretKeyRef") if isinstance(source, dict) else None
        if not isinstance(reference, dict) or reference.get("name") != "contract-audit-model" or \
                reference.get("key") != "TYPESAFE_API_KEY" or not reference.get("optional", False):
            fail(f"{name}: model-gateway must read an optional TYPESAFE_API_KEY secret")


def check_jev_compose(name: str, role: str, environment: dict[str, str]) -> None:
    if role in {"core", "execution", "worker"}:
        for key in JEV_ADVISORY_KEYS:
            if key not in environment:
                fail(f"compose {name}: role {role} is missing Jev configuration {key}")
    if role == "model":
        keys = JEV_ADVISORY_KEYS + (("TYPESAFE_JEV_BASE_URL",) if name == "model-gateway" else ())
        for key in keys:
            if key not in environment:
                fail(f"compose {name}: role model is missing Jev configuration {key}")
    if name not in {"model-gateway", "model-gateway-fixture"} and "TYPESAFE_API_KEY" in environment:
        fail(f"compose {name}: only the model gateway may hold TYPESAFE_API_KEY")


def check_kustomize(root: Path, entrypoints: set[str], roles: set[str], dsn_keys: dict[str, str], probes: set[str]) -> None:
    base_objects, base_files = resolve_kustomization(root, root / "deployments/k8s/base/kustomization.yaml")
    configs = configmap_lookup(root, base_objects)
    runtime = next((document.get("data") or {} for document in base_objects
                    if document.get("kind") == "ConfigMap" and document.get("metadata", {}).get("name") == "contract-audit-runtime"), {})
    if "contract-audit-runtime" not in configs:
        fail("the rendered base does not include the contract-audit-runtime ConfigMap")
    if str(runtime.get("APP_ENV", "")).strip() == "production":
        if str(runtime.get("TYPESAFE_JEV_MODE", "")).strip() != "off":
            fail("production base must disable Jev; enable advisory only in an authorized deployment overlay")
        if str(runtime.get("TYPESAFE_JEV_MODE", "")).strip() == "fixture" or \
                str(runtime.get("TYPESAFE_JEV_MODEL", "")).strip() == "jev-fixture":
            fail("production runtime must not enable the Jev fixture")
        if "TYPESAFE_API_KEY" in runtime:
            fail("TYPESAFE_API_KEY must not be stored in the runtime ConfigMap")
    kinds = [document.get("kind") for document in base_objects]
    if "HorizontalPodAutoscaler" in kinds:
        fail("the rendered base must contain zero HorizontalPodAutoscaler objects")
    if "PodDisruptionBudget" not in kinds:
        fail("the rendered base declares no PodDisruptionBudget")
    if "Job" in kinds:
        fail("the migration Job must be applied through deployments/k8s/migrate, not the base render")
    optional_hpa = root / "deployments/k8s/optional/scaling-hpa.yaml"
    if not optional_hpa.exists():
        fail("the unreferenced autoscaling spec file deployments/k8s/optional/scaling-hpa.yaml is missing")
    else:
        for document in read_yaml_all(optional_hpa):
            if document.get("kind") == "HorizontalPodAutoscaler":
                for metric in document["spec"]["metrics"]:
                    if metric.get("type") == "External":
                        fail(f"optional HPA {document['metadata']['name']} depends on an External metric with no producer")

    deployments = [document for document in base_objects if document.get("kind") == "Deployment"]
    if not deployments:
        fail("the rendered base declares no Deployment")
    pod_labels: list[dict] = []
    serving_roles: set[str] = set()
    for deployment in deployments:
        name = deployment["metadata"]["name"]
        spec = deployment["spec"]["template"]["spec"]
        pod_labels.append(deployment["spec"]["template"].get("metadata", {}).get("labels") or {})
        for container in spec.get("containers", []):
            image = str(container.get("image", ""))
            backend = "contract-audit-backend" in image
            for token in list(container.get("command") or []) + list(container.get("args") or []):
                if token.startswith("/app/") and token not in entrypoints:
                    fail(f"{name}: entrypoint {token} is not copied into the backend stage by deploy/app/Dockerfile")
            env = {item["name"]: item.get("valueFrom", item.get("value")) for item in container.get("env", [])}
            config_from: set[str] = set()
            for source in container.get("envFrom") or []:
                reference = (source.get("configMapRef") or {})
                config_from |= configs.get(reference.get("name"), set())
            role = env.get("SERVICE_ROLE")
            if name == "gateway":
                check_gateway_env(name, env, config_from, configs)
            check_container_env(name, container, role, configs, probes, True)
            if role is None:
                if backend and name != "gateway":
                    fail(f"{name}: backend container declares no SERVICE_ROLE")
                if name == "gateway":
                    serving_roles.add("gateway")
                continue
            if role not in roles:
                fail(f"{name}: SERVICE_ROLE {role!r} is not accepted by bootstrap.OpenService")
                continue
            serving_roles.add(role)
            check_jev_kubernetes(name, role, env, config_from)
            check_experience_kubernetes(name, role, container, env, runtime)
            dsn = dsn_keys.get(role)
            if dsn is None or dsn not in env:
                fail(f"{name}: role {role} must declare its database identity key {dsn or '(unknown role)'}")
                continue
            for key in REQUIRED.get(role, ()):
                if key in env or key in config_from:
                    continue
                if key == "LLM_API_KEY" and str(env.get("MODEL_GATEWAY_MODE")).strip('"') == "fixture":
                    continue
                fail(f"{name}: role {role} is missing required env {key}")
            for key in IDENTITY_KEYS:
                if key not in env and key not in config_from:
                    fail(f"{name}: model identity key {key} is not declared for role {role}")
            hint = HTTP_PORT_HINT.get(role)
            if hint and hint not in {entry.get("containerPort") for entry in container.get("ports", [])}:
                fail(f"{name}: role {role} does not expose its HTTP port {hint}")

    missing = roles - serving_roles
    if missing:
        fail(f"the rendered base declares no Deployment for roles: {sorted(missing)}")

    for document in base_objects:
        if document.get("kind") != "Service":
            continue
        selector = document["spec"].get("selector") or {}
        if not any(all(labels.get(k) == v for k, v in selector.items()) for labels in pod_labels):
            fail(f"Service {document['metadata']['name']} selects no Deployment pod")
        for port in document["spec"].get("ports", []):
            target = port.get("targetPort")
            if isinstance(target, str) and target not in {
                entry.get("name") for deployment in deployments
                for container in deployment["spec"]["template"]["spec"]["containers"]
                for entry in container.get("ports", [])
            }:
                fail(f"Service {document['metadata']['name']} targets undefined port name {target!r}")

    migration, migration_files = resolve_kustomization(root, root / "deployments/k8s/migrate/kustomization.yaml")
    jobs = [document for document in migration if document.get("kind") == "Job"]
    if len(jobs) != 1:
        fail(f"deployments/k8s/migrate must render exactly one migration Job, rendered {len(jobs)}")
    else:
        pod_spec = jobs[0]["spec"]["template"]["spec"]
        init = pod_spec.get("initContainers") or []
        expected_init = [
            ("offline-migrator-provision", "provision-offline-migrator"),
            ("clm-owner-provision", "provision-clm-maintenance"),
            ("auth-owner-provision", "provision-tenant-identity"),
        ]
        actual_init = []
        for item in init:
            argv = list(item.get("command") or []) + list(item.get("args") or [])
            actual_init.append((item.get("name"), argv[-1] if len(argv) == 2 and argv[0] == "/app/migrate" else ""))
        if actual_init != expected_init:
            fail("migration Job must run offline migrator, CLM owner, then AUTH_FENCE owner provisioning")
        else:
            if len(init) == 3 and any(item.get("image") != (pod_spec.get("containers") or [{}])[0].get("image") for item in init):
                fail("all migration provisioning steps must use the same backend image as ordinary migration")
            for item in init:
                provision_env = {entry["name"]: entry.get("valueFrom", entry.get("value"))
                                 for entry in item.get("env", [])}
                admin_key = "CLM_ADMIN_DATABASE_URL" if item.get("name") == "clm-owner-provision" else "AUTH_ADMIN_DATABASE_URL"
                for key in (admin_key, "MIGRATOR_DATABASE_URL"):
                    source = provision_env.get(key)
                    ref = source.get("secretKeyRef") if isinstance(source, dict) else None
                    secret_key = "AUTH_ADMIN_DATABASE_URL" if key == "CLM_ADMIN_DATABASE_URL" else key
                    if not isinstance(ref, dict) or ref.get("name") != "contract-audit-db-urls" or ref.get("key") != secret_key or ref.get("optional", False):
                        fail(f"{item.get('name')} must receive required {key} only from the private database Secret")
                for key in ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL"):
                    ref = (provision_env.get(key) or {}).get("secretKeyRef", {})
                    if ref.get("name") != "contract-audit-db-urls" or ref.get("key") != key or ref.get("optional", False):
                        fail(f"{item.get('name')} must receive required runtime identity {key}")
        containers = pod_spec.get("containers", [])
        if len(containers) != 1 or containers[0].get("name") != "migrate":
            fail("migration Job must contain exactly one ordinary migrate container")
            container = {}
        else:
            container = containers[0]
        argv = list(container.get("command") or []) + list(container.get("args") or [])
        if "/app/migrate" not in argv:
            fail("the migration Job must run /app/migrate")
        env = {item["name"]: item.get("valueFrom", item.get("value")) for item in container.get("env", [])}
        for admin_key in ("AUTH_ADMIN_DATABASE_URL", "CLM_ADMIN_DATABASE_URL"):
            if admin_key in env:
                fail(f"ordinary migration container must never receive {admin_key}")
        if env.get("SERVICE_DATABASE_ROLES") != "true":
            fail("the migration Job must set SERVICE_DATABASE_ROLES=true")
        for key in ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL"):
            if key not in env:
                fail(f"the migration Job is missing {key}")
        prod_source = env.get("PROD_DATABASE_URL")
        prod_ref = prod_source.get("secretKeyRef") if isinstance(prod_source, dict) else None
        if not isinstance(prod_ref, dict) or prod_ref.get("name") != "contract-audit-db-urls" or prod_ref.get("key") != "PROD_DATABASE_URL" or prod_ref.get("optional", False):
            fail("ordinary migration must require PROD_DATABASE_URL from the private database Secret")
        notes.append("release order: release-k8s.py bootstrap, migrate-k8s.sh <candidate-lock>, then release-k8s.py deploy <candidate-lock>")
    notes.append("cluster runtime: NOT_RUN (static manifest evidence only)")


def check_compose(root: Path, entrypoints: set[str], roles: set[str], dsn_keys: dict[str, str], probes: set[str]) -> None:
    compose = read_compose(root / "compose.app.yaml")
    services = compose.get("services") or {}
    if not services:
        fail("compose.app.yaml declares no service")
        return
    for name, service in services.items():
        if name == "gateway":
            environment = compose_environment(service.get("environment"))
            check_gateway_env("compose gateway", environment, set(environment), {})
            for key in GATEWAY_ENV:
                if key.endswith("_TOKEN") and key not in environment:
                    fail(f"compose gateway: the edge is missing {key}")
    for name, service in services.items():
        if service.get("deploy"):
            fail(f"compose {name}: declares a deploy block; the local stack runs one replica per service")
        for token in list(service.get("command") or []) + list(service.get("entrypoint") or []):
            if token.startswith("/app/") and token not in entrypoints:
                fail(f"compose {name}: entrypoint {token} is not copied into the backend stage by deploy/app/Dockerfile")
        environment = compose_environment(service.get("environment"))
        if name not in {"migrate-offline-migrator", "migrate-auth-owner"} and "AUTH_ADMIN_DATABASE_URL" in environment:
            fail(f"compose {name} must not receive AUTH_ADMIN_DATABASE_URL")
        if name != "migrate-clm-owner" and "CLM_ADMIN_DATABASE_URL" in environment:
            fail(f"compose {name} must not receive CLM_ADMIN_DATABASE_URL")
        if name == "health-monitor":
            for key in ("MODEL_GATEWAY_URL", *JEV_ADVISORY_KEYS):
                if key not in environment:
                    fail(f"compose health-monitor: missing non-secret Jev status {key}")
            if "TYPESAFE_API_KEY" in environment:
                fail("compose health-monitor: must not hold TYPESAFE_API_KEY")
        role = environment.get("SERVICE_ROLE")
        check_tool_authority_compose(name, role, environment)
        check_model_authority_compose(name, role, environment)
        healthcheck = " ".join(service.get("healthcheck", {}).get("test", []))
        if role is not None or name == "gateway":
            for url in re.findall(r"http://[^\s\"']+", healthcheck):
                path = "/" + url.split("/", 3)[-1] if url.count("/") >= 3 else "/"
                if path not in probes:
                    fail(f"compose {name}: healthcheck path {path} is outside the unified health surface")
        if role is None:
            continue
        if role not in roles:
            fail(f"compose {name}: SERVICE_ROLE {role!r} is not accepted by bootstrap.OpenService")
            continue
        check_experience_compose(name, role, environment)
        check_jev_compose(name, role, environment)
        dsn = dsn_keys.get(role)
        if dsn is None or dsn not in environment:
            fail(f"compose {name}: role {role} must declare its database identity key {dsn or '(unknown role)'}")
        for key in REQUIRED.get(role, ()):
            if key in environment:
                continue
            if key == "LLM_API_KEY" and environment.get("MODEL_GATEWAY_MODE", "").strip() == "fixture":
                continue
            fail(f"compose {name}: role {role} is missing required env {key}")

    provision_specs = (
        ("migrate-offline-migrator", "provision-offline-migrator"),
        ("migrate-clm-owner", "provision-clm-maintenance"),
        ("migrate-auth-owner", "provision-tenant-identity"),
    )
    provision_envs: dict[str, dict[str, str]] = {}
    for service_name, command_name in provision_specs:
        provision = services.get(service_name) or {}
        environment = compose_environment(provision.get("environment"))
        provision_envs[service_name] = environment
        if list(provision.get("command") or []) != ["/app/migrate", command_name]:
            fail(f"compose {service_name} must run /app/migrate {command_name}")
        admin_key = "CLM_ADMIN_DATABASE_URL" if service_name == "migrate-clm-owner" else "AUTH_ADMIN_DATABASE_URL"
        for key in (admin_key, "MIGRATOR_DATABASE_URL"):
            source_name = "AUTH_ADMIN_DATABASE_URL" if key == "CLM_ADMIN_DATABASE_URL" else key
            if not re.fullmatch(rf"\$\{{{source_name}:\?[^}}]*\}}", environment.get(key, "")):
                fail(f"compose {service_name} must require private {key}")
        for key in ("CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL"):
            if not re.fullmatch(rf"\$\{{{key}:\?[^}}]*\}}", environment.get(key, "")):
                fail(f"compose {service_name} must require runtime identity {key}")
    ordinary_migration = compose_environment((services.get("migrate") or {}).get("environment"))
    for admin_key in ("AUTH_ADMIN_DATABASE_URL", "CLM_ADMIN_DATABASE_URL"):
        if admin_key in ordinary_migration:
            fail(f"compose ordinary migration must not receive {admin_key}")
    for service_name, environment in (("migrate", ordinary_migration), *provision_envs.items()):
        for key in ("DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL"):
            if environment.get(key) != f"${{{key}:-}}":
                fail(f"compose {service_name} must pass only the private APP_ENV-selected {key} slot")

    identity: dict[str, str] = {}
    for role in IDENTITY_SERVICES:
        for name, service in services.items():
            environment = compose_environment(service.get("environment"))
            if environment.get("SERVICE_ROLE") != role:
                continue
            for key in IDENTITY_KEYS:
                declared = environment.get(key)
                if declared is None:
                    fail(f"compose {name}: role {role} does not declare model identity {key}")
                    continue
                previous = identity.setdefault(key, declared)
                if previous != declared:
                    fail(f"compose: {key} differs between services ({previous!r} vs {declared!r})")

    if "--scale" in (root / "deployments/local.py").read_text(encoding="utf-8"):
        fail("deployments/local.py still overrides replica counts")


def source_evidence(root: Path) -> tuple[str, int]:
    inputs = (
        "deployments/k8s", "deployments/local.py", "deployments/scripts/manifest-conformance.py",
        "deploy/app/Dockerfile", "deploy/app/nginx", "compose.app.yaml", "compose.observe.yaml", "deployments/compose.identity.yaml", "Makefile",
        "internal/bootstrap/services.go", "internal/bootstrap/runtime.go", "internal/httpapi/health.go",
        "internal/apidocs/openapi.json",
    )
    digest = hashlib.sha256()
    files = 0
    for entry in inputs:
        path = root / entry
        candidates = sorted(item for item in path.rglob("*") if item.is_file()) if path.is_dir() else [path]
        for candidate in candidates:
            digest.update(str(candidate.relative_to(root)).encode("utf-8"))
            digest.update(b"\0")
            digest.update(candidate.read_bytes())
            files += 1
    return digest.hexdigest(), files


def run_checks(root: Path, mode: str = "template") -> int:
    failures.clear()
    notes.clear()
    if mode not in {"template", "preflight"}:
        print(f"FAIL: unknown conformance mode {mode!r}", file=sys.stderr)
        return 2
    entrypoints, _, _ = dockerfile_expectations(root)
    roles, _, dsn_keys, probes = source_expectations(root)
    check_kustomize(root, entrypoints, roles, dsn_keys, probes)
    check_compose(root, entrypoints, roles, dsn_keys, probes)
    check_core_authority_config(root, mode)
    digest, files = source_evidence(root)
    print(f"evidence: source-digest sha256:{digest} over {files} deployment/source files")
    notes.append("production prerequisite: an audited identity directory must be mounted before APP_ENV=production starts")
    for note in notes:
        print(f"note: {note}")
    if failures:
        for failure in failures:
            print(f"FAIL: {failure}", file=sys.stderr)
        print(f"FAIL: {len(failures)} manifest conformance violation(s)", file=sys.stderr)
        return 1
    print(
        f"PASS: {len(entrypoints)} entrypoints, {len(roles)} roles, {len(probes)} probe paths, "
        "zero HPA in the render, migration checked, Kustomize base and compose.app.yaml conform"
    )
    if mode == "template":
        print("template PASS only; production deployment preflight NOT_RUN")
    else:
        print("static production configuration preflight PASS; cluster, DNS, TLS handshake, and callback NOT_RUN")
    return 0


SUBTREE = (
    "deployments", "deploy", "cmd", "internal/bootstrap", "internal/httpapi/health.go",
    "internal/apidocs/openapi.json", "compose.app.yaml", "compose.observe.yaml", "Makefile",
)

def replace_mutation(tree: Path, relative: str, old: str, new: str, last: bool = False) -> None:
    path = tree / relative
    content = path.read_text(encoding="utf-8")
    if old not in content:
        raise ValueError(f"self-test mutation anchor missing in {relative}")
    if last:
        before, _, after = content.rpartition(old)
        content = before + new + after
    else:
        content = content.replace(old, new, 1)
    path.write_text(content, encoding="utf-8")


def remove_compose_service_merge(tree: Path, service: str) -> None:
    path = tree / "compose.app.yaml"
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    start = next(index for index, line in enumerate(lines) if line == f"  {service}:\n")
    end = next((index for index in range(start + 1, len(lines))
                if re.fullmatch(r"  [A-Za-z0-9_-]+:\n", lines[index])), len(lines))
    block = "".join(lines[start:end])
    anchor = "      <<: [*jev-advisory, *core-authority-env]\n"
    if anchor not in block:
        raise ValueError(f"authority merge mutation anchor missing in Compose service {service}")
    path.write_text("".join(lines[:start]) + block.replace(anchor, "      <<: *jev-advisory\n", 1) + "".join(lines[end:]), encoding="utf-8")


def self_test(root: Path) -> int:
    """Bounded negative cases: each injected defect must fail the checker."""
    import subprocess

    workload = "deployments/k8s/base/workloads.yaml"
    config = "deployments/k8s/base/config.yaml"
    compose = "compose.app.yaml"
    compose_ca_mount = "      - *core-authority-ca-mount\n"
    token_binding = ("            - name: WORKER_CORE_TOKEN\n"
                     "              valueFrom: {secretKeyRef: {name: contract-audit-service-tokens, key: WORKER_CORE_TOKEN}}\n")
    tool_binding = ("            - name: WORKER_TOOL_TOKEN\n"
                    "              valueFrom: {secretKeyRef: {name: contract-audit-service-tokens, key: WORKER_TOOL_TOKEN}}\n")
    tool_authority_binding = ("            - name: TOOL_CORE_AUTHORITY_TOKEN\n"
                             "              valueFrom: {secretKeyRef: {name: contract-audit-service-tokens, key: TOOL_CORE_AUTHORITY_TOKEN}}\n")
    compose_token = "${WORKER_CORE_TOKEN:?Add a distinct WORKER_CORE_TOKEN to the private distributed profile}"
    compose_url = "${CORE_SERVICE_URL:?Add CORE_SERVICE_URL to the private distributed profile}"
    mutations = (
        ("missing required env", lambda tree: replace_mutation(tree, workload, tool_binding, ""), "role worker is missing required env WORKER_TOOL_TOKEN"),
        ("unapproved production Jev", lambda tree: replace_mutation(tree, config, 'TYPESAFE_JEV_MODE: "off"', "TYPESAFE_JEV_MODE: advisory"), "production base must disable Jev"),
        ("missing probe", lambda tree: replace_mutation(tree, workload, "          livenessProbe:\n", "          livenessProbeDisabled:\n"), "declares no livenessProbe"),
        ("HPA back in the render", lambda tree: replace_mutation(tree, "deployments/k8s/base/kustomization.yaml", "  - poddisruptionbudgets.yaml", "  - poddisruptionbudgets.yaml\n  - ../optional/scaling-hpa.yaml"), "must contain zero HorizontalPodAutoscaler"),
        ("migration kustomization removed", lambda tree: tree.joinpath("deployments/k8s/migrate/kustomization.yaml").unlink(), "missing kustomization"),
        ("core token missing", lambda tree: replace_mutation(tree, workload, token_binding, ""), "core must use the required WORKER_CORE_TOKEN"),
        ("worker token missing", lambda tree: replace_mutation(tree, workload, token_binding, "", last=True), "worker must use the required WORKER_CORE_TOKEN"),
        ("worker token references another secret", lambda tree: replace_mutation(tree, workload, token_binding, token_binding.replace("key: WORKER_CORE_TOKEN", "key: WORKER_TOOL_TOKEN"), last=True), "worker must use the required WORKER_CORE_TOKEN"),
        ("core token optional", lambda tree: replace_mutation(tree, workload, token_binding, token_binding.replace("key: WORKER_CORE_TOKEN}", "key: WORKER_CORE_TOKEN, optional: true}")), "core must use the required WORKER_CORE_TOKEN"),
        ("worker reuses core token", lambda tree: replace_mutation(tree, workload, tool_binding, tool_binding.replace("key: WORKER_TOOL_TOKEN", "key: WORKER_CORE_TOKEN")), "WORKER_CORE_TOKEN must be distinct from WORKER_TOOL_TOKEN"),
        ("core authority token binding missing", lambda tree: replace_mutation(tree, workload, tool_authority_binding, ""), "core must use the required TOOL_CORE_AUTHORITY_TOKEN service secret"),
        ("tool authority token binds worker credential", lambda tree: replace_mutation(tree, workload, tool_authority_binding, tool_authority_binding.replace("key: TOOL_CORE_AUTHORITY_TOKEN", "key: WORKER_CORE_TOKEN"), last=True), "tool must use the required TOOL_CORE_AUTHORITY_TOKEN service secret"),
        ("worker receives tool-only authority credential", lambda tree: replace_mutation(tree, workload, tool_binding, tool_binding + tool_authority_binding), "audit-worker: worker must not receive TOOL_CORE_AUTHORITY_TOKEN"),
        ("worker endpoint absent", lambda tree: replace_mutation(tree, config, "  CORE_SERVICE_URL: http://core-api:8081\n", ""), "runtime CORE_SERVICE_URL must be an HTTP(S) service URL"),
        ("worker endpoint blank", lambda tree: replace_mutation(tree, config, "  CORE_SERVICE_URL: http://core-api:8081\n", '  CORE_SERVICE_URL: ""\n'), "runtime CORE_SERVICE_URL must be an HTTP(S) service URL"),
        ("compose core token optional", lambda tree: replace_mutation(tree, compose, compose_token, "${WORKER_CORE_TOKEN:-}"), "core must require the shared WORKER_CORE_TOKEN"),
        ("compose worker token mismatch", lambda tree: replace_mutation(tree, compose, compose_token, "${WORKER_TOOL_TOKEN:?missing}", last=True), "worker must require the shared WORKER_CORE_TOKEN"),
        ("compose worker endpoint optional", lambda tree: replace_mutation(tree, compose, compose_url, "${CORE_SERVICE_URL:-}"), "worker must require the CORE_SERVICE_URL"),
        ("compose core authority token missing", lambda tree: replace_mutation(tree, compose, "      TOOL_CORE_AUTHORITY_TOKEN: ${TOOL_CORE_AUTHORITY_TOKEN:?Run make app-bootstrap}\n", ""), "compose core-api must require TOOL_CORE_AUTHORITY_TOKEN from the private profile"),
        ("compose tool binds worker credential", lambda tree: replace_mutation(tree, compose, "      TOOL_CORE_AUTHORITY_TOKEN: ${TOOL_CORE_AUTHORITY_TOKEN:?Run make app-bootstrap}\n", "      TOOL_CORE_AUTHORITY_TOKEN: ${WORKER_CORE_TOKEN:?Run make app-bootstrap}\n", last=True), "compose tool-gateway must require TOOL_CORE_AUTHORITY_TOKEN from the private profile"),
        ("compose worker receives tool-only authority credential", lambda tree: replace_mutation(tree, compose, "      WORKER_CORE_TOKEN: ${WORKER_CORE_TOKEN:?Add a distinct WORKER_CORE_TOKEN to the private distributed profile}\n", "      WORKER_CORE_TOKEN: ${WORKER_CORE_TOKEN:?Add a distinct WORKER_CORE_TOKEN to the private distributed profile}\n      TOOL_CORE_AUTHORITY_TOKEN: ${TOOL_CORE_AUTHORITY_TOKEN:?Run make app-bootstrap}\n", last=True), "compose audit-worker must not receive the Core-to-Tool authority credential"),
        ("production callback insecure flag", lambda tree: replace_mutation(tree, config, 'CORE_AUTHORITY_ALLOW_INSECURE_DEV: "false"', 'CORE_AUTHORITY_ALLOW_INSECURE_DEV: "true"'), "production Core authority clients must set CORE_AUTHORITY_ALLOW_INSECURE_DEV=false"),
        ("production callback HTTP", lambda tree: replace_mutation(tree, config, "CORE_AUTHORITY_URL: https://REPLACE-ME-CORE-AUTH-ENDPOINT", "CORE_AUTHORITY_URL: http://core-api:8081"), "production template must retain the explicit nondeployable HTTPS Core authority placeholder"),
        ("k3d callback outside local boundary", lambda tree: replace_mutation(tree, "deployments/k3d/config-patch.yaml", "CORE_AUTHORITY_URL: http://core-api:8081", "CORE_AUTHORITY_URL: http://core-api.evil:8081"), "k3d development profile must use the exact bounded Core authority HTTP exception"),
        ("compose callback URL omitted", lambda tree: replace_mutation(tree, compose, "  CORE_AUTHORITY_URL: ${CORE_AUTHORITY_URL:?Run make app-bootstrap}\n", ""), "compose document-service must explicitly pass CORE_AUTHORITY_URL from the private profile"),
        ("worker authority env omitted", lambda tree: remove_compose_service_merge(tree, "audit-worker"), "compose audit-worker must explicitly pass CORE_AUTHORITY_URL from the private profile"),
        ("model gateway authority credential missing", lambda tree: replace_mutation(tree, compose, "      CORE_MODEL_AUTHORITY_TOKEN: ${CORE_MODEL_AUTHORITY_TOKEN:?Run make app-bootstrap}\n      OTEL_EXPORTER_OTLP_ENDPOINT", "      OTEL_EXPORTER_OTLP_ENDPOINT"), "compose model-gateway must require CORE_MODEL_AUTHORITY_TOKEN from the private profile"),
        ("fixture gateway misses model authority credential", lambda tree: replace_mutation(tree, compose, "      MODEL_GATEWAY_MODE: fixture\n      SERVICE_ROLE: model\n      MODEL_DATABASE_URL: ${MODEL_DATABASE_URL:?Run make app-bootstrap}\n      CORE_MODEL_AUTHORITY_TOKEN: ${CORE_MODEL_AUTHORITY_TOKEN:?Run make app-bootstrap}\n", "      MODEL_GATEWAY_MODE: fixture\n      SERVICE_ROLE: model\n      MODEL_DATABASE_URL: ${MODEL_DATABASE_URL:?Run make app-bootstrap}\n"), "compose model-gateway must require CORE_MODEL_AUTHORITY_TOKEN from the private profile"),
        ("migration lacks auth owner provision", lambda tree: replace_mutation(tree, "deployments/k8s/migrate/migration-job.yaml", "        - name: auth-owner-provision\n", "        - name: wrong-provision\n"), "migration Job must run offline migrator, CLM owner, then AUTH_FENCE owner provisioning"),
        ("migration admin DSN leaks into long-lived container", lambda tree: replace_mutation(tree, "deployments/k8s/migrate/migration-job.yaml", "            - name: TEST_DATABASE_URL\n              valueFrom: {secretKeyRef: {name: contract-audit-db-urls, key: TEST_DATABASE_URL, optional: true}}\n            - name: PROD_DATABASE_URL", "            - name: AUTH_ADMIN_DATABASE_URL\n              valueFrom: {secretKeyRef: {name: contract-audit-db-urls, key: AUTH_ADMIN_DATABASE_URL}}\n            - name: TEST_DATABASE_URL\n              valueFrom: {secretKeyRef: {name: contract-audit-db-urls, key: TEST_DATABASE_URL, optional: true}}\n            - name: PROD_DATABASE_URL", last=True), "ordinary migration container must never receive AUTH_ADMIN_DATABASE_URL"),
        ("compose duplicate merge key", lambda tree: replace_mutation(tree, compose, "      <<: [*jev-advisory, *core-authority-env]\n", "      <<: *core-authority-env\n      <<: [*jev-advisory, *core-authority-env]\n", last=True), "duplicate YAML key '<<'"),
        ("tool authority env omitted", lambda tree: replace_mutation(tree, compose, "      <<: *core-authority-env\n", "", last=True), "compose tool-gateway must explicitly pass CORE_AUTHORITY_URL from the private profile"),
        ("transparent gateway receives authority config", lambda tree: replace_mutation(tree, compose, "      CORE_URLS: ${CORE_URLS:-http://core-api:8081}\n", "      CORE_URLS: ${CORE_URLS:-http://core-api:8081}\n      CORE_AUTHORITY_URL: ${CORE_AUTHORITY_URL:?Run make app-bootstrap}\n"), "compose gateway must not receive unused Core authority client configuration"),
        ("compose callback CA mount omitted", lambda tree: replace_mutation(tree, compose, compose_ca_mount, ""), "compose document-service must mount the optional Core authority CA read-only at the configured path"),
    )
    problems = 0
    with tempfile.TemporaryDirectory() as directory:
        tree = Path(directory) / "baseline"
        for entry in SUBTREE:
            source = root / entry
            target = tree / entry
            target.parent.mkdir(parents=True, exist_ok=True)
            if source.is_dir():
                shutil.copytree(source, target)
            else:
                shutil.copy2(source, target)
        baseline = subprocess.run(
            [sys.executable, "-I", str(root / "deployments/scripts/manifest-conformance.py"), "--root", str(tree)],
            capture_output=True, text=True, cwd=tree,
        )
        if baseline.returncode != 0:
            print(f"FAIL: the unmutated baseline does not pass: {baseline.stderr.strip()[:400]}", file=sys.stderr)
            return 1
    for label, mutate, expected in mutations:
        with tempfile.TemporaryDirectory() as directory:
            tree = Path(directory) / "repo"
            for entry in SUBTREE:
                source = root / entry
                target = tree / entry
                target.parent.mkdir(parents=True, exist_ok=True)
                if source.is_dir():
                    shutil.copytree(source, target)
                else:
                    shutil.copy2(source, target)
            mutate(tree)
            result = subprocess.run(
                [sys.executable, "-I", str(root / "deployments/scripts/manifest-conformance.py"), "--root", str(tree)],
                capture_output=True, text=True, cwd=tree,
            )
            if result.returncode == 0 or expected not in result.stderr or "Traceback" in result.stderr:
                print(f"FAIL: self-test case {label!r} was not detected by a checker verdict", file=sys.stderr)
                problems += 1
            else:
                print(f"self-test: {label} -> refused")
    if problems:
        print(f"FAIL: {problems} self-test case(s) went undetected", file=sys.stderr)
        return 1
    print("PASS: every injected defect was refused")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=str(Path(__file__).resolve().parents[2]), help="repository root to check")
    parser.add_argument("--mode", choices=("template", "preflight"), default="template",
                        help="template checks keep the production placeholder; preflight requires a configured HTTPS callback origin")
    parser.add_argument("--self-test", action="store_true", help="prove the checks reject injected defects")
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()
    if args.self_test:
        return self_test(root)
    return run_checks(root, args.mode)


if __name__ == "__main__":
    raise SystemExit(main())

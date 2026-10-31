#!/usr/bin/env python3
"""Render and operate Kubernetes releases using one immutable candidate lock.

The four image references must be registry manifest digests obtained after a
successful push, not Docker's local image/config IDs. The document service and
migration executable are both in the backend image. Retain the lock and any
packaging report as release evidence; this tool validates their structure but
does not contact the registry or prove the supplied digests came from a build.

Typical sequence (the namespace's Secrets and CNPG prerequisites are external):

  python3 deployments/scripts/release-k8s.py lock --version "$VERSION" \
    --package-report "$PACKAGING_REPORT" --backend "$BACKEND_DIGEST" \
    --client "$CLIENT_DIGEST" --admin "$ADMIN_DIGEST" --portal "$PORTAL_DIGEST" \
    --output "$RELEASE_LOCK"
  python3 deployments/scripts/release-k8s.py bootstrap "$RELEASE_LOCK"
  deployments/scripts/migrate-k8s.sh "$RELEASE_LOCK"
  python3 deployments/scripts/release-k8s.py deploy "$RELEASE_LOCK"

Alternatively use --source-identity with the source-identity SHA256 produced by
CI when images were built through another authorized packaging path. The report
path is the JSON written by package-app-images.py; all four successful image
records are required. A lock binds the supplied source identity and pushed
references, but does not independently establish their provenance.

Rollback is permitted only when the requested historical revision has its own
completed migration Job and its lock equals the latest migration lock, or that
latest lock explicitly names the historical lock in its rollback compatibility
window. When creating a release lock, --rollback-compatible-lock can be repeated
for previously retained lock files; --compatibility-evidence must identify the
release-specific schema and mixed-service compatibility evidence covering that
window. Missing metadata denies cross-release rollback. This declared window is
not an independent verification of the external evidence.

Inspect the proposed action, then set
CONTRACT_AUDIT_ROLLBACK_CONFIRM=application-deployment-only and run:

  python3 deployments/scripts/release-k8s.py inspect-rollback \
    <deployment> <revision> <old-lock.json> <current-migration-lock.json>
  deployments/scripts/rollback-k8s.sh \
    <deployment> <revision> <old-lock.json> <current-migration-lock.json>

Only the two specified lock identities may be running across the application.
This application's migrator has no schema version history; without an explicit
window, compatibility with a later migration is unknown and rollback is denied.
The command never rolls back the database or runs a migration during rollback.

KUBE_CONTEXT and KUBE_NAMESPACE select the cluster and namespace. A cooperative
ConfigMap serializes bootstrap, migration, deploy and rollback. If a process
terminates without releasing it, first establish that no release operation is
running and inspect the recorded owner; clear its owner with a resource-version
checked Kubernetes update before retrying. Do not delete migration Jobs: their
completion, order and lock annotations are required for deploy and rollback.
Directly applying the raw base/migration templates bypasses these guarantees.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid

try:
    import yaml
except ImportError:
    print("FAIL: PyYAML is required (also used by manifest-conformance.py)", file=sys.stderr)
    raise SystemExit(1)

ROOT = Path(__file__).resolve().parents[2]
K8S = ROOT / "deployments" / "k8s"
LOCK_FORMAT = "contract-audit-release/v1"
LOCK_ANNOTATION = "contract-audit.io/release-lock"
VERSION_ANNOTATION = "contract-audit.io/release-version"
SOURCE_ANNOTATION = "contract-audit.io/source-identity"
MUTEX_NAME = "contract-audit-release-operation"
IMAGE_COMPONENTS = ("backend", "client", "admin", "portal")
DEPLOYMENTS = {
    "gateway": ("backend", "gateway"),
    "core-api": ("backend", "core-api"),
    "document-service": ("backend", "document-service"),
    "audit-execution": ("backend", "audit-execution"),
    "audit-worker": ("backend", "audit-worker"),
    "model-gateway": ("backend", "model-gateway"),
    "tool-gateway": ("backend", "tool-gateway"),
    "client": ("client", "nginx"),
    "admin": ("admin", "nginx"),
    "portal": ("portal", "nginx"),
}
HEX256 = re.compile(r"^[0-9a-f]{64}$")
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
REGISTRY_IMAGE = re.compile(
    r"^([a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?)/"
    r"([a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*"
    r"(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*)@sha256:([0-9a-f]{64})$"
)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def canonical(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


def read_json(path: str | Path) -> dict:
    def unique_pairs(pairs: list[tuple[str, object]]) -> dict:
        result: dict = {}
        for key, value in pairs:
            require(key not in result, f"duplicate JSON key: {key}")
            result[key] = value
        return result

    with Path(path).expanduser().open(encoding="utf-8") as stream:
        data = json.load(stream, object_pairs_hook=unique_pairs)
    require(isinstance(data, dict), f"{path}: expected a JSON object")
    return data


def exact_keys(value: object, keys: set[str], name: str) -> dict:
    require(isinstance(value, dict) and set(value) == keys, f"{name} must contain exactly: {', '.join(sorted(keys))}")
    return value


def validate_image(value: object) -> str:
    require(isinstance(value, str), "image reference must be a string")
    match = REGISTRY_IMAGE.fullmatch(value)
    require(match is not None, "image must be an untagged registry/repository@sha256:<64 lowercase hex> reference")
    registry = match.group(1)
    host = registry.split(":", 1)[0]
    require(("." in registry or ":" in registry) and host not in {"localhost", "127.0.0.1", "0.0.0.0"},
            "image must name a non-local registry, not a development tag")
    return value


def validate_lock(lock: dict) -> dict:
    exact_keys(lock, {"format", "version", "source_identity_sha256", "images", "packaging_report_sha256",
                      "rollback_compatibility"}, "release lock")
    require(lock["format"] == LOCK_FORMAT, "unsupported release lock format")
    require(isinstance(lock["version"], str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", lock["version"]) is not None,
            "release version must use 1-128 letters, digits, '.', '_' or '-'")
    require(isinstance(lock["source_identity_sha256"], str) and HEX256.fullmatch(lock["source_identity_sha256"]) is not None,
            "source_identity_sha256 must be 64 lowercase hex characters")
    report_sha = lock["packaging_report_sha256"]
    require(report_sha is None or (isinstance(report_sha, str) and HEX256.fullmatch(report_sha) is not None),
            "packaging_report_sha256 must be null or 64 lowercase hex characters")
    compatibility = exact_keys(lock["rollback_compatibility"], {"targets", "evidence"}, "rollback_compatibility")
    targets = compatibility["targets"]
    require(isinstance(targets, list) and all(isinstance(value, str) and DIGEST.fullmatch(value) for value in targets)
            and targets == sorted(set(targets)), "rollback compatibility targets must be unique, sorted lock digests")
    evidence = compatibility["evidence"]
    require((not targets and evidence is None) or
            (bool(targets) and isinstance(evidence, str) and bool(evidence.strip())),
            "rollback compatibility targets require a nonempty evidence reference")
    images = exact_keys(lock["images"], set(IMAGE_COMPONENTS), "images")
    for component in IMAGE_COMPONENTS:
        validate_image(images[component])
    return lock


def lock_id(lock: dict) -> str:
    return "sha256:" + hashlib.sha256(canonical(lock)).hexdigest()


def package_source(path: str) -> tuple[str, str]:
    report_path = Path(path).expanduser()
    report = read_json(report_path)
    require(report.get("schema_version") == "ev02-app-images/v1" and report.get("status") == "PASS",
            "packaging report must be a successful package-app-images.py report")
    source = report.get("source_identity_sha256")
    require(isinstance(source, str) and HEX256.fullmatch(source) is not None, "packaging report has no source identity")
    expected = report.get("component_identity_sha256")
    records = report.get("images")
    require(isinstance(expected, dict) and isinstance(records, list), "packaging report lacks verified image records")
    found: set[str] = set()
    for record in records:
        require(isinstance(record, dict), "invalid packaging image record")
        match = re.fullmatch(r"contract-audit-(backend|client|admin|portal):[A-Za-z0-9_.-]+", str(record.get("tag", "")))
        require(match is not None, "packaging report contains an unknown image tag")
        component = match.group(1)
        require(component not in found, f"duplicate packaging record for {component}")
        require(isinstance(record.get("image_id"), str) and DIGEST.fullmatch(record["image_id"]) is not None,
                f"packaging report has no local image ID for {component}")
        require(isinstance(expected.get(component), str) and HEX256.fullmatch(expected[component]) is not None
                and record.get("component_identity_sha256") == expected[component],
                f"packaging inputs do not match for {component}")
        found.add(component)
    require(found == set(IMAGE_COMPONENTS), "packaging report must verify backend and all three web images")
    return source, hashlib.sha256(report_path.read_bytes()).hexdigest()


def create_lock(args: argparse.Namespace) -> None:
    if args.package_report:
        source, report_sha = package_source(args.package_report)
    else:
        source, report_sha = args.source_identity, None
    lock = validate_lock({
        "format": LOCK_FORMAT,
        "version": args.version,
        "source_identity_sha256": source,
        "images": {component: getattr(args, component) for component in IMAGE_COMPONENTS},
        "packaging_report_sha256": report_sha,
        "rollback_compatibility": {
            "targets": sorted({lock_id(validate_lock(read_json(path))) for path in args.rollback_compatible_lock}),
            "evidence": args.compatibility_evidence,
        },
    })
    output = Path(args.output).expanduser()
    with output.open("x", encoding="utf-8") as stream:
        json.dump(lock, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    print(f"Created {output}; release lock {lock_id(lock)}")


def kube_args() -> list[str]:
    context = os.environ.get("KUBE_CONTEXT", "")
    return ["kubectl", *(["--context", context] if context else []), "--namespace", namespace()]


def namespace() -> str:
    value = os.environ.get("KUBE_NAMESPACE", "contract-audit")
    require(re.fullmatch(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?", value) is not None and len(value) <= 63,
            "KUBE_NAMESPACE must be a Kubernetes namespace name")
    return value


def run(command: list[str], *, data: str | None = None) -> str:
    try:
        result = subprocess.run(command, input=data, text=True, capture_output=True, check=False)
    except OSError as error:
        raise RuntimeError(f"could not execute {command[0]}: {error}") from error
    if result.returncode:
        raise RuntimeError(f"{' '.join(command)} failed ({result.returncode}): {result.stderr.strip()}")
    return result.stdout


def kubectl(*args: str, data: str | None = None) -> str:
    return run([*kube_args(), *args], data=data)


def kube_json(*args: str) -> dict:
    text = kubectl(*args, "-o", "json")
    result = json.loads(text)
    require(isinstance(result, dict), "Kubernetes returned a non-object response")
    return result


def optional_configmap() -> dict | None:
    text = kubectl("get", "configmap", MUTEX_NAME, "--ignore-not-found", "-o", "json")
    if not text.strip():
        return None
    configmap = json.loads(text)
    require(isinstance(configmap, dict), "release operation ConfigMap is invalid")
    return configmap


def replace_configmap(configmap: dict) -> None:
    kubectl("replace", "-f", "-", data=json.dumps(configmap))


@contextmanager
def release_operation():
    owner = str(uuid.uuid4())
    configmap = optional_configmap()
    if configmap is None:
        configmap = {"apiVersion": "v1", "kind": "ConfigMap",
                     "metadata": {"name": MUTEX_NAME, "namespace": namespace()}, "data": {"owner": owner}}
        created = json.loads(kubectl("create", "-f", "-", "-o", "json", data=json.dumps(configmap)))
        uid = created["metadata"]["uid"]
    else:
        data = configmap.get("data")
        require(isinstance(data, dict) and set(data) == {"owner"} and isinstance(data["owner"], str),
                "release operation ConfigMap has an unexpected format")
        require(not data["owner"], f"release operation is locked by {data['owner']}; inspect it before recovery")
        uid = configmap["metadata"]["uid"]
        data["owner"] = owner
        replace_configmap(configmap)  # resourceVersion provides compare-and-swap
    try:
        yield
    finally:
        current = optional_configmap()
        require(current is not None and current.get("metadata", {}).get("uid") == uid
                and current.get("data", {}).get("owner") == owner,
                f"release operation lock changed; inspect owner {owner}")
        current["data"]["owner"] = ""
        replace_configmap(current)


def annotations(lock: dict) -> dict[str, str]:
    return {LOCK_ANNOTATION: lock_id(lock), VERSION_ANNOTATION: lock["version"],
            SOURCE_ANNOTATION: lock["source_identity_sha256"]}


def annotate(obj: dict, lock: dict) -> None:
    obj.setdefault("metadata", {}).setdefault("annotations", {}).update(annotations(lock))


def single_container(template: dict, expected_name: str) -> dict:
    spec = template.get("spec", {})
    require(not spec.get("initContainers"), "unexpected init containers require explicit release image handling")
    containers = spec.get("containers")
    require(isinstance(containers, list) and len(containers) == 1 and containers[0].get("name") == expected_name,
            f"expected exactly one container named {expected_name}")
    return containers[0]


def migration_container(template: dict) -> dict:
    container = single_container(template, "migrate")
    require(not container.get("command") and container.get("args") == ["/app/migrate"],
            "migration Job must run the backend image's /app/migrate entrypoint")
    role_flags = [item for item in container.get("env", []) if item.get("name") == "SERVICE_DATABASE_ROLES"]
    require(len(role_flags) == 1 and role_flags[0].get("value") == "true",
            "migration Job must apply the service database roles")
    return container


def job_name(lock: dict) -> str:
    return "contract-audit-migrate-" + lock_id(lock)[7:31]


def rendered_objects(mode: str, lock: dict) -> list[dict]:
    source = K8S / ("migrate" if mode == "migration" else "base")
    rendered = run(["kubectl", "kustomize", str(source)])
    objects = [obj for obj in yaml.safe_load_all(rendered) if obj is not None]
    require(all(isinstance(obj, dict) for obj in objects), "Kustomize produced an invalid manifest")
    seen: set[str] = set()
    for obj in objects:
        meta = obj.setdefault("metadata", {})
        if obj.get("kind") == "Namespace":
            meta["name"] = namespace()
        elif "namespace" in meta:
            meta["namespace"] = namespace()
        if mode == "migration" and obj.get("kind") == "Job":
            require(not seen, "migration overlay must contain exactly one Job")
            seen.add("migration")
            meta["name"] = job_name(lock)
            meta.setdefault("labels", {})["contract-audit.io/managed-release"] = "true"
            annotate(obj, lock)
            template = obj["spec"]["template"]
            annotate(template, lock)
            container = migration_container(template)
            require(container.get("image") == "contract-audit-backend:release", "unexpected migration image template")
            container["image"] = lock["images"]["backend"]
        if mode != "migration" and obj.get("kind") == "Deployment":
            name = meta.get("name")
            require(name in DEPLOYMENTS and name not in seen, f"unexpected or duplicate Deployment {name}")
            seen.add(name)
            component, container_name = DEPLOYMENTS[name]
            annotate(obj, lock)
            template = obj["spec"]["template"]
            annotate(template, lock)
            container = single_container(template, container_name)
            require(container.get("image") == f"contract-audit-{component}:release", f"unexpected image template for {name}")
            container["image"] = lock["images"][component]
    if mode == "migration":
        require(seen == {"migration"} and len(objects) == 1, "migration overlay must render only one Job")
        return objects
    require(seen == set(DEPLOYMENTS), "base render must contain exactly the ten application Deployments")
    if mode == "bootstrap":
        return [obj for obj in objects if obj.get("kind") != "Deployment"]
    return [obj for obj in objects if obj.get("kind") == "Deployment"]


def apply_objects(objects: list[dict]) -> None:
    require(bool(objects), "refusing to apply an empty manifest")
    payload = json.dumps({"apiVersion": "v1", "kind": "List", "items": objects})
    print(kubectl("apply", "-f", "-", data=payload), end="")


def bootstrap(lock: dict) -> None:
    objects = rendered_objects("bootstrap", lock)
    namespaces = [obj for obj in objects if obj.get("kind") == "Namespace"]
    require(len(namespaces) == 1, "base must render exactly one namespace")
    apply_objects(namespaces)  # the operation lock itself lives in this namespace
    with release_operation():
        apply_objects([obj for obj in objects if obj.get("kind") != "Namespace"])


def conditions(job: dict) -> set[str]:
    return {item.get("type") for item in job.get("status", {}).get("conditions", []) if item.get("status") == "True"}


def migration_jobs() -> list[dict]:
    listing = kube_json("get", "jobs", "-l", "app.kubernetes.io/name=migration")
    jobs = listing.get("items")
    require(isinstance(jobs, list), "Kubernetes did not return migration Jobs")
    return jobs


def migration_record(job: dict) -> dict:
    require("Complete" in conditions(job) and "Failed" not in conditions(job), "migration Job did not complete")
    meta = job["metadata"]
    job_annotations = meta.get("annotations", {})
    lock = job_annotations.get(LOCK_ANNOTATION)
    require(isinstance(lock, str) and DIGEST.fullmatch(lock) is not None, "migration has no immutable release lock")
    require(meta.get("name") == "contract-audit-migrate-" + lock[7:31], "migration Job name does not match its lock")
    template = job["spec"]["template"]
    pod_annotations = template.get("metadata", {}).get("annotations", {})
    require(all(job_annotations.get(key) and pod_annotations.get(key) == job_annotations[key]
                for key in (LOCK_ANNOTATION, VERSION_ANNOTATION, SOURCE_ANNOTATION)),
            "migration Job and pod release identities differ")
    image = validate_image(migration_container(template).get("image"))
    return {"job": meta["name"], "lock": lock, "backend_image": image,
            "version": job_annotations[VERSION_ANNOTATION], "source_identity_sha256": job_annotations[SOURCE_ANNOTATION]}


def active_migration(jobs: list[dict] | None = None) -> dict:
    if jobs is None:
        jobs = migration_jobs()
    require(bool(jobs), "no migration Job is recorded in this namespace")
    for job in jobs:
        require(conditions(job) & {"Complete", "Failed"},
                f"migration Job {job.get('metadata', {}).get('name')} has not reached a terminal state")
        require(job.get("metadata", {}).get("creationTimestamp"), "migration Job has no creation timestamp")
    jobs.sort(key=lambda item: item["metadata"]["creationTimestamp"])
    if len(jobs) > 1:
        require(jobs[-1]["metadata"]["creationTimestamp"] != jobs[-2]["metadata"]["creationTimestamp"],
                "migration Job order is ambiguous; inspect the Jobs before proceeding")
    return migration_record(jobs[-1])


def migrate(lock: dict) -> None:
    objects = rendered_objects("migration", lock)
    with release_operation():
        for job in migration_jobs():
            require(conditions(job) & {"Complete", "Failed"},
                    f"migration Job {job.get('metadata', {}).get('name')} is still active")
        kubectl("wait", "--for=condition=Ready", "cluster/contract-audit-db", "--timeout=20m")
        payload = json.dumps(objects[0])
        name = job_name(lock)
        print(kubectl("create", "-f", "-", data=payload), end="")
        try:
            print(kubectl("wait", "--for=condition=Complete", f"job/{name}", "--timeout=20m"), end="")
        except RuntimeError:
            try:
                print(kubectl("logs", f"job/{name}"), file=sys.stderr, end="")
            except RuntimeError as log_error:
                print(f"Could not fetch migration logs: {log_error}", file=sys.stderr)
            raise
        active = active_migration()
        require(active["lock"] == lock_id(lock) and active["backend_image"] == lock["images"]["backend"]
                and active["version"] == lock["version"]
                and active["source_identity_sha256"] == lock["source_identity_sha256"],
                "completed migration does not match the candidate lock")
        print(kubectl("logs", f"job/{name}"), end="")


def deploy(lock: dict) -> None:
    objects = rendered_objects("application", lock)
    with release_operation():
        active = active_migration()
        require(active["lock"] == lock_id(lock) and active["backend_image"] == lock["images"]["backend"]
                and active["version"] == lock["version"]
                and active["source_identity_sha256"] == lock["source_identity_sha256"],
                "latest completed migration is not this candidate and backend digest")
        apply_objects(objects)
        for name in DEPLOYMENTS:
            print(kubectl("rollout", "status", f"deployment/{name}", "--timeout=10m"), end="")
            state = deployment_state(kube_json("get", "deployment", name))
            require(state["lock"] == lock_id(lock) and state["image"] == lock["images"][DEPLOYMENTS[name][0]]
                    and state["version"] == lock["version"]
                    and state["source_identity_sha256"] == lock["source_identity_sha256"],
                    f"Deployment {name} does not match the rolled out candidate")


def deployment_state(obj: dict, *, ready: bool = True) -> dict:
    meta = obj["metadata"]
    name = meta["name"]
    require(name in DEPLOYMENTS, f"unexpected Deployment {name}")
    template = obj["spec"]["template"]
    image = validate_image(single_container(template, DEPLOYMENTS[name][1]).get("image"))
    release_lock = template.get("metadata", {}).get("annotations", {}).get(LOCK_ANNOTATION)
    require(isinstance(release_lock, str) and DIGEST.fullmatch(release_lock) is not None,
            f"Deployment {name} does not have an immutable release lock")
    if ready:
        desired = obj["spec"].get("replicas", 1)
        status = obj.get("status", {})
        require(status.get("observedGeneration", 0) >= meta["generation"]
                and status.get("replicas", 0) == desired
                and status.get("updatedReplicas", 0) == desired
                and status.get("availableReplicas", 0) == desired,
                f"Deployment {name} has not finished rolling out")
    pod_annotations = template.get("metadata", {}).get("annotations", {})
    return {"uid": meta["uid"], "generation": meta["generation"], "lock": release_lock, "image": image,
            "version": pod_annotations.get(VERSION_ANNOTATION),
            "source_identity_sha256": pod_annotations.get(SOURCE_ANNOTATION)}


def rollback_state(deployment: str, revision: int, target: dict, current_lock: dict) -> dict:
    require(deployment in DEPLOYMENTS, "unknown application Deployment")
    require(revision > 0, "rollback revision must be a positive integer")
    jobs = migration_jobs()
    migration = active_migration(jobs)
    current_id = lock_id(current_lock)
    target_id = lock_id(target)
    require(migration["lock"] == current_id and migration["backend_image"] == current_lock["images"]["backend"]
            and migration["version"] == current_lock["version"]
            and migration["source_identity_sha256"] == current_lock["source_identity_sha256"],
            "the supplied current lock does not match the latest completed migration")
    historical_jobs = [job for job in jobs if job.get("metadata", {}).get("annotations", {}).get(LOCK_ANNOTATION) == target_id]
    require(len(historical_jobs) == 1, "the target release requires its own retained completed migration Job")
    historical = migration_record(historical_jobs[0])
    require(historical["backend_image"] == target["images"]["backend"] and historical["version"] == target["version"]
            and historical["source_identity_sha256"] == target["source_identity_sha256"],
            "the historical migration does not match the target release lock")
    compatibility = current_lock["rollback_compatibility"]
    require(target_id == current_id or target_id in compatibility["targets"],
            "rollback compatibility is unknown: the latest migration lock has no version window for this target")
    listing = kube_json("get", "deployments", *DEPLOYMENTS)
    items = listing.get("items")
    require(isinstance(items, list), "Kubernetes did not return a Deployment list")
    states: dict[str, dict] = {}
    for obj in items:
        name = obj.get("metadata", {}).get("name")
        require(name in DEPLOYMENTS and name not in states, f"unexpected or duplicate Deployment {name}")
        states[name] = deployment_state(obj, ready=name != deployment)
    require(set(states) == set(DEPLOYMENTS), "all application Deployments are required for compatibility review")
    releases = {target_id: target, current_id: current_lock}
    for name, state in states.items():
        release = releases.get(state["lock"])
        require(release is not None, f"Deployment {name} is outside the declared rollback version window")
        require(state["image"] == release["images"][DEPLOYMENTS[name][0]]
                and state["version"] == release["version"]
                and state["source_identity_sha256"] == release["source_identity_sha256"],
                f"Deployment {name} does not match its declared release lock")
    replicasets = kube_json("get", "replicasets").get("items")
    require(isinstance(replicasets, list), "Kubernetes did not return ReplicaSets")
    matches = []
    for replica in replicasets:
        meta = replica.get("metadata", {})
        owners = meta.get("ownerReferences", [])
        owned = any(owner.get("kind") == "Deployment" and owner.get("uid") == states[deployment]["uid"]
                    and owner.get("controller") is True for owner in owners)
        if not owned:
            continue
        if replica.get("spec", {}).get("replicas", 0) or replica.get("status", {}).get("replicas", 0):
            live_template = replica["spec"]["template"]
            live_annotations = live_template.get("metadata", {}).get("annotations", {})
            live_release = releases.get(live_annotations.get(LOCK_ANNOTATION))
            require(live_release is not None, "an active target ReplicaSet is outside the rollback version window")
            require(live_annotations.get(VERSION_ANNOTATION) == live_release["version"]
                    and live_annotations.get(SOURCE_ANNOTATION) == live_release["source_identity_sha256"]
                    and single_container(live_template, DEPLOYMENTS[deployment][1]).get("image")
                    == live_release["images"][DEPLOYMENTS[deployment][0]],
                    "an active target ReplicaSet does not match its release lock")
        if meta.get("annotations", {}).get("deployment.kubernetes.io/revision") == str(revision):
            matches.append(replica)
    require(len(matches) == 1, "the requested Deployment revision is missing or ambiguous")
    template = matches[0]["spec"]["template"]
    target_annotations = template.get("metadata", {}).get("annotations", {})
    expected_image = target["images"][DEPLOYMENTS[deployment][0]]
    require(target_annotations.get(LOCK_ANNOTATION) == lock_id(target)
            and target_annotations.get(VERSION_ANNOTATION) == target["version"]
            and target_annotations.get(SOURCE_ANNOTATION) == target["source_identity_sha256"],
            "the requested revision does not belong to the supplied release lock")
    require(single_container(template, DEPLOYMENTS[deployment][1]).get("image") == expected_image,
            "the requested revision does not contain the target candidate image")
    return {"deployment": deployment, "revision": revision, "target_lock": target_id,
            "target_image": expected_image, "target_migration": historical, "current_migration": migration,
            "compatibility_evidence": compatibility["evidence"] if target_id != current_id else None,
            "deployments": states}


def rollback(deployment: str, revision: int, target: dict, current_lock: dict) -> None:
    require(os.environ.get("CONTRACT_AUDIT_ROLLBACK_CONFIRM") == "application-deployment-only",
            "set CONTRACT_AUDIT_ROLLBACK_CONFIRM=application-deployment-only to roll back a Deployment")
    with release_operation():
        state = rollback_state(deployment, revision, target, current_lock)
        print(kubectl("rollout", "undo", f"deployment/{deployment}", f"--to-revision={revision}"), end="")
        print(kubectl("rollout", "status", f"deployment/{deployment}", "--timeout=10m"), end="")
        result = deployment_state(kube_json("get", "deployment", deployment))
        require(result["lock"] == lock_id(target) and result["image"] == state["target_image"]
                and result["version"] == target["version"]
                and result["source_identity_sha256"] == target["source_identity_sha256"],
                "rolled out Deployment does not match the requested lock and image")
        print(f"Rolled back {deployment} to revision {revision}, {lock_id(target)}")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = result.add_subparsers(dest="command", required=True)
    create = commands.add_parser("lock", help="create a new candidate lock from pushed manifest digests")
    create.add_argument("--version", required=True)
    source = create.add_mutually_exclusive_group(required=True)
    source.add_argument("--package-report", help="successful report from package-app-images.py")
    source.add_argument("--source-identity", help="64-character source identity SHA256 from the image build")
    for component in IMAGE_COMPONENTS:
        create.add_argument(f"--{component}", required=True, help=f"pushed {component} registry manifest digest reference")
    create.add_argument("--rollback-compatible-lock", action="append", default=[], metavar="PATH",
                        help="historical lock in the explicitly supported schema/mixed-service rollback window; repeatable")
    create.add_argument("--compatibility-evidence", help="release-specific evidence reference covering the rollback window")
    create.add_argument("--output", required=True, help="new release lock file; will not overwrite")
    for command in ("bootstrap", "migrate", "deploy"):
        command_parser = commands.add_parser(command)
        command_parser.add_argument("lock", help="candidate lock JSON")
    render = commands.add_parser("render", help="render candidate manifests as a JSON Kubernetes List")
    render.add_argument("kind", choices=("bootstrap", "migration", "application"))
    render.add_argument("lock")
    for command in ("inspect-rollback", "rollback"):
        rollback_parser = commands.add_parser(command)
        rollback_parser.add_argument("deployment", choices=DEPLOYMENTS)
        rollback_parser.add_argument("revision", type=int)
        rollback_parser.add_argument("lock", help="target historical release lock JSON")
        rollback_parser.add_argument("current_lock", help="latest completed migration's release lock JSON")
    return result


def main() -> int:
    args = parser().parse_args()
    try:
        if args.command == "lock":
            create_lock(args)
            return 0
        lock = validate_lock(read_json(args.lock))
        if args.command == "bootstrap":
            bootstrap(lock)
        elif args.command == "migrate":
            migrate(lock)
        elif args.command == "deploy":
            deploy(lock)
        elif args.command == "render":
            print(json.dumps({"apiVersion": "v1", "kind": "List", "items": rendered_objects(args.kind, lock)},
                             ensure_ascii=False, indent=2))
        elif args.command == "inspect-rollback":
            current_lock = validate_lock(read_json(args.current_lock))
            print(json.dumps(rollback_state(args.deployment, args.revision, lock, current_lock), ensure_ascii=False, indent=2))
        elif args.command == "rollback":
            current_lock = validate_lock(read_json(args.current_lock))
            rollback(args.deployment, args.revision, lock, current_lock)
        return 0
    except (OSError, ValueError, RuntimeError, KeyError, TypeError, json.JSONDecodeError, yaml.YAMLError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

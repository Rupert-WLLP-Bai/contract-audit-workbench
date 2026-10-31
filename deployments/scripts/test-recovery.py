#!/usr/bin/env python3
"""Isolated CNPG PITR and versioned-original recovery drill (test only).

Requires Python 3, kubectl, AWS CLI v2, CNPG with the Barman Cloud plugin, a
reachable test backup ObjectStore and PostgreSQL/WAL archives. The test
namespace, source Cluster, Backup and ObjectStore must carry environment=test.
The operator needs Kubernetes get/list/create/wait/pods/exec permissions in
that namespace. No production cluster or bucket is supported.

Prepare four *existing*, versioning-enabled S3 buckets tagged environment=test:
source originals, a new empty backup, a new empty replica in another failure
domain, and a new empty restore target. Their credentials must permit tag and
version listing and reads; backup, replica and restore also need conditional
PutObject. The AWS CLI must support s3api put-object --if-none-match. Each site
may use a separate AWS profile. No credentials belong in this JSON file:

  {"source":{"endpoint":"https://...","bucket":"...","region":"...","site":"a","profile":"..."},
   "backup":{"endpoint":"https://...","bucket":"...","region":"...","site":"a","profile":"..."},
   "replica":{"endpoint":"https://...","bucket":"...","region":"...","site":"b","profile":"..."},
   "restore":{"endpoint":"https://...","bucket":"...","region":"...","site":"b","profile":"..."}}

The replica must declare a different site and a different endpoint or region.
The operator must independently provision and verify the stated failure
domains, retention, credentials, object-store accessibility and WAL retention.
S3 versioning must have been enabled before the requested target time.

With application writes quiesced through object capture, use:

  CONTRACT_AUDIT_TEST_BACKUP_CONFIRM=backup-contract-audit-test-db \
    ./deployments/scripts/backup-test-db.sh --record /private/db-backup.json
  # Choose an RFC3339 target time later than the backup stopped. Ensure WAL is archived.
  python3 deployments/scripts/test-recovery.py backup-objects --backup-record /private/db-backup.json \
    --target-time 2026-01-01T00:00:00Z --sites /private/sites.json --output /private/objects.json
  CONTRACT_AUDIT_TEST_RESTORE_CONFIRM=restore-contract-audit-test-db-to-new-cluster \
    ./deployments/scripts/restore-test-db.sh --backup-record /private/db-backup.json \
    --target-time 2026-01-01T00:00:00Z --target-name contract-audit-test-restore-example \
    --record /private/db-restore.json
  python3 deployments/scripts/test-recovery.py restore-objects --manifest /private/objects.json \
    --restore-record /private/db-restore.json --sites /private/sites.json --output /private/reconciliation.json

The timestamps above are syntax examples, not evidence. Capture records the
entire visible S3 key set at the target using specific source version IDs,
including unreferenced keys, and verifies both copies by reading and hashing
all bytes. The recovery compares the restored database's complete document
rows to the captured rows, then checks every backup, replica and restored
object and its version, size and SHA-256. If writes were not quiesced, or a
version was deleted or versioning was not active at the target, consistency
cannot be assumed: a mismatch fails the drill. Local-only document references
are refused; this workflow covers remote originals only. No object or cluster
is deleted, and an interrupted run requires new empty destination buckets.
The resulting JSON contains sensitive object and document identifiers; keep it
outside the repository with restricted access. KUBE_CONTEXT selects a context.
"""
from __future__ import annotations

import argparse
import datetime as dt
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit, urlunsplit

HERE = Path(__file__).resolve().parent
MANIFESTS = HERE.parent / "k8s" / "test"
NAMESPACE = "contract-audit-test"
SOURCE_CLUSTER = "contract-audit-test-db"
BACKUP_STORE = "contract-audit-test-backups"
PLUGIN = "barman-cloud.cloudnative-pg.io"

spec = importlib.util.spec_from_file_location("local_restore", HERE / "restore-local.py")
if spec is None or spec.loader is None:
    raise RuntimeError("cannot load restore-local helpers")
local_restore = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local_restore)
Failure = local_restore.Failure


def command(argv: list[str], *, data: str | None = None, env: dict[str, str] | None = None,
            timeout: int = 120) -> str:
    try:
        result = subprocess.run(argv, input=data, text=True, capture_output=True, env=env, timeout=timeout,
                                stdin=subprocess.DEVNULL if data is None else None, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise Failure(f"could not run {argv[0]}: {error}") from error
    if result.returncode:
        raise Failure(f"{argv[0]} failed ({result.returncode}): {result.stderr.strip()[-1500:]}")
    return result.stdout


def decode(text: str, what: str):
    try:
        return json.loads(text)
    except (ValueError, TypeError) as error:
        raise Failure(f"invalid {what} JSON: {error}") from error


def read_json(path: str) -> dict:
    try:
        value = decode(Path(path).read_text(encoding="utf-8"), path)
    except OSError as error:
        raise Failure(f"cannot read {path}: {error}") from error
    if not isinstance(value, dict):
        raise Failure(f"{path} must contain a JSON object")
    return value


def ensure_new(path: str) -> None:
    if os.path.lexists(path):
        raise Failure(f"output {path} already exists")
    parent = Path(path).absolute().parent.resolve()
    if parent == local_restore.ROOT or local_restore.ROOT in parent.parents:
        raise Failure("recovery records must be kept outside the source repository")
    if not parent.is_dir():
        raise Failure(f"output parent {parent} does not exist")


def write_new(path: str, value: dict) -> None:
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, sort_keys=True, indent=2)
            stream.write("\n")
    except OSError as error:
        raise Failure(f"cannot create output {path}: {error}") from error


def timestamp(value: str) -> dt.datetime:
    if not isinstance(value, str) or not re.fullmatch(
        r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,6})?(?:Z|[+-]\d\d:\d\d)", value
    ):
        raise Failure("target time must be an RFC3339 timestamp with timezone and at most microseconds")
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(dt.timezone.utc)
    except ValueError as error:
        raise Failure(f"invalid timestamp {value!r}") from error


def utc(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def kube(*args: str, data: str | None = None, timeout: int = 120) -> str:
    argv = ["kubectl"]
    if os.environ.get("KUBE_CONTEXT"):
        argv += ["--context", os.environ["KUBE_CONTEXT"]]
    return command(argv + list(args), data=data, timeout=timeout)


def get(resource: str, name: str, *, namespace: bool = True) -> dict:
    args = ["get", resource, name, "-o", "json"]
    if namespace:
        args += ["-n", NAMESPACE]
    value = decode(kube(*args), f"Kubernetes {resource}/{name}")
    if not isinstance(value, dict):
        raise Failure(f"invalid Kubernetes {resource}/{name}")
    return value


def require_test(obj: dict, description: str) -> None:
    metadata = obj.get("metadata") or {}
    if (metadata.get("labels") or {}).get("environment") != "test" or not metadata.get("uid"):
        raise Failure(f"{description} must have environment=test and a Kubernetes UID")


def source_identity() -> tuple[dict, dict]:
    require_test(get("namespace", NAMESPACE, namespace=False), "namespace")
    cluster = get("cluster.postgresql.cnpg.io", SOURCE_CLUSTER)
    store = get("objectstore.barmancloud.cnpg.io", BACKUP_STORE)
    require_test(cluster, "source cluster")
    require_test(store, "backup ObjectStore")
    cluster_spec = cluster.get("spec") or {}
    matches = [p for p in cluster_spec.get("plugins", []) if p.get("name") == PLUGIN and p.get("isWALArchiver")]
    if len(matches) != 1 or (matches[0].get("parameters") or {}).get("barmanObjectName") != BACKUP_STORE:
        raise Failure("source cluster must archive WAL using the test Barman ObjectStore")
    parameters = matches[0].get("parameters") or {}
    server = parameters.get("serverName") or SOURCE_CLUSTER
    configuration = (store.get("spec") or {}).get("configuration") or {}
    destination = configuration.get("destinationPath", "")
    endpoint = configuration.get("endpointURL", "")
    test_prefix = "s3://contract-audit-test-backups/drills"
    if not isinstance(destination, str) or (destination != test_prefix and not destination.startswith(test_prefix + "/")):
        raise Failure("the database backup ObjectStore must use the test-only drills path")
    if not endpoint or (urlsplit(endpoint).hostname or "").endswith(".invalid"):
        raise Failure("configure a reachable test backup ObjectStore endpoint and credentials first")
    image = cluster_spec.get("imageName")
    if not image or not server:
        raise Failure("source cluster image and archive server identity are required")
    identity = {
        "namespace": NAMESPACE, "cluster": SOURCE_CLUSTER, "cluster_uid": cluster["metadata"]["uid"],
        "objectstore": BACKUP_STORE, "objectstore_uid": store["metadata"]["uid"],
        "objectstore_configuration": configuration, "server_name": server, "image": image,
    }
    return identity, cluster


def backup_identity(obj: dict, source: dict) -> dict:
    require_test(obj, "backup")
    metadata, spec_, status = obj.get("metadata") or {}, obj.get("spec") or {}, obj.get("status") or {}
    if ((spec_.get("cluster") or {}).get("name") != SOURCE_CLUSTER or spec_.get("method") != "plugin"
            or (spec_.get("pluginConfiguration") or {}).get("name") != PLUGIN):
        raise Failure("backup does not belong to the test cluster and Barman plugin")
    if status.get("phase") != "completed":
        raise Failure("the selected Backup has not completed")
    backup_id, started, stopped = status.get("backupId"), status.get("startedAt"), status.get("stoppedAt")
    if not isinstance(backup_id, str) or not re.fullmatch(r"\d{8}T\d{6}", backup_id):
        raise Failure("the completed Backup has no valid Barman backupId")
    if not started or not stopped or timestamp(started) > timestamp(stopped):
        raise Failure("the completed Backup has no valid start/stop times")
    plugin_uid = (status.get("pluginMetadata") or {}).get("clusterUID")
    if plugin_uid and plugin_uid != source["cluster_uid"]:
        raise Failure("backup plugin metadata identifies a different source cluster UID")
    return {"name": metadata["name"], "uid": metadata["uid"], "backup_id": backup_id,
            "started_at": utc(timestamp(started)), "stopped_at": utc(timestamp(stopped))}


def checked_backup(record: dict) -> tuple[dict, dict, dict]:
    if record.get("schema") != "contract-audit-test-db-backup/v1":
        raise Failure("not a test database backup record")
    source, cluster = source_identity()
    if record.get("source") != source:
        raise Failure("source cluster or backup ObjectStore identity/configuration changed since backup")
    backup = record.get("backup") or {}
    name = backup.get("name", "")
    if not isinstance(name, str) or not name.startswith("contract-audit-test-backup-"):
        raise Failure("invalid test Backup name in record")
    actual = backup_identity(get("backup.postgresql.cnpg.io", name), source)
    if backup != actual:
        raise Failure("Backup UID, Barman ID, time or source differs from the recorded backup")
    return source, backup, cluster


def checked_target(value: str, backup: dict) -> str:
    target = timestamp(value)
    if target <= timestamp(backup["stopped_at"]):
        raise Failure("PITR target must be later than the selected base backup completion")
    if target > dt.datetime.now(dt.timezone.utc):
        raise Failure("PITR target cannot be in the future")
    return utc(target)


def backup_db(args) -> None:
    if os.environ.get("CONTRACT_AUDIT_TEST_BACKUP_CONFIRM") != "backup-contract-audit-test-db":
        raise Failure("set CONTRACT_AUDIT_TEST_BACKUP_CONFIRM=backup-contract-audit-test-db")
    ensure_new(args.record)
    source, _ = source_identity()
    kube("wait", "-n", NAMESPACE, "--for=condition=Ready", f"cluster/{SOURCE_CLUSTER}", "--timeout=15m", timeout=930)
    created = decode(kube("create", "-n", NAMESPACE, "-f", str(MANIFESTS / "backup.yaml"), "-o", "json"), "created Backup")
    name = (created.get("metadata") or {}).get("name", "")
    require_test(created, "created backup")
    if not name.startswith("contract-audit-test-backup-"):
        raise Failure("created Backup name does not identify the test workflow")
    print(f"Created backup/{name}; waiting for completion", file=sys.stderr)
    kube("wait", "-n", NAMESPACE, "--for=jsonpath={.status.phase}=completed", f"backup/{name}", "--timeout=60m", timeout=3630)
    after, _ = source_identity()
    if after != source:
        raise Failure(f"source configuration changed during backup/{name}; do not use it as verified evidence")
    backup = backup_identity(get("backup.postgresql.cnpg.io", name), source)
    if backup["uid"] != created["metadata"]["uid"]:
        raise Failure("Backup was replaced during creation")
    write_new(args.record, {"schema": "contract-audit-test-db-backup/v1", "source": source, "backup": backup})
    print(json.dumps({"backup": backup, "record": args.record}, ensure_ascii=False))


def restore_db(args) -> None:
    if os.environ.get("CONTRACT_AUDIT_TEST_RESTORE_CONFIRM") != "restore-contract-audit-test-db-to-new-cluster":
        raise Failure("set CONTRACT_AUDIT_TEST_RESTORE_CONFIRM=restore-contract-audit-test-db-to-new-cluster")
    ensure_new(args.record)
    if not re.fullmatch(r"contract-audit-test-restore-[a-z0-9](?:[-a-z0-9]{0,31}[a-z0-9])?", args.target_name):
        raise Failure("target name must be a new contract-audit-test-restore-* DNS name")
    source, backup, _ = checked_backup(read_json(args.backup_record))
    target = checked_target(args.target_time, backup)
    # Check orphaned PVCs and related objects too; run the drill in a namespace
    # without concurrent resource creators. Kubernetes create remains atomic.
    resources = "clusters.postgresql.cnpg.io,pods,services,persistentvolumeclaims,secrets,configmaps"
    for ref in kube("get", resources, "-n", NAMESPACE, "-o", "name").splitlines():
        existing = ref.rpartition("/")[2]
        if existing == args.target_name or existing.startswith(args.target_name + "-"):
            raise Failure(f"restore target collides with existing {ref}")
    template = (MANIFESTS / "restore-cluster.yaml").read_text(encoding="utf-8")
    values = {"__RESTORE_NAME__": args.target_name, "__BACKUP_NAME__": backup["name"],
              "__BACKUP_UID__": backup["uid"], "__SOURCE_UID__": source["cluster_uid"],
              "__BACKUP_ID_ANNOTATION__": backup["backup_id"], "__TARGET_TIME_ANNOTATION__": target,
              "__BACKUP_ID__": backup["backup_id"], "__TARGET_TIME__": target,
              "__POSTGRES_IMAGE__": source["image"], "__SERVER_NAME__": source["server_name"]}
    for marker, value in values.items():
        if template.count(marker) != 1:
            raise Failure(f"restore template marker {marker} is missing or duplicated")
        template = template.replace(marker, json.dumps(value, ensure_ascii=False))
    # create, not apply: the API server atomically refuses an existing target.
    created = decode(kube("create", "-n", NAMESPACE, "-f", "-", "-o", "json", data=template), "created restore Cluster")
    require_test(created, "restore cluster")
    uid = created["metadata"]["uid"]
    if created["metadata"].get("name") != args.target_name or uid == source["cluster_uid"]:
        raise Failure("created restore cluster identity is not isolated")
    print(f"Created isolated cluster/{args.target_name}; source cluster is unchanged", file=sys.stderr)
    kube("wait", "-n", NAMESPACE, "--for=condition=Ready", f"cluster/{args.target_name}", "--timeout=60m", timeout=3630)
    record = {"schema": "contract-audit-test-db-restore/v1", "source": source, "backup": backup,
              "target_time": target, "cluster": {"name": args.target_name, "uid": uid}}
    checked_restore(record)
    write_new(args.record, record)
    print(json.dumps({"cluster": record["cluster"], "target_time": target, "record": args.record}))


def checked_restore(record: dict) -> dict:
    if record.get("schema") != "contract-audit-test-db-restore/v1":
        raise Failure("not a test database restore record")
    source, backup, _ = checked_backup({"schema": "contract-audit-test-db-backup/v1", "source": record.get("source"),
                                        "backup": record.get("backup")})
    target = checked_target(record.get("target_time"), backup)
    name = (record.get("cluster") or {}).get("name", "")
    if not isinstance(name, str) or not re.fullmatch(r"contract-audit-test-restore-[a-z0-9](?:[-a-z0-9]{0,31}[a-z0-9])?", name):
        raise Failure("invalid restored test cluster name")
    cluster = get("cluster.postgresql.cnpg.io", name)
    require_test(cluster, "restore cluster")
    metadata, cspec = cluster.get("metadata") or {}, cluster.get("spec") or {}
    if metadata.get("uid") != record["cluster"].get("uid") or metadata.get("uid") == source["cluster_uid"]:
        raise Failure("restored cluster UID differs or aliases the source")
    annotations = metadata.get("annotations") or {}
    expected = {"backup-name": backup["name"], "backup-uid": backup["uid"], "source-uid": source["cluster_uid"],
                "backup-id": backup["backup_id"], "target-time": target}
    if any(annotations.get("contract-audit.test/" + key) != value for key, value in expected.items()):
        raise Failure("restore annotations do not bind the selected backup and PITR target")
    recovery = ((cspec.get("bootstrap") or {}).get("recovery") or {})
    recovery_target = recovery.get("recoveryTarget") or {}
    if (recovery_target.get("backupID") != backup["backup_id"] or recovery_target.get("targetTime") != target
            or recovery_target.get("exclusive") not in (None, False)
            or any(recovery_target.get(key) for key in ("targetImmediate", "targetLSN", "targetName", "targetXID"))):
        raise Failure("restored cluster no longer selects the recorded Barman ID and target time")
    external = cspec.get("externalClusters") or []
    if (cspec.get("imageName") != source["image"] or recovery.get("source") != "contract-audit-test-backup-source"
            or len(external) != 1 or external[0].get("name") != recovery["source"]
            or (external[0].get("plugin") or {}).get("name") != PLUGIN
            or (external[0].get("plugin") or {}).get("parameters") != {
                "barmanObjectName": BACKUP_STORE, "serverName": source["server_name"]}):
        raise Failure("restore cluster no longer selects the recorded image and backup archive")
    if cspec.get("plugins"):
        raise Failure("restored cluster must not write WAL or backups to the source ObjectStore")
    conditions = (cluster.get("status") or {}).get("conditions") or []
    if not any(c.get("type") == "Ready" and c.get("status") == "True" for c in conditions):
        raise Failure("restored test cluster is not Ready")
    return cluster


def documents(cluster: dict, schema: str) -> tuple[str, list[dict], str | None]:
    if not re.fullmatch(r"[a-z_][a-z0-9_]{0,62}", schema):
        raise Failure("database schema must be a simple PostgreSQL identifier")
    metadata = cluster["metadata"]
    pod_name = (cluster.get("status") or {}).get("currentPrimary")
    if not pod_name:
        raise Failure("cluster has no current primary pod")
    pod = get("pod", pod_name)
    pm = pod.get("metadata") or {}
    if ((pm.get("labels") or {}).get("cnpg.io/cluster") != metadata["name"] or
            not any(ref.get("uid") == metadata["uid"] for ref in pm.get("ownerReferences") or [])):
        raise Failure("primary pod is not owned by the selected cluster")
    table = local_restore.quote_identifier(schema) + ".documents"
    sql = ("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET LOCAL row_security=off; "
           "SELECT json_build_object('at', clock_timestamp(), 'replay', pg_last_xact_replay_timestamp(), "
           "'recovering', pg_is_in_recovery(), 'references', COALESCE((SELECT json_agg(json_build_object("
           "'id', id, 'tenant', tenant_id, 'org', org_id, 'case', case_id, 'digest', digest, "
           "'size', size, 'backend', storage_backend) ORDER BY id) FROM " + table + "), '[]'::json)); COMMIT;")
    raw = kube("exec", "-n", NAMESPACE, pod_name, "-c", "postgres", "--", "psql", "-X", "-qAt",
               "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "contract_audit_test", "-c", sql, timeout=300)
    value = decode(raw, "database snapshot")
    rows = value.get("references")
    if not isinstance(rows, list):
        raise Failure("database did not return the complete document reference list")
    if value.get("recovering"):
        raise Failure("cluster primary is still in recovery")
    ids: set[str] = set()
    for row in rows:
        if (not isinstance(row, dict) or not all(isinstance(row.get(k), str) and row[k] for k in ("id", "tenant", "org", "case"))
                or not re.fullmatch(r"[0-9a-f]{64}", str(row.get("digest", "")))
                or isinstance(row.get("size"), bool) or not isinstance(row.get("size"), int) or row["size"] < 0):
            raise Failure("database contains an invalid document identity, size or digest")
        if row["id"] in ids:
            raise Failure("database returned a duplicate document identity")
        ids.add(row["id"])
        if row.get("backend") != "remote":
            raise Failure("database references local originals; this S3 drill cannot cover them")
    if not rows:
        raise Failure("database has no document originals; an empty drill cannot prove recovery")
    return utc(timestamp(value["at"])), rows, value.get("replay")


def load_sites(path: str) -> dict[str, dict]:
    sites = read_json(path)
    if set(sites) != {"source", "backup", "replica", "restore"}:
        raise Failure("sites JSON must contain source, backup, replica and restore")
    result = {}
    for name, value in sites.items():
        if not isinstance(value, dict) or set(value) - {"endpoint", "bucket", "region", "site", "profile"}:
            raise Failure(f"site {name} must contain only endpoint, bucket, region, site and optional profile")
        if any(not isinstance(value.get(k), str) or not value[k] for k in ("endpoint", "bucket", "region", "site")):
            raise Failure(f"site {name} is missing endpoint, bucket, region or site")
        if value.get("profile") is not None and (not isinstance(value["profile"], str) or not value["profile"]):
            raise Failure(f"site {name} profile must be a nonempty string")
        url = urlsplit(value["endpoint"])
        if url.scheme not in ("http", "https") or not url.hostname or "@" in url.netloc or url.query or url.fragment:
            raise Failure(f"site {name} endpoint must be an HTTP(S) URL without credentials, query or fragment")
        endpoint = urlunsplit((url.scheme.lower(), url.netloc.lower(), url.path.rstrip("/"), "", ""))
        bucket = value["bucket"]
        if not re.fullmatch(r"[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]", bucket):
            raise Failure(f"site {name} has an invalid bucket name")
        result[name] = {"endpoint": endpoint, "bucket": bucket, "region": value["region"], "site": value["site"],
                        "profile": value.get("profile", "")}
    if len({item["bucket"] for item in result.values()}) != 4:
        raise Failure("all source, backup, replica and restore bucket names must differ")
    if (result["source"]["site"] == result["replica"]["site"] or
            (result["source"]["endpoint"], result["source"]["region"]) == (result["replica"]["endpoint"], result["replica"]["region"])):
        raise Failure("replica must declare a different site and a different endpoint or region")
    return result


class Site:
    def __init__(self, config: dict):
        self.config = config
        self.env = dict(os.environ, AWS_PAGER="", AWS_CLI_AUTO_PROMPT="off")
        if config["profile"]:
            # An explicit profile must not silently use unrelated environment credentials.
            for key in ("AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE"):
                self.env.pop(key, None)

    def call(self, operation: str, *args: str, timeout: int = 300) -> dict:
        argv = ["aws", "--endpoint-url", self.config["endpoint"], "--region", self.config["region"],
                "--no-cli-pager", "--output", "json"]
        if self.config["profile"]:
            argv += ["--profile", self.config["profile"]]
        raw = command(argv + ["s3api", operation, "--bucket", self.config["bucket"], *args], env=self.env, timeout=timeout)
        result = decode(raw or "{}", f"S3 {operation}")
        if not isinstance(result, dict):
            raise Failure(f"S3 {operation} returned an unexpected result")
        return result

    def check(self) -> None:
        tags = self.call("get-bucket-tagging").get("TagSet") or []
        if not any(tag.get("Key") == "environment" and tag.get("Value") == "test" for tag in tags):
            raise Failure(f"bucket {self.config['bucket']} must be tagged environment=test")
        if self.call("get-bucket-versioning").get("Status") != "Enabled":
            raise Failure(f"bucket {self.config['bucket']} must have versioning enabled")

    def versions(self) -> tuple[list[dict], list[dict]]:
        result = self.call("list-object-versions")
        return result.get("Versions") or [], result.get("DeleteMarkers") or []

    def ensure_empty(self) -> None:
        versions, markers = self.versions()
        if versions or markers:
            raise Failure(f"destination bucket {self.config['bucket']} contains versions or delete markers")

    def get(self, key: str, version: str, file: Path, size: int, digest: str | None = None) -> str:
        result = self.call("get-object", "--key", key, "--version-id", version, str(file))
        if result.get("VersionId") != version or result.get("ContentLength") != size:
            raise Failure(f"{self.config['bucket']}/{key}: downloaded version or size differs")
        actual = local_restore.sha256_file(file)
        if file.stat().st_size != size or (digest is not None and actual != digest):
            raise Failure(f"{self.config['bucket']}/{key}: downloaded bytes differ from size or SHA-256")
        return actual

    def put(self, key: str, file: Path) -> str:
        result = self.call("put-object", "--key", key, "--body", str(file), "--if-none-match", "*")
        version = result.get("VersionId")
        if not isinstance(version, str) or not version or version == "null":
            raise Failure(f"{self.config['bucket']}/{key}: upload has no persistent version ID")
        return version


def inventory_at(site: Site, target: str) -> dict[str, dict]:
    versions, markers = site.versions()
    latest: dict[str, tuple[dt.datetime, bool, dict]] = {}
    for deleted, entries in ((False, versions), (True, markers)):
        for item in entries:
            key, version = item.get("Key"), item.get("VersionId")
            if not isinstance(key, str) or not key or not isinstance(version, str) or not version:
                raise Failure("source version inventory has an invalid key or version")
            modified = timestamp(item.get("LastModified"))
            if modified > timestamp(target):
                continue
            prior = latest.get(key)
            if prior and modified == prior[0]:
                raise Failure(f"source {key}: versions have indistinguishable timestamps at the recovery target")
            if prior is None or modified > prior[0]:
                latest[key] = (modified, deleted, item)
    result = {}
    for key, (_, deleted, item) in latest.items():
        if deleted:
            continue
        if item["VersionId"] == "null" or not isinstance(item.get("Size"), int) or item["Size"] < 0:
            raise Failure(f"source {key}: target version was not protected by versioning or has an invalid size")
        result[key] = {"version": item["VersionId"], "size": item["Size"], "modified": item["LastModified"]}
    return result


def references(rows: list[dict]) -> dict[str, tuple[int, str]]:
    expected: dict[str, tuple[int, str]] = {}
    for row in rows:
        key = local_restore.remote_object_key(row["tenant"], row["case"], row["digest"])
        value = (row["size"], row["digest"])
        if key in expected and expected[key] != value:
            raise Failure(f"database contains conflicting original references for {key}")
        expected[key] = value
    return expected


def verify_site(site: Site, objects: list[dict], version_field: str, temp: Path) -> None:
    versions, markers = site.versions()
    expected = {(o["key"], o[version_field], o["size"]) for o in objects}
    actual = {(o.get("Key"), o.get("VersionId"), o.get("Size")) for o in versions}
    if markers or len(versions) != len(expected) or actual != expected:
        raise Failure(f"bucket {site.config['bucket']}: key/version/size inventory differs or contains delete markers")
    for obj in objects:
        site.get(obj["key"], obj[version_field], temp, obj["size"], obj["sha256"])


def backup_objects(args) -> None:
    ensure_new(args.output)
    source, backup, cluster = checked_backup(read_json(args.backup_record))
    target = checked_target(args.target_time, backup)
    sites = load_sites(args.sites)
    clients = {name: Site(sites[name]) for name in ("source", "backup", "replica")}
    for client in clients.values():
        client.check()
    clients["backup"].ensure_empty()
    clients["replica"].ensure_empty()
    observed_at, rows, _ = documents(cluster, args.schema)
    if timestamp(observed_at) < timestamp(target):
        raise Failure("database snapshot is earlier than the requested recovery target")
    snapshot = inventory_at(clients["source"], target)
    expected = references(rows)
    if not set(expected).issubset(snapshot):
        raise Failure(f"source versions at target are missing {len(set(expected) - set(snapshot))} referenced originals")
    objects = []
    with tempfile.TemporaryDirectory(prefix="contract-audit-recovery-") as directory:
        file = Path(directory) / "original"
        for key, item in sorted(snapshot.items()):
            digest = clients["source"].get(key, item["version"], file, item["size"])
            if key in expected and expected[key] != (item["size"], digest):
                raise Failure(f"source {key}: bytes do not match the database digest and size")
            backup_version = clients["backup"].put(key, file)
            clients["backup"].get(key, backup_version, file, item["size"], digest)
            replica_version = clients["replica"].put(key, file)
            clients["replica"].get(key, replica_version, file, item["size"], digest)
            objects.append({"key": key, "size": item["size"], "sha256": digest,
                            "source_version": item["version"], "source_modified": item["modified"],
                            "backup_version": backup_version, "replica_version": replica_version})
        verify_site(clients["backup"], objects, "backup_version", file)
        verify_site(clients["replica"], objects, "replica_version", file)
    if source_identity()[0] != source:
        raise Failure("source database identity changed during object backup")
    manifest = {"schema": "contract-audit-test-original-backup/v1", "source": source, "backup": backup,
                "target_time": target, "database_schema": args.schema, "source_snapshot_time": observed_at,
                "sites": {key: sites[key] for key in ("source", "backup", "replica")}, "references": rows,
                "objects": objects, "unreferenced_keys": sorted(set(snapshot) - set(expected)),
                "checks": {"object_copy_and_hash": "PASS", "restored_db_reconciliation": "NOT_RUN",
                           "pitr_recovery": "NOT_RUN", "independent_failure_domains": "NOT_RUN"}}
    write_new(args.output, manifest)
    print(json.dumps({"object_copy_and_hash": "PASS", "pitr_recovery": "NOT_RUN", "objects": len(objects),
                      "references": len(rows), "unreferenced": len(manifest["unreferenced_keys"]), "manifest": args.output}))


def restore_objects(args) -> None:
    ensure_new(args.output)
    manifest, record, sites = read_json(args.manifest), read_json(args.restore_record), load_sites(args.sites)
    if manifest.get("schema") != "contract-audit-test-original-backup/v1":
        raise Failure("not a test original backup manifest")
    cluster = checked_restore(record)
    if any(manifest.get(k) != record.get(k) for k in ("source", "backup", "target_time")):
        raise Failure("object backup and restored database do not select the same source, backup and target")
    if manifest.get("sites") != {key: sites[key] for key in ("source", "backup", "replica")}:
        raise Failure("object source, backup or replica site identity differs from the captured manifest")
    clients = {name: Site(sites[name]) for name in ("backup", "replica", "restore")}
    for client in clients.values():
        client.check()
    clients["restore"].ensure_empty()
    observed_at, rows, replay = documents(cluster, manifest.get("database_schema", ""))
    if rows != manifest.get("references"):
        raise Failure("restored database document references differ from the captured snapshot")
    if not replay or timestamp(replay) > timestamp(record["target_time"]):
        raise Failure("restored cluster has no usable WAL replay timestamp at or before the PITR target")
    objects = manifest.get("objects")
    if not isinstance(objects, list) or not objects or any(not isinstance(o, dict) for o in objects):
        raise Failure("object manifest has no complete object inventory")
    by_key = {obj.get("key"): obj for obj in objects}
    if len(by_key) != len(objects):
        raise Failure("object manifest contains duplicate keys")
    for key, (size, digest) in references(rows).items():
        if key not in by_key or (by_key[key].get("size"), by_key[key].get("sha256")) != (size, digest):
            raise Failure(f"manifest does not match restored database reference {key}")
    restored = []
    with tempfile.TemporaryDirectory(prefix="contract-audit-recovery-") as directory:
        file = Path(directory) / "original"
        verify_site(clients["backup"], objects, "backup_version", file)
        verify_site(clients["replica"], objects, "replica_version", file)
        for obj in objects:
            clients["replica"].get(obj["key"], obj["replica_version"], file, obj["size"], obj["sha256"])
            version = clients["restore"].put(obj["key"], file)
            clients["restore"].get(obj["key"], version, file, obj["size"], obj["sha256"])
            restored.append({**obj, "restore_version": version})
        verify_site(clients["restore"], restored, "restore_version", file)
    report = {"schema": "contract-audit-test-original-reconciliation/v1",
              "checks": {"object_copy_and_hash": "PASS", "restored_db_reconciliation": "PASS",
                         "configured_target_and_wal_replay": "PASS", "independent_pitr_boundary": "NOT_RUN",
                         "independent_failure_domains": "NOT_RUN"},
              "source": record["source"], "backup": record["backup"], "target_time": record["target_time"],
              "cluster": record["cluster"], "restore_site": sites["restore"], "database_snapshot_time": observed_at,
              "last_replay_time": replay, "references": rows, "objects": restored,
              "unreferenced_keys": manifest.get("unreferenced_keys", [])}
    write_new(args.output, report)
    print(json.dumps({"object_copy_and_hash": "PASS", "restored_db_reconciliation": "PASS",
                      "configured_target_and_wal_replay": "PASS", "independent_pitr_boundary": "NOT_RUN",
                      "objects": len(restored), "references": len(rows), "report": args.output}))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="action", required=True)
    backup = sub.add_parser("backup-db", help="create and record one completed test database backup")
    backup.add_argument("--record", required=True)
    backup.set_defaults(run=backup_db)
    restore = sub.add_parser("restore-db", help="create an isolated, explicitly targeted PITR cluster")
    restore.add_argument("--backup-record", required=True)
    restore.add_argument("--target-time", required=True)
    restore.add_argument("--target-name", required=True)
    restore.add_argument("--record", required=True)
    restore.set_defaults(run=restore_db)
    objects = sub.add_parser("backup-objects", help="capture exact source versions and verify two independent copies")
    objects.add_argument("--backup-record", required=True)
    objects.add_argument("--target-time", required=True)
    objects.add_argument("--sites", required=True)
    objects.add_argument("--schema", default="public")
    objects.add_argument("--output", required=True)
    objects.set_defaults(run=backup_objects)
    reconcile = sub.add_parser("restore-objects", help="restore the cross-site copies and reconcile against PITR DB")
    reconcile.add_argument("--manifest", required=True)
    reconcile.add_argument("--restore-record", required=True)
    reconcile.add_argument("--sites", required=True)
    reconcile.add_argument("--output", required=True)
    reconcile.set_defaults(run=restore_objects)
    args = parser.parse_args()
    try:
        args.run(args)
    except (Failure, OSError, KeyError, TypeError, ValueError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

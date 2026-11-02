"""Regression cases for the release-lock and rollback contract.

All image references and Kubernetes objects in this file are synthetic. No
registry or cluster is contacted; runtime acceptance remains a separate gate.
"""
from contextlib import nullcontext
from copy import deepcopy
import hashlib
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deployments/scripts/release-k8s.py"
SPEC = importlib.util.spec_from_file_location("release_k8s", SCRIPT)
assert SPEC and SPEC.loader
RELEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RELEASE)


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


def make_lock(version, targets=(), evidence=None):
    return RELEASE.validate_lock({
        "format": RELEASE.LOCK_FORMAT,
        "version": version,
        "source_identity_sha256": digest("source-" + version),
        "images": {name: f"registry.example.test/app/{name}@sha256:{digest(version + name)}"
                   for name in RELEASE.IMAGE_COMPONENTS},
        "packaging_report_sha256": None,
        "rollback_compatibility": {"targets": sorted(targets), "evidence": evidence},
    })


def make_job(lock, timestamp, status="Complete"):
    annotations = RELEASE.annotations(lock)
    return {
        "metadata": {"name": RELEASE.job_name(lock), "creationTimestamp": timestamp,
                     "annotations": deepcopy(annotations)},
        "spec": {"template": {"metadata": {"annotations": deepcopy(annotations)},
                              "spec": {"initContainers": [
                                                          {"name": "offline-migrator-provision", "image": lock["images"]["backend"], "args": ["/app/migrate", "provision-offline-migrator"], "env": [
                                                              *[{"name": key, "valueFrom": {"secretKeyRef": {"name": "contract-audit-db-urls", "key": key}}} for key in ("AUTH_ADMIN_DATABASE_URL", "MIGRATOR_DATABASE_URL", "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL")]]},
                                                          {"name": "clm-owner-provision", "image": lock["images"]["backend"], "args": ["/app/migrate", "provision-clm-maintenance"], "env": [
                                                              {"name": "CLM_ADMIN_DATABASE_URL", "valueFrom": {"secretKeyRef": {"name": "contract-audit-db-urls", "key": "AUTH_ADMIN_DATABASE_URL"}}},
                                                              *[{"name": key, "valueFrom": {"secretKeyRef": {"name": "contract-audit-db-urls", "key": key}}} for key in ("MIGRATOR_DATABASE_URL", "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL")]]},
                                                          {"name": "auth-owner-provision", "image": lock["images"]["backend"], "args": ["/app/migrate", "provision-tenant-identity"], "env": [
                                                              *[{"name": key, "valueFrom": {"secretKeyRef": {"name": "contract-audit-db-urls", "key": key}}} for key in ("AUTH_ADMIN_DATABASE_URL", "MIGRATOR_DATABASE_URL", "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL")]]}],
                                       "containers": [{"name": "migrate", "image": lock["images"]["backend"],
                                                       "args": ["/app/migrate"],
                                                       "env": [{"name": "SERVICE_DATABASE_ROLES", "value": "true"}]}]}}},
        "status": {"conditions": [{"type": status, "status": "True"}]},
    }


def make_template(lock, deployment):
    component, container = RELEASE.DEPLOYMENTS[deployment]
    return {"metadata": {"annotations": RELEASE.annotations(lock)},
            "spec": {"containers": [{"name": container, "image": lock["images"][component]}]}}


def make_deployment(lock, deployment):
    return {
        "metadata": {"name": deployment, "uid": "uid-" + deployment, "generation": 3},
        "spec": {"replicas": 1, "template": make_template(lock, deployment)},
        "status": {"observedGeneration": 3, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1},
    }


def make_replica(lock, deployment, revision):
    return {
        "metadata": {"annotations": {"deployment.kubernetes.io/revision": str(revision)},
                     "ownerReferences": [{"kind": "Deployment", "uid": "uid-" + deployment, "controller": True}]},
        "spec": {"template": make_template(lock, deployment)},
    }


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.old = make_lock("old")
        self.current = make_lock("current", [RELEASE.lock_id(self.old)], "release-evidence/current-to-old")
        self.jobs = [make_job(self.old, "2026-01-01T00:00:00Z"),
                     make_job(self.current, "2026-01-02T00:00:00Z")]
        self.deployments = [make_deployment(self.current, name) for name in RELEASE.DEPLOYMENTS]
        self.replicas = [make_replica(self.old, "core-api", 2)]

    def kube_objects(self, *args):
        if args[:2] == ("get", "deployments"):
            return {"items": deepcopy(self.deployments)}
        if args[:2] == ("get", "replicasets"):
            return {"items": deepcopy(self.replicas)}
        self.fail(f"unexpected kubectl request: {args}")

    def inspect_rollback(self, target=None, current=None):
        with patch.object(RELEASE, "migration_jobs", return_value=deepcopy(self.jobs)), \
                patch.object(RELEASE, "kube_json", side_effect=self.kube_objects):
            return RELEASE.rollback_state("core-api", 2, target or self.old, current or self.current)

    def test_lock_requires_all_registry_manifest_digests_and_evidence_for_window(self):
        for image in ("contract-audit-backend:release", "registry.example.test/app/backend:latest",
                      "registry.example.test/app/backend@sha256:123", "localhost:5000/app/backend@sha256:" + digest("x")):
            lock = deepcopy(self.old)
            lock["images"]["backend"] = image
            with self.subTest(image=image), self.assertRaises(ValueError):
                RELEASE.validate_lock(lock)
        incomplete = deepcopy(self.old)
        del incomplete["images"]["portal"]
        with self.assertRaises(ValueError):
            RELEASE.validate_lock(incomplete)
        with self.assertRaises(ValueError):
            make_lock("current", [RELEASE.lock_id(self.old)])

    def test_real_templates_bind_document_and_migration_to_same_backend_digest(self):
        workloads = (ROOT / "deployments/k8s/base/workloads.yaml").read_text(encoding="utf-8")
        migration = (ROOT / "deployments/k8s/migrate/migration-job.yaml").read_text(encoding="utf-8")

        def kustomize(command, **_kwargs):
            return migration if command[-1].endswith("/migrate") else workloads

        with patch.object(RELEASE, "run", side_effect=kustomize):
            apps = RELEASE.rendered_objects("application", self.old)
            job = RELEASE.rendered_objects("migration", self.old)[0]
        self.assertEqual(len(apps), len(RELEASE.DEPLOYMENTS))
        for obj in apps:
            name = obj["metadata"]["name"]
            image = obj["spec"]["template"]["spec"]["containers"][0]["image"]
            self.assertEqual(image, self.old["images"][RELEASE.DEPLOYMENTS[name][0]])
            self.assertEqual(obj["spec"]["template"]["metadata"]["annotations"][RELEASE.LOCK_ANNOTATION],
                             RELEASE.lock_id(self.old))
        self.assertEqual(job["spec"]["template"]["spec"]["containers"][0]["image"], self.old["images"]["backend"])
        self.assertTrue(all(item["image"] == self.old["images"]["backend"] for item in job["spec"]["template"]["spec"]["initContainers"]))
        self.assertEqual(job["metadata"]["name"], RELEASE.job_name(self.old))

    def test_migration_job_requires_isolated_auth_owner_preprovision(self):
        template = make_job(self.old, "2026-01-01T00:00:00Z")["spec"]["template"]
        template["spec"]["initContainers"] = []
        with self.assertRaisesRegex(ValueError, "offline migrator"):
            RELEASE.migration_container(template)

    def test_migration_job_never_passes_admin_dsn_to_resident_migrator(self):
        template = make_job(self.old, "2026-01-01T00:00:00Z")["spec"]["template"]
        template["spec"]["containers"][0]["env"].append({"name": "AUTH_ADMIN_DATABASE_URL"})
        with self.assertRaisesRegex(ValueError, "never receive an admin database URL"):
            RELEASE.migration_container(template)

    def test_deploy_rejects_a_different_completed_migration_before_apply(self):
        previous = RELEASE.migration_record(self.jobs[0])
        with patch.object(RELEASE, "rendered_objects", return_value=[{"kind": "Deployment"}]), \
                patch.object(RELEASE, "release_operation", side_effect=nullcontext), \
                patch.object(RELEASE, "active_migration", return_value=previous), \
                patch.object(RELEASE, "apply_objects") as apply_objects:
            with self.assertRaisesRegex(ValueError, "latest completed migration"):
                RELEASE.deploy(self.current)
            apply_objects.assert_not_called()

    def test_latest_incomplete_failed_or_ambiguous_migration_blocks_release(self):
        failed = deepcopy(self.jobs)
        failed[-1]["status"]["conditions"][0]["type"] = "Failed"
        with self.assertRaises(ValueError):
            RELEASE.active_migration(failed)
        pending = deepcopy(self.jobs)
        pending[-1]["status"] = {}
        with self.assertRaises(ValueError):
            RELEASE.active_migration(pending)
        ambiguous = deepcopy(self.jobs)
        ambiguous[-1]["metadata"]["creationTimestamp"] = ambiguous[0]["metadata"]["creationTimestamp"]
        with self.assertRaises(ValueError):
            RELEASE.active_migration(ambiguous)
        wrong_command = deepcopy(self.jobs)
        wrong_command[-1]["spec"]["template"]["spec"]["containers"][0]["args"] = ["/app/server"]
        with self.assertRaisesRegex(ValueError, "must run"):
            RELEASE.active_migration(wrong_command)

    def test_exact_history_and_declared_window_allow_specific_revision(self):
        state = self.inspect_rollback()
        self.assertEqual(state["target_lock"], RELEASE.lock_id(self.old))
        self.assertEqual(state["target_image"], self.old["images"]["backend"])
        self.assertEqual(state["current_migration"]["lock"], RELEASE.lock_id(self.current))
        self.assertEqual(state["compatibility_evidence"], "release-evidence/current-to-old")

    def test_failed_target_rollout_can_return_to_known_revision(self):
        target = next(obj for obj in self.deployments if obj["metadata"]["name"] == "core-api")
        target["status"]["availableReplicas"] = 0
        active = make_replica(self.current, "core-api", 3)
        active["spec"]["replicas"] = 1
        self.replicas.append(active)
        self.assertEqual(self.inspect_rollback()["target_lock"], RELEASE.lock_id(self.old))
        active["spec"]["template"] = make_template(make_lock("unrelated"), "core-api")
        with self.assertRaisesRegex(ValueError, "active target ReplicaSet is outside"):
            self.inspect_rollback()

    def test_rollback_denies_unknown_window_missing_history_and_unrelated_release(self):
        no_window = make_lock("current")
        self.jobs[-1] = make_job(no_window, "2026-01-02T00:00:00Z")
        with self.assertRaisesRegex(ValueError, "compatibility is unknown"):
            self.inspect_rollback(current=no_window)
        self.jobs = [make_job(self.current, "2026-01-02T00:00:00Z")]
        with self.assertRaisesRegex(ValueError, "retained completed migration"):
            self.inspect_rollback()
        self.jobs.insert(0, make_job(self.old, "2026-01-01T00:00:00Z"))
        self.deployments[0] = make_deployment(make_lock("unrelated"), "gateway")
        with self.assertRaisesRegex(ValueError, "outside the declared rollback version window"):
            self.inspect_rollback()

    def test_rollback_rejects_wrong_replica_digest(self):
        self.replicas[0]["spec"]["template"]["spec"]["containers"][0]["image"] = self.current["images"]["backend"]
        with self.assertRaisesRegex(ValueError, "does not contain the target candidate image"):
            self.inspect_rollback()

    def test_rollback_uses_explicit_revision_and_checks_result(self):
        expected = {"target_image": self.old["images"]["backend"]}
        restored = make_deployment(self.old, "core-api")
        with patch.dict("os.environ", {"CONTRACT_AUDIT_ROLLBACK_CONFIRM": "application-deployment-only"}), \
                patch.object(RELEASE, "release_operation", side_effect=nullcontext), \
                patch.object(RELEASE, "rollback_state", return_value=expected), \
                patch.object(RELEASE, "kubectl", return_value="") as kubectl, \
                patch.object(RELEASE, "kube_json", return_value=restored):
            RELEASE.rollback("core-api", 2, self.old, self.current)
        self.assertEqual(kubectl.call_args_list[0].args,
                         ("rollout", "undo", "deployment/core-api", "--to-revision=2"))
        self.assertEqual(kubectl.call_args_list[1].args,
                         ("rollout", "status", "deployment/core-api", "--timeout=10m"))


if __name__ == "__main__":
    unittest.main()

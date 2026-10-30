"""The source identity manifest must cover executable delivery inputs, deterministically.

The deployment tree holds executable delivery inputs (local bootstrap and test
scripts, the Kustomize application source) and is part of the identity, while
build caches and dependency artifacts stay excluded.
"""
import tempfile
import unittest
from pathlib import Path

from source_identity import source_identity

ROOT_FILES = ("go.mod", "go.sum", "quality-gates.json", "Makefile", "compose.yaml", ".env.example", "compose.app.yaml")


class SourceIdentityScopeTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        for name in ROOT_FILES:
            self.write(name, f"{name}\n")
        self.write("cmd/server/main.go", "package main\n")
        self.write("capability-packs/telecom.json", "{\"version\":1}\n")
        self.write("deployments/local.py", "print('bootstrap')\n")
        self.write("deployments/scripts/journey-local.py", "print('journey')\n")
        self.write("deployments/k8s/base/config.yaml", "apiVersion: v1\n")
        self.write("deployments/k8s/base/kustomization.yaml", "resources: []\n")
        self.write("deploy/nginx/client.conf", "server {}\n")
        self.baseline = source_identity(self.root)

    def tearDown(self):
        self.directory.cleanup()

    def write(self, relative, text):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def test_digest_is_deterministic_and_lists_deployment_inputs(self):
        again = source_identity(self.root)
        self.assertEqual(self.baseline["digest"], again["digest"])
        paths = {record["path"] for record in self.baseline["files"]}
        for required in ("deployments/local.py", "deployments/scripts/journey-local.py",
                         "deployments/k8s/base/config.yaml", "deployments/k8s/base/kustomization.yaml",
                         "deploy/nginx/client.conf", "cmd/server/main.go"):
            self.assertIn(required, paths)

    def test_changed_capability_pack_changes_digest(self):
        self.write("capability-packs/telecom.json", "{\"version\":2}\n")
        changed = source_identity(self.root)
        self.assertNotEqual(self.baseline["digest"], changed["digest"])
        self.assertIn("capability-packs/telecom.json",
                      {record["path"] for record in changed["files"]})

    def test_changed_deployment_script_changes_digest(self):
        self.write("deployments/scripts/journey-local.py", "print('journey v2')\n")
        changed = source_identity(self.root)
        self.assertNotEqual(self.baseline["digest"], changed["digest"])
        self.assertIn("deployments/scripts/journey-local.py",
                      {record["path"] for record in changed["files"]})

    def test_changed_deployment_manifest_changes_digest(self):
        self.write("deployments/k8s/base/config.yaml", "apiVersion: v1\nkind: ConfigMap\n")
        self.write("deployments/scripts/nightly-k8s-check.sh", "#!/bin/sh\nexit 0\n")
        changed = source_identity(self.root)
        self.assertNotEqual(self.baseline["digest"], changed["digest"])
        self.assertIn("deployments/scripts/nightly-k8s-check.sh",
                      {record["path"] for record in changed["files"]})

    def test_build_cache_and_dependency_artifacts_do_not_change_digest(self):
        self.write("deployments/__pycache__/local.cpython-314.pyc", "cache\n")
        self.write("deployments/scripts/__pycache__/journey-local.cpython-314.pyc", "cache\n")
        self.write("web/node_modules/left-pad/index.js", "module.exports = 1\n")
        self.write("web/dist/assets/app.js", "built\n")
        self.write("web/.impeccable/config.json", "{}\n")
        self.write("web/.impeccable/hook.cache.json", "{\"generated\": true}\n")
        self.write(".github/hooks/impeccable.json", "{}\n")
        unchanged = source_identity(self.root)
        self.assertEqual(self.baseline["digest"], unchanged["digest"])
        excluded = {record["path"] for record in unchanged["files"]}
        self.assertFalse([path for path in excluded if path.endswith(".pyc") or "__pycache__" in path])
        self.assertFalse([path for path in excluded if path.startswith("web/node_modules/") or path.startswith("web/dist/")])
        self.assertFalse([path for path in excluded if path.startswith("web/.impeccable/")])
        self.assertNotIn(".github/hooks/impeccable.json", excluded)


class SourceIdentityRepositoryTests(unittest.TestCase):
    def test_real_repository_covers_deployment_delivery_inputs_without_build_cache(self):
        root = Path(__file__).resolve().parents[1]
        first = source_identity(root)
        second = source_identity(root)
        self.assertEqual(first["digest"], second["digest"])
        paths = {record["path"] for record in first["files"]}
        for required in ("deployments/local.py",
                         "deployments/scripts/journey-local.py",
                         "deployments/scripts/restore-local.py",
                         "deployments/scripts/fault-test-local.py",
                         "deployments/scripts/backup-test-db.sh",
                         "deployments/scripts/restore-test-db.sh",
                         "deployments/k8s/base/config.yaml",
                         "deployments/k8s/base/workloads.yaml",
                         "deployments/k8s/test/backup.yaml",
                         "deploy/app/Dockerfile",
                         "capability-packs/dedicated-line-1.0.0.json",
                         "compose.app.yaml",
                         "compose.observe.yaml"):
            self.assertIn(required, paths)
        self.assertFalse([path for path in paths if path.endswith(".pyc") or "__pycache__" in path])
        self.assertFalse([path for path in paths if path.startswith("artifacts/") or path.startswith("outputs/")])


if __name__ == "__main__":
    unittest.main()

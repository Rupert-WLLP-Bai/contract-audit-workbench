"""Image tag selection stays isolated across repeated packager invocations."""
import unittest
import importlib.util
from pathlib import Path
import tempfile

PACKAGE = Path(__file__).resolve().parents[1] / "deployments/scripts/package-app-images.py"
SPEC = importlib.util.spec_from_file_location("package_app_images", PACKAGE)
assert SPEC and SPEC.loader
PACKAGER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PACKAGER)


class ImageTagSelectionTests(unittest.TestCase):
    def test_suffixes_do_not_mutate_default_or_each_other(self):
        first_backend, first_frontends = PACKAGER.image_tags("p0-20261030")
        second_backend, second_frontends = PACKAGER.image_tags("p1-review")
        default_backend, default_frontends = PACKAGER.image_tags()

        self.assertEqual(first_backend, "contract-audit-backend:p0-20261030")
        self.assertEqual(second_backend, "contract-audit-backend:p1-review")
        self.assertEqual(default_backend, PACKAGER.BACKEND_TAG)
        self.assertEqual(default_frontends, PACKAGER.FRONTENDS)
        self.assertEqual(first_frontends["client"][0], "contract-audit-client:p0-20261030")
        self.assertEqual(second_frontends["client"][0], "contract-audit-client:p1-review")
        self.assertEqual(first_frontends["client"][1], PACKAGER.FRONTENDS["client"][1])
        self.assertEqual(second_frontends["client"][1], PACKAGER.FRONTENDS["client"][1])


class ComponentIdentityTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.old_root = PACKAGER.ROOT
        PACKAGER.ROOT = self.root
        self.addCleanup(setattr, PACKAGER, "ROOT", self.old_root)
        files = (
            "go.mod", "go.sum", "quality-gates.json", "Makefile", "compose.yaml", ".env.example",
            "compose.app.yaml", "compose.observe.yaml", "deploy/app/entrypoint.sh",
            "web/package.json", "web/bun.lock", "web/index.html", "web/admin.html", "web/portal.html",
            "web/vite.config.ts", "web/tsconfig.json", "web/tsconfig.test.json",
            "deployments/scripts/package-app-images.py", "capability-packs/telecom.json",
            "web/src/app.ts", "deploy/app/nginx/client.conf", "deploy/app/nginx/admin.conf",
            "deploy/app/nginx/portal.conf", "deploy/app/nginx/backend-proxy.conf",
            "deploy/app/nginx/proxy-headers.conf",
        )
        for name in files:
            self.write(name, "fixture\n")
        for command in PACKAGER.COMMANDS:
            self.write(f"cmd/{command}/main.go", "package main\n")
        self.write("internal/service.go", "package internal\n")
        for name, (_, entrypoint) in PACKAGER.FRONTENDS.items():
            self.write(f"web/dist/{name}/{entrypoint}", "<!-- fixture -->\n")

    def write(self, relative, content):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def test_capability_pack_changes_backend_component_identity(self):
        before = PACKAGER.source_snapshot(PACKAGER.frontend_files())
        self.write("capability-packs/telecom.json", "{\"rules\":[\"changed\"]}\n")
        after = PACKAGER.source_snapshot(PACKAGER.frontend_files())
        self.assertNotEqual(before["sha256"], after["sha256"])
        self.assertNotEqual(before["component_identity_sha256"]["backend"],
                            after["component_identity_sha256"]["backend"])


if __name__ == "__main__":
    unittest.main()

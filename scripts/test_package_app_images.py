"""Image tag selection stays isolated across repeated packager invocations."""
import unittest
import importlib.util
from pathlib import Path

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


if __name__ == "__main__":
    unittest.main()

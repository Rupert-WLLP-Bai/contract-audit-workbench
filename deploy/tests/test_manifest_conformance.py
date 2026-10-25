"""Exercise manifest self-tests using the same isolated copies as the CLI."""
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ManifestConformanceTests(unittest.TestCase):
    def test_self_test_accepts_baseline_and_rejects_all_injected_defects(self):
        result = subprocess.run(
            [sys.executable, "-I", str(ROOT / "deployments/scripts/manifest-conformance.py"), "--self-test"],
            cwd=ROOT,
            text=True,
            capture_output=True,
            timeout=120,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("PASS: every injected defect was refused", result.stdout)


if __name__ == "__main__":
    unittest.main()

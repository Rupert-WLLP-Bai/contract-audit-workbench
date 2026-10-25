"""The source tree must keep AGENTS.md and keep delivery material out."""
import subprocess
import unittest
from pathlib import Path

import repository_policy

ROOT = Path(__file__).resolve().parents[1]


class RepositoryPolicyTest(unittest.TestCase):
    def test_agents_and_readme_stay_while_delivery_files_stay_out(self):
        self.assertFalse(repository_policy.is_ignored(ROOT, "AGENTS.md"))
        self.assertFalse(repository_policy.is_ignored(ROOT, "README.md"))
        for relative in (
            "ROADMAP.md",
            "docs/design.md",
            "prompts/00-baseline-and-repository-policy.md",
            "presentation/deck.pptx",
            "screenshots/demo.png",
            "materials/sample.zip",
        ):
            self.assertTrue(repository_policy.is_ignored(ROOT, relative), relative)
        self.assertFalse(repository_policy.is_ignored(ROOT, "internal/documentparse/testdata/pdf/gold.json"))
        self.assertFalse(repository_policy.is_ignored(ROOT, "testdata/decision-zh-v1.json"))

    def test_matcher_agrees_with_git(self):
        samples = [
            "AGENTS.md",
            "README.md",
            "internal/store/revision.go",
            "docs/plan.md",
            "prompts/START-CURSOR.md",
            "web/src/app.tsx",
            "internal/model/testdata/warranty36.png",
            "notes/screenshot.png",
            ".env",
            ".env.example",
        ]
        for relative in samples:
            git = subprocess.run(
                ["git", "-C", str(ROOT), "check-ignore", "-q", "--no-index", relative],
                capture_output=True,
            )
            self.assertIn(git.returncode, (0, 1), relative)
            self.assertEqual(repository_policy.is_ignored(ROOT, relative), git.returncode == 0, relative)

    def test_current_tree_has_no_policy_violations(self):
        self.assertEqual(repository_policy.violations(ROOT), [])


if __name__ == "__main__":
    unittest.main()

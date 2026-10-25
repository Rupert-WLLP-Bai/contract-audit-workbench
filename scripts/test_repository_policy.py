"""The source tree must keep AGENTS.md and keep delivery material out."""
import subprocess
import tempfile
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

    def test_tracked_ignored_files_and_oversized_fixtures_fail(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            subprocess.run(["git", "init"], cwd=root, check=True, capture_output=True)
            (root / ".gitignore").write_text(
                "*.md\n!/README.md\n!/AGENTS.md\n!/public.md\n*.pdf\n!**/testdata/**/*.pdf\n",
                encoding="utf-8",
            )
            (root / "README.md").write_text("read\n", encoding="utf-8")
            (root / "AGENTS.md").write_text("agents\n", encoding="utf-8")
            (root / "public.md").write_text("untracked markdown\n", encoding="utf-8")
            design = root / "docs"
            design.mkdir()
            (design / "design.md").write_text("forced design\n", encoding="utf-8")
            fixture = root / "internal" / "documentparse" / "testdata" / "pdf"
            fixture.mkdir(parents=True)
            (fixture / "ok.pdf").write_bytes(b"%PDF-1.4\n")
            (fixture / "huge.pdf").write_bytes(b"x" * (repository_policy.FIXTURE_MAX_BYTES + 1))
            subprocess.run(
                ["git", "add", "-f", "README.md", "AGENTS.md", "docs/design.md",
                 "internal/documentparse/testdata/pdf/ok.pdf",
                 "internal/documentparse/testdata/pdf/huge.pdf"],
                cwd=root, check=True, capture_output=True,
            )
            found = repository_policy.violations(root)
            self.assertTrue(any("docs/design.md" in item for item in found), found)
            self.assertTrue(any("public.md" in item for item in found), found)
            self.assertTrue(any("huge.pdf" in item and "exceeds" in item for item in found), found)
            self.assertFalse(any("ok.pdf" in item for item in found), found)


if __name__ == "__main__":
    unittest.main()

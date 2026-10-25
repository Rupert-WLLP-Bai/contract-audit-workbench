"""The source tree must keep AGENTS.md and keep delivery material out."""
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import zipfile
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
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            subprocess.run(["git", "init"], cwd=root, check=True, capture_output=True)
            shutil.copyfile(ROOT / ".gitignore", root / ".gitignore")
            for relative in samples:
                git = subprocess.run(
                    ["git", "-C", str(root), "check-ignore", "-q", "--no-index", relative],
                    capture_output=True,
                )
                self.assertIn(git.returncode, (0, 1), relative)
                self.assertEqual(repository_policy.is_ignored(root, relative), git.returncode == 0, relative)

    def test_current_tree_has_no_policy_violations(self):
        self.assertEqual(repository_policy.violations(ROOT), [])

    def _write_source_tree(self, root):
        root.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(ROOT / ".gitignore", root / ".gitignore")
        (root / "README.md").write_text("read\n", encoding="utf-8")
        (root / "AGENTS.md").write_text("agents\n", encoding="utf-8")
        (root / "docs").mkdir()
        (root / "docs" / "design.md").write_text("ignored delivery material\n", encoding="utf-8")
        (root / "node_modules").mkdir()
        (root / "node_modules" / "dependency.md").write_text("ignored dependency\n", encoding="utf-8")

    def test_source_archive_runs_matcher_and_tree_policy_without_git_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive_path = Path(tmp) / "source.zip"
            with zipfile.ZipFile(archive_path, "w") as archive:
                for relative in (
                    ".gitignore", "README.md", "AGENTS.md",
                    "scripts/repository_policy.py", "scripts/test_repository_policy.py",
                ):
                    archive.write(ROOT / relative, relative)
                archive.writestr("docs/design.md", "ignored delivery material\n")
            root = Path(tmp) / "source"
            with zipfile.ZipFile(archive_path) as archive:
                archive.extractall(root)
            completed = subprocess.run(
                [sys.executable, "-m", "unittest",
                 "test_repository_policy.RepositoryPolicyTest.test_matcher_agrees_with_git",
                 "test_repository_policy.RepositoryPolicyTest.test_current_tree_has_no_policy_violations"],
                cwd=root / "scripts", capture_output=True, text=True,
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)

    def test_source_tree_without_git_uses_ignore_aware_filesystem_inventory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "source"
            self._write_source_tree(root)
            with mock.patch.dict(os.environ, {"PATH": str(Path(tmp) / "no-git")}):
                self.assertEqual(repository_policy.violations(root), [])

    def test_source_tree_without_git_still_rejects_unignored_delivery_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "source"
            self._write_source_tree(root)
            with (root / ".gitignore").open("a", encoding="utf-8") as ignore:
                ignore.write("\n!/public.md\n")
            (root / "public.md").write_text("unignored delivery material\n", encoding="utf-8")
            fixtures = root / "testdata"
            fixtures.mkdir()
            (fixtures / "ok.pdf").write_bytes(b"%PDF-1.4\n")
            (fixtures / "huge.pdf").write_bytes(b"x" * (repository_policy.FIXTURE_MAX_BYTES + 1))
            with mock.patch.dict(os.environ, {"PATH": str(Path(tmp) / "no-git")}):
                found = repository_policy.violations(root)
            self.assertEqual(len(found), 2, found)
            self.assertIn("markdown is outside the source allowlist: public.md", found)
            self.assertTrue(any("huge.pdf" in item and "exceeds" in item for item in found), found)

    def test_source_tree_does_not_use_parent_repository_inventory(self):
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp)
            subprocess.run(["git", "init"], cwd=parent, check=True, capture_output=True)
            root = parent / "source"
            self._write_source_tree(root)
            subprocess.run(
                ["git", "add", "-f", "source/docs/design.md"],
                cwd=parent, check=True, capture_output=True,
            )
            self.assertEqual(repository_policy.violations(root), [])

    def test_corrupt_own_git_metadata_fails_closed_inside_parent_repository(self):
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp)
            subprocess.run(["git", "init"], cwd=parent, check=True, capture_output=True)
            root = parent / "source"
            self._write_source_tree(root)
            (root / ".git").mkdir()
            found = repository_policy.violations(root)
            self.assertTrue(any("tracked file inventory failed:" in item for item in found), found)

    def test_own_git_metadata_without_git_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "source"
            self._write_source_tree(root)
            subprocess.run(["git", "init"], cwd=root, check=True, capture_output=True)
            with mock.patch.dict(os.environ, {"PATH": str(Path(tmp) / "no-git")}):
                found = repository_policy.violations(root)
            self.assertTrue(any("tracked file inventory failed:" in item for item in found), found)

    def test_gitfile_inventory_keeps_tracking_ignored_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp)
            root = parent / "source"
            self._write_source_tree(root)
            subprocess.run(
                ["git", "init", "--separate-git-dir", str(parent / "metadata")],
                cwd=root, check=True, capture_output=True,
            )
            subprocess.run(
                ["git", "add", "-f", "docs/design.md"],
                cwd=root, check=True, capture_output=True,
            )
            found = repository_policy.violations(root)
            self.assertTrue(any("docs/design.md" in item for item in found), found)
            self.assertFalse(any("tracked file inventory failed:" in item for item in found), found)

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

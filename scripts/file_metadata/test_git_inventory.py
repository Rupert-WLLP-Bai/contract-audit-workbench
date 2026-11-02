import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from git_inventory import (
    GitInventoryError,
    GitRepo,
    compare_manifest_coverage,
    manifest_self_updated,
    new_path_revision,
    revision_for_window,
)
from metadata_core import START, STYLES, insert_header


def git(repo: Path, *args: str, env=None) -> bytes:
    return subprocess.run(["git", "-C", str(repo), *args], check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, env=env).stdout


def commit(repo: Path, message: str, stamp: str) -> str:
    env = dict(os.environ, GIT_AUTHOR_DATE=stamp, GIT_COMMITTER_DATE=stamp)
    git(repo, "add", "-A", env=env)
    git(repo, "commit", "-m", message, env=env)
    return git(repo, "rev-parse", "HEAD").decode().strip()


def commit_index(repo: Path, message: str, stamp: str) -> str:
    env = dict(os.environ, GIT_AUTHOR_DATE=stamp, GIT_COMMITTER_DATE=stamp)
    git(repo, "commit", "-m", message, env=env)
    return git(repo, "rev-parse", "HEAD").decode().strip()


def init_repo(repo: Path) -> None:
    git(repo, "init", "--initial-branch=main")
    git(repo, "config", "user.name", "Metadata Fixture")
    git(repo, "config", "user.email", "metadata-fixture@example.invalid")


class GitInventoryTests(unittest.TestCase):
    def test_tree_and_index_preserve_spaces_unicode_newlines_links_modes_and_bytes(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-tree-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            (repo / "space name.txt").write_bytes(b"space\n")
            (repo / "目录" / "子 文件.txt").parent.mkdir(parents=True)
            (repo / "目录" / "子 文件.txt").write_bytes(b"unicode\n")
            newline_path = os.fsencode(repo) + b"/line\nbreak.txt"
            fd = os.open(newline_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
            os.write(fd, b"newline\n")
            os.close(fd)
            os.symlink("space name.txt", repo / "linked path")
            first = commit(repo, "tracked paths", "2026-01-02T03:04:05Z")
            git(repo, "update-index", "--add", "--cacheinfo", f"160000,{first},submodule")
            env = dict(os.environ, GIT_AUTHOR_DATE="2026-01-03T03:04:05Z", GIT_COMMITTER_DATE="2026-01-03T03:04:05Z")
            git(repo, "commit", "-m", "gitlink", env=env)
            second = git(repo, "rev-parse", "HEAD").decode().strip()

            tool = GitRepo(repo)
            tree = tool.tree_inventory(second)
            index = tool.index_inventory()
            expected = {"space name.txt", "目录/子 文件.txt", "line\nbreak.txt", "linked path", "submodule"}
            self.assertEqual(set(tree), expected)
            self.assertEqual(set(index), expected)
            self.assertEqual(tree["linked path"].mode, "120000")
            self.assertEqual(tree["linked path"].content, b"space name.txt")
            self.assertEqual(tree["submodule"].mode, "160000")
            self.assertIsNone(tree["submodule"].content)
            self.assertEqual(tree["line\nbreak.txt"].content, b"newline\n")
            head, tree_oid = tool.commit_identity(second)
            self.assertEqual(head, second)
            self.assertTrue(tree_oid)
            self.assertEqual(tool.require_baseline_ancestor(first, second), (second, tree_oid))

    def test_raw_diff_reports_rename_delete_add_without_splitting_paths(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-diff-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            old_body = b"alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\n"
            (repo / "old name.txt").write_bytes(old_body)
            (repo / "delete me.txt").write_bytes(b"remove\n")
            before = commit(repo, "initial tree", "2026-02-01T00:00:00Z")
            os.rename(repo / "old name.txt", repo / "renamed\n名字.txt")
            (repo / "renamed\n名字.txt").write_bytes(old_body.replace(b"epsilon", b"EPSILON"))
            (repo / "delete me.txt").unlink()
            (repo / "added path.txt").write_bytes(b"new\n")
            after = commit(repo, "rename edit delete add", "2026-02-02T00:00:00Z")

            changes = GitRepo(repo).diff(before, after)
            renamed = next(change for change in changes if change.status.startswith("R"))
            self.assertEqual((renamed.old_path, renamed.new_path), (b"old name.txt", "renamed\n名字.txt".encode()))
            self.assertTrue(any(change.status == "D" and change.old_path == b"delete me.txt" for change in changes))
            self.assertTrue(any(change.status == "A" and change.new_path == b"added path.txt" for change in changes))

    def test_provenance_follows_verified_rename_and_uses_synthetic_utc_dates(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-history-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            original = b"one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n"
            (repo / "old path.txt").write_bytes(original)
            created_sha = commit(repo, "add source", "2026-02-01T00:30:00+02:00")
            renamed_path = "new\npath 名.txt"
            os.rename(repo / "old path.txt", repo / renamed_path)
            (repo / renamed_path).write_bytes(original.replace(b"four", b"FOUR"))
            updated_sha = commit(repo, "rename with small edit", "2026-02-02T00:30:00+02:00")

            evidence = GitRepo(repo).path_provenance(renamed_path, subject=updated_sha)
            self.assertEqual(evidence.created.value, "2026-01-31")
            self.assertEqual(evidence.created.source["kind"], "rename-history")
            self.assertEqual(evidence.created.source["commit"], created_sha)
            self.assertEqual(evidence.created.source["path"], "old path.txt")
            self.assertEqual(evidence.updated.value, "2026-02-01")
            self.assertEqual(evidence.updated.source["commit"], updated_sha)
            self.assertEqual(evidence.updated.source["path"], renamed_path)
            self.assertEqual(evidence.origin_path, "old path.txt")
            self.assertTrue(evidence.followed_rename)

    def test_fixed_subject_ignores_later_head_edits_rename_and_delete(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-fixed-subject-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            original = b"one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n"
            (repo / "old path.txt").write_bytes(original)
            created_sha = commit(repo, "add source", "2026-02-01T00:00:00Z")
            os.rename(repo / "old path.txt", repo / "snapshot path.txt")
            (repo / "snapshot path.txt").write_bytes(original.replace(b"four", b"FOUR"))
            subject = commit(repo, "snapshot rename", "2026-02-02T00:00:00Z")
            snapshot_bytes = GitRepo(repo).tree_inventory(subject)["snapshot path.txt"].content

            (repo / "snapshot path.txt").write_bytes((repo / "snapshot path.txt").read_bytes().replace(b"FOUR", b"LATER"))
            commit(repo, "later edit", "2026-02-03T00:00:00Z")
            os.rename(repo / "snapshot path.txt", repo / "future path.txt")
            commit(repo, "later rename", "2026-02-04T00:00:00Z")
            (repo / "future path.txt").unlink()
            commit(repo, "later delete", "2026-02-05T00:00:00Z")

            tool = GitRepo(repo)
            self.assertEqual(tool.tree_inventory(subject)["snapshot path.txt"].content, snapshot_bytes)
            evidence = tool.path_provenance("snapshot path.txt", subject=subject)
            self.assertEqual(evidence.created.source["commit"], created_sha)
            self.assertEqual(evidence.updated.source["commit"], subject)
            future_only = tool.path_provenance("future path.txt", subject=subject)
            self.assertEqual(future_only.created.value, "unknown")
            self.assertEqual(future_only.updated.value, "unknown")

    def test_gitlink_oid_change_is_a_real_history_change_without_submodule_checkout(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-gitlink-") as tmp:
            root = Path(tmp)
            submodule = root / "submodule-source"
            submodule.mkdir()
            init_repo(submodule)
            (submodule / "payload.txt").write_bytes(b"first pointer target\n")
            first_oid = commit(submodule, "submodule first", "2026-02-01T00:00:00Z")
            (submodule / "payload.txt").write_bytes(b"second pointer target\n")
            second_oid = commit(submodule, "submodule second", "2026-02-02T00:00:00Z")

            parent = root / "parent"
            parent.mkdir()
            init_repo(parent)
            git(parent, "update-index", "--add", "--cacheinfo", f"160000,{first_oid},submodule")
            introduced = commit_index(parent, "add submodule pointer", "2026-02-03T00:00:00Z")
            git(parent, "update-index", "--cacheinfo", f"160000,{second_oid},submodule")
            pointer_update = commit_index(parent, "update submodule pointer", "2026-02-04T00:00:00Z")

            provenance = GitRepo(parent).path_provenance("submodule", subject=pointer_update)
            self.assertEqual(provenance.created.source["commit"], introduced)
            self.assertEqual(provenance.updated.source["commit"], pointer_update)
            self.assertEqual(provenance.created.source["path"], "submodule")
            self.assertEqual(provenance.updated.source["path"], "submodule")

    def test_metadata_revision_window_and_missing_prior_provenance_fail_closed(self):
        record = {
            "purpose": "Fixture purpose describing a test contract.",
            "authors": [{"name": "Bai Junhao", "email": "baijunhao@cq.chinamobile.com", "contact": "+86 13617696321"}],
            "created": {"value": "2026-01-01"}, "updated": {"value": "2026-01-02"},
            "revision": "r1", "copyright": [{"notice": "See LICENSE"}],
        }
        with tempfile.TemporaryDirectory(prefix="file-meta-revision-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            target = repo / "script.sh"
            original = b"#!/bin/sh\necho ok\n"
            target.write_bytes(original)
            first = commit(repo, "baseline body", "2026-03-01T00:00:00Z")
            tool = GitRepo(repo)
            original_entry = tool.tree_inventory(first)["script.sh"]

            target.write_bytes(insert_header(original, record, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100644"))
            header_commit = commit(repo, "metadata only", "2026-03-02T00:00:00Z")
            header_entry = tool.tree_inventory(header_commit)["script.sh"]
            self.assertEqual(revision_for_window("r1", original_entry, header_entry, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell"), "r1")

            target.write_bytes(target.read_bytes().replace(b"echo ok", b"echo changed"))
            body_commit = commit(repo, "body update", "2026-03-03T00:00:00Z")
            body_entry = tool.tree_inventory(body_commit)["script.sh"]
            self.assertEqual(revision_for_window("r1", header_entry, body_entry, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell"), "r2")

            target.chmod(0o755)
            mode_commit = commit(repo, "mode update", "2026-03-04T00:00:00Z")
            mode_entry = tool.tree_inventory(mode_commit)["script.sh"]
            self.assertEqual(revision_for_window("r2", body_entry, mode_entry, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell"), "r3")
            (repo / "copy.sh").write_bytes(target.read_bytes())
            copy_commit = commit(repo, "new path", "2026-03-05T00:00:00Z")
            copy_entry = tool.tree_inventory(copy_commit)["copy.sh"]
            with self.assertRaises(GitInventoryError):
                revision_for_window("r3", mode_entry, copy_entry, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell")
            self.assertEqual(new_path_revision(), "r1")
            with self.assertRaises(GitInventoryError):
                revision_for_window("r1", None, copy_entry, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell")

    def test_uncommitted_path_dates_are_unknown_and_self_record_is_exact_path_only(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-unknown-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            (repo / "base.txt").write_bytes(b"base\n")
            base = commit(repo, "base", "2026-02-02T00:00:00Z")
            (repo / "pending.txt").write_bytes(b"pending\n")
            git(repo, "add", "pending.txt")
            item = GitRepo(repo).path_provenance("pending.txt", subject=base)
            self.assertEqual(item.created.value, "unknown")
            self.assertEqual(item.updated.value, "unknown")
            self.assertEqual(item.created.source["kind"], "unknown")
        self_record = manifest_self_updated()
        self.assertEqual(self_record.value, "unknown")
        self.assertEqual(self_record.source["kind"], "unknown")
        with self.assertRaises(GitInventoryError):
            manifest_self_updated("docs/engineering/file-metadata.md")

    def test_mode_only_commit_does_not_invent_a_body_updated_date(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-mode-history-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            target = repo / "script.sh"
            target.write_bytes(b"#!/bin/sh\necho stable\n")
            first = commit(repo, "add script", "2026-02-01T00:00:00Z")
            target.chmod(0o755)
            commit(repo, "make executable", "2026-02-02T00:00:00Z")
            record = {
                "purpose": "Fixture purpose describing a test contract.",
                "authors": [{"name": "Bai Junhao", "email": "baijunhao@cq.chinamobile.com", "contact": "+86 13617696321"}],
                "created": {"value": "2026-02-01"}, "updated": {"value": "2026-02-02"},
                "revision": "r1", "copyright": [{"notice": "See LICENSE"}],
            }
            target.write_bytes(insert_header(target.read_bytes(), record, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100755"))
            header_commit = commit(repo, "add metadata mirror only", "2026-02-03T00:00:00Z")
            evidence = GitRepo(repo).path_provenance("script.sh", subject=header_commit, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell")
            self.assertEqual(evidence.created.source["commit"], first)
            self.assertEqual(evidence.updated.source["commit"], first)

    def test_ambiguous_older_body_preserves_created_and_never_invents_updated(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-ambiguous-history-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            path = repo / "fixture.py"
            path.write_bytes(b"value = 1\n")
            created = commit(repo, "initial source", "2026-02-01T00:00:00Z")
            path.write_bytes(b"# " + START + b"\ninvalid fixture body\n")
            commit(repo, "ambiguous historical source", "2026-02-02T00:00:00Z")
            path.write_bytes(b"value = 2\n")
            ambiguous = commit(repo, "restore ordinary source", "2026-02-03T00:00:00Z")
            g = GitRepo(repo)
            uncertain = g.path_provenance("fixture.py", subject=ambiguous, style=STYLES["hash"],
                                          handling="header-and-manifest", file_kind="python")
            self.assertEqual(uncertain.created.source["commit"], created)
            self.assertEqual(uncertain.updated.value, "unknown")
            self.assertEqual(uncertain.updated.source["kind"], "unknown")
            self.assertIn(ambiguous, uncertain.updated.source["reason"])
            self.assertIn("ambiguous", uncertain.updated.source["reason"])
            path.write_bytes(b"value = 3\n")
            latest = commit(repo, "later unambiguous body change", "2026-02-04T00:00:00Z")
            known = g.path_provenance("fixture.py", subject=latest, style=STYLES["hash"],
                                      handling="header-and-manifest", file_kind="python")
            self.assertEqual(known.created.source["commit"], created)
            self.assertEqual(known.updated.source["commit"], latest)
            self.assertEqual(known.updated.value, "2026-02-04")

    def test_non_utf8_path_fails_with_reversible_hex_diagnostic(self):
        with tempfile.TemporaryDirectory(prefix="file-meta-nonutf-") as tmp:
            repo = Path(tmp)
            init_repo(repo)
            raw_path = os.fsencode(repo) + b"/not-utf8-\xff"
            fd = os.open(raw_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
            os.write(fd, b"opaque\n")
            os.close(fd)
            commit(repo, "non UTF-8 path", "2026-02-03T00:00:00Z")
            with self.assertRaisesRegex(GitInventoryError, r"bytes\[6e6f742d757466382dff\]"):
                GitRepo(repo).tree_inventory("HEAD")

    def test_manifest_coverage_uses_exact_sets(self):
        missing, stale = compare_manifest_coverage(["a path", "b\npath", "目录/x"], ["a path", "old path"])
        self.assertEqual(missing, ["b\npath", "目录/x"])
        self.assertEqual(stale, ["old path"])
        with self.assertRaises(GitInventoryError):
            compare_manifest_coverage([], [])


if __name__ == "__main__":
    unittest.main()

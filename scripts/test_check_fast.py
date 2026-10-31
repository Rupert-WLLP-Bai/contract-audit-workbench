"""Selection and failure-boundary tests for the shared fast-check CLI."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import check_fast

SCRIPT = Path(__file__).resolve().with_name("check_fast.py")


def write(root: Path, relative: str, text: str = "") -> Path:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def git(root: Path, *arguments: str, check: bool = True):
    env = dict(os.environ)
    for name in ("GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"):
        env.pop(name, None)
    env.update({
        "GIT_AUTHOR_NAME": "Fast Check Test",
        "GIT_AUTHOR_EMAIL": "fast-check@example.test",
        "GIT_COMMITTER_NAME": "Fast Check Test",
        "GIT_COMMITTER_EMAIL": "fast-check@example.test",
    })
    return subprocess.run(["git", "-C", str(root), *arguments],
                          check=check, capture_output=True, text=True, env=env)


def commit(root: Path, message: str) -> str:
    git(root, "add", "-A")
    git(root, "-c", "commit.gpgsign=false", "commit", "--no-verify", "-m", message)
    return git(root, "rev-parse", "HEAD").stdout.strip()


def script_env(**overrides):
    env = dict(os.environ)
    for name in ("CAW_FAST_BASE", "CAW_FAST_OUT", "CAW_DELIVERY_DIR",
                 "GITHUB_ACTIONS", "TEST_DATABASE_URL", "MIGRATOR_DATABASE_URL",
                 "MODEL_DATABASE_URL", "GIT_DIR", "GIT_INDEX_FILE",
                 "GIT_WORK_TREE"):
        env.pop(name, None)
    env.update(overrides)
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    return env


def run_script(*arguments: str, env=None, cwd=None):
    return subprocess.run([sys.executable, str(SCRIPT), *arguments],
                          capture_output=True, text=True, cwd=cwd,
                          env=env or script_env())


class DiffParsingTests(unittest.TestCase):
    def test_rename_spaces_and_deletion_are_preserved(self):
        changes = check_fast.parse_name_status_z(
            b"M\0a b.go\0D\0gone.go\0R100\0old path.go\0new path.go\0"
            b"A\0new.py\0")
        self.assertEqual(len(changes), 4)
        self.assertEqual(changes[1].path, "gone.go")
        self.assertTrue(changes[1].deleted)
        self.assertEqual(changes[2].old_path, "old path.go")
        self.assertEqual(changes[2].path, "new path.go")
        self.assertEqual(changes[3].affected_paths(), ("new.py",))

    def test_incomplete_record_is_rejected(self):
        with self.assertRaises(check_fast.FastError):
            check_fast.parse_name_status_z(b"R100\0only-old.go\0")


class PlanSelectionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        write(self.root, "go.mod", "module example.test/project\n\ngo 1.25\n")
        write(self.root, "internal/shared/shared.go",
              'package shared\n\nvar Value = 1\n')
        write(self.root, "cmd/app/main.go",
              'package main\n\nimport "example.test/project/internal/shared"\n\n'
              'func main() { _ = shared.Value }\n')
        write(self.root, "internal/legacy/old.go", "package legacy\n")
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        write(self.root, "internal/decision/decision.go",
              "package decision\n\nvar Value = 1\n")
        write(self.root, "web/src/app.tsx", "export const app = true\n")
        write(self.root, "web/public/logo.png", "")
        write(self.root, "web/vite.config.ts", "export default {}\n")
        write(self.root, "scripts/tool.py", "def run():\n    return 1\n")
        write(self.root, "scripts/test_tool.py",
              "import unittest\nfrom tool import run\n\n"
              "class ToolTest(unittest.TestCase):\n"
              "    def test_run(self):\n        self.assertEqual(run(), 1)\n")
        write(self.root, "scripts/unused.py", "VALUE = 2\n")
        write(self.root, "scripts/two.py", "VALUE = 3\n")
        write(self.root, "scripts/test_extra.py", "import unittest\nimport two\n")
        write(self.root, "README.md", "# docs\n")

    def plan(self, *changes):
        return check_fast.build_plan(self.root, changes)

    def test_go_change_selects_format_compile_and_unit(self):
        plan = self.plan(check_fast.Change("M", "internal/decision/decision.go"))
        self.assertTrue(plan.go)
        self.assertEqual(plan.go_format_files, ["internal/decision/decision.go"])
        self.assertIn("./internal/decision", plan.go_packages)
        self.assertIn("./internal/decision", plan.go_unit_packages)
        self.assertFalse(plan.go_build_all)

    def test_shared_package_change_expands_compile(self):
        plan = self.plan(check_fast.Change("M", "internal/shared/shared.go"))
        self.assertTrue(plan.go)
        self.assertTrue(plan.go_build_all)
        self.assertIn("shared-package", plan.reasons)

    def test_dependency_change_compiles_all_without_tests(self):
        plan = self.plan(check_fast.Change("M", "go.mod"))
        self.assertTrue(plan.go)
        self.assertTrue(plan.go_build_all)
        self.assertEqual(plan.go_unit_packages, set())

    def test_web_source_requires_typecheck_but_not_build(self):
        plan = self.plan(check_fast.Change("M", "web/src/app.tsx"))
        self.assertTrue(plan.web)
        self.assertFalse(plan.web_build)

    def test_web_build_config_requires_build(self):
        plan = self.plan(check_fast.Change("M", "web/vite.config.ts"))
        self.assertTrue(plan.web)
        self.assertTrue(plan.web_build)

    def test_docs_and_public_assets_are_not_compiled(self):
        plan = self.plan(check_fast.Change("M", "README.md"),
                         check_fast.Change("M", "web/public/logo.png"))
        self.assertFalse(plan.go or plan.web or plan.python or plan.database)

    def test_python_module_selects_only_related_tests(self):
        plan = self.plan(check_fast.Change("M", "scripts/tool.py"))
        self.assertTrue(plan.python)
        self.assertEqual(plan.python_tests, ["scripts/test_tool.py"])
        self.assertEqual(plan.python_syntax_files, ["scripts/tool.py"])

    def test_python_change_without_test_has_no_test_check(self):
        plan = self.plan(check_fast.Change("M", "scripts/unused.py"))
        self.assertTrue(plan.python)
        self.assertEqual(plan.python_tests, [])

    def test_deleted_go_file_keeps_format_and_compile_scope(self):
        plan = self.plan(check_fast.Change("D", "internal/leaf/leaf.go"))
        self.assertTrue(plan.go)
        self.assertEqual(plan.go_format_files, [])
        self.assertIn("./internal/leaf", plan.go_packages)

    def test_rename_keeps_new_path_and_old_package(self):
        plan = self.plan(check_fast.Change(
            "R", "internal/shared/shared.go", "internal/legacy/old.go"))
        self.assertTrue(plan.go)
        self.assertEqual(plan.go_format_files, ["internal/shared/shared.go"])
        self.assertIn("./internal/shared", plan.go_packages)
        self.assertIn("./internal/legacy", plan.go_packages)

    def test_deleted_package_builds_everything(self):
        plan = self.plan(check_fast.Change("D", "internal/removed/removed.go"))
        self.assertTrue(plan.go)
        self.assertTrue(plan.go_build_all)

    def test_session_change_selects_small_database_group(self):
        plan = self.plan(check_fast.Change(
            "M", "internal/store/session_lifecycle.go"))
        self.assertTrue(plan.database)
        self.assertEqual(len(plan.database_tests), 1)
        target = plan.database_tests[0]
        self.assertEqual(target.package, "./internal/store")
        self.assertEqual(target.tests, tuple(sorted(check_fast.SESSION_TESTS)))
        self.assertNotIn("TestLoginOriginalAndRetryStayInPostgres", target.tests)

    def test_budget_change_requires_model_role(self):
        plan = self.plan(check_fast.Change("M", "internal/store/model_budget.go"))
        self.assertTrue(plan.database)
        self.assertTrue(plan.database_tests[0].needs_model)
        self.assertEqual(plan.database_tests[0].tests,
                         tuple(sorted(check_fast.BUDGET_TESTS)))

    def test_python_migration_selects_database_and_go_toolchain(self):
        plan = self.plan(check_fast.Change("M", "scripts/migrate_schema.py"))
        self.assertTrue(plan.python)
        self.assertTrue(plan.database)
        self.assertTrue(plan.go)
        self.assertFalse(plan.go_build_all)

    def test_schema_change_requires_real_database_migration_check(self):
        plan = self.plan(check_fast.Change("M", "internal/store/schema.go"))
        self.assertTrue(plan.database)
        self.assertIn("migration", plan.database_tests[0].reasons)
        self.assertIn("TestEvaluationProvenanceMigrationPreservesHistoricalRows",
                      plan.database_tests[0].tests)

    def test_object_freeze_change_requires_persisted_snapshot_check(self):
        plan = self.plan(check_fast.Change("M", "internal/store/service_object_context.go"))
        self.assertTrue(plan.database)
        self.assertIn("frozen-evidence", plan.database_tests[0].reasons)
        self.assertIn("TestDurableReviewFreezesServiceObjectContextAndRejectsChangedRetryAfterReopen",
                      plan.database_tests[0].tests)



class CliBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "repo"
        self.root.mkdir()
        git(self.root, "init", "-b", "main")
        git(self.root, "config", "user.name", "Fast Check Test")
        git(self.root, "config", "user.email", "fast-check@example.test")
        write(self.root, "go.mod", "module example.test/project\n\ngo 1.25\n")
        write(self.root, "README.md", "# repo\n")
        write(self.root, "internal/decision/decision.go",
              "package decision\n\nvar Value = 1\n")
        write(self.root, "docs old/keep.txt", "same bytes\n")
        write(self.root, "internal/old/old.py", "OLD = 1\n")
        self.base = commit(self.root, "base")
        write(self.root, "internal/decision/decision.go",
              "package decision\n\nvar Value = 2\n")
        commit(self.root, "change decision")

    def prepare_working_tree(self):
        git(self.root, "rm", "internal/old/old.py")
        git(self.root, "mv", "docs old/keep.txt", "docs old/renamed.txt")
        write(self.root, "web/src/app.tsx", "export const app = true\n")
        write(self.root, "scripts/tool.py", "def run():\n    return 1\n")
        write(self.root, "scripts/test_tool.py",
              "import unittest\nfrom tool import run\n\n"
              "class ToolTest(unittest.TestCase):\n"
              "    def test_run(self):\n        self.assertEqual(run(), 1)\n")

    def test_plan_is_one_line_and_github_output_is_appended(self):
        self.prepare_working_tree()
        output = Path(self.temporary.name) / "github-output"
        output.write_text("keep=1\n", encoding="utf-8")
        result = run_script("--root", str(self.root), "--base", self.base,
                            "--plan", "--github-output", str(output),
                            env=script_env())
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = result.stdout.strip().splitlines()
        self.assertEqual(len(lines), 1)
        plan = json.loads(lines[0])
        self.assertTrue(plan["go"])
        self.assertTrue(plan["web"])
        self.assertTrue(plan["python"])
        self.assertFalse(plan["database"])
        self.assertIn("web/src/app.tsx", plan["changed_paths"])
        self.assertIn("internal/old/old.py", plan["changed_paths"])
        self.assertIn("docs old/renamed.txt", plan["changed_paths"])
        self.assertEqual(plan["python_tests"], ["scripts/test_tool.py"])
        appended = output.read_text(encoding="utf-8").splitlines()
        self.assertEqual(appended[0], "keep=1")
        self.assertEqual(appended[1:], ["go=true", "web=true", "python=true",
                                        "database=false", "poppler=false",
                                        "legal_assets=false"])

    def test_plan_uses_caw_fast_base_when_base_flag_is_omitted(self):
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        result = run_script("--root", str(self.root), "--plan",
                            env=script_env(CAW_FAST_BASE=self.base))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["go"])

    def test_missing_base_ref_is_non_zero(self):
        result = run_script("--root", str(self.root), "--base", "not-a-ref",
                            "--plan", env=script_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("base ref", result.stderr)

    def fake_binaries(self):
        directory = Path(self.temporary.name) / "bin"
        directory.mkdir()
        marker = Path(self.temporary.name) / "executed"
        for name in ("go", "gofmt", "bun"):
            path = directory / name
            path.write_text("#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$FAKE_MARKER\"\n"
                            "exit 0\n", encoding="utf-8")
            path.chmod(0o755)
        return directory, marker

    def test_missing_database_environment_blocks_before_checks(self):
        write(self.root, "internal/store/session_lifecycle.go",
              "package store\n")
        binaries, marker = self.fake_binaries()
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                         FAKE_MARKER=str(marker))
        result = run_script("--root", str(self.root), "--base", self.base,
                            "--out", str(out), env=env)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("database plan requires", result.stderr)
        self.assertFalse(marker.exists())
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(report["passed"])
        self.assertEqual(report["checks"][0]["status"], "NOT_RUN")

    def test_output_inside_repository_is_rejected(self):
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        result = run_script("--root", str(self.root), "--base", self.base,
                            "--out", str(self.root / "fast-out"),
                            env=script_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("inside the repository", result.stderr)
        self.assertFalse((self.root / "fast-out").exists())


if __name__ == "__main__":
    unittest.main()

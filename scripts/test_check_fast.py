# File metadata (manifest mirror):
# Purpose: Regression checks for test_rename_spaces_and_deletion_are_preserved, test_incomplete_record_is_rejected, test_go_change_selects_format_compile_and_unit, test_shared_package_change_expands_compile, test_shared_package_without_tests_widens_unit_scope; they assert the deployment, parser, or repository-policy contract that the corresponding implementation must preserve.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-31
# Updated: 2026-11-10
# Revision: r6
# Copyright: See LICENSE
# End file metadata.

"""Selection and failure-boundary tests for the shared fast-check CLI."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

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
    env.update(
        {
            "GIT_AUTHOR_NAME": "Fast Check Test",
            "GIT_AUTHOR_EMAIL": "fast-check@example.test",
            "GIT_COMMITTER_NAME": "Fast Check Test",
            "GIT_COMMITTER_EMAIL": "fast-check@example.test",
        }
    )
    return subprocess.run(
        ["git", "-C", str(root), *arguments], check=check, capture_output=True, text=True, env=env
    )


def commit(root: Path, message: str) -> str:
    git(root, "add", "-A")
    git(root, "-c", "commit.gpgsign=false", "commit", "--no-verify", "-m", message)
    return git(root, "rev-parse", "HEAD").stdout.strip()


def script_env(**overrides):
    env = dict(os.environ)
    for name in (
        "CAW_FAST_BASE",
        "CAW_FAST_OUT",
        "CAW_DELIVERY_DIR",
        "CAW_METADATA_EXTERNAL_GUARD",
        "GITHUB_ACTIONS",
        "TEST_DATABASE_URL",
        "MIGRATOR_DATABASE_URL",
        "MODEL_DATABASE_URL",
        "GIT_DIR",
        "GIT_INDEX_FILE",
        "GIT_WORK_TREE",
    ):
        env.pop(name, None)
    env.update(overrides)
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    return env


def run_script(*arguments: str, env=None, cwd=None):
    return subprocess.run(
        [sys.executable, str(SCRIPT), *arguments],
        capture_output=True,
        text=True,
        cwd=cwd,
        env=env or script_env(),
    )


class DiffParsingTests(unittest.TestCase):
    def test_rename_spaces_and_deletion_are_preserved(self):
        changes = check_fast.parse_name_status_z(
            b"M\0a b.go\0D\0gone.go\0R100\0old path.go\0new path.go\0A\0new.py\0"
        )
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
        write(self.root, "internal/shared/shared.go", "package shared\n\nvar Value = 1\n")
        write(
            self.root,
            "cmd/app/main.go",
            'package main\n\nimport "example.test/project/internal/shared"\n\n'
            "func main() { _ = shared.Value }\n",
        )
        write(self.root, "internal/legacy/old.go", "package legacy\n")
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        write(self.root, "internal/decision/decision.go", "package decision\n\nvar Value = 1\n")
        write(
            self.root,
            "internal/decision/decision_test.go",
            """package decision

import "testing"

func TestValue(t *testing.T) { if Value != 1 { t.Fatal(Value) } }
              """,
        )
        write(self.root, "web/src/app.tsx", "export const app = true\n")
        write(self.root, "web/src/api/parse.ts", "export const parse = true\n")
        write(self.root, "web/public/logo.png", "")
        write(self.root, "web/vite.config.ts", "export default {}\n")
        write(self.root, "scripts/tool.py", "def run():\n    return 1\n")
        write(
            self.root,
            "scripts/test_tool.py",
            "import unittest\nfrom tool import run\n\n"
            "class ToolTest(unittest.TestCase):\n"
            "    def test_run(self):\n        self.assertEqual(run(), 1)\n",
        )
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

    def test_shared_package_without_tests_widens_unit_scope(self):
        plan = self.plan(check_fast.Change("M", "internal/shared/shared.go"))
        self.assertEqual(plan.go_unit_packages, {"./internal/decision"})
        self.assertIn("unit-scope-widened", plan.reasons)

    def test_dependency_change_widens_unit_scope_to_safe_packages(self):
        # Issue #53: a go.mod change has no affected packages, so the unit
        # stage widens to the module's database-safe packages.
        plan = self.plan(check_fast.Change("M", "go.mod"))
        self.assertTrue(plan.go)
        self.assertTrue(plan.go_build_all)
        self.assertEqual(plan.go_unit_packages, {"./internal/decision"})
        self.assertIn("unit-scope-widened", plan.reasons)

    def test_web_source_requires_typecheck_but_not_build(self):
        plan = self.plan(check_fast.Change("M", "web/src/app.tsx"))
        self.assertTrue(plan.web)
        self.assertFalse(plan.web_build)
        self.assertEqual(plan.web_eslint_files, ["web/src/app.tsx"])
        self.assertEqual(plan.web_prettier_files, ["web/src/app.tsx"])

    def test_generated_and_fixture_web_files_are_not_formatted(self):
        generated = self.plan(check_fast.Change("M", "web/src/api/wire.generated.ts"))
        fixture = self.plan(check_fast.Change("M", "web/e2e/fixtures/sample.ts"))
        self.assertEqual(generated.web_prettier_files, [])
        self.assertEqual(generated.web_eslint_files, [])
        self.assertEqual(fixture.web_prettier_files, [])
        self.assertEqual(fixture.web_eslint_files, [])

    def test_deleted_or_renamed_old_web_paths_are_not_passed_to_tools(self):
        deleted = self.plan(check_fast.Change("D", "web/src/removed.ts"))
        renamed = self.plan(check_fast.Change("R", "web/src/new name.tsx", "web/src/old name.tsx"))
        self.assertEqual(deleted.web_eslint_files, [])
        self.assertEqual(deleted.web_prettier_files, [])
        self.assertEqual(renamed.web_eslint_files, ["web/src/new name.tsx"])
        self.assertEqual(renamed.web_prettier_files, ["web/src/new name.tsx"])

    def test_web_build_config_requires_build(self):
        plan = self.plan(check_fast.Change("M", "web/vite.config.ts"))
        self.assertTrue(plan.web)
        self.assertTrue(plan.web_build)

    def test_public_assets_require_web_build_without_go(self):
        plan = self.plan(
            check_fast.Change("M", "README.md"), check_fast.Change("M", "web/public/logo.png")
        )
        self.assertFalse(plan.go or plan.python or plan.database)
        self.assertTrue(plan.web)
        self.assertTrue(plan.web_build)

    def test_python_module_selects_only_related_tests(self):
        plan = self.plan(check_fast.Change("M", "scripts/tool.py"))
        self.assertTrue(plan.python)
        self.assertEqual(plan.python_tests, ["scripts/test_tool.py"])
        self.assertEqual(plan.python_syntax_files, ["scripts/tool.py"])
        self.assertEqual(plan.python_ruff_files, ["scripts/tool.py"])

    def test_python_change_without_test_has_no_test_check(self):
        plan = self.plan(check_fast.Change("M", "scripts/unused.py"))
        self.assertTrue(plan.python)
        self.assertEqual(plan.python_tests, [])
        self.assertEqual(plan.python_ruff_files, ["scripts/unused.py"])

    def test_ruff_config_change_loads_settings_without_scanning_python_files(self):
        plan = self.plan(check_fast.Change("M", "ruff.toml"))
        self.assertTrue(plan.python)
        self.assertEqual(plan.python_syntax_files, [])
        self.assertEqual(plan.python_ruff_files, [])
        self.assertTrue(plan.python_ruff_config)
        with patch.object(check_fast, "_resolve_binary", return_value="/fixture/bin/ruff"):
            checks = check_fast._build_checks(
                plan, self.root, sys.executable, self.root.parent / "check-output"
            )
        self.assertEqual([check.name for check in checks], ["python-ruff-settings"])
        self.assertEqual(
            checks[0].command,
            ("/fixture/bin/ruff", "check", "--show-settings", "scripts/check_fast.py"),
        )

    def test_web_tool_config_changes_keep_file_scope_incremental(self):
        eslint = self.plan(check_fast.Change("M", "web/eslint.config.mjs"))
        prettier = self.plan(check_fast.Change("M", "web/.prettierrc.json"))
        prettier_ignore = self.plan(check_fast.Change("M", "web/.prettierignore"))
        package = self.plan(check_fast.Change("M", "web/package.json"))
        lockfile = self.plan(check_fast.Change("M", "web/bun.lock"))

        self.assertEqual(eslint.web_eslint_files, ["web/eslint.config.mjs"])
        self.assertEqual(eslint.web_prettier_files, ["web/eslint.config.mjs"])
        self.assertEqual(prettier.web_eslint_files, [])
        self.assertEqual(prettier.web_prettier_files, ["web/.prettierrc.json"])
        self.assertTrue(prettier_ignore.web_prettier_ignore_config)
        self.assertEqual(prettier_ignore.web_prettier_files, [])
        self.assertEqual(package.web_eslint_files, [])
        self.assertEqual(package.web_prettier_files, ["web/package.json"])
        self.assertEqual(lockfile.web_eslint_files, [])
        self.assertEqual(lockfile.web_prettier_files, [])

        with patch.object(check_fast, "_resolve_binary", return_value="/fixture/bin/bun"):
            checks = check_fast._build_checks(
                prettier_ignore, self.root, sys.executable, self.root.parent / "check-output"
            )
        self.assertEqual(
            [check.name for check in checks],
            ["web-prettier-ignore-settings", "web-typecheck"],
        )
        self.assertEqual(
            checks[0].command,
            ("/fixture/bin/bun", "run", "prettier", "--", "--file-info", "src/api/parse.ts"),
        )

    def test_docs_only_selects_docs_build_without_application_scopes(self):
        plan = self.plan(
            check_fast.Change("M", "docs/index.md"),
            check_fast.Change("M", "docs/glossary/concepts.json"),
        )
        self.assertTrue(plan.docs_build)
        self.assertFalse(plan.go or plan.web or plan.python or plan.database)
        self.assertFalse(plan.poppler or plan.legal_assets)

    def test_docs_gate_sources_select_docs_build(self):
        for path in (
            "docs-site/package.json",
            "docs-site/bun.lock",
            "scripts/check_docs.py",
            "scripts/check_glossary.py",
            "scripts/check_fast.py",
            ".github/workflows/pr-fast.yml",
        ):
            with self.subTest(path=path):
                self.assertTrue(self.plan(check_fast.Change("M", path)).docs_build)

    def test_web_only_does_not_select_docs_build(self):
        plan = self.plan(check_fast.Change("M", "web/src/app.tsx"))
        self.assertTrue(plan.web)
        self.assertFalse(plan.docs_build)
        self.assertFalse(plan.ui_terminology)

    def test_manual_findings_and_glossary_select_only_bounded_term_guard(self):
        for path in (
            "web/src/features/review/manual-findings/manual-findings-panel.tsx",
            "docs/glossary/concepts.json",
            "scripts/ui-terminology/policy.json",
            "scripts/check_ui_terminology.py",
            "scripts/test_ui_terminology.py",
        ):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertTrue(plan.ui_terminology)
        ui_only = self.plan(
            check_fast.Change(
                "M", "web/src/features/review/manual-findings/manual-findings-panel.tsx"
            )
        )
        self.assertTrue(ui_only.web)
        self.assertFalse(ui_only.docs_build)
        with patch.object(check_fast, "_resolve_binary", return_value="/fixture/bin/bun"):
            checks = check_fast._build_checks(
                ui_only, self.root, sys.executable, self.root.parent / "check-output"
            )
        self.assertEqual(
            [check.name for check in checks],
            ["ui-terminology", "web-eslint", "web-prettier", "web-typecheck"],
        )
        self.assertEqual(
            checks[1].command[-1], "src/features/review/manual-findings/manual-findings-panel.tsx"
        )
        self.assertEqual(
            checks[2].command[-1], "src/features/review/manual-findings/manual-findings-panel.tsx"
        )

    def test_docs_rename_deletion_and_space_paths_keep_docs_scope(self):
        renamed = self.plan(
            check_fast.Change("R", "docs-site/handbook config.js", "docs/old handbook.md")
        )
        deleted = self.plan(check_fast.Change("D", "docs/removed handbook.md"))
        self.assertTrue(renamed.docs_build)
        self.assertTrue(deleted.docs_build)

    def test_docs_dependency_check_requires_frozen_lockfile(self):
        package_dir = self.root / "docs-site"
        package_dir.mkdir()
        bun = self.root / "fake-bun"
        bun.write_text(
            "#!/bin/sh\nif [ ! -f bun.lock ]; then "
            "echo 'missing docs lockfile' >&2; exit 2; fi\nexit 0\n",
            encoding="utf-8",
        )
        bun.chmod(0o755)
        check = check_fast.FastCheck(
            "docs-dependencies",
            (str(bun), "install", "--frozen-lockfile"),
            cwd=package_dir,
            env=dict(os.environ),
            timeout=10,
        )
        result = check_fast.execute_check(check, self.root / "logs", self.root)
        self.assertEqual(result.status, "FAIL")
        self.assertIn("missing docs lockfile", Path(result.log).read_text(encoding="utf-8"))

    def test_architecture_only_changes_select_guard_without_application_go(self):
        plan = self.plan(check_fast.Change("M", "scripts/architecture/architecture.json"))
        self.assertTrue(plan.architecture)
        self.assertFalse(plan.go or plan.database or plan.web or plan.docs_build)
        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "check-output"
        )
        self.assertEqual([check.name for check in checks], ["architecture-dependencies"])
        self.assertIn("--out", checks[0].command)
        self.assertEqual(
            checks[0].command[-1],
            str((self.root.parent / "check-output" / "architecture-check.json").resolve()),
        )

    def test_architecture_trigger_paths_and_rename_deletion(self):
        for path in (
            "scripts/architecture/check_architecture.py",
            "scripts/architecture/architecture.schema.json",
            "scripts/architecture/fixtures/allowed.json",
            "internal/modelgateway/jev.go",
            "internal/agentflow/advisory.go",
            "internal/advisorycontract/contract.go",
            "go.mod",
            "go.sum",
            "go.work",
            "go.work.sum",
            "go.env",
            "scripts/check_fast.py",
            ".github/workflows/pr-fast.yml",
        ):
            with self.subTest(path=path):
                self.assertTrue(self.plan(check_fast.Change("M", path)).architecture)
        package = self.plan(check_fast.Change("M", "internal/contractmoney/decimal.go"))
        self.assertTrue(package.architecture)
        self.assertTrue(package.go)
        moved = self.plan(
            check_fast.Change(
                "R", "internal/other/new file.go", "internal/contractmoney/old file.go"
            )
        )
        removed = self.plan(check_fast.Change("D", "internal/contractmoney/old file.go"))
        self.assertTrue(moved.architecture)
        self.assertTrue(removed.architecture)

    def test_every_internal_go_package_triggers_architecture(self):
        for path in ("internal/coreauthority/transport.go", "internal/newbusiness/new.go"):
            with self.subTest(path=path):
                self.assertTrue(self.plan(check_fast.Change("M", path)).architecture)

    def test_wire_python_test_prepares_the_typescript_consumers(self):
        with patch.object(
            check_fast, "python_tests_for", return_value=["scripts/test_generate_openapi_types.py"]
        ):
            plan = self.plan(check_fast.Change("M", "scripts/generate_openapi_types.py"))
        self.assertTrue(plan.web)

    def test_deleted_go_file_keeps_format_and_compile_scope(self):
        plan = self.plan(check_fast.Change("D", "internal/leaf/leaf.go"))
        self.assertTrue(plan.go)
        self.assertEqual(plan.go_format_files, [])
        self.assertIn("./internal/leaf", plan.go_packages)

    def test_rename_keeps_new_path_and_old_package(self):
        plan = self.plan(
            check_fast.Change("R", "internal/shared/shared.go", "internal/legacy/old.go")
        )
        self.assertTrue(plan.go)
        self.assertEqual(plan.go_format_files, ["internal/shared/shared.go"])
        self.assertIn("./internal/shared", plan.go_packages)
        self.assertIn("./internal/legacy", plan.go_packages)

    def test_deleted_package_builds_everything(self):
        plan = self.plan(check_fast.Change("D", "internal/removed/removed.go"))
        self.assertTrue(plan.go)
        self.assertTrue(plan.go_build_all)

    def test_session_change_selects_small_database_group(self):
        plan = self.plan(check_fast.Change("M", "internal/store/session_lifecycle.go"))
        self.assertTrue(plan.database)
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(set(targets), {"./internal/store", "./internal/httpapi"})
        target = targets["./internal/store"]
        self.assertEqual(target.tests, tuple(sorted(check_fast.SESSION_TESTS)))
        self.assertNotIn("TestLoginOriginalAndRetryStayInPostgres", target.tests)
        self.assertEqual(
            set(targets["./internal/httpapi"].tests), set(check_fast.TENANT_IDENTITY_HTTP_TESTS)
        )

    def test_tenant_identity_paths_select_core_and_oidc_postgres_cases(self):
        paths = tuple(sorted(check_fast.TENANT_AUTHORITY_CORE_PATHS)) + (
            "internal/coreauthority/transport.go",
            "internal/operationauthority/client.go",
            "internal/operationauthority/client_test.go",
        )
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(plan.database)
                self.assertIn("./internal/store", targets)
                self.assertTrue(
                    set(check_fast.TENANT_IDENTITY_TESTS).issubset(
                        targets["./internal/store"].tests
                    )
                )
                self.assertEqual(
                    targets["./internal/store"].needs_model, check_fast._is_budget(path)
                )
                checks = check_fast._build_checks(
                    plan,
                    self.root,
                    sys.executable,
                    self.root.parent / "tenant-identity-check-output",
                )
                store_checks = [
                    check
                    for check in checks
                    if check.name.startswith("database-fixed-point")
                    and check.command[-1] == "./internal/store"
                ]
                self.assertEqual(len(store_checks), 1)
                self.assertEqual(
                    set(store_checks[0].expected_tests), set(targets["./internal/store"].tests)
                )
                pattern = store_checks[0].command[store_checks[0].command.index("-run") + 1]
                for name in check_fast.TENANT_IDENTITY_TESTS:
                    self.assertIn(name, pattern)
                self.assertIn("./internal/bootstrap", targets)
                expected_bootstrap = set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS)
                if check_fast._is_case_intake_authority(path):
                    expected_bootstrap.add(
                        "TestDistributedIntakeRecoversLostDocumentResponseAcrossSessions"
                    )
                self.assertEqual(set(targets["./internal/bootstrap"].tests), expected_bootstrap)
                if check_fast._is_case_intake_authority(path):
                    self.assertTrue(
                        set(check_fast.CASE_INTAKE_AUTHORITY_TESTS).issubset(
                            targets["./internal/store"].tests
                        )
                    )

    def test_intake_recovery_paths_select_role_and_http_regressions(self):
        for path in (
            "internal/store/case_intake.go",
            "internal/store/upload_idempotency.go",
            "internal/store/migrations/0041_intake_session_recovery.sql",
            "internal/documentservice/intake_upload.go",
        ):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: set(target.tests) for target in plan.database_tests}
                self.assertTrue(plan.database)
                self.assertTrue(
                    set(check_fast.CASE_INTAKE_AUTHORITY_TESTS).issubset(
                        targets["./internal/store"]
                    )
                )
                self.assertIn(
                    "TestDistributedIntakeRecoversLostDocumentResponseAcrossSessions",
                    targets["./internal/bootstrap"],
                )

    def test_tenant_identity_http_paths_select_oidc_cases(self):
        http_paths = (
            "internal/accounts/accounts.go",
            "internal/identity/manager.go",
            "internal/httpapi/oidc.go",
            "internal/httpapi/oidc_test.go",
            "internal/httpapi/session_audience.go",
            "internal/httpapi/server.go",
            "internal/httpapi/services.go",
            "internal/store/tenant_identity.go",
            "internal/store/migrations/0033_tenant_membership.sql",
            "internal/store/service_roles.go",
            "internal/store/identity_import.go",
            "internal/store/identity_import_test.go",
        )
        for path in http_paths:
            with self.subTest(http_path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertIn("./internal/httpapi", targets)
                expected_http = set(check_fast.TENANT_IDENTITY_HTTP_TESTS)
                if path == "internal/httpapi/server.go":
                    expected_http.update(check_fast.REVIEW_READMISSION_HTTP_TESTS)
                    expected_http.update(check_fast.REPORT_RECEIPT_HTTP_TESTS)
                self.assertEqual(set(targets["./internal/httpapi"].tests), expected_http)
                self.assertIn("./internal/store", targets)
                self.assertTrue(
                    set(check_fast.TENANT_IDENTITY_TESTS).issubset(
                        targets["./internal/store"].tests
                    )
                )

    def test_review_readmission_sources_select_postgres_regressions(self):
        for path in (
            "internal/store/execution.go",
            "internal/store/write_idempotency.go",
            "internal/store/attempt_finalize.go",
            "internal/store/document_prepare.go",
            "internal/store/review_readmission_pg_test.go",
            "internal/store/case_context_migration_test.go",
            "internal/httpapi/server.go",
            "internal/store/migrations/0042_review_receipt_session_recovery.sql",
            "internal/store/migrations/0043_review_receipt_origin_org.sql",
            "internal/store/service_roles.go",
            "internal/store/tenant_identity_role_provision.go",
            "internal/store/migrations.go",
            "internal/store/table_catalog.json",
        ):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertTrue(plan.database)
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(
                    set(check_fast.REVIEW_READMISSION_TESTS).issubset(
                        targets["./internal/store"].tests
                    )
                )
        for path in (
            "internal/httpapi/server.go",
            "internal/httpapi/pending_image_test.go",
            "internal/store/execution.go",
            "internal/store/write_idempotency.go",
            "internal/store/migrations/0042_review_receipt_session_recovery.sql",
            "internal/store/migrations/0043_review_receipt_origin_org.sql",
        ):
            with self.subTest(http_path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(
                    set(check_fast.REVIEW_READMISSION_HTTP_TESTS).issubset(
                        targets["./internal/httpapi"].tests
                    )
                )

    def test_report_receipt_sources_select_replay_regressions(self):
        sources = (
            "internal/store/business_reports.go",
            "internal/store/write_idempotency.go",
            "internal/store/report_receipt_replay_pg_test.go",
            "internal/store/migrations/0044_report_receipt_session_recovery.sql",
            "internal/store/migrations.go",
            "internal/store/table_catalog.json",
            "internal/store/service_roles.go",
            "internal/store/tenant_identity_role_provision.go",
            "internal/httpapi/business.go",
            "internal/httpapi/server.go",
            "internal/httpapi/report_receipt_replay_test.go",
        )
        for path in sources:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target.tests for target in plan.database_tests}
                self.assertTrue(plan.database)
                self.assertTrue({
                    "TestReportReceiptReplayAfterNewSessionPostgres",
                    "TestReportReceiptReplayPreservesSessionOriginOrganizationPostgres",
                }.issubset(targets["./internal/store"]))
                self.assertFalse(plan.web)
        for path in ("internal/store/business_reports.go",
                     "internal/store/migrations/0044_report_receipt_session_recovery.sql",
                     "internal/httpapi/business.go", "internal/httpapi/server.go",
                     "internal/httpapi/report_receipt_replay_test.go"):
            with self.subTest(http_path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target.tests for target in plan.database_tests}
                self.assertIn("TestReportReceiptReplayAfterLoginHTTP", targets["./internal/httpapi"])

    def test_tenant_authority_session_and_model_paths_select_required_suites(self):
        paths = (
            "internal/store/tool_authority.go",
            "internal/store/pg.go",
            "internal/modelgateway/service.go",
            "internal/modelgateway/jev.go",
        )
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(plan.database)
                self.assertEqual(
                    set(targets),
                    {"./internal/store", "./internal/bootstrap"}
                    | ({"./internal/httpapi"} if path.endswith("/pg.go") else set()),
                )
                store_tests = set(targets["./internal/store"].tests)
                self.assertTrue(set(check_fast.TENANT_IDENTITY_TESTS).issubset(store_tests))
                self.assertEqual(
                    set(targets["./internal/bootstrap"].tests),
                    set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS),
                )
                self.assertEqual(
                    targets["./internal/store"].needs_model, check_fast._is_budget(path)
                )
                if path.endswith("/pg.go"):
                    self.assertEqual(
                        set(targets["./internal/httpapi"].tests),
                        set(check_fast.TENANT_IDENTITY_HTTP_TESTS),
                    )
                self.assertFalse(any(target.needs_model for target in targets.values()))

    def test_tenant_identity_rule_unions_auth_session_clm_and_both_outbox_packages(self):
        paths = (
            "internal/accounts/accounts.go",
            "internal/identity/manager.go",
            "internal/httpapi/oidc.go",
            "internal/httpapi/session_audience.go",
            "internal/store/tenant_identity.go",
            "internal/store/identity_import.go",
            "internal/store/context_transaction.go",
            "internal/store/context_support.go",
            "internal/store/session_lifecycle.go",
            "internal/store/contract_payments.go",
            "internal/store/contract_assets.go",
            "internal/store/contract_sources.go",
            "internal/store/clm_outbox.go",
            "internal/clmoutbox/relay.go",
        )
        plan = self.plan(*(check_fast.Change("M", path) for path in paths))
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(
            set(targets),
            {
                "./internal/store",
                "./internal/httpapi",
                "./internal/bootstrap",
                "./internal/clmoutbox",
            },
        )
        store_tests = set(targets["./internal/store"].tests)
        for selected in (
            check_fast.AUTH_TESTS,
            check_fast.SESSION_TESTS,
            check_fast.TENANT_IDENTITY_TESTS,
            check_fast.CONTEXT_FENCE_TESTS,
            check_fast.CONTRACT_PAYMENT_TESTS,
            check_fast.CONTRACT_ASSET_TESTS,
            check_fast.CONTRACT_SOURCE_TESTS,
            check_fast.CLM_OUTBOX_STORE_TESTS,
        ):
            self.assertTrue(set(selected).issubset(store_tests), selected)
        self.assertTrue(
            set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS).issubset(
                targets["./internal/bootstrap"].tests
            )
        )
        self.assertEqual(
            set(targets["./internal/httpapi"].tests), set(check_fast.TENANT_IDENTITY_HTTP_TESTS)
        )
        self.assertIn(check_fast.CLM_OUTBOX_RELAY_TESTS[0], targets["./internal/clmoutbox"].tests)
        self.assertFalse(any(target.needs_model for target in targets.values()))

    def test_context_fence_sources_select_the_frozen_store_postgres_case(self):
        self.assertTrue(check_fast.CONTEXT_FENCE_PATHS)
        for path in sorted(check_fast.CONTEXT_FENCE_PATHS):
            with self.subTest(path=path):
                self.assertTrue((check_fast.REPO_ROOT / path).is_file())
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(plan.database)
                self.assertIn("./internal/store", targets)
                self.assertTrue(
                    set(check_fast.CONTEXT_FENCE_TESTS).issubset(targets["./internal/store"].tests)
                )

        plan = self.plan(check_fast.Change("M", "internal/httpapi/context_session.go"))
        self.assertEqual({target.package for target in plan.database_tests}, {"./internal/store"})
        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "context-fence-check-output"
        )
        store_checks = [
            check
            for check in checks
            if check.name.startswith("database-fixed-point")
            and check.command[-1] == "./internal/store"
        ]
        self.assertEqual(len(store_checks), 1)
        self.assertTrue(
            set(check_fast.CONTEXT_FENCE_TESTS).issubset(store_checks[0].expected_tests)
        )
        self.assertTrue(set(check_fast.SESSION_TESTS).issubset(store_checks[0].expected_tests))
        pattern = store_checks[0].command[store_checks[0].command.index("-run") + 1]
        for name in check_fast.CONTEXT_FENCE_TESTS:
            self.assertIn(name, pattern)

    def test_tenant_identity_database_rule_does_not_spread_to_ui_docs_or_nats(self):
        for path in (
            "web/src/pages/contracts-page.tsx",
            "docs/guide.md",
            "internal/outboxrelay/jetstream.go",
            "deploy/nats/jetstream.yaml",
        ):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertFalse(plan.database)
                self.assertEqual(plan.database_tests, ())

    def test_membership_authority_bootstrap_test_file_selects_required_pg_tests(self):
        plan = self.plan(
            check_fast.Change("M", "internal/bootstrap/membership_authority_integration_test.go")
        )
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(set(targets), {"./internal/store", "./internal/bootstrap"})
        self.assertEqual(
            set(targets["./internal/store"].tests), set(check_fast.TENANT_IDENTITY_TESTS)
        )
        self.assertEqual(
            set(targets["./internal/bootstrap"].tests),
            set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS),
        )

        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "membership-check-output"
        )
        checks_by_package = {
            check.command[-1]: check
            for check in checks
            if check.name.startswith("database-fixed-point")
        }
        self.assertEqual(set(checks_by_package), set(targets))
        for package, check in checks_by_package.items():
            self.assertEqual(set(check.expected_tests), set(targets[package].tests))
            self.assertIn("-run", check.command)
            pattern = check.command[check.command.index("-run") + 1]
            self.assertNotEqual(pattern, "^$")
            if package == "./internal/bootstrap":
                self.assertIn("TestDistributedMembershipAdmissionAndModelSettlement", pattern)

    def test_identity_db_provisioner_script_selects_go_and_required_pg_targets(self):
        plan = self.plan(check_fast.Change("M", "scripts/provision_test_db.sh"))
        targets = {target.package: target for target in plan.database_tests}
        self.assertTrue(plan.database)
        self.assertTrue(plan.go)
        self.assertFalse(plan.python)
        self.assertEqual(
            set(targets), {"./internal/store", "./internal/bootstrap", "./internal/httpapi"}
        )
        self.assertEqual(
            set(targets["./internal/store"].tests),
            set(check_fast.TENANT_IDENTITY_TESTS)
            | set(check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS)
            | set(check_fast.CONTEXT_FENCE_TESTS),
        )
        self.assertEqual(
            set(targets["./internal/bootstrap"].tests),
            set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS),
        )
        self.assertEqual(
            set(targets["./internal/httpapi"].tests), set(check_fast.TENANT_IDENTITY_HTTP_TESTS)
        )
        self.assertFalse(any(target.needs_model for target in targets.values()))

        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "provision-check-output"
        )
        db_checks = {
            check.command[-1]: check
            for check in checks
            if check.name.startswith("database-fixed-point")
        }
        self.assertEqual(set(db_checks), set(targets))
        for package, check in db_checks.items():
            self.assertEqual(set(check.expected_tests), set(targets[package].tests))
            self.assertIn("-run", check.command)

    def test_offline_migrator_bootstrap_paths_select_fresh_and_legacy_pg_case(self):
        paths = (
            "internal/store/offline_migrator_provision.go",
            "internal/store/offline_migrator_provision_pg_test.go",
            "cmd/migrate/main.go",
            "scripts/provision_test_db.sh",
            "internal/store/clm_role_provision.go",
            "internal/store/tenant_identity_role_provision.go",
            "internal/store/migrations/0042_review_receipt_session_recovery.sql",
            "internal/store/migrations/0043_review_receipt_origin_org.sql",
        )
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(plan.go)
                self.assertTrue(plan.database)
                self.assertIn("./internal/store", targets)
                self.assertIn(
                    check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS[0],
                    targets["./internal/store"].tests,
                )
                self.assertEqual(
                    targets["./internal/store"].needs_model, check_fast._is_budget(path)
                )

                checks = check_fast._build_checks(
                    plan,
                    self.root,
                    sys.executable,
                    self.root.parent / "offline-migrator-check-output",
                )
                store_checks = [
                    check
                    for check in checks
                    if check.name.startswith("database-fixed-point")
                    and check.command[-1] == "./internal/store"
                ]
                self.assertEqual(len(store_checks), 1)
                self.assertIn(
                    check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS[0], store_checks[0].expected_tests
                )
                pattern = store_checks[0].command[store_checks[0].command.index("-run") + 1]
                self.assertIn(check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS[0], pattern)

        budget_plan = self.plan(check_fast.Change("M", "internal/modelgateway/budget.go"))
        budget_targets = {target.package: target for target in budget_plan.database_tests}
        self.assertTrue(budget_targets["./internal/store"].needs_model)
        self.assertEqual(budget_targets["./internal/store"].tests, check_fast.BUDGET_TESTS)

    def test_budget_change_requires_model_role(self):
        plan = self.plan(check_fast.Change("M", "internal/store/model_budget.go"))
        self.assertTrue(plan.database)
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(set(targets), {"./internal/store", "./internal/bootstrap"})
        self.assertTrue(targets["./internal/store"].needs_model)
        self.assertEqual(
            set(targets["./internal/store"].tests),
            set(check_fast.BUDGET_TESTS)
            | set(check_fast.TENANT_IDENTITY_TESTS)
            | set(check_fast.CONTEXT_FENCE_TESTS),
        )
        self.assertEqual(
            set(targets["./internal/bootstrap"].tests),
            set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS),
        )
        self.assertFalse(targets["./internal/bootstrap"].needs_model)

    def test_python_migration_selects_database_and_go_toolchain(self):
        plan = self.plan(check_fast.Change("M", "scripts/migrate_schema.py"))
        self.assertTrue(plan.python)
        self.assertTrue(plan.database)
        self.assertTrue(plan.go)
        self.assertFalse(plan.go_build_all)
        self.assertEqual(plan.go_unit_packages, set())

    def test_schema_change_requires_real_database_migration_check(self):
        plan = self.plan(check_fast.Change("M", "internal/store/schema.go"))
        self.assertTrue(plan.database)
        self.assertIn("migration", plan.database_tests[0].reasons)
        self.assertIn(
            "TestEvaluationProvenanceMigrationPreservesHistoricalRows", plan.database_tests[0].tests
        )

    def test_object_freeze_change_requires_persisted_snapshot_check(self):
        plan = self.plan(check_fast.Change("M", "internal/store/service_object_context.go"))
        self.assertTrue(plan.database)
        self.assertIn("frozen-evidence", plan.database_tests[0].reasons)
        self.assertIn(
            "TestDurableReviewFreezesServiceObjectContextAndRejectsChangedRetryAfterReopen",
            plan.database_tests[0].tests,
        )

    def test_contract_payment_sources_select_focused_postgres_guard(self):
        paths = (
            "internal/store/migrations/0029_contract_payments.sql",
            "internal/store/migrations.go",
            "internal/store/contract_payments.go",
            "internal/store/contract_payments_pg_test.go",
            "internal/store/contract_draft.go",
            "internal/store/service_roles.go",
            "internal/store/contract_fulfillment_pg_test.go",
            "internal/httpapi/contract_payments.go",
            "internal/httpapi/contract_payments_test.go",
            "internal/httpapi/server.go",
            "internal/httpapi/services.go",
            "internal/httpapi/services_ownership_test.go",
            "cmd/server/main.go",
            "cmd/server/main_test.go",
        )
        target = check_fast.CONTRACT_PAYMENT_TESTS[0]
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertTrue(plan.database)
                targets = {target.package: target for target in plan.database_tests}
                payment_target = targets["./internal/store"]
                self.assertEqual(payment_target.package, "./internal/store")
                self.assertIn("contract-payment", payment_target.reasons)
                self.assertIn(target, payment_target.tests)
                self.assertFalse(payment_target.needs_model)

    def test_contract_asset_sources_select_focused_postgres_guard(self):
        paths = (
            "internal/store/migrations/0030_contract_assets.sql",
            "internal/store/migrations.go",
            "internal/store/contract_assets.go",
            "internal/store/contract_assets_pg_test.go",
            "internal/store/contract_draft.go",
            "internal/store/service_roles.go",
            "internal/store/contract_fulfillment_pg_test.go",
            "internal/store/immutable_parsing_test.go",
            "internal/store/session_lifecycle_test.go",
            "internal/httpapi/contract_assets.go",
            "internal/httpapi/contract_assets_test.go",
            "internal/httpapi/server.go",
            "internal/httpapi/services.go",
            "internal/httpapi/services_ownership_test.go",
            "cmd/server/main.go",
            "cmd/server/main_test.go",
        )
        target = check_fast.CONTRACT_ASSET_TESTS[0]
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertTrue(plan.database)
                targets = {target.package: target for target in plan.database_tests}
                asset_target = targets["./internal/store"]
                self.assertEqual(asset_target.package, "./internal/store")
                self.assertIn("contract-asset", asset_target.reasons)
                self.assertIn(target, asset_target.tests)
                self.assertFalse(asset_target.needs_model)

        shared_core_plan = self.plan(check_fast.Change("M", "internal/store/contract_draft.go"))
        shared_core_target = shared_core_plan.database_tests[0]
        self.assertIn(check_fast.CONTRACT_PAYMENT_TESTS[0], shared_core_target.tests)
        self.assertIn(target, shared_core_target.tests)

    def test_contract_source_observation_sources_select_focused_postgres_guard(self):
        paths = (
            "internal/clmsource/contract.go",
            "internal/clmsource/contract_test.go",
            "internal/store/contract_sources.go",
            "internal/store/contract_sources_crypto.go",
            "internal/store/contract_sources_crypto_test.go",
            "internal/store/contract_sources_pg_test.go",
            "internal/store/contract_approval_decision.go",
            "internal/store/contract_approval_decision_pg_test.go",
            "internal/store/contract_draft.go",
            "internal/store/migrations/0031_contract_source_observations.sql",
            "internal/store/migrations.go",
            "internal/store/service_roles.go",
            "internal/httpapi/contract_sources.go",
            "internal/httpapi/contract_sources_test.go",
            "internal/httpapi/server.go",
            "internal/httpapi/services.go",
            "cmd/server/main.go",
            "cmd/server/main_test.go",
        )
        targets = check_fast.CONTRACT_SOURCE_TESTS
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertTrue(plan.database)
                pg_targets = {target.package: target for target in plan.database_tests}
                expected_packages = {"./internal/store"}
                if path in {
                    "internal/store/migrations.go",
                    "internal/store/service_roles.go",
                    "internal/httpapi/server.go",
                    "internal/httpapi/services.go",
                    "cmd/server/main.go",
                    "cmd/server/main_test.go",
                }:
                    expected_packages.update({"./internal/bootstrap", "./internal/httpapi"})
                self.assertEqual(set(pg_targets), expected_packages)
                self.assertEqual(len(plan.database_tests), len(expected_packages))
                pg_target = pg_targets["./internal/store"]
                self.assertEqual(pg_target.package, "./internal/store")
                self.assertIn("contract-source", pg_target.reasons)
                for target in targets:
                    self.assertIn(target, pg_target.tests)
                self.assertFalse(pg_target.needs_model)

        legacy_approval_plan = self.plan(
            check_fast.Change("M", "internal/store/contract_approval_decision_pg_test.go")
        )
        legacy_approval_target = legacy_approval_plan.database_tests[0]
        self.assertTrue(legacy_approval_plan.database)
        self.assertIn("contract-source", legacy_approval_target.reasons)
        for target in targets:
            self.assertIn(target, legacy_approval_target.tests)

        shared_core_plan = self.plan(check_fast.Change("M", "internal/store/contract_draft.go"))
        shared_core_target = shared_core_plan.database_tests[0]
        for target in targets:
            self.assertIn(target, shared_core_target.tests)
        self.assertIn(check_fast.CONTRACT_PAYMENT_TESTS[0], shared_core_target.tests)
        self.assertIn(check_fast.CONTRACT_ASSET_TESTS[0], shared_core_target.tests)

    def test_clm_outbox_changes_select_store_and_bridge_packages_separately(self):
        paths = (
            "internal/clmoutbox/relay.go",
            "internal/clmoutbox/relay_pg_test.go",
            "internal/clmoutbox/consumer/cache.go",
            "internal/store/clm_outbox.go",
            "internal/store/clm_metrics.go",
            "internal/store/clm_outbox_pg_test.go",
            "internal/store/migrations/0032_clm_outbox_metrics.sql",
            "internal/store/migrations.go",
            "internal/store/clm_role_provision.go",
            "internal/store/service_roles.go",
            "internal/store/runtime_metrics.go",
            "internal/store/state.go",
            "internal/bootstrap/services.go",
            "internal/bootstrap/services_integration_test.go",
            "internal/telemetry/runtimemetrics.go",
            "cmd/server/main.go",
            "cmd/server/main_test.go",
        )
        plan = self.plan(*(check_fast.Change("M", path) for path in paths))
        self.assertTrue(plan.database)
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(
            set(targets),
            {
                "./internal/store",
                "./internal/clmoutbox",
                "./internal/bootstrap",
                "./internal/httpapi",
            },
        )
        for test in check_fast.CLM_OUTBOX_STORE_TESTS:
            self.assertIn(test, targets["./internal/store"].tests)
        self.assertEqual(
            targets["./internal/clmoutbox"].tests, tuple(sorted(check_fast.CLM_OUTBOX_RELAY_TESTS))
        )
        self.assertEqual(
            set(targets["./internal/bootstrap"].tests),
            set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS)
            | {"TestDistributedIntakeRecoversLostDocumentResponseAcrossSessions"},
        )
        self.assertEqual(
            set(targets["./internal/httpapi"].tests), set(check_fast.TENANT_IDENTITY_HTTP_TESTS)
        )
        self.assertFalse(any(target.needs_model for target in targets.values()))

        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "check-output"
        )
        db_checks = [check for check in checks if check.name.startswith("database-fixed-point")]
        self.assertEqual(len(db_checks), 4)
        self.assertEqual(len({check.name for check in db_checks}), 4)
        for check in db_checks:
            package = check.command[-1]
            self.assertIn(package, targets)
            self.assertEqual(set(check.expected_tests), set(targets[package].tests))
            self.assertIn("-vet=off", check.command)
            self.assertIn("-run", check.command)
        self.assertEqual(
            {check.command[-1] for check in db_checks},
            {
                "./internal/store",
                "./internal/clmoutbox",
                "./internal/bootstrap",
                "./internal/httpapi",
            },
        )

    def test_clm_outbox_scope_unions_existing_source_payment_and_asset_checks(self):
        plan = self.plan(
            check_fast.Change("M", "internal/store/clm_outbox.go"),
            check_fast.Change("M", "internal/store/contract_sources.go"),
            check_fast.Change("M", "internal/store/contract_payments.go"),
            check_fast.Change("M", "internal/store/contract_assets.go"),
            check_fast.Change("M", "internal/clmoutbox/relay.go"),
        )
        targets = {target.package: target for target in plan.database_tests}
        store_target = targets["./internal/store"]
        self.assertIn("contract-source", store_target.reasons)
        self.assertIn("contract-payment", store_target.reasons)
        self.assertIn("contract-asset", store_target.reasons)
        self.assertIn("clm-outbox-core", store_target.reasons)
        self.assertIn(
            "TestExternalContractSourceObservationOrderingAndAuthorization", store_target.tests
        )
        self.assertIn(check_fast.CONTRACT_PAYMENT_TESTS[0], store_target.tests)
        self.assertIn(check_fast.CONTRACT_ASSET_TESTS[0], store_target.tests)
        for test in check_fast.CLM_OUTBOX_STORE_TESTS:
            self.assertIn(test, store_target.tests)
        self.assertIn("./internal/clmoutbox", targets)
        self.assertIn(check_fast.CLM_OUTBOX_RELAY_TESTS[0], targets["./internal/clmoutbox"].tests)
        self.assertFalse(any(target.needs_model for target in targets.values()))

    def test_clm_outbox_pure_nats_transport_or_deployment_config_does_not_require_database(self):
        for path in ("internal/outboxrelay/jetstream.go", "deploy/nats/jetstream.yaml"):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertFalse(plan.database)
                self.assertEqual(plan.database_tests, ())


class CliBoundaryTests(unittest.TestCase):
    def metadata_fixture_tools(self, gate: str):
        for module in ("metadata_core", "git_inventory", "manifest_tool", "legacy_bootstrap"):
            write(self.root, f"scripts/file_metadata/{module}.py", "# isolated fixture module\n")
        write(self.root, "scripts/file_metadata/gate_adapter.py", gate)

    def run_metadata_fixture(self, base: str, subject: str):
        out = Path(self.temporary.name) / "metadata-out"
        (out / "logs").mkdir(parents=True, exist_ok=True)
        return check_fast.execute_check(
            check_fast.FastCheck(
                "file-metadata",
                (sys.executable,),
                cwd=self.root,
                kind="metadata-gate",
                metadata_base=base,
                metadata_subject=subject,
            ),
            out / "logs",
            self.root,
        )

    def test_metadata_runs_base_blobs_instead_of_modified_candidate(self):
        self.metadata_fixture_tools(
            'import json,sys; print(json.dumps({"status":"NOT_RUN","mode":"base-fixture"})); sys.exit(3)\n'
        )
        base = commit(self.root, "trusted metadata fixture")
        marker = Path(self.temporary.name) / "candidate-executed"
        write(
            self.root,
            "scripts/file_metadata/gate_adapter.py",
            f'from pathlib import Path\nPath({str(marker)!r}).write_text("unsafe")\n',
        )
        subject = commit(self.root, "untrusted candidate verifier")
        result = self.run_metadata_fixture(base, subject)
        self.assertEqual(result.status, "NOT_RUN")
        self.assertEqual(result.evidence["verifier_source"], "trusted-base")
        self.assertFalse(marker.exists())

    def test_metadata_foundation_cannot_self_report_pass(self):
        write(
            self.root,
            "scripts/file_metadata/policy.json",
            json.dumps({"manifest_status": "populated-from-issue-115-baseline"}),
        )
        base = commit(self.root, "legacy metadata policy")
        self.metadata_fixture_tools('import json\nprint(json.dumps({"status":"PASS"}))\n')
        subject = commit(self.root, "candidate claims initialized")
        result = self.run_metadata_fixture(base, subject)
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.evidence["verifier_source"], "foundation-candidate")

    def test_metadata_partial_base_tools_fail_closed(self):
        write(self.root, "scripts/file_metadata/gate_adapter.py", "# incomplete verifier\n")
        base = commit(self.root, "partial metadata tools")
        result = self.run_metadata_fixture(base, base)
        self.assertEqual(result.status, "FAIL")
        self.assertIn("missing or incomplete", result.reason)

    def test_metadata_status_and_exit_code_must_agree(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for status, code, expected in (
                ("PASS", 0, "PASS"),
                ("NOT_RUN", 3, "NOT_RUN"),
                ("PASS", 3, "FAIL"),
                ("NOT_RUN", 0, "FAIL"),
            ):
                with self.subTest(status=status, code=code):
                    command = (
                        sys.executable,
                        "-c",
                        f'import json,sys; print(json.dumps({{"status": {status!r}}})); sys.exit({code})',
                    )
                    result = check_fast.execute_check(
                        check_fast.FastCheck(
                            "metadata-contract", command, cwd=root, kind="metadata-gate"
                        ),
                        root / "logs",
                        root,
                    )
                    self.assertEqual(result.status, expected)

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "repo"
        self.root.mkdir()
        shutil.copytree(SCRIPT.parent / "architecture", self.root / "scripts/architecture")
        # This sandbox exercises fast-gate orchestration using a synthetic Go
        # graph, not the production domain registry. Establish its own empty
        # registry before the base commit so both sides have the same policy.
        architecture = self.root / "scripts/architecture/architecture.json"
        config = json.loads(architecture.read_text(encoding="utf-8"))
        config["ratchet"]["domains"] = []
        architecture.write_text(json.dumps(config, indent=2) + "\n", encoding="utf-8")
        git(self.root, "init", "-b", "main")
        git(self.root, "config", "user.name", "Fast Check Test")
        git(self.root, "config", "user.email", "fast-check@example.test")
        write(self.root, "go.mod", "module example.test/project\n\ngo 1.25\n")
        write(self.root, "README.md", "# repo\n")
        write(self.root, "internal/decision/decision.go", "package decision\n\nvar Value = 1\n")
        write(self.root, "docs old/keep.txt", "same bytes\n")
        write(self.root, "internal/old/old.py", "OLD = 1\n")
        self.base = commit(self.root, "base")
        write(self.root, "internal/decision/decision.go", "package decision\n\nvar Value = 2\n")
        commit(self.root, "change decision")

    def prepare_working_tree(self):
        git(self.root, "rm", "internal/old/old.py")
        git(self.root, "mv", "docs old/keep.txt", "docs old/renamed.txt")
        write(self.root, "web/src/app.tsx", "export const app = true\n")
        write(self.root, "scripts/tool.py", "def run():\n    return 1\n")
        write(
            self.root,
            "scripts/test_tool.py",
            "import unittest\nfrom tool import run\n\n"
            "class ToolTest(unittest.TestCase):\n"
            "    def test_run(self):\n        self.assertEqual(run(), 1)\n",
        )

    def test_plan_is_one_line_and_github_output_is_appended(self):
        self.prepare_working_tree()
        output = Path(self.temporary.name) / "github-output"
        output.write_text("keep=1\n", encoding="utf-8")
        result = run_script(
            "--root",
            str(self.root),
            "--base",
            self.base,
            "--plan",
            "--github-output",
            str(output),
            env=script_env(),
        )
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
        self.assertEqual(
            appended[1:],
            [
                "go=true",
                "web=true",
                "python=true",
                "database=false",
                "docs_build=false",
                "architecture=true",
                "ui_terminology=false",
                "workflow=false",
                "poppler=false",
                "legal_assets=false",
            ],
        )

    def test_plan_uses_caw_fast_base_when_base_flag_is_omitted(self):
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        result = run_script(
            "--root", str(self.root), "--plan", env=script_env(CAW_FAST_BASE=self.base)
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["go"])

    def test_missing_base_ref_is_non_zero(self):
        result = run_script(
            "--root", str(self.root), "--base", "not-a-ref", "--plan", env=script_env()
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("base ref", result.stderr)

    def fake_binaries(self):
        directory = Path(self.temporary.name) / "bin"
        directory.mkdir()
        marker = Path(self.temporary.name) / "executed"
        for name in ("go", "gofmt", "bun"):
            path = directory / name
            path.write_text(
                '#!/bin/sh\nprintf \'%s\\n\' "$0" >> "$FAKE_MARKER"\nexit 0\n', encoding="utf-8"
            )
            path.chmod(0o755)
        return directory, marker

    @staticmethod
    def fake_go_with_unit_failure(path: Path):
        path.write_text(
            "#!/bin/sh\n"
            'if [ "$1" = env ]; then\n'
            '  printf \'%s\\n\' \'{"GOOS":"linux","GOARCH":"amd64",'
            '"GOVERSION":"go1.26.0","CGO_ENABLED":"1",'
            '"GOFLAGS":"","GOENV":"","GOWORK":""}\'\n'
            "  exit 0\nfi\n"
            'if [ "$1" = list ]; then\n'
            '  printf \'%s\\n\' \'{"ImportPath":"contractaudit/internal/contractmoney",'
            '"Name":"contractmoney","GoFiles":["decimal.go"],'
            '"Imports":["errors","fmt","math/big","strconv","strings"]}\' '
            '\'{"ImportPath":"contractaudit/internal/modelgateway",'
            '"Name":"modelgateway","GoFiles":["jev.go"],'
            '"Imports":["contractaudit/internal/store"]}\' '
            '\'{"ImportPath":"contractaudit/internal/advisorycontract",'
            '"Name":"advisorycontract","GoFiles":["contract.go"],'
            '"Imports":["time"]}\' '
            '\'{"ImportPath":"contractaudit/internal/coreauthority",'
            '"Name":"coreauthority","GoFiles":["transport.go"],'
            '"Imports":["net/http","time"]}\'\n'
            "  exit 0\nfi\n"
            'for argument in "$@"; do\n'
            '  if [ "$argument" = "-json" ]; then exit 1; fi\n'
            "done\nexit 0\n",
            encoding="utf-8",
        )
        path.chmod(0o755)

    def test_missing_database_environment_blocks_before_checks(self):
        write(self.root, "internal/store/session_lifecycle.go", "package store\n")
        binaries, marker = self.fake_binaries()
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(
            PATH=str(binaries) + os.pathsep + os.environ["PATH"], FAKE_MARKER=str(marker)
        )
        result = run_script(
            "--root", str(self.root), "--base", self.base, "--out", str(out), env=env
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("database plan requires", result.stderr)
        self.assertFalse(marker.exists())
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(report["passed"])
        self.assertEqual(report["checks"][0]["status"], "NOT_RUN")

    def test_architecture_blocked_report_fails_the_fast_gate(self):
        config_path = self.root / "scripts/architecture/architecture.json"
        config = json.loads(config_path.read_text(encoding="utf-8"))
        config["review_marker"] = "architecture-trigger"
        config_path.write_text(json.dumps(config) + "\n", encoding="utf-8")
        binaries, marker = self.fake_binaries()
        go = binaries / "go"
        go.write_text(
            "#!/bin/sh\necho 'synthetic Go environment unavailable' >&2\nexit 7\n", encoding="utf-8"
        )
        go.chmod(0o755)
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(
            PATH=str(binaries) + os.pathsep + os.environ["PATH"], FAKE_MARKER=str(marker)
        )
        result = run_script(
            "--root", str(self.root), "--base", self.base, "--out", str(out), env=env
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        fast_report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(fast_report["passed"])
        self.assertEqual(fast_report["checks"][0]["name"], "architecture-dependencies")
        self.assertEqual(fast_report["checks"][0]["status"], "FAIL")
        guard_report = json.loads((out / "architecture-check.json").read_text(encoding="utf-8"))
        self.assertEqual(guard_report["status"], "BLOCKED")
        self.assertEqual(guard_report["exit_code"], 2)

    def test_go_unit_failure_fails_gate_when_unit_selection_falls_back(self):
        # Issue #53 regression: a go.mod-only change empties the affected
        # package set; the widened unit run must still fail the gate when the
        # unit tests fail, instead of returning 0 without running any test.
        write(
            self.root,
            "internal/decision/decision_test.go",
            """package decision

import "testing"

func TestValue(t *testing.T) { if Value != 2 { t.Fatal(Value) } }
              """,
        )
        new_base = commit(self.root, "add decision test")
        write(self.root, "go.mod", "module example.test/project\n\ngo 1.26\n")
        binaries, marker = self.fake_binaries()
        go = binaries / "go"
        self.fake_go_with_unit_failure(go)
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(
            PATH=str(binaries) + os.pathsep + os.environ["PATH"], FAKE_MARKER=str(marker)
        )
        result = run_script(
            "--root", str(self.root), "--base", new_base, "--out", str(out), env=env
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(report["passed"])
        checks = {check["name"]: check for check in report["checks"]}
        self.assertIn("go-unit", checks)
        self.assertEqual(checks["go-unit"]["status"], "FAIL")

    def test_go_unit_failure_fails_gate_when_compile_expands_to_all(self):
        write(
            self.root,
            "internal/decision/decision_test.go",
            """package decision

import "testing"

func TestValue(t *testing.T) { if Value != 2 { t.Fatal(Value) } }
              """,
        )
        write(
            self.root,
            "cmd/app/main.go",
            'package main\n\nimport (\n\t"example.test/project/internal/decision"\n'
            '\t"example.test/project/internal/shared"\n)\n\n'
            "func main() { _ = decision.Value; _ = shared.Value }\n",
        )
        binaries, marker = self.fake_binaries()
        go = binaries / "go"
        self.fake_go_with_unit_failure(go)
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(
            PATH=str(binaries) + os.pathsep + os.environ["PATH"], FAKE_MARKER=str(marker)
        )
        result = run_script(
            "--root", str(self.root), "--base", self.base, "--out", str(out), env=env
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(report["passed"])
        checks = {check["name"]: check for check in report["checks"]}
        self.assertEqual(checks["go-unit"]["status"], "FAIL")

    def test_affected_go_change_without_unit_tests_reports_not_run(self):
        # Issue #53: an affected Go change whose packages hold no database-safe
        # unit tests keeps an explicit go-unit NOT_RUN in the report instead of
        # dropping the stage silently; it does not fabricate a failure.
        write(self.root, "cmd/tool/main.go", "package main\n\nfunc main() {}\n")
        # This fixture isolates fast-gate orchestration; metadata validation
        # itself has dedicated temporary-Git integration tests.
        write(
            self.root,
            "scripts/file_metadata/gate_adapter.py",
            "import json, sys\n"
            'print(json.dumps({"status": "NOT_RUN", "mode": "fixture-legacy", '
            '"base": sys.argv[sys.argv.index("--base")+1], '
            '"subject": sys.argv[sys.argv.index("--subject")+1], '
            '"reason": "fixture baseline has no initialized metadata"}))\n'
            "sys.exit(3)\n",
        )
        for module in ("metadata_core", "git_inventory", "manifest_tool", "legacy_bootstrap"):
            write(
                self.root, f"scripts/file_metadata/{module}.py", "# orchestration fixture module\n"
            )
        new_base = commit(self.root, "add tool without unit tests")
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        binaries, marker = self.fake_binaries()
        self.fake_go_with_unit_failure(binaries / "go")
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(
            PATH=str(binaries) + os.pathsep + os.environ["PATH"], FAKE_MARKER=str(marker)
        )
        result = run_script(
            "--root", str(self.root), "--base", new_base, "--out", str(out), env=env
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertTrue(report["passed"])
        checks = {check["name"]: check for check in report["checks"]}
        self.assertIn("go-unit", checks)
        self.assertEqual(checks["go-unit"]["status"], "NOT_RUN")
        self.assertIn("no database-safe", checks["go-unit"]["reason"])
        self.assertEqual(checks["go-unit"]["command"], "")
        self.assertEqual(checks["file-metadata"]["status"], "NOT_RUN")
        self.assertEqual(checks["file-metadata"]["evidence"]["base"], new_base)
        self.assertEqual(
            checks["file-metadata"]["evidence"]["subject"],
            git(self.root, "rev-parse", "HEAD").stdout.strip(),
        )

    def test_output_inside_repository_is_rejected(self):
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        result = run_script(
            "--root",
            str(self.root),
            "--base",
            self.base,
            "--out",
            str(self.root / "fast-out"),
            env=script_env(),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("inside the repository", result.stderr)
        self.assertFalse((self.root / "fast-out").exists())


class RequiredPythonEvidenceTests(unittest.TestCase):
    def test_skip_and_expected_failure_cannot_count_as_pass(self):
        for summary in ("OK (skipped=1)", "OK (expected failures=1)"):
            with self.subTest(summary=summary):
                _, error = check_fast._unittest_evidence(
                    "Ran 1 test in 0.001s\n\n" + summary + "\n"
                )
                self.assertTrue(error)

    def test_missing_success_summary_is_not_evidence(self):
        self.assertTrue(check_fast._unittest_evidence("Ran 1 test in 0.001s\n")[1])


if __name__ == "__main__":
    unittest.main()

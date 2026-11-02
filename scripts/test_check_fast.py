"""Selection and failure-boundary tests for the shared fast-check CLI."""
import json
import os
from pathlib import Path
import shutil
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
        write(self.root, "internal/decision/decision_test.go",
              '''package decision

import "testing"

func TestValue(t *testing.T) { if Value != 1 { t.Fatal(Value) } }
              ''')
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

    def test_docs_only_selects_docs_build_without_application_scopes(self):
        plan = self.plan(check_fast.Change("M", "docs/index.md"),
                         check_fast.Change("M", "docs/glossary/concepts.json"))
        self.assertTrue(plan.docs_build)
        self.assertFalse(plan.go or plan.web or plan.python or plan.database)
        self.assertFalse(plan.poppler or plan.legal_assets)

    def test_docs_gate_sources_select_docs_build(self):
        for path in ("docs-site/package.json", "docs-site/bun.lock",
                     "scripts/check_docs.py", "scripts/check_glossary.py",
                     "scripts/check_fast.py", ".github/workflows/pr-fast.yml"):
            with self.subTest(path=path):
                self.assertTrue(self.plan(check_fast.Change("M", path)).docs_build)

    def test_web_only_does_not_select_docs_build(self):
        plan = self.plan(check_fast.Change("M", "web/src/app.tsx"))
        self.assertTrue(plan.web)
        self.assertFalse(plan.docs_build)
        self.assertFalse(plan.ui_terminology)

    def test_manual_findings_and_glossary_select_only_bounded_term_guard(self):
        for path in ("web/src/features/review/manual-findings/manual-findings-panel.tsx",
                     "docs/glossary/concepts.json",
                     "scripts/ui-terminology/policy.json",
                     "scripts/check_ui_terminology.py",
                     "scripts/test_ui_terminology.py"):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertTrue(plan.ui_terminology)
        ui_only = self.plan(check_fast.Change(
            "M", "web/src/features/review/manual-findings/manual-findings-panel.tsx"))
        self.assertTrue(ui_only.web)
        self.assertFalse(ui_only.docs_build)
        checks = check_fast._build_checks(
            ui_only, self.root, sys.executable, self.root.parent / "check-output")
        self.assertEqual([check.name for check in checks],
                         ["ui-terminology", "web-typecheck"])

    def test_docs_rename_deletion_and_space_paths_keep_docs_scope(self):
        renamed = self.plan(check_fast.Change(
            "R", "docs-site/handbook config.js", "docs/old handbook.md"))
        deleted = self.plan(check_fast.Change("D", "docs/removed handbook.md"))
        self.assertTrue(renamed.docs_build)
        self.assertTrue(deleted.docs_build)

    def test_docs_dependency_check_requires_frozen_lockfile(self):
        package_dir = self.root / "docs-site"
        package_dir.mkdir()
        bun = self.root / "fake-bun"
        bun.write_text("#!/bin/sh\nif [ ! -f bun.lock ]; then "
                       "echo 'missing docs lockfile' >&2; exit 2; fi\nexit 0\n",
                       encoding="utf-8")
        bun.chmod(0o755)
        check = check_fast.FastCheck(
            "docs-dependencies", (str(bun), "install", "--frozen-lockfile"),
            cwd=package_dir, env=dict(os.environ), timeout=10)
        result = check_fast.execute_check(check, self.root / "logs", self.root)
        self.assertEqual(result.status, "FAIL")
        self.assertIn("missing docs lockfile",
                      Path(result.log).read_text(encoding="utf-8"))

    def test_architecture_only_changes_select_guard_without_application_go(self):
        plan = self.plan(check_fast.Change(
            "M", "scripts/architecture/architecture.json"))
        self.assertTrue(plan.architecture)
        self.assertFalse(plan.go or plan.database or plan.web or plan.docs_build)
        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "check-output")
        self.assertEqual([check.name for check in checks],
                         ["architecture-dependencies"])
        self.assertIn("--out", checks[0].command)
        self.assertEqual(checks[0].command[-1],
                         str((self.root.parent / "check-output" /
                              "architecture-check.json").resolve()))

    def test_architecture_trigger_paths_and_rename_deletion(self):
        for path in ("scripts/architecture/check_architecture.py",
                     "scripts/architecture/architecture.schema.json",
                     "scripts/architecture/fixtures/allowed.json",
                     "internal/modelgateway/jev.go",
                     "internal/agentflow/advisory.go",
                     "internal/advisorycontract/contract.go",
                     "go.mod", "go.sum", "go.work", "go.work.sum", "go.env",
                     "scripts/check_fast.py", ".github/workflows/pr-fast.yml"):
            with self.subTest(path=path):
                self.assertTrue(self.plan(check_fast.Change("M", path)).architecture)
        package = self.plan(check_fast.Change("M", "internal/contractmoney/decimal.go"))
        self.assertTrue(package.architecture)
        self.assertTrue(package.go)
        moved = self.plan(check_fast.Change(
            "R", "internal/other/new file.go",
            "internal/contractmoney/old file.go"))
        removed = self.plan(check_fast.Change("D", "internal/contractmoney/old file.go"))
        self.assertTrue(moved.architecture)
        self.assertTrue(removed.architecture)

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
                self.assertTrue(set(check_fast.TENANT_IDENTITY_TESTS).issubset(
                    targets["./internal/store"].tests))
                self.assertEqual(targets["./internal/store"].needs_model,
                                 check_fast._is_budget(path))
                checks = check_fast._build_checks(
                    plan, self.root, sys.executable,
                    self.root.parent / "offline-migrator-check-output")
                store_checks = [check for check in checks
                                if check.name.startswith("database-fixed-point")
                                and check.command[-1] == "./internal/store"]
                self.assertEqual(len(store_checks), 1)
                self.assertIn(check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS[0],
                              store_checks[0].expected_tests)
                pattern = store_checks[0].command[
                    store_checks[0].command.index("-run") + 1]
                self.assertIn(check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS[0], pattern)
                self.assertIn("./internal/bootstrap", targets)
                self.assertEqual(set(targets["./internal/bootstrap"].tests),
                                 set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS))
                if check_fast._is_case_intake_authority(path):
                    self.assertTrue(set(check_fast.CASE_INTAKE_AUTHORITY_TESTS).issubset(
                        targets["./internal/store"].tests))

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
                self.assertEqual(
                    set(targets["./internal/httpapi"].tests),
                    set(check_fast.TENANT_IDENTITY_HTTP_TESTS))
                self.assertIn("./internal/store", targets)
                self.assertTrue(set(check_fast.TENANT_IDENTITY_TESTS).issubset(
                    targets["./internal/store"].tests))

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
                self.assertEqual(set(targets), {"./internal/store", "./internal/bootstrap"}
                                 | ({"./internal/httpapi"} if path.endswith("/pg.go") else set()))
                store_tests = set(targets["./internal/store"].tests)
                self.assertTrue(set(check_fast.TENANT_IDENTITY_TESTS).issubset(
                    store_tests))
                self.assertEqual(set(targets["./internal/bootstrap"].tests),
                                 set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS))
                self.assertEqual(targets["./internal/store"].needs_model,
                                 check_fast._is_budget(path))
                if path.endswith("/pg.go"):
                    self.assertEqual(set(targets["./internal/httpapi"].tests),
                                     set(check_fast.TENANT_IDENTITY_HTTP_TESTS))
                self.assertFalse(any(target.needs_model for target in targets.values()))

    def test_tenant_identity_rule_unions_auth_session_clm_and_both_outbox_packages(self):
        paths = (
            "internal/accounts/accounts.go",
            "internal/identity/manager.go",
            "internal/httpapi/oidc.go",
            "internal/httpapi/session_audience.go",
            "internal/store/tenant_identity.go",
            "internal/store/session_lifecycle.go",
            "internal/store/contract_payments.go",
            "internal/store/contract_assets.go",
            "internal/store/contract_sources.go",
            "internal/store/clm_outbox.go",
            "internal/clmoutbox/relay.go",
        )
        plan = self.plan(*(check_fast.Change("M", path) for path in paths))
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(set(targets),
                         {"./internal/store", "./internal/httpapi", "./internal/bootstrap",
                          "./internal/clmoutbox"})
        store_tests = set(targets["./internal/store"].tests)
        for selected in (
                check_fast.AUTH_TESTS,
                check_fast.SESSION_TESTS,
                check_fast.TENANT_IDENTITY_TESTS,
                check_fast.CONTRACT_PAYMENT_TESTS,
                check_fast.CONTRACT_ASSET_TESTS,
                check_fast.CONTRACT_SOURCE_TESTS,
                check_fast.CLM_OUTBOX_STORE_TESTS):
            self.assertTrue(set(selected).issubset(store_tests), selected)
        self.assertTrue(set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS).issubset(
            targets["./internal/bootstrap"].tests))
        self.assertEqual(set(targets["./internal/httpapi"].tests),
                         set(check_fast.TENANT_IDENTITY_HTTP_TESTS))
        self.assertIn(check_fast.CLM_OUTBOX_RELAY_TESTS[0],
                      targets["./internal/clmoutbox"].tests)
        self.assertFalse(any(target.needs_model for target in targets.values()))

    def test_tenant_identity_database_rule_does_not_spread_to_ui_docs_or_nats(self):
        for path in ("web/src/pages/contracts-page.tsx", "docs/guide.md",
                     "internal/outboxrelay/jetstream.go", "deploy/nats/jetstream.yaml"):
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                self.assertFalse(plan.database)
                self.assertEqual(plan.database_tests, ())

    def test_membership_authority_bootstrap_test_file_selects_required_pg_tests(self):
        plan = self.plan(check_fast.Change(
            "M", "internal/bootstrap/membership_authority_integration_test.go"))
        targets = {target.package: target for target in plan.database_tests}
        self.assertEqual(set(targets), {"./internal/store", "./internal/bootstrap"})
        self.assertEqual(set(targets["./internal/store"].tests),
                         set(check_fast.TENANT_IDENTITY_TESTS))
        self.assertEqual(set(targets["./internal/bootstrap"].tests),
                         set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS))

        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "membership-check-output")
        checks_by_package = {check.command[-1]: check for check in checks
                             if check.name.startswith("database-fixed-point")}
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
        self.assertEqual(set(targets),
                         {"./internal/store", "./internal/bootstrap", "./internal/httpapi"})
        self.assertEqual(set(targets["./internal/store"].tests),
                         set(check_fast.TENANT_IDENTITY_TESTS))
        self.assertEqual(set(targets["./internal/bootstrap"].tests),
                         set(check_fast.TENANT_AUTHORITY_BOOTSTRAP_TESTS))
        self.assertEqual(set(targets["./internal/httpapi"].tests),
                         set(check_fast.TENANT_IDENTITY_HTTP_TESTS))
        self.assertFalse(any(target.needs_model for target in targets.values()))

        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "provision-check-output")
        db_checks = {check.command[-1]: check for check in checks
                     if check.name.startswith("database-fixed-point")}
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
        )
        for path in paths:
            with self.subTest(path=path):
                plan = self.plan(check_fast.Change("M", path))
                targets = {target.package: target for target in plan.database_tests}
                self.assertTrue(plan.go)
                self.assertTrue(plan.database)
                self.assertIn("./internal/store", targets)
                self.assertIn(check_fast.OFFLINE_MIGRATOR_BOOTSTRAP_TESTS[0],
                              targets["./internal/store"].tests)
                self.assertEqual(targets["./internal/store"].needs_model,
                                 check_fast._is_budget(path))

        budget_plan = self.plan(check_fast.Change(
            "M", "internal/modelgateway/budget.go"))
        budget_targets = {target.package: target for target in budget_plan.database_tests}
        self.assertTrue(budget_targets["./internal/store"].needs_model)
        self.assertEqual(budget_targets["./internal/store"].tests,
                         check_fast.BUDGET_TESTS)

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
        self.assertEqual(plan.go_unit_packages, set())

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
                payment_target = plan.database_tests[0]
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
                asset_target = plan.database_tests[0]
                self.assertEqual(asset_target.package, "./internal/store")
                self.assertIn("contract-asset", asset_target.reasons)
                self.assertIn(target, asset_target.tests)
                self.assertFalse(asset_target.needs_model)

        shared_core_plan = self.plan(
            check_fast.Change("M", "internal/store/contract_draft.go"))
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
                self.assertEqual(len(plan.database_tests), 1)
                pg_target = plan.database_tests[0]
                self.assertEqual(pg_target.package, "./internal/store")
                self.assertIn("contract-source", pg_target.reasons)
                for target in targets:
                    self.assertIn(target, pg_target.tests)
                self.assertFalse(pg_target.needs_model)

        legacy_approval_plan = self.plan(
            check_fast.Change("M", "internal/store/contract_approval_decision_pg_test.go"))
        legacy_approval_target = legacy_approval_plan.database_tests[0]
        self.assertTrue(legacy_approval_plan.database)
        self.assertIn("contract-source", legacy_approval_target.reasons)
        for target in targets:
            self.assertIn(target, legacy_approval_target.tests)

        shared_core_plan = self.plan(
            check_fast.Change("M", "internal/store/contract_draft.go"))
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
        self.assertEqual(set(targets), {"./internal/store", "./internal/clmoutbox"})
        for test in check_fast.CLM_OUTBOX_STORE_TESTS:
            self.assertIn(test, targets["./internal/store"].tests)
        self.assertEqual(targets["./internal/clmoutbox"].tests,
                         tuple(sorted(check_fast.CLM_OUTBOX_RELAY_TESTS)))
        self.assertFalse(any(target.needs_model for target in targets.values()))

        checks = check_fast._build_checks(
            plan, self.root, sys.executable, self.root.parent / "check-output")
        db_checks = [check for check in checks if check.name.startswith("database-fixed-point")]
        self.assertEqual(len(db_checks), 2)
        self.assertEqual(len({check.name for check in db_checks}), 2)
        for check in db_checks:
            package = check.command[-1]
            self.assertIn(package, targets)
            self.assertEqual(set(check.expected_tests), set(targets[package].tests))
            self.assertIn("-vet=off", check.command)
            self.assertIn("-run", check.command)
        self.assertEqual(
            {check.command[-1] for check in db_checks},
            {"./internal/store", "./internal/clmoutbox"},
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
        self.assertIn("TestExternalContractSourceObservationOrderingAndAuthorization", store_target.tests)
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
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "repo"
        self.root.mkdir()
        shutil.copytree(SCRIPT.parent / "architecture",
                        self.root / "scripts/architecture")
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
                                        "database=false", "docs_build=false",
                                        "architecture=false", "ui_terminology=false",
                                        "poppler=false",
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

    @staticmethod
    def fake_go_with_unit_failure(path: Path):
        path.write_text(
            "#!/bin/sh\n"
            "if [ \"$1\" = env ]; then\n"
            "  printf '%s\\n' '{\"GOOS\":\"linux\",\"GOARCH\":\"amd64\","
            "\"GOVERSION\":\"go1.26.0\",\"CGO_ENABLED\":\"1\","
            "\"GOFLAGS\":\"\",\"GOENV\":\"\",\"GOWORK\":\"\"}'\n"
            "  exit 0\nfi\n"
            "if [ \"$1\" = list ]; then\n"
            "  printf '%s\\n' '{\"ImportPath\":\"contractaudit/internal/contractmoney\","
            "\"Name\":\"contractmoney\",\"GoFiles\":[\"decimal.go\"],"
            "\"Imports\":[\"errors\",\"fmt\",\"math/big\",\"strconv\",\"strings\"]}' "
            "'{\"ImportPath\":\"contractaudit/internal/modelgateway\","
            "\"Name\":\"modelgateway\",\"GoFiles\":[\"jev.go\"],"
            "\"Imports\":[\"contractaudit/internal/store\"]}' "
            "'{\"ImportPath\":\"contractaudit/internal/advisorycontract\","
            "\"Name\":\"advisorycontract\",\"GoFiles\":[\"contract.go\"],"
            "\"Imports\":[\"time\"]}'\n"
            "  exit 0\nfi\n"
            "for argument in \"$@\"; do\n"
            "  if [ \"$argument\" = \"-json\" ]; then exit 1; fi\n"
            "done\nexit 0\n", encoding="utf-8")
        path.chmod(0o755)

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

    def test_architecture_blocked_report_fails_the_fast_gate(self):
        config_path = self.root / "scripts/architecture/architecture.json"
        config = json.loads(config_path.read_text(encoding="utf-8"))
        config["review_marker"] = "architecture-trigger"
        config_path.write_text(json.dumps(config) + "\n", encoding="utf-8")
        binaries, marker = self.fake_binaries()
        go = binaries / "go"
        go.write_text("#!/bin/sh\necho 'synthetic Go environment unavailable' >&2\nexit 7\n",
                      encoding="utf-8")
        go.chmod(0o755)
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                         FAKE_MARKER=str(marker))
        result = run_script("--root", str(self.root), "--base", self.base,
                            "--out", str(out), env=env)
        self.assertEqual(result.returncode, 1, result.stderr)
        fast_report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(fast_report["passed"])
        self.assertEqual(fast_report["checks"][0]["name"],
                         "architecture-dependencies")
        self.assertEqual(fast_report["checks"][0]["status"], "FAIL")
        guard_report = json.loads((out / "architecture-check.json").read_text(encoding="utf-8"))
        self.assertEqual(guard_report["status"], "BLOCKED")
        self.assertEqual(guard_report["exit_code"], 2)

    def test_go_unit_failure_fails_gate_when_unit_selection_falls_back(self):
        # Issue #53 regression: a go.mod-only change empties the affected
        # package set; the widened unit run must still fail the gate when the
        # unit tests fail, instead of returning 0 without running any test.
        write(self.root, "internal/decision/decision_test.go",
              '''package decision

import "testing"

func TestValue(t *testing.T) { if Value != 2 { t.Fatal(Value) } }
              ''')
        new_base = commit(self.root, "add decision test")
        write(self.root, "go.mod", "module example.test/project\n\ngo 1.26\n")
        binaries, marker = self.fake_binaries()
        go = binaries / "go"
        self.fake_go_with_unit_failure(go)
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                         FAKE_MARKER=str(marker))
        result = run_script("--root", str(self.root), "--base", new_base,
                            "--out", str(out), env=env)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertFalse(report["passed"])
        checks = {check["name"]: check for check in report["checks"]}
        self.assertIn("go-unit", checks)
        self.assertEqual(checks["go-unit"]["status"], "FAIL")

    def test_go_unit_failure_fails_gate_when_compile_expands_to_all(self):
        write(self.root, "internal/decision/decision_test.go",
              '''package decision

import "testing"

func TestValue(t *testing.T) { if Value != 2 { t.Fatal(Value) } }
              ''')
        write(self.root, "cmd/app/main.go",
              'package main\n\nimport (\n\t"example.test/project/internal/decision"\n'
              '\t"example.test/project/internal/shared"\n)\n\n'
              'func main() { _ = decision.Value; _ = shared.Value }\n')
        binaries, marker = self.fake_binaries()
        go = binaries / "go"
        self.fake_go_with_unit_failure(go)
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                         FAKE_MARKER=str(marker))
        result = run_script("--root", str(self.root), "--base", self.base,
                            "--out", str(out), env=env)
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
        new_base = commit(self.root, "add tool without unit tests")
        write(self.root, "internal/leaf/leaf.go", "package leaf\n")
        binaries, marker = self.fake_binaries()
        out = Path(self.temporary.name) / "fast-out"
        env = script_env(PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                         FAKE_MARKER=str(marker))
        result = run_script("--root", str(self.root), "--base", new_base,
                            "--out", str(out), env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads((out / "fast-check.json").read_text(encoding="utf-8"))
        self.assertTrue(report["passed"])
        checks = {check["name"]: check for check in report["checks"]}
        self.assertIn("go-unit", checks)
        self.assertEqual(checks["go-unit"]["status"], "NOT_RUN")
        self.assertIn("no database-safe", checks["go-unit"]["reason"])
        self.assertEqual(checks["go-unit"]["command"], "")

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

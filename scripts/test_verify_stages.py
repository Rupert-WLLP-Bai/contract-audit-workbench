"""Tests for verify.py stage selection, evidence cleanup, and CI diagnostics."""
from contextlib import redirect_stderr, redirect_stdout
from io import StringIO
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import verify


def make_check(name, stage, **options):
    return verify.Check(name, stage, ("true",), **options)


class StageSelectionTests(unittest.TestCase):
    def test_all_without_browser_skips_browser_checks(self):
        checks = [make_check("go-tests", "go"), make_check("browser-warranty", "browser")]
        selected = [check.name for check in checks
                    if verify.selected_check(check, "all", False)]
        self.assertEqual(selected, ["go-tests"])

    def test_all_with_browser_includes_browser_checks(self):
        checks = [make_check("go-tests", "go"), make_check("browser-warranty", "browser")]
        selected = [check.name for check in checks
                    if verify.selected_check(check, "all", True)]
        self.assertEqual(selected, ["go-tests", "browser-warranty"])

    def test_specific_stage_selects_only_its_checks(self):
        checks = [make_check("vet", "static"), make_check("go-tests", "go")]
        selected = [check.name for check in checks
                    if verify.selected_check(check, "static", False)]
        self.assertEqual(selected, ["vet"])

    def test_browser_stage_runs_without_the_browser_flag(self):
        self.assertTrue(verify.selected_check(make_check("browser-telecom", "browser"),
                                              "browser", False))

    def test_version_probes_run_only_in_all(self):
        probe = make_check("go-version", "all")
        self.assertTrue(verify.selected_check(probe, "all", False))
        self.assertFalse(verify.selected_check(probe, "static", False))


class CleanupOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.original_root = verify.ROOT
        self.original_out = verify.OUT
        self.original_external = verify.OUT_IS_EXTERNAL
        verify.ROOT = self.root
        self.addCleanup(setattr, verify, "ROOT", self.original_root)
        verify.OUT = self.root / "artifacts"
        verify.OUT_IS_EXTERNAL = False
        self.addCleanup(setattr, verify, "OUT", self.original_out)
        self.addCleanup(setattr, verify, "OUT_IS_EXTERNAL", self.original_external)

    def touch(self, relative):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("x", encoding="utf-8")
        return path

    def test_coverage_cleanup_preserves_go_and_web_inputs(self):
        go_input = self.touch("artifacts/go-coverage.out")
        web_input = self.touch("web/coverage/coverage-summary.json")
        summary = self.touch("artifacts/coverage-summary.json")
        verify.clean_outputs(verify.STAGE_OUTPUTS["coverage"])
        self.assertTrue(go_input.exists())
        self.assertTrue(web_input.exists())
        self.assertFalse(summary.exists())

    def test_owning_stages_clean_their_inputs(self):
        go_input = self.touch("artifacts/go-coverage.out")
        web_coverage = self.root / "web/coverage"
        web_coverage.mkdir(parents=True)
        web_summary = web_coverage / "coverage-summary.json"
        web_summary.write_text("x", encoding="utf-8")
        verify.clean_outputs(verify.STAGE_OUTPUTS["go"])
        self.assertFalse(go_input.exists())
        self.assertTrue(web_summary.exists())
        verify.clean_outputs(verify.STAGE_OUTPUTS["web"])
        self.assertFalse(web_coverage.exists())

    def test_coverage_stage_never_owns_upstream_inputs(self):
        owned = set(verify.STAGE_OUTPUTS["coverage"])
        self.assertNotIn("artifacts/go-coverage.out", owned)
        self.assertNotIn("web/coverage", owned)

    def test_external_cleanup_only_removes_verifier_owned_delivery_outputs(self):
        delivery = self.root / "delivery" / "verification"
        owned = delivery / "go-coverage.out"
        unrelated = self.root / "artifacts" / "keep.json"
        web_coverage = delivery / "web-coverage"
        for path in (owned, unrelated, web_coverage / "coverage-summary.json"):
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("x", encoding="utf-8")
        verify.OUT = delivery
        verify.OUT_IS_EXTERNAL = True
        verify.clean_outputs(("artifacts/go-coverage.out", "web/coverage"))
        self.assertFalse(owned.exists())
        self.assertFalse(web_coverage.exists())
        self.assertTrue(unrelated.exists())


class DeliveryPathTests(unittest.TestCase):
    def test_default_delivery_path_is_outside_source_tree(self):
        root = Path("/workspace/app")
        self.assertEqual(verify.default_output(root, {}),
                         Path("/workspace/contract-audit-delivery/verification"))

    def test_explicit_delivery_path_wins_over_ci_artifact_compatibility(self):
        root = Path("/workspace/app")
        self.assertEqual(verify.default_output(root, {
            "CAW_DELIVERY_DIR": "/tmp/release-evidence", "GITHUB_ACTIONS": "true",
        }), Path("/tmp/release-evidence/verification"))

    def test_ci_keeps_legacy_artifact_collection_path(self):
        root = Path("/workspace/app")
        self.assertEqual(verify.default_output(root, {"GITHUB_ACTIONS": "true"}),
                         root / "artifacts")

    def test_external_run_rewrites_generated_paths_and_coverage_inputs(self):
        original_out = verify.OUT
        original_external = verify.OUT_IS_EXTERNAL
        delivery = Path("/delivery/p0/verification")
        verify.OUT = delivery
        verify.OUT_IS_EXTERNAL = True
        self.addCleanup(setattr, verify, "OUT", original_out)
        self.addCleanup(setattr, verify, "OUT_IS_EXTERNAL", original_external)
        go = verify.Check("go-tests", "go", ("go", "test", "-coverprofile=artifacts/go-coverage.out"))
        self.assertEqual(verify.command_with_output_dir(go)[-1],
                         f"-coverprofile={delivery}/go-coverage.out")
        web = verify.Check("web-tests", "web", ("bun", "test", "--outputFile=../artifacts/vitest-results.json"),
                           cwd=verify.ROOT / "web")
        web_command = verify.command_with_output_dir(web)
        self.assertEqual(web_command[2], f"--outputFile={delivery}/vitest-results.json")
        self.assertIn(f"--coverage.reportsDirectory={delivery}/web-coverage", web_command)
        coverage = verify.Check("coverage-gates", "coverage",
                                ("python3", "scripts/check_coverage.py"))
        command = verify.command_with_output_dir(coverage)
        self.assertIn(f"--web {delivery}/web-coverage/coverage-summary.json", " ".join(command))


class DecoderTests(unittest.TestCase):
    def test_mixed_jsonl_and_plain_text(self):
        text = "\n".join([
            json.dumps({"Action": "output", "Output": "=== RUN   TestOne\n"}),
            "plain diagnostic",
            "[1, 2]",
            json.dumps({"Action": "pass", "Test": "TestOne", "Package": "p"}),
            json.dumps({"Action": "fail", "Test": "TestOne", "Package": "p"}),
            json.dumps({"Action": "fail", "Package": "p"}),
        ])
        self.assertEqual(verify.decode_go_test_log(text), "\n".join([
            "=== RUN   TestOne", "plain diagnostic", "[1, 2]", "FAIL\tp",
        ]))

    def test_output_without_trailing_newline_is_preserved(self):
        text = json.dumps({"Action": "output", "Output": "no newline"})
        self.assertEqual(verify.decode_go_test_log(text), "no newline")

    def test_truncated_timeout_text_is_preserved(self):
        text = "partial\npanic: test timed out after 20m\n"
        self.assertEqual(verify.decode_go_test_log(text),
                         "partial\npanic: test timed out after 20m")


class RunCheckExitSemanticsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.original_root = verify.ROOT
        self.original_out = verify.OUT
        self.original_external = verify.OUT_IS_EXTERNAL
        verify.ROOT = self.root
        verify.OUT = self.root / "delivery" / "verification"
        verify.OUT.mkdir(parents=True)
        verify.OUT_IS_EXTERNAL = True
        self.addCleanup(setattr, verify, "ROOT", self.original_root)
        self.addCleanup(setattr, verify, "OUT", self.original_out)
        self.addCleanup(setattr, verify, "OUT_IS_EXTERNAL", self.original_external)
        self.logs = verify.OUT / "logs"
        self.logs.mkdir()
        self.report = {"stage": "go", "checks": []}

    def run_check(self, check):
        with redirect_stdout(StringIO()), redirect_stderr(StringIO()):
            result = verify.run_check(check, os.environ.copy(), self.report, self.logs)
        return result, self.report["checks"][-1]

    def test_real_exit_seven_is_preserved(self):
        check = verify.Check("exit-seven", "go", (sys.executable, "-c", "raise SystemExit(7)"))
        passed, record = self.run_check(check)
        self.assertFalse(passed)
        self.assertEqual(record["status"], "FAIL")
        self.assertEqual(record["returncode"], 7)
        self.assertEqual(record["command_returncode"], 7)
        self.assertNotIn("error_kind", record)

    def test_timeout_preserves_real_returncode_and_timeout_status(self):
        check = verify.Check("timeout", "go",
                             (sys.executable, "-c", "import time; time.sleep(10)"), timeout=0.05)
        passed, record = self.run_check(check)
        self.assertFalse(passed)
        self.assertEqual(record["status"], "FAIL")
        self.assertIn(record["returncode"], (-signal.SIGTERM, -signal.SIGKILL))
        self.assertEqual(record["command_returncode"], record["returncode"])
        self.assertEqual(record["error_kind"], "TimeoutExpired")
        self.assertTrue(record["timed_out"])

    @staticmethod
    def pid_is_live(pid):
        stat = Path(f"/proc/{pid}/stat")
        if stat.exists():
            value = stat.read_text()
            fields = value[value.rfind(")") + 2:].split()
            return bool(fields) and fields[0] != "Z"
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return False
        return True

    def test_normal_parent_exit_cleans_child_holding_inherited_output(self):
        pid_path = self.root / "child.pid"
        child_code = "import time; time.sleep(60)"
        parent_code = (
            "import pathlib,subprocess,sys; "
            f"p=subprocess.Popen([sys.executable,'-c',{child_code!r}]); "
            f"pathlib.Path({str(pid_path)!r}).write_text(str(p.pid))"
        )
        check = verify.Check("orphan-child", "go", (sys.executable, "-c", parent_code))
        before = time.monotonic()
        passed, record = self.run_check(check)
        self.assertTrue(passed, record)
        self.assertLess(time.monotonic() - before, 4)
        self.assertTrue(record["process_group_cleanup"]["performed"])
        self.assertFalse(record["process_group_cleanup"]["remaining_live_members"])
        child_pid = int(pid_path.read_text())
        self.assertFalse(self.pid_is_live(child_pid))

    def test_timeout_kills_term_ignoring_child_within_bound(self):
        pid_path = self.root / "term-ignoring-child.pid"
        ready_path = self.root / "term-ignoring-child.ready"
        child_code = (
            "import pathlib,signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); "
            f"pathlib.Path({str(ready_path)!r}).write_text('ready'); time.sleep(60)"
        )
        parent_code = (
            "import pathlib,subprocess,sys,time\n"
            f"p=subprocess.Popen([sys.executable,'-c',{child_code!r}])\n"
            f"ready=pathlib.Path({str(ready_path)!r})\n"
            "deadline=time.monotonic()+5\n"
            "while not ready.exists() and time.monotonic()<deadline:\n"
            "    time.sleep(.01)\n"
            f"pathlib.Path({str(pid_path)!r}).write_text(str(p.pid))\n"
            "time.sleep(60)\n"
        )
        check = verify.Check("timeout-child", "go", (sys.executable, "-c", parent_code), timeout=0.2)
        before = time.monotonic()
        passed, record = self.run_check(check)
        elapsed = time.monotonic() - before
        self.assertFalse(passed)
        self.assertTrue(record["timed_out"])
        self.assertEqual(record["returncode"], -signal.SIGTERM)
        self.assertTrue(record["process_group_cleanup"]["sigkill_sent"])
        self.assertFalse(record["process_group_cleanup"]["remaining_live_members"])
        self.assertLess(elapsed, 5)
        self.assertFalse(self.pid_is_live(int(pid_path.read_text())))

    def test_startup_failure_has_no_synthetic_returncode(self):
        check = verify.Check("cannot-start", "go", ("/missing/verify-command",))
        passed, record = self.run_check(check)
        self.assertFalse(passed)
        self.assertEqual(record["status"], "FAIL")
        self.assertIsNone(record["returncode"])
        self.assertIsNone(record["command_returncode"])
        self.assertEqual(record["error_kind"], "FileNotFoundError")
        self.assertIn("/missing/verify-command", record["error_detail"])

    def test_evidence_failure_preserves_successful_process_returncode(self):
        def reject_evidence(_path):
            raise ValueError("expected evidence marker is missing")

        check = verify.Check("bad-evidence", "go",
                             (sys.executable, "-c", "raise SystemExit(0)"), validate=reject_evidence)
        passed, record = self.run_check(check)
        self.assertFalse(passed)
        self.assertEqual(record["status"], "FAIL")
        self.assertEqual(record["returncode"], 0)
        self.assertEqual(record["command_returncode"], 0)
        self.assertIn("EVIDENCE CHECK FAILED", record["failure"])


class AnnotationTests(unittest.TestCase):
    def test_workflow_command_escaping(self):
        self.assertEqual(verify.workflow_escape("a%b\r\nc"), "a%25b%0D%0Ac")
        self.assertEqual(verify.workflow_escape("a:b,c", True), "a%3Ab%2Cc")

    def test_raw_playwright_annotation_is_reemitted(self):
        display = "before\n::error file=web/e2e/c1b-journey.ts,line=491::boom\nafter"
        output = StringIO()
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}, clear=False), \
                redirect_stdout(output):
            verify.emit_failure_annotations("browser-telecom", display)
        self.assertIn("::error file=web/e2e/c1b-journey.ts,line=491::boom",
                      output.getvalue())

    def test_stack_frame_synthesizes_file_annotation(self):
        display = (
            "error: no visible select option matched x\n"
            "    at chooseAntOption (/home/runner/work/repo/repo/web/e2e/c1b-journey.ts:491:32)\n"
        )
        output = StringIO()
        environment = {
            "GITHUB_ACTIONS": "true",
            "GITHUB_WORKSPACE": "/home/runner/work/repo/repo",
        }
        with patch.dict(os.environ, environment, clear=False), redirect_stdout(output):
            verify.emit_failure_annotations("browser-telecom", display)
        value = output.getvalue()
        self.assertIn("::error file=web/e2e/c1b-journey.ts,line=491,col=32", value)
        self.assertIn("title=browser-telecom", value)

    def test_go_failure_gets_both_test_and_file_annotations(self):
        display = (
            "--- FAIL: TestThing (0.10s)\n"
            "    thing_test.go:12: boom\n"
            "FAIL\texample/internal/thing\n"
        )
        output = StringIO()
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}, clear=False), \
                redirect_stdout(output):
            verify.emit_failure_annotations("go-tests", display)
        value = output.getvalue()
        self.assertIn("title=go-tests::--- FAIL: TestThing", value)
        self.assertNotIn("file=", value)

    def test_no_annotation_outside_actions(self):
        output = StringIO()
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "false"}, clear=False), \
                redirect_stdout(output):
            verify.emit_failure_annotations("x", "--- FAIL: TestX")
        self.assertEqual(output.getvalue(), "")


class StepSummaryTests(unittest.TestCase):
    def test_summary_lists_pass_fail_and_not_run(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / "summary.md"
            report = {
                "stage": "go",
                "passed": False,
                "source_identity": {"digest": "abc"},
                "checks": [
                    {"name": "go-tests", "status": "PASS", "seconds": 1.5,
                     "log": "artifacts/logs/go-tests.txt"},
                    {"name": "web-tests", "status": "NOT_RUN", "seconds": 0,
                     "log": ""},
                ],
                "error": "go-tests failed",
            }
            with patch.dict(os.environ,
                            {"GITHUB_STEP_SUMMARY": str(destination)}, clear=False):
                verify.write_step_summary(report)
            text = destination.read_text(encoding="utf-8")
            self.assertIn("## go gate: FAIL", text)
            self.assertIn("source identity: `abc`", text)
            self.assertIn("| go-tests | PASS | 1.5 |", text)
            self.assertIn("| web-tests | NOT_RUN | 0 |", text)
            self.assertIn("error: go-tests failed", text)

    def test_summary_is_skipped_without_destination(self):
        with patch.dict(os.environ, {}, clear=True):
            verify.write_step_summary({"stage": "all", "checks": [], "passed": True})


class ReportPathTests(unittest.TestCase):
    def test_all_keeps_legacy_report_name(self):
        self.assertEqual(verify.report_path("all").name, "verification.json")

    def test_stage_reports_are_distinct(self):
        self.assertEqual(verify.report_path("go").name, "verification-go.json")
        self.assertNotEqual(verify.report_path("go"), verify.report_path("web"))


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.checks = {check.name: check
                       for check in verify.build_checks("go", "bun")}

    def test_expected_checks_and_stages(self):
        expected = {
            "go-version": "all",
            "bun-version": "all",
            "node-version": "all",
            "quality-tests": "static",
            "infra-tests": "static",
            "fault-harness-tests": "static",
            "format": "static",
            "modules": "static",
            "vet": "static",
            "go-tests": "go",
            "backend-build": "go",
            "backend-build-worker": "go",
            "backend-build-toolgateway": "go",
            "backend-build-migrate": "go",
            "evaluate": "go",
            "agent-demo": "go",
            "rule-shadow": "go",
            "web-typecheck": "web",
            "web-tests": "web",
            "web-cdp-helper-tests": "web",
            "web-build": "web",
            "web-c1b-clock-tests": "browser",
            "coverage-gates": "coverage",
            "browser-warranty": "browser",
            "browser-telecom": "browser",
            "browser-telecom-compatible": "browser",
            "browser-telecom-insufficient": "browser",
        }
        self.assertEqual({name: check.stage
                          for name, check in self.checks.items()}, expected)

    def test_browser_checks_continue_after_failure(self):
        self.assertTrue(self.checks["browser-warranty"].continue_on_failure)
        self.assertTrue(self.checks["browser-telecom"].continue_on_failure)
        self.assertTrue(self.checks["browser-telecom-compatible"].continue_on_failure)
        self.assertTrue(self.checks["browser-telecom-insufficient"].continue_on_failure)

    def test_browser_checks_are_split(self):
        names = [name for name, check in self.checks.items()
                 if check.stage == "browser"]
        self.assertEqual(names, ["web-c1b-clock-tests", "browser-warranty", "browser-telecom",
                                 "browser-telecom-compatible", "browser-telecom-insufficient"])

    def test_jsonl_checks_keep_raw_log(self):
        for name in ("go-tests", "browser-warranty", "browser-telecom",
                     "browser-telecom-compatible", "browser-telecom-insufficient"):
            self.assertTrue(self.checks[name].jsonl)

    def test_evidence_validators_are_attached(self):
        for name in ("go-tests", "web-tests", "agent-demo", "rule-shadow"):
            self.assertIsNotNone(self.checks[name].validate)

    def test_web_test_timeout_covers_measured_full_suite_runtime(self):
        self.assertEqual(self.checks["web-tests"].timeout, 900)

    def test_cdp_helper_tests_run_in_default_web_gate(self):
        check = self.checks["web-cdp-helper-tests"]
        self.assertEqual(check.cmd, ("bun", "run", "test:cdp-helper"))
        self.assertEqual(check.cwd, verify.ROOT / "web")
        self.assertTrue(verify.selected_check(check, "web", False))
        self.assertFalse(verify.selected_check(check, "browser", False))


if __name__ == "__main__":
    unittest.main()

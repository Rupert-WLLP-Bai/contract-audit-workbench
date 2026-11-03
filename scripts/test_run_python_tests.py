#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Checks recursive test discovery, module isolation, skipped and expected failures, empty selections and timeout diagnostics using disposable Python test fixtures.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-03
# Updated: 2026-11-03
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Tests for the external Python unittest runner candidate."""
from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

RUNNER = Path(__file__).with_name("run_python_tests.py")


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def execute(*roots: Path, timeout: float = 300.0):
    command = [sys.executable, str(RUNNER), "--timeout", str(timeout)]
    for root in roots:
        command.extend(("--root", str(root)))
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    return result.returncode, json.loads(result.stdout)


class PythonTestRunnerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="caw-python-runner-")
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)

    def test_nested_discovery_exclusions_and_per_file_import_isolation(self):
        root_a, root_b = self.base / "a", self.base / "b"
        write(root_a / "shared_helper.py", "VALUE = 'a'\n")
        write(root_b / "shared_helper.py", "VALUE = 'b'\n")
        write(root_a / "nested" / "test_same.py",
              "import unittest\nimport shared_helper\n"
              "class NestedCase(unittest.TestCase):\n"
              " def test_root_a_helper(self): self.assertEqual(shared_helper.VALUE, 'a')\n")
        write(root_b / "deep" / "test_same.py",
              "import unittest\nimport shared_helper\n"
              "class NestedCase(unittest.TestCase):\n"
              " def test_root_b_helper(self): self.assertEqual(shared_helper.VALUE, 'b')\n")
        write(root_a / "node_modules" / "ignored" / "test_ignored.py",
              "raise RuntimeError('excluded directory was imported')\n")

        code, report = execute(root_a, root_b)

        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "PASS")
        self.assertEqual(report["discovered_files"], 2)
        self.assertEqual(report["tests_run"], 2)
        self.assertEqual([row["status"] for row in report["files"]], ["PASS", "PASS"])

    def test_skip_is_reported_as_failure_and_returns_nonzero(self):
        skipped = self.base / "skipped"
        write(skipped / "test_skip.py",
              "import unittest\nclass SkipCase(unittest.TestCase):\n"
              " @unittest.skip('fixture skip')\n"
              " def test_skip(self): pass\n")

        code, report = execute(skipped)

        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["skipped"], 1)
        self.assertEqual(report["files"][0]["status"], "FAIL")

    def test_failure_propagates_to_parent_exit_and_json(self):
        failing = self.base / "failing"
        write(failing / "test_failure.py",
              "import unittest\nclass FailureCase(unittest.TestCase):\n"
              " def test_failure(self): self.fail('fixture failure marker')\n")

        code, report = execute(failing)

        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["failures"], 1)
        self.assertIn("fixture failure marker", report["files"][0]["failure_details"][0]["traceback"])

    def test_empty_scope_is_nonzero_and_not_run(self):
        empty = self.base / "empty"
        empty.mkdir()

        code, report = execute(empty)

        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "NOT_RUN")
        self.assertEqual(report["tests_run"], 0)

    def test_expected_failure_is_nonzero_and_keeps_its_reason(self):
        expected = self.base / "expected"
        write(expected / "test_expected.py",
              "import unittest\nclass ExpectedCase(unittest.TestCase):\n"
              " @unittest.expectedFailure\n"
              " def test_expected(self): self.fail('expected failure marker')\n")

        code, report = execute(expected)

        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["expected_failures"], 1)
        self.assertIn("expected failure marker",
                      report["files"][0]["expected_failure_details"][0]["traceback"])
        self.assertEqual(report["files"][0]["failure_details"][0]["kind"], "expected_failure")

    def test_timeout_is_bounded_and_preserves_child_output(self):
        slow = self.base / "slow"
        write(slow / "test_slow.py",
              "import os\nimport time\n"
              "os.write(2, b'fixture-before-timeout\\n')\n"
              "time.sleep(2)\n")

        code, report = execute(slow, timeout=0.2)

        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "FAIL")
        row = report["files"][0]
        self.assertTrue(row["timed_out"])
        self.assertEqual(row["timeout_seconds"], 0.2)
        self.assertIn("fixture-before-timeout", row["child_stderr"])
        self.assertEqual(row["failure_details"][0]["kind"], "timeout")


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Unit tests for the read-only UI style clue scanner and in-repo UI2 coverage matrix schema.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-08
# Updated: 2026-11-08
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from ui_style_scan import scan


ROOT = Path(__file__).resolve().parents[1]
MATRIX = ROOT / "docs/engineering/ui-coverage-matrix.json"


class UIStyleScanTests(unittest.TestCase):
    def test_scanner_reports_clues_without_failing_on_repo(self) -> None:
        report = scan(ROOT)
        self.assertEqual(report["status"], "PASS")
        self.assertEqual(report["mode"], "clues-only")
        self.assertGreater(report["files_scanned"], 50)
        self.assertIn("clue_counts", report)

    def test_scanner_finds_fixture_clues(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            src = root / "web" / "src"
            src.mkdir(parents=True)
            (src / "demo.css").write_text(
                ".x{color:#ff0000;font-size:13px;overflow:hidden !important}\n",
                encoding="utf-8",
            )
            (src / "demo.tsx").write_text("<button type=\"button\">Go</button>\n", encoding="utf-8")
            report = scan(root)
            kinds = {c["kind"] for c in report["clues"]}
            self.assertIn("hardcoded-color", kinds)
            self.assertIn("hardcoded-size", kinds)
            self.assertIn("important", kinds)
            self.assertIn("native-control", kinds)


class UICoverageMatrixTests(unittest.TestCase):
    def test_matrix_registers_fifty_four_views_with_existing_sources(self) -> None:
        data = json.loads(MATRIX.read_text(encoding="utf-8"))
        self.assertEqual(data["schema_version"], 1)
        views = data["views"]
        self.assertEqual(len(views), 54)
        self.assertEqual(data["counts"]["design_views"], 54)
        allowed = {"PARTIAL", "BLOCKED", "NOT_RUN", "PASS", "FAIL"}
        for view in views:
            self.assertIn(view["status"], allowed)
            self.assertTrue(view["designId"].startswith("UI-"))
            for source in view["sources"]:
                self.assertTrue((ROOT / source).exists(), msg=source)
        self.assertEqual(data["acceptance"]["matrix_registered"], "PASS")
        self.assertEqual(data["acceptance"]["source_paths_exist"], "PASS")
        self.assertEqual(data["acceptance"]["fixed_candidate_five_stage"], "NOT_RUN")
        by_status = data["counts"]["by_status"]
        self.assertEqual(by_status.get("BLOCKED"), 2)
        self.assertEqual(by_status.get("PARTIAL"), 52)


if __name__ == "__main__":
    unittest.main()

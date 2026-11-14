# File metadata (manifest mirror):
# Purpose: Regression checks for test_manifest_rejects_traversal_and_digest_mismatch, test_cli_requires_recorder_before_creating_a_run, test_pending_parsed_once_and_review_evidence_retained, test_precheck_accepts_designed_partial_frozen_attempt, test_precheck_rejects_unfinished_attempt; they assert the deployment, parser, or repository-policy contract that the corresponding implementation must preserve.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-27
# Updated: 2026-11-12
# Revision: r2
# Copyright: See LICENSE
# End file metadata.

import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import argparse
import subprocess
import sys

import run_real_corpus as runner


def diagnostics_result(events, case_id="case", run_id="run", attempt_id="attempt", state="completed"):
    return {"status": "PASS", "response": {
        "case_id": case_id,
        "run_id": run_id,
        "execution": {
            "attempt": {"id": attempt_id, "run_id": run_id, "state": state},
            "events": [{"attempt_id": attempt_id, **event} for event in events],
        },
    }}


class RealCorpusRunnerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.original = self.root / "contract.pdf"
        self.original.write_bytes(b"original contract bytes")
        self.manifest_path = self.root / "manifest.json"
        self.manifest = {"version": 1, "dataset_id": "real", "cases": [{
            "id": "one", "title": "contract", "contract_type": "service", "materials": [{
                "role": "draft_contract", "path": "contract.pdf", "sha256": hashlib.sha256(self.original.read_bytes()).hexdigest(),
                "data_class": "original", "source": "source reference",
            }],
        }]}
        self.write_manifest()

    def write_manifest(self):
        self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")

    def args(self):
        return argparse.Namespace(manifest=self.manifest_path, case_id=None, base="http://localhost", username=None,
                                  env_file=self.root / "absent", review_timeout=10, request_timeout=10,
                                  model_record_dir=None, model_record_time="record-time", review_scope="full_review")

    def test_manifest_rejects_traversal_and_digest_mismatch(self):
        self.manifest["cases"][0]["materials"][0]["path"] = "../contract.pdf"
        self.write_manifest()
        with self.assertRaises(runner.RunnerError):
            runner.load_manifest(self.manifest_path)
        self.manifest["cases"][0]["materials"][0]["path"] = "contract.pdf"
        self.manifest["cases"][0]["materials"][0]["sha256"] = "0" * 64
        self.write_manifest()
        _, manifest = runner.load_manifest(self.manifest_path)
        with self.assertRaises(runner.RunnerError):
            runner.material_bytes(manifest["cases"][0]["materials"][0])

    def test_cli_requires_recorder_before_creating_a_run(self):
        delivery = self.root / "delivery"
        env = os.environ.copy()
        env["CAW_DELIVERY_DIR"] = str(delivery)
        command = [sys.executable, str(Path(runner.__file__)), "--manifest", str(self.manifest_path),
                   "--base", "http://127.0.0.1:1"]
        for extra in ([], ["--model-record-dir", str(self.root / "missing")]):
            with self.subTest(extra=extra):
                completed = subprocess.run(command + extra, env=env, capture_output=True, text=True, timeout=10)
                self.assertEqual(completed.returncode, 2)
                self.assertIn("--model-record-dir", completed.stderr)
                self.assertFalse(delivery.exists())

    def test_pending_parsed_once_and_review_evidence_retained(self):
        class FakeAPI:
            def __init__(self, base, timeout):
                self.parsed = False
                self.parse_calls = 0
            def upload(self, case_id, material):
                self.assert_material = runner.material_bytes(material)
                return {"id": "doc"}
            def request(self, method, path, payload=None, **kwargs):
                if path == "/api/session":
                    return {"actor": {"id": "actor"}}
                if path == "/api/cases":
                    return {"id": "case"}
                if path.endswith("/parse"):
                    self.parse_calls += 1
                    self.parsed = True
                    return {"id": "doc"}
                if path == "/api/cases/case":
                    return {"documents": [{"id": "doc", "parse_status": "ready" if self.parsed else "pending"}]}
                if method == "POST" and path.endswith("/reviews"):
                    return {"id": "run", "attempt_id": "attempt"}
                if path.endswith("/diagnostics?attempt_id=attempt"):
                    return {"case_id": "case", "run_id": "run",
                            "execution": {"attempt": {"id": "attempt", "run_id": "run", "state": "completed"},
                                          "events": [{"attempt_id": "attempt", "node_kind": "model_call", "phase": "planner", "event_type": "model_completed"}]}}
                return {"run": {"id": "run", "state": "awaiting_human"}, "snapshot": {"document_ids": ["doc"]},
                        "findings": [{"id": "finding", "references": ["doc"]}],
                        "execution": None,
                        "review_attempts": {"attempt": {"id": "attempt", "run_id": "run", "state": "completed"}}}
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}), patch.object(runner, "API", FakeAPI):
            output = runner.run(self.args())
        result = json.loads((output / "result.json").read_text())
        self.assertEqual(result["status"], "complete")
        self.assertEqual(result["cases"][0]["materials"][0]["parse_status"], "ready")
        self.assertEqual(result["cases"][0]["workspace"]["snapshot"]["document_ids"], ["doc"])
        self.assertIsNone(result["cases"][0]["workspace"]["execution"])
        self.assertEqual(result["cases"][0]["diagnostics"]["response"]["case_id"], "case")
        self.assertEqual(result["cases"][0]["attempt_id"], "attempt")
        self.assertEqual(result["cases"][0]["model_call_purposes"], {"planner": 1})
        self.assertEqual(result["model_record_time"], "record-time")
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)

    def test_precheck_accepts_designed_partial_frozen_attempt(self):
        class FakeAPI:
            def __init__(self, base, timeout):
                self.diagnostics_path = None
            def upload(self, case_id, material):
                return {"id": "doc"}
            def request(self, method, path, payload=None, **kwargs):
                if path == "/api/session":
                    return {"actor": {"id": "actor"}}
                if path == "/api/cases":
                    return {"id": "case"}
                if path == "/api/cases/case":
                    return {"documents": [{"id": "doc", "parse_status": "ready"}]}
                if method == "POST" and path.endswith("/reviews"):
                    self.scope = (payload or {}).get("scope")
                    return {"id": "run", "attempt_id": "attempt"}
                if path.endswith("/diagnostics?attempt_id=attempt"):
                    self.diagnostics_path = path
                    return {"case_id": "case", "run_id": "run",
                            "execution": {"attempt": {"id": "attempt", "run_id": "run", "state": "partial"},
                                          "events": [
                                              {"attempt_id": "attempt", "node_kind": "model_call", "phase": "contract_facts", "event_type": "model_completed"},
                                              {"attempt_id": "attempt", "node_kind": "model_call", "phase": "ocr", "event_type": "model_completed"},
                                          ]}}
                return {"run": {"id": "run", "state": "partial", "partial_reason": "单合同预审仅核对已识别的付款比例"},
                        "snapshot": {"document_ids": ["doc"]},
                        "documents": [{"id": "doc", "parsed_document_id": "parsed-doc"}],
                        "execution": None,
                        "findings": [{"id": "finding"}], "evaluations": [{"id": "evaluation"}],
                        "contract_facts": {"schema_version": "contract-facts/v1", "attempt_id": "attempt",
                                           "status": "partial", "structured_output_status": "valid",
                                           "input_codepoints": 8, "input_truncated": False,
                                           "source": {"document_id": "doc", "parsed_document_id": "parsed-doc",
                                                      "original_digest": "a" * 64, "normalized_digest": "b" * 64},
                                           "fields": [{"field": field, "status": "unknown", "value": "", "citations": []}
                                                      for field in sorted(runner.CONTRACT_FACT_FIELDS)]},
                        "review_attempts": {"attempt": {"id": "attempt", "run_id": "run", "finished_at": "t",
                                                        "history_kind": "frozen", "frozen_result_available": True,
                                                        "review_scope": {"id": "single_contract_precheck",
                                                                         "rule_set_id": "single-contract-precheck"}}}}
        api = FakeAPI("http://localhost", 10)
        args = self.args()
        args.review_scope = "single_contract_precheck"
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}):
            with patch.object(runner, "API", lambda base, timeout: api):
                output = runner.run(args)
        result = json.loads((output / "result.json").read_text())
        self.assertEqual(result["status"], "complete")
        self.assertEqual(api.scope, "single_contract_precheck")
        case = result["cases"][0]
        self.assertEqual(case["run_state"], "partial")
        self.assertEqual(case["precheck"]["findings"], 1)
        self.assertEqual(case["precheck"]["rule_set"], "single-contract-precheck")
        self.assertEqual(case["precheck"]["history_kind"], "frozen")
        self.assertEqual(case["model_call_acceptance"]["status"], "NOT_APPLICABLE")
        facts = case["contract_facts_acceptance"]
        self.assertEqual(facts["request_completion"]["status"], "PASS")
        self.assertEqual(facts["structured_output"]["status"], "PASS")
        self.assertEqual(facts["golden_fact_quality"]["status"], "NOT_RUN")
        self.assertEqual(facts["structured_output"]["coverage"]["unknown_fields"], sorted(runner.CONTRACT_FACT_FIELDS))
        self.assertEqual(case["model_call_purposes"], {"contract_facts": 1, "ocr": 1})
        self.assertIsNone(case["workspace"]["execution"])
        self.assertEqual(api.diagnostics_path, "/api/cases/case/reviews/run/diagnostics?attempt_id=attempt")
        self.assertEqual(case["diagnostics"]["response"]["execution"]["attempt"]["id"], case["attempt_id"])

    def test_facts_projection_without_contract_facts_event_is_not_a_completed_model_call(self):
        workspace = {"contract_facts": {"schema_version": "contract-facts/v1", "attempt_id": "attempt",
                                        "status": "partial", "structured_output_status": "valid",
                                        "input_codepoints": 8, "input_truncated": False,
                                        "source": {"document_id": "doc", "parsed_document_id": "parsed-doc",
                                                   "original_digest": "a" * 64, "normalized_digest": "b" * 64},
                                        "fields": [{"field": field, "status": "unknown", "value": "", "citations": []}
                                                   for field in sorted(runner.CONTRACT_FACT_FIELDS)]},
                    "documents": [{"id": "doc", "parsed_document_id": "parsed-doc"}],
                    "execution": None}
        diagnostics = diagnostics_result([{"node_kind": "model_call", "phase": "ocr", "event_type": "model_completed"}])
        result = runner.contract_facts_acceptance(workspace, diagnostics, "case", "run", "attempt")
        self.assertEqual(result["request_completion"]["status"], "NOT_RUN")
        self.assertEqual(result["structured_output"]["status"], "NOT_RUN")
        self.assertEqual(result["golden_fact_quality"]["status"], "NOT_RUN")
        self.assertIn("synchronous and legacy", result["request_completion"]["reason"])

    def test_facts_http_completion_with_invalid_json_fails_structured_output(self):
        fields = [{"field": field, "status": "unknown", "value": "", "citations": []}
                  for field in sorted(runner.CONTRACT_FACT_FIELDS)]
        workspace = {
            "contract_facts": {
                "schema_version": "contract-facts/v1", "attempt_id": "attempt", "status": "partial",
                "structured_output_status": "invalid", "input_codepoints": 8, "input_truncated": False,
                "source": {"document_id": "doc", "parsed_document_id": "parsed-doc",
                           "original_digest": "a" * 64, "normalized_digest": "b" * 64}, "fields": fields,
            },
            "documents": [{"id": "doc", "parsed_document_id": "parsed-doc"}],
            "execution": None,
        }
        diagnostics = diagnostics_result([{"node_kind": "model_call", "phase": "contract_facts", "event_type": "model_completed"}])
        result = runner.contract_facts_acceptance(workspace, diagnostics, "case", "run", "attempt")
        self.assertEqual(result["request_completion"]["status"], "PASS")
        self.assertEqual(result["structured_output"]["status"], "FAIL")
        self.assertIn("invalid structured JSON", result["structured_output"]["reason"])
        self.assertEqual(result["golden_fact_quality"]["status"], "NOT_RUN")

    def test_facts_unknown_fields_and_truncation_report_coverage_without_quality_pass(self):
        source = {"document_id": "doc", "parsed_document_id": "parsed-doc",
                  "original_digest": "a" * 64, "normalized_digest": "b" * 64}
        citation = {**source, "quote": "合同", "start": 0, "end": 2}
        fields = [
            {"field": field, "status": "unknown", "value": "", "citations": []}
            if field == "term" else
            {"field": field, "status": "known", "value": "合同", "citations": [citation]}
            for field in sorted(runner.CONTRACT_FACT_FIELDS)
        ]
        workspace = {
            "contract_facts": {
                "schema_version": "contract-facts/v1", "attempt_id": "attempt", "status": "partial",
                "structured_output_status": "valid", "input_codepoints": 8, "input_truncated": True,
                "source": source, "fields": fields,
            },
            "documents": [{"id": "doc", "parsed_document_id": "parsed-doc"}],
            "execution": None,
        }
        diagnostics = diagnostics_result([{"node_kind": "model_call", "phase": "contract_facts", "event_type": "model_completed"}])
        result = runner.contract_facts_acceptance(workspace, diagnostics, "case", "run", "attempt")
        self.assertEqual(result["request_completion"]["status"], "PASS")
        self.assertEqual(result["structured_output"]["status"], "PASS")
        self.assertEqual(result["structured_output"]["coverage"]["unknown_fields"], ["term"])
        self.assertTrue(result["structured_output"]["coverage"]["input_truncated"])
        self.assertEqual(result["golden_fact_quality"]["status"], "NOT_RUN")

    def test_invalid_projection_fails_even_when_diagnostics_are_denied(self):
        workspace = {"contract_facts": {
            "schema_version": "contract-facts/v1", "attempt_id": "attempt",
            "structured_output_status": "invalid",
        }, "execution": None}
        result = runner.contract_facts_acceptance(
            workspace, {"status": "BLOCKED", "reason": "diagnostics access was denied"},
            "case", "run", "attempt")
        self.assertEqual(result["request_completion"]["status"], "BLOCKED")
        self.assertEqual(result["structured_output"]["status"], "FAIL")

    def test_diagnostics_access_errors_do_not_imply_that_no_model_call_occurred(self):
        for code, expected_status in ((403, "BLOCKED"), (404, "NOT_RUN")):
            with self.subTest(code=code):
                class FakeAPI:
                    def __init__(self, status):
                        self.status = status
                    def request(self, method, path):
                        raise runner.RunnerError(f"{method} {path} returned HTTP {self.status}")
                diagnostics = runner.request_diagnostics(FakeAPI(code), "case", "run", "attempt")
                result = runner.contract_facts_acceptance({}, diagnostics, "case", "run", "attempt")
                self.assertEqual(result["request_completion"]["status"], expected_status)
                self.assertEqual(result["structured_output"]["status"], "NOT_RUN")

    def test_diagnostics_rejects_foreign_attempt_and_event_identity(self):
        class FakeAPI:
            def request(self, method, path):
                return {"case_id": "case", "run_id": "run",
                        "execution": {"attempt": {"id": "attempt", "run_id": "run"},
                                      "events": [{"attempt_id": "foreign", "node_kind": "model_call",
                                                  "phase": "contract_facts", "event_type": "model_completed"}]}}
        result = runner.request_diagnostics(FakeAPI(), "case", "run", "attempt")
        self.assertEqual(result["status"], "BLOCKED")
        self.assertEqual(result["reason"], "diagnostics events are not bound to the accepted attempt")

        class ForeignAttemptAPI:
            def request(self, method, path):
                return {"case_id": "case", "run_id": "run",
                        "execution": {"attempt": {"id": "foreign", "run_id": "run"},
                                      "events": [{"attempt_id": "foreign", "node_kind": "model_call",
                                                  "phase": "contract_facts", "event_type": "model_completed"}]}}
        result = runner.request_diagnostics(ForeignAttemptAPI(), "case", "run", "attempt")
        self.assertEqual(result["status"], "BLOCKED")
        self.assertEqual(result["reason"], "diagnostics response attempt identity does not match")

    def test_precheck_rejects_unfinished_attempt(self):
        class FakeAPI:
            def __init__(self, base, timeout):
                pass
            def upload(self, case_id, material):
                return {"id": "doc"}
            def request(self, method, path, payload=None, **kwargs):
                if path == "/api/session":
                    return {"actor": {"id": "actor"}}
                if path == "/api/cases":
                    return {"id": "case"}
                if path == "/api/cases/case":
                    return {"documents": [{"id": "doc", "parse_status": "ready"}]}
                if method == "POST" and path.endswith("/reviews"):
                    return {"id": "run", "attempt_id": "attempt"}
                if path.endswith("/diagnostics?attempt_id=attempt"):
                    return {"case_id": "case", "run_id": "run",
                            "execution": {"attempt": {"id": "attempt", "run_id": "run", "state": "partial"}, "events": []}}
                return {"run": {"id": "run", "state": "partial"}, "snapshot": {"document_ids": ["doc"]},
                        "review_attempts": {"attempt": {"id": "attempt", "run_id": "run", "finished_at": "t",
                                                        "history_kind": "frozen", "frozen_result_available": False,
                                                        "review_scope": {"id": "single_contract_precheck"}}}}
        args = self.args()
        args.review_scope = "single_contract_precheck"
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}):
            with patch.object(runner, "API", FakeAPI):
                with self.assertRaises(runner.RunnerError):
                    runner.run(args)
        result = json.loads(next((self.root / "delivery").glob("test-runs/real-corpus-*/result.json")).read_text())
        self.assertEqual(result["cases"][0]["status"], "failed")

    def precheck_workspace(self, **attempt):
        base = {"id": "attempt", "run_id": "run", "finished_at": "t", "history_kind": "frozen",
                "frozen_result_available": True,
                "review_scope": {"id": "single_contract_precheck", "rule_set_id": "single-contract-precheck"}}
        base.update(attempt)
        return {"run": {"id": "run", "state": "partial"}, "snapshot": {"document_ids": ["doc"]},
                "review_attempts": {"attempt": base}}

    def run_precheck_workspace(self, workspace):
        class FakeAPI:
            def __init__(self, base, timeout):
                pass
            def upload(self, case_id, material):
                return {"id": "doc"}
            def request(self, method, path, payload=None, **kwargs):
                if path == "/api/session":
                    return {"actor": {"id": "actor"}}
                if path == "/api/cases":
                    return {"id": "case"}
                if path == "/api/cases/case":
                    return {"documents": [{"id": "doc", "parse_status": "ready"}]}
                if method == "POST" and path.endswith("/reviews"):
                    return {"id": "run", "attempt_id": "attempt"}
                if path.endswith("/diagnostics?attempt_id=attempt"):
                    return {"case_id": "case", "run_id": "run",
                            "execution": {"attempt": {"id": "attempt", "run_id": "run", "state": "partial"}, "events": []}}
                return workspace
        args = self.args()
        args.review_scope = "single_contract_precheck"
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}):
            with patch.object(runner, "API", FakeAPI):
                with self.assertRaises(runner.RunnerError):
                    runner.run(args)

    def test_precheck_rejects_foreign_attempt_identity(self):
        self.run_precheck_workspace(self.precheck_workspace(id="other-attempt"))

    def test_precheck_rejects_unfrozen_history(self):
        self.run_precheck_workspace(self.precheck_workspace(history_kind="live"))

    def test_invalid_input_leaves_failure_checkpoint(self):
        self.original.write_bytes(b"changed")
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}):
            with self.assertRaises(runner.RunnerError):
                runner.run(self.args())
        result_path = next((self.root / "delivery" / "test-runs").glob("*/result.json"))
        result = json.loads(result_path.read_text())
        self.assertEqual(result["status"], "failed")
        self.assertIn("SHA-256 mismatch", result["error"])
        self.assertEqual(result["cases"], [])


if __name__ == "__main__":
    unittest.main()

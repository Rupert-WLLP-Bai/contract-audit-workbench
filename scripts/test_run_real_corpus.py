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
                return {"run": {"state": "awaiting_human"}, "snapshot": {"document_ids": ["doc"]},
                        "findings": [{"id": "finding", "references": ["doc"]}],
                        "execution": {"attempt": {"id": "attempt", "state": "completed"},
                                      "events": [{"node_kind": "model_call", "event_type": "model_completed"}]}}
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}), patch.object(runner, "API", FakeAPI):
            output = runner.run(self.args())
        result = json.loads((output / "result.json").read_text())
        self.assertEqual(result["status"], "complete")
        self.assertEqual(result["cases"][0]["materials"][0]["parse_status"], "ready")
        self.assertEqual(result["cases"][0]["workspace"]["snapshot"]["document_ids"], ["doc"])
        self.assertEqual(result["cases"][0]["attempt_id"], "attempt")
        self.assertEqual(result["model_record_time"], "record-time")
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)

    def test_precheck_accepts_designed_partial_frozen_attempt(self):
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
                    self.scope = (payload or {}).get("scope")
                    return {"id": "run", "attempt_id": "attempt"}
                return {"run": {"state": "partial", "partial_reason": "单合同预审仅核对已识别的付款比例"},
                        "snapshot": {"document_ids": ["doc"]},
                        "findings": [{"id": "finding"}], "evaluations": [{"id": "evaluation"}],
                        "review_attempts": {"attempt": {"id": "attempt", "finished_at": "t",
                                                        "frozen_result_available": True,
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
                return {"run": {"state": "partial"}, "snapshot": {"document_ids": ["doc"]},
                        "review_attempts": {"attempt": {"id": "attempt", "finished_at": "t",
                                                        "frozen_result_available": False,
                                                        "review_scope": {"id": "single_contract_precheck"}}}}
        args = self.args()
        args.review_scope = "single_contract_precheck"
        with patch.dict(os.environ, {"CAW_DELIVERY_DIR": str(self.root / "delivery")}):
            with patch.object(runner, "API", FakeAPI):
                with self.assertRaises(runner.RunnerError):
                    runner.run(args)
        result = json.loads(next((self.root / "delivery").glob("test-runs/real-corpus-*/result.json")).read_text())
        self.assertEqual(result["cases"][0]["status"], "failed")

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

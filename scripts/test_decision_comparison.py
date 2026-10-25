"""Permanent regression coverage for the decision-comparison contract."""

import copy
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from compare_decisions import (
    ComparisonError,
    build_comparison,
    read_json_file,
    summarize_group,
)


BASELINE_SHA = "a" * 64
REQUEST_SHA = "b" * 64
DATASET_SHA = "c" * 64
CANDIDATES = ["bandwidth", "monthly_sla", "advance_payment", "manual_review"]
MANDATORY_IDS = ["ENT-COM-ADV-001", "ENT-TECH-BW-001", "ENT-TECH-SLA-001"]


def make_dataset(expected_labels=("bandwidth", "monthly_sla")):
    return {
        "schema_version": "decision-benchmark/v1",
        "classification": "D0_SYNTHETIC_ZH_REVIEW_FOCUS",
        "candidates": [{"id": label, "description": label} for label in CANDIDATES],
        "cases": [{"id": f"S-{index + 1}", "expected": label}
                  for index, label in enumerate(expected_labels)],
    }


def probabilities(winner):
    values = {label: 0.05 for label in CANDIDATES}
    values[winner] = 0.85
    return values


def make_row(sample_id, expected, backend, status=None, choice=None, usage=None, probs=None,
             failure_reason="", rules_unchanged=True, output_sha=BASELINE_SHA):
    mode = "disabled" if backend == "rules_only" else "shadow"
    status = status or ("disabled" if backend == "rules_only" else "suggested")
    suggestion = None
    if choice is not None:
        suggestion = {"candidate_id": choice, "probabilities": probs,
                      "confidence": None, "confidence_source": "not_reported",
                      "response_model": "observed-model", "upstream_abstained": status == "abstained"}
    decision = {
        "status": status,
        "accepted_rules": [],
        "event": {
            "request_id": sample_id, "decision_id": f"decision-{sample_id}",
            "run_id": "run-fixture", "attempt_id": f"attempt-{sample_id}",
            "trace_id": f"trace-{sample_id}", "tenant_id": "tenant-fixture",
            "case_id": "case-fixture", "actor_id": "actor-fixture",
            "capability": "choice", "mode": mode, "status": status,
            "model_name": "configured-model" if backend != "rules_only" else "",
            "response_model": "observed-model" if choice else "",
            "latency_ms": 3.0 if backend != "rules_only" else 0.0,
            "failure_reason": failure_reason,
            "rulepack_version": "1", "rulepack_hash": "d" * 64,
            "policy_version": "case-scope-v1",
        },
    }
    if suggestion is not None:
        decision["suggestion"] = suggestion
    row = {
        "id": sample_id, "expected": expected, "decision": decision,
        "duration_ms": 5.0, "rule_duration_ms": 1.0,
        "rule_output_sha256": output_sha, "rules_unchanged": rules_unchanged,
    }
    if backend == "chat":
        row["chat"] = {"choice": choice or "", "observed_model": "observed-model" if choice else "",
                       "usage": usage}
    else:
        row["chat"] = None
    return row


def make_report(backend, dataset, dataset_sha=DATASET_SHA, request_sha=REQUEST_SHA,
                rows=None, baseline_sha=BASELINE_SHA):
    mode = "disabled" if backend == "rules_only" else "shadow"
    baseline = {"results": [{"rule_id": rule_id, "relation": "conflict"} for rule_id in MANDATORY_IDS]}
    pack = {"schema_version": "rulepack/v1", "rules": [
        {"rule_id": rule_id, "mandatory": True} for rule_id in MANDATORY_IDS
    ]}
    if rows is None:
        rows = [make_row(case["id"], case["expected"], backend,
                         choice=None if backend == "rules_only" else case["expected"],
                         probs=probabilities(case["expected"]) if backend == "laya" else None,
                         usage=None) for case in dataset["cases"]]
    return {
        "schema_version": "ruleshadow-report/v1", "backend": backend, "mode": mode,
        "classification": dataset["classification"], "dataset_sha256": dataset_sha,
        "request_sha256": request_sha, "baseline_sha256": baseline_sha,
        "baseline": baseline, "published_pack": pack, "rows": rows,
    }


def make_reports(dataset):
    return {backend: make_report(backend, dataset) for backend in ("rules_only", "laya", "chat")}


class DecisionComparisonTests(unittest.TestCase):
    def test_missing_duplicate_samples_and_digest_or_typed_request_drift_are_rejected(self):
        dataset = make_dataset()
        reports = make_reports(dataset)
        valid = build_comparison(dataset, DATASET_SHA, reports)
        self.assertEqual(valid["mandatory_rule_invariance"]["status"], "PASS")
        self.assertEqual(valid["groups"]["chat"]["classification"]["accuracy"], 1.0)


        missing = copy.deepcopy(reports)
        missing["laya"]["rows"].pop()
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, missing)

        duplicate = copy.deepcopy(reports)
        duplicate["chat"]["rows"][1]["id"] = duplicate["chat"]["rows"][0]["id"]
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, duplicate)

        dataset_drift = copy.deepcopy(reports)
        dataset_drift["rules_only"]["dataset_sha256"] = "e" * 64
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, dataset_drift)

        request_drift = copy.deepcopy(reports)
        request_drift["chat"]["request_sha256"] = "f" * 64
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, request_drift)

        gold_drift = copy.deepcopy(reports)
        gold_drift["laya"]["rows"][0]["expected"] = "monthly_sla"
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, gold_drift)

        identity_drift = copy.deepcopy(reports)
        identity_drift["chat"]["rows"][0]["decision"]["event"]["actor_id"] = "different-actor"
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, identity_drift)

    def test_fixed_mandatory_conflict_baseline_is_required_and_shared(self):
        dataset = make_dataset()
        reports = make_reports(dataset)
        reports["laya"]["baseline"]["results"][0]["relation"] = "compatible"
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, reports)

        reports = make_reports(dataset)
        reports["chat"]["baseline_sha256"] = "9" * 64
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, reports)

    def test_failed_and_abstained_samples_remain_in_classification_denominator(self):
        dataset = make_dataset(("bandwidth", "monthly_sla", "advance_payment"))
        rows = [
            make_row("S-1", "bandwidth", "laya", choice="bandwidth", probs=probabilities("bandwidth")),
            make_row("S-2", "monthly_sla", "laya", status="unavailable", failure_reason="timeout"),
            make_row("S-3", "advance_payment", "laya", status="abstained", choice="manual_review",
                     probs=probabilities("manual_review")),
        ]
        summary, _ = summarize_group("laya", rows, dataset["cases"], CANDIDATES, BASELINE_SHA)
        metrics = summary["classification"]
        self.assertEqual(metrics["sample_count"], 3)
        self.assertEqual(metrics["scored_denominator"], 3)
        self.assertEqual(metrics["accuracy"], 0.333333)
        self.assertEqual(metrics["missing_prediction"], 2)
        self.assertEqual(metrics["manual_review"], 1)
        self.assertEqual(metrics["abstained"], 1)
        self.assertEqual(summary["model_calls"], 3)
        self.assertEqual(summary["errors"], 1)
        self.assertEqual(summary["timeouts"], 1)
        self.assertEqual(summary["degraded_to_rules"], 2)

    def test_zero_predictions_has_undefined_precision_but_zero_f1(self):
        dataset = make_dataset(("bandwidth",))
        row = make_row("S-1", "bandwidth", "laya", status="unavailable", failure_reason="transport_error")
        summary, _ = summarize_group("laya", [row], dataset["cases"], CANDIDATES, BASELINE_SHA)
        bandwidth = summary["classification"]["per_class"]["bandwidth"]
        self.assertIsNone(bandwidth["precision"])
        self.assertEqual(bandwidth["recall"], 0.0)
        self.assertEqual(bandwidth["f1"], 0.0)

    def test_laya_probability_calibration_uses_multiclass_brier_and_ten_bins(self):
        dataset = make_dataset(("bandwidth", "monthly_sla"))
        first = {"bandwidth": 0.8, "monthly_sla": 0.1, "advance_payment": 0.05, "manual_review": 0.05}
        second = {"bandwidth": 0.6, "monthly_sla": 0.3, "advance_payment": 0.05, "manual_review": 0.05}
        rows = [
            make_row("S-1", "bandwidth", "laya", choice="bandwidth", probs=first),
            make_row("S-2", "monthly_sla", "laya", choice="bandwidth", probs=second),
        ]
        summary, _ = summarize_group("laya", rows, dataset["cases"], CANDIDATES, BASELINE_SHA)
        calibration = summary["classification"]["probability_metrics"]
        self.assertEqual(calibration["status"], "AVAILABLE")
        self.assertEqual(calibration["multiclass_brier"], 0.455)
        self.assertEqual(calibration["ece_10_bin"], 0.4)
        self.assertEqual(calibration["bins"][6]["count"], 1)
        self.assertEqual(calibration["bins"][8]["count"], 1)

    def test_chat_unknown_usage_keeps_known_subtotals_and_never_invents_confidence(self):
        dataset = make_dataset(("bandwidth", "monthly_sla"))
        known_usage = {"source": "openai_response.usage", "input_tokens": 10,
                       "output_tokens": 4, "total_tokens": 14}
        rows = [
            make_row("S-1", "bandwidth", "chat", choice="bandwidth", usage=known_usage),
            make_row("S-2", "monthly_sla", "chat", choice="monthly_sla", usage=None),
        ]
        summary, _ = summarize_group("chat", rows, dataset["cases"], CANDIDATES, BASELINE_SHA)
        usage = summary["usage"]
        self.assertEqual(usage["unknown_calls"], 1)
        self.assertEqual(usage["known_subtotals"]["input_tokens"], 10)
        self.assertEqual(usage["known_subtotals"]["output_tokens"], 4)
        self.assertEqual(usage["known_subtotals"]["total_tokens"], 14)
        self.assertIsNone(usage["total_tokens_all_calls"])
        probability_metrics = summary["classification"]["probability_metrics"]
        self.assertEqual(probability_metrics["status"], "N/A")
        self.assertIsNone(probability_metrics["ece_10_bin"])
        reports = {
            "rules_only": make_report("rules_only", dataset),
            "laya": make_report("laya", dataset),
            "chat": make_report("chat", dataset, rows=rows),
        }
        comparison = build_comparison(dataset, DATASET_SHA, reports)
        self.assertEqual(comparison["rows"][0]["groups"]["chat"]["usage"], known_usage)
        self.assertIsNone(comparison["rows"][1]["groups"]["chat"]["usage"])
        reports["chat"]["rows"][0]["decision"]["suggestion"]["confidence"] = 0.8
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, reports)

    def test_laya_probabilities_use_quantization_tolerance_without_renormalizing(self):
        from compare_decisions import validate_probabilities

        rounded = {"bandwidth": 0.85, "monthly_sla": 0.0498,
                   "advance_payment": 0.05, "manual_review": 0.05}
        self.assertEqual(validate_probabilities(rounded, CANDIDATES, "probabilities"), rounded)
        outside_tolerance = dict(rounded, monthly_sla=0.0497)
        with self.assertRaises(ComparisonError):
            validate_probabilities(outside_tolerance, CANDIDATES, "probabilities")

    def test_duplicate_json_probability_keys_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "duplicate.json"
            path.write_text('{"probabilities":{"bandwidth":0.8,"bandwidth":0.7}}', encoding="utf-8")
            with self.assertRaises(ComparisonError):
                read_json_file(path, "duplicate probability report")

    def test_invalid_laya_probabilities_are_rejected(self):
        dataset = make_dataset(("bandwidth",))
        rows = [make_row("S-1", "bandwidth", "laya", choice="bandwidth",
                         probs={"bandwidth": 0.4, "monthly_sla": 0.2,
                                "advance_payment": 0.1, "manual_review": 0.1})]
        with self.assertRaises(ComparisonError):
            build_comparison(dataset, DATASET_SHA, {
                "rules_only": make_report("rules_only", dataset),
                "laya": make_report("laya", dataset, rows=rows),
                "chat": make_report("chat", dataset),
            })


if __name__ == "__main__":
    unittest.main()

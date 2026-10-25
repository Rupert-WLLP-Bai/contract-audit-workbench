#!/usr/bin/env python3
"""Compare fixed RulePack-only, chat-shadow, and Laya-shadow decision reports."""

import argparse
import datetime as dt
import hashlib
import json
import math
import re
import subprocess
import sys
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
ROOT = SCRIPT_DIR.parent
sys.path.insert(0, str(SCRIPT_DIR))
from source_identity import source_identity  # noqa: E402

SCHEMA_VERSION = "ruleshadow-report/v1"
OUTPUT_SCHEMA_VERSION = "decision-comparison-report/v1"
GROUPS = ("rules_only", "laya", "chat")
NO_PREDICTION = "__no_prediction__"
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
SOURCE_SHA_RE = re.compile(r"^(?:[0-9a-f]{40}|[0-9a-f]{64})$")
PROBABILITY_QUANTUM = 1e-4
PROBABILITY_FLOAT_SLACK = 1e-12


class ComparisonError(ValueError):
    """An input report or frozen dataset violates the comparison contract."""


def sha256_bytes(body):
    return hashlib.sha256(body).hexdigest()


def canonical_json(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def require_digest(value, label):
    if not isinstance(value, str) or not SHA256_RE.fullmatch(value):
        raise ComparisonError(f"{label} must be a lowercase SHA-256 digest")
    return value


def require_object(value, label):
    if not isinstance(value, dict):
        raise ComparisonError(f"{label} must be an object")
    return value


def require_string(value, label, allow_empty=False):
    if not isinstance(value, str) or (not allow_empty and not value.strip()):
        raise ComparisonError(f"{label} must be a {'possibly empty ' if allow_empty else ''}string")
    return value


def finite_number(value, label, minimum=None, maximum=None):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ComparisonError(f"{label} must be a finite number")
    if minimum is not None and value < minimum:
        raise ComparisonError(f"{label} must be at least {minimum}")
    if maximum is not None and value > maximum:
        raise ComparisonError(f"{label} must be at most {maximum}")
    return float(value)


def validate_dataset(data):
    data = require_object(data, "dataset")
    if data.get("schema_version") != "decision-benchmark/v1":
        raise ComparisonError("dataset schema_version must be decision-benchmark/v1")
    classification = require_string(data.get("classification"), "dataset.classification")
    candidates = data.get("candidates")
    cases = data.get("cases")
    if not isinstance(candidates, list) or len(candidates) < 2:
        raise ComparisonError("dataset.candidates must contain at least two candidates")
    if not isinstance(cases, list) or not cases:
        raise ComparisonError("dataset.cases must be non-empty")

    candidate_ids = []
    for index, candidate in enumerate(candidates):
        candidate = require_object(candidate, f"dataset.candidates[{index}]")
        candidate_id = require_string(candidate.get("id"), f"dataset.candidates[{index}].id")
        if candidate_id in candidate_ids:
            raise ComparisonError(f"duplicate candidate ID: {candidate_id}")
        candidate_ids.append(candidate_id)
    if "manual_review" not in candidate_ids:
        raise ComparisonError("dataset must include manual_review")

    case_ids = set()
    for index, case in enumerate(cases):
        case = require_object(case, f"dataset.cases[{index}]")
        case_id = require_string(case.get("id"), f"dataset.cases[{index}].id")
        if case_id in case_ids:
            raise ComparisonError(f"duplicate dataset sample ID: {case_id}")
        case_ids.add(case_id)
        expected = require_string(case.get("expected"), f"dataset.cases[{index}].expected")
        if expected not in candidate_ids:
            raise ComparisonError(f"unknown gold label {expected!r} for sample {case_id}")
    return classification, candidate_ids, cases


def validate_probabilities(value, candidate_ids, label):
    probabilities = require_object(value, label)
    if set(probabilities) != set(candidate_ids):
        missing = sorted(set(candidate_ids) - set(probabilities))
        extra = sorted(set(probabilities) - set(candidate_ids))
        raise ComparisonError(f"{label} keys must exactly match candidates (missing={missing}, extra={extra})")
    checked = {}
    for candidate_id in candidate_ids:
        value = probabilities[candidate_id]
        finite_number(value, f"{label}.{candidate_id}", minimum=0.0, maximum=1.0)
        checked[candidate_id] = value
    total = sum(checked.values())
    tolerance = len(candidate_ids) * PROBABILITY_QUANTUM / 2 + PROBABILITY_FLOAT_SLACK
    if abs(total - 1.0) > tolerance:
        raise ComparisonError(f"{label} must sum to 1 within {tolerance:.12g} (observed {total:.8f})")
    return checked


def validate_baseline_and_pack(report, label):
    baseline = require_object(report.get("baseline"), f"{label}.baseline")
    pack = require_object(report.get("published_pack"), f"{label}.published_pack")
    pack_rules = pack.get("rules")
    results = baseline.get("results")
    if not isinstance(pack_rules, list) or not isinstance(results, list):
        raise ComparisonError(f"{label} must include published_pack.rules and baseline.results")

    mandatory_ids = []
    for index, rule in enumerate(pack_rules):
        rule = require_object(rule, f"{label}.published_pack.rules[{index}]")
        if rule.get("mandatory") is True:
            rule_id = require_string(rule.get("rule_id"), f"{label}.published_pack.rules[{index}].rule_id")
            if rule_id in mandatory_ids:
                raise ComparisonError(f"{label} published pack has duplicate mandatory rule {rule_id}")
            mandatory_ids.append(rule_id)
    if len(mandatory_ids) != 3:
        raise ComparisonError(f"{label} must contain the fixed three mandatory conflict rules")

    by_id = {}
    for index, result in enumerate(results):
        result = require_object(result, f"{label}.baseline.results[{index}]")
        rule_id = require_string(result.get("rule_id"), f"{label}.baseline.results[{index}].rule_id")
        if rule_id in by_id:
            raise ComparisonError(f"{label} baseline has duplicate rule result {rule_id}")
        by_id[rule_id] = result
    if set(mandatory_ids) - set(by_id):
        raise ComparisonError(f"{label} baseline is missing a mandatory rule result")
    non_conflicts = [rule_id for rule_id in mandatory_ids if by_id[rule_id].get("relation") != "conflict"]
    if non_conflicts:
        raise ComparisonError(f"{label} fixed mandatory conflict fixture changed: {non_conflicts}")
    return baseline, pack, mandatory_ids


def validate_group_report(report, name, path_label, classification, candidate_ids, cases, dataset_sha256):
    report = require_object(report, path_label)
    if report.get("schema_version") != SCHEMA_VERSION:
        raise ComparisonError(f"{path_label}.schema_version must be {SCHEMA_VERSION}")
    expected_backend = name
    if report.get("backend") != expected_backend:
        raise ComparisonError(f"{path_label}.backend must be {expected_backend}")
    mode = report.get("mode")
    if mode not in ("disabled", "shadow"):
        raise ComparisonError(f"{path_label}.mode must be disabled or shadow")
    if name == "rules_only" and mode != "disabled":
        raise ComparisonError(f"{path_label} rules_only route must use disabled mode")
    if name in ("laya", "chat") and mode != "shadow":
        raise ComparisonError(f"{path_label} {name} route must use shadow mode")
    if require_digest(report.get("dataset_sha256"), f"{path_label}.dataset_sha256") != dataset_sha256:
        raise ComparisonError(f"{path_label} dataset SHA-256 does not match the frozen dataset")
    require_digest(report.get("request_sha256"), f"{path_label}.request_sha256")
    require_digest(report.get("baseline_sha256"), f"{path_label}.baseline_sha256")
    if report.get("classification") != classification:
        raise ComparisonError(f"{path_label}.classification does not match the dataset")

    source_sha = report.get("source_sha256")
    if source_sha is not None and (not isinstance(source_sha, str) or not SOURCE_SHA_RE.fullmatch(source_sha)):
        raise ComparisonError(f"{path_label}.source_sha256 must be null or a full Git SHA")

    baseline, pack, mandatory_ids = validate_baseline_and_pack(report, path_label)
    rows = report.get("rows")
    if not isinstance(rows, list):
        raise ComparisonError(f"{path_label}.rows must be an array")
    rows_by_id = {}
    typed_request_identity = None
    case_by_id = {case["id"]: case for case in cases}
    for index, row in enumerate(rows):
        row = require_object(row, f"{path_label}.rows[{index}]")
        row_id = require_string(row.get("id"), f"{path_label}.rows[{index}].id")
        if row_id in rows_by_id:
            raise ComparisonError(f"{path_label} has duplicate sample ID {row_id}")
        if row_id not in case_by_id:
            raise ComparisonError(f"{path_label} has unknown sample ID {row_id}")
        if row.get("expected") != case_by_id[row_id]["expected"]:
            raise ComparisonError(f"{path_label} gold label drift for sample {row_id}")
        finite_number(row.get("duration_ms"), f"{path_label}.rows[{row_id}].duration_ms", minimum=0.0)
        finite_number(row.get("rule_duration_ms"), f"{path_label}.rows[{row_id}].rule_duration_ms", minimum=0.0)
        output_digest = require_digest(row.get("rule_output_sha256"), f"{path_label}.rows[{row_id}].rule_output_sha256")
        if not isinstance(row.get("rules_unchanged"), bool):
            raise ComparisonError(f"{path_label}.rows[{row_id}].rules_unchanged must be boolean")

        decision = require_object(row.get("decision"), f"{path_label}.rows[{row_id}].decision")
        status = require_string(decision.get("status"), f"{path_label}.rows[{row_id}].decision.status")
        accepted_rules = decision.get("accepted_rules")
        if not isinstance(accepted_rules, list) or any(not isinstance(item, str) for item in accepted_rules):
            raise ComparisonError(f"{path_label}.rows[{row_id}].decision.accepted_rules must be a string array")
        event = require_object(decision.get("event"), f"{path_label}.rows[{row_id}].decision.event")
        required_event_strings = (
            "request_id", "decision_id", "run_id", "attempt_id", "trace_id", "tenant_id", "case_id",
            "actor_id", "capability", "mode", "status", "model_name", "response_model", "failure_reason",
            "rulepack_version", "rulepack_hash", "policy_version",
        )
        for field in required_event_strings:
            require_string(event.get(field), f"{path_label}.rows[{row_id}].decision.event.{field}", allow_empty=field in ("model_name", "response_model", "failure_reason"))
        if event["request_id"] != row_id:
            raise ComparisonError(f"{path_label} typed request ID mismatch for sample {row_id}")
        if event["mode"] != mode or event["status"] != status:
            raise ComparisonError(f"{path_label} event mode/status mismatch for sample {row_id}")
        if event["capability"] != "choice":
            raise ComparisonError(f"{path_label} has non-choice capability for sample {row_id}")
        pack_version = pack.get("version")
        if pack_version is not None and event["rulepack_version"] != str(pack_version):
            raise ComparisonError(f"{path_label} RulePack version mismatch for sample {row_id}")
        lifecycle = pack.get("lifecycle")
        verification = lifecycle.get("verification") if isinstance(lifecycle, dict) else None
        manifest_hash = verification.get("manifest_hash") if isinstance(verification, dict) else None
        if manifest_hash is not None and event["rulepack_hash"] != manifest_hash:
            raise ComparisonError(f"{path_label} RulePack hash mismatch for sample {row_id}")
        finite_number(event.get("latency_ms"), f"{path_label}.rows[{row_id}].decision.event.latency_ms", minimum=0.0)

        suggestion = decision.get("suggestion")
        if suggestion is not None:
            suggestion = require_object(suggestion, f"{path_label}.rows[{row_id}].decision.suggestion")
            candidate_id = suggestion.get("candidate_id")
            if candidate_id is not None and candidate_id not in candidate_ids:
                raise ComparisonError(f"{path_label} has unknown candidate {candidate_id!r} for sample {row_id}")
            probabilities = suggestion.get("probabilities")
            checked_probabilities = None
            if name == "laya" and probabilities is not None:
                checked_probabilities = validate_probabilities(
                    probabilities, candidate_ids,
                    f"{path_label}.rows[{row_id}].decision.suggestion.probabilities",
                )
                if candidate_id is None or checked_probabilities[candidate_id] != max(checked_probabilities.values()):
                    raise ComparisonError(f"{path_label} Laya choice is not the probability maximum for sample {row_id}")
            if name == "laya" and probabilities is None:
                raise ComparisonError(f"{path_label} Laya suggestion is missing probabilities for sample {row_id}")
            confidence = suggestion.get("confidence")
            if confidence is not None:
                confidence_value = finite_number(confidence, f"{path_label}.rows[{row_id}].decision.suggestion.confidence", minimum=0.0, maximum=1.0)
                if checked_probabilities is not None and abs(confidence_value - max(checked_probabilities.values())) > PROBABILITY_QUANTUM + PROBABILITY_FLOAT_SLACK:
                    raise ComparisonError(f"{path_label} Laya confidence disagrees with maximum probability for sample {row_id}")
        if name == "laya" and status in ("suggested", "abstained") and suggestion is None:
            raise ComparisonError(f"{path_label} Laya {status} row is missing its suggestion for sample {row_id}")
        if name == "chat" and status == "suggested" and suggestion is None:
            raise ComparisonError(f"{path_label} successful chat row is missing its decision suggestion for sample {row_id}")
        if name == "chat" and suggestion is not None:
            if "probabilities" not in suggestion or suggestion["probabilities"] is not None:
                raise ComparisonError(f"{path_label} chat suggestion probabilities must be null for sample {row_id}")
            if "confidence" not in suggestion or suggestion["confidence"] is not None:
                raise ComparisonError(f"{path_label} chat suggestion confidence must be null for sample {row_id}")

        chat = row.get("chat")
        if name != "chat" and row.get("chat") is not None:
            raise ComparisonError(f"{path_label} non-chat route contains a chat result for sample {row_id}")
        if name == "chat" and chat is not None:
            chat = require_object(chat, f"{path_label}.rows[{row_id}].chat")
            choice = chat.get("choice")
            if choice is not None and not isinstance(choice, str):
                raise ComparisonError(f"{path_label}.rows[{row_id}].chat.choice must be a string or null")
            if choice and choice not in candidate_ids:
                raise ComparisonError(f"{path_label} has unknown chat choice {choice!r} for sample {row_id}")
            observed_model = chat.get("observed_model")
            if observed_model is not None and not isinstance(observed_model, str):
                raise ComparisonError(f"{path_label}.rows[{row_id}].chat.observed_model must be a string or null")
            if "usage" not in chat:
                raise ComparisonError(f"{path_label}.rows[{row_id}].chat must preserve usage as an object or null")
            validate_usage(chat["usage"], f"{path_label}.rows[{row_id}].chat.usage")
            if choice and suggestion and suggestion.get("candidate_id") and choice != suggestion["candidate_id"]:
                raise ComparisonError(f"{path_label} chat choice disagrees with decision suggestion for sample {row_id}")
        elif name == "chat" and status == "suggested":
            raise ComparisonError(f"{path_label} successful chat row is missing its observed chat result")

        if name == "chat" and status == "suggested" and isinstance(chat, dict) and not chat.get("choice"):
            raise ComparisonError(f"{path_label} successful chat row has no categorical choice for sample {row_id}")
        if name == "rules_only" and status != "disabled":
            raise ComparisonError(f"{path_label} rules_only decision must be disabled for sample {row_id}")
        if name == "rules_only" and suggestion is not None:
            raise ComparisonError(f"{path_label} rules_only route must not emit a classification suggestion")
        if row["rules_unchanged"] and (output_digest != report["baseline_sha256"] or accepted_rules):
            raise ComparisonError(f"{path_label} claims unchanged mandatory rules with a mismatching output for {row_id}")
        identity = tuple(event[field] for field in ("tenant_id", "case_id", "actor_id", "capability",
                                                     "rulepack_version", "rulepack_hash", "policy_version"))
        if typed_request_identity is None:
            typed_request_identity = identity
        elif identity != typed_request_identity:
            raise ComparisonError(f"{path_label} typed request identity drift at sample {row_id}")
        rows_by_id[row_id] = row

    missing = [case["id"] for case in cases if case["id"] not in rows_by_id]
    if missing:
        raise ComparisonError(f"{path_label} is missing samples: {missing}")
    if len(rows_by_id) != len(cases):
        raise ComparisonError(f"{path_label} sample count does not match the dataset")
    if "passed" in report:
        if not isinstance(report["passed"], bool):
            raise ComparisonError(f"{path_label}.passed must be boolean")
        observed_passed = all(
            row["rules_unchanged"] and row["rule_output_sha256"] == report["baseline_sha256"]
            and not row["decision"]["accepted_rules"] for row in rows_by_id.values()
        )
        if report["passed"] != observed_passed:
            raise ComparisonError(f"{path_label}.passed contradicts per-sample mandatory-rule invariance")
    return {
        "report": report,
        "rows_by_id": rows_by_id,
        "baseline": baseline,
        "pack": pack,
        "mandatory_ids": mandatory_ids,
        "mode": mode,
    }


def validate_usage(usage, label):
    if usage is None:
        return
    usage = require_object(usage, label)
    require_string(usage.get("source"), f"{label}.source")
    for field in ("input_tokens", "output_tokens", "total_tokens"):
        value = usage.get(field)
        if value is not None:
            if isinstance(value, bool) or not isinstance(value, int) or value < 0:
                raise ComparisonError(f"{label}.{field} must be a non-negative integer or null")


def validate_cross_report_contract(validated):
    baseline_digests = {item["report"]["baseline_sha256"] for item in validated.values()}
    request_digests = {item["report"]["request_sha256"] for item in validated.values()}
    if len(baseline_digests) != 1:
        raise ComparisonError("the three routes do not share the mandatory-rule baseline digest")
    if len(request_digests) != 1:
        raise ComparisonError("the three routes do not share the same typed request digest")

    first = validated[GROUPS[0]]
    baseline_json = canonical_json(first["baseline"])
    pack_json = canonical_json(first["pack"])
    for name, item in validated.items():
        if canonical_json(item["baseline"]) != baseline_json:
            raise ComparisonError(f"{name} mandatory-rule baseline differs from rules_only")
        if canonical_json(item["pack"]) != pack_json:
            raise ComparisonError(f"{name} published RulePack differs from rules_only")

    identity_fields = ("tenant_id", "case_id", "actor_id", "capability", "rulepack_version",
                       "rulepack_hash", "policy_version")
    for case_id in first["rows_by_id"]:
        expected_identity = tuple(first["rows_by_id"][case_id]["decision"]["event"][field]
                                  for field in identity_fields)
        for name, item in validated.items():
            observed_identity = tuple(item["rows_by_id"][case_id]["decision"]["event"][field]
                                      for field in identity_fields)
            if observed_identity != expected_identity:
                raise ComparisonError(f"{name} typed request identity differs for sample {case_id}")
    source_shas = {
        item["report"].get("source_sha256") for item in validated.values()
        if item["report"].get("source_sha256") is not None
    }
    if len(source_shas) > 1:
        raise ComparisonError("input reports contain different source_sha256 values")
    return next(iter(baseline_digests)), next(iter(request_digests)), next(iter(source_shas), None)


def nearest_rank(values, fraction):
    ordered = sorted(values)
    if not ordered:
        return None
    return ordered[max(0, math.ceil(len(ordered) * fraction) - 1)]


def latency_stats(values, first_id=None):
    values = [float(value) for value in values]
    if not values:
        return {"count": 0, "mean_ms": None, "p50_ms": None, "p95_ms": None,
                "first_request_ms": None, "first_request_id": None,
                "remaining_requests": {"count": 0, "mean_ms": None, "p50_ms": None, "p95_ms": None}}
    remaining = values[1:]
    return {
        "count": len(values),
        "mean_ms": round(sum(values) / len(values), 6),
        "p50_ms": round(nearest_rank(values, 0.50), 6),
        "p95_ms": round(nearest_rank(values, 0.95), 6),
        "first_request_ms": round(values[0], 6),
        "first_request_id": first_id,
        "remaining_requests": {
            "count": len(remaining),
            "mean_ms": round(sum(remaining) / len(remaining), 6) if remaining else None,
            "p50_ms": round(nearest_rank(remaining, 0.50), 6) if remaining else None,
            "p95_ms": round(nearest_rank(remaining, 0.95), 6) if remaining else None,
        },
    }


def normalized_observation(name, row, expected, candidate_ids):
    decision = row["decision"]
    event = decision["event"]
    status = decision["status"]
    suggestion = decision.get("suggestion") or {}
    raw_choice = suggestion.get("candidate_id")
    chat = row.get("chat")
    if name == "chat" and isinstance(chat, dict):
        chat_choice = chat.get("choice")
        if chat_choice:
            raw_choice = chat_choice
    if name == "rules_only":
        raw_choice = None

    abstained = (status == "abstained" or event.get("abstention") == "abstained" or
                 suggestion.get("upstream_abstained") is True)
    prediction = raw_choice if status == "suggested" and not abstained else None
    if prediction is not None and prediction not in candidate_ids:
        raise ComparisonError(f"unknown prediction {prediction!r} for sample {row['id']}")
    if name == "rules_only":
        prediction = None
    call_attempted = name != "rules_only" and status in ("suggested", "abstained", "unavailable")
    failure_reason = event.get("failure_reason", "")
    latency_ms = finite_number(event.get("latency_ms", 0), f"{row['id']}.event.latency_ms", minimum=0.0)
    probabilities = None
    if name == "laya" and suggestion.get("probabilities") is not None:
        probabilities = validate_probabilities(suggestion["probabilities"], candidate_ids, f"{row['id']}.probabilities")
    usage = None
    if name == "chat" and isinstance(chat, dict):
        usage = chat.get("usage")
    return {
        "id": row["id"], "expected": expected, "status": status,
        "observed_choice": raw_choice, "prediction": prediction,
        "abstained": abstained, "probabilities": probabilities,
        "call_attempted": call_attempted, "failure_reason": failure_reason,
        "event_latency_ms": latency_ms, "usage": usage,
        "duration_ms": float(row["duration_ms"]), "rule_duration_ms": float(row["rule_duration_ms"]),
        "rule_output_sha256": row["rule_output_sha256"], "rules_unchanged": row["rules_unchanged"],
        "accepted_rules": decision["accepted_rules"], "decision": decision, "chat": chat,
    }


def brier_and_ece(observations, candidate_ids):
    scored = [item for item in observations if item["probabilities"] is not None]
    if not scored:
        return {"status": "N/A", "reason": "no probability distributions were observed",
                "probability_sample_count": 0, "unscored_sample_count": len(observations),
                "multiclass_brier": None, "ece_10_bin": None, "bins": []}

    brier_values = []
    bin_rows = [{"lower": index / 10, "upper": (index + 1) / 10, "count": 0,
                 "mean_confidence": None, "accuracy": None, "contribution": None} for index in range(10)]
    bin_confidence_sums = [0.0] * 10
    bin_correct_sums = [0] * 10
    for item in scored:
        probabilities = item["probabilities"]
        brier_values.append(sum((probabilities[label] - (1.0 if label == item["expected"] else 0.0)) ** 2
                                for label in candidate_ids))
        top_label = max(candidate_ids, key=lambda label: probabilities[label])
        confidence = probabilities[top_label]
        bin_index = min(9, int(confidence * 10))
        bin_rows[bin_index]["count"] += 1
        bin_confidence_sums[bin_index] += confidence
        bin_correct_sums[bin_index] += int(top_label == item["expected"])

    ece = 0.0
    for index, bin_row in enumerate(bin_rows):
        count = bin_row["count"]
        if not count:
            continue
        mean_confidence = bin_confidence_sums[index] / count
        accuracy = bin_correct_sums[index] / count
        contribution = count / len(scored) * abs(accuracy - mean_confidence)
        bin_row["mean_confidence"] = round(mean_confidence, 6)
        bin_row["accuracy"] = round(accuracy, 6)
        bin_row["contribution"] = round(contribution, 6)
        ece += contribution
    return {
        "status": "AVAILABLE" if len(scored) == len(observations) else "PARTIAL",
        "probability_sample_count": len(scored),
        "unscored_sample_count": len(observations) - len(scored),
        "multiclass_brier": round(sum(brier_values) / len(brier_values), 6),
        "brier_definition": "mean over scored calls of the sum across classes of (p - one_hot)^2",
        "ece_10_bin": round(ece, 6),
        "ece_definition": "top-probability confidence vs top-probability class accuracy; equal-width bins [i/10,(i+1)/10), with 1.0 in the last bin",
        "bins": bin_rows,
    }


def usage_summary(observations, name):
    calls = [item for item in observations if item["call_attempted"]]
    if name == "rules_only":
        return {"status": "NOT_APPLICABLE", "model_calls": 0, "known_calls": 0, "unknown_calls": 0,
                "partial_calls": 0, "known_subtotals": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
                "total_tokens_all_calls": 0, "source_subtotals": {}}

    totals = {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
    known_fields = {field: 0 for field in totals}
    source_subtotals = {}
    known_calls = 0
    unknown_calls = 0
    partial_calls = 0
    total_complete = True
    for item in calls:
        usage = item["usage"] if name == "chat" else None
        if usage is None:
            unknown_calls += 1
            total_complete = False
            continue
        known_calls += 1
        source = usage["source"]
        source_data = source_subtotals.setdefault(source, {"calls": 0, "input_tokens": 0, "output_tokens": 0, "total_tokens": 0})
        source_data["calls"] += 1
        missing_field = False
        for field in totals:
            value = usage.get(field)
            if value is None:
                missing_field = True
                if field == "total_tokens":
                    total_complete = False
                continue
            totals[field] += value
            source_data[field] += value
        if missing_field:
            partial_calls += 1
    total_tokens_all_calls = totals["total_tokens"] if total_complete else None
    return {
        "status": "AVAILABLE" if calls and not unknown_calls and not partial_calls else ("PARTIAL" if calls else "NOT_RUN"),
        "model_calls": len(calls), "known_calls": known_calls, "unknown_calls": unknown_calls,
        "partial_calls": partial_calls, "known_subtotals": totals,
        "total_tokens_all_calls": total_tokens_all_calls,
        "total_tokens_all_calls_is_complete": total_complete,
        "source_subtotals": source_subtotals,
        "cost": None,
    }


def summarize_group(name, rows, cases, candidate_ids, baseline_sha256):
    rows_by_id = {row["id"]: row for row in rows}
    observations = [normalized_observation(name, rows_by_id[case["id"]], case["expected"], candidate_ids)
                    for case in cases]
    row_count = len(observations)
    unchanged_count = sum(
        item["rules_unchanged"] and item["rule_output_sha256"] == baseline_sha256 and not item["accepted_rules"]
        for item in observations
    )
    latency = {
        "end_to_end": latency_stats([item["duration_ms"] for item in observations], observations[0]["id"] if observations else None),
        "rule": latency_stats([item["rule_duration_ms"] for item in observations], observations[0]["id"] if observations else None),
        "model_call": latency_stats([item["event_latency_ms"] for item in observations if item["call_attempted"]],
                                     next((item["id"] for item in observations if item["call_attempted"]), None)),
        "first_request_is_cold_start": False,
        "cold_start_measurement": {"status": "NOT_RUN", "value_ms": None},
    }
    model_calls = sum(item["call_attempted"] for item in observations)
    status_counts = {}
    failures_by_reason = {}
    for item in observations:
        status_counts[item["status"]] = status_counts.get(item["status"], 0) + 1
        if item["failure_reason"]:
            failures_by_reason[item["failure_reason"]] = failures_by_reason.get(item["failure_reason"], 0) + 1

    if name == "rules_only":
        classification = {
            "status": "N/A", "reason": "RulePack-only executes typed mandatory rules and emits no focus classification",
            "sample_count": row_count, "scored_denominator": None, "accuracy": None,
            "per_class": None, "macro_f1": None, "confusion_matrix": None,
            "manual_review": None, "abstained": None,
            "probability_metrics": {"status": "N/A", "reason": "no classifier probabilities"},
        }
        errors = 0
        timeouts = 0
        degraded = 0
        calls = 0
        usage = usage_summary(observations, name)
    else:
        confusion = {label: {predicted: 0 for predicted in candidate_ids + [NO_PREDICTION]} for label in candidate_ids}
        true_positive = {label: 0 for label in candidate_ids}
        predicted_count = {label: 0 for label in candidate_ids}
        support = {label: 0 for label in candidate_ids}
        correct = 0
        manual_review = 0
        abstained = 0
        errors = 0
        timeouts = 0
        degraded = 0
        for item in observations:
            expected = item["expected"]
            prediction = item["prediction"]
            support[expected] += 1
            matrix_prediction = prediction if prediction is not None else NO_PREDICTION
            confusion[expected][matrix_prediction] += 1
            if prediction is not None:
                predicted_count[prediction] += 1
                if prediction == expected:
                    true_positive[prediction] += 1
                    correct += 1
            manual_review += int(item["observed_choice"] == "manual_review")
            abstained += int(item["abstained"])
            is_error = item["status"] not in ("suggested", "abstained") or bool(item["failure_reason"])
            errors += int(is_error)
            normalized_reason = item["failure_reason"].lower().replace("-", "_")
            timed_out = "timeout" in normalized_reason or "deadline_exceeded" in normalized_reason
            timeouts += int(timed_out)
            degraded += int(prediction is None)
        per_class = {}
        f1_values = []
        for label in candidate_ids:
            tp = true_positive[label]
            fp = predicted_count[label] - tp
            fn = support[label] - tp
            precision = tp / predicted_count[label] if predicted_count[label] else None
            recall = tp / support[label] if support[label] else None
            f1_denominator = 2 * tp + fp + fn
            f1 = 2 * tp / f1_denominator if f1_denominator else None
            if f1 is not None:
                f1_values.append(f1)
            per_class[label] = {
                "support": support[label], "predicted": predicted_count[label], "true_positive": tp,
                "false_positive": fp, "false_negative": fn,
                "precision": round(precision, 6) if precision is not None else None,
                "recall": round(recall, 6) if recall is not None else None,
                "f1": round(f1, 6) if f1 is not None else None,
            }
        classification = {
            "status": "AVAILABLE", "sample_count": row_count, "scored_denominator": row_count,
            "correct": correct, "accuracy": round(correct / row_count, 6) if row_count else None,
            "per_class": per_class, "macro_f1": round(sum(f1_values) / len(f1_values), 6) if f1_values else None,
            "confusion_matrix": {"rows": candidate_ids, "columns": candidate_ids + [NO_PREDICTION], "counts": confusion},
            "manual_review": manual_review, "abstained": abstained,
            "missing_prediction": sum(item["prediction"] is None for item in observations),
            "probability_metrics": brier_and_ece(observations, candidate_ids) if name == "laya" else {
                "status": "N/A", "reason": "chat reports categorical choice only; no confidence is inferred",
                "multiclass_brier": None, "ece_10_bin": None,
            },
        }
        calls = model_calls
        usage = usage_summary(observations, name)

    group_summary = {
        "backend": name, "mode": "disabled" if name == "rules_only" else "shadow",
        "sample_count": row_count, "model_calls": calls,
        "model_call_count_basis": "one decision request per row with suggested/abstained/unavailable status; retries are not represented",
        "errors": errors, "timeouts": timeouts, "degraded_to_rules": degraded,
        "status_counts": status_counts, "failures_by_reason": failures_by_reason,
        "mandatory_rule_invariance": {"numerator": unchanged_count, "denominator": row_count,
                                      "status": "PASS" if unchanged_count == row_count else "FAIL"},
        "classification": classification, "latency": latency, "usage": usage,
        "models": {
            "configured": sorted({item["decision"]["event"].get("model_name", "") for item in rows
                                  if item["decision"]["event"].get("model_name")}),
            "observed": sorted({value for item in observations
                                 for value in (item["decision"]["event"].get("response_model", ""),
                                               (item["chat"] or {}).get("observed_model", "") if isinstance(item["chat"], dict) else "")
                                 if value}),
            "revision": sorted({item["decision"]["event"].get("model_revision", "") for item in rows
                                if item["decision"]["event"].get("model_revision")}),
        },
    }
    return group_summary, observations


def build_comparison(dataset_data, dataset_sha256, reports):
    classification, candidate_ids, cases = validate_dataset(dataset_data)
    dataset_sha256 = require_digest(dataset_sha256, "dataset SHA-256")
    validated = {}
    for name in GROUPS:
        if name not in reports:
            raise ComparisonError(f"missing {name} input report")
        validated[name] = validate_group_report(reports[name], name, name, classification, candidate_ids, cases, dataset_sha256)
    baseline_digest, request_digest, input_source_sha = validate_cross_report_contract(validated)

    groups = {}
    observations_by_group = {}
    for name in GROUPS:
        item = validated[name]
        ordered_rows = [item["rows_by_id"][case["id"]] for case in cases]
        groups[name], observations_by_group[name] = summarize_group(name, ordered_rows, cases, candidate_ids, baseline_digest)

    result_rows = []
    for index, case in enumerate(cases):
        row_entry = {"id": case["id"], "expected": case["expected"], "groups": {}}
        for name in GROUPS:
            observation = observations_by_group[name][index]
            row_entry["groups"][name] = {
                "status": observation["status"], "prediction": observation["prediction"],
                "observed_choice": observation["observed_choice"], "abstained": observation["abstained"],
                "probabilities": observation["probabilities"],
                "model_call_attempted": observation["call_attempted"],
                "usage": observation["usage"],
                "failure_reason": observation["failure_reason"],
                "duration_ms": observation["duration_ms"], "rule_duration_ms": observation["rule_duration_ms"],
                "rule_output_sha256": observation["rule_output_sha256"],
                "rules_unchanged": observation["rules_unchanged"],
                "accepted_rules": observation["accepted_rules"],
                "decision": observation["decision"], "chat": observation["chat"],
            }
        result_rows.append(row_entry)

    total_invariant_numerator = sum(group["mandatory_rule_invariance"]["numerator"] for group in groups.values())
    total_invariant_denominator = sum(group["mandatory_rule_invariance"]["denominator"] for group in groups.values())
    all_invariant = total_invariant_numerator == total_invariant_denominator
    mandatory_ids = validated["rules_only"]["mandatory_ids"]
    return {
        "schema_version": OUTPUT_SCHEMA_VERSION,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "dataset": {"schema_version": dataset_data["schema_version"], "classification": classification,
                    "sample_count": len(cases), "candidate_ids": candidate_ids, "sha256": dataset_sha256},
        "typed_request_sha256": request_digest,
        "mandatory_baseline_sha256": baseline_digest,
        "input_source_sha256": input_source_sha,
        "groups": groups,
        "mandatory_rule_invariance": {
            "numerator": total_invariant_numerator, "denominator": total_invariant_denominator,
            "status": "PASS" if all_invariant else "FAIL",
            "per_group": {name: group["mandatory_rule_invariance"] for name, group in groups.items()},
        },
        "critical_risk_fixture": {
            "status": "NO_MISS_OBSERVED" if all_invariant else "UNKNOWN_OUTPUT_CHANGED",
            "independent_typed_fact_fixtures": 1,
            "mandatory_conflict_rules": len(mandatory_ids), "mandatory_conflict_rule_ids": mandatory_ids,
            "missed_conflicts": 0 if all_invariant else None,
            "per_rule": {rule_id: {"baseline_relation": "conflict",
                                  "missed": 0 if all_invariant else None,
                                  "status": "NO_MISS_OBSERVED" if all_invariant else "UNKNOWN_OUTPUT_CHANGED"}
                         for rule_id in mandatory_ids},
            "invariance_observations": total_invariant_denominator,
            "limitation": "The three mandatory conflicts come from one fixed typed-fact fixture repeated across dataset rows; these are not 40 independent risk samples.",
        },
        "rows": result_rows,
        "resources": {
            "cpu": {"status": "NOT_RUN", "value": None},
            "gpu": {"status": "NOT_RUN", "value": None},
            "memory_bytes": {"status": "NOT_RUN", "value": None},
            "cold_start": {"status": "NOT_RUN", "value_ms": None},
            "laya_runtime_configuration": {"status": "NOT_RUN", "evidence_sha256": None},
        },
        "limitations": [
            "D0_SYNTHETIC_ZH_REVIEW_FOCUS is a fixed developer-visible synthetic dataset, not independent business validation or UAT.",
            "RulePack-only has no focus-classification prediction; classification metrics are N/A.",
            "Chat exposes a categorical choice without calibrated probabilities; no confidence or calibration score is inferred.",
            "The typed request body is not a CLI input; request_sha256 and event identity are cross-checked across routes, but the digest cannot be recomputed here.",
            "Laya Brier and ECE use rows with observed valid distributions; unscored failures remain in classification denominators and are listed separately.",
            "First-request latency is reported as observed and does not establish cold-start behavior.",
            "CPU, GPU, memory, and cold-start measurements are NOT_RUN because the input reports do not prove them.",
            "The separate Laya manifest records container limits, CPU device/thread configuration and a prior smoke timing; it is not utilization data and is not linked to these input reports by run ID.",
            "Unknown or partial token usage remains unknown; known token subtotals are not converted to full-call totals or money.",
            "Model call counts are inferred from final decision status; retry counts are not present in ruleshadow-report/v1.",
        ],
    }


def working_tree_status(root):
    try:
        result = subprocess.run(["git", "status", "--porcelain"], cwd=root, capture_output=True,
                                text=True, check=False)
    except OSError:
        return "UNKNOWN"
    if result.returncode != 0:
        return "UNKNOWN"
    return "uncommitted" if result.stdout.strip() else "clean"


def laya_runtime_context(root):
    manifest_path = Path(root) / "artifacts" / "laya" / "run-manifest.json"
    try:
        manifest, raw = read_json_file(manifest_path, "Laya runtime manifest")
        runtime = require_object(manifest.get("runtime"), "Laya runtime manifest.runtime")
        host = require_object(manifest.get("host"), "Laya runtime manifest.host")
        image = require_object(manifest.get("image"), "Laya runtime manifest.image")
        model = require_object(manifest.get("model"), "Laya runtime manifest.model")
        startup = require_object(manifest.get("startup_and_inference"), "Laya runtime manifest.startup_and_inference")
    except (ComparisonError, OSError):
        return {"status": "NOT_RUN", "evidence_path": str(manifest_path), "evidence_sha256": None}
    return {
        "status": "RECORDED_CONFIGURATION",
        "evidence_path": str(manifest_path.resolve()),
        "evidence_sha256": sha256_bytes(raw),
        "model_revision": model.get("revision"),
        "image_id": image.get("image_id"),
        "device": runtime.get("device"),
        "laya_threads": runtime.get("laya_threads"),
        "omp_num_threads": runtime.get("omp_num_threads"),
        "container_cpu_limit": host.get("docker_cpus"),
        "container_memory_limit_bytes": host.get("docker_memory_bytes"),
        "gpu_present_but_unused": host.get("gpu_present_but_unused"),
        "smoke_start_to_response_seconds": startup.get("smoke_command_wall_seconds_from_start_until_response"),
        "smoke_first_inference_after_preload_seconds": startup.get("first_inference_http_seconds_after_preload"),
        "scope": "Separate recorded Laya deployment configuration and smoke timing; not measured resource utilization, not a cold-start estimate, and not bound by run ID to the supplied comparison report.",
    }


def reject_duplicate_keys(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ComparisonError(f"duplicate JSON object key: {key}")
        result[key] = value
    return result


def read_json_file(path, label):
    try:
        raw = Path(path).read_bytes()
    except OSError as exc:
        raise ComparisonError(f"cannot read {label}: {exc}") from exc
    try:
        parsed = json.loads(raw, object_pairs_hook=reject_duplicate_keys)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ComparisonError(f"invalid JSON in {label}: {exc}") from exc
    return parsed, raw


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rules", required=True, help="rules_only ruleshadow-report/v1 JSON")
    parser.add_argument("--laya", required=True, help="Laya shadow ruleshadow-report/v1 JSON")
    parser.add_argument("--chat", required=True, help="chat shadow ruleshadow-report/v1 JSON")
    parser.add_argument("--dataset", required=True, help="frozen decision-benchmark/v1 JSON")
    parser.add_argument("--source-sha", required=True, help="full Git commit SHA (40 or 64 lowercase hex characters)")
    parser.add_argument("--out", required=True, help="comparison JSON output path")
    args = parser.parse_args(argv)
    if not SOURCE_SHA_RE.fullmatch(args.source_sha):
        parser.error("--source-sha must be a full 40- or 64-character lowercase Git SHA")

    try:
        dataset, dataset_raw = read_json_file(args.dataset, "dataset")
        report_files = {"rules_only": args.rules, "laya": args.laya, "chat": args.chat}
        reports = {}
        report_hashes = {}
        report_paths = {}
        for key, path in report_files.items():
            reports[key], raw = read_json_file(path, f"{key} report")
            report_hashes[key] = sha256_bytes(raw)
            report_paths[key] = str(Path(path).resolve())

        comparison = build_comparison(dataset, sha256_bytes(dataset_raw), reports)
        laya_runtime = laya_runtime_context(ROOT)
        comparison["resources"]["laya_runtime_configuration"] = laya_runtime
        input_source_sha = comparison["input_source_sha256"]
        if input_source_sha is not None and input_source_sha != args.source_sha:
            raise ComparisonError("--source-sha does not match source_sha256 recorded in input reports")

        identity = source_identity(ROOT)
        output_path = Path(args.out)
        script_hash = sha256_bytes(Path(__file__).read_bytes())
        test_script = SCRIPT_DIR / "test_decision_comparison.py"
        test_hash = sha256_bytes(test_script.read_bytes()) if test_script.exists() else None
        comparison.update({
            "source_sha256": args.source_sha,
            "source_identity": identity,
            "working_tree_status": working_tree_status(ROOT),
            "provenance": {
                "input_reports": {key: {"path": report_paths[key], "sha256": report_hashes[key]} for key in report_files},
                "dataset": {"path": str(Path(args.dataset).resolve()), "sha256": sha256_bytes(dataset_raw)},
                "comparison_script": {"path": str(Path(__file__).resolve()), "sha256": script_hash},
                "comparison_test_script": {"path": str(test_script.resolve()) if test_script.exists() else None,
                                           "sha256": test_hash},
                "laya_runtime_manifest": {"path": laya_runtime.get("evidence_path"),
                                          "sha256": laya_runtime.get("evidence_sha256")},
                "source_identity_algorithm": identity["algorithm"],
                "source_identity_sha256": identity["digest"],
                "passed_source_sha256": args.source_sha,
                "working_tree_status_basis": "git status --porcelain",
            },
        })
        comparison["report_status"] = "COMPLETE"
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(json.dumps(comparison, ensure_ascii=False, indent=2, allow_nan=False) + "\n", encoding="utf-8")
        print(json.dumps({"report_status": comparison["report_status"],
                          "mandatory_rule_invariance": comparison["mandatory_rule_invariance"]["status"],
                          "output": str(output_path.resolve())}, ensure_ascii=False))
        return 1 if comparison["mandatory_rule_invariance"]["status"] != "PASS" else 0
    except (ComparisonError, OSError, TypeError, ValueError) as exc:
        print(f"compare_decisions: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())

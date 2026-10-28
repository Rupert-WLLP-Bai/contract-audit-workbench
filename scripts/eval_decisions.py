#!/usr/bin/env python3
"""Measure a real chat baseline on the frozen synthetic Chinese choice dataset."""
import argparse
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import time
import urllib.error
import urllib.request

INSTRUCTION = "根据材料选择唯一应优先核查的重点；多个同等问题、材料含糊或超出范围时选择 manual_review。只做分流，不判断合同结论。"


def percentile(values, fraction):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * fraction) - 1)] if ordered else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dataset", default="testdata/decision-zh-v1.json")
    parser.add_argument("--base-url", default="http://127.0.0.1:3425/v1")
    parser.add_argument("--model", default="group/auto-deepseek-v4-flash")
    parser.add_argument("--timeout", type=float, default=20)
    parser.add_argument("--out", default="artifacts/decision-llm.json")
    args = parser.parse_args()
    raw = Path(args.dataset).read_bytes()
    data = json.loads(raw)
    criteria = {item["id"]: item["description"] for item in data["candidates"]}
    report = {"schema_version": "decision-llm-report/v1", "classification": data["classification"],
              "dataset_sha256": hashlib.sha256(raw).hexdigest(), "configured_model": args.model,
              "started_at": dt.datetime.now(dt.timezone.utc).isoformat(), "rows": [],
              "limitations": ["D0 synthetic developer-visible cases; not independent business validation",
                              "Chat returns a choice; no invented LLM confidence",
                              "Sequential HTTP wall time includes local gateway and any upstream routing",
                              "No claim about TypeSafe Jev latency or accuracy from this chat-only baseline",
                              "No automatic contract disposition or mandatory-rule suppression"]}
    output = Path(args.out)
    output.parent.mkdir(parents=True, exist_ok=True)
    for item in data["cases"]:
        question = {"state": item["state"], "questions": {"review_focus": {
            "type": "choice", "instructions": INSTRUCTION, "criteria": criteria}}}
        payload = {"model": args.model, "temperature": 0, "max_tokens": 256, "reasoning_effort": "low",
                   "messages": [{"role": "user", "content": json.dumps(question, ensure_ascii=False, sort_keys=True)
                                 + '\n仅输出一个JSON对象，且只有choice字段，其值必须来自criteria的候选ID。'}]}
        result = {"id": item["id"], "expected": item["expected"], "choice": None,
                  "confidence": None, "error": None, "correct": False}
        started = time.perf_counter()
        try:
            request = urllib.request.Request(args.base_url.rstrip("/") + "/chat/completions",
                                             json.dumps(payload, ensure_ascii=False).encode(),
                                             {"Content-Type": "application/json"})
            with urllib.request.urlopen(request, timeout=args.timeout) as response:
                body = response.read(1024 * 1024 + 1)
            if len(body) > 1024 * 1024:
                raise ValueError("oversized_response")
            parsed = json.loads(body)
            first = parsed["choices"][0]
            if first.get("finish_reason") != "stop":
                raise ValueError("incomplete_completion")
            answer = json.loads(first["message"]["content"])
            if set(answer) != {"choice"} or answer["choice"] not in criteria:
                raise ValueError("invalid_choice")
            result.update(choice=answer["choice"], correct=answer["choice"] == item["expected"],
                          observed_model=parsed.get("model"), usage=parsed.get("usage"))
        except urllib.error.HTTPError as exc:
            result["error"] = "http_" + str(exc.code)
        except (OSError, ValueError, KeyError, IndexError, TypeError) as exc:
            result["error"] = type(exc).__name__
        result["duration_ms"] = round((time.perf_counter() - started) * 1000, 3)
        report["rows"].append(result)
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(f"{item['id']}: choice={result['choice']} ms={result['duration_ms']} error={result['error']}", flush=True)
    rows = report["rows"]
    durations = [row["duration_ms"] for row in rows]
    report["summary"] = {"total": len(rows), "correct": sum(row["correct"] for row in rows),
                         "errors": sum(row["error"] is not None for row in rows),
                         "manual_routed": sum(row["choice"] == "manual_review" for row in rows),
                         "p50_ms": percentile(durations, .5), "p95_ms": percentile(durations, .95),
                         "first_request_ms": durations[0],
                         "per_class": {key: {"total": sum(row["expected"] == key for row in rows),
                                             "correct": sum(row["expected"] == key and row["correct"] for row in rows)}
                                       for key in criteria}}
    report["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report["summary"], ensure_ascii=False))
    return 1 if report["summary"]["errors"] else 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Reproducible deterministic acceptance; optional real-browser release gate."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

from source_identity import source_identity

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "artifacts"


def validate_go_test_results(text):
    passed = 0
    skipped = []
    failed = []
    packages_without_tests = []
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as error:
            raise ValueError("Go test evidence is not JSONL") from error
        if not isinstance(row, dict):
            raise ValueError("Go test evidence contains a non-object record")
        action = row.get("Action")
        identity = f"{row.get('Package', '')}/{row.get('Test', '')}"
        if action == "skip":
            if row.get("Test"):
                skipped.append(identity)
            else:
                packages_without_tests.append(row.get("Package", ""))
        if action == "fail":
            failed.append(identity)
        if action == "pass" and row.get("Test"):
            passed += 1
    if skipped:
        raise ValueError("deterministic test suite skipped: " + ", ".join(skipped))
    if failed:
        raise ValueError("Go JSONL contains failed tests: " + ", ".join(failed))
    if passed < 1:
        raise ValueError("Go suite executed no passing test; empty -run matches are not acceptance")
    return {"test_pass_events": passed, "skipped": 0, "packages_without_tests": packages_without_tests}


def validate_web_test_results(value):
    if not isinstance(value, dict) or value.get("success") is not True:
        raise ValueError("Vitest did not report success")
    total, passed = value.get("numTotalTests"), value.get("numPassedTests")
    if type(total) is not int or type(passed) is not int or total < 1 or passed != total:
        raise ValueError("Vitest did not pass every declared test")
    for key in ("numFailedTests", "numPendingTests", "numTodoTests", "numFailedTestSuites"):
        if value.get(key, 0) != 0:
            raise ValueError(f"Vitest reported nonzero {key}")
    assertions = [assertion for suite in value.get("testResults", [])
                  for assertion in suite.get("assertionResults", [])]
    if len(assertions) != total or any(item.get("status") != "passed" for item in assertions):
        raise ValueError("Vitest assertion evidence is missing, skipped, todo, or failed")
    return {"tests": total, "passed": passed, "skipped": 0}


def validate_agent_demo(value):
    if (not isinstance(value, dict) or value.get("passed") is not True or
            value.get("classification") != "D0_SYNTHETIC_CONTROL_REGRESSION" or
            type(value.get("total")) is not int or value["total"] < 1 or
            value.get("passed_count") != value["total"] or
            len(value.get("results", [])) != value["total"] or
            not all(row.get("passed") is True and row.get("violations") == []
                    for row in value["results"])):
        raise ValueError("Agent control regression evidence is incomplete or failed")
    return {"synthetic_control_scenarios": value["total"], "live_model": "NOT_RUN"}


def validate_rule_shadow(value):
    dataset = (ROOT / "testdata/decision-zh-v1.json").read_bytes()
    if (value.get("schema_version") != "ruleshadow-report/v1" or value.get("passed") is not True or
            value.get("mode") != "disabled" or
            value.get("dataset_sha256") != hashlib.sha256(dataset).hexdigest()):
        raise ValueError("RulePack smoke identity or disabled mode is invalid")
    results = value.get("baseline", {}).get("results", [])
    expected = {"ENT-COM-ADV-001", "ENT-TECH-BW-001", "ENT-TECH-SLA-001"}
    if (len(results) != 3 or {row.get("rule_id") for row in results} != expected or
            any(row.get("relation") != "conflict" or row.get("mandatory") is not True for row in results)):
        raise ValueError("RulePack synthetic conflicts or mandatory rules are missing")
    rows = value.get("rows", [])
    ids = {item["id"] for item in json.loads(dataset)["cases"]}
    if (len(rows) != len(ids) or {row.get("id") for row in rows} != ids or
            any(row.get("rules_unchanged") is not True or
                row.get("rule_output_sha256") != value.get("baseline_sha256") or
                row.get("decision", {}).get("status") != "disabled" or
                row.get("decision", {}).get("accepted_rules") != [] for row in rows)):
        raise ValueError("Decision shadow changed rules or smoke evidence is incomplete")
    return {"mandatory_conflicts": 3, "invariance_cases": len(rows), "network": "disabled"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", action="store_true", help="require a real Chromium user journey")
    args = parser.parse_args()
    OUT.mkdir(exist_ok=True)
    logs = OUT / "logs"
    logs.mkdir(exist_ok=True)
    # Remove only generated gate inputs so a failed step cannot reuse a green report.
    for path in (OUT / "go-coverage.out", OUT / "coverage-summary.json", OUT / "evaluation.json",
                 OUT / "vitest-results.json", OUT / "agent-demo.json", OUT / "ruleshadow-disabled.json"):
        path.unlink(missing_ok=True)
    web_coverage = ROOT / "web/coverage"
    if web_coverage.exists():
        shutil.rmtree(web_coverage)
    go, bun = os.environ.get("GO", "go"), os.environ.get("BUN", "bun")
    env = dict(os.environ, APP_ENV="test")
    report = {"started_at": dt.datetime.now(dt.timezone.utc).isoformat(),
              "browser_requested": args.browser, "live_model": "NOT_RUN: explicit opt-in required",
              "checks": [], "passed": False}
    report["source_identity"] = source_identity(ROOT)

    def save():
        (OUT / "verification.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")

    def run(name, cmd, cwd=ROOT, timeout=300, validate=None):
        print(f"[{name}] {' '.join(cmd)}", flush=True)
        before = time.monotonic()
        log = logs / f"{name}.txt"
        try:
            with log.open("w") as handle:
                result = subprocess.run(cmd, cwd=cwd, env=env, stdout=handle, stderr=subprocess.STDOUT,
                                        timeout=timeout, check=False)
            code = result.returncode
        except (OSError, subprocess.TimeoutExpired) as error:
            with log.open("a") as handle:
                handle.write(str(error) + "\n")
            code = 127
        evidence = None
        if code == 0 and validate is not None:
            try:
                evidence = validate(log)
            except (OSError, ValueError, KeyError, TypeError) as error:
                with log.open("a") as handle:
                    handle.write(f"\nEVIDENCE CHECK FAILED: {error}\n")
                code = 1
        check = {"name": name, "returncode": code,
                                  "seconds": round(time.monotonic() - before, 3),
                                  "log": str(log.relative_to(ROOT))}
        if evidence is not None:
            check["evidence"] = evidence
        report["checks"].append(check)
        save()
        if code:
            print(log.read_text()[-6000:], file=sys.stderr)
            raise RuntimeError(f"{name} failed; see {log.relative_to(ROOT)}")

    try:
        run("go-version", [go, "version"])
        run("bun-version", [bun, "--version"])
        run("node-version", ["node", "--version"])
        run("quality-tests", [sys.executable, "-m", "unittest", "discover", "-s", "scripts", "-p", "test_*.py"])
        run("infra-tests", [sys.executable, "-m", "unittest", "discover", "-s", "deploy/tests", "-v"])
        run("format", [sys.executable, "scripts/check_format.py"])
        run("modules", [go, "mod", "verify"])
        run("vet", [go, "vet", "./..."])
        run("go-tests", [go, "test", "-race", "-count=1", "-json", "-timeout=20m",
                         "-covermode=atomic", "-coverpkg=./internal/...",
                         "-coverprofile=artifacts/go-coverage.out", "./..."], timeout=2400,
            validate=lambda log: validate_go_test_results(log.read_text()))
        run("web-typecheck", [bun, "run", "typecheck"], ROOT / "web")
        run("web-tests", [bun, "run", "test:coverage", "--reporter=default", "--reporter=json",
                          "--outputFile=../artifacts/vitest-results.json"], ROOT / "web",
            validate=lambda _: validate_web_test_results(json.loads((OUT / "vitest-results.json").read_text())))
        run("web-build", [bun, "run", "build"], ROOT / "web")
        # The deliverable is a source ZIP without Git metadata; parent checkout
        # state must not change whether this archive can be built.
        run("backend-build", [go, "build", "-buildvcs=false", "-trimpath", "-o", "artifacts/contractaudit", "./cmd/server"])
        for binary in ("worker", "toolgateway", "migrate"):
            run("backend-build-" + binary, [go, "build", "-buildvcs=false", "-trimpath",
                "-o", str(OUT / ("contractaudit-" + binary)), "./cmd/" + binary])
        run("evaluate", [go, "run", "./cmd/eval", "-out", "artifacts/evaluation.json"])
        run("agent-demo", [go, "run", "./cmd/agentdemo", "-out", "artifacts/agent-demo.json"],
            validate=lambda _: validate_agent_demo(json.loads((OUT / "agent-demo.json").read_text())))
        run("rule-shadow", [go, "run", "./cmd/ruleshadow", "-publish-demo", "-out", "artifacts/ruleshadow-disabled.json"],
            validate=lambda _: validate_rule_shadow(json.loads((OUT / "ruleshadow-disabled.json").read_text())))
        run("coverage-gates", [sys.executable, "scripts/check_coverage.py"])
        if args.browser:
            if not env.get("CHROMIUM_BIN"):
                candidates: list[str] = []
                try:
                    candidates.append(subprocess.run(
                        ["node", "-e", "process.stdout.write(require('./web/node_modules/playwright').chromium.executablePath())"],
                        cwd=ROOT, env=env, capture_output=True, text=True, check=True).stdout.strip())
                except (OSError, subprocess.SubprocessError):
                    pass
                candidates += ["/usr/bin/chromium", "/usr/bin/chromium-browser",
                               "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable"]
                executable = next((item for item in candidates if item and Path(item).is_file()), "")
                if not executable:
                    raise RuntimeError(
                        "Chromium unavailable. Run `cd web && bunx playwright install chromium`, "
                        "or set CHROMIUM_BIN."
                    )
                env["CHROMIUM_BIN"] = executable
            # Bun must also be visible to Go's exec.LookPath in the browser test.
            bun_path = shutil.which(bun)
            if bun_path:
                env["PATH"] = str(Path(bun_path).parent) + os.pathsep + env.get("PATH", "")
            run("browser-system", [go, "test", "-tags", "browser", "-count=1", "-json",
                                   "-timeout=150s", "./e2e", "-run", "^TestBrowser"], timeout=180,
                validate=lambda log: validate_go_test_results(log.read_text()))
        else:
            report["browser"] = "NOT_RUN: use make acceptance for release gate"
        if source_identity(ROOT) != report["source_identity"]:
            raise RuntimeError("Executable source changed during verification; rerun on a stable tree.")
        report["passed"] = True
        print("PASS: deterministic acceptance" + (" including Chromium" if args.browser else " (browser not requested)"))
        return 0
    except (RuntimeError, subprocess.CalledProcessError) as error:
        report["error"] = str(error)
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    finally:
        report["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
        save()


if __name__ == "__main__":
    raise SystemExit(main())

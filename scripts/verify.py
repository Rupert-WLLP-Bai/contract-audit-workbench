#!/usr/bin/env python3
"""Reproducible deterministic acceptance; optional real-browser release gate."""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass
from typing import Callable

from source_identity import source_identity

ROOT = Path(__file__).resolve().parents[1]
# CI keeps collecting the historical in-repository paths. Other runs keep
# verifier-owned artifacts in the sibling delivery tree.
def default_output(root, environ):
    if environ.get("CAW_DELIVERY_DIR"):
        return Path(environ["CAW_DELIVERY_DIR"]).expanduser().resolve() / "verification"
    if environ.get("GITHUB_ACTIONS") == "true":
        # Keep the paths consumed by existing CI artifact-upload steps.
        return root / "artifacts"
    return root.parent / "contract-audit-delivery" / "verification"


OUT = default_output(ROOT, os.environ)
OUT_IS_EXTERNAL = OUT.resolve() != (ROOT / "artifacts").resolve()

STAGES = ("static", "go", "web", "coverage", "browser")
# Only the outputs a stage owns are removed before it runs. The coverage stage
# consumes files produced by the go and web jobs, so it must never clean them.
STAGE_OUTPUTS = {
    "static": (),
    "go": ("artifacts/go-coverage.out", "artifacts/evaluation.json",
           "artifacts/agent-demo.json", "artifacts/ruleshadow-disabled.json"),
    "web": ("artifacts/vitest-results.json", "web/coverage"),
    "coverage": ("artifacts/coverage-summary.json",),
    "browser": ("artifacts/screenshots",),
}


@dataclass(frozen=True)
class Check:
    name: str
    stage: str
    cmd: tuple[str, ...]
    cwd: Path = ROOT
    timeout: int = 300
    jsonl: bool = False
    validate: Callable[[Path], dict] | None = None
    continue_on_failure: bool = False


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


def decode_go_test_log(text):
    """Render go test -json output as readable lines; keep non-JSON input as-is."""
    lines = []
    for line in text.splitlines():
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            lines.append(line)
            continue
        if not isinstance(row, dict):
            lines.append(line)
            continue
        action = row.get("Action")
        if action == "output":
            lines.append(str(row.get("Output", "")).rstrip("\n"))
        elif action == "fail" and not row.get("Test"):
            lines.append(f"FAIL\t{row.get('Package', '')}")
    return "\n".join(lines)


def workflow_escape(value, property_value=False):
    value = value.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    if property_value:
        value = value.replace(":", "%3A").replace(",", "%2C")
    return value


def workflow_error(title, message):
    if os.environ.get("GITHUB_ACTIONS") != "true":
        return
    print(f"::error title={workflow_escape(title, True)}::{workflow_escape(message)}")


def emit_failure_annotations(name, display):
    """Surface a failure as a check annotation, including a file/line when found."""
    if os.environ.get("GITHUB_ACTIONS") != "true":
        return
    workspace = os.environ.get("GITHUB_WORKSPACE", "").rstrip("/")
    if workspace:
        display = display.replace(workspace + "/", "")
    emitted = 0
    file_annotated = False
    for raw in display.splitlines():
        line = raw.strip()
        if line.startswith(("::error ", "::warning ", "::notice ")):
            print(line)
            emitted += 1
            if "file=" in line:
                file_annotated = True
        elif line.startswith("--- FAIL:"):
            workflow_error(name, line)
            emitted += 1
        if emitted >= 20:
            break
    if file_annotated:
        return
    frame = re.search(r"(?<![\w/.-])((?:web/e2e|internal|e2e|cmd)/[^\s:()]+?\.(?:ts|tsx|go)):(\d+)(?::(\d+))?",
                      display)
    if frame:
        filename, line, column = frame.group(1), frame.group(2), frame.group(3)
        suffix = f",col={column}" if column else ""
        print(f"::error file={workflow_escape(filename, True)},line={line}{suffix},"
              f"title={workflow_escape(name, True)}::{workflow_escape(name + ' failed')}")
        return
    if emitted:
        return
    workflow_error(name, f"{name} failed; see artifacts/logs/{name}.txt")


def write_step_summary(report):
    destination = os.environ.get("GITHUB_STEP_SUMMARY")
    if not destination:
        return
    status = "PASS" if report.get("passed") else "FAIL"
    lines = [f"## {report.get('stage', 'all')} gate: {status}", ""]
    digest = report.get("source_identity", {}).get("digest")
    if digest:
        lines.extend([f"source identity: `{digest}`", ""])
    lines.extend(["| check | result | seconds | log |", "| --- | --- | --- | --- |"])
    for check in report.get("checks", []):
        lines.append(f"| {check['name']} | {check.get('status', 'FAIL')} | "
                     f"{check.get('seconds', 0)} | `{check.get('log', '')}` |")
    if report.get("error"):
        lines.extend(["", f"error: {report['error']}"])
    with open(destination, "a", encoding="utf-8") as handle:
        handle.write("\n".join(lines) + "\n")


def report_path(stage):
    if stage == "all":
        return OUT / "verification.json"
    return OUT / f"verification-{stage}.json"


def save_report(report):
    report_path(report.get("stage", "all")).write_text(
        json.dumps(report, ensure_ascii=False, indent=2) + "\n")


def clean_outputs(paths):
    for relative in paths:
        if relative.startswith("artifacts/") and OUT_IS_EXTERNAL:
            path = OUT / relative.removeprefix("artifacts/")
        elif relative == "web/coverage" and OUT_IS_EXTERNAL:
            path = OUT / "web-coverage"
        else:
            path = ROOT / relative
        if path.is_dir() and not path.is_symlink():
            shutil.rmtree(path)
        else:
            path.unlink(missing_ok=True)


def selected_check(check, stage, browser):
    if stage == "all":
        return check.stage != "browser" or browser
    return check.stage == stage


def command_with_output_dir(check):
    """Keep generated artifacts with the run outside source/CI collection paths."""
    if not OUT_IS_EXTERNAL:
        return list(check.cmd)
    if check.name == "coverage-gates":
        return [*check.cmd, "--go", str(OUT / "go-coverage.out"),
                "--web", str(OUT / "web-coverage" / "coverage-summary.json"),
                "--out", str(OUT / "coverage-summary.json")]
    def relocate(argument):
        option, separator, value = argument.partition("=")
        path_value = value if separator else argument
        if path_value.startswith("artifacts/"):
            path_value = str(OUT / path_value.removeprefix("artifacts/"))
        elif path_value.startswith("../artifacts/") and check.cwd == ROOT / "web":
            path_value = str(OUT / path_value.removeprefix("../artifacts/"))
        return f"{option}={path_value}" if separator else path_value

    command = [relocate(argument) for argument in check.cmd]
    if check.name == "web-tests":
        command.append(f"--coverage.reportsDirectory={OUT / 'web-coverage'}")
    return command


def evidence_path(path):
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        return str(path.resolve())


def prepare_browser(env):
    if not env.get("CHROMIUM_BIN"):
        candidates = []
        try:
            candidates.append(subprocess.run(
                ["node", "-e",
                 "process.stdout.write(require('./web/node_modules/playwright').chromium.executablePath())"],
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
    bun_path = shutil.which(env.get("BUN", "bun"))
    if bun_path:
        env["PATH"] = str(Path(bun_path).parent) + os.pathsep + env.get("PATH", "")


def build_checks(go, bun):
    return (
        Check("go-version", "all", (go, "version")),
        Check("bun-version", "all", (bun, "--version")),
        Check("node-version", "all", ("node", "--version")),
        Check("quality-tests", "static",
              (sys.executable, "-m", "unittest", "discover", "-s", "scripts", "-p", "test_*.py")),
        Check("infra-tests", "static",
              (sys.executable, "-m", "unittest", "discover", "-s", "deploy/tests", "-v")),
        Check("fault-harness-tests", "static",
              (sys.executable, "-m", "unittest", "deployments.scripts.test_fault_test_local")),
        Check("format", "static", (sys.executable, "scripts/check_format.py")),
        Check("modules", "static", (go, "mod", "verify")),
        Check("vet", "static", (go, "vet", "./...")),
        Check("go-tests", "go",
              (go, "test", "-race", "-count=1", "-json", "-timeout=20m",
               "-covermode=atomic", "-coverpkg=./internal/...",
               "-coverprofile=artifacts/go-coverage.out", "./..."),
              timeout=2400, jsonl=True,
              validate=lambda path: validate_go_test_results(path.read_text())),
        Check("web-typecheck", "web", (bun, "run", "typecheck"), cwd=ROOT / "web"),
        Check("web-tests", "web",
              (bun, "run", "test:coverage", "--reporter=default", "--reporter=json",
               "--outputFile=../artifacts/vitest-results.json"), cwd=ROOT / "web",
              validate=lambda _: validate_web_test_results(
                  json.loads((OUT / "vitest-results.json").read_text()))),
        Check("web-c1b-clock-tests", "browser",
              (bun, "test", "e2e/c1b-clock.test.ts"), cwd=ROOT / "web"),
        Check("web-build", "web", (bun, "run", "build"), cwd=ROOT / "web"),
        Check("backend-build", "go",
              (go, "build", "-buildvcs=false", "-trimpath",
               "-o", "artifacts/contractaudit", "./cmd/server")),
        Check("backend-build-worker", "go",
              (go, "build", "-buildvcs=false", "-trimpath",
               "-o", "artifacts/contractaudit-worker", "./cmd/worker")),
        Check("backend-build-toolgateway", "go",
              (go, "build", "-buildvcs=false", "-trimpath",
               "-o", "artifacts/contractaudit-toolgateway", "./cmd/toolgateway")),
        Check("backend-build-migrate", "go",
              (go, "build", "-buildvcs=false", "-trimpath",
               "-o", "artifacts/contractaudit-migrate", "./cmd/migrate")),
        Check("evaluate", "go",
              (go, "run", "./cmd/eval", "-out", "artifacts/evaluation.json")),
        Check("agent-demo", "go",
              (go, "run", "./cmd/agentdemo", "-out", "artifacts/agent-demo.json"),
              validate=lambda _: validate_agent_demo(
                  json.loads((OUT / "agent-demo.json").read_text()))),
        Check("rule-shadow", "go",
              (go, "run", "./cmd/ruleshadow", "-publish-demo",
               "-out", "artifacts/ruleshadow-disabled.json"),
              validate=lambda _: validate_rule_shadow(
                  json.loads((OUT / "ruleshadow-disabled.json").read_text()))),
        Check("coverage-gates", "coverage", (sys.executable, "scripts/check_coverage.py")),
        Check("browser-warranty", "browser",
              (go, "test", "-tags", "browser", "-count=1", "-json", "-timeout=150s",
               "./e2e", "-run", "^TestBrowserWarrantyConflictAndConfirm$"),
              timeout=180, jsonl=True, continue_on_failure=True,
              validate=lambda path: validate_go_test_results(path.read_text())),
        Check("browser-telecom", "browser",
              (go, "test", "-tags", "browser", "-count=1", "-json", "-timeout=330s",
               "./e2e", "-run", "^TestBrowserTelecomC1BFullJourney$"),
              timeout=360, jsonl=True, continue_on_failure=True,
              validate=lambda path: validate_go_test_results(path.read_text())),
        Check("browser-telecom-compatible", "browser",
              (go, "test", "-tags", "browser", "-count=1", "-json", "-timeout=330s",
               "./e2e", "-run", "^TestBrowserTelecomC1BCompatibleScenario$"),
              timeout=360, jsonl=True, continue_on_failure=True,
              validate=lambda path: validate_go_test_results(path.read_text())),
        Check("browser-telecom-insufficient", "browser",
              (go, "test", "-tags", "browser", "-count=1", "-json", "-timeout=330s",
               "./e2e", "-run", "^TestBrowserTelecomC1BInsufficientScenario$"),
              timeout=360, jsonl=True, continue_on_failure=True,
              validate=lambda path: validate_go_test_results(path.read_text())),
    )


def run_check(check, env, report, logs):
    command = command_with_output_dir(check)
    print(f"[{check.name}] {' '.join(command)}", flush=True)
    before = time.monotonic()
    raw = logs / (check.name + (".jsonl" if check.jsonl else ".txt"))
    display_path = logs / (check.name + ".txt")
    try:
        with raw.open("w") as handle:
            result = subprocess.run(command, cwd=check.cwd, env=env, stdout=handle,
                                    stderr=subprocess.STDOUT, timeout=check.timeout, check=False)
        code = result.returncode
    except (OSError, subprocess.TimeoutExpired) as error:
        with raw.open("a") as handle:
            handle.write(str(error) + "\n")
        code = 127
    raw_text = raw.read_text(errors="replace")
    if check.jsonl:
        display = decode_go_test_log(raw_text)
        display_path.write_text(display + ("\n" if display else ""))
    else:
        display = raw_text
    evidence = None
    failure = None
    if code == 0 and check.validate is not None:
        try:
            evidence = check.validate(raw)
        except (OSError, ValueError, KeyError, TypeError) as error:
            failure = f"EVIDENCE CHECK FAILED: {error}"
            with display_path.open("a") as handle:
                handle.write(f"\n{failure}\n")
            display += f"\n{failure}"
            code = 1
    record = {
        "name": check.name,
        "stage": check.stage,
        "status": "PASS" if code == 0 else "FAIL",
        "returncode": code,
        "seconds": round(time.monotonic() - before, 3),
        "log": evidence_path(display_path),
    }
    if check.jsonl:
        record["raw_log"] = evidence_path(raw)
    if evidence is not None:
        record["evidence"] = evidence
    if failure:
        record["failure"] = failure
    report["checks"].append(record)
    save_report(report)
    if code:
        emit_failure_annotations(check.name, display)
        print(display[-6000:], file=sys.stderr)
        return False
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", action="store_true",
                        help="require a real Chromium user journey")
    parser.add_argument("--stage", choices=(*STAGES, "all"), default="all",
                        help="run only one gate stage (default: all)")
    args = parser.parse_args()
    OUT.mkdir(parents=True, exist_ok=True)
    logs = OUT / "logs"
    logs.mkdir(exist_ok=True)
    stages = STAGE_OUTPUTS if args.stage == "all" else (args.stage,)
    for stage in stages:
        clean_outputs(STAGE_OUTPUTS[stage])
    go, bun = os.environ.get("GO", "go"), os.environ.get("BUN", "bun")
    # Verification never calls a hosted model. The fixture tests set their own
    # mode explicitly, so an inherited developer shell cannot turn this into a
    # paid external run.
    env = dict(os.environ, APP_ENV="test", TYPESAFE_JEV_MODE="off")
    if OUT_IS_EXTERNAL:
        env["CAW_DELIVERY_DIR"] = str(OUT.parent)
        env["E2E_SCREENSHOT_DIR"] = str(OUT / "screenshots")
    env.pop("TYPESAFE_API_KEY", None)
    browser_selected = args.browser or args.stage == "browser"
    report = {
        "started_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "stage": args.stage,
        "browser_requested": browser_selected,
        "live_model": "NOT_RUN: explicit opt-in required",
        "checks": [],
        "passed": False,
    }
    report["source_identity"] = source_identity(ROOT)
    checks = build_checks(go, bun)
    selected = [check for check in checks
                if selected_check(check, args.stage, args.browser)]
    if not selected:
        print(f"FAIL: stage {args.stage} selected no checks", file=sys.stderr)
        return 2
    browser_selected = any(check.stage == "browser" for check in selected)
    report["browser_requested"] = browser_selected
    report["browser"] = "NOT_RUN: use make acceptance for release gate"
    executed = set()
    failures = []
    try:
        if browser_selected:
            prepare_browser(env)
        for check in selected:
            executed.add(check.name)
            if not run_check(check, env, report, logs):
                if not check.continue_on_failure:
                    raise RuntimeError(
                        f"{check.name} failed; see artifacts/logs/{check.name}.txt")
                failures.append(check.name)
        report["browser"] = ("PASS" if browser_selected else
                             "NOT_RUN: use make acceptance for release gate")
        if failures:
            report["browser"] = "FAIL"
            raise RuntimeError(
                f"{', '.join(failures)} failed; see artifacts/logs/")
        if source_identity(ROOT) != report["source_identity"]:
            raise RuntimeError(
                "Executable source changed during verification; rerun on a stable tree.")
        report["passed"] = True
        print("PASS: deterministic acceptance"
              + (" including Chromium" if browser_selected else ""))
        return 0
    except (RuntimeError, subprocess.CalledProcessError) as error:
        report["error"] = str(error)
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    finally:
        for check in selected:
            if check.name not in executed:
                report["checks"].append({
                    "name": check.name, "stage": check.stage, "status": "NOT_RUN",
                    "returncode": None, "seconds": 0, "log": "",
                })
        report["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
        save_report(report)
        write_step_summary(report)


if __name__ == "__main__":
    raise SystemExit(main())

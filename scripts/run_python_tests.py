#!/usr/bin/env python3
"""Run each discovered unittest file in its own interpreter and emit JSON evidence."""
from __future__ import annotations

import argparse
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import traceback
import unittest

SCHEMA = "python-test-gate/v1"
EXCLUDED_DIRS = {
    ".git", ".hg", ".svn", "node_modules", "vendor", "cache", "caches",
    "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox",
    ".venv", "venv",
}


class CountingResult(unittest.TestResult):
    """Retain unittest's native counters and serialize a compact outcome."""

    def as_dict(self) -> dict:
        return {
            "tests_run": self.testsRun,
            "failures": len(self.failures),
            "errors": len(self.errors),
            "skipped": len(self.skipped),
            "expected_failures": len(self.expectedFailures),
            "unexpected_successes": len(self.unexpectedSuccesses),
            "failure_details": [
                {"test": str(test), "traceback": detail, "kind": "failure_or_error"}
                for test, detail in self.failures + self.errors
            ] + [
                {"test": str(test), "traceback": detail, "kind": "expected_failure"}
                for test, detail in self.expectedFailures
            ],
            "expected_failure_details": [
                {"test": str(test), "traceback": detail}
                for test, detail in self.expectedFailures
            ],
            "skipped_tests": [str(test) for test, _ in self.skipped],
            "unexpected_success_tests": [str(test) for test in self.unexpectedSuccesses],
        }


def _module_name(path: Path) -> str:
    digest = hashlib.sha256(str(path.resolve()).encode("utf-8")).hexdigest()[:20]
    return f"_caw_isolated_test_{digest}"


def _child_main(test_file: Path, search_roots: list[Path]) -> int:
    result_data = {
        "path": str(test_file), "status": "NOT_RUN", "tests_run": 0,
        "failures": 0, "errors": 0, "skipped": 0,
        "expected_failures": 0, "unexpected_successes": 0,
        "failure_details": [], "expected_failure_details": [], "skipped_tests": [],
        "unexpected_success_tests": [],
    }
    try:
        # unittest discover makes the selected test directory importable. Add
        # each explicit root too, so sibling helpers can be imported without
        # sharing the test module itself across per-file processes.
        containing_roots = sorted(
            (root.resolve() for root in search_roots
             if test_file.resolve().is_relative_to(root.resolve())),
            key=lambda path: len(path.parts), reverse=True,
        )
        import_paths = list(dict.fromkeys(
            str(path) for path in (test_file.parent.resolve(), *containing_roots)
        ))
        sys.path[:0] = [path for path in import_paths if path not in sys.path]
        spec = importlib.util.spec_from_file_location(_module_name(test_file), test_file)
        if spec is None or spec.loader is None:
            raise ImportError(f"cannot create import spec for {test_file}")
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        # Route test print output to the child stderr pipe so it remains
        # available if the parent kills a timed-out child before JSON is emitted.
        with contextlib.redirect_stdout(sys.stderr), contextlib.redirect_stderr(sys.stderr):
            spec.loader.exec_module(module)
            suite = unittest.defaultTestLoader.loadTestsFromModule(module)
            result = CountingResult()
            suite.run(result)
        result_data.update(result.as_dict())
        if result_data["tests_run"] == 0:
            result_data["status"] = "NOT_RUN"
        elif any(result_data[key] for key in (
            "failures", "errors", "skipped", "expected_failures", "unexpected_successes"
        )):
            result_data["status"] = "FAIL"
        else:
            result_data["status"] = "PASS"
    except BaseException as error:  # serialize import/collection failures as evidence
        result_data["status"] = "FAIL"
        result_data["errors"] = max(1, result_data["errors"])
        result_data["failure_details"] = [{
            "test": "<module import/collection>", "kind": "import_or_collection_error",
            "traceback": "".join(traceback.format_exception(type(error), error, error.__traceback__)),
        }]
    print(json.dumps(result_data, ensure_ascii=False, sort_keys=True))
    return 0 if result_data["status"] == "PASS" else 1


def discover(roots: list[Path]) -> list[Path]:
    found: set[Path] = set()
    for root in roots:
        for directory, names, files in os.walk(root):
            names[:] = sorted(name for name in names if name.lower() not in EXCLUDED_DIRS)
            for filename in files:
                if filename.startswith("test_") and filename.endswith(".py"):
                    path = (Path(directory) / filename).resolve()
                    found.add(path)
    return sorted(found, key=lambda path: str(path))


def _text_output(value: str | bytes | None) -> str:
    if value is None:
        return ""
    return value.decode("utf-8", errors="replace") if isinstance(value, bytes) else value


def positive_seconds(raw: str) -> float:
    try:
        seconds = float(raw)
    except ValueError as error:
        raise argparse.ArgumentTypeError("must be a positive finite number of seconds") from error
    if not (0 < seconds < float("inf")):
        raise argparse.ArgumentTypeError("must be a positive finite number of seconds")
    return seconds


def run_parent(args: argparse.Namespace) -> int:
    roots = [path.expanduser().resolve() for path in args.root]
    invalid = [str(path) for path in roots if not path.is_dir()]
    if invalid:
        report = {
            "schema": SCHEMA, "status": "FAIL", "roots": [str(p) for p in roots],
            "discovered_files": 0, "tests_run": 0, "files": [],
            "error": "root is not a directory: " + ", ".join(invalid),
        }
        _emit(report, args.json_out)
        return 1

    files = discover(roots)
    records = []
    for test_file in files:
        command = [sys.executable, str(Path(__file__).resolve()), "--_child",
                   str(test_file)]
        for root in roots:
            command.extend(("--_search-root", str(root)))
        try:
            completed = subprocess.run(command, capture_output=True, text=True,
                                       timeout=args.timeout, check=False)
            lines = [line for line in completed.stdout.splitlines() if line.strip()]
            record = json.loads(lines[-1]) if lines else {
                "path": str(test_file), "status": "FAIL", "tests_run": 0,
                "failures": 0, "errors": 1, "skipped": 0,
                "expected_failures": 0, "unexpected_successes": 0,
                "failure_details": [{
                    "test": "<child process>", "kind": "invalid_child_result",
                    "traceback": completed.stderr or "child produced no JSON result",
                }],
                "expected_failure_details": [],
                "skipped_tests": [], "unexpected_success_tests": [],
            }
            if completed.stderr:
                record["child_stderr"] = completed.stderr
            record["child_returncode"] = completed.returncode
            if completed.returncode != 0 and record.get("status") == "PASS":
                record["status"] = "FAIL"
                record.setdefault("failure_details", []).append({
                    "test": "<child process>", "kind": "nonzero_child_exit",
                    "traceback": completed.stderr or f"child exit code {completed.returncode}",
                })
        except subprocess.TimeoutExpired as error:
            child_stdout = _text_output(error.stdout)
            child_stderr = _text_output(error.stderr)
            record = {
                "path": str(test_file), "status": "FAIL", "tests_run": 0,
                "failures": 0, "errors": 0, "skipped": 0,
                "expected_failures": 0, "unexpected_successes": 0,
                "timed_out": True, "timeout_seconds": args.timeout,
                "failure_details": [{
                    "test": "<child process>", "kind": "timeout",
                    "traceback": f"test file exceeded {args.timeout:g}s timeout",
                }],
                "expected_failure_details": [], "skipped_tests": [],
                "unexpected_success_tests": [],
                "child_stdout": child_stdout, "child_stderr": child_stderr,
            }
        except (OSError, json.JSONDecodeError) as error:
            record = {
                "path": str(test_file), "status": "FAIL", "tests_run": 0,
                "failures": 0, "errors": 1, "skipped": 0,
                "expected_failures": 0, "unexpected_successes": 0,
                "failure_details": [{"test": "<child process>", "kind": "spawn_or_json_error",
                                     "traceback": str(error)}],
                "expected_failure_details": [],
                "skipped_tests": [], "unexpected_success_tests": [],
            }
        records.append(record)

    totals = {
        key: sum(int(record.get(key, 0)) for record in records)
        for key in ("tests_run", "failures", "errors", "skipped",
                    "expected_failures", "unexpected_successes")
    }
    if any(record.get("status") == "FAIL" for record in records):
        status = "FAIL"
    elif not files or totals["tests_run"] == 0:
        status = "NOT_RUN"
    elif any(record.get("status") != "PASS" for record in records):
        status = "FAIL"
    else:
        status = "PASS"
    report = {
        "schema": SCHEMA, "status": status, "roots": [str(p) for p in roots],
        "discovered_files": len(files), **totals, "files": records,
    }
    _emit(report, args.json_out)
    return 0 if status == "PASS" else 1


def _emit(report: dict, destination: str | None) -> None:
    rendered = json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True)
    if destination:
        path = Path(destination).expanduser().resolve()
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(rendered + "\n", encoding="utf-8")
    print(rendered)


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv and argv[0] == "--_child":
        parser = argparse.ArgumentParser(add_help=False)
        parser.add_argument("--_child", dest="test_file", type=Path, required=True)
        parser.add_argument("--_search-root", action="append", type=Path, default=[])
        child_args = parser.parse_args(argv)
        return _child_main(child_args.test_file.resolve(), child_args._search_root)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", action="append", type=Path, default=[],
                        help="test root; repeat to add roots (default: scripts, deploy/tests, deployments/scripts)")
    parser.add_argument("--json-out", help="also write the JSON report to this path")
    parser.add_argument("--timeout", type=positive_seconds, default=300.0,
                        help="maximum seconds for each test-file child process (default: 300)")
    args = parser.parse_args(argv)
    if not args.root:
        args.root = [Path("scripts"), Path("deploy/tests"), Path("deployments/scripts")]
    return run_parent(args)


if __name__ == "__main__":
    raise SystemExit(main())

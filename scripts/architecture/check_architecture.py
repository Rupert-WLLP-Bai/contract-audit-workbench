#!/usr/bin/env python3
"""Check one configured Go package's direct production imports."""
from __future__ import annotations

import argparse
import hashlib
import json
import platform
import shlex
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath


SCRIPT_DIR = Path(__file__).resolve().parent
CONFIG_PATH = SCRIPT_DIR / "architecture.json"


class Blocked(Exception):
    """The requested source/environment could not be checked reliably."""


def run_command(argv: list[str], cwd: Path, *, timeout: int = 60) -> str:
    try:
        result = subprocess.run(
            argv,
            cwd=cwd,
            text=True,
            encoding="utf-8",
            errors="replace",
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise Blocked(f"Command unavailable or timed out: {argv!r}: {exc}") from exc
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()[:4000]
        raise Blocked(f"Command failed ({result.returncode}): {argv!r}: {detail}")
    return result.stdout


def load_config() -> tuple[dict, str]:
    try:
        raw = CONFIG_PATH.read_bytes()
        config = json.loads(raw)
    except (OSError, json.JSONDecodeError) as exc:
        raise Blocked(f"Cannot read architecture config: {exc}") from exc
    if not isinstance(config, dict) or config.get("schema_version") != 1:
        raise Blocked("architecture.json must be an object with schema_version=1")
    module = config.get("module")
    pilot = config.get("pilot")
    allowed = config.get("allowed_direct_imports")
    if not isinstance(module, str) or not module or any(char.isspace() for char in module):
        raise Blocked("Configuration module must be a non-empty Go module path")
    if not isinstance(pilot, str) or not pilot:
        raise Blocked("Configuration pilot must be a non-empty relative package path")
    parts = PurePosixPath(pilot)
    if parts.is_absolute() or ".." in parts.parts or "." in parts.parts or "\\" in pilot:
        raise Blocked("Configuration pilot must stay inside the repository")
    if str(parts) != pilot or module.startswith("/") or module.endswith("/"):
        raise Blocked("Configuration paths must be canonical")
    if not isinstance(allowed, list) or not allowed or not all(
        isinstance(item, str) and item and not any(char.isspace() for char in item)
        for item in allowed
    ):
        raise Blocked("allowed_direct_imports must be a non-empty string array")
    if len(set(allowed)) != len(allowed):
        raise Blocked("allowed_direct_imports contains duplicates")
    return config, hashlib.sha256(raw).hexdigest()


def decode_go_list(raw: str) -> list[dict]:
    """Decode go list -json's concatenated JSON objects."""
    decoder = json.JSONDecoder()
    records: list[dict] = []
    offset = 0
    try:
        while offset < len(raw):
            while offset < len(raw) and raw[offset].isspace():
                offset += 1
            if offset == len(raw):
                break
            record, offset = decoder.raw_decode(raw, offset)
            if not isinstance(record, dict):
                raise Blocked("Each GoList record must be a JSON object")
            records.append(record)
    except json.JSONDecodeError as exc:
        raise Blocked(f"GoList returned invalid JSON: {exc}") from exc
    if not records:
        raise Blocked("GoList returned no package records")
    for record in records:
        if record.get("Error") or record.get("DepsErrors") or record.get("Incomplete"):
            raise Blocked("GoList reported an incomplete package or dependency error")
        if not isinstance(record.get("ImportPath"), str) or not record["ImportPath"]:
            raise Blocked("GoList record is missing a valid ImportPath")
    return records


def build_tags_from_goflags(go_flags: str) -> list[str]:
    try:
        flags = shlex.split(go_flags)
    except ValueError as exc:
        raise Blocked(f"GOFLAGS could not be parsed: {exc}") from exc
    tags: list[str] = []
    index = 0
    while index < len(flags):
        item = flags[index]
        if item == "-tags":
            index += 1
            if index >= len(flags):
                raise Blocked("GOFLAGS has -tags without a value")
            value = flags[index]
        elif item.startswith("-tags="):
            value = item.partition("=")[2]
        else:
            index += 1
            continue
        if not value:
            raise Blocked("GOFLAGS has an empty -tags value")
        tags.extend(tag for tag in value.split(",") if tag)
        index += 1
    return sorted(set(tags))


def inspect_package(records: list[dict], expected: str, allowed: list[str]) -> dict:
    matches = [record for record in records if record["ImportPath"] == expected]
    if len(matches) != 1:
        raise Blocked(f"Expected exactly one target package {expected}; found {len(matches)}")
    package = matches[0]
    go_files = package.get("GoFiles")
    if not isinstance(go_files, list) or not go_files or not all(
        isinstance(name, str) and name.endswith(".go") for name in go_files
    ):
        raise Blocked("Target package has missing, invalid, or zero GoFiles")
    imports = package.get("Imports", [])
    if not isinstance(imports, list) or not all(isinstance(item, str) and item for item in imports):
        raise Blocked("Target production Imports must be a string array")
    actual = sorted(set(imports))
    return {
        "package": expected,
        "go_files": sorted(go_files),
        "direct_production_imports": actual,
        "forbidden_imports": sorted(set(actual) - set(allowed)),
        "excluded_go_list_fields": ["TestImports", "XTestImports", "Deps"],
    }


def inspect_go_environment(repo: Path) -> dict:
    raw = run_command(
        ["go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "CGO_ENABLED", "GOFLAGS", "GOENV", "GOWORK"],
        repo,
    )
    try:
        environment = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise Blocked(f"go env returned invalid JSON: {exc}") from exc
    required_strings = ("GOOS", "GOARCH", "GOVERSION", "CGO_ENABLED", "GOFLAGS")
    if not isinstance(environment, dict) or any(
        not isinstance(environment.get(key), str)
        or (key != "GOFLAGS" and not environment[key])
        for key in required_strings
    ):
        raise Blocked("go env omitted required Go build context values")
    if environment["CGO_ENABLED"] not in ("0", "1"):
        raise Blocked("go env returned invalid CGO_ENABLED")
    environment["build_tags"] = build_tags_from_goflags(environment["GOFLAGS"])
    return environment


def inspect_repo(repo_arg: str, config: dict, report: dict) -> None:
    repo = Path(repo_arg).expanduser().absolute()
    if not repo.is_dir():
        raise Blocked("Repository path is not a directory")
    report["source"]["repository_path"] = str(repo)
    source_sha = run_command(["git", "rev-parse", "HEAD"], repo).strip()
    if not source_sha:
        raise Blocked("Repository source SHA could not be determined")
    report["source"]["source_sha"] = source_sha
    status = run_command(["git", "status", "--porcelain", "--untracked-files=normal"], repo)
    dirty_paths = [line[3:] for line in status.splitlines() if len(line) >= 4]
    report["source"]["working_tree_dirty"] = bool(dirty_paths)
    report["source"]["dirty_paths"] = dirty_paths

    report["environment"]["go_commands_executed"] = True
    environment = inspect_go_environment(repo)
    report["environment"]["go"] = environment
    report["environment"]["build_context"] = {
        "GOOS": environment["GOOS"],
        "GOARCH": environment["GOARCH"],
        "CGO_ENABLED": environment["CGO_ENABLED"],
        "GOFLAGS": environment["GOFLAGS"],
        "build_tags_from_GOFLAGS": environment["build_tags"],
    }
    argv = ["go", "list", "-json", "./" + config["pilot"]]
    report["environment"]["go_list_command"] = argv
    raw = run_command(argv, repo)
    expected = config["module"] + "/" + config["pilot"]
    report["inspection"] = inspect_package(
        decode_go_list(raw), expected, config["allowed_direct_imports"],
    )


def inspect_fixture(path: str, config: dict, report: dict) -> None:
    fixture = Path(path).expanduser().absolute()
    try:
        raw = fixture.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise Blocked(f"Cannot read GoList fixture: {exc}") from exc
    report["source"]["fixture_path"] = str(fixture)
    report["source"]["input_sha256"] = hashlib.sha256(raw.encode("utf-8")).hexdigest()
    expected = config["module"] + "/" + config["pilot"]
    report["inspection"] = inspect_package(
        decode_go_list(raw), expected, config["allowed_direct_imports"],
    )


def write_report(report: dict, destination: str | None) -> int:
    if report["status"] == "FAIL":
        report["exit_code"] = 1
    elif report["status"] == "BLOCKED":
        report["exit_code"] = 2
    else:
        report["exit_code"] = 0
    encoded = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    if destination:
        try:
            target = Path(destination).expanduser()
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(encoded, encoding="utf-8")
        except OSError as exc:
            report["status"] = "BLOCKED"
            report["exit_code"] = 2
            report.setdefault("errors", []).append(f"Unable to write report: {exc}")
            print(json.dumps(report, ensure_ascii=False, indent=2), file=sys.stderr)
            return 2
    else:
        print(encoded, end="")
    return report["exit_code"]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--repo", help="Local Git checkout; runs go list for configured pilot")
    source.add_argument("--fixture", help="Synthetic go list JSON; never runs Git or Go")
    parser.add_argument("--out", help="Write JSON report here; otherwise print to stdout")
    args = parser.parse_args()
    fixture_mode = args.fixture is not None
    report = {
        "schema_version": 1,
        "check": "direct-production-import-allowlist",
        "created_at_utc": datetime.now(timezone.utc).isoformat(),
        "evidence_kind": "synthetic_fixture" if fixture_mode else "repository_static_check",
        "status": "BLOCKED",
        "exit_code": 2,
        "application_test_status": "NOT_RUN",
        "source": {
            "kind": "synthetic_fixture" if fixture_mode else "git_working_tree",
            "source_sha": None,
            "working_tree_dirty": None,
            "dirty_paths": None,
        },
        "environment": {
            "python": platform.python_version(),
            "platform": platform.platform(),
            "go_commands_executed": False,
        },
        "scope": {
            "target": "configured pilot package only",
            "imports": "direct production Imports only",
            "test_imports_included": False,
            "transitive_dependencies_included": False,
            "modelgateway_scanned": False,
            "reverse_store_to_contractmoney_edge_prohibited": False,
            "build_context": "fixture only" if fixture_mode else "current GOOS/GOARCH/CGO_ENABLED/GOFLAGS only",
            "all_build_tags_covered": False,
            "whole_repository_pass_claim": False,
        },
        "errors": [],
        "violations": [],
    }
    try:
        config, config_sha = load_config()
        report["configuration"] = {
            "path": str(CONFIG_PATH),
            "sha256": config_sha,
            **config,
        }
        report["scope"]["target_package"] = config["module"] + "/" + config["pilot"]
        if fixture_mode:
            inspect_fixture(args.fixture, config, report)
        else:
            inspect_repo(args.repo, config, report)
        forbidden = report["inspection"]["forbidden_imports"]
        report["violations"] = [
            {"rule": "configured-direct-import-allowlist", "from": report["inspection"]["package"], "to": target}
            for target in forbidden
        ]
        report["status"] = "FAIL" if forbidden else "PASS"
    except (Blocked, OSError, ValueError, TypeError) as exc:
        report["errors"].append(str(exc))
    return write_report(report, args.out)


if __name__ == "__main__":
    raise SystemExit(main())

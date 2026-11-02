#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: 读取命名 Go 包依赖策略并对照 go list imports，报告允许边、违规边及未声明包。
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-01
# Updated: 2026-11-02
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Check named Go package direct-import policies against go list."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import re
import shlex
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath


SCRIPT_DIR = Path(__file__).resolve().parent
CONFIG_PATH = SCRIPT_DIR / "architecture.json"
SCHEMA_PATH = SCRIPT_DIR / "architecture.schema.json"
SUPPORTED_SCHEMA_CANONICAL_SHA256 = (
    "977287ff43a8f8f9681f1def9cedc34ae084ae4d357cd1cdc7cc036de960c9d2"
)
REQUIRED_POLICY_RULES = {
    "contractmoney-pure-domain": {
        "contractmoney-allowlist": {
            "package": "internal/contractmoney",
            "mode": "allowlist",
            "allowed_direct_imports": {
                "errors", "fmt", "math/big", "strconv", "strings",
            },
        },
    },
    "advisory-contract-boundary": {
        "modelgateway-no-orchestration-or-facade": {
            "package": "internal/modelgateway",
            "mode": "forbid",
            "forbidden_direct_imports": {
                "internal/agentflow", "internal/advisory",
            },
        },
        "advisorycontract-stdlib-only": {
            "package": "internal/advisorycontract",
            "mode": "allowlist",
            "allowed_direct_imports": {"time"},
        },
    },
    "core-authority-transport-boundary": {
        "coreauthority-stdlib-only": {
            "package": "internal/coreauthority",
            "mode": "allowlist",
            "allowed_direct_imports": {
                "crypto/tls", "crypto/x509", "errors", "net", "net/http",
                "net/url", "os", "strings", "time",
            },
        },
    },
}


class Blocked(Exception):
    """The requested source/environment could not be checked reliably."""


def run_command(argv: list[str], cwd: Path, *, timeout: int = 60) -> str:
    try:
        result = subprocess.run(
            argv, cwd=cwd, text=True, encoding="utf-8", errors="replace",
            capture_output=True, timeout=timeout, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise Blocked(f"Command unavailable or timed out: {argv!r}: {exc}") from exc
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()[:4000]
        raise Blocked(f"Command failed ({result.returncode}): {argv!r}: {detail}")
    return result.stdout


def _canonical_package(value: object, label: str) -> str:
    if not isinstance(value, str) or not value or "\\" in value:
        raise Blocked(f"{label} must be a non-empty canonical Go package path")
    path = PurePosixPath(value)
    if not path.parts or path.is_absolute() or any(
        part in ("", ".", "..") for part in path.parts
    ):
        raise Blocked(f"{label} must stay inside the module")
    if str(path) != value or any(char.isspace() for char in value):
        raise Blocked(f"{label} must be a canonical Go package path")
    return value


def _string_list(value: object, label: str) -> list[str]:
    if not isinstance(value, list) or not value or not all(
        isinstance(item, str) and item and not any(char.isspace() for char in item)
        for item in value
    ):
        raise Blocked(f"{label} must be a non-empty string array")
    if len(set(value)) != len(value):
        raise Blocked(f"{label} contains duplicates")
    return value


def validate_config(config: object) -> dict:
    if not isinstance(config, dict) or set(config) != ({
        "$schema", "schema_version", "module", "policies"
    } | ({"ratchet"} if config.get("schema_version") == 3 else set())):
        raise Blocked("architecture.json must match the version 2 or 3 configuration shape")
    if config["$schema"] != "./architecture.schema.json" or config["schema_version"] not in (2, 3):
        raise Blocked("architecture.json must use architecture.schema.json and schema_version 2 or 3")
    module = config["module"]
    if (not isinstance(module, str) or module != "contractaudit" or any(char.isspace() for char in module)
            or module.startswith("/") or module.endswith("/") or "\\" in module
            or any(part in ("", ".", "..") for part in module.split("/"))):
        raise Blocked("Configuration module must remain the repository module contractaudit")
    policies = config["policies"]
    if not isinstance(policies, list) or not policies:
        raise Blocked("policies must be a non-empty array")

    policy_ids: set[str] = set()
    package_paths: set[str] = set()
    for policy in policies:
        if not isinstance(policy, dict) or set(policy) - {"id", "description", "checks"}:
            raise Blocked("Each policy must contain only id, description, and checks")
        policy_id = policy.get("id")
        if not isinstance(policy_id, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", policy_id):
            raise Blocked("Each policy id must be a canonical lowercase identifier")
        if policy_id in policy_ids:
            raise Blocked(f"Duplicate policy id: {policy_id}")
        policy_ids.add(policy_id)
        if "description" in policy and not isinstance(policy["description"], str):
            raise Blocked(f"{policy_id}: description must be a string")
        checks = policy.get("checks")
        if not isinstance(checks, list) or not checks:
            raise Blocked(f"{policy_id}: checks must be a non-empty array")
        check_ids: set[str] = set()
        for check in checks:
            if not isinstance(check, dict):
                raise Blocked(f"{policy_id}: each check must be an object")
            check_id = check.get("id")
            if not isinstance(check_id, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", check_id):
                raise Blocked(f"{policy_id}: check id must be a canonical lowercase identifier")
            if check_id in check_ids:
                raise Blocked(f"{policy_id}: duplicate check id {check_id}")
            check_ids.add(check_id)
            package = _canonical_package(check.get("package"), f"{policy_id}.{check_id}.package")
            if package in package_paths:
                raise Blocked(f"Package is configured more than once: {package}")
            package_paths.add(package)
            mode = check.get("mode")
            if mode == "allowlist":
                if set(check) != {"id", "package", "mode", "allowed_direct_imports"}:
                    raise Blocked(f"{policy_id}.{check_id}: allowlist check has invalid fields")
                _string_list(check["allowed_direct_imports"],
                             f"{policy_id}.{check_id}.allowed_direct_imports")
            elif mode == "forbid":
                if set(check) != {"id", "package", "mode", "forbidden_direct_imports"}:
                    raise Blocked(f"{policy_id}.{check_id}: forbid check has invalid fields")
                for forbidden in _string_list(
                    check["forbidden_direct_imports"],
                    f"{policy_id}.{check_id}.forbidden_direct_imports",
                ):
                    _canonical_package(forbidden, f"{policy_id}.{check_id}.forbidden_direct_imports")
            else:
                raise Blocked(f"{policy_id}.{check_id}: mode must be allowlist or forbid")
    if policy_ids != set(REQUIRED_POLICY_RULES):
        raise Blocked("Required named architecture policies are missing or unexpected")
    policies_by_id = {policy["id"]: policy for policy in policies}
    for policy_id, required_checks in REQUIRED_POLICY_RULES.items():
        checks = {check["id"]: check for check in policies_by_id[policy_id]["checks"]}
        if set(checks) != set(required_checks):
            raise Blocked(f"{policy_id}: required checks are missing or unexpected")
        for check_id, required in required_checks.items():
            check = checks[check_id]
            for field in ("package", "mode"):
                if check[field] != required[field]:
                    raise Blocked(f"{policy_id}.{check_id}: required {field} boundary changed")
            import_field = ("allowed_direct_imports" if check["mode"] == "allowlist"
                            else "forbidden_direct_imports")
            if set(check[import_field]) != required[import_field]:
                raise Blocked(f"{policy_id}.{check_id}: required import boundary changed")
    if config["schema_version"] == 3:
        validate_ratchet(config["ratchet"])
    return config


LEGACY_STORE_IMPORTERS = frozenset({
    "internal/apidocs/contracts", "internal/bootstrap", "internal/clmoutbox",
    "internal/documentclient", "internal/documentservice", "internal/experienceclient",
    "internal/httpapi", "internal/modelgateway", "internal/outboxrelay", "internal/toolgateway",
})


def validate_ratchet(ratchet: object) -> None:
    if not isinstance(ratchet, dict) or set(ratchet) != {"legacy_store_importers", "domains"}:
        raise Blocked("invalid ratchet configuration")
    legacy = ratchet["legacy_store_importers"]
    if (not isinstance(legacy, list) or any(not isinstance(path, str) for path in legacy)
            or len(legacy) != len(set(legacy)) or not set(legacy) <= LEGACY_STORE_IMPORTERS):
        raise Blocked("legacy store importers must be a unique subset of the reviewed list")
    domains = ratchet["domains"]
    if not isinstance(domains, list):
        raise Blocked("domains must be an array")
    ids, paths = set(), set()
    for domain in domains:
        if not isinstance(domain, dict) or set(domain) != {"id", "app", "transport", "postgres"}:
            raise Blocked("invalid domain registration")
        ident = domain["id"]
        if not isinstance(ident, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", ident) or ident in ids:
            raise Blocked("invalid or duplicate domain id")
        ids.add(ident)
        for role in ("app", "transport", "postgres"):
            values = domain[role]
            if not isinstance(values, list):
                raise Blocked("domain roles must be arrays")
            for value in values:
                path = _canonical_package(value, "domain package")
                if (not path.startswith("internal/") or any(
                        path == prior or path.startswith(prior + "/") or prior.startswith(path + "/")
                        for prior in paths)):
                    raise Blocked("domain role paths must be disjoint internal prefixes")
                paths.add(path)


def validate_schema_contract(schema: object) -> dict:
    """Accept only the canonical supported schema, not arbitrary JSON Schema."""
    if (not isinstance(schema, dict) or
            schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema" or
            schema.get("x-caw-config-version") != 3):
        raise Blocked("architecture.schema.json is missing the version 3 schema contract")
    canonical = json.dumps(schema, ensure_ascii=False, sort_keys=True,
                           separators=(",", ":")).encode("utf-8")
    if hashlib.sha256(canonical).hexdigest() != SUPPORTED_SCHEMA_CANONICAL_SHA256:
        raise Blocked("architecture.schema.json differs from the fixed structure supported by this checker")
    properties = schema.get("properties")
    definitions = schema.get("$defs")
    if not isinstance(properties, dict) or not isinstance(definitions, dict):
        raise Blocked("architecture.schema.json is incomplete")
    if (properties.get("$schema", {}).get("const") != "./architecture.schema.json" or
            properties.get("schema_version", {}).get("enum") != [2, 3] or
            properties.get("module", {}).get("type") != "string" or
            properties.get("policies", {}).get("type") != "array" or
            properties.get("policies", {}).get("items", {}).get("$ref") != "#/$defs/policy"):
        raise Blocked("architecture.schema.json does not describe the active config fields")
    policy = definitions.get("policy", {})
    check = definitions.get("check", {})
    one_of = check.get("oneOf", [])
    check_refs = {entry.get("$ref") for entry in one_of if isinstance(entry, dict)}
    if (not {"id", "checks"}.issubset(policy.get("required", [])) or
            policy.get("properties", {}).get("checks", {}).get("items", {}).get("$ref") != "#/$defs/check" or
            len(one_of) != 2 or check_refs != {
                "#/$defs/allowlistCheck", "#/$defs/forbidCheck"
            }):
        raise Blocked("architecture.schema.json does not describe named policy checks")
    allowlist = definitions.get("allowlistCheck", {})
    forbid = definitions.get("forbidCheck", {})
    if (allowlist.get("properties", {}).get("mode", {}).get("const") != "allowlist" or
            "allowed_direct_imports" not in allowlist.get("required", []) or
            forbid.get("properties", {}).get("mode", {}).get("const") != "forbid" or
            "forbidden_direct_imports" not in forbid.get("required", [])):
        raise Blocked("architecture.schema.json omits an active direct-import rule field")
    return schema


def load_config() -> tuple[dict, str, str]:
    try:
        raw = CONFIG_PATH.read_bytes()
        schema_raw = SCHEMA_PATH.read_bytes()
        config = validate_config(json.loads(raw))
        if config["schema_version"] != 3:
            raise Blocked("active configuration must use schema_version=3")
        schema = validate_schema_contract(json.loads(schema_raw))
    except (OSError, json.JSONDecodeError) as exc:
        raise Blocked(f"Cannot read architecture config/schema: {exc}") from exc
    return config, hashlib.sha256(raw).hexdigest(), hashlib.sha256(schema_raw).hexdigest()


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


def inspect_package(records: list[dict], module: str, check: dict) -> dict:
    expected = module + "/" + check["package"]
    matches = [record for record in records if record["ImportPath"] == expected]
    if len(matches) != 1:
        raise Blocked(f"Expected exactly one target package {expected}; found {len(matches)}")
    package = matches[0]
    go_files = package.get("GoFiles")
    if not isinstance(go_files, list) or not go_files or not all(
        isinstance(name, str) and name.endswith(".go") for name in go_files
    ):
        raise Blocked(f"{check['id']}: target package has missing, invalid, or zero GoFiles")
    imports = package.get("Imports")
    if not isinstance(imports, list) or not all(
        isinstance(item, str) and item for item in imports
    ):
        raise Blocked(f"{check['id']}: target production Imports must be a string array")
    actual = sorted(set(imports))
    if check["mode"] == "allowlist":
        allowed = check["allowed_direct_imports"]
        forbidden = sorted(set(actual) - set(allowed))
        rule_values = {"allowed_direct_imports": allowed}
    else:
        denied = [module + "/" + item for item in check["forbidden_direct_imports"]]
        forbidden = sorted({
            imported for imported in actual
            if any(imported == path or imported.startswith(path + "/")
                   for path in denied)
        })
        rule_values = {"forbidden_direct_imports": denied}
    return {
        "id": check["id"],
        "package": expected,
        "mode": check["mode"],
        "go_files": sorted(go_files),
        "direct_production_imports": actual,
        **rule_values,
        "forbidden_imports": forbidden,
        "excluded_go_list_fields": ["TestImports", "XTestImports", "Deps"],
    }


def inspect_policies(records: list[dict], config: dict, report: dict) -> None:
    for policy in config["policies"]:
        policy_report = {
            "id": policy["id"],
            "description": policy.get("description", ""),
            "status": "BLOCKED",
            "checks": [],
        }
        report["policy_results"].append(policy_report)
        for check in policy["checks"]:
            result = inspect_package(records, config["module"], check)
            policy_report["checks"].append(result)
            report["violations"].extend(
                {"policy_id": policy["id"], "check_id": check["id"],
                 "rule": check["mode"], "from": result["package"], "to": target}
                for target in result["forbidden_imports"]
            )
        policy_report["status"] = (
            "FAIL" if any(item["forbidden_imports"] for item in policy_report["checks"])
            else "PASS"
        )
    if config["schema_version"] == 3:
        inspect_ratchet(records, config, report)
    first = report["policy_results"][0]["checks"][0]
    # Keep the v1 inspection fields at top-level for existing evidence readers.
    report["inspection"] = {
        key: first[key] for key in (
            "package", "go_files", "direct_production_imports",
            "forbidden_imports", "excluded_go_list_fields",
        )
    }


def inspect_ratchet(records: list[dict], config: dict, report: dict) -> None:
    module = config["module"] + "/"
    legacy = set(config["ratchet"]["legacy_store_importers"])
    domains = config["ratchet"]["domains"]
    owners = {module + path: (domain["id"], role) for domain in domains
              for role in ("app", "transport", "postgres") for path in domain[role]}
    def owner_for(path: str):
        matches = [(root, value) for root, value in owners.items()
                   if path == root or path.startswith(root + "/")]
        return max(matches, key=lambda item: len(item[0]))[1] if matches else None

    seen = set()
    for record in records:
        name = record.get("ImportPath")
        if not isinstance(name, str) or name in seen:
            raise Blocked("duplicate or invalid GoList package")
        seen.add(name)
        if not name.startswith(module + "internal/"):
            continue
        imports = record.get("Imports", [])
        if not isinstance(imports, list) or any(not isinstance(item, str) or not item for item in imports):
            raise Blocked(f"{name}: invalid production imports")
        for target in imports:
            rule = None
            if target == module + "internal/store" or target.startswith(module + "internal/store/"):
                if name[len(module):] not in legacy:
                    rule = "store-no-new-business-import"
            owner = owner_for(name)
            target_owner = owner_for(target)
            if owner and target_owner and owner[0] != target_owner[0] and target_owner[1] == "postgres":
                rule = "cross-domain-postgres"
            if owner and owner[1] == "transport":
                if target in {"database/sql", "github.com/jackc/pgx/v5"} or target.startswith("github.com/jackc/pgx/v5/"):
                    rule = "transport-no-sql"
                elif target_owner and (target_owner[0] != owner[0] or target_owner[1] != "app"):
                    rule = "transport-to-app"
            if rule:
                report["violations"].append({"policy_id": "domain-ratchet", "check_id": rule,
                                             "rule": rule, "from": name, "to": target})
    missing = set(owners) - seen
    if missing:
        raise Blocked(f"registered domain packages missing from GoList: {sorted(missing)}")
    report["ratchet"] = {"status": "FAIL" if any(v["policy_id"] == "domain-ratchet" for v in report["violations"]) else "PASS",
                         "store_guard": "active for all internal packages except exact legacy importers",
                         "registered_domain_count": len(domains)}


def inspect_go_environment(repo: Path) -> dict:
    raw = run_command(
        ["go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "CGO_ENABLED",
         "GOFLAGS", "GOENV", "GOWORK"], repo,
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


def _configured_checks(config: dict) -> list[dict]:
    return [check for policy in config["policies"] for check in policy["checks"]]


def validate_base_ratchet(base: dict, candidate: dict) -> None:
    validate_config(base)
    if base["schema_version"] == 2:
        return
    if not set(candidate["ratchet"]["legacy_store_importers"]) <= set(base["ratchet"]["legacy_store_importers"]):
        raise Blocked("BASE legacy store exemptions cannot be reintroduced")
    current = {d["id"]: d for d in candidate["ratchet"]["domains"]}
    for domain in base["ratchet"]["domains"]:
        newer = current.get(domain["id"])
        if newer is None or any(not set(domain[role]) <= set(newer[role])
                                for role in ("app", "transport", "postgres")):
            raise Blocked(f"BASE domain registrations removed or reassigned: {domain['id']}")


def load_base_ratchet(repo: Path, base_ref: str, candidate: dict) -> tuple[str, str]:
    sha = run_command(["git", "rev-parse", "--verify", base_ref + "^{commit}"], repo).strip()
    if not re.fullmatch(r"[0-9a-f]{40,64}", sha):
        raise Blocked("BASE did not resolve to a full commit SHA")
    merge_base = run_command(["git", "merge-base", sha, "HEAD"], repo).strip()
    if not re.fullmatch(r"[0-9a-f]{40,64}", merge_base):
        raise Blocked("BASE and HEAD do not have a verifiable merge-base")
    try:
        base = json.loads(run_command(["git", "show", sha + ":scripts/architecture/architecture.json"], repo))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise Blocked(f"BASE architecture config is invalid: {exc}") from exc
    validate_base_ratchet(base, candidate)
    return sha, merge_base


def inspect_repo(repo_arg: str, config: dict, report: dict, base_ref: str) -> None:
    repo = Path(repo_arg).expanduser().absolute()
    if not repo.is_dir():
        raise Blocked("Repository path is not a directory")
    report["source"]["repository_path"] = str(repo)
    base_sha, merge_base = load_base_ratchet(repo, base_ref, config)
    report["source"]["base_sha"] = base_sha
    report["source"]["merge_base_sha"] = merge_base
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
    packages = [check["package"] for check in _configured_checks(config)]
    argv = ["go", "list", "-json", *( ["./..."] if config["schema_version"] == 3 else ["./" + path for path in packages])]
    report["environment"]["go_list_command"] = argv
    raw = run_command(argv, repo)
    inspect_policies(decode_go_list(raw), config, report)


def inspect_fixture(path: str, config: dict, report: dict) -> None:
    fixture = Path(path).expanduser().absolute()
    try:
        raw = fixture.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise Blocked(f"Cannot read GoList fixture: {exc}") from exc
    report["source"]["fixture_path"] = str(fixture)
    report["source"]["input_sha256"] = hashlib.sha256(raw.encode("utf-8")).hexdigest()
    report["source"]["fixture_record_count"] = len(decode_go_list(raw))
    inspect_policies(decode_go_list(raw), config, report)


def write_report(report: dict, destination: str | None) -> int:
    if report["errors"]:
        report["status"] = "BLOCKED"
    elif report["violations"]:
        report["status"] = "FAIL"
    elif report["status"] != "BLOCKED":
        report["status"] = "PASS"
    report["exit_code"] = {"PASS": 0, "FAIL": 1, "BLOCKED": 2}[report["status"]]
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
    source.add_argument("--repo", help="Local Git checkout; runs go list for all configured packages")
    source.add_argument("--fixture", help="Synthetic go list JSON; never runs Git or Go")
    parser.add_argument("--base", default=None, help="Trusted target BASE commit/ref for the ratchet")
    parser.add_argument("--out", help="Write JSON report here; otherwise print to stdout")
    args = parser.parse_args()
    fixture_mode = args.fixture is not None
    report = {
        "schema_version": 2,
        "check": "named-direct-import-policies",
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
            "target": "all internal packages for store ratchet and registered domain packages",
            "imports": "direct production Imports only",
            "test_imports_included": False,
            "transitive_dependencies_included": False,
            "modelgateway_store_indirect_path_policy":
                "direct store import is allowed; its transitive agentflow edge is not traversed",
            "build_context": "fixture only" if fixture_mode else "current GOOS/GOARCH/CGO_ENABLED/GOFLAGS only",
            "all_build_tags_covered": False,
            "whole_repository_pass_claim": False,
        },
        "configuration": None,
        "policy_results": [],
        "inspection": None,
        "errors": [],
        "violations": [],
    }
    try:
        config, config_sha, schema_sha = load_config()
        report["configuration"] = {
            "path": str(CONFIG_PATH),
            "sha256": config_sha,
            "schema_path": str(SCHEMA_PATH),
            "schema_sha256": schema_sha,
            "schema_version": config["schema_version"],
            "module": config["module"],
            "policies": config["policies"],
        }
        report["scope"]["policy_ids"] = [policy["id"] for policy in config["policies"]]
        report["scope"]["target_packages"] = [
            config["module"] + "/" + check["package"]
            for check in _configured_checks(config)
        ]
        if fixture_mode:
            inspect_fixture(args.fixture, config, report)
        else:
            base_ref = args.base or os.environ.get("CAW_FAST_BASE") or os.environ.get("BASE_SHA") or "origin/main"
            inspect_repo(args.repo, config, report, base_ref)
        if report["violations"]:
            report["status"] = "FAIL"
        else:
            report["status"] = "PASS"
    except (Blocked, OSError, ValueError, TypeError, AttributeError) as exc:
        report["errors"].append(str(exc))
        report["status"] = "BLOCKED"
    return write_report(report, args.out)


if __name__ == "__main__":
    raise SystemExit(main())

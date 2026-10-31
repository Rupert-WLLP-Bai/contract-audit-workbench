#!/usr/bin/env python3
"""Scoped fast checks for pull requests.

The diff spans every commit since the merge-base of ``--base`` (or
``CAW_FAST_BASE``, default ``origin/main``) plus staged, unstaged, and untracked
worktree files. Deletions, renames, and paths containing spaces are preserved.

``--plan`` prints one JSON line with the booleans ``go``, ``web``, ``python``,
and ``database`` (plus ``poppler`` and ``legal_assets``). ``--github-output``
appends those names as ``true`` or ``false`` so a GitHub Actions job can
conditionally install tools or databases.

All logs and the JSON report are written outside the repository.
"""
from __future__ import annotations

import argparse
import ast
from dataclasses import dataclass, field
from datetime import datetime, timezone
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import shutil
import signal
import subprocess
import sys
import time
import tokenize
from typing import Callable, Iterable
from go_test_scope import discover_unit_test_packages, unit_test_environment

REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_BASE = "origin/main"
PLAN_KEYS = ("go", "web", "python", "database")
GITHUB_KEYS = PLAN_KEYS + ("poppler", "legal_assets")
POPPLER_TEST_PACKAGES = frozenset({"./internal/documentparse"})
LEGAL_ASSET_TEST_PACKAGES = frozenset({"./internal/httpapi", "./internal/legalnorm"})
SKIP_PARTS = frozenset({".git", ".mypy_cache", ".pytest_cache", ".ruff_cache",
                        ".venv", "__pycache__", "artifacts", "coverage", "dist",
                        "node_modules", "vendor", "venv"})
GO_SKIP_PARTS = frozenset({".git", ".venv", "artifacts", "dist", "node_modules",
                           "vendor", "web"})
DB_ENV_KEYS = ("TEST_DATABASE_URL", "MIGRATOR_DATABASE_URL", "MODEL_DATABASE_URL")

WEB_BUILD_FILES = frozenset({
    "admin.html", "bun.lock", "bun.lockb", "bunfig.toml", "index.html",
    "package.json", "playwright.functional.config.ts", "playwright.oidc.config.ts",
    "portal.html", "tsconfig.json", "tsconfig.test.json", "vite.config.ts",
    "vitest.config.ts",
})
WEB_SOURCE_SUFFIXES = frozenset({".cjs", ".css", ".html", ".js", ".json", ".jsx",
                                 ".mjs", ".scss", ".toml", ".ts", ".tsx"})

AUTH_TESTS = ("TestGovernanceServiceRoleBoundaries",
              "TestLoginOriginalAndRetryStayInPostgres")
SESSION_TESTS = ("TestConcurrentLogoutCannotBeResurrectedByCaseWrite",
                 "TestSessionNarrowWritesKeepAuthorityAndRevocation")
TENANT_TESTS = ("TestCrossOrganizationGenerationPreservesCaseIdentity",
                "TestGovernanceCrossTenantIsolation",
                "TestMaterialTablesEnforceTenantRLSAndAppendOnlyServiceOwnership")
MIGRATION_TESTS = ("TestEvaluationProvenanceMigrationPreservesHistoricalRows",)
BUDGET_TESTS = ("TestModelBudgetRoleIsolationAndFunctionPrivileges",)
LEASE_TESTS = ("TestStaleGenerationCompletionIsRejected",)
DATABASE_CORE_TESTS = ("TestLoginOriginalAndRetryStayInPostgres",)
FROZEN_EVIDENCE_TESTS = (
    "TestDurableReviewFreezesServiceObjectContextAndRejectsChangedRetryAfterReopen",
)


class FastError(RuntimeError):
    """A reproducible failure before any check can run."""


@dataclass(frozen=True)
class Change:
    status: str
    path: str
    old_path: str | None = None

    @property
    def deleted(self) -> bool:
        return self.status == "D"

    def affected_paths(self) -> tuple[str, ...]:
        paths = [self.path]
        if self.old_path and self.old_path != self.path:
            paths.append(self.old_path)
        return tuple(paths)


@dataclass(frozen=True)
class SensitiveRule:
    reason: str
    matches: Callable[[str], bool]
    tests: tuple[str, ...]
    needs_model: bool = False


@dataclass(frozen=True)
class DatabaseTarget:
    package: str
    tests: tuple[str, ...]
    reasons: tuple[str, ...]
    needs_model: bool

    def payload(self) -> dict:
        return {"package": self.package, "tests": list(self.tests),
                "reasons": list(self.reasons), "needs_model": self.needs_model}


@dataclass
class Plan:
    changes: tuple[Change, ...]
    go: bool = False
    web: bool = False
    python: bool = False
    database: bool = False
    poppler: bool = False
    legal_assets: bool = False
    go_build_all: bool = False
    web_build: bool = False
    go_format_files: list[str] = field(default_factory=list)
    go_packages: set[str] = field(default_factory=set)
    go_unit_packages: set[str] = field(default_factory=set)
    python_syntax_files: list[str] = field(default_factory=list)
    python_tests: list[str] = field(default_factory=list)
    database_tests: tuple[DatabaseTarget, ...] = ()
    reasons: set[str] = field(default_factory=set)

    def payload(self) -> dict:
        return {
            "go": self.go,
            "web": self.web,
            "python": self.python,
            "database": self.database,
            "poppler": self.poppler,
            "legal_assets": self.legal_assets,
            "go_compile": "all" if self.go_build_all else "affected",
            "go_build_all": self.go_build_all,
            "go_format_files": list(self.go_format_files),
            "go_packages": sorted(self.go_packages),
            "go_unit_packages": sorted(self.go_unit_packages),
            "web_build": self.web_build,
            "python_syntax_files": list(self.python_syntax_files),
            "python_tests": list(self.python_tests),
            "database_tests": [target.payload() for target in self.database_tests],
            "changed_paths": sorted({path for change in self.changes
                                     for path in change.affected_paths()}),
            "reasons": sorted(self.reasons),
        }


@dataclass(frozen=True)
class FastCheck:
    name: str
    command: tuple[str, ...] = ()
    cwd: Path = REPO_ROOT
    env: dict[str, str] | None = None
    timeout: int = 600
    kind: str = "command"  # command, gofmt, json, unittest, python-syntax
    files: tuple[str, ...] = ()
    expected_tests: tuple[str, ...] = ()
    required: bool = True


@dataclass(frozen=True)
class CheckResult:
    name: str
    status: str
    seconds: float
    command: str
    log: str
    reason: str = ""
    evidence: dict | None = None

    def payload(self) -> dict:
        result = {"name": self.name, "status": self.status,
                  "seconds": self.seconds, "command": self.command,
                  "log": self.log}
        if self.reason:
            result["reason"] = self.reason
        if self.evidence is not None:
            result["evidence"] = self.evidence
        return result


def _stem(path: str) -> str:
    return PurePosixPath(path).stem


def _normalize_stem(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "_", value.lower()).strip("_")


def _is_auth(path: str) -> bool:
    if path.startswith(("internal/identity/", "internal/access/",
                        "internal/accounts/")):
        return True
    if path.startswith("internal/store/") and "authorization" in _stem(path):
        return True
    return (path.startswith("internal/httpapi/") and
            any(token in _stem(path) for token in ("auth", "login", "oidc", "session")))


def _is_session(path: str) -> bool:
    if path.startswith("internal/identity/"):
        return True
    return "session" in _stem(path) and path.startswith(("internal/store/",
                                                         "internal/httpapi/"))


def _is_tenant(path: str) -> bool:
    if path.startswith("internal/access/"):
        return True
    if not path.startswith("internal/store/"):
        return False
    return any(token in _stem(path) for token in
               ("scope", "scoped", "tenant", "governance", "authorization",
                "legal_applicability"))


def _is_migration(path: str) -> bool:
    if path.startswith("internal/store/") and "schema" in _stem(path):
        return True
    if not path.startswith(("cmd/migrate/", "deploy/", "deployments/",
                            "internal/", "scripts/")):
        return False
    return "migrat" in path.lower()


def _is_budget(path: str) -> bool:
    return (path.startswith(("internal/execution/", "internal/modelgateway/",
                             "internal/store/")) and "budget" in _stem(path))


def _is_lease(path: str) -> bool:
    if path.startswith("internal/execution/"):
        return "budget" not in _stem(path)
    return (path.startswith("internal/store/") and
            any(token in _stem(path) for token in
                ("stale_claim", "execution", "attempt_finalize", "queue")))


def _is_database_core(path: str) -> bool:
    return path in ("internal/store/pg.go", "internal/store/service_roles.go",
                    "internal/config/select.go")


def _is_frozen_evidence(path: str) -> bool:
    return path.startswith("internal/store/") and any(
        token in _stem(path) for token in
        ("frozen", "snapshot", "service_object_context", "attempt_input", "material"))


SENSITIVE_RULES = (
    SensitiveRule("auth", _is_auth, AUTH_TESTS),
    SensitiveRule("session", _is_session, SESSION_TESTS),
    SensitiveRule("tenant", _is_tenant, TENANT_TESTS),
    SensitiveRule("migration", _is_migration, MIGRATION_TESTS),
    SensitiveRule("budget", _is_budget, BUDGET_TESTS, needs_model=True),
    SensitiveRule("lease", _is_lease, LEASE_TESTS),
    SensitiveRule("database-core", _is_database_core, DATABASE_CORE_TESTS),
    SensitiveRule("frozen-evidence", _is_frozen_evidence, FROZEN_EVIDENCE_TESTS),
)


def parse_name_status_z(payload: bytes) -> tuple[Change, ...]:
    """Parse ``git diff --name-status -z --find-renames`` output."""
    tokens = [os.fsdecode(token) for token in payload.split(b"\0")]
    if tokens and tokens[-1] == "":
        tokens.pop()
    changes: list[Change] = []
    index = 0
    while index < len(tokens):
        status = tokens[index]
        index += 1
        if not status:
            raise FastError("git diff produced an empty status record")
        if status[0] in ("R", "C"):
            if index + 1 >= len(tokens):
                raise FastError("git diff produced an incomplete rename record")
            old_path, path = tokens[index], tokens[index + 1]
            index += 2
            changes.append(Change(status[0], path, old_path))
        else:
            if index >= len(tokens):
                raise FastError("git diff produced an incomplete path record")
            changes.append(Change(status[0], tokens[index]))
            index += 1
    return tuple(changes)


def _git(root: Path, *arguments: str, allow_failure: bool = False) -> bytes:
    try:
        result = subprocess.run(["git", "-C", str(root), *arguments],
                                capture_output=True)
    except OSError as error:
        raise FastError(f"cannot run git: {error}") from error
    if result.returncode != 0 and not allow_failure:
        detail = result.stderr.decode("utf-8", "replace").strip()
        raise FastError(f"git {' '.join(arguments)} failed: {detail}")
    return result.stdout if result.returncode == 0 else b""


def collect_changes(root: Path, base: str) -> tuple[tuple[Change, ...], str]:
    """Return changes since the merge-base and the merge-base commit."""
    base_sha = _git(root, "rev-parse", "--verify", "--quiet",
                    f"{base}^{{commit}}", allow_failure=True)
    if not base_sha.strip():
        raise FastError(f"base ref {base!r} is not a known commit; fetch it or pass --base")
    merge_base = _git(root, "merge-base", base_sha.decode().strip(), "HEAD",
                      allow_failure=True).decode().strip()
    if not merge_base:
        raise FastError(f"base ref {base!r} has no merge-base with HEAD")
    diff = _git(root, "diff", "--name-status", "-z", "--find-renames", "-M",
                merge_base, "--")
    changes = list(parse_name_status_z(diff))
    untracked = _git(root, "ls-files", "-z", "--others", "--exclude-standard", "--")
    for raw in untracked.split(b"\0"):
        if not raw:
            continue
        changes.append(Change("A", os.fsdecode(raw)))
    return tuple(changes), merge_base


def _package_selector(package_dir: str) -> str:
    return "./..." if package_dir in ("", ".") else "./" + package_dir.strip("/")


def _go_package_dir(path: str) -> str:
    return str(PurePosixPath(path).parent)


def _live_go_package(root: Path, package_dir: str) -> bool:
    directory = root / package_dir
    return directory.is_dir() and any(
        not path.name.endswith("_test.go") for path in directory.glob("*.go"))


def _module_path(root: Path) -> str:
    try:
        text = (root / "go.mod").read_text(encoding="utf-8")
    except OSError:
        return ""
    match = re.search(r"(?m)^module\s+(\S+)", text)
    return match.group(1) if match else ""


def _go_imports(text: str) -> Iterable[str]:
    for block in re.finditer(r"(?ms)^\s*import\s*\((.*?)^\s*\)", text):
        for match in re.finditer(r'"([^"]+)"', block.group(1)):
            yield match.group(1)
    for match in re.finditer(r'(?m)^\s*import\s+(?:[\w.]+\s+)?"([^"]+)"', text):
        yield match.group(1)


def _go_importer_counts(root: Path, module: str) -> dict[str, set[str]]:
    """Map module-internal import path to non-test packages that import it."""
    counts: dict[str, set[str]] = {}
    if not module:
        return counts
    for path in _walk_files(root, ".go", GO_SKIP_PARTS):
        relative = path.relative_to(root)
        if path.name.endswith("_test.go"):
            continue
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        package_dir = str(relative.parent)
        for imported in _go_imports(text):
            if imported == module or imported.startswith(module + "/"):
                counts.setdefault(imported, set()).add(package_dir)
    return counts


def _web_relevant(path: str) -> bool:
    if not path.startswith("web/"):
        return False
    relative = path[len("web/"):]
    if not relative or relative.startswith(("dist/", "node_modules/", "public/")):
        return False
    if relative.startswith(("src/", "e2e/")):
        return True
    return PurePosixPath(relative).name in WEB_BUILD_FILES or \
        PurePosixPath(relative).suffix in WEB_SOURCE_SUFFIXES


def _web_build_config(path: str) -> bool:
    if not path.startswith("web/"):
        return False
    relative = path[len("web/"):]
    name = PurePosixPath(relative).name
    return name in WEB_BUILD_FILES or name.startswith(("tsconfig", "vite.config",
                                                       "vitest.config"))


def _python_module(path: str) -> str:
    return path[:-3].replace("/", ".")


def _walk_files(root: Path, suffix: str, skip_dirs: frozenset[str]):
    for current, directories, files in os.walk(root):
        directories[:] = sorted(name for name in directories
                                 if name not in skip_dirs)
        for name in files:
            if name.endswith(suffix):
                yield Path(current) / name


def _test_file_candidates(root: Path) -> list[tuple[str, ast.AST | None]]:
    candidates: list[tuple[str, ast.AST | None]] = []
    for path in _walk_files(root, ".py", SKIP_PARTS):
        if not path.name.startswith("test_"):
            continue
        relative = path.relative_to(root)
        try:
            tree = ast.parse(path.read_text(encoding="utf-8", errors="replace"))
        except (OSError, SyntaxError, ValueError):
            tree = None
        candidates.append((str(relative), tree))
    return candidates


def _test_references(tree: ast.AST, module: str, stem: str, filename: str) -> bool:
    def imported(name: str) -> bool:
        return (name == module or name == stem or module.endswith("." + name) or
                name.endswith("." + module))

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            if any(imported(alias.name) for alias in node.names):
                return True
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0 and node.module and imported(node.module):
                return True
        elif isinstance(node, ast.Constant) and isinstance(node.value, str):
            value = node.value
            if value == filename or value.endswith("/" + filename):
                return True
            if value == stem or value.endswith("/" + stem):
                return True
    return False


def python_tests_for(root: Path, changed_paths: Iterable[str]) -> list[str]:
    """Select only tests that name or import a changed Python module."""
    candidates = _test_file_candidates(root)
    selected: set[str] = set()
    for changed in changed_paths:
        if not changed.endswith(".py"):
            continue
        filename = PurePosixPath(changed).name
        stem = _stem(changed)
        normalized = _normalize_stem(stem)
        module = _python_module(changed)
        for test_path, tree in candidates:
            test_stem = _normalize_stem(_stem(test_path))
            if test_stem == "test_" + normalized:
                selected.add(test_path)
                continue
            if changed == test_path and PurePosixPath(changed).name.startswith("test_"):
                selected.add(test_path)
                continue
            if tree is not None and _test_references(tree, module, stem, filename):
                selected.add(test_path)
    return sorted(selected)


def _database_targets(changes: Iterable[Change]) -> tuple[DatabaseTarget, ...]:
    reasons: set[str] = set()
    tests: set[str] = set()
    needs_model = False
    for change in changes:
        for path in change.affected_paths():
            for rule in SENSITIVE_RULES:
                if rule.matches(path):
                    reasons.add(rule.reason)
                    tests.update(rule.tests)
                    needs_model = needs_model or rule.needs_model
    if not reasons:
        return ()
    return (DatabaseTarget("./internal/store", tuple(sorted(tests)),
                           tuple(sorted(reasons)), needs_model),)


def build_plan(root: Path, changes: Iterable[Change]) -> Plan:
    changes = tuple(changes)
    plan = Plan(changes)
    go_source_packages: set[str] = set()

    for change in changes:
        for path in change.affected_paths():
            if path.endswith(".go"):
                plan.go = True
                package_dir = _go_package_dir(path)
                if package_dir in ("", "."):
                    plan.go_build_all = True
                else:
                    plan.go_packages.add(_package_selector(package_dir))
                if change.deleted or path == change.old_path:
                    if not _live_go_package(root, package_dir):
                        plan.go_build_all = True
                elif (not path.endswith("_test.go") and
                      package_dir not in ("", ".")):
                    go_source_packages.add(_package_selector(package_dir))
                if path == change.path and not change.deleted and path.endswith(".go"):
                    plan.go_format_files.append(path)
            elif path in ("go.mod", "go.sum"):
                plan.go = True
                plan.go_build_all = True
            if path.startswith("web/") and _web_relevant(path):
                plan.web = True
                plan.web_build = plan.web_build or _web_build_config(path)
            if path.endswith(".py"):
                plan.python = True
                if path == change.path and not change.deleted:
                    plan.python_syntax_files.append(path)

    plan.go_format_files = sorted(set(plan.go_format_files))
    plan.python_syntax_files = sorted(set(plan.python_syntax_files))
    if plan.go_packages:
        module = _module_path(root)
        importer_counts = _go_importer_counts(root, module) if module else {}
        for package in go_source_packages:
            package_dir = package.removeprefix("./")
            import_path = module if package_dir == "." else f"{module}/{package_dir}"
            importers = importer_counts.get(import_path, set()) - {package_dir}
            if len(importers) >= 2 or any(importer == "cmd" or
                                           importer.startswith("cmd/")
                                           for importer in importers):
                plan.go_build_all = True
                plan.reasons.add("shared-package")
    plan.go_unit_packages = plan.go_packages & discover_unit_test_packages(root)
    plan.poppler = bool(plan.go_unit_packages & POPPLER_TEST_PACKAGES)
    plan.legal_assets = bool(plan.go_unit_packages & LEGAL_ASSET_TEST_PACKAGES)
    changed_python = sorted({path for change in changes
                             for path in change.affected_paths() if path.endswith(".py")})
    plan.python_tests = python_tests_for(root, changed_python)
    plan.database_tests = _database_targets(changes)
    plan.database = bool(plan.database_tests)
    if plan.database:
        # Database fixed-point checks execute through `go test`, so a migration
        # or schema script change still needs the Go toolchain and modules.
        plan.go = True
        for target in plan.database_tests:
            plan.reasons.update(target.reasons)
    return plan


def _required_database_env(plan: Plan) -> list[str]:
    missing = [name for name in ("TEST_DATABASE_URL", "MIGRATOR_DATABASE_URL")
               if not os.environ.get(name, "").strip()]
    if any(target.needs_model for target in plan.database_tests):
        if not os.environ.get("MODEL_DATABASE_URL", "").strip():
            missing.append("MODEL_DATABASE_URL")
    return missing


def _resolve_binary(name: str) -> str:
    # ``GO`` and ``BUN`` are the conventional build variables.
    value = os.environ.get("GO" if name == "go" else "BUN", name)
    found = shutil.which(value)
    if not found:
        raise FastError(f"{name} is required for this plan but was not found on PATH")
    return found


def _gofmt_binary(go: str) -> str:
    candidate = Path(go).parent / "gofmt"
    if candidate.is_file():
        return str(candidate)
    found = shutil.which("gofmt")
    if not found:
        raise FastError("gofmt is required for Go changes but was not found")
    return found


def _base_env() -> dict[str, str]:
    env = dict(os.environ)
    env["APP_ENV"] = "test"
    env["TYPESAFE_JEV_MODE"] = "off"
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    env.pop("TYPESAFE_API_KEY", None)
    return env


def _check_env(database: bool = False) -> dict[str, str]:
    env = _base_env()
    if not database:
        for name in DB_ENV_KEYS:
            env.pop(name, None)
    return env


def _build_checks(plan: Plan, root: Path, python: str) -> list[FastCheck]:
    checks: list[FastCheck] = []
    env = _check_env()
    if plan.go:
        go = _resolve_binary("go")
        if plan.go_format_files:
            checks.append(FastCheck(
                "go-format", (_gofmt_binary(go), "-l", *plan.go_format_files),
                cwd=root, env=env, timeout=120, kind="gofmt"))
        packages = sorted(plan.go_packages)
        if plan.go_build_all:
            checks.append(FastCheck(
                "go-compile-all",
                (go, "test", "-run", "^$", "-count=1", "-timeout=300s", "./..."),
                cwd=root, env=env, timeout=360))
        elif packages:
            checks.append(FastCheck(
                "go-compile",
                (go, "test", "-run", "^$", "-count=1", "-timeout=300s", *packages),
                cwd=root, env=env, timeout=360))
        if plan.go_unit_packages:
            checks.append(FastCheck(
                "go-unit",
                (go, "test", "-count=1", "-json", "-timeout=600s",
                 *sorted(plan.go_unit_packages)),
                cwd=root, env=unit_test_environment(env), timeout=660, kind="json", required=False))
    if plan.python:
        if plan.python_syntax_files:
            checks.append(FastCheck("python-syntax", cwd=root, env=env,
                                    kind="python-syntax",
                                    files=tuple(plan.python_syntax_files)))
        for test_path in plan.python_tests:
            test_file = PurePosixPath(test_path)
            checks.append(FastCheck(
                f"python-test:{test_path}",
                (python, "-m", "unittest", "discover", "-s", str(test_file.parent),
                 "-p", test_file.name, "-v"),
                cwd=root, env=env, timeout=300, kind="unittest"))
    if plan.web:
        bun = _resolve_binary("bun")
        checks.append(FastCheck("web-typecheck", (bun, "run", "typecheck"),
                                cwd=root / "web", env=env, timeout=300))
        if plan.web_build:
            checks.append(FastCheck("web-build", (bun, "run", "build"),
                                    cwd=root / "web", env=env, timeout=900))
    for target in plan.database_tests:
        go = _resolve_binary("go")
        pattern = "^(" + "|".join(re.escape(name) for name in target.tests) + ")$"
        checks.append(FastCheck(
            "database-fixed-point",
            (go, "test", "-count=1", "-json", "-timeout=900s", "-run", pattern,
             target.package),
            cwd=root, env=_check_env(database=True), timeout=960,
            kind="json", expected_tests=target.tests))
    return checks


def _run_process(command: tuple[str, ...], cwd: Path, env: dict[str, str],
                 timeout: int) -> tuple[int | None, str, str]:
    try:
        process = subprocess.Popen(command, cwd=cwd, env=env,
                                   stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT,
                                   text=True, errors="replace",
                                   start_new_session=True)
    except OSError as error:
        return None, "", f"{type(error).__name__}: {error}"
    try:
        output, _ = process.communicate(timeout=timeout)
        return process.returncode, output or "", ""
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        try:
            output, _ = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            output = ""
        return None, output or "", f"timed out after {timeout}s"


def _parse_go_json(text: str) -> tuple[list[dict], str]:
    rows: list[dict] = []
    display: list[str] = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        try:
            row = json.loads(stripped)
        except json.JSONDecodeError:
            display.append(line)
            continue
        if not isinstance(row, dict):
            display.append(line)
            continue
        rows.append(row)
        action = row.get("Action")
        if action == "output":
            display.append(str(row.get("Output", "")).rstrip("\n"))
        elif action == "fail":
            name = row.get("Test")
            if name:
                display.append(f"FAIL: {name}")
            else:
                display.append(f"FAIL\t{row.get('Package', '')}")
        elif action == "skip" and row.get("Test"):
            display.append(f"SKIP: {row['Test']}")
    return rows, "\n".join(display)


def _go_test_evidence(rows: Iterable[dict]) -> dict:
    passed: set[str] = set()
    failed: set[str] = set()
    skipped: set[str] = set()
    for row in rows:
        action, name = row.get("Action"), row.get("Test")
        if not name:
            continue
        if action == "pass":
            passed.add(name)
        elif action == "fail":
            failed.add(name)
        elif action == "skip":
            skipped.add(name)
    return {"passed": sorted(passed), "failed": sorted(failed),
            "skipped": sorted(skipped)}


def _unittest_evidence(output: str) -> tuple[int, str]:
    match = re.search(r"(?m)^Ran (\d+) tests? in ", output)
    if not match:
        return 0, "unittest did not report an executed test count"
    count = int(match.group(1))
    if count == 0:
        return 0, "unittest executed no tests"
    return count, ""


def _syntax_evidence(root: Path, files: Iterable[str]) -> tuple[list[str], list[str]]:
    errors: list[str] = []
    checked: list[str] = []
    for relative in files:
        path = root / relative
        try:
            source = tokenize.open(path).read()
            compile(source, str(path), "exec")
            checked.append(relative)
        except (OSError, LookupError, SyntaxError, UnicodeDecodeError,
                ValueError) as error:
            errors.append(f"{relative}: {type(error).__name__}: {error}")
    return checked, errors


def _write_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text if text.endswith("\n") or not text else text + "\n",
                    encoding="utf-8")


def _safe_log_name(name: str) -> str:
    return re.sub(r"[^A-Za-z0-9_.-]+", "__", name)


def execute_check(check: FastCheck, logs: Path, root: Path) -> CheckResult:
    started = time.monotonic()
    log_path = logs / (_safe_log_name(check.name) + ".log")
    command_text = " ".join(shlex.quote(part) for part in check.command)
    evidence: dict | None = None
    reason = ""
    status = "PASS"
    output = ""

    if check.kind == "python-syntax":
        checked, errors = _syntax_evidence(root, check.files)
        evidence = {"checked": checked, "errors": errors}
        if errors:
            status, reason = "FAIL", "Python syntax check failed"
            output = "\n".join(errors)
        else:
            output = "Python syntax OK: " + ", ".join(checked)
        _write_text(log_path, output)
    else:
        command_text = " ".join(shlex.quote(part) for part in check.command)
        returncode, output, error = _run_process(check.command, check.cwd,
                                                check.env or _base_env(),
                                                check.timeout)
        raw_path = logs / (_safe_log_name(check.name) +
                           (".jsonl" if check.kind == "json" else ".log"))
        if check.kind == "json":
            _write_text(raw_path, output)
            rows, display = _parse_go_json(output)
            evidence = _go_test_evidence(rows)
            _write_text(log_path, display or output)
            if error:
                status, reason = "FAIL", error
            elif returncode != 0:
                status, reason = "FAIL", f"command exited {returncode}"
            elif check.expected_tests:
                required = set(check.expected_tests)
                missing = sorted(required - set(evidence["passed"]))
                skipped = sorted(required & set(evidence["skipped"]))
                failed = sorted(required & set(evidence["failed"]))
                if failed:
                    status, reason = "FAIL", "targeted tests failed: " + ", ".join(failed)
                elif skipped:
                    status, reason = "NOT_RUN", "targeted tests skipped: " + ", ".join(skipped)
                elif missing:
                    status, reason = "NOT_RUN", "targeted tests did not run: " + ", ".join(missing)
            elif not evidence["passed"]:
                status, reason = "NOT_RUN", "no Go tests executed (all skipped or empty package)"
        else:
            _write_text(log_path, output)
            if error:
                status, reason = "FAIL", error
            elif returncode != 0:
                status, reason = "FAIL", f"command exited {returncode}"
            elif check.kind == "gofmt" and output.strip():
                status, reason = "FAIL", "gofmt reported unformatted files"
            elif check.kind == "unittest":
                count, problem = _unittest_evidence(output)
                evidence = {"tests": count}
                if problem:
                    status, reason = "FAIL", problem
    return CheckResult(check.name, status, round(time.monotonic() - started, 3),
                       command_text, str(log_path), reason, evidence)


def _default_out(root: Path) -> Path:
    explicit = os.environ.get("CAW_FAST_OUT", "").strip()
    if explicit:
        return Path(explicit).expanduser().resolve()
    if os.environ.get("GITHUB_ACTIONS") == "true" and os.environ.get("RUNNER_TEMP"):
        return (Path(os.environ["RUNNER_TEMP"]).expanduser().resolve() / "caw-fast")
    delivery = os.environ.get("CAW_DELIVERY_DIR", "").strip()
    if delivery:
        return (Path(delivery).expanduser().resolve() / "verification" / "fast")
    return (root.parent / "contract-audit-delivery" / "verification" / "fast").resolve()


def _ensure_outside_repo(root: Path, out: Path) -> None:
    root = root.resolve()
    out = out.resolve()
    if out == root or root in out.parents:
        raise FastError(f"fast-check output {out} is inside the repository; "
                        "choose a path outside it")


def _write_report(out: Path, report: dict) -> None:
    _write_text(out / "fast-check.json",
                json.dumps(report, ensure_ascii=False, indent=2))


def _write_github_output(path: str, plan: Plan) -> None:
    destination = Path(path).expanduser()
    lines = [f"{name}={'true' if getattr(plan, name) else 'false'}"
             for name in GITHUB_KEYS]
    prefix = ""
    try:
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists() and destination.stat().st_size:
            with destination.open("rb") as handle:
                handle.seek(-1, os.SEEK_END)
                if handle.read(1) != b"\n":
                    prefix = "\n"
        with destination.open("a", encoding="utf-8") as handle:
            handle.write(prefix + "\n".join(lines) + "\n")
    except OSError as error:
        raise FastError(f"cannot append GitHub output {destination}: {error}") from error


def _deferred_checks() -> list[dict]:
    return [
        {"name": "static-full", "status": "NOT_RUN",
         "reason": "whole-repository go vet remains in the full static stage"},
        {"name": "python-full", "status": "NOT_RUN",
         "reason": "only changed-file syntax and directly related Python tests run here"},
        {"name": "web-unit", "status": "NOT_RUN",
         "reason": "web unit tests remain on the full web stage"},
        {"name": "go-race", "status": "NOT_RUN",
         "reason": "full race suite remains on the main and candidate paths"},
        {"name": "coverage-gates", "status": "NOT_RUN",
         "reason": "coverage thresholds remain on the main and candidate paths"},
        {"name": "browser-acceptance", "status": "NOT_RUN",
         "reason": "browser journey remains a release-only acceptance gate"},
    ]


def _new_report(base: str, merge_base: str, plan: Plan) -> dict:
    return {
        "schema_version": "fast-check/v1",
        "started_at": datetime.now(timezone.utc).isoformat(),
        "base": base,
        "merge_base": merge_base,
        "plan": plan.payload(),
        "checks": [],
        "deferred_checks": _deferred_checks(),
        "passed": False,
    }


def _print_summary(plan: Plan, report: dict, out: Path) -> None:
    flags = " ".join(f"{name}={'true' if getattr(plan, name) else 'false'}"
                     for name in GITHUB_KEYS)
    status = "PASS" if report["passed"] else "FAIL"
    print(f"FAST: {status} ({flags})")
    for deferred in report["deferred_checks"]:
        print(f"  {deferred['name']}: NOT_RUN")
    print(f"report: {out / 'fast-check.json'}")


def run_checks(root: Path, plan: Plan, base: str, merge_base: str,
               out: Path, python: str) -> int:
    _ensure_outside_repo(root, out)
    logs = out / "logs"
    logs.mkdir(parents=True, exist_ok=True)
    report = _new_report(base, merge_base, plan)
    missing_database = _required_database_env(plan) if plan.database else []
    if missing_database:
        reason = ("database plan requires " + ", ".join(missing_database) +
                  "; start the isolated PostgreSQL role and rerun")
        report["checks"].append(CheckResult(
            "database-fixed-point", "NOT_RUN", 0.0, "",
            str(logs / "database-fixed-point.log"), reason,
            {"missing_env": missing_database}).payload())
        _write_text(logs / "database-fixed-point.log", reason)
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        _write_report(out, report)
        print(f"FAST: FAIL ({reason})", file=sys.stderr)
        print(f"report: {out / 'fast-check.json'}", file=sys.stderr)
        return 1

    try:
        checks = _build_checks(plan, root, python)
    except FastError as error:
        report["error"] = str(error)
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        _write_report(out, report)
        raise
    if not checks:
        report["passed"] = True
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        _write_report(out, report)
        _print_summary(plan, report, out)
        return 0

    for index, check in enumerate(checks):
        command_text = " ".join(shlex.quote(part) for part in check.command)
        print(f"[{check.name}]{' ' + command_text if command_text else ''}",
              flush=True)
        result = execute_check(check, logs, root)
        report["checks"].append(result.payload())
        if result.status == "PASS":
            print(f"[{check.name}] PASS ({result.seconds}s)")
        elif result.status == "NOT_RUN":
            print(f"[{check.name}] NOT_RUN: {result.reason}")
        else:
            print(f"[{check.name}] FAIL: {result.reason}", file=sys.stderr)
            if check.required:
                tail = ""
                try:
                    tail = Path(result.log).read_text(encoding="utf-8",
                                                      errors="replace")[-4000:]
                except OSError:
                    pass
                if tail:
                    print(tail, file=sys.stderr)
        _write_report(out, report)
        if result.status == "FAIL" or (result.status == "NOT_RUN" and check.required):
            for remaining in checks[index + 1:]:
                report["checks"].append(CheckResult(
                    remaining.name, "NOT_RUN", 0.0,
                    " ".join(shlex.quote(part) for part in remaining.command),
                    "", f"not run after {check.name} did not pass").payload())
            report["passed"] = False
            report["finished_at"] = datetime.now(timezone.utc).isoformat()
            _write_report(out, report)
            _print_summary(plan, report, out)
            return 1
    report["passed"] = True
    report["finished_at"] = datetime.now(timezone.utc).isoformat()
    _write_report(out, report)
    _print_summary(plan, report, out)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default=None,
                        help="git base ref (default: CAW_FAST_BASE or origin/main)")
    parser.add_argument("--plan", action="store_true",
                        help="print the one-line selection JSON without running checks")
    parser.add_argument("--github-output", default=None,
                        help="append go/web/python/database/poppler/legal_assets=true|false")
    parser.add_argument("--out", default=None,
                        help="log/report directory outside the repository")
    parser.add_argument("--root", default=None, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    root = Path(args.root or REPO_ROOT).resolve()
    base = args.base or os.environ.get("CAW_FAST_BASE") or DEFAULT_BASE
    try:
        changes, merge_base = collect_changes(root, base)
        plan = build_plan(root, changes)
        if args.github_output:
            _write_github_output(args.github_output, plan)
        if args.plan:
            print(json.dumps(plan.payload(), ensure_ascii=False,
                             separators=(",", ":")))
            return 0
        out = Path(args.out).expanduser().resolve() if args.out else _default_out(root)
        return run_checks(root, plan, base, merge_base, out, sys.executable)
    except FastError as error:
        print(f"check_fast: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())

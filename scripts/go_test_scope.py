#!/usr/bin/env python3
"""Discover and run Go test packages safe for the database-free unit entrypoint."""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import platform
import re
import shlex
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
SKIP_DIRS = frozenset({".git", ".venv", "artifacts", "coverage", "dist", "node_modules",
                      "testdata", "vendor", "web", "e2e"})
EXCLUDED_UNIT_TAGS = frozenset({"existingDB", "browser", "distributed", "live"})
DATABASE_URL = re.compile(r"\b(?:[A-Z][A-Z0-9_]*_)?DATABASE_URL\b")
DATABASE_OPERATION = re.compile(
    r"\b(?:pgx|pgxpool)\.(?:Connect|ConnectConfig|New|NewWithConfig)\s*\(|"
    r"\bsql\.Open(?:DB)?\s*\(|\b(?:store\.)?OpenTest\s*\(|"
    r"\bstore\.Open[A-Za-z0-9_]*\s*\(|"
    r"\b(?i:[A-Za-z_]\w*(?:db|database|pool|conn|connection|tx|transaction))\.(?:Exec|Query|QueryRow|Begin|BeginTx)\s*\(|"
    r"\b(?:exec|command)\.Command(?:Context)?\s*\(\s*\"(?:psql|postgres)\""
)
FUNCTION = re.compile(r"(?m)^func\s+(?:\([^\n)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(")
CALL = re.compile(r"\b([A-Za-z_][A-Za-z0-9_]*)\s*\(")
BUILD_LINE = re.compile(r"^\s*//(?:go:build\s+|\s*\+build\s+)(.*)$")
GOOS_TAGS = frozenset({"aix", "android", "darwin", "dragonfly", "freebsd", "illumos",
                       "ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris",
                       "wasip1", "windows"})
GOARCH_TAGS = frozenset({"386", "amd64", "amd64p32", "arm", "arm64", "loong64", "mips",
                         "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64",
                         "s390x", "wasm"})


def _go_flags_tags() -> set[str]:
    tokens = shlex.split(os.environ.get("GOFLAGS", ""))
    tags: set[str] = set()
    index = 0
    while index < len(tokens):
        token = tokens[index]
        value = ""
        if token == "-tags" and index + 1 < len(tokens):
            index += 1
            value = tokens[index]
        elif token.startswith("-tags="):
            value = token.partition("=")[2]
        if value:
            tags.update(tag for tag in re.split(r"[ ,]+", value)
                        if tag and tag not in EXCLUDED_UNIT_TAGS)
        index += 1
    return tags


def _host_tags(root: Path, race: bool) -> set[str]:
    goos = os.environ.get("GOOS") or {
        "linux": "linux", "darwin": "darwin", "win32": "windows",
    }.get(sys.platform, sys.platform)
    machine = platform.machine().lower()
    goarch = os.environ.get("GOARCH") or {
        "x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64",
        "i386": "386", "i686": "386", "armv7l": "arm",
    }.get(machine, machine)
    tags = {goos, goarch, *_go_flags_tags()}
    if goos in {"aix", "android", "darwin", "dragonfly", "freebsd", "illumos",
                "ios", "linux", "netbsd", "openbsd", "solaris"}:
        tags.add("unix")
    if os.environ.get("CGO_ENABLED", "1") != "0":
        tags.add("cgo")
    if race:
        tags.add("race")
    module = root / "go.mod"
    if module.is_file():
        version = re.search(r"(?m)^go\s+\d+\.(\d+)", module.read_text(encoding="utf-8"))
        if version:
            tags.update(f"go1.{minor}" for minor in range(1, int(version.group(1)) + 1))
    return tags


def _eval_build_expression(expression: str, tags: set[str]) -> bool:
    tokens = re.findall(r"&&|\|\||[!()]|[A-Za-z0-9_.]+", expression)
    if "".join(tokens) != re.sub(r"\s+", "", expression):
        return False
    cursor = 0

    def atom() -> bool:
        nonlocal cursor
        if cursor >= len(tokens):
            raise ValueError("missing build tag")
        token = tokens[cursor]
        cursor += 1
        if token == "!":
            return not atom()
        if token == "(":
            value = disjunction()
            if cursor >= len(tokens) or tokens[cursor] != ")":
                raise ValueError("unclosed build tag group")
            cursor += 1
            return value
        if token in {"&&", "||", ")"}:
            raise ValueError("unexpected build tag operator")
        return token in tags

    def conjunction() -> bool:
        nonlocal cursor
        value = atom()
        while cursor < len(tokens) and tokens[cursor] == "&&":
            cursor += 1
            right = atom()
            value = value and right
        return value

    def disjunction() -> bool:
        nonlocal cursor
        value = conjunction()
        while cursor < len(tokens) and tokens[cursor] == "||":
            cursor += 1
            right = conjunction()
            value = value or right
        return value

    try:
        result = disjunction()
    except ValueError:
        return False
    return cursor == len(tokens) and result


def _filename_enabled(path: Path, tags: set[str]) -> bool:
    stem = path.name[:-len("_test.go")] if path.name.endswith("_test.go") else path.name.removesuffix(".go")
    parts = stem.split("_")
    if parts[-1] in GOARCH_TAGS:
        if parts[-1] not in tags:
            return False
        if len(parts) > 1 and parts[-2] in GOOS_TAGS and parts[-2] not in tags:
            return False
    elif parts[-1] in GOOS_TAGS and parts[-1] not in tags:
        return False
    return True


def _go_file_enabled(path: Path, tags: set[str]) -> bool:
    go_build: str | None = None
    legacy: list[str] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.startswith("package "):
            break
        match = BUILD_LINE.match(line)
        if match:
            if line.lstrip().startswith("//go:build"):
                go_build = match.group(1)
            else:
                legacy.append(match.group(1))
    if go_build is not None:
        return _filename_enabled(path, tags) and _eval_build_expression(go_build, tags)
    for expression in legacy:
        alternatives = ["(" + " && ".join(group.split(",")) + ")"
                        for group in expression.split()]
        if not alternatives or not _eval_build_expression(" || ".join(alternatives), tags):
            return False
    return _filename_enabled(path, tags)


def _functions(sources: list[str]) -> list[tuple[str, str]]:
    functions: list[tuple[str, str]] = []
    for source in sources:
        headers = list(FUNCTION.finditer(source))
        functions.extend((match.group(1), source[match.start():headers[index + 1].start()
                                                if index + 1 < len(headers) else len(source)])
                         for index, match in enumerate(headers))
    return functions


def _database_events(body: str, database_functions: set[str]) -> list[int]:
    events = [match.start() for match in DATABASE_OPERATION.finditer(body)]
    events.extend(match.start() for match in CALL.finditer(body)
                  if match.group(1) in database_functions)
    return events


def _database_guard_position(body: str, database_functions: set[str]) -> int | None:
    skip = re.search(r"\bt\.Skipf?\s*\(", body)
    if not skip:
        return None
    prefix = body[:skip.start()]
    url_bindings = set(re.findall(
        r"\b([A-Za-z_]\w*)\s*:=\s*[^;\n]*\b[A-Z][A-Z0-9_]*_DATABASE_URL\b", prefix))
    for block in re.finditer(r"\bif\s+([^\n{]+)\{([^{}]*)\}", body):
        condition, statements = block.group(1), block.group(2)
        if not block.start() <= skip.start() < block.end():
            continue
        if not re.search(r"==\s*(?:\"\"|'')", condition):
            continue
        refers_to_database = bool(re.search(r"\b[A-Z][A-Z0-9_]*_DATABASE_URL\b", condition))
        refers_to_database = refers_to_database or any(
            re.search(rf"\b{re.escape(name)}\b", condition) for name in url_bindings)
        if not refers_to_database or not re.search(r"\bt\.Skipf?\s*\(", statements):
            continue
        if any(position < skip.start() for position in _database_events(body, database_functions)):
            return None
        return skip.start()
    return None


def _database_tests_are_guarded(sources: list[str]) -> bool:
    functions = _functions(sources)
    direct_database_functions = {name for name, body in functions
                                 if DATABASE_OPERATION.search(body)}
    database_functions = set(direct_database_functions)
    while True:
        discovered = {name for name, body in functions
                      if any(match.group(1) in database_functions
                             for match in CALL.finditer(body))}
        expanded = database_functions | discovered
        if expanded == database_functions:
            break
        database_functions = expanded

    if any(name == "init" and name in database_functions for name, _ in functions):
        return False

    bodies_by_name: dict[str, list[str]] = {}
    for name, body in functions:
        if name in database_functions:
            bodies_by_name.setdefault(name, []).append(body)
    guarded_functions = {
        name for name, bodies in bodies_by_name.items()
        if all(_database_guard_position(body, database_functions) is not None
               for body in bodies)
    }

    for name, body in functions:
        if not (name.startswith(("Test", "Fuzz", "Example")) or name == "TestMain"):
            continue
        events = _database_events(body, database_functions)
        if not events:
            continue
        local_guard = _database_guard_position(body, database_functions)
        guarded_calls = [match.start() for match in CALL.finditer(body)
                         if match.group(1) in guarded_functions]
        for position in events:
            if position in guarded_calls:
                continue
            if local_guard is not None and local_guard < position:
                continue
            if any(guarded < position for guarded in guarded_calls):
                continue
            return False
    return True


def discover_unit_test_packages(root: Path, *, race: bool = False) -> set[str]:
    """Find default-build test packages whose DB fixture paths fail closed.

    The existingDB, browser, distributed and live tags are opt-in and removed
    from unit runs. Untagged fixtures are included only when every test-reachable
    database operation is preceded by the repository's missing-URL t.Skip guard.
    """
    root = root.resolve()
    tags = _host_tags(root, race)
    packages: set[str] = set()
    for directory, child_dirs, filenames in os.walk(root):
        current = Path(directory)
        relative = current.relative_to(root)
        child_dirs[:] = [name for name in child_dirs
                         if name not in SKIP_DIRS and not name.startswith((".", "_"))]
        if relative != Path(".") and (current / "go.mod").is_file():
            child_dirs[:] = []
            continue
        if relative == Path("e2e"):
            child_dirs[:] = []
            continue
        go_paths = [current / name for name in filenames
                    if name.endswith(".go") and _go_file_enabled(current / name, tags)]
        test_paths = [path for path in go_paths if path.name.endswith("_test.go")]
        if not test_paths:
            continue
        sources = [path.read_text(encoding="utf-8") for path in go_paths]
        test_source_functions = _functions([path.read_text(encoding="utf-8") for path in test_paths])
        if not any(name.startswith(("Test", "Fuzz", "Example")) or name == "TestMain"
                   for name, _ in test_source_functions):
            continue
        if not _database_tests_are_guarded(sources):
            continue
        selector = "./" + relative.as_posix() if relative != Path(".") else "./"
        packages.add(selector)
    return packages


def unit_test_environment(source: dict[str, str] | None = None) -> dict[str, str]:
    """Remove database credentials and opt-in integration tags from unit runs."""
    environment = dict(os.environ if source is None else source)
    for name in tuple(environment):
        if name == "DATABASE_URL" or name.endswith("_DATABASE_URL"):
            environment.pop(name, None)
    environment.pop("DATABASE_SCHEMA", None)
    environment.pop("TYPESAFE_API_KEY", None)
    environment["APP_ENV"] = "test"
    environment["TYPESAFE_JEV_MODE"] = "off"
    tokens = shlex.split(environment.get("GOFLAGS", ""))
    filtered: list[str] = []
    index = 0
    while index < len(tokens):
        token = tokens[index]
        if token == "-tags" and index + 1 < len(tokens):
            index += 1
            remaining = [tag for tag in re.split(r"[ ,]+", tokens[index])
                         if tag and tag not in EXCLUDED_UNIT_TAGS]
            if remaining:
                filtered.extend(("-tags", ",".join(remaining)))
        elif token.startswith("-tags="):
            remaining = [tag for tag in re.split(r"[ ,]+", token.partition("=")[2])
                         if tag and tag not in EXCLUDED_UNIT_TAGS]
            if remaining:
                filtered.append("-tags=" + ",".join(remaining))
        else:
            filtered.append(token)
        index += 1
    if filtered:
        environment["GOFLAGS"] = shlex.join(filtered)
    else:
        environment.pop("GOFLAGS", None)
    return environment


def run_unit_tests(root: Path, go: str) -> int:
    packages = sorted(discover_unit_test_packages(root, race=True))
    if not packages:
        print("test-unit: no supported no-database Go test packages were discovered", file=sys.stderr)
        return 2
    command_prefix = shlex.split(go)
    if not command_prefix:
        print("test-unit: GO must name a Go command", file=sys.stderr)
        return 2
    environment = unit_test_environment()
    print("test-unit packages: " + " ".join(packages), flush=True)
    for command in (
        [*command_prefix, "test", "-race", "-count=1", *packages],
        [*command_prefix, "test", "-race", "-count=1", "./e2e", "-run", "^TestOperator"],
    ):
        result = subprocess.run(command, cwd=root, env=environment)
        if result.returncode:
            return result.returncode
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default=os.environ.get("GO", "go"),
                        help="Go command (defaults to the GO environment variable or go)")
    args = parser.parse_args(argv)
    return run_unit_tests(ROOT, args.go)


if __name__ == "__main__":
    raise SystemExit(main())

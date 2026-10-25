#!/usr/bin/env python3
"""Fail-closed coverage gates. Standard library only; no network or secrets."""
import argparse
import json
import os
import re
import subprocess
from collections import defaultdict
from pathlib import Path


def percentage(hit, total):
    if total <= 0:
        raise ValueError("empty executable coverage denominator")
    return 100 * hit / total


def parse_go(text):
    lines = text.splitlines()
    if not lines or lines[0] not in ("mode: atomic", "mode: count", "mode: set"):
        raise ValueError("missing Go profile header")
    blocks = {}
    for line in lines[1:]:
        if not line.strip():
            continue
        position, statements, count = line.rsplit(" ", 2)
        statements, count = int(statements), int(count)
        if statements < 0 or count < 0:
            raise ValueError("negative coverage value")
        previous = blocks.get(position)
        if previous and previous[0] != statements:
            raise ValueError("inconsistent duplicate block")
        blocks[position] = (statements, count > 0 or bool(previous and previous[1]))
    packages = defaultdict(lambda: [0, 0])
    for position, (statements, hit) in blocks.items():
        filename = position.rsplit(":", 1)[0]
        package = filename.rsplit("/", 1)[0]
        if not package.startswith("contractaudit/internal/"):
            raise ValueError(f"unexpected Go coverage scope: {package}")
        packages[package][0] += statements if hit else 0
        packages[package][1] += statements
    covered = sum(v[0] for v in packages.values())
    total = sum(v[1] for v in packages.values())
    return {
        "covered_statements": covered, "total_statements": total,
        "statement_percent": percentage(covered, total),
        "files": sorted({position.rsplit(":", 1)[0] for position in blocks}),
        "packages": {k: {"covered": v[0], "total": v[1], "percent": percentage(*v)}
                     for k, v in sorted(packages.items()) if v[1]},
    }


def parse_web(value):
    total = value["total"]
    result = {}
    for metric in ("lines", "branches", "functions", "statements"):
        row = total[metric]
        covered, denominator = row["covered"], row["total"]
        if not isinstance(covered, int) or not isinstance(denominator, int) or not 0 <= covered <= denominator:
            raise ValueError(f"invalid {metric} counters")
        result[metric] = {"covered": covered, "total": denominator, "percent": percentage(covered, denominator)}
    return result


def violations(go, web, config):
    problems = []
    minimum = config["go"]["minimum_statement_percent"]
    if go["statement_percent"] + 1e-9 < minimum:
        problems.append(f"Go statements {go['statement_percent']:.2f}% < {minimum}%")
    for package, minimum in config["go"]["packages"].items():
        if package not in go["packages"]:
            problems.append(f"Missing required package: {package}")
        elif go["packages"][package]["percent"] + 1e-9 < minimum:
            problems.append(f"{package} {go['packages'][package]['percent']:.2f}% < {minimum}%")
    for metric in ("lines", "branches", "functions"):
        minimum = config["web"][f"minimum_{metric}_percent"]
        if web[metric]["percent"] + 1e-9 < minimum:
            problems.append(f"Web {metric} {web[metric]['percent']:.2f}% < {minimum}%")
    return problems


def scope_violations(go_files, web_files, expected_go_files, expected_web_files):
    """Missing non-critical files are still denominator loss, not a free pass."""
    problems = []
    for kind, actual, expected in (("Go", set(go_files), set(expected_go_files)),
                                    ("Web", set(web_files), set(expected_web_files))):
        for filename in sorted(expected - actual):
            problems.append(f"Missing {kind} source coverage: {filename}")
        for filename in sorted(actual - expected):
            problems.append(f"Unexpected {kind} source coverage: {filename}")
    return problems


def json_objects(text):
    """go list -json emits consecutive JSON values rather than one array."""
    decoder = json.JSONDecoder()
    remaining = text.lstrip()
    while remaining:
        value, end = decoder.raw_decode(remaining)
        yield value
        remaining = remaining[end:].lstrip()


def source_inventory(root, go_files):
    """Resolve Go build-selected source and all configured web source files.

    A Go file omitted by the coverage runtime is exempt only after Go's own
    cover tool proves it has zero instrumentable statements. No filename
    whitelist may hide newly added executable code.
    """
    go = os.environ.get("GO", "go")
    listed = subprocess.run([go, "list", "-json", "./internal/..."], cwd=root,
                            check=True, capture_output=True, text=True, timeout=60)
    expected_go = set()
    exemptions = []
    packages = []
    observed = set(go_files)
    for package in json_objects(listed.stdout):
        import_path = package["ImportPath"]
        if not import_path.startswith("contractaudit/internal/"):
            raise ValueError(f"unexpected build package: {import_path}")
        packages.append(import_path)
        for name in package.get("GoFiles", []) + package.get("CgoFiles", []):
            key = f"{import_path}/{name}"
            path = Path(package["Dir"]) / name
            if not path.resolve().is_relative_to((root / "internal").resolve()):
                raise ValueError("Go source path escaped internal scope")
            if key not in observed:
                probe = subprocess.run([go, "tool", "cover", "-mode=set", "-var=ScopeProbe", str(path)],
                                       cwd=root, check=True, capture_output=True, text=True, timeout=30)
                literal = re.search(r"NumStmt:\s*\[\d+\]uint16\s*\{(.*?)\}", probe.stdout, re.S)
                if not literal:
                    raise ValueError(f"cannot inspect omitted source: {key}")
                counters = re.sub(r"//[^\n]*", "", literal.group(1))
                if sum(int(value) for value in re.findall(r"\d+", counters)) == 0:
                    exemptions.append(key)
                    continue
            expected_go.add(key)
    if not packages or not expected_go:
        raise ValueError("empty Go source inventory")
    expected_web = set()
    web_src = root / "web" / "src"
    for path in web_src.rglob("*"):
        if not path.is_file() or path.suffix not in (".ts", ".tsx"):
            continue
        relative = path.relative_to(web_src)
        if (relative.parts[0] == "test" or ".test." in path.name or
                path.name.endswith(".d.ts") or relative.as_posix() == "main.tsx"):
            continue
        expected_web.add(path.relative_to(root).as_posix())
    if not expected_web:
        raise ValueError("empty web source inventory")
    return expected_go, expected_web, {"go_packages": sorted(packages),
                                      "go_zero_statement_files": sorted(exemptions)}


def web_source_keys(value, root):
    keys = set()
    for key in value:
        if key == "total":
            continue
        path = Path(key)
        if not path.is_absolute():
            path = root / path
        resolved = path.resolve()
        if not resolved.is_relative_to(root.resolve()):
            raise ValueError(f"web coverage file outside project: {key}")
        keys.add(resolved.relative_to(root.resolve()).as_posix())
    return keys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="artifacts/go-coverage.out")
    parser.add_argument("--web", default="web/coverage/coverage-summary.json")
    parser.add_argument("--config", default="quality-gates.json")
    parser.add_argument("--out", default="artifacts/coverage-summary.json")
    args = parser.parse_args()
    try:
        go = parse_go(Path(args.go).read_text())
        web_value = json.loads(Path(args.web).read_text())
        web = parse_web(web_value)
        config = json.loads(Path(args.config).read_text())
        errors = violations(go, web, config)
        root = Path(__file__).resolve().parents[1]
        expected_go, expected_web, inventory = source_inventory(root, go["files"])
        errors.extend(scope_violations(go["files"], web_source_keys(web_value, root), expected_go, expected_web))
        inventory.update({"go_files": len(expected_go), "web_files": len(expected_web)})
        report = {"go": go, "web": web, "scope": inventory, "passed": not errors, "violations": errors}
        dest = Path(args.out)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(f"Go statements {go['statement_percent']:.2f}%; web lines {web['lines']['percent']:.2f}%; "
              f"branches {web['branches']['percent']:.2f}%; functions {web['functions']['percent']:.2f}%")
        for error in errors:
            print("FAIL:", error)
        return 1 if errors else 0
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print("FAIL: coverage report missing or invalid:", error)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Read-only UI style clue scanner for web/src CSS/TSX; reports native controls, hardcoded colors/sizes, !important, and root-ish overflow clipping without auto-fixing.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-08
# Updated: 2026-11-08
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Emit read-only style clues for UI2-M5. Clues are not confirmed defects."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from dataclasses import asdict, dataclass
from pathlib import Path

ROOT_DEFAULT = Path(__file__).resolve().parents[1]
SCAN_ROOT = Path("web/src")
INCLUDE_SUFFIXES = {".css", ".tsx", ".ts"}
EXCLUDE_PARTS = {"/node_modules/", "/dist/", "/.git/", "/__snapshots__/"}

HARD_COLOR = re.compile(
    r"(?<![\w-])#(?:[0-9a-fA-F]{3,8})\b|(?<![\w-])rgba?\([^)]*\)|(?<![\w-])hsla?\([^)]*\)",
)
HARD_SIZE = re.compile(
    r"(?<![\w-])(?:font-size|line-height|letter-spacing)\s*:\s*[-+]?\d+(?:\.\d+)?(?:px|rem|em)\b",
    re.I,
)
IMPORTANT = re.compile(r"!important\b")
# Lowercase HTML tags only; Ant Design <Button>/<Input> are not native clues.
NATIVE_CONTROL = re.compile(r"<(?:button|input|select|textarea)\b")
OVERFLOW_CLIP = re.compile(
    r"overflow(?:-x|-y)?\s*:\s*(?:hidden|clip)\b",
    re.I,
)
ROOTISH = re.compile(r"(?:^|[^\w-])(?:html|body|:root|#root|\.app-shell|\.admin-shell|\.page)\b")


@dataclass(frozen=True)
class Clue:
    path: str
    line: int
    kind: str
    excerpt: str


def iter_files(root: Path) -> list[Path]:
    base = root / SCAN_ROOT
    if not base.is_dir():
        raise FileNotFoundError(f"missing scan root: {base}")
    files: list[Path] = []
    for path in sorted(base.rglob("*")):
        if not path.is_file() or path.suffix not in INCLUDE_SUFFIXES:
            continue
        text = str(path).replace("\\", "/")
        if any(part in text for part in EXCLUDE_PARTS):
            continue
        if path.name.endswith((".test.ts", ".test.tsx", ".d.ts")):
            continue
        files.append(path)
    return files


def scan_file(root: Path, path: Path) -> list[Clue]:
    rel = path.relative_to(root).as_posix()
    try:
        text = path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        return [Clue(rel, 1, "unreadable", "non-utf8 file skipped as clue")]
    clues: list[Clue] = []
    for idx, line in enumerate(text.splitlines(), start=1):
        stripped = line.strip()
        if not stripped or stripped.startswith("//") or stripped.startswith("*"):
            continue
        excerpt = stripped[:160]
        if path.suffix == ".css" and HARD_COLOR.search(line):
            # Allow CSS variables and token comments; still report literal colors as clues.
            if "var(" not in line:
                clues.append(Clue(rel, idx, "hardcoded-color", excerpt))
        if path.suffix == ".css" and HARD_SIZE.search(line):
            if "var(" not in line:
                clues.append(Clue(rel, idx, "hardcoded-size", excerpt))
        if IMPORTANT.search(line):
            clues.append(Clue(rel, idx, "important", excerpt))
        if path.suffix in {".tsx", ".ts"} and NATIVE_CONTROL.search(line):
            # Prefer antd controls; native tags are clues only.
            clues.append(Clue(rel, idx, "native-control", excerpt))
        if OVERFLOW_CLIP.search(line) and (ROOTISH.search(line) or path.name in {"styles.css", "app-shell.css", "admin-shell.css"}):
            clues.append(Clue(rel, idx, "rootish-overflow-clip", excerpt))
    return clues


def scan(root: Path) -> dict:
    clues: list[Clue] = []
    files = iter_files(root)
    for path in files:
        clues.extend(scan_file(root, path))
    counts = Counter(c.kind for c in clues)
    return {
        "status": "PASS",
        "mode": "clues-only",
        "note": "Clues are not confirmed defects and must not be auto-fixed by this tool.",
        "files_scanned": len(files),
        "clue_counts": dict(sorted(counts.items())),
        "clue_total": len(clues),
        "clues": [asdict(c) for c in clues],
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT_DEFAULT)
    parser.add_argument("--json", action="store_true", help="print full JSON including every clue")
    parser.add_argument("--limit", type=int, default=40, help="max clues to print in text mode")
    args = parser.parse_args(argv)
    try:
        report = scan(args.root.resolve())
    except FileNotFoundError as exc:
        print(json.dumps({"status": "FAIL", "error": str(exc)}), file=sys.stderr)
        return 2
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return 0
    print(f"status={report['status']} mode={report['mode']} files={report['files_scanned']} clues={report['clue_total']}")
    for kind, count in report["clue_counts"].items():
        print(f"  {kind}: {count}")
    for clue in report["clues"][: max(0, args.limit)]:
        print(f"{clue['path']}:{clue['line']}: {clue['kind']}: {clue['excerpt']}")
    if report["clue_total"] > args.limit:
        print(f"... {report['clue_total'] - args.limit} more clues omitted; use --json for full output")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

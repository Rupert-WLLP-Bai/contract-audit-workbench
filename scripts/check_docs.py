#!/usr/bin/env python3
"""Check handbook-local Markdown links and the canonical glossary views."""

from __future__ import annotations

import re
import subprocess
import sys
import unicodedata
from pathlib import Path
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"
LINK = re.compile(r"\[[^\]]*\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+[^)]*)?\s*\)")
FENCE = re.compile(r"^\s*(```+|~~~+)")
INLINE_CODE = re.compile(r"`+[^`]*`+")
HTML_ID = re.compile(r"\bid\s*=\s*['\"]([^'\"]+)['\"]", re.IGNORECASE)


def _markdown_files() -> list[Path]:
    return sorted(path for path in DOCS.rglob("*.md") if path.is_file())


def _headings(path: Path) -> set[str]:
    headings: set[str] = set()
    used: dict[str, int] = {}
    in_fence = False
    fence_marker = ""
    for line in path.read_text(encoding="utf-8").splitlines():
        fence = FENCE.match(line)
        if fence:
            marker = fence.group(1)[0]
            if not in_fence:
                in_fence, fence_marker = True, marker
            elif marker == fence_marker:
                in_fence = False
            continue
        if in_fence:
            continue
        headings.update(HTML_ID.findall(line))
        match = re.match(r"^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$", line)
        if not match:
            continue
        title = INLINE_CODE.sub("", match.group(1))
        title = re.sub(r"!??\[([^]]+)\]\([^)]*\)", r"\1", title)
        chars = []
        for char in title.lower():
            category = unicodedata.category(char)
            if char.isspace():
                chars.append("-")
            elif char in "-_" or category[0] in {"L", "N", "M"}:
                chars.append(char)
        base = re.sub(r"-+", "-", "".join(chars)).strip("-")
        if not base:
            continue
        duplicate = used.get(base, 0)
        used[base] = duplicate + 1
        headings.add(base if duplicate == 0 else f"{base}-{duplicate}")
    return headings


def _resolve_target(source: Path, target: str) -> Path | None:
    candidate = (ROOT / target.lstrip("/")) if target.startswith("/") else source.parent / target
    candidate = candidate.resolve()
    try:
        candidate.relative_to(ROOT)
    except ValueError:
        return None
    if candidate.is_file():
        return candidate
    if candidate.is_dir():
        for name in ("index.md", "README.md"):
            if (candidate / name).is_file():
                return candidate / name
        return None
    if not candidate.suffix:
        for suffix in (".md", ".html"):
            alternative = candidate.with_suffix(suffix)
            if alternative.is_file():
                return alternative
    return None


def check_links(files: list[Path]) -> list[str]:
    errors: list[str] = []
    heading_cache: dict[Path, set[str]] = {}
    checked = 0
    for source in files:
        lines = source.read_text(encoding="utf-8").splitlines()
        in_fence = False
        fence_marker = ""
        for line_number, line in enumerate(lines, 1):
            fence = FENCE.match(line)
            if fence:
                marker = fence.group(1)[0]
                if not in_fence:
                    in_fence, fence_marker = True, marker
                elif marker == fence_marker:
                    in_fence = False
                continue
            if in_fence:
                continue
            line = INLINE_CODE.sub("", line)
            for match in LINK.finditer(line):
                raw = match.group(1) or match.group(2) or ""
                url = urlsplit(raw)
                if url.scheme or raw.startswith("//"):
                    continue
                checked += 1
                path_text = unquote(url.path)
                if not path_text:
                    target = source
                else:
                    target = _resolve_target(source, path_text)
                if target is None:
                    errors.append(f"{source.relative_to(ROOT)}:{line_number}: missing target {raw!r}")
                    continue
                if url.fragment and target.suffix.lower() == ".md":
                    headings = heading_cache.setdefault(target, _headings(target))
                    if unquote(url.fragment) not in headings:
                        errors.append(f"{source.relative_to(ROOT)}:{line_number}: missing anchor {url.fragment!r} in {target.relative_to(ROOT)}")
    print(f"Markdown links: checked {checked} local targets across {len(files)} files")
    return errors


def main() -> int:
    files = _markdown_files()
    if not files:
        print("FAIL: docs/ contains no Markdown handbook pages", file=sys.stderr)
        return 1
    glossary = subprocess.run(
        [sys.executable, str(ROOT / "scripts/check_glossary.py")],
        cwd=ROOT, check=False,
    )
    errors = check_links(files)
    if glossary.returncode:
        print("FAIL: canonical glossary validation failed", file=sys.stderr)
        return glossary.returncode
    if errors:
        for error in errors:
            print(f"FAIL: {error}", file=sys.stderr)
        return 1
    print("PASS: handbook links and canonical glossary source/views validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

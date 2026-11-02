# File metadata (manifest mirror):
# Purpose: Source-repository boundary: runtime code stays, delivery material stays out. This module’s command-line arguments and return artifacts define its operational boundary.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-25
# Updated: 2026-11-02
# Revision: r2
# Copyright: See LICENSE
# End file metadata.

"""Source-repository boundary: runtime code stays, delivery material stays out."""
import os
from pathlib import Path
import re
import subprocess

REQUIRED_MARKDOWN = {"README.md", "AGENTS.md"}
ROOT_MARKDOWN_ALLOWLIST = REQUIRED_MARKDOWN | {"CHANGELOG.md", "CONTRIBUTING.md"}
HANDBOOK_DIRS = {"architecture", "engineering", "glossary", "operations", "product"}
FORBIDDEN_ROOTS = {"demos", "presentation", "materials", "references", "screenshots"}
FORBIDDEN_DOC_DIRS = {"docs/research", "docs/prompts", "docs/samples", "docs/reports", "docs/screenshots"}
HANDBOOK_DATA = {"docs/glossary/concepts.json", "docs/architecture/store-ownership.csv"}
BULK_SUFFIXES = {
    ".pdf", ".doc", ".docx", ".ppt", ".pptx", ".xls", ".xlsx",
    ".png", ".jpg", ".jpeg", ".webp", ".gif", ".mp4", ".mov", ".wav",
    ".zip", ".tar", ".tgz", ".7z", ".rar",
    ".safetensors", ".gguf", ".onnx", ".pt", ".pth",
}
FIXTURE_MARKERS = ("testdata",)
# Current regression fixtures are under 100 KiB. This bound keeps a force-added
# design dump or dataset from using the fixture exception.
FIXTURE_MAX_BYTES = 262144
# Explicit runtime assets are required to render the shipped application.
RUNTIME_ASSETS = {"web/public/huangjueshu-competition-logo.png"}
RUNTIME_ASSET_MAX_BYTES = 1 << 20


def load_rules(root):
    path = Path(root) / ".gitignore"
    rules = []
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        negation = line.startswith("!")
        pattern = line[1:] if negation else line
        directory_only = pattern.endswith("/")
        if directory_only:
            pattern = pattern[:-1]
        anchored = pattern.startswith("/")
        if anchored:
            pattern = pattern[1:]
        elif "/" not in pattern:
            pattern = "**/" + pattern
        rules.append((negation, directory_only, re.compile(glob_to_regex(pattern))))
    return rules


def glob_to_regex(pattern):
    parts = []
    index = 0
    while index < len(pattern):
        if pattern.startswith("**/", index):
            parts.append("(?:.*/)?")
            index += 3
            continue
        if pattern.startswith("**", index):
            parts.append(".*")
            index += 2
            continue
        character = pattern[index]
        if character == "*":
            parts.append("[^/]*")
        elif character == "?":
            parts.append("[^/]")
        else:
            parts.append(re.escape(character))
        index += 1
    return r"\A" + "".join(parts) + r"\Z"


def is_ignored(root, relative):
    relative = relative.replace("\\", "/").lstrip("/")
    ignored = False
    for negation, directory_only, expression in load_rules(root):
        matched = False
        if directory_only:
            parts = relative.split("/")
            # A directory rule ignores that directory and everything inside it.
            for depth in range(1, len(parts) + 1):
                if expression.fullmatch("/".join(parts[:depth])):
                    matched = True
                    break
        elif expression.fullmatch(relative):
            matched = True
        if matched:
            ignored = not negation
    return ignored


def tracked_files(root):
    """Use only this tree’s Git metadata; source archives use filesystem inventory."""
    root = Path(root).resolve()
    metadata = root / ".git"
    if not os.path.lexists(metadata):
        return [], ""
    # An explicit metadata path prevents discovery of a surrounding checkout,
    # including when this tree has a corrupt .git directory or Gitfile.
    try:
        completed = subprocess.run(
            ["git", "-C", str(root), "--git-dir", str(metadata),
             "--work-tree", str(root), "ls-files", "-z"],
            capture_output=True,
        )
    except OSError as error:
        return None, str(error)
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", "replace").strip() or "git ls-files failed"
        return None, detail
    paths = [item.decode("utf-8") for item in completed.stdout.split(b"\0") if item]
    return paths, ""


def violations(root):
    root = Path(root)
    found = []
    if not (root / "README.md").is_file():
        found.append("README.md is missing")
    if not (root / "AGENTS.md").is_file():
        found.append("AGENTS.md is missing")
    for name in REQUIRED_MARKDOWN:
        if (root / name).is_file() and is_ignored(root, name):
            found.append(f"{name} is ignored; the repository policy must keep it")
    tracked, err = tracked_files(root)
    if err:
        found.append(f"tracked file inventory failed: {err}")
        tracked = []
    ordered = []
    seen = set()
    for relative in list(tracked) + list(iter_unignored(root)):
        relative = relative.replace("\\", "/").lstrip("/")
        if not relative or relative in seen:
            continue
        seen.add(relative)
        ordered.append(relative)
    for relative in ordered:
        _append_path_violations(root, relative, found)
    return found


def _markdown_allowed(relative):
    """Keep Markdown only in the root/docs handbooks and exact maintenance pages."""
    if relative in ROOT_MARKDOWN_ALLOWLIST:
        return True
    if relative in {"docs/index.md", ".github/pull_request_template.md", "docs-site/README.md"}:
        return True
    parts = Path(relative).parts
    return len(parts) >= 3 and parts[0] == "docs" and parts[1] in HANDBOOK_DIRS


def _append_path_violations(root, relative, found):
    path = Path(relative)
    if path.suffix.lower() == ".md" and not _markdown_allowed(relative):
        found.append(f"markdown is outside the source allowlist: {relative}")
    if path.parts and path.parts[0] in FORBIDDEN_ROOTS:
        found.append(f"delivery material is in the source tree: {relative}")
    if path.parts and path.parts[0] == "docs":
        if any(relative == prefix or relative.startswith(prefix + "/") for prefix in FORBIDDEN_DOC_DIRS):
            found.append(f"delivery material is in the source tree: {relative}")
        elif path.suffix.lower() != ".md" and relative not in HANDBOOK_DATA:
            found.append(f"docs content is outside the handbook allowlist: {relative}")
    if path.suffix.lower() not in BULK_SUFFIXES:
        return
    if path.as_posix() in RUNTIME_ASSETS:
        maximum = RUNTIME_ASSET_MAX_BYTES
        category = "runtime asset"
    elif _fixture(path):
        maximum = FIXTURE_MAX_BYTES
        category = "fixture"
    else:
        found.append(f"bulk or presentation file is outside the fixture exception: {relative}")
        return
    try:
        size = (root / relative).stat().st_size
    except OSError as error:
        found.append(f"{category} could not be measured: {relative} ({error})")
        return
    if size > maximum:
        found.append(f"{category} exceeds {maximum} bytes: {relative} ({size})")


def _fixture(path):
    if FIXTURE_MARKERS[0] in path.parts:
        return True
    return path.as_posix().startswith("web/e2e/fixtures/")


def iter_unignored(root):
    root = Path(root)
    for directory, dirnames, filenames in os.walk(root):
        current = Path(directory)
        relative_dir = current.relative_to(root).as_posix()
        if relative_dir == ".":
            relative_dir = ""
        kept = []
        for name in dirnames:
            if name == ".git":
                continue
            child = f"{relative_dir}/{name}" if relative_dir else name
            if not is_ignored(root, child):
                kept.append(name)
        dirnames[:] = kept
        for name in filenames:
            relative = f"{relative_dir}/{name}" if relative_dir else name
            if not is_ignored(root, relative):
                yield relative

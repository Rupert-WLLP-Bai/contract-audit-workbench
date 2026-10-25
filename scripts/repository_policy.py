"""Source-repository boundary: runtime code stays, delivery material stays out."""
import os
from pathlib import Path
import re

REQUIRED_MARKDOWN = {"README.md", "AGENTS.md"}
FORBIDDEN_ROOTS = {"docs", "demos", "presentation", "materials", "references", "screenshots"}
BULK_SUFFIXES = {
    ".pdf", ".doc", ".docx", ".ppt", ".pptx", ".xls", ".xlsx",
    ".png", ".jpg", ".jpeg", ".webp", ".gif", ".mp4", ".mov", ".wav",
    ".zip", ".tar", ".tgz", ".7z", ".rar",
    ".safetensors", ".gguf", ".onnx", ".pt", ".pth",
}
FIXTURE_MARKERS = ("testdata",)


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
    for relative in iter_unignored(root):
        path = Path(relative)
        if path.suffix.lower() == ".md" and relative not in REQUIRED_MARKDOWN:
            found.append(f"unignored markdown is outside the source allowlist: {relative}")
        if path.parts and path.parts[0] in FORBIDDEN_ROOTS:
            found.append(f"delivery material is not ignored: {relative}")
        if path.suffix.lower() in BULK_SUFFIXES and not _fixture(path):
            found.append(f"bulk or presentation file is not ignored: {relative}")
    return found


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

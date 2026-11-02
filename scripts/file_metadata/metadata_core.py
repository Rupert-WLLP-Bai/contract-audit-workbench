"""Conservative byte-preserving core for a future file-metadata checker.

This draft deliberately handles only a recognized metadata mirror block and
body/mode comparison. Git inventory, schema validation, provenance, and CI
selection remain separate, not-yet-implemented work.
"""
from __future__ import annotations

from dataclasses import dataclass
import re
from typing import Mapping


LABELS = (b"Purpose", b"Author", b"Contact", b"Created", b"Updated", b"Revision", b"Copyright")
START = b"File metadata (manifest mirror):"
END = b"End file metadata."
SUPPORTED_HANDLING = {"header-and-manifest", "manifest-only", "preserve-notice-and-manifest", "generated-manifest-only"}


class MetadataError(ValueError):
    """Input cannot be safely normalized under the supported metadata rules."""


@dataclass(frozen=True)
class CommentStyle:
    prefix: bytes
    suffix: bytes = b""


STYLES = {
    "slash": CommentStyle(b"// "),
    "hash": CommentStyle(b"# "),
    "html": CommentStyle(b"<!-- ", b" -->"),
    "block": CommentStyle(b"/* ", b" */"),
}
FILE_KIND_STYLE = {
    "go": "slash",
    "python": "hash",
    "shell": "hash",
    "hash-config": "hash",
    "toml": "hash",
    "env": "hash",
    "makefile": "hash",
    "typescript": "slash",
    "javascript": "slash",
    "html": "html",
    "markdown": "html",
    "css": "block",
}
NORMALIZABLE_GIT_MODES = {"100644", "100755"}


@dataclass(frozen=True)
class ParsedHeader:
    fields: Mapping[str, str]
    start: int
    end: int


def _line_ending(data: bytes) -> bytes:
    return b"\r\n" if b"\r\n" in data else b"\n"


def _split_lines(data: bytes) -> tuple[list[bytes], list[int]]:
    """Return raw lines without endings and each line's absolute byte offset."""
    if not data:
        return [], []
    chunks = data.split(b"\n")
    lines: list[bytes] = []
    offsets: list[int] = []
    pos = 0
    for index, chunk in enumerate(chunks):
        if index == len(chunks) - 1 and chunk == b"" and data.endswith(b"\n"):
            break
        offsets.append(pos)
        lines.append(chunk[:-1] if chunk.endswith(b"\r") else chunk)
        pos += len(chunk) + 1
    return lines, offsets


def _content_line(raw: bytes, style: CommentStyle) -> bytes:
    line = raw
    if style.suffix:
        if not (line.startswith(style.prefix) and line.endswith(style.suffix)):
            raise MetadataError("metadata block has an invalid comment wrapper")
        line = line[len(style.prefix) : -len(style.suffix)]
        terminator = b"-->" if b"--" in style.suffix else b"*/"
        opener = b"<!--" if terminator == b"-->" else b"/*"
        if terminator in line or opener in line:
            raise MetadataError("metadata line contains an internal comment delimiter")
    else:
        if not line.startswith(style.prefix):
            raise MetadataError("metadata block has an invalid line-comment prefix")
        line = line[len(style.prefix) :]
    return line


def parse_header(data: bytes, style: CommentStyle) -> ParsedHeader | None:
    """Find one canonical block; reject duplicates, malformed markers/order, or ambiguity."""
    if style not in STYLES.values():
        raise MetadataError("comment style is not an approved native style")
    start_hits = data.count(START)
    end_hits = data.count(END)
    if start_hits == 0 and end_hits == 0:
        return None
    if start_hits != 1 or end_hits != 1:
        raise MetadataError("metadata markers are missing, duplicated, or ambiguous")
    lines, offsets = _split_lines(data)
    start_index = end_index = -1
    for i, raw in enumerate(lines):
        try:
            value = _content_line(raw, style)
        except MetadataError:
            continue
        if value == START:
            start_index = i
        elif value == END:
            end_index = i
    if start_index < 0 or end_index <= start_index:
        raise MetadataError("metadata markers are not complete comment lines")
    if end_index - start_index != len(LABELS) + 1:
        raise MetadataError("metadata block must contain exactly seven ordered fields")
    fields: dict[str, str] = {}
    if _content_line(lines[start_index], style) != START or _content_line(lines[end_index], style) != END:
        raise MetadataError("metadata marker lines are malformed")
    for label_index, label in enumerate(LABELS, start=start_index + 1):
        value = _content_line(lines[label_index], style)
        key, sep, field_value = value.partition(b": ")
        if not sep or key != label or not field_value:
            raise MetadataError("metadata fields are missing, empty, or out of order")
        try:
            fields[label.decode("ascii")] = field_value.decode("utf-8", "strict")
        except UnicodeDecodeError as exc:
            raise MetadataError("metadata field is not valid UTF-8") from exc
        if b"\r" in field_value or b"\n" in field_value:
            raise MetadataError("metadata field contains a line break")
    start = offsets[start_index]
    final_line = lines[end_index]
    end = offsets[end_index] + len(final_line)
    newline = _line_ending(data)
    # Require one empty separator line after the end marker. This exact envelope
    # makes add/remove a reversible byte operation instead of normalizing body.
    if data[end:end + len(newline) * 2] != newline * 2:
        raise MetadataError("metadata block must be followed by exactly one blank separator line")
    end += len(newline) * 2
    return ParsedHeader(fields=fields, start=start, end=end)


def metadata_body(data: bytes, style: CommentStyle, handling: str, *, file_kind: str | None = None,
                  mode: str) -> bytes:
    """Return bytes outside one eligible header; protected formats remain byte-exact."""
    if handling not in SUPPORTED_HANDLING:
        raise MetadataError(f"unsupported handling value: {handling!r}")
    if handling != "header-and-manifest":
        return data
    if mode not in NORMALIZABLE_GIT_MODES:
        raise MetadataError(f"Git mode {mode!r} is not eligible for metadata normalization")
    _validate_kind_style(file_kind, style)
    header = parse_header(data, style)
    if header is None:
        return data
    if file_kind is None:
        raise MetadataError("file kind is required to verify safe metadata placement")
    if header.start != _safe_insertion_offset(data, file_kind):
        raise MetadataError("metadata header is not at the approved directive-safe location")
    return data[: header.start] + data[header.end :]


def content_changed(before: bytes, after: bytes, *, mode_before: str, mode_after: str,
                    style: CommentStyle, handling: str, file_kind: str) -> bool:
    """One auditable snapshot-window change means body bytes OR Git mode changed."""
    if handling not in SUPPORTED_HANDLING:
        raise MetadataError(f"unsupported handling value: {handling!r}")
    if mode_before not in NORMALIZABLE_GIT_MODES or mode_after not in NORMALIZABLE_GIT_MODES:
        return before != after or mode_before != mode_after
    before_body = metadata_body(before, style, handling, file_kind=file_kind, mode=mode_before)
    after_body = metadata_body(after, style, handling, file_kind=file_kind, mode=mode_after)
    return before_body != after_body or mode_before != mode_after


def render_fields(record: Mapping[str, object]) -> dict[str, str]:
    """Render the seven human mirror values from a manifest record."""
    try:
        authors = record["authors"]
        purpose = record["purpose"]
        created = record["created"]
        updated = record["updated"]
        revision = record["revision"]
        copyright = record["copyright"]
        if not isinstance(authors, list) or not isinstance(copyright, list):
            raise TypeError
        author_values = []
        for author in authors:
            if not isinstance(author, dict) or not author.get("name"):
                raise TypeError
            name = str(author["name"])
            email = author.get("email")
            author_values.append(f"{name} <{email}>" if email else name)
        if not isinstance(created, dict) or not isinstance(updated, dict):
            raise TypeError
        created_value, updated_value = str(created["value"]), str(updated["value"])
        copyright_values = [str(item["notice"]) for item in copyright if isinstance(item, dict) and item.get("notice")]
        if not copyright_values:
            raise TypeError
        values = {
            "Purpose": str(purpose),
            "Author": "; ".join(author_values),
            "Contact": "; ".join(str(a["contact"]) for a in authors if isinstance(a, dict) and a.get("contact")),
            "Created": created_value,
            "Updated": updated_value,
            "Revision": str(revision),
            "Copyright": "; ".join(copyright_values),
        }
    except (KeyError, TypeError, ValueError) as exc:
        raise MetadataError("manifest record cannot produce a complete header mirror") from exc
    if any(not value or "\n" in value or "\r" in value for value in values.values()):
        raise MetadataError("header mirror values must be non-empty single-line text")
    return values


def insert_header(data: bytes, record: Mapping[str, object], *, style: CommentStyle,
                  handling: str, file_kind: str, mode: str) -> bytes:
    """Insert one mirror only at a conservative, supported directive boundary."""
    if handling != "header-and-manifest":
        raise MetadataError("header insertion is forbidden for manifest-only/protected content")
    if file_kind in {"json", "sql", "lock", "binary", "generated", "fixture", "third-party"}:
        raise MetadataError(f"header insertion is forbidden for {file_kind} files")
    _validate_kind_style(file_kind, style)
    if mode not in NORMALIZABLE_GIT_MODES:
        raise MetadataError(f"Git mode {mode!r} is not eligible for metadata normalization")
    existing = parse_header(data, style)
    fields = render_fields(record)
    if existing is not None:
        if existing.start != _safe_insertion_offset(data, file_kind):
            raise MetadataError("existing metadata header is not at the approved directive-safe location")
        if dict(existing.fields) != fields:
            raise MetadataError("existing metadata header differs from the manifest mirror")
        return data
    newline = _line_ending(data)
    insertion = _safe_insertion_offset(data, file_kind)
    block_lines = [START] + [label + b": " + fields[label.decode("ascii")].encode("utf-8") for label in LABELS] + [END]
    for line in block_lines:
        if style.suffix:
            terminator = b"-->" if b"--" in style.suffix else b"*/"
            opener = b"<!--" if terminator == b"-->" else b"/*"
            if terminator in line or opener in line:
                raise MetadataError("header value contains a block-comment delimiter")
    rendered = newline.join(style.prefix + line + style.suffix for line in block_lines) + newline * 2
    needs_leading_newline = insertion > 0 and data[insertion - 1 : insertion] not in (b"\n", b"\r")
    if needs_leading_newline:
        raise MetadataError("directive preamble has no line ending; refusing non-reversible insertion")
    return data[:insertion] + rendered + data[insertion:]


def _validate_kind_style(file_kind: str | None, style: CommentStyle) -> None:
    if file_kind not in FILE_KIND_STYLE:
        raise MetadataError(f"no safe native comment rule for file kind {file_kind!r}")
    expected = STYLES[FILE_KIND_STYLE[file_kind]]
    if style != expected:
        raise MetadataError(f"comment style does not match native syntax for {file_kind!r}")


def _safe_insertion_offset(data: bytes, file_kind: str) -> int:
    """Keep directives and format prologues before the metadata block."""
    newline = _line_ending(data)
    pos = 3 if data.startswith(b"\xef\xbb\xbf") else 0
    lines, offsets = _split_lines(data[pos:])
    local_offsets = [pos + value for value in offsets]
    consumed = 0
    if lines and lines[0].startswith(b"#!"):
        consumed = 1
    if file_kind == "python":
        for i in range(min(2, len(lines))):
            if re.search(rb"coding\s*[:=]\s*[-\w.]+", lines[i]):
                consumed = max(consumed, i + 1)
    elif file_kind == "go":
        while consumed < len(lines) and lines[consumed].startswith((b"//go:build ", b"// +build ")):
            consumed += 1
        if consumed and consumed < len(lines) and lines[consumed] == b"":
            consumed += 1
    elif file_kind == "markdown" or file_kind == "html":
        if consumed < len(lines) and lines[consumed].strip() == b"---":
            closing = next((i for i in range(consumed + 1, len(lines)) if lines[i].strip() == b"---"), None)
            if closing is None:
                raise MetadataError("unterminated front matter; refusing insertion")
            consumed = closing + 1
            if consumed < len(lines) and lines[consumed] == b"":
                consumed += 1
        elif consumed < len(lines) and lines[consumed].lower().startswith(b"<!doctype html"):
            consumed += 1
            if consumed < len(lines) and lines[consumed] == b"":
                consumed += 1
    elif file_kind == "css":
        if consumed < len(lines) and lines[consumed].lstrip().lower().startswith(b"@charset "):
            consumed += 1
            if consumed < len(lines) and lines[consumed] == b"":
                consumed += 1
    elif file_kind in {"typescript", "javascript"}:
        while consumed < len(lines) and lines[consumed].strip().startswith((b'"use ', b"'use ")) and lines[consumed].strip().endswith((b'";', b"';")):
            consumed += 1
        if consumed and consumed < len(lines) and lines[consumed] == b"":
            consumed += 1
    elif file_kind in {"shell", "hash-config", "toml", "env", "makefile"}:
        pass
    else:
        raise MetadataError(f"no safe insertion rule for file kind {file_kind!r}")
    if consumed == 0:
        return pos
    if consumed >= len(lines):
        return len(data)
    # Include directive line endings while keeping the original following body intact.
    return local_offsets[consumed]

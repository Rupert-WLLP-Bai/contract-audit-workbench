# File metadata (manifest mirror):
# Purpose: NUL-safe Git snapshots and conservative per-path provenance for FILE metadata.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-02
# Updated: 2026-11-02
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""NUL-safe Git snapshots and conservative per-path provenance for FILE metadata.

This is an external #124/#125 draft module. It does not touch the repository or
write a manifest. All Git commands use argv vectors and binary pipes.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
import re
import subprocess
from typing import Iterable

from metadata_core import CommentStyle, MetadataError, content_changed, metadata_body

MANIFEST_SELF_PATH = "scripts/file_metadata/manifest.json"
SHA_RE = re.compile(rb"^[0-9a-f]{40,64}$")
REVISION_RE = re.compile(r"^r([1-9][0-9]*)$")


class GitInventoryError(RuntimeError):
    """Git output is unavailable, incomplete, ambiguous, or unsafe to represent."""


@dataclass(frozen=True)
class TreeEntry:
    path: str
    raw_path: bytes
    mode: str
    object_type: str
    oid: str
    content: bytes | None


@dataclass(frozen=True)
class RawChange:
    status: str
    old_mode: str
    new_mode: str
    old_oid: str
    new_oid: str
    old_path: bytes | None
    new_path: bytes | None


@dataclass(frozen=True)
class FileEvidence:
    value: str
    source: dict[str, str]


@dataclass(frozen=True)
class PathProvenance:
    created: FileEvidence
    updated: FileEvidence
    origin_path: str | None
    followed_rename: bool


class GitRepo:
    def __init__(self, repo: str | Path, *, timeout: float = 30.0):
        self.repo = Path(repo).resolve()
        self.timeout = timeout
        self._blob_cache: dict[str, bytes] = {}

    def run(self, *args: str | bytes, input: bytes | None = None) -> bytes:
        argv: list[str | bytes] = ["git", "-C", str(self.repo)]
        argv.extend(args)
        try:
            result = subprocess.run(
                argv,
                input=input,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
                shell=False,
                timeout=self.timeout,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise GitInventoryError(f"Git command could not run: {argv!r}: {exc}") from exc
        if result.returncode:
            error = result.stderr.decode("utf-8", "backslashreplace").strip()
            raise GitInventoryError(f"Git command failed ({result.returncode}): {argv!r}: {error}")
        return result.stdout

    def commit_identity(self, revision: str) -> tuple[str, str]:
        raw = self.run("rev-parse", "--verify", f"{revision}^{{commit}}").strip()
        if not SHA_RE.fullmatch(raw):
            raise GitInventoryError(f"Git returned an invalid commit identity for {revision!r}")
        sha = raw.decode("ascii")
        tree = self.run("rev-parse", "--verify", f"{sha}^{{tree}}").strip()
        if not SHA_RE.fullmatch(tree):
            raise GitInventoryError(f"Git returned an invalid tree identity for {sha}")
        return sha, tree.decode("ascii")

    def require_baseline_ancestor(self, baseline: str, subject: str) -> tuple[str, str]:
        """Validate provenance baseline separately from subject-tree coverage."""
        baseline_sha, _ = self.commit_identity(baseline)
        subject_sha, subject_tree = self.commit_identity(subject)
        try:
            self.run("merge-base", "--is-ancestor", baseline_sha, subject_sha)
        except GitInventoryError as exc:
            raise GitInventoryError(f"manifest baseline {baseline_sha} is not a verified ancestor of subject {subject_sha}") from exc
        return subject_sha, subject_tree

    def tree_inventory(self, revision: str) -> dict[str, TreeEntry]:
        sha, _ = self.commit_identity(revision)
        raw = self.run("ls-tree", "-r", "-z", "--full-tree", sha, "--")
        rows: list[tuple[bytes, str, str, str]] = []
        for record in _nul_records(raw, "ls-tree"):
            metadata, sep, path = record.partition(b"\t")
            if not sep or b"\x00" in path:
                raise GitInventoryError("malformed ls-tree record")
            try:
                mode, object_type, oid = metadata.decode("ascii").split(" ")
            except (UnicodeDecodeError, ValueError) as exc:
                raise GitInventoryError("malformed ls-tree object metadata") from exc
            _validate_object_fields(mode, object_type, oid)
            rows.append((path, mode, object_type, oid))
        return self._materialize(rows)

    def index_inventory(self) -> dict[str, TreeEntry]:
        raw = self.run("ls-files", "--stage", "-z")
        rows: list[tuple[bytes, str, str, str]] = []
        for record in _nul_records(raw, "ls-files --stage"):
            metadata, sep, path = record.partition(b"\t")
            if not sep:
                raise GitInventoryError("malformed ls-files stage record")
            try:
                mode, oid, stage = metadata.decode("ascii").split(" ")
            except (UnicodeDecodeError, ValueError) as exc:
                raise GitInventoryError("malformed ls-files stage metadata") from exc
            if stage != "0":
                raise GitInventoryError(f"unmerged index entry has stage {stage}; refusing ambiguous inventory")
            object_type = "commit" if mode == "160000" else "blob"
            _validate_object_fields(mode, object_type, oid)
            rows.append((path, mode, object_type, oid))
        return self._materialize(rows)

    def _materialize(self, rows: list[tuple[bytes, str, str, str]]) -> dict[str, TreeEntry]:
        raw_seen: set[bytes] = set()
        blob_ids = list(dict.fromkeys(oid for _, mode, object_type, oid in rows if object_type == "blob" and mode != "160000"))
        blobs = self.cat_blobs(blob_ids)
        result: dict[str, TreeEntry] = {}
        for raw_path, mode, object_type, oid in rows:
            if raw_path in raw_seen:
                raise GitInventoryError(f"duplicate tracked path record: {_path_diagnostic(raw_path)}")
            raw_seen.add(raw_path)
            try:
                path = raw_path.decode("utf-8", "strict")
            except UnicodeDecodeError as exc:
                raise GitInventoryError(f"tracked path cannot be represented losslessly in UTF-8 JSON: {_path_diagnostic(raw_path)}") from exc
            if not path or path.startswith("/") or "\\" in path or any(part in {"", ".", ".."} for part in path.split("/")):
                raise GitInventoryError(f"unsafe or non-canonical repository path: {_path_diagnostic(raw_path)}")
            content = blobs.get(oid) if object_type == "blob" and mode != "160000" else None
            result[path] = TreeEntry(path, raw_path, mode, object_type, oid, content)
        return result

    def cat_blobs(self, object_ids: Iterable[str]) -> dict[str, bytes]:
        unique = list(dict.fromkeys(object_ids))
        missing = [oid for oid in unique if oid not in self._blob_cache]
        if missing:
            if any(not re.fullmatch(r"[0-9a-f]{40,64}", oid) for oid in missing):
                raise GitInventoryError("refusing a malformed blob object id")
            output = self.run("cat-file", "--batch", input=("\n".join(missing) + "\n").encode("ascii"))
            offset = 0
            for requested in missing:
                line_end = output.find(b"\n", offset)
                if line_end < 0:
                    raise GitInventoryError("truncated cat-file batch header")
                header = output[offset:line_end].split(b" ")
                offset = line_end + 1
                if len(header) != 3 or header[0].decode("ascii", "replace") != requested or header[1] != b"blob":
                    raise GitInventoryError(f"Git object {requested} is not an available blob")
                try:
                    size = int(header[2])
                except ValueError as exc:
                    raise GitInventoryError("invalid cat-file object length") from exc
                if size < 0 or offset + size >= len(output):
                    raise GitInventoryError("truncated cat-file blob body")
                body = output[offset : offset + size]
                offset += size
                if output[offset : offset + 1] != b"\n":
                    raise GitInventoryError("cat-file batch object terminator is missing")
                offset += 1
                self._blob_cache[requested] = body
            if offset != len(output):
                raise GitInventoryError("unexpected trailing cat-file batch bytes")
        return {oid: self._blob_cache[oid] for oid in unique}

    def blob(self, oid: str) -> bytes:
        return self.cat_blobs([oid])[oid]

    def diff(self, old_revision: str, new_revision: str) -> list[RawChange]:
        old_sha, _ = self.commit_identity(old_revision)
        new_sha, _ = self.commit_identity(new_revision)
        raw = self.run("diff-tree", "--no-commit-id", "-r", "--raw", "-z", "-M", "--no-abbrev", old_sha, new_sha, "--")
        return parse_raw_changes(raw)

    def path_provenance(self, path: str, *, subject: str, style: CommentStyle | None = None,
                        handling: str = "manifest-only", file_kind: str | None = None) -> PathProvenance:
        raw_path = _encode_path(path)
        subject_sha, _ = self.commit_identity(subject)
        subject_entry = self._tree_entry_for_path(subject_sha, raw_path)
        if subject_entry is None:
            unknown = unknown_evidence(f"Path {path!r} is absent from fixed subject {subject_sha}; uncommitted or later paths have no source date in this snapshot.")
            return PathProvenance(unknown, unknown, None, False)
        subject_mode, _, subject_oid = subject_entry
        self._verify_path_blob(subject_sha, raw_path, subject_oid, subject_mode)
        log = self.run("log", "--follow", "--format=%H%x00%ct", subject_sha, "--", b":(literal)" + raw_path)
        commits = _parse_log(log)
        if not commits:
            return PathProvenance(
                unknown_evidence("No committed Git introduction or content-change history is available for this path."),
                unknown_evidence("No committed Git body-change history is available for this path."),
                None,
                False,
            )
        current_path = raw_path
        created: FileEvidence | None = None
        updated: FileEvidence | None = None
        origin_path: bytes | None = None
        saw_rename = False
        for commit, timestamp in commits:
            self._verify_commit(commit)
            parent_result = self.run("rev-list", "--parents", "-n", "1", commit).strip().split()
            if not parent_result or parent_result[0].decode("ascii") != commit:
                raise GitInventoryError(f"cannot resolve parent list for {commit}")
            if len(parent_result) > 1:
                parent = parent_result[1].decode("ascii")
                changes = parse_raw_changes(self.run("diff-tree", "--no-commit-id", "-r", "--raw", "-z", "-M", "--no-abbrev", parent, commit, "--"))
            else:
                changes = parse_raw_changes(self.run("diff-tree", "--root", "--no-commit-id", "-r", "--raw", "-z", "-M", "--no-abbrev", commit, "--"))
            change = _change_for_current_path(changes, current_path)
            if change is None:
                reason = f"Git history for {path!r} has a first-parent/rename gap at {commit}; provenance is unknown rather than inferred across it."
                unknown = unknown_evidence(reason)
                return PathProvenance(unknown, unknown, None, saw_rename)
            after_path = change.new_path
            if change.status.startswith("R"):
                saw_rename = True
            if change.status == "A":
                if after_path is None:
                    raise GitInventoryError("addition record is missing its new path")
                self._verify_path_blob(commit, after_path, change.new_oid, change.new_mode)
                evidence = _git_evidence(timestamp, commit, after_path, "git-first-introduction", "first available Git introduction")
                created = evidence if not saw_rename else _rename_evidence(evidence, after_path)
                if updated is None:
                    updated = _git_evidence(timestamp, commit, after_path, "git-last-content-change", "file introduction created its initial content")
                origin_path = after_path
                break
            if change.status == "D":
                # A deletion ends this identity; an earlier same-name file is
                # not the creation source for a later re-add.
                break
            if after_path is None:
                continue
            self._verify_path_blob(commit, after_path, change.new_oid, change.new_mode)
            # Once the newest body change is known (or explicitly unknown),
            # older commits only establish identity and creation. Parsing their
            # bodies cannot change that conclusion and can be ambiguous.
            if updated is None:
                before_bytes = (
                    change.old_oid.encode("ascii") if change.old_mode == "160000"
                    else self.blob(change.old_oid) if change.old_mode != "000000"
                    else b""
                )
                after_bytes = (
                    change.new_oid.encode("ascii") if change.new_mode == "160000"
                    else self.blob(change.new_oid)
                )
                try:
                    changed = _history_body_changed(before_bytes, after_bytes, change.old_mode, change.new_mode,
                                                    style, handling, file_kind)
                except MetadataError as exc:
                    updated = unknown_evidence(
                        f"Cannot verify the latest content change for {path!r}: metadata parsing is ambiguous "
                        f"at commit {commit} and its first-parent comparison ({exc}).")
                else:
                    if changed:
                        updated = _git_evidence(timestamp, commit, after_path, "git-last-content-change", "latest traceable body-changing commit")
            if change.status.startswith("R"):
                if change.old_path is None:
                    raise GitInventoryError("rename record is missing its old path")
                current_path = change.old_path
        if created is None:
            created = unknown_evidence("History did not provide a verifiable add event for this path identity; do not infer through a missing rename or truncated history.")
        if updated is None:
            updated = unknown_evidence("History did not provide a verifiable body-changing commit for this path.")
        if saw_rename and created.value != "unknown":
            created = FileEvidence(created.value, {**created.source, "kind": "rename-history"})
        return PathProvenance(created, updated, _decode_path(origin_path) if origin_path else None, saw_rename)

    def _tree_entry_for_path(self, commit: str, raw_path: bytes) -> tuple[str, str, str] | None:
        raw = self.run("ls-tree", "-r", "-z", "--full-tree", commit, "--", b":(literal)" + raw_path)
        records = _nul_records(raw, "ls-tree exact path")
        matches: list[tuple[str, str, str]] = []
        for record in records:
            metadata, sep, found_path = record.partition(b"\t")
            if not sep or found_path != raw_path:
                raise GitInventoryError("fixed-subject path lookup returned a different or malformed path")
            try:
                mode, object_type, oid = metadata.decode("ascii").split(" ")
            except (UnicodeDecodeError, ValueError) as exc:
                raise GitInventoryError("malformed fixed-subject tree entry") from exc
            _validate_object_fields(mode, object_type, oid)
            matches.append((mode, object_type, oid))
        if len(matches) > 1:
            raise GitInventoryError(f"fixed subject contains duplicate path records: {_path_diagnostic(raw_path)}")
        return matches[0] if matches else None

    def _verify_commit(self, sha: str) -> None:
        kind = self.run("cat-file", "-t", sha).strip()
        if kind != b"commit":
            raise GitInventoryError(f"history source {sha} is not a commit object")

    def _verify_path_blob(self, commit: str, path: bytes, oid: str, mode: str) -> None:
        if not SHA_RE.fullmatch(oid.encode("ascii")):
            raise GitInventoryError("history record contains invalid object id")
        resolved = self.run("rev-parse", "--verify", commit.encode("ascii") + b":" + path).strip()
        if resolved != oid.encode("ascii"):
            raise GitInventoryError(f"history source path/blob mismatch: {commit}:{_path_diagnostic(path)}")
        if mode == "160000":
            # The parent Git tree stores only a submodule commit pointer; the
            # referenced object may intentionally exist only in the submodule.
            return
        kind = self.run("cat-file", "-t", oid).strip()
        if kind != b"blob":
            raise GitInventoryError(f"history path source {oid} is not a blob")


def parse_raw_changes(raw: bytes) -> list[RawChange]:
    tokens = _nul_records(raw, "raw diff")
    out: list[RawChange] = []
    index = 0
    while index < len(tokens):
        meta = tokens[index]
        index += 1
        if not meta.startswith(b":"):
            raise GitInventoryError("malformed raw diff header")
        fields = meta[1:].split(b" ")
        if len(fields) != 5:
            raise GitInventoryError("malformed raw diff fields")
        try:
            old_mode, new_mode = fields[0].decode("ascii"), fields[1].decode("ascii")
            old_oid, new_oid = fields[2].decode("ascii"), fields[3].decode("ascii")
            status = fields[4].decode("ascii")
        except UnicodeDecodeError as exc:
            raise GitInventoryError("raw diff metadata is not ASCII") from exc
        if not status or status[0] not in "ACDMRTUXB":
            raise GitInventoryError(f"unsupported raw diff status {status!r}")
        path_count = 2 if status[0] in "RC" else 1
        if index + path_count > len(tokens):
            raise GitInventoryError("truncated raw diff path record")
        paths = tokens[index : index + path_count]
        index += path_count
        if path_count == 2:
            old_path, new_path = paths
        elif status[0] == "A":
            old_path, new_path = None, paths[0]
        elif status[0] == "D":
            old_path, new_path = paths[0], None
        else:
            old_path = new_path = paths[0]
        _validate_diff_oid(old_oid)
        _validate_diff_oid(new_oid)
        out.append(RawChange(status, old_mode, new_mode, old_oid, new_oid, old_path, new_path))
    return out


def compare_manifest_coverage(tracked_paths: Iterable[str], manifest_paths: Iterable[str]) -> tuple[list[str], list[str]]:
    tracked, records = set(tracked_paths), set(manifest_paths)
    if not tracked:
        raise GitInventoryError("required tracked-file coverage scope is empty")
    if not records:
        raise GitInventoryError("manifest path set is empty")
    missing = sorted(tracked - records)
    stale = sorted(records - tracked)
    return missing, stale


def revision_for_window(previous: str, before: TreeEntry | None, after: TreeEntry, *,
                        style: CommentStyle, handling: str, file_kind: str,
                        identity_relation: str = "same") -> str:
    if before is None or before.content is None and before.mode != "160000":
        raise GitInventoryError("unknown prior body snapshot cannot prove unchanged revision; review or fail closed")
    if after.content is None and after.mode != "160000":
        raise GitInventoryError("candidate body snapshot is unavailable")
    if identity_relation not in {"same", "verified-rename"}:
        raise GitInventoryError(f"unsupported file identity relation {identity_relation!r}")
    if before.path != after.path and identity_relation != "verified-rename":
        raise GitInventoryError("path identity changed without verified rename lineage; use r1 for a new path")
    match = REVISION_RE.fullmatch(previous)
    if not match:
        raise GitInventoryError(f"invalid file revision {previous!r}")
    before_bytes = before.content if before.content is not None else before.oid.encode("ascii")
    after_bytes = after.content if after.content is not None else after.oid.encode("ascii")
    if content_changed(before_bytes, after_bytes, mode_before=before.mode, mode_after=after.mode,
                       style=style, handling=handling, file_kind=file_kind):
        return f"r{int(match.group(1)) + 1}"
    return previous


def new_path_revision() -> str:
    return "r1"


def manifest_self_updated(path: str = MANIFEST_SELF_PATH) -> FileEvidence:
    if path != MANIFEST_SELF_PATH:
        raise GitInventoryError("the unknown self-reference exception is only valid for the canonical manifest path")
    return unknown_evidence("The manifest cannot embed the commit/blob identity of its own current update; retain this exact-path self-reference exception.")


def unknown_evidence(reason: str) -> FileEvidence:
    return FileEvidence("unknown", {"kind": "unknown", "reason": reason})


def _history_body_changed(before: bytes, after: bytes, old_mode: str, new_mode: str,
                          style: CommentStyle | None, handling: str,
                          file_kind: str | None) -> bool:
    if style is None or file_kind is None or old_mode not in {"100644", "100755"} or new_mode not in {"100644", "100755"}:
        return before != after
    old_body = metadata_body(before, style, handling, file_kind=file_kind, mode=old_mode)
    new_body = metadata_body(after, style, handling, file_kind=file_kind, mode=new_mode)
    return old_body != new_body


def _change_for_current_path(changes: list[RawChange], path: bytes) -> RawChange | None:
    matches = [change for change in changes if change.new_path == path or change.old_path == path]
    if len(matches) > 1:
        raise GitInventoryError(f"ambiguous same-commit path identity: {_path_diagnostic(path)}")
    return matches[0] if matches else None


def _parse_log(raw: bytes) -> list[tuple[str, int]]:
    commits: list[tuple[str, int]] = []
    for line in raw.splitlines():
        if not line:
            continue
        fields = line.split(b"\x00")
        if len(fields) != 2 or not SHA_RE.fullmatch(fields[0]):
            raise GitInventoryError("malformed NUL-delimited Git log record")
        try:
            commits.append((fields[0].decode("ascii"), int(fields[1])))
        except (UnicodeDecodeError, ValueError) as exc:
            raise GitInventoryError("invalid Git log commit/timestamp") from exc
    return commits


def _git_evidence(timestamp: int, commit: str, path: bytes, kind: str, reason: str) -> FileEvidence:
    try:
        date = datetime.fromtimestamp(timestamp, timezone.utc).date().isoformat()
    except (OverflowError, OSError, ValueError):
        return unknown_evidence(f"Commit {commit} has an invalid committer timestamp; no date was invented.")
    return FileEvidence(date, {
        "kind": kind,
        "commit": commit,
        "path": _decode_path(path),
        "reason": reason + "; date is the Git committer timestamp converted to a UTC calendar date, not an asserted authoring date.",
    })


def _rename_evidence(evidence: FileEvidence, path: bytes) -> FileEvidence:
    return FileEvidence(evidence.value, {**evidence.source, "kind": "rename-history", "path": _decode_path(path)})


def _validate_object_fields(mode: str, object_type: str, oid: str) -> None:
    if mode not in {"100644", "100755", "120000", "160000", "040000"}:
        raise GitInventoryError(f"unsupported Git tree mode {mode!r}")
    if mode == "160000" and object_type != "commit":
        raise GitInventoryError("gitlink mode must point to a commit object")
    if mode != "160000" and object_type != "blob":
        raise GitInventoryError(f"unexpected Git object type {object_type!r} for mode {mode}")
    if not re.fullmatch(r"[0-9a-f]{40,64}", oid):
        raise GitInventoryError("invalid Git object id in tree record")


def _validate_diff_oid(oid: str) -> None:
    if oid == "0" * len(oid):
        return
    if not re.fullmatch(r"[0-9a-f]{40,64}", oid):
        raise GitInventoryError("invalid Git object id in raw diff record")


def _nul_records(raw: bytes, source: str) -> list[bytes]:
    if raw and not raw.endswith(b"\x00"):
        raise GitInventoryError(f"{source} output is not NUL terminated")
    return raw[:-1].split(b"\x00") if raw else []


def _encode_path(path: str) -> bytes:
    if not isinstance(path, str) or not path or "\x00" in path or path.startswith("/") or "\\" in path:
        raise GitInventoryError("path must be a non-empty repository-relative POSIX string")
    if any(part in {"", ".", ".."} for part in path.split("/")):
        raise GitInventoryError("path contains an unsafe component")
    try:
        return path.encode("utf-8", "strict")
    except UnicodeEncodeError as exc:
        raise GitInventoryError("path cannot be encoded losslessly as UTF-8") from exc


def _decode_path(raw: bytes) -> str:
    try:
        return raw.decode("utf-8", "strict")
    except UnicodeDecodeError as exc:
        raise GitInventoryError(f"Git path cannot be represented losslessly in UTF-8 JSON: {_path_diagnostic(raw)}") from exc


def _path_diagnostic(raw: bytes) -> str:
    return "bytes[" + raw.hex() + "]"

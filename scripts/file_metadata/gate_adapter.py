"""Fixed-base FILE metadata verification and explicit header application.

The fast gate loads its verifier from the actual target BASE. It trusts the
BASE's canonical manifest Git blob and treats uncommitted source
body changes as non-verifiable, never as a metadata-only PASS.
"""
from __future__ import annotations

import argparse
from dataclasses import asdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile
from typing import Any

from git_inventory import GitRepo, GitInventoryError, TreeEntry, parse_raw_changes, compare_manifest_coverage
import manifest_tool as mt
import legacy_bootstrap as lb
from metadata_core import MetadataError, insert_header, metadata_body, parse_header, render_fields

LEGACY_POLICY_STATUS = "populated-from-issue-115-baseline"
INITIALIZED_POLICY_STATUS = "initialized-from-reviewed-legacy-bootstrap"
BOOTSTRAP_PERMISSION = {"version": 1, "legacy_baseline": "561c6c260622d8d817eae9b877299fda206b85b7", "anchor": "77d516c40142b049cea7957d0010e938d4f9d515", "mode": "exact-reviewed-transition"}
LEGACY_INVENTORY_BASELINE = "561c6c260622d8d817eae9b877299fda206b85b7"
LEGACY_CANONICAL_ANCHOR = "77d516c40142b049cea7957d0010e938d4f9d515"
LEGACY_BASE_TRACKED_COUNT = 1205
LEGACY_BASE_TREE_OID = "ada406808f284441458c1589783f41c1ce3f9b63"
LEGACY_ANCHOR_TREE_OID = "bc2fce38148b788a9689e8c9a4aaf39ff012c8ed"
LEGACY_ANCHOR_MANIFEST_OID = "23b3d04ffed263985acbd8c6af200cab48d538c8"
MANIFEST_PATH = "scripts/file_metadata/manifest.json"
SCHEMA_PATH = "scripts/file_metadata/schema.json"
POLICY_PATH = "scripts/file_metadata/policy.json"
LEGACY_EXPLICIT_ADDITIONS = {
    "docs/engineering/file-metadata.md", MANIFEST_PATH, POLICY_PATH, SCHEMA_PATH,
}
LEGACY_EXPLICIT_ADDITIONS_ORDER = [
    "docs/engineering/file-metadata.md", SCHEMA_PATH, POLICY_PATH, MANIFEST_PATH,
]
FOUNDATION_TOOL_PATHS = {
    "scripts/check_fast.py",
    "scripts/file_metadata/metadata_core.py",
    "scripts/file_metadata/git_inventory.py",
    "scripts/file_metadata/manifest_tool.py",
    "scripts/file_metadata/legacy_bootstrap.py",
    "scripts/file_metadata/gate_adapter.py",
}
SUPPORTED_HEADER_STATES = {
    "pending-header-application", "manifest-only", "upstream-notice-preserved", "generator-owned-output",
}
SUPPORTED_HANDLING = {
    "header-and-manifest", "manifest-only", "preserve-notice-and-manifest", "generated-manifest-only",
}


class GateError(RuntimeError):
    pass


def _json_bytes(raw: bytes, label: str) -> Any:
    try:
        return json.loads(raw.decode("utf-8", "strict"), object_pairs_hook=mt._pairs)
    except (UnicodeError, json.JSONDecodeError, mt.ManifestError) as exc:
        raise GateError(f"{label}: invalid strict JSON: {exc}") from exc


def _base_blob(repo: GitRepo, commit: str, path: str) -> tuple[bytes, str]:
    entry = repo._tree_entry_for_path(commit, path.encode("utf-8"))
    if entry is None:
        raise GateError(f"trusted BASE lacks required {path}")
    mode, kind, oid = entry
    if kind != "blob" or mode not in {"100644", "100755"}:
        raise GateError(f"trusted BASE {path} is not an ordinary Git file")
    repo._verify_path_blob(commit, path.encode("utf-8"), oid, mode)
    return repo.blob(oid), oid


def _fixed_sha(repo: GitRepo, ref: str, label: str) -> tuple[str, str]:
    if not isinstance(ref, str) or not re.fullmatch(r"[0-9a-f]{40,64}", ref):
        raise GateError(f"{label} must be an explicit full lowercase Git commit SHA")
    sha, tree = repo.commit_identity(ref)
    if sha != ref:
        raise GateError(f"{label} did not resolve to the supplied canonical commit SHA")
    return sha, tree


def _working_snapshot(repo: GitRepo, subject: str) -> tuple[dict[str, TreeEntry], dict[str, str]]:
    """Overlay index/worktree and untracked files on fixed HEAD without losing modes."""
    subject_sha, _ = repo.commit_identity(subject)
    subject_tree = repo.tree_inventory(subject_sha)
    index = repo.index_inventory()
    # The index is the tracked candidate set. Later filesystem overlay captures
    # unstaged edits/deletions and untracked files.
    snapshot = dict(index)
    raw_untracked = repo.run("ls-files", "--others", "--exclude-standard", "-z")
    untracked: list[bytes] = []
    if raw_untracked:
        if not raw_untracked.endswith(b"\x00"):
            raise GateError("Git returned a truncated NUL-delimited untracked path list")
        untracked = raw_untracked[:-1].split(b"\x00")
    paths = set(snapshot) | set(subject_tree)
    for raw in untracked:
        try:
            path = raw.decode("utf-8", "strict")
        except UnicodeDecodeError as exc:
            raise GateError(f"untracked path cannot be represented losslessly in the manifest: {raw.hex()}") from exc
        paths.add(path)
    dirty: dict[str, str] = {}
    for path in sorted(paths):
        full = repo.repo / path
        try:
            mode_info = full.lstat()
        except FileNotFoundError:
            snapshot.pop(path, None)
            dirty[path] = "deleted"
            continue
        raw_path = path.encode("utf-8", "strict")
        if stat.S_ISLNK(mode_info.st_mode):
            data = os.fsencode(os.readlink(full))
            mode, kind = "120000", "blob"
        elif stat.S_ISREG(mode_info.st_mode):
            data = full.read_bytes()
            mode, kind = ("100755" if mode_info.st_mode & 0o111 else "100644"), "blob"
        elif stat.S_ISDIR(mode_info.st_mode):
            # A gitlink's checked-out directory is not the object; retain its index entry.
            old = snapshot.get(path)
            if old is not None and old.mode == "160000":
                continue
            raise GateError(f"tracked path became a directory without a gitlink entry: {path}")
        else:
            raise GateError(f"unsupported worktree filesystem type: {path}")
        oid = repo.run("hash-object", "--stdin", input=data).decode("ascii").strip()
        from git_inventory import TreeEntry as Entry
        snapshot[path] = Entry(path, raw_path, mode, kind, oid, data)
        original = subject_tree.get(path)
        if original is None:
            dirty[path] = "untracked-addition"
        elif original.mode != mode or original.oid != oid:
            dirty[path] = "worktree-or-index-change"
    return snapshot, dirty


def _load_current_file(repo_root: Path, relative: str) -> bytes:
    supplied = Path(relative)
    if supplied.is_absolute():
        try:
            relative = Path(os.path.abspath(supplied)).relative_to(repo_root.resolve()).as_posix()
        except ValueError as exc:
            raise GateError(f"candidate path escapes repository root: {relative}") from exc
    p = _safe_target(repo_root, relative)
    if p.is_symlink() or not p.is_file():
        raise GateError(f"candidate {relative} is missing or not a regular file")
    return p.read_bytes()


def _safe_target(repo_root: Path, relative: str) -> Path:
    """Resolve a repo-relative file without following any symlink component."""
    rel = Path(relative)
    if rel.is_absolute() or not rel.parts or any(part in {"", ".", ".."} for part in rel.parts):
        raise GateError(f"unsafe repository-relative path: {relative!r}")
    current = repo_root.resolve()
    for part in rel.parts[:-1]:
        current = current / part
        if current.is_symlink() or not current.is_dir():
            raise GateError(f"{relative}: parent path is missing, non-directory, or symlinked")
    target = current / rel.parts[-1]
    if target.is_symlink():
        raise GateError(f"{relative}: symlink targets are never modified")
    try:
        target.resolve(strict=False).relative_to(repo_root.resolve())
    except ValueError as exc:
        raise GateError(f"{relative}: resolved target escapes repository root") from exc
    return target


def _repo_argument_path(repo: Path, supplied: Path) -> Path:
    if supplied.is_absolute():
        lexical = Path(os.path.abspath(supplied))
        try:
            lexical.relative_to(repo.resolve())
        except ValueError as exc:
            raise GateError(f"path argument escapes repository: {supplied}") from exc
        return lexical
    return _safe_target(repo, supplied.as_posix())


def _state(repo: GitRepo, policy: dict, manifest: dict, base_sha: str,
           base_tree: dict[str, TreeEntry]) -> tuple[str, dict[str, list[str]]]:
    status = policy.get("manifest_status")
    internal = manifest.get("inventory", {}).get("baseline_head")
    # The reviewed legacy state is identified by both its original policy
    # marker and fixed historical inventory baseline. The reviewed bootstrap
    # moves that inventory anchor while retaining descriptive policy history.
    if status == LEGACY_POLICY_STATUS and internal == LEGACY_INVENTORY_BASELINE:
        if not manifest.get("files") or not manifest.get("inventory"):
            raise GateError("legacy BASE is empty or lacks inventory; refusing transition mode")
        inventory = manifest["inventory"]
        if inventory.get("explicit_additions") != LEGACY_EXPLICIT_ADDITIONS_ORDER:
            raise GateError("legacy BASE explicit_additions differs from the reviewed historical set")
        anchor_sha, _ = _fixed_sha(repo, LEGACY_CANONICAL_ANCHOR, "reviewed canonical legacy anchor")
        try:
            repo.run("merge-base", "--is-ancestor", anchor_sha, base_sha)
        except GitInventoryError as exc:
            raise GateError("reviewed canonical legacy anchor is not an ancestor of trusted BASE") from exc
        anchor_tree = repo.tree_inventory(anchor_sha)
        _, actual_anchor_tree_oid = repo.commit_identity(anchor_sha)
        if actual_anchor_tree_oid != LEGACY_ANCHOR_TREE_OID:
            raise GateError("reviewed canonical legacy anchor tree differs from its audited bridge attestation")
        if inventory.get("tracked_file_count") != LEGACY_BASE_TRACKED_COUNT:
            raise GateError("legacy manifest tracked_file_count differs from the reviewed 561c bridge evidence")
        bridge_evidence = "reviewed-anchor-attestation"
        # Validate the original bridge directly when its pre-squash commit is
        # available, but never require CI to retain that off-main object.
        try:
            legacy_sha, legacy_tree_oid = repo.commit_identity(internal)
        except GitInventoryError:
            legacy_sha = None
        else:
            if legacy_tree_oid != LEGACY_BASE_TREE_OID:
                raise GateError("available legacy commit tree differs from the reviewed 561c bridge evidence")
            try:
                lb.verify_bridge_trees(repo.tree_inventory(legacy_sha), anchor_tree)
            except lb.BootstrapError as exc:
                raise GateError(f"available legacy commit fails the reviewed exact bridge proof: {exc}") from exc
            bridge_evidence = "legacy-object-and-reviewed-anchor-verified"
        if LEGACY_BASE_TRACKED_COUNT + len(LEGACY_EXPLICIT_ADDITIONS) != len(anchor_tree):
            raise GateError("reviewed bridge attestation does not reconcile legacy count and four additions to anchor tree")
        anchor_manifest_raw, anchor_manifest_oid = _base_blob(repo, anchor_sha, MANIFEST_PATH)
        if anchor_manifest_oid != LEGACY_ANCHOR_MANIFEST_OID:
            raise GateError("reviewed canonical legacy anchor manifest blob differs from its audited identity")
        anchor_schema_raw, _ = _base_blob(repo, anchor_sha, SCHEMA_PATH)
        anchor_policy_raw, _ = _base_blob(repo, anchor_sha, POLICY_PATH)
        base_manifest_raw, _ = _base_blob(repo, base_sha, MANIFEST_PATH)
        if anchor_manifest_raw != base_manifest_raw:
            raise GateError("trusted BASE legacy manifest differs from the reviewed anchor canonical blob")
        anchor_manifest = _json_bytes(anchor_manifest_raw, "reviewed anchor manifest")
        anchor_schema = _json_bytes(anchor_schema_raw, "reviewed anchor schema")
        anchor_policy = _json_bytes(anchor_policy_raw, "reviewed anchor policy")
        mt.validate_schema(anchor_manifest, anchor_schema)
        if anchor_manifest.get("inventory", {}).get("baseline_head") != LEGACY_INVENTORY_BASELINE:
            raise GateError("reviewed anchor manifest does not identify the fixed historical legacy baseline")
        if anchor_manifest.get("inventory", {}).get("tracked_file_count") != LEGACY_BASE_TRACKED_COUNT:
            raise GateError("reviewed anchor manifest has an unexpected legacy tracked-file count")
        if anchor_manifest.get("inventory", {}).get("explicit_additions") != LEGACY_EXPLICIT_ADDITIONS_ORDER:
            raise GateError("reviewed anchor manifest does not retain the exact audited four additions")
        anchor_missing, anchor_stale = compare_manifest_coverage(anchor_tree, anchor_manifest.get("files", {}))
        if anchor_missing or anchor_stale:
            raise GateError(f"reviewed anchor canonical manifest coverage mismatch: missing={anchor_missing[:8]}, stale={anchor_stale[:8]}")
        _validate_foundation_delta(anchor_policy, policy, anchor_schema, _json_bytes(_base_blob(repo, base_sha, SCHEMA_PATH)[0], "BASE schema"))
        base_missing, base_stale = compare_manifest_coverage(base_tree, manifest["files"])
        # The old registry is intentionally not today's full tree. Record the
        # exact drift for NOT_RUN; do not reinterpret it as passing coverage.
        renames = [f"{c.old_path.decode('utf-8', 'strict')}->{c.new_path.decode('utf-8', 'strict')}"
                   for c in repo.diff(anchor_sha, base_sha)
                   if c.status.startswith("R") and c.old_path and c.new_path]
        return "legacy-uninitialized", {"base_missing": base_missing, "base_stale": base_stale,
                                         "historical_renames": renames,
                                         "bridge_evidence": bridge_evidence}
    if status not in {LEGACY_POLICY_STATUS, INITIALIZED_POLICY_STATUS}:
        raise GateError(f"BASE manifest_status is not a recognized initialized state: {status!r}")
    missing, extra = compare_manifest_coverage(base_tree, manifest.get("files", {}))
    if missing or extra:
        raise GateError(f"initialized BASE canonical manifest coverage mismatch: missing={missing[:5]}, stale={extra[:5]}")
    return "initialized", {"base_missing": [], "base_stale": [], "historical_renames": []}


def _validate_foundation_delta(base_policy: dict, candidate_policy: dict,
                               base_schema: dict, candidate_schema: dict) -> None:
    """Permit additive policy classes and narrowly additive state enums only."""
    if base_policy.get("manifest_status") != candidate_policy.get("manifest_status"):
        raise GateError("foundation cannot change the trusted legacy manifest_status")
    base_rest = {k: v for k, v in base_policy.items() if k not in {"file_classes", "reviewed_bootstrap"}}
    cand_rest = {k: v for k, v in candidate_policy.items() if k not in {"file_classes", "reviewed_bootstrap"}}
    if candidate_policy.get("reviewed_bootstrap") not in (None, BOOTSTRAP_PERMISSION) or base_policy.get("reviewed_bootstrap") not in (None, BOOTSTRAP_PERMISSION):
        raise GateError("bootstrap permission must match the exact reviewed transition")
    if base_policy.get("reviewed_bootstrap") is not None and candidate_policy.get("reviewed_bootstrap") != base_policy.get("reviewed_bootstrap"):
        raise GateError("foundation cannot remove reviewed bootstrap permission")
    if base_rest != cand_rest:
        raise GateError("foundation policy changed a protected field outside additive file classes")
    base_classes = base_policy.get("file_classes")
    cand_classes = candidate_policy.get("file_classes")
    if not isinstance(base_classes, list) or not isinstance(cand_classes, list):
        raise GateError("foundation policy file_classes must remain arrays")
    old_by_id = {x.get("id"): x for x in base_classes if isinstance(x, dict)}
    new_by_id = {x.get("id"): x for x in cand_classes if isinstance(x, dict)}
    if len(old_by_id) != len(base_classes) or len(new_by_id) != len(cand_classes):
        raise GateError("foundation policy file class IDs must be unique objects")
    if any(new_by_id.get(key) != value for key, value in old_by_id.items()):
        raise GateError("foundation cannot remove or weaken an existing file class")
    if not set(old_by_id) <= set(new_by_id):
        raise GateError("foundation cannot remove an existing file class")
    for class_id, rule in new_by_id.items():
        if class_id in old_by_id:
            continue
        paths = rule.get("paths")
        if rule == lb.GLOSSARY_VIEWS_RULE:
            continue
        if (not isinstance(paths, list) or not paths or "patterns" in rule or
                not set(paths) <= FOUNDATION_TOOL_PATHS or
                rule.get("handling") not in SUPPORTED_HANDLING):
            raise GateError("foundation may add only literal classes for the fixed new tool paths")

    # Schema foundation may append recognized state values only; all other
    # validation structure and existing enum members stay byte-semantically fixed.
    import copy
    base_copy = copy.deepcopy(base_schema)
    candidate_copy = copy.deepcopy(candidate_schema)
    try:
        old_states = base_copy["$defs"]["record"]["properties"]["header_state"]["enum"]
        new_states = candidate_copy["$defs"]["record"]["properties"]["header_state"]["enum"]
        old_handling = base_copy["$defs"]["record"]["properties"]["handling"]["enum"]
        new_handling = candidate_copy["$defs"]["record"]["properties"]["handling"]["enum"]
    except (KeyError, TypeError) as exc:
        raise GateError("foundation schema lacks the reviewed record enum structure") from exc
    if not isinstance(old_states, list) or not isinstance(new_states, list) or not set(old_states) <= set(new_states):
        raise GateError("foundation may only append header_state enum values")
    if not isinstance(old_handling, list) or not isinstance(new_handling, list) or not set(old_handling) <= set(new_handling):
        raise GateError("foundation may only append handling enum values")
    if len(set(new_states)) != len(new_states) or len(set(new_handling)) != len(new_handling):
        raise GateError("foundation schema enum values must remain unique")
    if not set(new_states) <= SUPPORTED_HEADER_STATES or not set(new_handling) <= SUPPORTED_HANDLING:
        raise GateError("foundation schema enum values must use the fixed supported metadata vocabulary")
    candidate_copy["$defs"]["record"]["properties"]["header_state"]["enum"] = old_states
    candidate_copy["$defs"]["record"]["properties"]["handling"]["enum"] = old_handling
    if candidate_copy != base_copy:
        raise GateError("foundation schema changed constraints outside additive reviewed enum values")


def _exact_record_header(path: str, entry: TreeEntry, record: dict, rule: dict) -> bool:
    kind, style, _ = mt._native(rule, path)
    if rule["handling"] != "header-and-manifest":
        return False
    if entry.content is None or entry.mode not in {"100644", "100755"}:
        raise GateError(f"{path}: header-bearing record is not an ordinary readable file")
    try:
        body = metadata_body(entry.content, style, rule["handling"], file_kind=kind, mode=entry.mode)
        parsed = parse_header(entry.content, style)
    except MetadataError as exc:
        raise GateError(f"{path}: unsafe or malformed metadata mirror: {exc}") from exc
    if parsed is None:
        raise GateError(f"{path}: initialized metadata gate requires the approved header mirror")
    if dict(parsed.fields) != render_fields(record):
        raise GateError(f"{path}: header mirror differs from canonical record")
    return bool(parsed)


def _entry_body(entry: TreeEntry, rule: dict, path: str) -> bytes | None:
    if entry.content is None:
        return None
    kind, style, _ = mt._native(rule, path)
    if style is None:
        return entry.content
    try:
        return metadata_body(entry.content, style, rule["handling"], file_kind=kind, mode=entry.mode)
    except MetadataError as exc:
        raise GateError(f"{path}: body normalization failed closed: {exc}") from exc


def _renames(repo: GitRepo, base_sha: str, subject_sha: str) -> dict[str, str]:
    return {change.new_path.decode("utf-8", "strict"): change.old_path.decode("utf-8", "strict")
            for change in repo.diff(base_sha, subject_sha)
            if change.status.startswith("R") and change.old_path and change.new_path}


def _window_identity_origin(repo: GitRepo, base_sha: str, subject_sha: str,
                            candidate_path: str, *, base_on_first_parent: bool) -> str | None:
    """Walk NUL-parsed first-parent changes backward to the BASE identity.

    A deletion or a new add after BASE ends identity, even when the net tree
    diff later reports a rename from the old spelling to a different path.
    Merge windows whose BASE is reachable only through another parent are
    deliberately unsupported for inheritance.
    """
    if not base_on_first_parent:
        raise GateError("unsupported identity lineage: target BASE is not on candidate SUBJECT first-parent chain")
    current = candidate_path.encode("utf-8", "strict")
    commits = repo.run("rev-list", "--first-parent", f"{base_sha}..{subject_sha}").splitlines()
    for raw_commit in commits:
        commit = raw_commit.decode("ascii")
        parents = repo.run("rev-list", "--parents", "-n", "1", commit).split()
        if len(parents) < 2:
            changes = parse_raw_changes(repo.run("diff-tree", "--root", "--no-commit-id", "-r", "--raw", "-z", "-M", "--no-abbrev", commit, "--"))
        else:
            changes = repo.diff(parents[1].decode("ascii"), commit)
        change = next((item for item in changes if item.new_path == current or
                       item.old_path == current), None)
        if change is None:
            continue
        if change.status == "A" or change.status == "D":
            return None
        if change.status.startswith("R"):
            if change.new_path != current or change.old_path is None:
                return None
            current = change.old_path
    return current.decode("utf-8", "strict")


def _verify_new_evidence(repo: GitRepo, path: str, evidence: dict, actual, *, subject_sha: str,
                         rule: dict) -> None:
    if evidence != {"value": actual.value, "source": actual.source}:
        raise GateError(f"{path}: new or changed provenance differs from fixed candidate Git history")


def _validate_bootstrap_transition(repo: GitRepo, base_sha: str, subject_sha: str,
                                   base_policy: dict, base_schema_raw: bytes,
                                   candidate: dict, candidate_policy: dict,
                                   tree: dict[str, TreeEntry], *, require_headers: bool) -> dict[str, Any]:
    if base_policy.get("reviewed_bootstrap") != BOOTSTRAP_PERMISSION:
        raise GateError("trusted BASE does not authorize the reviewed bootstrap transition")
    if candidate_policy.get("manifest_status") != INITIALIZED_POLICY_STATUS:
        raise GateError("bootstrap candidate must explicitly select the initialized policy state")
    if base_sha.encode("ascii") not in set(repo.run("rev-list", "--first-parent", subject_sha).splitlines()):
        raise GateError("bootstrap BASE must be on SUBJECT's first-parent chain")
    base_tree = repo.tree_inventory(base_sha)
    trusted_manifest = _json_bytes(_base_blob(repo, base_sha, MANIFEST_PATH)[0], "trusted BASE manifest")
    if _state(repo, base_policy, trusted_manifest, base_sha, base_tree)[0] != "legacy-uninitialized":
        raise GateError("bootstrap requires the reviewed legacy BASE")
    if set(tree) != set(base_tree):
        raise GateError("bootstrap cannot add or delete tracked paths; initialize the exact trusted BASE tree")
    for path in FOUNDATION_TOOL_PATHS:
        before, after = base_tree.get(path), tree.get(path)
        if before is None or after is None or before.mode != after.mode or before.object_type != after.object_type:
            raise GateError(f"{path}: bootstrap trust foundation is missing or has a changed mode")
        rule = mt.Policy(candidate_policy).classify(path)
        if rule is None or _entry_body(before, rule, path) != _entry_body(after, rule, path):
            raise GateError(f"{path}: bootstrap cannot change the trusted tool body")
    anchor_tree = repo.tree_inventory(LEGACY_CANONICAL_ANCHOR)
    renames = _renames(repo, LEGACY_CANONICAL_ANCHOR, base_sha)
    needed = sorted(set(base_tree) - set(anchor_tree) - set(renames))
    declarations = []
    for path in needed:
        record = candidate.get("files", {}).get(path)
        entry = base_tree[path]
        if not isinstance(record, dict): raise GateError(f"{path}: reviewed record missing")
        declarations.append({"path":path, "mode":entry.mode, "oid":entry.oid,
                             "purpose":record.get("purpose"), "authors":record.get("authors"),
                             "copyright":record.get("copyright")})
    expected, audit, fixtures = lb.bootstrap(repo.repo, LEGACY_INVENTORY_BASELINE,
        LEGACY_CANONICAL_ANCHOR, base_sha, {"subject_sha":base_sha, "purpose_additions":declarations})
    if audit["report"]["status"] != "PASS":
        raise GateError(f"reviewed bootstrap reconstruction failed: {audit['report']['failures'][:5]}")
    expected_policy = fixtures["policy"]
    expected_policy["manifest_status"] = INITIALIZED_POLICY_STATUS
    if candidate_policy != expected_policy:
        raise GateError("bootstrap policy differs from the deterministic exact-path catalog transition")
    if candidate != expected:
        raise GateError("bootstrap manifest differs from the reviewed fixed-base reconstruction")
    if _load_current_file(repo.repo, SCHEMA_PATH) != base_schema_raw:
        raise GateError("bootstrap cannot change schema bytes")
    policy = mt.Policy(candidate_policy)
    mt.validate_schema(candidate, _json_bytes(base_schema_raw, "trusted BASE schema"))
    for path, before in base_tree.items():
        after = tree[path]
        if before.mode != after.mode or before.object_type != after.object_type:
            raise GateError(f"{path}: bootstrap cannot change object type or Git mode")
        if path in {MANIFEST_PATH, POLICY_PATH}: continue
        if before.mode not in {"100644", "100755"} and before.oid != after.oid:
            raise GateError(f"{path}: bootstrap changed a protected non-regular Git object")
        rule = policy.classify(path)
        if rule is None: raise GateError(f"{path}: bootstrap has no exact policy classification")
        if _entry_body(before, rule, path) != _entry_body(after, rule, path):
            raise GateError(f"{path}: bootstrap changed content outside approved metadata")
        if require_headers: _exact_record_header(path, after, candidate["files"][path], rule)
        mt.validate_record(path, candidate["files"][path], policy)
    return {"status":"PASS", "mode":"reviewed-bootstrap", "base":base_sha,
            "subject":subject_sha, "candidate_path_count":len(tree),
            "full_metadata_enforcement":"PASS", "bootstrap_audit":audit["report"]}

def check(repo_path: Path, base_ref: str, subject_ref: str, manifest_path: Path,
          schema_path: Path, policy_path: Path) -> dict[str, Any]:
    repo = GitRepo(repo_path)
    base_sha, _ = _fixed_sha(repo, base_ref, "target BASE")
    subject_sha, subject_tree_oid = _fixed_sha(repo, subject_ref, "candidate SUBJECT")
    try:
        repo.run("merge-base", "--is-ancestor", base_sha, subject_sha)
    except GitInventoryError as exc:
        raise GateError(f"target BASE {base_sha} is not an ancestor of candidate SUBJECT {subject_sha}") from exc

    base_manifest_raw, base_manifest_oid = _base_blob(repo, base_sha, MANIFEST_PATH)
    base_schema_raw, base_schema_oid = _base_blob(repo, base_sha, SCHEMA_PATH)
    base_policy_raw, base_policy_oid = _base_blob(repo, base_sha, POLICY_PATH)
    base_manifest = _json_bytes(base_manifest_raw, "BASE canonical manifest")
    base_schema = _json_bytes(base_schema_raw, "BASE schema")
    base_policy = _json_bytes(base_policy_raw, "BASE policy")
    mt.validate_schema(base_manifest, base_schema)
    base_tree = repo.tree_inventory(base_sha)
    base_state, legacy_drift = _state(repo, base_policy, base_manifest, base_sha, base_tree)

    supplied_paths = {
        "manifest": (manifest_path, MANIFEST_PATH),
        "schema": (schema_path, SCHEMA_PATH),
        "policy": (policy_path, POLICY_PATH),
    }
    for label, (supplied, canonical) in supplied_paths.items():
        if _repo_argument_path(repo.repo, supplied) != (repo.repo / canonical).resolve():
            raise GateError(f"candidate {label} path must be canonical: {canonical}")

    current_schema_raw = _load_current_file(repo.repo, str(schema_path))
    current_policy_raw = _load_current_file(repo.repo, str(policy_path))
    candidate_schema = _json_bytes(current_schema_raw, "candidate schema")
    candidate_policy = _json_bytes(current_policy_raw, "candidate policy")
    candidate_raw = _load_current_file(repo.repo, str(manifest_path))
    candidate = _json_bytes(candidate_raw, "candidate manifest")
    mt.validate_schema(candidate, candidate_schema)
    policy_obj = mt.Policy(candidate_policy)

    tree, dirty = _working_snapshot(repo, subject_sha)
    if not tree:
        raise GateError("candidate scope is empty")
    changes = repo.diff(base_sha, subject_sha)
    renamed = _renames(repo, base_sha, subject_sha)
    deleted_committed = sorted(set(base_tree) - set(repo.tree_inventory(subject_sha)))
    added_or_dirty = sorted(set(tree) - set(base_tree))

    if base_state == "legacy-uninitialized" and candidate_policy.get("manifest_status") == INITIALIZED_POLICY_STATUS:
        return _validate_bootstrap_transition(repo, base_sha, subject_sha, base_policy, base_schema_raw,
                                              candidate, candidate_policy, tree, require_headers=True)

    if base_state == "legacy-uninitialized":
        if candidate_policy.get("manifest_status") != base_policy.get("manifest_status"):
            raise GateError("candidate cannot self-declare initialized metadata enforcement from a legacy BASE")
        _validate_foundation_delta(base_policy, candidate_policy, base_schema, candidate_schema)
        if candidate_raw != base_manifest_raw:
            raise GateError("legacy foundation must preserve the trusted canonical manifest bytes exactly")
        if not candidate.get("files"):
            raise GateError("legacy transition cannot clear the canonical manifest")
        # The canonical legacy blob was reviewed before today's semantic
        # classifier existed. Defer only discrepancies already present under
        # the fixed anchor policy, and identify every affected path; any new
        # discrepancy introduced by the candidate policy still fails closed.
        anchor_policy = mt.Policy(_json_bytes(_base_blob(repo, LEGACY_CANONICAL_ANCHOR, POLICY_PATH)[0], "reviewed anchor policy"))
        deferred_records = []
        for path, rec in sorted(candidate["files"].items()):
            try:
                mt.validate_record(path, rec, policy_obj)
            except mt.ManifestError as candidate_error:
                try:
                    mt.validate_record(path, rec, anchor_policy)
                except mt.ManifestError as anchor_error:
                    if str(anchor_error) == str(candidate_error):
                        deferred_records.append({"path": path, "reason": str(candidate_error)})
                        continue
                raise GateError(f"{path}: candidate introduces a new legacy record inconsistency: {candidate_error}") from candidate_error
        committed_tree = repo.tree_inventory(subject_sha)
        # Candidate must retain every governance object that already existed in
        # BASE, and introduce the complete fixed foundation tool set.
        base_governance = {p for p in base_tree
                           if p.startswith("scripts/file_metadata/") or p == "docs/engineering/file-metadata.md"}
        required = set(LEGACY_EXPLICIT_ADDITIONS) | FOUNDATION_TOOL_PATHS
        removed_governance = sorted(base_governance - set(committed_tree))
        removed_worktree_governance = sorted(base_governance - set(tree))
        if removed_governance or removed_worktree_governance:
            removed_governance = sorted(set(removed_governance) | set(removed_worktree_governance))
            raise GateError(f"foundation candidate deleted existing governance objects: {removed_governance[:12]}")
        missing_required = sorted(required - set(committed_tree))
        missing_worktree_required = sorted(required - set(tree))
        if missing_required or missing_worktree_required:
            missing_required = sorted(set(missing_required) | set(missing_worktree_required))
            raise GateError(f"foundation candidate lacks required canonical/tool objects: {missing_required}")
        empty_existing_governance = sorted(p for p in base_governance
            if committed_tree[p].content is None or not committed_tree[p].content
            or tree[p].content is None or not tree[p].content)
        if empty_existing_governance:
            raise GateError(f"foundation candidate replaced existing governance objects with empty content: {empty_existing_governance[:12]}")
        empty_required = sorted(p for p in required if committed_tree[p].content is None or not committed_tree[p].content
                                or tree[p].content is None or not tree[p].content)
        if empty_required:
            raise GateError(f"foundation required objects cannot be empty: {empty_required}")
        if committed_tree[MANIFEST_PATH].oid != base_manifest_oid:
            raise GateError("foundation candidate must preserve the canonical legacy manifest Git blob")
        for required_path, expected_raw in ((SCHEMA_PATH, current_schema_raw), (POLICY_PATH, current_policy_raw)):
            if committed_tree[required_path].content != expected_raw:
                raise GateError(f"foundation {required_path} changes must be committed in the fixed candidate")
        # New tooling source files are intentionally absent from the legacy
        # manifest; report this drift without claiming full coverage.
        candidate_missing, candidate_stale = compare_manifest_coverage(tree, candidate.get("files", {}))
        return {
            "status": "NOT_RUN", "mode": "strict-legacy-transition", "reason": "BASE is trusted legacy-uninitialized; full header/provenance enforcement starts only after reviewed bootstrap",
            "base": base_sha, "base_tree": repo.commit_identity(base_sha)[1], "base_manifest_blob": base_manifest_oid,
            "base_schema_blob": base_schema_oid, "base_policy_blob": base_policy_oid,
            "subject": subject_sha, "subject_tree": subject_tree_oid,
            "candidate_path_count": len(tree), "base_legacy_missing": legacy_drift["base_missing"],
            "base_legacy_stale": legacy_drift["base_stale"],
            "legacy_historical_renames": legacy_drift["historical_renames"],
            "legacy_bridge_evidence": legacy_drift["bridge_evidence"],
            "deferred_legacy_record_validation": deferred_records,
            "candidate_registry_missing": candidate_missing, "candidate_registry_stale": candidate_stale,
            "missing": candidate_missing, "stale": candidate_stale,
            "candidate_additions": added_or_dirty, "candidate_deletions": deleted_committed,
            "worktree_delta_count": len(dirty), "full_metadata_enforcement": "NOT_RUN",
        }

    if candidate_policy.get("manifest_status") != base_policy.get("manifest_status"):
        raise GateError("candidate downgrades or changes the trusted BASE manifest_status")
    if current_schema_raw != base_schema_raw or current_policy_raw != base_policy_raw:
        raise GateError("schema/policy changes require the separately reviewed foundation transition; initialized BASE cannot be weakened inline")
    missing, extra = compare_manifest_coverage(tree, candidate.get("files", {}))
    if missing or extra:
        raise GateError(f"candidate/worktree coverage mismatch: missing={missing[:8]}, stale={extra[:8]}")
    old_files = base_manifest["files"]
    internal_base = base_manifest["inventory"]["baseline_head"]
    internal_sha, _ = repo.commit_identity(internal_base)
    try:
        repo.run("merge-base", "--is-ancestor", internal_sha, base_sha)
    except GitInventoryError as exc:
        raise GateError("BASE manifest inventory baseline is not an ancestor of the trusted BASE") from exc
    internal_tree = repo.tree_inventory(internal_sha)
    if base_manifest["inventory"]["tracked_file_count"] != len(internal_tree):
        raise GateError("BASE manifest tracked_file_count differs from its declared provenance baseline")
    for old_path, old_record in old_files.items():
        mt.validate_record(old_path, old_record, policy_obj)
    candidate_internal = candidate.get("inventory", {}).get("baseline_head")
    if candidate_internal != internal_sha:
        raise GateError("candidate may not replace or self-report a different inventory baseline in an ordinary window")
    if candidate.get("inventory", {}).get("tracked_file_count") != len(internal_tree):
        raise GateError("candidate tracked_file_count differs from the trusted inventory baseline")
    if candidate.get("inventory", {}).get("baseline_timestamp") != base_manifest["inventory"].get("baseline_timestamp"):
        raise GateError("candidate may not rewrite the trusted inventory baseline timestamp")

    # Once initialized, the gate cannot PASS a candidate that edits the Python
    # modules executing its own policy. Updating these files requires the
    # separately reviewed foundation path; this prevents a candidate checker
    # from weakening the check that is currently evaluating it.
    committed_tree = repo.tree_inventory(subject_sha)
    guard_prefix = "scripts/file_metadata/"
    changed_guard = [path for path, entry in base_tree.items()
                     if path.startswith(guard_prefix) and path.endswith(".py") and
                     (path not in committed_tree or
                      (committed_tree[path].mode, committed_tree[path].oid) != (entry.mode, entry.oid))]
    if changed_guard:
        raise GateError(f"metadata guard implementation changes require a separately reviewed transition: {changed_guard[:12]}")

    candidate_additions = sorted(set(tree) - set(internal_tree))
    if sorted(candidate["inventory"].get("explicit_additions", [])) != candidate_additions:
        raise GateError("candidate explicit_additions differs from declared inventory baseline to candidate scope")
    expected_declared = set(tree)
    if set(candidate["files"]) != expected_declared:
        raise GateError("candidate manifest paths differ from complete candidate tracked/worktree scope")

    inherited: list[dict[str, str]] = []
    metadata_only: list[str] = []
    uncommitted_body: list[str] = []
    first_parent_chain = set(repo.run("rev-list", "--first-parent", subject_sha).splitlines())
    base_on_first_parent = base_sha.encode("ascii") in first_parent_chain
    for path, entry in tree.items():
        record = candidate["files"][path]
        rule = policy_obj.classify(path)
        if rule is None:
            raise GateError(f"{path}: no policy class; candidate fails closed")
        mt.validate_record(path, record, policy_obj)
        _exact_record_header(path, entry, record, rule)
        committed_entry = committed_tree.get(path)
        if committed_entry is None or (entry.mode, entry.object_type) != (committed_entry.mode, committed_entry.object_type):
            uncommitted_body.append(path)
            continue
        old_path = renamed.get(path, path)
        old_rec = old_files.get(old_path)
        before = base_tree.get(old_path)
        if old_rec is not None and before is not None:
            identity_at_base = _window_identity_origin(repo, base_sha, subject_sha, path,
                base_on_first_parent=base_on_first_parent)
            if identity_at_base is None:
                # A deletion/re-add breaks inheritance even if the net
                # BASE-to-SUBJECT diff reports a rename.
                old_rec = None
                before = None
            elif identity_at_base != old_path:
                old_rec = None
                before = None
        if old_rec is None or before is None:
            if record["revision"] != "r1":
                raise GateError(f"{path}: new or copied path must start at r1")
            prov = repo.path_provenance(path, subject=subject_sha,
                style=mt._native(rule, path)[1], handling=rule["handling"], file_kind=mt._native(rule, path)[0])
            _verify_new_evidence(repo, path, record["created"], prov.created, subject_sha=subject_sha, rule=rule)
            if path not in committed_tree:
                raise GateError(f"{path}: uncommitted new source has no verifiable Git date; full gate cannot PASS")
            _verify_new_evidence(repo, path, record["updated"], prov.updated, subject_sha=subject_sha, rule=rule)
            continue

        if old_path != path and old_path not in base_tree:
            raise GateError(f"{path}: rename source is not present in trusted BASE")
        old_rule = policy_obj.classify(old_path)
        if old_rule is None:
            raise GateError(f"{old_path}: prior policy class is missing")
        _exact_record_header(old_path, before, old_rec, old_rule)
        old_body = _entry_body(before, old_rule, old_path)
        new_body = _entry_body(entry, rule, path)
        body_same = old_body == new_body
        mode_same = before.mode == entry.mode and before.object_type == entry.object_type
        if old_path == path and before.oid != entry.oid and body_same and mode_same:
            metadata_only.append(path)
        if before.mode in {"100644", "100755"} and entry.mode in {"100644", "100755"}:
            expected_revision = mt.revision_for_window(old_rec["revision"], before, entry,
                style=mt._native(rule, path)[1], handling=rule["handling"], file_kind=mt._native(rule, path)[0],
                identity_relation="verified-rename" if old_path != path else "same")
        else:
            expected_revision = mt.revision_for_window(old_rec["revision"], before, entry,
                style=mt._native(rule, path)[1] or mt.STYLES["slash"], handling=rule["handling"],
                file_kind=mt._native(rule, path)[0] or "go",
                identity_relation="verified-rename" if old_path != path else "same")
        if record["revision"] != expected_revision:
            raise GateError(f"{path}: revision {record['revision']} differs from BASE-to-candidate window {expected_revision}")

        # Created/updated evidence is inherited only byte-for-byte from trusted
        # BASE and only along same-path identity or a Git-verified rename.
        if record["created"] == old_rec["created"]:
            inherited.append({"path": path, "field": "created", "source": "inherited-from-reviewed-base"})
        else:
            prov = repo.path_provenance(path, subject=subject_sha,
                style=mt._native(rule, path)[1], handling=rule["handling"], file_kind=mt._native(rule, path)[0])
            _verify_new_evidence(repo, path, record["created"], prov.created, subject_sha=subject_sha, rule=rule)
        is_self_manifest = path == MANIFEST_PATH
        if is_self_manifest:
            if record["updated"] != mt.manifest_self_updated().__dict__:
                raise GateError(f"{path}: canonical self-reference must keep its exact unknown updated exception")
            # The JSON registry has no separable in-band header. Its exact
            # self-reference unknown is checked by validate_record; do not ask
            # it to cite the commit that would contain this very JSON update.
        elif body_same:
            if record["updated"] != old_rec["updated"]:
                raise GateError(f"{path}: net-equal body/mode window must preserve trusted BASE updated evidence")
            inherited.append({"path": path, "field": "updated", "source": "inherited-from-reviewed-base"})
        else:
            head_entry = committed_tree.get(path)
            if head_entry is None or _entry_body(head_entry, rule, path) != new_body or head_entry.mode != entry.mode:
                uncommitted_body.append(path)
                continue
            prov = repo.path_provenance(path, subject=subject_sha,
                style=mt._native(rule, path)[1], handling=rule["handling"], file_kind=mt._native(rule, path)[0])
            _verify_new_evidence(repo, path, record["updated"], prov.updated, subject_sha=subject_sha, rule=rule)
        if old_path != path:
            inherited.append({"path": path, "field": "identity", "source": f"verified-rename-from:{old_path}"})

    if uncommitted_body:
        raise GateError(f"uncommitted source body/mode changes have no candidate Git provenance; cannot PASS: {uncommitted_body[:12]}")
    return {
        "status": "PASS", "mode": "initialized-target-base-window", "base": base_sha,
        "base_tree": repo.commit_identity(base_sha)[1], "base_manifest_blob": base_manifest_oid,
        "subject": subject_sha, "subject_tree": subject_tree_oid,
        "candidate_path_count": len(tree), "candidate_additions": added_or_dirty,
        "candidate_deletions": deleted_committed, "verified_renames": [{"from": old, "to": new} for new, old in renamed.items()],
        "metadata_only_normalized_paths": sorted(metadata_only), "inherited_base_fields": inherited,
        "uncommitted_delta_paths": sorted(dirty), "history_source_changes_verified_in_candidate": True,
    }


def _plan_header_apply(repo_path: Path, base: str, subject: str, manifest_path: Path,
                       schema_path: Path, policy_path: Path, paths: list[str]) -> dict[str, Any]:
    repo = GitRepo(repo_path)
    base_sha, _ = _fixed_sha(repo, base, "target BASE")
    subject_sha, _ = _fixed_sha(repo, subject, "candidate SUBJECT")
    if _repo_argument_path(repo.repo, manifest_path) != (repo.repo / MANIFEST_PATH).resolve():
        raise GateError(f"apply only accepts the canonical manifest path {MANIFEST_PATH}")
    base_manifest_raw, _ = _base_blob(repo, base_sha, MANIFEST_PATH)
    base_schema_raw, _ = _base_blob(repo, base_sha, SCHEMA_PATH)
    base_policy_raw, _ = _base_blob(repo, base_sha, POLICY_PATH)
    base_manifest, schema, policy_raw = (_json_bytes(base_manifest_raw, "BASE manifest"),
                                         _json_bytes(base_schema_raw, "BASE schema"),
                                         _json_bytes(base_policy_raw, "BASE policy"))
    candidate = _json_bytes(_load_current_file(repo.repo, str(manifest_path)), "candidate manifest")
    current_schema_raw = _load_current_file(repo.repo, str(schema_path))
    current_policy_raw = _load_current_file(repo.repo, str(policy_path))
    current_policy = _json_bytes(current_policy_raw, "candidate policy")
    is_bootstrap = current_policy.get("manifest_status") == INITIALIZED_POLICY_STATUS and policy_raw.get("manifest_status") == LEGACY_POLICY_STATUS
    if current_schema_raw != base_schema_raw:
        raise GateError("apply planning requires schema bytes unchanged from trusted BASE")
    if is_bootstrap:
        working, _ = _working_snapshot(repo, subject_sha)
        _validate_bootstrap_transition(repo, base_sha, subject_sha, policy_raw, base_schema_raw,
                                       candidate, current_policy, working, require_headers=False)
        effective_policy = current_policy
    else:
        if current_policy_raw != base_policy_raw:
            raise GateError("apply planning requires policy bytes unchanged from trusted BASE")
        effective_policy = policy_raw
    mt.validate_schema(candidate, schema)
    policy_obj = mt.Policy(effective_policy)
    tree = repo.tree_inventory(subject_sha)
    missing, extra = compare_manifest_coverage(tree, candidate.get("files", {}))
    if missing or extra:
        raise GateError(f"apply candidate coverage mismatch: missing={missing[:8]}, stale={extra[:8]}")
    changes: list[dict[str, Any]] = []
    for path in paths:
        if path not in tree or path not in candidate["files"]:
            raise GateError(f"requested header path is absent from fixed subject/manifest: {path}")
        record = candidate["files"][path]
        rule = policy_obj.classify(path)
        if rule is None or rule["handling"] != "header-and-manifest":
            raise GateError(f"{path}: protected, generated, or registry-only content is never header-applied")
        mt.validate_record(path, record, policy_obj)
        kind, style, _ = mt._native(rule, path)
        if tree[path].content is None or tree[path].mode not in {"100644", "100755"}:
            raise GateError(f"{path}: unsupported object/mode for header application")
        planned = insert_header(tree[path].content, record, style=style, handling=rule["handling"], file_kind=kind, mode=tree[path].mode)
        current_path = _safe_target(repo.repo, path)
        if current_path.is_symlink() or not current_path.is_file():
            raise GateError(f"{path}: working copy is not an ordinary file")
        current = current_path.read_bytes()
        full_mode = stat.S_IMODE(current_path.stat().st_mode)
        current_mode = "100755" if full_mode & 0o111 else "100644"
        if current_mode != tree[path].mode:
            raise GateError(f"{path}: working-copy executable mode differs from fixed subject")
        current_hash = hashlib.sha256(current).hexdigest()
        expected_source_hash = hashlib.sha256(tree[path].content).hexdigest()
        # Idempotent already-applied files are allowed; a body or concurrent edit is not.
        try:
            current_body = metadata_body(current, style, rule["handling"], file_kind=kind, mode=tree[path].mode)
            source_body = metadata_body(tree[path].content, style, rule["handling"], file_kind=kind, mode=tree[path].mode)
        except MetadataError as exc:
            raise GateError(f"{path}: current file cannot be safely compared to subject: {exc}") from exc
        if current_body != source_body:
            raise GateError(f"{path}: current body differs from fixed subject; refusing apply")
        changes.append({"path": path, "expected_current_sha256": current_hash,
                        "expected_current_mode": full_mode,
                        "subject_sha256": expected_source_hash,
                        "output_sha256": hashlib.sha256(planned).hexdigest(),
                        "output_bytes_hex_preview": planned[:0].hex()})
    return {"version": 1, "repo": str(repo.repo), "base": base_sha, "subject": subject_sha,
            "manifest_sha256": hashlib.sha256(_load_current_file(repo.repo, str(manifest_path))).hexdigest(),
            "schema_blob_oid": _base_blob(repo, base_sha, SCHEMA_PATH)[1],
            "policy_blob_oid": _base_blob(repo, base_sha, POLICY_PATH)[1],
            "candidate_policy_sha256": hashlib.sha256(current_policy_raw).hexdigest(),
            "bootstrap": is_bootstrap, "changes": changes}


def apply_header_plan(plan_path: Path, plan_sha: str, *, apply: bool) -> dict[str, Any]:
    raw = plan_path.read_bytes()
    actual_plan_sha = hashlib.sha256(raw).hexdigest()
    if actual_plan_sha != plan_sha:
        raise GateError("apply plan hash mismatch; review the exact plan before applying")
    plan = _json_bytes(raw, "apply plan")
    repo = Path(plan["repo"]).resolve()
    git = GitRepo(repo)
    base_sha, _ = git.commit_identity(plan["base"])
    subject_sha, _ = git.commit_identity(plan["subject"])
    if base_sha != plan["base"] or subject_sha != plan["subject"]:
        raise GateError("apply plan commit identities changed")
    candidate_policy_raw = _load_current_file(repo, POLICY_PATH)
    if hashlib.sha256(candidate_policy_raw).hexdigest() != plan.get("candidate_policy_sha256", hashlib.sha256(_base_blob(git, base_sha, POLICY_PATH)[0]).hexdigest()):
        raise GateError("candidate policy changed after plan creation")
    if not plan.get("bootstrap", False) and candidate_policy_raw != _base_blob(git, base_sha, POLICY_PATH)[0]:
        raise GateError("non-bootstrap apply cannot change trusted BASE policy")
    if plan.get("bootstrap", False):
        base_policy_raw, _ = _base_blob(git, base_sha, POLICY_PATH)
        base_schema_raw, _ = _base_blob(git, base_sha, SCHEMA_PATH)
        working, _ = _working_snapshot(git, subject_sha)
        _validate_bootstrap_transition(git, base_sha, subject_sha,
            _json_bytes(base_policy_raw, "BASE policy"), base_schema_raw,
            _json_bytes(_load_current_file(repo, MANIFEST_PATH), "candidate manifest"),
            _json_bytes(candidate_policy_raw, "candidate policy"), working, require_headers=False)
    prepared: list[tuple[Path, bytes, bytes, int, str]] = []
    for change in plan["changes"]:
        path = change["path"]
        target = _safe_target(repo, path)
        if target.is_symlink() or not target.is_file():
            raise GateError(f"{path}: apply target is missing or not a regular file")
        expected_mode = change.get("expected_current_mode")
        if not isinstance(expected_mode, int) or isinstance(expected_mode, bool) or not 0 <= expected_mode <= 0o7777:
            raise GateError(f"{path}: apply plan lacks a valid approved full mode")
        if stat.S_IMODE(target.stat().st_mode) != expected_mode:
            raise GateError(f"{path}: full file mode changed after plan creation; refusing write")
        before = target.read_bytes()
        if hashlib.sha256(before).hexdigest() != change["expected_current_sha256"]:
            raise GateError(f"{path}: raw file changed after plan creation; refusing write")
        # The rendered bytes are reconstructed from the fixed commit and manifest,
        # never embedded as caller-controlled content in the plan.
        manifest_raw = _load_current_file(repo, MANIFEST_PATH)
        if hashlib.sha256(manifest_raw).hexdigest() != plan["manifest_sha256"]:
            raise GateError("candidate manifest changed after apply plan creation")
        manifest = _json_bytes(manifest_raw, "candidate manifest")
        schema_raw, schema_oid = _base_blob(git, base_sha, SCHEMA_PATH)
        policy_raw, policy_oid = _base_blob(git, base_sha, POLICY_PATH)
        if schema_oid != plan["schema_blob_oid"] or policy_oid != plan["policy_blob_oid"]:
            raise GateError("trusted BASE schema/policy changed after apply plan creation")
        schema = _json_bytes(schema_raw, "BASE schema")
        policy = _json_bytes(candidate_policy_raw if plan.get("bootstrap", False) else policy_raw, "apply policy")
        policy_obj = mt.Policy(policy)
        rule = policy_obj.classify(path)
        if rule is None or rule["handling"] != "header-and-manifest":
            raise GateError(f"{path}: apply plan points to protected or unclassified content")
        entry = git.tree_inventory(subject_sha).get(path)
        if entry is None or entry.content is None:
            raise GateError(f"{path}: path no longer exists in fixed subject tree")
        current_mode = "100755" if target.stat().st_mode & 0o111 else "100644"
        if current_mode != entry.mode:
            raise GateError(f"{path}: current mode differs from fixed subject; refusing write")
        mt.validate_schema(manifest, schema)
        rec = manifest["files"][path]
        mt.validate_record(path, rec, policy_obj)
        kind, style, _ = mt._native(rule, path)
        try:
            current_body = metadata_body(before, style, rule["handling"], file_kind=kind, mode=entry.mode)
            subject_body = metadata_body(entry.content, style, rule["handling"], file_kind=kind, mode=entry.mode)
        except MetadataError as exc:
            raise GateError(f"{path}: pre-apply body comparison failed closed: {exc}") from exc
        if current_body != subject_body:
            raise GateError(f"{path}: current body differs from fixed subject; refusing write")
        output = insert_header(entry.content, rec, style=style, handling=rule["handling"], file_kind=kind, mode=entry.mode)
        if hashlib.sha256(output).hexdigest() != change["output_sha256"]:
            raise GateError(f"{path}: reconstructed output differs from reviewed plan")
        mode = stat.S_IMODE(target.stat().st_mode)
        if mode != expected_mode:
            raise GateError(f"{path}: full file mode changed during preparation; refusing write")
        prepared.append((target, before, output, expected_mode, change["path"]))
    result = {"status": "DRY_RUN" if not apply else "APPLIED", "plan_sha256": actual_plan_sha,
              "paths": [item[4] for item in prepared]}
    if not apply:
        return result
    # Prewrite all temporary files first, then recheck every target's original hash
    # before the first replacement. Roll back replacements if any later replace fails.
    temps: list[tuple[Path, Path, bytes, int, str]] = []
    replaced: list[tuple[Path, bytes, int]] = []
    backup_dir = plan_path.parent / f"apply-backup-{actual_plan_sha[:12]}"
    try:
        if backup_dir.exists() or backup_dir.is_symlink():
            raise GateError(f"backup destination already exists; refusing to reuse it: {backup_dir}")
        backup_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
        backup_index: list[dict[str, str]] = []
        for target, before, output, mode, relative in prepared:
            if _safe_target(repo, relative) != target or target.is_symlink() or not target.is_file() or stat.S_IMODE(target.stat().st_mode) != mode or target.read_bytes() != before:
                raise GateError(f"{relative}: file or full mode changed before preparation")
            backup_name = hashlib.sha256(relative.encode("utf-8")).hexdigest() + ".bin"
            backup_path = backup_dir / backup_name
            fd, backup_tmp_name = tempfile.mkstemp(prefix=".backup-", dir=backup_dir)
            with os.fdopen(fd, "wb") as stream:
                stream.write(before)
                stream.flush()
                os.fsync(stream.fileno())
            os.chmod(backup_tmp_name, 0o600)
            os.replace(backup_tmp_name, backup_path)
            backup_index.append({"path": relative, "backup": backup_name,
                                 "sha256": hashlib.sha256(before).hexdigest(), "mode": oct(mode)})
            fd, temp_name = tempfile.mkstemp(prefix=f".{target.name}.metadata-", dir=target.parent)
            temp_path = Path(temp_name)
            with os.fdopen(fd, "wb") as stream:
                stream.write(output)
                stream.flush()
                os.fsync(stream.fileno())
            os.chmod(temp_path, mode)
            temps.append((target, temp_path, before, mode, relative))
        (backup_dir / "index.json").write_text(json.dumps(backup_index, indent=2) + "\n", encoding="utf-8")
        os.chmod(backup_dir / "index.json", 0o600)
        for target, before, _, mode, relative in prepared:
            current_mode = stat.S_IMODE(target.stat().st_mode)
            if current_mode != mode or hashlib.sha256(target.read_bytes()).digest() != hashlib.sha256(before).digest():
                raise GateError(f"{target.relative_to(repo)}: concurrent write detected before apply")
        for target, temp_path, before, mode, relative in temps:
            if _safe_target(repo, relative) != target or target.is_symlink() or not target.is_file() or stat.S_IMODE(target.stat().st_mode) != mode or target.read_bytes() != before:
                raise GateError(f"{relative}: file or full mode changed before replacement")
            os.replace(temp_path, target)
            replaced.append((target, before, mode))
    except Exception as exc:
        rollback_errors = []
        for target, before, mode in reversed(replaced):
            try:
                fd, temp_name = tempfile.mkstemp(prefix=f".{target.name}.metadata-rollback-", dir=target.parent)
                with os.fdopen(fd, "wb") as stream:
                    stream.write(before); stream.flush(); os.fsync(stream.fileno())
                os.chmod(temp_name, mode)
                os.replace(temp_name, target)
            except Exception as rollback_exc:
                rollback_errors.append(f"{target}: {rollback_exc}")
        for _, temp_path, _, _, _ in temps:
            try: temp_path.unlink(missing_ok=True)
            except OSError: pass
        if rollback_errors:
            backup_dir = plan_path.parent / f"apply-failure-backup-{actual_plan_sha[:12]}"
            backup_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
            backups = []
            for target, before, _, mode, _ in prepared:
                backup_name = hashlib.sha256(str(target.relative_to(repo)).encode("utf-8")).hexdigest() + ".bin"
                backup_path = backup_dir / backup_name
                backup_path.write_bytes(before)
                backup_path.chmod(0o600)
                backups.append(f"{target.relative_to(repo)}={backup_path}")
            raise GateError(f"apply failed; rollback incomplete for {rollback_errors}; original bytes preserved in {backups}") from exc
        if isinstance(exc, GateError): raise
        raise GateError(f"apply failed; completed replacements rolled back: {exc}") from exc
    for _, temp_path, _, _, _ in temps:
        temp_path.unlink(missing_ok=True)
    result["backup_dir"] = str(backup_dir)
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    check_parser = sub.add_parser("check")
    check_parser.add_argument("--repo", type=Path, required=True)
    check_parser.add_argument("--base", required=True, help="actual target branch BASE SHA from CI")
    check_parser.add_argument("--subject", required=True, help="fixed candidate SHA")
    check_parser.add_argument("--manifest", type=Path, default=Path(MANIFEST_PATH))
    check_parser.add_argument("--schema", type=Path, default=Path(SCHEMA_PATH))
    check_parser.add_argument("--policy", type=Path, default=Path(POLICY_PATH))
    plan_parser = sub.add_parser("plan-apply")
    plan_parser.add_argument("--repo", type=Path, required=True)
    plan_parser.add_argument("--base", required=True)
    plan_parser.add_argument("--subject", required=True)
    plan_parser.add_argument("--manifest", type=Path, default=Path(MANIFEST_PATH))
    plan_parser.add_argument("--schema", type=Path, default=Path(SCHEMA_PATH))
    plan_parser.add_argument("--policy", type=Path, default=Path(POLICY_PATH))
    plan_parser.add_argument("--path", action="append", required=True, help="exact eligible file; repeat for each path")
    plan_parser.add_argument("--output", type=Path, required=True)
    apply_parser = sub.add_parser("apply")
    apply_parser.add_argument("--plan", type=Path, required=True)
    apply_parser.add_argument("--plan-sha256", required=True, help="reviewed SHA-256 of the exact apply plan")
    apply_parser.add_argument("--apply", action="store_true", help="perform writes; omitted means dry-run only")
    args = parser.parse_args(argv)
    try:
        if args.command == "check":
            result = check(args.repo.resolve(), args.base, args.subject, args.manifest, args.schema, args.policy)
            print(json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True))
            return 0 if result["status"] == "PASS" else 3
        if args.command == "plan-apply":
            plan = _plan_header_apply(args.repo.resolve(), args.base, args.subject,
                                      args.manifest, args.schema, args.policy, args.path)
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(plan, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
            plan_sha = hashlib.sha256(args.output.read_bytes()).hexdigest()
            print(json.dumps({"status": "DRY_RUN", "plan": str(args.output), "plan_sha256": plan_sha,
                              "paths": [x["path"] for x in plan["changes"]]}, ensure_ascii=False, indent=2))
            return 0
        result = apply_header_plan(args.plan, args.plan_sha256, apply=args.apply)
        print(json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True))
        return 0
    except (GateError, mt.ManifestError, GitInventoryError, MetadataError, OSError, KeyError, TypeError, ValueError) as exc:
        print(json.dumps({"status": "FAIL", "error": str(exc)}, ensure_ascii=False), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())

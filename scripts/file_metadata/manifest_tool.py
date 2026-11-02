"""Read-only FILE metadata manifest inventory and validation.

No command in this module writes to the target repository. Schema validation is
closed over a declared keyword set and rejects unsupported keywords.
"""
from __future__ import annotations

import argparse
from datetime import date, datetime, timezone
import fnmatch
import json
from pathlib import Path
import re
import sys
from typing import Any

from git_inventory import (GitRepo, GitInventoryError, compare_manifest_coverage,
                           manifest_self_updated, revision_for_window, new_path_revision,
                           parse_raw_changes)
from metadata_core import FILE_KIND_STYLE, STYLES, content_changed, metadata_body, MetadataError

SUPPORTED_SCHEMA_KEYWORDS = {
    "$schema", "$id", "$defs", "$ref", "title", "description", "type", "const", "enum",
    "properties", "required", "additionalProperties", "propertyNames", "items", "minItems",
    "uniqueItems", "minLength", "pattern", "format", "minimum", "allOf", "oneOf", "if",
    "then", "not", "contains",
}

class ManifestError(ValueError):
    pass

def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ManifestError(f"duplicate JSON object key: {key!r}")
        result[key] = value
    return result

def load_json(path: Path):
    try:
        return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=_pairs)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ManifestError(f"cannot read strict UTF-8 JSON {path}: {exc}") from exc

def _resolve_ref(root, ref):
    if not isinstance(ref, str) or not ref.startswith("#/"):
        raise ManifestError(f"unsupported schema reference {ref!r}")
    node = root
    for segment in ref[2:].split("/"):
        segment = segment.replace("~1", "/").replace("~0", "~")
        if not isinstance(node, dict) or segment not in node:
            raise ManifestError(f"unresolvable schema reference {ref!r}")
        node = node[segment]
    return node

MANIFEST_PATH = "scripts/file_metadata/manifest.json"

def load_anchored_previous(repo, baseline, external_path=None):
    """Read the prior manifest from baseline's canonical Git blob, never trust free JSON."""
    g = GitRepo(repo)
    baseline_sha, _ = g.commit_identity(baseline)
    entry = g._tree_entry_for_path(baseline_sha, MANIFEST_PATH.encode("utf-8"))
    if entry is None:
        raise ManifestError(f"baseline {baseline_sha} has no canonical manifest blob; provenance is unknown")
    mode, object_type, oid = entry
    if object_type != "blob" or mode not in {"100644", "100755"}:
        raise ManifestError("canonical previous manifest is not an ordinary Git blob")
    g._verify_path_blob(baseline_sha, MANIFEST_PATH.encode("utf-8"), oid, mode)
    raw = g.blob(oid)
    if external_path is not None:
        try: supplied = Path(external_path).read_bytes()
        except OSError as exc: raise ManifestError(f"cannot read supplied previous manifest: {exc}") from exc
        if supplied != raw: raise ManifestError("supplied previous manifest bytes differ from canonical baseline Git blob")
    try: parsed = json.loads(raw.decode("utf-8", "strict"), object_pairs_hook=_pairs)
    except (UnicodeError, json.JSONDecodeError) as exc: raise ManifestError(f"canonical baseline manifest is invalid strict JSON: {exc}") from exc
    return parsed, oid, raw

def _verify_recorded_updated(repo: GitRepo, path: str, record: dict, target: "TreeEntry",
                             *, target_commit: str, rule: dict, kind: str | None, style):
    evidence = record.get("updated", {})
    value, source = evidence.get("value"), evidence.get("source", {})
    if value == "unknown" and source.get("kind") == "unknown":
        if len(source.get("reason", "").strip()) < 8:
            raise ManifestError(f"{path}: unknown updated provenance requires an explicit reason")
        # This verifies honest uncertainty only; it does not establish a date.
        return
    commit = source.get("commit")
    source_path = source.get("path", path)
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40,64}", commit):
        raise ManifestError(f"{path}: updated source lacks a full Git commit")
    commit_sha, _ = repo.commit_identity(commit)
    try: repo.run("merge-base", "--is-ancestor", commit_sha, target_commit)
    except GitInventoryError as exc: raise ManifestError(f"{path}: updated source commit is not an ancestor of the fixed target") from exc
    try:
        timestamp = int(repo.run("show", "-s", "--format=%ct", commit_sha).strip())
        actual_date = datetime.fromtimestamp(timestamp, timezone.utc).date().isoformat()
    except (ValueError, OverflowError, OSError) as exc: raise ManifestError(f"{path}: updated source has invalid committer date") from exc
    if value != actual_date: raise ManifestError(f"{path}: updated date does not match source committer UTC date")
    try: raw_source_path = source_path.encode("utf-8", "strict")
    except (AttributeError, UnicodeError) as exc: raise ManifestError(f"{path}: updated source path is invalid") from exc
    source_entry = repo._tree_entry_for_path(commit_sha, raw_source_path)
    if source_entry is None: raise ManifestError(f"{path}: updated source path is absent from its commit")
    mode, object_type, oid = source_entry
    repo._verify_path_blob(commit_sha, raw_source_path, oid, mode)
    # Require the source commit to belong to the target path's Git --follow
    # identity history, so an unrelated path with an identical blob is rejected.
    identity_path = _identity_path_at_commit(repo, target.path, target_commit, commit_sha)
    if identity_path != source_path:
        raise ManifestError(f"{path}: updated commit is outside this path's verified rename/history chain")
    if mode in {"100644", "100755"} and target.mode in {"100644", "100755"}:
        source_data = repo.blob(oid)
        target_data = target.content
        if target_data is None: raise ManifestError(f"{path}: target file body is unavailable")
        try:
            source_body = metadata_body(source_data, style, rule["handling"], file_kind=kind, mode=mode) if style else source_data
            target_body = metadata_body(target_data, style, rule["handling"], file_kind=kind, mode=target.mode) if style else target_data
        except MetadataError as exc: raise ManifestError(f"{path}: cannot safely normalize source/body for updated evidence: {exc}") from exc
        if source_body != target_body: raise ManifestError(f"{path}: updated source body differs from fixed target body")
    elif mode != target.mode or oid != target.oid or object_type != target.object_type:
        raise ManifestError(f"{path}: non-regular updated source must match target mode/type/object exactly")

def _identity_path_at_commit(repo: GitRepo, target_path: str, target_commit: str, source_commit: str) -> str:
    raw_path = target_path.encode("utf-8", "strict")
    log = repo.run("log", "--follow", "--format=%H", target_commit, "--", b":(literal)" + raw_path)
    commits = [line.decode("ascii") for line in log.splitlines()]
    if source_commit not in commits: raise ManifestError(f"{target_path}: source commit is not in Git follow history")
    current_path = raw_path
    for sha in commits:
        parents = repo.run("rev-list", "--parents", "-n", "1", sha).strip().split()
        if len(parents) > 1:
            changes = repo.diff(parents[1].decode("ascii"), sha)
        else:
            changes = parse_raw_changes(repo.run("diff-tree", "--root", "--no-commit-id", "-r", "--raw", "-z", "-M", "--no-abbrev", sha, "--"))
        change = next((c for c in changes if c.new_path == current_path or (c.status == "D" and c.old_path == current_path)), None)
        if change is None: raise ManifestError(f"{target_path}: Git history has an identity gap at {sha}")
        if sha == source_commit:
            return current_path.decode("utf-8", "strict")
        if change.status.startswith("R"):
            if change.old_path is None: raise ManifestError(f"{target_path}: rename lacks old path at {sha}")
            current_path = change.old_path
        elif change.status == "A":
            break
    raise ManifestError(f"{target_path}: source commit is not reachable in tracked file identity chain")

def _same_normalized_body(before, after, rule, path):
    kind, style, _ = _native(rule, path)
    if before.mode in {"100644", "100755"} and after.mode in {"100644", "100755"}:
        try:
            old_body = metadata_body(before.content or b"", style, rule["handling"], file_kind=kind, mode=before.mode) if style else before.content
            new_body = metadata_body(after.content or b"", style, rule["handling"], file_kind=kind, mode=after.mode) if style else after.content
        except MetadataError as exc: raise ManifestError(f"{path}: body comparison failed closed: {exc}") from exc
        return old_body == new_body
    return before.mode == after.mode and before.object_type == after.object_type and before.oid == after.oid

def validate_schema(manifest, schema):
    """Validate the current manifest schema with a deliberately bounded subset.

    Every keyword used by the checked-in schema is evaluated. Schema shapes or
    formats outside this explicit subset fail before any instance can pass.
    """
    def check_schema(node, where="$schema"):
        if isinstance(node, bool): return
        if not isinstance(node, dict): raise ManifestError(f"unsupported schema node at {where}")
        unknown = set(node) - SUPPORTED_SCHEMA_KEYWORDS
        if unknown: raise ManifestError(f"unsupported JSON Schema keyword(s) at {where}: {sorted(unknown)}")
        if "$ref" in node and (not isinstance(node["$ref"], str) or not node["$ref"].startswith("#/")):
            raise ManifestError(f"unsupported $ref at {where}")
        for key in ("$schema", "$id", "title", "description", "format", "pattern"):
            if key in node and not isinstance(node[key], str): raise ManifestError(f"invalid {key} at {where}")
        if "type" in node and node["type"] not in {"object", "array", "string", "integer", "number", "boolean", "null"}:
            raise ManifestError(f"unsupported JSON Schema type at {where}: {node['type']!r}")
        if "format" in node and node["format"] not in {"date", "date-time", "email"}:
            raise ManifestError(f"unsupported JSON Schema format at {where}: {node['format']!r}")
        if "pattern" in node:
            try: re.compile(node["pattern"])
            except (TypeError, re.error) as exc: raise ManifestError(f"invalid schema pattern at {where}: {exc}") from exc
        if "required" in node and (not isinstance(node["required"], list) or not all(isinstance(x, str) for x in node["required"])):
            raise ManifestError(f"invalid required list at {where}")
        if "enum" in node and not isinstance(node["enum"], list): raise ManifestError(f"invalid enum at {where}")
        if "uniqueItems" in node and not isinstance(node["uniqueItems"], bool): raise ManifestError(f"invalid uniqueItems at {where}")
        for key in ("minItems", "minLength"):
            if key in node and (not isinstance(node[key], int) or isinstance(node[key], bool) or node[key] < 0):
                raise ManifestError(f"invalid {key} at {where}")
        if "minimum" in node and (not isinstance(node["minimum"], (int, float)) or isinstance(node["minimum"], bool)):
            raise ManifestError(f"invalid minimum at {where}")
        for key in ("properties", "$defs"):
            if key in node:
                if not isinstance(node[key], dict): raise ManifestError(f"invalid {key} at {where}")
                for name, child in node[key].items(): check_schema(child, f"{where}/{key}/{name}")
        for key in ("items", "additionalProperties", "propertyNames", "contains", "not", "if", "then"):
            if key in node:
                value = node[key]
                if key == "additionalProperties" and isinstance(value, bool): continue
                check_schema(value, f"{where}/{key}")
        for key in ("allOf", "oneOf"):
            if key in node:
                if not isinstance(node[key], list) or not node[key]: raise ManifestError(f"invalid {key} at {where}")
                for i, child in enumerate(node[key]): check_schema(child, f"{where}/{key}/{i}")
    check_schema(schema)

    def errors_for(value, rule, path):
        if rule is False: return [f"{path}: forbidden by false schema"]
        if rule is True: return []
        if not isinstance(rule, dict): return [f"{path}: invalid schema node"]
        errors = []
        if "$ref" in rule:
            errors.extend(errors_for(value, _resolve_ref(schema, rule["$ref"]), path))
            # JSON Schema applies sibling keywords alongside $ref; do not return.
        typ = rule.get("type")
        type_ok = {"object": lambda v: isinstance(v, dict), "array": lambda v: isinstance(v, list),
                   "string": lambda v: isinstance(v, str), "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
                   "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
                   "boolean": lambda v: isinstance(v, bool), "null": lambda v: v is None}
        if typ and not type_ok[typ](value): return errors + [f"{path}: expected {typ}"]
        if "const" in rule and value != rule["const"]: errors.append(f"{path}: must equal {rule['const']!r}")
        if "enum" in rule and value not in rule["enum"]: errors.append(f"{path}: value is outside enum")
        if isinstance(value, dict):
            for key in rule.get("required", []):
                if key not in value: errors.append(f"{path}: missing required {key}")
            props = rule.get("properties", {})
            for key, child in props.items():
                if key in value: errors.extend(errors_for(value[key], child, f"{path}.{key}"))
            ap = rule.get("additionalProperties", True)
            for key in sorted(set(value) - set(props)):
                if ap is False: errors.append(f"{path}: unexpected property {key}")
                elif isinstance(ap, dict): errors.extend(errors_for(value[key], ap, f"{path}.{key}"))
            if "propertyNames" in rule:
                for key in value: errors.extend(errors_for(key, rule["propertyNames"], f"{path}.<key>"))
        if isinstance(value, list):
            if len(value) < rule.get("minItems", 0): errors.append(f"{path}: too few items")
            if rule.get("uniqueItems") and len({json.dumps(v, sort_keys=True) for v in value}) != len(value): errors.append(f"{path}: duplicate items")
            if "items" in rule:
                for i, item in enumerate(value): errors.extend(errors_for(item, rule["items"], f"{path}[{i}]"))
            if "contains" in rule and not any(not errors_for(v, rule["contains"], f"{path}[]") for v in value): errors.append(f"{path}: no item matches contains")
        if isinstance(value, str):
            if len(value) < rule.get("minLength", 0): errors.append(f"{path}: string too short")
            if "pattern" in rule and re.search(rule["pattern"], value) is None: errors.append(f"{path}: pattern mismatch")
            if rule.get("format") == "date":
                try: date.fromisoformat(value)
                except ValueError: errors.append(f"{path}: invalid calendar date")
            if rule.get("format") == "date-time":
                try: datetime.fromisoformat(value.replace("Z", "+00:00"))
                except ValueError: errors.append(f"{path}: invalid date-time")
            if rule.get("format") == "email" and not re.fullmatch(r"[^\s@]+@[^\s@]+\.[^\s@]+", value): errors.append(f"{path}: invalid email")
        if isinstance(value, (int, float)) and not isinstance(value, bool) and value < rule.get("minimum", float("-inf")): errors.append(f"{path}: below minimum")
        for child in rule.get("allOf", []): errors.extend(errors_for(value, child, path))
        if "oneOf" in rule:
            matches = sum(not errors_for(value, child, path) for child in rule["oneOf"])
            if matches != 1: errors.append(f"{path}: must match exactly one alternative")
        if "if" in rule:
            branch = "then" if not errors_for(value, rule["if"], path) else None
            if branch and branch in rule: errors.extend(errors_for(value, rule[branch], path))
        if "not" in rule and not errors_for(value, rule["not"], path): errors.append(f"{path}: matches forbidden not schema")
        return errors
    errors = errors_for(manifest, schema, "$manifest")
    if errors: raise ManifestError("schema validation failed:\n" + "\n".join(errors[:30]))

class Policy:
    def __init__(self, raw):
        self.raw = raw
    @staticmethod
    def _glob(pattern):
        return re.compile(fnmatch.translate(pattern.replace("**/", "*/")))
    def classify(self, path):
        classes = self.raw["file_classes"]
        if path.rsplit("/", 1)[-1].startswith("Dockerfile"):
            return {"id": "dockerfile-parser-directives", "handling": "manifest-only",
                    "reason": "Keep # syntax/# escape parser directives at byte zero until a parser-safe insertion position is verified."}
        for item in classes:
            if path in item.get("paths", []): return item
        for item in classes:
            patterns = item.get("patterns", [])
            if item["id"] in {"frozen-test-fixtures", "third-party-derived-source"}:
                if any(fnmatch.fnmatchcase(path, p) or path.startswith(p.removesuffix("**")) for p in patterns): return item
        for item in classes:
            if any(fnmatch.fnmatchcase(path, p) or fnmatch.fnmatchcase("x/" + path, p) for p in item.get("patterns", [])): return item
        return None

def _meaningful_purpose(value):
    if not isinstance(value, str) or len(value.strip()) < 35: return False
    low = value.lower()
    forbidden = ("main/run internal package behavior", "implements operations", "web adapter", "component-hook", "data and objects")
    return not any(x in low for x in forbidden)

def validate_record(path, rec, policy):
    rule = policy.classify(path)
    if rule is None: raise ManifestError(f"unclassified tracked path: {path}")
    if rec.get("handling") != rule["handling"]: raise ManifestError(f"{path}: handling differs from policy")
    if not _meaningful_purpose(rec.get("purpose")): raise ManifestError(f"{path}: purpose is missing or generic")
    expected_state = {"header-and-manifest": "pending-header-application",
                      "manifest-only": "manifest-only",
                      "preserve-notice-and-manifest": "upstream-notice-preserved",
                      "generated-manifest-only": "generator-owned-output"}[rule["handling"]]
    if rec.get("header_state") != expected_state: raise ManifestError(f"{path}: header_state must be {expected_state!r}")
    auth = rec.get("authors", [])
    project = [a for a in auth if a.get("role") == "project-author"]
    valid_project = any(a.get("name") == "Bai Junhao" and a.get("email") == "baijunhao@cq.chinamobile.com" and a.get("contact") == "+86 13617696321" and len(a.get("source", "").strip()) >= 8 for a in project)
    unknown = [a for a in auth if a.get("role") == "unknown" and a.get("name") == "unknown" and len(a.get("source", "").strip()) >= 8]
    upstream = [a for a in auth if a.get("role") == "upstream-author" and len(a.get("verbatim_attribution", "").strip()) > 0 and len(a.get("source", "").strip()) >= 8]
    if project and (not valid_project or len(project) != 1):
        raise ManifestError(f"{path}: project author must match the explicitly reviewed project identity")
    if any(a.get("role") == "unknown" and a not in unknown for a in auth):
        raise ManifestError(f"{path}: unknown authorship requires an explicit reason")
    if not (valid_project or unknown or upstream):
        raise ManifestError(f"{path}: author requires reviewed attribution or explicit unknown evidence")
    rights = rec.get("copyright", [])
    if not rights or any(len(c.get("source", "").strip()) < 8 or (c.get("holder") == "unknown" and not c.get("notice", "").strip()) for c in rights):
        raise ManifestError(f"{path}: copyright requires an explicit source or unknown reason")
    if not rec.get("maintainers") or not any(m.get("name") == "Bai Junhao" and m.get("email") == "baijunhao@cq.chinamobile.com" for m in rec["maintainers"]):
        raise ManifestError(f"{path}: project maintainer missing")
    if rule["id"] == "third-party-derived-source" and not upstream:
        raise ManifestError(f"{path}: upstream author notice missing")
    if upstream and not any(c.get("verbatim") is True for c in rec.get("copyright", [])):
        raise ManifestError(f"{path}: upstream notice must remain verbatim")
    for field in ("created", "updated"):
        evidence = rec.get(field, {})
        src = evidence.get("source", {})
        if evidence.get("value") == "unknown" and (src.get("kind") != "unknown" or len(src.get("reason", "")) < 8):
            raise ManifestError(f"{path}: unknown {field} requires explicit reason")
        if evidence.get("value") != "unknown" and (src.get("kind") == "unknown" or not src.get("commit")):
            raise ManifestError(f"{path}: known {field} requires traceable commit")
    self_evidence = manifest_self_updated().source
    if path == "scripts/file_metadata/manifest.json":
        expected = {"value": "unknown", "source": self_evidence}
        if rec.get("updated") != expected: raise ManifestError(f"{path}: updated must be the exact canonical self-reference unknown evidence")
    elif any(rec.get(field, {}).get("source", {}).get("reason") == self_evidence["reason"] for field in ("created", "updated")):
        raise ManifestError(f"{path}: manifest self-reference exception cannot be reused")
    if not re.fullmatch(r"r[1-9][0-9]*", rec.get("revision", "")): raise ManifestError(f"{path}: invalid independent revision")
    if rule.get("reason") or rule["handling"] != "header-and-manifest":
        exc = rec.get("exception")
        if (not isinstance(exc, dict) or exc.get("path", path) != path or exc.get("source") != path
                or exc.get("rule_id") != rule["id"] or len(exc.get("reason", "")) < 12):
            raise ManifestError(f"{path}: exact path-specific exception missing")
    expected_generators = {
        "generated-wire-types": {"tool": "scripts/generate_openapi_types.py", "inputs": ["internal/apidocs/openapi.json"]},
        "generated-glossary-views": {"tool": "scripts/check_glossary.py --write-views", "inputs": ["docs/glossary/concepts.json"]},
    }
    if rule["id"] in expected_generators and rec.get("generator") != expected_generators[rule["id"]]:
        raise ManifestError(f"{path}: generator provenance mismatch")
    if rule["handling"] == "header-and-manifest":
        ext = path.rsplit("/", 1)[-1]
        kind = "go" if ext.endswith(".go") else "python" if ext.endswith(".py") else "typescript" if ext.endswith((".ts", ".tsx")) else "shell" if ext.endswith(".sh") else "html" if ext.endswith((".html", ".md")) else "css" if ext.endswith(".css") else "hash-config"
        if kind not in FILE_KIND_STYLE: raise ManifestError(f"{path}: unsupported native header kind")

def validate_manifest(repo, manifest, schema, policy, subject, baseline=None, verify_history=False,
                      previous_manifest=None, previous_manifest_path=None):
    validate_schema(manifest, schema)
    g = GitRepo(repo)
    inv = manifest["inventory"]
    declared_base = inv["baseline_head"]
    if baseline is not None and baseline != declared_base:
        raise ManifestError("explicit baseline does not match manifest.inventory.baseline_head")
    base = declared_base
    subject_sha, subject_tree = g.require_baseline_ancestor(base, subject)
    baseline_tree = g.tree_inventory(base)
    tree = g.tree_inventory(subject_sha)
    missing, extra = compare_manifest_coverage(tree, manifest["files"])
    if missing or extra: raise ManifestError(f"subject coverage mismatch; missing={missing[:8]}, extra={extra[:8]}")
    if inv["tracked_file_count"] != len(baseline_tree): raise ManifestError("tracked_file_count differs from baseline tree")
    additions = sorted(set(tree) - set(baseline_tree))
    if sorted(inv["explicit_additions"]) != additions: raise ManifestError("explicit_additions differs from baseline-to-subject tree delta")
    if set(tree) != set(manifest["files"]): raise ManifestError("manifest paths differ from fixed subject tracked tree")
    previous_tree = None
    p = Policy(policy)
    if verify_history:
        anchored, previous_blob_oid, _ = load_anchored_previous(repo, base, previous_manifest_path)
        if previous_manifest is not None and previous_manifest != anchored:
            raise ManifestError("supplied previous manifest object differs from canonical baseline Git blob")
        previous_manifest = anchored
        validate_schema(previous_manifest, schema)
        previous_tree = baseline_tree
        old_missing, old_extra = compare_manifest_coverage(previous_tree, previous_manifest.get("files", {}))
        if old_missing or old_extra:
            raise ManifestError(f"previous manifest coverage mismatch; missing={old_missing[:8]}, extra={old_extra[:8]}")
        for old_path, old_rec in previous_manifest["files"].items(): validate_record(old_path, old_rec, p)
    for path, entry in tree.items():
        rec = manifest["files"][path]
        validate_record(path, rec, p)
        if verify_history:
            rule = p.classify(path)
            kind, style, _ = _native(rule, path)
            evidence = g.path_provenance(path, subject=subject_sha, style=style, handling=rule["handling"], file_kind=kind)
            for field, actual in (("created", evidence.created),):
                if rec[field] != {"value": actual.value, "source": actual.source}: raise ManifestError(f"{path}: {field} Git evidence mismatch")
            is_self_manifest = path == MANIFEST_PATH
            if is_self_manifest:
                if rec["updated"] != {"value": manifest_self_updated().value, "source": manifest_self_updated().source}:
                    raise ManifestError(f"{path}: canonical self-reference updated evidence mismatch")
            changes = g.diff(base, subject_sha)
            rename = next((c for c in changes if c.status.startswith("R") and c.new_path == entry.raw_path and c.old_path), None)
            old_path = rename.old_path.decode("utf-8", "strict") if rename else path
            old_rec = previous_manifest["files"].get(old_path)
            before = previous_tree.get(old_path)
            if old_rec is None or before is None:
                if rec["revision"] != "r1": raise ManifestError(f"{path}: new path must start at r1")
                if rec["updated"] != {"value": evidence.updated.value, "source": evidence.updated.source}:
                    raise ManifestError(f"{path}: new path updated evidence mismatch")
                _verify_recorded_updated(g, path, rec, entry, target_commit=subject_sha,
                    rule=rule, kind=kind, style=style)
                continue
            old_rule = p.classify(old_path)
            old_kind, old_style, _ = _native(old_rule, old_path)
            try:
                expected_revision = revision_for_window(old_rec["revision"], before, entry,
                    style=style or STYLES["slash"], handling=rule["handling"], file_kind=kind or "go",
                    identity_relation="verified-rename" if rename else "same")
            except GitInventoryError as exc:
                raise ManifestError(f"{path}: cannot prove revision window: {exc}") from exc
            if rec["revision"] != expected_revision:
                raise ManifestError(f"{path}: revision {rec['revision']} does not match snapshot-window result {expected_revision}")
            body_equal = _same_normalized_body(before, entry, rule, path)
            body_changed = not body_equal
            if is_self_manifest:
                expected_updated = {"value": manifest_self_updated().value, "source": manifest_self_updated().source}
            elif body_changed:
                expected_updated = {"value": evidence.updated.value, "source": evidence.updated.source}
            else:
                _verify_recorded_updated(g, old_path, old_rec, before, target_commit=base,
                    rule=old_rule, kind=old_kind, style=old_style)
                expected_updated = old_rec["updated"]
            if rec["updated"] != expected_updated:
                raise ManifestError(f"{path}: updated evidence does not match the snapshot-window rule")
            if body_changed and not is_self_manifest:
                _verify_recorded_updated(g, path, rec, entry, target_commit=subject_sha,
                    rule=rule, kind=kind, style=style)
    return {"status": "PASS" if verify_history else "NOT_RUN", "subject": subject_sha, "subject_tree": subject_tree, "baseline": g.commit_identity(base)[0], "baseline_count": len(baseline_tree), "subject_count": len(tree), "explicit_additions": len(additions), "history_verification": "PASS" if verify_history else "NOT_RUN"}

def _native(rule, path):
    if rule["handling"] != "header-and-manifest": return None, None, None
    name = path.rsplit("/", 1)[-1]
    kind = ("go" if name.endswith(".go") else "python" if name.endswith(".py") else
            "typescript" if name.endswith((".ts", ".tsx")) else "javascript" if name.endswith(".js") else
            "shell" if name.endswith(".sh") else "html" if name.endswith((".html", ".md")) else
            "css" if name.endswith(".css") else "toml" if name.endswith(".toml") else
            "makefile" if name == "Makefile" else "hash-config")
    style_name = FILE_KIND_STYLE.get(kind)
    if style_name is None: raise ManifestError(f"{path}: no approved native style mapping")
    return kind, STYLES[style_name], style_name

def build_candidate_manifest(repo, previous, schema, policy, *, baseline, subject, reviewed_additions):
    """Build in memory only; new paths need human-authored reviewed records."""
    g = GitRepo(repo)
    subject_sha, _ = g.require_baseline_ancestor(baseline, subject)
    anchored, _, _ = load_anchored_previous(repo, baseline)
    if previous != anchored:
        raise ManifestError("previous manifest object differs from canonical baseline Git blob")
    validate_schema(previous, schema)
    old_tree, new_tree = g.tree_inventory(baseline), g.tree_inventory(subject_sha)
    missing_old, extra_old = compare_manifest_coverage(old_tree, previous.get("files", {}))
    if missing_old or extra_old: raise ManifestError(f"previous manifest coverage mismatch; missing={missing_old[:8]}, extra={extra_old[:8]}")
    changes = g.diff(baseline, subject_sha)
    old_files = previous.get("files", {})
    p = Policy(policy)
    for old_path, old_record in old_files.items(): validate_record(old_path, old_record, p)
    result = {}
    for path, entry in new_tree.items():
        rule = p.classify(path)
        if rule is None: raise ManifestError(f"unclassified tracked path: {path}")
        renamed_from = next((c.old_path.decode("utf-8", "strict") for c in changes
                             if c.status.startswith("R") and c.new_path == entry.raw_path and c.old_path), None)
        prior_path = renamed_from or path
        prior = old_files.get(prior_path)
        identity_relation = "verified-rename" if renamed_from else "same"
        old_updated = None
        prior_revision = None
        if prior is None:
            prior = reviewed_additions.get(path)
            if prior is None: raise ManifestError(f"new tracked path needs reviewed purpose/attribution: {path}")
            rec = json.loads(json.dumps(prior))
            rec["revision"] = new_path_revision()
        else:
            rec = json.loads(json.dumps(prior))
            old_updated = json.loads(json.dumps(rec.get("updated")))
            prior_revision = rec.get("revision")
            before = old_tree.get(path)
            kind, style, _ = _native(rule, path)
            if renamed_from:
                before = old_tree.get(renamed_from)
                old_record = old_files.get(renamed_from)
                if before is None or old_record is None:
                    raise ManifestError(f"verified rename {renamed_from!r} -> {path!r} lacks prior record/snapshot")
                rec = json.loads(json.dumps(old_record))
            rec["revision"] = revision_for_window(rec["revision"], before, entry,
                style=style or STYLES["slash"], handling=rule["handling"], file_kind=kind or "go",
                identity_relation=identity_relation)
        rec["handling"] = rule["handling"]
        rec["header_state"] = {"header-and-manifest": "pending-header-application",
                                "manifest-only": "manifest-only",
                                "preserve-notice-and-manifest": "upstream-notice-preserved",
                                "generated-manifest-only": "generator-owned-output"}[rule["handling"]]
        if rule["id"] == "dockerfile-parser-directives":
            rec["exception"] = {"rule_id": rule["id"], "source": path,
                                "reason": f"{path} is registry-only because Docker parser directives must remain at byte zero until a parser-safe metadata insertion position is verified."}
        evidence = g.path_provenance(path, subject=subject_sha,
            style=_native(rule, path)[1], handling=rule["handling"], file_kind=_native(rule, path)[0])
        rec["created"] = {"value": evidence.created.value, "source": evidence.created.source}
        # A manifest-to-manifest net-equal window preserves updated even if
        # intermediate commits edited and later reverted the content.
        if prior_revision is None or not _same_normalized_body(before, entry, rule, path):
            rec["updated"] = {"value": evidence.updated.value, "source": evidence.updated.source}
        else:
            rec["updated"] = old_updated
        if path == "scripts/file_metadata/manifest.json":
            evidence = manifest_self_updated(path)
            rec["updated"] = {"value": evidence.value, "source": evidence.source}
            rec.setdefault("exception", {"path": path, "reason": evidence.source["reason"]})
        validate_record(path, rec, p)
        result[path] = rec
    inv = {
        "baseline_head": g.commit_identity(baseline)[0],
        "baseline_timestamp": g.run("show", "-s", "--format=%cI", g.commit_identity(baseline)[0]).decode("ascii").strip(),
        "tracked_file_count": len(old_tree),
        "explicit_additions": sorted(set(new_tree) - set(old_tree)),
        "excluded_untracked_paths": [x for x in previous["inventory"].get("excluded_untracked_paths", []) if x["path"] not in new_tree],
    }
    candidate = {"schema_version": previous["schema_version"], "inventory": inv, "files": result}
    validate_schema(candidate, schema)
    return candidate

def report(repo, manifest, schema, policy, subject, baseline=None, verify_history=False,
           previous_manifest=None, previous_manifest_path=None):
    summary = validate_manifest(repo, manifest, schema, policy, subject, baseline,
                                verify_history, previous_manifest, previous_manifest_path)
    print(json.dumps(summary, indent=2, sort_keys=True))
    return summary

def main(argv=None):
    ap = argparse.ArgumentParser(description="Read-only FILE metadata manifest plan/report prototype")
    sub = ap.add_subparsers(dest="command", required=True)
    for name in ("plan", "report"):
        sp = sub.add_parser(name)
        sp.add_argument("--repo", required=True, type=Path)
        sp.add_argument("--manifest", required=True, type=Path)
        sp.add_argument("--schema", required=True, type=Path)
        sp.add_argument("--policy", required=True, type=Path)
        sp.add_argument("--subject", required=True, help="explicit Git commit-ish; resolved once to canonical SHA")
        sp.add_argument("--baseline", help="optional explicit baseline override")
        sp.add_argument("--previous-manifest", type=Path, help="required with --verify-history; fixed baseline snapshot")
        sp.add_argument("--verify-history", action="store_true")
    args = ap.parse_args(argv)
    try:
        result = report(args.repo, load_json(args.manifest), load_json(args.schema), load_json(args.policy), args.subject,
                        args.baseline, args.verify_history, None, args.previous_manifest)
        if args.command == "plan": print("READ_ONLY_PLAN: no files will be changed")
        return 0
    except (ManifestError, GitInventoryError, MetadataError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1

if __name__ == "__main__": raise SystemExit(main())

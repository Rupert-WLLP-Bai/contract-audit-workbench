#!/usr/bin/env python3
"""Validate the canonical bilingual concept source and its optional views."""

from __future__ import annotations

import json
import re
import sys
import argparse
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "docs/glossary/concepts.json"
ID_PATTERN = re.compile(r"^T[0-9]{2,}$")
VIEW_ID_PATTERN = re.compile(r"\bT[0-9]{2,}\b")


def fail(message: str) -> None:
    print(f"FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def render_view(data: dict, audience: str) -> str:
    concepts = data["concepts"]
    user_view = audience == "user"
    title = "用户术语表" if user_view else "系统术语表"
    out = [
        f"# {title}",
        "",
        "> 本页由 `concepts.json` 生成；概念定义、实现状态和 ID 只在机器源中维护。",
        "",
        "| 概念 ID | 用户词 | 系统词 |",
        "| --- | --- | --- |",
    ]
    for concept in concepts:
        out.append(f"| {concept['id']} | {concept['user_term']} | `{concept['system_term']}` |")
    for concept in concepts:
        heading = concept["user_term"] if user_view else concept["system_term"]
        other_label = "系统映射" if user_view else "用户词"
        other_value = concept["system_term"] if user_view else concept["user_term"]
        out.extend(["", f"## {concept['id']} · {heading}", "", f"**{other_label}：** `{other_value}`", "", concept["definition"], "", "边界："])
        out.extend(f"- {item}" for item in concept.get("boundaries", []))
        for example in concept.get("misuse_examples", []):
            out.extend(["", f"**误用：** {example['incorrect']}", "", f"**原因：** {example['why_incorrect']}", "", f"**建议：** {example['preferred']}"])
        implementation = concept["implementation"]
        status_label = data["status_legend"][implementation["status"]]
        out.extend(["", f"**实现状态：** `{implementation['status']}` — {status_label}", "", implementation["note"], ""])
        if concept.get("code_anchors"):
            out.append("源码锚点：")
            out.extend(f"- `{anchor['symbol']}` — [`{anchor['path']}`](../../{anchor['path']})" for anchor in concept["code_anchors"])
        if concept.get("related_concepts"):
            out.extend(["", "相关概念：" + ", ".join(f"`{item}`" for item in concept["related_concepts"]), ""])
    return "\n".join(out).rstrip() + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write-views", action="store_true", help="regenerate the user and system reading views from concepts.json")
    args = parser.parse_args()
    try:
        data = json.loads(SOURCE.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read {SOURCE.relative_to(ROOT)}: {exc}")

    concepts = data.get("concepts")
    if not isinstance(concepts, list) or not concepts:
        fail("concepts must be a non-empty list")

    ids: list[str] = []
    for concept in concepts:
        if not isinstance(concept, dict):
            fail(f"concept entry must be an object: {concept!r}")
        concept_id = concept.get("id")
        if not isinstance(concept_id, str) or not ID_PATTERN.fullmatch(concept_id):
            fail(f"invalid concept id: {concept_id!r}")
        ids.append(concept_id)
        for field in ("user_term", "system_term", "definition"):
            if not isinstance(concept.get(field), str) or not concept[field].strip():
                fail(f"{concept_id}: {field} must be a non-empty string")
        boundaries = concept.get("boundaries")
        if not isinstance(boundaries, list) or not boundaries:
            fail(f"{concept_id}: boundaries must be a non-empty array of non-empty strings")
        for index, boundary in enumerate(boundaries):
            if not isinstance(boundary, str) or not boundary.strip():
                fail(f"{concept_id}: boundaries[{index}] must be a non-empty string")

        misuse_examples = concept.get("misuse_examples")
        if not isinstance(misuse_examples, list) or not misuse_examples:
            fail(f"{concept_id}: misuse_examples must be a non-empty array")
        for index, example in enumerate(misuse_examples):
            if not isinstance(example, dict):
                fail(f"{concept_id}: misuse_examples[{index}] must be an object")
            for field in ("incorrect", "why_incorrect", "preferred"):
                if not isinstance(example.get(field), str) or not example[field].strip():
                    fail(f"{concept_id}: misuse_examples[{index}].{field} must be a non-empty string")

        for field in ("code_anchors", "related_concepts"):
            values = concept.get(field, [])
            if not isinstance(values, list):
                fail(f"{concept_id}: {field} must be an array")
        for index, anchor in enumerate(concept.get("code_anchors", [])):
            if not isinstance(anchor, dict):
                fail(f"{concept_id}: code_anchors[{index}] must be an object")
            for field in ("path", "symbol"):
                if not isinstance(anchor.get(field), str) or not anchor[field].strip():
                    fail(f"{concept_id}: code_anchors[{index}].{field} must be a non-empty string")
        for index, related_id in enumerate(concept.get("related_concepts", [])):
            if not isinstance(related_id, str) or not related_id.strip():
                fail(f"{concept_id}: related_concepts[{index}] must be a non-empty string")

        implementation = concept.get("implementation", {})
        if implementation.get("status") not in data.get("status_legend", {}):
            fail(f"{concept_id}: unknown implementation status")
        for anchor in concept.get("code_anchors", []):
            relative_path = anchor.get("path", "")
            path = ROOT / relative_path
            if not path.is_file():
                fail(f"{concept_id}: code anchor file does not exist: {relative_path}")
            symbol = anchor.get("symbol", "")
            # Package and owner qualifiers are documentation context; the final
            # identifier should be present in the anchored source file.
            identifier = symbol.rsplit(".", 1)[-1]
            if identifier and identifier not in path.read_text(encoding="utf-8"):
                fail(f"{concept_id}: anchor symbol {symbol!r} not found in {relative_path}")

    if len(ids) != len(set(ids)):
        fail("concept IDs must be unique")
    known_ids = set(ids)
    for concept in concepts:
        for related_id in concept.get("related_concepts", []):
            if related_id not in known_ids:
                fail(f"{concept['id']}: related concept does not exist: {related_id}")
    for distinction in data.get("distinctions", []):
        for concept_id in distinction.get("concept_ids", []):
            if concept_id not in known_ids:
                fail(f"distinction references missing concept: {concept_id}")

    generated_views = data.get("generated_views", [])
    if args.write_views:
        if len(generated_views) != 2:
            fail("exactly two generated_views are required")
        for view_name, audience in zip(generated_views, ("user", "system"), strict=True):
            (ROOT / view_name).write_text(render_view(data, audience), encoding="utf-8")
    present_views = 0
    for view_name in generated_views:
        view = ROOT / view_name
        if not view.exists():
            continue
        present_views += 1
        view_ids = set(VIEW_ID_PATTERN.findall(view.read_text(encoding="utf-8")))
        if view_ids != known_ids:
            missing = sorted(known_ids - view_ids)
            extra = sorted(view_ids - known_ids)
            fail(f"{view_name}: concept IDs differ (missing={missing}, extra={extra})")

    if present_views == 0:
        print(f"PASS: {len(ids)} unique concepts, terms, relationships and source anchors validated; reading views are NOT_RUN")
    elif present_views != len(generated_views):
        print(f"PASS: {len(ids)} concepts and {present_views}/{len(generated_views)} reading views validated; remaining views are NOT_RUN")
    else:
        print(f"PASS: {len(ids)} concepts, source anchors and {present_views} reading views validated")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Check the bounded Manual Findings UI terminology pilot against the glossary."""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
POLICY = ROOT / "scripts/ui-terminology/policy.json"
CONCEPTS = ROOT / "docs/glossary/concepts.json"
PILOT_SCOPE = "web/src/features/review/manual-findings/manual-findings-panel.tsx"
EXPECTED_MAPPINGS = {
    "attempt": "T08",
    "approval_round": "T09",
    "manual_finding": "T37",
    "machine_finding": "T11",
    "human_finding_decision": "T12",
    "approval_decision": "T13",
}
EXPECTED_RULES = {
    "attempt-is-approval-round": {
        "concepts": ["attempt", "approval_round"],
        "mode": "must_not_equate",
        "patterns": [
            "(?:本次|当前)?执行尝试(?:是|即|属于|代表|作为)会签轮次",
            "会签轮次(?:是|即|代表)(?:本次|当前)?执行尝试",
        ],
    },
    "manual-finding-is-approval": {
        "concepts": ["manual_finding", "machine_finding", "human_finding_decision", "approval_decision"],
        "mode": "must_not_equate",
        "patterns": [
            "人工发现(?:是|即|属于|代表|作为)合同批准",
            "人工发现(?:是|即|属于|代表|作为)会签决定",
            "人工发现(?:是|即|属于|代表|作为)审批通过",
            "人工发现(?:是|即|属于|代表|作为)确认结论",
            "合同批准(?:是|即|代表)人工发现",
            "会签决定(?:是|即|代表)人工发现",
            "审批通过(?:是|即|代表)人工发现",
        ],
    },
}
EXPECTED_ADVISORIES = ["轮次", "发现处理意见"]


class GuardError(ValueError):
    """An incomplete, weakened, or ambiguous guard input."""


def _code_without_comments(source: str) -> str:
    """Remove JS comments while preserving quoted text and line positions."""
    out: list[str] = []
    i = 0
    quote: str | None = None
    while i < len(source):
        char = source[i]
        if quote:
            out.append(char)
            if char == "\\" and i + 1 < len(source):
                i += 1
                out.append(source[i])
            elif char == quote:
                quote = None
            i += 1
            continue
        if char in ("'", '"', chr(96)):
            quote = char
            out.append(char)
            i += 1
            continue
        if source.startswith("//", i):
            end = source.find("\n", i)
            if end < 0:
                break
            out.append("\n")
            i = end + 1
            continue
        if source.startswith("/*", i):
            end = source.find("*/", i + 2)
            if end < 0:
                raise GuardError("unterminated comment in pilot source")
            out.extend("\n" for ch in source[i:end + 2] if ch == "\n")
            i = end + 2
            continue
        out.append(char)
        i += 1
    return "".join(out)


def _matching_delimiter(source: str, start: int, opening: str, closing: str) -> int:
    if start >= len(source) or source[start] != opening:
        raise GuardError(f"expected {opening!r} at pilot component boundary")
    depth = 0
    quote: str | None = None
    i = start
    while i < len(source):
        char = source[i]
        if quote:
            if char == "\\":
                i += 2
                continue
            if char == quote:
                quote = None
        elif char in ("'", '"', chr(96)):
            quote = char
        elif char == opening:
            depth += 1
        elif char == closing:
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise GuardError(f"unbalanced {opening}{closing} in pilot component")


def _component_and_render(source: str) -> tuple[str, str]:
    clean = _code_without_comments(source)
    signatures = list(re.finditer(r"\bexport\s+function\s+ManualFindingsPanel\s*\(", clean))
    if len(signatures) != 1:
        raise GuardError("expected exactly one exported ManualFindingsPanel function")
    signature = signatures[0]
    params_open = clean.find("(", signature.start())
    params_close = _matching_delimiter(clean, params_open, "(", ")")
    body_open = clean.find("{", params_close)
    if body_open < 0:
        raise GuardError("ManualFindingsPanel function body is missing")
    body_close = _matching_delimiter(clean, body_open, "{", "}")
    component = clean[signature.start():body_close + 1]
    returns = list(re.finditer(r"\breturn\s*\(", component))
    if len(returns) != 1:
        raise GuardError("expected exactly one JSX return in ManualFindingsPanel")
    jsx_open = component.find("(", returns[0].start())
    jsx_close = _matching_delimiter(component, jsx_open, "(", ")")
    return component, component[jsx_open + 1:jsx_close]


def _jsx_copy(jsx: str) -> str:
    """Collect rendered JSX text, accessible text attributes, and literal branches."""
    text_nodes = re.findall(r">([^<>]+)<", jsx, flags=re.S)
    text_nodes = [re.sub(r"\s+", " ", item).strip() for item in text_nodes]
    text_nodes = [item for item in text_nodes if item]
    visible_attrs = re.findall(
        r"\b(?:aria-label|title|placeholder|alt)\s*=\s*\{?([\"'])(.*?)\1\}?",
        jsx, flags=re.S)
    literals = re.findall(r"(['\"])(.*?)\1", jsx, flags=re.S)
    return "\n".join(text_nodes + [value for _, value in visible_attrs] +
                      [value for _, value in literals])


def _manual_messages(component: str) -> str:
    values: list[str] = []
    quote_class = chr(96) + "'\""
    for name in ("setError", "setNotice"):
        expression = rf"\b{name}\(\s*([" + re.escape(quote_class) + r"])(.*?)\1\s*\)"
        for match in re.finditer(expression, component, re.S):
            values.append(match.group(2))
    return "\n".join(values)


def _button_elements(jsx: str) -> list[tuple[str, str]]:
    """Read this pilot's simple Button elements without treating expression > as tag end."""
    elements: list[tuple[str, str]] = []
    for start_match in re.finditer(r"<Button\b", jsx):
        i = start_match.start()
        quote: str | None = None
        brace_depth = 0
        while i < len(jsx):
            char = jsx[i]
            if quote:
                if char == "\\":
                    i += 2
                    continue
                if char == quote:
                    quote = None
            elif char in ("'", '"', chr(96)):
                quote = char
            elif char == "{":
                brace_depth += 1
            elif char == "}" and brace_depth:
                brace_depth -= 1
            elif char == ">" and brace_depth == 0:
                end_open = i + 1
                close = jsx.find("</Button>", end_open)
                if close < 0:
                    raise GuardError("unterminated Button in ManualFindingsPanel")
                elements.append((jsx[start_match.start():end_open], jsx[end_open:close]))
                break
            i += 1
        else:
            raise GuardError("unterminated Button opening tag in ManualFindingsPanel")
    return elements


def validate_policy(policy: object) -> dict:
    """Reject policy omissions or edits that weaken this bounded pilot guard."""
    if not isinstance(policy, dict):
        raise GuardError("policy must be an object")
    if set(policy) != {"schema_version", "scope", "canonical_concepts",
                       "definite_confusions", "uncertain_advisories"}:
        raise GuardError("policy has missing or unknown top-level fields")
    if type(policy["schema_version"]) is not int or policy["schema_version"] != 1:
        raise GuardError("unsupported policy schema_version")
    if policy["scope"] != PILOT_SCOPE:
        raise GuardError("policy scope must remain the Manual Findings pilot file")
    mappings = policy["canonical_concepts"]
    if not isinstance(mappings, dict) or mappings != EXPECTED_MAPPINGS:
        raise GuardError("canonical concept mapping is missing, changed, or widened")
    rules = policy["definite_confusions"]
    if not isinstance(rules, list) or len(rules) != len(EXPECTED_RULES):
        raise GuardError("policy must retain every required definite-confusion rule")
    seen: set[str] = set()
    for rule in rules:
        if not isinstance(rule, dict) or set(rule) != {"id", "concepts", "mode", "patterns"}:
            raise GuardError("each confusion rule must include its id, concepts, mode, and patterns")
        rule_id = rule["id"]
        if not isinstance(rule_id, str):
            raise GuardError("each confusion rule id must be a string")
        expected = EXPECTED_RULES.get(rule_id)
        if expected is None or rule_id in seen:
            raise GuardError(f"unknown or duplicate required rule: {rule_id!r}")
        if (not isinstance(rule["concepts"], list) or
                any(not isinstance(concept, str) for concept in rule["concepts"]) or
                rule["concepts"] != expected["concepts"] or
                not isinstance(rule["mode"], str) or rule["mode"] != expected["mode"]):
            raise GuardError(f"rule {rule_id} concepts or enforcement mode was weakened")
        patterns = rule["patterns"]
        if (not isinstance(patterns, list) or patterns != expected["patterns"] or
                any(not isinstance(pattern, str) or not pattern.strip() for pattern in patterns)):
            raise GuardError(f"rule {rule_id} patterns were removed or weakened")
        try:
            for pattern in patterns:
                re.compile(pattern)
        except re.error as exc:
            raise GuardError(f"rule {rule_id} has an invalid pattern: {exc}") from exc
        seen.add(rule_id)
    if seen != set(EXPECTED_RULES):
        raise GuardError("a required confusion rule is missing")
    advisories = policy["uncertain_advisories"]
    if not isinstance(advisories, list) or advisories != EXPECTED_ADVISORIES:
        raise GuardError("uncertain advisory list is missing, changed, or widened")
    return policy


def _canonical_terms(concepts_data: object, policy: dict) -> dict[str, str]:
    if not isinstance(concepts_data, dict) or not isinstance(concepts_data.get("concepts"), list):
        raise GuardError("canonical concepts source is malformed")
    concepts = {item.get("id"): item for item in concepts_data["concepts"]
                if isinstance(item, dict)}
    terms: dict[str, str] = {}
    for key, concept_id in policy["canonical_concepts"].items():
        concept = concepts.get(concept_id)
        if not isinstance(concept, dict):
            raise GuardError(f"policy maps {key} to missing canonical concept {concept_id}")
        term = concept.get("user_term")
        if not isinstance(term, str) or not term.strip():
            raise GuardError(f"canonical concept {concept_id} lacks a user term")
        terms[key] = term
    expected_terms = {"attempt": "执行尝试", "approval_round": "会签轮次",
                      "manual_finding": "人工发现"}
    for key, expected in expected_terms.items():
        if terms[key] != expected:
            raise GuardError(f"canonical concept {policy['canonical_concepts'][key]} changed unexpectedly")
    return terms


def inspect_copy(copy: str, policy: dict) -> tuple[list[str], list[str]]:
    """Return definite violations and context-sensitive advisory terms."""
    violations = []
    for rule in policy["definite_confusions"]:
        if any(re.search(pattern, copy) for pattern in rule["patterns"]):
            violations.append(rule["id"])
    advisories = [term for term in policy["uncertain_advisories"]
                  if term in copy and not (term == "轮次" and "会签轮次" in copy)]
    return sorted(set(violations)), advisories


def inspect_component(source: str, policy: dict, concepts_data: object) -> tuple[list[str], list[str]]:
    policy = validate_policy(policy)
    terms = _canonical_terms(concepts_data, policy)
    component, jsx = _component_and_render(source)
    copy = _jsx_copy(jsx) + "\n" + _manual_messages(component)
    heading = re.findall(
        r'<h2\s+id=["\']manual-findings-heading["\']\s*>([^<]*)</h2>', jsx)
    if heading != [terms["manual_finding"]]:
        raise GuardError("ManualFindingsPanel heading must use the T37 canonical term")
    empty_attempt = re.findall(r"!attemptId\s*\?\s*<p>([^<]*)</p>", jsx)
    if len(empty_attempt) != 1 or terms["attempt"] not in empty_attempt[0]:
        raise GuardError("missing-attempt UI slot must name the T08 execution attempt")
    if terms["approval_round"] in empty_attempt[0]:
        raise GuardError("missing-attempt UI slot must not call an attempt an approval round")
    primary_save = [children.strip() for opening, children in _button_elements(jsx)
                    if re.search(r'\bhtmlType="submit"', opening)]
    if primary_save != [f"保存{terms['manual_finding']}"]:
        raise GuardError("ManualFindingsPanel submit label must remain mapped to T37")
    if terms["attempt"] not in copy or terms["manual_finding"] not in copy:
        raise GuardError("pilot visible copy must use canonical attempt and Manual Finding terms")
    return inspect_copy(copy, policy)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args(argv)
    try:
        policy = validate_policy(json.loads(POLICY.read_text(encoding="utf-8")))
        concepts_data = json.loads(CONCEPTS.read_text(encoding="utf-8"))
        source = (ROOT / PILOT_SCOPE).read_text(encoding="utf-8")
        violations, advisories = inspect_component(source, policy, concepts_data)
    except (OSError, json.JSONDecodeError, GuardError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    if violations:
        print("FAIL: definite terminology confusion: " + ", ".join(violations), file=sys.stderr)
        return 1
    for term in advisories:
        print(f"ADVISORY: pilot copy contains context-sensitive wording {term!r}; review manually")
    print("PASS: bounded Manual Findings UI terminology matches canonical glossary concepts")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

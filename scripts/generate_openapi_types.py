#!/usr/bin/env python3
"""Generate the frontend's HTTP wire types from internal/apidocs/openapi.json.

The default roots are the schemas currently consumed by the frontend. References
are included transitively; --all also checks every component schema. --check
compares the deterministic output without writing it. No third-party package is
required. Types do not enforce format, length, pattern or numeric constraints;
the existing response readers and the server remain responsible for validation.

Run `python3 scripts/generate_openapi_types.py` after editing the contract, or
add `--check` to fail on drift. `--all --output PATH` can inspect all component
schemas without replacing the default frontend-only generated file.
"""

import argparse
import json
import math
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCHEMA = ROOT / "internal/apidocs/openapi.json"
OUTPUT = ROOT / "web/src/api/wire.generated.ts"
ROOTS = (
	"MembershipChoice", "PendingMembershipList", "PendingMembershipSelectionRequest",
	"Activity", "ArchiveRequest", "CaseProfile", "CaseProfileInput", "CaseRelationInput",
	"ContractAcceptance", "ContractAcceptanceDefect", "ContractAcceptanceDefectInput", "ContractAcceptanceList", "ContractAcceptanceRequest",
	"ContractSourceCapability",
	"ContractDelivery", "ContractDeliveryList", "ContractDeliveryRequest", "ContractFulfillmentItemSummary", "ContractRectification", "ContractRectificationRequest",
    "ContractPaymentEligibilityRequest", "ContractPaymentEligibility", "ContractPaymentReceiptRequest", "ContractPaymentReceipt", "ContractPaymentSummary",
    "ContractAssetList", "ContractAssetPlanSummary", "ContractAssetHandover", "ContractAssetReceipt",
    "ContractAssetHandoverRequest", "ContractAssetReceiptRequest",
    "ContractSigningCheckRequest", "ContractSigningCheckResult", "ManualSigningComparison", "ContractSigningRegistration",
    "ContractSigningRegistrationList", "ContractSigningRegistrationRequest", "ContractSupplement",
    "ContractSupplementList", "ContractSupplementRequest", "ContractSupplementResult",
    "CreateGenerationRequest", "CreateReportRequest", "Decision", "DecisionRequest",
    "EvidenceReceiptCreate", "EvidenceRequestCreate", "EvidenceRequestVerify",
    "ExperienceEvidenceRef", "ExperienceEvidenceView", "ExperienceFeedbackRequest",
    "FindingCounts", "KnowledgeInput", "LLMModelSelectionInput", "MaterialProjection",
    "MaterialSelectionProjection", "MaterialVersionProjection", "RemediationInput",
    "RemediationSubmitInput", "RemediationVerifyInput", "TextOffset", "WorkbenchTask",
    "WorkbenchTaskInput", "WorkbenchTaskUpdateInput",
)
# Runtime parsers consume these exact enum values, rather than maintaining a
# second list that could drift from the generated type.
ENUM_PATHS = (
    ("MaterialProjection", "legacy_status"),
    ("MaterialProjection", "purpose"),
    ("MaterialProjection", "role"),
    ("MaterialSelectionProjection", "status"),
    ("WorkbenchTask", "kind"),
    ("WorkbenchTask", "priority"),
    ("WorkbenchTask", "status"),
)

ANNOTATIONS = {
    "title", "description", "default", "example", "examples", "deprecated",
    "readOnly", "writeOnly", "format", "contentEncoding", "contentMediaType",
    "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
    "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems",
    "minProperties", "maxProperties",
}
KEYWORDS = ANNOTATIONS | {
    "$ref", "type", "nullable", "const", "enum", "properties", "required",
    "additionalProperties", "items", "allOf", "anyOf", "oneOf",
}
TYPE_NAMES = {"string", "number", "integer", "boolean", "null", "object", "array"}


class SchemaError(ValueError):
    pass


def quoted(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def literal(value, path):
    if value is None or isinstance(value, (str, bool, int)):
        return quoted(value)
    if isinstance(value, float) and math.isfinite(value):
        return quoted(value)
    raise SchemaError(f"{path}: enum/const must be a JSON scalar")


def value_kind(value):
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, str):
        return "string"
    if isinstance(value, (int, float)):
        return "number"
    return None


def matches_type(value, kinds):
    kind = value_kind(value)
    if kind == "number":
        if "number" in kinds:
            return True
        return "integer" in kinds and not isinstance(value, bool) and float(value).is_integer()
    return kind in kinds


class Generator:
    def __init__(self, document):
        if not isinstance(document, dict) or not str(document.get("openapi", "")).startswith("3."):
            raise SchemaError("expected an OpenAPI 3 document")
        components = document.get("components")
        self.schemas = components.get("schemas") if isinstance(components, dict) else None
        if not isinstance(self.schemas, dict) or not self.schemas:
            raise SchemaError("components.schemas must be a nonempty object")
        self.pending = set()
        self.aliases = {}
        self.current_references = None

    def reference(self, ref, path):
        prefix = "#/components/schemas/"
        if not isinstance(ref, str) or not ref.startswith(prefix):
            raise SchemaError(f"{path}: only local component schema references are supported")
        token = ref[len(prefix):]
        if not token or "/" in token or "~" in token.replace("~0", "").replace("~1", ""):
            raise SchemaError(f"{path}: invalid component reference {ref!r}")
        name = token.replace("~1", "/").replace("~0", "~")
        if name not in self.schemas:
            raise SchemaError(f"{path}: missing schema {name!r}")
        return name

    def resolve(self, schema, path, seen=None):
        # For oneOf exclusivity checks, dereference a plain reference only.
        seen = set() if seen is None else seen
        while isinstance(schema, dict) and "$ref" in schema:
            if set(schema) - (ANNOTATIONS | {"$ref"}):
                return schema
            name = self.reference(schema["$ref"], path)
            if name in seen:
                return schema
            seen.add(name)
            schema = self.schemas[name]
        return schema

    def kinds(self, schema, path):
        schema = self.resolve(schema, path)
        if not isinstance(schema, dict):
            return None
        kinds = schema.get("type")
        if isinstance(kinds, str):
            kinds = {kinds}
        elif isinstance(kinds, list) and all(isinstance(item, str) for item in kinds):
            kinds = set(kinds)
        elif "const" in schema:
            kinds = {value_kind(schema["const"])}
        elif isinstance(schema.get("enum"), list):
            kinds = {value_kind(item) for item in schema["enum"]}
        else:
            return None
        if None in kinds:
            return None
        if "integer" in kinds:
            kinds.remove("integer")
            kinds.add("number")
        if schema.get("nullable") is True:
            kinds.add("null")
        return kinds

    def literal_domain(self, schema, path):
        schema = self.resolve(schema, path)
        if not isinstance(schema, dict):
            return None
        values = [schema["const"]] if "const" in schema else schema.get("enum")
        if not isinstance(values, list) or not values or not all(value_kind(value) is not None for value in values):
            return None
        result = {literal(value, path) for value in values}
        if schema.get("nullable") is True:
            result.add("null")
        return result

    def discriminants(self, schema, path):
        schema = self.resolve(schema, path)
        if not isinstance(schema, dict) or schema.get("type") != "object":
            return {}
        properties = schema.get("properties", {})
        required = schema.get("required", [])
        if not isinstance(properties, dict) or not isinstance(required, list):
            return {}
        result = {}
        for key in required:
            values = self.literal_domain(properties.get(key), path)
            if values is not None:
                result[key] = values
        return result

    def exclusive(self, left, right, path):
        left_values, right_values = self.literal_domain(left, path), self.literal_domain(right, path)
        if left_values is not None and right_values is not None and left_values.isdisjoint(right_values):
            return True
        left_kinds, right_kinds = self.kinds(left, path), self.kinds(right, path)
        if left_kinds is None or right_kinds is None:
            return False
        overlap = left_kinds & right_kinds
        if not overlap:
            return True
        # A discriminator only excludes other objects, not a shared null or
        # another primitive admitted by either branch.
        if overlap != {"object"}:
            return False
        left_discriminants = self.discriminants(left, path)
        right_discriminants = self.discriminants(right, path)
        return any(left_discriminants[key].isdisjoint(right_discriminants[key])
                   for key in left_discriminants.keys() & right_discriminants.keys())

    def object_type(self, schema, path):
        properties = schema.get("properties", {})
        required = schema.get("required", [])
        additional = schema.get("additionalProperties", True)
        if not isinstance(properties, dict) or not all(isinstance(key, str) for key in properties):
            raise SchemaError(f"{path}: properties must be an object")
        if not isinstance(required, list) or not all(isinstance(key, str) for key in required):
            raise SchemaError(f"{path}: required must be a string array")
        if len(required) != len(set(required)) or set(required) - set(properties):
            raise SchemaError(f"{path}: required properties must be unique and declared")
        members = []
        for name in sorted(properties):
            optional = "" if name in required else "?"
            members.append(f"{quoted(name)}{optional}: {self.render(properties[name], path + '.' + name)};")
        if isinstance(additional, dict):
            if members:
                raise SchemaError(f"{path}: typed additionalProperties with declared properties cannot be represented exactly")
            # An inline index signature supports recursive aliases, whereas
            # the mapped-type alias Record<string, RecursiveValue> does not.
            return f"{{ [key: string]: {self.render(additional, path + '.*')}; }}"
        if not isinstance(additional, bool):
            raise SchemaError(f"{path}: invalid additionalProperties")
        if not members:
            return "Record<string, unknown>" if additional else "Record<string, never>"
        if additional:
            members.append("[key: string]: unknown;")
        return "{ " + " ".join(members) + " }"

    def primitive(self, kind, schema, path):
        if kind == "object":
            return self.object_type(schema, path)
        if kind == "array":
            if "items" not in schema:
                raise SchemaError(f"{path}: array items are required")
            return f"Array<{self.render(schema['items'], path + '[]')}>"
        return {"integer": "number", "number": "number", "string": "string",
                "boolean": "boolean", "null": "null"}[kind]

    def render(self, schema, path):
        if not isinstance(schema, dict) or not schema:
            raise SchemaError(f"{path}: unsupported or unconstrained schema")
        unknown = set(schema) - KEYWORDS
        if unknown:
            raise SchemaError(f"{path}: unsupported schema keyword(s): {', '.join(sorted(unknown))}")
        if "nullable" in schema and not isinstance(schema["nullable"], bool):
            raise SchemaError(f"{path}: nullable must be boolean")
        parts = []
        if "$ref" in schema:
            name = self.reference(schema["$ref"], path)
            self.pending.add(name)
            if self.current_references is not None:
                self.current_references.add(name)
            parts.append(self.aliases.get(name, f"WireSchemas[{quoted(name)}]"))
        base_index = len(parts)
        kind = schema.get("type")
        if kind is not None:
            kinds = [kind] if isinstance(kind, str) else kind
            if not isinstance(kinds, list) or not kinds or not all(isinstance(item, str) and item in TYPE_NAMES for item in kinds):
                raise SchemaError(f"{path}: unsupported type {kind!r}")
            if len(kinds) != len(set(kinds)):
                raise SchemaError(f"{path}: duplicate types")
            if any(key in schema for key in ("properties", "required", "additionalProperties")) and "object" not in kinds:
                raise SchemaError(f"{path}: object keywords require object type")
            if "items" in schema and "array" not in kinds:
                raise SchemaError(f"{path}: items requires array type")
            parts.append(" | ".join(self.primitive(item, schema, path) for item in kinds))
        elif any(key in schema for key in ("properties", "required", "additionalProperties", "items")):
            raise SchemaError(f"{path}: structural keywords require an explicit type")
        if "enum" in schema:
            values = schema["enum"]
            if not isinstance(values, list) or not values:
                raise SchemaError(f"{path}: enum must be a nonempty array")
            literals = [literal(value, path) for value in values]
            if len(literals) != len(set(literals)):
                raise SchemaError(f"{path}: duplicate enum values")
            if kind is not None:
                if not all(matches_type(value, kinds) or (value is None and schema.get("nullable") is True) for value in values):
                    raise SchemaError(f"{path}: enum value conflicts with type")
                parts[base_index] = " | ".join(literals)
            else:
                parts.append(" | ".join(literals))
        if "const" in schema:
            value = schema["const"]
            text = literal(value, path)
            if kind is not None and not matches_type(value, kinds) and not (value is None and schema.get("nullable") is True):
                raise SchemaError(f"{path}: const value conflicts with type")
            if "enum" in schema:
                if text not in literals:
                    raise SchemaError(f"{path}: const value conflicts with enum")
                parts[-1] = text
            elif kind is not None:
                parts[base_index] = text
            else:
                parts.append(text)
        for keyword in ("allOf", "anyOf", "oneOf"):
            if keyword not in schema:
                continue
            branches = schema[keyword]
            if not isinstance(branches, list) or not branches:
                raise SchemaError(f"{path}: {keyword} must be a nonempty array")
            if keyword == "oneOf":
                for index, left in enumerate(branches):
                    for right in branches[index + 1:]:
                        if not self.exclusive(left, right, path):
                            raise SchemaError(f"{path}: overlapping oneOf branches cannot be represented exactly")
            separator = " & " if keyword == "allOf" else " | "
            parts.append(separator.join(f"({self.render(branch, path + '.' + keyword)})" for branch in branches))
        if not parts:
            raise SchemaError(f"{path}: missing representable type, reference, enum, const or composition")
        # Intersections preserve simultaneous constraints such as type + enum.
        result = " & ".join(f"({part})" if " | " in part and len(parts) > 1 else part for part in parts)
        if schema.get("nullable") is True:
            result = f"({result}) | null"
        return result

    def generate(self, roots):
        self.pending = set(roots)
        self.aliases = {}
        rendered = {}
        references = {}
        while self.pending:
            name = min(self.pending)
            self.pending.remove(name)
            if name in rendered:
                continue
            if name not in self.schemas:
                raise SchemaError(f"missing root schema {name!r}")
            references[name] = set()
            self.current_references = references[name]
            rendered[name] = self.render(self.schemas[name], f"components.schemas.{name}")
        self.current_references = None
        # Recursive values need named aliases: indexing the enclosing
        # WireSchemas interface inside its own property annotation causes
        # TS2502, even when the recursion passes through an array or object.
        recursive = set()
        for name in references:
            reachable = list(references[name])
            visited = set()
            while reachable:
                target = reachable.pop()
                if target == name:
                    recursive.add(name)
                    break
                if target not in visited:
                    visited.add(target)
                    reachable.extend(references[target])
        self.aliases = {name: f"WireRecursiveSchema{index}"
                        for index, name in enumerate(sorted(recursive))}
        if self.aliases:
            rendered = {name: self.render(self.schemas[name], f"components.schemas.{name}")
                        for name in sorted(rendered)}
            self.pending.clear()
        lines = [
            "// Generated by scripts/generate_openapi_types.py from internal/apidocs/openapi.json.",
            "// Do not edit. Runtime readers and the server validate constraints that TypeScript cannot express.",
        ]
        lines.extend(f"type {self.aliases[name]} = {rendered[name]};" for name in sorted(self.aliases))
        lines.append("export interface WireSchemas {")
        lines.extend(f"  {quoted(name)}: {self.aliases.get(name, rendered[name])};" for name in sorted(rendered))
        lines.extend(["}", "", "export const wireEnums = {"])
        for name, field in sorted(ENUM_PATHS):
            schema = self.schemas.get(name)
            prop = schema.get("properties", {}).get(field) if isinstance(schema, dict) else None
            values = prop.get("enum") if isinstance(prop, dict) else None
            if not isinstance(values, list) or not values or not all(isinstance(value, str) for value in values):
                raise SchemaError(f"{name}.{field}: expected a nonempty string enum for runtime validation")
            if len(values) != len(set(values)):
                raise SchemaError(f"{name}.{field}: duplicate enum values")
            lines.append(f"  {quoted(name + '.' + field)}: [{', '.join(quoted(value) for value in values)}],")
        lines.extend(["} as const;", ""])
        return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema", type=Path, default=SCHEMA)
    parser.add_argument("--output", type=Path, default=OUTPUT)
    parser.add_argument("--all", action="store_true", help="include all component schemas")
    parser.add_argument("--check", action="store_true", help="fail if the output is missing or stale")
    args = parser.parse_args()
    try:
        with args.schema.open(encoding="utf-8") as stream:
            document = json.load(stream)
        generator = Generator(document)
        content = generator.generate(generator.schemas.keys() if args.all else ROOTS)
        if args.check:
            if not args.output.is_file() or args.output.read_text(encoding="utf-8") != content:
                raise SchemaError(f"{args.output}: generated wire types are missing or stale")
        else:
            args.output.write_text(content, encoding="utf-8")
    except (OSError, json.JSONDecodeError, SchemaError) as error:
        print(f"OpenAPI wire generation failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

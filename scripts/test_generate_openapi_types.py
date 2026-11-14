# File metadata (manifest mirror):
# Purpose: Exercises the public SDK-backed wire generator with recursive JSON types and schema-derived runtime enums while preserving negative TypeScript assertions.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-01
# Updated: 2026-11-12
# Revision: r2
# Copyright: See LICENSE
# End file metadata.

"""The public SDK-backed generator must preserve recursive wire value types."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GENERATOR = ROOT / "web/scripts/generate-openapi-types.ts"
TSC = ROOT / "web/node_modules/typescript/bin/tsc"
BUN = shutil.which(os.environ.get("BUN", "bun"))


@unittest.skipUnless(TSC.is_file() and BUN, "requires the installed Bun and TypeScript toolchain")
class RecursiveWireTests(unittest.TestCase):
    def test_generated_self_and_mutual_recursion_preserve_value_types(self):
        value_ref = {"$ref": "#/components/schemas/Value"}
        document = {
            "openapi": "3.1.0",
            "info": {"title": "recursive test", "version": "1"},
            "paths": {},
            "components": {
                "schemas": {
                    "Value": {
                        "oneOf": [
                            {"type": kind} for kind in ("string", "number", "boolean", "null")
                        ]
                        + [
                            {"type": "array", "items": value_ref},
                            {"type": "object", "additionalProperties": value_ref},
                        ]
                    },
                    "Branch": {
                        "anyOf": [
                            {"type": "string"},
                            {
                                "type": "object",
                                "additionalProperties": {
                                    "$ref": "#/components/schemas/BranchList",
                                },
                            },
                        ]
                    },
                    "BranchList": {
                        "type": "array",
                        "items": {
                            "$ref": "#/components/schemas/Branch",
                        },
                    },
                    "MaterialProjection": {
                        "type": "object",
                        "properties": {
                            "legacy_status": {
                                "type": "string",
                                "enum": ["native", "pending_confirmation"],
                            },
                            "purpose": {"type": "string", "enum": ["primary_contract", "other"]},
                            "role": {"type": "string", "enum": ["procurement", "attachment"]},
                        },
                    },
                    "MaterialSelectionProjection": {
                        "type": "object",
                        "properties": {
                            "status": {
                                "type": "string",
                                "enum": ["pending_confirmation", "confirmed"],
                            },
                        },
                    },
                    "WorkbenchTask": {
                        "type": "object",
                        "properties": {
                            "kind": {"type": "string", "enum": ["review", "decision"]},
                            "priority": {"type": "string", "enum": ["high", "low"]},
                            "status": {"type": "string", "enum": ["open", "completed"]},
                        },
                    },
                }
            },
        }
        with tempfile.TemporaryDirectory(prefix="caw-wire-types-") as directory:
            temporary = Path(directory)
            schema = temporary / "openapi.json"
            output = temporary / "wire.generated.ts"
            schema.write_text(json.dumps(document), encoding="utf-8")
            result = subprocess.run(
                [
                    BUN,
                    "run",
                    str(GENERATOR),
                    "--",
                    "--schema",
                    str(schema),
                    "--output",
                    str(output),
                    "--all",
                ],
                cwd=ROOT / "web",
                capture_output=True,
                text=True,
                timeout=60,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            content = output.read_text(encoding="utf-8")
            self.assertIn('"MaterialProjection.legacy_status"', content)
            self.assertIn('"pending_confirmation"', content)
            checks = """
const json: WireSchemas["Value"] = { nested: [null, "text", true, 42, { deeper: [] }] };
const mutual: WireSchemas["Branch"] = { children: ["leaf", { more: [] }] };
// @ts-expect-error undefined is not a JSON array item
const missing: WireSchemas["Value"] = [undefined];
// @ts-expect-error functions are not JSON object values
const callable: WireSchemas["Value"] = { nested: () => 1 };
// @ts-expect-error bigint is not a JSON number
const bigint: WireSchemas["Value"] = 1n;
// @ts-expect-error mutual recursive branches require lists, not numbers
const invalidBranch: WireSchemas["Branch"] = { children: 1 };
"""
            source = temporary / "recursive.ts"
            source.write_text(content + "\n" + checks, encoding="utf-8")
            result = subprocess.run(
                [
                    BUN,
                    str(TSC),
                    "--noEmit",
                    "--strict",
                    "--skipLibCheck",
                    "--target",
                    "ES2022",
                    str(source),
                ],
                capture_output=True,
                text=True,
                timeout=30,
            )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

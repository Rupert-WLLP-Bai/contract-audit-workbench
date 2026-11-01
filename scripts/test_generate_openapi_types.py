"""Generated recursive wire schemas must compile without admitting non-JSON values."""

import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import generate_openapi_types as wire

ROOT = Path(__file__).resolve().parents[1]
TSC = ROOT / "web/node_modules/typescript/bin/tsc"
JS_RUNTIME = shutil.which("bun") or shutil.which("node")


@unittest.skipUnless(TSC.is_file() and JS_RUNTIME, "requires the web TypeScript toolchain")
class RecursiveWireTests(unittest.TestCase):
    def test_generated_self_and_mutual_recursion_preserve_value_types(self):
        value_ref = {"$ref": "#/components/schemas/Value"}
        document = {
            "openapi": "3.1.0",
            "components": {"schemas": {
                "Value": {"oneOf": [
                    {"type": kind} for kind in ("string", "number", "boolean", "null")
                ] + [
                    {"type": "array", "items": value_ref},
                    {"type": "object", "additionalProperties": value_ref},
                ]},
                "Branch": {"anyOf": [
                    {"type": "string"},
                    {"type": "object", "additionalProperties": {
                        "$ref": "#/components/schemas/BranchList",
                    }},
                ]},
                "BranchList": {"type": "array", "items": {
                    "$ref": "#/components/schemas/Branch",
                }},
            }},
        }
        with patch.object(wire, "ENUM_PATHS", ()):
            content = wire.Generator(document).generate(["Value", "Branch"])
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
        with tempfile.TemporaryDirectory(prefix="caw-wire-types-") as directory:
            source = Path(directory) / "recursive.ts"
            source.write_text(content + checks, encoding="utf-8")
            result = subprocess.run(
                [JS_RUNTIME, str(TSC), "--noEmit", "--strict", "--skipLibCheck",
                 "--target", "ES2022", str(source)],
                capture_output=True, text=True, timeout=30,
            )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

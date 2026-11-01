"""Unit checks for the named direct-import policies."""
from __future__ import annotations

import copy
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import check_architecture as guard


class NamedPolicyTests(unittest.TestCase):
    def setUp(self):
        self.config, _, _ = guard.load_config()

    @staticmethod
    def records(*, gateway_imports=None, contract_imports=None):
        return [
            {
                "ImportPath": "contractaudit/internal/contractmoney",
                "GoFiles": ["decimal.go", "validate.go"],
                "Imports": ["errors", "fmt", "math/big", "strconv", "strings"],
                "TestImports": ["contractaudit/internal/store"],
            },
            {
                "ImportPath": "contractaudit/internal/modelgateway",
                "GoFiles": ["jev.go"],
                "Imports": gateway_imports or [
                    "contractaudit/internal/advisorycontract",
                    "contractaudit/internal/store",
                    "net/http",
                ],
            },
            {
                "ImportPath": "contractaudit/internal/advisorycontract",
                "GoFiles": ["contract.go"],
                "Imports": contract_imports or ["time"],
            },
        ]

    def inspect(self, records):
        report = {"policy_results": [], "violations": []}
        guard.inspect_policies(records, self.config, report)
        return report

    def test_all_policies_pass_and_store_direct_import_is_allowed(self):
        report = self.inspect(self.records())
        self.assertEqual([row["status"] for row in report["policy_results"]],
                         ["PASS", "PASS"])
        gateway = report["policy_results"][1]["checks"][0]
        self.assertIn("contractaudit/internal/store",
                      gateway["direct_production_imports"])
        self.assertEqual(report["violations"], [])

    def test_gateway_forbids_both_direct_orchestration_edges(self):
        report = self.inspect(self.records(gateway_imports=[
            "contractaudit/internal/agentflow/advisory",
            "contractaudit/internal/advisory/provider",
            "contractaudit/internal/store",
        ]))
        self.assertEqual(report["policy_results"][0]["status"], "PASS")
        self.assertEqual(report["policy_results"][1]["status"], "FAIL")
        self.assertEqual({row["to"] for row in report["violations"]}, {
            "contractaudit/internal/agentflow/advisory",
            "contractaudit/internal/advisory/provider",
        })

    def test_neutral_contract_allows_time_but_rejects_nonallowlisted_imports(self):
        report = self.inspect(self.records(contract_imports=[
            "time", "fmt", "contractaudit/internal/agentflow",
        ]))
        check = report["policy_results"][1]["checks"][1]
        self.assertEqual(check["allowed_direct_imports"], ["time"])
        self.assertEqual(set(check["forbidden_imports"]), {
            "fmt", "contractaudit/internal/agentflow",
        })

    def test_missing_or_empty_named_target_is_blocked(self):
        records = self.records()
        with self.assertRaises(guard.Blocked):
            guard.inspect_package(records[:-1], self.config["module"],
                                  self.config["policies"][1]["checks"][1])
        empty = copy.deepcopy(records)
        empty[-1]["GoFiles"] = []
        with self.assertRaisesRegex(guard.Blocked, "zero GoFiles"):
            guard.inspect_package(empty, self.config["module"],
                                  self.config["policies"][1]["checks"][1])

    def test_unknown_or_malformed_policy_is_blocked(self):
        malformed = copy.deepcopy(self.config)
        malformed["policies"][1]["checks"][0]["mode"] = "transitive"
        with self.assertRaisesRegex(guard.Blocked, "mode must be allowlist or forbid"):
            guard.validate_config(malformed)

    def test_required_policy_ids_targets_modes_and_edges_cannot_be_removed_or_weakened(self):
        mutations = []

        missing_policy = copy.deepcopy(self.config)
        missing_policy["policies"] = [missing_policy["policies"][0]]
        mutations.append(missing_policy)

        for check_id in ("modelgateway-no-orchestration-or-facade",
                         "advisorycontract-stdlib-only"):
            missing_check = copy.deepcopy(self.config)
            policy = missing_check["policies"][1]
            policy["checks"] = [check for check in policy["checks"]
                                if check["id"] != check_id]
            mutations.append(missing_check)

        changed_target = copy.deepcopy(self.config)
        changed_target["policies"][1]["checks"][0]["package"] = "internal/store"
        mutations.append(changed_target)

        changed_mode = copy.deepcopy(self.config)
        changed_mode["policies"][1]["checks"][0]["mode"] = "allowlist"
        mutations.append(changed_mode)

        weakened_deny = copy.deepcopy(self.config)
        weakened_deny["policies"][1]["checks"][0]["forbidden_direct_imports"].remove(
            "internal/agentflow")
        mutations.append(weakened_deny)

        weakened_allow = copy.deepcopy(self.config)
        weakened_allow["policies"][1]["checks"][1]["allowed_direct_imports"].append("fmt")
        mutations.append(weakened_allow)

        for malformed in mutations:
            with self.subTest(config=malformed):
                with self.assertRaises(guard.Blocked):
                    guard.validate_config(malformed)

    def test_schema_must_describe_both_active_rule_modes(self):
        schema_path = Path(guard.SCHEMA_PATH)
        original = json.loads(schema_path.read_text(encoding="utf-8"))
        for mutate in (
            lambda schema: schema["$defs"]["forbidCheck"]["required"].remove(
                "forbidden_direct_imports"),
            lambda schema: schema["properties"]["policies"].update(maxItems=1),
        ):
            schema = copy.deepcopy(original)
            mutate(schema)
            with self.assertRaisesRegex(guard.Blocked, "fixed structure"):
                guard.validate_schema_contract(schema)

    def test_cli_returns_blocked_for_removed_policy_and_unsupported_schema_change(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config_path = root / "architecture.json"
            schema_path = root / "architecture.schema.json"
            report_path = root / "report.json"
            valid_config = json.loads(Path(guard.CONFIG_PATH).read_text(encoding="utf-8"))
            valid_schema = json.loads(Path(guard.SCHEMA_PATH).read_text(encoding="utf-8"))

            removed_policy = copy.deepcopy(valid_config)
            removed_policy["policies"] = [removed_policy["policies"][0]]
            config_path.write_text(json.dumps(removed_policy), encoding="utf-8")
            with patch.object(guard, "CONFIG_PATH", config_path), patch.object(
                sys, "argv", ["check_architecture.py", "--fixture", "unused", "--out", str(report_path)]
            ):
                self.assertEqual(guard.main(), 2)
            self.assertEqual(json.loads(report_path.read_text())["status"], "BLOCKED")

            config_path.write_text(json.dumps(valid_config), encoding="utf-8")
            changed_schema = copy.deepcopy(valid_schema)
            changed_schema["properties"]["policies"]["maxItems"] = 1
            schema_path.write_text(json.dumps(changed_schema), encoding="utf-8")
            with patch.object(guard, "CONFIG_PATH", config_path), patch.object(
                guard, "SCHEMA_PATH", schema_path
            ), patch.object(
                sys, "argv", ["check_architecture.py", "--fixture", "unused", "--out", str(report_path)]
            ):
                self.assertEqual(guard.main(), 2)
            final_report = json.loads(report_path.read_text())
            self.assertEqual(final_report["status"], "BLOCKED")
            self.assertEqual(final_report["exit_code"], 2)


if __name__ == "__main__":
    unittest.main()

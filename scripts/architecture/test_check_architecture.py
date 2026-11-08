# File metadata (manifest mirror):
# Purpose: Pins named policy compatibility, new Store import rejection, domain subpackage boundaries and monotonic baseline behavior through deterministic architecture fixtures.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-01
# Updated: 2026-11-08
# Revision: r3
# Copyright: See LICENSE
# End file metadata.

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
                "ImportPath": "contractaudit/internal/coreauthority",
                "GoFiles": ["transport.go"],
                "Imports": [
                    "crypto/tls",
                    "crypto/x509",
                    "errors",
                    "net",
                    "net/http",
                    "net/url",
                    "os",
                    "strings",
                    "time",
                ],
            },
            {
                "ImportPath": "contractaudit/internal/advisorycontract",
                "GoFiles": ["contract.go"],
                "Imports": contract_imports or ["time"],
            },
        ]

    def with_ratchet(self, records, config=None):
        cfg = config or self.config
        completed = list(records)
        seen = {record["ImportPath"] for record in completed}
        for domain in cfg["ratchet"]["domains"]:
            for role in ("app", "transport", "postgres"):
                for path in domain[role]:
                    name = cfg["module"] + "/" + path
                    if name not in seen:
                        completed.append({"ImportPath": name, "GoFiles": ["fixture.go"], "Imports": []})
                        seen.add(name)
        return completed

    def inspect(self, records, config=None):
        report = {"policy_results": [], "violations": []}
        guard.inspect_policies(self.with_ratchet(records, config), config or self.config, report)
        return report

    def test_all_policies_pass_and_store_direct_import_is_allowed(self):
        report = self.inspect(self.records())
        self.assertEqual([row["id"] for row in report["policy_results"]], [
            "contractmoney-pure-domain",
            "advisory-contract-boundary",
            "core-authority-transport-boundary",
        ])
        self.assertEqual([row["status"] for row in report["policy_results"]],
                         ["PASS", "PASS", "PASS"])
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
        without_target = [record for record in records
                          if record["ImportPath"] != "contractaudit/internal/advisorycontract"]
        with self.assertRaises(guard.Blocked):
            guard.inspect_package(without_target, self.config["module"],
                                  self.config["policies"][1]["checks"][1])
        empty = copy.deepcopy(records)
        next(record for record in empty
             if record["ImportPath"] == "contractaudit/internal/advisorycontract")["GoFiles"] = []
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

    def test_v2_config_remains_readable(self):
        old = copy.deepcopy(self.config)
        old["schema_version"] = 2
        del old["ratchet"]
        guard.validate_config(old)

    def test_new_package_store_ratchet_and_exact_legacy_exception(self):
        records = self.records() + [{"ImportPath": "contractaudit/internal/newbusiness", "GoFiles": ["x.go"], "Imports": ["contractaudit/internal/store"]}]
        report = self.inspect(records)
        self.assertEqual([(v["check_id"], v["from"]) for v in report["violations"]], [("store-no-new-business-import", "contractaudit/internal/newbusiness")])
        records[-1]["Imports"] = ["fmt"]
        self.assertEqual(self.inspect(records)["violations"], [])
        self.assertEqual(self.inspect(records)["ratchet"]["store_guard"], "active for all internal packages except exact legacy importers")

    def test_registered_domains_enforce_both_postgres_directions_and_transport(self):
        self.config["ratchet"]["domains"] = [
            {"id": "alpha", "app": ["internal/alpha/app"], "transport": ["internal/alpha/transport"], "postgres": ["internal/alpha/postgres"]},
            {"id": "beta", "app": ["internal/beta/app"], "transport": ["internal/beta/transport"], "postgres": ["internal/beta/postgres"]},
        ]
        guard.validate_config(self.config)
        records = self.with_ratchet(self.records())
        by_name = {r["ImportPath"]: r for r in records}
        by_name["contractaudit/internal/alpha/postgres"]["Imports"] = ["contractaudit/internal/beta/postgres/query"]
        by_name["contractaudit/internal/beta/postgres"]["Imports"] = ["contractaudit/internal/alpha/postgres"]
        by_name["contractaudit/internal/alpha/transport"]["Imports"] = ["contractaudit/internal/alpha/app", "database/sql"]
        report = self.inspect(records)
        self.assertEqual([v["check_id"] for v in report["violations"]].count("cross-domain-postgres"), 2)
        self.assertIn("transport-no-sql", [v["check_id"] for v in report["violations"]])
        by_name["contractaudit/internal/alpha/postgres"]["Imports"] = []
        by_name["contractaudit/internal/beta/postgres"]["Imports"] = []
        by_name["contractaudit/internal/alpha/transport"]["Imports"] = ["contractaudit/internal/alpha/app"]
        self.assertEqual(self.inspect(records)["violations"], [])
        by_name["contractaudit/internal/alpha/transport"]["Imports"] = ["contractaudit/internal/beta/app"]
        self.assertEqual(self.inspect(records)["violations"][0]["check_id"], "transport-to-app")

    def test_ratchet_cannot_expand_legacy_exemptions(self):
        self.config["ratchet"]["legacy_store_importers"].append("internal/newbusiness")
        with self.assertRaises(guard.Blocked):
            guard.validate_config(self.config)

    def test_active_config_cannot_downgrade_to_v2(self):
        old = copy.deepcopy(self.config)
        old["schema_version"] = 2
        del old["ratchet"]
        with tempfile.TemporaryDirectory() as temporary:
            config = Path(temporary) / "architecture.json"
            config.write_text(json.dumps(old))
            with patch.object(guard, "CONFIG_PATH", config), self.assertRaisesRegex(guard.Blocked, "active configuration"):
                guard.load_config()

    def test_registered_prefixes_are_monotonic_and_disjoint(self):
        base = copy.deepcopy(self.config)
        base["ratchet"]["domains"] = [{"id": "alpha", "app": ["internal/alpha/app"], "transport": ["internal/alpha/transport"], "postgres": ["internal/alpha/postgres"]}]
        candidate = copy.deepcopy(base)
        guard.validate_base_ratchet(base, candidate)
        candidate["ratchet"]["domains"][0]["app"].clear()
        with self.assertRaises(guard.Blocked):
            guard.validate_base_ratchet(base, candidate)
        candidate = copy.deepcopy(base)
        candidate["ratchet"]["domains"][0]["app"].clear()
        candidate["ratchet"]["domains"][0]["postgres"].append("internal/alpha/app")
        with self.assertRaises(guard.Blocked):
            guard.validate_base_ratchet(base, candidate)
        candidate = copy.deepcopy(base)
        candidate["ratchet"]["domains"].append({"id": "beta", "app": ["internal/alpha/app/sub"], "transport": [], "postgres": []})
        with self.assertRaisesRegex(guard.Blocked, "disjoint"):
            guard.validate_config(candidate)
        legacy = copy.deepcopy(self.config)
        legacy["schema_version"] = 2
        del legacy["ratchet"]
        guard.validate_base_ratchet(legacy, base)
        with patch.object(guard, "run_command", side_effect=guard.Blocked("missing BASE")):
            with self.assertRaisesRegex(guard.Blocked, "missing BASE"):
                guard.load_base_ratchet(Path("/unused"), "origin/main", base)

    def test_legacy_exemptions_can_shrink_to_empty(self):
        base = copy.deepcopy(self.config)
        candidate = copy.deepcopy(base)
        candidate["ratchet"]["legacy_store_importers"] = []
        guard.validate_config(candidate)
        guard.validate_base_ratchet(base, candidate)
        report = self.inspect(self.records(), candidate)
        self.assertTrue(any(v["from"] == "contractaudit/internal/modelgateway" and
                            v["check_id"] == "store-no-new-business-import"
                            for v in report["violations"]))

    def test_removed_legacy_exemption_cannot_be_reintroduced(self):
        base = copy.deepcopy(self.config)
        base["ratchet"]["legacy_store_importers"].remove("internal/modelgateway")
        candidate = copy.deepcopy(self.config)
        with self.assertRaisesRegex(guard.Blocked, "cannot be reintroduced"):
            guard.validate_base_ratchet(base, candidate)

    def test_diverged_base_uses_target_registry_not_merge_base(self):
        base = copy.deepcopy(self.config)
        base["ratchet"]["domains"] = [{"id": "alpha", "app": ["internal/alpha/app"], "transport": [], "postgres": []}]
        candidate = copy.deepcopy(base)
        base_sha, merge_sha = "a" * 40, "b" * 40
        def git_output(argv, cwd):
            if argv[1] == "rev-parse":
                return base_sha + "\n"
            if argv[1] == "merge-base":
                self.assertEqual(argv[2:], [base_sha, "HEAD"])
                return merge_sha + "\n"
            if argv[1] == "show":
                self.assertEqual(argv[2], base_sha + ":scripts/architecture/architecture.json")
                return json.dumps(base)
            self.fail(f"unexpected command {argv}")
        with patch.object(guard, "run_command", side_effect=git_output):
            self.assertEqual(guard.load_base_ratchet(Path("/unused"), "target", candidate), (base_sha, merge_sha))
            candidate["ratchet"]["domains"] = []
            with self.assertRaisesRegex(guard.Blocked, "removed or reassigned"):
                guard.load_base_ratchet(Path("/unused"), "target", candidate)

    def test_prefix_ownership_applies_to_importer_and_target_subpackages(self):
        self.config["ratchet"]["domains"] = [
            {"id": "alpha", "app": ["internal/alpha/app"], "transport": ["internal/alpha/transport"], "postgres": ["internal/alpha/postgres"]},
            {"id": "beta", "app": ["internal/beta/app"], "transport": ["internal/beta/transport"], "postgres": ["internal/beta/postgres"]},
        ]
        records = self.records() + [{"ImportPath": "contractaudit/" + path, "GoFiles": ["x.go"], "Imports": []} for d in self.config["ratchet"]["domains"] for role in ("app", "transport", "postgres") for path in d[role]]
        records += [
            {"ImportPath": "contractaudit/internal/alpha/postgres/sub", "Imports": ["contractaudit/internal/beta/postgres/sub"]},
            {"ImportPath": "contractaudit/internal/beta/postgres/sub", "Imports": ["contractaudit/internal/alpha/postgres/sub"]},
            {"ImportPath": "contractaudit/internal/alpha/transport/http", "Imports": ["contractaudit/internal/beta/app/sub"]},
        ]
        found = self.inspect(records)["violations"]
        self.assertEqual([v["check_id"] for v in found].count("cross-domain-postgres"), 2)
        self.assertEqual([v["check_id"] for v in found].count("transport-to-app"), 1)
        records[-1]["Imports"] = ["contractaudit/internal/alpha/app/sub"]
        records[-2]["Imports"] = []
        records[-3]["Imports"] = []
        self.assertEqual(self.inspect(records)["violations"], [])

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

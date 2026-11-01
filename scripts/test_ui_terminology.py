import copy
import json
from pathlib import Path
import unittest

import check_ui_terminology


ROOT = Path(__file__).resolve().parents[1]


class UITerminologyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.policy = json.loads(
            (ROOT / "scripts/ui-terminology/policy.json").read_text(encoding="utf-8"))
        cls.concepts = json.loads(
            (ROOT / "docs/glossary/concepts.json").read_text(encoding="utf-8"))
        cls.source = (ROOT / cls.policy["scope"]).read_text(encoding="utf-8")

    def test_actual_panel_source_passes(self):
        self.assertEqual(check_ui_terminology.inspect_component(
            self.source, self.policy, self.concepts), ([], []))

    def test_definite_attempt_and_approval_confusions_fail(self):
        violations, advisories = check_ui_terminology.inspect_copy(
            "本次执行尝试是会签轮次。人工发现是合同批准。", self.policy)
        self.assertEqual(violations, ["attempt-is-approval-round",
                                      "manual-finding-is-approval"])
        self.assertEqual(advisories, [])

    def test_explicit_boundary_and_uncertain_wording_are_not_failures(self):
        violations, advisories = check_ui_terminology.inspect_copy(
            "人工发现不是合同批准；本次轮次的来源尚不明确。", self.policy)
        self.assertEqual(violations, [])
        self.assertEqual(advisories, ["轮次"])

    def test_actual_missing_attempt_slot_cannot_be_renamed_to_approval_round(self):
        mutated = self.source.replace(
            "请选择一次执行尝试。", "请选择会签轮次。", 1)
        self.assertNotEqual(mutated, self.source)
        with self.assertRaises(check_ui_terminology.GuardError):
            check_ui_terminology.inspect_component(mutated, self.policy, self.concepts)

    def test_actual_panel_heading_cannot_relabel_manual_finding_as_approval(self):
        mutated = self.source.replace(
            ">人工发现</h2>", ">合同批准</h2>", 1)
        self.assertNotEqual(mutated, self.source)
        with self.assertRaises(check_ui_terminology.GuardError):
            check_ui_terminology.inspect_component(mutated, self.policy, self.concepts)

    def test_policy_must_retain_scope_mappings_rules_patterns_and_modes(self):
        mutations = []
        deleted_rule = copy.deepcopy(self.policy)
        deleted_rule["definite_confusions"].pop()
        mutations.append(deleted_rule)
        empty_rules = copy.deepcopy(self.policy)
        empty_rules["definite_confusions"] = []
        mutations.append(empty_rules)
        empty_patterns = copy.deepcopy(self.policy)
        empty_patterns["definite_confusions"][0]["patterns"] = []
        mutations.append(empty_patterns)
        moved_scope = copy.deepcopy(self.policy)
        moved_scope["scope"] = "web/src/pages/admin.tsx"
        mutations.append(moved_scope)
        changed_mode = copy.deepcopy(self.policy)
        changed_mode["definite_confusions"][0]["mode"] = "advisory"
        mutations.append(changed_mode)
        changed_mapping = copy.deepcopy(self.policy)
        changed_mapping["canonical_concepts"]["attempt"] = "T09"
        mutations.append(changed_mapping)
        malformed_types = copy.deepcopy(self.policy)
        malformed_types["definite_confusions"][0]["id"] = ["attempt-is-approval-round"]
        mutations.append(malformed_types)
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                with self.assertRaises(check_ui_terminology.GuardError):
                    check_ui_terminology.validate_policy(mutation)

    def test_component_boundary_is_unique_and_unknown_cli_options_are_rejected(self):
        with self.assertRaises(check_ui_terminology.GuardError):
            check_ui_terminology._component_and_render(
                self.source + "\nexport function ManualFindingsPanel() { return (<div />); }\n")
        with self.assertRaises(SystemExit):
            check_ui_terminology.main(["--unknown-option"])

    def test_comments_outside_pilot_copy_and_admin_copy_are_not_scanned(self):
        source = (self.source + "\n// 会签轮次是人工发现；ManualFinding 是合同批准。\n"
                  "export const administrationCopy = '管理员看见轮次和租户';\n"
                  "export const technicalName = 'ApprovalRound manual_finding_records';\n")
        self.assertEqual(check_ui_terminology.inspect_component(
            source, self.policy, self.concepts), ([], []))


if __name__ == "__main__":
    unittest.main()

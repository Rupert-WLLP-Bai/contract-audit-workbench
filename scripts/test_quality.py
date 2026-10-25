"""The gate itself must reject missing evidence and low coverage."""
import unittest
import json
from check_coverage import parse_go, parse_web, scope_violations, violations
from verify import validate_go_test_results, validate_web_test_results, validate_agent_demo


class CoverageGateTest(unittest.TestCase):
    def test_duplicate_instrumentation_does_not_inflate_denominator(self):
        report = parse_go("mode: atomic\ncontractaudit/internal/example/a.go:1.1,2.2 3 0\n"
                          "contractaudit/internal/example/a.go:1.1,2.2 3 2\n"
                          "contractaudit/internal/example/a.go:4.1,5.2 1 0\n")
        self.assertEqual(report["statement_percent"], 75)
        self.assertEqual(report["total_statements"], 4)

    def test_empty_or_corrupt_reports_fail_closed(self):
        for text in ("", "mode: atomic\n", "mode: invalid", "mode: set\na:1.2 1 -1"):
            with self.assertRaises(ValueError):
                parse_go(text)
        with self.assertRaises(KeyError):
            parse_web({})

    def test_required_package_and_low_web_are_rejected(self):
        go = parse_go("mode: set\ncontractaudit/internal/example/a.go:1.1,2.2 1 1\n")
        web = parse_web({"total": {metric: {"covered": 7, "total": 10}
                                  for metric in ("lines", "branches", "functions", "statements")}})
        config = {"go": {"minimum_statement_percent": 80, "packages": {"missing": 80}},
                  "web": {f"minimum_{metric}_percent": 80 for metric in ("lines", "branches", "functions")}}
        self.assertEqual(len(violations(go, web, config)), 4)

    def test_missing_noncritical_backend_and_unimported_page_fail_scope(self):
        observed_go = {"contractaudit/internal/engine/rules.go"}
        expected_go = observed_go | {"contractaudit/internal/store/reviews.go"}
        observed_web = {"web/src/app.tsx"}
        expected_web = observed_web | {"web/src/pages/settings-page.tsx"}
        errors = scope_violations(observed_go, observed_web, expected_go, expected_web)
        self.assertEqual(len(errors), 2)
        self.assertTrue(any("store/reviews.go" in error for error in errors))
        self.assertTrue(any("settings-page.tsx" in error for error in errors))
        self.assertEqual(scope_violations(expected_go, expected_web, expected_go, expected_web), [])

    def test_unexpected_scope_also_fails(self):
        errors = scope_violations({"external/file.go"}, {"web/src/unexpected.ts"}, set(), set())
        self.assertEqual(len(errors), 2)


class TestExecutionEvidenceTest(unittest.TestCase):
    def test_agent_demo_missing_or_failed_evidence_cannot_pass(self):
        good = {"passed": True, "classification": "D0_SYNTHETIC_CONTROL_REGRESSION",
                "total": 1, "passed_count": 1, "results": [{"passed": True, "violations": []}]}
        self.assertEqual(validate_agent_demo(good)["synthetic_control_scenarios"], 1)
        for change in ({"results": []}, {"total": 0}, {"passed_count": 0},
                       {"classification": "real_accuracy"},
                       {"results": [{"passed": True, "violations": ["wrong_status"]}]}):
            with self.assertRaises(ValueError):
                validate_agent_demo(dict(good, **change))

    def go_log(self, *rows):
        return "\n".join(json.dumps(row) for row in rows)

    def test_go_requires_a_real_test_pass_not_just_package_exit(self):
        self.assertEqual(validate_go_test_results(self.go_log(
            {"Action": "pass", "Package": "example", "Test": "TestBehavior"}
        ))["test_pass_events"], 1)
        for text in ("", self.go_log({"Action": "pass", "Package": "example"}), "not json"):
            with self.assertRaises(ValueError):
                validate_go_test_results(text)

    def test_go_skip_or_fail_cannot_hide_behind_another_passing_test(self):
        for action in ("skip", "fail"):
            text = self.go_log({"Action": "pass", "Package": "example", "Test": "TestOne"},
                               {"Action": action, "Package": "example", "Test": "TestRequired"})
            with self.assertRaises(ValueError):
                validate_go_test_results(text)

    def test_testless_package_is_not_a_skipped_or_passing_test(self):
        no_tests = {"Action": "skip", "Package": "example/node_modules/dependency"}
        result = validate_go_test_results(self.go_log(
            {"Action": "pass", "Package": "example/internal/app", "Test": "TestBehavior"},
            no_tests,
        ))
        self.assertEqual(result["test_pass_events"], 1)
        self.assertEqual(result["packages_without_tests"], [no_tests["Package"]])
        with self.assertRaises(ValueError):
            validate_go_test_results(self.go_log(no_tests))
        with self.assertRaises(ValueError):
            validate_go_test_results(self.go_log(
                {"Action": "pass", "Package": "example", "Test": "TestBehavior"},
                {"Action": "fail", "Package": "example/broken"},
            ))

    def web_result(self):
        return {"success": True, "numTotalTests": 1, "numPassedTests": 1,
                "numFailedTests": 0, "numPendingTests": 0, "numTodoTests": 0,
                "numFailedTestSuites": 0,
                "testResults": [{"assertionResults": [{"status": "passed"}]}]}

    def test_web_requires_complete_assertion_evidence(self):
        self.assertEqual(validate_web_test_results(self.web_result())["tests"], 1)
        for status in ("pending", "skipped", "todo", "failed"):
            value = self.web_result()
            value["testResults"][0]["assertionResults"][0]["status"] = status
            with self.assertRaises(ValueError):
                validate_web_test_results(value)
        for key in ("numPendingTests", "numTodoTests", "numFailedTestSuites"):
            value = self.web_result()
            value[key] = 1
            with self.assertRaises(ValueError):
                validate_web_test_results(value)

    def test_web_empty_suite_or_missing_assertions_cannot_pass(self):
        value = self.web_result()
        value["numTotalTests"] = value["numPassedTests"] = 0
        with self.assertRaises(ValueError):
            validate_web_test_results(value)
        value = self.web_result()
        value["testResults"] = []
        with self.assertRaises(ValueError):
            validate_web_test_results(value)


if __name__ == "__main__":
    unittest.main()

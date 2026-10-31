"""Isolated contract tests for the local fault harness entry points."""
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("fault_test_local", ROOT / "deployments/scripts/fault-test-local.py")
FAULT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FAULT)


class FaultHarnessContractTest(unittest.TestCase):
    def test_scenario_selection_filters_and_rejects_unknown_names(self):
        scenarios = [("one", object()), ("two", object())]
        self.assertEqual([name for name, _ in FAULT.select_scenarios(["two"], scenarios)], ["two"])
        with self.assertRaisesRegex(ValueError, "missing"):
            FAULT.select_scenarios(["missing"], scenarios)

    def test_compose_profile_uses_private_project_and_optional_overlays(self):
        import deployments.local as local
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory) / "infra" / ".env"
            distributed = Path(directory) / "infra" / ".env.distributed"
            base.parent.mkdir()
            base.write_text("POSTGRES_PASSWORD=x\n", encoding="utf-8")
            distributed.write_text(
                "IDENTITY_DIRECTORY_ROOT=/private/identity\nOBSERVE=1\n", encoding="utf-8")
            with mock.patch.object(local, "ENV_PATH", base), \
                    mock.patch.object(local, "DISTRIBUTED_ENV_PATH", distributed), \
                    mock.patch.object(FAULT, "DISTRIBUTED_ENV_PATH", distributed), \
                    mock.patch.dict("os.environ", {"COMPOSE_BIN": "compose-stub", "COMPOSE_PROJECT_NAME": ""}, clear=False):
                prefix = FAULT.compose_prefix_for(distributed, "test")
            second_base = Path(directory) / "second" / ".env"
            second_base.parent.mkdir()
            second_base.write_text("POSTGRES_PASSWORD=y\n", encoding="utf-8")
            with mock.patch.object(local, "ENV_PATH", second_base), \
                    mock.patch.object(local, "DISTRIBUTED_ENV_PATH", distributed), \
                    mock.patch.dict("os.environ", {"COMPOSE_BIN": "compose-stub", "COMPOSE_PROJECT_NAME": ""}, clear=False):
                second_prefix = FAULT.local_compose_prefix(distributed)
            self.assertIn("contract-audit-infra-" + __import__("hashlib").sha256(str(base).encode()).hexdigest()[:8], prefix)
            self.assertNotEqual(prefix[prefix.index("-p") + 1], second_prefix[second_prefix.index("-p") + 1])
            self.assertIn(str(ROOT / "deployments/compose.identity.yaml"), prefix)
            self.assertIn(str(ROOT / "compose.observe.yaml"), prefix)
            self.assertIn("--profile", prefix)

    def test_fault_main_passes_database_target_and_refuses_other_profile(self):
        with tempfile.TemporaryDirectory() as directory:
            active = Path(directory) / ".env.distributed"
            active.write_text("A=B\n", encoding="utf-8")
            with mock.patch.object(FAULT, "DISTRIBUTED_ENV_PATH", active), \
                    mock.patch.object(FAULT, "load_journey") as load_journey, \
                    mock.patch.object(FAULT, "ROOT", Path(directory)), \
                    mock.patch.object(FAULT, "source_identity_module"):
                journey = load_journey.return_value
                journey.Failure = type("Failure", (Exception,), {})
                journey.require_isolated_stack.side_effect = journey.Failure("captured")
                result = FAULT.main(["--env", str(active), "--confirm", "contract_audit_test", "--database-target", "test",
                                     "--artifact-dir", str(Path(directory) / "artifacts")])
                self.assertEqual(result, 2)
                journey.require_isolated_stack.assert_called_once_with(active, "contract_audit_test", "test")
                self.assertEqual(FAULT.main(["--scenario", "not-a-scenario"]), 2)
                load_journey.assert_called_once()
            other = Path(directory) / "other.env"
            other.write_text("A=B\n", encoding="utf-8")
            with mock.patch.object(FAULT, "DISTRIBUTED_ENV_PATH", active):
                with self.assertRaises(FAULT.Failure):
                    FAULT.compose_prefix_for(other)


if __name__ == "__main__":
    unittest.main()

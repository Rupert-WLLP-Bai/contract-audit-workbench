"""Cover fixture model routing at the local deployment launcher boundary."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("deployment_local", ROOT / "deployments/local.py")
local = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(local)


class FixtureModelRoutingTests(unittest.TestCase):
    def test_fixture_startup_routes_workers_to_the_fixture_gateway(self):
        commands = []
        environment_at_start = []

        def record_run(command, **_kwargs):
            commands.append(command)
            environment_at_start.append(local.os.environ.get("MODEL_GATEWAY_URL"))

        with (
            patch.dict(local.os.environ, {"MODEL_GATEWAY_URL": "http://model-gateway:8084/v1"}, clear=True),
            patch.object(local, "read_env", side_effect=[{"APP_ENV": "development"}, {}]),
            patch.object(local, "compose_prefix", return_value=["docker", "compose"]),
            patch.object(local, "run", side_effect=record_run),
        ):
            local.start_apps(fixture=True, offline_images=False, planner_mode=None)

        self.assertEqual(len(commands), 2)
        app_start = commands[1]
        self.assertEqual(environment_at_start, [local.FIXTURE_MODEL_GATEWAY_URL] * 2)
        self.assertIn("--profile", app_start)
        profile_index = app_start.index("--profile")
        self.assertEqual(app_start[profile_index + 1], "model-fixture")
        self.assertIn("model-gateway-fixture", app_start)
        self.assertNotIn("model-gateway", app_start)


if __name__ == "__main__":
    unittest.main()

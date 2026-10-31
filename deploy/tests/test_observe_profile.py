"""Exercise the optional observation configuration and application lifecycle."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import yaml

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("deployment_local", ROOT / "deployments/local.py")
local = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(local)

OBSERVATION_SERVICES = {"otel-collector", "tempo", "prometheus", "grafana"}
APPLICATION_SERVICES = {
    "health-monitor",
    "gateway", "core-api", "document-service", "audit-execution", "audit-worker",
    "model-gateway", "model-gateway-fixture", "tool-gateway", "client", "admin", "portal",
}


class ObserveProfileTest(unittest.TestCase):
    def load_yaml(self, relative_path):
        return yaml.safe_load((ROOT / relative_path).read_text(encoding="utf-8"))

    def test_collector_keeps_debug_and_adds_query_exporters(self):
        collector = self.load_yaml("deploy/app/otel-collector.yaml")
        pipelines = collector["service"]["pipelines"]
        self.assertEqual(pipelines["traces"]["exporters"], ["debug", "otlp/tempo"])
        self.assertEqual(pipelines["metrics"]["exporters"], ["debug", "prometheus"])
        # No log backend ships, so the collector declares no logs pipeline;
        # container stdout read via docker/kubectl logs is the log capability.
        self.assertNotIn("logs", pipelines)
        self.assertEqual(collector["exporters"]["otlp/tempo"]["endpoint"], "tempo:4317")
        self.assertEqual(collector["exporters"]["prometheus"]["endpoint"], "0.0.0.0:8889")

    def test_observation_backends_and_query_routes_are_isolated_from_default_stack(self):
        application = self.load_yaml("compose.app.yaml")["services"]
        services = self.load_yaml("compose.observe.yaml")["services"]
        self.assertEqual(set(services), OBSERVATION_SERVICES)
        self.assertTrue(set(application).isdisjoint(OBSERVATION_SERVICES))
        for service in services.values():
            self.assertEqual(service["profiles"], ["otel"])
        for name, image in (
            ("tempo", "${TEMPO_IMAGE:-grafana/tempo:2.10.10}"),
            ("prometheus", "${PROMETHEUS_IMAGE:-prom/prometheus:v3.15.1}"),
            ("grafana", "${GRAFANA_IMAGE:-grafana/grafana:13.2.4}"),
        ):
            self.assertEqual(services[name]["image"], image)
        grafana_credential = services["grafana"]["environment"]["GF_SECURITY_ADMIN_PASSWORD"]
        self.assertTrue(grafana_credential.startswith("${GRAFANA_ADMIN_PASSWORD:?"))
        prometheus = self.load_yaml("deploy/app/observe/prometheus.yml")
        scrape = next(item for item in prometheus["scrape_configs"] if item["job_name"] == "otel-collector")
        self.assertEqual(scrape["static_configs"], [{"targets": ["otel-collector:8889"]}])
        tempo = self.load_yaml("deploy/app/observe/tempo.yaml")
        self.assertEqual(tempo["storage"]["trace"]["backend"], "local")
        datasources = {
            item["uid"]: item for item in self.load_yaml("deploy/app/observe/grafana-datasources.yaml")["datasources"]
        }
        self.assertEqual(datasources["prometheus"]["url"], "http://prometheus:9090")
        self.assertEqual(datasources["tempo"]["url"], "http://tempo:3200")
        self.assertEqual(datasources["tempo"]["jsonData"]["tracesToMetrics"]["datasourceUid"], "prometheus")


class ObservationLifecycleTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        directory = Path(temporary.name)
        self.infrastructure_env = directory / ".env"
        self.distributed_env = directory / ".env.distributed"
        self.infrastructure_env.write_text(
            "APP_ENV=development\n"
            "DEV_DATABASE_URL=postgres://localhost/contract_audit_dev\n"
            "TEST_DATABASE_URL=postgres://localhost/contract_audit_test\n",
            encoding="utf-8",
        )
        self.distributed_env.write_text("", encoding="utf-8")
        patches = (
            patch.object(local, "ENV_PATH", self.infrastructure_env),
            patch.object(local, "DISTRIBUTED_ENV_PATH", self.distributed_env),
            patch.object(local, "SUBPROCESS_RECORDS", []),
            patch.dict(local.os.environ, {"COMPOSE_BIN": "compose-under-test"}, clear=True),
            patch.object(local.subprocess, "run", side_effect=self.complete_process),
        )
        for item in patches:
            item.start()
            self.addCleanup(item.stop)

    @staticmethod
    def complete_process(command, **kwargs):
        # Keep the launcher and its command recording real; only Docker and
        # infrastructure subprocesses are replaced at the process boundary.
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    def compose_commands(self, action):
        return [
            record["argv"] for record in local.SUBPROCESS_RECORDS
            if record["argv"][0] == "compose-under-test" and action in record["argv"]
        ]

    def assert_observation_profile(self, command, enabled):
        overlay = str(ROOT / "compose.observe.yaml")
        profiles = [command[index + 1] for index, argument in enumerate(command[:-1]) if argument == "--profile"]
        if enabled:
            self.assertIn(overlay, command)
            self.assertIn("otel", profiles)
        else:
            self.assertNotIn(overlay, command)
            self.assertNotIn("otel", profiles)

    def test_enabled_observation_services_start_for_real_and_fixture_models(self):
        for value in ("1", " true ", "YES"):
            for fixture in (False, True):
                with self.subTest(observe=value, fixture=fixture):
                    local.SUBPROCESS_RECORDS.clear()
                    local.os.environ["OBSERVE"] = value
                    local.start_apps(fixture, False, None)
                    commands = self.compose_commands("up")
                    self.assertEqual(len(commands), 1)
                    command = commands[0]
                    model = "model-gateway-fixture" if fixture else "model-gateway"
                    expected = APPLICATION_SERVICES - {"model-gateway", "model-gateway-fixture"} | {model} | OBSERVATION_SERVICES
                    selected = command[command.index("180") + 1:]
                    self.assertEqual(set(selected), expected)
                    self.assertEqual(len(selected), len(expected))
                    self.assert_observation_profile(command, True)
                    migration = self.compose_commands("run")
                    self.assertEqual(len(migration), 1)
                    self.assertEqual(migration[0][-1], "migrate")
                    self.assertTrue(set(migration[0]).isdisjoint(OBSERVATION_SERVICES))

    def test_enabled_observation_services_use_offline_image_policy(self):
        local.os.environ["OBSERVE"] = "1"
        local.start_apps(False, True, None)
        command, = self.compose_commands("up")
        self.assertTrue(OBSERVATION_SERVICES.issubset(command))
        self.assertIn("--no-build", command)
        self.assertEqual(command[command.index("--pull") + 1], "never")
        self.assertNotIn("--build", command)

    def test_enabled_observation_services_stop_with_applications(self):
        local.os.environ["OBSERVE"] = "1"
        local.stop_apps()
        command, = self.compose_commands("stop")
        selected = command[command.index("stop") + 1:]
        self.assertEqual(set(selected), APPLICATION_SERVICES | OBSERVATION_SERVICES)
        self.assertEqual(len(selected), len(APPLICATION_SERVICES | OBSERVATION_SERVICES))
        self.assert_observation_profile(command, True)

    def test_disabled_observation_does_not_select_services_or_require_overlay(self):
        for value in (None, "", "0", "false", "no"):
            for fixture in (False, True):
                with self.subTest(observe=value, fixture=fixture):
                    local.SUBPROCESS_RECORDS.clear()
                    if value is None:
                        local.os.environ.pop("OBSERVE", None)
                    else:
                        local.os.environ["OBSERVE"] = value
                    local.start_apps(fixture, False, None)
                    local.stop_apps()
                    for action in ("up", "stop"):
                        command, = self.compose_commands(action)
                        self.assertTrue(set(command).isdisjoint(OBSERVATION_SERVICES))
                        self.assert_observation_profile(command, False)

    def test_observation_flag_uses_dotenv_files_and_environment_precedence(self):
        for infrastructure, distributed, environment, enabled in (
            ("1", None, None, True),
            ("0", "true", None, True),
            ("1", "0", None, False),
            ("1", "yes", "0", False),
            ("0", "false", "1", True),
        ):
            with self.subTest(infrastructure=infrastructure, distributed=distributed, environment=environment):
                local.SUBPROCESS_RECORDS.clear()
                self.infrastructure_env.write_text(f"APP_ENV=development\nOBSERVE={infrastructure}\n", encoding="utf-8")
                self.distributed_env.write_text("" if distributed is None else f"OBSERVE={distributed}\n", encoding="utf-8")
                local.os.environ.pop("OBSERVE", None)
                if environment is not None:
                    local.os.environ["OBSERVE"] = environment
                local.start_apps(False, False, None)
                local.stop_apps()
                for action in ("up", "stop"):
                    command, = self.compose_commands(action)
                    self.assertEqual(OBSERVATION_SERVICES.intersection(command), OBSERVATION_SERVICES if enabled else set())
                    self.assert_observation_profile(command, enabled)

    def test_infrastructure_lifecycle_does_not_take_ownership_of_observation(self):
        local.os.environ["OBSERVE"] = "1"
        local.start_infrastructure(False)
        local.stop_infrastructure(False)
        for action in ("up", "stop"):
            command, = self.compose_commands(action)
            self.assertEqual(command[-1], "nats")
            self.assertTrue(set(command).isdisjoint(OBSERVATION_SERVICES))


class ExperienceDeliveryProfileTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        directory = Path(temporary.name)
        self.infrastructure_env = directory / ".env"
        self.distributed_env = directory / ".env.distributed"
        self.infrastructure = {
            "POSTGRES_PASSWORD": "p" * 40,
            "DEV_DATABASE_URL": "postgres://localhost/contract_audit_dev",
            "TEST_DATABASE_URL": "postgres://localhost/contract_audit_test",
        }
        self.infrastructure_env.write_text(
            "".join(f"{key}={value}\n" for key, value in self.infrastructure.items()), encoding="utf-8"
        )
        for item in (
            patch.object(local, "ENV_PATH", self.infrastructure_env),
            patch.object(local, "DISTRIBUTED_ENV_PATH", self.distributed_env),
            patch.object(local, "infra_bootstrap"),
            patch.dict(local.os.environ, {"EV02_DATABASE_TARGET": "development"}, clear=True),
        ):
            item.start()
            self.addCleanup(item.stop)
        self.values = local.generated_values(self.infrastructure)

    def write_profile(self, values):
        self.distributed_env.write_text(
            "".join(f"{key}={value}\n" for key, value in values.items()), encoding="utf-8"
        )
        self.distributed_env.chmod(0o600)

    def test_new_profile_is_complete_and_reused_without_rewriting(self):
        values = local.ensure_private_environment()
        self.assertEqual(values["CORE_SERVICE_URL"], "http://core-api:8081")
        self.assertGreaterEqual(len(values["WORKER_CORE_TOKEN"]), 32)
        before = self.distributed_env.read_bytes()
        self.assertEqual(self.distributed_env.stat().st_mode & 0o777, 0o600)
        self.assertEqual(local.ensure_private_environment(), values)
        self.assertEqual(self.distributed_env.read_bytes(), before)

    def test_legacy_profile_requires_manual_upgrade_without_rewriting(self):
        for missing in ("WORKER_CORE_TOKEN", "CORE_SERVICE_URL"):
            with self.subTest(missing=missing):
                values = dict(self.values)
                del values[missing]
                self.write_profile(values)
                before = self.distributed_env.read_bytes()
                with self.assertRaisesRegex(RuntimeError, missing):
                    local.ensure_private_environment()
                self.assertEqual(self.distributed_env.read_bytes(), before)

    def test_incomplete_or_reused_delivery_credentials_are_rejected(self):
        for key, value, expected in (
            ("WORKER_CORE_TOKEN", "", "WORKER_CORE_TOKEN"),
            ("WORKER_CORE_TOKEN", "short", "WORKER_CORE_TOKEN"),
            ("WORKER_CORE_TOKEN", self.values["WORKER_TOOL_TOKEN"], "WORKER_TOOL_TOKEN"),
            ("WORKER_CORE_TOKEN", self.values["GATEWAY_CORE_TOKEN"], "GATEWAY_CORE_TOKEN"),
            ("CORE_SERVICE_URL", "", "CORE_SERVICE_URL"),
            ("CORE_SERVICE_URL", "http://core-api:8081/?query=1", "CORE_SERVICE_URL"),
        ):
            with self.subTest(key=key, expected=expected):
                values = dict(self.values)
                values[key] = value
                self.write_profile(values)
                before = self.distributed_env.read_bytes()
                with self.assertRaisesRegex(ValueError, expected):
                    local.ensure_private_environment()
                self.assertEqual(self.distributed_env.read_bytes(), before)

    def test_process_environment_overrides_cannot_reuse_worker_credentials(self):
        self.write_profile(self.values)
        with patch.dict(local.os.environ, {"WORKER_CORE_TOKEN": self.values["WORKER_MODEL_TOKEN"]}):
            with self.assertRaisesRegex(ValueError, "WORKER_MODEL_TOKEN"):
                local.ensure_private_environment()


if __name__ == "__main__":
    unittest.main()

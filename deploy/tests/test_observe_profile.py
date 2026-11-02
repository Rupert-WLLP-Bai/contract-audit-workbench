"""Exercise the optional observation configuration and application lifecycle."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
import shutil
from urllib.parse import urlsplit
from unittest.mock import patch

import yaml

ROOT = Path(__file__).resolve().parents[2]
LEGACY_85B_PROFILE_KEYS = (
    # Exact generated_values key set at 85b967aff3c499236b047483521087eba693f812.
    "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL",
    "TOOL_DATABASE_URL", "MIGRATOR_DATABASE_URL", "GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN",
    "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN", "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN",
    "WORKER_TOOL_TOKEN", "WORKER_CORE_TOKEN", "CORE_DOCUMENT_TOKEN", "DOCUMENT_MODEL_TOKEN",
    "CORE_URLS", "DOCUMENT_URL", "EXECUTION_URL", "TOOL_URLS", "DOCUMENT_SERVICE_URL",
    "CORE_SERVICE_URL", "MODEL_GATEWAY_URL", "NATS_URL", "LLM_BASE_URL", "EDGE_PORT",
    "CLIENT_PORT", "ADMIN_PORT", "PORTAL_PORT", "CORS_ORIGIN",
)
SPEC = importlib.util.spec_from_file_location("deployment_local", ROOT / "deployments/local.py")
local = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(local)
CONFORMANCE_SPEC = importlib.util.spec_from_file_location(
    "deployment_manifest_conformance", ROOT / "deployments/scripts/manifest-conformance.py"
)
conformance = importlib.util.module_from_spec(CONFORMANCE_SPEC)
CONFORMANCE_SPEC.loader.exec_module(conformance)
K3D_SPEC = importlib.util.spec_from_file_location("deployment_k3d", ROOT / "deployments/k3d.py")
k3d = importlib.util.module_from_spec(K3D_SPEC)
K3D_SPEC.loader.exec_module(k3d)

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
                    self.assertEqual(len(migration), 4)
                    self.assertEqual([command[-1] for command in migration], ["migrate-offline-migrator", "migrate-clm-owner", "migrate-auth-owner", "migrate"])
                    self.assertTrue(all(set(command).isdisjoint(OBSERVATION_SERVICES) for command in migration))

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

    def test_context_switch_defaults_false_and_accepts_explicit_boolean_only(self):
        self.assertEqual(self.values["CONTEXT_SWITCH_ENABLED"], "false")
        enabled = local.generated_values({**self.infrastructure, "CONTEXT_SWITCH_ENABLED": "TRUE"})
        self.assertEqual(enabled["CONTEXT_SWITCH_ENABLED"], "true")
        with self.assertRaisesRegex(ValueError, "CONTEXT_SWITCH_ENABLED must be a valid boolean"):
            local.generated_values({**self.infrastructure, "CONTEXT_SWITCH_ENABLED": "enabled"})

    def write_profile(self, values):
        self.distributed_env.write_text(
            "".join(f"{key}={value}\n" for key, value in values.items()), encoding="utf-8"
        )
        self.distributed_env.chmod(0o600)

    def test_new_profile_is_complete_and_reused_without_rewriting(self):
        values = local.ensure_private_environment()
        self.assertEqual(values["CORE_SERVICE_URL"], "http://core-api:8081")
        self.assertGreaterEqual(len(values["WORKER_CORE_TOKEN"]), 32)
        self.assertGreaterEqual(len(values["TOOL_CORE_AUTHORITY_TOKEN"]), 32)
        self.assertGreaterEqual(len(values["CORE_MODEL_AUTHORITY_TOKEN"]), 32)
        self.assertGreaterEqual(len(values["AUTH_ADMIN_DATABASE_URL"]), 32)
        service_tokens = [value for key, value in values.items() if key.endswith("_TOKEN")]
        self.assertEqual(len(service_tokens), len(set(service_tokens)))
        before = self.distributed_env.read_bytes()
        self.assertEqual(self.distributed_env.stat().st_mode & 0o777, 0o600)
        self.assertEqual(local.ensure_private_environment(), values)
        self.assertEqual(self.distributed_env.read_bytes(), before)

    def test_existing_local_profile_rotates_only_its_migrator_login(self):
        values = dict(self.values)
        legacy_admin = "postgres://contractaudit:old-private-password@db:5432/contract_audit_dev?sslmode=disable"
        values["AUTH_ADMIN_DATABASE_URL"] = legacy_admin
        values["MIGRATOR_DATABASE_URL"] = legacy_admin
        values["DEV_DATABASE_URL"] = legacy_admin
        self.write_profile(values)
        upgraded = local.ensure_private_environment()
        self.assertEqual(upgraded["AUTH_ADMIN_DATABASE_URL"], legacy_admin)
        self.assertEqual(upgraded["MIGRATOR_DATABASE_URL"], upgraded["DEV_DATABASE_URL"])
        self.assertEqual(urlsplit(upgraded["MIGRATOR_DATABASE_URL"]).username, "audit_offline_migrator")
        for key in set(values) - {"AUTH_ADMIN_DATABASE_URL", "MIGRATOR_DATABASE_URL", "DEV_DATABASE_URL"}:
            self.assertEqual(upgraded[key], values[key])
        self.assertEqual(self.distributed_env.stat().st_mode & 0o777, 0o600)

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

    def test_exact_85b_profile_gets_only_allowlisted_runtime_keys_and_offline_migrator(self):
        legacy = {key: self.values[key] for key in LEGACY_85B_PROFILE_KEYS}
        legacy_admin = "postgres://contractaudit:old-private-password@db:5432/contract_audit_dev?sslmode=disable"
        legacy["MIGRATOR_DATABASE_URL"] = legacy_admin
        self.write_profile(legacy)
        upgraded = local.ensure_private_environment()
        self.assertEqual(upgraded["AUTH_ADMIN_DATABASE_URL"], legacy_admin)
        self.assertEqual(urlsplit(upgraded["MIGRATOR_DATABASE_URL"]).username, "audit_offline_migrator")
        self.assertEqual(upgraded["DEV_DATABASE_URL"], upgraded["MIGRATOR_DATABASE_URL"])
        self.assertEqual(upgraded["TEST_DATABASE_URL"], "")
        self.assertEqual(upgraded["PROD_DATABASE_URL"], "")
        for key in set(LEGACY_85B_PROFILE_KEYS) - {"MIGRATOR_DATABASE_URL"}:
            self.assertEqual(upgraded[key], legacy[key])
        self.assertEqual(self.distributed_env.stat().st_mode & 0o777, 0o600)
        once = self.distributed_env.read_bytes()
        self.assertEqual(local.ensure_private_environment(), upgraded)
        self.assertEqual(self.distributed_env.read_bytes(), once)

    def test_incomplete_or_reused_delivery_credentials_are_rejected(self):
        for key, value, expected in (
            ("WORKER_CORE_TOKEN", "", "WORKER_CORE_TOKEN"),
            ("WORKER_CORE_TOKEN", "short", "WORKER_CORE_TOKEN"),
            ("WORKER_CORE_TOKEN", self.values["WORKER_TOOL_TOKEN"], "WORKER_TOOL_TOKEN"),
            ("WORKER_CORE_TOKEN", self.values["GATEWAY_CORE_TOKEN"], "GATEWAY_CORE_TOKEN"),
            ("TOOL_CORE_AUTHORITY_TOKEN", "", "TOOL_CORE_AUTHORITY_TOKEN"),
            ("TOOL_CORE_AUTHORITY_TOKEN", "short", "TOOL_CORE_AUTHORITY_TOKEN"),
            ("TOOL_CORE_AUTHORITY_TOKEN", self.values["WORKER_CORE_TOKEN"], "WORKER_CORE_TOKEN"),
            ("TOOL_CORE_AUTHORITY_TOKEN", self.values["WORKER_TOOL_TOKEN"], "WORKER_TOOL_TOKEN"),
            ("TOOL_CORE_AUTHORITY_TOKEN", self.values["GATEWAY_TOOL_TOKEN"], "GATEWAY_TOOL_TOKEN"),
            ("TOOL_CORE_AUTHORITY_TOKEN", self.values["CORE_DOCUMENT_TOKEN"], "CORE_DOCUMENT_TOKEN"),
            ("CORE_MODEL_AUTHORITY_TOKEN", "short", "CORE_MODEL_AUTHORITY_TOKEN"),
            ("CORE_MODEL_AUTHORITY_TOKEN", self.values["WORKER_MODEL_TOKEN"], "WORKER_MODEL_TOKEN"),
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
        with patch.dict(local.os.environ, {"TOOL_CORE_AUTHORITY_TOKEN": self.values["WORKER_MODEL_TOKEN"]}):
            with self.assertRaisesRegex(ValueError, "TOOL_CORE_AUTHORITY_TOKEN must differ from WORKER_MODEL_TOKEN"):
                local.ensure_private_environment()

    def test_generated_migration_profile_sets_only_the_app_env_database_slot(self):
        cases = (
            ("development", {"DEV_DATABASE_URL": "postgres://contractaudit:pw@localhost/contract_audit_dev"}, "DEV_DATABASE_URL"),
            ("test", {"TEST_DATABASE_URL": "postgres://contractaudit:pw@localhost/contract_audit_test"}, "TEST_DATABASE_URL"),
            ("production", {"PROD_DATABASE_URL": "postgres://contractaudit:pw@db/contract_audit_prod"}, "PROD_DATABASE_URL"),
        )
        for mode, selected, expected_slot in cases:
            with self.subTest(app_env=mode):
                infra = {"POSTGRES_PASSWORD": "p" * 40, "APP_ENV": mode, **selected}
                values = local.generated_values(infra)
                self.assertEqual(local.application_database_slot(infra), expected_slot)
                self.assertEqual(values[expected_slot], values["MIGRATOR_DATABASE_URL"])
                for slot in {"DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL"} - {expected_slot}:
                    self.assertEqual(values[slot], "")
                admin = urlsplit(values["AUTH_ADMIN_DATABASE_URL"])
                migrator = urlsplit(values["MIGRATOR_DATABASE_URL"])
                self.assertEqual(admin.path, migrator.path)
                self.assertNotEqual(admin.username, migrator.username)
                self.assertEqual(migrator.username, "audit_offline_migrator")

    def test_app_env_rejects_a_database_in_another_environment_slot(self):
        with self.assertRaisesRegex(ValueError, "TEST_DATABASE_URL must select"):
            local.generated_values({
                "POSTGRES_PASSWORD": "p" * 40, "APP_ENV": "test",
                "TEST_DATABASE_URL": "postgres://contractaudit:pw@localhost/contract_audit_dev",
            })
        with self.assertRaisesRegex(ValueError, "private PROD_DATABASE_URL"):
            local.generated_values({"POSTGRES_PASSWORD": "p" * 40, "APP_ENV": "production"})


class ExecutionAuthorityDeploymentTests(unittest.TestCase):
    def copy_config(self, destination):
        for relative in (
            "compose.app.yaml", "deployments/k8s/base/workloads.yaml",
            "deployments/k8s/base/config.yaml", "deployments/k3d/config-patch.yaml",
        ):
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, target)

    def check_root(self, root):
        conformance.failures.clear()
        conformance.notes.clear()
        conformance.check_core_authority_config(root, "template")
        return list(conformance.failures)

    @staticmethod
    def set_runtime_ca_path(config_path, value):
        documents = list(yaml.safe_load_all(config_path.read_text()))
        runtime = next(doc for doc in documents if doc.get("kind") == "ConfigMap" and
                       doc.get("metadata", {}).get("name") == "contract-audit-runtime")
        runtime["data"]["CORE_AUTHORITY_CA_FILE"] = value
        config_path.write_text("---\n".join(yaml.safe_dump(doc, sort_keys=False) for doc in documents))

    def test_execution_receives_authority_config_and_optional_trust_mount(self):
        failures = self.check_root(ROOT)
        self.assertEqual(failures, [])
        compose = yaml.safe_load((ROOT / "compose.app.yaml").read_text())["services"]
        env = compose["audit-execution"]["environment"]
        self.assertIn("CORE_AUTHORITY_URL", env)
        self.assertIn("CORE_AUTHORITY_CA_FILE", env)
        self.assertIn("CORE_AUTHORITY_ALLOW_INSECURE_DEV", env)
        self.assertTrue(any("core-authority-ca" in str(volume) for volume in compose["audit-execution"]["volumes"]))
        self.assertNotIn("TOOL_CORE_AUTHORITY_TOKEN", env)
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            self.copy_config(root)
            config_path = root / "deployments/k8s/base/config.yaml"
            self.set_runtime_ca_path(config_path, "/run/contract-audit/core-authority-ca/ca.pem")
            self.assertEqual(self.check_root(root), [])

    def test_context_switch_is_core_only_and_defaults_closed(self):
        conformance.failures.clear()
        conformance.check_context_switch_config(ROOT)
        self.assertEqual(conformance.failures, [])
        compose = yaml.safe_load((ROOT / "compose.app.yaml").read_text())["services"]
        self.assertEqual(conformance.compose_environment(compose["core-api"]["environment"])["CONTEXT_SWITCH_ENABLED"],
                         "${CONTEXT_SWITCH_ENABLED:-false}")
        for name, service in compose.items():
            if name != "core-api":
                self.assertNotIn("CONTEXT_SWITCH_ENABLED", conformance.compose_environment(service.get("environment")))

    def test_execution_never_receives_tool_only_service_token(self):
        conformance.failures.clear()
        conformance.check_tool_authority_compose(
            "audit-execution", "execution", {"TOOL_CORE_AUTHORITY_TOKEN": "${TOOL_CORE_AUTHORITY_TOKEN:?required}"}
        )
        self.assertTrue(any("must not receive" in item for item in conformance.failures))
        conformance.failures.clear()
        conformance.check_tool_authority_kubernetes(
            "audit-execution", "execution", {"TOOL_CORE_AUTHORITY_TOKEN": {
                "secretKeyRef": {"name": "contract-audit-service-tokens", "key": "TOOL_CORE_AUTHORITY_TOKEN"}
            }}
        )
        self.assertTrue(any("must not receive" in item for item in conformance.failures))

    def test_execution_missing_authority_or_ca_mount_is_rejected(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            self.copy_config(root)
            compose_path = root / "compose.app.yaml"
            compose = yaml.safe_load(compose_path.read_text())
            compose["services"]["audit-execution"]["environment"].pop("CORE_AUTHORITY_URL")
            compose_path.write_text(yaml.safe_dump(compose, sort_keys=False))
            failures = self.check_root(root)
            self.assertTrue(any("compose audit-execution must explicitly pass CORE_AUTHORITY_URL" in x for x in failures), failures)

        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            self.copy_config(root)
            config_path = root / "deployments/k8s/base/config.yaml"
            self.set_runtime_ca_path(config_path, "/run/contract-audit/core-authority-ca/ca.pem")
            workload_path = root / "deployments/k8s/base/workloads.yaml"
            workloads = list(yaml.safe_load_all(workload_path.read_text()))
            execution = next(item for item in workloads if item.get("metadata", {}).get("name") == "audit-execution")
            pod = execution["spec"]["template"]["spec"]
            pod["containers"][0]["volumeMounts"] = [m for m in pod["containers"][0]["volumeMounts"] if m["name"] != "core-authority-ca"]
            workload_path.write_text("---\n".join(yaml.safe_dump(item, sort_keys=False) for item in workloads))
            failures = self.check_root(root)
            self.assertTrue(any("in the audit-execution workload" in x for x in failures), failures)


class ModelAuthorityDeploymentTests(unittest.TestCase):
    def test_only_core_and_real_model_gateway_receive_the_model_authority_token(self):
        conformance.failures.clear()
        conformance.notes.clear()
        compose = yaml.safe_load((ROOT / "compose.app.yaml").read_text())["services"]
        self.assertEqual(conformance.compose_environment(compose["core-api"]["environment"])["CORE_MODEL_AUTHORITY_TOKEN"],
                         "${CORE_MODEL_AUTHORITY_TOKEN:?Run make app-bootstrap}")
        real_env = conformance.compose_environment(compose["model-gateway"]["environment"])
        self.assertEqual(real_env["CORE_MODEL_AUTHORITY_TOKEN"], "${CORE_MODEL_AUTHORITY_TOKEN:?Run make app-bootstrap}")
        self.assertIn("CORE_AUTHORITY_URL", real_env)
        fixture_env = conformance.compose_environment(compose["model-gateway-fixture"]["environment"])
        self.assertEqual(fixture_env["CORE_MODEL_AUTHORITY_TOKEN"], "${CORE_MODEL_AUTHORITY_TOKEN:?Run make app-bootstrap}")
        self.assertIn("CORE_AUTHORITY_URL", fixture_env)
        for name, service in compose.items():
            conformance.check_model_authority_compose(name, conformance.compose_environment(service.get("environment", {})).get("SERVICE_ROLE"),
                                                       conformance.compose_environment(service.get("environment", {})))
        self.assertEqual(conformance.failures, [])

        workloads = [item for item in yaml.safe_load_all((ROOT / "deployments/k8s/base/workloads.yaml").read_text()) if item]
        by_name = {item.get("metadata", {}).get("name"): item for item in workloads if item.get("kind") == "Deployment"}
        for name in ("core-api", "model-gateway"):
            env = {item["name"]: item.get("valueFrom", item.get("value"))
                   for item in by_name[name]["spec"]["template"]["spec"]["containers"][0].get("env", [])}
            conformance.failures.clear()
            conformance.check_model_authority_kubernetes(name, "core" if name == "core-api" else "model", env)
            self.assertEqual(conformance.failures, [])
        for name in ("document-service", "audit-execution", "audit-worker", "tool-gateway"):
            env = {item["name"]: item.get("valueFrom", item.get("value"))
                   for item in by_name[name]["spec"]["template"]["spec"]["containers"][0].get("env", [])}
            conformance.failures.clear()
            conformance.check_model_authority_kubernetes(name, {
                "document-service": "document", "audit-execution": "execution",
                "audit-worker": "worker", "tool-gateway": "tool",
            }[name], env)
            self.assertEqual(conformance.failures, [])

    def test_missing_or_misbound_model_authority_credential_fails(self):
        conformance.failures.clear()
        conformance.check_model_authority_compose("model-gateway", "model", {})
        self.assertTrue(any("must require CORE_MODEL_AUTHORITY_TOKEN" in item for item in conformance.failures))
        conformance.failures.clear()
        conformance.check_model_authority_kubernetes("model-gateway", "model", {
            "CORE_MODEL_AUTHORITY_TOKEN": {"secretKeyRef": {"name": "contract-audit-service-tokens", "key": "TOOL_CORE_AUTHORITY_TOKEN"}}
        })
        self.assertTrue(any("must use the required CORE_MODEL_AUTHORITY_TOKEN" in item for item in conformance.failures))


class K3dCredentialUpgradeTests(unittest.TestCase):
    def test_legacy_private_profile_is_upgraded_without_rotating_existing_tokens(self):
        with tempfile.TemporaryDirectory() as temporary:
            artifacts = Path(temporary)
            secrets_path = artifacts / "secrets.json"
            database = "postgres://contractaudit:pw@db:5432/contract_audit_dev?sslmode=disable"
            values = {
                "POSTGRES_PASSWORD": "p" * 64,
                "RUSTFS_ACCESS_KEY": "a" * 32,
                "RUSTFS_SECRET_KEY": "s" * 48,
                "LLM_API_KEY": "l" * 48,
                "LLM_CONFIG_KEY": "Y2" * 32,
                "CLM_DATA_KEY": "Y2" * 32,
                "CORE_DATABASE_URL": "postgres://core:pw@db:5432/contract_audit_dev",
                "DOCUMENT_DATABASE_URL": "postgres://doc:pw@db:5432/contract_audit_dev",
                "EXECUTION_DATABASE_URL": "postgres://exec:pw@db:5432/contract_audit_dev",
                "MODEL_DATABASE_URL": "postgres://model:pw@db:5432/contract_audit_dev",
                "TOOL_DATABASE_URL": "postgres://tool:pw@db:5432/contract_audit_dev",
                "MIGRATOR_DATABASE_URL": database,
                "DEV_DATABASE_URL": database,
            }
            for key in k3d.SERVICE_TOKEN_KEYS:
                if key != "CORE_MODEL_AUTHORITY_TOKEN":
                    values[key] = "token-" + key + "-" + "x" * 40
            before = dict(values)
            secrets_path.write_text(json.dumps(values), encoding="utf-8")
            secrets_path.chmod(0o600)
            with patch.object(k3d, "ARTIFACTS", artifacts), patch.object(k3d, "SECRETS", secrets_path):
                upgraded = k3d.ensure_secrets()
            self.assertNotEqual(upgraded["MIGRATOR_DATABASE_URL"], before["MIGRATOR_DATABASE_URL"])
            self.assertEqual(upgraded["AUTH_ADMIN_DATABASE_URL"], before["MIGRATOR_DATABASE_URL"])
            self.assertIn("audit_offline_migrator", upgraded["MIGRATOR_DATABASE_URL"])
            self.assertEqual(urlsplit(upgraded["PROD_DATABASE_URL"]).username, "audit_offline_migrator")
            self.assertIn("/contract_audit_prod?", upgraded["PROD_DATABASE_URL"])
            self.assertEqual(upgraded["CONTEXT_SWITCH_ENABLED"], "false")
            for key, value in before.items():
                if key not in {"MIGRATOR_DATABASE_URL", "DEV_DATABASE_URL"}:
                    self.assertEqual(upgraded[key], value)
            self.assertGreaterEqual(len(upgraded["CORE_MODEL_AUTHORITY_TOKEN"]), 32)
            self.assertNotIn(upgraded["CORE_MODEL_AUTHORITY_TOKEN"], set(before.values()))
            self.assertEqual(secrets_path.stat().st_mode & 0o777, 0o600)

    def test_context_switch_profile_upgrade_defaults_false_and_explicit_k3d_opt_in(self):
        with tempfile.TemporaryDirectory() as temporary:
            artifacts = Path(temporary)
            secrets_path = artifacts / "secrets.json"
            values = {
                "POSTGRES_PASSWORD": "p" * 64,
                "RUSTFS_ACCESS_KEY": "a" * 32,
                "RUSTFS_SECRET_KEY": "s" * 48,
                "LLM_API_KEY": "l" * 48,
                "CORE_DATABASE_URL": "postgres://core:pw@db:5432/contract_audit_dev",
                "DOCUMENT_DATABASE_URL": "postgres://doc:pw@db:5432/contract_audit_dev",
                "EXECUTION_DATABASE_URL": "postgres://exec:pw@db:5432/contract_audit_dev",
                "MODEL_DATABASE_URL": "postgres://model:pw@db:5432/contract_audit_dev",
                "TOOL_DATABASE_URL": "postgres://tool:pw@db:5432/contract_audit_dev",
                "MIGRATOR_DATABASE_URL": "postgres://audit_offline_migrator:pw@db:5432/contract_audit_dev",
                "DEV_DATABASE_URL": "postgres://audit_offline_migrator:pw@db:5432/contract_audit_dev",
                "AUTH_ADMIN_DATABASE_URL": "postgres://contractaudit:pw@db:5432/contract_audit_dev",
                "PROD_DATABASE_URL": "postgres://audit_offline_migrator:pw@db:5432/contract_audit_prod",
                "LLM_CONFIG_KEY": "Y2" * 32,
                "CLM_DATA_KEY": "Y2" * 32,
            }
            for key in k3d.SERVICE_TOKEN_KEYS:
                values[key] = "token-" + key + "-" + "x" * 40
            before = dict(values)
            secrets_path.write_text(json.dumps(values), encoding="utf-8")
            secrets_path.chmod(0o600)
            with patch.object(k3d, "ARTIFACTS", artifacts), patch.object(k3d, "SECRETS", secrets_path):
                upgraded = k3d.ensure_secrets()
            self.assertEqual(upgraded["CONTEXT_SWITCH_ENABLED"], "false")
            for key, value in before.items():
                self.assertEqual(upgraded[key], value)

        result = subprocess.CompletedProcess([], 0, stdout="", stderr="")
        with patch.object(k3d, "ensure_secrets", return_value={"CONTEXT_SWITCH_ENABLED": "false"}), \
             patch.dict(k3d.os.environ, {"CONTEXT_SWITCH_ENABLED": "true"}), \
             patch.object(k3d, "run", return_value=result) as mocked_run:
            k3d.apply_context_switch_override()
        self.assertIn("contract-audit-core-runtime", mocked_run.call_args.args[0])
        self.assertIn('"CONTEXT_SWITCH_ENABLED": "true"', mocked_run.call_args.args[0][-1])

    def test_explicit_empty_context_switch_override_keeps_k3d_disabled(self):
        result = subprocess.CompletedProcess([], 0, stdout="", stderr="")
        with patch.object(k3d, "ensure_secrets", return_value={"CONTEXT_SWITCH_ENABLED": "true"}), \
             patch.dict(k3d.os.environ, {"CONTEXT_SWITCH_ENABLED": ""}), \
             patch.object(k3d, "run", return_value=result) as mocked_run:
            k3d.apply_context_switch_override()
        self.assertIn('"CONTEXT_SWITCH_ENABLED": "false"', mocked_run.call_args.args[0][-1])
if __name__ == "__main__":
    unittest.main()

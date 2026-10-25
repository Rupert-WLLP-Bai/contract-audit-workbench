"""The otel profile must keep debug export and add queryable metrics and traces."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ObserveProfileTest(unittest.TestCase):
    def test_collector_keeps_debug_and_adds_query_exporters(self):
        collector = (ROOT / "deploy/app/otel-collector.yaml").read_text(encoding="utf-8")
        self.assertIn("exporters: [debug, otlp/tempo]", collector)
        self.assertIn("exporters: [debug, prometheus]", collector)
        self.assertIn("endpoint: tempo:4317", collector)
        self.assertIn("endpoint: 0.0.0.0:8889", collector)
        self.assertNotIn("exporters: [debug]\n", collector.split("logs:", 1)[0])

    def test_compose_profile_pins_backends_and_refuses_a_default_grafana_password(self):
        application = (ROOT / "compose.app.yaml").read_text(encoding="utf-8")
        compose = (ROOT / "compose.observe.yaml").read_text(encoding="utf-8")
        tempo = (ROOT / "deploy/app/observe/tempo.yaml").read_text(encoding="utf-8")
        prometheus = (ROOT / "deploy/app/observe/prometheus.yml").read_text(encoding="utf-8")
        grafana = (ROOT / "deploy/app/observe/grafana-datasources.yaml").read_text(encoding="utf-8")
        for image in (
            "grafana/tempo:2.10.10",
            "prom/prometheus:v3.15.1",
            "grafana/grafana:13.2.4",
        ):
            self.assertIn(image, compose)
            self.assertNotIn(image, application)
        self.assertNotIn("GRAFANA_ADMIN_PASSWORD", application)
        self.assertIn("GRAFANA_ADMIN_PASSWORD:?", compose.replace(" ", ""))
        self.assertIn('targets: ["otel-collector:8889"]', prometheus)
        self.assertIn("audit.execution.events", prometheus)
        self.assertIn("backend: local", tempo)
        self.assertIn("uid: prometheus", grafana)
        self.assertIn("uid: tempo", grafana)
        self.assertIn("datasourceUid: prometheus", grafana)
        self.assertIn("url: http://tempo:3200", grafana)
        self.assertIn("url: http://prometheus:9090", grafana)


if __name__ == "__main__":
    unittest.main()

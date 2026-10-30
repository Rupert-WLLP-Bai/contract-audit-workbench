"""Local behavior checks for the P0 source and route acceptance helper."""
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess

import p0_runtime


class RouteFixture:
    def __init__(self, role, *, bad_marker=False):
        self.role = role
        self.bad_marker = bad_marker
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                if owner.role == "admin" and self.path == "/":
                    self.send_response(308)
                    self.send_header("Location", "/wrong/" if owner.bad_marker else "/admin/")
                    self.end_headers()
                    return
                if owner.role == "portal" and self.path == "/":
                    self.send_response(308)
                    self.send_header("Location", "/portal/")
                    self.end_headers()
                    return
                bodies = {
                    "client": b'<script src="/assets/client.js">',
                    "admin": b'<script src="/admin/assets/admin.js">',
                    "portal": b'<script src="/portal/assets/portal.js">',
                    "gateway": b'{"status":"ready"}',
                }
                body = bodies[owner.role]
                if owner.bad_marker and owner.role == "client":
                    body = b"not a release artifact"
                self.send_response(200)
                self.send_header("Content-Type", "application/json" if owner.role == "gateway" else "text/html")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, format, *args):
                return

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"http://127.0.0.1:{self.server.server_port}"

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)


class RouteAcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.fixtures = [RouteFixture(name) for name in ("client", "admin", "portal", "gateway")]
        self.addCleanup(lambda: [fixture.close() for fixture in self.fixtures])

    def urls(self):
        return [fixture.url for fixture in self.fixtures]

    def test_release_routes_check_three_sites_and_gateway(self):
        checks, passed = p0_runtime.route_checks(*self.urls())
        self.assertTrue(passed)
        self.assertEqual([check["name"] for check in checks], [
            "client_entry", "admin_redirect", "admin_entry", "portal_redirect", "portal_entry", "gateway_ready",
        ])
        self.assertTrue(all(check["passed"] for check in checks))

    def test_missing_asset_marker_or_wrong_redirect_fails(self):
        self.fixtures[0].close()
        self.fixtures[0] = RouteFixture("client", bad_marker=True)
        self.fixtures[1].close()
        self.fixtures[1] = RouteFixture("admin", bad_marker=True)
        checks, passed = p0_runtime.route_checks(*self.urls())
        self.assertFalse(passed)
        self.assertFalse(checks[0]["passed"])
        self.assertFalse(checks[1]["passed"])
        self.assertTrue(checks[-1]["passed"], "gateway readiness must depend on HTTP status only")


class SnapshotFileSelectionTests(unittest.TestCase):
    def test_git_source_selection_excludes_private_and_ignored_runtime_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "source"
            source.mkdir()
            subprocess.run(["git", "init", "-q", str(source)], check=True)
            (source / ".gitignore").write_text(".env\nartifacts/\n", encoding="utf-8")
            for name in ("README.md", ".env.example", "tracked.go"):
                (source / name).write_text(name, encoding="utf-8")
            (source / ".env").write_text("secret", encoding="utf-8")
            (source / "untracked.go").write_text("package fixture", encoding="utf-8")
            (source / "artifacts").mkdir()
            (source / "artifacts" / "run.json").write_text("runtime output", encoding="utf-8")
            (source / "secret-target").write_text("private", encoding="utf-8")
            (source / "linked-secret").symlink_to("secret-target")
            subprocess.run(["git", "-C", str(source), "add", "README.md", ".env.example", "tracked.go", ".gitignore"], check=True)
            paths = p0_runtime.source_paths(source)
            names = {path.as_posix() for path in paths}
            self.assertIn(".env.example", names)
            self.assertIn("untracked.go", names)
            self.assertNotIn(".env", names)
            self.assertNotIn("artifacts/run.json", names)
            self.assertNotIn("linked-secret", names)


if __name__ == "__main__":
    unittest.main()

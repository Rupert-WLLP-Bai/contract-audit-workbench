import http.client
import http.server
import json
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.parse

import record_model_proxy


class Upstream(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    requests = []

    def log_message(self, format, *args):
        pass

    def do_GET(self):
        self.requests.append((self.path, self.headers, b""))
        body = b'{"data":[{"id":"real-model"}]}'
        if self.path.endswith("?truncate=1"):
            self.send_response(200)
            self.send_header("Content-Length", "100")
            self.end_headers()
            self.wfile.write(b"partial")
            self.close_connection = True
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        self.requests.append((self.path, self.headers, body))
        result = b'{"error":"upstream rejected request"}'
        self.send_response(429)
        self.send_header("Content-Length", str(len(result)))
        self.end_headers()
        self.wfile.write(result)


class RecorderTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.output = Path(self.temp.name)
        Upstream.requests = []
        self.upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        url = urllib.parse.urlsplit(f"http://127.0.0.1:{self.upstream.server_port}/custom/v1")
        self.proxy = record_model_proxy.Server(("127.0.0.1", 0), url, self.output)
        self.threads = [threading.Thread(target=server.serve_forever) for server in (self.upstream, self.proxy)]
        for thread in self.threads:
            thread.start()

    def tearDown(self):
        for server in (self.proxy, self.upstream):
            server.shutdown()
            server.server_close()
        for thread in self.threads:
            thread.join()
        self.temp.cleanup()

    def test_forwarding_records_exact_bodies_and_no_headers(self):
        connection = http.client.HTTPConnection("127.0.0.1", self.proxy.server_port)
        connection.request("GET", "/v1/models", headers={"Authorization": "Bearer private-value"})
        response = connection.getresponse()
        self.assertEqual(response.status, 200)
        model_body = response.read()
        payload = b'{"model":"real-model","messages":[{"role":"user","content":"private contract"}]}'
        connection.request("POST", "/v1/chat/completions?trace=1", body=payload,
                           headers={"Authorization": "Bearer private-value", "Cookie": "session=private-cookie",
                                    "X-Api-Key": "private-key", "Content-Type": "application/json"})
        response = connection.getresponse()
        self.assertEqual(response.status, 429)
        error_body = response.read()
        connection.close()
        self.proxy.shutdown()
        self.proxy.server_close()
        self.assertEqual([entry[0] for entry in Upstream.requests], ["/custom/v1/models", "/custom/v1/chat/completions?trace=1"])
        self.assertEqual(Upstream.requests[1][1]["Authorization"], "Bearer private-value")
        self.assertEqual(Upstream.requests[1][2], payload)
        records = []
        for directory in self.output.iterdir():
            metadata = json.loads((directory / "metadata.json").read_text())
            records.append((metadata, directory))
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            for file in directory.iterdir():
                self.assertEqual(file.stat().st_mode & 0o777, 0o600)
                self.assertNotIn(b"private-value", file.read_bytes())
                self.assertNotIn(b"private-cookie", file.read_bytes())
                self.assertNotIn(b"private-key", file.read_bytes())
            self.assertFalse(metadata["interrupted"])
            self.assertIsNotNone(metadata["ended_at"])
            self.assertIsNone(metadata["error"])
        by_endpoint = {metadata["endpoint"]: (metadata, directory) for metadata, directory in records}
        self.assertEqual(by_endpoint["/v1/models"][1].joinpath("request.body").read_bytes(), b"")
        self.assertEqual(by_endpoint["/v1/models"][1].joinpath("response.body").read_bytes(), model_body)
        metadata, directory = by_endpoint["/v1/chat/completions"]
        self.assertEqual(metadata["upstream_status"], 429)
        self.assertEqual(directory.joinpath("request.body").read_bytes(), payload)
        self.assertEqual(directory.joinpath("response.body").read_bytes(), error_body)

    def test_truncated_upstream_response_is_recorded_as_interrupted(self):
        connection = http.client.HTTPConnection("127.0.0.1", self.proxy.server_port)
        connection.request("GET", "/v1/models?truncate=1")
        response = connection.getresponse()
        self.assertEqual(response.status, 200)
        with self.assertRaises(http.client.IncompleteRead):
            response.read()
        connection.close()
        self.proxy.shutdown()
        self.proxy.server_close()
        directory = next(self.output.iterdir())
        metadata = json.loads((directory / "metadata.json").read_text())
        self.assertEqual(directory.joinpath("response.body").read_bytes(), b"partial")
        self.assertEqual(metadata["upstream_status"], 200)
        self.assertTrue(metadata["interrupted"])
        self.assertEqual(metadata["error"], "IncompleteRead")
        self.assertIsNotNone(metadata["ended_at"])


if __name__ == "__main__":
    unittest.main()

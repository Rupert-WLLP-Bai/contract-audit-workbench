#!/usr/bin/env python3
"""Record local OpenAI-compatible traffic while forwarding it to a real upstream."""

import argparse
import datetime
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import tempfile
import urllib.parse


REPOSITORY = Path(__file__).resolve().parent.parent
HOP_HEADERS = {
    "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
    "te", "trailer", "transfer-encoding", "upgrade",
}


def timestamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def private_file(path):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.fchmod(fd, 0o600)
    return os.fdopen(fd, "wb")


def save_metadata(directory, metadata):
    fd, name = tempfile.mkstemp(prefix=".metadata-", dir=directory)
    os.fchmod(fd, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            json.dump(metadata, output, ensure_ascii=False, indent=2)
            output.write("\n")
        os.replace(name, directory / "metadata.json")
    finally:
        if os.path.exists(name):
            os.unlink(name)


def filtered_headers(headers):
    blocked = HOP_HEADERS | {
        token.strip().lower()
        for value in headers.get_all("Connection", [])
        for token in value.split(",")
    }
    return [(name, value) for name, value in headers.items() if name.lower() not in blocked]


def copy_request_body(handler, output):
    transfer = handler.headers.get("Transfer-Encoding", "").lower().strip()
    length = handler.headers.get("Content-Length")
    if transfer:
        if transfer != "chunked" or length is not None:
            raise ValueError("unsupported request framing")
        total = 0
        while True:
            line = handler.rfile.readline(65537)
            if not line.endswith(b"\r\n") or len(line) > 65536:
                raise ValueError("invalid chunk header")
            size = int(line.split(b";", 1)[0], 16)
            if size < 0:
                raise ValueError("invalid chunk size")
            if size == 0:
                while True:
                    trailer = handler.rfile.readline(65537)
                    if not trailer or len(trailer) > 65536:
                        raise ValueError("invalid trailer")
                    if trailer == b"\r\n":
                        return total
            remaining = size
            while remaining:
                chunk = handler.rfile.read(min(65536, remaining))
                if not chunk:
                    raise EOFError("incomplete request")
                output.write(chunk)
                total += len(chunk)
                remaining -= len(chunk)
            if handler.rfile.read(2) != b"\r\n":
                raise ValueError("invalid chunk end")
    if length is None:
        return 0
    remaining = int(length)
    if remaining < 0:
        raise ValueError("invalid content length")
    total = remaining
    while remaining:
        chunk = handler.rfile.read(min(65536, remaining))
        if not chunk:
            raise EOFError("incomplete request")
        output.write(chunk)
        remaining -= len(chunk)
    return total


class Recorder(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, format, *args):
        # Request targets and exception text can contain credentials or private data.
        pass

    def handle_request(self):
        directory = Path(tempfile.mkdtemp(prefix="request-", dir=self.server.output))
        os.chmod(directory, 0o700)
        metadata = {
            "method": self.command,
            "endpoint": None,
            "started_at": timestamp(),
            "ended_at": None,
            "upstream_status": None,
            "error": None,
            "interrupted": True,
        }
        save_metadata(directory, metadata)
        response_started = False
        connection = None
        client_interrupted = False
        try:
            with private_file(directory / "request.body") as request_body, private_file(directory / "response.body") as response_body:
                path = urllib.parse.urlsplit(self.path)
                endpoint = path.path
                metadata["endpoint"] = endpoint if endpoint in ("/v1/models", "/v1/chat/completions") else None
                size = copy_request_body(self, request_body)
                if (endpoint, self.command) not in (("/v1/models", "GET"), ("/v1/chat/completions", "POST")):
                    metadata["interrupted"] = False
                    metadata["error"] = "unsupported_endpoint_or_method"
                    self.send_error(404)
                    return
                request_body.flush()
                upstream = self.server.upstream
                connection_class = http.client.HTTPSConnection if upstream.scheme == "https" else http.client.HTTPConnection
                connection = connection_class(upstream.hostname, upstream.port, timeout=600)
                target = upstream.path.rstrip("/") + endpoint[len("/v1"):]
                if path.query:
                    target += "?" + path.query
                connection.putrequest(self.command, target, skip_host=True, skip_accept_encoding=True)
                connection.putheader("Host", upstream.netloc)
                for name, value in filtered_headers(self.headers):
                    if name.lower() not in ("host", "content-length"):
                        connection.putheader(name, value)
                if size or self.headers.get("Content-Length") is not None or self.headers.get("Transfer-Encoding"):
                    connection.putheader("Content-Length", str(size))
                connection.endheaders()
                with open(directory / "request.body", "rb") as stored_request:
                    while True:
                        chunk = stored_request.read(65536)
                        if not chunk:
                            break
                        connection.send(chunk)
                response = connection.getresponse()
                metadata["upstream_status"] = response.status
                save_metadata(directory, metadata)
                self.send_response_only(response.status, response.reason)
                for name, value in filtered_headers(response.headers):
                    self.send_header(name, value)
                # Chunked upstream bodies are decoded by http.client; close-delimit them.
                if response.chunked or response.length is None:
                    self.send_header("Connection", "close")
                    self.close_connection = True
                self.end_headers()
                response_started = True
                expected = response.length
                received = 0
                while True:
                    chunk = response.read1(65536)
                    if not chunk:
                        break
                    response_body.write(chunk)
                    received += len(chunk)
                    if not client_interrupted:
                        try:
                            self.wfile.write(chunk)
                            self.wfile.flush()
                        except (BrokenPipeError, ConnectionResetError, socket.timeout):
                            client_interrupted = True
                            self.close_connection = True
                if expected is not None and received != expected:
                    raise http.client.IncompleteRead(b"", expected - received)
                metadata["interrupted"] = client_interrupted
                if client_interrupted:
                    metadata["error"] = "client_disconnected"
        except Exception as error:
            # Exception messages can contain URLs, headers, or upstream content.
            metadata["error"] = type(error).__name__
            self.close_connection = True
            if not response_started:
                try:
                    self.send_error(502, "Upstream or recording error")
                except (BrokenPipeError, ConnectionResetError, socket.timeout):
                    pass
        finally:
            if connection is not None:
                connection.close()
            metadata["ended_at"] = timestamp()
            save_metadata(directory, metadata)

    do_GET = handle_request
    do_POST = handle_request
    do_PUT = handle_request
    do_PATCH = handle_request
    do_DELETE = handle_request
    do_OPTIONS = handle_request
    do_HEAD = handle_request


class Server(http.server.ThreadingHTTPServer):
    def __init__(self, address, upstream, output):
        super().__init__(address, Recorder)
        self.upstream = upstream
        self.output = output

    def handle_error(self, request, client_address):
        # The standard traceback can contain private request or response data.
        pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", required=True, help="OpenAI-compatible root URL, e.g. http://127.0.0.1:3425/v1")
    parser.add_argument("--listen", required=True, help="127.0.0.1:PORT")
    parser.add_argument("--out", required=True, help="private recording directory outside the source repository")
    args = parser.parse_args()
    upstream = urllib.parse.urlsplit(args.upstream)
    try:
        if (upstream.scheme not in ("http", "https") or not upstream.hostname or "@" in upstream.netloc
                or upstream.query or upstream.fragment):
            raise ValueError
        upstream.port
        host, port = args.listen.rsplit(":", 1)
        if host != "127.0.0.1" or not 0 <= int(port) <= 65535:
            raise ValueError
    except ValueError:
        parser.error("invalid upstream URL or loopback listen address")
    output = Path(args.out).expanduser().resolve()
    if output == REPOSITORY or REPOSITORY in output.parents:
        parser.error("recordings must be outside the source repository")
    output.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(output, 0o700)
    server = Server((host, int(port)), upstream, output)
    print(f"Model gateway URL: http://127.0.0.1:{server.server_port}/v1", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

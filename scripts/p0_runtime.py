#!/usr/bin/env python3
"""Freeze a local P0 source candidate and probe its distributed release routes."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.request import HTTPRedirectHandler, Request, build_opener

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
from source_identity import source_identity  # noqa: E402

IGNORED_DIRS = {".git", ".agents", "__pycache__", "node_modules", "dist", "coverage",
                "artifacts", "outputs", "target", "build", "var"}


def source_paths(root: Path) -> list[Path]:
    result = subprocess.run(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
                            cwd=root, check=True, capture_output=True, timeout=30)
    paths = []
    for encoded in result.stdout.split(b"\0"):
        if not encoded:
            continue
        relative = Path(encoded.decode("utf-8"))
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("Git returned an unsafe source path")
        if any(part in IGNORED_DIRS for part in relative.parts) or relative.suffix == ".pyc":
            continue
        if relative.name.startswith(".env") and relative.name != ".env.example":
            continue
        path = root / relative
        if path.is_symlink() or not path.is_file():
            continue
        paths.append(relative)
    return sorted(set(paths), key=lambda item: item.as_posix())


def copy_source_files(source: Path, destination: Path, paths: list[Path]) -> None:
    for relative in paths:
        src = source / relative
        if src.is_symlink() or not src.is_file():
            continue
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, target)


def file_manifest(root: Path) -> list[dict[str, str]]:
    rows = []
    for path in sorted(item for item in root.rglob("*") if item.is_file() and not item.is_symlink()):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        rows.append({"path": path.relative_to(root).as_posix(), "sha256": digest})
    return rows


def manifest_digest(rows: list[dict[str, str]]) -> str:
    encoded = json.dumps(rows, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def snapshot_source(destination: Path) -> dict[str, object]:
    destination = destination.expanduser().resolve()
    if destination.is_relative_to(ROOT):
        raise ValueError("P0 source snapshots must be outside the source repository")
    if destination.exists():
        raise FileExistsError(f"refusing to overwrite P0 snapshot: {destination}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    before = source_identity(ROOT)
    destination.mkdir(mode=0o755)
    try:
        paths = source_paths(ROOT)
        copy_source_files(ROOT, destination, paths)
        copied = source_identity(destination)
        if before["digest"] != copied["digest"]:
            raise RuntimeError("source identity changed during snapshot copy")
    except BaseException:
        shutil.rmtree(destination)
        raise
    rows = file_manifest(destination)
    git_head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True,
                              capture_output=True, text=True, timeout=10).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain=v1", "-z"], cwd=ROOT, check=True,
                            capture_output=True, timeout=10).stdout
    diff = subprocess.run(["git", "diff", "--binary", "HEAD"], cwd=ROOT, check=True,
                          capture_output=True, timeout=30).stdout
    return {
        "schema_version": "p0-source-snapshot/v1",
        "created_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "baseline_commit": git_head,
        "source_identity_sha256": before["digest"],
        "snapshot_tree_sha256": manifest_digest(rows),
        "working_tree_status_sha256": hashlib.sha256(status).hexdigest(),
        "tracked_diff_sha256": hashlib.sha256(diff).hexdigest(),
        "files": rows,
        "source_file_count": len(paths),
        "source_directory": str(destination),
    }


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, file_pointer, code, message, headers, new_url):
        return None


def fetch(url: str, marker: str | None = None) -> dict[str, object]:
    request = Request(url, headers={"User-Agent": "contract-audit-p0-smoke/1"})
    opener = build_opener(NoRedirect)
    try:
        response = opener.open(request, timeout=10)
    except HTTPError as error:
        with error:
            body = error.read(4 << 20)
        return {"url": url, "status_code": error.code, "location": error.headers.get("Location"),
                "body_sha256": hashlib.sha256(body).hexdigest(), "body_bytes": len(body),
                "marker_present": marker.encode() in body if marker else None}
    except (URLError, TimeoutError, OSError) as error:
        return {"url": url, "status_code": None, "error": type(error).__name__}
    with response:
        body = response.read(4 << 20)
        return {"url": url, "status_code": response.status, "location": response.headers.get("Location"),
                "body_sha256": hashlib.sha256(body).hexdigest(), "body_bytes": len(body),
                "content_type": response.headers.get("Content-Type", ""),
                "marker_present": marker.encode() in body if marker else None}


def route_checks(client: str, admin: str, portal: str, gateway: str) -> tuple[list[dict[str, object]], bool]:
    probes = [
        ("client_entry", client.rstrip("/") + "/", 200, "/assets/"),
        ("admin_redirect", admin.rstrip("/") + "/", 308, "/admin/"),
        ("admin_entry", admin.rstrip("/") + "/admin/", 200, "/admin/assets/"),
        ("portal_redirect", portal.rstrip("/") + "/", 308, "/portal/"),
        ("portal_entry", portal.rstrip("/") + "/portal/", 200, "/portal/assets/"),
        ("gateway_ready", gateway.rstrip("/") + "/ready", 200, None),
    ]
    checks = []
    passed = True
    for name, url, expected, marker in probes:
        result = fetch(url, marker)
        actual = result.get("status_code")
        observed_marker = (True if marker is None else
                           str(result.get("location") or "").startswith(marker)
                           if expected == 308 else result.get("marker_present") is True)
        ok = actual == expected and bool(observed_marker)
        result.update({"name": name, "expected_status": expected, "passed": bool(ok)})
        checks.append(result)
        passed = passed and bool(ok)
    return checks, passed


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("snapshot", "routes"))
    parser.add_argument("--destination", default="")
    parser.add_argument("--client-url", default="")
    parser.add_argument("--admin-url", default="")
    parser.add_argument("--portal-url", default="")
    parser.add_argument("--gateway-url", default="")
    parser.add_argument("--report", default="")
    args = parser.parse_args(argv)
    report: dict[str, object] = {"schema_version": "p0-runtime-check/v1", "command": args.command,
                                 "started_at_unix_ns": time.time_ns(), "status": "FAIL"}
    try:
        if args.command == "snapshot":
            if not args.destination:
                parser.error("snapshot requires --destination")
            report.update(snapshot_source(Path(args.destination)))
            report["status"] = "PASS"
        else:
            values = (args.client_url, args.admin_url, args.portal_url, args.gateway_url)
            if not all(values):
                parser.error("routes requires client, admin, portal, and gateway URLs")
            checks, passed = route_checks(*values)
            report["checks"] = checks
            report["status"] = "PASS" if passed else "FAIL"
    except (OSError, RuntimeError, subprocess.SubprocessError, ValueError) as error:
        report["error"] = type(error).__name__
        report["message"] = str(error)
    report["finished_at_unix_ns"] = time.time_ns()
    if args.report:
        output = Path(args.report).expanduser().resolve()
        if output.exists():
            print(f"FAIL: refusing to overwrite runtime report: {output}", file=sys.stderr)
            return 1
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"status": report["status"], "source_identity_sha256": report.get("source_identity_sha256"),
                      "snapshot_tree_sha256": report.get("snapshot_tree_sha256"),
                      "report": args.report or None}, ensure_ascii=False))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())

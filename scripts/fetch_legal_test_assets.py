#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Fetch and verify the immutable public legal-source fixture bundle. This module’s command-line arguments and return artifacts define its operational boundary.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-31
# Updated: 2026-11-02
# Revision: r2
# Copyright: See LICENSE
# End file metadata.

"""Fetch and verify the immutable public legal-source fixture bundle."""

import argparse
import base64
import binascii
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sys
import tarfile
import tempfile
import urllib.request
from urllib.parse import urlsplit


CONFIG_PATH = Path(__file__).with_name("legal_test_assets.json")
MAX_ARCHIVE_BYTES = 8 * 1024 * 1024
DOWNLOAD_TIMEOUT_SECONDS = 60


class AssetError(Exception):
    """Raised when the pinned archive or its extracted files are invalid."""


def load_config(config):
    if isinstance(config, dict):
        value = config
    else:
        try:
            value = json.loads(Path(config).read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise AssetError(f"cannot read asset manifest: {exc}") from exc
    validate_config(value)
    return value


def validate_config(config):
    if not isinstance(config, dict) or config.get("schema_version") != "legal-test-assets/v1":
        raise AssetError("unsupported legal asset manifest")
    url = config.get("release_url")
    source = config.get("source")
    if (url is None) == (source is None):
        raise AssetError("asset manifest must declare exactly one archive source")
    if url is not None:
        try:
            parsed = urlsplit(url)
        except (TypeError, ValueError) as exc:
            raise AssetError("asset release URL must be a pinned GitHub HTTPS URL") from exc
        if (
            parsed.scheme != "https"
            or parsed.netloc != "github.com"
            or not parsed.path
        ):
            raise AssetError("asset release URL must be a pinned GitHub HTTPS URL")
    else:
        if not isinstance(source, dict) or source.get("kind") != "repository_variables":
            raise AssetError("unsupported legal asset archive source")
        prefix = source.get("variable_prefix")
        chunks = source.get("chunk_count")
        if (
            not isinstance(prefix, str)
            or re.fullmatch(r"CAW_LEGAL_TEST_ARCHIVE_[A-Z0-9_]+_", prefix) is None
            or type(chunks) is not int
            or chunks < 1
            or chunks > 16
        ):
            raise AssetError("invalid legal asset repository-variable source")
    if not _is_sha256(config.get("archive_sha256")):
        raise AssetError("asset archive SHA256 is invalid")
    size = config.get("archive_size_bytes")
    if type(size) is not int or size <= 0 or size > MAX_ARCHIVE_BYTES:
        raise AssetError("asset archive size is invalid")
    files = config.get("files")
    if not isinstance(files, list) or not files:
        raise AssetError("asset manifest file list is empty")
    seen = set()
    for entry in files:
        if not isinstance(entry, dict):
            raise AssetError("asset manifest entry must be an object")
        name = _safe_archive_path(entry.get("path"))
        if name in seen:
            raise AssetError(f"duplicate asset path: {name}")
        seen.add(name)
        if type(entry.get("size_bytes")) is not int or entry["size_bytes"] < 0:
            raise AssetError(f"invalid size for asset: {name}")
        if not _is_sha256(entry.get("sha256")):
            raise AssetError(f"invalid SHA256 for asset: {name}")


def _is_sha256(value):
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def _safe_archive_path(value):
    if not isinstance(value, str) or not value or "\\" in value:
        raise AssetError("archive path is empty or invalid")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ("", ".", "..") for part in path.parts):
        raise AssetError(f"unsafe archive path: {value!r}")
    canonical = path.as_posix()
    if canonical != value:
        raise AssetError(f"non-canonical archive path: {value!r}")
    return canonical


def _read_download(url):
    request = urllib.request.Request(url, headers={"User-Agent": "contract-audit-ci-assets/1"})
    try:
        with urllib.request.urlopen(request, timeout=DOWNLOAD_TIMEOUT_SECONDS) as response:
            data = response.read(MAX_ARCHIVE_BYTES + 1)
    except Exception as exc:
        raise AssetError(f"failed to download pinned legal assets: {exc}") from exc
    if len(data) > MAX_ARCHIVE_BYTES:
        raise AssetError("downloaded legal asset archive exceeds size limit")
    return data


def _read_local_archive(path):
    archive_path = Path(path)
    if archive_path.is_symlink() or not archive_path.is_file():
        raise AssetError("local legal asset archive must be a regular non-symlink file")
    try:
        if archive_path.stat().st_size > MAX_ARCHIVE_BYTES:
            raise AssetError("local legal asset archive exceeds size limit")
        with archive_path.open("rb") as archive_file:
            data = archive_file.read(MAX_ARCHIVE_BYTES + 1)
    except OSError as exc:
        raise AssetError(f"cannot read local legal asset archive: {exc}") from exc
    if len(data) > MAX_ARCHIVE_BYTES:
        raise AssetError("local legal asset archive exceeds size limit")
    return data


def _read_repository_variable_archive(source, environ, expected_size):
    prefix = source["variable_prefix"]
    chunks = []
    for index in range(1, source["chunk_count"] + 1):
        name = f"{prefix}{index:02d}"
        value = environ.get(name)
        if not isinstance(value, str) or not value:
            raise AssetError(f"missing legal asset repository variable: {name}")
        if (
            len(value) > 48 * 1024
            or re.fullmatch(r"[A-Za-z0-9+/]*={0,2}", value) is None
            or len(value) % 4 != 0
        ):
            raise AssetError(f"invalid base64 legal asset repository variable: {name}")
        chunks.append(value)
    encoded = "".join(chunks)
    expected_encoded_size = ((expected_size + 2) // 3) * 4
    if len(encoded) != expected_encoded_size:
        raise AssetError("legal asset repository variables have an unexpected encoded size")
    try:
        data = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise AssetError("legal asset repository variables are not valid base64") from exc
    if len(data) != expected_size or len(data) > MAX_ARCHIVE_BYTES:
        raise AssetError("repository-variable legal asset archive size mismatch")
    return data


def _expected_files(config):
    return {entry["path"]: entry for entry in config["files"]}


def _expected_directories(files):
    directories = set()
    for name in files:
        parent = PurePosixPath(name).parent
        while parent.as_posix() != ".":
            directories.add(parent.as_posix())
            parent = parent.parent
    return directories


def _validate_directory(root, config):
    files = _expected_files(config)
    expected_dirs = _expected_directories(files)
    if root.is_symlink() or not root.is_dir():
        raise AssetError(f"asset destination is not a regular directory: {root}")
    seen_files = set()
    seen_dirs = set()
    for current, dirs, names in os.walk(root, followlinks=False):
        current_path = Path(current)
        for name in list(dirs):
            path = current_path / name
            rel = path.relative_to(root).as_posix()
            if path.is_symlink() or not path.is_dir():
                raise AssetError(f"asset destination contains a non-directory path: {rel}")
            seen_dirs.add(rel)
        for name in names:
            path = current_path / name
            rel = path.relative_to(root).as_posix()
            if path.is_symlink() or not path.is_file():
                raise AssetError(f"asset destination contains a non-regular file: {rel}")
            seen_files.add(rel)
    if seen_files != set(files) or seen_dirs != expected_dirs:
        raise AssetError("existing legal asset directory has missing or unexpected paths")
    for rel, entry in files.items():
        data = (root / rel).read_bytes()
        if len(data) != entry["size_bytes"] or hashlib.sha256(data).hexdigest() != entry["sha256"]:
            raise AssetError(f"existing legal asset differs from pinned bytes: {rel}")


def _extract_verified(archive, destination, config):
    files = _expected_files(config)
    expected_dirs = _expected_directories(files)
    expected_names = set(files) | expected_dirs
    actual_names = set()
    try:
        bundle = tarfile.open(fileobj=io.BytesIO(archive), mode="r:*")
    except (tarfile.TarError, OSError) as exc:
        raise AssetError(f"legal asset archive is not a valid compressed tar: {exc}") from exc
    with bundle:
        members = bundle.getmembers()
        for member in members:
            name = member.name[:-1] if member.isdir() and member.name.endswith("/") else member.name
            name = _safe_archive_path(name)
            if name in actual_names:
                raise AssetError(f"duplicate archive member: {name}")
            actual_names.add(name)
            if member.isdir():
                if name not in expected_dirs:
                    raise AssetError(f"unexpected archive directory: {name}")
            elif member.isfile():
                if name not in files:
                    raise AssetError(f"unexpected archive file: {name}")
                if member.size != files[name]["size_bytes"]:
                    raise AssetError(f"archive size mismatch for {name}")
            else:
                raise AssetError(f"archive contains unsupported member type: {name}")
        if actual_names != expected_names:
            raise AssetError("archive file list does not match the pinned manifest")
        destination.mkdir(parents=True, exist_ok=True)
        for member in members:
            if member.isdir():
                name = member.name[:-1] if member.name.endswith("/") else member.name
                (destination / _safe_archive_path(name)).mkdir(parents=True, exist_ok=True)
                continue
            name = _safe_archive_path(member.name)
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            source = bundle.extractfile(member)
            if source is None:
                raise AssetError(f"cannot read archive member: {name}")
            data = source.read(files[name]["size_bytes"] + 1)
            if len(data) != files[name]["size_bytes"]:
                raise AssetError(f"truncated archive member: {name}")
            if hashlib.sha256(data).hexdigest() != files[name]["sha256"]:
                raise AssetError(f"SHA256 mismatch for archive member: {name}")
            with target.open("xb") as output:
                output.write(data)


def prepare_assets(destination, config=CONFIG_PATH, downloader=None, archive_path=None, environ=None):
    config = load_config(config)
    destination = Path(destination)
    if destination.exists() or destination.is_symlink():
        _validate_directory(destination, config)
        return destination
    local_archive = archive_path or os.environ.get("CAW_LEGAL_TEST_ARCHIVE")
    if local_archive:
        archive = _read_local_archive(local_archive)
    elif "source" in config:
        archive = _read_repository_variable_archive(
            config["source"],
            os.environ if environ is None else environ,
            config["archive_size_bytes"],
        )
    else:
        archive = (downloader or _read_download)(config["release_url"])
    if not isinstance(archive, bytes):
        raise AssetError("asset downloader did not return bytes")
    if len(archive) != config["archive_size_bytes"] or len(archive) > MAX_ARCHIVE_BYTES:
        raise AssetError("downloaded legal asset archive size mismatch")
    if hashlib.sha256(archive).hexdigest() != config["archive_sha256"]:
        raise AssetError("downloaded legal asset archive SHA256 mismatch")
    destination.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".caw-legal-assets-", dir=destination.parent))
    try:
        _extract_verified(archive, staging, config)
        _validate_directory(staging, config)
        try:
            os.rename(staging, destination)
        except FileExistsError:
            _validate_directory(destination, config)
            return destination
        return destination
    finally:
        if staging.exists():
            shutil.rmtree(staging)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--destination",
        default=str(Path(os.environ.get("RUNNER_TEMP", tempfile.gettempdir())) / "caw-legal-test-assets"),
        help="directory under which the pinned legal-sources tree is installed",
    )
    parser.add_argument("--manifest", default=str(CONFIG_PATH), help=argparse.SUPPRESS)
    parser.add_argument(
        "--archive",
        default=None,
        help="use a local archive file instead of the configured CI source",
    )
    args = parser.parse_args(argv)
    try:
        target = prepare_assets(args.destination, args.manifest, archive_path=args.archive)
    except AssetError as exc:
        print(f"legal test assets: {exc}", file=sys.stderr)
        return 1
    print(f"legal test assets verified at {target}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

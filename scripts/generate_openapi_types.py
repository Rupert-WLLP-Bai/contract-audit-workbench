#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Generate the frontend's HTTP wire types from internal/apidocs/openapi.json. This module’s command-line arguments and return artifacts define its operational boundary.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-31
# Updated: 2026-11-03
# Revision: r4
# Copyright: See LICENSE
# End file metadata.

"""Compatibility launcher for the openapi-typescript Node API generator."""

from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
GENERATOR = ROOT / "web/scripts/generate-openapi-types.ts"
PATH_OPTIONS = {"--schema", "--output", "-o"}


def _absolute_path(value: str) -> str:
    path = Path(value)
    return str(path.resolve() if path.is_absolute() else (Path.cwd() / path).resolve())


def _resolve_path_arguments(arguments: list[str]) -> list[str]:
    result = list(arguments)
    index = 0
    while index < len(result):
        argument = result[index]
        if argument in PATH_OPTIONS and index + 1 < len(result):
            result[index + 1] = _absolute_path(result[index + 1])
            index += 2
            continue
        for prefix in ("--schema=", "--output="):
            if argument.startswith(prefix):
                result[index] = prefix + _absolute_path(argument[len(prefix) :])
                break
        index += 1
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = _resolve_path_arguments(list(sys.argv[1:] if argv is None else argv))
    command = [os.environ.get("BUN", "bun"), "run", str(GENERATOR), "--", *arguments]
    try:
        return subprocess.run(command, cwd=ROOT / "web", check=False).returncode
    except OSError as error:
        print(f"OpenAPI wire generation failed: {error}", file=sys.stderr)
        return 127


if __name__ == "__main__":
    raise SystemExit(main())

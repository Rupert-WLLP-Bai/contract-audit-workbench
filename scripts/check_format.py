#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Read-only gofmt gate, avoiding dependencies/build artifacts. This module’s command-line arguments and return artifacts define its operational boundary.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-25
# Updated: 2026-10-25
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Read-only gofmt gate, avoiding dependencies/build artifacts."""
from pathlib import Path
import os
import shutil
import subprocess

root = Path(__file__).resolve().parents[1]
go = shutil.which(os.environ.get("GO", "go"))
gofmt = str(Path(go).parent / "gofmt") if go else "gofmt"
files = sorted(str(p.relative_to(root)) for top in ("cmd", "internal", "e2e") for p in (root / top).rglob("*.go"))
result = subprocess.run([gofmt, "-l", *files], cwd=root, capture_output=True, text=True)
if result.returncode:
    print(result.stderr)
    raise SystemExit(result.returncode)
if result.stdout.strip():
    print("Run gofmt on:\n" + result.stdout)
    raise SystemExit(1)
print(f"gofmt clean: {len(files)} Go files")

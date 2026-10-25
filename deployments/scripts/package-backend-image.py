#!/usr/bin/env python3
"""Package the host-built backend binaries into a locally cached base image.

The default Dockerfile needs `golang:1.26.7-bookworm`, `oven/bun:1.4.3` and
`nginxinc/nginx-unprivileged:1.29-alpine` from Docker Hub, which is unreachable
from this host. This script therefore produces the *same container contract*
offline: the nine Go binaries are built with the host toolchain (CGO disabled,
trimpath, stripped), copied into an already-cached base image, and tagged with
the name the Compose files expect (``contract-audit-backend:local``).

It never pulls: the base must already exist locally, and a missing base fails
with the explicit BLOCKED reason rather than attempting network access. Every
built image is labelled with its provenance and its digest is printed, so the
runtime proof is attributable and the default upstream image build stays a
separate, still-blocked item.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
COMMANDS = ("server", "gateway", "documentservice", "auditexecution", "worker", "modelgateway", "toolgateway", "migrate", "healthmonitor")
DEFAULT_BASE = "contract-audit-pdf-runtime:local"


def run(command: list[str], cwd: Path | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(command, cwd=str(cwd or ROOT), capture_output=True, text=True, timeout=1800)


def local_image_digest(image: str) -> str:
    result = run(["docker", "image", "inspect", "--format", "{{.Id}}", image])
    return result.stdout.strip() if result.returncode == 0 else ""


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default=DEFAULT_BASE, help="already-cached base image (never pulled)")
    parser.add_argument("--tag", default="contract-audit-backend:local", help="image tag the compose files expect")
    parser.add_argument("--source-digest", default="", help="source identity this packaging corresponds to")
    parser.add_argument("--preflight", action="store_true", help="only check the base image and the toolchain")
    parser.add_argument("--report", default="artifacts/c1c/ev-02/local-image-packaging.json",
                        help="write packaging evidence here (existing reports are never overwritten)")
    args = parser.parse_args(argv)

    base_digest = local_image_digest(args.base)
    if not base_digest:
        print(f"BLOCKED: base image {args.base} is not cached locally; refusing to pull", file=sys.stderr)
        return 2
    go = run(["go", "version"])
    if go.returncode != 0:
        print("BLOCKED: the Go toolchain is unavailable", file=sys.stderr)
        return 2
    print(f"preflight ok: base {args.base} -> {base_digest[:19]}…, {go.stdout.strip()}")
    if args.preflight:
        return 0

    with tempfile.TemporaryDirectory(prefix="contract-audit-packaging-") as workspace:
        context = Path(workspace)
        binaries = context / "bin"
        binaries.mkdir(parents=True, exist_ok=True)
        build = run(["env", "CGO_ENABLED=0", "go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(binaries) + "/", "./cmd/..."])
        if build.returncode != 0:
            print("FAIL: host build failed\n" + build.stderr[-1500:], file=sys.stderr)
            return 1
        entrypoint = (ROOT / "deploy/app/entrypoint.sh").read_text(encoding="utf-8")
        (context / "entrypoint").write_text(entrypoint, encoding="utf-8")
        dockerfile = [
            f"FROM {args.base}",
            "USER root",
            "RUN command -v pdfinfo && command -v pdftotext && command -v pdfimages && command -v pdftoppm && test -d /usr/share/poppler/cMap",
            "COPY bin/ /app/",
            "COPY entrypoint /app/entrypoint",
            "RUN chmod 0555 /app/entrypoint",
            "ENV DATA_DIR=/var/lib/contract-audit",
            "RUN install -d -o nginx -g nginx /var/lib/contract-audit",
            "WORKDIR /app",
            "USER nginx:nginx",
            "ENTRYPOINT [\"/app/entrypoint\"]",
            "CMD [\"/app/server\"]",
            "",
        ]
        (context / "Dockerfile").write_text("\n".join(dockerfile), encoding="utf-8")
        labels = [
            "--label", f"contract-audit.packaging=offline-host-binaries",
            "--label", f"contract-audit.base={args.base}",
            "--label", f"contract-audit.base.digest={base_digest}",
        ]
        if args.source_digest:
            labels += ["--label", f"contract-audit.source.digest={args.source_digest}"]
        manifest = {name: hashlib.sha256((binaries / name).read_bytes()).hexdigest() for name in COMMANDS if (binaries / name).exists()}
        missing = [name for name in COMMANDS if name not in manifest]
        if missing:
            print(f"FAIL: the host build did not produce {missing}", file=sys.stderr)
            return 1
        build_image = run(["docker", "build", "--no-cache=false", "-t", args.tag, *labels, str(context)])
        if build_image.returncode != 0:
            print("FAIL: image build failed\n" + build_image.stderr[-1500:], file=sys.stderr)
            return 1
    digest = local_image_digest(args.tag)
    print(json.dumps({
        "image": args.tag, "image_digest": digest, "base": args.base, "base_digest": base_digest,
        "source_digest": args.source_digest,
        "binaries": {name: sha[:16] + "…" for name, sha in manifest.items()},
        "note": "offline packaging; the default Dockerfile image build remains BLOCKED on Docker Hub egress",
    }, ensure_ascii=False, indent=1))
    report_path = Path(args.report)
    if not report_path.is_absolute():
        report_path = ROOT / report_path
    if report_path.exists():
        print(f"BLOCKED: report already exists at {report_path}; refusing to overwrite", file=sys.stderr)
        return 2
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(
        {"image": args.tag, "image_digest": digest, "base": args.base, "base_digest": base_digest,
         "source_digest": args.source_digest, "binaries": manifest},
        ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Build or verify current Compose app images from cached bases.

The offline path uses the locked host Go/Bun toolchains, current frontend source,
current ``web/dist`` artifacts, and cached Docker bases. Docker image builds run
without network access or pulls. The four output tags match compose.app.yaml.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from source_identity import source_identity  # noqa: E402

COMMANDS = ("server", "gateway", "documentservice", "auditexecution", "worker", "modelgateway", "toolgateway", "migrate", "healthmonitor")
BACKEND_TAG = "contract-audit-backend:local"
FRONTENDS = {
    "client": ("contract-audit-client:local", "index.html"),
    "admin": ("contract-audit-admin:local", "admin.html"),
    "portal": ("contract-audit-portal:local", "portal.html"),
}
DOCKERFILE_BASES = (
    "golang:1.26.7-bookworm", "oven/bun:1.4.3", "alpine:3.22", "nginxinc/nginx-unprivileged:1.29-alpine",
)
DEFAULT_OFFLINE_BASE = "contract-audit-pdf-runtime:local"
SUBPROCESSES: list[dict[str, object]] = []


def command_prefix() -> list[str]:
    docker = os.environ.get("DOCKER_BIN", "docker")
    if not shutil.which(docker):
        raise RuntimeError("Docker CLI is unavailable")
    return [docker]


def run(command: list[str], *, cwd: Path = ROOT, timeout: int = 1800) -> subprocess.CompletedProcess[str]:
    started = time.monotonic()
    try:
        result = subprocess.run(command, cwd=str(cwd), capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as error:
        SUBPROCESSES.append({"argv": command, "returncode": None, "timed_out": True,
                             "timeout_seconds": timeout, "seconds": round(time.monotonic() - started, 3)})
        raise error
    SUBPROCESSES.append({
        "argv": command,
        "returncode": result.returncode,
        "seconds": round(time.monotonic() - started, 3),
        "stdout_bytes": len(result.stdout.encode()),
        "stdout_sha256": hashlib.sha256(result.stdout.encode()).hexdigest(),
        "stderr_bytes": len(result.stderr.encode()),
        "stderr_sha256": hashlib.sha256(result.stderr.encode()).hexdigest(),
    })
    return result


def image_info(image: str, *, labels: bool = False) -> dict[str, object]:
    template = "{{.Id}}|{{.Os}}/{{.Architecture}}"
    if labels:
        template += "|{{json .Config.Labels}}"
    result = run(command_prefix() + ["image", "inspect", "--format", template, image], timeout=30)
    if result.returncode != 0:
        return {}
    fields = result.stdout.strip().split("|", 2)
    if len(fields) < 2 or not fields[0]:
        return {}
    info: dict[str, object] = {"id": fields[0], "platform": fields[1]}
    if labels:
        try:
            info["labels"] = json.loads(fields[2]) if len(fields) == 3 else {}
        except json.JSONDecodeError:
            return {}
    return info


def regular_files(paths: list[Path]) -> list[Path]:
    files: list[Path] = []
    for root in paths:
        if not root.exists():
            continue
        for path in root.rglob("*") if root.is_dir() else (root,):
            if path.is_symlink():
                raise ValueError(f"symlink is not allowed in package input: {path.relative_to(ROOT)}")
            if path.is_file():
                files.append(path)
    return files


def file_manifest(files: list[Path]) -> dict[str, str]:
    return {
        path.relative_to(ROOT).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(set(files), key=lambda item: item.relative_to(ROOT).as_posix())
    }


def manifest_digest(manifest: dict[str, str]) -> str:
    return hashlib.sha256(json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def frontend_files() -> dict[str, list[Path]]:
    result: dict[str, list[Path]] = {}
    for name, (_, entrypoint) in FRONTENDS.items():
        directory = ROOT / "web" / "dist" / name
        files = regular_files([directory])
        if not files or not (directory / entrypoint).is_file():
            raise ValueError(f"frontend build is missing web/dist/{name}/{entrypoint}")
        result[name] = files
    return result


def source_snapshot(fronts: dict[str, list[Path]]) -> dict[str, object]:
    identity = source_identity(ROOT)
    web_inputs = regular_files([ROOT / "web" / "src"])
    web_inputs.extend(ROOT / "web" / name for name in (
        "package.json", "bun.lock", "index.html", "admin.html", "portal.html",
        "vite.config.ts", "tsconfig.json", "tsconfig.test.json",
    ))
    web_inputs.extend(regular_files([ROOT / "deploy/app/nginx"]))
    web_inputs.append(Path(__file__).resolve())
    backend_roots = [ROOT / "cmd" / name for name in COMMANDS]
    backend_roots.append(ROOT / "internal")
    backend_inputs = [path for path in regular_files(backend_roots) if not path.name.endswith("_test.go")]
    backend_inputs.extend(ROOT / name for name in ("go.mod", "go.sum", "deploy/app/entrypoint.sh"))
    backend_inputs.append(Path(__file__).resolve())
    components = {
        "backend": file_manifest(backend_inputs),
        **{name: file_manifest([*web_inputs, *files]) for name, files in fronts.items()},
    }
    return {
        "algorithm": identity["algorithm"],
        "sha256": identity["digest"],
        "files": identity["files"],
        "frontend_files_sha256": {name: file_manifest(files) for name, files in fronts.items()},
        "component_manifests": components,
        "component_identity_sha256": {name: manifest_digest(manifest) for name, manifest in components.items()},
    }


def write_report(path: Path, report: dict[str, object]) -> None:
    path = path.expanduser()
    if not path.is_absolute():
        path = ROOT / path
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write("\n")


def toolchain_report() -> tuple[dict[str, object], list[str]]:
    failures: list[str] = []
    docker = command_prefix()
    go_binary = os.environ.get("GO_BIN", "go")
    bun_binary = os.environ.get("BUN_BIN", "bun")
    go = run([go_binary, "version"], timeout=30)
    bun = run([bun_binary, "--version"], timeout=30)
    docker_version = run(docker + ["--version"], timeout=30)
    compose_binary = os.environ.get("COMPOSE_BIN", "")
    compose_version = run(([compose_binary] if compose_binary else docker + ["compose"]) + ["version"], timeout=30)
    go_required_match = re.search(r"(?m)^go\s+(\d+)\.(\d+)(?:\.(\d+))?", (ROOT / "go.mod").read_text(encoding="utf-8"))
    go_actual_match = re.search(r"\bgo(\d+)\.(\d+)(?:\.(\d+))?\b", go.stdout)
    bun_match = re.search(r"(?m)^\s*\"packageManager\"\s*:\s*\"bun@([^\"]+)\"", (ROOT / "web/package.json").read_text(encoding="utf-8"))
    required_bun = bun_match.group(1) if bun_match else None
    actual_bun = bun.stdout.strip()
    if go.returncode != 0 or go_required_match is None or go_actual_match is None:
        failures.append("Go toolchain or go.mod version is unavailable")
    else:
        required_version = tuple(int(part or 0) for part in go_required_match.groups())
        actual_version = tuple(int(part or 0) for part in go_actual_match.groups())
        if actual_version < required_version:
            failures.append("Go toolchain is older than go.mod requires")
    if bun.returncode != 0 or required_bun is None or actual_bun != required_bun:
        failures.append("Bun toolchain does not match web/package.json packageManager")
    if docker_version.returncode != 0 or compose_version.returncode != 0:
        failures.append("Docker Engine and Compose are required")
    return ({
        "go": go.stdout.strip() if go.returncode == 0 else None,
        "go_required": go_required_match.group(0).split()[-1] if go_required_match else None,
        "bun": actual_bun if bun.returncode == 0 else None,
        "bun_required": required_bun,
        "docker": docker_version.stdout.strip() if docker_version.returncode == 0 else None,
        "compose": compose_version.stdout.strip() if compose_version.returncode == 0 else None,
    }, failures)


def online_base_inventory() -> dict[str, dict[str, object]]:
    return {image: image_info(image) for image in DOCKERFILE_BASES}


def preflight(base: str, *, verify: bool = False) -> tuple[dict[str, object], dict[str, list[Path]], list[str]]:
    failures: list[str] = []
    toolchain: dict[str, object] = {}
    if not verify:
        toolchain, tool_failures = toolchain_report()
        failures.extend(tool_failures)
    try:
        fronts = frontend_files()
    except (OSError, ValueError) as error:
        fronts = {}
        failures.append(str(error))
    base_info = image_info(base) if not verify else {}
    if not verify and not base_info:
        failures.append(f"offline base image is not cached: {base}")
    if base_info and not str(base_info.get("platform", "")).startswith("linux/"):
        failures.append(f"offline base is not Linux: {base}")
    try:
        identity = source_snapshot(fronts) if fronts else None
    except OSError as error:
        identity = None
        failures.append(f"cannot read source identity: {error}")
    inventory = online_base_inventory() if not verify else {}
    return ({
        "toolchain": toolchain,
        "offline_base": {"name": base, **base_info} if base_info else {"name": base, "status": "NOT_CHECKED" if verify else "MISSING"},
        "online_dockerfile_bases": {
            name: ({"status": "CACHED", **info} if info else {"status": "MISSING"})
            for name, info in inventory.items()
        },
        "source_identity": identity,
    }, fronts, failures)


def backend_dockerfile(base: str) -> str:
    return "\n".join([
        f"FROM {base}",
        "USER root",
        "RUN command -v pdfinfo && command -v pdftotext && command -v pdfimages && command -v pdftoppm && test -d /usr/share/poppler/cMap",
        "RUN mkdir -p /var/lib/contract-audit /run/contract-audit && chown nginx:nginx /var/lib/contract-audit /run/contract-audit",
        "RUN install -m 0444 /dev/null /run/contract-audit/identity-directory.json",
        "COPY bin/ /app/",
        "COPY entrypoint /app/entrypoint",
        "RUN chmod 0555 /app/entrypoint",
        "ENV DATA_DIR=/var/lib/contract-audit",
        "WORKDIR /app",
        "USER nginx:nginx",
        'ENTRYPOINT ["/app/entrypoint"]',
        'CMD ["/app/server"]',
        "",
    ])


def nginx_config() -> str:
    return "\n".join([
        "worker_processes auto;", "error_log /dev/stderr notice;", "pid /tmp/nginx/nginx.pid;",
        "events { worker_connections 1024; }", "http {", "    include /etc/nginx/mime.types;",
        "    default_type application/octet-stream;", "    sendfile on;", "    keepalive_timeout 65;",
        "    client_body_temp_path /tmp/nginx/client_body;", "    proxy_temp_path /tmp/nginx/proxy;",
        "    fastcgi_temp_path /tmp/nginx/fastcgi;", "    uwsgi_temp_path /tmp/nginx/uwsgi;",
        "    scgi_temp_path /tmp/nginx/scgi;", "    include /etc/nginx/conf.d/*.conf;", "}", "",
    ])


def frontend_dockerfile(base: str) -> str:
    return "\n".join([
        f"FROM {base}", "USER root", "COPY nginx.conf /etc/nginx/nginx.conf",
        "COPY default.conf /etc/nginx/conf.d/default.conf",
        "COPY backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf",
        "COPY proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf",
        "COPY dist/ /usr/share/nginx/html/",
        "RUN mkdir -p /tmp/nginx/client_body /tmp/nginx/proxy /tmp/nginx/fastcgi /tmp/nginx/uwsgi /tmp/nginx/scgi && chown -R nginx:nginx /tmp/nginx",
        "EXPOSE 8080", "USER nginx:nginx", 'ENTRYPOINT ["nginx", "-g", "daemon off;"]', "",
    ])


def build_image(docker: list[str], tag: str, base: str, base_info: dict[str, object], source_digest: str,
                component_digest: str, context: Path, packaging: str) -> dict[str, object]:
    command = docker + [
        "build", "--pull=false", "--no-cache", "--network=none", "--platform", str(base_info["platform"]),
        "-t", tag, "--label", f"contract-audit.packaging={packaging}",
        "--label", f"contract-audit.base={base}", "--label", f"contract-audit.base.id={base_info['id']}",
        "--label", f"contract-audit.source.identity={source_digest}",
        "--label", f"contract-audit.component.identity={component_digest}",
        "--label", f"contract-audit.input.manifest={component_digest}", str(context),
    ]
    result = run(command, timeout=1800)
    if result.returncode != 0:
        raise subprocess.CalledProcessError(result.returncode, command)
    info = image_info(tag, labels=True)
    if not info:
        raise RuntimeError(f"Docker built {tag} but could not read its local image identity")
    return {"tag": tag, "image_id": info["id"], "platform": info["platform"], "base": base,
            "base_id": base_info["id"], "source_identity_sha256": source_digest,
            "component_identity_sha256": component_digest, "packaging": packaging}


def verify_images(source: dict[str, object], base: str, targets: list[str]) -> tuple[list[dict[str, object]], list[str]]:
    failures: list[str] = []
    expected = source["component_identity_sha256"]
    assert isinstance(expected, dict)
    all_tags = [(BACKEND_TAG, "backend"), *((tag, name) for name, (tag, _) in FRONTENDS.items())]
    tags = [(tag, component) for tag, component in all_tags if component in targets]
    images: list[dict[str, object]] = []
    for tag, component in tags:
        info = image_info(tag, labels=True)
        labels = info.get("labels") if info else None
        if not info or not isinstance(labels, dict):
            failures.append(f"required image is missing or unreadable: {tag}")
            continue
        expected_component = expected[component]
        if labels.get("contract-audit.component.identity") != expected_component:
            failures.append(f"image inputs do not match current {component} inputs: {tag}")
        if labels.get("contract-audit.base") != base:
            failures.append(f"image base name does not match the authorized offline base: {tag}")
        images.append({"tag": tag, "image_id": info["id"], "platform": info["platform"],
                       "base": labels.get("contract-audit.base"), "base_id": labels.get("contract-audit.base.id"),
                       "built_from_source_identity_sha256": labels.get("contract-audit.source.identity"),
                       "component_identity_sha256": labels.get("contract-audit.component.identity")})
    return images, failures


def build_frontends(bun: str, names: list[str]) -> None:
    for name in names:
        result = run([bun, "run", f"build:{name}"], cwd=ROOT / "web", timeout=600)
        if result.returncode != 0:
            raise subprocess.CalledProcessError(result.returncode, [bun, "run", f"build:{name}"])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default=DEFAULT_OFFLINE_BASE, help="cached backend/frontend base image; never pulled")
    parser.add_argument("--preflight", action="store_true", help="record toolchain, image cache and frontend preflight")
    parser.add_argument("--verify", action="store_true", help="verify the current component inputs for all four Compose tags")
    parser.add_argument("--only", default="all", help="comma-separated build targets: backend,client,admin,portal (default all)")
    parser.add_argument("--report", default="", help="new evidence JSON path; existing reports are never overwritten")
    parser.add_argument("--export", default="", help="optional Docker archive for an explicitly authorized offline transfer")
    args = parser.parse_args(argv)
    if args.preflight and args.verify:
        parser.error("--preflight and --verify are separate modes")
    targets = ["backend", "client", "admin", "portal"] if args.only == "all" else args.only.split(",")
    if not targets or len(set(targets)) != len(targets) or set(targets) - {"backend", "client", "admin", "portal"}:
        parser.error("--only must name unique targets from backend,client,admin,portal")

    report: dict[str, object] = {
        "schema_version": "ev02-app-images/v1", "task_id": "EV-02",
        "mode": "preflight" if args.preflight else ("verify" if args.verify else "offline-build"),
        "parent_argv": [sys.executable, *sys.argv], "started_at_unix_ns": time.time_ns(),
        "status": "FAIL", "subprocesses": SUBPROCESSES,
    }
    try:
        preflight_report, fronts, failures = preflight(args.base, verify=args.verify)
        report.update(preflight_report)
        if args.preflight:
            report["status"] = "PASS" if not failures else "BLOCKED"
            report["reasons"] = failures
            exit_code = 0 if not failures else 2
        elif failures:
            report["status"] = "BLOCKED"
            report["reasons"] = failures
            exit_code = 2
        else:
            if not args.verify and any(name != "backend" for name in targets):
                bun = os.environ.get("BUN_BIN", "bun")
                build_frontends(bun, [name for name in ("client", "admin", "portal") if name in targets])
                fronts = frontend_files()
                preflight_report["source_identity"] = source_snapshot(fronts)
                report["source_identity"] = preflight_report["source_identity"]
            source = preflight_report["source_identity"]
            assert isinstance(source, dict)
            report["source_identity_sha256"] = source["sha256"]
            report["frontend_files_sha256"] = source["frontend_files_sha256"]
            report["component_identity_sha256"] = source["component_identity_sha256"]
            if args.verify:
                images, verification_failures = verify_images(source, args.base, targets)
                report["images"] = images
                report["reasons"] = verification_failures
                report["status"] = "PASS" if not verification_failures else "BLOCKED"
                exit_code = 0 if not verification_failures else 2
            else:
                goos, goarch = str(preflight_report["offline_base"]["platform"]).split("/", 1)
                if goos != "linux":
                    raise RuntimeError(f"backend runtime requires a Linux base, got {goos}")
                docker = command_prefix()
                base_info = preflight_report["offline_base"]
                assert isinstance(base_info, dict)
                source_digest = str(source["sha256"])
                components = source["component_identity_sha256"]
                assert isinstance(components, dict)
                images: list[dict[str, object]] = []
                binaries: dict[str, str] = {}
                with tempfile.TemporaryDirectory(prefix="contract-audit-ev02-images-") as temporary:
                    workspace = Path(temporary)
                    if "backend" in targets:
                        backend = workspace / "backend"
                        binary_dir = backend / "bin"
                        binary_dir.mkdir(parents=True)
                        go = os.environ.get("GO_BIN", "go")
                        build_command = ["env", "CGO_ENABLED=0", "GOOS=linux", f"GOARCH={goarch}", go,
                                         "build", "-trimpath", "-ldflags=-s -w", "-o", str(binary_dir) + "/",
                                         *(f"./cmd/{name}" for name in COMMANDS)]
                        build = run(build_command, timeout=1200)
                        if build.returncode != 0:
                            raise subprocess.CalledProcessError(build.returncode, build_command)
                        binaries = {name: hashlib.sha256((binary_dir / name).read_bytes()).hexdigest()
                                    for name in COMMANDS if (binary_dir / name).is_file()}
                        missing = sorted(set(COMMANDS) - set(binaries))
                        if missing:
                            raise RuntimeError(f"host build did not produce required commands: {', '.join(missing)}")
                        (backend / "entrypoint").write_text((ROOT / "deploy/app/entrypoint.sh").read_text(encoding="utf-8"), encoding="utf-8")
                        (backend / "Dockerfile").write_text(backend_dockerfile(args.base), encoding="utf-8")
                        images.append(build_image(docker, BACKEND_TAG, args.base, base_info, source_digest,
                                                  str(components["backend"]), backend, "offline-host-binaries"))
                    for name, (tag, _) in FRONTENDS.items():
                        if name not in targets:
                            continue
                        context = workspace / name
                        context.mkdir()
                        shutil.copytree(ROOT / "web/dist" / name, context / "dist")
                        shutil.copy2(ROOT / "deploy/app/nginx" / f"{name}.conf", context / "default.conf")
                        for filename in ("backend-proxy.conf", "proxy-headers.conf"):
                            shutil.copy2(ROOT / "deploy/app/nginx" / filename, context / filename)
                        (context / "nginx.conf").write_text(nginx_config(), encoding="utf-8")
                        (context / "Dockerfile").write_text(frontend_dockerfile(args.base), encoding="utf-8")
                        images.append(build_image(docker, tag, args.base, base_info, source_digest,
                                                  str(components[name]), context, "offline-static-web-dist"))
                report["binary_sha256"] = binaries if "backend" in targets else None
                report["images_built"] = images
                verified_images, verification_failures = verify_images(source, args.base, targets)
                report["images"] = verified_images
                if verification_failures:
                    report["status"] = "BLOCKED"
                    report["reasons"] = verification_failures
                    exit_code = 2
                else:
                    if args.export:
                        archive = Path(args.export).expanduser().resolve()
                        if archive.exists():
                            raise FileExistsError(f"refusing to overwrite offline image archive: {archive}")
                        archive.parent.mkdir(parents=True, exist_ok=True)
                        tags = [BACKEND_TAG, *(tag for tag, _ in FRONTENDS.values())]
                        save_command = docker + ["image", "save", "--output", str(archive), *tags]
                        saved = run(save_command, timeout=1800)
                        if saved.returncode != 0:
                            raise subprocess.CalledProcessError(saved.returncode, save_command)
                        report["offline_archive"] = {
                            "path": str(archive), "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
                            "size_bytes": archive.stat().st_size,
                            "import_argv": [docker[0], "image", "load", "--input", str(archive)],
                        }
                    report["status"] = "PASS"
                    exit_code = 0
    except subprocess.TimeoutExpired as error:
        report["status"] = "BLOCKED"
        report["error"] = "subprocess_timeout"
        report["timeout_seconds"] = error.timeout
        exit_code = 124
    except subprocess.CalledProcessError as error:
        report["status"] = "FAIL"
        report["error"] = "subprocess_failed"
        report["failing_returncode"] = error.returncode
        exit_code = error.returncode if error.returncode > 0 else 1
    except (OSError, ValueError, RuntimeError, KeyError, AssertionError) as error:
        report["status"] = "FAIL"
        report["error"] = str(error)
        exit_code = 1
    report["finished_at_unix_ns"] = time.time_ns()
    report["parent_exit_code"] = exit_code
    report_path = Path(args.report) if args.report else Path("artifacts/c1c/ev-02") / f"app-images-{report['started_at_unix_ns']}.json"
    try:
        write_report(report_path, report)
    except OSError as error:
        print(f"FAIL: cannot write image evidence: {error}", file=sys.stderr)
        return 1
    absolute_report = report_path.expanduser()
    if not absolute_report.is_absolute():
        absolute_report = ROOT / absolute_report
    print(json.dumps({"status": report["status"], "parent_exit_code": exit_code,
                      "source_identity_sha256": report.get("source_identity_sha256"),
                      "report": str(absolute_report.resolve())}, ensure_ascii=False, indent=2))
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())

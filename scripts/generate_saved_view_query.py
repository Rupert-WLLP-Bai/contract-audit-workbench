"""Regenerate the Contract saved-view query and its repository metadata mirrors."""

import json
import os
import subprocess
from pathlib import Path

from file_metadata.metadata_core import STYLES, insert_header

ROOT = Path(__file__).resolve().parents[1]
CONFIG = Path("internal/contract/postgres/sqlc.json")
OUTPUT = CONFIG.parent / "internal/savedviewquery"
FILES = tuple(OUTPUT / name for name in ("db.go", "models.go", "saved_views.sql.go"))
SQLC_VERSION = "v1.31.1"


def main() -> None:
    sqlc = os.environ.get("SQLC", "sqlc")
    version = subprocess.check_output([sqlc, "version"], text=True).strip()
    if version != SQLC_VERSION:
        raise SystemExit(f"sqlc {SQLC_VERSION} is required, found {version}")
    manifest = ROOT / "scripts/file_metadata/manifest.json"
    records = json.loads(manifest.read_text(encoding="utf-8"))["files"]
    metadata = {path: records[path.as_posix()] for path in FILES}
    subprocess.run([sqlc, "generate", "--no-remote", "-f", CONFIG], check=True, cwd=ROOT)
    for path, record in metadata.items():
        target = ROOT / path
        target.write_bytes(
            insert_header(
                target.read_bytes(),
                record,
                style=STYLES["slash"],
                handling="header-and-manifest",
                file_kind="go",
                mode="100644",
            )
        )


if __name__ == "__main__":
    main()

"""Hash the executable source and build/gate configuration bound to a test run."""
import hashlib
import json
from pathlib import Path

EXCLUDED = {"node_modules", "dist", "coverage", "__pycache__", ".vite", ".git"}


def source_identity(root):
    root = Path(root)
    files = []
    for directory in ("cmd", "internal", "e2e", "web", "scripts", ".github", "deploy", "deployments",
                      "testdata"):
        for path in (root / directory).rglob("*"):
            relative = path.relative_to(root)
            if (not path.is_file() or path.is_symlink() or
                    any(part in EXCLUDED for part in relative.parts) or path.suffix == ".pyc"):
                continue
            # Documentation may evolve after a code run; it is separately
            # covered by the final release manifest rather than test identity.
            if path.suffix.lower() in (".md", ".txt") and directory not in ("internal", "e2e"):
                continue
            files.append(path)
    files += [root / name for name in ("go.mod", "go.sum", "quality-gates.json", "Makefile", "compose.yaml", ".env.example")]
    for name in ("compose.app.yaml", "compose.observe.yaml"):
        path = root / name
        if path.is_file():
            files.append(path)
    records = [{"path": p.relative_to(root).as_posix(), "sha256": hashlib.sha256(p.read_bytes()).hexdigest()}
               for p in sorted(set(files))]
    payload = json.dumps(records, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return {"algorithm": "sha256-sorted-source-manifest-v1", "digest": hashlib.sha256(payload).hexdigest(), "files": records}

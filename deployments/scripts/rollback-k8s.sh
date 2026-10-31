#!/usr/bin/env bash
set -eu

# The Python entrypoint checks the historical revision, both release locks,
# compatibility window and the existing explicit confirmation before changing
# this one Deployment. It does not touch the database.
exec python3 "$(dirname "$0")/release-k8s.py" rollback "$@"

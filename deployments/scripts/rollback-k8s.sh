#!/usr/bin/env bash
# File metadata (manifest mirror):
# Purpose: Dispatches the release rollback subcommand to release-k8s.py; the Python entrypoint verifies historical revision/lock compatibility and changes only the selected Deployment, never the database.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-25
# Updated: 2026-10-31
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

set -eu

# The Python entrypoint checks the historical revision, both release locks,
# compatibility window and the existing explicit confirmation before changing
# this one Deployment. It does not touch the database.
exec python3 "$(dirname "$0")/release-k8s.py" rollback "$@"

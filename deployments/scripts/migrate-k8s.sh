#!/usr/bin/env bash
# File metadata (manifest mirror):
# Purpose: Dispatches the release migration subcommand to release-k8s.py, which consumes the backend digest from the fixed candidate lock and retains the Kubernetes Job as migration evidence.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-25
# Updated: 2026-10-31
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

set -eu

# The migration Job consumes the exact backend digest from this release lock.
exec python3 "$(dirname "$0")/release-k8s.py" migrate "$@"

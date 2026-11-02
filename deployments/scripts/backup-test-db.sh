#!/usr/bin/env bash
# File metadata (manifest mirror):
# Purpose: Runs exec python3 "$(dirname "$0")/test-recovery.py" backup-db "$@"; this shell wrapper is the operational entry point for the named local deployment or maintenance task.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-25
# Updated: 2026-10-31
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

set -eu
exec python3 "$(dirname "$0")/test-recovery.py" backup-db "$@"

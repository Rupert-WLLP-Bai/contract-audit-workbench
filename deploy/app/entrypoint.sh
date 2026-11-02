#!/bin/sh
# File metadata (manifest mirror):
# Purpose: Runs exec "$@"; this shell wrapper is the operational entry point for the named local deployment or maintenance task.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-25
# Updated: 2026-10-25
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

set -eu

case "${APP_ENV:-}" in
    development|test|production) ;;
    *)
        printf '%s\n' 'APP_ENV must be development, test, or production' >&2
        exit 1
        ;;
esac
exec "$@"

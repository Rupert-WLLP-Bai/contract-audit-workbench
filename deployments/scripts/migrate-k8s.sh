#!/usr/bin/env bash
set -eu

# The migration Job consumes the exact backend digest from this release lock.
exec python3 "$(dirname "$0")/release-k8s.py" migrate "$@"

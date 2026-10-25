#!/bin/sh
set -eu

case "${APP_ENV:-}" in
    development|test|production) ;;
    *)
        printf '%s\n' 'APP_ENV must be development, test, or production' >&2
        exit 1
        ;;
esac
exec "$@"

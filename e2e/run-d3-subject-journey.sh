#!/usr/bin/env bash
# Capture the outer Go test's real exit status as well as its complete output.
set -uo pipefail
umask 077

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P) || exit 1
if [[ -z "${CAW_DELIVERY_DIR:-}" ]]; then
  printf '%s\n' 'CAW_DELIVERY_DIR must name an existing directory outside the source checkout' >&2
  exit 1
fi
delivery=$(realpath -e -- "$CAW_DELIVERY_DIR") || exit 1
if [[ ! -d "$delivery" ]]; then
  printf '%s\n' 'CAW_DELIVERY_DIR must be a directory' >&2
  exit 1
fi
case "$delivery/" in
  "$repo_root/"*) printf '%s\n' 'CAW_DELIVERY_DIR must be outside the source checkout' >&2; exit 1 ;;
esac

run_dir=$(mktemp -d "$delivery/p8-d3-go-XXXXXXXX") || exit 1
printf 'NOT_RECORDED\n' > "$run_dir/go-test.exit" || exit 1
printf 'NOT_RECORDED\n' > "$run_dir/source-identity-comparison.txt" || exit 1
printf '%s\n' "$repo_root" > "$run_dir/go-test.cwd" || exit 1
printf 'D3 evidence directory: %s\n' "$run_dir"

capture_identity() {
  python3 -B -c 'import json, sys; sys.path.insert(0, sys.argv[1]); from source_identity import source_identity; print(json.dumps(source_identity(sys.argv[2]), ensure_ascii=False, sort_keys=True))' \
    "$repo_root/scripts" "$repo_root" > "$1"
}
if ! capture_identity "$run_dir/source-manifest-before.json"; then
  printf '%s\n' 'Could not capture candidate source identity before Go test' >&2
  exit 1
fi

export CAW_DELIVERY_DIR="$run_dir"
cd -- "$repo_root" || exit 1
go test -tags browser -run '^TestBrowserD3SubjectControlledSourceSOD$' -count=1 -v ./e2e 2>&1 | tee "$run_dir/go-test.log"
statuses=("${PIPESTATUS[@]}")
go_status=${statuses[0]}
log_status=${statuses[1]}
printf '%s\n' "$go_status" > "$run_dir/go-test.exit" || exit 1
printf '%s\n' "$log_status" > "$run_dir/log-writer.exit" || exit 1

identity_status=1
if capture_identity "$run_dir/source-manifest-after.json"; then
  if cmp -s "$run_dir/source-manifest-before.json" "$run_dir/source-manifest-after.json"; then
    printf 'PASS\n' > "$run_dir/source-identity-comparison.txt" || exit 1
    identity_status=0
  else
    printf 'FAIL\n' > "$run_dir/source-identity-comparison.txt" || exit 1
    printf '%s\n' 'Candidate source changed during Go test' >&2
  fi
else
  printf '%s\n' 'Could not capture candidate source identity after Go test' >&2
fi

if (( go_status != 0 )); then exit "$go_status"; fi
if (( log_status != 0 )); then exit "$log_status"; fi
exit "$identity_status"

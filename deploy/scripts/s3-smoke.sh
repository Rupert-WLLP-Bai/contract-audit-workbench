#!/bin/sh
set -eu
: "${S3_ENDPOINT:?}" "${S3_BUCKET:?}"
aws_local() {
  aws --endpoint-url "$S3_ENDPOINT" --region us-east-1 \
    --cli-connect-timeout 3 --cli-read-timeout 5 "$@"
}
work=$(mktemp -d)
key="_smoke/$(date +%s)-$$-$(basename "$work").txt"
cleanup() {
  aws_local s3api delete-object --bucket "$S3_BUCKET" --key "$key" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM
printf '%s\n' 'Contract Audit synthetic S3 roundtrip; no business contract data.' > "$work/source.txt"
aws_local s3api put-object --bucket "$S3_BUCKET" --key "$key" --body "$work/source.txt" >/dev/null
aws_local s3api get-object --bucket "$S3_BUCKET" --key "$key" "$work/restored.txt" >/dev/null
expected=$(sha256sum "$work/source.txt" | cut -d ' ' -f 1)
actual=$(sha256sum "$work/restored.txt" | cut -d ' ' -f 1)
[ "$expected" = "$actual" ] || { echo 'S3 byte hash mismatch' >&2; exit 1; }
aws_local s3api delete-object --bucket "$S3_BUCKET" --key "$key" >/dev/null
count=$(aws_local s3api list-objects-v2 --bucket "$S3_BUCKET" --prefix "$key" --query 'length(Contents || `[]`)' --output text)
[ "$count" = 0 ] || { echo 'S3 object remained after deletion' >&2; exit 1; }
printf 'PASS: S3 put/get/delete and SHA256 %s\n' "$actual"

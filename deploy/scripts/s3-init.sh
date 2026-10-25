#!/bin/sh
set -eu
: "${S3_ENDPOINT:?}" "${S3_BUCKET:?}" "${AWS_ACCESS_KEY_ID:?}" "${AWS_SECRET_ACCESS_KEY:?}"
aws_local() {
  aws --endpoint-url "$S3_ENDPOINT" --region us-east-1 \
    --cli-connect-timeout 3 --cli-read-timeout 5 "$@"
}
attempt=0
until aws_local s3api list-buckets >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "S3 startup/authentication failed after bounded retries" >&2
    exit 1
  fi
  sleep 2
done
if ! aws_local s3api head-bucket --bucket "$S3_BUCKET" >/dev/null 2>&1; then
  aws_local s3api create-bucket --bucket "$S3_BUCKET" >/dev/null
fi
aws_local s3api head-bucket --bucket "$S3_BUCKET"
printf '%s\n' 'PASS: local S3 bucket exists and is authenticated; application S3 integration remains pending'

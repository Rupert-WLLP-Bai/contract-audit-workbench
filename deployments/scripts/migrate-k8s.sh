#!/usr/bin/env bash
set -eu

namespace=${KUBE_NAMESPACE:-contract-audit}
kubectl_args=()
if [ -n "${KUBE_CONTEXT:-}" ]; then
    kubectl_args+=(--context "$KUBE_CONTEXT")
fi
kubectl "${kubectl_args[@]}" wait --namespace "$namespace" --for=condition=Ready cluster/contract-audit-db --timeout=20m
job_ref=$(kubectl "${kubectl_args[@]}" create --namespace "$namespace" -f "$(dirname "$0")/../k8s/base/migration-job.yaml" -o name)
printf 'Created independent migration %s\n' "$job_ref"
if ! kubectl "${kubectl_args[@]}" wait --namespace "$namespace" --for=condition=Complete "$job_ref" --timeout=20m; then
    kubectl "${kubectl_args[@]}" logs --namespace "$namespace" "$job_ref" || true
    exit 1
fi
kubectl "${kubectl_args[@]}" logs --namespace "$namespace" "$job_ref"

#!/usr/bin/env bash
set -eu

namespace=contract-audit-test
if [ "${CONTRACT_AUDIT_TEST_BACKUP_CONFIRM:-}" != "backup-contract-audit-test-db" ]; then
    printf '%s\n' 'Set CONTRACT_AUDIT_TEST_BACKUP_CONFIRM=backup-contract-audit-test-db to run this backup.' >&2
    exit 2
fi

kubectl_args=()
if [ -n "${KUBE_CONTEXT:-}" ]; then
    kubectl_args+=(--context "$KUBE_CONTEXT")
fi

if [ "$(kubectl "${kubectl_args[@]}" get namespace "$namespace" -o 'jsonpath={.metadata.labels.environment}')" != test ]; then
    printf '%s\n' 'Refusing backup: the target namespace is not labeled environment=test.' >&2
    exit 2
fi

kubectl "${kubectl_args[@]}" wait --namespace "$namespace" --for=condition=Ready cluster/contract-audit-test-db --timeout=15m
backup_ref=$(kubectl "${kubectl_args[@]}" create --namespace "$namespace" -f "$(dirname "$0")/../k8s/test/backup.yaml" -o name)
backup_name=${backup_ref#*/}
printf 'Created %s\n' "$backup_ref"
kubectl "${kubectl_args[@]}" wait --namespace "$namespace" --for=jsonpath='{.status.phase}'=completed "backup/$backup_name" --timeout=60m
kubectl "${kubectl_args[@]}" get --namespace "$namespace" "backup/$backup_name" -o wide

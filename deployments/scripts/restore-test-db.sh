#!/usr/bin/env bash
set -eu

namespace=contract-audit-test
if [ "${CONTRACT_AUDIT_TEST_RESTORE_CONFIRM:-}" != "restore-contract-audit-test-db-to-new-cluster" ]; then
    printf '%s\n' 'Set CONTRACT_AUDIT_TEST_RESTORE_CONFIRM=restore-contract-audit-test-db-to-new-cluster to create a separate restore cluster.' >&2
    exit 2
fi

kubectl_args=()
if [ -n "${KUBE_CONTEXT:-}" ]; then
    kubectl_args+=(--context "$KUBE_CONTEXT")
fi

if [ "$(kubectl "${kubectl_args[@]}" get namespace "$namespace" -o 'jsonpath={.metadata.labels.environment}')" != test ]; then
    printf '%s\n' 'Refusing restore: the target namespace is not labeled environment=test.' >&2
    exit 2
fi

kubectl "${kubectl_args[@]}" get --namespace "$namespace" objectstore/contract-audit-test-backups >/dev/null
backups=$(kubectl "${kubectl_args[@]}" get --namespace "$namespace" backup -o name)
if [ -z "$backups" ]; then
    printf '%s\n' 'No backup objects are available in the isolated test namespace.' >&2
    exit 2
fi
restore_ref=$(kubectl "${kubectl_args[@]}" create --namespace "$namespace" -f "$(dirname "$0")/../k8s/test/restore-cluster.yaml" -o name)
printf 'Created isolated restore target %s; the source cluster and backup remain unchanged.\n' "$restore_ref"
kubectl "${kubectl_args[@]}" wait --namespace "$namespace" --for=condition=Ready "$restore_ref" --timeout=60m
kubectl "${kubectl_args[@]}" get --namespace "$namespace" "$restore_ref" -o wide

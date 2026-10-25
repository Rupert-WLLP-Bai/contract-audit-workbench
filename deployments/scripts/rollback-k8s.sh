#!/usr/bin/env bash
set -eu

if [ "$#" -ne 1 ]; then
    printf '%s\n' 'Usage: rollback-k8s.sh <gateway|core-api|document-service|audit-execution|audit-worker|model-gateway|tool-gateway|client|admin|portal>' >&2
    exit 2
fi
deployment=$1
case "$deployment" in
    gateway|core-api|document-service|audit-execution|audit-worker|model-gateway|tool-gateway|client|admin|portal) ;;
    *)
        printf '%s\n' 'Refusing unknown deployment.' >&2
        exit 2
        ;;
esac
if [ "${CONTRACT_AUDIT_ROLLBACK_CONFIRM:-}" != "application-deployment-only" ]; then
    printf '%s\n' 'Set CONTRACT_AUDIT_ROLLBACK_CONFIRM=application-deployment-only to roll back this Deployment revision.' >&2
    exit 2
fi

namespace=${KUBE_NAMESPACE:-contract-audit}
kubectl_args=()
if [ -n "${KUBE_CONTEXT:-}" ]; then
    kubectl_args+=(--context "$KUBE_CONTEXT")
fi
kubectl "${kubectl_args[@]}" rollout undo --namespace "$namespace" "deployment/$deployment"
kubectl "${kubectl_args[@]}" rollout status --namespace "$namespace" "deployment/$deployment" --timeout=10m

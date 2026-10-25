#!/usr/bin/env python3
"""Executable G6 contract examples, not an application cache or origin adapter.

The application does not import this module. Passing these examples proves the
specified admission/closure decisions only; enabling result reuse still requires
C1-A and the PostgreSQL/authorization gates in PERFORMANCE-CACHE-SPEC.md.
"""
from dataclasses import dataclass
import hashlib
import hmac
import json


@dataclass(frozen=True)
class Identity:
    tenant: str
    case: str
    artifact: str
    parsed_digest: str
    rule_hash: str
    model_revision: str
    policy_epoch: str
    dependency_root: str
    schema: str = "cache-contract-example/v1"


@dataclass(frozen=True)
class Authorization:
    tenant: str
    case: str
    epoch: str
    allowed: bool


@dataclass(frozen=True)
class Entry:
    identity: Identity
    artifact_digest: str
    expected_digest: str
    state: str = "ready"


def exact_key(identity, trusted_secret):
    """Canonicalization is limited to this fixed all-string example schema."""
    fields = vars(identity)
    if not trusted_secret or any(type(value) is not str or not value for value in fields.values()):
        raise ValueError("incomplete identity or missing trusted key")
    raw = json.dumps(fields, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return hmac.new(trusted_secret, raw, hashlib.sha256).hexdigest()


def inspect_reuse(identity, authorize, lookup, trusted_secret, *, immutable_gate=False):
    """Run the contract's authorization-before-lookup and return-time check.

    authorize and lookup represent independent authoritative reads in examples.
    No result bytes are served and no origin work is dispatched by this function.
    """
    def permitted(current):
        return (current.allowed and current.tenant == identity.tenant and
                current.case == identity.case and current.epoch == identity.policy_epoch)

    current = authorize()
    if not permitted(current):
        return "denied"
    if not immutable_gate:
        return "bypass_immutable_gate"
    key = exact_key(identity, trusted_secret)
    entry = lookup(key)
    if entry is None:
        return "miss"
    if (entry.identity != identity or entry.state != "ready" or
            not entry.artifact_digest or entry.artifact_digest != entry.expected_digest):
        return "identity_mismatch"
    if not permitted(authorize()):
        return "denied"
    return "hit_reference_only"


def invalidation_closure(nodes, dependencies, changed, *, complete=True, deleted=False):
    """Return ('full', all nodes) when the dependency proof is incomplete."""
    nodes, changed = set(nodes), set(changed)
    fallback = ("full", frozenset(nodes))
    if not complete or deleted or not changed.issubset(nodes):
        return fallback
    consumers = {node: set() for node in nodes}
    remaining = {node: 0 for node in nodes}
    for consumer, source in set(dependencies):
        if consumer not in nodes or source not in nodes:
            return fallback
        consumers[source].add(consumer)
        remaining[consumer] += 1
    ready = [node for node in nodes if remaining[node] == 0]
    visited = 0
    while ready:
        node = ready.pop()
        visited += 1
        for consumer in consumers[node]:
            remaining[consumer] -= 1
            if remaining[consumer] == 0:
                ready.append(consumer)
    if visited != len(nodes):
        return fallback
    affected, pending = set(changed), list(changed)
    while pending:
        for consumer in consumers[pending.pop()]:
            if consumer not in affected:
                affected.add(consumer)
                pending.append(consumer)
    return "incremental", frozenset(affected)

#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Selects existing changed sources and sensitive regression scope from Git differences, orchestrates pinned native lint/format tools and scoped checks, and preserves explicit execution evidence.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-10-31
# Updated: 2026-11-14
# Revision: r7
# Copyright: See LICENSE
# End file metadata.

"""Scoped fast checks for pull requests.

The diff spans every commit since the merge-base of ``--base`` (or
``CAW_FAST_BASE``, default ``origin/main``) plus staged, unstaged, and untracked
worktree files. Deletions, renames, and paths containing spaces are preserved.

``--plan`` prints one JSON line with the booleans ``go``, ``web``, ``python``,
``database``, ``docs_build``, ``architecture`` and ``ui_terminology`` (plus ``poppler`` and ``legal_assets``). ``--github-output``
appends those names as ``true`` or ``false`` so a GitHub Actions job can
conditionally install tools or databases.

All logs and the JSON report are written outside the repository.
"""

from __future__ import annotations

import argparse
import ast
from dataclasses import dataclass, field
from datetime import datetime, timezone
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import tokenize
from typing import Callable, Iterable
from go_test_scope import discover_unit_test_packages, unit_test_environment

REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_BASE = "origin/main"
PLAN_KEYS = (
    "go",
    "web",
    "python",
    "database",
    "docs_build",
    "architecture",
    "ui_terminology",
    "workflow",
)
GITHUB_KEYS = PLAN_KEYS + ("poppler", "legal_assets")
POPPLER_TEST_PACKAGES = frozenset({"./internal/documentparse"})
LEGAL_ASSET_TEST_PACKAGES = frozenset({"./internal/httpapi", "./internal/legalnorm"})
SKIP_PARTS = frozenset(
    {
        ".git",
        ".mypy_cache",
        ".pytest_cache",
        ".ruff_cache",
        ".venv",
        "__pycache__",
        "artifacts",
        "coverage",
        "dist",
        "node_modules",
        "vendor",
        "venv",
    }
)
GO_SKIP_PARTS = frozenset({".git", ".venv", "artifacts", "dist", "node_modules", "vendor", "web"})
DB_ENV_KEYS = ("TEST_DATABASE_URL", "MIGRATOR_DATABASE_URL", "MODEL_DATABASE_URL")

WEB_BUILD_FILES = frozenset(
    {
        "admin.html",
        "bun.lock",
        "bun.lockb",
        "bunfig.toml",
        "index.html",
        "package.json",
        "playwright.functional.config.ts",
        "playwright.oidc.config.ts",
        "portal.html",
        "tsconfig.json",
        "tsconfig.test.json",
        "vite.config.ts",
        "vitest.config.ts",
    }
)
WEB_SOURCE_SUFFIXES = frozenset(
    {
        ".cjs",
        ".css",
        ".html",
        ".js",
        ".json",
        ".jsx",
        ".mjs",
        ".scss",
        ".toml",
        ".ts",
        ".tsx",
    }
)
WEB_CODE_SUFFIXES = frozenset({".cjs", ".js", ".jsx", ".mjs", ".ts", ".tsx"})
WEB_FORMAT_SUFFIXES = frozenset(
    {".cjs", ".css", ".html", ".js", ".jsx", ".json", ".mjs", ".scss", ".ts", ".tsx"}
)
WEB_GENERATED_FILES = frozenset({"web/src/api/wire.generated.ts"})
WEB_EXCLUDED_PARTS = frozenset(
    {"coverage", "dist", "fixtures", "node_modules", "playwright-report", "test-results"}
)
RUFF_CONFIG_FILES = frozenset({"requirements-dev.txt", "ruff.toml"})

AUTH_TESTS = (
    "TestGovernanceServiceRoleBoundaries",
    "TestLoginOriginalAndRetryStayInPostgres",
)
SESSION_TESTS = (
    "TestConcurrentLogoutCannotBeResurrectedByCaseWrite",
    "TestSessionNarrowWritesKeepAuthorityAndRevocation",
)
TENANT_TESTS = (
    "TestCrossOrganizationGenerationPreservesCaseIdentity",
    "TestGovernanceCrossTenantIsolation",
    "TestMaterialTablesEnforceTenantRLSAndAppendOnlyServiceOwnership",
)
TENANT_IDENTITY_TESTS = (
    "TestTenantIdentityResolverAndPendingMembershipCoreRole",
    "TestTenantIdentityNonSuperMigratorProvisioningAndMigration",
)
TENANT_IDENTITY_HTTP_TESTS = (
    "TestOIDCAuthorizationCodeHTTPFlowsUseAudienceBoundPostgresSessions",
    "TestOIDCAuthorizationCodeSeparatesClientAndAdminOrigins",
    "TestOIDCPendingMembershipHTTPChoiceIsSameOriginAndOneTime",
)
TENANT_AUTHORITY_BOOTSTRAP_TESTS = (
    "TestDistributedDocumentIntakeRedeemsCoreTicket",
    "TestToolGatewaySessionAuthorityPreservesDatabaseMembershipOverHTTP",
    "TestToolGatewayClaimAuthorityRechecksLiveFrozenJobThroughCoreHTTP",
    "TestDelegatedPreparationRejectsForgedClaimFieldsAndRevokedReview",
    "TestDistributedMembershipAdmissionAndModelSettlement",
)
OFFLINE_MIGRATOR_BOOTSTRAP_TESTS = ("TestOfflineMigratorFreshAndControlledLegacyBootstrap",)
CASE_INTAKE_AUTHORITY_TESTS = (
    "TestCaseIntakePendingOwnerResumeArchiveAndReceiptPostgres",
    "TestCaseIntakePendingLegacyAndCurrentCaseAuthorityPostgres",
    "TestCaseIntakeRecoversAcrossBoundSessionsPostgres",
    "TestCaseIntakeSessionRecoveryBoundariesPostgres",
    "TestCaseIntakeRecoveryUsesCurrentMembershipPostgres",
    "TestIntakeRecoveryPolicyProjectionExecuteRequirement",
)
MIGRATION_TESTS = (
    "TestEvaluationProvenanceMigrationPreservesHistoricalRows",
    "TestContractAggregateMigrationBoundaries",
)
BUDGET_TESTS = ("TestModelBudgetRoleIsolationAndFunctionPrivileges",)
LEASE_TESTS = ("TestStaleGenerationCompletionIsRejected",)
DATABASE_CORE_TESTS = ("TestLoginOriginalAndRetryStayInPostgres",)
FROZEN_EVIDENCE_TESTS = (
    "TestDurableReviewFreezesServiceObjectContextAndRejectsChangedRetryAfterReopen",
)
REVIEW_READMISSION_TESTS = (
    "TestReviewReadmissionPreparingScanPostgres",
    "TestReviewReadmissionDifferentKeysAndBoundariesPostgres",
    "TestReviewReadmissionFrozenAndRevokedPostgres",
    "TestReviewReadmissionRejectsExpiredEpochPostgres",
    "TestReviewReceiptReplayAfterNewSessionPostgres",
    "TestReviewReceiptConcurrentSessionsPostgres",
    "TestReviewReceiptSessionAndServiceBoundariesPostgres",
    "TestReviewReceiptRejectsChangedEpochPostgres",
    "TestReviewReceiptCurrentMembershipPostgres",
    "TestReviewReceiptPolicyProjectionExecuteRequirement",
    "TestReviewReceiptLegacyMigrationPreservesPostgres",
    "TestReviewReceiptOriginDiffersFromCaseOrganizationPostgres",
    "TestCaseContextFenceMigrationUpgradeAndRoleACL",
)
REVIEW_READMISSION_HTTP_TESTS = ("TestCancelledPreparingReadmissionHTTP",)
REPORT_RECEIPT_TESTS = (
    "TestReportReceiptReplayAfterNewSessionPostgres",
    "TestReportReceiptReplayPreservesSessionOriginOrganizationPostgres",
)
REPORT_RECEIPT_HTTP_TESTS = ("TestReportReceiptReplayAfterLoginHTTP",)
CONTRACT_SIGNING_TESTS = ("TestContractSigningRequiresHumanSourceAttestationAndApprovedRevision",)
CONTRACT_FULFILLMENT_TESTS = (
    "TestContractFulfillmentPartialAcceptanceAndLinearRetest",
    "TestContractFulfillmentLegacyMigrationSkipsUnknownPlans",
)
CONTRACT_PAYMENT_TESTS = ("TestContractPaymentEligibilityAndTrustedReceiptFacts",)
CONTRACT_ASSET_TESTS = ("TestContractAssetHandoverAndERPReceiptFacts",)
CONTRACT_SOURCE_TESTS = (
    "TestExternalContractSourceObservationOrderingAndAuthorization",
    "TestExternalLegacyActiveApprovalRejectsNewDecisionButKeepsReceiptReplay",
)
CLM_OUTBOX_STORE_TESTS = (
    "TestCLMMaintenanceRoleNonSuperuserBootstrapAndRerun",
    "TestCLMOutboxMaintenanceRoleClaimLeaseAndMetrics",
)
CLM_OUTBOX_RELAY_TESTS = ("TestCLMOutboxRelayRetryConsumerRefreshDoesNotChangeBusinessFacts",)
CONTEXT_FENCE_TESTS = ("TestSessionContextSwitchSupportExpiryAndWriteFence",)


class FastError(RuntimeError):
    """A reproducible failure before any check can run."""


@dataclass(frozen=True)
class Change:
    status: str
    path: str
    old_path: str | None = None

    @property
    def deleted(self) -> bool:
        return self.status == "D"

    def affected_paths(self) -> tuple[str, ...]:
        paths = [self.path]
        if self.old_path and self.old_path != self.path:
            paths.append(self.old_path)
        return tuple(paths)


@dataclass(frozen=True)
class SensitiveRule:
    reason: str
    matches: Callable[[str], bool]
    tests: tuple[str, ...]
    needs_model: bool = False
    package: str = "./internal/store"


@dataclass(frozen=True)
class DatabaseTarget:
    package: str
    tests: tuple[str, ...]
    reasons: tuple[str, ...]
    needs_model: bool

    def payload(self) -> dict:
        return {
            "package": self.package,
            "tests": list(self.tests),
            "reasons": list(self.reasons),
            "needs_model": self.needs_model,
        }


@dataclass
class Plan:
    changes: tuple[Change, ...]
    go: bool = False
    web: bool = False
    python: bool = False
    database: bool = False
    poppler: bool = False
    legal_assets: bool = False
    docs_build: bool = False
    architecture: bool = False
    ui_terminology: bool = False
    workflow: bool = False
    go_build_all: bool = False
    web_build: bool = False
    go_format_files: list[str] = field(default_factory=list)
    web_eslint_files: list[str] = field(default_factory=list)
    web_prettier_files: list[str] = field(default_factory=list)
    web_prettier_ignore_config: bool = False
    python_ruff_files: list[str] = field(default_factory=list)
    python_ruff_config: bool = False
    go_packages: set[str] = field(default_factory=set)
    go_unit_packages: set[str] = field(default_factory=set)
    python_syntax_files: list[str] = field(default_factory=list)
    python_tests: list[str] = field(default_factory=list)
    database_tests: tuple[DatabaseTarget, ...] = ()
    reasons: set[str] = field(default_factory=set)

    def payload(self) -> dict:
        return {
            "go": self.go,
            "web": self.web,
            "python": self.python,
            "file_metadata": True,
            "database": self.database,
            "poppler": self.poppler,
            "legal_assets": self.legal_assets,
            "docs_build": self.docs_build,
            "architecture": self.architecture,
            "ui_terminology": self.ui_terminology,
            "workflow": self.workflow,
            "go_compile": "all" if self.go_build_all else "affected",
            "go_build_all": self.go_build_all,
            "go_format_files": list(self.go_format_files),
            "web_eslint_files": list(self.web_eslint_files),
            "web_prettier_files": list(self.web_prettier_files),
            "web_prettier_ignore_config": self.web_prettier_ignore_config,
            "python_ruff_files": list(self.python_ruff_files),
            "python_ruff_config": self.python_ruff_config,
            "go_packages": sorted(self.go_packages),
            "go_unit_packages": sorted(self.go_unit_packages),
            "web_build": self.web_build,
            "python_syntax_files": list(self.python_syntax_files),
            "python_tests": list(self.python_tests),
            "database_tests": [target.payload() for target in self.database_tests],
            "changed_paths": sorted(
                {path for change in self.changes for path in change.affected_paths()}
            ),
            "reasons": sorted(self.reasons),
        }


@dataclass(frozen=True)
class FastCheck:
    name: str
    command: tuple[str, ...] = ()
    cwd: Path = REPO_ROOT
    env: dict[str, str] | None = None
    timeout: int = 600
    kind: str = "command"  # command, gofmt, json, unittest, python-syntax, not-run
    files: tuple[str, ...] = ()
    expected_tests: tuple[str, ...] = ()
    required: bool = True
    note: str = ""
    metadata_base: str = ""
    metadata_subject: str = ""


@dataclass(frozen=True)
class CheckResult:
    name: str
    status: str
    seconds: float
    command: str
    log: str
    reason: str = ""
    evidence: dict | None = None

    def payload(self) -> dict:
        result = {
            "name": self.name,
            "status": self.status,
            "seconds": self.seconds,
            "command": self.command,
            "log": self.log,
        }
        if self.reason:
            result["reason"] = self.reason
        if self.evidence is not None:
            result["evidence"] = self.evidence
        return result


def _stem(path: str) -> str:
    return PurePosixPath(path).stem


def _normalize_stem(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "_", value.lower()).strip("_")


def _is_auth(path: str) -> bool:
    if path.startswith(("internal/identity/", "internal/access/", "internal/accounts/")):
        return True
    if path.startswith("internal/store/") and "authorization" in _stem(path):
        return True
    return path.startswith("internal/httpapi/") and any(
        token in _stem(path) for token in ("auth", "login", "oidc", "session")
    )


def _is_session(path: str) -> bool:
    if path.startswith("internal/identity/"):
        return True
    return "session" in _stem(path) and path.startswith(("internal/store/", "internal/httpapi/"))


def _is_tenant(path: str) -> bool:
    if path.startswith("internal/access/"):
        return True
    if not path.startswith("internal/store/"):
        return False
    return any(
        token in _stem(path)
        for token in (
            "scope",
            "scoped",
            "tenant",
            "governance",
            "authorization",
            "legal_applicability",
        )
    )


TENANT_AUTHORITY_CORE_PATHS = frozenset(
    {
        "scripts/provision_test_db.sh",
        "cmd/auditexecution/main.go",
        "cmd/documentservice/main.go",
        "cmd/identity-import/main.go",
        "cmd/migrate/main.go",
        "cmd/modelgateway/main.go",
        "cmd/server/main.go",
        "cmd/server/main_test.go",
        "cmd/toolgateway/main.go",
        "cmd/toolgateway/main_test.go",
        "cmd/worker/main.go",
        "cmd/worker/authority.go",
        "cmd/worker/authority_test.go",
        "internal/accounts/accounts.go",
        "internal/bootstrap/runtime.go",
        "internal/bootstrap/services.go",
        "internal/bootstrap/membership_authority_integration_test.go",
        "internal/bootstrap/services_integration_test.go",
        "internal/documentclient/intake.go",
        "internal/documentservice/intake_upload.go",
        "internal/documentservice/intake_upload_test.go",
        "internal/documentservice/intake_authority_client.go",
        "internal/documentservice/intake_authority_client_test.go",
        "internal/documentservice/service.go",
        "internal/identity/manager.go",
        "internal/model/scope.go",
        "internal/httpapi/case_intake.go",
        "internal/httpapi/claim_authority.go",
        "internal/httpapi/core_session_resolver.go",
        "internal/httpapi/document_intake_authority.go",
        "internal/httpapi/document_intake_authority_test.go",
        "internal/httpapi/operation_authority.go",
        "internal/httpapi/oidc.go",
        "internal/httpapi/oidc_test.go",
        "internal/httpapi/server.go",
        "internal/httpapi/services.go",
        "internal/httpapi/session_authority.go",
        "internal/httpapi/session_authority_test.go",
        "internal/httpapi/session_audience.go",
        "internal/store/admission_session_context.go",
        "internal/store/agent_review.go",
        "internal/store/attempt_finalize.go",
        "internal/store/authorization.go",
        "internal/store/case_intake.go",
        "internal/store/case_intake_authority.go",
        "internal/store/case_intake_pending.go",
        "internal/store/cases.go",
        "internal/store/document_prepare.go",
        "internal/store/execution.go",
        "internal/store/experience_distill.go",
        "internal/store/experience_distill_worker.go",
        "internal/store/identity_import.go",
        "internal/store/identity_import_test.go",
        "internal/store/knowledge_acceptance.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0033_tenant_membership.sql",
        "internal/store/model_budget.go",
        "internal/store/operation_authority.go",
        "internal/store/operation_authority_pg_test.go",
        "internal/store/queue_storage.go",
        "internal/store/queue_types.go",
        "internal/store/rulepack_shadow_input.go",
        "internal/store/runtime_authorization.go",
        "internal/store/scoped_mutation.go",
        "internal/store/service_roles.go",
        "internal/store/session_security.go",
        "internal/store/session_lifecycle_test.go",
        "internal/store/single_contract_execution.go",
        "internal/store/state.go",
        "internal/store/telecom_agent_review.go",
        "internal/store/telecom_runtime.go",
        "internal/store/tenant_identity.go",
        "internal/store/tenant_identity_pg_test.go",
        "internal/store/tenant_identity_role_provision.go",
        "internal/store/tool_authority.go",
        "internal/store/pg.go",
        "internal/modelgateway/service.go",
        "internal/modelgateway/jev.go",
        "internal/store/worker_claim_authority.go",
        "internal/toolgateway/core_claim.go",
        "internal/toolgateway/core_claim_test.go",
        "internal/toolgateway/core_session.go",
        "internal/toolgateway/core_session_test.go",
        "internal/toolgateway/server.go",
    }
)


def _is_tenant_identity(path: str) -> bool:
    return (
        path in TENANT_AUTHORITY_CORE_PATHS
        or path.startswith(("internal/coreauthority/", "internal/operationauthority/"))
        or path.startswith("internal/store/tenant_identity")
    )


def _is_tenant_authority_bootstrap(path: str) -> bool:
    return _is_tenant_identity(path)


def _is_case_intake_authority(path: str) -> bool:
    return path in {
        "internal/httpapi/case_intake.go",
        "internal/store/case_intake.go",
        "internal/store/case_intake_authority.go",
        "internal/store/case_intake_pending.go",
        "internal/store/case_intake_pending_test.go",
        "internal/store/case_intake_session_recovery_test.go",
        "internal/store/case_intake_membership_recovery_test.go",
        "internal/store/upload_idempotency.go",
        "internal/store/cases.go",
        "internal/store/materials.go",
        "internal/store/service_roles.go",
        "internal/store/migrations/0041_intake_session_recovery.sql",
        "internal/documentservice/intake_upload.go",
        "internal/documentclient/intake.go",
        "internal/bootstrap/intake_session_recovery_test.go",
    }


def _is_tenant_identity_http(path: str) -> bool:
    return path in {
        "scripts/provision_test_db.sh",
        "cmd/server/main.go",
        "cmd/server/main_test.go",
        "cmd/identity-import/main.go",
        "internal/accounts/accounts.go",
        "internal/identity/manager.go",
        "internal/httpapi/oidc.go",
        "internal/httpapi/oidc_test.go",
        "internal/httpapi/server.go",
        "internal/httpapi/services.go",
        "internal/httpapi/session_audience.go",
        "internal/store/authorization.go",
        "internal/store/cases.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0033_tenant_membership.sql",
        "internal/store/service_roles.go",
        "internal/store/identity_import.go",
        "internal/store/identity_import_test.go",
        "internal/store/session_lifecycle.go",
        "internal/store/session_security.go",
        "internal/store/state.go",
        "internal/store/tenant_identity.go",
        "internal/store/tenant_identity_pg_test.go",
        "internal/store/tenant_identity_role_provision.go",
        "internal/store/pg.go",
    }


def _is_migration(path: str) -> bool:
    if path.startswith("internal/store/") and "schema" in _stem(path):
        return True
    if not path.startswith(("cmd/migrate/", "deploy/", "deployments/", "internal/", "scripts/")):
        return False
    return "migrat" in path.lower()


def _is_budget(path: str) -> bool:
    return path.startswith(
        ("internal/execution/", "internal/modelgateway/", "internal/store/")
    ) and "budget" in _stem(path)


def _is_lease(path: str) -> bool:
    if path.startswith("internal/execution/"):
        return "budget" not in _stem(path)
    return path.startswith("internal/store/") and any(
        token in _stem(path) for token in ("stale_claim", "execution", "attempt_finalize", "queue")
    )


def _is_database_core(path: str) -> bool:
    return path in (
        "internal/store/pg.go",
        "internal/store/service_roles.go",
        "internal/config/select.go",
    )


def _is_frozen_evidence(path: str) -> bool:
    return path.startswith("internal/store/") and any(
        token in _stem(path)
        for token in (
            "frozen",
            "snapshot",
            "service_object_context",
            "attempt_input",
            "material",
        )
    )


def _is_review_readmission(path: str) -> bool:
    return path in {
        "internal/store/execution.go",
        "internal/store/write_idempotency.go",
        "internal/store/attempt_finalize.go",
        "internal/store/document_prepare.go",
        "internal/store/review_readmission_pg_test.go",
        "internal/store/case_context_migration_test.go",
        "internal/store/migrations/0042_review_receipt_session_recovery.sql",
        "internal/store/migrations/0043_review_receipt_origin_org.sql",
        "internal/store/migrations.go",
        "internal/store/table_catalog.json",
        "internal/store/service_roles.go",
        "internal/store/tenant_identity_role_provision.go",
        "internal/httpapi/server.go",
    }


def _is_review_readmission_http(path: str) -> bool:
    return path in {
        "internal/httpapi/server.go",
        "internal/httpapi/pending_image_test.go",
        "internal/store/execution.go",
        "internal/store/write_idempotency.go",
        "internal/store/migrations/0042_review_receipt_session_recovery.sql",
        "internal/store/migrations/0043_review_receipt_origin_org.sql",
    }


def _is_report_receipt_http(path: str) -> bool:
    return path in {
        "internal/httpapi/business.go",
        "internal/httpapi/server.go",
        "internal/httpapi/report_receipt_replay_test.go",
        "internal/store/business_reports.go",
        "internal/store/migrations/0044_report_receipt_session_recovery.sql",
    }


def _is_report_receipt(path: str) -> bool:
    return _is_report_receipt_http(path) or path in {
        "internal/store/write_idempotency.go",
        "internal/store/report_receipt_replay_pg_test.go",
        "internal/store/migrations.go",
        "internal/store/table_catalog.json",
        "internal/store/service_roles.go",
        "internal/store/tenant_identity_role_provision.go",
    }


def _is_contract_signing(path: str) -> bool:
    return path in (
        "internal/store/contract_signing.go",
        "internal/store/contract_signing_pg_test.go",
        "internal/store/migrations/0027_contract_signing.sql",
    )


def _is_contract_fulfillment(path: str) -> bool:
    return path in (
        "internal/store/contract_fulfillment.go",
        "internal/store/contract_fulfillment_pg_test.go",
        "internal/store/migrations/0028_contract_fulfillment.sql",
    )


def _is_contract_payment(path: str) -> bool:
    return path in (
        "cmd/server/main.go",
        "cmd/server/main_test.go",
        "internal/httpapi/contract_payments.go",
        "internal/httpapi/contract_payments_test.go",
        "internal/httpapi/server.go",
        "internal/httpapi/services.go",
        "internal/httpapi/services_ownership_test.go",
        "internal/store/contract_draft.go",
        "internal/store/contract_fulfillment_pg_test.go",
        "internal/store/contract_payments.go",
        "internal/store/contract_payments_pg_test.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0029_contract_payments.sql",
        "internal/store/service_roles.go",
    )


def _is_contract_asset(path: str) -> bool:
    return path in (
        "cmd/server/main.go",
        "cmd/server/main_test.go",
        "internal/httpapi/contract_assets.go",
        "internal/httpapi/contract_assets_test.go",
        "internal/httpapi/server.go",
        "internal/httpapi/services.go",
        "internal/httpapi/services_ownership_test.go",
        "internal/store/contract_assets.go",
        "internal/store/contract_assets_pg_test.go",
        "internal/store/contract_draft.go",
        "internal/store/contract_fulfillment_pg_test.go",
        "internal/store/immutable_parsing_test.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0030_contract_assets.sql",
        "internal/store/service_roles.go",
        "internal/store/session_lifecycle_test.go",
    )


def _is_contract_source(path: str) -> bool:
    return path in (
        "cmd/server/main.go",
        "cmd/server/main_test.go",
        "internal/clmsource/contract.go",
        "internal/clmsource/contract_test.go",
        "internal/httpapi/contract_sources.go",
        "internal/httpapi/contract_sources_test.go",
        "internal/httpapi/server.go",
        "internal/httpapi/services.go",
        "internal/store/contract_draft.go",
        "internal/store/contract_approval_decision.go",
        "internal/store/contract_approval_decision_pg_test.go",
        "internal/store/contract_sources.go",
        "internal/store/contract_sources_crypto.go",
        "internal/store/contract_sources_crypto_test.go",
        "internal/store/contract_sources_pg_test.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0031_contract_source_observations.sql",
        "internal/store/service_roles.go",
    )


def _is_clm_outbox_relay(path: str) -> bool:
    # This package contains the Core-Outbox relay and its real PostgreSQL
    # bridge test. Keep it a separate go test target: its test cannot be
    # executed by selecting names in ./internal/store.
    return path.startswith("internal/clmoutbox/")


def _is_offline_migrator_bootstrap(path: str) -> bool:
    return path in {
        "cmd/migrate/main.go",
        "scripts/provision_test_db.sh",
        "internal/store/offline_migrator_provision.go",
        "internal/store/offline_migrator_provision_pg_test.go",
        "internal/store/migrations.go",
        "internal/store/service_roles.go",
        "internal/store/clm_role_provision.go",
        "internal/store/tenant_identity_role_provision.go",
        "internal/store/migrations/0033_tenant_membership.sql",
        "internal/store/migrations/0042_review_receipt_session_recovery.sql",
        "internal/store/migrations/0043_review_receipt_origin_org.sql",
    }


# These Store files call the shared human-command transaction seam or own the
# identity/session proof that it consumes. Keep this exact set in sync with the
# reviewed beginContextTx caller inventory; a new human-write caller must route
# the context-fence PostgreSQL case as part of its change.
CONTEXT_FENCE_PATHS = frozenset(
    {
        "cmd/server/main.go",
        "internal/accounts/accounts.go",
        "internal/httpapi/context_session.go",
        "internal/httpapi/context_support.go",
        "internal/httpapi/core_session_resolver.go",
        "internal/httpapi/operation_authority.go",
        "internal/httpapi/server.go",
        "internal/httpapi/services.go",
        "internal/httpapi/session_authority.go",
        "internal/httpapi/session_audience.go",
        "internal/operationauthority/client.go",
        "internal/operationauthority/session.go",
        "internal/toolgateway/core_session.go",
        "internal/toolgateway/server.go",
        "internal/store/agent_review.go",
        "internal/store/attempt_finalize.go",
        "internal/store/case_intake.go",
        "internal/store/case_intake_authority.go",
        "internal/store/case_intake_pending.go",
        "internal/store/cases.go",
        "internal/store/context_fence_pg_test.go",
        "internal/store/context_session.go",
        "internal/store/context_support.go",
        "internal/store/context_transaction.go",
        "internal/store/contract_assets.go",
        "internal/store/contract_audit.go",
        "internal/store/contract_draft.go",
        "internal/store/contract_fulfillment.go",
        "internal/store/contract_ledger.go",
        "internal/store/contract_payments.go",
        "internal/store/contract_references.go",
        "internal/store/contract_signing.go",
        "internal/store/contract_sources.go",
        "internal/store/contract_version.go",
        "internal/store/contract_version_diff.go",
        "internal/store/document_prepare.go",
        "internal/store/execution.go",
        "internal/store/execution_events.go",
        "internal/store/experience_distill.go",
        "internal/store/experience_distill_worker.go",
        "internal/store/experience_feedback.go",
        "internal/store/experience_metrics.go",
        "internal/store/experience_usage.go",
        "internal/store/governance_sample.go",
        "internal/store/history_reads.go",
        "internal/store/identity_import.go",
        "internal/store/knowledge_governance.go",
        "internal/store/legal_applicability.go",
        "internal/store/legal_applicability_history.go",
        "internal/store/legal_knowledge.go",
        "internal/store/legal_review_snapshot.go",
        "internal/store/llm_endpoint.go",
        "internal/store/model_budget.go",
        "internal/store/operation_authority.go",
        "internal/store/operator_audit.go",
        "internal/store/outbox_relay.go",
        "internal/store/parsing.go",
        "internal/store/pg.go",
        "internal/store/queue_scheduler.go",
        "internal/store/queue_storage.go",
        "internal/store/reviews.go",
        "internal/store/rulepack_shadow.go",
        "internal/store/rulepack_shadow_input.go",
        "internal/store/runtime_metrics.go",
        "internal/store/scoped_case_write.go",
        "internal/store/scoped_mutation.go",
        "internal/store/scoped_read.go",
        "internal/store/service_objects_store.go",
        "internal/store/tenant_identity.go",
        "internal/store/tenant_identity_role_provision.go",
        "internal/store/tenant_model_usage.go",
        "internal/store/tool_authority.go",
        "internal/store/tool_budget.go",
        "internal/store/transaction.go",
        "internal/store/upload_idempotency.go",
        "internal/store/workbench.go",
        "internal/store/write_idempotency.go",
        "internal/store/authorization.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0034_session_context_fence.sql",
        "internal/store/service_roles.go",
        "internal/store/session_lifecycle_test.go",
        "internal/store/state.go",
        "scripts/provision_test_db.sh",
    }
)


def _is_context_fence(path: str) -> bool:
    return path in CONTEXT_FENCE_PATHS


def _is_clm_outbox_core(path: str) -> bool:
    if path.startswith("internal/store/") and any(
        token in _stem(path) for token in ("clm_outbox", "clm_metrics")
    ):
        return True
    return path in {
        "cmd/server/main.go",
        "cmd/server/main_test.go",
        "internal/bootstrap/services.go",
        "internal/bootstrap/services_integration_test.go",
        "internal/store/migrations.go",
        "internal/store/migrations/0032_clm_outbox_metrics.sql",
        "internal/store/clm_role_provision.go",
        "internal/store/runtime_metrics.go",
        "internal/store/runtime_metrics_test.go",
        "internal/store/service_roles.go",
        "internal/store/state.go",
        "internal/telemetry/runtimemetrics.go",
        "internal/telemetry/runtimemetrics_test.go",
    }


SENSITIVE_RULES = (
    SensitiveRule("auth", _is_auth, AUTH_TESTS),
    SensitiveRule("session", _is_session, SESSION_TESTS),
    SensitiveRule("tenant", _is_tenant, TENANT_TESTS),
    SensitiveRule("tenant-identity-core", _is_tenant_identity, TENANT_IDENTITY_TESTS),
    SensitiveRule(
        "tenant-identity-http",
        _is_tenant_identity_http,
        TENANT_IDENTITY_HTTP_TESTS,
        package="./internal/httpapi",
    ),
    SensitiveRule(
        "tenant-authority-bootstrap",
        _is_tenant_authority_bootstrap,
        TENANT_AUTHORITY_BOOTSTRAP_TESTS,
        package="./internal/bootstrap",
    ),
    SensitiveRule("case-intake-authority", _is_case_intake_authority, CASE_INTAKE_AUTHORITY_TESTS),
    SensitiveRule(
        "case-intake-http",
        _is_case_intake_authority,
        ("TestDistributedIntakeRecoversLostDocumentResponseAcrossSessions",),
        package="./internal/bootstrap",
    ),
    SensitiveRule("migration", _is_migration, MIGRATION_TESTS),
    SensitiveRule("budget", _is_budget, BUDGET_TESTS, needs_model=True),
    SensitiveRule("lease", _is_lease, LEASE_TESTS),
    SensitiveRule("database-core", _is_database_core, DATABASE_CORE_TESTS),
    SensitiveRule("frozen-evidence", _is_frozen_evidence, FROZEN_EVIDENCE_TESTS),
    SensitiveRule("review-readmission", _is_review_readmission, REVIEW_READMISSION_TESTS),
    SensitiveRule(
        "review-readmission-http",
        _is_review_readmission_http,
        REVIEW_READMISSION_HTTP_TESTS,
        package="./internal/httpapi",
    ),
    SensitiveRule("report-receipt", _is_report_receipt, REPORT_RECEIPT_TESTS),
    SensitiveRule(
        "report-receipt-http",
        _is_report_receipt_http,
        REPORT_RECEIPT_HTTP_TESTS,
        package="./internal/httpapi",
    ),
    SensitiveRule("contract-signing", _is_contract_signing, CONTRACT_SIGNING_TESTS),
    SensitiveRule("contract-fulfillment", _is_contract_fulfillment, CONTRACT_FULFILLMENT_TESTS),
    SensitiveRule("contract-payment", _is_contract_payment, CONTRACT_PAYMENT_TESTS),
    SensitiveRule("contract-asset", _is_contract_asset, CONTRACT_ASSET_TESTS),
    SensitiveRule("contract-source", _is_contract_source, CONTRACT_SOURCE_TESTS),
    SensitiveRule("clm-outbox-core", _is_clm_outbox_core, CLM_OUTBOX_STORE_TESTS),
    SensitiveRule(
        "clm-outbox-relay",
        _is_clm_outbox_relay,
        CLM_OUTBOX_RELAY_TESTS,
        package="./internal/clmoutbox",
    ),
    SensitiveRule(
        "offline-migrator-bootstrap",
        _is_offline_migrator_bootstrap,
        OFFLINE_MIGRATOR_BOOTSTRAP_TESTS,
    ),
    SensitiveRule("tenant-context-fence", _is_context_fence, CONTEXT_FENCE_TESTS),
)


def parse_name_status_z(payload: bytes) -> tuple[Change, ...]:
    """Parse ``git diff --name-status -z --find-renames`` output."""
    tokens = [os.fsdecode(token) for token in payload.split(b"\0")]
    if tokens and tokens[-1] == "":
        tokens.pop()
    changes: list[Change] = []
    index = 0
    while index < len(tokens):
        status = tokens[index]
        index += 1
        if not status:
            raise FastError("git diff produced an empty status record")
        if status[0] in ("R", "C"):
            if index + 1 >= len(tokens):
                raise FastError("git diff produced an incomplete rename record")
            old_path, path = tokens[index], tokens[index + 1]
            index += 2
            changes.append(Change(status[0], path, old_path))
        else:
            if index >= len(tokens):
                raise FastError("git diff produced an incomplete path record")
            changes.append(Change(status[0], tokens[index]))
            index += 1
    return tuple(changes)


def _git(root: Path, *arguments: str, allow_failure: bool = False) -> bytes:
    try:
        result = subprocess.run(["git", "-C", str(root), *arguments], capture_output=True)
    except OSError as error:
        raise FastError(f"cannot run git: {error}") from error
    if result.returncode != 0 and not allow_failure:
        detail = result.stderr.decode("utf-8", "replace").strip()
        raise FastError(f"git {' '.join(arguments)} failed: {detail}")
    return result.stdout if result.returncode == 0 else b""


def collect_changes(root: Path, base: str) -> tuple[tuple[Change, ...], str]:
    """Return changes since the merge-base and the merge-base commit."""
    base_sha = _git(
        root,
        "rev-parse",
        "--verify",
        "--quiet",
        f"{base}^{{commit}}",
        allow_failure=True,
    )
    if not base_sha.strip():
        raise FastError(f"base ref {base!r} is not a known commit; fetch it or pass --base")
    merge_base = (
        _git(root, "merge-base", base_sha.decode().strip(), "HEAD", allow_failure=True)
        .decode()
        .strip()
    )
    if not merge_base:
        raise FastError(f"base ref {base!r} has no merge-base with HEAD")
    diff = _git(root, "diff", "--name-status", "-z", "--find-renames", "-M", merge_base, "--")
    changes = list(parse_name_status_z(diff))
    untracked = _git(root, "ls-files", "-z", "--others", "--exclude-standard", "--")
    for raw in untracked.split(b"\0"):
        if not raw:
            continue
        changes.append(Change("A", os.fsdecode(raw)))
    return tuple(changes), merge_base


def _package_selector(package_dir: str) -> str:
    return "./..." if package_dir in ("", ".") else "./" + package_dir.strip("/")


def _go_package_dir(path: str) -> str:
    return str(PurePosixPath(path).parent)


def _live_go_package(root: Path, package_dir: str) -> bool:
    directory = root / package_dir
    return directory.is_dir() and any(
        not path.name.endswith("_test.go") for path in directory.glob("*.go")
    )


def _module_path(root: Path) -> str:
    try:
        text = (root / "go.mod").read_text(encoding="utf-8")
    except OSError:
        return ""
    match = re.search(r"(?m)^module\s+(\S+)", text)
    return match.group(1) if match else ""


def _go_imports(text: str) -> Iterable[str]:
    for block in re.finditer(r"(?ms)^\s*import\s*\((.*?)^\s*\)", text):
        for match in re.finditer(r'"([^"]+)"', block.group(1)):
            yield match.group(1)
    for match in re.finditer(r'(?m)^\s*import\s+(?:[\w.]+\s+)?"([^"]+)"', text):
        yield match.group(1)


def _go_importer_counts(root: Path, module: str) -> dict[str, set[str]]:
    """Map module-internal import path to non-test packages that import it."""
    counts: dict[str, set[str]] = {}
    if not module:
        return counts
    for path in _walk_files(root, ".go", GO_SKIP_PARTS):
        relative = path.relative_to(root)
        if path.name.endswith("_test.go"):
            continue
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        package_dir = str(relative.parent)
        for imported in _go_imports(text):
            if imported == module or imported.startswith(module + "/"):
                counts.setdefault(imported, set()).add(package_dir)
    return counts


def _web_relevant(path: str) -> bool:
    if not path.startswith("web/"):
        return False
    relative = path[len("web/") :]
    if not relative or relative.startswith(("dist/", "node_modules/")):
        return False
    if relative.startswith(("src/", "e2e/", "public/")):
        return True
    return (
        relative == ".prettierignore"
        or PurePosixPath(relative).name in WEB_BUILD_FILES
        or PurePosixPath(relative).suffix in WEB_SOURCE_SUFFIXES
    )


def _web_tool_file(path: str, suffixes: frozenset[str]) -> bool:
    if not path.startswith("web/") or path in WEB_GENERATED_FILES:
        return False
    relative = path[len("web/") :]
    parts = PurePosixPath(relative).parts
    return (
        PurePosixPath(relative).suffix in suffixes
        and not any(part in WEB_EXCLUDED_PARTS for part in parts)
        and not relative.endswith((".lock", ".lockb"))
    )


def _web_build_config(path: str) -> bool:
    if not path.startswith("web/"):
        return False
    relative = path[len("web/") :]
    name = PurePosixPath(relative).name
    return (
        relative.startswith("public/")
        or name in WEB_BUILD_FILES
        or name.startswith(("tsconfig", "vite.config", "vitest.config"))
    )


def _docs_build_relevant(path: str) -> bool:
    return (
        path.startswith("docs/")
        or path.startswith("docs-site/")
        or path
        in {
            "scripts/check_docs.py",
            "scripts/check_glossary.py",
            "scripts/check_fast.py",
            ".github/workflows/pr-fast.yml",
        }
    )


def _architecture_relevant(path: str) -> bool:
    if path.startswith("internal/") and path.endswith(".go"):
        return True
    if path.startswith(
        (
            "scripts/architecture/",
            "internal/contractmoney/",
            "internal/modelgateway/",
            "internal/agentflow/",
            "internal/advisorycontract/",
            "internal/advisory/",
        )
    ):
        return True
    return path in {
        "scripts/check_fast.py",
        ".github/workflows/pr-fast.yml",
        "go.mod",
        "go.sum",
        "go.work",
        "go.work.sum",
        "go.env",
    }


def _ui_terminology_relevant(path: str) -> bool:
    return (
        path.startswith("web/src/features/review/manual-findings/")
        or path.startswith("scripts/ui-terminology/")
        or path
        in {
            "docs/glossary/concepts.json",
            "scripts/check_ui_terminology.py",
            "scripts/test_ui_terminology.py",
            "scripts/check_fast.py",
        }
    )


def _python_module(path: str) -> str:
    return path[:-3].replace("/", ".")


def _walk_files(root: Path, suffix: str, skip_dirs: frozenset[str]):
    for current, directories, files in os.walk(root):
        directories[:] = sorted(name for name in directories if name not in skip_dirs)
        for name in files:
            if name.endswith(suffix):
                yield Path(current) / name


def _test_file_candidates(root: Path) -> list[tuple[str, ast.AST | None]]:
    candidates: list[tuple[str, ast.AST | None]] = []
    for path in _walk_files(root, ".py", SKIP_PARTS):
        if not path.name.startswith("test_"):
            continue
        relative = path.relative_to(root)
        try:
            tree = ast.parse(path.read_text(encoding="utf-8", errors="replace"))
        except (OSError, SyntaxError, ValueError):
            tree = None
        candidates.append((str(relative), tree))
    return candidates


def _test_references(tree: ast.AST, module: str, stem: str, filename: str) -> bool:
    def imported(name: str) -> bool:
        return (
            name == module
            or name == stem
            or module.endswith("." + name)
            or name.endswith("." + module)
        )

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            if any(imported(alias.name) for alias in node.names):
                return True
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0 and node.module and imported(node.module):
                return True
        elif isinstance(node, ast.Constant) and isinstance(node.value, str):
            value = node.value
            if value == filename or value.endswith("/" + filename):
                return True
            if value == stem or value.endswith("/" + stem):
                return True
    return False


def python_tests_for(root: Path, changed_paths: Iterable[str]) -> list[str]:
    """Select only tests that name or import a changed Python module."""
    candidates = _test_file_candidates(root)
    selected: set[str] = set()
    for changed in changed_paths:
        if not changed.endswith(".py"):
            continue
        filename = PurePosixPath(changed).name
        stem = _stem(changed)
        normalized = _normalize_stem(stem)
        module = _python_module(changed)
        for test_path, tree in candidates:
            test_stem = _normalize_stem(_stem(test_path))
            if test_stem == "test_" + normalized:
                selected.add(test_path)
                continue
            if changed == test_path and PurePosixPath(changed).name.startswith("test_"):
                selected.add(test_path)
                continue
            if tree is not None and _test_references(tree, module, stem, filename):
                selected.add(test_path)
    return sorted(selected)


def _database_targets(changes: Iterable[Change]) -> tuple[DatabaseTarget, ...]:
    selections: dict[str, dict[str, set[str] | bool]] = {}
    for change in changes:
        for path in change.affected_paths():
            for rule in SENSITIVE_RULES:
                if rule.matches(path):
                    selection = selections.setdefault(
                        rule.package,
                        {
                            "reasons": set(),
                            "tests": set(),
                            "needs_model": False,
                        },
                    )
                    selection["reasons"].add(rule.reason)
                    selection["tests"].update(rule.tests)
                    selection["needs_model"] = selection["needs_model"] or rule.needs_model
    return tuple(
        DatabaseTarget(
            package,
            tuple(sorted(selection["tests"])),
            tuple(sorted(selection["reasons"])),
            selection["needs_model"],
        )
        for package, selection in sorted(selections.items())
    )


def build_plan(root: Path, changes: Iterable[Change]) -> Plan:
    changes = tuple(changes)
    plan = Plan(changes)
    go_source_packages: set[str] = set()

    for change in changes:
        for path in change.affected_paths():
            if path.startswith(".github/workflows/") and path.endswith((".yml", ".yaml")):
                plan.workflow = True
            if path.endswith(".go"):
                plan.go = True
                package_dir = _go_package_dir(path)
                if package_dir in ("", "."):
                    plan.go_build_all = True
                else:
                    plan.go_packages.add(_package_selector(package_dir))
                if change.deleted or path == change.old_path:
                    if not _live_go_package(root, package_dir):
                        plan.go_build_all = True
                elif not path.endswith("_test.go") and package_dir not in ("", "."):
                    go_source_packages.add(_package_selector(package_dir))
                if path == change.path and not change.deleted and path.endswith(".go"):
                    plan.go_format_files.append(path)
            elif path in ("go.mod", "go.sum"):
                plan.go = True
                plan.go_build_all = True
            if path.startswith("web/") and _web_relevant(path):
                plan.web = True
                plan.web_build = plan.web_build or _web_build_config(path)
                if path == "web/.prettierignore":
                    plan.web_prettier_ignore_config = True
                if path == change.path and not change.deleted:
                    if _web_tool_file(path, WEB_CODE_SUFFIXES):
                        plan.web_eslint_files.append(path)
                    if _web_tool_file(path, WEB_FORMAT_SUFFIXES):
                        plan.web_prettier_files.append(path)
            if _docs_build_relevant(path):
                plan.docs_build = True
            if _architecture_relevant(path):
                plan.architecture = True
            if _ui_terminology_relevant(path):
                plan.ui_terminology = True
            if path.endswith(".py"):
                plan.python = True
                if path == change.path and not change.deleted:
                    plan.python_syntax_files.append(path)
                    plan.python_ruff_files.append(path)
            if path in RUFF_CONFIG_FILES:
                plan.python = True

    plan.go_format_files = sorted(set(plan.go_format_files))
    plan.web_eslint_files = sorted(set(plan.web_eslint_files))
    plan.web_prettier_files = sorted(set(plan.web_prettier_files))
    plan.python_syntax_files = sorted(set(plan.python_syntax_files))
    plan.python_ruff_files = sorted(set(plan.python_ruff_files))
    plan.python_ruff_config = any(
        path in RUFF_CONFIG_FILES for change in changes for path in change.affected_paths()
    )
    if plan.go_packages:
        module = _module_path(root)
        importer_counts = _go_importer_counts(root, module) if module else {}
        for package in go_source_packages:
            package_dir = package.removeprefix("./")
            import_path = module if package_dir == "." else f"{module}/{package_dir}"
            importers = importer_counts.get(import_path, set()) - {package_dir}
            if len(importers) >= 2 or any(
                importer == "cmd" or importer.startswith("cmd/") for importer in importers
            ):
                plan.go_build_all = True
                plan.reasons.add("shared-package")
    unit_packages = discover_unit_test_packages(root)
    plan.go_unit_packages = plan.go_packages & unit_packages
    if not plan.go_unit_packages and plan.go_build_all:
        # Issue #53: a change whose compile blast radius is the whole module
        # (go.mod, root package, deleted package, shared package) must not lose
        # the only stage that executes tests; widen the unit run to the
        # module's database-safe packages instead of skipping it silently.
        plan.go_unit_packages = set(unit_packages)
        if plan.go_unit_packages:
            plan.reasons.add("unit-scope-widened")
    plan.poppler = bool(plan.go_unit_packages & POPPLER_TEST_PACKAGES)
    plan.legal_assets = bool(plan.go_unit_packages & LEGAL_ASSET_TEST_PACKAGES)
    changed_python = sorted(
        {path for change in changes for path in change.affected_paths() if path.endswith(".py")}
    )
    plan.python_tests = python_tests_for(root, changed_python)
    if "scripts/test_generate_openapi_types.py" in plan.python_tests:
        plan.web = True
        plan.reasons.add("wire-typescript-consumers")
    if "scripts/test_ci_workflows.py" in plan.python_tests:
        plan.workflow = True
    plan.database_tests = _database_targets(changes)
    plan.database = bool(plan.database_tests)
    if plan.database:
        # Database fixed-point checks execute through `go test`, so a migration
        # or schema script change still needs the Go toolchain and modules.
        plan.go = True
        for target in plan.database_tests:
            plan.reasons.update(target.reasons)
    return plan


def _required_database_env(plan: Plan) -> list[str]:
    missing = [
        name
        for name in ("TEST_DATABASE_URL", "MIGRATOR_DATABASE_URL")
        if not os.environ.get(name, "").strip()
    ]
    if any(target.needs_model for target in plan.database_tests):
        if not os.environ.get("MODEL_DATABASE_URL", "").strip():
            missing.append("MODEL_DATABASE_URL")
    return missing


def _resolve_binary(name: str) -> str:
    # Tool-specific environment overrides use the conventional uppercase name.
    value = os.environ.get(name.upper(), name)
    found = shutil.which(value)
    if not found:
        raise FastError(f"{name} is required for this plan but was not found on PATH")
    return found


def _gofmt_binary(go: str) -> str:
    candidate = Path(go).parent / "gofmt"
    if candidate.is_file():
        return str(candidate)
    found = shutil.which("gofmt")
    if not found:
        raise FastError("gofmt is required for Go changes but was not found")
    return found


def _base_env() -> dict[str, str]:
    env = dict(os.environ)
    env["APP_ENV"] = "test"
    env["TYPESAFE_JEV_MODE"] = "off"
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    env.pop("TYPESAFE_API_KEY", None)
    return env


def _check_env(database: bool = False) -> dict[str, str]:
    env = _base_env()
    if not database:
        for name in DB_ENV_KEYS:
            env.pop(name, None)
    return env


def _build_checks(
    plan: Plan, root: Path, python: str, out: Path, base: str | None = None
) -> list[FastCheck]:
    checks: list[FastCheck] = []
    env = _check_env()
    if plan.workflow:
        checks.append(
            FastCheck(
                "workflow-syntax",
                (_resolve_binary("actionlint"), "-shellcheck=", "-pyflakes="),
                cwd=root,
                env=env,
                timeout=120,
            )
        )
    if plan.architecture:
        _ensure_outside_repo(root, out)
        checks.append(
            FastCheck(
                "architecture-dependencies",
                (
                    python,
                    "scripts/architecture/check_architecture.py",
                    "--repo",
                    str(root),
                    *(("--base", base) if base else ()),
                    "--out",
                    str(out / "architecture-check.json"),
                ),
                cwd=root,
                env=env,
                timeout=120,
            )
        )
    if plan.ui_terminology:
        checks.append(
            FastCheck(
                "ui-terminology",
                (python, "scripts/check_ui_terminology.py"),
                cwd=root,
                env=env,
                timeout=120,
            )
        )
    if plan.go:
        go = _resolve_binary("go")
        if plan.go_format_files:
            checks.append(
                FastCheck(
                    "go-format",
                    (_gofmt_binary(go), "-l", *plan.go_format_files),
                    cwd=root,
                    env=env,
                    timeout=120,
                    kind="gofmt",
                )
            )
        packages = sorted(plan.go_packages)
        if plan.go_build_all:
            checks.append(
                FastCheck(
                    "go-compile-all",
                    (
                        go,
                        "test",
                        "-vet=off",
                        "-run",
                        "^$",
                        "-count=1",
                        "-timeout=300s",
                        "./...",
                    ),
                    cwd=root,
                    env=env,
                    timeout=360,
                )
            )
        elif packages:
            checks.append(
                FastCheck(
                    "go-compile",
                    (
                        go,
                        "test",
                        "-vet=off",
                        "-run",
                        "^$",
                        "-count=1",
                        "-timeout=300s",
                        *packages,
                    ),
                    cwd=root,
                    env=env,
                    timeout=360,
                )
            )
        if plan.go_unit_packages:
            checks.append(
                FastCheck(
                    "go-unit",
                    (
                        go,
                        "test",
                        "-vet=off",
                        "-count=1",
                        "-json",
                        "-timeout=600s",
                        *sorted(plan.go_unit_packages),
                    ),
                    cwd=root,
                    env=unit_test_environment(env),
                    timeout=660,
                    kind="json",
                    required=False,
                )
            )
        elif plan.go_packages or plan.go_build_all:
            # Go change with an empty unit selection: keep the stage visible in
            # the report instead of letting the gate pass as if the unit stage
            # never existed (#53).
            checks.append(
                FastCheck(
                    "go-unit",
                    cwd=root,
                    kind="not-run",
                    required=False,
                    note="no database-safe Go unit test packages discovered for the affected scope",
                )
            )
    if plan.python:
        if plan.python_syntax_files:
            checks.append(
                FastCheck(
                    "python-syntax",
                    cwd=root,
                    env=env,
                    kind="python-syntax",
                    files=tuple(plan.python_syntax_files),
                )
            )
        if plan.python_ruff_files:
            ruff = _resolve_binary("ruff")
            checks.append(
                FastCheck(
                    "python-ruff-lint",
                    (ruff, "check", "--output-format=concise", *plan.python_ruff_files),
                    cwd=root,
                    env=env,
                    timeout=180,
                )
            )
            checks.append(
                FastCheck(
                    "python-ruff-format",
                    (ruff, "format", "--check", *plan.python_ruff_files),
                    cwd=root,
                    env=env,
                    timeout=180,
                )
            )
        elif plan.python_ruff_config:
            ruff = _resolve_binary("ruff")
            checks.append(
                FastCheck(
                    "python-ruff-settings",
                    (ruff, "check", "--show-settings", "scripts/check_fast.py"),
                    cwd=root,
                    env=env,
                    timeout=180,
                )
            )
        for test_path in plan.python_tests:
            test_file = PurePosixPath(test_path)
            checks.append(
                FastCheck(
                    f"python-test:{test_path}",
                    (
                        python,
                        "-m",
                        "unittest",
                        "discover",
                        "-s",
                        str(test_file.parent),
                        "-p",
                        test_file.name,
                        "-v",
                    ),
                    cwd=root,
                    env=env,
                    timeout=300,
                    kind="unittest",
                )
            )
    if plan.web:
        bun = _resolve_binary("bun")
        if plan.web_eslint_files:
            relative_files = [path.removeprefix("web/") for path in plan.web_eslint_files]
            checks.append(
                FastCheck(
                    "web-eslint",
                    (bun, "run", "lint:files", "--", *relative_files),
                    cwd=root / "web",
                    env=env,
                    timeout=300,
                )
            )
        if plan.web_prettier_files:
            relative_files = [path.removeprefix("web/") for path in plan.web_prettier_files]
            checks.append(
                FastCheck(
                    "web-prettier",
                    (bun, "run", "format:files", "--", *relative_files),
                    cwd=root / "web",
                    env=env,
                    timeout=300,
                )
            )
        elif plan.web_prettier_ignore_config:
            checks.append(
                FastCheck(
                    "web-prettier-ignore-settings",
                    (bun, "run", "prettier", "--", "--file-info", "src/api/parse.ts"),
                    cwd=root / "web",
                    env=env,
                    timeout=120,
                )
            )
        checks.append(
            FastCheck(
                "web-typecheck",
                (bun, "run", "typecheck"),
                cwd=root / "web",
                env=env,
                timeout=300,
            )
        )
        if plan.web_build:
            checks.append(
                FastCheck(
                    "web-build",
                    (bun, "run", "build"),
                    cwd=root / "web",
                    env=env,
                    timeout=900,
                )
            )
    if plan.docs_build:
        bun = _resolve_binary("bun")
        preview = (
            Path(
                os.environ.get(
                    "CAW_DOCS_PREVIEW_DIR",
                    str(
                        root.parent
                        / "contract-audit-delivery"
                        / "verification"
                        / "docs-site-preview"
                    ),
                )
            )
            .expanduser()
            .resolve()
        )
        _ensure_outside_repo(root, preview)
        checks.append(
            FastCheck(
                "docs-local-check",
                (python, "scripts/check_docs.py"),
                cwd=root,
                env=env,
                timeout=120,
            )
        )
        checks.append(
            FastCheck(
                "docs-dependencies",
                (bun, "install", "--frozen-lockfile"),
                cwd=root / "docs-site",
                env=env,
                timeout=900,
            )
        )
        checks.append(
            FastCheck(
                "docs-build",
                (bun, "run", "build", "--", "--out-dir", str(preview)),
                cwd=root / "docs-site",
                env=env,
                timeout=900,
            )
        )
    for target in plan.database_tests:
        go = _resolve_binary("go")
        pattern = "^(" + "|".join(re.escape(name) for name in target.tests) + ")$"
        check_name = "database-fixed-point"
        if len(plan.database_tests) > 1:
            package_label = target.package.removeprefix("./").replace("/", "-")
            check_name += ":" + package_label
        checks.append(
            FastCheck(
                check_name,
                (
                    go,
                    "test",
                    "-vet=off",
                    "-count=1",
                    "-json",
                    "-timeout=900s",
                    "-run",
                    pattern,
                    target.package,
                ),
                cwd=root,
                env=_check_env(database=True),
                timeout=960,
                kind="json",
                expected_tests=target.tests,
            )
        )
    return checks


def _run_process(
    command: tuple[str, ...], cwd: Path, env: dict[str, str], timeout: int
) -> tuple[int | None, str, str]:
    try:
        process = subprocess.Popen(
            command,
            cwd=cwd,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            errors="replace",
            start_new_session=True,
        )
    except OSError as error:
        return None, "", f"{type(error).__name__}: {error}"
    try:
        output, _ = process.communicate(timeout=timeout)
        return process.returncode, output or "", ""
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        try:
            output, _ = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            output = ""
        return None, output or "", f"timed out after {timeout}s"


def _parse_go_json(text: str) -> tuple[list[dict], str]:
    rows: list[dict] = []
    display: list[str] = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        try:
            row = json.loads(stripped)
        except json.JSONDecodeError:
            display.append(line)
            continue
        if not isinstance(row, dict):
            display.append(line)
            continue
        rows.append(row)
        action = row.get("Action")
        if action == "output":
            display.append(str(row.get("Output", "")).rstrip("\n"))
        elif action == "fail":
            name = row.get("Test")
            if name:
                display.append(f"FAIL: {name}")
            else:
                display.append(f"FAIL\t{row.get('Package', '')}")
        elif action == "skip" and row.get("Test"):
            display.append(f"SKIP: {row['Test']}")
    return rows, "\n".join(display)


def _go_test_evidence(rows: Iterable[dict]) -> dict:
    passed: set[str] = set()
    failed: set[str] = set()
    skipped: set[str] = set()
    for row in rows:
        action, name = row.get("Action"), row.get("Test")
        if not name:
            continue
        if action == "pass":
            passed.add(name)
        elif action == "fail":
            failed.add(name)
        elif action == "skip":
            skipped.add(name)
    return {
        "passed": sorted(passed),
        "failed": sorted(failed),
        "skipped": sorted(skipped),
    }


def _unittest_evidence(output: str) -> tuple[int, str]:
    matches = list(re.finditer(r"(?m)^Ran (\d+) tests? in ", output))
    if not matches:
        return 0, "unittest did not report an executed test count"
    count = int(matches[-1].group(1))
    if count == 0:
        return 0, "unittest executed no tests"
    summaries = re.findall(r"(?m)^OK(?: \(([^\n]*)\))?\s*$", output)
    if not summaries:
        return count, "unittest did not report a successful result"
    skipped = re.search(r"\bskipped=(\d+)", summaries[-1])
    if skipped and int(skipped.group(1)):
        return count - int(skipped.group(1)), "unittest skipped required tests: " + skipped.group(1)
    expected = re.search(r"\bexpected failures=(\d+)", summaries[-1])
    if expected and int(expected.group(1)):
        return count, "unittest contains expected failures: " + expected.group(1)
    return count, ""


def _syntax_evidence(root: Path, files: Iterable[str]) -> tuple[list[str], list[str]]:
    errors: list[str] = []
    checked: list[str] = []
    for relative in files:
        path = root / relative
        try:
            source = tokenize.open(path).read()
            compile(source, str(path), "exec")
            checked.append(relative)
        except (
            OSError,
            LookupError,
            SyntaxError,
            UnicodeDecodeError,
            ValueError,
        ) as error:
            errors.append(f"{relative}: {type(error).__name__}: {error}")
    return checked, errors


def _write_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text if text.endswith("\n") or not text else text + "\n", encoding="utf-8")


def _safe_log_name(name: str) -> str:
    return re.sub(r"[^A-Za-z0-9_.-]+", "__", name)


def _metadata_command(check: FastCheck, logs: Path, root: Path) -> tuple[tuple[str, ...], str]:
    paths = tuple(
        f"scripts/file_metadata/{name}.py"
        for name in (
            "metadata_core",
            "git_inventory",
            "manifest_tool",
            "legacy_bootstrap",
            "gate_adapter",
        )
    )

    def inventory(ref: str) -> dict[str, tuple[str, str, str]]:
        rows = _git(root, "ls-tree", "-z", ref, "--", *paths)
        entries = {}
        for row in rows.split(b"\0"):
            if not row:
                continue
            fields, path = row.split(b"\t", 1)
            mode, kind, oid = fields.decode("ascii").split()
            entries[path.decode("utf-8")] = (mode, kind, oid)
        return entries

    ref, source = check.metadata_base, "trusted-base"
    entries = inventory(ref)
    if not entries:
        try:
            policy = json.loads(_git(root, "show", f"{ref}:scripts/file_metadata/policy.json"))
        except (ValueError, FastError) as error:
            raise FastError("metadata BASE has no verifiable foundation policy") from error
        if policy.get("manifest_status") != "populated-from-issue-115-baseline":
            raise FastError(
                "metadata BASE without trusted tools is not an approved legacy foundation"
            )
        ref, source = check.metadata_subject, "foundation-candidate"
        entries = inventory(ref)
    if set(entries) != set(paths):
        raise FastError("metadata verifier tool set is missing or incomplete")
    directory = Path(tempfile.mkdtemp(prefix="metadata-verifier-", dir=logs.parent))
    for path, (mode, kind, oid) in entries.items():
        if mode not in {"100644", "100755"} or kind != "blob":
            raise FastError(f"metadata verifier is not a regular Git file: {path}")
        content = _git(root, "cat-file", "blob", oid)
        if not content:
            raise FastError(f"metadata verifier is empty: {path}")
        (directory / PurePosixPath(path).name).write_bytes(content)
    command = (
        check.command[0],
        str(directory / "gate_adapter.py"),
        "check",
        "--repo",
        str(root),
        "--base",
        check.metadata_base,
        "--subject",
        check.metadata_subject,
    )
    return command, source


def execute_check(check: FastCheck, logs: Path, root: Path) -> CheckResult:
    started = time.monotonic()
    log_path = logs / (_safe_log_name(check.name) + ".log")
    command_text = " ".join(shlex.quote(part) for part in check.command)
    evidence: dict | None = None
    reason = ""
    status = "PASS"
    output = ""

    if check.kind == "not-run":
        _write_text(log_path, check.note)
        return CheckResult(check.name, "NOT_RUN", 0.0, command_text, str(log_path), check.note)
    if check.kind == "python-syntax":
        checked, errors = _syntax_evidence(root, check.files)
        evidence = {"checked": checked, "errors": errors}
        if errors:
            status, reason = "FAIL", "Python syntax check failed"
            output = "\n".join(errors)
        else:
            output = "Python syntax OK: " + ", ".join(checked)
        _write_text(log_path, output)
    else:
        command, metadata_source = check.command, ""
        if check.kind == "metadata-gate" and check.metadata_base:
            try:
                command, metadata_source = _metadata_command(check, logs, root)
            except (FastError, OSError, ValueError) as error:
                reason = str(error)
                _write_text(log_path, reason)
                return CheckResult(
                    check.name,
                    "FAIL",
                    round(time.monotonic() - started, 3),
                    command_text,
                    str(log_path),
                    reason,
                )
        command_text = " ".join(shlex.quote(part) for part in command)
        returncode, output, error = _run_process(
            command, check.cwd, check.env or _base_env(), check.timeout
        )
        raw_path = logs / (
            _safe_log_name(check.name) + (".jsonl" if check.kind == "json" else ".log")
        )
        if check.kind == "metadata-gate":
            _write_text(raw_path, output)
            try:
                payload = json.loads(output)
                gate_status = payload.get("status")
                evidence = {
                    key: payload[key]
                    for key in ("status", "mode", "reason", "base", "subject")
                    if key in payload
                }
                if metadata_source:
                    evidence["verifier_source"] = metadata_source
                if gate_status == "NOT_RUN" and returncode == 3:
                    status = "NOT_RUN"
                    reason = str(
                        payload.get("reason", "metadata history enforcement is not initialized")
                    )
                elif gate_status == "PASS" and metadata_source == "foundation-candidate":
                    status, reason = (
                        "FAIL",
                        "foundation candidate cannot self-attest initialized metadata enforcement",
                    )
                elif gate_status == "PASS" and returncode == 0:
                    status = "PASS"
                else:
                    status = "FAIL"
                    reason = str(
                        payload.get(
                            "error",
                            payload.get("reason", f"metadata gate exited {returncode}"),
                        )
                    )
            except (json.JSONDecodeError, AttributeError, TypeError):
                status, reason = "FAIL", error or "metadata gate returned invalid JSON"
            _write_text(log_path, output or reason)
        elif check.kind == "json":
            _write_text(raw_path, output)
            rows, display = _parse_go_json(output)
            evidence = _go_test_evidence(rows)
            _write_text(log_path, display or output)
            if error:
                status, reason = "FAIL", error
            elif returncode != 0:
                status, reason = "FAIL", f"command exited {returncode}"
            elif check.expected_tests:
                required = set(check.expected_tests)
                missing = sorted(required - set(evidence["passed"]))
                skipped = sorted(required & set(evidence["skipped"]))
                failed = sorted(required & set(evidence["failed"]))
                if failed:
                    status, reason = (
                        "FAIL",
                        "targeted tests failed: " + ", ".join(failed),
                    )
                elif skipped:
                    status, reason = (
                        "NOT_RUN",
                        "targeted tests skipped: " + ", ".join(skipped),
                    )
                elif missing:
                    status, reason = (
                        "NOT_RUN",
                        "targeted tests did not run: " + ", ".join(missing),
                    )
            elif not evidence["passed"]:
                status, reason = (
                    "NOT_RUN",
                    "no Go tests executed (all skipped or empty package)",
                )
        else:
            _write_text(log_path, output)
            if error:
                status, reason = "FAIL", error
            elif returncode != 0:
                status, reason = "FAIL", f"command exited {returncode}"
            elif check.kind == "gofmt" and output.strip():
                status, reason = "FAIL", "gofmt reported unformatted files"
            elif check.kind == "unittest":
                count, problem = _unittest_evidence(output)
                evidence = {"tests": count}
                if problem:
                    status, reason = (
                        ("NOT_RUN" if problem.startswith("unittest skipped") else "FAIL"),
                        problem,
                    )
    return CheckResult(
        check.name,
        status,
        round(time.monotonic() - started, 3),
        command_text,
        str(log_path),
        reason,
        evidence,
    )


def _default_out(root: Path) -> Path:
    explicit = os.environ.get("CAW_FAST_OUT", "").strip()
    if explicit:
        return Path(explicit).expanduser().resolve()
    if os.environ.get("GITHUB_ACTIONS") == "true" and os.environ.get("RUNNER_TEMP"):
        return Path(os.environ["RUNNER_TEMP"]).expanduser().resolve() / "caw-fast"
    delivery = os.environ.get("CAW_DELIVERY_DIR", "").strip()
    if delivery:
        return Path(delivery).expanduser().resolve() / "verification" / "fast"
    return (root.parent / "contract-audit-delivery" / "verification" / "fast").resolve()


def _ensure_outside_repo(root: Path, out: Path) -> None:
    root = root.resolve()
    out = out.resolve()
    if out == root or root in out.parents:
        raise FastError(
            f"fast-check output {out} is inside the repository; choose a path outside it"
        )


def _write_report(out: Path, report: dict) -> None:
    _write_text(out / "fast-check.json", json.dumps(report, ensure_ascii=False, indent=2))


def _write_github_output(path: str, plan: Plan) -> None:
    destination = Path(path).expanduser()
    lines = [f"{name}={'true' if getattr(plan, name) else 'false'}" for name in GITHUB_KEYS]
    prefix = ""
    try:
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists() and destination.stat().st_size:
            with destination.open("rb") as handle:
                handle.seek(-1, os.SEEK_END)
                if handle.read(1) != b"\n":
                    prefix = "\n"
        with destination.open("a", encoding="utf-8") as handle:
            handle.write(prefix + "\n".join(lines) + "\n")
    except OSError as error:
        raise FastError(f"cannot append GitHub output {destination}: {error}") from error


def _deferred_checks() -> list[dict]:
    return [
        {
            "name": "static-full",
            "status": "NOT_RUN",
            "reason": "whole-repository go vet remains in the full static stage",
        },
        {
            "name": "python-full",
            "status": "NOT_RUN",
            "reason": "only changed-file syntax and directly related Python tests run here",
        },
        {
            "name": "web-unit",
            "status": "NOT_RUN",
            "reason": "web unit tests remain on the full web stage",
        },
        {
            "name": "go-race",
            "status": "NOT_RUN",
            "reason": "full race suite remains on the main and candidate paths",
        },
        {
            "name": "coverage-gates",
            "status": "NOT_RUN",
            "reason": "coverage thresholds remain on the main and candidate paths",
        },
        {
            "name": "browser-acceptance",
            "status": "NOT_RUN",
            "reason": "browser journey remains a release-only acceptance gate",
        },
    ]


def _new_report(base: str, merge_base: str, plan: Plan) -> dict:
    return {
        "schema_version": "fast-check/v1",
        "started_at": datetime.now(timezone.utc).isoformat(),
        "base": base,
        "merge_base": merge_base,
        "plan": plan.payload(),
        "checks": [],
        "deferred_checks": _deferred_checks(),
        "passed": False,
    }


def _print_summary(plan: Plan, report: dict, out: Path) -> None:
    flags = " ".join(f"{name}={'true' if getattr(plan, name) else 'false'}" for name in GITHUB_KEYS)
    status = "PASS" if report["passed"] else "FAIL"
    print(f"FAST: {status} ({flags})")
    for deferred in report["deferred_checks"]:
        print(f"  {deferred['name']}: NOT_RUN")
    print(f"report: {out / 'fast-check.json'}")


def run_checks(
    root: Path,
    plan: Plan,
    base: str,
    merge_base: str,
    out: Path,
    python: str,
    metadata_base: str,
    metadata_subject: str,
) -> int:
    _ensure_outside_repo(root, out)
    logs = out / "logs"
    logs.mkdir(parents=True, exist_ok=True)
    report = _new_report(base, merge_base, plan)
    missing_database = _required_database_env(plan) if plan.database else []
    if missing_database:
        reason = (
            "database plan requires "
            + ", ".join(missing_database)
            + "; start the isolated PostgreSQL role and rerun"
        )
        report["checks"].append(
            CheckResult(
                "database-fixed-point",
                "NOT_RUN",
                0.0,
                "",
                str(logs / "database-fixed-point.log"),
                reason,
                {"missing_env": missing_database},
            ).payload()
        )
        report["checks"].append(
            CheckResult(
                "file-metadata",
                "NOT_RUN",
                0.0,
                "",
                str(logs / "file-metadata.log"),
                "metadata gate not run because required database checks are blocked",
            ).payload()
        )
        _write_text(logs / "database-fixed-point.log", reason)
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        _write_report(out, report)
        print(f"FAST: FAIL ({reason})", file=sys.stderr)
        print(f"report: {out / 'fast-check.json'}", file=sys.stderr)
        return 1

    try:
        checks = _build_checks(plan, root, python, out, metadata_base)
        if os.environ.get("CAW_METADATA_EXTERNAL_GUARD") == "1":
            checks.append(
                FastCheck(
                    "file-metadata",
                    cwd=root,
                    kind="not-run",
                    required=False,
                    note="delegated to the workflow's independent fixed-BASE metadata guard",
                )
            )
        else:
            checks.append(
                FastCheck(
                    "file-metadata",
                    (python,),
                    cwd=root,
                    env=_check_env(),
                    timeout=300,
                    kind="metadata-gate",
                    required=False,
                    metadata_base=metadata_base,
                    metadata_subject=metadata_subject,
                )
            )
    except FastError as error:
        report["error"] = str(error)
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        _write_report(out, report)
        raise
    if not checks:
        report["passed"] = True
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        _write_report(out, report)
        _print_summary(plan, report, out)
        return 0

    for index, check in enumerate(checks):
        command_text = " ".join(shlex.quote(part) for part in check.command)
        print(f"[{check.name}]{' ' + command_text if command_text else ''}", flush=True)
        result = execute_check(check, logs, root)
        report["checks"].append(result.payload())
        if result.status == "PASS":
            print(f"[{check.name}] PASS ({result.seconds}s)")
        elif result.status == "NOT_RUN":
            print(f"[{check.name}] NOT_RUN: {result.reason}")
        else:
            print(f"[{check.name}] FAIL: {result.reason}", file=sys.stderr)
            if check.required:
                tail = ""
                try:
                    tail = Path(result.log).read_text(encoding="utf-8", errors="replace")[-4000:]
                except OSError:
                    pass
                if tail:
                    print(tail, file=sys.stderr)
        _write_report(out, report)
        if result.status == "FAIL" or (result.status == "NOT_RUN" and check.required):
            for remaining in checks[index + 1 :]:
                report["checks"].append(
                    CheckResult(
                        remaining.name,
                        "NOT_RUN",
                        0.0,
                        " ".join(shlex.quote(part) for part in remaining.command),
                        "",
                        f"not run after {check.name} did not pass",
                    ).payload()
                )
            report["passed"] = False
            report["finished_at"] = datetime.now(timezone.utc).isoformat()
            _write_report(out, report)
            _print_summary(plan, report, out)
            return 1
    report["passed"] = True
    report["finished_at"] = datetime.now(timezone.utc).isoformat()
    _write_report(out, report)
    _print_summary(plan, report, out)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--base",
        default=None,
        help="git base ref (default: CAW_FAST_BASE or origin/main)",
    )
    parser.add_argument(
        "--plan",
        action="store_true",
        help="print the one-line selection JSON without running checks",
    )
    parser.add_argument(
        "--github-output",
        default=None,
        help="append scoped check booleans for GitHub Actions",
    )
    parser.add_argument("--out", default=None, help="log/report directory outside the repository")
    parser.add_argument("--root", default=None, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    root = Path(args.root or REPO_ROOT).resolve()
    base = args.base or os.environ.get("CAW_FAST_BASE") or DEFAULT_BASE
    try:
        changes, merge_base = collect_changes(root, base)
        metadata_base = _git(root, "rev-parse", "--verify", f"{base}^{{commit}}").decode().strip()
        metadata_subject = _git(root, "rev-parse", "--verify", "HEAD^{commit}").decode().strip()
        plan = build_plan(root, changes)
        if args.github_output:
            _write_github_output(args.github_output, plan)
        if args.plan:
            print(json.dumps(plan.payload(), ensure_ascii=False, separators=(",", ":")))
            return 0
        out = Path(args.out).expanduser().resolve() if args.out else _default_out(root)
        return run_checks(
            root,
            plan,
            base,
            merge_base,
            out,
            sys.executable,
            metadata_base,
            metadata_subject,
        )
    except FastError as error:
        print(f"check_fast: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())

// File metadata (manifest mirror):
// Purpose: knowledgeGovernanceSchemaSQL is registered by the main migration after the base, case and RulePack schemas. These are Core-owned tables; the migrator must additionally install… Related `store` operations include validKnowledgeText, knowledgeTime, validKnowledgeHash, knowledgeScopeKey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	legalPostgres "contractaudit/internal/legal/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

// knowledgeGovernanceSchemaSQL is registered by the main migration after the
// base, case and RulePack schemas. These are Core-owned tables; the migrator must
// additionally install forced tenant RLS and grant Execution/Worker read-only
// access. No legacy business_knowledge row is promoted by this schema.
const knowledgeGovernanceSchemaSQL = `
CREATE TABLE IF NOT EXISTS governed_knowledge_versions (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL CHECK (version_id ~ '^[0-9a-f]{64}$'),
 display_version text NOT NULL,
 scope_key text NOT NULL CHECK (scope_key ~ '^[0-9a-f]{64}$'),
 kind text NOT NULL CHECK (kind IN ('policy','guidance','exception','experience')),
 scope_org_id text,
 scope_case_id text,
 source_org_id text,
 source_case_id text,
 candidate_id text,
 candidate_version_id text,
 candidate_content_sha256 text,
 content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
 content bytea NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, knowledge_id, version_id),
 UNIQUE (tenant_id, knowledge_id, display_version),
 UNIQUE (tenant_id, knowledge_id, version_id, scope_key),
 CHECK (scope_case_id IS NULL OR scope_org_id IS NOT NULL),
 CHECK ((source_org_id IS NULL) = (source_case_id IS NULL)),
 CHECK ((candidate_id IS NULL AND candidate_version_id IS NULL AND candidate_content_sha256 IS NULL) OR
        (kind='experience' AND candidate_id IS NOT NULL AND candidate_version_id IS NOT NULL AND candidate_content_sha256 IS NOT NULL)),
 FOREIGN KEY (tenant_id, scope_org_id, scope_case_id) REFERENCES cases (tenant_id, org_id, id),
 FOREIGN KEY (tenant_id, source_org_id, source_case_id) REFERENCES cases (tenant_id, org_id, id),
 FOREIGN KEY (tenant_id, candidate_id, candidate_version_id, candidate_content_sha256) REFERENCES experience_candidate_versions (tenant_id, candidate_id, version_id, content_sha256)
);
CREATE INDEX IF NOT EXISTS governed_knowledge_versions_scope ON governed_knowledge_versions (tenant_id, scope_key, knowledge_id, kind);
CREATE TABLE IF NOT EXISTS governed_knowledge_state (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL,
 status text NOT NULL CHECK (status IN ('draft','submitted','validated','approved','published','withdrawn')),
 revision bigint NOT NULL CHECK (revision > 0),
 approved_digest text NOT NULL DEFAULT '',
 approval_actor_id text NOT NULL DEFAULT '',
 approval_reason text NOT NULL DEFAULT '',
 cross_case_reviewed boolean NOT NULL DEFAULT false,
 PRIMARY KEY (tenant_id, knowledge_id, version_id),
 FOREIGN KEY (tenant_id, knowledge_id, version_id) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id)
);
CREATE TABLE IF NOT EXISTS governed_knowledge_pointers (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 scope_key text NOT NULL,
 version_id text NOT NULL,
 active boolean NOT NULL,
 pointer_revision bigint NOT NULL CHECK (pointer_revision > 0),
 updated_by text NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, knowledge_id, scope_key),
 FOREIGN KEY (tenant_id, knowledge_id, version_id, scope_key) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id, scope_key)
);
CREATE TABLE IF NOT EXISTS governed_policy_rule_bindings (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL,
 pack_id text NOT NULL,
 pack_version text NOT NULL,
 binding bytea NOT NULL,
 PRIMARY KEY (tenant_id, knowledge_id, version_id),
 FOREIGN KEY (tenant_id, knowledge_id, version_id) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id),
 FOREIGN KEY (tenant_id, pack_id, pack_version) REFERENCES rule_pack_versions (tenant_id, pack_id, version)
);
CREATE TABLE IF NOT EXISTS governed_knowledge_history (
 seq bigserial PRIMARY KEY,
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL,
 action text NOT NULL CHECK (action IN ('create','submit','validate','approve','publish','withdraw','rollback')),
 revision bigint NOT NULL CHECK (revision > 0),
 pointer_revision bigint NOT NULL CHECK (pointer_revision >= 0),
 actor_id text NOT NULL,
 reason text NOT NULL,
 content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
 previous_version_id text NOT NULL DEFAULT '',
 previous_active boolean NOT NULL DEFAULT false,
 resulting_version_id text NOT NULL DEFAULT '',
 resulting_active boolean NOT NULL DEFAULT false,
 occurred_at timestamptz NOT NULL,
 FOREIGN KEY (tenant_id, knowledge_id, version_id) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id)
);
CREATE INDEX IF NOT EXISTS governed_knowledge_history_order ON governed_knowledge_history (tenant_id, knowledge_id, seq);
CREATE TABLE IF NOT EXISTS governed_knowledge_idempotency (
 tenant_id text NOT NULL,
 actor_id text NOT NULL,
 idem_key text NOT NULL,
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 response bytea NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, actor_id, idem_key)
);
CREATE OR REPLACE FUNCTION guard_governed_knowledge_pointer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'knowledge pointer tombstone must be retained'; END IF;
 IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.knowledge_id IS DISTINCT FROM OLD.knowledge_id OR
    NEW.scope_key IS DISTINCT FROM OLD.scope_key OR NEW.pointer_revision <> OLD.pointer_revision + 1 THEN
   RAISE EXCEPTION 'knowledge pointer revision or identity invalid';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS governed_knowledge_pointer_guard ON governed_knowledge_pointers;
CREATE TRIGGER governed_knowledge_pointer_guard BEFORE UPDATE OR DELETE ON governed_knowledge_pointers FOR EACH ROW EXECUTE FUNCTION guard_governed_knowledge_pointer();
CREATE OR REPLACE FUNCTION guard_governed_knowledge_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'knowledge state cannot be deleted'; END IF;
 IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.knowledge_id IS DISTINCT FROM OLD.knowledge_id OR
    NEW.version_id IS DISTINCT FROM OLD.version_id OR NEW.revision <> OLD.revision + 1 THEN
   RAISE EXCEPTION 'knowledge revision or identity invalid';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS governed_knowledge_state_guard ON governed_knowledge_state;
CREATE TRIGGER governed_knowledge_state_guard BEFORE UPDATE OR DELETE ON governed_knowledge_state FOR EACH ROW EXECUTE FUNCTION guard_governed_knowledge_state();
DROP TRIGGER IF EXISTS governed_knowledge_versions_append_only ON governed_knowledge_versions;
CREATE TRIGGER governed_knowledge_versions_append_only BEFORE UPDATE OR DELETE ON governed_knowledge_versions FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS governed_policy_rule_bindings_append_only ON governed_policy_rule_bindings;
CREATE TRIGGER governed_policy_rule_bindings_append_only BEFORE UPDATE OR DELETE ON governed_policy_rule_bindings FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS governed_knowledge_history_append_only ON governed_knowledge_history;
CREATE TRIGGER governed_knowledge_history_append_only BEFORE UPDATE OR DELETE ON governed_knowledge_history FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS governed_knowledge_idempotency_append_only ON governed_knowledge_idempotency;
CREATE TRIGGER governed_knowledge_idempotency_append_only BEFORE UPDATE OR DELETE ON governed_knowledge_idempotency FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

const knowledgeContentEncoding = knowledgeModel.ContentEncoding
const policyThresholdCompilerRef = knowledgeModel.PolicyThresholdCompilerRef

// These aliases keep the Store's keyed-literal and wire surface while the
// Knowledge module owns the values and their pure rules.
type KnowledgeRef = knowledgeModel.KnowledgeRef
type KnowledgeScope = knowledgeModel.KnowledgeScope
type KnowledgeCondition = knowledgeModel.KnowledgeCondition
type KnowledgeSourceSpan = knowledgeModel.KnowledgeSourceSpan
type PolicyRuleBinding = knowledgeModel.PolicyRuleBinding
type GovernedKnowledgeContent = knowledgeModel.GovernedKnowledgeContent
type CreateGovernedKnowledgeInput = knowledgeModel.CreateGovernedKnowledgeInput
type GovernedKnowledgeCommandInput = knowledgeModel.GovernedKnowledgeCommandInput
type GovernedKnowledgeVersion = knowledgeModel.GovernedKnowledgeVersion
type GovernedKnowledgeHistoryEntry = knowledgeModel.GovernedKnowledgeHistoryEntry

type GovernedRulePackRef = knowledgeModel.GovernedRulePackRef
type GovernedKnowledgeSelection = knowledgeModel.GovernedKnowledgeSelection
type GovernedKnowledgeExclusion = knowledgeModel.GovernedKnowledgeExclusion

// ReviewKnowledgeSnapshot preserves the frozen v1 value and Legal-owned authority type.
type ReviewKnowledgeSnapshot = knowledgeModel.ReviewKnowledgeSnapshot[FrozenLegalAuthority]

func validKnowledgeText(value string, limit int) bool {
	return knowledgeModel.ValidText(value, limit)
}

func knowledgeTime(raw string) (time.Time, error) {
	return knowledgeModel.ParseBusinessTime(raw)
}

func validKnowledgeHash(value string) bool {
	return knowledgeModel.ValidHash(value)
}

func knowledgeScopeKey(scope KnowledgeScope) (string, error) {
	return knowledgeModel.ScopeKey(scope)
}

func knowledgeVersionID(tenant, id, version string) string {
	return knowledgeModel.VersionID(tenant, id, version)
}

func validateKnowledgeContent(content GovernedKnowledgeContent) error {
	return knowledgeModel.ValidateContent(content)
}

func loadKnowledgeCase(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string) (Case, error) {
	var item Case
	var members string
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,business_line,scenario,business_status,archived_at,title,contract_type,owner_id,member_ids,created_at,updated_at FROM cases WHERE tenant_id=$1 AND id=$2`,
		accounts.Tenant(user), caseID).Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.BusinessLine, &item.Scenario, &item.BusinessStatus,
		&item.ArchivedAt, &item.Title, &item.ContractType, &item.OwnerID, &members, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return Case{}, err
	}
	if err := json.Unmarshal([]byte(members), &item.MemberIDs); err != nil {
		return Case{}, err
	}
	if !authorizeCase(item, user, access.Read) {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	return item, nil
}

func authorizeKnowledgeSource(ctx context.Context, tx pgx.Tx, admin accounts.User, content GovernedKnowledgeContent) error {
	return authorizeKnowledgeSourceWithCaseLocks(ctx, tx, admin, content, true)
}

func authorizeKnowledgeSourceWithCaseLocks(ctx context.Context, tx pgx.Tx, admin accounts.User, content GovernedKnowledgeContent, lockCases bool) error {
	if lockCases {
		if content.Scope.CaseID != "" {
			if err := scopedCaseLock(ctx, tx, content.Scope.CaseID); err != nil {
				return err
			}
		}
		if content.SourceCaseID != "" && content.SourceCaseID != content.Scope.CaseID {
			if content.Scope.CaseID != "" {
				var acquired bool
				if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(current_schema() || ':case:' || $1,0))`, content.SourceCaseID).Scan(&acquired); err != nil {
					return err
				}
				if !acquired {
					return fault.New(503, "case_busy", "关联案件正被修改，请重试")
				}
			} else if err := scopedCaseLock(ctx, tx, content.SourceCaseID); err != nil {
				return err
			}
		}
	}
	if content.Scope.CaseID != "" {
		item, err := loadKnowledgeCase(ctx, tx, admin, content.Scope.CaseID)
		if err != nil {
			return err
		}
		if item.OwnerOrgID != content.Scope.OrgID || (content.SourceCaseID == item.ID && content.SourceOrgID != item.OwnerOrgID) {
			return fault.NotFound("没有找到对应记录")
		}
	}
	if content.SourceCaseID != "" && content.SourceCaseID != content.Scope.CaseID {
		item, err := loadKnowledgeCase(ctx, tx, admin, content.SourceCaseID)
		if err != nil {
			return err
		}
		if item.OwnerOrgID != content.SourceOrgID {
			return fault.NotFound("没有找到对应记录")
		}
	}
	return nil
}

// knowledgeCandidateWithCaseLocks validates an exact source candidate for legacy readers.
func knowledgeCandidateWithCaseLocks(ctx context.Context, tx pgx.Tx, admin accounts.User, content GovernedKnowledgeContent, lockCases bool) (ExperienceCandidate, error) {
	if content.Kind != "experience" {
		return ExperienceCandidate{}, nil
	}
	if !validResourceID(content.CandidateID) || !validKnowledgeHash(content.CandidateVersionID) || !validKnowledgeHash(content.CandidateSHA256) || content.SourceCaseID == "" {
		return ExperienceCandidate{}, fault.Invalid("经验候选的编号不完整")
	}
	if err := authorizeKnowledgeSourceWithCaseLocks(ctx, tx, admin, content, lockCases); err != nil {
		return ExperienceCandidate{}, err
	}
	source, err := loadKnowledgeCase(ctx, tx, admin, content.SourceCaseID)
	if err != nil {
		return ExperienceCandidate{}, err
	}
	candidate, err := scanExperienceCandidate(tx.QueryRow(ctx, `SELECT `+candidateSelectColumns+` FROM experience_candidate_versions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND candidate_id=$4 AND version_id=$5`,
		source.TenantID, source.OwnerOrgID, source.ID, content.CandidateID, content.CandidateVersionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceCandidate{}, fault.NotFound("没有找到对应经验候选")
	}
	if err != nil {
		return ExperienceCandidate{}, err
	}
	identity, err := json.Marshal([]any{"experience-candidate-version/v1", source.TenantID, candidate.CandidateID, candidate.JobID, candidate.Generation, candidate.ResultSHA256})
	if err != nil || sha256Bytes(identity) != candidate.VersionID || candidate.ContentSHA256 != content.CandidateSHA256 ||
		(content.SourceFeedbackID != "" && content.SourceFeedbackID != candidate.FeedbackID) {
		return ExperienceCandidate{}, fault.Conflict("经验候选的编号或来源摘要不一致")
	}
	var feedbackExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experience_feedback WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND feedback_id=$4)`,
		source.TenantID, source.OwnerOrgID, source.ID, candidate.FeedbackID).Scan(&feedbackExists); err != nil {
		return ExperienceCandidate{}, err
	}
	if !feedbackExists {
		return ExperienceCandidate{}, fault.Conflict("经验候选的反馈来源不存在")
	}
	if err := authorizeExperienceAncestorsWithCaseLocks(ctx, tx, admin, source, candidate.CandidateID, candidate.BaseVersionID, lockCases); err != nil {
		return ExperienceCandidate{}, err
	}

	return candidate, nil
}

func authorizeKnowledgeReadWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, content GovernedKnowledgeContent, lockCases bool) error {
	if err := authorizeKnowledgeSourceWithCaseLocks(ctx, tx, user, content, lockCases); err != nil {
		return err
	}
	_, err := knowledgeCandidateWithCaseLocks(ctx, tx, user, content, lockCases)
	return err
}

// Redacted experience is independently approved for cross-case use. Its
// reviewed content can be adopted by a target-case reviewer without a grant to
// the source cases. Unredacted content retains every source and ancestor grant.
func authorizeKnowledgeAdoptionWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, version GovernedKnowledgeVersion, lockCases bool) error {
	if version.Content.Kind == "experience" && version.CrossCaseReviewed {
		return nil
	}
	return authorizeKnowledgeReadWithCaseLocks(ctx, tx, user, version.Content, lockCases)
}

func nullableKnowledgeID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// CreateGovernedKnowledge persists a new immutable draft, never mutating or
// automatically approving a legacy version. Tenant and actor are directory-bound.
func (s *Store) CreateGovernedKnowledge(user accounts.User, input CreateGovernedKnowledgeInput) (GovernedKnowledgeVersion, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return GovernedKnowledgeVersion{}, fault.Forbidden("当前服务不能修改制度治理")
	}
	var out GovernedKnowledgeVersion
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		ctx := commandContext(context.Background(), user)
		repository := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
		facts := governedKnowledgeFacts{tx: tx, admin: admin, candidates: make(map[string]ExperienceCandidate)}
		var inner error
		out, inner = knowledgeApp.CreateGovernedKnowledge(ctx, repository, facts, knowledgeApp.CreateDraftIdentity{
			TenantID: accounts.Tenant(admin), ActorID: admin.ID,
		}, input)
		return inner
	})
	return out, err
}

// governedKnowledgeRow retains the complete immutable record and its current
// state so single-version and batched reads use exactly the same checks.
type governedKnowledgeRow struct {
	version                                           GovernedKnowledgeVersion
	raw, binding                                      []byte
	scopeKey, storedKind, display, pointerID          string
	scopeOrg, scopeCase, sourceOrg, sourceCase        *string
	candidateID, candidateVersionID, candidateDigest  *string
	bindingPackID, bindingPackVersion, legalCaptureID string
	active                                            bool
	created                                           time.Time
	decodeErr                                         error
}

const governedKnowledgeSelect = `SELECT v.knowledge_id,v.version_id,v.content,v.content_sha256,v.display_version,v.scope_key,v.kind,v.scope_org_id,v.scope_case_id,v.source_org_id,v.source_case_id,v.candidate_id,v.candidate_version_id,v.candidate_content_sha256,v.created_by,v.created_at,COALESCE(v.legal_capture_snapshot_id,''),
	COALESCE(s.status,''),COALESCE(s.revision,0),COALESCE(s.approved_digest,''),COALESCE(s.approval_actor_id,''),COALESCE(s.approval_reason,''),COALESCE(s.cross_case_reviewed,false),
	COALESCE(p.version_id,''),COALESCE(p.active,false),COALESCE(p.pointer_revision,0),COALESCE(b.binding,''::bytea),COALESCE(b.pack_id,''),COALESCE(b.pack_version,'')
	FROM governed_knowledge_versions v LEFT JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id)
	LEFT JOIN governed_policy_rule_bindings b USING (tenant_id,knowledge_id,version_id)
	LEFT JOIN governed_knowledge_pointers p ON p.tenant_id=v.tenant_id AND p.knowledge_id=v.knowledge_id AND p.scope_key=v.scope_key`

func scanGovernedKnowledge(row pgx.Row) (governedKnowledgeRow, error) {
	var stored governedKnowledgeRow
	out := &stored.version
	err := row.Scan(&out.Ref.KnowledgeID, &out.Ref.VersionID, &stored.raw, &out.Ref.ContentSHA256, &stored.display, &stored.scopeKey, &stored.storedKind,
		&stored.scopeOrg, &stored.scopeCase, &stored.sourceOrg, &stored.sourceCase, &stored.candidateID, &stored.candidateVersionID, &stored.candidateDigest, &out.CreatedBy, &stored.created, &stored.legalCaptureID,
		&out.Status, &out.Revision, &out.ApprovedDigest, &out.ApprovalActorID, &out.ApprovalReason, &out.CrossCaseReviewed,
		&stored.pointerID, &stored.active, &out.PointerRevision, &stored.binding, &stored.bindingPackID, &stored.bindingPackVersion)
	if err != nil {
		return governedKnowledgeRow{}, err
	}
	stored.decodeErr = json.Unmarshal(stored.raw, &out.Content)
	return stored, nil
}

// loadGovernedKnowledge checks all duplicated identities and the separately
// stored binding before treating persisted bytes as authoritative.
func loadGovernedKnowledge(ctx context.Context, tx pgx.Tx, tenant, knowledgeID, versionID string) (GovernedKnowledgeVersion, string, string, bool, error) {
	stored, err := scanGovernedKnowledge(tx.QueryRow(ctx, governedKnowledgeSelect+` WHERE v.tenant_id=$1 AND v.knowledge_id=$2 AND v.version_id=$3`, tenant, knowledgeID, versionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return GovernedKnowledgeVersion{}, "", "", false, fault.NotFound("没有找到对应制度版本")
	}
	if err != nil {
		return GovernedKnowledgeVersion{}, "", "", false, err
	}
	return validateGovernedKnowledgeRow(ctx, tx, tenant, stored, nil)
}

func validateGovernedKnowledgeRow(ctx context.Context, tx pgx.Tx, tenant string, stored governedKnowledgeRow, captures map[string]*legalCaptureReference) (GovernedKnowledgeVersion, string, string, bool, error) {
	out := stored.version
	if out.Status == "" {
		return GovernedKnowledgeVersion{}, "", "", false, fault.NotFound("没有找到对应制度版本")
	}
	raw, binding := stored.raw, stored.binding
	scopeKey, storedKind, display, pointerID := stored.scopeKey, stored.storedKind, stored.display, stored.pointerID
	scopeOrg, scopeCase, sourceOrg, sourceCase := stored.scopeOrg, stored.scopeCase, stored.sourceOrg, stored.sourceCase
	candidateID, candidateVersionID, candidateDigest := stored.candidateID, stored.candidateVersionID, stored.candidateDigest
	bindingPackID, bindingPackVersion, legalCaptureID := stored.bindingPackID, stored.bindingPackVersion, stored.legalCaptureID
	active, knowledgeID, versionID := stored.active, out.Ref.KnowledgeID, out.Ref.VersionID
	out.CreatedAt = stored.created.UTC().Format(time.RFC3339Nano)
	if stored.decodeErr != nil || knowledgeModel.SHA256(raw) != out.Ref.ContentSHA256 {
		return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("制度正文或摘要损坏")
	}
	canonical, err := knowledgeModel.CanonicalContentBytes(out.Content)
	if err != nil || !bytes.Equal(raw, canonical) {
		return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("制度规范编码损坏")
	}
	if out.Content.Kind == "legal" {
		if len(binding) != 0 || bindingPackID != "" || bindingPackVersion != "" {
			return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("法律来源不得存在伪造的规则绑定")
		}
		if captures == nil {
			if err := validateLegalCaptureReference(ctx, tx, tenant, out.Content); err != nil {
				return GovernedKnowledgeVersion{}, "", "", false, err
			}
		} else {
			if err := validateLegalMetadata(out.Content.LegalMetadata, out.Content.Body); err != nil {
				return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("已保存的法律条文元数据无效")
			}
			capture, ok := captures[out.Content.LegalMetadata.SourceSnapshotID]
			if !ok {
				return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("法律条文引用的原始来源快照不存在")
			}
			if err := verifyLegalCaptureReference(out.Content, capture); err != nil {
				return GovernedKnowledgeVersion{}, "", "", false, err
			}
		}
	} else {
		bindingBytes, err := json.Marshal(out.Content.Binding)
		if err != nil || !bytes.Equal(binding, bindingBytes) || bindingPackID != out.Content.Binding.PackID || bindingPackVersion != out.Content.Binding.PackVersion {
			return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("制度规则绑定损坏")
		}
	}
	key, err := knowledgeScopeKey(out.Content.Scope)
	if err != nil || key != scopeKey || out.Content.KnowledgeID != knowledgeID || out.Content.Version != display ||
		out.Content.Kind != storedKind || knowledgeVersionID(tenant, knowledgeID, display) != versionID ||
		out.Content.Scope.OrgID != knowledgeNullableString(scopeOrg) || out.Content.Scope.CaseID != knowledgeNullableString(scopeCase) ||
		out.Content.SourceOrgID != knowledgeNullableString(sourceOrg) || out.Content.SourceCaseID != knowledgeNullableString(sourceCase) ||
		out.Content.CandidateID != knowledgeNullableString(candidateID) || out.Content.CandidateVersionID != knowledgeNullableString(candidateVersionID) || out.Content.CandidateSHA256 != knowledgeNullableString(candidateDigest) {
		return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("制度编号或适用范围记录已损坏")
	}
	if (out.Content.Kind == "legal" && legalCaptureID != out.Content.LegalMetadata.SourceSnapshotID) ||
		(out.Content.Kind != "legal" && legalCaptureID != "") {
		return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("法律来源快照外键与制度内容不一致")
	}
	if err := validateKnowledgeContent(out.Content); err != nil {
		return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("已保存的制度内容无效")
	}
	out.Active = active && pointerID == versionID && out.Status == "published"
	return out, scopeKey, pointerID, active, nil
}

// queryGovernedKnowledgeRows fetches versions and their state in one query,
// then fetches each distinct legal capture once for the whole batch. The caller
// validates and authorizes each row in order before exposing it.
func queryGovernedKnowledgeRows(ctx context.Context, tx pgx.Tx, tenant, suffix string, args ...any) ([]governedKnowledgeRow, map[string]*legalCaptureReference, error) {
	params := make([]any, 1, len(args)+1)
	params[0] = tenant
	params = append(params, args...)
	rows, err := tx.Query(ctx, governedKnowledgeSelect+` WHERE v.tenant_id=$1 `+suffix, params...)
	if err != nil {
		return nil, nil, err
	}
	var versions []governedKnowledgeRow
	var captureIDs []string
	seen := make(map[string]bool)
	for rows.Next() {
		stored, err := scanGovernedKnowledge(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		versions = append(versions, stored)
		content := stored.version.Content
		if stored.decodeErr == nil && content.Kind == "legal" && content.LegalMetadata != nil && !seen[content.LegalMetadata.SourceSnapshotID] {
			id := content.LegalMetadata.SourceSnapshotID
			seen[id] = true
			captureIDs = append(captureIDs, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	captures := make(map[string]*legalCaptureReference, len(captureIDs))
	if len(captureIDs) == 0 {
		return versions, captures, nil
	}
	raw, err := legalPostgres.NewCaptures(platformpostgres.Bind(tx)).ReadReferences(ctx, tenant, captureIDs)
	if err != nil {
		return nil, nil, err
	}
	for id, value := range raw {
		capture := mapLegalCaptureReference(value)
		captures[id] = &capture
	}
	return versions, captures, nil
}

func knowledgeNullableString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// validateKnowledgeBinding checks the exact published, active RulePack and the
// rule/source mapping, under the same transaction and schema lock as a publish.
func validateKnowledgeBinding(ctx context.Context, tx pgx.Tx, tenant string, content GovernedKnowledgeContent) error {
	if content.Kind == "legal" {
		return validateLegalCaptureReference(ctx, tx, tenant, content)
	}
	b := content.Binding
	var raw []byte
	var hash, digest, status, pointerVersion string
	var active bool
	err := tx.QueryRow(ctx, `SELECT v.manifest,v.content_hash,v.snapshot_digest,v.status,COALESCE(p.version,''),COALESCE(p.active,false)
		FROM rule_pack_versions v LEFT JOIN rule_pack_pointers p ON p.tenant_id=v.tenant_id AND p.pack_id=v.pack_id
		WHERE v.tenant_id=$1 AND v.pack_id=$2 AND v.version=$3`, tenant, b.PackID, b.PackVersion).
		Scan(&raw, &hash, &digest, &status, &pointerVersion, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict("制度没有对应的已发布规则包")
	}
	if err != nil {
		return err
	}
	return knowledgeApp.ValidateRulePackBinding(tenant, content, knowledgeApp.RulePackBindingFacts{
		Manifest: raw, ContentHash: hash, SnapshotDigest: digest, Status: status, PointerVersion: pointerVersion, Active: active,
	})
}

type PolicyThresholdCandidate = knowledgeModel.PolicyThresholdCandidate
type PolicyRuleComparison = knowledgeModel.PolicyRuleComparison
type PolicyTextIssue = knowledgeModel.PolicyTextIssue
type GovernedPolicyPreview = knowledgeModel.GovernedPolicyPreview

func extractGovernedPolicyThresholds(body string) (map[string][]PolicyThresholdCandidate, []PolicyTextIssue, []PolicyTextIssue) {
	return knowledgeModel.ExtractGovernedPolicyThresholds(body)
}

func compareGovernedPolicyThresholds(content GovernedKnowledgeContent, manifest rulepack.Manifest) GovernedPolicyPreview {
	return knowledgeModel.CompareGovernedPolicyThresholds(content, manifest)
}

// PreviewGovernedPolicy exposes the server-computed extraction and structured
// differences before approval. It reads only an exact authorized draft/version.
func (s *Store) PreviewGovernedPolicy(user accounts.User, knowledgeID, versionID string) (GovernedPolicyPreview, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return GovernedPolicyPreview{}, err
	}
	var preview GovernedPolicyPreview
	err = s.readKnowledgeAs(admin, func(tx pgx.Tx) error {
		ctx := commandContext(context.Background(), user)
		repository := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
		facts := governedKnowledgeFacts{tx: tx, admin: admin}
		var inner error
		preview, inner = knowledgeApp.PreviewGovernedPolicy(ctx, repository, facts, accounts.Tenant(admin), knowledgeID, versionID)
		return inner
	})
	return preview, err
}

// GovernedKnowledgeCommand performs one idempotent, version-CAS lifecycle step.
// All commands carry the reviewed digest; pointer changes also carry pointer CAS.
func (s *Store) GovernedKnowledgeCommand(user accounts.User, input GovernedKnowledgeCommandInput) (GovernedKnowledgeVersion, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return GovernedKnowledgeVersion{}, fault.Forbidden("当前服务不能修改制度治理")
	}
	var out GovernedKnowledgeVersion
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		ctx := commandContext(context.Background(), user)
		repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
		facts := governedKnowledgeFacts{tx: tx, admin: admin, candidates: make(map[string]ExperienceCandidate)}
		var inner error
		out, inner = knowledgeApp.Command(ctx, repo, facts, knowledgeApp.CreateDraftIdentity{TenantID: accounts.Tenant(admin), ActorID: admin.ID}, input)
		return inner
	})
	return out, err
}

func (s *Store) readKnowledgeAs(admin accounts.User, fn func(pgx.Tx) error) error {
	ctx := commandContext(context.Background(), admin)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := trustedAdmin(admin); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type GovernedKnowledgeSummary = knowledgeModel.GovernedKnowledgeSummary

// ListGovernedKnowledgeVersions returns metadata only; case-sourced versions
// remain invisible to administrators lacking current source-case access.
func (s *Store) ListGovernedKnowledgeVersions(user accounts.User) ([]GovernedKnowledgeSummary, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return nil, err
	}
	out := []GovernedKnowledgeSummary{}
	err = s.readKnowledgeAs(admin, func(tx pgx.Tx) error {
		ctx := commandContext(context.Background(), user)
		repository := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
		facts := governedKnowledgeFacts{tx: tx, admin: admin}
		var inner error
		out, inner = knowledgeApp.ListGovernedKnowledgeVersions(ctx, repository, facts, accounts.Tenant(admin))
		return inner
	})
	return out, err
}

// GetGovernedKnowledgeVersion reads exact content only for a currently
// authorized knowledge administrator, including any source-case authorization.
func (s *Store) GetGovernedKnowledgeVersion(user accounts.User, knowledgeID, versionID string) (GovernedKnowledgeVersion, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	var out GovernedKnowledgeVersion
	err = s.readKnowledgeAs(admin, func(tx pgx.Tx) error {
		repository := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
		facts := governedKnowledgeFacts{tx: tx, admin: admin}
		var inner error
		out, inner = knowledgeApp.GetGovernedKnowledgeVersion(context.Background(), repository, facts, accounts.Tenant(admin), knowledgeID, versionID)
		return inner
	})
	return out, err
}

// ListGovernedKnowledgeHistory returns ordered append-only records. The source
// version is authorized before any history entry for it is returned.
func (s *Store) ListGovernedKnowledgeHistory(user accounts.User, knowledgeID string) ([]GovernedKnowledgeHistoryEntry, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return nil, err
	}
	out := []GovernedKnowledgeHistoryEntry{}
	err = s.readKnowledgeAs(admin, func(tx pgx.Tx) error {
		ctx := commandContext(context.Background(), user)
		repository := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
		facts := governedKnowledgeFacts{tx: tx, admin: admin}
		var inner error
		out, inner = knowledgeApp.ListGovernedKnowledgeHistory(ctx, repository, facts, accounts.Tenant(admin), knowledgeID)
		return inner
	})
	return out, err
}

func knowledgeApplies(content GovernedKnowledgeContent, item Case, at time.Time) (bool, error) {
	return knowledgeModel.KnowledgeApplies(content, knowledgeModel.ReviewCaseFacts{CaseID: item.ID, OrgID: item.OwnerOrgID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType, BusinessStatus: item.BusinessStatus}, at)
}

func knowledgeSelection(v GovernedKnowledgeVersion) GovernedKnowledgeSelection {
	return knowledgeModel.KnowledgeSelection(v)
}

func knowledgeAuthorizationRevision(user accounts.User, item Case) (string, error) {
	identity := struct {
		Principal access.Principal `json:"principal"`
		OwnerID   string           `json:"owner_id"`
		OrgID     string           `json:"org_id"`
		MemberIDs []string         `json:"member_ids"`
	}{bindLocalDemoCaseGrant(item, user, accounts.Principal(user)), item.OwnerID, item.OwnerOrgID, item.MemberIDs}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return sha256Bytes(raw), nil
}

func rulePackRef(pack *ActiveRulePack) GovernedRulePackRef {
	return GovernedRulePackRef{PackID: pack.PackID, Version: pack.Version, ContentHash: pack.ContentHash, SnapshotDigest: pack.SnapshotDigest,
		CompilerRef: pack.CompilerMarker, PointerRevision: pack.PointerRevision}
}

func sealKnowledgeSnapshot(snapshot ReviewKnowledgeSnapshot) (ReviewKnowledgeSnapshot, error) {
	if err := prepareLegalKnowledgeReuse(&snapshot); err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	return knowledgeModel.SealReviewSnapshot(snapshot)
}

// resolveGovernedKnowledgeTx is also the acceptance integration seam. Caller
// must hold the schema lock, then the case lock, and commit the returned snapshot
// with the new attempt in this same transaction. It reads only governed rows.
func resolveGovernedKnowledgeTx(ctx context.Context, tx pgx.Tx, user accounts.User, caseID, businessAt string, optionalLimit int) (ReviewKnowledgeSnapshot, error) {
	return resolveGovernedKnowledgeTxWithEvidenceCutoff(ctx, tx, user, caseID, businessAt, optionalLimit, time.Time{}, "", "")
}

func resolveGovernedKnowledgeTxWithEvidenceCutoff(ctx context.Context, tx pgx.Tx, user accounts.User, caseID, businessAt string, optionalLimit int,
	evidenceCutoff time.Time, cutoffMode, explicitCutoff string) (ReviewKnowledgeSnapshot, error) {
	return resolveGovernedKnowledgeWithCaseLocks(ctx, tx, user, caseID, businessAt, optionalLimit, evidenceCutoff, cutoffMode, explicitCutoff, true)
}

func resolveGovernedKnowledgeWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, caseID, businessAt string, optionalLimit int,
	evidenceCutoff time.Time, cutoffMode, explicitCutoff string, lockCases bool) (ReviewKnowledgeSnapshot, error) {
	current, err := trustedUser(user)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	if optionalLimit < 0 || optionalLimit > 1024 {
		return ReviewKnowledgeSnapshot{}, fault.Invalid("可选制度预算无效")
	}
	at, err := knowledgeTime(businessAt)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	if !authorizeCase(item, current, access.Review) {
		return ReviewKnowledgeSnapshot{}, fault.NotFound("没有找到对应记录")
	}
	pack, err := selectActiveRulePackTx(ctx, tx, item.TenantID, item.BusinessLine, item.Scenario, item.ContractType, at)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	if pack == nil {
		return ReviewKnowledgeSnapshot{}, fault.Conflict("案件没有适用的已发布规则包")
	}
	parsed, err := rulepack.Parse(pack.Snapshot)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, fault.Conflict("规则包内容损坏")
	}
	repository := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
	facts := newReviewKnowledgeFacts(tx, current, item, lockCases)
	return knowledgeApp.ResolveReview(ctx, repository, facts, knowledgeApp.ReviewSelectionInput{
		TenantID: item.TenantID, Case: knowledgeModel.ReviewCaseFacts{CaseID: item.ID, OrgID: item.OwnerOrgID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType, BusinessStatus: item.BusinessStatus},
		BusinessAt: at, Pack: rulePackRef(pack), Manifest: parsed.Manifest(), OptionalLimit: optionalLimit,
	}, newLegalReviewFacts(tx, current, item, lockCases), parsed, evidenceCutoff, cutoffMode, explicitCutoff)
}

// ResolveGovernedKnowledge resolves for a real authorized case and explicit
// business time. Acceptance must use resolveGovernedKnowledgeTx instead.
func (s *Store) ResolveGovernedKnowledge(ctx context.Context, user accounts.User, caseID, businessAt string, optionalLimit int) (ReviewKnowledgeSnapshot, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		return ReviewKnowledgeSnapshot{}, fault.Invalid("解析上下文无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	snapshot, err := resolveGovernedKnowledgeWithCaseLocks(ctx, tx, current, caseID, businessAt, optionalLimit, time.Time{}, "", "", false)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	return snapshot, nil
}

// RevalidateGovernedKnowledgeSnapshotTx re-resolves current dependencies under
// the schema and target-case locks. It is used when deciding whether a retry can
// reuse an accepted run; changed legal/fact dependencies require a new review.
func RevalidateGovernedKnowledgeSnapshotTx(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot) error {
	return revalidateGovernedKnowledgeWithCaseLocks(ctx, tx, user, caseID, frozen, true)
}

func revalidateGovernedKnowledgeWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot, lockCases bool) error {
	currentUser, err := validateFrozenGovernedKnowledgeWithCaseLocks(ctx, tx, user, caseID, frozen, lockCases)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	mode, explicit := "acceptance_time", ""
	if frozen.EvidenceCutoffMode == "explicit" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, frozen.ExplicitEvidenceCutoffAt)
		if parseErr != nil {
			return fault.Conflict("本次审查的显式法律证据截止时间无效")
		}
		cutoff, mode, explicit = parsed.UTC(), "explicit", frozen.ExplicitEvidenceCutoffAt
	}
	current, err := resolveGovernedKnowledgeWithCaseLocks(ctx, tx, currentUser, caseID, frozen.BusinessAt, 1024, cutoff, mode, explicit, lockCases)
	if err != nil {
		return err
	}
	legalPresent := hasLegalReviewSnapshot(&frozen) || hasLegalReviewSnapshot(&current)
	legalValid := true
	if legalPresent {
		legalValid = hasLegalReviewSnapshot(&frozen) && hasLegalReviewSnapshot(&current) && validLegalReuseFingerprint(&frozen) && validLegalReuseFingerprint(&current) && frozen.LegalReuseFingerprint == current.LegalReuseFingerprint
	}
	return knowledgeApp.CompareRetrySelections(frozen, current, legalPresent, legalValid)
}

// validateFrozenGovernedKnowledgeForWorkerTx verifies the immutable accepted
// snapshot, current authorization, exact legal provenance and the pre-existing
// mandatory-policy withdrawal boundary. It deliberately does not resolve new
// legal facts or current authority status for an already accepted attempt.
func validateFrozenGovernedKnowledgeForWorkerTx(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot) (accounts.User, error) {
	return validateFrozenGovernedKnowledgeWithCaseLocks(ctx, tx, user, caseID, frozen, true)
}

func validateFrozenGovernedKnowledgeWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot, lockCases bool) (accounts.User, error) {
	if err := knowledgeApp.ValidateFrozenIdentity(frozen); err != nil {
		return accounts.User{}, err
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, err
	}
	item, err := loadKnowledgeCase(ctx, tx, currentUser, caseID)
	if err != nil {
		return accounts.User{}, err
	}
	if !authorizeCase(item, currentUser, access.Review) {
		return accounts.User{}, fault.NotFound("没有找到对应记录")
	}
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
	facts := newReviewKnowledgeFacts(tx, currentUser, item, lockCases)
	if err := knowledgeApp.ValidateFrozenDependencies(ctx, repo, facts, newLegalReviewFacts(tx, currentUser, item, lockCases), accounts.Tenant(currentUser), frozen); err != nil {
		return accounts.User{}, err
	}
	return currentUser, nil
}

// ValidateFrozenGovernedKnowledgeSnapshotTx is the adoption/read path. New
// dependencies never replace those already frozen into an accepted attempt.
func ValidateFrozenGovernedKnowledgeSnapshotTx(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot) error {
	_, err := validateFrozenGovernedKnowledgeForWorkerTx(ctx, tx, user, caseID, frozen)
	return err
}

// ValidateFrozenGovernedKnowledgeSnapshot validates one consistent database
// snapshot for worker stages that do not already hold the acceptance tx.
func (s *Store) ValidateFrozenGovernedKnowledgeSnapshot(ctx context.Context, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot) error {
	ctx = commandContext(ctx, user)
	if ctx == nil || s == nil || s.pool == nil {
		return fault.New(503, "governance_unavailable", "制度数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return err
	}
	if _, err := validateFrozenGovernedKnowledgeWithCaseLocks(ctx, tx, current, caseID, frozen, false); err != nil {
		return err
	}
	if _, err := trustedUser(current); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RevalidateGovernedKnowledgeSnapshot compares an accepted snapshot with
// currently selected dependencies. Use it to decide whether a new review can
// reuse the prior run, not to adopt work already accepted into an attempt.
func (s *Store) RevalidateGovernedKnowledgeSnapshot(ctx context.Context, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot) error {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		return fault.Invalid("解析上下文无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return err
	}
	if err := revalidateGovernedKnowledgeWithCaseLocks(ctx, tx, current, caseID, frozen, false); err != nil {
		return err
	}
	if _, err := trustedUser(current); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type GovernedKnowledgeReviewContent = knowledgeModel.GovernedKnowledgeReviewContent

// ReadFrozenGovernedKnowledge reads only a reference from the attempt's
// authoritative stored snapshot. Callers must supply the persisted snapshot,
// not a snapshot or digest supplied by a browser, model, or other untrusted
// source. Revalidation checks current target and, when required, source and
// ancestor access along with withdrawal under the same transaction.
func (s *Store) ReadFrozenGovernedKnowledge(ctx context.Context, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot, ref KnowledgeRef) (GovernedKnowledgeReviewContent, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		return GovernedKnowledgeReviewContent{}, fault.Invalid("解析上下文无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	if _, err := validateFrozenGovernedKnowledgeWithCaseLocks(ctx, tx, current, caseID, frozen, false); err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
	facts := governedKnowledgeFacts{tx: tx, admin: current}
	out, err := knowledgeApp.ReadFrozenContent(ctx, repo, facts, accounts.Tenant(current), frozen, ref)
	if err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	return out, nil
}

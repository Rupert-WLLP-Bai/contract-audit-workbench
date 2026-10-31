package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
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

const knowledgeContentEncoding = "governed-knowledge/v1"
const policyThresholdCompilerRef = "governed-policy-threshold/v1"

// KnowledgeRef identifies immutable content; VersionID identifies the tenant,
// family and display-version tuple, while ContentSHA256 identifies its content.
type KnowledgeRef struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
}

// KnowledgeScope contains explicit applicability restrictions. Empty OrgID
// means tenant-wide; CaseID requires OrgID. Business fields never mean wildcard.
type KnowledgeScope struct {
	OrgID        string `json:"org_id,omitempty"`
	CaseID       string `json:"case_id,omitempty"`
	BusinessLine string `json:"business_line"`
	Scenario     string `json:"scenario"`
	ContractType string `json:"contract_type"`
}

// KnowledgeCondition is an equality over a server-owned case attribute.
// Applicability requires all conditions; any exclusion match rejects a version.
type KnowledgeCondition struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

// KnowledgeSourceSpan links a rule to Unicode [start,end) in the policy body.
type KnowledgeSourceSpan struct {
	RuleID string `json:"rule_id"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Quote  string `json:"quote"`
}

// PolicyRuleBinding is immutable and participates in the reviewed content hash.
// ValidationEvidence and CandidateDiff are reviewable evidence, not an assertion
// that arbitrary natural-language requirements were compiled automatically.
type PolicyRuleBinding struct {
	PackID                string                `json:"pack_id"`
	PackVersion           string                `json:"pack_version"`
	PackContentHash       string                `json:"pack_content_hash"`
	PackSnapshotDigest    string                `json:"pack_snapshot_digest"`
	CompilerRef           string                `json:"compiler_ref"`
	ExtractionCompilerRef string                `json:"extraction_compiler_ref,omitempty"`
	RuleIDs               []string              `json:"rule_ids"`
	SourceSpans           []KnowledgeSourceSpan `json:"source_spans"`
	CandidateDiff         string                `json:"candidate_diff"`
	ValidationEvidence    string                `json:"validation_evidence"`
}

// GovernedKnowledgeContent is the complete immutable, versioned hash input.
// SourceCaseID identifies the authorized source case, not a grant to its contents.
type GovernedKnowledgeContent struct {
	Encoding              string               `json:"encoding"`
	KnowledgeID           string               `json:"knowledge_id"`
	Version               string               `json:"version"`
	Kind                  string               `json:"kind"`
	Title                 string               `json:"title"`
	Body                  string               `json:"body"`
	Summary               string               `json:"summary"`
	SourceType            string               `json:"source_type"`
	SourceReference       string               `json:"source_reference"`
	SourceCaseID          string               `json:"source_case_id,omitempty"`
	SourceOrgID           string               `json:"source_org_id,omitempty"`
	SourceFeedbackID      string               `json:"source_feedback_id,omitempty"`
	CandidateID           string               `json:"candidate_id,omitempty"`
	CandidateVersionID    string               `json:"candidate_version_id,omitempty"`
	CandidateSHA256       string               `json:"candidate_content_sha256,omitempty"`
	CandidateReviewDiff   string               `json:"candidate_review_diff,omitempty"`
	ReviewedApplicability []string             `json:"reviewed_applicability,omitempty"`
	ReviewedExclusions    []string             `json:"reviewed_exclusions,omitempty"`
	Scope                 KnowledgeScope       `json:"scope"`
	ValidFrom             string               `json:"valid_from"`
	ValidUntil            string               `json:"valid_until"`
	Applicability         []KnowledgeCondition `json:"applicability"`
	Exclusions            []KnowledgeCondition `json:"exclusions"`
	Binding               PolicyRuleBinding    `json:"binding"`
	ExceptionBasis        string               `json:"exception_basis,omitempty"`
	CheckAction           string               `json:"check_action,omitempty"`
	MinimumEvidence       string               `json:"minimum_evidence,omitempty"`
	Counterexamples       []string             `json:"counterexamples,omitempty"`
	PolicyRefs            []KnowledgeRef       `json:"policy_refs,omitempty"`
	LegalMetadata         *LegalMetadata       `json:"legal_metadata,omitempty"`
}

type CreateGovernedKnowledgeInput struct {
	Content        GovernedKnowledgeContent `json:"content"`
	IdempotencyKey string                   `json:"idempotency_key"`
}

type GovernedKnowledgeCommandInput struct {
	KnowledgeID             string `json:"knowledge_id"`
	VersionID               string `json:"version_id"`
	Action                  string `json:"action"`
	ExpectedRevision        int64  `json:"expected_revision"`
	ExpectedContentSHA256   string `json:"expected_content_sha256"`
	ExpectedPointerRevision int64  `json:"expected_pointer_revision"`
	Reason                  string `json:"reason"`
	CrossCaseReviewed       bool   `json:"cross_case_reviewed"`
	IdempotencyKey          string `json:"idempotency_key"`
}

type GovernedKnowledgeVersion struct {
	Ref               KnowledgeRef             `json:"ref"`
	Content           GovernedKnowledgeContent `json:"content"`
	Status            string                   `json:"status"`
	Revision          int64                    `json:"revision"`
	ApprovedDigest    string                   `json:"approved_digest,omitempty"`
	ApprovalActorID   string                   `json:"approval_actor_id,omitempty"`
	ApprovalReason    string                   `json:"approval_reason,omitempty"`
	CrossCaseReviewed bool                     `json:"cross_case_reviewed"`
	Active            bool                     `json:"active"`
	PointerRevision   int64                    `json:"pointer_revision"`
	CreatedBy         string                   `json:"created_by"`
	CreatedAt         string                   `json:"created_at"`
}

type GovernedKnowledgeHistoryEntry struct {
	Sequence           int64        `json:"sequence"`
	Ref                KnowledgeRef `json:"ref"`
	Action             string       `json:"action"`
	Revision           int64        `json:"revision"`
	PointerRevision    int64        `json:"pointer_revision"`
	ActorID            string       `json:"actor_id"`
	Reason             string       `json:"reason"`
	PreviousVersionID  string       `json:"previous_version_id,omitempty"`
	PreviousActive     bool         `json:"previous_active"`
	ResultingVersionID string       `json:"resulting_version_id,omitempty"`
	ResultingActive    bool         `json:"resulting_active"`
	OccurredAt         string       `json:"occurred_at"`
}

type GovernedRulePackRef struct {
	PackID          string `json:"pack_id"`
	Version         string `json:"version"`
	ContentHash     string `json:"content_sha256"`
	SnapshotDigest  string `json:"snapshot_sha256"`
	CompilerRef     string `json:"compiler_ref"`
	PointerRevision int64  `json:"pointer_revision"`
}

type GovernedKnowledgeSelection struct {
	Ref                    KnowledgeRef         `json:"ref"`
	Kind                   string               `json:"kind"`
	SourceType             string               `json:"source_type"`
	Scope                  KnowledgeScope       `json:"scope"`
	Required               bool                 `json:"required"`
	Summary                string               `json:"summary"`
	ValidFrom              string               `json:"valid_from"`
	ValidUntil             string               `json:"valid_until"`
	ExceptionBasis         string               `json:"exception_basis,omitempty"`
	PolicyRefs             []KnowledgeRef       `json:"policy_refs,omitempty"`
	Applicability          []KnowledgeCondition `json:"applicability"`
	Exclusions             []KnowledgeCondition `json:"exclusions"`
	ReviewedApplicability  []string             `json:"reviewed_applicability,omitempty"`
	ReviewedExclusions     []string             `json:"reviewed_exclusions,omitempty"`
	CheckAction            string               `json:"check_action,omitempty"`
	MinimumEvidence        string               `json:"minimum_evidence,omitempty"`
	Counterexamples        []string             `json:"counterexamples,omitempty"`
	SemanticOnly           []PolicyTextIssue    `json:"semantic_only,omitempty"`
	RequiresFactEvaluation bool                 `json:"requires_fact_evaluation,omitempty"`
	PointerRevision        int64                `json:"pointer_revision"`
	Binding                PolicyRuleBinding    `json:"binding"`
	SelectionReason        string               `json:"selection_reason"`
}

type GovernedKnowledgeExclusion struct {
	Ref    KnowledgeRef `json:"ref"`
	Reason string       `json:"reason"`
}

// ReviewKnowledgeSnapshot is a candidate list to freeze on an attempt. It must
// be created inside the same locked transaction as acceptance, and revalidated
// before reuse or adoption. Policy entries are never subject to OptionalLimit.
type ReviewKnowledgeSnapshot struct {
	Encoding                 string                       `json:"encoding"`
	BusinessAt               string                       `json:"business_at"`
	Scope                    KnowledgeScope               `json:"scope"`
	AuthorizationRevision    string                       `json:"authorization_revision"`
	RulePack                 GovernedRulePackRef          `json:"rule_pack"`
	RequiredPolicies         []GovernedKnowledgeSelection `json:"required_policies"`
	OptionalCandidates       []GovernedKnowledgeSelection `json:"optional_candidates"`
	Excluded                 []GovernedKnowledgeExclusion `json:"excluded"`
	OptionalTruncated        bool                         `json:"optional_truncated"`
	SnapshotSHA256           string                       `json:"snapshot_sha256"`
	EvidenceCutoffAt         string                       `json:"evidence_cutoff_at,omitempty"`
	EvidenceCutoffMode       string                       `json:"evidence_cutoff_mode,omitempty"`
	ExplicitEvidenceCutoffAt string                       `json:"explicit_evidence_cutoff_at,omitempty"`
	LegalAuthorities         []FrozenLegalAuthority       `json:"legal_authorities,omitempty"`
	LegalReuseFingerprint    string                       `json:"legal_reuse_fingerprint,omitempty"`
}

func validKnowledgeText(s string, limit int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit
}

func knowledgeTime(raw string) (time.Time, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return time.Time{}, fault.Invalid("必须提供带时区的业务时间")
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || at.IsZero() {
		return time.Time{}, fault.Invalid("时间必须是带时区的 RFC3339 时间")
	}
	return at.UTC(), nil
}

func validKnowledgeHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func knowledgeScopeKey(scope KnowledgeScope) (string, error) {
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	return sha256Bytes(raw), nil
}

func knowledgeVersionID(tenant, id, version string) string {
	return knowledgeView(tenant, Knowledge{ID: id, Version: version}).VersionID
}

func validateKnowledgeContent(content GovernedKnowledgeContent) error {
	if content.Encoding != knowledgeContentEncoding || !validResourceID(content.KnowledgeID) || !validResourceID(content.Version) ||
		!validKnowledgeText(content.Title, 200) || !validKnowledgeText(content.Body, 30000) || !validKnowledgeText(content.Summary, 2000) ||
		!validKnowledgeText(content.SourceReference, 2000) {
		return fault.Invalid("制度内容或来源无效")
	}
	switch content.Kind {
	case "policy", "guidance", "exception", "experience", "legal":
	default:
		return fault.Invalid("制度类型无效")
	}
	switch content.SourceType {
	case "reference", "synthetic_policy", "human_feedback":
	default:
		return fault.Invalid("制度来源类型无效")
	}
	if content.Kind == "legal" {
		return validateLegalKnowledgeContent(content)
	}
	if content.LegalMetadata != nil {
		return fault.Invalid("只有法律知识条目可以携带法律来源元数据")
	}
	s := content.Scope
	if !validResourceID(s.BusinessLine) || !validResourceID(s.Scenario) || !validResourceID(s.ContractType) ||
		(s.OrgID != "" && !validResourceID(s.OrgID)) || (s.CaseID != "" && (!validResourceID(s.CaseID) || s.OrgID == "")) ||
		(content.SourceCaseID != "" && (!validResourceID(content.SourceCaseID) || !validResourceID(content.SourceOrgID))) ||
		(content.SourceCaseID == "" && content.SourceOrgID != "") {
		return fault.Invalid("制度作用域无效或不完整")
	}
	from, err := knowledgeTime(content.ValidFrom)
	if err != nil {
		return err
	}
	until, err := knowledgeTime(content.ValidUntil)
	if err != nil || !until.After(from) {
		return fault.Invalid("制度有效期必须是有时区的非空半开区间")
	}
	if len(content.Applicability) > 32 || len(content.Exclusions) > 32 || len(content.PolicyRefs) > 32 || len(content.Counterexamples) > 32 {
		return fault.Invalid("适用条件或依赖数量超过上限")
	}
	for _, condition := range append(append([]KnowledgeCondition(nil), content.Applicability...), content.Exclusions...) {
		switch condition.Field {
		case "org_id", "case_id", "business_line", "scenario", "contract_type", "business_status":
		default:
			return fault.Invalid("不支持的适用条件")
		}
		if !validResourceID(condition.Equals) {
			return fault.Invalid("适用条件的值无效")
		}
	}
	for _, ref := range content.PolicyRefs {
		if !validResourceID(ref.KnowledgeID) || !validKnowledgeHash(ref.VersionID) || !validKnowledgeHash(ref.ContentSHA256) {
			return fault.Invalid("引用的制度编号无效")
		}
	}
	if content.Kind == "exception" && (s.OrgID == "" || !validKnowledgeText(content.ExceptionBasis, 2000) || len(content.PolicyRefs) == 0) {
		return fault.Invalid("例外必须限定组织或案件，并提供批准依据、有效期和政策依赖")
	}
	if content.Kind == "experience" {
		if content.SourceCaseID == "" || content.SourceType != "human_feedback" || !validResourceID(content.SourceFeedbackID) ||
			!validResourceID(content.CandidateID) || !validKnowledgeHash(content.CandidateVersionID) || !validKnowledgeHash(content.CandidateSHA256) ||
			!validKnowledgeText(content.CandidateReviewDiff, 10000) || !nonemptyExperienceList(content.ReviewedApplicability) || !nonemptyExperienceList(content.ReviewedExclusions) ||
			!validKnowledgeText(content.CheckAction, 2000) || !validKnowledgeText(content.MinimumEvidence, 2000) || len(content.Counterexamples) == 0 || len(content.PolicyRefs) == 0 {
			return fault.Invalid("经验需要精确候选及审阅差异、授权来源、适用和排除条件、检查动作、最低证据、反例和政策依赖")
		}
		for _, example := range content.Counterexamples {
			if !validKnowledgeText(example, 2000) {
				return fault.Invalid("经验反例无效")
			}
		}
		if len(content.ReviewedApplicability) > 32 || len(content.ReviewedExclusions) > 32 {
			return fault.Invalid("经验适用或排除条件超过上限")
		}
		for _, statement := range append(append([]string(nil), content.ReviewedApplicability...), content.ReviewedExclusions...) {
			if !validKnowledgeText(statement, 2000) {
				return fault.Invalid("经验条件无效")
			}
		}
	}
	if content.Kind != "experience" && (content.SourceFeedbackID != "" || content.CandidateID != "" || content.CandidateVersionID != "" || content.CandidateSHA256 != "") {
		return fault.Invalid("只有经验制度可以关联经验候选编号")
	}
	if content.Kind != "experience" && content.SourceCaseID != "" && content.Scope.CaseID != content.SourceCaseID {
		return fault.Invalid("包含案件来源的制度必须限定该案件")
	}
	b := content.Binding
	if !validIdentifier(b.PackID) || !validIdentifier(b.PackVersion) || !validKnowledgeHash(b.PackContentHash) ||
		!validKnowledgeHash(b.PackSnapshotDigest) || b.CompilerRef != governedCompilerMarker || len(b.RuleIDs) == 0 || len(b.RuleIDs) > maxGovernanceRules ||
		!validKnowledgeText(b.CandidateDiff, 10000) || !validKnowledgeText(b.ValidationEvidence, 10000) {
		return fault.Invalid("规则包绑定或验证依据无效")
	}
	if content.Kind == "policy" && b.ExtractionCompilerRef != policyThresholdCompilerRef {
		return fault.Invalid("制度数值提取器的编号无效")
	}
	seen := make(map[string]bool, len(b.RuleIDs))
	for _, id := range b.RuleIDs {
		if !validIdentifier(id) || seen[id] {
			return fault.Invalid("规则绑定包含重复或无效规则")
		}
		seen[id] = true
	}
	spanRules := make(map[string]bool, len(b.RuleIDs))
	body := []rune(content.Body)
	for _, span := range b.SourceSpans {
		if !seen[span.RuleID] || span.Start < 0 || span.End <= span.Start || span.End > len(body) || string(body[span.Start:span.End]) != span.Quote {
			return fault.Invalid("规则来源位置与制度正文不一致")
		}
		spanRules[span.RuleID] = true
	}
	for id := range seen {
		if !spanRules[id] {
			return fault.Invalid("每条规则都必须有正文来源位置")
		}
	}
	return nil
}

func validateLegalKnowledgeContent(content GovernedKnowledgeContent) error {
	metadata := content.LegalMetadata
	if content.SourceType != "reference" || content.SourceReference != metadataURL(metadata) ||
		!reflect.DeepEqual(content.Scope, KnowledgeScope{}) || content.ValidFrom != "" || content.ValidUntil != "" ||
		content.SourceCaseID != "" || content.SourceOrgID != "" || content.SourceFeedbackID != "" || content.CandidateID != "" ||
		content.CandidateVersionID != "" || content.CandidateSHA256 != "" || content.CandidateReviewDiff != "" ||
		len(content.ReviewedApplicability) != 0 || len(content.ReviewedExclusions) != 0 || len(content.Applicability) != 0 ||
		len(content.Exclusions) != 0 || len(content.PolicyRefs) != 0 || content.ExceptionBasis != "" || content.CheckAction != "" ||
		content.MinimumEvidence != "" || len(content.Counterexamples) != 0 || !reflect.DeepEqual(content.Binding, PolicyRuleBinding{}) {
		return fault.Invalid("法律来源不得伪装成案件制度、规则绑定或政策依赖")
	}
	return validateLegalMetadata(metadata, content.Body)
}

func metadataURL(metadata *LegalMetadata) string {
	if metadata == nil {
		return ""
	}
	return metadata.OfficialURL
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

// knowledgeCandidate loads a real, immutable extraction result and its source
// feedback under the source-case lock. No client assertion creates provenance.
func knowledgeCandidate(ctx context.Context, tx pgx.Tx, admin accounts.User, content GovernedKnowledgeContent, requireReady bool) (ExperienceCandidate, error) {
	return knowledgeCandidateWithCaseLocks(ctx, tx, admin, content, requireReady, true)
}

func knowledgeCandidateWithCaseLocks(ctx context.Context, tx pgx.Tx, admin accounts.User, content GovernedKnowledgeContent, requireReady, lockCases bool) (ExperienceCandidate, error) {
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
	if !requireReady {
		return candidate, nil
	}
	if err := validateExperienceCandidateSource(ctx, tx, source, candidate); err != nil {
		return ExperienceCandidate{}, err
	}
	if err := candidate.ApprovalReady(time.Now().UTC()); err != nil {
		return ExperienceCandidate{}, err
	}
	from, errFrom := knowledgeTime(content.ValidFrom)
	until, errUntil := knowledgeTime(content.ValidUntil)
	candidateFrom, errCandidateFrom := knowledgeTime(candidate.Content.ValidFrom)
	candidateUntil, errCandidateUntil := knowledgeTime(candidate.Content.ValidUntil)
	if errFrom != nil || errUntil != nil || errCandidateFrom != nil || errCandidateUntil != nil || from.Before(candidateFrom) || until.After(candidateUntil) {
		return ExperienceCandidate{}, fault.Conflict("经验有效期不能超出候选的有效期")
	}
	refs := make(map[KnowledgeRef]int, len(content.PolicyRefs))
	for _, ref := range content.PolicyRefs {
		refs[ref]++
	}
	for _, ref := range candidate.Content.PolicyRefs {
		refs[KnowledgeRef{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256}]--
	}
	for _, count := range refs {
		if count != 0 {
			return ExperienceCandidate{}, fault.Conflict("经验政策依赖与候选不一致")
		}
	}
	return candidate, nil
}

func authorizeKnowledgeReadWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, content GovernedKnowledgeContent, lockCases bool) error {
	if err := authorizeKnowledgeSourceWithCaseLocks(ctx, tx, user, content, lockCases); err != nil {
		return err
	}
	_, err := knowledgeCandidateWithCaseLocks(ctx, tx, user, content, false, lockCases)
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

func knowledgeIdempotency(ctx context.Context, tx pgx.Tx, user accounts.User, key string, request any, apply func() (GovernedKnowledgeVersion, error)) (GovernedKnowledgeVersion, error) {
	if !validKnowledgeText(key, 128) || key != strings.TrimSpace(key) {
		return GovernedKnowledgeVersion{}, fault.Invalid("需要有效的请求标识（幂等键）")
	}
	digest, err := governanceIdempotencyKey(request)
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	var storedDigest string
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM governed_knowledge_idempotency WHERE tenant_id=$1 AND actor_id=$2 AND idem_key=$3`, accounts.Tenant(user), user.ID, key).Scan(&storedDigest, &stored)
	if err == nil {
		if storedDigest != digest {
			return GovernedKnowledgeVersion{}, fault.Conflict("这个请求标识已用于其他操作")
		}
		var out GovernedKnowledgeVersion
		if err := json.Unmarshal(stored, &out); err != nil {
			return GovernedKnowledgeVersion{}, err
		}
		// Authorization is current even on replay, including the source case.
		if _, err := trustedAdmin(user); err != nil {
			return GovernedKnowledgeVersion{}, err
		}
		if err := authorizeKnowledgeSource(ctx, tx, user, out.Content); err != nil {
			return GovernedKnowledgeVersion{}, err
		}
		if _, err := knowledgeCandidate(ctx, tx, user, out.Content, false); err != nil {
			return GovernedKnowledgeVersion{}, err
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GovernedKnowledgeVersion{}, err
	}
	out, err := apply()
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	if _, err := trustedAdmin(user); err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	if err := authorizeKnowledgeSource(ctx, tx, user, out.Content); err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO governed_knowledge_idempotency (tenant_id,actor_id,idem_key,request_digest,response,created_at) VALUES ($1,$2,$3,$4,$5,clock_timestamp())`, accounts.Tenant(user), user.ID, key, digest, raw)
	return out, err
}

func insertKnowledgeHistory(ctx context.Context, tx pgx.Tx, tenant string, entry GovernedKnowledgeHistoryEntry) error {
	_, err := tx.Exec(ctx, `INSERT INTO governed_knowledge_history
		(tenant_id,knowledge_id,version_id,action,revision,pointer_revision,actor_id,reason,content_sha256,previous_version_id,previous_active,resulting_version_id,resulting_active,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,clock_timestamp())`, tenant, entry.Ref.KnowledgeID, entry.Ref.VersionID,
		entry.Action, entry.Revision, entry.PointerRevision, entry.ActorID, entry.Reason, entry.Ref.ContentSHA256, entry.PreviousVersionID,
		entry.PreviousActive, entry.ResultingVersionID, entry.ResultingActive)
	return err
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
		ctx := context.Background()
		request := struct {
			Action  string                   `json:"action"`
			Content GovernedKnowledgeContent `json:"content"`
		}{"create", input.Content}
		var inner error
		out, inner = knowledgeIdempotency(ctx, tx, admin, input.IdempotencyKey, request, func() (GovernedKnowledgeVersion, error) {
			content := input.Content
			content.Encoding = knowledgeContentEncoding
			if content.Kind == "policy" {
				content.Binding.ExtractionCompilerRef = policyThresholdCompilerRef
			}
			content.SourceOrgID = "" // source organization is server-derived
			if content.SourceCaseID != "" {
				if err := scopedCaseLock(ctx, tx, content.SourceCaseID); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				item, err := loadKnowledgeCase(ctx, tx, admin, content.SourceCaseID)
				if err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				content.SourceOrgID = item.OwnerOrgID
			}
			if content.Kind == "experience" {
				content.SourceFeedbackID = ""
				candidate, err := knowledgeCandidate(ctx, tx, admin, content, false)
				if err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				content.SourceFeedbackID = candidate.FeedbackID
			}
			if err := validateKnowledgeContent(content); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if content.Kind == "legal" {
				if err := validateLegalCaptureReference(ctx, tx, accounts.Tenant(admin), content); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
			}
			if err := authorizeKnowledgeSource(ctx, tx, admin, content); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if err := validateKnowledgeVersionFamily(ctx, tx, accounts.Tenant(admin), content); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			raw, err := json.Marshal(content)
			if err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			binding, err := json.Marshal(content.Binding)
			if err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			scopeKey, err := knowledgeScopeKey(content.Scope)
			if err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			id := knowledgeVersionID(accounts.Tenant(admin), content.KnowledgeID, content.Version)
			ref := KnowledgeRef{KnowledgeID: content.KnowledgeID, VersionID: id, ContentSHA256: sha256Bytes(raw)}
			var legalCaptureID any
			if content.Kind == "legal" {
				legalCaptureID = content.LegalMetadata.SourceSnapshotID
			}
			var created time.Time
			err = tx.QueryRow(ctx, `INSERT INTO governed_knowledge_versions
				(tenant_id,knowledge_id,version_id,display_version,scope_key,kind,scope_org_id,scope_case_id,source_org_id,source_case_id,candidate_id,candidate_version_id,candidate_content_sha256,content_sha256,content,created_by,created_at,legal_capture_snapshot_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,clock_timestamp(),$17) RETURNING created_at`,
				accounts.Tenant(admin), content.KnowledgeID, id, content.Version, scopeKey, content.Kind,
				nullableKnowledgeID(content.Scope.OrgID), nullableKnowledgeID(content.Scope.CaseID), nullableKnowledgeID(content.SourceOrgID),
				nullableKnowledgeID(content.SourceCaseID), nullableKnowledgeID(content.CandidateID), nullableKnowledgeID(content.CandidateVersionID),
				nullableKnowledgeID(content.CandidateSHA256), ref.ContentSHA256, raw, admin.ID, legalCaptureID).Scan(&created)
			if err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO governed_knowledge_state (tenant_id,knowledge_id,version_id,status,revision) VALUES ($1,$2,$3,'draft',1)`, accounts.Tenant(admin), content.KnowledgeID, id); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if content.Kind != "legal" {
				if _, err = tx.Exec(ctx, `INSERT INTO governed_policy_rule_bindings (tenant_id,knowledge_id,version_id,pack_id,pack_version,binding) VALUES ($1,$2,$3,$4,$5,$6)`, accounts.Tenant(admin), content.KnowledgeID, id, content.Binding.PackID, content.Binding.PackVersion, binding); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
			}
			version := GovernedKnowledgeVersion{Ref: ref, Content: content, Status: "draft", Revision: 1, CreatedBy: admin.ID, CreatedAt: created.UTC().Format(time.RFC3339Nano)}
			var pointerVersion string
			var pointerActive bool
			if err := tx.QueryRow(ctx, `SELECT version_id,active,pointer_revision FROM governed_knowledge_pointers WHERE tenant_id=$1 AND knowledge_id=$2 AND scope_key=$3`, accounts.Tenant(admin), content.KnowledgeID, scopeKey).Scan(&pointerVersion, &pointerActive, &version.PointerRevision); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return GovernedKnowledgeVersion{}, err
			}
			if err := insertKnowledgeHistory(ctx, tx, accounts.Tenant(admin), GovernedKnowledgeHistoryEntry{Ref: ref, Action: "create", Revision: 1, PointerRevision: version.PointerRevision, ActorID: admin.ID, PreviousVersionID: pointerVersion, PreviousActive: pointerActive, ResultingVersionID: pointerVersion, ResultingActive: pointerActive}); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			return version, nil
		})
		return inner
	})
	return out, err
}

// governedKnowledgeRow retains the complete immutable record and its current
// state so single-version and batched reads use exactly the same checks.
type governedKnowledgeRow struct {
	version GovernedKnowledgeVersion
	raw, binding []byte
	scopeKey, storedKind, display, pointerID string
	scopeOrg, scopeCase, sourceOrg, sourceCase *string
	candidateID, candidateVersionID, candidateDigest *string
	bindingPackID, bindingPackVersion, legalCaptureID string
	active bool
	created time.Time
	decodeErr error
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
	if stored.decodeErr != nil || sha256Bytes(raw) != out.Ref.ContentSHA256 {
		return GovernedKnowledgeVersion{}, "", "", false, fault.Conflict("制度正文或摘要损坏")
	}
	canonical, err := json.Marshal(out.Content)
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
	rows, err = tx.Query(ctx, `SELECT `+legalCaptureReferenceColumns+` FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=ANY($2::text[])`, tenant, captureIDs)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var capture legalCaptureReference
		if err := capture.scan(rows); err != nil {
			rows.Close()
			return nil, nil, err
		}
		captures[capture.id] = &capture
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
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
	pack, err := rulepack.Parse(raw)
	if err != nil || status != GovernancePublished || !active || pointerVersion != b.PackVersion ||
		hash != b.PackContentHash || digest != b.PackSnapshotDigest || sha256Bytes(raw) != digest || pack.Hash() != hash {
		return fault.Conflict("制度关联的规则包未启用，或内容编号不一致")
	}
	manifest := pack.Manifest()
	if manifest.ID != b.PackID || manifest.Version != b.PackVersion || manifest.TenantID != tenant || manifest.Lifecycle.State != rulepack.StatePublished ||
		manifest.Lifecycle.Verification == nil || !manifest.Lifecycle.Verification.Passed {
		return fault.Conflict("制度规则包发布或验证记录无效")
	}
	from, e1 := knowledgeTime(content.ValidFrom)
	until, e2 := knowledgeTime(content.ValidUntil)
	packFrom, e3 := knowledgeTime(manifest.EffectiveFrom)
	packUntil, e4 := knowledgeTime(manifest.EffectiveUntil)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || from.Before(packFrom) || until.After(packUntil) {
		return fault.Conflict("制度有效期超出规则包的有效期")
	}
	rules := make(map[string]rulepack.Rule, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		rules[rule.RuleID] = rule
	}
	for _, id := range b.RuleIDs {
		rule, ok := rules[id]
		if !ok || rule.TenantID != tenant || rule.BusinessScope.BusinessLine != content.Scope.BusinessLine ||
			rule.BusinessScope.Scenario != content.Scope.Scenario || rule.BusinessScope.ContractType != content.Scope.ContractType {
			return fault.Conflict("制度引用的规则不存在或不适用于制度范围")
		}
		if content.Kind == "policy" {
			found := false
			for _, source := range rule.AuthoritySources {
				if source.ID == content.KnowledgeID && source.Version == content.Version &&
					(source.Kind == "internal_policy" && content.SourceType == "reference" || source.Kind == "synthetic_demo" && content.SourceType == "synthetic_policy") {
					found = true
				}
			}
			if !found {
				return fault.Conflict("规则未引用该精确政策版本")
			}
		}
	}
	if content.Kind == "policy" {
		preview := compareGovernedPolicyThresholds(content, manifest)
		if !preview.Approvable {
			return fault.Conflict("政策阈值无法唯一提取、验证或与规则包一致；请查看制度规则差异")
		}
	}
	return nil
}

// PolicyThresholdCandidate is extracted from policy text, before comparison
// with a RulePack. Offsets are Unicode code points in the immutable body.
type PolicyThresholdCandidate struct {
	Comparator string `json:"comparator"`
	Value      string `json:"value"`
	Unit       string `json:"unit"`
	Basis      string `json:"basis,omitempty"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Quote      string `json:"quote"`
}

type PolicyRuleComparison struct {
	RuleID        string                    `json:"rule_id"`
	Comparator    string                    `json:"comparator"`
	Candidate     *PolicyThresholdCandidate `json:"candidate,omitempty"`
	RuleThreshold rulepack.Threshold        `json:"rule_threshold"`
	Status        string                    `json:"status"`
	Reason        string                    `json:"reason,omitempty"`
}

type PolicyTextIssue struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
}

// GovernedPolicyPreview reports extracted candidates and the exact comparison
// against the selected RulePack. SemanticOnly statements are not compiled into
// deterministic rules and require a separate semantic review.
type GovernedPolicyPreview struct {
	Ref          KnowledgeRef           `json:"ref"`
	ExtractorRef string                 `json:"extractor_ref"`
	Comparisons  []PolicyRuleComparison `json:"comparisons"`
	Issues       []PolicyTextIssue      `json:"issues"`
	SemanticOnly []PolicyTextIssue      `json:"semantic_only"`
	Approvable   bool                   `json:"approvable"`
}

var policyThresholdPatterns = []struct {
	comparator string
	pattern    *regexp.Regexp
}{
	{rulepack.ComparatorMinimumBandwidth, regexp.MustCompile(`(?:最低保障带宽|保障带宽|最低带宽|带宽|保障速率)\s*(?:不得低于|不低于|不少于|至少|下限为)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)\s*(Mbit/s|Gbit/s)`)},
	{rulepack.ComparatorMonthlySLA, regexp.MustCompile(`(?:(?:月度|月)\s*|SLA\s*)?可用率\s*(?:不得低于|不低于|不少于|至少|下限为)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)\s*([%％])`)},
	{rulepack.ComparatorAdvancePaymentCeiling, regexp.MustCompile(`(?:预付款比例|预付款上限|预付款)\s*(?:不得超过|不超过|不多于|至多|不得高于|不高于|上限为)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)\s*([%％])?`)},
}

var policySLASuffix = regexp.MustCompile(`^(?:\s*[，,]\s*(?:统计周期(?:为|[:：])?\s*自然月|时区(?:为|[:：])?\s*[A-Za-z_]+/[A-Za-z_]+|公历(?:为|[:：])?\s*gregorian|故障口径(?:为|[:：])?\s*[a-z_]+|无排除项|不设排除))+$`)
var policyAnyNumber = regexp.MustCompile(`[0-9]+`)

func policyStatementSuffixAllowed(comparator, suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return comparator != rulepack.ComparatorMonthlySLA
	}
	switch comparator {
	case rulepack.ComparatorMonthlySLA:
		return policySLASuffix.MatchString(suffix) && strings.Count(suffix, "统计周期") == 1 && strings.Count(suffix, "时区") == 1 &&
			strings.Count(suffix, "公历") == 1 && strings.Count(suffix, "故障口径") == 1 &&
			strings.Count(suffix, "无排除项")+strings.Count(suffix, "不设排除") == 1
	case rulepack.ComparatorAdvancePaymentCeiling:
		suffix = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(suffix, "，"), ","))
		return suffix == "预付款以合同总额为基础" || suffix == "预付款按合同总额" || suffix == "合同总额为预付款计算基础"
	default:
		return false
	}
}

func policyMetadataStatement(text string) bool {
	text = strings.TrimSpace(text)
	return text == "无排除项" || text == "不设排除" || strings.HasPrefix(text, "统计周期") ||
		strings.HasPrefix(text, "时区") || strings.HasPrefix(text, "公历") || strings.HasPrefix(text, "故障口径")
}

func policyDimensionMentioned(text string) bool {
	lower := strings.ToLower(text)
	for _, term := range []string{"带宽", "保障速率", "可用率", "预付款", "bandwidth", "availability", "advance payment"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func policyUnresolvedQualifier(text string) bool {
	lower := strings.ToLower(text)
	for _, term := range []string{"例外", "除外", "不适用", "除非", "并非", "不保证", "另行协商", "视情况", "可能调整", "特殊情况", "可以降低", "予以豁免", "if ", "unless ", "except "} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// policyStatements preserves byte positions while using the same sentence
// boundaries as the RulePack evidence verifier.
func policyStatements(body string) []struct {
	text  string
	start int
} {
	var out []struct {
		text  string
		start int
	}
	start := 0
	for index, r := range body {
		if !strings.ContainsRune("。；;！!？?\n\r", r) {
			continue
		}
		if strings.TrimSpace(body[start:index]) != "" {
			out = append(out, struct {
				text  string
				start int
			}{body[start:index], start})
		}
		start = index + utf8.RuneLen(r)
	}
	if strings.TrimSpace(body[start:]) != "" {
		out = append(out, struct {
			text  string
			start int
		}{body[start:], start})
	}
	return out
}

func policyRational(value string) (*big.Rat, bool) {
	if len(value) == 0 || len(value) > 96 {
		return nil, false
	}
	parsed, ok := new(big.Rat).SetString(value)
	return parsed, ok && parsed.Sign() >= 0
}

func extractGovernedPolicyThresholds(body string) (map[string][]PolicyThresholdCandidate, []PolicyTextIssue, []PolicyTextIssue) {
	candidates := make(map[string][]PolicyThresholdCandidate)
	issues, semantic := []PolicyTextIssue{}, []PolicyTextIssue{}
	for _, statement := range policyStatements(body) {
		matches := 0
		for _, definition := range policyThresholdPatterns {
			for _, indices := range definition.pattern.FindAllStringSubmatchIndex(statement.text, -1) {
				matches++
				start, end := statement.start+indices[0], statement.start+indices[1]
				issue := PolicyTextIssue{Start: utf8.RuneCountInString(body[:start]), End: utf8.RuneCountInString(body[:end]), Quote: body[start:end]}
				if strings.TrimSpace(statement.text[:indices[0]]) != "" || !policyStatementSuffixAllowed(definition.comparator, statement.text[indices[1]:]) {
					issue.Reason = "unexpressible_qualifier_or_scope"
					issues = append(issues, issue)
					continue
				}
				value, ok := policyRational(statement.text[indices[2]:indices[3]])
				if !ok {
					issue.Reason = "invalid_numeric_value"
					issues = append(issues, issue)
					continue
				}
				unit := ""
				if indices[4] >= 0 {
					unit = statement.text[indices[4]:indices[5]]
				}
				candidate := PolicyThresholdCandidate{Comparator: definition.comparator, Start: issue.Start, End: issue.End, Quote: issue.Quote}
				switch definition.comparator {
				case rulepack.ComparatorMinimumBandwidth:
					candidate.Unit = unit
				case rulepack.ComparatorMonthlySLA:
					candidate.Unit = "percent"
					if value.Cmp(big.NewRat(100, 1)) > 0 {
						issue.Reason = "percentage_out_of_range"
					}
				case rulepack.ComparatorAdvancePaymentCeiling:
					candidate.Unit, candidate.Basis = "ratio", "contract_total"
					if unit != "" {
						value.Quo(value, big.NewRat(100, 1))
					} else if !strings.Contains(statement.text[indices[2]:indices[3]], "/") || !strings.Contains(issue.Quote, "比例") {
						issue.Reason = "ratio_unit_missing"
					}
					if value.Cmp(big.NewRat(1, 1)) > 0 {
						issue.Reason = "ratio_out_of_range"
					}
				}
				if issue.Reason != "" {
					issues = append(issues, issue)
					continue
				}
				candidate.Value = value.RatString()
				candidates[definition.comparator] = append(candidates[definition.comparator], candidate)
			}
		}
		if matches == 0 {
			issue := PolicyTextIssue{Start: utf8.RuneCountInString(body[:statement.start]), End: utf8.RuneCountInString(body[:statement.start+len(statement.text)]), Quote: statement.text}
			if policyDimensionMentioned(statement.text) || policyAnyNumber.MatchString(statement.text) || policyUnresolvedQualifier(statement.text) || policyMetadataStatement(statement.text) {
				issue.Reason = "unexpressible_threshold"
				issues = append(issues, issue)
			} else {
				issue.Reason = "semantic_check_only"
				semantic = append(semantic, issue)
			}
		}
	}
	return candidates, issues, semantic
}

func policyCandidateProved(rule rulepack.Rule, candidate PolicyThresholdCandidate, body string) bool {
	object := "带宽"
	if rule.Comparator == rulepack.ComparatorMinimumBandwidth && !strings.Contains(candidate.Quote, object) {
		object = "保障速率"
	}
	if rule.Comparator == rulepack.ComparatorMonthlySLA {
		object = "可用率"
	}
	if rule.Comparator == rulepack.ComparatorAdvancePaymentCeiling {
		object = "预付款"
	}
	fact := rulepack.Fact{ObjectRef: object, Value: candidate.Value, Unit: candidate.Unit, SourceRole: "procurement",
		MeasurementWindow: rule.RequiredFact.MeasurementWindow, TimeZone: rule.RequiredFact.TimeZone, Calendar: rule.RequiredFact.Calendar,
		OutageDefinition: rule.RequiredFact.OutageDefinition, Exclusions: rule.RequiredFact.Exclusions, Basis: candidate.Basis,
		Citations: []rulepack.Citation{{Quote: body}}}
	return rulepack.EvidenceSupportsAuthority(rule, fact)
}

func equalPolicyThreshold(candidate PolicyThresholdCandidate, threshold rulepack.Threshold) bool {
	left, leftOK := policyRational(candidate.Value)
	right, rightOK := policyRational(threshold.Value)
	if !leftOK || !rightOK {
		return false
	}
	if candidate.Comparator == rulepack.ComparatorMinimumBandwidth {
		if candidate.Unit == "Gbit/s" {
			left.Mul(left, big.NewRat(1000, 1))
		}
		if threshold.Unit == "Gbit/s" {
			right.Mul(right, big.NewRat(1000, 1))
		}
	} else if candidate.Unit != threshold.Unit || candidate.Basis != threshold.Basis {
		return false
	}
	return left.Cmp(right) == 0
}

func compareGovernedPolicyThresholds(content GovernedKnowledgeContent, manifest rulepack.Manifest) GovernedPolicyPreview {
	candidates, issues, semantic := extractGovernedPolicyThresholds(content.Body)
	preview := GovernedPolicyPreview{ExtractorRef: policyThresholdCompilerRef, Comparisons: []PolicyRuleComparison{}, Issues: issues, SemanticOnly: semantic, Approvable: true}
	rules := make(map[string]rulepack.Rule, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		rules[rule.RuleID] = rule
	}
	boundComparators := make(map[string]bool)
	for _, id := range content.Binding.RuleIDs {
		rule, exists := rules[id]
		comparison := PolicyRuleComparison{RuleID: id, Comparator: rule.Comparator, RuleThreshold: rule.Threshold, Status: "unexpressible"}
		if !exists {
			comparison.Reason = "unknown_rule"
			preview.Comparisons = append(preview.Comparisons, comparison)
			continue
		}
		boundComparators[rule.Comparator] = true
		items := candidates[rule.Comparator]
		if len(items) != 1 {
			comparison.Reason = "threshold_missing"
			if len(items) > 1 {
				comparison.Status, comparison.Reason = "ambiguous", "multiple_thresholds"
			}
			preview.Comparisons = append(preview.Comparisons, comparison)
			continue
		}
		candidate := items[0]
		comparison.Candidate = &candidate
		spans, covered := 0, 0
		for _, span := range content.Binding.SourceSpans {
			if span.RuleID == id {
				spans++
				if span.Start <= candidate.Start && span.End >= candidate.End {
					covered++
				}
			}
		}
		switch {
		case spans != 1 || covered != 1:
			comparison.Reason = "source_span_missing_or_ambiguous"
		case !policyCandidateProved(rule, candidate, content.Body):
			comparison.Reason = "unsupported_semantics_or_measurement_basis"
		case !equalPolicyThreshold(candidate, rule.Threshold):
			comparison.Status, comparison.Reason = "different", "threshold_or_unit_mismatch"
		default:
			comparison.Status = "match"
		}
		preview.Comparisons = append(preview.Comparisons, comparison)
	}
	for comparator, items := range candidates {
		if boundComparators[comparator] {
			continue
		}
		for _, candidate := range items {
			preview.Issues = append(preview.Issues, PolicyTextIssue{Start: candidate.Start, End: candidate.End, Quote: candidate.Quote, Reason: "unbound_supported_threshold"})
		}
	}
	sort.Slice(preview.Issues, func(i, j int) bool {
		if preview.Issues[i].Start != preview.Issues[j].Start {
			return preview.Issues[i].Start < preview.Issues[j].Start
		}
		if preview.Issues[i].End != preview.Issues[j].End {
			return preview.Issues[i].End < preview.Issues[j].End
		}
		return preview.Issues[i].Reason < preview.Issues[j].Reason
	})
	for _, comparison := range preview.Comparisons {
		if comparison.Status != "match" {
			preview.Approvable = false
		}
	}
	if len(preview.Issues) != 0 || len(preview.Comparisons) == 0 {
		preview.Approvable = false
	}
	return preview
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
		ctx := context.Background()
		version, _, _, _, err := loadGovernedKnowledge(ctx, tx, accounts.Tenant(admin), knowledgeID, versionID)
		if err != nil {
			return err
		}
		if err := authorizeKnowledgeReadWithCaseLocks(ctx, tx, admin, version.Content, false); err != nil {
			return err
		}
		if version.Content.Kind != "policy" {
			return fault.Invalid("只有政策版本可以提取规则候选")
		}
		binding := version.Content.Binding
		var raw []byte
		var contentHash, snapshotDigest string
		err = tx.QueryRow(ctx, `SELECT manifest,content_hash,snapshot_digest FROM rule_pack_versions WHERE tenant_id=$1 AND pack_id=$2 AND version=$3`,
			accounts.Tenant(admin), binding.PackID, binding.PackVersion).Scan(&raw, &contentHash, &snapshotDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.Conflict("制度引用的规则包不存在")
		}
		if err != nil {
			return err
		}
		pack, err := rulepack.Parse(raw)
		if err != nil || sha256Bytes(raw) != snapshotDigest || pack.Hash() != contentHash || contentHash != binding.PackContentHash || snapshotDigest != binding.PackSnapshotDigest {
			return fault.Conflict("制度引用的规则包编号无效")
		}
		preview = compareGovernedPolicyThresholds(version.Content, pack.Manifest())
		preview.Ref = version.Ref
		if err := validateKnowledgeBinding(ctx, tx, accounts.Tenant(admin), version.Content); err != nil {
			var problem *fault.Error
			if !errors.As(err, &problem) || problem.Status != 409 {
				return err
			}
			preview.Approvable = false
			preview.Issues = append(preview.Issues, PolicyTextIssue{Reason: "binding_or_policy_invalid"})
		}
		return nil
	})
	return preview, err
}

func validateKnowledgeDependencies(ctx context.Context, tx pgx.Tx, tenant string, content GovernedKnowledgeContent) error {
	seen := make(map[KnowledgeRef]bool, len(content.PolicyRefs))
	for _, ref := range content.PolicyRefs {
		if seen[ref] {
			return fault.Invalid("政策依赖不能重复")
		}
		seen[ref] = true
		policy, _, _, _, err := loadGovernedKnowledge(ctx, tx, tenant, ref.KnowledgeID, ref.VersionID)
		if err != nil {
			return fault.Conflict("依赖的政策版本不存在")
		}
		if policy.Ref != ref || !policy.Active || policy.Content.Kind != "policy" || policy.ApprovedDigest != ref.ContentSHA256 ||
			policy.Content.Binding.PackID != content.Binding.PackID || policy.Content.Binding.PackVersion != content.Binding.PackVersion ||
			policy.Content.Binding.PackContentHash != content.Binding.PackContentHash || policy.Content.Binding.PackSnapshotDigest != content.Binding.PackSnapshotDigest {
			return fault.Conflict("依赖的政策版本未批准、未生效或规则包不同")
		}
		p, s := policy.Content.Scope, content.Scope
		if p.BusinessLine != s.BusinessLine || p.Scenario != s.Scenario || p.ContractType != s.ContractType ||
			(p.OrgID != "" && p.OrgID != s.OrgID) || (p.CaseID != "" && p.CaseID != s.CaseID) {
			return fault.Conflict("依赖政策的范围不能覆盖该制度")
		}
		from, e1 := knowledgeTime(content.ValidFrom)
		until, e2 := knowledgeTime(content.ValidUntil)
		policyFrom, e3 := knowledgeTime(policy.Content.ValidFrom)
		policyUntil, e4 := knowledgeTime(policy.Content.ValidUntil)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || from.Before(policyFrom) || until.After(policyUntil) {
			return fault.Conflict("依赖政策的有效期不能覆盖该制度")
		}
		if err := validateKnowledgeBinding(ctx, tx, tenant, policy.Content); err != nil {
			return err
		}
	}
	return nil
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
		ctx := context.Background()
		var inner error
		out, inner = knowledgeIdempotency(ctx, tx, admin, input.IdempotencyKey, input, func() (GovernedKnowledgeVersion, error) {
			if !validResourceID(input.KnowledgeID) || !validKnowledgeHash(input.VersionID) || !validKnowledgeHash(input.ExpectedContentSHA256) ||
				input.ExpectedRevision < 1 || input.ExpectedPointerRevision < 0 || !validKnowledgeText(input.Reason, 2000) {
				return GovernedKnowledgeVersion{}, fault.Invalid("操作编号、版本或依据无效")
			}
			switch input.Action {
			case "submit", "validate", "approve", "publish", "withdraw", "rollback":
			default:
				return GovernedKnowledgeVersion{}, fault.Invalid("不支持的制度治理动作")
			}
			current, scopeKey, previousID, previousActive, err := loadGovernedKnowledge(ctx, tx, accounts.Tenant(admin), input.KnowledgeID, input.VersionID)
			if err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if err := authorizeKnowledgeSource(ctx, tx, admin, current.Content); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if current.Revision != input.ExpectedRevision || current.Ref.ContentSHA256 != input.ExpectedContentSHA256 {
				return GovernedKnowledgeVersion{}, fault.Conflict("制度修订或正文摘要已改变")
			}
			nextStatus := current.Status
			pointerAction := false
			crossCaseReviewed := current.CrossCaseReviewed
			approvedDigest, approvalActor, approvalReason := current.ApprovedDigest, current.ApprovalActorID, current.ApprovalReason
			switch input.Action {
			case "submit":
				if current.Status != "draft" {
					return GovernedKnowledgeVersion{}, fault.Conflict("只有草稿可以提交")
				}
				nextStatus = "submitted"
			case "validate":
				if current.Status != "submitted" {
					return GovernedKnowledgeVersion{}, fault.Conflict("只有已提交版本可以校验")
				}
				if err := validateKnowledgeBinding(ctx, tx, accounts.Tenant(admin), current.Content); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				nextStatus = "validated"
			case "approve":
				if current.Status != "validated" {
					return GovernedKnowledgeVersion{}, fault.Conflict("只有已校验版本可以批准")
				}
				if err := validateKnowledgeBinding(ctx, tx, accounts.Tenant(admin), current.Content); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				if _, err := knowledgeCandidate(ctx, tx, admin, current.Content, true); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				if err := validateKnowledgeDependencies(ctx, tx, accounts.Tenant(admin), current.Content); err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				if current.Content.Kind == "experience" && current.Content.Scope.CaseID != current.Content.SourceCaseID && !input.CrossCaseReviewed {
					return GovernedKnowledgeVersion{}, fault.Invalid("跨案经验必须明确审阅并去除来源案件敏感内容")
				}
				crossCaseReviewed = input.CrossCaseReviewed
				approvedDigest, approvalActor, approvalReason = current.Ref.ContentSHA256, admin.ID, input.Reason
				nextStatus = "approved"
			case "publish":
				if current.Status != "approved" {
					return GovernedKnowledgeVersion{}, fault.Conflict("只有已批准版本可以发布")
				}
				pointerAction, nextStatus = true, "published"
			case "withdraw":
				if current.Status != "published" || !current.Active {
					return GovernedKnowledgeVersion{}, fault.Conflict("只能撤回当前生效版本")
				}
				pointerAction, nextStatus = true, "withdrawn"
			case "rollback":
				if current.Status != "published" && current.Status != "withdrawn" {
					return GovernedKnowledgeVersion{}, fault.Conflict("只能回滚到曾经发布的版本")
				}
				if current.Active {
					return GovernedKnowledgeVersion{}, fault.Conflict("该版本已经生效")
				}
				pointerAction, nextStatus = true, "published"
			}
			if pointerAction {
				if input.ExpectedPointerRevision != current.PointerRevision {
					return GovernedKnowledgeVersion{}, fault.Conflict("制度生效指针已改变")
				}
				if input.Action != "withdraw" {
					if approvedDigest != current.Ref.ContentSHA256 || approvalActor == "" || approvalReason == "" {
						return GovernedKnowledgeVersion{}, fault.Conflict("制度批准与正文摘要不一致")
					}
					if err := validateKnowledgeBinding(ctx, tx, accounts.Tenant(admin), current.Content); err != nil {
						return GovernedKnowledgeVersion{}, err
					}
					if _, err := knowledgeCandidate(ctx, tx, admin, current.Content, true); err != nil {
						return GovernedKnowledgeVersion{}, err
					}
					if err := validateKnowledgeDependencies(ctx, tx, accounts.Tenant(admin), current.Content); err != nil {
						return GovernedKnowledgeVersion{}, err
					}
					if current.Content.Kind == "experience" && current.Content.Scope.CaseID != current.Content.SourceCaseID && !crossCaseReviewed {
						return GovernedKnowledgeVersion{}, fault.Conflict("跨案经验缺少内容审阅")
					}
				}
			}
			tag, err := tx.Exec(ctx, `UPDATE governed_knowledge_state SET status=$1,revision=revision+1,approved_digest=$2,approval_actor_id=$3,approval_reason=$4,cross_case_reviewed=$5
				WHERE tenant_id=$6 AND knowledge_id=$7 AND version_id=$8 AND revision=$9`, nextStatus, approvedDigest, approvalActor, approvalReason, crossCaseReviewed,
				accounts.Tenant(admin), input.KnowledgeID, input.VersionID, input.ExpectedRevision)
			if err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			if tag.RowsAffected() != 1 {
				return GovernedKnowledgeVersion{}, fault.Conflict("制度修订已改变")
			}
			pointerRevision := current.PointerRevision
			resultID, resultActive := previousID, previousActive
			if pointerAction {
				pointerRevision++
				resultID, resultActive = input.VersionID, input.Action != "withdraw"
				if current.PointerRevision == 0 {
					tag, err = tx.Exec(ctx, `INSERT INTO governed_knowledge_pointers (tenant_id,knowledge_id,scope_key,version_id,active,pointer_revision,updated_by,updated_at)
						VALUES ($1,$2,$3,$4,$5,1,$6,clock_timestamp()) ON CONFLICT DO NOTHING`, accounts.Tenant(admin), input.KnowledgeID, scopeKey, resultID, resultActive, admin.ID)
				} else {
					tag, err = tx.Exec(ctx, `UPDATE governed_knowledge_pointers SET version_id=$1,active=$2,pointer_revision=pointer_revision+1,updated_by=$3,updated_at=clock_timestamp()
						WHERE tenant_id=$4 AND knowledge_id=$5 AND scope_key=$6 AND pointer_revision=$7`, resultID, resultActive, admin.ID, accounts.Tenant(admin), input.KnowledgeID, scopeKey, current.PointerRevision)
				}
				if err != nil {
					return GovernedKnowledgeVersion{}, err
				}
				if tag.RowsAffected() != 1 {
					return GovernedKnowledgeVersion{}, fault.Conflict("制度生效指针已改变")
				}
			}
			if err := insertKnowledgeHistory(ctx, tx, accounts.Tenant(admin), GovernedKnowledgeHistoryEntry{Ref: current.Ref, Action: input.Action,
				Revision: current.Revision + 1, PointerRevision: pointerRevision, ActorID: admin.ID, Reason: input.Reason,
				PreviousVersionID: previousID, PreviousActive: previousActive, ResultingVersionID: resultID, ResultingActive: resultActive}); err != nil {
				return GovernedKnowledgeVersion{}, err
			}
			updated, _, _, _, err := loadGovernedKnowledge(ctx, tx, accounts.Tenant(admin), input.KnowledgeID, input.VersionID)
			return updated, err
		})
		return inner
	})
	return out, err
}

func (s *Store) readKnowledgeAs(admin accounts.User, fn func(pgx.Tx) error) error {
	ctx := context.Background()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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

type GovernedKnowledgeSummary struct {
	Ref             KnowledgeRef   `json:"ref"`
	Version         string         `json:"version"`
	Title           string         `json:"title"`
	Kind            string         `json:"kind"`
	SourceType      string         `json:"source_type"`
	Scope           KnowledgeScope `json:"scope"`
	ValidFrom       string         `json:"valid_from"`
	ValidUntil      string         `json:"valid_until"`
	Status          string         `json:"status"`
	Revision        int64          `json:"revision"`
	PointerRevision int64          `json:"pointer_revision"`
	Active          bool           `json:"active"`
}

// ListGovernedKnowledgeVersions returns metadata only; case-sourced versions
// remain invisible to administrators lacking current source-case access.
func (s *Store) ListGovernedKnowledgeVersions(user accounts.User) ([]GovernedKnowledgeSummary, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return nil, err
	}
	out := []GovernedKnowledgeSummary{}
	err = s.readKnowledgeAs(admin, func(tx pgx.Tx) error {
		ctx := context.Background()
		const batchSize = 128
		tenant := accounts.Tenant(admin)
		lastKnowledge, lastVersion := "", ""
		for {
			rows, captures, err := queryGovernedKnowledgeRows(ctx, tx, tenant,
				`AND ($2='' OR (v.knowledge_id,v.version_id)>($2,$3)) ORDER BY v.knowledge_id,v.version_id LIMIT $4`, lastKnowledge, lastVersion, batchSize)
			if err != nil {
				return err
			}
			for _, row := range rows {
				lastKnowledge, lastVersion = row.version.Ref.KnowledgeID, row.version.Ref.VersionID
				v, _, _, _, err := validateGovernedKnowledgeRow(ctx, tx, tenant, row, captures)
				if err != nil {
					return err
				}
				if err := authorizeKnowledgeReadWithCaseLocks(ctx, tx, admin, v.Content, false); err != nil {
					var problem *fault.Error
					if errors.As(err, &problem) && problem.Status == 404 {
						continue
					}
					return err
				}
				out = append(out, GovernedKnowledgeSummary{Ref: v.Ref, Version: v.Content.Version, Title: v.Content.Title, Kind: v.Content.Kind,
					SourceType: v.Content.SourceType, Scope: v.Content.Scope, ValidFrom: v.Content.ValidFrom, ValidUntil: v.Content.ValidUntil,
					Status: v.Status, Revision: v.Revision, PointerRevision: v.PointerRevision, Active: v.Active})
			}
			if len(rows) < batchSize {
				break
			}
		}
		return nil
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
		var inner error
		out, _, _, _, inner = loadGovernedKnowledge(context.Background(), tx, accounts.Tenant(admin), knowledgeID, versionID)
		if inner != nil {
			return inner
		}
		return authorizeKnowledgeReadWithCaseLocks(context.Background(), tx, admin, out.Content, false)
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
	var out []GovernedKnowledgeHistoryEntry
	err = s.readKnowledgeAs(admin, func(tx pgx.Tx) error {
		ctx := context.Background()
		rows, err := tx.Query(ctx, `SELECT seq,version_id,action,revision,pointer_revision,actor_id,reason,content_sha256,previous_version_id,previous_active,resulting_version_id,resulting_active,occurred_at
			FROM governed_knowledge_history WHERE tenant_id=$1 AND knowledge_id=$2 ORDER BY seq`, accounts.Tenant(admin), knowledgeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry GovernedKnowledgeHistoryEntry
			var at time.Time
			entry.Ref.KnowledgeID = knowledgeID
			if err := rows.Scan(&entry.Sequence, &entry.Ref.VersionID, &entry.Action, &entry.Revision, &entry.PointerRevision, &entry.ActorID, &entry.Reason, &entry.Ref.ContentSHA256,
				&entry.PreviousVersionID, &entry.PreviousActive, &entry.ResultingVersionID, &entry.ResultingActive, &at); err != nil {
				return err
			}
			entry.OccurredAt = at.UTC().Format(time.RFC3339Nano)
			out = append(out, entry)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		const batchSize = 128
		checked := make(map[string]bool)
		for start := 0; start < len(out); start += batchSize {
			end := start + batchSize
			if end > len(out) {
				end = len(out)
			}
			var ids []string
			pending := make(map[string]bool)
			for _, entry := range out[start:end] {
				id := entry.Ref.VersionID
				if !checked[id] && !pending[id] {
					pending[id] = true
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				continue
			}
			tenant := accounts.Tenant(admin)
			versions, captures, err := queryGovernedKnowledgeRows(ctx, tx, tenant,
				`AND v.knowledge_id=$2 AND v.version_id=ANY($3::text[])`, knowledgeID, ids)
			if err != nil {
				return err
			}
			byID := make(map[string]governedKnowledgeRow, len(versions))
			for _, version := range versions {
				byID[version.version.Ref.VersionID] = version
			}
			for _, entry := range out[start:end] {
				id := entry.Ref.VersionID
				if checked[id] {
					continue
				}
				stored, ok := byID[id]
				if !ok {
					return fault.NotFound("没有找到对应制度版本")
				}
				version, _, _, _, err := validateGovernedKnowledgeRow(ctx, tx, tenant, stored, captures)
				if err != nil {
					return err
				}
				if err := authorizeKnowledgeReadWithCaseLocks(ctx, tx, admin, version.Content, false); err != nil {
					return err
				}
				checked[id] = true
			}
		}
		return nil
	})
	if out == nil {
		out = []GovernedKnowledgeHistoryEntry{}
	}
	return out, err
}

func knowledgeCaseValue(item Case, field string) string {
	switch field {
	case "org_id":
		return item.OwnerOrgID
	case "case_id":
		return item.ID
	case "business_line":
		return item.BusinessLine
	case "scenario":
		return item.Scenario
	case "contract_type":
		return item.ContractType
	case "business_status":
		return item.BusinessStatus
	}
	return ""
}

func knowledgeApplies(content GovernedKnowledgeContent, item Case, at time.Time) (bool, error) {
	if item.OwnerOrgID == "" || item.ID == "" || item.BusinessLine == "" || item.Scenario == "" || item.ContractType == "" {
		return false, fault.Conflict("案件适用范围不完整")
	}
	scope := content.Scope
	if scope.OrgID != "" && scope.OrgID != item.OwnerOrgID || scope.CaseID != "" && scope.CaseID != item.ID ||
		scope.BusinessLine != item.BusinessLine || scope.Scenario != item.Scenario || scope.ContractType != item.ContractType {
		return false, nil
	}
	from, err := knowledgeTime(content.ValidFrom)
	if err != nil {
		return false, fault.Conflict("制度有效期无效")
	}
	until, err := knowledgeTime(content.ValidUntil)
	if err != nil {
		return false, fault.Conflict("制度有效期无效")
	}
	if at.Before(from) || !at.Before(until) {
		return false, nil
	}
	for _, condition := range content.Applicability {
		value := knowledgeCaseValue(item, condition.Field)
		if value == "" {
			return false, fault.Conflict("案件缺少判断制度适用性所需的信息")
		}
		if value != condition.Equals {
			return false, nil
		}
	}
	for _, condition := range content.Exclusions {
		value := knowledgeCaseValue(item, condition.Field)
		if value == "" {
			return false, fault.Conflict("案件缺少判断制度排除条件所需的信息")
		}
		if value == condition.Equals {
			return false, nil
		}
	}
	return true, nil
}

func knowledgeSelection(v GovernedKnowledgeVersion) GovernedKnowledgeSelection {
	var semanticOnly []PolicyTextIssue
	if v.Content.Kind == "policy" {
		_, _, statements := extractGovernedPolicyThresholds(v.Content.Body)
		if len(statements) != 0 {
			semanticOnly = statements
		}
	}
	return GovernedKnowledgeSelection{
		Ref: v.Ref, Kind: v.Content.Kind, SourceType: v.Content.SourceType, Scope: v.Content.Scope, Required: v.Content.Kind == "policy", Summary: v.Content.Summary,
		ValidFrom: v.Content.ValidFrom, ValidUntil: v.Content.ValidUntil, ExceptionBasis: v.Content.ExceptionBasis, PolicyRefs: v.Content.PolicyRefs,
		Applicability: v.Content.Applicability, Exclusions: v.Content.Exclusions, ReviewedApplicability: v.Content.ReviewedApplicability,
		ReviewedExclusions: v.Content.ReviewedExclusions, CheckAction: v.Content.CheckAction, MinimumEvidence: v.Content.MinimumEvidence,
		Counterexamples: v.Content.Counterexamples, SemanticOnly: semanticOnly,
		RequiresFactEvaluation: v.Content.Kind == "experience" || v.Content.Kind == "exception" || len(semanticOnly) > 0,
		PointerRevision:        v.PointerRevision, Binding: v.Content.Binding, SelectionReason: "published, authorized, in scope and effective at business time",
	}
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
	snapshot.SnapshotSHA256 = ""
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	snapshot.SnapshotSHA256 = sha256Bytes(raw)
	return snapshot, nil
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
	manifest := parsed.Manifest()
	type requiredRule struct{ id, version, ruleID string }
	var required []requiredRule
	for _, rule := range manifest.Rules {
		if !rule.Mandatory || rule.BusinessScope.BusinessLine != item.BusinessLine || rule.BusinessScope.Scenario != item.Scenario || rule.BusinessScope.ContractType != item.ContractType {
			continue
		}
		for _, authority := range rule.AuthoritySources {
			if authority.Kind == "internal_policy" || authority.Kind == "synthetic_demo" {
				required = append(required, requiredRule{authority.ID, authority.Version, rule.RuleID})
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT knowledge_id,version_id FROM governed_knowledge_pointers WHERE tenant_id=$1 AND active ORDER BY knowledge_id,scope_key`, item.TenantID)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	type identity struct{ id, version string }
	var ids []identity
	for rows.Next() {
		var id identity
		if err := rows.Scan(&id.id, &id.version); err != nil {
			rows.Close()
			return ReviewKnowledgeSnapshot{}, err
		}
		ids = append(ids, id)
		if len(ids) > 4096 {
			rows.Close()
			return ReviewKnowledgeSnapshot{}, fault.Conflict("适用制度集合超过处理上限")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ReviewKnowledgeSnapshot{}, err
	}
	rows.Close()
	var policies, optional []GovernedKnowledgeVersion
	seen := make(map[string]bool)
	for _, id := range ids {
		version, _, _, _, err := loadGovernedKnowledge(ctx, tx, item.TenantID, id.id, id.version)
		if err != nil {
			return ReviewKnowledgeSnapshot{}, fault.Conflict("无法解析生效制度的编号或内容")
		}
		// Legal text is searchable reference material. It is never automatically
		// inserted into policy dependency snapshots or treated as an executable
		// policy candidate.
		if version.Content.Kind == "legal" {
			continue
		}
		applies, err := knowledgeApplies(version.Content, item, at)
		if err != nil {
			return ReviewKnowledgeSnapshot{}, err
		}
		if !applies {
			continue
		}
		if !version.Active || version.ApprovedDigest != version.Ref.ContentSHA256 || version.ApprovalActorID == "" || version.ApprovalReason == "" {
			return ReviewKnowledgeSnapshot{}, fault.Conflict("适用制度未发布或缺少有效批准")
		}
		if err := authorizeKnowledgeAdoptionWithCaseLocks(ctx, tx, current, version, lockCases); err != nil {
			var problem *fault.Error
			if errors.As(err, &problem) && problem.Status == 404 {
				if version.Content.Kind == "policy" {
					return ReviewKnowledgeSnapshot{}, fault.Conflict("强制政策的来源授权不可用")
				}
				continue
			}
			return ReviewKnowledgeSnapshot{}, err
		}
		if seen[id.id] {
			return ReviewKnowledgeSnapshot{}, fault.Conflict("存在多个适用的制度版本")
		}
		seen[id.id] = true
		if err := validateKnowledgeBinding(ctx, tx, item.TenantID, version.Content); err != nil {
			return ReviewKnowledgeSnapshot{}, err
		}
		b := version.Content.Binding
		if b.PackID != pack.PackID || b.PackVersion != pack.Version || b.PackContentHash != pack.ContentHash || b.PackSnapshotDigest != pack.SnapshotDigest {
			return ReviewKnowledgeSnapshot{}, fault.Conflict("制度绑定与案件适用规则包不一致")
		}
		if version.Content.Kind == "experience" && version.Content.SourceCaseID != item.ID && !version.CrossCaseReviewed {
			return ReviewKnowledgeSnapshot{}, fault.Conflict("跨案经验缺少敏感内容审阅")
		}
		if version.Content.Kind == "policy" {
			policies = append(policies, version)
		} else {
			optional = append(optional, version)
		}
	}
	for _, need := range required {
		matched := false
		for _, policy := range policies {
			if policy.Content.KnowledgeID != need.id || policy.Content.Version != need.version {
				continue
			}
			for _, ruleID := range policy.Content.Binding.RuleIDs {
				if ruleID == need.ruleID {
					matched = true
				}
			}
		}
		if !matched {
			return ReviewKnowledgeSnapshot{}, fault.Conflict("强制规则缺少适用的已发布政策绑定")
		}
	}
	for _, version := range optional {
		for _, dependency := range version.Content.PolicyRefs {
			found := false
			for _, policy := range policies {
				if policy.Ref == dependency {
					found = true
				}
			}
			if !found {
				return ReviewKnowledgeSnapshot{}, fault.Conflict("制度所依赖的精确政策未生效")
			}
		}
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].Ref.KnowledgeID < policies[j].Ref.KnowledgeID })
	sort.Slice(optional, func(i, j int) bool { return optional[i].Ref.KnowledgeID < optional[j].Ref.KnowledgeID })
	authorizationRevision, err := knowledgeAuthorizationRevision(current, item)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	snapshot := ReviewKnowledgeSnapshot{Encoding: "review-knowledge-snapshot/v1", BusinessAt: at.Format(time.RFC3339Nano),
		Scope:                 KnowledgeScope{OrgID: item.OwnerOrgID, CaseID: item.ID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType},
		AuthorizationRevision: authorizationRevision, RulePack: rulePackRef(pack), RequiredPolicies: []GovernedKnowledgeSelection{}, OptionalCandidates: []GovernedKnowledgeSelection{}, Excluded: []GovernedKnowledgeExclusion{}}
	for _, policy := range policies {
		snapshot.RequiredPolicies = append(snapshot.RequiredPolicies, knowledgeSelection(policy))
	}
	for i, version := range optional {
		if i < optionalLimit {
			snapshot.OptionalCandidates = append(snapshot.OptionalCandidates, knowledgeSelection(version))
		} else {
			snapshot.OptionalTruncated = true
			snapshot.Excluded = append(snapshot.Excluded, GovernedKnowledgeExclusion{Ref: version.Ref, Reason: "optional_budget"})
		}
	}
	if !evidenceCutoff.IsZero() {
		frozenAuthorities, err := freezeLegalAuthoritiesTx(ctx, tx, current, item, parsed, at, evidenceCutoff)
		if err != nil {
			return ReviewKnowledgeSnapshot{}, err
		}
		if len(frozenAuthorities) > 0 {
			snapshot.EvidenceCutoffAt = evidenceCutoff.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
			snapshot.EvidenceCutoffMode = cutoffMode
			snapshot.ExplicitEvidenceCutoffAt = explicitCutoff
			snapshot.LegalAuthorities = frozenAuthorities
		}
	}
	return sealKnowledgeSnapshot(snapshot)
}

// ResolveGovernedKnowledge resolves for a real authorized case and explicit
// business time. Acceptance must use resolveGovernedKnowledgeTx instead.
func (s *Store) ResolveGovernedKnowledge(ctx context.Context, user accounts.User, caseID, businessAt string, optionalLimit int) (ReviewKnowledgeSnapshot, error) {
	if ctx == nil {
		return ReviewKnowledgeSnapshot{}, fault.Invalid("解析上下文无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ReviewKnowledgeSnapshot{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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
	if hasLegalReviewSnapshot(&frozen) || hasLegalReviewSnapshot(&current) {
		if !hasLegalReviewSnapshot(&frozen) || !hasLegalReviewSnapshot(&current) || !validLegalReuseFingerprint(&frozen) ||
			!validLegalReuseFingerprint(&current) || frozen.LegalReuseFingerprint != current.LegalReuseFingerprint {
			return fault.Conflict("本次审查的法律来源或适用事实已改变，请发起新的审查")
		}
	}
	if current.Scope != frozen.Scope || current.AuthorizationRevision != frozen.AuthorizationRevision || current.RulePack != frozen.RulePack || len(current.RequiredPolicies) != len(frozen.RequiredPolicies) {
		return fault.Conflict("授权、规则包或强制政策已改变")
	}
	for i := range frozen.RequiredPolicies {
		if !reflect.DeepEqual(frozen.RequiredPolicies[i], current.RequiredPolicies[i]) {
			return fault.Conflict("强制政策已改变或撤回")
		}
	}
	seenOptional := make(map[KnowledgeRef]bool, len(frozen.OptionalCandidates))
	for _, selected := range frozen.OptionalCandidates {
		if seenOptional[selected.Ref] {
			return fault.Conflict("本次审查保存的制度记录包含重复候选")
		}
		seenOptional[selected.Ref] = true
		found := false
		for _, available := range current.OptionalCandidates {
			if reflect.DeepEqual(selected, available) {
				found = true
				break
			}
		}
		if !found {
			return fault.Conflict("本次审查选择的可选制度已发生变化或被撤回")
		}
	}
	return nil
}

// validateFrozenGovernedKnowledgeForWorkerTx verifies the immutable accepted
// snapshot, current authorization, exact legal provenance and the pre-existing
// mandatory-policy withdrawal boundary. It deliberately does not resolve new
// legal facts or current authority status for an already accepted attempt.
func validateFrozenGovernedKnowledgeForWorkerTx(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot) (accounts.User, error) {
	return validateFrozenGovernedKnowledgeWithCaseLocks(ctx, tx, user, caseID, frozen, true)
}

func validateFrozenGovernedKnowledgeWithCaseLocks(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot, lockCases bool) (accounts.User, error) {
	if frozen.Encoding != "review-knowledge-snapshot/v1" || !validKnowledgeHash(frozen.SnapshotSHA256) {
		return accounts.User{}, fault.Conflict("本次审查保存的制度记录无效")
	}
	sealed, err := sealKnowledgeSnapshot(frozen)
	if err != nil || sealed.SnapshotSHA256 != frozen.SnapshotSHA256 {
		return accounts.User{}, fault.Conflict("本次审查保存的制度记录摘要不一致")
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
	revision, err := knowledgeAuthorizationRevision(currentUser, item)
	if err != nil || revision != frozen.AuthorizationRevision {
		return accounts.User{}, fault.Conflict("本次审查的案件授权已改变")
	}
	if err := validateFrozenLegalAuthorityCapturesTx(ctx, tx, accounts.Tenant(currentUser), frozen.LegalAuthorities); err != nil {
		return accounts.User{}, err
	}
	for _, selected := range frozen.RequiredPolicies {
		version, _, _, active, loadErr := loadGovernedKnowledge(ctx, tx, accounts.Tenant(currentUser), selected.Ref.KnowledgeID, selected.Ref.VersionID)
		if loadErr != nil || !active || version.Ref != selected.Ref || !reflect.DeepEqual(knowledgeSelection(version), selected) {
			return accounts.User{}, fault.Conflict("强制政策已改变或撤回")
		}
	}
	for _, selected := range frozen.OptionalCandidates {
		version, _, _, _, loadErr := loadGovernedKnowledge(ctx, tx, accounts.Tenant(currentUser), selected.Ref.KnowledgeID, selected.Ref.VersionID)
		if loadErr != nil || version.Ref != selected.Ref {
			return accounts.User{}, fault.Conflict("本次审查保存的可选制度无法验证")
		}
		// The frozen content and selection stay immutable, but a source-case
		// grant can be revoked after acceptance. Recheck access to each exact
		// frozen candidate before a worker adopts or returns its content.
		if err := authorizeKnowledgeAdoptionWithCaseLocks(ctx, tx, currentUser, version, lockCases); err != nil {
			var problem *fault.Error
			if errors.As(err, &problem) && (problem.Status == 403 || problem.Status == 404) {
				return accounts.User{}, fault.Conflict("本次审查可选制度的案件授权已改变")
			}
			return accounts.User{}, err
		}
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
	if ctx == nil || s == nil || s.pool == nil {
		return fault.New(503, "governance_unavailable", "制度数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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
	if ctx == nil {
		return fault.Invalid("解析上下文无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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

// GovernedKnowledgeReviewContent is the reviewed content allowed into an
// authorized attempt. Source-case and feedback identifiers are intentionally
// absent; source-sensitive content requires the separate source-case grant.
type GovernedKnowledgeReviewContent struct {
	Ref                   KnowledgeRef `json:"ref"`
	Kind                  string       `json:"kind"`
	Title                 string       `json:"title"`
	Body                  string       `json:"body"`
	Summary               string       `json:"summary"`
	ValidFrom             string       `json:"valid_from"`
	ValidUntil            string       `json:"valid_until"`
	ExceptionBasis        string       `json:"exception_basis,omitempty"`
	ReviewedApplicability []string     `json:"reviewed_applicability,omitempty"`
	ReviewedExclusions    []string     `json:"reviewed_exclusions,omitempty"`
	CheckAction           string       `json:"check_action,omitempty"`
	MinimumEvidence       string       `json:"minimum_evidence,omitempty"`
	Counterexamples       []string     `json:"counterexamples,omitempty"`
}

// ReadFrozenGovernedKnowledge reads only a reference from the attempt's
// authoritative stored snapshot. Callers must supply the persisted snapshot,
// not a snapshot or digest supplied by a browser, model, or other untrusted
// source. Revalidation checks current target and, when required, source and
// ancestor access along with withdrawal under the same transaction.
func (s *Store) ReadFrozenGovernedKnowledge(ctx context.Context, user accounts.User, caseID string, frozen ReviewKnowledgeSnapshot, ref KnowledgeRef) (GovernedKnowledgeReviewContent, error) {
	if ctx == nil {
		return GovernedKnowledgeReviewContent{}, fault.Invalid("解析上下文无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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
	found := false
	for _, v := range frozen.RequiredPolicies {
		if v.Ref == ref {
			found = true
		}
	}
	for _, v := range frozen.OptionalCandidates {
		if v.Ref == ref {
			found = true
		}
	}
	if !found {
		return GovernedKnowledgeReviewContent{}, fault.NotFound("制度不在本次审查保存的候选列表中")
	}
	version, _, _, _, err := loadGovernedKnowledge(ctx, tx, accounts.Tenant(current), ref.KnowledgeID, ref.VersionID)
	if err != nil || version.Ref != ref || !version.Active {
		return GovernedKnowledgeReviewContent{}, fault.Conflict("本次审查使用的制度已撤回，或记录编号不一致")
	}
	c := version.Content
	out := GovernedKnowledgeReviewContent{Ref: ref, Kind: c.Kind, Title: c.Title, Body: c.Body, Summary: c.Summary,
		ValidFrom: c.ValidFrom, ValidUntil: c.ValidUntil, ExceptionBasis: c.ExceptionBasis, ReviewedApplicability: c.ReviewedApplicability,
		ReviewedExclusions: c.ReviewedExclusions, CheckAction: c.CheckAction, MinimumEvidence: c.MinimumEvidence, Counterexamples: c.Counterexamples}
	if _, err := trustedUser(current); err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GovernedKnowledgeReviewContent{}, err
	}
	return out, nil
}

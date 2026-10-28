package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docversion"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ExperienceFeedbackSchemaSQL is registered by the migrator after the existing
// review tables and forbid_append_only function. Core owns both new tables;
// registration must also configure their forced RLS and service grants.
const ExperienceFeedbackSchemaSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS experience_run_scope_identity ON review_runs (tenant_id,org_id,case_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS experience_attempt_scope_identity ON review_attempts (tenant_id,org_id,run_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS experience_finding_scope_identity ON findings (tenant_id,org_id,case_id,run_id,attempt_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS experience_decision_scope_identity ON decisions (tenant_id,org_id,case_id,run_id,finding_id,id);
CREATE TABLE IF NOT EXISTS experience_feedback (
    tenant_id text NOT NULL,
    org_id text NOT NULL,
    case_id text NOT NULL,
    feedback_id text NOT NULL,
    run_id text NOT NULL,
    attempt_id text NOT NULL,
    finding_id text NOT NULL,
    decision_id text NOT NULL,
    decision_action text NOT NULL,
    decision_disposition text NOT NULL,
    reason text NOT NULL,
    evidence_refs jsonb NOT NULL,
    applicant_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,feedback_id),
    UNIQUE (tenant_id,org_id,case_id,feedback_id),
    UNIQUE (tenant_id,applicant_id,idempotency_key),
    FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
    FOREIGN KEY (tenant_id,org_id,run_id,attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id,attempt_id,finding_id) REFERENCES findings (tenant_id,org_id,case_id,run_id,attempt_id,id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id,finding_id,decision_id) REFERENCES decisions (tenant_id,org_id,case_id,run_id,finding_id,id)
);
CREATE INDEX IF NOT EXISTS experience_feedback_case ON experience_feedback (tenant_id,org_id,case_id,created_at,feedback_id);
DROP TRIGGER IF EXISTS experience_feedback_append_only ON experience_feedback;
CREATE TRIGGER experience_feedback_append_only BEFORE UPDATE OR DELETE ON experience_feedback FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE TABLE IF NOT EXISTS experience_candidate_versions (
    tenant_id text NOT NULL,
    org_id text NOT NULL,
    case_id text NOT NULL,
    feedback_id text NOT NULL,
    candidate_id text NOT NULL,
    version_id text NOT NULL,
    base_version_id text,
    content_sha256 text NOT NULL,
    job_id text NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    input_sha256 text NOT NULL,
    result_sha256 text NOT NULL,
    model_ref text NOT NULL,
    template_ref text NOT NULL,
    content_bytes bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,candidate_id,version_id),
    UNIQUE (tenant_id,job_id,generation),
    UNIQUE (tenant_id,candidate_id,version_id,content_sha256),
    FOREIGN KEY (tenant_id,org_id,case_id,feedback_id) REFERENCES experience_feedback (tenant_id,org_id,case_id,feedback_id),
    FOREIGN KEY (tenant_id,candidate_id,base_version_id) REFERENCES experience_candidate_versions (tenant_id,candidate_id,version_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS experience_candidate_first_revision ON experience_candidate_versions (tenant_id,candidate_id) WHERE base_version_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS experience_candidate_successor ON experience_candidate_versions (tenant_id,candidate_id,base_version_id) WHERE base_version_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS experience_candidate_source ON experience_candidate_versions (tenant_id,org_id,case_id,feedback_id,created_at);
DROP TRIGGER IF EXISTS experience_candidate_append_only ON experience_candidate_versions;
CREATE TRIGGER experience_candidate_append_only BEFORE UPDATE OR DELETE ON experience_candidate_versions FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

// ExperienceEvidenceRef points into one selected, ready parse of the attempt.
// Offsets are Unicode code points in [Start, End), not byte indices.
type ExperienceEvidenceRef struct {
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	NormalizedDigest string `json:"normalized_digest"`
	Quote            string `json:"quote"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
}

type ExperienceFeedbackInput struct {
	CaseID         string                  `json:"case_id"`
	RunID          string                  `json:"run_id"`
	AttemptID      string                  `json:"attempt_id"`
	FindingID      string                  `json:"finding_id"`
	DecisionID     string                  `json:"decision_id"`
	Reason         string                  `json:"reason"`
	EvidenceRefs   []ExperienceEvidenceRef `json:"evidence_refs"`
	IdempotencyKey string                  `json:"idempotency_key"`
}

type ExperienceFeedback struct {
	FeedbackID          string                  `json:"feedback_id"`
	TenantID            string                  `json:"tenant_id"`
	OrgID               string                  `json:"org_id"`
	CaseID              string                  `json:"case_id"`
	RunID               string                  `json:"run_id"`
	AttemptID           string                  `json:"attempt_id"`
	FindingID           string                  `json:"finding_id"`
	DecisionID          string                  `json:"decision_id"`
	DecisionAction      string                  `json:"decision_action"`
	DecisionDisposition string                  `json:"decision_disposition"`
	Reason              string                  `json:"reason"`
	EvidenceRefs        []ExperienceEvidenceRef `json:"evidence_refs"`
	ApplicantID         string                  `json:"applicant_id"`
	CreatedAt           time.Time               `json:"created_at"`
}

func experienceIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 256
}

func validateExperienceFeedbackInput(input *ExperienceFeedbackInput) error {
	if !experienceIdentity(input.CaseID) || !experienceIdentity(input.RunID) || !experienceIdentity(input.AttemptID) ||
		!experienceIdentity(input.FindingID) || !experienceIdentity(input.DecisionID) ||
		!experienceIdentity(input.IdempotencyKey) || len(input.IdempotencyKey) > 80 {
		return fault.Invalid("反馈缺少有效的来源编号或请求标识")
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if runeCount(input.Reason) < 2 || runeCount(input.Reason) > 1000 || len(input.EvidenceRefs) == 0 || len(input.EvidenceRefs) > 32 {
		return fault.Invalid("反馈需要填写纠正理由，并选择本次审查使用的原文作为证据")
	}
	for _, ref := range input.EvidenceRefs {
		if !experienceIdentity(ref.DocumentID) || !experienceIdentity(ref.ParsedDocumentID) ||
			ref.Quote == "" || runeCount(ref.Quote) > 4096 || ref.Start < 0 || ref.End <= ref.Start {
			return fault.Invalid("反馈证据的原文位置不完整")
		}
		if ref.NormalizedDigest != "" {
			if _, err := docversion.ParseDigest(ref.NormalizedDigest); err != nil {
				return fault.Invalid("反馈证据的摘要无效")
			}
		}
	}
	return nil
}

// beginExperienceCase takes the same case lock as membership and review writers,
// then resolves the case and current grants inside the transaction.
func (s *Store) beginExperienceCase(ctx context.Context, user accounts.User, caseID string, action access.Action) (pgx.Tx, accounts.User, Case, error) {
	if ctx == nil {
		return nil, accounts.User{}, Case{}, errors.New("feedback context is nil")
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, accounts.User{}, Case{}, err
	}
	if !experienceIdentity(caseID) {
		return nil, accounts.User{}, Case{}, fault.NotFound("没有找到对应记录")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, accounts.User{}, Case{}, err
	}
	fail := func(err error) (pgx.Tx, accounts.User, Case, error) {
		_ = tx.Rollback(context.Background())
		return nil, accounts.User{}, Case{}, err
	}
	scope := userScope(current)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return fail(err)
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return fail(err)
	}
	var item Case
	var members string
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids,business_status,archived_at FROM cases WHERE tenant_id=$1 AND id=$2`,
		accounts.Tenant(current), caseID).Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members, &item.BusinessStatus, &item.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(fault.NotFound("没有找到对应记录"))
	}
	if err != nil {
		return fail(err)
	}
	if err := json.Unmarshal([]byte(members), &item.MemberIDs); err != nil {
		return fail(err)
	}
	if !authorizeCase(item, current, access.Read) {
		return fail(fault.NotFound("没有找到对应记录"))
	}
	if !authorizeCase(item, current, action) {
		return fail(fault.Forbidden("当前授权不能执行此案件操作"))
	}
	scope.OrgID = item.OwnerOrgID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return fail(err)
	}
	return tx, current, item, nil
}

func feedbackRequestHash(input ExperienceFeedbackInput) (string, error) {
	raw, err := json.Marshal(struct {
		Schema string                  `json:"schema"`
		Input  ExperienceFeedbackInput `json:"input"`
	}{Schema: "experience-feedback-request/v1", Input: input})
	if err != nil {
		return "", err
	}
	return sha256Bytes(raw), nil
}

func scanExperienceFeedback(row pgx.Row) (ExperienceFeedback, string, error) {
	var feedback ExperienceFeedback
	var refs []byte
	var requestHash string
	err := row.Scan(&feedback.FeedbackID, &feedback.TenantID, &feedback.OrgID, &feedback.CaseID,
		&feedback.RunID, &feedback.AttemptID, &feedback.FindingID, &feedback.DecisionID,
		&feedback.DecisionAction, &feedback.DecisionDisposition, &feedback.Reason, &refs,
		&feedback.ApplicantID, &feedback.CreatedAt, &requestHash)
	if err != nil {
		return ExperienceFeedback{}, "", err
	}
	if err := json.Unmarshal(refs, &feedback.EvidenceRefs); err != nil {
		return ExperienceFeedback{}, "", err
	}
	return feedback, requestHash, nil
}

const feedbackSelectColumns = `feedback_id,tenant_id,org_id,case_id,run_id,attempt_id,finding_id,decision_id,
 decision_action,decision_disposition,reason,evidence_refs,applicant_id,created_at,request_sha256`

// SubmitExperienceFeedback appends a suggestion while leaving the referenced
// human decision and the frozen review untouched. A repeated key returns the
// original receipt only for the same canonical request and current case grant.
func (s *Store) SubmitExperienceFeedback(ctx context.Context, user accounts.User, input ExperienceFeedbackInput) (ExperienceFeedback, error) {
	if s.ReadOnly() {
		return ExperienceFeedback{}, fault.New(503, "read_only", "只读模式下不能提交反馈")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return ExperienceFeedback{}, fault.Forbidden("当前服务不能提交反馈")
	}
	if err := validateExperienceFeedbackInput(&input); err != nil {
		return ExperienceFeedback{}, err
	}
	input.EvidenceRefs = append([]ExperienceEvidenceRef(nil), input.EvidenceRefs...)
	requestHash, err := feedbackRequestHash(input)
	if err != nil {
		return ExperienceFeedback{}, err
	}
	tx, current, caseItem, err := s.beginExperienceCase(ctx, user, input.CaseID, access.Decide)
	if err != nil {
		return ExperienceFeedback{}, err
	}
	defer tx.Rollback(context.Background())
	stored, existingHash, err := scanExperienceFeedback(tx.QueryRow(ctx, `SELECT `+feedbackSelectColumns+` FROM experience_feedback
		WHERE tenant_id=$1 AND applicant_id=$2 AND idempotency_key=$3`, caseItem.TenantID, current.ID, input.IdempotencyKey))
	if err == nil {
		if stored.CaseID != caseItem.ID || existingHash != requestHash {
			return ExperienceFeedback{}, fault.Conflict("这个请求标识已用于另一条反馈")
		}
		if !authorizeCase(caseItem, current, access.Read) || !authorizeCase(caseItem, current, access.Decide) {
			return ExperienceFeedback{}, fault.NotFound("没有找到对应记录")
		}
		if err := tx.Commit(ctx); err != nil {
			return ExperienceFeedback{}, err
		}
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExperienceFeedback{}, err
	}
	var action, disposition, frozenJSON string
	err = tx.QueryRow(ctx, `SELECT d.action,d.new_disposition,i.documents
		FROM review_runs r
		JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_attempt_inputs i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.run_id=r.id AND i.attempt_id=a.id AND i.input_hash=a.input_hash
		JOIN findings f ON f.tenant_id=r.tenant_id AND f.org_id=r.org_id AND f.case_id=r.case_id AND f.run_id=r.id AND f.attempt_id=a.id
		JOIN decisions d ON d.tenant_id=f.tenant_id AND d.org_id=f.org_id AND d.case_id=f.case_id AND d.run_id=f.run_id AND d.finding_id=f.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5 AND f.id=$6 AND d.id=$7
		AND a.history_kind='frozen' AND a.input_hash IS NOT NULL AND a.input_hash<>''`,
		caseItem.TenantID, caseItem.OwnerOrgID, caseItem.ID, input.RunID, input.AttemptID, input.FindingID, input.DecisionID).
		Scan(&action, &disposition, &frozenJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceFeedback{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return ExperienceFeedback{}, err
	}
	if expected, ok := decisionActions[action]; !ok || expected != disposition {
		return ExperienceFeedback{}, fault.Conflict("来源审查判断的类型和状态不一致")
	}
	var frozen []FrozenDocument
	if err := json.Unmarshal([]byte(frozenJSON), &frozen); err != nil {
		return ExperienceFeedback{}, err
	}
	for i := range input.EvidenceRefs {
		ref := &input.EvidenceRefs[i]
		if ref.NormalizedDigest == "" {
			for _, source := range frozen {
				if source.Document.ID == ref.DocumentID && source.ParsedDocumentID == ref.ParsedDocumentID && source.NormalizedHash != nil {
					ref.NormalizedDigest = *source.NormalizedHash
					break
				}
			}
		}
		if err := verifyFeedbackEvidence(ctx, tx, caseItem, input.RunID, input.AttemptID, frozen, *ref); err != nil {
			return ExperienceFeedback{}, err
		}
	}
	id, err := newID("feedback_")
	if err != nil {
		return ExperienceFeedback{}, err
	}
	refs, err := json.Marshal(input.EvidenceRefs)
	if err != nil {
		return ExperienceFeedback{}, err
	}
	stored, _, err = scanExperienceFeedback(tx.QueryRow(ctx, `INSERT INTO experience_feedback
		(tenant_id,org_id,case_id,feedback_id,run_id,attempt_id,finding_id,decision_id,decision_action,decision_disposition,reason,evidence_refs,applicant_id,idempotency_key,request_sha256)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (tenant_id,applicant_id,idempotency_key) DO NOTHING RETURNING `+feedbackSelectColumns,
		caseItem.TenantID, caseItem.OwnerOrgID, caseItem.ID, id, input.RunID, input.AttemptID, input.FindingID, input.DecisionID,
		action, disposition, input.Reason, refs, current.ID, input.IdempotencyKey, requestHash))
	if errors.Is(err, pgx.ErrNoRows) {
		stored, existingHash, err = scanExperienceFeedback(tx.QueryRow(ctx, `SELECT `+feedbackSelectColumns+` FROM experience_feedback
			WHERE tenant_id=$1 AND applicant_id=$2 AND idempotency_key=$3`, caseItem.TenantID, current.ID, input.IdempotencyKey))
		if err == nil && (stored.CaseID != caseItem.ID || existingHash != requestHash) {
			return ExperienceFeedback{}, fault.Conflict("这个请求标识已用于另一条反馈")
		}
	}
	if err != nil {
		return ExperienceFeedback{}, err
	}
	if !authorizeCase(caseItem, current, access.Read) || !authorizeCase(caseItem, current, access.Decide) {
		return ExperienceFeedback{}, fault.NotFound("没有找到对应记录")
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceFeedback{}, err
	}
	return stored, nil
}

// GetExperienceFeedback rechecks the requester's current authority over the
// source case; knowing a feedback ID or sharing its tenant is insufficient.
func (s *Store) GetExperienceFeedback(ctx context.Context, user accounts.User, caseID, feedbackID string) (ExperienceFeedback, error) {
	tx, _, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return ExperienceFeedback{}, err
	}
	defer tx.Rollback(context.Background())
	feedback, _, err := scanExperienceFeedback(tx.QueryRow(ctx, `SELECT `+feedbackSelectColumns+` FROM experience_feedback
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND feedback_id=$4`, item.TenantID, item.OwnerOrgID, item.ID, feedbackID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceFeedback{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return ExperienceFeedback{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceFeedback{}, err
	}
	return feedback, nil
}

func verifyFeedbackEvidence(ctx context.Context, tx pgx.Tx, item Case, runID, attemptID string, frozen []FrozenDocument, ref ExperienceEvidenceRef) error {
	invalid := func() error {
		return fault.Invalid("所选证据与本次审查使用的原文不匹配，或无法在原文中找到")
	}
	var source *FrozenDocument
	for i := range frozen {
		if frozen[i].Document.ID == ref.DocumentID {
			if source != nil {
				return invalid()
			}
			source = &frozen[i]
		}
	}
	if source == nil || !source.Ready || source.Document.TenantID != item.TenantID || source.Document.OrgID != item.OwnerOrgID ||
		source.Document.CaseID != item.ID || source.ParsedDocumentID != ref.ParsedDocumentID ||
		source.NormalizedHash == nil || *source.NormalizedHash != ref.NormalizedDigest {
		return invalid()
	}
	var payload, artifact, normalized, original, parserID, parserVersion, configHash, normalization, selectionArtifact, docDigest string
	var selectionOrder int64
	err := tx.QueryRow(ctx, `SELECT p.payload,p.artifact_digest,p.normalized_digest,s.original_digest,s.selection_order,
		s.parser_id,s.parser_version,s.config_hash,s.normalization_version,s.artifact_digest,d.digest
		FROM review_attempt_parse_selections s
		JOIN documents d ON d.tenant_id=s.tenant_id AND d.org_id=s.org_id AND d.id=s.document_id AND d.case_id=$3
		JOIN parsed_documents p ON p.tenant_id=s.tenant_id AND p.org_id=s.org_id AND p.id=s.parsed_document_id AND p.document_id=d.id
		WHERE s.tenant_id=$1 AND s.org_id=$2 AND s.run_id=$4 AND s.attempt_id=$5 AND s.document_id=$6 AND s.parsed_document_id=$7 AND p.status='ready' AND p.normalized_digest IS NOT NULL`,
		item.TenantID, item.OwnerOrgID, item.ID, runID, attemptID, ref.DocumentID, ref.ParsedDocumentID).
		Scan(&payload, &artifact, &normalized, &original, &selectionOrder, &parserID, &parserVersion, &configHash, &normalization, &selectionArtifact, &docDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid()
	}
	if err != nil {
		return err
	}
	parsed, err := parsePayload(payload)
	if err != nil || verifyParsed(parsed, Document{ID: ref.DocumentID, Digest: docDigest}) != nil ||
		parsed.ID != ref.ParsedDocumentID || parsed.Status != "ready" || parsed.ArtifactDigest != artifact ||
		parsed.NormalizedDigest != normalized || normalized != ref.NormalizedDigest || original != docDigest ||
		parsed.SelectionOrder != selectionOrder || parsed.ParserID != parserID || parsed.ParserVersion != parserVersion ||
		parsed.ConfigHash != configHash || parsed.NormalizationVersion != normalization ||
		(selectionArtifact != "" && selectionArtifact != artifact) ||
		source.OriginalHash != docDigest || source.Document.Digest != docDigest || source.ParsedArtifactDigest != artifact ||
		source.SelectionOrder != selectionOrder || source.ParserID != parserID || source.ParserVersion != parserVersion ||
		source.ConfigHash != configHash || source.NormalizationVersion != normalization || source.ParsedStatus != parsed.Status ||
		source.ParsedCreatedAt != parsed.CreatedAt || source.IRDigest != parsed.IRDigest || source.Text != parsed.Text ||
		source.FromOCR != parsed.FromOCR || source.ParseError != parsed.ParseError || !reflect.DeepEqual(source.IR, parsed.IR) {
		return invalid()
	}
	text := []rune(parsed.Text)
	if ref.End > len(text) || string(text[ref.Start:ref.End]) != ref.Quote {
		return invalid()
	}
	return nil
}

// ExperienceDistillInput is the immutable, server-created input of an
// independently leased experience_distill job. A revision names the exact
// previous version; an empty base creates the candidate's first version.
type ExperienceDistillInput struct {
	SchemaVersion        string                         `json:"schema_version"`
	TenantID             string                         `json:"tenant_id"`
	OrgID                string                         `json:"org_id"`
	CaseID               string                         `json:"case_id"`
	FeedbackID           string                         `json:"feedback_id"`
	DecisionID           string                         `json:"decision_id"`
	ApplicantID          string                         `json:"applicant_id"`
	CandidateID          string                         `json:"candidate_id"`
	BaseVersionID        string                         `json:"base_version_id"`
	ModelConfigurationID string                         `json:"model_configuration_id"`
	TemplateRef          string                         `json:"template_ref"`
	PolicyContext        ExperienceDistillPolicyContext `json:"policy_context"`
}

// ExperienceDistillPolicyContext contains only policies that were mandatory in
// the feedback's immutable review attempt. It is part of the job input hash.
type ExperienceDistillPolicyContext struct {
	RunID                   string                    `json:"run_id"`
	AttemptID               string                    `json:"attempt_id"`
	ReviewInputSHA256       string                    `json:"review_input_sha256"`
	KnowledgeSnapshotSHA256 string                    `json:"knowledge_snapshot_sha256"`
	RequiredPolicies        []ExperienceDistillPolicy `json:"required_policies"`
}

type ExperienceDistillPolicy struct {
	Ref        ExperiencePolicyRef `json:"ref"`
	Summary    string              `json:"summary"`
	ValidFrom  string              `json:"valid_from"`
	ValidUntil string              `json:"valid_until"`
}

type ExperiencePolicyRef struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
}

// ExperienceCandidateContent is the result JSON. It describes a checking
// method, never a finding disposition or an approval. Empty fields are kept in
// the draft so a failed or incomplete extraction cannot look approved.
type ExperienceCandidateContent struct {
	SchemaVersion   string                `json:"schema_version"`
	Applicability   []string              `json:"applicability"`
	Exclusions      []string              `json:"exclusions"`
	CheckAction     string                `json:"check_action"`
	MinimumEvidence []string              `json:"minimum_evidence"`
	Counterexamples []string              `json:"counterexamples"`
	ValidFrom       string                `json:"valid_from"`
	ValidUntil      string                `json:"valid_until"`
	PolicyRefs      []ExperiencePolicyRef `json:"policy_refs"`
	ReviewDiff      string                `json:"review_diff"`
}

// ExperienceCandidate is one immutable draft revision and its actual job and
// model-result provenance. No candidate is adopted by saving this record.
type ExperienceCandidate struct {
	TenantID      string                     `json:"tenant_id"`
	OrgID         string                     `json:"org_id"`
	CaseID        string                     `json:"case_id"`
	FeedbackID    string                     `json:"feedback_id"`
	CandidateID   string                     `json:"candidate_id"`
	VersionID     string                     `json:"version_id"`
	BaseVersionID string                     `json:"base_version_id,omitempty"`
	ContentSHA256 string                     `json:"content_sha256"`
	JobID         string                     `json:"job_id"`
	Generation    int64                      `json:"generation"`
	InputSHA256   string                     `json:"input_sha256"`
	ResultSHA256  string                     `json:"result_sha256"`
	ModelRef      string                     `json:"model_ref"`
	TemplateRef   string                     `json:"template_ref"`
	Content       ExperienceCandidateContent `json:"content"`
	CreatedAt     time.Time                  `json:"created_at"`
}

// ApprovalReady is a necessary completeness check, not an authorization or
// publication decision. Governance must call it on the exact stored revision
// inside the approval transaction, then separately check policy authority,
// review/redaction, source-case access, content hash and expected revision.
func (candidate ExperienceCandidate) ApprovalReady(now time.Time) error {
	if now.IsZero() || !experienceIdentity(candidate.TenantID) || !experienceIdentity(candidate.OrgID) ||
		!experienceIdentity(candidate.CaseID) || !experienceIdentity(candidate.FeedbackID) ||
		!experienceIdentity(candidate.CandidateID) || !experienceIdentity(candidate.JobID) ||
		!experienceIdentity(candidate.ModelRef) || !experienceIdentity(candidate.TemplateRef) || candidate.Generation <= 0 ||
		candidate.ContentSHA256 != candidate.ResultSHA256 {
		return fault.Invalid("经验候选缺少完整的来源编号或提炼任务编号")
	}
	for _, digest := range []string{candidate.VersionID, candidate.ContentSHA256, candidate.InputSHA256, candidate.ResultSHA256} {
		if _, err := docversion.ParseDigest(digest); err != nil {
			return fault.Invalid("经验候选来源摘要无效")
		}
	}
	content := candidate.Content
	if content.SchemaVersion != "experience-candidate/v1" ||
		!nonemptyExperienceList(content.Applicability) || !nonemptyExperienceList(content.Exclusions) ||
		!nonemptyExperienceList(content.MinimumEvidence) || !nonemptyExperienceList(content.Counterexamples) ||
		strings.TrimSpace(content.CheckAction) == "" || len(content.PolicyRefs) == 0 ||
		(candidate.BaseVersionID != "" && strings.TrimSpace(content.ReviewDiff) == "") {
		return fault.Invalid("经验候选缺少批准所需的检查边界、证据、反例或差异")
	}
	from, errFrom := time.Parse(time.RFC3339Nano, content.ValidFrom)
	until, errUntil := time.Parse(time.RFC3339Nano, content.ValidUntil)
	if errFrom != nil || errUntil != nil || !until.After(from) || !until.After(now) {
		return fault.Invalid("经验候选缺少有效的起止时间")
	}
	for _, ref := range content.PolicyRefs {
		if !experienceIdentity(ref.KnowledgeID) {
			return fault.Invalid("经验候选缺少精确政策版本")
		}
		if _, err := docversion.ParseDigest(ref.VersionID); err != nil {
			return fault.Invalid("经验候选引用的制度版本编号无效")
		}
		if _, err := docversion.ParseDigest(ref.ContentSHA256); err != nil {
			return fault.Invalid("经验候选政策摘要无效")
		}
	}
	return nil
}

func nonemptyExperienceList(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func decodeExperienceJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 65536 || !utf8.Valid(raw) {
		return errors.New("experience JSON size or encoding is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("experience JSON has trailing content")
	}
	return nil
}

const candidateSelectColumns = `tenant_id,org_id,case_id,feedback_id,candidate_id,version_id,base_version_id,
 content_sha256,job_id,generation,input_sha256,result_sha256,model_ref,template_ref,content_bytes,created_at`

func scanExperienceCandidate(row pgx.Row) (ExperienceCandidate, error) {
	var candidate ExperienceCandidate
	var base *string
	var raw []byte
	if err := row.Scan(&candidate.TenantID, &candidate.OrgID, &candidate.CaseID, &candidate.FeedbackID,
		&candidate.CandidateID, &candidate.VersionID, &base, &candidate.ContentSHA256, &candidate.JobID,
		&candidate.Generation, &candidate.InputSHA256, &candidate.ResultSHA256, &candidate.ModelRef,
		&candidate.TemplateRef, &raw, &candidate.CreatedAt); err != nil {
		return ExperienceCandidate{}, err
	}
	if base != nil {
		candidate.BaseVersionID = *base
	}
	if candidate.ContentSHA256 != candidate.ResultSHA256 || sha256Bytes(raw) != candidate.ContentSHA256 {
		return ExperienceCandidate{}, errors.New("stored experience candidate content identity is invalid")
	}
	if err := decodeExperienceJSON(raw, &candidate.Content); err != nil || candidate.Content.SchemaVersion != "experience-candidate/v1" {
		return ExperienceCandidate{}, errors.New("stored experience candidate content is invalid")
	}
	return candidate, nil
}

// authorizeExperienceAncestors protects drafts derived from an older case. An
// immutable revision may contain source-sensitive details from every ancestor,
// so access to just the newest source case is not sufficient.
func authorizeExperienceAncestors(ctx context.Context, tx pgx.Tx, current accounts.User, item Case, candidateID, baseVersionID string) error {
	if baseVersionID == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE ancestors AS (
		SELECT candidate_id,version_id,base_version_id,case_id FROM experience_candidate_versions
		WHERE tenant_id=$1 AND candidate_id=$2 AND version_id=$3
		UNION ALL
		SELECT v.candidate_id,v.version_id,v.base_version_id,v.case_id FROM experience_candidate_versions v
		JOIN ancestors a ON v.tenant_id=$1 AND v.candidate_id=a.candidate_id AND v.version_id=a.base_version_id
	) SELECT DISTINCT case_id FROM ancestors`, item.TenantID, candidateID, baseVersionID)
	if err != nil {
		return err
	}
	var cases []string
	for rows.Next() {
		var caseID string
		if err := rows.Scan(&caseID); err != nil {
			rows.Close()
			return err
		}
		cases = append(cases, caseID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return fault.Conflict("经验候选的前一版本不存在")
	}
	for _, caseID := range cases {
		if caseID == item.ID {
			continue
		}
		// The current case is already locked. A nonblocking second lock avoids
		// a cross-case deadlock when two candidates have opposite ancestry.
		var acquired bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(current_schema() || ':case:' || $1,0))`, caseID).Scan(&acquired); err != nil {
			return err
		}
		if !acquired {
			problem := fault.New(503, "case_busy", "关联案件正被修改，请重试")
			problem.Retryable = true
			return problem
		}
		var ancestor Case
		var members string
		err := tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids FROM cases WHERE tenant_id=$1 AND id=$2`, item.TenantID, caseID).
			Scan(&ancestor.ID, &ancestor.TenantID, &ancestor.OwnerOrgID, &ancestor.OwnerID, &members)
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(members), &ancestor.MemberIDs); err != nil {
			return err
		}
		if !authorizeCase(ancestor, current, access.Read) {
			return fault.NotFound("没有找到对应记录")
		}
	}
	return nil
}

// AcceptExperienceCandidate loads its bytes from the Execution-owned result
// under the real job/generation lease. The caller supplies only the exact
// delivery identity; it cannot submit model content or claim a lease.
func (s *Store) AcceptExperienceCandidate(ctx context.Context, user accounts.User, caseID, feedbackID, jobID string, generation int64, inputSHA256, resultSHA256 string) (ExperienceCandidate, error) {
	if s.ReadOnly() {
		return ExperienceCandidate{}, fault.New(503, "read_only", "只读模式下不能接纳经验候选")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return ExperienceCandidate{}, fault.Forbidden("当前服务不能接纳经验候选")
	}
	if !experienceIdentity(feedbackID) || !experienceIdentity(jobID) || generation <= 0 {
		return ExperienceCandidate{}, fault.Invalid("经验提炼结果的编号不完整")
	}
	if _, err := docversion.ParseDigest(inputSHA256); err != nil {
		return ExperienceCandidate{}, fault.Invalid("提炼输入摘要无效")
	}
	if _, err := docversion.ParseDigest(resultSHA256); err != nil {
		return ExperienceCandidate{}, fault.Invalid("提炼结果摘要无效")
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return ExperienceCandidate{}, err
	}
	defer tx.Rollback(context.Background())
	feedback, _, err := scanExperienceFeedback(tx.QueryRow(ctx, `SELECT `+feedbackSelectColumns+` FROM experience_feedback
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND feedback_id=$4`, item.TenantID, item.OwnerOrgID, item.ID, feedbackID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceCandidate{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return ExperienceCandidate{}, err
	}
	if feedback.ApplicantID != current.ID {
		return ExperienceCandidate{}, fault.NotFound("没有找到对应记录")
	}
	// Replaying an accepted delivery must not require the job still to be live.
	stored, err := scanExperienceCandidate(tx.QueryRow(ctx, `SELECT `+candidateSelectColumns+` FROM experience_candidate_versions
		WHERE tenant_id=$1 AND job_id=$2 AND generation=$3`, item.TenantID, jobID, generation))
	if err == nil {
		if stored.OrgID != item.OwnerOrgID || stored.CaseID != item.ID || stored.FeedbackID != feedbackID ||
			stored.InputSHA256 != inputSHA256 || stored.ResultSHA256 != resultSHA256 {
			return ExperienceCandidate{}, fault.Conflict("提炼结果已用于不同的候选")
		}
		if err := authorizeExperienceAncestors(ctx, tx, current, item, stored.CandidateID, stored.BaseVersionID); err != nil {
			return ExperienceCandidate{}, err
		}
		if !authorizeCase(item, current, access.Read) {
			return ExperienceCandidate{}, fault.NotFound("没有找到对应记录")
		}
		if err := tx.Commit(ctx); err != nil {
			return ExperienceCandidate{}, err
		}
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExperienceCandidate{}, err
	}
	if !authorizeCase(item, current, access.Review) {
		return ExperienceCandidate{}, fault.Forbidden("当前授权不能接纳提炼结果")
	}
	var tenantID, orgID, sourceCase, applicantID, sourceFeedback, storedInputSHA, storedResultSHA, modelRef, templateRef string
	var inputBytes, resultBytes []byte
	err = tx.QueryRow(ctx, `SELECT tenant_id,org_id,case_id,actor_id,feedback_id,input_sha256,input_bytes,
		result_sha256,result_bytes,model_ref,template_ref FROM experience_distill_lock_result($1,$2,$3,$4)`,
		jobID, generation, inputSHA256, resultSHA256).
		Scan(&tenantID, &orgID, &sourceCase, &applicantID, &sourceFeedback, &storedInputSHA, &inputBytes,
			&storedResultSHA, &resultBytes, &modelRef, &templateRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceCandidate{}, fault.Conflict("提炼结果的任务或租约已失效")
	}
	if err != nil {
		return ExperienceCandidate{}, err
	}
	if tenantID != item.TenantID || orgID != item.OwnerOrgID || sourceCase != item.ID || applicantID != current.ID ||
		sourceFeedback != feedback.FeedbackID || storedInputSHA != inputSHA256 || storedResultSHA != resultSHA256 ||
		sha256Bytes(inputBytes) != inputSHA256 || sha256Bytes(resultBytes) != resultSHA256 ||
		!experienceIdentity(modelRef) || !experienceIdentity(templateRef) {
		return ExperienceCandidate{}, fault.Conflict("提炼结果与任务来源不一致")
	}
	var input ExperienceDistillInput
	var content ExperienceCandidateContent
	if err := decodeExperienceJSON(inputBytes, &input); err != nil || input.SchemaVersion != "experience-distill-input/v1" ||
		input.TenantID != tenantID || input.OrgID != orgID || input.CaseID != sourceCase || input.FeedbackID != feedbackID ||
		input.DecisionID != feedback.DecisionID || input.ApplicantID != applicantID || !experienceIdentity(input.CandidateID) ||
		!experienceIdentity(input.ModelConfigurationID) || !experienceIdentity(input.TemplateRef) || input.TemplateRef != templateRef {
		return ExperienceCandidate{}, fault.Conflict("提炼任务输入与反馈来源不一致")
	}
	if input.BaseVersionID != "" {
		if _, err := docversion.ParseDigest(input.BaseVersionID); err != nil {
			return ExperienceCandidate{}, fault.Conflict("经验候选的前一版本无效")
		}
		if err := authorizeExperienceAncestors(ctx, tx, current, item, input.CandidateID, input.BaseVersionID); err != nil {
			return ExperienceCandidate{}, err
		}
	}
	if err := validateExperiencePolicyContext(ctx, tx, item, feedback, input); err != nil {
		return ExperienceCandidate{}, err
	}
	if err := decodeExperienceJSON(resultBytes, &content); err != nil || content.SchemaVersion != "experience-candidate/v1" {
		return ExperienceCandidate{}, fault.Conflict("提炼结果不是可读取的经验候选")
	}
	if err := validateExperiencePolicyRefs(content, input.PolicyContext); err != nil {
		return ExperienceCandidate{}, err
	}
	versionBytes, err := json.Marshal([]any{"experience-candidate-version/v1", tenantID, input.CandidateID, jobID, generation, resultSHA256})
	if err != nil {
		return ExperienceCandidate{}, err
	}
	versionID := sha256Bytes(versionBytes)
	var base any
	if input.BaseVersionID != "" {
		base = input.BaseVersionID
	}
	stored, err = scanExperienceCandidate(tx.QueryRow(ctx, `INSERT INTO experience_candidate_versions
		(tenant_id,org_id,case_id,feedback_id,candidate_id,version_id,base_version_id,content_sha256,job_id,generation,input_sha256,result_sha256,model_ref,template_ref,content_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING `+candidateSelectColumns,
		tenantID, orgID, sourceCase, feedbackID, input.CandidateID, versionID, base, resultSHA256,
		jobID, generation, inputSHA256, resultSHA256, modelRef, templateRef, resultBytes))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ExperienceCandidate{}, fault.Conflict("经验候选版本或提炼结果已被其他请求使用")
	}
	if err != nil {
		return ExperienceCandidate{}, fmt.Errorf("saving experience candidate: %w", err)
	}
	if !authorizeCase(item, current, access.Read) {
		return ExperienceCandidate{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeCase(item, current, access.Review) {
		return ExperienceCandidate{}, fault.Forbidden("当前授权不能接纳提炼结果")
	}
	if err := authorizeExperienceAncestors(ctx, tx, current, item, input.CandidateID, input.BaseVersionID); err != nil {
		return ExperienceCandidate{}, err
	}
	// The live result was locked before validation. It must still have a live
	// lease and a currently authorized applicant at the commit boundary.
	var live int
	err = tx.QueryRow(ctx, `SELECT 1 FROM experience_distill_lock_result($1,$2,$3,$4)`,
		jobID, generation, inputSHA256, resultSHA256).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceCandidate{}, fault.Conflict("提炼结果的任务或租约已失效")
	}
	if err != nil {
		return ExperienceCandidate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceCandidate{}, err
	}
	return stored, nil
}

// GetExperienceCandidate requires the exact version and current access to its
// source and all ancestor sources. Approval and publication are separate.
func (s *Store) GetExperienceCandidate(ctx context.Context, user accounts.User, caseID, candidateID, versionID string) (ExperienceCandidate, error) {
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return ExperienceCandidate{}, err
	}
	defer tx.Rollback(context.Background())
	candidate, err := scanExperienceCandidate(tx.QueryRow(ctx, `SELECT `+candidateSelectColumns+` FROM experience_candidate_versions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND candidate_id=$4 AND version_id=$5`,
		item.TenantID, item.OwnerOrgID, item.ID, candidateID, versionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceCandidate{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return ExperienceCandidate{}, err
	}
	if err := authorizeExperienceAncestors(ctx, tx, current, item, candidate.CandidateID, candidate.BaseVersionID); err != nil {
		return ExperienceCandidate{}, err
	}
	if !authorizeCase(item, current, access.Read) {
		return ExperienceCandidate{}, fault.NotFound("没有找到对应记录")
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceCandidate{}, err
	}
	return candidate, nil
}

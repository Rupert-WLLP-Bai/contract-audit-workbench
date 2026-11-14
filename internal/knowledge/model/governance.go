// Package model defines the immutable Knowledge governance values and their
// pure validation and lifecycle rules.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/fault"
	"contractaudit/internal/legalnorm"
)

const (
	ContentEncoding             = "governed-knowledge/v1"
	PolicyThresholdCompilerRef  = "governed-policy-threshold/v1"
	RulePackCompilerRef         = "governed-rulepack/v1"
	MaxPolicyBindingRules       = 32
	maxPolicyTextRunes          = 30000
	maxKnowledgeResourceIDBytes = 120
	maxRulePackIdentifierBytes  = 128
)

// KnowledgeRef identifies immutable content. VersionID identifies the tenant,
// family and display-version tuple; ContentSHA256 identifies the content.
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

// LegalMetadata binds one governed article body to an immutable captured
// official source and point-in-time registry observation. Legal review is
// deliberately fixed at NOT_RUN for this product slice.
type LegalMetadata struct {
	SourceID                    string               `json:"source_id"`
	NormTitle                   string               `json:"norm_title"`
	DocumentType                string               `json:"document_type"`
	NormVersion                 string               `json:"norm_version"`
	ArticleNumber               int                  `json:"article_number"`
	ArticleLabel                string               `json:"article_label"`
	Issuer                      string               `json:"issuer"`
	Publisher                   string               `json:"publisher"`
	OfficialURL                 string               `json:"official_url"`
	PromulgatedOn               string               `json:"promulgated_on"`
	EffectiveFrom               string               `json:"effective_from"`
	EffectiveUntil              string               `json:"effective_until,omitempty"`
	OfficialStatusObservationID string               `json:"official_status_observation_id"`
	OfficialStatus              string               `json:"official_status"`
	StatusRegistryURL           string               `json:"status_registry_url"`
	StatusObservedAt            string               `json:"status_observed_at"`
	StatusEvidenceSHA256        string               `json:"status_evidence_sha256"`
	StatusResponseStatus        int                  `json:"status_response_status"`
	StatusContentType           string               `json:"status_content_type"`
	SourceSnapshotID            string               `json:"source_snapshot_id"`
	SourceProvenance            string               `json:"source_provenance"`
	SourceCapturedAt            string               `json:"source_captured_at"`
	SourceResponseStatus        int                  `json:"source_response_status"`
	SourceContentType           string               `json:"source_content_type"`
	SourceSHA256                string               `json:"source_sha256"`
	NormTextSHA256              string               `json:"norm_text_sha256"`
	CanonicalTextVersion        string               `json:"canonical_text_version"`
	SourceTextStart             int                  `json:"source_text_start"`
	SourceTextEnd               int                  `json:"source_text_end"`
	ExtractorID                 string               `json:"extractor_id"`
	ExtractorVersion            string               `json:"extractor_version"`
	TextCheckStatus             string               `json:"text_check_status"`
	LegalReviewStatus           string               `json:"legal_review_status"`
	Reviewer                    *string              `json:"reviewer"`
	ApplicabilityConditions     []KnowledgeCondition `json:"applicability_conditions,omitempty"`
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

// ValidText reports whether a required string is non-empty, valid UTF-8 and at
// most limit Unicode code points.
func ValidText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit
}

// ParseBusinessTime parses the Knowledge API's explicit RFC3339 business time.
func ParseBusinessTime(raw string) (time.Time, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return time.Time{}, fault.Invalid("必须提供带时区的业务时间")
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || at.IsZero() {
		return time.Time{}, fault.Invalid("时间必须是带时区的 RFC3339 时间")
	}
	return at.UTC(), nil
}

// ValidHash accepts lowercase SHA-256 hex digests.
func ValidHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

// SHA256 returns the lowercase hexadecimal SHA-256 digest of raw bytes.
func SHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CanonicalContentBytes preserves the struct field order and JSON tags used by
// the persisted governed-knowledge/v1 content bytes.
func CanonicalContentBytes(content GovernedKnowledgeContent) ([]byte, error) {
	return json.Marshal(content)
}

// ContentDigest returns the canonical content bytes and the digest stored in
// KnowledgeRef.ContentSHA256.
func ContentDigest(content GovernedKnowledgeContent) ([]byte, string, error) {
	raw, err := CanonicalContentBytes(content)
	if err != nil {
		return nil, "", err
	}
	return raw, SHA256(raw), nil
}

// ScopeKey hashes the canonical JSON representation of a scope.
func ScopeKey(scope KnowledgeScope) (string, error) {
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	return SHA256(raw), nil
}

// VersionID preserves the legacy deterministic tuple encoding used for exact
// Knowledge version IDs.
func VersionID(tenantID, knowledgeID, displayVersion string) string {
	raw, _ := json.Marshal([]string{tenantID, knowledgeID, displayVersion})
	return SHA256(raw)
}

// ValidResourceID reports whether a Knowledge identifier obeys the existing resource ID rules.
func ValidResourceID(value string) bool { return validResourceID(value) }

func validResourceID(value string) bool {
	if value == "" || len(value) > maxKnowledgeResourceIDBytes || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f || char == '/' || char == '\\' {
			return false
		}
	}
	return true
}

func validRulePackIdentifier(value string) bool {
	if len(value) == 0 || len(value) > maxRulePackIdentifierBytes {
		return false
	}
	for index := range len(value) {
		char := value[index]
		switch {
		case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z', char >= '0' && char <= '9':
		case index > 0 && (char == '.' || char == '_' || char == ':' || char == '/' || char == '-'):
		default:
			return false
		}
	}
	return true
}

func validTextList(values []string) bool {
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

func metadataURL(metadata *LegalMetadata) string {
	if metadata == nil {
		return ""
	}
	return metadata.OfficialURL
}

// ValidateContent validates the Knowledge-owned immutable content value. Source
// existence, actor grants, candidate provenance and capture lookup stay outside.
func ValidateContent(content GovernedKnowledgeContent) error {
	if content.Encoding != ContentEncoding || !validResourceID(content.KnowledgeID) || !validResourceID(content.Version) ||
		!ValidText(content.Title, 200) || !ValidText(content.Body, maxPolicyTextRunes) || !ValidText(content.Summary, 2000) ||
		!ValidText(content.SourceReference, 2000) {
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
		return ValidateLegalContent(content)
	}
	if content.LegalMetadata != nil {
		return fault.Invalid("只有法律知识条目可以携带法律来源元数据")
	}
	scope := content.Scope
	if !validResourceID(scope.BusinessLine) || !validResourceID(scope.Scenario) || !validResourceID(scope.ContractType) ||
		(scope.OrgID != "" && !validResourceID(scope.OrgID)) || (scope.CaseID != "" && (!validResourceID(scope.CaseID) || scope.OrgID == "")) ||
		(content.SourceCaseID != "" && (!validResourceID(content.SourceCaseID) || !validResourceID(content.SourceOrgID))) ||
		(content.SourceCaseID == "" && content.SourceOrgID != "") {
		return fault.Invalid("制度作用域无效或不完整")
	}
	from, err := ParseBusinessTime(content.ValidFrom)
	if err != nil {
		return err
	}
	until, err := ParseBusinessTime(content.ValidUntil)
	if err != nil || !until.After(from) {
		return fault.Invalid("制度有效期必须是有时区的非空半开区间")
	}
	if len(content.Applicability) > 32 || len(content.Exclusions) > 32 || len(content.PolicyRefs) > 32 || len(content.Counterexamples) > 32 {
		return fault.Invalid("适用条件或依赖数量超过上限")
	}
	conditions := append(append([]KnowledgeCondition(nil), content.Applicability...), content.Exclusions...)
	for _, condition := range conditions {
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
		if !validResourceID(ref.KnowledgeID) || !ValidHash(ref.VersionID) || !ValidHash(ref.ContentSHA256) {
			return fault.Invalid("引用的制度编号无效")
		}
	}
	if content.Kind == "exception" && (scope.OrgID == "" || !ValidText(content.ExceptionBasis, 2000) || len(content.PolicyRefs) == 0) {
		return fault.Invalid("例外必须限定组织或案件，并提供批准依据、有效期和政策依赖")
	}
	if content.Kind == "experience" {
		if content.SourceCaseID == "" || content.SourceType != "human_feedback" || !validResourceID(content.SourceFeedbackID) ||
			!validResourceID(content.CandidateID) || !ValidHash(content.CandidateVersionID) || !ValidHash(content.CandidateSHA256) ||
			!ValidText(content.CandidateReviewDiff, 10000) || !validTextList(content.ReviewedApplicability) || !validTextList(content.ReviewedExclusions) ||
			!ValidText(content.CheckAction, 2000) || !ValidText(content.MinimumEvidence, 2000) || len(content.Counterexamples) == 0 || len(content.PolicyRefs) == 0 {
			return fault.Invalid("经验需要精确候选及审阅差异、授权来源、适用和排除条件、检查动作、最低证据、反例和政策依赖")
		}
		for _, example := range content.Counterexamples {
			if !ValidText(example, 2000) {
				return fault.Invalid("经验反例无效")
			}
		}
		if len(content.ReviewedApplicability) > 32 || len(content.ReviewedExclusions) > 32 {
			return fault.Invalid("经验适用或排除条件超过上限")
		}
		for _, statement := range append(append([]string(nil), content.ReviewedApplicability...), content.ReviewedExclusions...) {
			if !ValidText(statement, 2000) {
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
	binding := content.Binding
	if !validRulePackIdentifier(binding.PackID) || !validRulePackIdentifier(binding.PackVersion) || !ValidHash(binding.PackContentHash) ||
		!ValidHash(binding.PackSnapshotDigest) || binding.CompilerRef != RulePackCompilerRef || len(binding.RuleIDs) == 0 || len(binding.RuleIDs) > MaxPolicyBindingRules ||
		!ValidText(binding.CandidateDiff, 10000) || !ValidText(binding.ValidationEvidence, 10000) {
		return fault.Invalid("规则包绑定或验证依据无效")
	}
	if content.Kind == "policy" && binding.ExtractionCompilerRef != PolicyThresholdCompilerRef {
		return fault.Invalid("制度数值提取器的编号无效")
	}
	seen := make(map[string]bool, len(binding.RuleIDs))
	for _, id := range binding.RuleIDs {
		if !validRulePackIdentifier(id) || seen[id] {
			return fault.Invalid("规则绑定包含重复或无效规则")
		}
		seen[id] = true
	}
	spanRules := make(map[string]bool, len(binding.RuleIDs))
	bodyRunes := []rune(content.Body)
	for _, span := range binding.SourceSpans {
		if !seen[span.RuleID] || span.Start < 0 || span.End <= span.Start || span.End > len(bodyRunes) || string(bodyRunes[span.Start:span.End]) != span.Quote {
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

// ValidateLegalContent rejects policy fields on legal article values and then
// validates the article metadata without reading Legal capture storage.
func ValidateLegalContent(content GovernedKnowledgeContent) error {
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
	return ValidateLegalMetadata(metadata, content.Body)
}

// ValidateLegalMetadata checks only the immutable legal metadata value. It does
// not prove that the referenced capture exists or is visible to the caller.
func ValidateLegalMetadata(metadata *LegalMetadata, body string) error {
	if metadata == nil || metadata.SourceID == "" || metadata.NormTitle == "" || metadata.NormVersion == "" ||
		metadata.ArticleNumber <= 0 || metadata.ArticleLabel == "" || metadata.Issuer == "" || metadata.Publisher == "" ||
		metadata.OfficialURL == "" || metadata.OfficialStatusObservationID == "" || metadata.StatusRegistryURL == "" ||
		metadata.SourceSnapshotID == "" || metadata.SourceCapturedAt == "" || metadata.StatusObservedAt == "" ||
		!ValidHash(metadata.SourceSHA256) || !ValidHash(metadata.NormTextSHA256) || !ValidHash(metadata.StatusEvidenceSHA256) ||
		metadata.SourceTextStart < 0 || metadata.SourceTextEnd <= metadata.SourceTextStart || metadata.SourceResponseStatus != 200 ||
		metadata.StatusResponseStatus != 200 || metadata.TextCheckStatus != "PASS" || metadata.LegalReviewStatus != "NOT_RUN" ||
		metadata.Reviewer != nil || metadata.SourceProvenance != "verified_official_capture" || metadata.CanonicalTextVersion != legalnorm.CanonicalTextVersion ||
		metadata.ExtractorID == "" || metadata.ExtractorVersion == "" || !ValidHash(SHA256([]byte(body))) || metadata.NormTextSHA256 != SHA256([]byte(body)) {
		return fault.Invalid("法律条文缺少服务端核验的来源、正文或效力元数据")
	}
	switch metadata.DocumentType {
	case "law", "administrative_regulation", "judicial_interpretation", "normative_notice", "draft", "legislative_plan":
	default:
		return fault.Invalid("法律来源文件类型无效")
	}
	switch metadata.OfficialStatus {
	case legalnorm.StatusEffective, legalnorm.StatusRepealed, legalnorm.StatusNotYetEffective:
	default:
		return fault.Invalid("官方登记效力状态无效")
	}
	if _, err := time.Parse(time.RFC3339, metadata.SourceCapturedAt); err != nil {
		return fault.Invalid("法律来源抓取时间无效")
	}
	if _, err := time.Parse(time.RFC3339, metadata.StatusObservedAt); err != nil {
		return fault.Invalid("法律效力观察时间无效")
	}
	if _, err := time.Parse("2006-01-02", metadata.PromulgatedOn); err != nil {
		return fault.Invalid("法律公布日期无效")
	}
	if _, err := time.Parse("2006-01-02", metadata.EffectiveFrom); err != nil {
		return fault.Invalid("法律生效日期无效")
	}
	return nil
}

// PolicyRefSet preserves per-reference duplicate detection order while a caller
// loads exact dependency versions from its transaction.
type PolicyRefSet struct {
	seen map[KnowledgeRef]struct{}
}

func NewPolicyRefSet() *PolicyRefSet {
	return &PolicyRefSet{seen: make(map[KnowledgeRef]struct{})}
}

func (set *PolicyRefSet) Add(ref KnowledgeRef) error {
	if _, exists := set.seen[ref]; exists {
		return fault.Invalid("政策依赖不能重复")
	}
	set.seen[ref] = struct{}{}
	return nil
}

// ValidatePolicyDependency evaluates one exact, already-loaded dependency.
// Existence and any RulePack data reads remain with the caller's typed ports.
func ValidatePolicyDependency(content GovernedKnowledgeContent, expected KnowledgeRef, policy GovernedKnowledgeVersion) error {
	if policy.Ref != expected || !policy.Active || policy.Content.Kind != "policy" || policy.ApprovedDigest != expected.ContentSHA256 ||
		policy.Content.Binding.PackID != content.Binding.PackID || policy.Content.Binding.PackVersion != content.Binding.PackVersion ||
		policy.Content.Binding.PackContentHash != content.Binding.PackContentHash || policy.Content.Binding.PackSnapshotDigest != content.Binding.PackSnapshotDigest {
		return fault.Conflict("依赖的政策版本未批准、未生效或规则包不同")
	}
	policyScope, contentScope := policy.Content.Scope, content.Scope
	if policyScope.BusinessLine != contentScope.BusinessLine || policyScope.Scenario != contentScope.Scenario || policyScope.ContractType != contentScope.ContractType ||
		(policyScope.OrgID != "" && policyScope.OrgID != contentScope.OrgID) || (policyScope.CaseID != "" && policyScope.CaseID != contentScope.CaseID) {
		return fault.Conflict("依赖政策的范围不能覆盖该制度")
	}
	from, contentFromErr := ParseBusinessTime(content.ValidFrom)
	until, contentUntilErr := ParseBusinessTime(content.ValidUntil)
	policyFrom, policyFromErr := ParseBusinessTime(policy.Content.ValidFrom)
	policyUntil, policyUntilErr := ParseBusinessTime(policy.Content.ValidUntil)
	if contentFromErr != nil || contentUntilErr != nil || policyFromErr != nil || policyUntilErr != nil || from.Before(policyFrom) || until.After(policyUntil) {
		return fault.Conflict("依赖政策的有效期不能覆盖该制度")
	}
	return nil
}

// ValidateExperienceCandidateReference validates the exact candidate identity
// fields required before the caller loads a source-owned candidate revision.
func ValidateExperienceCandidateReference(content GovernedKnowledgeContent) error {
	if !validResourceID(content.CandidateID) || !ValidHash(content.CandidateVersionID) || !ValidHash(content.CandidateSHA256) || content.SourceCaseID == "" {
		return fault.Invalid("经验候选的编号不完整")
	}
	return nil
}

type CommandPlan struct {
	NextStatus                     string
	PointerAction                  bool
	RequiresRulePackBinding        bool
	RequiresExperienceCandidate    bool
	RequiresPolicyDependencies     bool
	ApprovedDigest                 string
	ApprovalActorID                string
	ApprovalReason                 string
	CrossCaseReviewed              bool
	CheckCrossCaseReviewAfterFacts bool
}

// ValidateCommandInput validates command syntax before the caller loads a row.
func ValidateCommandInput(input GovernedKnowledgeCommandInput) error {
	if !validResourceID(input.KnowledgeID) || !ValidHash(input.VersionID) || !ValidHash(input.ExpectedContentSHA256) ||
		input.ExpectedRevision < 1 || input.ExpectedPointerRevision < 0 || !ValidText(input.Reason, 2000) {
		return fault.Invalid("操作编号、版本或依据无效")
	}
	switch input.Action {
	case "submit", "validate", "approve", "publish", "withdraw", "rollback":
		return nil
	default:
		return fault.Invalid("不支持的制度治理动作")
	}
}

// PlanCommand performs Knowledge-owned lifecycle decisions that depend only on
// the current immutable version and command. External facts are validated by the
// caller between PlanCommand and CompleteCommand, preserving their error order.
func PlanCommand(current GovernedKnowledgeVersion, input GovernedKnowledgeCommandInput) (CommandPlan, error) {
	if current.Revision != input.ExpectedRevision || current.Ref.ContentSHA256 != input.ExpectedContentSHA256 {
		return CommandPlan{}, fault.Conflict("制度修订或正文摘要已改变")
	}
	plan := CommandPlan{
		NextStatus: current.Status, CrossCaseReviewed: current.CrossCaseReviewed,
		ApprovedDigest: current.ApprovedDigest, ApprovalActorID: current.ApprovalActorID, ApprovalReason: current.ApprovalReason,
	}
	switch input.Action {
	case "submit":
		if current.Status != "draft" {
			return CommandPlan{}, fault.Conflict("只有草稿可以提交")
		}
		plan.NextStatus = "submitted"
	case "validate":
		if current.Status != "submitted" {
			return CommandPlan{}, fault.Conflict("只有已提交版本可以校验")
		}
		plan.NextStatus = "validated"
		plan.RequiresRulePackBinding = true
	case "approve":
		if current.Status != "validated" {
			return CommandPlan{}, fault.Conflict("只有已校验版本可以批准")
		}
		plan.NextStatus = "approved"
		plan.RequiresRulePackBinding = true
		plan.RequiresExperienceCandidate = true
		plan.RequiresPolicyDependencies = true
		plan.CheckCrossCaseReviewAfterFacts = true
	case "publish":
		if current.Status != "approved" {
			return CommandPlan{}, fault.Conflict("只有已批准版本可以发布")
		}
		plan.PointerAction, plan.NextStatus = true, "published"
	case "withdraw":
		if current.Status != "published" || !current.Active {
			return CommandPlan{}, fault.Conflict("只能撤回当前生效版本")
		}
		plan.PointerAction, plan.NextStatus = true, "withdrawn"
	case "rollback":
		if current.Status != "published" && current.Status != "withdrawn" {
			return CommandPlan{}, fault.Conflict("只能回滚到曾经发布的版本")
		}
		if current.Active {
			return CommandPlan{}, fault.Conflict("该版本已经生效")
		}
		plan.PointerAction, plan.NextStatus = true, "published"
	default:
		return CommandPlan{}, fault.Invalid("不支持的制度治理动作")
	}
	if plan.PointerAction {
		if input.ExpectedPointerRevision != current.PointerRevision {
			return CommandPlan{}, fault.Conflict("制度生效指针已改变")
		}
		if input.Action != "withdraw" {
			if plan.ApprovedDigest != current.Ref.ContentSHA256 || plan.ApprovalActorID == "" || plan.ApprovalReason == "" {
				return CommandPlan{}, fault.Conflict("制度批准与正文摘要不一致")
			}
			plan.RequiresRulePackBinding = true
			plan.RequiresExperienceCandidate = true
			plan.RequiresPolicyDependencies = true
			plan.CheckCrossCaseReviewAfterFacts = true
		}
	}
	return plan, nil
}

// CompleteCommand records approval facts after external dependency checks have
// succeeded. It does not read or authorize source/candidate records.
func CompleteCommand(current GovernedKnowledgeVersion, input GovernedKnowledgeCommandInput, actorID string, plan CommandPlan) (CommandPlan, error) {
	if !plan.CheckCrossCaseReviewAfterFacts {
		return plan, nil
	}
	crossCaseExperience := current.Content.Kind == "experience" && current.Content.Scope.CaseID != current.Content.SourceCaseID
	if input.Action == "approve" {
		if crossCaseExperience && !input.CrossCaseReviewed {
			return CommandPlan{}, fault.Invalid("跨案经验必须明确审阅并去除来源案件敏感内容")
		}
		plan.CrossCaseReviewed = input.CrossCaseReviewed
		plan.ApprovedDigest = current.Ref.ContentSHA256
		plan.ApprovalActorID = actorID
		plan.ApprovalReason = input.Reason
		return plan, nil
	}
	if crossCaseExperience && !plan.CrossCaseReviewed {
		return CommandPlan{}, fault.Conflict("跨案经验缺少内容审阅")
	}
	return plan, nil
}

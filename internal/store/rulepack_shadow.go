package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/engine"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
	"github.com/jackc/pgx/v5"
)

const shadowSampleLimit = 20
const shadowRecentScanLimit = 250

type RuleShadowSampleRef struct {
	CaseID    string `json:"case_id"`
	RunID     string `json:"run_id"`
	AttemptID string `json:"attempt_id"`
}

type RuleShadowSampleOption struct {
	RuleShadowSampleRef
	FinishedAt string `json:"finished_at"`
	State      string `json:"state"`
}

type RuleShadowInput struct {
	PackID              string                `json:"-"`
	Version             string                `json:"-"`
	ExpectedRevision    int64                 `json:"expected_revision"`
	ExpectedContentHash string                `json:"expected_content_hash"`
	Samples             []RuleShadowSampleRef `json:"samples"`
}

type RuleShadowPackIdentity struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Revision       int64  `json:"revision"`
	ContentSHA256  string `json:"content_sha256"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
}

type RuleShadowChange struct {
	RuleID            string `json:"rule_id"`
	Kind              string `json:"kind"`
	BaseRelation      string `json:"base_relation"`
	CandidateRelation string `json:"candidate_relation"`
	BaseReason        string `json:"base_reason"`
	CandidateReason   string `json:"candidate_reason"`
	Detail            string `json:"detail"`
}

type RuleShadowSampleResult struct {
	RuleShadowSampleRef
	CaseAuthorized         bool               `json:"case_authorized"`
	Status                 string             `json:"status"`
	ErrorCode              string             `json:"error_code"`
	Message                string             `json:"message"`
	InputSHA256            string             `json:"input_sha256"`
	OutputSHA256           string             `json:"output_sha256"`
	KnowledgeSHA256        string             `json:"knowledge_sha256"`
	RuleRequestSHA256      string             `json:"rule_request_sha256"`
	ExtractorRef           string             `json:"extractor_ref"`
	BasePackID             string             `json:"base_pack_id"`
	BasePackVersion        string             `json:"base_pack_version"`
	BasePackSHA256         string             `json:"base_pack_sha256"`
	BasePackSnapshotSHA256 string             `json:"base_pack_snapshot_sha256"`
	Changes                []RuleShadowChange `json:"changes"`
}

type RuleShadowReport struct {
	SchemaVersion string                   `json:"schema_version"`
	ID            string                   `json:"id"`
	CreatedAt     string                   `json:"created_at"`
	CreatedBy     string                   `json:"created_by"`
	Candidate     RuleShadowPackIdentity   `json:"candidate"`
	Samples       []RuleShadowSampleResult `json:"samples"`
	Completed     int                      `json:"completed"`
	Partial       int                      `json:"partial"`
	Failed        int                      `json:"failed"`
}

type RuleShadowEnvelope struct {
	Report       RuleShadowReport `json:"report"`
	ReportSHA256 string           `json:"report_sha256"`
}

func (s *Store) shadowTransaction(ctx context.Context, admin accounts.User, write bool) (pgx.Tx, error) {
	if ctx == nil {
		return nil, fault.Invalid("请求上下文无效")
	}
	if write && s.ReadOnly() {
		return nil, fault.New(503, "read_only", "只读模式下不能保存影子报告")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err == nil {
		err = setLocalScope(ctx, tx, governanceScope(admin))
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func shadowCandidate(ctx context.Context, tx pgx.Tx, admin accounts.User, packID, version string) (governanceRow, *rulepack.Pack, error) {
	row, err := loadGovernanceRow(ctx, tx, accounts.Tenant(admin), packID, version)
	if errors.Is(err, errGovernanceVersionMissing) {
		return governanceRow{}, nil, fault.NotFound("没有找到对应规则包版本")
	}
	if err != nil {
		return governanceRow{}, nil, err
	}
	pack, err := rulepack.Parse(row.Version.Manifest)
	if err != nil || pack.Manifest().ID != packID || pack.Manifest().Version != version || pack.Manifest().TenantID != accounts.Tenant(admin) || pack.Hash() != row.Version.ContentHash || sha256Bytes(row.Version.Manifest) != row.Version.SnapshotDigest {
		return governanceRow{}, nil, fault.Conflict("候选规则包的内容或摘要不一致")
	}
	// These transitions create only an in-memory executable copy. They do not
	// attest, approve or publish the persisted candidate.
	if pack.Manifest().Lifecycle.State == rulepack.StateDraft {
		pack, err = pack.Validate()
	}
	if err == nil && pack.Manifest().Lifecycle.State == rulepack.StateValidated {
		pack, err = pack.Approve("rulepack-shadow-only", pack.Hash())
	}
	if err == nil && pack.Manifest().Lifecycle.State == rulepack.StateApproved {
		pack, err = pack.Publish()
	}
	if err != nil || pack.Manifest().Lifecycle.State != rulepack.StatePublished || pack.Hash() != row.Version.ContentHash {
		return governanceRow{}, nil, fault.Conflict("候选规则包无法创建影子执行副本")
	}
	return row, pack, nil
}

func shadowScopeMatches(rules []rulepack.Rule, item Case) bool {
	for _, rule := range rules {
		if rule.TenantID == item.TenantID && rule.BusinessScope.BusinessLine == item.BusinessLine && rule.BusinessScope.Scenario == item.Scenario && rule.BusinessScope.ContractType == item.ContractType {
			return true
		}
	}
	return false
}

// RuleShadowSamples returns bounded recent identities only after current case
// read and review authorization. It never returns case text or frozen facts.
func (s *Store) RuleShadowSamples(ctx context.Context, user accounts.User, packID, version string) ([]RuleShadowSampleOption, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return nil, err
	}
	tx, err := s.shadowTransaction(ctx, admin, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	_, pack, err := shadowCandidate(ctx, tx, admin, packID, version)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT r.case_id,r.id,a.id,COALESCE(a.finished_at,''),a.state FROM review_attempts a JOIN review_runs r ON r.tenant_id=a.tenant_id AND r.org_id=a.org_id AND r.id=a.run_id WHERE r.tenant_id=$1 AND a.state IN ('completed','partial') AND a.finished_at IS NOT NULL ORDER BY a.finished_at DESC,a.id DESC LIMIT $2`, accounts.Tenant(admin), shadowRecentScanLimit)
	if err != nil {
		return nil, err
	}
	options := make([]RuleShadowSampleOption, 0, shadowRecentScanLimit)
	for rows.Next() {
		var option RuleShadowSampleOption
		if err := rows.Scan(&option.CaseID, &option.RunID, &option.AttemptID, &option.FinishedAt, &option.State); err != nil {
			rows.Close()
			return nil, err
		}
		options = append(options, option)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rules := pack.Manifest().Rules
	visible := make([]RuleShadowSampleOption, 0, 50)
	for _, option := range options {
		item, err := loadKnowledgeCase(ctx, tx, admin, option.CaseID)
		if err != nil || !authorizeCase(item, admin, access.Review) || !shadowScopeMatches(rules, item) {
			continue
		}
		var knowledge []byte
		if err := tx.QueryRow(ctx, `SELECT governed_knowledge FROM review_run_snapshots WHERE tenant_id=$1 AND org_id=$2 AND run_id=$3`, item.TenantID, item.OwnerOrgID, option.RunID).Scan(&knowledge); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if len(knowledge) > 0 {
			frozen, decodeErr := decodeGovernedKnowledge(knowledge)
			if decodeErr == nil && frozen != nil && shadowKnowledgeAuthorization(ctx, tx, admin, frozen) != nil {
				continue
			}
		}
		visible = append(visible, option)
		if len(visible) == 50 {
			break
		}
	}
	if _, err := trustedAdmin(admin); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return visible, nil
}

func validateShadowSamples(samples []RuleShadowSampleRef) error {
	if len(samples) < 1 || len(samples) > shadowSampleLimit {
		return fault.Invalid("请选择 1 至 20 条审查执行记录")
	}
	seen := make(map[RuleShadowSampleRef]bool, len(samples))
	for _, sample := range samples {
		if !validResourceID(sample.CaseID) || !validResourceID(sample.RunID) || !validResourceID(sample.AttemptID) || seen[sample] {
			return fault.Invalid("样本编号无效或重复")
		}
		seen[sample] = true
	}
	return nil
}

// shadowKnowledgeAuthorization checks the existing frozen selections for the
// current reader. It never resolves a new policy, source or applicability fact.
func shadowKnowledgeAuthorization(ctx context.Context, tx pgx.Tx, admin accounts.User, snapshot *ReviewKnowledgeSnapshot) error {
	if snapshot == nil {
		return errors.New("knowledge snapshot unavailable")
	}
	sealed, err := sealKnowledgeSnapshot(*snapshot)
	if err != nil || snapshot.Encoding != "review-knowledge-snapshot/v1" || sealed.SnapshotSHA256 != snapshot.SnapshotSHA256 {
		return errors.New("knowledge snapshot hash mismatch")
	}
	if err := validateFrozenLegalAuthorityCapturesTx(ctx, tx, accounts.Tenant(admin), snapshot.LegalAuthorities); err != nil {
		return err
	}
	checkSelection := func(selected GovernedKnowledgeSelection, required bool) error {
		if selected.Required != required {
			return errors.New("frozen knowledge selection has an invalid role")
		}
		version, _, _, active, loadErr := loadGovernedKnowledge(ctx, tx, accounts.Tenant(admin), selected.Ref.KnowledgeID, selected.Ref.VersionID)
		if loadErr != nil || version.Ref != selected.Ref || (required && (!active || !reflect.DeepEqual(knowledgeSelection(version), selected))) {
			return errors.New("frozen knowledge unavailable or withdrawn")
		}
		return authorizeKnowledgeAdoption(ctx, tx, admin, version)
	}
	for _, selected := range snapshot.RequiredPolicies {
		if err := checkSelection(selected, true); err != nil {
			return err
		}
	}
	for _, selected := range snapshot.OptionalCandidates {
		if err := checkSelection(selected, false); err != nil {
			return err
		}
	}
	return nil
}

func shadowFailure(result *RuleShadowSampleResult, code, message string) {
	result.Status, result.ErrorCode, result.Message = "failed", code, message
}

func (s *Store) replayShadowSample(ctx context.Context, tx pgx.Tx, admin accounts.User, candidate *rulepack.Pack, ref RuleShadowSampleRef) RuleShadowSampleResult {
	result := RuleShadowSampleResult{RuleShadowSampleRef: ref, Changes: []RuleShadowChange{}}
	item, err := loadKnowledgeCase(ctx, tx, admin, ref.CaseID)
	if err != nil || !authorizeCase(item, admin, access.Review) {
		shadowFailure(&result, "not_found_or_not_authorized", "案件不存在或当前身份没有审查权限")
		return result
	}
	result.CaseAuthorized = true
	st, attempt, input, err := s.evidenceAttempt(ctx, tx, item, ref.RunID, ref.AttemptID)
	if err != nil {
		shadowFailure(&result, "frozen_review_unavailable", "冻结审查输入、输出或原文未通过完整性校验")
		return result
	}
	snapshot := st.snapshotOf(ref.RunID)
	result.InputSHA256, result.OutputSHA256 = input.InputHash, *attempt.OutputHash
	result.ExtractorRef = snapshot.ComparatorRef
	result.BasePackID, result.BasePackVersion = attempt.RulePackID, attempt.RulePackVersion
	result.BasePackSHA256, result.BasePackSnapshotSHA256 = attempt.RulePackHash, attempt.RulePackSnapshotDigest
	if snapshot.GovernedKnowledge == nil {
		shadowFailure(&result, "knowledge_unavailable", "这次审查没有可验证的冻结知识快照")
		return result
	}
	if err := shadowKnowledgeAuthorization(ctx, tx, admin, snapshot.GovernedKnowledge); err != nil {
		shadowFailure(&result, "knowledge_not_authorized", "冻结知识或其来源不可用，或当前身份没有授权")
		return result
	}
	result.KnowledgeSHA256 = snapshot.GovernedKnowledge.SnapshotSHA256
	base, err := ParseFrozenGovernedRulePack(attempt, item)
	if err != nil || base == nil {
		shadowFailure(&result, "base_pack_unavailable", "原审查没有完整的治理规则包快照")
		return result
	}
	var raw []byte
	var requestHash, inputHash, packID, packVersion, packHash, packDigest, extractor string
	err = tx.QueryRow(ctx, `SELECT request_bytes,request_sha256,attempt_input_sha256,pack_id,pack_version,pack_content_sha256,pack_snapshot_sha256,extractor_ref FROM review_rulepack_inputs WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5`, item.TenantID, item.OwnerOrgID, item.ID, ref.RunID, ref.AttemptID).Scan(&raw, &requestHash, &inputHash, &packID, &packVersion, &packHash, &packDigest, &extractor)
	if errors.Is(err, pgx.ErrNoRows) {
		shadowFailure(&result, "facts_unavailable", "该历史执行没有保存完整的类型化规则输入，无法忠实重放")
		return result
	}
	if err != nil {
		shadowFailure(&result, "frozen_request_unavailable", "保存的规则输入不可读取")
		return result
	}
	result.RuleRequestSHA256 = requestHash
	var frozen frozenRuleRequest
	if sha256Bytes(raw) != requestHash || inputHash != input.InputHash || packID != attempt.RulePackID || packVersion != attempt.RulePackVersion || packHash != attempt.RulePackHash || packDigest != attempt.RulePackSnapshotDigest || extractor != snapshot.ComparatorRef || json.Unmarshal(raw, &frozen) != nil || frozen.Encoding != "rulepack-request/v1" {
		shadowFailure(&result, "frozen_request_invalid", "类型化规则输入的身份或摘要不一致")
		return result
	}
	canonical, err := json.Marshal(frozen)
	if err != nil || !bytes.Equal(canonical, raw) || frozen.Facts == nil || frozen.At.IsZero() {
		shadowFailure(&result, "frozen_request_invalid", "保存的规则输入不完整")
		return result
	}
	principal := bindLocalDemoCaseGrant(item, admin, accounts.Principal(admin))
	request := frozen.bind(rulepack.Request{Principal: principal, Resource: access.Resource{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, OwnerID: item.OwnerID}})
	sources, err := telecomSources(interpretedFromFrozen(input.Documents))
	gates, gateErr := rulepackAuthorityGates(snapshot.GovernedKnowledge)
	if err != nil || gateErr != nil || !reflect.DeepEqual(sources, request.Sources) || !reflect.DeepEqual(append([]rulepack.AuthorityGate{}, gates...), request.AuthorityGates) || rulepack.ValidateSources(request) != nil || request.BusinessLine != item.BusinessLine || request.Scenario != item.Scenario || request.ContractType != item.ContractType {
		shadowFailure(&result, "frozen_evidence_mismatch", "规则输入与冻结解析、知识或案件范围不一致")
		return result
	}
	baseline, err := executeFrozenShadow(ctx, snapshot, request, base)
	if err != nil {
		shadowFailure(&result, "baseline_replay_failed", "原规则包重放失败："+err.Error())
		return result
	}
	if baseline.PackID != attempt.RulePackID || baseline.PackVersion != attempt.RulePackVersion || baseline.PackHash != attempt.RulePackHash {
		shadowFailure(&result, "baseline_mismatch", "原规则包重放身份与冻结快照不一致")
		return result
	}
	if !shadowBaselineMatches(st.evaluationsForAttempt(ref.RunID, ref.AttemptID), baseline) {
		shadowFailure(&result, "baseline_mismatch", "原规则包重放结果与已保存审查结果不一致")
		return result
	}
	candidateRequest := request
	candidateManifest := candidate.Manifest()
	candidateRules := make(map[string]rulepack.Rule, len(candidateManifest.Rules))
	for _, rule := range candidateManifest.Rules {
		candidateRules[rule.RuleID] = rule
	}
	candidateRequest.Facts = make([]rulepack.Fact, 0, len(request.Facts))
	for _, fact := range request.Facts {
		if _, ok := candidateRules[fact.RuleID]; ok {
			candidateRequest.Facts = append(candidateRequest.Facts, fact)
		}
	}
	replayed, err := executeFrozenShadow(ctx, snapshot, candidateRequest, candidate)
	if err != nil {
		shadowFailure(&result, "candidate_replay_failed", "候选规则包重放失败："+err.Error())
		return result
	}
	if replayed.PackID != candidateManifest.ID || replayed.PackVersion != candidateManifest.Version || replayed.PackHash != candidate.Hash() {
		shadowFailure(&result, "candidate_replay_failed", "候选规则包执行身份不一致")
		return result
	}
	result.Changes, result.Status = compareShadowResults(base.Manifest(), candidateManifest, baseline, replayed)
	if result.Status == "partial" {
		result.ErrorCode = "facts_or_authority_unavailable"
		result.Message = "部分规则缺少对应的冻结事实或知识依据，不能视为完整对比"
	} else {
		result.Message = "已基于保存的规则输入完成对比；结果仅供人工审批参考"
	}
	return result
}

func executeFrozenShadow(ctx context.Context, snapshot RunSnapshot, request rulepack.Request, pack *rulepack.Pack) (rulepack.Report, error) {
	switch snapshot.ComparatorRef {
	case engine.ComparatorRef + "+" + telecom.CompilerRef:
		if legacyFrozenAuthoritySemantics(snapshot, pack) {
			return telecom.ExecuteLegacyFrozen(ctx, request, pack)
		}
		return telecom.ExecuteFrozen(ctx, request, pack)
	case engine.ComparatorRef + "+" + telecom.DeclaredCompilerRef:
		return telecom.ExecuteDeclared(ctx, request, pack)
	default:
		return rulepack.Report{}, errors.New("冻结的提取器版本在当前运行环境中不可用")
	}
}

func shadowBaselineMatches(evaluations []ConstraintEval, baseline rulepack.Report) bool {
	if len(evaluations) != len(baseline.Results) {
		return false
	}
	byID := make(map[string]ConstraintEval, len(evaluations))
	for _, evaluation := range evaluations {
		if evaluation.RuleID == "" {
			return false
		}
		if _, duplicate := byID[evaluation.RuleID]; duplicate {
			return false
		}
		byID[evaluation.RuleID] = evaluation
	}
	for _, value := range baseline.Results {
		evaluation, ok := byID[value.RuleID]
		if !ok || evaluation.RuleVersion != value.RuleVersion || evaluation.CapabilityID != value.CapabilityID || evaluation.Domain != value.Domain || evaluation.Mandatory != value.Mandatory || evaluation.Relation != string(value.Relation) || evaluation.ReasonCode != value.ReasonCode || !reflect.DeepEqual(append([]string{}, evaluation.FactIDs...), append([]string{}, value.FactIDs...)) {
			return false
		}
		expected := make(map[ExperienceEvidenceRef]bool, len(value.Citations))
		for _, citation := range value.Citations {
			expected[ExperienceEvidenceRef{DocumentID: citation.SourceID, ParsedDocumentID: citation.SourceVersion, NormalizedDigest: citation.SourceDigest, Quote: citation.Quote, Start: citation.Start, End: citation.End}] = true
		}
		actual := make(map[ExperienceEvidenceRef]bool, len(evaluation.Evidence))
		for _, evidence := range evaluation.Evidence {
			actual[evidence.Ref] = true
		}
		if !reflect.DeepEqual(expected, actual) {
			return false
		}
	}
	return true
}

func shadowRuleFactsComparable(base, candidate rulepack.Rule) bool {
	return base.Comparator == candidate.Comparator && base.Domain == candidate.Domain && base.CapabilityID == candidate.CapabilityID && base.BusinessScope == candidate.BusinessScope && reflect.DeepEqual(base.RequiredFact, candidate.RequiredFact) && reflect.DeepEqual(base.AuthoritySources, candidate.AuthoritySources) && reflect.DeepEqual(base.LegalAuthorities, candidate.LegalAuthorities)
}

func compareShadowResults(base, candidate rulepack.Manifest, before, after rulepack.Report) ([]RuleShadowChange, string) {
	oldRules, newRules := map[string]rulepack.Rule{}, map[string]rulepack.Rule{}
	oldResults, newResults := map[string]rulepack.Result{}, map[string]rulepack.Result{}
	for _, rule := range base.Rules {
		oldRules[rule.RuleID] = rule
	}
	for _, rule := range candidate.Rules {
		newRules[rule.RuleID] = rule
	}
	for _, result := range before.Results {
		oldResults[result.RuleID] = result
	}
	for _, result := range after.Results {
		newResults[result.RuleID] = result
	}
	keys := make([]string, 0, len(oldResults)+len(newResults))
	seen := map[string]bool{}
	for key := range oldResults {
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range newResults {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	changes := make([]RuleShadowChange, 0)
	status := "completed"
	for _, id := range keys {
		old, hadOld := oldResults[id]
		next, hadNew := newResults[id]
		change := RuleShadowChange{RuleID: id, BaseRelation: string(old.Relation), CandidateRelation: string(next.Relation), BaseReason: old.ReasonCode, CandidateReason: next.ReasonCode}
		switch {
		case !hadOld:
			change.Kind, change.Detail, status = "unavailable", "新增规则没有该次执行的冻结事实", "partial"
		case !hadNew:
			change.Kind, change.Detail = "removed", "候选规则包没有此规则"
		case !shadowRuleFactsComparable(oldRules[id], newRules[id]):
			change.Kind, change.Detail, status = "unavailable", "规则的事实或依据定义已改变，原冻结输入不能证明可比", "partial"
		case old.Relation != next.Relation || old.ReasonCode != next.ReasonCode || old.RuleVersion != next.RuleVersion || old.Mandatory != next.Mandatory || old.LegalAuthorityOutcome != next.LegalAuthorityOutcome || old.LegalAuthorityReason != next.LegalAuthorityReason || !reflect.DeepEqual(append([]string{}, old.FactIDs...), append([]string{}, next.FactIDs...)) || !reflect.DeepEqual(append([]rulepack.Citation{}, old.Citations...), append([]rulepack.Citation{}, next.Citations...)):
			change.Kind = "changed"
			if old.Relation == next.Relation && old.ReasonCode == next.ReasonCode {
				change.Detail = "规则版本、强制属性、适用依据或引用身份发生变化"
			}
		default:
			continue
		}
		changes = append(changes, change)
	}
	return changes, status
}

// CreateRuleShadowReport appends a hash-bound report; it never adopts results,
// records a human decision, approves content or moves an active pointer.
func (s *Store) CreateRuleShadowReport(ctx context.Context, user accounts.User, input RuleShadowInput) (RuleShadowEnvelope, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	if err := validateShadowSamples(input.Samples); err != nil {
		return RuleShadowEnvelope{}, err
	}
	if input.ExpectedRevision < 1 || !isSHA256Hex(input.ExpectedContentHash) {
		return RuleShadowEnvelope{}, fault.Invalid("需要当前规则包修订和内容摘要")
	}
	tx, err := s.shadowTransaction(ctx, admin, true)
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockGovernancePack(ctx, tx, accounts.Tenant(admin), input.PackID); err != nil {
		return RuleShadowEnvelope{}, err
	}
	row, candidate, err := shadowCandidate(ctx, tx, admin, input.PackID, input.Version)
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	if row.Version.Revision != input.ExpectedRevision || row.Version.ContentHash != input.ExpectedContentHash {
		return RuleShadowEnvelope{}, fault.Conflict("候选规则包已变化，请刷新后重新选择样本")
	}
	caseIDs := make([]string, 0, len(input.Samples))
	for _, ref := range input.Samples {
		caseIDs = append(caseIDs, ref.CaseID)
	}
	if err := scopedCaseLocks(ctx, tx, caseIDs); err != nil {
		return RuleShadowEnvelope{}, err
	}
	for _, ref := range input.Samples {
		item, loadErr := loadKnowledgeCase(ctx, tx, admin, ref.CaseID)
		if loadErr != nil || !authorizeCase(item, admin, access.Review) {
			return RuleShadowEnvelope{}, fault.NotFound("没有找到对应记录")
		}
	}
	id, err := newID("shadow_")
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	report := RuleShadowReport{SchemaVersion: "rulepack-shadow-report/v1", ID: id, CreatedAt: stamp(), CreatedBy: admin.ID,
		Candidate: RuleShadowPackIdentity{ID: input.PackID, Version: input.Version, Revision: row.Version.Revision, ContentSHA256: row.Version.ContentHash, SnapshotSHA256: row.Version.SnapshotDigest}, Samples: make([]RuleShadowSampleResult, 0, len(input.Samples))}
	for _, ref := range input.Samples {
		if err := ctx.Err(); err != nil {
			return RuleShadowEnvelope{}, err
		}
		result := s.replayShadowSample(ctx, tx, admin, candidate, ref)
		if !result.CaseAuthorized {
			return RuleShadowEnvelope{}, fault.NotFound("没有找到对应记录")
		}
		switch result.Status {
		case "completed":
			report.Completed++
		case "partial":
			report.Partial++
		default:
			report.Failed++
		}
		report.Samples = append(report.Samples, result)
	}
	currentAdmin, err := trustedAdmin(admin)
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	if err := authorizeShadowReport(ctx, tx, currentAdmin, report); err != nil {
		return RuleShadowEnvelope{}, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	digest := sha256Bytes(raw)
	_, err = tx.Exec(ctx, `INSERT INTO rule_pack_shadow_reports (tenant_id,org_id,report_id,pack_id,pack_version,pack_revision,pack_content_sha256,pack_snapshot_sha256,pack_snapshot,report_sha256,report_bytes,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, accounts.Tenant(admin), admin.OrgID, id, input.PackID, input.Version, row.Version.Revision, row.Version.ContentHash, row.Version.SnapshotDigest, row.Version.Manifest, digest, raw, admin.ID)
	if err != nil {
		return RuleShadowEnvelope{}, err
	}
	if _, err := trustedAdmin(admin); err != nil {
		return RuleShadowEnvelope{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleShadowEnvelope{}, err
	}
	return RuleShadowEnvelope{Report: report, ReportSHA256: digest}, nil
}

func readShadowReportRow(raw, packBytes []byte, tenant, reportID, createdBy, reportHash, packHash, packDigest, packID, version string, revision int64) (RuleShadowEnvelope, error) {
	var report RuleShadowReport
	pack, err := rulepack.Parse(packBytes)
	if err != nil {
		return RuleShadowEnvelope{}, errors.New("shadow report pack is invalid")
	}
	manifest := pack.Manifest()
	if sha256Bytes(packBytes) != packDigest || pack.Hash() != packHash || manifest.TenantID != tenant || manifest.ID != packID || manifest.Version != version || sha256Bytes(raw) != reportHash || json.Unmarshal(raw, &report) != nil || report.SchemaVersion != "rulepack-shadow-report/v1" || report.ID != reportID || report.CreatedBy != createdBy || report.Candidate.ID != packID || report.Candidate.Version != version || report.Candidate.Revision != revision || report.Candidate.ContentSHA256 != packHash || report.Candidate.SnapshotSHA256 != packDigest || len(report.Samples) < 1 || len(report.Samples) > shadowSampleLimit {
		return RuleShadowEnvelope{}, errors.New("shadow report integrity mismatch")
	}
	completed, partial, failed := 0, 0, 0
	for _, sample := range report.Samples {
		if !sample.CaseAuthorized || !validResourceID(sample.CaseID) || !validResourceID(sample.RunID) || !validResourceID(sample.AttemptID) {
			return RuleShadowEnvelope{}, errors.New("shadow report sample identity mismatch")
		}
		switch sample.Status {
		case "completed":
			completed++
		case "partial":
			partial++
		case "failed":
			failed++
		default:
			return RuleShadowEnvelope{}, errors.New("shadow report status mismatch")
		}
	}
	if completed != report.Completed || partial != report.Partial || failed != report.Failed {
		return RuleShadowEnvelope{}, errors.New("shadow report counts mismatch")
	}
	canonical, err := json.Marshal(report)
	if err != nil || !bytes.Equal(canonical, raw) {
		return RuleShadowEnvelope{}, errors.New("shadow report encoding mismatch")
	}
	return RuleShadowEnvelope{Report: report, ReportSHA256: reportHash}, nil
}

func authorizeShadowReport(ctx context.Context, tx pgx.Tx, admin accounts.User, report RuleShadowReport) error {
	for _, sample := range report.Samples {
		if !sample.CaseAuthorized {
			return fault.NotFound("没有找到对应影子报告")
		}
		item, err := loadKnowledgeCase(ctx, tx, admin, sample.CaseID)
		if err != nil || !authorizeCase(item, admin, access.Review) {
			return fault.NotFound("没有找到对应影子报告")
		}
		if sample.KnowledgeSHA256 == "" {
			continue
		}
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT s.governed_knowledge FROM review_run_snapshots s JOIN review_runs r ON r.tenant_id=s.tenant_id AND r.org_id=s.org_id AND r.id=s.run_id WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4`, item.TenantID, item.OwnerOrgID, item.ID, sample.RunID).Scan(&raw)
		if err != nil {
			return fault.NotFound("没有找到对应影子报告")
		}
		frozen, err := decodeGovernedKnowledge(raw)
		if err != nil || frozen == nil || frozen.SnapshotSHA256 != sample.KnowledgeSHA256 || shadowKnowledgeAuthorization(ctx, tx, admin, frozen) != nil {
			return fault.NotFound("没有找到对应影子报告")
		}
	}
	return nil
}

func (s *Store) RuleShadowReports(ctx context.Context, user accounts.User, packID, version, reportID string) ([]RuleShadowEnvelope, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return nil, err
	}
	if reportID != "" && (strings.TrimSpace(reportID) != reportID || !validResourceID(reportID)) {
		return nil, fault.NotFound("没有找到对应影子报告")
	}
	tx, err := s.shadowTransaction(ctx, admin, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT report_bytes,pack_snapshot,report_id,created_by,report_sha256,pack_content_sha256,pack_snapshot_sha256,pack_revision FROM rule_pack_shadow_reports WHERE tenant_id=$1 AND pack_id=$2 AND pack_version=$3 AND ($4='' OR report_id=$4) ORDER BY created_at DESC,report_id DESC LIMIT 50`, accounts.Tenant(admin), packID, version, reportID)
	if err != nil {
		return nil, err
	}
	type rowData struct {
		raw, pack                             []byte
		id, actor, hash, packHash, packDigest string
		revision                              int64
	}
	var saved []rowData
	for rows.Next() {
		var row rowData
		if err := rows.Scan(&row.raw, &row.pack, &row.id, &row.actor, &row.hash, &row.packHash, &row.packDigest, &row.revision); err != nil {
			rows.Close()
			return nil, err
		}
		saved = append(saved, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]RuleShadowEnvelope, 0, len(saved))
	for _, row := range saved {
		envelope, err := readShadowReportRow(row.raw, row.pack, accounts.Tenant(admin), row.id, row.actor, row.hash, row.packHash, row.packDigest, packID, version, row.revision)
		if err != nil {
			return nil, fault.Conflict("影子报告摘要校验失败")
		}
		if err := authorizeShadowReport(ctx, tx, admin, envelope.Report); err != nil {
			if reportID != "" {
				return nil, err
			}
			continue
		}
		out = append(out, envelope)
	}
	if reportID != "" && len(out) == 0 {
		return nil, fault.NotFound("没有找到对应影子报告")
	}
	if _, err := trustedAdmin(admin); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

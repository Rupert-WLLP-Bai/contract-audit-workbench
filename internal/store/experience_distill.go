package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ExperienceDistillRequest identifies a feedback and, optionally, the exact
// existing candidate revision to extend. Tenant and applicant are server-bound.
type ExperienceDistillRequest struct {
	CaseID         string `json:"case_id"`
	FeedbackID     string `json:"feedback_id"`
	CandidateID    string `json:"candidate_id,omitempty"`
	BaseVersionID  string `json:"base_version_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ExperienceDistillReceipt struct {
	JobID string `json:"job_id"`
}

// ExperienceResultDelivery contains only the immutable result identity. Core
// resolves the applicant from the stored input and checks current case access.
type ExperienceResultDelivery struct {
	TenantID     string `json:"tenant_id"`
	JobID        string `json:"job_id"`
	Generation   int64  `json:"generation"`
	InputSHA256  string `json:"input_sha256"`
	ResultSHA256 string `json:"result_sha256"`
}

type ExperienceResultAcceptor interface {
	AcceptExperienceResult(context.Context, ExperienceResultDelivery) error
}

func (s *Store) UseExperienceResultAcceptor(acceptor ExperienceResultAcceptor) {
	s.mu.Lock()
	s.experienceAcceptor = acceptor
	s.mu.Unlock()
}

// AcceptExperienceResult is the internal Core operation. Tenant scopes the
// lookup; the worker cannot supply the applicant or candidate content.
func (s *Store) AcceptExperienceResult(ctx context.Context, delivery ExperienceResultDelivery) error {
	if ctx == nil || !experienceIdentity(delivery.TenantID) || !experienceIdentity(delivery.JobID) || delivery.Generation < 1 {
		return fault.Invalid("经验提炼结果的编号无效")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "experience-delivery", TenantID: delivery.TenantID, Role: "api"}); err != nil {
		return err
	}
	var actorID, caseID, feedbackID string
	err = tx.QueryRow(ctx, `SELECT actor_id,case_id,feedback_id FROM experience_distill_inputs
		WHERE tenant_id=$1 AND job_id=$2 AND input_sha256=$3`, delivery.TenantID, delivery.JobID, delivery.InputSHA256).
		Scan(&actorID, &caseID, &feedbackID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound("没有找到对应提炼任务")
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_, err = s.AcceptExperienceCandidate(ctx, accounts.User{ID: actorID, TenantID: delivery.TenantID}, caseID, feedbackID,
		delivery.JobID, delivery.Generation, delivery.InputSHA256, delivery.ResultSHA256)
	return err
}

// experiencePolicyContext reads only the frozen source review. Current policy
// pointers are intentionally checked later by governance, under its lock.
func experiencePolicyContext(ctx context.Context, tx pgx.Tx, item Case, feedback ExperienceFeedback) (ExperienceDistillPolicyContext, error) {
	var snapshot RunSnapshot
	var attempt Attempt
	var sourceInput, documentsJSON, documentIDsJSON, parsedIDsJSON string
	var knowledge, plannerPayload, cloudPolicy []byte
	snapshot.RunID, attempt.RunID = feedback.RunID, feedback.RunID
	err := tx.QueryRow(ctx, `SELECT a.input_hash,a.rule_pack_id,a.rule_pack_version,a.rule_pack_hash,
		a.rule_pack_snapshot_digest,a.rule_pack_compiler_ref,a.rule_pack_pointer_revision,a.rule_pack_snapshot,
		s.rule_pack_id,s.rule_pack_version,s.rule_pack_hash,s.rule_pack_digest,s.rule_pack_compiler_ref,s.rule_pack_snapshot,s.governed_knowledge,
		s.document_ids,s.parsed_document_ids,s.rule_set_hash,s.playbook_hash,s.model_ref,s.comparator_ref,s.model_configuration_id,
		s.parse_selection_hash,s.parser_configuration_id,s.planner_configuration_id,s.planner_configuration,s.cloud_policy_snapshot,s.cloud_policy_digest,i.documents
		FROM review_runs r
		JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_attempt_inputs i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.run_id=a.run_id AND i.attempt_id=a.id AND i.input_hash=a.input_hash
		JOIN review_run_snapshots s ON s.tenant_id=r.tenant_id AND s.org_id=r.org_id AND s.run_id=r.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5 AND a.history_kind='frozen'`,
		item.TenantID, item.OwnerOrgID, item.ID, feedback.RunID, feedback.AttemptID).
		Scan(&sourceInput, &attempt.RulePackID, &attempt.RulePackVersion, &attempt.RulePackHash,
			&attempt.RulePackSnapshotDigest, &attempt.RulePackCompilerRef, &attempt.RulePackPointerRevision, &attempt.RulePackSnapshot,
			&snapshot.RulePackID, &snapshot.RulePackVersion, &snapshot.RulePackHash, &snapshot.RulePackDigest,
			&snapshot.RulePackCompilerRef, &snapshot.RulePackSnapshot, &knowledge, &documentIDsJSON, &parsedIDsJSON,
			&snapshot.RuleSetHash, &snapshot.PlaybookHash, &snapshot.ModelRef, &snapshot.ComparatorRef, &snapshot.ModelConfigurationID,
			&snapshot.ParseSelectionHash, &snapshot.ParserConfigurationID, &snapshot.PlannerConfigurationID, &plannerPayload,
			&cloudPolicy, &snapshot.CloudPolicyDigest, &documentsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源缺少已保存的审查执行记录")
	}
	if err != nil {
		return ExperienceDistillPolicyContext{}, err
	}
	governed, err := decodeGovernedKnowledge(knowledge)
	if err != nil || governed == nil {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源缺少本次审查保存的有效制度记录")
	}
	snapshot.GovernedKnowledge = governed
	if !validKnowledgeHash(sourceInput) || governed.Scope.OrgID != item.OwnerOrgID || governed.Scope.CaseID != item.ID {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源的制度记录编号无效")
	}
	if _, err := copyGovernedKnowledge(snapshot); err != nil {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源的规则包与保存的制度记录不一致")
	}
	if err := validateGovernedAttemptSnapshot(attempt, snapshot); err != nil {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源的执行记录与保存的制度记录不一致")
	}
	var documents []FrozenDocument
	if json.Unmarshal([]byte(documentIDsJSON), &snapshot.DocumentIDs) != nil ||
		json.Unmarshal([]byte(parsedIDsJSON), &snapshot.ParsedDocumentIDs) != nil ||
		json.Unmarshal([]byte(documentsJSON), &documents) != nil {
		return ExperienceDistillPolicyContext{}, fault.Conflict("无法读取反馈来源审查当时保存的输入")
	}
	if len(plannerPayload) > 0 {
		snapshot.PlannerConfiguration = append(json.RawMessage(nil), plannerPayload...)
	}
	if len(cloudPolicy) > 0 {
		snapshot.CloudPolicySnapshot = append(json.RawMessage(nil), cloudPolicy...)
	}
	inputHash, err := frozenInputHash(snapshot, documents)
	if err != nil || inputHash != sourceInput || !frozenInputMatchesSnapshot(snapshot, documents) ||
		!sameDocumentSet(snapshot.DocumentIDs, frozenDocumentIDs(documents)) {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源审查当时保存的输入摘要不一致")
	}
	at, err := knowledgeTime(governed.BusinessAt)
	if err != nil || len(governed.RequiredPolicies) == 0 {
		return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源没有可用的必需政策")
	}
	out := ExperienceDistillPolicyContext{RunID: feedback.RunID, AttemptID: feedback.AttemptID,
		ReviewInputSHA256: sourceInput, KnowledgeSnapshotSHA256: governed.SnapshotSHA256,
		RequiredPolicies: make([]ExperienceDistillPolicy, 0, len(governed.RequiredPolicies))}
	seen := make(map[ExperiencePolicyRef]bool, len(governed.RequiredPolicies))
	for _, policy := range governed.RequiredPolicies {
		ref := ExperiencePolicyRef{KnowledgeID: policy.Ref.KnowledgeID, VersionID: policy.Ref.VersionID, ContentSHA256: policy.Ref.ContentSHA256}
		from, fromErr := knowledgeTime(policy.ValidFrom)
		until, untilErr := knowledgeTime(policy.ValidUntil)
		binding := policy.Binding
		pack := governed.RulePack
		if policy.Kind != "policy" || !policy.Required || !validResourceID(ref.KnowledgeID) ||
			!validKnowledgeHash(ref.VersionID) || !validKnowledgeHash(ref.ContentSHA256) || seen[ref] ||
			!validKnowledgeText(policy.Summary, 2000) || fromErr != nil || untilErr != nil ||
			!until.After(from) || at.Before(from) || !at.Before(until) ||
			binding.PackID != pack.PackID || binding.PackVersion != pack.Version ||
			binding.PackContentHash != pack.ContentHash || binding.PackSnapshotDigest != pack.SnapshotDigest || binding.CompilerRef != pack.CompilerRef {
			return ExperienceDistillPolicyContext{}, fault.Conflict("反馈来源保存的必需制度记录无效")
		}
		seen[ref] = true
		out.RequiredPolicies = append(out.RequiredPolicies, ExperienceDistillPolicy{
			Ref: ref, Summary: policy.Summary, ValidFrom: policy.ValidFrom, ValidUntil: policy.ValidUntil,
		})
	}
	return out, nil
}

func validateExperiencePolicyContext(ctx context.Context, tx pgx.Tx, item Case, feedback ExperienceFeedback, input ExperienceDistillInput) error {
	expected, err := experiencePolicyContext(ctx, tx, item, feedback)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(input.PolicyContext, expected) {
		return fault.Conflict("提炼任务的制度来源与当时保存的审查记录不一致")
	}
	return nil
}

// Incomplete candidates may omit references, but any reference supplied by the
// model must be a unique exact identity in the source attempt.
func validateExperiencePolicyRefs(content ExperienceCandidateContent, source ExperienceDistillPolicyContext) error {
	if len(content.PolicyRefs) > 32 {
		return fault.Conflict("经验候选政策依赖数量超过上限")
	}
	allowed := make(map[ExperiencePolicyRef]ExperienceDistillPolicy, len(source.RequiredPolicies))
	for _, policy := range source.RequiredPolicies {
		allowed[policy.Ref] = policy
	}
	from, fromErr := knowledgeTime(content.ValidFrom)
	until, untilErr := knowledgeTime(content.ValidUntil)
	seen := make(map[ExperiencePolicyRef]bool, len(content.PolicyRefs))
	for _, ref := range content.PolicyRefs {
		policy, exists := allowed[ref]
		if !exists || seen[ref] {
			return fault.Conflict("经验候选引用的制度不属于来源审查记录，或存在重复")
		}
		if fromErr == nil && untilErr == nil && until.After(from) {
			policyFrom, errFrom := knowledgeTime(policy.ValidFrom)
			policyUntil, errUntil := knowledgeTime(policy.ValidUntil)
			if errFrom != nil || errUntil != nil || from.Before(policyFrom) || until.After(policyUntil) {
				return fault.Conflict("经验候选的有效期超出来源政策")
			}
		}
		seen[ref] = true
	}
	return nil
}

// validateExperienceCandidateSource is called under governance's existing
// schema and source-case locks before a candidate can be approved or reused.
func validateExperienceCandidateSource(ctx context.Context, tx pgx.Tx, item Case, candidate ExperienceCandidate) error {
	feedback, _, err := scanExperienceFeedback(tx.QueryRow(ctx, `SELECT `+feedbackSelectColumns+` FROM experience_feedback
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND feedback_id=$4`, item.TenantID, item.OwnerOrgID, item.ID, candidate.FeedbackID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.Conflict("经验候选的反馈来源不存在")
		}
		return err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT input_bytes FROM experience_distill_inputs
		WHERE job_id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND feedback_id=$5 AND actor_id=$6 AND input_sha256=$7`,
		candidate.JobID, item.TenantID, item.OwnerOrgID, item.ID, feedback.FeedbackID, feedback.ApplicantID, candidate.InputSHA256).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict("经验候选的提炼输入不存在")
	}
	if err != nil {
		return err
	}
	var input ExperienceDistillInput
	if sha256Bytes(raw) != candidate.InputSHA256 || decodeExperienceJSON(raw, &input) != nil ||
		input.SchemaVersion != "experience-distill-input/v1" || input.TenantID != item.TenantID || input.OrgID != item.OwnerOrgID ||
		input.CaseID != item.ID || input.FeedbackID != feedback.FeedbackID || input.DecisionID != feedback.DecisionID ||
		input.ApplicantID != feedback.ApplicantID || input.CandidateID != candidate.CandidateID || input.BaseVersionID != candidate.BaseVersionID ||
		!experienceIdentity(input.ModelConfigurationID) || input.TemplateRef != candidate.TemplateRef {
		return fault.Conflict("经验候选的提炼输入编号无效")
	}
	if err := validateExperiencePolicyContext(ctx, tx, item, feedback, input); err != nil {
		return err
	}
	return validateExperiencePolicyRefs(candidate.Content, input.PolicyContext)
}

// RequestExperienceDistillation atomically enqueues a bounded independent job
// and its notification after checking the source and all previous source cases.
func (s *Store) RequestExperienceDistillation(ctx context.Context, user accounts.User, input ExperienceDistillRequest) (ExperienceDistillReceipt, error) {
	if s.ReadOnly() {
		return ExperienceDistillReceipt{}, fault.New(503, "read_only", "只读模式下不能提炼经验")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return ExperienceDistillReceipt{}, fault.Forbidden("当前服务不能提交经验提炼")
	}
	if !experienceIdentity(input.CaseID) || !experienceIdentity(input.FeedbackID) || !experienceIdentity(input.IdempotencyKey) || len(input.IdempotencyKey) > 80 ||
		(input.CandidateID == "") != (input.BaseVersionID == "") ||
		(input.CandidateID != "" && (!experienceIdentity(input.CandidateID) || !experienceIdentity(input.BaseVersionID))) {
		return ExperienceDistillReceipt{}, fault.Invalid("经验提炼的来源或版本编号无效")
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, input.CaseID, access.Review)
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	defer tx.Rollback(context.Background())
	feedback, _, err := scanExperienceFeedback(tx.QueryRow(ctx, `SELECT `+feedbackSelectColumns+` FROM experience_feedback
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND feedback_id=$4`, item.TenantID, item.OwnerOrgID, item.ID, input.FeedbackID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceDistillReceipt{}, fault.NotFound("没有找到对应反馈")
	}
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	if feedback.ApplicantID != current.ID {
		return ExperienceDistillReceipt{}, fault.Forbidden("只有反馈申请人可以请求提炼")
	}
	requestBytes, err := json.Marshal(struct {
		Schema string                   `json:"schema"`
		Input  ExperienceDistillRequest `json:"input"`
	}{"experience-distill-request/v1", input})
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	var priorJob, priorHash, priorOrg, priorCase, priorFeedback string
	err = tx.QueryRow(ctx, `SELECT job_id,request_sha256,org_id,case_id,feedback_id FROM experience_distill_inputs
		WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, item.TenantID, current.ID, input.IdempotencyKey).
		Scan(&priorJob, &priorHash, &priorOrg, &priorCase, &priorFeedback)
	if err == nil {
		if priorHash != sha256Bytes(requestBytes) || priorOrg != item.OwnerOrgID || priorCase != item.ID || priorFeedback != feedback.FeedbackID {
			return ExperienceDistillReceipt{}, fault.Conflict("这个请求标识已用于其他经验提炼请求")
		}
		if err := authorizeExperienceAncestors(ctx, tx, current, item, input.CandidateID, input.BaseVersionID); err != nil {
			return ExperienceDistillReceipt{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ExperienceDistillReceipt{}, err
		}
		return ExperienceDistillReceipt{JobID: priorJob}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExperienceDistillReceipt{}, err
	}
	models := s.currentModels()
	if models == nil || strings.TrimSpace(models.ConfigurationID()) == "" {
		return ExperienceDistillReceipt{}, fault.New(503, "model_unavailable", "经验提炼模型配置不可用")
	}
	policyContext, err := experiencePolicyContext(ctx, tx, item, feedback)
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	var previous *ExperienceCandidateContent
	candidateID := input.CandidateID
	if candidateID != "" {
		if err := authorizeExperienceAncestors(ctx, tx, current, item, candidateID, input.BaseVersionID); err != nil {
			return ExperienceDistillReceipt{}, err
		}
		var hasSuccessor bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experience_candidate_versions
			WHERE tenant_id=$1 AND candidate_id=$2 AND base_version_id=$3)`, item.TenantID, candidateID, input.BaseVersionID).Scan(&hasSuccessor); err != nil {
			return ExperienceDistillReceipt{}, err
		}
		if hasSuccessor {
			return ExperienceDistillReceipt{}, fault.Conflict("经验候选已有后继版本，请刷新")
		}
		candidate, err := scanExperienceCandidate(tx.QueryRow(ctx, `SELECT `+candidateSelectColumns+` FROM experience_candidate_versions
			WHERE tenant_id=$1 AND candidate_id=$2 AND version_id=$3`, item.TenantID, candidateID, input.BaseVersionID))
		if err != nil {
			return ExperienceDistillReceipt{}, err
		}
		previous = &candidate.Content
	} else {
		candidateID, err = newID("experience_")
		if err != nil {
			return ExperienceDistillReceipt{}, err
		}
	}
	frozen := ExperienceDistillInput{SchemaVersion: "experience-distill-input/v1", TenantID: item.TenantID,
		OrgID: item.OwnerOrgID, CaseID: item.ID, FeedbackID: feedback.FeedbackID, DecisionID: feedback.DecisionID,
		ApplicantID: current.ID, CandidateID: candidateID, BaseVersionID: input.BaseVersionID,
		ModelConfigurationID: models.ConfigurationID(), TemplateRef: experienceDistillTemplate, PolicyContext: policyContext}
	prompt, err := json.Marshal(experienceDistillWork{Input: frozen, Feedback: feedback, Previous: previous})
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	if len(prompt) > 32<<10 {
		return ExperienceDistillReceipt{}, fault.Invalid("反馈和政策上下文超过经验提炼的输入上限")
	}
	frozenBytes, err := json.Marshal(frozen)
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	jobID, err := newID("job_")
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	outboxID, err := newID("outbox_")
	if err != nil {
		return ExperienceDistillReceipt{}, err
	}
	cfg := s.executionQueuePolicy()
	function := pgx.Identifier{s.schema, "experience_distill_enqueue"}.Sanitize()
	var accepted string
	err = tx.QueryRow(ctx, `SELECT `+function+`($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		jobID, outboxID, item.TenantID, item.OwnerOrgID, item.ID, current.ID, feedback.FeedbackID,
		input.IdempotencyKey, sha256Bytes(requestBytes), sha256Bytes(frozenBytes), frozenBytes,
		cfg.hash, cfg.encoded, cfg.GlobalPending, cfg.TenantPending, cfg.MaxClaims, cfg.MaxAgeSeconds).Scan(&accepted)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return ExperienceDistillReceipt{}, fault.Conflict("这个请求标识已用于其他经验提炼请求")
			case "53300", "55000":
				problem := fault.New(503, "execution_capacity", "经验提炼队列暂不可用，请稍后重试")
				problem.Retryable = true
				return ExperienceDistillReceipt{}, problem
			}
		}
		return ExperienceDistillReceipt{}, err
	}
	if strings.TrimSpace(accepted) == "" {
		return ExperienceDistillReceipt{}, errors.New("experience task admission returned no identity")
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceDistillReceipt{}, err
	}
	return ExperienceDistillReceipt{JobID: accepted}, nil
}

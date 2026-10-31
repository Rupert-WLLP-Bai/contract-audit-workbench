package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
	"contractaudit/internal/execution"
	"contractaudit/internal/fault"
	"contractaudit/internal/model"

	"github.com/jackc/pgx/v5"
)

const (
	maxConcurrentReviews    = 2
	reviewExecutionLimit    = 90 * time.Second
	attemptFinalizeTimeout  = 5 * time.Second
)

var (
	errAttemptTerminal    = errors.New("review attempt is already terminal")
	errAttemptMissing     = errors.New("review attempt does not exist")
	errModelNotConfigured = errors.New("model is not configured")
)

// StartReview retains the existing ungoverned entry point. Governed cases must
// use StartReviewAt with the business instant being reviewed.
func (s *Store) StartReview(ctx context.Context, user accounts.User, caseID string) (string, error) {
	return s.startReview(ctx, user, caseID, "", "", false)
}

// StartReviewAt accepts an explicit RFC3339 business instant, including a time
// zone. The caller supplies the time relevant to the contract; the store derives
// tenant, organization, actor and all knowledge identities from trusted state.
func (s *Store) StartReviewAt(ctx context.Context, user accounts.User, caseID, businessAt string) (string, error) {
	return s.StartReviewAtWithEvidenceCutoff(ctx, user, caseID, businessAt, "")
}

// StartReviewAtWithEvidenceCutoff accepts an optional explicit evidence cutoff
// in addition to the business instant. The accepted snapshot records whether
// the cutoff was explicit or sampled by the server.
func (s *Store) StartReviewAtWithEvidenceCutoff(ctx context.Context, user accounts.User, caseID, businessAt, evidenceCutoffAt string) (string, error) {
	if _, err := knowledgeTime(businessAt); err != nil {
		return "", err
	}
	cutoff, err := canonicalizeLegalCutoffInput(evidenceCutoffAt)
	if err != nil {
		return "", err
	}
	return s.startReview(ctx, user, caseID, businessAt, cutoff, false)
}

// StartReviewScope accepts an explicit review scope while keeping the legacy
// empty scope and business-time behavior for existing clients.
func (s *Store) StartReviewScope(ctx context.Context, user accounts.User, caseID, businessAt, evidenceCutoffAt, scope string) (string, error) {
	if businessAt != "" {
		if _, err := knowledgeTime(businessAt); err != nil {
			return "", err
		}
	}
	cutoff, err := canonicalizeLegalCutoffInput(evidenceCutoffAt)
	if err != nil {
		return "", err
	}
	return s.startReview(ctx, user, caseID, businessAt, cutoff, false, scope)
}

func (s *Store) StartReviewAsyncScope(ctx context.Context, user accounts.User, caseID, businessAt, evidenceCutoffAt, scope string) (JobAcceptance, error) {
	if businessAt != "" {
		if _, err := knowledgeTime(businessAt); err != nil {
			return JobAcceptance{}, err
		}
	}
	cutoff, err := canonicalizeLegalCutoffInput(evidenceCutoffAt)
	if err != nil {
		return JobAcceptance{}, err
	}
	return s.startReviewAsync(ctx, user, caseID, businessAt, cutoff, scope)
}

func (s *Store) StartReviewAsync(ctx context.Context, user accounts.User, caseID string) (JobAcceptance, error) {
	return s.startReviewAsync(ctx, user, caseID, "", "")
}

// StartReviewAsyncAt is the durable counterpart of StartReviewAt.
func (s *Store) StartReviewAsyncAt(ctx context.Context, user accounts.User, caseID, businessAt string) (JobAcceptance, error) {
	return s.StartReviewAsyncAtWithEvidenceCutoff(ctx, user, caseID, businessAt, "")
}

func (s *Store) StartReviewAsyncAtWithEvidenceCutoff(ctx context.Context, user accounts.User, caseID, businessAt, evidenceCutoffAt string) (JobAcceptance, error) {
	if _, err := knowledgeTime(businessAt); err != nil {
		return JobAcceptance{}, err
	}
	cutoff, err := canonicalizeLegalCutoffInput(evidenceCutoffAt)
	if err != nil {
		return JobAcceptance{}, err
	}
	return s.startReviewAsync(ctx, user, caseID, businessAt, cutoff)
}

func (s *Store) startReviewAsync(ctx context.Context, user accounts.User, caseID, businessAt, evidenceCutoffAt string, scope ...string) (JobAcceptance, error) {
	runID, err := s.startReview(ctx, user, caseID, businessAt, evidenceCutoffAt, true, scope...)
	if err != nil {
		return JobAcceptance{}, err
	}
	st, err := s.snapshotAs(user)
	if err != nil {
		return JobAcceptance{}, err
	}
	attempt, ok := st.latestAttempt(runID)
	if !ok {
		return JobAcceptance{}, fault.Conflict("审查任务缺少执行记录")
	}
	for _, job := range st.Jobs {
		if job.AttemptID == attempt.ID {
			return JobAcceptance{RunID: runID, AttemptID: attempt.ID, JobID: job.ID}, nil
		}
	}
	return JobAcceptance{}, fault.Conflict("审查任务没有保存到任务队列")
}

func (s *Store) startReview(ctx context.Context, user accounts.User, caseID, businessAt, evidenceCutoffAt string, durable bool, requestedScope ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	rawScope := ""
	if len(requestedScope) > 0 {
		rawScope = requestedScope[0]
	}
	scope, err := normalizeReviewScope(rawScope)
	if err != nil {
		return "", err
	}
	minimumMaterials := 2
	if scope == ReviewScopeSingleContract {
		minimumMaterials = 1
	}
	if evidenceCutoffAt != "" && businessAt == "" {
		return "", fault.Invalid("显式法律证据截止时间需要同时提供业务时间")
	}
	if s.ReadOnly() {
		return "", fault.New(503, "read_only", "只读模式下不能发起审查")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return "", err
	}
	user = currentUser
	if !accounts.CanStart(user.Role) {
		return "", fault.Forbidden("当前角色不能发起审查")
	}
	seen, err := s.snapshotAs(user)
	if err != nil {
		return "", err
	}
	if _, err := s.writableCase(&seen, user, caseID, access.Review); err != nil {
		return "", err
	}
	current := selectedMaterialDocuments(seen, caseID)
	readinessMaterials := make([]ReviewReadinessMaterial, 0, len(current))
	for _, doc := range current {
		readinessMaterials = append(readinessMaterials, ReviewReadinessMaterial{DocumentID: doc.ID, Role: doc.Role, Selected: true})
	}
	caseItem, _ := seen.caseByID(caseID)
	preflight := evaluateReviewReadiness(reviewReadinessInputs{Scope: scope, PrimaryDocumentID: currentPrimaryDraftID(seen, caseID), CanRead: true, CanStartRole: true, Writable: true,
		SelectionConfirmed: !materialSelectionPending(seen, caseID), MinimumMaterials: minimumMaterials, Materials: readinessMaterials,
		BusinessAtProvided: businessAt != "" || !isGovernedReviewCase(caseItem)})
	if err := reviewAdmissionError(preflight.Blockers); err != nil {
		return "", err
	}
	reviewDocumentIDs := documentIDs(current)
	if err := s.acquireParseLock(ctx); err != nil {
		return "", err
	}
	parseLocked := true
	defer func() {
		if parseLocked {
			s.releaseParseLock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	seen, err = s.snapshotAs(user)
	if err != nil {
		return "", err
	}
	current = selectedMaterialDocuments(seen, caseID)
	if !sameDocumentSet(documentIDs(current), reviewDocumentIDs) {
		return "", fault.Conflict("案件材料刚被更新，请重新发起审查")
	}
	models := s.currentModels()
	frozenDocs, selections, err := s.acceptanceParsePlan(ctx, seen, current, models)
	if err != nil {
		return "", err
	}
	if !parsedSelectionCurrent(seen, frozenDocs) {
		return "", fault.Conflict("所选的材料解析版本刚被更新，请重新发起审查")
	}
	seen, err = s.snapshotAs(user)
	if err != nil {
		return "", err
	}
	if !sameDocumentSet(documentIDs(selectedMaterialDocuments(seen, caseID)), reviewDocumentIDs) || !parsedSelectionCurrent(seen, frozenDocs) {
		return "", fault.Conflict("所选的材料解析版本刚被更新，请重新发起审查")
	}
	caseItem, ok := seen.caseByID(caseID)
	if !ok {
		return "", fault.NotFound("没有找到对应记录")
	}
	if err := businessWritable(caseItem); err != nil {
		return "", err
	}
	// GOV-03: the governed pack is selected inside the acceptance transaction, not
	// in a separate read, so the pack that is frozen is the pack the acceptance
	// write saw under its own locks. The planner configuration is frozen in the
	// same signature so a retry can never switch prompt or limits.
	plannerConfiguration, err := s.plannerConfigurationForAcceptance(signatureModelConfigurationID(models))
	if err != nil {
		return "", err
	}
	var activePack *ActiveRulePack
	plannerPayload, payloadErr := json.Marshal(plannerConfiguration)
	if payloadErr != nil {
		return "", payloadErr
	}
	signature := applyReviewScope(s.currentSignatureWithRulePack(reviewDocumentIDs, frozenDocs, nil, telecomCompilerRefForCase(caseItem), plannerConfiguration.ConfigurationID, plannerPayload), scope)
	reserved := false
	jobID, outboxID := "", ""
	if durable {
		jobID, err = newID("job_")
		if err != nil {
			return "", err
		}
		outboxID, err = newID("out_")
		if err != nil {
			return "", err
		}
	}
	runID, err := newID("run_")
	if err != nil {
		return "", err
	}
	attemptID, err := newID("att_")
	if err != nil {
		return "", err
	}
	acceptedAt := stamp()
	attempt := Attempt{
		ID: attemptID, RunID: runID, Ordinal: 1, ActorID: user.ID, State: "accepted", AcceptedAt: acceptedAt,
		HistoryKind: "preparing",
	}
	var executionCase Case
	created := false
	// The same schema-then-case locked transaction resolves the case, active
	// pack and approved knowledge, and commits the resulting snapshot with the
	// run, attempt and durable job. Nothing is selected from a client identity.
	// The context planner must see the same immutable material identities the
	// worker will later freeze into AttemptInput; otherwise a retry could not
	// recompute the accepted context from the exact frozen materials.
	serviceObjectDocuments := serviceObjectFrozenMaterials(seen, frozenDocs)
	var acceptanceTx pgx.Tx
	hook := func(tx pgx.Tx) error {
		acceptanceTx = tx
		lockedCase, err := loadKnowledgeCase(ctx, tx, user, caseID)
		if err != nil {
			return err
		}
		var at time.Time
		if businessAt != "" {
			at, err = knowledgeTime(businessAt)
			if err != nil {
				return err
			}
		}
		// Service-object dependencies are authoritative for every case, not
		// only for cases that selected a governed pack. An ungoverned case that
		// already registered an object still freezes the exact version,
		// association and mapping pins, and it must name the business instant
		// itself instead of borrowing the server clock.
		if !isGovernedReviewCase(lockedCase) {
			signature = applyReviewScope(s.currentSignatureWithRulePack(reviewDocumentIDs, frozenDocs, nil, telecomCompilerRefForCase(lockedCase), plannerConfiguration.ConfigurationID, plannerPayload), scope)
			signature, err = withServiceObjectContextTx(ctx, tx, lockedCase, at, serviceObjectDocuments, signature)
			return err
		}
		if at.IsZero() {
			return fault.Invalid("必须提供带时区的业务时间")
		}
		cutoffNow := time.Now().UTC().Truncate(time.Microsecond)
		cutoffAt, cutoffMode, explicitCutoff := cutoffNow, "acceptance_time", ""
		if evidenceCutoffAt != "" {
			parsed, err := parseCanonicalLegalCutoff(evidenceCutoffAt)
			if err != nil {
				return fault.Invalid("显式法律证据截止时间必须是规范UTC微秒时间")
			}
			cutoffAt, err = time.Parse(time.RFC3339Nano, parsed)
			if err != nil || cutoffAt.After(cutoffNow) {
				return fault.Invalid("证据截止时间必须不晚于审查受理时间")
			}
			cutoffMode, explicitCutoff = "explicit", parsed
		}
		assessment, err := assessGovernedReviewTx(ctx, tx, user, lockedCase, at, cutoffAt, cutoffMode, explicitCutoff)
		if err != nil {
			return err
		}
		frozenMaterials := readinessMaterialsForFrozen(seen, frozenDocs)
		checks := reviewReadinessRuleChecks(assessment.Pack, assessment.Knowledge, frozenMaterials, lockedCase)
		readiness := evaluateReviewReadiness(reviewReadinessInputs{Scope: scope, PrimaryDocumentID: currentPrimaryDraftID(seen, caseID), CanRead: true, CanStartRole: true, Writable: true,
			SelectionConfirmed: true, BusinessAtProvided: true, MinimumMaterials: minimumMaterials, Materials: frozenMaterials, Checks: checks})
		if err := reviewAdmissionError(readiness.Blockers); err != nil {
			return err
		}
		activePack = assessment.Pack
		signature = applyReviewScope(s.currentSignatureWithRulePack(reviewDocumentIDs, frozenDocs, assessment.Pack, telecomCompilerRefForCase(lockedCase), plannerConfiguration.ConfigurationID, plannerPayload), scope)
		if assessment.Knowledge != nil {
			signature.GovernedKnowledge = assessment.Knowledge
		}
		// P3 service-object identity and associations are authoritative SQL
		// state. Freeze them from this same case-locked acceptance transaction,
		// using only the material/parse versions already selected above.
		signature, err = withServiceObjectContextTx(ctx, tx, lockedCase, at, serviceObjectDocuments, signature)
		if err != nil {
			return err
		}
		return nil
	}
	err = s.updateCaseAsWith(user, caseID, hook, func(st *state) error {
		currentCase, err := s.writableCase(st, user, caseID, access.Review)
		if err != nil {
			return err
		}
		executionCase = *currentCase
		signature, err = withPartyContext(*st, caseID, signature)
		if err != nil {
			return err
		}
		if materialSelectionPending(*st, caseID) || !sameDocumentSet(documentIDs(selectedMaterialDocuments(*st, caseID)), reviewDocumentIDs) || !parsedSelectionCurrent(*st, frozenDocs) {
			return fault.Conflict("所选的材料解析版本刚被更新，请重新发起审查")
		}
		if scope == ReviewScopeSingleContract {
			selected := readinessMaterialsForFrozen(*st, frozenDocs)
			gate := evaluateReviewReadiness(reviewReadinessInputs{Scope: scope, PrimaryDocumentID: currentPrimaryDraftID(*st, caseID), CanRead: true, CanStartRole: true, Writable: true, SelectionConfirmed: true, BusinessAtProvided: true, MinimumMaterials: 1, Materials: selected})
			if err := reviewAdmissionError(gate.Blockers); err != nil {
				return err
			}
		}
		if run, ok := st.latestRun(caseID); ok {
			previous := st.snapshotOf(run.ID)
			if sameRunSignature(previous, signature) && sameRunCompilerIdentity(previous, signature) && (!durable || hasJobForRun(*st, run.ID)) {
				if previous.GovernedKnowledge != nil {
					if err := RevalidateGovernedKnowledgeSnapshotTx(ctx, acceptanceTx, user, caseID, *previous.GovernedKnowledge); err != nil {
						return err
					}
					if isCloudServicesCase(*currentCase) {
						if _, err := validateCloudPolicyBindings(previous, *previous.GovernedKnowledge, currentCase.TenantID); err != nil {
							return fault.Conflict("云服务治理制度绑定无效：" + err.Error())
						}
					}
				}
				runID = run.ID
				return nil
			}
		}
		if !durable {
			if !s.reserveExecution() {
				return &fault.Error{Status: 503, Code: "execution_capacity", Message: "审查执行容量已满，请稍后重试", Retryable: true}
			}
			reserved = true
		}
		now := acceptedAt
		st.Runs = append(st.Runs, Run{ID: runID, CaseID: caseID, State: "accepted", ModelRef: signature.ModelRef, CreatedAt: now, UpdatedAt: now})
		if err := sealSnapshot(st, runID, signature); err != nil {
			return err
		}
		// The accepted attempt carries the exact frozen pack bytes and planner
		// configuration, so the worker never resolves latest.
		attempt.ReviewScope = signature.ReviewScope
		attempt.RulePackSnapshot = append(json.RawMessage(nil), signature.RulePackSnapshot...)
		attempt.RulePackHash = signature.RulePackHash
		attempt.RulePackSnapshotDigest = signature.RulePackDigest
		attempt.RulePackCompilerRef = signature.RulePackCompilerRef
		attempt.RulePackID = signature.RulePackID
		attempt.RulePackVersion = signature.RulePackVersion
		attempt.PlannerConfigurationID = signature.PlannerConfigurationID
		attempt.PlannerConfiguration = append(json.RawMessage(nil), signature.PlannerConfiguration...)
		if activePack != nil {
			attempt.RulePackPointerRevision = activePack.PointerRevision
		}
		st.Attempts = append(st.Attempts, attempt)
		if durable {
			caseItem, ok := st.caseByID(caseID)
			if !ok {
				return fault.NotFound("没有找到对应记录")
			}
			acceptance := JobAcceptance{RunID: runID, AttemptID: attemptID, JobID: jobID}
			payload, err := json.Marshal(acceptance)
			if err != nil {
				return err
			}
			st.Jobs = append(st.Jobs, Job{ID: jobID, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, RunID: runID, AttemptID: attemptID, ActorID: user.ID, Kind: "review", State: "queued", CreatedAt: acceptedAt, UpdatedAt: acceptedAt})
			st.Outbox = append(st.Outbox, OutboxRecord{ID: outboxID, JobID: jobID, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, EventType: "review.accepted", Payload: string(payload), State: "pending", CreatedAt: acceptedAt})
		}
		for _, selection := range selections {
			selection.AttemptID = attemptID
			selection.RunID = runID
			st.AttemptParseSelections = append(st.AttemptParseSelections, selection)
		}
		st.ExecutionEvents = append(st.ExecutionEvents, acceptedEvent(attempt))
		if err := appendAudit(st, runID, user.ID, "review_started", []string{caseID}); err != nil {
			return err
		}
		appendRunEvent(st, runID, "accepted")
		activityID, err := newID("act_")
		if err != nil {
			return err
		}
		summary := "开始了一次审查"
		if _, ok := st.previousRun(Run{ID: runID, CaseID: caseID}); ok {
			summary = "开始了一次修改稿复审"
		}
		st.Activity = append(st.Activity, Activity{ID: activityID, CaseID: caseID, At: now, Summary: summary})
		created = true
		return nil
	})
	s.releaseParseLock()
	parseLocked = false
	if err != nil || !created {
		if reserved {
			s.releaseExecution()
		}
		return runID, err
	}
	if !durable {
		s.launchAttempt(runID, attemptID, models, executionCase, user.ID)
	}
	return runID, nil
}

func (s *Store) Retry(ctx context.Context, user accounts.User, caseID, runID string) error {
	return s.retry(ctx, user, caseID, runID, false)
}

func (s *Store) RetryAsync(ctx context.Context, user accounts.User, caseID, runID string) (JobAcceptance, error) {
	if err := s.retry(ctx, user, caseID, runID, true); err != nil {
		return JobAcceptance{}, err
	}
	st, err := s.snapshotAs(user)
	if err != nil {
		return JobAcceptance{}, err
	}
	attempt, ok := st.latestAttempt(runID)
	if !ok {
		return JobAcceptance{}, fault.Conflict("重试任务缺少执行记录")
	}
	for _, job := range st.Jobs {
		if job.AttemptID == attempt.ID {
			return JobAcceptance{RunID: runID, AttemptID: attempt.ID, JobID: job.ID}, nil
		}
	}
	return JobAcceptance{}, fault.Conflict("重试任务没有保存到任务队列")
}

func (s *Store) retry(ctx context.Context, user accounts.User, caseID, runID string, durable bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能重试审查")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return err
	}
	user = currentUser
	if !accounts.CanStart(user.Role) {
		return fault.Forbidden("当前角色不能重试审查")
	}
	seen, err := s.snapshotAs(user)
	if err != nil {
		return err
	}
	if _, err := s.visibleCase(&seen, user, caseID); err != nil {
		return err
	}
	run, ok := seen.latestRun(caseID)
	if !ok || run.ID != runID {
		return fault.NotFound("没有找到对应记录")
	}
	caseItem, ok := seen.caseByID(caseID)
	if !ok {
		return fault.NotFound("没有找到对应记录")
	}
	frozen := seen.snapshotOf(runID)
	if _, ok := seen.latestAttempt(runID); !ok {
		return fault.Conflict("较早的审查缺少当时保存的执行记录，无法安全重试")
	}
	// A duplicate request for an active run is an idempotent no-op. It must not
	// depend on AttemptInput, which the worker has not prepared yet; the locked
	// retry transaction re-checks the live attempt state before creating one.
	// One operation, one client: the retried attempt must keep the identity it
	// validated, even if the endpoint watcher applies a new revision meanwhile.
	models := s.currentModels()
	currentSignature := withFrozenGovernance(currentSignatureFor(frozen.DocumentIDs, nil, models, telecomCompilerRefForCase(caseItem)), frozen)
	currentSignature, err = withPartyContext(seen, caseID, currentSignature)
	if err != nil {
		return err
	}
	if frozen.PartyContextHash != currentSignature.PartyContextHash {
		return fault.Conflict("主体信息已变更，请发起新的审查。原审查记录会保留")
	}
	if !sameRunConfiguration(frozen, currentSignature) || !sameRunCompilerIdentity(frozen, currentSignature) {
		return fault.Conflict("规则或模型配置已变更，请发起新的审查。原审查记录会保留")
	}
	reserved := false
	jobID, outboxID := "", ""
	if durable {
		jobID, err = newID("job_")
		if err != nil {
			return err
		}
		outboxID, err = newID("out_")
		if err != nil {
			return err
		}
	}
	attemptID, err := newID("att_")
	if err != nil {
		return err
	}
	acceptedAt := stamp()
	created := false
	var executionCase Case
	var retryTx pgx.Tx
	err = s.updateCaseAsWith(user, caseID, func(tx pgx.Tx) error {
		// The locked callback below re-reads the live attempt state and then
		// validates the frozen object dependencies on this same transaction, so
		// an active duplicate and a real retry cannot race each other.
		retryTx = tx
		return nil
	}, func(st *state) error {
		currentCase, err := s.writableCase(st, user, caseID, access.Review)
		if err != nil {
			return err
		}
		executionCase = *currentCase
		latestRun, ok := st.latestRun(caseID)
		if !ok || latestRun.ID != runID {
			return fault.NotFound("没有找到对应记录")
		}
		currentSignature := withFrozenGovernance(currentSignatureFor(frozen.DocumentIDs, nil, models, telecomCompilerRefForCase(*currentCase)), frozen)
		currentSignature, err = withPartyContext(*st, caseID, currentSignature)
		if err != nil {
			return err
		}
		if !sameRunSignature(st.snapshotOf(runID), frozen) {
			return fault.Conflict("审查记录已变化，请重新发起审查")
		}
		if currentSignature.PartyContextHash != frozen.PartyContextHash {
			return fault.Conflict("主体信息已变更，请发起新的审查。原审查记录会保留")
		}
		if !sameRunConfiguration(frozen, currentSignature) ||
			!sameRunCompilerIdentity(frozen, currentSignature) {
			return fault.Conflict("规则或模型配置已变更，请发起新的审查。原审查记录会保留")
		}
		if frozen.GovernedKnowledge == nil {
			if isCloudServicesCase(*currentCase) || (isGovernedReviewCase(*currentCase) && frozen.RulePackID != "") {
				return fault.Conflict("较早的审查缺少当时保存的制度记录，无法安全重试")
			}
		} else {
			if err := RevalidateGovernedKnowledgeSnapshotTx(ctx, retryTx, user, caseID, *frozen.GovernedKnowledge); err != nil {
				return err
			}
			if isCloudServicesCase(*currentCase) {
				if _, err := validateCloudPolicyBindings(frozen, *frozen.GovernedKnowledge, currentCase.TenantID); err != nil {
					return fault.Conflict("云服务治理制度绑定无效：" + err.Error())
				}
			}
		}
		last, ok := st.latestAttempt(runID)
		if !ok {
			return fault.Conflict("较早的审查没有保存当时使用的输入，无法安全重试；请发起新的审查")
		}
		if attemptActive(last.State) {
			return nil
		}
		frozenInput, ok := st.attemptInput(last.ID)
		if !ok || last.HistoryKind != "frozen" || !frozenInputMatchesSnapshot(frozen, frozenInput.Documents) ||
			!attemptSelectionsMatchFrozen(st.parseSelectionsForAttempt(last.ID), frozenInput.Documents) ||
			!s.currentParserConfigurationMatches(*st, frozenInput.Documents) {
			return fault.Conflict("本次审查保存的输入刚发生变化，无法安全重试")
		}
		if err := s.verifyFrozenDocuments(*st, frozenInput.Documents); err != nil {
			return fault.Conflict("本次审查保存的输入未通过完整性校验，无法安全重试")
		}
		expectedHash, err := frozenInputHash(frozen, frozenInput.Documents)
		if err != nil || expectedHash != frozenInput.InputHash || last.InputHash == nil || *last.InputHash != frozenInput.InputHash {
			return fault.Conflict("本次审查保存的输入摘要未通过校验，无法安全重试")
		}
		// A real retry recomputes the object and mapping dependencies under the
		// current authorization and the exact frozen material/parse pairs. Any
		// change rejects this retry before an attempt row is written, and the
		// historical snapshot keeps its original bytes.
		lockedCase, err := loadKnowledgeCase(ctx, retryTx, user, caseID)
		if err != nil {
			return err
		}
		if (frozen.ServiceObjectContext == nil) != (frozen.ServiceObjectContextHash == "") {
			return fault.Conflict("审查记录的服务对象上下文不完整，无法安全重试")
		}
		if frozen.ServiceObjectContext == nil {
			// A legacy run without a frozen object dependency keeps its nil
			// extension. It cannot silently adopt an object registered after the
			// run was accepted, so a newly present inventory is a conflict, not a
			// backfill.
			sources, err := serviceObjectReviewContextSourcesTx(ctx, retryTx, lockedCase)
			if err != nil {
				return err
			}
			if len(sources) > 0 {
				return fault.Conflict("本次审查未保存服务对象上下文，请发起新的审查。原审查记录会保留")
			}
		} else {
			recomputed := withFrozenGovernance(currentSignatureFor(frozen.DocumentIDs, nil, models, telecomCompilerRefForCase(*currentCase)), frozen)
			recomputed, err = withServiceObjectContextTx(ctx, retryTx, lockedCase, frozen.ServiceObjectContext.BusinessAt, frozenInput.Documents, recomputed)
			if err != nil {
				return err
			}
			if recomputed.ServiceObjectContextHash != frozen.ServiceObjectContextHash {
				return fault.Conflict("服务对象或当前映射已变更，请发起新的审查。原审查记录会保留")
			}
		}
		if !durable {
			if !s.reserveExecution() {
				return &fault.Error{Status: 503, Code: "execution_capacity", Message: "审查执行容量已满，请稍后重试", Retryable: true}
			}
			reserved = true
		}
		retryOf := last.ID
		attempt := Attempt{
			ID: attemptID, RunID: runID, Ordinal: last.Ordinal + 1, ActorID: user.ID, RetryOfAttemptID: &retryOf,
			State: "accepted", AcceptedAt: acceptedAt, HistoryKind: "frozen",
			RulePackSnapshot: append([]byte(nil), last.RulePackSnapshot...), RulePackHash: last.RulePackHash,
			RulePackSnapshotDigest: last.RulePackSnapshotDigest, RulePackCompilerRef: last.RulePackCompilerRef,
			RulePackID: last.RulePackID, RulePackVersion: last.RulePackVersion,
			RulePackPointerRevision: last.RulePackPointerRevision, PlannerConfigurationID: last.PlannerConfigurationID,
			PlannerConfiguration: append(json.RawMessage(nil), last.PlannerConfiguration...),
		}
		st.Attempts = append(st.Attempts, attempt)
		copyInput := frozenInput
		copyInput.AttemptID = attemptID
		st.AttemptInputs = append(st.AttemptInputs, copyInput)
		for _, selection := range st.parseSelectionsForAttempt(last.ID) {
			selection.AttemptID = attemptID
			st.AttemptParseSelections = append(st.AttemptParseSelections, selection)
		}
		inputHash := copyInput.InputHash
		for i := range st.Attempts {
			if st.Attempts[i].ID == attemptID {
				st.Attempts[i].InputHash = &inputHash
			}
		}
		st.ExecutionEvents = append(st.ExecutionEvents, acceptedEvent(attempt))
		if durable {
			caseItem, ok := st.caseByID(caseID)
			if !ok {
				return fault.NotFound("没有找到对应记录")
			}
			payload, err := json.Marshal(JobAcceptance{RunID: runID, AttemptID: attemptID, JobID: jobID})
			if err != nil {
				return err
			}
			st.Jobs = append(st.Jobs, Job{ID: jobID, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, RunID: runID, AttemptID: attemptID, ActorID: user.ID, Kind: "review", State: "queued", CreatedAt: acceptedAt, UpdatedAt: acceptedAt})
			st.Outbox = append(st.Outbox, OutboxRecord{ID: outboxID, JobID: jobID, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, EventType: "review.accepted", Payload: string(payload), State: "pending", CreatedAt: acceptedAt})
		}
		for i := range st.Runs {
			if st.Runs[i].ID == runID {
				st.Runs[i].State = "accepted"
				st.Runs[i].PartialReason = ""
				st.Runs[i].FailureMessage = ""
				st.Runs[i].UpdatedAt = acceptedAt
			}
		}
		if err := appendAudit(st, runID, user.ID, "review_retried", []string{caseID}); err != nil {
			return err
		}
		appendRunEvent(st, runID, "accepted")
		activityID, err := newID("act_")
		if err != nil {
			return err
		}
		st.Activity = append(st.Activity, Activity{ID: activityID, CaseID: caseID, At: acceptedAt, Summary: "按冻结输入重试了同一次审查"})
		created = true
		return nil
	})
	if err != nil || !created {
		if reserved {
			s.releaseExecution()
		}
		return err
	}
	if !durable {
		s.launchAttempt(runID, attemptID, models, executionCase, user.ID)
	}
	return nil
}

func (s *Store) CancelReview(ctx context.Context, user accounts.User, caseID, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能取消审查")
	}
	if !accounts.CanStart(user.Role) {
		return fault.Forbidden("当前角色不能取消审查")
	}
	seen, err := s.snapshotAs(user)
	if err != nil {
		return err
	}
	if _, err := s.visibleCase(&seen, user, caseID); err != nil {
		return err
	}
	resolve := func(st state) (Attempt, error) {
		run, ok := st.latestRun(caseID)
		if !ok || run.ID != runID {
			return Attempt{}, fault.NotFound("没有找到对应记录")
		}
		attempt, ok := st.latestAttempt(runID)
		if !ok {
			return Attempt{}, fault.NotFound("没有找到对应记录")
		}
		return attempt, nil
	}
	attempt, err := resolve(seen)
	if err != nil {
		return err
	}
	if !attemptActive(attempt.State) {
		return nil
	}
	// The update locks the attempt row, so it cannot interleave with a worker
	// phase write or a late model event. A worker claim taken after the snapshot
	// above fails the job generation CAS closed; retry once from fresh state
	// before surfacing that conflict, because the user's cancel is still valid.
	for retry := 0; ; retry++ {
		if retry > 0 {
			seen, err = s.snapshotAs(user)
			if err != nil {
				return err
			}
			attempt, err = resolve(seen)
			if err != nil {
				return err
			}
			if !attemptActive(attempt.State) {
				return nil
			}
		}
		if s.serviceOwner != "" {
			// Distributed deployment: cancellation comes from the execution API
			// instance, which usually shares no process with the worker, so it uses
			// the trusted user plus fresh in-lock reads instead of a local claim.
			err = s.cancelAttemptNarrow(context.Background(), user, caseID, runID, attempt.ID)
			if errors.Is(err, errAttemptTerminal) {
				err = nil
			}
		} else {
			err = s.updateCaseAttemptAs(user, caseID, attempt.ID, func(st *state) error {
				return s.cancelAttemptInState(st, user, caseID, runID, attempt)
			})
		}
		if !errors.Is(err, errLeaseLost) || retry > 0 {
			break
		}
	}
	if errors.Is(err, errAttemptTerminal) {
		latest, snapshotErr := s.snapshotAs(user)
		if snapshotErr == nil {
			if current, ok := latest.attemptByID(attempt.ID); ok && current.State == "cancelled" {
				return nil
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	s.runningMu.Lock()
	cancel := s.running[attempt.ID]
	s.runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// cancelAttemptInState applies the durable cancellation inside one state
// transaction: the attempt and run become cancelled, an active job loses its
// lease, and the attempt, run and audit streams record who cancelled it.
func (s *Store) cancelAttemptInState(st *state, user accounts.User, caseID, runID string, attempt Attempt) error {
	if _, err := s.writableCase(st, user, caseID, access.Review); err != nil {
		return err
	}
	current, ok := st.latestAttempt(runID)
	if !ok || current.ID != attempt.ID {
		return fault.NotFound("没有找到对应记录")
	}
	if !attemptActive(current.State) {
		return nil
	}
	finishedAt := stamp()
	errorCode := "cancelled_by_user"
	for i := range st.Attempts {
		if st.Attempts[i].ID == attempt.ID {
			st.Attempts[i].State = "cancelled"
			st.Attempts[i].FinishedAt = &finishedAt
			st.Attempts[i].ErrorCode = &errorCode
		}
	}
	for i := range st.Runs {
		if st.Runs[i].ID == runID {
			st.Runs[i].State = "cancelled"
			st.Runs[i].UpdatedAt = finishedAt
		}
	}
	for i := range st.Jobs {
		if st.Jobs[i].AttemptID != attempt.ID {
			continue
		}
		if st.Jobs[i].State == "running" || st.Jobs[i].State == "queued" {
			st.Jobs[i].State = "cancelled"
			st.Jobs[i].LeaseUntil = nil
			st.Jobs[i].UpdatedAt = finishedAt
		}
	}
	appendExecutionEvent(st, ExecutionEvent{
		AttemptID: attempt.ID, SpanID: stringPointer(attempt.ID), NodeKind: "attempt",
		Phase: "cancellation", EventType: "attempt_cancelled", At: finishedAt,
		Status: "cancelled", ErrorCode: &errorCode,
	})
	appendRunEvent(st, runID, "cancelled")
	return appendAudit(st, runID, user.ID, "review_cancelled", []string{caseID})
}

func (s *Store) OperationalMetadata(user accounts.User) (map[string]any, error) {
	current, err := trustedUser(user)
	if err != nil {
		return nil, err
	}
	if current.Role != "admin" {
		return nil, fault.Forbidden("只有管理员可以查看审查运行的运维信息")
	}
	st, err := s.snapshotScoped(context.Background(), userScope(current))
	if err != nil {
		return nil, err
	}
	counts := map[string]any{
		"total_attempts": 0, "running_attempts": 0, "completed_attempts": 0,
		"partial_attempts": 0, "failed_attempts": 0, "cancelled_attempts": 0,
		"model_calls": 0, "unknown_usage_calls": 0, "queue_depth": nil,
	}
	for _, attempt := range st.Attempts {
		counts["total_attempts"] = counts["total_attempts"].(int) + 1
		switch attempt.State {
		case "accepted", "running":
			counts["running_attempts"] = counts["running_attempts"].(int) + 1
		case "completed":
			counts["completed_attempts"] = counts["completed_attempts"].(int) + 1
		case "partial":
			counts["partial_attempts"] = counts["partial_attempts"].(int) + 1
		case "failed", "interrupted":
			counts["failed_attempts"] = counts["failed_attempts"].(int) + 1
		case "cancelled":
			counts["cancelled_attempts"] = counts["cancelled_attempts"].(int) + 1
		}
	}
	usage := usageSummary(st.ExecutionEvents, nil)
	counts["model_calls"] = usage.Calls
	counts["unknown_usage_calls"] = usage.UnknownCalls
	return counts, nil
}

func (s *Store) reserveExecution() bool {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.closing {
		return false
	}
	select {
	case s.executionSlots <- struct{}{}:
		s.executionWG.Add(1)
		return true
	default:
		return false
	}
}

func (s *Store) releaseExecution() {
	<-s.executionSlots
	s.executionWG.Done()
}

func (s *Store) launchAttempt(runID, attemptID string, models Models, caseItem Case, actorID string) {
	claim, hasClaim := s.claimForAttempt(attemptID)
	installedClaim := false
	if !hasClaim {
		claim = JobClaim{
			TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseItem.ID,
			RunID: runID, AttemptID: attemptID, ActorID: actorID, Kind: "review",
		}
		s.setClaim(claim)
		installedClaim = true
	}
	go func() {
		defer s.releaseExecution()
		if installedClaim {
			defer s.clearClaim(claim)
		}
		s.executeAttempt(runID, attemptID, models)
	}()
}

func (s *Store) executeAttempt(runID, attemptID string, models Models) {
	s.executeAttemptContext(context.Background(), runID, attemptID, models)
}

func (s *Store) executeAttemptContext(parent context.Context, runID, attemptID string, models Models) {
	ctx, cancel := context.WithTimeout(parent, reviewExecutionLimit)
	defer cancel()
	scope, bound := model.DelegatedScopeFromContext(ctx)
	if !bound || scope.Kind != "execution" {
		if claim, ok := s.claimForAttempt(attemptID); ok {
			ctx = withExecutionClaim(ctx, claim)
			scope, bound = model.DelegatedScopeFromContext(ctx)
		}
	}
	if bound && scope.Kind == "execution" {
		ctx = model.WithDelegatedScope(ctx, modelScope(scope, models))
	}
	s.runningMu.Lock()
	s.running[attemptID] = cancel
	s.runningMu.Unlock()
	defer func() {
		s.runningMu.Lock()
		delete(s.running, attemptID)
		s.runningMu.Unlock()
	}()

	recorder := attemptRecorder{store: s, attemptID: attemptID, failure: &recorderFailure{}}
	if err := s.markAttemptStarted(ctx, runID, attemptID); err != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}
	initial, err := s.snapshotForAttemptContext(ctx, attemptID)
	if err != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}
	frozenScope := initial.snapshotOf(runID)
	if frozenScope.RuleSetHash == engine.SingleContractRuleSetHash() || frozenScope.PlaybookHash == engine.SingleContractPlaybookHash() {
		if !isSingleContractSnapshot(frozenScope) {
			_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "rule_mapping_integrity_failed")
			return
		}
		s.executeSingleContractPrecheck(parent, ctx, runID, attemptID, models, recorder)
		return
	}
	if s.telecomAttempt(initial, runID) {
		telecomRecorder := recorder
		telecomRecorder.runID = runID
		telecomCtx := execution.WithSpan(ctx, attemptID)
		telecomCtx = model.WithCallObserver(telecomCtx, attemptModelObserver{recorder: telecomRecorder})
		s.executeTelecomAttempt(parent, telecomCtx, runID, attemptID, models, telecomRecorder)
		return
	}
	runCtx := execution.WithSpan(ctx, attemptID)
	runCtx = model.WithCallObserver(runCtx, attemptModelObserver{recorder: recorder})
	var docs []Interpreted
	var proposal model.Warranty
	var modelErr error
	rulesPublished := false
	pipelineErr := execution.Run(runCtx, recorder,
		execution.Stage{Name: "input_preparation", Run: func(stageCtx context.Context) error {
			var err error
			docs, err = s.prepareAttempt(stageCtx, runID, attemptID, models)
			return err
		}},
		execution.Stage{Name: "deterministic_rules", Run: func(stageCtx context.Context) error {
			if len(docs) == 0 {
				return errors.New("frozen input is unavailable")
			}
			if err := s.publishRuleResults(stageCtx, runID, attemptID, docs); err != nil {
				return err
			}
			rulesPublished = true
			return nil
		}},
		execution.Stage{Name: "model_review", Run: func(stageCtx context.Context) error {
			if readyCount(docs) < 2 {
				modelErr = errors.New("insufficient_ready_documents")
				return modelErr
			}
			if models == nil {
				modelErr = errModelNotConfigured
				return modelErr
			}
			materials := make([]model.Material, len(docs))
			for i, doc := range docs {
				materials[i] = model.Material{ID: doc.Document.ID, Role: doc.Document.Role, Text: doc.Text, Ready: doc.Ready, ParseError: doc.ParseError}
			}
			proposal, modelErr = models.Warranty(stageCtx, materials)
			if recordErr := recorder.err(); recordErr != nil {
				return recordErr
			}
			return modelErr
		}},
		execution.Stage{Name: "citation_verification", Run: func(stageCtx context.Context) error {
			sources := sourcesOf(docs)
			quotes := make([]engine.QuoteRef, len(proposal.Quotes))
			for i, quote := range proposal.Quotes {
				quotes[i] = engine.QuoteRef{DocumentID: quote.DocumentID, Quote: quote.Quote}
			}
			_, drafts, err := engine.AcceptWarranty(sources, engine.Proposal{
				Claim: proposal.Claim, Relation: proposal.Relation,
				Disposition: proposal.Disposition, Quotes: quotes,
			})
			if err != nil {
				modelErr = err
				return err
			}
			var added []Finding
			if err := s.updateExecutionContext(stageCtx, attemptID, func(next *state) error {
				for _, draft := range drafts {
					idx := -1
					for i := range next.Findings {
						finding := &next.Findings[i]
						if finding.AttemptID == attemptID && finding.Origin == "rule" && finding.Domain == draft.Domain && finding.Relation == draft.Relation && finding.Claim == draft.Claim {
							idx = i
							break
						}
					}
					if idx < 0 {
						made, err := materialize(runIDCase(*next, runID), runID, attemptID, stamp(), []engine.Draft{draft}, docs)
						if err != nil {
							return err
						}
						next.Findings = append(next.Findings, made...)
						added = append(added, made...)
						continue
					}
					finding := &next.Findings[idx]
					finding.DispositionSuggestion = draft.Disposition
					finding.UnverifiedReason = draft.UnverifiedReason
					for _, anchor := range draft.Anchors {
						found := false
						for _, existing := range finding.Anchors {
							if existing.DocumentID == anchor.DocumentID && existing.ParsedDocumentID == parsedDocumentID(docs, anchor.DocumentID) && existing.Quote == anchor.Quote && existing.Status == anchor.Status && existing.Start == anchor.Start && existing.End == anchor.End {
								found = true
								break
							}
						}
						if found {
							continue
						}
						id, err := newID("ev_")
						if err != nil {
							return err
						}
						parsedID, normalizedDigest := parsedIdentity(docs, anchor.DocumentID)
						finding.Anchors = append(finding.Anchors, Anchor{
							ID: id, DocumentID: anchor.DocumentID, ParsedDocumentID: parsedID, NormalizedDigest: normalizedDigest,
							Quote: anchor.Quote, Status: anchor.Status, Start: anchor.Start, End: anchor.End,
							Reason: anchor.Reason, Page: anchor.Page, ElementID: anchor.ElementID,
						})
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if hasVerifiedFinding(added) {
				return s.recordFirstVerifiedFinding(stageCtx, recorder, attemptID)
			}
			return nil
		}},
	)
	if parent.Err() != nil {
		return
	}
	if !rulesPublished || pipelineErr != nil {
		readCtx := ctx
		cancelRead := func() {}
		if ctx.Err() != nil {
			readCtx, cancelRead = context.WithTimeout(parent, attemptFinalizeTimeout)
		}
		latest, err := s.snapshotForAttemptContext(readCtx, attemptID)
		cancelRead()
		if err != nil {
			_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
			return
		}
		if !rulesPublished {
			rulesPublished = len(latest.findingsForAttempt(runID, attemptID)) > 0 || len(latest.evaluationsForAttempt(runID, attemptID)) > 0
		}
		if pipelineErr != nil {
			if current, ok := latest.attemptByID(attemptID); !ok || !attemptActive(current.State) {
				return
			}
		}
	}

	partialReason := ""
	terminalState := "completed"
	errorCode := ""
	if pipelineErr != nil {
		if recorder.err() != nil {
			terminalState = "failed"
			errorCode = "execution_persistence_failed"
		} else if modelErr != nil && rulesPublished {
			terminalState = "partial"
			if errors.Is(modelErr, model.ErrRequest) {
				errorCode = "model_request_failed"
			} else if errors.Is(modelErr, model.ErrUnusable) {
				errorCode = "model_output_unusable"
			} else if errors.Is(modelErr, engine.ErrRejected) {
				errorCode = "model_evidence_rejected"
			} else if errors.Is(modelErr, errModelNotConfigured) {
				errorCode = "model_not_configured"
			} else if errors.Is(modelErr, context.DeadlineExceeded) {
				errorCode = "model_timeout"
			} else if errors.Is(modelErr, context.Canceled) {
				errorCode = "model_cancelled"
			} else {
				errorCode = "model_incomplete"
			}
			partialReason = publicModelError(modelErr)
			if readyCount(docs) < len(docs) {
				partialReason += " 有材料解析失败，结果不能当成完整审查。"
			}
		} else {
			terminalState = "failed"
			errorCode = "execution_failed"
			if rulesPublished {
				errorCode = "execution_persistence_failed"
			}
		}
	} else if readyCount(docs) < len(docs) {
		terminalState = "partial"
		partialReason = "有材料解析失败，结果不能当成完整审查。"
		errorCode = "document_parse_incomplete"
	}
	if err := s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, terminalState, partialReason, errorCode); err != nil {
		return
	}
}

func (s *Store) markAttemptStarted(ctx context.Context, runID, attemptID string) error {
	claim, hasClaim := s.claimForAttempt(attemptID)
	if executionScopeRequiresClaim(ctx) && (!hasClaim || !claimMatchesExecutionScope(ctx, claim)) {
		return errLeaseLost
	}
	if !hasClaim || claim.AttemptID != attemptID || claim.RunID != runID {
		return errExecutionEventScope
	}
	if claim.JobID != "" {
		if claim.Generation <= 0 || claim.stopReason != "" {
			return errExecutionEventScope
		}
		generation := claim.Generation
		return s.scopedAttemptMutationWithTx(ctx, runID, attemptID, false, func(tx pgx.Tx, next *state) error {
			occurred, recorded, err := attemptStartTimestamps(ctx, tx)
			if err != nil {
				return err
			}
			return markAttemptStartedInState(next, runID, attemptID, &generation, occurred, recorded)
		})
	}
	scope := claim.scope()
	var occurred, recorded string
	if err := s.lockExecutionContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	return s.updateStateWith(ctx, scope, attemptID, func(tx pgx.Tx) error {
		var err error
		occurred, recorded, err = attemptStartTimestamps(ctx, tx)
		return err
	}, func(st *state) error {
		return markAttemptStartedInState(st, runID, attemptID, nil, occurred, recorded)
	})
}

func attemptStartTimestamps(ctx context.Context, tx pgx.Tx) (string, string, error) {
	occurred := time.Now().UTC().Format(time.RFC3339Nano)
	var recorded time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&recorded); err != nil {
		return "", "", err
	}
	return occurred, recorded.UTC().Format(time.RFC3339Nano), nil
}

// A live durable generation records at most one start observation. A takeover
// gets its own generation-bound event while preserving the original StartedAt.
func markAttemptStartedInState(st *state, runID, attemptID string, generation *int64, occurred, recorded string) error {
	attemptIndex, runIndex := -1, -1
	for i := range st.Attempts {
		if st.Attempts[i].ID == attemptID && st.Attempts[i].RunID == runID {
			attemptIndex = i
			break
		}
	}
	for i := range st.Runs {
		if st.Runs[i].ID == runID {
			runIndex = i
			break
		}
	}
	if attemptIndex < 0 || runIndex < 0 {
		return errAttemptMissing
	}
	attempt := &st.Attempts[attemptIndex]
	if !attemptActive(attempt.State) {
		return errAttemptTerminal
	}
	const eventKey = "attempt/started"
	for _, event := range st.ExecutionEvents {
		if event.AttemptID != attemptID || event.EventType != "attempt_started" || event.EventKey == nil || *event.EventKey != eventKey {
			continue
		}
		if generation == nil && event.Generation == nil || generation != nil && event.Generation != nil && *generation == *event.Generation {
			if attempt.State != "running" || attempt.StartedAt == nil {
				return errors.New("attempt start event and state differ")
			}
			return nil
		}
	}
	attempt.State = "running"
	if attempt.StartedAt == nil {
		attempt.StartedAt = stringPointer(occurred)
	}
	if st.Runs[runIndex].State != "running" {
		st.Runs[runIndex].State = "running"
		st.Runs[runIndex].UpdatedAt = occurred
		appendRunEvent(st, runID, "running")
	}
	appendExecutionEvent(st, ExecutionEvent{
		AttemptID: attemptID, SpanID: stringPointer(attemptID), NodeKind: "attempt", Phase: "execution",
		EventType: "attempt_started", At: occurred, RecordedAt: stringPointer(recorded), EventKey: stringPointer(eventKey),
		Generation: generation, Status: "running",
	})
	return nil
}

func (s *Store) prepareAttempt(ctx context.Context, runID, attemptID string, models Models) ([]Interpreted, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st, err := s.snapshotForAttemptContext(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	snapshot := st.snapshotOf(runID)
	if input, ok := st.attemptInput(attemptID); ok {
		if input.RunID != runID || input.AttemptID != attemptID {
			return nil, errors.New("attempt frozen input identity mismatch")
		}
		inputHash, err := frozenInputHash(snapshot, input.Documents)
		if err != nil || inputHash != input.InputHash || !frozenInputMatchesSnapshot(snapshot, input.Documents) ||
			!attemptSelectionsMatchFrozen(st.parseSelectionsForAttempt(attemptID), input.Documents) {
			return nil, errors.New("attempt frozen input integrity check failed")
		}
		if err := s.verifyFrozenDocumentsContext(ctx, st, input.Documents); err != nil {
			return nil, fmt.Errorf("attempt parsed artifact integrity check failed: %w", err)
		}
		return interpretedFromFrozen(input.Documents), nil
	}
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
		return nil, errors.New("attempt is not active for input preparation")
	}
	selections := st.parseSelectionsForAttempt(attemptID)
	if len(selections) == 0 || len(selections) != len(snapshot.DocumentIDs) {
		return nil, errors.New("attempt has no complete pinned parse selection")
	}
	frozen := make([]FrozenDocument, len(selections))
	artifacts := make([]ParsedDocument, 0, len(selections))
	requireRemote, documentPreparer := s.remoteDocumentPreparer()
	var claim JobClaim
	if requireRemote {
		if documentPreparer == nil {
			return nil, ErrRemoteDocumentPreparationRequired
		}
		var hasClaim bool
		claim, hasClaim = s.claimForAttempt(attemptID)
		if !hasClaim || claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID != runID {
			return nil, errors.New("remote document preparation requires the current durable job claim")
		}
		if !claimMatchesExecutionScope(ctx, claim) {
			return nil, errLeaseLost
		}
	}
	for i, selection := range selections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if requireRemote {
			prepared, err := documentPreparer.PrepareAttemptDocument(ctx, PrepareDocumentRequest{
				TenantID: claim.TenantID, OrgID: claim.OrgID,
				JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation,
				RunID: runID, AttemptID: attemptID, DocumentID: selection.DocumentID, Selection: selection,
			})
			if err != nil {
				return nil, err
			}
			parsed := parsedDocumentFromFrozen(prepared)
			if prepared.Document.ID != selection.DocumentID || prepared.Document.Digest != selection.OriginalDigest ||
				prepared.Document.CaseID != claim.CaseID || prepared.Document.TenantID != claim.TenantID || prepared.Document.OrgID != claim.OrgID ||
				prepared.OriginalHash != selection.OriginalDigest || parsed.ID != selection.ParsedDocumentID || parsed.SelectionOrder != selection.SelectionOrder ||
				parsed.ParserID != selection.ParserID || parsed.ParserVersion != selection.ParserVersion || parsed.ConfigHash != selection.ConfigHash ||
				parsed.NormalizationVersion != selection.NormalizationVersion || (selection.ArtifactDigest != "" && parsed.ArtifactDigest != selection.ArtifactDigest) {
				return nil, errors.New("remote document response differs from the pinned parse selection")
			}
			if err := verifyParsed(parsed, prepared.Document); err != nil {
				return nil, fmt.Errorf("remote parsed artifact integrity check failed: %w", err)
			}
			frozen[i] = prepared
			bindFrozenMaterialIdentity(st, &frozen[i])
			continue
		}
		doc, ok := st.documentByIdentity(selection.DocumentID, selection.OriginalDigest)
		if !ok || doc.CaseID != runIDCase(st, runID) {
			return nil, errors.New("pinned parse selection original is unavailable")
		}
		parsed, exists := st.parsedByID(selection.ParsedDocumentID)
		if !exists {
			parserID, parserVersion, configHash := parserConfigurationFor(doc, models)
			if parserID != selection.ParserID || parserVersion != selection.ParserVersion || configHash != selection.ConfigHash ||
				selection.NormalizationVersion != docir.NormalizationVersion {
				return nil, errors.New("pinned parser configuration is unavailable")
			}
			interpreted := s.interpretUnpersistedWithModels(ctx, doc, models)
			var err error
			parsed, err = makeParsedWithSelection(doc, interpreted, selection)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, parsed)
		}
		if parsed.ID != selection.ParsedDocumentID || parsed.SelectionOrder != selection.SelectionOrder ||
			parsed.DocumentID != selection.DocumentID || parsed.OriginalDigest != selection.OriginalDigest ||
			parsed.ParserID != selection.ParserID || parsed.ParserVersion != selection.ParserVersion || parsed.ConfigHash != selection.ConfigHash ||
			parsed.NormalizationVersion != selection.NormalizationVersion ||
			(selection.ArtifactDigest != "" && selection.ArtifactDigest != parsed.ArtifactDigest) {
			return nil, errors.New("pinned parse selection differs from its artifact")
		}
		if exists {
			if err := s.verifyParsedRecordContext(ctx, parsed, doc); err != nil {
				return nil, fmt.Errorf("attempt parsed artifact integrity check failed: %w", err)
			}
		} else if err := verifyParsed(parsed, doc); err != nil {
			return nil, fmt.Errorf("attempt parsed artifact integrity check failed: %w", err)
		}
		frozen[i] = frozenFromParsed(doc, parsed)
		bindFrozenMaterialIdentity(st, &frozen[i])
	}
	if !frozenInputMatchesSnapshot(snapshot, frozen) {
		return nil, errors.New("pinned parse selection does not match the run snapshot")
	}
	inputHash, err := frozenInputHash(snapshot, frozen)
	if err != nil {
		return nil, err
	}
	input := AttemptInput{AttemptID: attemptID, RunID: runID, InputHash: inputHash, FrozenAt: stamp(), Documents: frozen}
	if err := s.updateExecutionContext(ctx, attemptID, func(next *state) error {
		if s.telecomAttempt(*next, runID) {
			if _, err := authorizeAttemptExecution(*next, runID, attemptID); err != nil {
				return err
			}
		}
		if !sameRunSignature(next.snapshotOf(runID), snapshot) {
			return errors.New("run snapshot changed during input preparation")
		}
		currentSelections := next.parseSelectionsForAttempt(attemptID)
		if len(currentSelections) != len(selections) {
			return errors.New("pinned parse selection changed during input preparation")
		}
		// Full and claim-scoped readers may return the same pinned selections
		// in insertion or generation order. Their immutable identities must match.
		for _, selection := range selections {
			if !slices.Contains(currentSelections, selection) {
				return errors.New("pinned parse selection changed during input preparation")
			}
		}
		if _, exists := next.attemptInput(attemptID); exists {
			return errors.New("attempt input was frozen concurrently")
		}
		// Parsed artifacts belong to the document service. The remote preparation
		// path above returns the frozen artifact instead of appending here, so
		// this list is only ever populated by a service that owns parsed_documents.
		// Fail closed rather than silently dropping an artifact if that ever
		// changes, because a silent drop would freeze an attempt without the
		// artifact its input hash covers.
		if len(artifacts) > 0 && !ownedTable(s.serviceOwner, "parsed_documents") {
			return errors.New("service cannot persist parsed artifacts it does not own")
		}
		for _, parsed := range artifacts {
			if existing, exists := next.parsedByID(parsed.ID); exists {
				if existing.ArtifactDigest != parsed.ArtifactDigest {
					return errors.New("reserved parsed artifact identity was reused")
				}
				continue
			}
			next.ParsedDocuments = append(next.ParsedDocuments, parsed)
		}
		currentAttempt, exists := next.attemptByID(attemptID)
		if !exists || currentAttempt.RunID != runID || !attemptActive(currentAttempt.State) {
			return errAttemptTerminal
		}
		next.AttemptInputs = append(next.AttemptInputs, input)
		for i := range next.Attempts {
			if next.Attempts[i].ID == attemptID {
				next.Attempts[i].HistoryKind = "frozen"
				next.Attempts[i].InputHash = stringPointer(inputHash)
			}
		}
		return appendAudit(next, runID, "system", "attempt_input_frozen", []string{attemptID})
	}); err != nil {
		return nil, err
	}
	latest, err := s.snapshotForAttemptContext(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if err := s.verifyFrozenDocumentsContext(ctx, latest, frozen); err != nil {
		return nil, fmt.Errorf("attempt parsed artifact integrity check failed: %w", err)
	}
	return interpretedFromFrozen(frozen), nil
}

func (s *Store) publishRuleResults(ctx context.Context, runID, attemptID string, docs []Interpreted) error {
	sources := sourcesOf(docs)
	evaluation := engine.Evaluate(sources)
	extras := engine.CrossFile(sources)
	drafts := engine.Findings(evaluation)
	for _, extra := range extras {
		drafts = append(drafts, engine.Findings(extra)...)
	}
	attemptState, err := s.snapshotForAttemptContext(ctx, attemptID)
	if err != nil {
		return err
	}
	made, err := materialize(runIDCase(attemptState, runID), runID, attemptID, stamp(), drafts, docs)
	if err != nil {
		return err
	}
	madeByDomain := make(map[string]string, len(made))
	for _, finding := range made {
		madeByDomain[finding.Domain] = finding.ID
	}
	var evaluations []ConstraintEval
	appendEval := func(ev engine.Evaluation, findingID string) error {
		id, err := newID("evl_")
		if err != nil {
			return err
		}
		evaluations = append(evaluations, ConstraintEval{
			ID: id, RunID: runID, AttemptID: attemptID, CaseID: runIDCase(attemptState, runID),
			Domain: ev.Domain, Relation: ev.Relation, RuleVersion: ev.RuleVersion, Reason: ev.Reason, FindingID: findingID,
		})
		return nil
	}
	findingID := ""
	if id, ok := madeByDomain[evaluation.Domain]; ok {
		findingID = id
	}
	if err := appendEval(evaluation, findingID); err != nil {
		return err
	}
	for _, ev := range extras {
		id := ""
		if mapped, ok := madeByDomain[ev.Domain]; ok {
			id = mapped
		}
		if err := appendEval(ev, id); err != nil {
			return err
		}
	}
	if err := s.updateExecutionContext(ctx, attemptID, func(st *state) error {
		st.Findings = append(st.Findings, made...)
		st.Evaluations = append(st.Evaluations, evaluations...)
		return nil
	}); err != nil {
		return err
	}
	if hasVerifiedFinding(made) {
		return s.recordFirstVerifiedFinding(ctx, attemptRecorder{store: s, attemptID: attemptID}, attemptID)
	}
	return nil
}

func (s *Store) recordFirstVerifiedFinding(ctx context.Context, recorder attemptRecorder, attemptID string) error {
	st, err := s.snapshotForAttemptContext(ctx, attemptID)
	if err != nil {
		return err
	}
	for _, event := range st.executionEvents(attemptID) {
		if event.EventType == "finding_verified" {
			return nil
		}
	}
	spanCtx, finish, err := execution.StartSpan(ctx, recorder, "citation_verification", "verified_finding", "")
	if err != nil {
		return err
	}
	spanID := execution.ParentSpanID(spanCtx)
	parentID := execution.ParentSpanID(ctx)
	verified := execution.Event{
		SpanID: spanID, ParentSpanID: parentID, NodeKind: "citation_verification", Phase: "verified_finding",
		EventType: "finding_verified", At: time.Now().UTC(), Status: "verified",
	}
	if err := recorder.RecordExecutionEvent(spanCtx, verified); err != nil {
		_ = finish(execution.SpanCompletion{EventType: "span_failed", Status: "failed", ErrorCode: "event_persistence_failed"})
		return err
	}
	return finish(execution.SpanCompletion{EventType: "span_completed", Status: "completed"})
}

// A failed or partial attempt may need to settle after the execution deadline,
// but worker shutdown or lease loss still cancels its bounded cleanup context.
func (s *Store) finishAttemptAfterExecution(parent, workCtx context.Context, runID, attemptID, stateName, partialReason, errorCode string) error {
	if stateName == "completed" {
		return s.finishAttempt(workCtx, runID, attemptID, stateName, partialReason, errorCode)
	}
	ctx, cancel := context.WithTimeout(parent, attemptFinalizeTimeout)
	defer cancel()
	if scope, ok := model.DelegatedScopeFromContext(workCtx); ok {
		ctx = model.WithDelegatedScope(ctx, scope)
	}
	return s.finishAttempt(ctx, runID, attemptID, stateName, partialReason, errorCode)
}

// Wait for the shared projection lock only while the operation remains live.
func (s *Store) lockExecutionContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.mu.TryLock() {
		return nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.mu.TryLock() {
				return nil
			}
		}
	}
}

func withExecutionClaim(ctx context.Context, claim JobClaim) context.Context {
	return model.WithDelegatedScope(ctx, model.DelegatedScope{
		Kind: "execution", TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID,
		ActorID: claim.ActorID, JobID: claim.JobID, RunID: claim.RunID, AttemptID: claim.AttemptID,
		WorkerID: claim.WorkerID, Generation: claim.Generation,
	})
}

func executionScopeRequiresClaim(ctx context.Context) bool {
	scope, ok := model.DelegatedScopeFromContext(ctx)
	return ok && scope.Kind == "execution" && scope.JobID != ""
}

// A delayed cleanup must not adopt another execution's newer in-process claim.
func claimMatchesExecutionScope(ctx context.Context, claim JobClaim) bool {
	scope, ok := model.DelegatedScopeFromContext(ctx)
	if !ok || scope.Kind != "execution" {
		return true
	}
	return scope.TenantID == claim.TenantID && scope.OrgID == claim.OrgID && scope.CaseID == claim.CaseID &&
		scope.ActorID == claim.ActorID && scope.JobID == claim.JobID && scope.RunID == claim.RunID &&
		scope.AttemptID == claim.AttemptID && scope.WorkerID == claim.WorkerID && scope.Generation == claim.Generation
}

func (s *Store) finishAttempt(ctx context.Context, runID, attemptID, stateName, partialReason, errorCode string) error {
	claim, ok := s.claimForAttempt(attemptID)
	if executionScopeRequiresClaim(ctx) && (!ok || !claimMatchesExecutionScope(ctx, claim)) {
		return errLeaseLost
	}
	if ok && claim.JobID != "" {
		if claim.AttemptID != attemptID || claim.RunID != runID {
			return errExecutionEventScope
		}
		// Durable worker path: the terminal transition is committed under the
		// shared case/attempt keys with the attempt version CAS and the claim
		// fence, using the same canonical output hash.
		return s.finishAttemptNarrow(ctx, runID, attemptID, stateName, partialReason, errorCode)
	}
	finishedAt := stamp()
	return s.updateExecutionContext(ctx, attemptID, func(st *state) error {
		attempt, ok := st.attemptByID(attemptID)
		if !ok || !attemptActive(attempt.State) {
			return errAttemptTerminal
		}
		if s.telecomAttempt(*st, runID) {
			if _, err := authorizeAttemptExecution(*st, runID, attemptID); errors.Is(err, errAttemptAuthorizationRevoked) {
				stateName, partialReason, errorCode = "cancelled", "", "authorization_revoked"
			} else if err != nil {
				return err
			}
		}
		for i := range st.Attempts {
			if st.Attempts[i].ID == attemptID {
				st.Attempts[i].State = stateName
				st.Attempts[i].FinishedAt = &finishedAt
				if errorCode != "" {
					code := errorCode
					st.Attempts[i].ErrorCode = &code
				}
				st.Attempts[i].OutputHash = nil
			}
		}
		findings := findingsForAttempt(*st, runID, attemptID)
		if stateName == "completed" && len(findings) > 0 {
			stateName = "completed"
		}
		for i := range st.Runs {
			if st.Runs[i].ID != runID {
				continue
			}
			st.Runs[i].State = stateName
			st.Runs[i].PartialReason = partialReason
			st.Runs[i].FailureMessage = ""
			if stateName == "failed" {
				st.Runs[i].FailureMessage = "这次运行未能完成。"
			}
			if stateName == "completed" && len(findings) > 0 {
				st.Runs[i].State = "awaiting_human"
			}
			st.Runs[i].UpdatedAt = finishedAt
		}
		reviewDigest := ""
		if run, ok := st.runByID(runID); ok {
			if caseItem, ok := st.caseByID(run.CaseID); ok {
				digest, digestErr := s.agentReviewDigestForContext(ctx, caseItem.TenantID, caseItem.OwnerOrgID, attemptID)
				if digestErr != nil {
					return digestErr
				}
				reviewDigest = digest
			}
		}
		outputHash, err := attemptOutputHash(*st, runID, attemptID, reviewDigest)
		if err != nil {
			return err
		}
		for i := range st.Attempts {
			if st.Attempts[i].ID == attemptID {
				st.Attempts[i].OutputHash = &outputHash
			}
		}
		var errorCodePointer *string
		if errorCode != "" {
			errorCodePointer = &errorCode
		}
		appendExecutionEvent(st, ExecutionEvent{
			AttemptID: attemptID, SpanID: stringPointer(attemptID), NodeKind: "attempt",
			Phase: "terminal_persistence", EventType: "attempt_finished", At: finishedAt,
			Status: stateName, ErrorCode: errorCodePointer,
		})
		appendRunEvent(st, runID, stateName)
		return nil
	})
}

func (s *Store) updateExecutionContext(ctx context.Context, attemptID string, fn func(*state) error) error {
	claim, hasClaim := s.claimForAttempt(attemptID)
	if executionScopeRequiresClaim(ctx) && (!hasClaim || !claimMatchesExecutionScope(ctx, claim)) {
		return errLeaseLost
	}
	if !hasClaim || claim.AttemptID != attemptID {
		return errExecutionEventScope
	}
	if claim.JobID != "" {
		// Durable worker path: every attempt mutation (freeze, preparation,
		// results, phase updates, finalize) runs through the bounded
		// attempt-scope adapter with the shared case/attempt locks, the claim
		// fence and the same validation/persist path.
		return s.scopedAttemptMutation(ctx, claim.RunID, attemptID, fn)
	}
	scope := claim.scope()
	if err := s.lockExecutionContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	return s.updateState(ctx, scope, attemptID, fn)
}

func insertExecutionEvent(ctx context.Context, tx pgx.Tx, event ExecutionEvent) error {
	var usage, trace any
	if event.Usage != nil {
		encoded, err := jsonText(event.Usage)
		if err != nil {
			return err
		}
		usage = encoded
	}
	if event.Trace != nil {
		encoded, err := jsonText(event.Trace)
		if err != nil {
			return err
		}
		trace = encoded
	}
	_, err := tx.Exec(ctx, `INSERT INTO execution_events (attempt_id, event_seq, span_id, parent_span_id, node_kind, phase, event_type, occurred_at, duration_ms, status, error_code, model_ref, usage, trace, recorded_at, event_key, generation)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) ON CONFLICT (attempt_id,event_seq) DO NOTHING`,
		event.AttemptID, event.Seq, event.SpanID, event.ParentSpanID, event.NodeKind, event.Phase,
		event.EventType, event.At, event.DurationMS, event.Status, event.ErrorCode, event.ModelRef, usage, trace, event.RecordedAt, event.EventKey, event.Generation)
	return err
}

func (r attemptRecorder) RecordExecutionEvent(ctx context.Context, event execution.Event) error {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	var parentID *string
	if event.ParentSpanID != "" {
		parentID = stringPointer(event.ParentSpanID)
	}
	var spanID *string
	if event.SpanID != "" {
		spanID = stringPointer(event.SpanID)
	}
	var modelRef *string
	if event.ModelRef != "" {
		modelRef = stringPointer(event.ModelRef)
	}
	var errorCode *string
	if event.ErrorCode != "" {
		errorCode = stringPointer(event.ErrorCode)
	}
	var usage *UsageObservation
	if event.Usage != nil {
		usage = &UsageObservation{
			Source: event.Usage.Source, InputTokens: event.Usage.InputTokens,
			OutputTokens: event.Usage.OutputTokens, TotalTokens: event.Usage.TotalTokens,
		}
	}
	stored := ExecutionEvent{
		AttemptID: r.attemptID, SpanID: spanID, ParentSpanID: parentID, NodeKind: event.NodeKind,
		Phase: event.Phase, EventType: event.EventType, At: event.At.UTC().Format(time.RFC3339Nano),
		DurationMS: event.DurationMS, Status: event.Status, ErrorCode: errorCode, ModelRef: modelRef, Usage: usage,
		Trace: event.Trace,
	}
	if event.EventKey != "" {
		stored.EventKey = stringPointer(event.EventKey)
	}
	var err error
	if claim, claimed := r.store.claimForAttempt(r.attemptID); claimed && r.runID != "" &&
		claim.JobID != "" && claim.AttemptID == r.attemptID && claim.RunID == r.runID {
		// The durable worker path appends telemetry through the fenced narrow
		// write: attempt row + job lease + per-attempt sequence, no global lock.
		err = r.store.appendExecutionEventFenced(ctx, r.runID, r.attemptID, stored)
	} else if r.runID != "" {
		err = r.store.updateAuthorizedExecution(ctx, r.runID, r.attemptID, func(st *state) error {
			appendExecutionEvent(st, stored)
			return nil
		})
	} else {
		err = r.store.updateExecutionContext(ctx, r.attemptID, func(st *state) error {
			appendExecutionEvent(st, stored)
			return nil
		})
	}
	if event.NodeKind == "model_call" && (event.EventType == "model_completed" || event.EventType == "model_failed") &&
		(errors.Is(err, errAttemptTerminal) || ctx.Err() != nil) {
		lateCtx := ctx
		cancelLate := func() {}
		if ctx.Err() != nil {
			// Only a late physical model observation may outlive the work context.
			// Retain its claim identity and bound the cancelled-attempt write.
			lateCtx, cancelLate = context.WithTimeout(context.WithoutCancel(ctx), attemptFinalizeTimeout)
		}
		err = r.store.persistLateModelEvent(lateCtx, r.attemptID, stored)
		cancelLate()
	}
	if err == nil {
		r.store.recordTelemetry(r.runID, r.attemptID, event)
	} else if !errors.Is(err, errAttemptTerminal) {
		r.remember(err)
	}
	return err
}

func (r attemptRecorder) StartModelCall(ctx context.Context, modelRef string) (context.Context, func(model.CallObservation)) {
	spanCtx, finish, err := execution.StartSpan(ctx, r, "model_call", "model_request", modelRef)
	if err != nil {
		r.remember(err)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled, func(model.CallObservation) {}
	}
	return spanCtx, func(result model.CallObservation) {
		var usage *execution.Usage
		if result.Usage != nil {
			usage = &execution.Usage{
				Source: result.Usage.Source, InputTokens: result.Usage.InputTokens,
				OutputTokens: result.Usage.OutputTokens, TotalTokens: result.Usage.TotalTokens,
			}
		}
		completion := execution.SpanCompletion{Usage: usage, ModelRef: result.ModelRef, Trace: result.Trace}
		if result.Status == "completed" {
			completion.EventType = "model_completed"
			completion.Status = "completed"
		} else {
			completion.EventType = "model_failed"
			completion.Status = "failed"
			completion.ErrorCode = result.ErrorCode
		}
		r.remember(finish(completion))
	}
}

type recorderFailure struct {
	mu  sync.Mutex
	err error
}

func (f *recorderFailure) remember(err error) {
	if f == nil || err == nil {
		return
	}
	f.mu.Lock()
	if f.err == nil {
		f.err = err
	}
	f.mu.Unlock()
}

func (f *recorderFailure) get() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

type attemptRecorder struct {
	store     *Store
	attemptID string
	runID     string
	failure   *recorderFailure
}

func (r attemptRecorder) err() error         { return r.failure.get() }
func (r attemptRecorder) remember(err error) { r.failure.remember(err) }

type attemptModelObserver struct{ recorder attemptRecorder }

func (o attemptModelObserver) StartModelCall(ctx context.Context, modelRef string) (context.Context, func(model.CallObservation)) {
	return o.recorder.StartModelCall(ctx, modelRef)
}

func (s *Store) persistLateModelEvent(ctx context.Context, attemptID string, event ExecutionEvent) error {
	claim, claimed := s.claimForAttempt(attemptID)
	if scope, scoped := model.DelegatedScopeFromContext(ctx); scoped && scope.Kind == "execution" && scope.JobID != "" &&
		(!claimed || !claimMatchesExecutionScope(ctx, claim)) {
		return errLeaseLost
	}
	if !claimed || claim.AttemptID != attemptID {
		return errExecutionEventScope
	}
	if s.serviceOwner != "" || claim.JobID != "" {
		return s.persistLateModelEventNarrow(ctx, attemptID, event)
	}
	if err := s.lockExecutionContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
		return err
	}
	var runID, stateName string
	var finishedAt *string
	var errorCode *string
	if err := tx.QueryRow(ctx, `SELECT run_id, state, finished_at, error_code FROM review_attempts WHERE id=$1 FOR UPDATE`, attemptID).Scan(&runID, &stateName, &finishedAt, &errorCode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	if stateName != "cancelled" || event.NodeKind != "model_call" || (event.EventType != "model_completed" && event.EventType != "model_failed") {
		return errAttemptTerminal
	}
	// Read the run and job rows instead of assuming their state. A cancel that
	// committed on another process may already have moved them, and this path
	// must never write a guess back into the shared in-memory projection.
	var runState string
	if err := tx.QueryRow(ctx, `SELECT state FROM review_runs WHERE id=$1`, runID).Scan(&runState); err != nil {
		return err
	}
	var jobID, jobState, jobWorkerID string
	var jobGeneration int64
	var jobLease sql.NullTime
	jobErr := tx.QueryRow(ctx, `SELECT id, state, worker_id, generation, lease_until FROM worker_jobs
		WHERE attempt_id=$1 ORDER BY created_at, id LIMIT 1`, attemptID).
		Scan(&jobID, &jobState, &jobWorkerID, &jobGeneration, &jobLease)
	if jobErr != nil && !errors.Is(jobErr, pgx.ErrNoRows) {
		return jobErr
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM execution_events WHERE attempt_id=$1`, attemptID).Scan(&event.Seq); err != nil {
		return err
	}
	if err := insertExecutionEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	next, err := s.st.clone()
	if err != nil {
		return err
	}
	for i := range next.Attempts {
		if next.Attempts[i].ID == attemptID {
			next.Attempts[i].State = stateName
			next.Attempts[i].FinishedAt = finishedAt
			next.Attempts[i].ErrorCode = errorCode
		}
	}
	for i := range next.Runs {
		if next.Runs[i].ID == runID {
			next.Runs[i].State = runState
		}
	}
	if jobErr == nil {
		for i := range next.Jobs {
			if next.Jobs[i].ID == jobID {
				next.Jobs[i].State = jobState
				next.Jobs[i].WorkerID = jobWorkerID
				next.Jobs[i].Generation = jobGeneration
				if jobLease.Valid {
					lease := jobLease.Time
					next.Jobs[i].LeaseUntil = &lease
				} else {
					next.Jobs[i].LeaseUntil = nil
				}
			}
		}
	}
	// The row insert above is idempotent on (attempt_id, event_seq); mirror that
	// in the projection so a repeated late event never doubles in memory.
	duplicate := false
	for _, existing := range next.ExecutionEvents {
		if existing.AttemptID == attemptID && existing.Seq == event.Seq {
			duplicate = true
			break
		}
	}
	if !duplicate {
		next.ExecutionEvents = append(next.ExecutionEvents, event)
	}
	s.st = next
	return nil
}

func (s *Store) persistLateModelEventNarrow(ctx context.Context, attemptID string, event ExecutionEvent) error {
	claim, ok := s.claimForAttempt(attemptID)
	if !ok || claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID == "" || claim.Generation <= 0 {
		return errExecutionEventScope
	}
	if !claimMatchesExecutionScope(ctx, claim) {
		return errLeaseLost
	}
	if event.AttemptID != "" && event.AttemptID != attemptID {
		return errExecutionEventScope
	}
	if event.NodeKind != "model_call" || (event.EventType != "model_completed" && event.EventType != "model_failed") {
		return errAttemptTerminal
	}
	event.AttemptID = attemptID
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		return err
	}
	if err := scopedCaseLock(ctx, tx, claim.CaseID); err != nil {
		return err
	}
	if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
		return err
	}
	var runID, stateName string
	if err := tx.QueryRow(ctx, `SELECT run_id, state FROM review_attempts WHERE id=$1 FOR UPDATE`, attemptID).Scan(&runID, &stateName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttemptMissing
		}
		return err
	}
	if runID != claim.RunID {
		return errExecutionEventScope
	}
	if stateName != "cancelled" {
		return errAttemptTerminal
	}
	var jobState string
	if err := tx.QueryRow(ctx, `SELECT state FROM worker_jobs
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND run_id=$5 AND attempt_id=$6
			AND worker_id=$7 AND generation=$8
			AND (state='cancelled' OR (state='running' AND lease_until > clock_timestamp()))
		FOR UPDATE`, claim.JobID, claim.TenantID, claim.OrgID, claim.CaseID, claim.RunID, attemptID, claim.WorkerID, claim.Generation).Scan(&jobState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errLeaseLost
		}
		return err
	}
	if jobState != "cancelled" && jobState != "running" {
		return errLeaseLost
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(event_seq),0)+1 FROM execution_events WHERE attempt_id=$1`, attemptID).Scan(&event.Seq); err != nil {
		return err
	}
	if err := insertExecutionEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := s.lockExecutionContext(ctx); err != nil {
		return nil // The authoritative event was already committed.
	}
	s.st.ExecutionEvents = mergeExecutionEventSlice(s.st.ExecutionEvents, []ExecutionEvent{event})
	s.mu.Unlock()
	return nil
}

func (s *Store) markUnfinishedInterrupted(st state) error {
	// Durable jobs are recovered by their queue lease; only in-process attempts
	// without an active durable job are interrupted on restart.
	durable := make(map[string]struct{})
	for _, job := range st.Jobs {
		if job.AttemptID != "" && (job.State == "queued" || job.State == "running") {
			durable[job.AttemptID] = struct{}{}
		}
	}
	for _, attempt := range st.Attempts {
		if !attemptActive(attempt.State) {
			continue
		}
		if _, ok := durable[attempt.ID]; ok {
			continue
		}
		run, ok := st.runByID(attempt.RunID)
		if !ok {
			return errors.New("restart recovery attempt has no run")
		}
		item, ok := st.caseByID(run.CaseID)
		if !ok || item.TenantID == "" {
			return errors.New("restart recovery attempt has no tenant-scoped case")
		}
		scope := dbScope{ActorID: "restart-recovery", Role: "api", TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID}
		if err := s.interruptAttempt(context.Background(), attempt.ID, run.ID, scope); err != nil {
			return err
		}
	}
	return nil
}

// interruptAttempt runs restart recovery for exactly one attempt in its own
// transaction, with the shared case and attempt keys and the attempt row lock, so
// it cannot interleave with a narrow attempt writer or a case transition. The
// attempt is re-read under those locks; an attempt that became terminal or
// finished in the meantime is left untouched.
func (s *Store) interruptAttempt(ctx context.Context, attemptID, runID string, scope dbScope) error {
	return s.updateState(ctx, scope, attemptID, func(next *state) error {
		finishedAt := stamp()
		code := "process_interrupted"
		var runIDForEvent string
		interrupted := false
		for i := range next.Attempts {
			if next.Attempts[i].ID != attemptID || next.Attempts[i].RunID != runID || !attemptActive(next.Attempts[i].State) {
				continue
			}
			next.Attempts[i].State = "interrupted"
			next.Attempts[i].FinishedAt = &finishedAt
			next.Attempts[i].ErrorCode = &code
			runIDForEvent = next.Attempts[i].RunID
			interrupted = true
		}
		if !interrupted {
			return nil
		}
		for i := range next.Runs {
			if next.Runs[i].ID == runIDForEvent {
				next.Runs[i].State = "interrupted"
				next.Runs[i].UpdatedAt = finishedAt
			}
		}
		appendExecutionEvent(next, ExecutionEvent{
			AttemptID: attemptID, SpanID: stringPointer(attemptID), NodeKind: "attempt", Phase: "restart_recovery",
			EventType: "attempt_interrupted", At: finishedAt, Status: "interrupted", ErrorCode: &code,
		})
		appendRunEvent(next, runIDForEvent, "interrupted")
		return nil
	})
}

func (st state) latestAttempt(runID string) (Attempt, bool) {
	var latest Attempt
	ok := false
	for _, attempt := range st.Attempts {
		if attempt.RunID == runID && (!ok || attempt.Ordinal > latest.Ordinal) {
			latest, ok = attempt, true
		}
	}
	return latest, ok
}

func (st state) attemptByID(attemptID string) (Attempt, bool) {
	for _, attempt := range st.Attempts {
		if attempt.ID == attemptID {
			return attempt, true
		}
	}
	return Attempt{}, false
}

func (st state) attemptInput(attemptID string) (AttemptInput, bool) {
	for _, input := range st.AttemptInputs {
		if input.AttemptID == attemptID {
			return input, true
		}
	}
	return AttemptInput{}, false
}

func (st state) runByID(runID string) (Run, bool) {
	for _, run := range st.Runs {
		if run.ID == runID {
			return run, true
		}
	}
	return Run{}, false
}

func (st state) executionEvents(attemptID string) []ExecutionEvent {
	var out []ExecutionEvent
	for _, event := range st.ExecutionEvents {
		if event.AttemptID == attemptID {
			out = append(out, event)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func (st state) documentsForAttempt(run Run, attemptID string) []Interpreted {
	if input, ok := st.attemptInput(attemptID); ok {
		return interpretedFromFrozen(input.Documents)
	}
	out := make([]Interpreted, 0)
	for _, doc := range st.runDocuments(run) {
		out = append(out, Interpreted{Document: doc})
	}
	return out
}

func (st state) findingsForAttempt(runID, attemptID string) []Finding {
	var out []Finding
	for _, finding := range st.Findings {
		if finding.RunID == runID && finding.AttemptID == attemptID {
			out = append(out, finding)
		}
	}
	return out
}

func (st state) evaluationsForAttempt(runID, attemptID string) []ConstraintEval {
	var out []ConstraintEval
	for _, evaluation := range st.Evaluations {
		if evaluation.RunID == runID && evaluation.AttemptID == attemptID {
			out = append(out, evaluation)
		}
	}
	return out
}

func findingsForAttempt(st state, runID, attemptID string) []Finding {
	return st.findingsForAttempt(runID, attemptID)
}

func appendRunEvent(st *state, runID, stateName string) {
	seq := 0
	for _, event := range st.Events {
		if event.RunID == runID && event.Seq > seq {
			seq = event.Seq
		}
	}
	st.Events = append(st.Events, Event{RunID: runID, Seq: seq + 1, Type: "state", State: stateName})
}

func appendExecutionEvent(st *state, event ExecutionEvent) {
	maxSeq := 0
	for _, previous := range st.ExecutionEvents {
		if previous.AttemptID == event.AttemptID && previous.Seq > maxSeq {
			maxSeq = previous.Seq
		}
	}
	event.Seq = maxSeq + 1
	st.ExecutionEvents = append(st.ExecutionEvents, event)
}

func acceptedEvent(attempt Attempt) ExecutionEvent {
	return ExecutionEvent{
		Seq: 1, AttemptID: attempt.ID, SpanID: stringPointer(attempt.ID), NodeKind: "attempt",
		Phase: "acceptance", EventType: "attempt_accepted", At: attempt.AcceptedAt, Status: "accepted",
	}
}

func attemptActive(state string) bool { return state == "accepted" || state == "running" }

func materialize(caseID, runID, attemptID, now string, drafts []engine.Draft, sources ...[]Interpreted) ([]Finding, error) {
	var docs []Interpreted
	if len(sources) > 0 {
		docs = sources[0]
	}
	out := make([]Finding, 0, len(drafts))
	for _, draft := range drafts {
		id, err := newID("find_")
		if err != nil {
			return nil, err
		}
		origin := draft.Origin
		if origin == "" {
			origin = "rule"
		}
		finding := Finding{
			ID: id, RunID: runID, AttemptID: attemptID, CaseID: caseID, Origin: origin, Domain: draft.Domain,
			Claim: draft.Claim, Relation: draft.Relation, DispositionSuggestion: draft.Disposition,
			UnverifiedReason: draft.UnverifiedReason, Version: 1, CreatedAt: now,
		}
		for _, anchor := range draft.Anchors {
			ev, err := newID("ev_")
			if err != nil {
				return nil, err
			}
			parsedID, normalizedDigest := parsedIdentity(docs, anchor.DocumentID)
			finding.Anchors = append(finding.Anchors, Anchor{
				ID: ev, DocumentID: anchor.DocumentID, ParsedDocumentID: parsedID, NormalizedDigest: normalizedDigest,
				Quote: anchor.Quote, Status: anchor.Status, Start: anchor.Start, End: anchor.End,
				Reason: anchor.Reason, Page: anchor.Page, ElementID: anchor.ElementID,
			})
		}
		if finding.Anchors == nil {
			finding.Anchors = []Anchor{}
		}
		out = append(out, finding)
	}
	return out, nil
}

func parsedIdentity(docs []Interpreted, documentID string) (string, string) {
	for _, doc := range docs {
		if doc.Document.ID == documentID && doc.ParsedDocument != nil {
			return doc.ParsedDocument.ID, doc.ParsedDocument.NormalizedDigest
		}
	}
	return "", ""
}

func parsedDocumentID(docs []Interpreted, documentID string) string {
	id, _ := parsedIdentity(docs, documentID)
	return id
}

func interpretedFromFrozen(frozen []FrozenDocument) []Interpreted {
	out := make([]Interpreted, len(frozen))
	for i, item := range frozen {
		var parsed *ParsedDocument
		if item.ParsedDocumentID != "" {
			normalizedDigest := ""
			if item.NormalizedHash != nil {
				normalizedDigest = *item.NormalizedHash
			}
			parsed = &ParsedDocument{
				ID: item.ParsedDocumentID, DocumentID: item.Document.ID, OriginalDigest: item.OriginalHash,
				ParserID: item.ParserID, ParserVersion: item.ParserVersion, ConfigHash: item.ConfigHash, SelectionOrder: item.SelectionOrder,
				NormalizationVersion: item.NormalizationVersion, NormalizedDigest: normalizedDigest,
				IRDigest: item.IRDigest, ArtifactDigest: item.ParsedArtifactDigest, CreatedAt: item.ParsedCreatedAt,
				Status: item.ParsedStatus, Text: item.Text, IR: item.IR, ParseError: item.ParseError, FromOCR: item.FromOCR,
			}
		}
		out[i] = Interpreted{
			Document: item.Document, Ready: item.Ready, FromOCR: item.FromOCR,
			ParseError: item.ParseError, Text: item.Text, IR: item.IR, ParsedDocument: parsed,
		}
	}
	return out
}

func frozenInputHash(snapshot RunSnapshot, docs []FrozenDocument) (string, error) {
	snapshot = scopeSnapshot(snapshot)
	snapshot.RunID = ""
	sort.Strings(snapshot.DocumentIDs)
	sort.Strings(snapshot.ParsedDocumentIDs)
	raw, err := json.Marshal(struct {
		Snapshot  RunSnapshot      `json:"snapshot"`
		Documents []FrozenDocument `json:"documents"`
	}{Snapshot: snapshot, Documents: docs})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func frozenInputMatchesSnapshot(snapshot RunSnapshot, docs []FrozenDocument) bool {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ParsedDocumentID
	}
	return sameDocumentSet(snapshot.ParsedDocumentIDs, ids) &&
		snapshot.ParseSelectionHash == selectionHash(docs) &&
		snapshot.ParserConfigurationID == frozenConfigHash(docs)
}

// attemptOutputHash derives the canonical output identity of one attempt. The
// caller supplies the verified agent-review digest of the same attempt (empty
// when the attempt has no agent-flow record), so a persisted projection is part
// of the frozen output and can never be swapped afterwards.
func attemptOutputHash(st state, runID, attemptID, agentReviewDigest string) (string, error) {
	findings := st.findingsForAttempt(runID, attemptID)
	attempt, _ := st.attemptByID(attemptID)
	for i := range findings {
		// Human decision concurrency versions are not part of the frozen rule output.
		findings[i].Version = 0
	}
	raw, err := json.Marshal(struct {
		Findings               []Finding        `json:"findings"`
		Evaluations            []ConstraintEval `json:"evaluations"`
		RulePackHash           string           `json:"rulepack_hash,omitempty"`
		RulePackSnapshotDigest string           `json:"rulepack_snapshot_digest,omitempty"`
		RulePackCompilerRef    string           `json:"rulepack_compiler_ref,omitempty"`
		AgentReviewSHA256      string           `json:"agent_review_sha256,omitempty"`
	}{
		Findings: findings, Evaluations: st.evaluationsForAttempt(runID, attemptID),
		RulePackHash:           attempt.RulePackHash,
		RulePackSnapshotDigest: attempt.RulePackSnapshotDigest,
		RulePackCompilerRef:    attempt.RulePackCompilerRef,
		AgentReviewSHA256:      agentReviewDigest,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// agentReviewDigestFor reads the verified projection digest of one attempt
// outside a transaction. It returns empty when the store has no pool (pure state
// tests) or the attempt has no record.
func (s *Store) agentReviewDigestFor(tenantID, orgID, attemptID string) (string, error) {
	return s.agentReviewDigestForContext(context.Background(), tenantID, orgID, attemptID)
}

func (s *Store) agentReviewDigestForContext(ctx context.Context, tenantID, orgID, attemptID string) (string, error) {
	if s == nil || s.pool == nil || attemptID == "" || tenantID == "" || orgID == "" {
		return "", nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "system", Role: "api", TenantID: tenantID, OrgID: orgID}); err != nil {
		return "", err
	}
	return agentReviewDigestForOutput(ctx, tx, tenantID, orgID, attemptID)
}

func hasVerifiedFinding(findings []Finding) bool {
	for _, finding := range findings {
		for _, anchor := range finding.Anchors {
			if anchor.Status == "located" {
				return true
			}
		}
	}
	return false
}

func runIDCase(st state, runID string) string {
	run, _ := st.runByID(runID)
	return run.CaseID
}

func stringPointer(value string) *string { return &value }

func eventAttemptIDs(st state, runID string) []string {
	var ids []string
	for _, attempt := range st.Attempts {
		if attempt.RunID == runID {
			ids = append(ids, attempt.ID)
		}
	}
	return ids
}

func selectedExecution(st state, runID, selectedID string) *Execution {
	allAttempts := make([]Attempt, 0)
	for _, attempt := range st.Attempts {
		if attempt.RunID == runID {
			allAttempts = append(allAttempts, attempt)
		}
	}
	if len(allAttempts) == 0 {
		return nil
	}
	sort.Slice(allAttempts, func(i, j int) bool { return allAttempts[i].Ordinal < allAttempts[j].Ordinal })
	for i := range allAttempts {
		allAttempts[i].ParsedDocumentIDs = attemptParsedDocumentIDs(st, allAttempts[i])
	}
	if selectedID == "" {
		selectedID = allAttempts[len(allAttempts)-1].ID
	}
	var selected *Attempt
	for i := range allAttempts {
		if allAttempts[i].ID == selectedID {
			selected = &allAttempts[i]
			break
		}
	}
	if selected == nil {
		return nil
	}
	events := st.executionEvents(selectedID)
	return &Execution{
		SchemaVersion: "audit-execution/v1", Attempt: selected, Attempts: allAttempts, Events: events,
		AgentReview:      nil,
		Usage:            usageSummary(st.ExecutionEvents, []string{selectedID}),
		AllAttemptsUsage: usageSummary(st.ExecutionEvents, eventAttemptIDs(st, runID)),
		DurationMS:       attemptDuration(*selected), FirstVerifiedFindingMS: firstVerifiedDuration(*selected, events),
		QueueWaitMS: nil,
	}
}

func attemptParsedDocumentIDs(st state, attempt Attempt) []string {
	ids := []string{}
	if attempt.HistoryKind != "frozen" {
		return ids
	}
	input, ok := st.attemptInput(attempt.ID)
	if !ok {
		return ids
	}
	for _, frozen := range input.Documents {
		if !validParsedID(frozen.ParsedDocumentID) || frozen.ParsedArtifactDigest == "" || frozen.ConfigHash == "" {
			continue
		}
		parsed, ok := st.parsedByID(frozen.ParsedDocumentID)
		if !ok || parsed.DocumentID != frozen.Document.ID || parsed.ArtifactDigest != frozen.ParsedArtifactDigest {
			continue
		}
		ids = append(ids, frozen.ParsedDocumentID)
	}
	return ids
}

func attemptDuration(attempt Attempt) *int64 {
	if attempt.FinishedAt == nil {
		return nil
	}
	start, errStart := time.Parse(time.RFC3339Nano, attempt.AcceptedAt)
	if errStart != nil {
		start, errStart = time.Parse(timeLayout, attempt.AcceptedAt)
	}
	finish, errFinish := time.Parse(time.RFC3339Nano, *attempt.FinishedAt)
	if errFinish != nil {
		finish, errFinish = time.Parse(timeLayout, *attempt.FinishedAt)
	}
	if errStart != nil || errFinish != nil || finish.Before(start) {
		return nil
	}
	value := finish.Sub(start).Milliseconds()
	return &value
}

func firstVerifiedDuration(attempt Attempt, events []ExecutionEvent) *int64 {
	accepted, err := time.Parse(time.RFC3339Nano, attempt.AcceptedAt)
	if err != nil {
		accepted, err = time.Parse(timeLayout, attempt.AcceptedAt)
	}
	if err != nil {
		return nil
	}
	for _, event := range events {
		if event.EventType != "finding_verified" {
			continue
		}
		verified, err := time.Parse(time.RFC3339Nano, event.At)
		if err != nil {
			verified, err = time.Parse(timeLayout, event.At)
		}
		if err != nil || verified.Before(accepted) {
			return nil
		}
		value := verified.Sub(accepted).Milliseconds()
		return &value
	}
	return nil
}

func usageSummary(events []ExecutionEvent, attemptIDs []string) UsageSummary {
	wanted := make(map[string]bool, len(attemptIDs))
	for _, id := range attemptIDs {
		wanted[id] = true
	}
	filter := attemptIDs != nil
	var calls int
	var unknown int
	var inputSum, outputSum, totalSum int64
	var inputKnown, outputKnown, totalKnown bool
	var inputAny, outputAny, totalAny bool
	sources := map[string]bool{}
	for _, event := range events {
		if filter && !wanted[event.AttemptID] {
			continue
		}
		if event.NodeKind != "model_call" || (event.EventType != "model_completed" && event.EventType != "model_failed") {
			continue
		}
		calls++
		if calls == 1 {
			inputKnown, outputKnown, totalKnown = true, true, true
		}
		usage := event.Usage
		if usage != nil && usage.Source != "" {
			sources[usage.Source] = true
		}
		if usage == nil || usage.InputTokens == nil && usage.OutputTokens == nil && usage.TotalTokens == nil {
			unknown++
			inputKnown, outputKnown, totalKnown = false, false, false
			continue
		}
		if usage.InputTokens != nil {
			inputAny = true
			inputSum += *usage.InputTokens
		} else {
			inputKnown = false
		}
		if usage.OutputTokens != nil {
			outputAny = true
			outputSum += *usage.OutputTokens
		} else {
			outputKnown = false
		}
		if usage.TotalTokens != nil {
			totalAny = true
			totalSum += *usage.TotalTokens
		} else {
			totalKnown = false
		}
	}
	var input, output, total *int64
	if calls > 0 && inputKnown {
		input = &inputSum
	}
	if calls > 0 && outputKnown {
		output = &outputSum
	}
	if calls > 0 && totalKnown {
		total = &totalSum
	}
	var inputSubtotal, outputSubtotal, totalSubtotal *int64
	if inputAny {
		inputSubtotal = &inputSum
	}
	if outputAny {
		outputSubtotal = &outputSum
	}
	if totalAny {
		totalSubtotal = &totalSum
	}
	sourceList := make([]string, 0, len(sources))
	for source := range sources {
		sourceList = append(sourceList, source)
	}
	sort.Strings(sourceList)
	return UsageSummary{
		InputTokens: input, OutputTokens: output, TotalTokens: total, Calls: calls, UnknownCalls: unknown,
		Sources:        sourceList,
		KnownSubtotals: KnownTokenSubtotals{InputTokens: inputSubtotal, OutputTokens: outputSubtotal, TotalTokens: totalSubtotal},
		Cost:           nil,
	}
}

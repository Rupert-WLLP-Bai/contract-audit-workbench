package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/docversion"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

var (
	errAgentReviewPersistence  = errors.New("agent review persistence failed")
	errAgentReviewScopeChanged = errors.New("agent review case scope changed")
)

func (s *Store) runTelecomAgentReview(ctx context.Context, runID, attemptID string, docs []Interpreted, models Models, recorder attemptRecorder) (bool, error) {
	claim, durable := s.claimForAttempt(attemptID)
	if !durable || claim.JobID == "" || claim.RunID != runID || claim.AttemptID != attemptID {
		return false, nil
	}
	caseItem, principal, resource, err := s.authorizeAgentReviewStage(runID, attemptID)
	if err != nil {
		return true, err
	}
	st := s.snapshotForAttempt(attemptID)
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
		return true, errAttemptTerminal
	}
	input, ok := st.attemptInput(attemptID)
	if !ok || input.RunID != runID || input.InputHash == "" {
		return true, errors.New("frozen attempt input identity is unavailable for the agent review")
	}
	configuration, required, err := frozenTelecomPlannerConfiguration(attempt)
	if !required {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	actor, err := authorizeAttemptExecution(st, runID, attemptID)
	if err != nil || actor.ID != principal.UserID {
		if err == nil {
			err = errAttemptAuthorizationRevoked
		}
		return true, err
	}
	frozenKnowledge, err := copyGovernedKnowledge(st.snapshotOf(runID))
	if err != nil {
		return true, err
	}
	authorizePlanning := func(ctx context.Context) error {
		return s.revalidateAgentPlannerKnowledge(ctx, runID, attemptID, resource, actor.ID, frozenKnowledge)
	}
	if err := authorizePlanning(ctx); err != nil {
		return true, err
	}
	selectedKnowledge := map[KnowledgeRef]bool{}
	readKnowledge := func(ctx context.Context, read agentflow.KnowledgeReadRequest) (string, error) {
		return s.readAgentPlannerKnowledge(ctx, runID, attemptID, resource, actor.ID, frozenKnowledge, selectedKnowledge, read)
	}
	planner, err := frozenAgentPlanner(configuration, models, recorder)
	if err != nil {
		return true, err
	}
	tools := append(agentflow.BaselineTools(), agentflow.SourceFactTools()...)
	runtime, err := agentflow.New(ctx, agentflow.Config{
		Planner: planner, Tools: tools, PlannerConfiguration: configuration,
		EventSink: agentReviewEventSink(recorder), AuthorizePlanning: authorizePlanning, ReadKnowledge: readKnowledge,
	})
	if err != nil {
		return true, err
	}
	sourceSnapshot, evidence, err := agentReviewEvidence(st, input, docs)
	if err != nil {
		return true, err
	}
	request := agentflow.Request{
		RequestID: attemptID, AttemptID: attemptID, Principal: principal, Case: resource,
		Scope: agentflow.Domains(), Gaps: telecomAgentReviewGaps(), Evidence: evidence,
		SourceSnapshot: sourceSnapshot, CanonicalEvaluations: telecomAgentReviewEvaluations(st, runID, attemptID),
	}
	if err := s.applyPriorAttemptRecovery(ctx, st, actor, caseItem, attempt, tools, &request); err != nil {
		return true, err
	}
	// A worker takeover resumes the same attempt with a newer lease generation.
	// Preserve the event sequence and elapsed budget already spent by the
	// previous generation; no tool output is fabricated from those events.
	if attempt.RetryOfAttemptID == nil && claim.Generation > 1 {
		if recovered := st.executionEvents(attemptID); len(recovered) > 0 {
			count, elapsed := priorAttemptUsage(recovered, attempt)
			request.PriorEventCount = count
			request.PriorElapsedMS = elapsed
		}
	}
	rulePackIdentity, err := agentReviewRulePackIdentity(attempt, caseItem)
	if err != nil {
		return true, err
	}
	request.RulePack = rulePackIdentity
	for _, event := range st.executionEvents(attemptID) {
		if event.NodeKind == "agent_review" && event.EventType == "agent_review_event" && event.Phase == "knowledge_read_started" && event.Status == "running" {
			request.PriorKnowledgeReadCount++
		}
	}
	if request.PriorKnowledgeReadCount > agentflow.KnowledgeReadMaxCalls {
		return true, errors.New("governed knowledge read budget exceeded")
	}
	reserved := request.PriorKnowledgeReadCount * agentflow.KnowledgeReadMaxBytes
	contextBudget := max(0, configuration.Limits.MaxEvidenceBytes-reserved)
	request.Knowledge, request.KnowledgeSelection, err = governedPlannerKnowledge(frozenKnowledge, request.Gaps, request.Evidence, contextBudget)
	if err != nil {
		return true, err
	}
	for _, ref := range request.Knowledge {
		selectedKnowledge[KnowledgeRef{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256}] = true
	}
	request.KnowledgeSelection.Budget.PriorReadCalls = request.PriorKnowledgeReadCount
	request.KnowledgeSelection.Budget.PriorReadReservedBytes = reserved
	report, runErr := runtime.Run(ctx, request)
	if errors.Is(runErr, agentflow.ErrEventSink) || errors.Is(runErr, agentflow.ErrPlanningAuthorization) || errors.Is(runErr, agentflow.ErrKnowledgeRead) {
		return true, runErr
	}
	if errors.Is(runErr, agentflow.ErrDenied) {
		return true, rulepack.ErrUnauthorized
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if report.Projection == nil {
		if runErr != nil {
			return true, runErr
		}
		return true, errors.New("agent review did not produce a persistable projection")
	}
	projectionBytes, err := json.Marshal(report.Projection)
	if err != nil {
		return true, err
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	digest := sha256.Sum256(projectionBytes)
	record := AgentReviewRecord{
		TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID,
		RunID: claim.RunID, AttemptID: claim.AttemptID, Generation: claim.Generation,
		AttemptInputSHA256: input.InputHash, AgentReviewSHA256: hex.EncodeToString(digest[:]), Review: projectionBytes,
	}
	if err := s.SyncAgentReviewRecord(ctx, claim, record); err != nil {
		return true, errors.Join(errAgentReviewPersistence, err)
	}
	incomplete := report.Status != "completed" || runErr != nil || caseItem.ID != resource.CaseID
	return incomplete, nil
}

// The recorder owns transaction, lease and generation fencing. The callback
// blocks runtime progress until the event has been committed or rejected.
func agentReviewEventSink(recorder attemptRecorder) func(context.Context, agentflow.Event) error {
	return func(ctx context.Context, event agentflow.Event) error {
		status := "completed"
		switch event.Kind {
		case "tool_started", "knowledge_read_started":
			status = "running"
		case "tool_failed", "knowledge_read_failed", "plan_rejected", "gap_unsupported":
			status = "failed"
		case "planner_fallback":
			status = "partial"
		case "run_cancelled":
			status = "cancelled"
		}
		var duration *int64
		if event.DurationMS > 0 {
			value := event.DurationMS
			duration = &value
		}
		return recorder.RecordExecutionEvent(ctx, execution.Event{
			EventKey:     fmt.Sprintf("agent-review/%d", event.Sequence),
			ParentSpanID: execution.ParentSpanID(ctx), NodeKind: "agent_review", Phase: event.Kind,
			EventType: "agent_review_event", At: event.OccurredAt, DurationMS: duration, Status: status,
			ErrorCode: event.Code,
		})
	}
}

func agentReviewRulePackIdentity(attempt Attempt, caseItem Case) (*agentflow.RulePackIdentity, error) {
	hasIdentityFields := attempt.RulePackID != "" || attempt.RulePackVersion != "" || attempt.RulePackPointerRevision != 0 ||
		attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" || len(attempt.RulePackSnapshot) != 0
	switch {
	case attempt.RulePackCompilerRef == governedCompilerMarker:
		pack, err := ParseFrozenGovernedRulePack(attempt, caseItem)
		if err != nil {
			return nil, err
		}
		if pack == nil {
			return nil, errors.New("frozen governed RulePack is unavailable for the agent review")
		}
		manifest := pack.Manifest()
		return &agentflow.RulePackIdentity{ID: manifest.ID, Version: manifest.Version, ContentSHA256: pack.Hash()}, nil
	case telecom.IsSyntheticCompiler(attempt.RulePackCompilerRef):
		if attempt.RulePackID != "" || attempt.RulePackVersion != "" || attempt.RulePackPointerRevision != 0 {
			return nil, errors.New("synthetic telecom RulePack carries governed identity fields")
		}
		if len(attempt.RulePackSnapshot) == 0 {
			if attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
				return nil, errors.New("synthetic telecom RulePack hashes have no frozen snapshot")
			}
			return nil, nil
		}
		pack, err := rulepack.Parse(attempt.RulePackSnapshot)
		if err != nil || pack == nil || pack.Manifest().Lifecycle.State != rulepack.StatePublished ||
			pack.Hash() != attempt.RulePackHash || sha256Bytes(attempt.RulePackSnapshot) != attempt.RulePackSnapshotDigest {
			return nil, errors.New("synthetic telecom RulePack identity does not match its frozen snapshot")
		}
		manifest := pack.Manifest()
		return &agentflow.RulePackIdentity{ID: manifest.ID, Version: manifest.Version, ContentSHA256: pack.Hash()}, nil
	case attempt.RulePackCompilerRef == "":
		if hasIdentityFields {
			return nil, errors.New("frozen RulePack fields have no compiler identity")
		}
		return nil, nil
	default:
		return nil, errors.New("frozen RulePack compiler identity is unknown")
	}
}

func frozenAgentPlanner(configuration agentflow.PlannerConfiguration, models Models, recorder attemptRecorder) (agentflow.Planner, error) {
	switch configuration.Mode {
	case agentflow.ModeFixedChain:
		return agentflow.FixedChainPlanner{}, nil
	case agentflow.ModeDeterministic:
		return agentflow.ScopePlanner{}, nil
	case agentflow.ModeModelFixture, agentflow.ModeModelLive:
		if models == nil || models.ConfigurationID() != configuration.ModelConfigurationID {
			return nil, errors.New("frozen planner model identity is unavailable")
		}
		chat, ok := any(models).(agentflow.PlannerChatModel)
		if !ok {
			return nil, errors.New("configured model does not implement the planner chat surface")
		}
		observed := observedPlannerChatModel{inner: chat, observer: recorder}
		return agentflow.ModelPlanner{Model: observed, Identity: models.Name()}, nil
	default:
		return nil, errors.New("frozen planner mode is unknown")
	}
}

func frozenTelecomPlannerConfiguration(attempt Attempt) (agentflow.PlannerConfiguration, bool, error) {
	configuration, err := FrozenPlannerConfiguration(attempt)
	if errors.Is(err, ErrFrozenPlannerAbsent) {
		return agentflow.PlannerConfiguration{}, false, nil
	}
	if err != nil {
		return agentflow.PlannerConfiguration{}, true, err
	}
	return configuration, true, nil
}

type observedPlannerChatModel struct {
	inner    agentflow.PlannerChatModel
	observer model.CallObserver
}

func (m observedPlannerChatModel) Generate(ctx context.Context, messages []*schema.Message, options ...einomodel.Option) (*schema.Message, error) {
	return m.inner.Generate(model.WithCallObserver(ctx, m.observer), messages, options...)
}

var _ agentflow.PlannerChatModel = observedPlannerChatModel{}
var _ model.CallObserver = attemptRecorder{}

func (s *Store) authorizeAgentReviewStage(runID, attemptID string) (Case, access.Principal, access.Resource, error) {
	st := s.snapshotForAttempt(attemptID)
	run, ok := st.runByID(runID)
	if !ok {
		return Case{}, access.Principal{}, access.Resource{}, errors.New("agent review run is unavailable")
	}
	caseItem, ok := st.caseByID(run.CaseID)
	if !ok || !isGovernedReviewCase(caseItem) {
		return Case{}, access.Principal{}, access.Resource{}, errAgentReviewScopeChanged
	}
	user, err := authorizeAttemptExecution(st, runID, attemptID)
	if err != nil {
		return Case{}, access.Principal{}, access.Resource{}, err
	}
	principal := bindLocalDemoCaseGrant(caseItem, user, accounts.Principal(user))
	resource := access.Resource{TenantID: caseItem.TenantID, CaseID: caseItem.ID, OrgID: caseItem.OwnerOrgID, OwnerID: caseItem.OwnerID}
	if !access.Check(access.Review, principal, resource, time.Now().UTC()).Allowed {
		return Case{}, access.Principal{}, access.Resource{}, rulepack.ErrUnauthorized
	}
	return caseItem, principal, resource, nil
}

// A fresh actor and stored snapshot are resolved for each planner round. The
// later adoption transaction must repeat this check under its own locks.
func (s *Store) revalidateAgentPlannerKnowledge(ctx context.Context, runID, attemptID string, resource access.Resource, actorID string, expected *ReviewKnowledgeSnapshot) error {
	actor, current, err := s.agentPlannerAuthority(runID, attemptID, resource, actorID, expected)
	if err != nil {
		return err
	}
	if current != nil {
		return s.RevalidateGovernedKnowledgeSnapshot(ctx, actor, resource.CaseID, *current)
	}
	return nil
}

func (s *Store) agentPlannerAuthority(runID, attemptID string, resource access.Resource, actorID string, expected *ReviewKnowledgeSnapshot) (accounts.User, *ReviewKnowledgeSnapshot, error) {
	st := s.snapshotForAttempt(attemptID)
	actor, err := authorizeAttemptExecution(st, runID, attemptID)
	if err != nil {
		return accounts.User{}, nil, err
	}
	if actor.ID != actorID {
		return accounts.User{}, nil, errAttemptAuthorizationRevoked
	}
	run, ok := st.runByID(runID)
	if !ok || run.CaseID != resource.CaseID {
		return accounts.User{}, nil, errAgentReviewScopeChanged
	}
	item, ok := st.caseByID(run.CaseID)
	if !ok || item.TenantID != resource.TenantID || item.OwnerOrgID != resource.OrgID || item.OwnerID != resource.OwnerID {
		return accounts.User{}, nil, errAgentReviewScopeChanged
	}
	current, err := copyGovernedKnowledge(st.snapshotOf(runID))
	if err != nil {
		return accounts.User{}, nil, err
	}
	if !sameKnowledgeSnapshotIdentity(expected, current) {
		return accounts.User{}, nil, errors.New("governed planner snapshot changed")
	}
	return actor, current, nil
}

func (s *Store) readAgentPlannerKnowledge(ctx context.Context, runID, attemptID string, resource access.Resource, actorID string,
	expected *ReviewKnowledgeSnapshot, selected map[KnowledgeRef]bool, read agentflow.KnowledgeReadRequest) (string, error) {
	ref := KnowledgeRef{KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256}
	if !selected[ref] {
		return "", errors.New("knowledge reference was not selected")
	}
	if read.Start < 0 || read.End <= read.Start || read.End-read.Start > agentflow.KnowledgeReadMaxRunes {
		return "", errors.New("invalid governed knowledge span")
	}
	actor, current, err := s.agentPlannerAuthority(runID, attemptID, resource, actorID, expected)
	if err != nil {
		return "", err
	}
	if current == nil {
		return "", errors.New("governed knowledge snapshot is unavailable")
	}
	content, err := s.ReadFrozenGovernedKnowledge(ctx, actor, resource.CaseID, *current, ref)
	if err != nil {
		return "", err
	}
	return governedKnowledgeSpan(content.Body, read.Start, read.End)
}

func agentReviewEvidence(st state, input AttemptInput, docs []Interpreted) (*agentflow.SourceSnapshot, []agentflow.Evidence, error) {
	snapshot := st.snapshotOf(input.RunID)
	inputHash, hashErr := frozenInputHash(snapshot, input.Documents)
	if hashErr != nil || inputHash != input.InputHash || !frozenInputMatchesSnapshot(snapshot, input.Documents) ||
		!sameDocumentSet(snapshot.DocumentIDs, frozenDocumentIDs(input.Documents)) ||
		!sameDocumentSet(snapshot.ParsedDocumentIDs, frozenParsedDocumentIDs(input.Documents)) ||
		snapshot.ParseSelectionHash != selectionHash(input.Documents) || snapshot.ParserConfigurationID != frozenConfigHash(input.Documents) {
		return nil, nil, errors.New("frozen agent review sources differ from the accepted run snapshot")
	}
	source := &agentflow.SourceSnapshot{
		DocumentIDs: make([]string, 0, len(input.Documents)), ParsedDocumentIDs: make([]string, 0, len(input.Documents)),
		ParseSelectionHash: snapshot.ParseSelectionHash, ParserConfigurationID: snapshot.ParserConfigurationID,
	}
	frozenByID := make(map[string]FrozenDocument, len(input.Documents))
	for _, frozen := range input.Documents {
		source.DocumentIDs = append(source.DocumentIDs, frozen.Document.ID)
		source.ParsedDocumentIDs = append(source.ParsedDocumentIDs, frozen.ParsedDocumentID)
		frozenByID[frozen.Document.ID] = frozen
	}
	evidence := make([]agentflow.Evidence, 0, len(docs))
	for _, doc := range docs {
		frozen, exists := frozenByID[doc.Document.ID]
		if !exists || !doc.Ready || doc.ParsedDocument == nil {
			continue
		}
		parsed := doc.ParsedDocument
		if !frozen.Ready || parsed.ID != frozen.ParsedDocumentID || parsed.DocumentID != frozen.Document.ID ||
			doc.Document.TenantID != frozen.Document.TenantID || doc.Document.CaseID != frozen.Document.CaseID ||
			doc.Document.Role != frozen.Document.Role || doc.Document.Digest != frozen.Document.Digest ||
			parsed.OriginalDigest != frozen.OriginalHash || parsed.OriginalDigest != doc.Document.Digest ||
			parsed.Status != "ready" || parsed.Text != doc.Text || parsed.IR.NormalizedText != doc.Text ||
			parsed.ParserID != frozen.ParserID || parsed.ParserVersion != frozen.ParserVersion ||
			parsed.ConfigHash != frozen.ConfigHash || parsed.NormalizationVersion != frozen.NormalizationVersion ||
			parsed.ArtifactDigest != frozen.ParsedArtifactDigest || parsed.IRDigest != frozen.IRDigest ||
			frozen.NormalizedHash == nil || parsed.NormalizedDigest == "" || parsed.NormalizedDigest != *frozen.NormalizedHash ||
			parsed.NormalizedDigest != docirDigest(doc.Text) || parsed.IR.NormalizedDigest != parsed.NormalizedDigest ||
			doc.IR.NormalizedText != doc.Text || doc.IR.NormalizedDigest != parsed.NormalizedDigest {
			return nil, nil, errors.New("prepared source does not match the accepted parse identity")
		}
		evidence = append(evidence, agentflow.Evidence{
			ID: doc.Document.ID, TenantID: doc.Document.TenantID, CaseID: doc.Document.CaseID,
			Version: doc.Document.Digest, Role: doc.Document.Role, Text: doc.Text, SHA256: agentflow.TextHash(doc.Text),
			DocumentID: doc.Document.ID, ParsedDocumentID: parsed.ID, OriginalDigest: parsed.OriginalDigest,
			NormalizedDigest: parsed.NormalizedDigest, ParserID: parsed.ParserID, ParserVersion: parsed.ParserVersion,
			ConfigHash: parsed.ConfigHash, NormalizationVersion: parsed.NormalizationVersion,
		})
	}
	return source, evidence, nil
}

func docirDigest(text string) string {
	return docversion.Hash([]byte(text)).String()
}

func frozenDocumentIDs(documents []FrozenDocument) []string {
	ids := make([]string, 0, len(documents))
	for _, document := range documents {
		ids = append(ids, document.Document.ID)
	}
	return ids
}

func frozenParsedDocumentIDs(documents []FrozenDocument) []string {
	ids := make([]string, 0, len(documents))
	for _, document := range documents {
		ids = append(ids, document.ParsedDocumentID)
	}
	return ids
}

func telecomAgentReviewGaps() []agentflow.Gap {
	gaps := make([]agentflow.Gap, 0, 18)
	add := func(domain agentflow.Domain, capability string) {
		gaps = append(gaps, agentflow.Gap{ID: "gap_" + string(domain) + "_" + capability,
			Domain: domain, Capability: capability, Question: "比较冻结材料中的受支持字段并保留来源缺口"})
	}
	for _, domain := range agentflow.Domains() {
		add(domain, agentflow.BaselineCapability(domain))
	}
	for _, capability := range []string{"amount", "party", "approval_authority", "effective_scope", "approval_validity"} {
		add(agentflow.Compliance, capability)
	}
	add(agentflow.Commercial, "amount")
	add(agentflow.Technical, "scope")
	add(agentflow.Fulfillment, "milestone")
	add(agentflow.Fulfillment, "explicit_start_event")
	for _, capability := range []string{"subject_id", "signing_party", "payee", "responsibility"} {
		add(agentflow.Counterparty, capability)
	}
	return gaps
}

func telecomAgentReviewEvaluations(st state, runID, attemptID string) []agentflow.CanonicalEvaluation {
	evaluations := st.evaluationsForAttempt(runID, attemptID)
	findings := st.findingsForAttempt(runID, attemptID)
	out := make([]agentflow.CanonicalEvaluation, 0, len(evaluations))
	for _, evaluation := range evaluations {
		if evaluation.ID == "" || evaluation.Domain == "" || evaluation.CapabilityID == "" || evaluation.Relation == "" {
			continue
		}
		item := agentflow.CanonicalEvaluation{
			ID: evaluation.ID, Domain: agentflow.Domain(evaluation.Domain), CapabilityID: evaluation.CapabilityID, Mandatory: evaluation.Mandatory,
			Relation: evaluation.Relation, ReasonCode: evaluation.ReasonCode, EvidenceIDs: []string{},
		}
		for _, finding := range findings {
			if finding.ID != evaluation.FindingID {
				continue
			}
			for _, anchor := range finding.Anchors {
				if anchor.DocumentID != "" {
					item.EvidenceIDs = append(item.EvidenceIDs, anchor.DocumentID)
				}
			}
		}
		item.EvidenceIDs = compactStrings(item.EvidenceIDs)
		out = append(out, item)
	}
	return out
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] != result[write-1] {
			result[write] = result[read]
			write++
		}
	}
	return result[:write]
}

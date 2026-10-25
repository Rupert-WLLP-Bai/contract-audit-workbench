package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	planner, err := frozenAgentPlanner(configuration, models, recorder)
	if err != nil {
		return true, err
	}
	tools := append(agentflow.BaselineTools(), agentflow.SourceFactTools()...)
	runtime, err := agentflow.New(ctx, agentflow.Config{
		Planner: planner, Tools: tools, PlannerConfiguration: configuration,
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
	rulePackIdentity, err := agentReviewRulePackIdentity(attempt, caseItem)
	if err != nil {
		return true, err
	}
	request.RulePack = rulePackIdentity
	report, runErr := runtime.Run(ctx, request)
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if report.Projection == nil {
		if errors.Is(runErr, agentflow.ErrDenied) {
			return true, rulepack.ErrUnauthorized
		}
		return true, errors.New("agent review did not produce a persistable projection")
	}
	projectionBytes, err := json.Marshal(report.Projection)
	if err != nil {
		return true, err
	}
	for _, event := range report.Events {
		status := "completed"
		switch event.Kind {
		case "tool_failed", "plan_rejected", "gap_unsupported":
			status = "failed"
		case "planner_fallback":
			status = "partial"
		}
		var duration *int64
		if event.DurationMS > 0 {
			value := event.DurationMS
			duration = &value
		}
		if err := recorder.RecordExecutionEvent(ctx, execution.Event{
			ParentSpanID: execution.ParentSpanID(ctx), NodeKind: "agent_review", Phase: event.Kind,
			EventType: "agent_review_event", At: time.Now().UTC(), DurationMS: duration, Status: status,
			ErrorCode: event.Code,
		}); err != nil {
			return true, err
		}
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

func agentReviewRulePackIdentity(attempt Attempt, caseItem Case) (*agentflow.RulePackIdentity, error) {
	hasIdentityFields := attempt.RulePackID != "" || attempt.RulePackVersion != "" || attempt.RulePackPointerRevision != 0 ||
		attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" || len(attempt.RulePackSnapshot) != 0
	switch attempt.RulePackCompilerRef {
	case governedCompilerMarker:
		pack, err := ParseFrozenGovernedRulePack(attempt, caseItem)
		if err != nil {
			return nil, err
		}
		if pack == nil {
			return nil, errors.New("frozen governed RulePack is unavailable for the agent review")
		}
		manifest := pack.Manifest()
		return &agentflow.RulePackIdentity{ID: manifest.ID, Version: manifest.Version, ContentSHA256: pack.Hash()}, nil
	case telecom.CompilerRef:
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
	case "":
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
	gaps := make([]agentflow.Gap, 0, 16)
	add := func(domain agentflow.Domain, capability string) {
		gaps = append(gaps, agentflow.Gap{ID: "gap_" + string(domain) + "_" + capability,
			Domain: domain, Capability: capability, Question: "比较冻结材料中的受支持字段并保留来源缺口"})
	}
	for _, domain := range agentflow.Domains() {
		add(domain, agentflow.BaselineCapability(domain))
	}
	for _, capability := range []string{"amount", "party", "approval_authority", "effective_scope"} {
		add(agentflow.Compliance, capability)
	}
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
			ID: evaluation.ID, Domain: agentflow.Domain(evaluation.Domain), CapabilityID: evaluation.CapabilityID,
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

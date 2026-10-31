package store

import (
	"bytes"
	"encoding/json"
	"errors"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/audit"
	"contractaudit/internal/engine"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

func documentIDs(docs []Document) []string {
	out := make([]string, len(docs))
	for i, doc := range docs {
		out[i] = doc.ID
	}
	return out
}

// currentSignature binds idempotence and retry to the same inputs that are
// sealed in the immutable audit snapshot. Changing providers needs a new run.
func (s *Store) currentSignature(docIDs []string, selection ...[]FrozenDocument) RunSnapshot {
	var frozen []FrozenDocument
	if len(selection) > 0 {
		frozen = selection[0]
	}
	return currentSignatureFor(docIDs, frozen, s.currentModels())
}

// currentSignatureWithRulePack extends the signature with the pack identity and
// planner configuration frozen at acceptance. Both are part of the run
// configuration, so a different selected pack version or planner configuration
// requires a new run.
func (s *Store) currentSignatureWithRulePack(docIDs []string, selection []FrozenDocument, pack *ActiveRulePack, compilerRef, plannerConfigurationID string, plannerPayload ...json.RawMessage) RunSnapshot {
	// The extractor belongs to comparator configuration; the governed pack has
	// its own compiler marker. Keep both identities so retries cannot execute
	// changed extraction semantics against a previously frozen run.
	signature := currentSignatureFor(docIDs, selection, s.currentModels(), compilerRef)
	signature.RulePackCompilerRef = ""
	applyActiveRulePackIdentity(&signature, pack)
	signature.PlannerConfigurationID = plannerConfigurationID
	if len(plannerPayload) > 0 {
		signature.PlannerConfiguration = append(json.RawMessage(nil), plannerPayload[0]...)
	}
	return signature
}

// applyActiveRulePackIdentity copies every immutable identity field used by
// accepted reviews and by preflight validation. Keeping this in one place
// prevents callers from constructing a partial snapshot that looks frozen but
// cannot be matched to its governance snapshot.
func applyActiveRulePackIdentity(snapshot *RunSnapshot, pack *ActiveRulePack) {
	if snapshot == nil || pack == nil {
		return
	}
	snapshot.RulePackCompilerRef = pack.CompilerMarker
	snapshot.RulePackID = pack.PackID
	snapshot.RulePackVersion = pack.Version
	snapshot.RulePackHash = pack.ContentHash
	snapshot.RulePackDigest = pack.SnapshotDigest
	snapshot.RulePackSnapshot = append([]byte(nil), pack.Snapshot...)
}

func telecomCompilerRefForCase(item Case) string {
	if isTelecomDedicatedLineCase(item) || isCloudServicesCase(item) {
		return telecom.CompilerRef
	}
	return ""
}

func requireGovernedCapabilitiesForCase(caseItem Case, snapshot []byte) error {
	switch {
	case isTelecomDedicatedLineCase(caseItem):
		return requireTelecomGovernedCapabilities(snapshot)
	case isCloudServicesCase(caseItem):
		if err := telecom.ValidateCloudServicesRulePack(snapshot); err != nil {
			return fault.Conflict("已发布云服务规则包缺少固定的强制能力或制度依据")
		}
		return nil
	default:
		return fault.Conflict("治理规则包与案件业务范围不匹配")
	}
}

// requireTelecomGovernedCapabilities enforces that a governed pack keeps every
// mandatory telecom capability with its comparator, exact business scope and
// nonempty authority source, so governance cannot disable mandatory coverage.
func requireTelecomGovernedCapabilities(snapshot []byte) error {
	pack, err := rulepack.Parse(snapshot)
	if err != nil {
		return fault.Conflict("已发布规则包内容损坏，不能用于审查")
	}
	if pack.Manifest().Lifecycle.State != rulepack.StatePublished {
		return fault.Conflict("已发布规则包生命周期状态不正确，不能用于审查")
	}
	required := map[string]string{
		"ENT-TECH-BW-001":  rulepack.ComparatorMinimumBandwidth,
		"ENT-TECH-SLA-001": rulepack.ComparatorMonthlySLA,
		"ENT-COM-ADV-001":  rulepack.ComparatorAdvancePaymentCeiling,
	}
	seen := map[string]bool{}
	for _, rule := range pack.Manifest().Rules {
		comparator, mandatory := required[rule.RuleID]
		if !mandatory {
			continue
		}
		if !rule.Mandatory || rule.Comparator != comparator {
			return fault.Conflict("已发布规则包不能取消或更改强制能力 " + rule.RuleID + " 的比较器")
		}
		if rule.BusinessScope.BusinessLine != "ENT" || rule.BusinessScope.Scenario != "telecom_dedicated_line" || rule.BusinessScope.ContractType != "service" {
			return fault.Conflict("已发布规则包不能更改强制能力的业务范围")
		}
		if len(rule.AuthoritySources) == 0 {
			return fault.Conflict("已发布规则包不能移除强制能力的授权来源")
		}
		seen[rule.RuleID] = true
	}
	for ruleID := range required {
		if !seen[ruleID] {
			return fault.Conflict("已发布规则包缺少强制能力 " + ruleID + "，不能用于审查")
		}
	}
	return nil
}

// withFrozenGovernance pins the governed pack, policy source and planner
// identity of an already frozen run onto a freshly computed signature.
func withFrozenGovernance(current, frozen RunSnapshot) RunSnapshot {
	if isSingleContractSnapshot(frozen) {
		current = applyReviewScope(current, ReviewScopeSingleContract)
	}
	current.RulePackCompilerRef = frozen.RulePackCompilerRef
	current.RulePackID = frozen.RulePackID
	current.RulePackVersion = frozen.RulePackVersion
	current.RulePackHash = frozen.RulePackHash
	current.RulePackDigest = frozen.RulePackDigest
	current.RulePackSnapshot = append([]byte(nil), frozen.RulePackSnapshot...)
	current.CloudPolicySnapshot = append(json.RawMessage(nil), frozen.CloudPolicySnapshot...)
	current.CloudPolicyDigest = frozen.CloudPolicyDigest
	current.GovernedKnowledge = frozen.GovernedKnowledge
	current.ServiceObjectContext = cloneServiceObjectReviewContext(frozen.ServiceObjectContext)
	current.ServiceObjectContextHash = frozen.ServiceObjectContextHash
	current.PlannerConfigurationID = frozen.PlannerConfigurationID
	current.PlannerConfiguration = append(json.RawMessage(nil), frozen.PlannerConfiguration...)
	return current
}

// plannerConfigurationForAcceptance freezes the agent planner configuration a
// run must use. A resolver failure is a hard error: an unfrozen planner identity
// would let a retry silently switch prompt or limits.
func (s *Store) plannerConfigurationForAcceptance(modelConfigurationID string) (agentflow.PlannerConfiguration, error) {
	configuration, err := agentflow.ResolvePlannerConfiguration(modelConfigurationID)
	if err != nil {
		return agentflow.PlannerConfiguration{}, fault.Conflict("审查计划配置不可用，无法保存本次审查信息：" + err.Error())
	}
	if err := configuration.Validate(); err != nil {
		return agentflow.PlannerConfiguration{}, fault.Conflict("审查计划配置未通过校验，无法保存本次审查信息")
	}
	return configuration, nil
}

func signatureModelConfigurationID(models Models) string {
	return frozenConfigurationID(models)
}

func currentSignatureFor(docIDs []string, frozen []FrozenDocument, models Models, compilerRefs ...string) RunSnapshot {
	name := ""
	configurationID := "models-disabled/v1"
	if models != nil {
		name = models.Name()
		if identity := frozenConfigurationID(models); identity != "" {
			configurationID = identity
		}
	}
	parsedIDs := make([]string, len(frozen))
	for i, item := range frozen {
		parsedIDs[i] = item.ParsedDocumentID
	}
	var parseSelectionHash, parserConfigurationID string
	if len(frozen) > 0 {
		parseSelectionHash = selectionHash(frozen)
		parserConfigurationID = frozenConfigHash(frozen)
	}
	compilerRef := ""
	if len(compilerRefs) > 0 {
		compilerRef = compilerRefs[0]
	}
	comparatorRef := engine.ComparatorRef
	if compilerRef != "" {
		comparatorRef += "+" + compilerRef
	}
	return RunSnapshot{
		DocumentIDs: append([]string(nil), docIDs...), ParsedDocumentIDs: parsedIDs,
		ParseSelectionHash: parseSelectionHash, ParserConfigurationID: parserConfigurationID,
		RuleSetHash: engine.RuleSetHash(), PlaybookHash: engine.PlaybookHash(),
		ModelRef: name, ComparatorRef: comparatorRef, ModelConfigurationID: configurationID,
		RulePackCompilerRef: compilerRef,
	}
}

func sameRunSignature(a, b RunSnapshot) bool {
	return sameDocumentSet(a.DocumentIDs, b.DocumentIDs) && sameDocumentSet(a.ParsedDocumentIDs, b.ParsedDocumentIDs) &&
		a.ParseSelectionHash == b.ParseSelectionHash && a.PartyContextHash == b.PartyContextHash && sameRunConfiguration(a, b)
}

func withPartyContext(st state, caseID string, signature RunSnapshot) (RunSnapshot, error) {
	items, hash, err := partyContextForCase(st, caseID)
	if err != nil {
		return RunSnapshot{}, err
	}
	signature.PartyContext = items
	signature.PartyContextHash = hash
	return signature, nil
}

func sameRunCompilerIdentity(a, b RunSnapshot) bool {
	return a.RulePackCompilerRef == b.RulePackCompilerRef
}

// sameRunRulePackIdentity compares the governed pack frozen at acceptance. A
// synthetic-baseline run (empty identity) is never comparable to a governed run.
func sameRunRulePackIdentity(a, b RunSnapshot) bool {
	return a.RulePackID == b.RulePackID && a.RulePackVersion == b.RulePackVersion &&
		a.RulePackHash == b.RulePackHash && a.RulePackDigest == b.RulePackDigest
}

func sameRunConfiguration(a, b RunSnapshot) bool {
	parserConfigurationMatches := a.ParserConfigurationID == "" || b.ParserConfigurationID == "" || a.ParserConfigurationID == b.ParserConfigurationID
	compilerIdentityMatches := a.RulePackCompilerRef == "" || b.RulePackCompilerRef == "" || a.RulePackCompilerRef == b.RulePackCompilerRef
	return a.RuleSetHash == b.RuleSetHash && a.PlaybookHash == b.PlaybookHash &&
		a.ModelRef == b.ModelRef && a.ModelConfigurationID == b.ModelConfigurationID &&
		a.ComparatorRef == b.ComparatorRef && parserConfigurationMatches && compilerIdentityMatches &&
		sameRunRulePackIdentity(a, b) && samePlannerConfiguration(a, b) &&
		a.CloudPolicyDigest == b.CloudPolicyDigest && a.ServiceObjectContextHash == b.ServiceObjectContextHash &&
		sameKnowledgeSnapshotIdentity(a.GovernedKnowledge, b.GovernedKnowledge)
}

// samePlannerConfiguration treats the absent pair as an explicit legacy state.
// Every accepted configuration must retain both its hash and exact payload.
func samePlannerConfiguration(a, b RunSnapshot) bool {
	if a.PlannerConfigurationID != b.PlannerConfigurationID {
		return false
	}
	if len(a.PlannerConfiguration) == 0 || len(b.PlannerConfiguration) == 0 {
		return a.PlannerConfigurationID == "" && len(a.PlannerConfiguration) == 0 && len(b.PlannerConfiguration) == 0
	}
	return bytes.Equal(a.PlannerConfiguration, b.PlannerConfiguration)
}

func validatePartyContextSnapshot(snapshot RunSnapshot) error {
	if len(snapshot.PartyContext) == 0 {
		if snapshot.PartyContextHash != "" {
			return errors.New("party context hash exists without a party context payload")
		}
		return nil
	}
	if snapshot.PartyContextHash == "" {
		return errors.New("party context payload exists without its hash")
	}
	encoded, err := json.Marshal(snapshot.PartyContext)
	if err != nil {
		return err
	}
	if hashJSON(encoded) != snapshot.PartyContextHash {
		return errors.New("party context hash does not match its payload")
	}
	return nil
}

func cloneServiceObjectReviewContext(input *ServiceObjectReviewContext) *ServiceObjectReviewContext {
	if input == nil {
		return nil
	}
	copy := *input
	copy.Items = append([]ServiceObjectReviewContextItem(nil), input.Items...)
	for i := range copy.Items {
		copy.Items[i].CandidateEvidence = append([]ServiceObjectCandidateEvidence(nil), input.Items[i].CandidateEvidence...)
	}
	return &copy
}

func decodePartyContext(payload []byte, hash string) ([]PartyContextItem, error) {
	if len(payload) == 0 && hash == "" {
		return nil, nil
	}
	if len(payload) == 0 || hash == "" {
		return nil, errors.New("party context payload and hash must be present together")
	}
	var items []PartyContextItem
	if err := json.Unmarshal(payload, &items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("party context payload cannot be an empty array")
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(payload, encoded) || hashJSON(encoded) != hash {
		return nil, errors.New("party context payload is noncanonical or its hash does not match")
	}
	return items, nil
}

func sealSnapshot(st *state, runID string, input RunSnapshot) error {
	for _, item := range st.Snapshots {
		if item.RunID == runID {
			return audit.ErrImmutable
		}
	}
	if err := validatePartyContextSnapshot(input); err != nil {
		return err
	}
	if err := validateServiceObjectReviewContext(input); err != nil {
		return err
	}
	knowledge, err := copyGovernedKnowledge(input)
	if err != nil {
		return err
	}
	frozen, err := audit.Freeze(snapshotValue(input))
	if err != nil {
		return err
	}
	st.Snapshots = append(st.Snapshots, RunSnapshot{
		RunID: runID, DocumentIDs: frozen.DocumentIDs, ParsedDocumentIDs: frozen.ParsedDocumentIDs,
		PartyContext: append([]PartyContextItem(nil), input.PartyContext...), PartyContextHash: input.PartyContextHash,
		ServiceObjectContext: cloneServiceObjectReviewContext(input.ServiceObjectContext), ServiceObjectContextHash: input.ServiceObjectContextHash,
		ParseSelectionHash: frozen.ParseSelectionHash, ParserConfigurationID: frozen.ParserConfigurationID,
		RuleSetHash: frozen.RuleSetHash, PlaybookHash: frozen.PlaybookHash, ModelRef: frozen.ModelRef,
		ComparatorRef: frozen.ComparatorRef, ModelConfigurationID: frozen.ModelConfigurationID,
		RulePackCompilerRef: frozen.RulePackCompilerRef, RulePackID: frozen.RulePackID,
		RulePackVersion: frozen.RulePackVersion, RulePackHash: frozen.RulePackHash,
		RulePackDigest: frozen.RulePackDigest, RulePackSnapshot: append([]byte(nil), input.RulePackSnapshot...),
		CloudPolicySnapshot:    append(json.RawMessage(nil), input.CloudPolicySnapshot...),
		CloudPolicyDigest:      input.CloudPolicyDigest,
		GovernedKnowledge:      knowledge,
		PlannerConfigurationID: frozen.PlannerConfigurationID,
		PlannerConfiguration:   append(json.RawMessage(nil), input.PlannerConfiguration...),
	})
	return nil
}

func appendAudit(st *state, runID, actor, eventType string, correlation []string) error {
	var prev audit.Event
	for _, item := range st.Audits {
		if item.RunID != runID || item.Seq < prev.Seq {
			continue
		}
		prev = item.event()
	}
	id, err := newID("evt_")
	if err != nil {
		return err
	}
	next, err := audit.Append(prev, audit.Event{
		EventID: id, RunID: runID, Actor: actor, Seq: prev.Seq + 1,
		EventType: eventType, CorrelationIDs: correlation,
	})
	if err != nil {
		return err
	}
	ids := append([]string(nil), next.CorrelationIDs...)
	st.Audits = append(st.Audits, AuditRecord{
		EventID: next.EventID, RunID: next.RunID, Actor: next.Actor, Seq: next.Seq,
		EventType: next.EventType, PrevHash: next.PrevHash, Hash: next.Hash, CorrelationIDs: ids,
	})
	return nil
}

func (rec AuditRecord) event() audit.Event {
	return audit.Event{
		EventID: rec.EventID, RunID: rec.RunID, Actor: rec.Actor, Seq: rec.Seq,
		EventType: rec.EventType, PrevHash: rec.PrevHash, Hash: rec.Hash,
		CorrelationIDs: append([]string(nil), rec.CorrelationIDs...),
	}
}

func appendEvaluation(st *state, caseID, runID string, ev engine.Evaluation, findingID string) error {
	id, err := newID("evl_")
	if err != nil {
		return err
	}
	st.Evaluations = append(st.Evaluations, ConstraintEval{
		ID: id, RunID: runID, CaseID: caseID, Domain: ev.Domain,
		Relation: ev.Relation, RuleVersion: ev.RuleVersion, Reason: ev.Reason, FindingID: findingID,
	})
	return nil
}

func (st *state) snapshotOf(runID string) RunSnapshot {
	for _, item := range st.Snapshots {
		if item.RunID == runID {
			item = scopeSnapshot(item)
			if item.DocumentIDs == nil {
				item.DocumentIDs = []string{}
			}
			return item
		}
	}
	return RunSnapshot{RunID: runID, DocumentIDs: []string{}}
}

func (st *state) audits(runID string) []AuditRecord {
	var out []AuditRecord
	for _, item := range st.Audits {
		if item.RunID == runID {
			if item.CorrelationIDs == nil {
				item.CorrelationIDs = []string{}
			}
			out = append(out, item)
		}
	}
	if out == nil {
		out = []AuditRecord{}
	}
	return out
}

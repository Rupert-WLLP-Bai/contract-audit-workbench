package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

const (
	telecomBandwidthRule = "ENT-TECH-BW-001"
	telecomSLARule       = "ENT-TECH-SLA-001"
	telecomAdvanceRule   = "ENT-COM-ADV-001"
	cloudBandwidthRule   = "CLOUD-TECH-BW-001"
	cloudSLARule         = "CLOUD-TECH-SLA-001"
	cloudAdvanceRule     = "CLOUD-COM-ADV-001"
)

type governedCapability struct {
	ruleID, capabilityID, domain, version string
	mandatory                            bool
}

var telecomCapabilities = []governedCapability{
	{ruleID: telecomBandwidthRule, capabilityID: "dedicated_line.minimum_bandwidth", domain: "technical", mandatory: true},
	{ruleID: telecomSLARule, capabilityID: "dedicated_line.monthly_availability", domain: "technical", mandatory: true},
	{ruleID: telecomAdvanceRule, capabilityID: "payment.advance_ratio.ceiling", domain: "commercial", mandatory: true},
}

var cloudCapabilities = []governedCapability{
	{ruleID: cloudBandwidthRule, capabilityID: "cloud_services.minimum_bandwidth", domain: "technical", mandatory: true},
	{ruleID: cloudSLARule, capabilityID: "cloud_services.monthly_availability", domain: "technical", mandatory: true},
	{ruleID: cloudAdvanceRule, capabilityID: "cloud_services.advance_payment_ceiling", domain: "commercial", mandatory: true},
}

func capabilitiesForCase(item Case) []governedCapability {
	if isTelecomDedicatedLineCase(item) {
		return telecomCapabilities
	}
	if isCloudServicesCase(item) {
		return cloudCapabilities
	}
	return nil
}

func capabilitiesForSnapshot(item Case, snapshot []byte) ([]governedCapability, error) {
	if !isDeclaredReviewCase(item) {
		return capabilitiesForCase(item), nil
	}
	pack, err := rulepack.Parse(snapshot)
	if err != nil || telecom.ValidateDeclaredPack(pack, rulepack.BusinessScope{
		BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType,
	}, item.TenantID) != nil {
		return nil, errors.New("declared RulePack snapshot does not match the case")
	}
	manifest := pack.Manifest()
	capabilities := make([]governedCapability, 0, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		capabilities = append(capabilities, governedCapability{ruleID: rule.RuleID, capabilityID: rule.CapabilityID,
			domain: rule.Domain, version: rule.Version, mandatory: rule.Mandatory})
	}
	return capabilities, nil
}

type telecomSourceKey struct {
	id      string
	version string
}

type telecomReviewOutput struct {
	evaluations []ConstraintEval
	findings    []Finding
}

func isTelecomDedicatedLineCase(item Case) bool {
	return item.BusinessLine == "ENT" && item.Scenario == "telecom_dedicated_line"
}

func isCloudServicesCase(item Case) bool {
	return item.BusinessLine == "ENT" && item.Scenario == "cloud_services" && item.ContractType == "service"
}

func isDeclaredReviewCase(item Case) bool {
	if item.BusinessLine == "" || item.Scenario == "" || item.ContractType == "" ||
		item.BusinessLine == "unspecified" || item.Scenario == "unspecified" {
		return false
	}
	if isTelecomDedicatedLineCase(item) {
		switch item.ContractType {
		case "service", "procurement_ict", "network_engineering", "unspecified":
			return false
		default:
			return true
		}
	}
	return !(item.BusinessLine == "ENT" && item.Scenario == "cloud_services")
}

func isGovernedReviewCase(item Case) bool {
	return isTelecomDedicatedLineCase(item) || isCloudServicesCase(item) || isDeclaredReviewCase(item)
}

type cloudPolicyRef struct {
	ref       KnowledgeRef
	version   string
	selection GovernedKnowledgeSelection
}

// validateCloudPolicyBindings requires a frozen, published policy source for
// every mandatory cloud rule. The identity uses the display version in the
// RulePack and the content-addressed version in the governance snapshot.
func validateCloudPolicyBindings(snapshot RunSnapshot, knowledge ReviewKnowledgeSnapshot, tenantID string) ([]cloudPolicyRef, error) {
	sealed, err := sealKnowledgeSnapshot(knowledge)
	if err != nil || knowledge.Encoding != "review-knowledge-snapshot/v1" ||
		knowledge.SnapshotSHA256 == "" || sealed.SnapshotSHA256 != knowledge.SnapshotSHA256 {
		return nil, errors.New("cloud governance snapshot is invalid")
	}
	if snapshot.RulePackCompilerRef != governedCompilerMarker || len(snapshot.RulePackSnapshot) == 0 ||
		sha256Bytes(snapshot.RulePackSnapshot) != snapshot.RulePackDigest ||
		telecom.ValidateCloudServicesRulePack(snapshot.RulePackSnapshot) != nil {
		return nil, errors.New("cloud governed RulePack freeze is invalid")
	}
	pack, err := rulepack.Parse(snapshot.RulePackSnapshot)
	if err != nil {
		return nil, errors.New("cloud governed RulePack cannot be decoded")
	}
	manifest := pack.Manifest()
	packRef := knowledge.RulePack
	if tenantID == "" || manifest.TenantID != tenantID || manifest.ID != snapshot.RulePackID ||
		manifest.Version != snapshot.RulePackVersion || pack.Hash() != snapshot.RulePackHash ||
		packRef.PackID != snapshot.RulePackID || packRef.Version != snapshot.RulePackVersion ||
		packRef.ContentHash != snapshot.RulePackHash || packRef.SnapshotDigest != snapshot.RulePackDigest ||
		packRef.CompilerRef != governedCompilerMarker || packRef.PointerRevision < 1 ||
		knowledge.Scope.BusinessLine != "ENT" || knowledge.Scope.Scenario != "cloud_services" || knowledge.Scope.ContractType != "service" {
		return nil, errors.New("cloud policy and RulePack identities differ")
	}
	required := make(map[string]bool, len(cloudCapabilities))
	for _, capability := range cloudCapabilities {
		required[capability.ruleID] = true
	}
	seenPolicies := make(map[KnowledgeRef]bool)
	var policies []cloudPolicyRef
	for _, rule := range manifest.Rules {
		if !required[rule.RuleID] {
			continue
		}
		matched := false
		for _, source := range rule.AuthoritySources {
			if source.Kind != "internal_policy" && source.Kind != "synthetic_demo" {
				continue
			}
			var selected *GovernedKnowledgeSelection
			for i := range knowledge.RequiredPolicies {
				candidate := &knowledge.RequiredPolicies[i]
				if candidate.Ref.KnowledgeID == source.ID && candidate.Ref.VersionID == knowledgeVersionID(tenantID, source.ID, source.Version) {
					if selected != nil {
						return nil, errors.New("cloud policy source is ambiguous")
					}
					selected = candidate
				}
			}
			if selected == nil || !selected.Required || selected.Kind != "policy" || selected.PointerRevision < 1 ||
				!validKnowledgeHash(selected.Ref.ContentSHA256) ||
				(source.Kind == "internal_policy" && selected.SourceType != "reference") ||
				(source.Kind == "synthetic_demo" && selected.SourceType != "synthetic_policy") {
				return nil, errors.New("cloud rule source is not an exact approved policy")
			}
			binding := selected.Binding
			if binding.PackID != packRef.PackID || binding.PackVersion != packRef.Version ||
				binding.PackContentHash != packRef.ContentHash || binding.PackSnapshotDigest != packRef.SnapshotDigest ||
				binding.CompilerRef != packRef.CompilerRef || !cloudBindingCoversRule(binding, rule.RuleID) {
				return nil, errors.New("cloud rule has no reviewed policy source binding")
			}
			matched = true
			if !seenPolicies[selected.Ref] {
				seenPolicies[selected.Ref] = true
				policies = append(policies, cloudPolicyRef{ref: selected.Ref, version: source.Version, selection: *selected})
			}
		}
		if !matched {
			return nil, errors.New("cloud mandatory rule has no approved policy source")
		}
	}
	return policies, nil
}

func cloudBindingCoversRule(binding PolicyRuleBinding, ruleID string) bool {
	bound := false
	for _, id := range binding.RuleIDs {
		if id == ruleID {
			bound = true
			break
		}
	}
	if !bound {
		return false
	}
	for _, span := range binding.SourceSpans {
		if span.RuleID == ruleID && span.Start >= 0 && span.End > span.Start && span.Quote != "" {
			return true
		}
	}
	return false
}

func cloudPoliciesForAttempt(st state, caseItem Case, runID, attemptID string) ([]cloudPolicyRef, error) {
	run, ok := st.runByID(runID)
	if !ok || run.CaseID != caseItem.ID || !isCloudServicesCase(caseItem) {
		return nil, errors.New("cloud review run or case scope changed")
	}
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
		return nil, errAttemptTerminal
	}
	snapshot := st.snapshotOf(runID)
	if snapshot.GovernedKnowledge == nil {
		return nil, errors.New("cloud review has no governed policy snapshot")
	}
	if _, err := ParseFrozenGovernedRulePack(attempt, caseItem); err != nil {
		return nil, err
	}
	if attempt.RulePackCompilerRef != snapshot.RulePackCompilerRef || attempt.RulePackID != snapshot.RulePackID ||
		attempt.RulePackVersion != snapshot.RulePackVersion || attempt.RulePackHash != snapshot.RulePackHash ||
		attempt.RulePackSnapshotDigest != snapshot.RulePackDigest ||
		attempt.RulePackPointerRevision != snapshot.GovernedKnowledge.RulePack.PointerRevision ||
		!bytes.Equal(attempt.RulePackSnapshot, snapshot.RulePackSnapshot) {
		return nil, errors.New("cloud attempt and governance RulePack freezes differ")
	}
	return validateCloudPolicyBindings(snapshot, *snapshot.GovernedKnowledge, caseItem.TenantID)
}

func (s *Store) cloudPolicyForReview(ctx context.Context, caseItem Case, runID, attemptID string, wanted cloudPolicyRef) (model.CloudPolicy, error) {
	st, err := s.snapshotForAttemptMetadataContext(ctx, attemptID)
	if err != nil {
		return model.CloudPolicy{}, err
	}
	currentCase, ok := st.caseByID(caseItem.ID)
	if !ok || !isCloudServicesCase(currentCase) {
		return model.CloudPolicy{}, errors.New("cloud review case scope changed")
	}
	actor, err := authorizeAttemptExecution(st, runID, attemptID)
	if err != nil {
		return model.CloudPolicy{}, err
	}
	snapshot := st.snapshotOf(runID)
	policies, err := cloudPoliciesForAttempt(st, currentCase, runID, attemptID)
	if err != nil {
		return model.CloudPolicy{}, err
	}
	var selected *cloudPolicyRef
	for i := range policies {
		if policies[i].ref == wanted.ref && policies[i].version == wanted.version {
			selected = &policies[i]
			break
		}
	}
	if selected == nil {
		return model.CloudPolicy{}, errors.New("cloud policy is absent from the frozen rule bindings")
	}
	content, err := s.ReadFrozenGovernedKnowledge(ctx, actor, currentCase.ID, *snapshot.GovernedKnowledge, selected.ref)
	if err != nil {
		return model.CloudPolicy{}, err
	}
	if content.Ref != selected.ref || content.Kind != "policy" || strings.TrimSpace(content.Body) == "" {
		return model.CloudPolicy{}, errors.New("frozen cloud policy content is invalid")
	}
	body := []rune(content.Body)
	for _, span := range selected.selection.Binding.SourceSpans {
		if span.Start < 0 || span.End <= span.Start || span.End > len(body) || string(body[span.Start:span.End]) != span.Quote {
			return model.CloudPolicy{}, errors.New("frozen cloud policy source span does not match its body")
		}
	}
	return model.CloudPolicy{ID: selected.ref.KnowledgeID, Version: selected.version, Body: content.Body}, nil
}

func cloudModelDrafts(review model.CloudReview, docs []Interpreted, policy model.CloudPolicy) ([]engine.Draft, error) {
	if len(review.Findings) > 4 || strings.TrimSpace(policy.ID) == "" || strings.TrimSpace(policy.Version) == "" || strings.TrimSpace(policy.Body) == "" {
		return nil, engine.ErrRejected
	}
	drafts := make([]engine.Draft, 0, len(review.Findings))
	seenDomains := make(map[string]bool, len(review.Findings))
	for _, proposed := range review.Findings {
		validDomain := proposed.Domain == "liability" || proposed.Domain == "data_security" || proposed.Domain == "intellectual_property" || proposed.Domain == "renewal"
		if !validDomain || seenDomains[proposed.Domain] || strings.TrimSpace(proposed.Claim) == "" || utf8.RuneCountInString(proposed.Claim) > 1000 ||
			strings.TrimSpace(proposed.PolicyClause) == "" || utf8.RuneCountInString(proposed.PolicyClause) > 1000 || !strings.Contains(policy.Body, proposed.PolicyClause) {
			return nil, engine.ErrRejected
		}
		seenDomains[proposed.Domain] = true
		var doc *Interpreted
		for i := range docs {
			if docs[i].Document.ID == proposed.DocumentID && docs[i].Document.Role == "draft_contract" {
				if doc != nil {
					return nil, engine.ErrRejected
				}
				doc = &docs[i]
			}
		}
		if doc == nil || !doc.Ready || doc.ParsedDocument == nil || doc.ParsedDocument.ID == "" ||
			doc.ParsedDocument.DocumentID != doc.Document.ID || doc.ParsedDocument.OriginalDigest != doc.Document.Digest ||
			doc.ParsedDocument.NormalizedDigest == "" || doc.ParsedDocument.NormalizedDigest != doc.IR.NormalizedDigest ||
			doc.ParsedDocument.ArtifactDigest == "" || proposed.Quote == "" || utf8.RuneCountInString(proposed.Quote) > 10000 ||
			!strings.Contains(doc.IR.NormalizedText, proposed.Quote) {
			return nil, engine.ErrRejected
		}
		byteStart := strings.Index(doc.IR.NormalizedText, proposed.Quote)
		start := utf8.RuneCountInString(doc.IR.NormalizedText[:byteStart])
		end := start + utf8.RuneCountInString(proposed.Quote)
		bound := docir.Bind(doc.IR, proposed.Quote, start, end)
		if bound.Status != docir.StatusLocated {
			return nil, engine.ErrRejected
		}
		anchor := engine.Anchor{DocumentID: doc.Document.ID, Quote: bound.Quote, Status: bound.Status,
			Start: bound.Start, End: bound.End, Reason: bound.Reason, Page: bound.Page, ElementID: bound.ElementID}
		drafts = append(drafts, engine.Draft{Domain: proposed.Domain, Origin: "model", Relation: "insufficient",
			Claim: proposed.Claim, Disposition: "insufficient_evidence", Anchors: []engine.Anchor{anchor},
			UnverifiedReason: fmt.Sprintf("模型建议尚需人工核验；引用制度 %s 版本 %s：%s", policy.ID, policy.Version, proposed.PolicyClause)})
	}
	return drafts, nil
}

func (s *Store) telecomAttempt(st state, runID string) bool {
	run, ok := st.runByID(runID)
	if !ok {
		return false
	}
	item, ok := st.caseByID(run.CaseID)
	return ok && isGovernedReviewCase(item)
}

func (s *Store) authorizeTelecomStage(ctx context.Context, runID, attemptID string) (Case, access.Principal, access.Resource, error) {
	st, err := s.snapshotForAttemptMetadataContext(ctx, attemptID)
	if err != nil {
		return Case{}, access.Principal{}, access.Resource{}, err
	}
	run, ok := st.runByID(runID)
	if !ok {
		return Case{}, access.Principal{}, access.Resource{}, errors.New("governed review run is unavailable")
	}
	item, ok := st.caseByID(run.CaseID)
	if !ok || !isGovernedReviewCase(item) {
		return Case{}, access.Principal{}, access.Resource{}, errors.New("governed review case scope changed")
	}
	principal, resource, err := trustedTelecomReviewIdentity(st, runID, attemptID, item)
	return item, principal, resource, err
}

func (s *Store) executeTelecomAttempt(parent, ctx context.Context, runID, attemptID string, models Models, recorder attemptRecorder) {
	initial, err := s.snapshotForAttemptMetadataContext(ctx, attemptID)
	if err != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}
	if attempt, ok := initial.attemptByID(attemptID); !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
		return
	}
	priorResults, resultErr := telecomEvaluationsPresent(initial, attemptID)
	if resultErr != nil {
		if parent.Err() == nil {
			_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_failed")
		}
		return
	}

	var docs []Interpreted
	var caseItem Case
	var request rulepack.Request
	var report rulepack.Report
	var frozenSnapshot []byte
	var legacyFrozenAuthority bool
	var contentHash, snapshotDigest string
	var output telecomReviewOutput
	var modelErr error
	var toolEvidenceNote string
	var agentReviewIncomplete bool
	var agentReviewFailed bool
	if _, _, _, err := s.authorizeTelecomStage(ctx, runID, attemptID); err != nil {
		if parent.Err() == nil {
			code := "execution_failed"
			if errors.Is(err, errAttemptAuthorizationRevoked) {
				code = "authorization_revoked"
			}
			_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", code)
		}
		return
	}

	authorizeAndPrepare := execution.Stage{Name: "authorized_input", Run: func(stageCtx context.Context) error {
		authorizedCase, principal, resource, err := s.authorizeTelecomStage(stageCtx, runID, attemptID)
		if err != nil {
			return err
		}
		caseItem = authorizedCase
		if isGovernedReviewCase(caseItem) {
			stored, err := s.snapshotForAttemptMetadataContext(stageCtx, attemptID)
			if err != nil {
				return err
			}
			attempt, ok := stored.attemptByID(attemptID)
			if !ok || attempt.RunID != runID {
				return errors.New("frozen governed attempt identity is unavailable")
			}
			if attempt.RulePackCompilerRef == governedCompilerMarker {
				frozen := stored.snapshotOf(runID)
				if frozen.RunID != runID || frozen.GovernedKnowledge == nil {
					return errors.New("frozen governed review snapshot is unavailable")
				}
				actor, err := authorizeAttemptExecution(stored, runID, attemptID)
				if err != nil {
					return err
				}
				if err := s.ValidateFrozenGovernedKnowledgeSnapshot(stageCtx, actor, caseItem.ID, *frozen.GovernedKnowledge); err != nil {
					return err
				}
			}
			if isCloudServicesCase(caseItem) {
				if _, err := cloudPoliciesForAttempt(stored, caseItem, runID, attemptID); err != nil {
					return err
				}
			}
		}
		docs, err = s.prepareAttempt(stageCtx, runID, attemptID, models)
		if err != nil {
			return err
		}
		sources, err := telecomSources(docs)
		if err != nil {
			return err
		}
		request = rulepack.Request{
			Principal: principal, Resource: resource, BusinessLine: caseItem.BusinessLine,
			Scenario: caseItem.Scenario, ContractType: caseItem.ContractType, At: time.Now().UTC(), Sources: sources,
		}
		if isDeclaredReviewCase(caseItem) {
			stored := s.snapshotForAttempt(attemptID)
			frozen := stored.snapshotOf(runID)
			if frozen.ComparatorRef != engine.ComparatorRef+"+"+telecom.DeclaredCompilerRef || frozen.GovernedKnowledge == nil {
				return errors.New("declared extractor identity or business time was not frozen")
			}
			request.At, err = time.Parse(time.RFC3339Nano, frozen.GovernedKnowledge.BusinessAt)
			if err != nil || request.At.IsZero() {
				return errors.New("declared review business time is invalid")
			}
		}
		return nil
	}}

	stages := []execution.Stage{authorizeAndPrepare}
	if priorResults == 0 {
		stages = append(stages,
			execution.Stage{Name: "fact_extraction", Run: func(stageCtx context.Context) error {
				_, principal, resource, authErr := s.authorizeTelecomStage(stageCtx, runID, attemptID)
				if authErr != nil {
					return authErr
				}
				request.Principal, request.Resource = principal, resource
				var facts []rulepack.Fact
				var err error
				if isDeclaredReviewCase(caseItem) {
					stored := s.snapshotForAttempt(attemptID)
					attempt, ok := stored.attemptByID(attemptID)
					if !ok || attempt.RunID != runID {
						return errors.New("declared review attempt is unavailable")
					}
					pack, loadErr := ParseFrozenGovernedRulePack(attempt, caseItem)
					if loadErr != nil {
						return loadErr
					}
					if pack == nil {
						return errors.New("declared review requires a frozen published RulePack")
					}
					facts, err = telecom.ExtractDeclared(request, pack)
				} else {
					facts, err = telecom.Extract(request)
				}
				if err != nil {
					return err
				}
				request.Facts = facts
				return nil
			}},
			execution.Stage{Name: "tool_evidence", Run: func(stageCtx context.Context) error {
				if isDeclaredReviewCase(caseItem) {
					return nil
				}
				// Only a durable worker claim with a configured reader consumes the
				// tool: the legacy in-process pipeline keeps its established
				// behaviour, and a missing reader is never treated as success.
				claim, ok := s.claimForAttempt(attemptID)
				if executionScopeRequiresClaim(stageCtx) && (!ok || !claimMatchesExecutionScope(stageCtx, claim)) {
					return errLeaseLost
				}
				if !ok || claim.JobID == "" || s.toolEvidenceReader() == nil {
					return nil
				}
				outcome, err := s.consumeToolEvidence(stageCtx, runID, attemptID, claim, caseItem, request, docs)
				for _, capability := range outcome.Capabilities {
					recorder.remember(recorder.RecordExecutionEvent(stageCtx, execution.Event{
						NodeKind: "tool_call", Phase: "evidence_tool", EventType: "tool_evidence",
						Status: capability.Status, ErrorCode: capability.ReasonCode, ModelRef: capability.CapabilityID,
					}))
				}
				if len(outcome.Accepted) > 0 {
					request.Facts = appendToolEvidenceFacts(request.Facts, outcome.Accepted)
				}
				switch {
				case err != nil:
					toolEvidenceNote = "工具冻结证据不可用，已保留确定性规则结果。"
				case outcome.Incomplete:
					toolEvidenceNote = "工具冻结证据存在失败或被拒绝的必检能力，已保留确定性规则结果。"
				case len(outcome.Accepted) == 0:
					toolEvidenceNote = "工具未找到可核验语句，已保留确定性规则结果。"
				}
				return nil
			}},
			execution.Stage{Name: "rulepack", Run: func(stageCtx context.Context) error {
				_, principal, resource, authErr := s.authorizeTelecomStage(stageCtx, runID, attemptID)
				if authErr != nil {
					return authErr
				}
				request.Principal, request.Resource = principal, resource
				stored, err := s.snapshotForAttemptMetadataContext(stageCtx, attemptID)
				if err != nil {
					return err
				}
				attempt, ok := stored.attemptByID(attemptID)
				if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
					return errAttemptTerminal
				}
				var frozenGovernedPack *rulepack.Pack
				hasGovernedIdentity := attempt.RulePackID != "" || attempt.RulePackVersion != "" || attempt.RulePackPointerRevision != 0
				if attempt.RulePackCompilerRef == governedCompilerMarker || hasGovernedIdentity {
					if attempt.RulePackCompilerRef != governedCompilerMarker {
						return errors.New("frozen governed RulePack compiler identity is incomplete")
					}
					frozenGovernedPack, err = ParseFrozenGovernedRulePack(attempt, caseItem)
					if err != nil {
						return err
					}
					frozenSnapshot = append([]byte(nil), attempt.RulePackSnapshot...)
					contentHash, snapshotDigest = attempt.RulePackHash, attempt.RulePackSnapshotDigest
					request.Facts = append([]rulepack.Fact(nil), request.Facts...)
					runSnapshot := stored.snapshotOf(runID)
					if runSnapshot.GovernedKnowledge != nil {
						request.AuthorityGates, err = rulepackAuthorityGates(runSnapshot.GovernedKnowledge)
						if err != nil {
							return err
						}
					} else {
						request.AuthorityGates = nil
					}
					legacyFrozenAuthority = legacyFrozenAuthoritySemantics(runSnapshot, frozenGovernedPack)
				} else if attempt.RulePackCompilerRef != "" || len(attempt.RulePackSnapshot) > 0 ||
					attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
					// Historical snapshots keep their original compiler ref. Replaying
					// one with the current extractor would change citations while
					// the frozen identity still said it was the old compiler.
					if attempt.RulePackCompilerRef != telecom.CompilerRef {
						return errors.New("frozen telecom compiler identity differs; start a new review run")
					}
					if len(attempt.RulePackSnapshot) > 0 {
						frozenSnapshot = append([]byte(nil), attempt.RulePackSnapshot...)
						contentHash, snapshotDigest, err = telecomSnapshotIdentity(frozenSnapshot)
						if err != nil || contentHash != attempt.RulePackHash || snapshotDigest != attempt.RulePackSnapshotDigest {
							return errors.New("frozen telecom RulePack identity is invalid")
						}
					} else if attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
						return errors.New("uncompiled telecom RulePack carries unexpected hashes")
					}
					request.Facts = append([]rulepack.Fact(nil), request.Facts...)
				} else {
					frozenSnapshot, err = telecom.Snapshot(request)
					if err != nil {
						return err
					}
					if len(frozenSnapshot) > 0 {
						contentHash, snapshotDigest, err = telecomSnapshotIdentity(frozenSnapshot)
						if err != nil {
							return err
						}
					} else {
						contentHash, snapshotDigest = "", ""
					}
					if err := s.freezeTelecomRulePack(stageCtx, runID, attemptID, telecom.CompilerRef, frozenSnapshot, contentHash, snapshotDigest); err != nil {
						return err
					}
				}
				if len(frozenSnapshot) > 0 && frozenGovernedPack == nil {
					pack, parseErr := rulepack.Parse(frozenSnapshot)
					if parseErr != nil || pack.Manifest().Lifecycle.State != rulepack.StatePublished {
						return errors.New("telecom compiler did not produce a published RulePack")
					}
				}
				_, principal, resource, authErr = s.authorizeTelecomStage(stageCtx, runID, attemptID)
				if authErr != nil {
					return authErr
				}
				request.Principal, request.Resource = principal, resource
				if frozenGovernedPack != nil {
					request, err = s.freezeAttemptRuleRequest(stageCtx, runID, attemptID, caseItem, request)
					if err != nil {
						return err
					}
				}
				if frozenGovernedPack != nil {
					if isDeclaredReviewCase(caseItem) {
						report, err = telecom.ExecuteDeclared(stageCtx, request, frozenGovernedPack)
					} else if legacyFrozenAuthority {
						report, err = telecom.ExecuteLegacyFrozen(stageCtx, request, frozenGovernedPack)
					} else {
						report, err = telecom.ExecuteFrozen(stageCtx, request, frozenGovernedPack)
					}
				} else {
					report, err = telecom.Execute(stageCtx, request)
				}
				if err != nil {
					return err
				}
				if len(frozenSnapshot) > 0 && report.PackHash != contentHash {
					return errors.New("telecom report does not match the frozen RulePack")
				}
				return nil
			}},
			execution.Stage{Name: "evidence_guard", Run: func(stageCtx context.Context) error {
				_, principal, resource, err := s.authorizeTelecomStage(stageCtx, runID, attemptID)
				if err != nil {
					return err
				}
				request.Principal, request.Resource = principal, resource
				output, err = guardTelecomReport(caseItem, runID, attemptID, docs, request, report, frozenSnapshot)
				return err
			}},
			execution.Stage{Name: "persistence", Run: func(stageCtx context.Context) error {
				if err := s.persistTelecomOutput(stageCtx, runID, attemptID, caseItem, output, docs, false); err != nil {
					return err
				}
				if hasVerifiedFinding(output.findings) {
					return s.recordFirstVerifiedFinding(stageCtx, recorder, attemptID)
				}
				return nil
			}},
		)
	} else {
		stages = append(stages, execution.Stage{Name: "persistence", Run: func(stageCtx context.Context) error {
			if err := s.persistTelecomOutput(stageCtx, runID, attemptID, caseItem, telecomReviewOutput{}, docs, true); err != nil {
				return err
			}
			st, err := s.snapshotForAttemptMetadataContext(stageCtx, attemptID)
			if err != nil {
				return err
			}
			findings := st.findingsForAttempt(runID, attemptID)
			if isCloudServicesCase(caseItem) {
				for _, finding := range findings {
					if finding.Origin == "rule" && hasVerifiedFinding([]Finding{finding}) {
						return s.recordFirstVerifiedFinding(stageCtx, recorder, attemptID)
					}
				}
			} else if hasVerifiedFinding(findings) {
				return s.recordFirstVerifiedFinding(stageCtx, recorder, attemptID)
			}
			return nil
		}})
	}

	if err := execution.Run(ctx, recorder, stages...); err != nil {
		if parent.Err() != nil {
			return
		}
		code := "execution_failed"
		if recorder.err() != nil {
			code = "execution_persistence_failed"
		}
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", code)
		return
	}
	if parent.Err() != nil {
		return
	}
	if recorder.err() != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}

	latest, err := s.snapshotForAttemptMetadataContext(ctx, attemptID)
	if err != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}
	if modelStage, done := telecomStageTerminal(latest.executionEvents(attemptID), "model_review"); done {
		if modelStage == "failed" {
			modelErr = errors.New("model_review_incomplete")
		}
	} else if citationStage, done := telecomStageTerminal(latest.executionEvents(attemptID), "citation_verification"); done {
		if citationStage == "failed" {
			modelErr = engine.ErrRejected
		}
	} else {
		modelErr = s.runTelecomModelReview(ctx, runID, attemptID, models, docs, caseItem, recorder)
	}
	if parent.Err() != nil {
		return
	}
	if recorder.err() != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}
	latest, err = s.snapshotForAttemptMetadataContext(ctx, attemptID)
	if err != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}
	if attempt, ok := latest.attemptByID(attemptID); ok {
		_, stageRequired, configErr := frozenTelecomPlannerConfiguration(attempt)
		if configErr != nil {
			_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "planner_configuration_integrity_failed")
			return
		}
		if stageRequired {
			claim, durable := s.claimForAttempt(attemptID)
			if executionScopeRequiresClaim(ctx) && (!durable || !claimMatchesExecutionScope(ctx, claim)) {
				return
			}
			if durable && claim.JobID != "" && claim.RunID == runID {
				agentEvents := latest.executionEvents(attemptID)
				if stageStatus, done := telecomStageTerminal(agentEvents, "agent_review"); done {
					resultStatus, resultCode := telecomAgentReviewResult(agentEvents)
					if resultStatus == "failed" || (stageStatus == "failed" && resultStatus != "partial") {
						if resultCode == "" {
							resultCode = "agent_review_failed"
						}
						_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", resultCode)
						return
					}
					agentReviewFailed = resultStatus == "partial" && resultCode == "agent_review_failed"
					agentReviewIncomplete = stageStatus != "completed" || telecomAgentReviewWasPartial(agentEvents)
				} else {
					reviewRunErr := execution.Run(ctx, recorder, execution.Stage{Name: "agent_review", Run: func(stageCtx context.Context) error {
						incomplete, err := s.runTelecomAgentReview(stageCtx, runID, attemptID, docs, models, recorder)
						if err != nil {
							return err
						}
						agentReviewIncomplete = incomplete
						status, errorCode := "completed", ""
						if incomplete {
							status, errorCode = "partial", "agent_review_incomplete"
						}
						return recordTelecomAgentReviewResult(stageCtx, recorder, status, errorCode)
					}})
					if parent.Err() != nil {
						return
					}
					if recorder.err() != nil {
						_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
						return
					}
					if reviewRunErr != nil {
						if errors.Is(reviewRunErr, errAttemptTerminal) {
							return
						}
						status, errorCode := "partial", "agent_review_failed"
						switch {
						case errors.Is(reviewRunErr, errAttemptAuthorizationRevoked):
							status, errorCode = "failed", "authorization_revoked"
						case errors.Is(reviewRunErr, rulepack.ErrUnauthorized):
							status, errorCode = "failed", "authorization_denied"
						case errors.Is(reviewRunErr, errAgentReviewScopeChanged):
							status, errorCode = "failed", "agent_review_scope_changed"
						case errors.Is(reviewRunErr, errAgentReviewPersistence):
							status, errorCode = "failed", "agent_review_persistence_failed"
						}
						if eventErr := recordTelecomAgentReviewResult(ctx, recorder, status, errorCode); eventErr != nil {
							failureCode := "execution_persistence_failed"
							if errors.Is(eventErr, errAttemptAuthorizationRevoked) {
								failureCode = "authorization_revoked"
							}
							if !errors.Is(eventErr, errAttemptTerminal) {
								_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", failureCode)
							}
							return
						}
						if status == "failed" {
							_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", errorCode)
							return
						}
						agentReviewIncomplete = true
						agentReviewFailed = true
					}
				}
			}
		}
	}
	if parent.Err() != nil {
		return
	}
	if recorder.err() != nil {
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", "execution_persistence_failed")
		return
	}

	terminalState, partialReason, errorCode := "completed", "", ""
	if modelErr != nil {
		terminalState = "partial"
		switch {
		case errors.Is(modelErr, model.ErrRequest):
			errorCode = "model_request_failed"
		case errors.Is(modelErr, model.ErrUnusable):
			errorCode = "model_output_unusable"
		case errors.Is(modelErr, engine.ErrRejected):
			errorCode = "model_evidence_rejected"
		case errors.Is(modelErr, errModelNotConfigured):
			errorCode = "model_not_configured"
		case errors.Is(modelErr, context.DeadlineExceeded):
			errorCode = "model_timeout"
		case errors.Is(modelErr, context.Canceled):
			errorCode = "model_cancelled"
		default:
			errorCode = "model_incomplete"
		}
		partialReason = publicModelError(modelErr)
	} else if toolEvidenceNote != "" {
		terminalState = "partial"
		partialReason = toolEvidenceNote
		errorCode = "tool_evidence_incomplete"
	} else if agentReviewFailed {
		terminalState = "partial"
		partialReason = "五域补充审查执行失败；专线必检规则结果保持独立。"
		errorCode = "agent_review_failed"
	} else if agentReviewIncomplete {
		terminalState = "partial"
		partialReason = "五域补充审查存在未解决或未执行的能力缺口；专线必检规则结果保持独立。"
		errorCode = "agent_review_incomplete"
	} else if readyCount(docs) < len(docs) {
		terminalState = "partial"
		partialReason = "有材料解析失败，结果不能当成完整审查。"
		errorCode = "document_parse_incomplete"
	}
	_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, terminalState, partialReason, errorCode)
}

func trustedTelecomReviewIdentity(st state, runID, attemptID string, caseItem Case) (access.Principal, access.Resource, error) {
	user, err := authorizeAttemptExecution(st, runID, attemptID)
	if err != nil {
		return access.Principal{}, access.Resource{}, err
	}
	principal := bindLocalDemoCaseGrant(caseItem, user, accounts.Principal(user))
	resource := access.Resource{TenantID: caseItem.TenantID, CaseID: caseItem.ID, OrgID: caseItem.OwnerOrgID, OwnerID: caseItem.OwnerID}
	if !access.Check(access.Review, principal, resource, time.Now().UTC()).Allowed {
		return access.Principal{}, access.Resource{}, rulepack.ErrUnauthorized
	}
	return principal, resource, nil
}

func telecomSources(docs []Interpreted) ([]rulepack.Source, error) {
	sources := make([]rulepack.Source, 0, len(docs))
	for _, doc := range docs {
		if !doc.Ready {
			continue
		}
		parsed := doc.ParsedDocument
		if parsed == nil || parsed.Status != "ready" {
			return nil, errors.New("ready telecom input lacks a frozen parsed identity")
		}
		if err := verifyParsed(*parsed, doc.Document); err != nil {
			return nil, fmt.Errorf("telecom parsed artifact integrity check: %w", err)
		}
		if doc.Text != parsed.Text || !reflect.DeepEqual(doc.IR, parsed.IR) || doc.FromOCR != parsed.FromOCR ||
			doc.ParseError != parsed.ParseError || doc.IR.CoordinateSpace != docir.CoordinateSpace {
			return nil, errors.New("telecom source differs from its frozen normalized parse")
		}
		sources = append(sources, rulepack.Source{
			ID: doc.Document.ID, Version: parsed.ID, TenantID: doc.Document.TenantID,
			CaseID: doc.Document.CaseID, Role: doc.Document.Role, Digest: parsed.NormalizedDigest, Text: parsed.Text,
		})
	}
	return sources, nil
}

func telecomSnapshotIdentity(raw []byte) (string, string, error) {
	if len(raw) == 0 {
		return "", "", errors.New("empty RulePack snapshot has no content identity")
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(raw)
	return pack.Hash(), hex.EncodeToString(digest[:]), nil
}

func (s *Store) freezeTelecomRulePack(ctx context.Context, runID, attemptID, compilerRef string, raw []byte, contentHash, snapshotDigest string) error {
	if compilerRef != telecom.CompilerRef {
		return errors.New("unknown telecom compiler identity")
	}
	if len(raw) == 0 {
		if contentHash != "" || snapshotDigest != "" {
			return errors.New("uncompiled RulePack has content hashes")
		}
	} else {
		actualHash, actualDigest, err := telecomSnapshotIdentity(raw)
		if err != nil || actualHash != contentHash || actualDigest != snapshotDigest {
			return errors.New("telecom RulePack content identity does not match its bytes")
		}
	}
	return s.updateAuthorizedExecution(ctx, runID, attemptID, func(st *state) error {
		attempt, ok := st.attemptByID(attemptID)
		if !ok || !attemptActive(attempt.State) {
			return errAttemptTerminal
		}
		run, ok := st.runByID(attempt.RunID)
		if !ok {
			return errors.New("telecom RulePack run is unavailable")
		}
		caseItem, ok := st.caseByID(run.CaseID)
		if !ok || !isTelecomDedicatedLineCase(caseItem) {
			return errors.New("telecom RulePack case scope changed")
		}
		if _, _, err := trustedTelecomReviewIdentity(*st, run.ID, attemptID, caseItem); err != nil {
			return err
		}
		if attempt.RulePackCompilerRef != "" || len(attempt.RulePackSnapshot) > 0 || attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
			if attempt.RulePackCompilerRef == compilerRef && bytes.Equal(attempt.RulePackSnapshot, raw) && attempt.RulePackHash == contentHash && attempt.RulePackSnapshotDigest == snapshotDigest {
				return nil
			}
			return errors.New("telecom RulePack snapshot is immutable once frozen")
		}
		for i := range st.Attempts {
			if st.Attempts[i].ID == attemptID {
				st.Attempts[i].RulePackSnapshot = append(st.Attempts[i].RulePackSnapshot[:0], raw...)
				st.Attempts[i].RulePackHash = contentHash
				st.Attempts[i].RulePackSnapshotDigest = snapshotDigest
				st.Attempts[i].RulePackCompilerRef = compilerRef
				return nil
			}
		}
		return errAttemptMissing
	})
}

func guardTelecomReport(caseItem Case, runID, attemptID string, docs []Interpreted, request rulepack.Request, report rulepack.Report, frozenSnapshot []byte) (telecomReviewOutput, error) {
	capabilityList, err := capabilitiesForSnapshot(caseItem, frozenSnapshot)
	if err != nil {
		return telecomReviewOutput{}, err
	}
	if len(capabilityList) == 0 || len(report.Results) != len(capabilityList) {
		return telecomReviewOutput{}, errors.New("governed report does not contain all declared results")
	}
	if isDeclaredReviewCase(caseItem) {
		pack, parseErr := rulepack.Parse(frozenSnapshot)
		if parseErr != nil || report.PackHash != pack.Hash() || report.PackID != pack.Manifest().ID || report.PackVersion != pack.Manifest().Version {
			return telecomReviewOutput{}, errors.New("declared report differs from its frozen RulePack")
		}
	}
	capabilities := make(map[string]governedCapability, len(capabilityList))
	for _, capability := range capabilityList {
		capabilities[capability.ruleID] = capability
	}
	facts := make(map[string]rulepack.Fact, len(request.Facts))
	for _, fact := range request.Facts {
		if fact.ID == "" || fact.RuleID == "" {
			return telecomReviewOutput{}, errors.New("telecom extraction returned an incomplete fact identity")
		}
		if _, exists := facts[fact.ID]; exists {
			return telecomReviewOutput{}, errors.New("telecom extraction returned a duplicate fact identity")
		}
		facts[fact.ID] = fact
	}
	if request.Resource.TenantID != caseItem.TenantID || request.Resource.OrgID != caseItem.OwnerOrgID || request.Resource.CaseID != caseItem.ID {
		return telecomReviewOutput{}, errors.New("telecom request differs from the authorized case")
	}
	verifiedSources, err := telecomSources(docs)
	if err != nil {
		return telecomReviewOutput{}, err
	}
	verified := make(map[telecomSourceKey]rulepack.Source, len(verifiedSources))
	docsBySource := make(map[telecomSourceKey]Interpreted, len(verifiedSources))
	seenDocuments := make(map[string]bool, len(verifiedSources))
	for _, source := range verifiedSources {
		key := telecomSourceKey{id: source.ID, version: source.Version}
		if _, exists := verified[key]; exists {
			return telecomReviewOutput{}, errors.New("telecom source identity is not unique")
		}
		verified[key] = source
	}
	for _, doc := range docs {
		if !doc.Ready {
			continue
		}
		if doc.Document.TenantID != caseItem.TenantID || doc.Document.OrgID != caseItem.OwnerOrgID || doc.Document.CaseID != caseItem.ID {
			return telecomReviewOutput{}, errors.New("telecom parsed document is outside the authorized case")
		}
		if seenDocuments[doc.Document.ID] {
			return telecomReviewOutput{}, errors.New("telecom document identity is not unique")
		}
		seenDocuments[doc.Document.ID] = true
		key := telecomSourceKey{id: doc.Document.ID, version: doc.ParsedDocument.ID}
		docsBySource[key] = doc
	}
	sources := make(map[telecomSourceKey]rulepack.Source, len(request.Sources))
	for _, source := range request.Sources {
		key := telecomSourceKey{id: source.ID, version: source.Version}
		if trusted, exists := verified[key]; !exists || source != trusted {
			return telecomReviewOutput{}, errors.New("telecom source is not bound to the frozen parsed document")
		}
		sources[key] = source
	}
	if len(sources) != len(verified) {
		return telecomReviewOutput{}, errors.New("telecom request omitted a frozen source")
	}
	if err := rulepack.ValidateSources(request); err != nil {
		return telecomReviewOutput{}, err
	}
	seenRules, seenCapabilities := map[string]bool{}, map[string]bool{}
	output := telecomReviewOutput{
		evaluations: make([]ConstraintEval, 0, len(report.Results)),
		findings:    []Finding{},
	}
	var drafts []engine.Draft
	var draftResultIndexes []int
	var draftObjectRefs []string
	for _, result := range report.Results {
		expected, known := capabilities[result.RuleID]
		if !known || result.CapabilityID != expected.capabilityID || result.Domain != expected.domain ||
			result.RuleVersion == "" || (expected.version != "" && result.RuleVersion != expected.version) || result.Mandatory != expected.mandatory ||
			result.ReasonCode == "" || seenRules[result.RuleID] || (!isDeclaredReviewCase(caseItem) && seenCapabilities[result.CapabilityID]) {
			return telecomReviewOutput{}, errors.New("telecom result identity is incomplete or duplicated")
		}
		if result.Relation != rulepack.RelationCompatible && result.Relation != rulepack.RelationConflict &&
			result.Relation != rulepack.RelationInsufficient && result.Relation != rulepack.RelationNotApplicable {
			return telecomReviewOutput{}, errors.New("telecom result has an unsupported relation")
		}
		seenRules[result.RuleID] = true
		seenCapabilities[result.CapabilityID] = true
		objectRefs := make([]string, 0, len(result.FactIDs))
		objectSet := make(map[string]struct{}, len(result.FactIDs))
		factCitations := make(map[rulepack.Citation][]string)
		seenFacts := make(map[string]bool, len(result.FactIDs))
		for _, factID := range result.FactIDs {
			fact, exists := facts[factID]
			if !exists || fact.RuleID != result.RuleID || seenFacts[factID] {
				return telecomReviewOutput{}, errors.New("telecom result refers to a foreign fact")
			}
			seenFacts[factID] = true
			for _, citation := range fact.Citations {
				factCitations[citation] = append(factCitations[citation], factID)
			}
			if fact.ObjectRef != "" {
				objectSet[fact.ObjectRef] = struct{}{}
			}
		}
		for objectRef := range objectSet {
			objectRefs = append(objectRefs, objectRef)
		}
		sort.Strings(objectRefs)
		evalID, err := newID("evl_")
		if err != nil {
			return telecomReviewOutput{}, err
		}
		evaluation := ConstraintEval{
			ID: evalID, RunID: runID, AttemptID: attemptID, CaseID: caseItem.ID,
			Domain: result.Domain, RuleID: result.RuleID, RuleVersion: result.RuleVersion,
			CapabilityID: result.CapabilityID, Mandatory: result.Mandatory,
			Relation: string(result.Relation), ReasonCode: result.ReasonCode,
			Reason: telecomResultReason(result), FactIDs: append([]string(nil), result.FactIDs...),
			ObjectRefs: objectRefs,
		}
		if evaluation.FactIDs == nil {
			evaluation.FactIDs = []string{}
		}
		if evaluation.ObjectRefs == nil {
			evaluation.ObjectRefs = []string{}
		}
		anchors := make([]engine.Anchor, 0, len(result.Citations))
		seenCitations := make(map[telecomSourceKey]map[[2]int]struct{}, len(result.Citations))
		seenEvidence := make(map[EvaluationEvidence]struct{}, len(result.Citations))
		for _, citation := range result.Citations {
			key := telecomSourceKey{id: citation.SourceID, version: citation.SourceVersion}
			source, exists := sources[key]
			if !exists || citation.TenantID != request.Resource.TenantID || citation.CaseID != request.Resource.CaseID ||
				citation.TenantID != source.TenantID || citation.CaseID != source.CaseID || citation.SourceDigest != source.Digest {
				return telecomReviewOutput{}, errors.New("telecom citation identity differs from the frozen source")
			}
			factIDs := factCitations[citation]
			if len(factIDs) == 0 {
				// The compiler directly cites contractual authority and SLA
				// annexes in the result without adding them to its fact IDs.
				factIDs = []string{""}
			}
			doc := docsBySource[key]
			bound := docir.Bind(doc.IR, citation.Quote, citation.Start, citation.End)
			if bound.Status != docir.StatusLocated {
				return telecomReviewOutput{}, errors.New("telecom citation does not bind to the frozen Unicode code-point span")
			}
			ref := ExperienceEvidenceRef{DocumentID: doc.Document.ID, ParsedDocumentID: doc.ParsedDocument.ID,
				NormalizedDigest: doc.ParsedDocument.NormalizedDigest, Quote: bound.Quote, Start: bound.Start, End: bound.End}
			for _, factID := range factIDs {
				evidence := EvaluationEvidence{FactID: factID, Ref: ref}
				if _, exists := seenEvidence[evidence]; !exists {
					evaluation.Evidence = append(evaluation.Evidence, evidence)
					seenEvidence[evidence] = struct{}{}
				}
			}
			bySpan := seenCitations[key]
			if bySpan == nil {
				bySpan = make(map[[2]int]struct{})
				seenCitations[key] = bySpan
			}
			span := [2]int{citation.Start, citation.End}
			if _, exists := bySpan[span]; exists {
				continue
			}
			bySpan[span] = struct{}{}
			anchors = append(anchors, engine.Anchor{
				DocumentID: doc.Document.ID, Quote: bound.Quote, Status: bound.Status,
				Start: bound.Start, End: bound.End, Reason: bound.Reason,
				Page: bound.Page, ElementID: bound.ElementID,
			})
		}
		output.evaluations = append(output.evaluations, evaluation)
		if result.Relation == rulepack.RelationCompatible || result.Relation == rulepack.RelationNotApplicable {
			continue
		}
		objectRef := ""
		if len(objectRefs) == 1 {
			objectRef = objectRefs[0]
		}
		disposition := "insufficient_evidence"
		if result.Relation == rulepack.RelationConflict {
			disposition = "confirm_risk"
		}
		drafts = append(drafts, engine.Draft{
			Domain: result.Domain, Origin: "rule", Relation: string(result.Relation),
			Claim: telecomResultClaim(result), Disposition: disposition,
			UnverifiedReason: telecomUnverifiedReason(result), Anchors: anchors,
		})
		draftResultIndexes = append(draftResultIndexes, len(output.evaluations)-1)
		draftObjectRefs = append(draftObjectRefs, objectRef)
	}
	if len(seenRules) != len(capabilities) || (!isDeclaredReviewCase(caseItem) && len(seenCapabilities) != len(capabilities)) {
		return telecomReviewOutput{}, errors.New("telecom report omitted a mandatory rule")
	}
	if len(frozenSnapshot) == 0 {
		for _, result := range report.Results {
			if result.Relation != rulepack.RelationInsufficient || !isAuthorityReason(result.ReasonCode) {
				return telecomReviewOutput{}, errors.New("telecom compiler returned no snapshot for a non-authority result")
			}
		}
	} else if report.PackHash == "" {
		return telecomReviewOutput{}, errors.New("telecom published RulePack report omitted its content hash")
	}
	made, err := materialize(caseItem.ID, runID, attemptID, stamp(), drafts, docs)
	if err != nil {
		return telecomReviewOutput{}, err
	}
	for i := range made {
		resultIndex := draftResultIndexes[i]
		made[i].RuleID = output.evaluations[resultIndex].RuleID
		made[i].CapabilityID = output.evaluations[resultIndex].CapabilityID
		made[i].ObjectRef = draftObjectRefs[i]
		output.evaluations[resultIndex].FindingID = made[i].ID
	}
	output.findings = made
	return output, nil
}

func telecomResultReason(result rulepack.Result) string {
	if result.LegalAuthorityOutcome == "unknown" {
		reason := strings.TrimSpace(result.LegalAuthorityReason)
		if reason == "" {
			reason = "legal_applicability_unresolved"
		}
		return "法律适用所需的来源或事实依据尚未核实（" + reason + "）。"
	}
	if result.LegalAuthorityOutcome == "not_applicable" {
		return "本次审查冻结的事实条件表明该规范不适用于此规则。"
	}
	switch result.ReasonCode {
	case "contractual_authority_missing":
		return "缺少采购文件或中标文件中的合同依据。"
	case "contractual_authority_ambiguous":
		return "采购文件或中标文件中的合同依据存在歧义。"
	case "authority_value_out_of_range":
		return "合同依据中的阈值超出当前规则支持范围。"
	case "authority_semantics_unsupported":
		return "合同依据的表述无法支持当前规则比较。"
	case "authority_measurement_basis_missing":
		return "合同依据未完整说明可用率的统计口径。"
	case "authority_amount_basis_missing":
		return "合同依据未说明预付款比例的计算基础。"
	case "required_fact_missing":
		return "拟签合同中没有提取到该规则所需的条款。"
	case "required_fact_ambiguous":
		return "拟签合同中存在多个候选条款，无法确定唯一约定。"
	case "minimum_bandwidth_below_threshold":
		return "拟签合同的最低保障带宽低于采购依据要求。"
	case "monthly_availability_below_threshold":
		return "拟签合同的月度可用率低于采购依据要求。"
	case "advance_payment_exceeds_ceiling":
		return "拟签合同的预付款比例超过审批上限。"
	case "minimum_bandwidth_met":
		return "拟签合同的最低保障带宽达到采购依据要求。"
	case "monthly_availability_met":
		return "拟签合同的月度可用率达到采购依据要求。"
	case "advance_payment_within_ceiling":
		return "拟签合同的预付款比例未超过审批上限。"
	default:
		return fmt.Sprintf("规则 %s 的审查结果为 %s（%s）。", result.RuleID, result.Relation, result.ReasonCode)
	}
}

func telecomResultClaim(result rulepack.Result) string {
	reason := telecomResultReason(result)
	if result.Relation == rulepack.RelationConflict {
		return reason
	}
	return fmt.Sprintf("规则 %s 信息不足：%s", result.RuleID, reason)
}

func telecomUnverifiedReason(result rulepack.Result) string {
	if result.Relation != rulepack.RelationInsufficient {
		return ""
	}
	// The report gate for the no-authority path requires the displayed claim and
	// the unverified reason to be the same string, so the reason carries the same
	// rule identity prefix instead of a bare sentence.
	return telecomResultClaim(result)
}

func isAuthorityReason(reason string) bool {
	switch reason {
	case "contractual_authority_missing", "contractual_authority_ambiguous", "authority_value_out_of_range",
		"authority_semantics_unsupported", "authority_measurement_basis_missing", "authority_amount_basis_missing":
		return true
	default:
		return false
	}
}

// validateTelecomEvidenceInput binds the guarded citations to the immutable
// input selected for this attempt before publishing the evaluation rows.
func validateTelecomEvidenceInput(st state, attempt Attempt, caseItem Case, output telecomReviewOutput, docs []Interpreted) error {
	input, ok := st.attemptInput(attempt.ID)
	if !ok || attempt.HistoryKind != "frozen" || attempt.InputHash == nil || *attempt.InputHash == "" ||
		input.AttemptID != attempt.ID || input.RunID != attempt.RunID || input.InputHash != *attempt.InputHash ||
		!attemptSelectionsMatchFrozen(st.parseSelectionsForAttempt(attempt.ID), input.Documents) ||
		!frozenInputMatchesSnapshot(st.snapshotOf(attempt.RunID), input.Documents) {
		return errors.New("telecom evaluations lack the selected frozen attempt input")
	}
	frozenByID := make(map[string]FrozenDocument, len(input.Documents))
	for _, frozen := range input.Documents {
		if _, exists := frozenByID[frozen.Document.ID]; exists {
			return errors.New("telecom frozen input contains duplicate documents")
		}
		frozenByID[frozen.Document.ID] = frozen
	}
	docsByID := make(map[string]Interpreted, len(docs))
	for _, doc := range docs {
		if _, exists := docsByID[doc.Document.ID]; exists {
			return errors.New("telecom interpreted input contains duplicate documents")
		}
		docsByID[doc.Document.ID] = doc
	}
	for _, evaluation := range output.evaluations {
		if evaluation.RunID != attempt.RunID || evaluation.AttemptID != attempt.ID || evaluation.CaseID != caseItem.ID {
			return errors.New("telecom evaluation is outside the frozen attempt")
		}
		for _, evidence := range evaluation.Evidence {
			ref := evidence.Ref
			factPresent := evidence.FactID == ""
			for _, factID := range evaluation.FactIDs {
				if factID == evidence.FactID && factID != "" {
					factPresent = true
					break
				}
			}
			frozen, found := frozenByID[ref.DocumentID]
			doc, interpreted := docsByID[ref.DocumentID]
			if !factPresent || !found || !interpreted || !frozen.Ready || !doc.Ready || doc.ParsedDocument == nil ||
				frozen.Document.TenantID != caseItem.TenantID || frozen.Document.OrgID != caseItem.OwnerOrgID ||
				frozen.Document.CaseID != caseItem.ID || frozen.OriginalHash != frozen.Document.Digest ||
				doc.Document.ID != frozen.Document.ID || doc.Document.Digest != frozen.Document.Digest ||
				doc.Document.TenantID != frozen.Document.TenantID || doc.Document.OrgID != frozen.Document.OrgID ||
				doc.Document.CaseID != frozen.Document.CaseID || doc.Document.Role != frozen.Document.Role ||
				frozen.ParsedDocumentID != ref.ParsedDocumentID || frozen.NormalizedHash == nil || *frozen.NormalizedHash != ref.NormalizedDigest {
				return errors.New("telecom citation is outside the selected frozen document")
			}
			parsed := parsedDocumentFromFrozen(frozen)
			stored, exists := st.parsedByID(parsed.ID)
			if !exists || verifyParsed(parsed, frozen.Document) != nil || !reflect.DeepEqual(parsed, stored) ||
				!reflect.DeepEqual(parsed, *doc.ParsedDocument) || doc.Text != parsed.Text || !reflect.DeepEqual(doc.IR, parsed.IR) ||
				docir.Bind(parsed.IR, ref.Quote, ref.Start, ref.End).Status != docir.StatusLocated {
				return errors.New("telecom citation differs from the verified frozen parse")
			}
		}
	}
	return nil
}

func (s *Store) persistTelecomOutput(ctx context.Context, runID, attemptID string, caseItem Case, output telecomReviewOutput, docs []Interpreted, alreadyPresent bool) error {
	return s.updateAuthorizedGovernedExecution(ctx, runID, attemptID, func(st *state) error {
		attempt, ok := st.attemptByID(attemptID)
		if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
			return errAttemptTerminal
		}
		run, ok := st.runByID(runID)
		if !ok || run.CaseID != caseItem.ID {
			return errors.New("telecom persistence run identity changed")
		}
		currentCase, ok := st.caseByID(caseItem.ID)
		if !ok || !isGovernedReviewCase(currentCase) {
			return errors.New("governed persistence case scope changed")
		}
		if _, _, err := trustedTelecomReviewIdentity(*st, runID, attemptID, currentCase); err != nil {
			return err
		}
		if isCloudServicesCase(currentCase) {
			if _, err := cloudPoliciesForAttempt(*st, currentCase, runID, attemptID); err != nil {
				return err
			}
		}
		count, err := telecomEvaluationsPresent(*st, attemptID)
		if err != nil {
			return err
		}
		capabilities, err := capabilitiesForSnapshot(currentCase, attempt.RulePackSnapshot)
		if err != nil || len(capabilities) == 0 {
			return errors.New("frozen governed rule coverage is unavailable")
		}
		if alreadyPresent {
			if count != len(capabilities) {
				return errors.New("persisted telecom results are incomplete")
			}
			return nil
		}
		if count != 0 {
			if count == len(output.evaluations) && len(output.evaluations) == len(capabilities) {
				return nil
			}
			return errors.New("telecom results would overwrite an existing partial report")
		}
		if len(output.evaluations) != len(capabilities) {
			return errors.New("governed persistence requires every declared result")
		}
		if attempt.RulePackCompilerRef != telecom.CompilerRef && attempt.RulePackCompilerRef != governedCompilerMarker {
			return errors.New("telecom RulePack compiler identity was not frozen before results")
		}
		if err := validateTelecomEvidenceInput(*st, attempt, currentCase, output, docs); err != nil {
			return err
		}
		st.Findings = append(st.Findings, output.findings...)
		st.Evaluations = append(st.Evaluations, output.evaluations...)
		return nil
	})
}

func telecomEvaluationsPresent(st state, attemptID string) (int, error) {
	attempt, ok := st.attemptByID(attemptID)
	if !ok {
		return 0, errors.New("governed review attempt is unavailable")
	}
	run, ok := st.runByID(attempt.RunID)
	if !ok {
		return 0, errors.New("governed review run is unavailable")
	}
	caseItem, ok := st.caseByID(run.CaseID)
	if !ok {
		return 0, errors.New("governed review case is unavailable")
	}
	if isDeclaredReviewCase(caseItem) {
		pack, err := ParseFrozenGovernedRulePack(attempt, caseItem)
		if err != nil || pack == nil {
			return 0, errors.New("declared review has no valid frozen RulePack")
		}
	}
	capabilities, err := capabilitiesForSnapshot(caseItem, attempt.RulePackSnapshot)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		known[capability.ruleID] = true
	}
	if len(known) == 0 {
		return 0, errors.New("attempt is outside a governed review scope")
	}
	seen := make(map[string]bool, len(known))
	count := 0
	for _, evaluation := range st.Evaluations {
		if evaluation.AttemptID != attemptID || !known[evaluation.RuleID] {
			continue
		}
		if seen[evaluation.RuleID] {
			return 0, errors.New("duplicate persisted governed result")
		}
		seen[evaluation.RuleID] = true
		count++
	}
	if count != 0 && count != len(known) {
		return 0, errors.New("incomplete persisted governed result set")
	}
	return count, nil
}

func (s *Store) runTelecomModelReview(ctx context.Context, runID, attemptID string, models Models, docs []Interpreted, caseItem Case, recorder attemptRecorder) error {
	var proposal model.Warranty
	var cloudProposals []struct {
		policy model.CloudPolicy
		review model.CloudReview
	}
	var modelErr error
	stages := []execution.Stage{
		{Name: "model_review", Run: func(stageCtx context.Context) error {
			if _, _, _, err := s.authorizeTelecomStage(stageCtx, runID, attemptID); err != nil {
				modelErr = err
				return err
			}
			if models == nil {
				modelErr = errModelNotConfigured
				return modelErr
			}
			if isCloudServicesCase(caseItem) {
				materials := make([]model.Material, 0, len(docs))
				for _, doc := range docs {
					if doc.Document.Role == "draft_contract" && doc.Ready {
						materials = append(materials, model.Material{ID: doc.Document.ID, Role: doc.Document.Role, Text: doc.Text, Ready: true})
					}
				}
				if len(materials) == 0 {
					modelErr = errors.New("insufficient_ready_documents")
					return modelErr
				}
				reviewer, ok := models.(interface {
					CloudContract(context.Context, model.CloudPolicy, []model.Material) (model.CloudReview, error)
				})
				if !ok {
					modelErr = errModelNotConfigured
					return modelErr
				}
				stored, err := s.snapshotForAttemptMetadataContext(stageCtx, attemptID)
				if err != nil {
					modelErr = err
					return err
				}
				policies, err := cloudPoliciesForAttempt(stored, caseItem, runID, attemptID)
				if err != nil {
					modelErr = err
					return err
				}
				for _, ref := range policies {
					policy, err := s.cloudPolicyForReview(stageCtx, caseItem, runID, attemptID, ref)
					if err != nil {
						modelErr = err
						return err
					}
					review, err := reviewer.CloudContract(stageCtx, policy, materials)
					if err != nil {
						modelErr = err
						return err
					}
					cloudProposals = append(cloudProposals, struct {
						policy model.CloudPolicy
						review model.CloudReview
					}{policy: policy, review: review})
				}
			} else {
				if readyCount(docs) < 2 {
					modelErr = errors.New("insufficient_ready_documents")
					return modelErr
				}
				materials := make([]model.Material, len(docs))
				for i, doc := range docs {
					materials[i] = model.Material{ID: doc.Document.ID, Role: doc.Document.Role, Text: doc.Text, Ready: doc.Ready, ParseError: doc.ParseError}
				}
				proposal, modelErr = models.Warranty(stageCtx, materials)
			}
			if recordErr := recorder.err(); recordErr != nil {
				return recordErr
			}
			return modelErr
		}},
		{Name: "citation_verification", Run: func(stageCtx context.Context) error {
			if _, _, _, err := s.authorizeTelecomStage(stageCtx, runID, attemptID); err != nil {
				modelErr = err
				return err
			}
			var drafts []engine.Draft
			var err error
			if isCloudServicesCase(caseItem) {
				for _, proposed := range cloudProposals {
					var reviewed []engine.Draft
					reviewed, err = cloudModelDrafts(proposed.review, docs, proposed.policy)
					if err != nil {
						break
					}
					drafts = append(drafts, reviewed...)
				}
			} else {
				sources := sourcesOf(docs)
				quotes := make([]engine.QuoteRef, len(proposal.Quotes))
				for i, quote := range proposal.Quotes {
					quotes[i] = engine.QuoteRef{DocumentID: quote.DocumentID, Quote: quote.Quote}
				}
				_, drafts, err = engine.AcceptWarranty(sources, engine.Proposal{
					Claim: proposal.Claim, Relation: proposal.Relation,
					Disposition: proposal.Disposition, Quotes: quotes,
				})
			}
			if err != nil {
				modelErr = err
				return err
			}
			made, err := materialize(caseItem.ID, runID, attemptID, stamp(), drafts, docs)
			if err != nil {
				modelErr = err
				return err
			}
			var added []Finding
			if err := s.updateAuthorizedGovernedExecution(stageCtx, runID, attemptID, func(next *state) error {
				attempt, ok := next.attemptByID(attemptID)
				if !ok || attempt.RunID != runID || !attemptActive(attempt.State) {
					return errAttemptTerminal
				}
				currentCase, ok := next.caseByID(caseItem.ID)
				if !ok {
					return errors.New("telecom model case is unavailable")
				}
				if _, _, err := trustedTelecomReviewIdentity(*next, runID, attemptID, currentCase); err != nil {
					return err
				}
				if isCloudServicesCase(currentCase) {
					if _, err := cloudPoliciesForAttempt(*next, currentCase, runID, attemptID); err != nil {
						return err
					}
				}
				for _, finding := range made {
					idx := -1
					for i := range next.Findings {
						current := &next.Findings[i]
						if current.AttemptID == attemptID && current.Origin == finding.Origin && current.Domain == finding.Domain && current.Relation == finding.Relation && current.Claim == finding.Claim &&
							(!isCloudServicesCase(currentCase) || current.UnverifiedReason == finding.UnverifiedReason) {
							idx = i
							break
						}
					}
					if idx < 0 {
						next.Findings = append(next.Findings, finding)
						added = append(added, finding)
						continue
					}
					existing := &next.Findings[idx]
					existing.DispositionSuggestion = finding.DispositionSuggestion
					existing.UnverifiedReason = finding.UnverifiedReason
					existing.Severity = finding.Severity
					existing.SeveritySource = finding.SeveritySource
					for _, anchor := range finding.Anchors {
						found := false
						for _, existingAnchor := range existing.Anchors {
							if existingAnchor.DocumentID == anchor.DocumentID && existingAnchor.ParsedDocumentID == anchor.ParsedDocumentID &&
								existingAnchor.Quote == anchor.Quote && existingAnchor.Status == anchor.Status &&
								existingAnchor.Start == anchor.Start && existingAnchor.End == anchor.End {
								found = true
								break
							}
						}
						if !found {
							existing.Anchors = append(existing.Anchors, anchor)
						}
					}
				}
				return nil
			}); err != nil {
				modelErr = err
				return err
			}
			if !isCloudServicesCase(caseItem) && hasVerifiedFinding(added) {
				return s.recordFirstVerifiedFinding(stageCtx, recorder, attemptID)
			}
			return nil
		}},
	}
	if err := execution.Run(ctx, recorder, stages...); err != nil {
		if recorder.err() != nil {
			return recorder.err()
		}
		if modelErr != nil {
			return modelErr
		}
		return err
	}
	return modelErr
}

func telecomStageTerminal(events []ExecutionEvent, phase string) (string, bool) {
	status := ""
	for _, event := range events {
		if event.Phase != phase {
			continue
		}
		if event.EventType == "node_completed" || event.EventType == "node_failed" {
			status = event.Status
		}
	}
	return status, status != ""
}

func telecomAgentReviewWasPartial(events []ExecutionEvent) bool {
	status := ""
	for _, event := range events {
		if event.Phase == "agent_review_result" && event.EventType == "agent_review_event" {
			status = event.Status
		}
	}
	return status != "" && status != "completed"
}

func telecomAgentReviewResult(events []ExecutionEvent) (string, string) {
	status, errorCode := "", ""
	for _, event := range events {
		if event.Phase != "agent_review_result" || event.EventType != "agent_review_event" {
			continue
		}
		status = event.Status
		if event.ErrorCode == nil {
			errorCode = ""
		} else {
			errorCode = *event.ErrorCode
		}
	}
	return status, errorCode
}

func recordTelecomAgentReviewResult(ctx context.Context, recorder attemptRecorder, status, errorCode string) error {
	return recorder.RecordExecutionEvent(ctx, execution.Event{
		ParentSpanID: execution.ParentSpanID(ctx), NodeKind: "agent_review", Phase: "agent_review_result",
		EventType: "agent_review_event", At: time.Now().UTC(), Status: status, ErrorCode: errorCode,
	})
}

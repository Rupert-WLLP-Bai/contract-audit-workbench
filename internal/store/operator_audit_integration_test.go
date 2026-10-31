package store

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	"contractaudit/internal/fulfillment"
	"contractaudit/internal/reviewimpact"
)

type operatorFixedConnector struct {
	observation auditext.Observation
}

type operatorBlockingConnector struct {
	observation auditext.Observation
	reads       atomic.Int32
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
}

func isForbiddenFault(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 403
}

func isNotFoundFault(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 404
}

func (c operatorFixedConnector) ID() string { return c.observation.ConnectorID }

func (c operatorFixedConnector) Read(context.Context, auditext.ReadRequest) (auditext.Observation, error) {
	return c.observation, nil
}

func (c *operatorBlockingConnector) ID() string { return c.observation.ConnectorID }

func (c *operatorBlockingConnector) Read(ctx context.Context, _ auditext.ReadRequest) (auditext.Observation, error) {
	c.reads.Add(1)
	c.startOnce.Do(func() { close(c.started) })
	select {
	case <-ctx.Done():
		return auditext.Observation{}, ctx.Err()
	case <-c.release:
		return c.observation, nil
	}
}

func TestDurableOperatorAuditFlow(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for the operator audit flow")
	}
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	reviewer, found := accounts.ByID("demo-reviewer")
	if !found {
		t.Fatal("demo reviewer missing")
	}
	ctx := context.Background()
	core := environment.openRole(t, "core")
	coreProposalRetry := environment.openRole(t, "core")
	document := environment.openRole(t, "document")
	execution := environment.openRole(t, "execution")

	pack := publishSyntheticAcceptancePack(t, core, admin)
	publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID, err := core.CreateCaseWithBusiness(admin, "专线跨系统核验", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	users := accounts.List()
	for i := range users {
		if users[i].Issuer == "" {
			users[i].Issuer = "https://p3-test-directory.invalid"
		}
		if users[i].Subject == "" {
			users[i].Subject = "principal:" + users[i].ID
		}
		// Replacing the local directory removes its implicit demo membership
		// binding. Preserve the intended case access explicitly for this
		// isolated trusted-principal fixture.
		switch users[i].ID {
		case admin.ID:
			users[i].Grants = []access.Grant{{Role: "case_owner", Scope: "case", CaseID: caseID}}
		case reviewer.ID:
			users[i].Grants = []access.Grant{{Role: "reviewer", Scope: "case", CaseID: caseID}}
		}
	}
	reviewerTwo := accounts.User{ID: "p3-mapping-reviewer-two", DisplayName: "映射独立审查人二", Role: "reviewer",
		TenantID: admin.TenantID, OrgID: admin.OrgID, Enabled: true, Issuer: "https://p3-test-directory.invalid", Subject: "mapping-reviewer-two",
		Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: caseID}}}
	users = append(users, reviewerTwo)
	directoryBytes, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{Users: users})
	if err != nil {
		t.Fatal(err)
	}
	directoryPath := filepath.Join(t.TempDir(), "p3-mapping-reviewers.json")
	if err := os.WriteFile(directoryPath, directoryBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directoryPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	admin, found = accounts.ByID("demo-admin")
	if !found {
		t.Fatal("admin disappeared after configuring the isolated trusted reviewer fixture")
	}
	reviewer, found = accounts.ByID("demo-reviewer")
	if !found {
		t.Fatal("reviewer disappeared after configuring the isolated trusted reviewer fixture")
	}
	reviewerTwo, found = accounts.ByID(reviewerTwo.ID)
	if !found {
		t.Fatal("second reviewer missing from the trusted directory fixture")
	}
	setReviewerTwoCaseGrant := func(enabled bool) {
		currentUsers := accounts.List()
		for i := range currentUsers {
			if currentUsers[i].ID == reviewerTwo.ID {
				if enabled {
					currentUsers[i].Grants = []access.Grant{{Role: "reviewer", Scope: "case", CaseID: caseID}}
				} else {
					currentUsers[i].Grants = []access.Grant{}
				}
			}
		}
		payload, err := json.Marshal(struct {
			Users []accounts.User `json:"users"`
		}{Users: currentUsers})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(directoryPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := accounts.ConfigureDirectory(directoryPath); err != nil {
			t.Fatal(err)
		}
		var ok bool
		reviewerTwo, ok = accounts.ByID("p3-mapping-reviewer-two")
		if !ok {
			t.Fatal("second reviewer disappeared while refreshing its explicit case grant")
		}
	}
	if _, err := document.AddDocument(ctx, admin, caseID, "draft_contract", "draft.txt", "text/plain",
		[]byte("拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。")); err != nil {
		t.Fatalf("add draft contract: %v", err)
	}
	if _, err := document.AddDocument(ctx, admin, caseID, "procurement", "award.txt", "text/plain",
		[]byte("采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。审批上限 20%。")); err != nil {
		t.Fatalf("add procurement source: %v", err)
	}
	if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatalf("authorize independent mapping reviewer for the case: %v", err)
	}
	if err := core.AddMember(admin, caseID, reviewerTwo.ID); err != nil {
		t.Fatalf("authorize second independent mapping reviewer for the case: %v", err)
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	businessAt, err := time.Parse(time.RFC3339Nano, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept review: %v", err)
	}
	claim, err := execution.ClaimNextJob(ctx, "operator-audit-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed %q, want %q", claim.JobID, accepted.JobID)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("run frozen review: %v", err)
	}
	detail, err := core.GetCase(admin, caseID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	contractID := ""
	for _, doc := range detail.Documents {
		if doc.Document.Role == "draft_contract" {
			contractID = doc.Document.ID
		}
	}
	if contractID == "" {
		t.Fatal("draft contract identity missing")
	}
	frozenState, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatalf("read the immutable attempt input: %v", err)
	}
	attemptInput, ok := frozenState.attemptInput(accepted.AttemptID)
	if !ok {
		t.Fatal("accepted attempt has no frozen input")
	}
	var frozenDraft *FrozenDocument
	for i := range attemptInput.Documents {
		if attemptInput.Documents[i].Document.ID == contractID {
			frozenDraft = &attemptInput.Documents[i]
			break
		}
	}
	if frozenDraft == nil || frozenDraft.Material == nil || frozenDraft.ParsedDocumentID == "" {
		t.Fatal("accepted attempt did not pin the draft material and parse")
	}
	materialVersion := 0
	for _, version := range frozenState.MaterialVersions {
		if version.MaterialID == frozenDraft.Material.MaterialID && version.DocumentID == contractID {
			materialVersion = version.Version
			break
		}
	}
	if materialVersion < 1 {
		t.Fatal("frozen draft has no immutable material version")
	}
	serviceObject, replayed, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectDedicatedLine, DisplayName: "专线 LINE-001",
		Aliases: []ServiceObjectAlias{{Kind: "circuit", Value: "LINE-001"}},
	}, "operator-service-object")
	if err != nil || replayed {
		t.Fatalf("create service identity for operator mapping: result=%+v replayed=%v err=%v", serviceObject, replayed, err)
	}
	associationSource := ServiceObjectAssociationPreviewInput{RunID: accepted.RunID, AttemptID: accepted.AttemptID,
		MaterialID: frozenDraft.Material.MaterialID, MaterialVersion: materialVersion, DocumentID: contractID,
		ParsedDocumentID: frozenDraft.ParsedDocumentID}
	preview, err := core.PreviewServiceObjectAssociation(ctx, admin, caseID, serviceObject.ServiceObjectID, associationSource)
	if err != nil || len(preview.Options) == 0 {
		t.Fatalf("preview the service identity's exact frozen association: candidates=%+v err=%v", preview.Options, err)
	}
	var objectOption *ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == "LINE-001" {
			objectOption = &preview.Options[i]
			break
		}
	}
	if objectOption == nil {
		t.Fatalf("frozen attempt did not resolve LINE-001: %+v", preview.Options)
	}
	_, _, err = core.ConfirmServiceObjectAssociation(ctx, admin, caseID, serviceObject.ServiceObjectID, "operator-service-association", ServiceObjectAssociationConfirmInput{
		Confirmed: true, ExpectedObjectVersion: preview.ExpectedObjectVersion, ExpectedAssociationVersion: preview.ExpectedAssociationVersion,
		Source: associationSource, ObjectRef: objectOption.ObjectRef, FactsSHA256: objectOption.FactsSHA256, CandidatesSHA256: preview.CandidatesSHA256,
	})
	if err != nil {
		t.Fatalf("confirm service identity's frozen association: %v", err)
	}

	bindingInput := OperatorObjectBindingInput{
		ContractObject:  OperatorContractObject{ObjectType: "service", ObjectID: "LINE-001", Label: strPtr("专线 LINE-001")},
		ExternalObject:  OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-001"},
		ServiceObjectID: serviceObject.ServiceObjectID, ExpectedObjectVersion: serviceObject.Version + 1, ExpectedAssociationVersion: 1,
		EffectiveFrom: businessAt.Add(-24 * time.Hour), EffectiveUntil: businessAt.Add(24 * time.Hour), Confirmed: true,
	}
	if _, err := core.CreateOperatorObjectBinding(ctx, admin, caseID, OperatorObjectBindingInput{
		ContractObject: bindingInput.ContractObject, ExternalObject: bindingInput.ExternalObject,
		EffectiveFrom: bindingInput.EffectiveFrom, EffectiveUntil: bindingInput.EffectiveUntil, Confirmed: true,
	}, "operator-binding-unpinned"); !isConflictFault(err) {
		t.Fatalf("new operator mapping accepted without a stable service-object pin: %v", err)
	}
	binding, err := core.CreateOperatorObjectBinding(ctx, admin, caseID, bindingInput, "operator-binding-1")
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if binding.Status != "confirmed" || binding.Version == "" || binding.MappingID == "" || binding.MappingVersion != 1 ||
		binding.ServiceObjectID != serviceObject.ServiceObjectID || binding.ObjectVersion != bindingInput.ExpectedObjectVersion || binding.AssociationVersion != 1 {
		t.Fatalf("unexpected binding: %+v", binding)
	}
	replayedBinding, err := core.CreateOperatorObjectBinding(ctx, admin, caseID, bindingInput, "operator-binding-1")
	firstBody, marshalFirstErr := json.Marshal(binding)
	replayBody, marshalReplayErr := json.Marshal(replayedBinding)
	if err != nil || marshalFirstErr != nil || marshalReplayErr != nil || !bytes.Equal(firstBody, replayBody) {
		t.Fatalf("exact mapping retry did not return its original row: result=%+v err=%v", replayedBinding, err)
	}
	changedBindingInput := bindingInput
	changedBindingInput.ExpectedAssociationVersion++
	if _, err := core.CreateOperatorObjectBinding(ctx, admin, caseID, changedBindingInput, "operator-binding-1"); !isConflictFault(err) {
		t.Fatalf("mapping idempotency key accepted a different service-object tuple: %v", err)
	}
	var lineageVersion int64
	var lineageBindingID string
	if err := environment.harness.pool.QueryRow(ctx, `SELECT current_version,current_binding_version FROM operator_mapping_lineages
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND mapping_id=$4`, admin.TenantID, admin.OrgID, caseID, binding.MappingID).
		Scan(&lineageVersion, &lineageBindingID); err != nil || lineageVersion != 1 || lineageBindingID != binding.BindingID {
		t.Fatalf("new mapping did not create its exact version-1 head: version=%d binding=%s err=%v", lineageVersion, lineageBindingID, err)
	}
	quote := []rune(frozenDraft.Text)
	basisDigest := sha256.Sum256([]byte(string(quote)))
	basisRefs, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: frozenDraft.Material.MaterialID,
		MaterialVersion: materialVersion, DocumentID: contractID, ParsedDocumentID: frozenDraft.ParsedDocumentID,
		Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(basisDigest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	cas := OperatorMappingChangeCAS{ExpectedMappingVersion: binding.MappingVersion, ExpectedBindingVersion: binding.Version,
		ExpectedObjectVersion: binding.ObjectVersion, ExpectedAssociationVersion: binding.AssociationVersion}
	patch := OperatorMappingChangePatch{Kind: OperatorMappingReplace,
		ExternalObject: &OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-001-replacement"},
		EffectiveFrom:  binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
	previewChange, err := core.PreviewOperatorMappingChange(ctx, admin, caseID, binding.MappingID, cas, patch, basisRefs)
	if err != nil {
		t.Fatalf("preview mapping correction from pinned contract basis: %v", err)
	}
	proposalInput := OperatorMappingChangeCreateInput{CAS: cas, Patch: patch, BasisRefs: basisRefs, ImpactSHA256: previewChange.ImpactSHA256}
	type mappingProposalAttempt struct {
		value    OperatorMappingChangeRequest
		replayed bool
		err      error
	}
	proposalAttempts := make(chan mappingProposalAttempt, 2)
	for _, proposalStore := range []*Store{core, coreProposalRetry} {
		proposalStore := proposalStore
		go func() {
			value, replayed, err := proposalStore.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID,
				proposalInput, "operator-mapping-proposal-1")
			proposalAttempts <- mappingProposalAttempt{value: value, replayed: replayed, err: err}
		}()
	}
	firstProposalAttempt, secondProposalAttempt := <-proposalAttempts, <-proposalAttempts
	if firstProposalAttempt.err != nil || secondProposalAttempt.err != nil ||
		firstProposalAttempt.value.RequestID == "" || firstProposalAttempt.value.RequestID != secondProposalAttempt.value.RequestID ||
		firstProposalAttempt.value.State != "pending_decision" || secondProposalAttempt.value.State != "pending_decision" ||
		firstProposalAttempt.replayed == secondProposalAttempt.replayed {
		t.Fatalf("concurrent same-key mapping proposals did not create one request plus one exact replay: first=%+v second=%+v", firstProposalAttempt, secondProposalAttempt)
	}
	proposal := firstProposalAttempt.value
	if firstProposalAttempt.replayed {
		proposal = secondProposalAttempt.value
	}
	replayedProposal := firstProposalAttempt.replayed || secondProposalAttempt.replayed
	proposalReplay := secondProposalAttempt.value
	if !replayedProposal || proposalReplay.RequestID != proposal.RequestID {
		t.Fatalf("mapping proposal retry did not replay exact request: request=%+v replay=%+v", proposal, proposalReplay)
	}
	changedProposalInput := proposalInput
	changedPatch := patch
	changedExternal := *patch.ExternalObject
	changedExternal.ExternalObjectID = "fixture-line-001-different-payload"
	changedPatch.ExternalObject = &changedExternal
	changedProposalInput.Patch = changedPatch
	if _, _, err := core.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID, changedProposalInput, "operator-mapping-proposal-1"); !isConflictFault(err) {
		t.Fatalf("mapping proposal idempotency key accepted a changed request payload: %v", err)
	}
	decisionInput := OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: cas.ExpectedMappingVersion,
		ExpectedBindingVersion: cas.ExpectedBindingVersion, ExpectedObjectVersion: cas.ExpectedObjectVersion,
		ExpectedAssociationVersion: cas.ExpectedAssociationVersion, ExpectedAfterSHA256: proposal.AfterSHA256,
		ExpectedImpactSHA256: proposal.ImpactSHA256, Reason: "依据冻结合同条款修正外部映射"}
	if _, _, err := core.DecideOperatorMappingChange(ctx, admin, caseID, proposal.RequestID, decisionInput, "operator-mapping-self-decision"); !isForbiddenFault(err) {
		t.Fatalf("mapping proposer was allowed to decide their own correction: %v", err)
	}
	type mappingDecisionAttempt struct {
		actor accounts.User
		key   string
		value OperatorMappingChangeDecisionResult
		err   error
	}
	attempts := make(chan mappingDecisionAttempt, 2)
	for _, candidate := range []struct {
		actor accounts.User
		key   string
	}{{reviewer, "operator-mapping-decision-1"}, {reviewerTwo, "operator-mapping-decision-2"}} {
		candidate := candidate
		go func() {
			value, _, err := core.DecideOperatorMappingChange(ctx, candidate.actor, caseID, proposal.RequestID, decisionInput, candidate.key)
			attempts <- mappingDecisionAttempt{actor: candidate.actor, key: candidate.key, value: value, err: err}
		}()
	}
	var decision OperatorMappingChangeDecisionResult
	var winningReviewer accounts.User
	var winningDecisionKey string
	approvedCount, conflictCount := 0, 0
	for range 2 {
		attempt := <-attempts
		if attempt.err == nil && attempt.value.Decision == "approved" {
			approvedCount++
			decision, winningReviewer, winningDecisionKey = attempt.value, attempt.actor, attempt.key
		} else if isConflictFault(attempt.err) {
			conflictCount++
		} else {
			t.Fatalf("concurrent independent SOD decision returned an unexpected outcome: %+v", attempt)
		}
	}
	if approvedCount != 1 || conflictCount != 1 || decision.MappingVersion != 2 {
		t.Fatalf("concurrent independent decisions must produce exactly one terminal approval: approved=%d conflicts=%d decision=%+v", approvedCount, conflictCount, decision)
	}
	decisionReplay, replayedDecision, err := core.DecideOperatorMappingChange(ctx, winningReviewer, caseID, proposal.RequestID, decisionInput, winningDecisionKey)
	if err != nil || !replayedDecision || decisionReplay != decision {
		t.Fatalf("mapping decision retry did not replay exact response: decision=%+v replayed=%v err=%v", decisionReplay, replayedDecision, err)
	}
	updatedMapping, storedDecision, err := core.GetOperatorMappingChange(ctx, reviewer, caseID, proposal.RequestID)
	if err != nil || storedDecision == nil || updatedMapping.State != "approved" {
		t.Fatalf("mapping correction decision history is missing: request=%+v decision=%+v err=%v", updatedMapping, storedDecision, err)
	}
	archiveCAS := OperatorMappingChangeCAS{ExpectedMappingVersion: decision.MappingVersion, ExpectedBindingVersion: decision.BindingVersion,
		ExpectedObjectVersion: binding.ObjectVersion, ExpectedAssociationVersion: binding.AssociationVersion}
	archivePatch := OperatorMappingChangePatch{Kind: OperatorMappingReplace,
		ExternalObject: &OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-archive-probe"},
		EffectiveFrom:  binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
	archivePreview, err := core.PreviewOperatorMappingChange(ctx, admin, caseID, binding.MappingID, archiveCAS, archivePatch, basisRefs)
	if err != nil {
		t.Fatalf("preview pending archive-boundary proposal: %v", err)
	}
	archiveProposal, _, err := core.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID,
		OperatorMappingChangeCreateInput{CAS: archiveCAS, Patch: archivePatch, BasisRefs: basisRefs, ImpactSHA256: archivePreview.ImpactSHA256},
		"operator-archive-boundary-proposal-1")
	if err != nil {
		t.Fatalf("persist pending archive-boundary proposal: %v", err)
	}

	// Identity changes are separately proposed and approved. Approval advances
	// the object identity, its pinned association, and this mapping atomically.
	identityCAS := OperatorMappingChangeCAS{ExpectedMappingVersion: decision.MappingVersion, ExpectedBindingVersion: decision.BindingVersion,
		ExpectedObjectVersion: binding.ObjectVersion, ExpectedAssociationVersion: binding.AssociationVersion}
	identityPatch := OperatorMappingChangePatch{Kind: OperatorMappingCorrectIdentity,
		Identity: &OperatorServiceIdentityPatch{ObjectType: ServiceObjectDedicatedLine, DisplayName: "测试专线主体更正",
			BusinessCode: "LINE-CORRECTED", Aliases: []ServiceObjectAlias{{Kind: "legacy_code", Value: "LINE-OLD"}},
			BasisSummary: "依据冻结合同更正主体标识", BasisRefs: basisRefs, State: ServiceObjectConfirmed},
		EffectiveFrom: binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
	identityPreview, err := core.PreviewOperatorMappingChange(ctx, admin, caseID, binding.MappingID, identityCAS, identityPatch, basisRefs)
	if err != nil {
		t.Fatalf("preview identity correction: %v", err)
	}
	identityInput := OperatorMappingChangeCreateInput{CAS: identityCAS, Patch: identityPatch, BasisRefs: basisRefs, ImpactSHA256: identityPreview.ImpactSHA256}
	identityProposal, replayedIdentityProposal, err := core.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID, identityInput, "operator-identity-proposal-1")
	if err != nil || replayedIdentityProposal || identityProposal.State != "pending_decision" {
		t.Fatalf("propose identity correction: %+v replayed=%v err=%v", identityProposal, replayedIdentityProposal, err)
	}
	setReviewerTwoCaseGrant(false)
	if _, _, err := core.DecideOperatorMappingChange(ctx, reviewerTwo, caseID, identityProposal.RequestID,
		OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: identityCAS.ExpectedMappingVersion,
			ExpectedBindingVersion: identityCAS.ExpectedBindingVersion, ExpectedObjectVersion: identityCAS.ExpectedObjectVersion,
			ExpectedAssociationVersion: identityCAS.ExpectedAssociationVersion, ExpectedAfterSHA256: identityProposal.AfterSHA256,
			ExpectedImpactSHA256: identityProposal.ImpactSHA256, Reason: "已撤销案件决定权限"}, "operator-revoked-decision"); err == nil || (!isForbiddenFault(err) && !isNotFoundFault(err)) {
		t.Fatalf("revoked reviewer retained authority to decide the mapping proposal: %v", err)
	}
	var revokedDecisionCount int
	if err := environment.harness.pool.QueryRow(ctx, `SELECT count(*) FROM operator_mapping_change_decisions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`, admin.TenantID, admin.OrgID, caseID, identityProposal.RequestID).Scan(&revokedDecisionCount); err != nil || revokedDecisionCount != 0 {
		t.Fatalf("revoked decision attempt persisted a mapping decision: count=%d err=%v", revokedDecisionCount, err)
	}
	setReviewerTwoCaseGrant(true)
	reviewer, _ = accounts.ByID("demo-reviewer")
	coreDecisionRace := environment.openRole(t, "core")
	identityDecisionInput := OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: identityCAS.ExpectedMappingVersion,
		ExpectedBindingVersion: identityCAS.ExpectedBindingVersion, ExpectedObjectVersion: identityCAS.ExpectedObjectVersion,
		ExpectedAssociationVersion: identityCAS.ExpectedAssociationVersion, ExpectedAfterSHA256: identityProposal.AfterSHA256,
		ExpectedImpactSHA256: identityProposal.ImpactSHA256, Reason: "依据冻结合同批准主体身份更正"}
	type identityDecisionResult struct {
		decision OperatorMappingChangeDecisionResult
		replayed bool
		err      error
	}
	decisionStart := make(chan struct{})
	decisionResults := make(chan identityDecisionResult, 2)
	go func() {
		<-decisionStart
		decision, replayed, err := core.DecideOperatorMappingChange(ctx, reviewer, caseID, identityProposal.RequestID,
			identityDecisionInput, "operator-identity-decision-1")
		decisionResults <- identityDecisionResult{decision: decision, replayed: replayed, err: err}
	}()
	go func() {
		<-decisionStart
		decision, replayed, err := coreDecisionRace.DecideOperatorMappingChange(ctx, reviewerTwo, caseID, identityProposal.RequestID,
			identityDecisionInput, "operator-identity-decision-race-1")
		decisionResults <- identityDecisionResult{decision: decision, replayed: replayed, err: err}
	}()
	close(decisionStart)
	firstDecision, secondDecision := <-decisionResults, <-decisionResults
	var identityDecision OperatorMappingChangeDecisionResult
	winners, conflicts := 0, 0
	for _, result := range []identityDecisionResult{firstDecision, secondDecision} {
		switch {
		case result.err == nil && !result.replayed && result.decision.Decision == "approved" && result.decision.MappingVersion == identityCAS.ExpectedMappingVersion+1:
			winners++
			identityDecision = result.decision
		case isConflictFault(result.err):
			conflicts++
		default:
			t.Fatalf("mapping decision race returned an unexpected result: %+v", result)
		}
	}
	var identityDecisionCount int
	if err := environment.harness.pool.QueryRow(ctx, `SELECT count(*) FROM operator_mapping_change_decisions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4 AND decision='approved'`,
		admin.TenantID, admin.OrgID, caseID, identityProposal.RequestID).Scan(&identityDecisionCount); err != nil ||
		winners != 1 || conflicts != 1 || identityDecisionCount != 1 {
		t.Fatalf("concurrent independent approvers did not produce exactly one terminal decision: winners=%d conflicts=%d stored=%d err=%v", winners, conflicts, identityDecisionCount, err)
	}
	identityDetail, err := core.GetServiceObject(ctx, reviewer, caseID, serviceObject.ServiceObjectID)
	if err != nil || identityDetail.CurrentVersion != identityCAS.ExpectedObjectVersion+1 || identityDetail.Association == nil ||
		identityDetail.Association.AssociationVersion != identityCAS.ExpectedAssociationVersion+1 || identityDetail.Identity.DisplayName != "测试专线主体更正" {
		t.Fatalf("identity correction did not advance immutable identity and association pins: detail=%+v err=%v", identityDetail, err)
	}
	oldAssociation, err := core.GetServiceObjectAssociation(ctx, reviewer, caseID, serviceObject.ServiceObjectID, identityCAS.ExpectedAssociationVersion)
	if err != nil || oldAssociation.FactsSHA256 != identityDetail.Association.FactsSHA256 || !bytes.Equal(oldAssociation.FactsCanonicalJSON, identityDetail.Association.FactsCanonicalJSON) {
		t.Fatalf("identity correction changed the frozen association facts: old=%+v current=%+v err=%v", oldAssociation, identityDetail.Association, err)
	}

	combinedCAS := OperatorMappingChangeCAS{ExpectedMappingVersion: identityDecision.MappingVersion, ExpectedBindingVersion: identityDecision.BindingVersion,
		ExpectedObjectVersion: identityDetail.CurrentVersion, ExpectedAssociationVersion: identityDetail.Association.AssociationVersion}
	combinedPatch := OperatorMappingChangePatch{Kind: OperatorMappingCorrectIdentityAndMapping,
		ExternalObject: &OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-001-combined"},
		Identity: &OperatorServiceIdentityPatch{ObjectType: identityDetail.Identity.ObjectType, DisplayName: "测试专线主体联合更正",
			BusinessCode: "LINE-COMBINED", Aliases: identityDetail.Identity.Aliases, BasisSummary: "同一依据下更正主体与外部映射",
			BasisRefs: basisRefs, State: ServiceObjectConfirmed}, EffectiveFrom: binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
	combinedPreview, err := core.PreviewOperatorMappingChange(ctx, admin, caseID, binding.MappingID, combinedCAS, combinedPatch, basisRefs)
	if err != nil {
		t.Fatalf("preview combined identity/mapping correction: %v", err)
	}
	combinedProposal, _, err := core.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID,
		OperatorMappingChangeCreateInput{CAS: combinedCAS, Patch: combinedPatch, BasisRefs: basisRefs, ImpactSHA256: combinedPreview.ImpactSHA256}, "operator-combined-proposal-1")
	if err != nil {
		t.Fatalf("propose combined identity/mapping correction: %v", err)
	}
	combinedDecision, _, err := core.DecideOperatorMappingChange(ctx, reviewer, caseID, combinedProposal.RequestID,
		OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: combinedCAS.ExpectedMappingVersion,
			ExpectedBindingVersion: combinedCAS.ExpectedBindingVersion, ExpectedObjectVersion: combinedCAS.ExpectedObjectVersion,
			ExpectedAssociationVersion: combinedCAS.ExpectedAssociationVersion, ExpectedAfterSHA256: combinedProposal.AfterSHA256,
			ExpectedImpactSHA256: combinedProposal.ImpactSHA256, Reason: "批准主体与映射联合更正"}, "operator-combined-decision-1")
	if err != nil || combinedDecision.Decision != "approved" || combinedDecision.MappingVersion != combinedCAS.ExpectedMappingVersion+1 {
		t.Fatalf("approve combined identity/mapping correction: %+v err=%v", combinedDecision, err)
	}
	combinedDetail, err := core.GetServiceObject(ctx, reviewer, caseID, serviceObject.ServiceObjectID)
	if err != nil || combinedDetail.CurrentVersion != combinedCAS.ExpectedObjectVersion+1 || combinedDetail.Association == nil ||
		combinedDetail.Association.AssociationVersion != combinedCAS.ExpectedAssociationVersion+1 || combinedDetail.Identity.DisplayName != "测试专线主体联合更正" {
		t.Fatalf("combined correction did not atomically advance the object and association: %+v err=%v", combinedDetail, err)
	}
	combinedAssociation, err := core.GetServiceObjectAssociation(ctx, reviewer, caseID, serviceObject.ServiceObjectID, combinedCAS.ExpectedAssociationVersion+1)
	if err != nil || combinedAssociation.FactsSHA256 != oldAssociation.FactsSHA256 || !bytes.Equal(combinedAssociation.FactsCanonicalJSON, oldAssociation.FactsCanonicalJSON) {
		t.Fatalf("combined correction did not copy the immutable frozen facts: %+v err=%v", combinedAssociation, err)
	}

	retireCAS := OperatorMappingChangeCAS{ExpectedMappingVersion: combinedDecision.MappingVersion, ExpectedBindingVersion: combinedDecision.BindingVersion,
		ExpectedObjectVersion: combinedDetail.CurrentVersion, ExpectedAssociationVersion: combinedDetail.Association.AssociationVersion}
	retirePatch := OperatorMappingChangePatch{Kind: OperatorMappingRetireServiceObject,
		Identity: &OperatorServiceIdentityPatch{ObjectType: combinedDetail.Identity.ObjectType, DisplayName: combinedDetail.Identity.DisplayName,
			BusinessCode: combinedDetail.Identity.BusinessCode, Aliases: combinedDetail.Identity.Aliases, BasisSummary: "依据冻结材料停止服务对象",
			BasisRefs: basisRefs, State: ServiceObjectInactive}, EffectiveFrom: binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
	retirePreview, err := core.PreviewOperatorMappingChange(ctx, admin, caseID, binding.MappingID, retireCAS, retirePatch, basisRefs)
	if err != nil {
		t.Fatalf("preview service-object retirement: %v", err)
	}
	retireProposal, _, err := core.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID,
		OperatorMappingChangeCreateInput{CAS: retireCAS, Patch: retirePatch, BasisRefs: basisRefs, ImpactSHA256: retirePreview.ImpactSHA256}, "operator-retire-object-proposal-1")
	if err != nil {
		t.Fatalf("propose service-object retirement: %v", err)
	}
	retireDecision, _, err := core.DecideOperatorMappingChange(ctx, reviewer, caseID, retireProposal.RequestID,
		OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: retireCAS.ExpectedMappingVersion,
			ExpectedBindingVersion: retireCAS.ExpectedBindingVersion, ExpectedObjectVersion: retireCAS.ExpectedObjectVersion,
			ExpectedAssociationVersion: retireCAS.ExpectedAssociationVersion, ExpectedAfterSHA256: retireProposal.AfterSHA256,
			ExpectedImpactSHA256: retireProposal.ImpactSHA256, Reason: "批准服务对象停用"}, "operator-retire-object-decision-1")
	retiredDetail, retiredErr := core.GetServiceObject(ctx, reviewer, caseID, serviceObject.ServiceObjectID)
	if err != nil || retireDecision.Decision != "approved" || retiredErr != nil || retiredDetail.Identity.State != ServiceObjectInactive ||
		retiredDetail.CurrentVersion != retireCAS.ExpectedObjectVersion+1 || retiredDetail.Association.AssociationVersion != retireCAS.ExpectedAssociationVersion+1 ||
		retireDecision.MappingVersion != retireCAS.ExpectedMappingVersion+1 {
		t.Fatalf("service-object retirement did not advance all SOD-pinned versions: decision=%+v detail=%+v decideErr=%v readErr=%v", retireDecision, retiredDetail, err, retiredErr)
	}

	payloadRef := "fixture://oss/snapshot-operator-1"
	recordVersion := "oss-record-v1"
	// The connector observes this version after the business instant. The
	// later explicit evidence cutoff below and immutable interval proof make
	// this historical evaluation eligible without backdating acquisition.
	sourceAsOf := businessAt.Add(time.Minute)
	validFrom := businessAt.Add(-24 * time.Hour)
	validUntil := businessAt.Add(24 * time.Hour)
	observation := auditext.Observation{
		SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: "snapshot-operator-1",
		TenantID: admin.TenantID, CaseID: caseID, ConnectorID: "fixture.oss", SourceSystem: "fixture-oss-east",
		ObjectBindingVersion: binding.Version, ExternalObjectID: "fixture-line-001", RecordID: "fixture-line-001",
		RecordVersion: &recordVersion, ObservedAt: sourceAsOf, EffectiveFrom: validFrom,
		EffectiveUntil: validUntil, Availability: auditext.AvailabilityAvailable,
		ReasonCode: "read_succeeded", PayloadRef: &payloadRef, DataClass: auditext.DataClassSynthetic,
		TemporalProof: &auditext.TemporalProof{Kind: auditext.TemporalProofHistoricalInterval,
			SourceCatalogID: "fixture-oss-catalog", DatasetVersion: "fixture-oss-v1", RecordVersion: recordVersion,
			SourceAsOf: &sourceAsOf, ValidFrom: &validFrom, ValidUntil: &validUntil},
		Fields: []auditext.Field{{
			Name: "configured_bandwidth", Value: "80", Unit: "Mbit/s",
			SourcePointer: "/serviceCharacteristic/0/value", SemanticKind: "configured_value",
		}},
	}
	connector := &operatorBlockingConnector{observation: observation, started: make(chan struct{}), release: make(chan struct{})}
	coreForRetry := environment.openRole(t, "core")
	firstCollection := make(chan struct {
		set OperatorSnapshotSet
		err error
	}, 1)
	go func() {
		set, err := core.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, businessAt,
			"fixture.oss", "fixture-line-001", connector, "operator-collect-1")
		firstCollection <- struct {
			set OperatorSnapshotSet
			err error
		}{set: set, err: err}
	}()
	select {
	case <-connector.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first external collection never reached the connector")
	}
	activeJob, err := coreForRetry.GetOperatorCollectionJob(ctx, admin, caseID, "operator-collect-1")
	if err != nil || activeJob.State != "in_progress" || activeJob.RetryAfter == nil || activeJob.SnapshotSet != nil {
		close(connector.release)
		t.Fatalf("collection status could not be recovered while connector read was active: job=%+v err=%v", activeJob, err)
	}
	if _, err := coreForRetry.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, businessAt,
		"fixture.oss", "fixture-line-001", connector, "operator-collect-1"); !isConflictFault(err) {
		close(connector.release)
		t.Fatalf("concurrent retry of an active collection claim did not return conflict: %v", err)
	}
	if got := connector.reads.Load(); got != 1 {
		close(connector.release)
		t.Fatalf("concurrent retry invoked connector more than once: reads=%d", got)
	}
	// Force the durable lease to expire while the original connector call is
	// still outside the transaction. A retry may perform another physical
	// read, but the newer generation must fence the original worker from
	// committing after it returns.
	leaseScope := userScope(admin)
	leaseScope.CaseID = caseID
	leaseTx, err := environment.harness.pool.Begin(ctx)
	if err != nil {
		close(connector.release)
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, leaseTx, leaseScope); err != nil {
		_ = leaseTx.Rollback(ctx)
		close(connector.release)
		t.Fatal(err)
	}
	if _, err := leaseTx.Exec(ctx, `UPDATE operator_external_collection_claims SET lease_until=clock_timestamp()-interval '1 second'
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5 AND status='in_progress'`,
		admin.TenantID, admin.OrgID, caseID, admin.ID, "operator-collect-1"); err != nil {
		_ = leaseTx.Rollback(ctx)
		close(connector.release)
		t.Fatalf("expire first collection generation: %v", err)
	}
	if err := leaseTx.Commit(ctx); err != nil {
		close(connector.release)
		t.Fatalf("commit collection lease expiry: %v", err)
	}
	winningSet, err := coreForRetry.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, businessAt,
		"fixture.oss", "fixture-line-001", operatorFixedConnector{observation: observation}, "operator-collect-1")
	if err != nil {
		close(connector.release)
		t.Fatalf("retry after expired collection lease: %v", err)
	}
	close(connector.release)
	stale := <-firstCollection
	if !isConflictFault(stale.err) {
		t.Fatalf("stale collection generation committed after takeover: result=%+v err=%v", stale.set, stale.err)
	}
	set := winningSet
	var durableSnapshotCount int
	if err := environment.harness.pool.QueryRow(ctx, `SELECT count(*) FROM operator_external_snapshot_sets
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		admin.TenantID, admin.OrgID, caseID, admin.ID, "operator-collect-1").Scan(&durableSnapshotCount); err != nil || durableSnapshotCount != 1 {
		t.Fatalf("collection lease fencing must leave one durable result: count=%d err=%v", durableSnapshotCount, err)
	}
	replayedSet, err := coreForRetry.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, businessAt,
		"fixture.oss", "fixture-line-001", connector, "operator-collect-1")
	firstSetBytes, marshalFirstErr := json.Marshal(set)
	replayedSetBytes, marshalReplayErr := json.Marshal(replayedSet)
	if err != nil || replayedSet.SnapshotSetID != set.SnapshotSetID || connector.reads.Load() != 1 || marshalFirstErr != nil || marshalReplayErr != nil || !bytes.Equal(firstSetBytes, replayedSetBytes) {
		t.Fatalf("completed collection retry did not reuse the durable result: replay=%+v original=%+v reads=%d err=%v", replayedSet, set, connector.reads.Load(), err)
	}
	completedJob, err := coreForRetry.GetOperatorCollectionJob(ctx, admin, caseID, "operator-collect-1")
	if err != nil || completedJob.State != "completed" || completedJob.SnapshotSetID == nil ||
		*completedJob.SnapshotSetID != set.SnapshotSetID || completedJob.SnapshotSet == nil ||
		completedJob.CollectionID == "" || completedJob.CollectionID != set.CollectionID {
		t.Fatalf("completed collection status could not be recovered: job=%+v err=%v", completedJob, err)
	}
	completedByID, err := coreForRetry.GetOperatorCollectionJobByID(ctx, admin, caseID, completedJob.CollectionID)
	if err != nil || completedByID.CollectionID != completedJob.CollectionID || completedByID.SnapshotSetID == nil ||
		*completedByID.SnapshotSetID != set.SnapshotSetID {
		t.Fatalf("collection state could not be recovered by stable collection ID with case read: job=%+v err=%v", completedByID, err)
	}
	// Exercise a pre-collection_id row using the actual unversioned baseline
	// migration. The current ledger already records that baseline, so a normal
	// Migrate call is correctly a no-op until its legacy SQL is applied here.
	legacyConn, err := environment.harness.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection for legacy collection migration fixture: %v", err)
	}
	legacyTx, err := legacyConn.Begin(ctx)
	if err != nil {
		legacyConn.Release()
		t.Fatalf("begin legacy collection migration fixture: %v", err)
	}
	legacyScope := userScope(admin)
	legacyScope.CaseID = caseID
	if err := setLocalScope(ctx, legacyTx, legacyScope); err != nil {
		_ = legacyTx.Rollback(ctx)
		legacyConn.Release()
		t.Fatalf("scope legacy collection migration fixture: %v", err)
	}
	if _, err := legacyTx.Exec(ctx, `DROP INDEX operator_external_collection_claims_collection_id_uq;
		ALTER TABLE operator_external_collection_claims DROP COLUMN collection_id`); err != nil {
		_ = legacyTx.Rollback(ctx)
		legacyConn.Release()
		t.Fatalf("restore legacy collection claim shape: %v", err)
	}
	catalog, err := migrationCatalog()
	if err != nil {
		_ = legacyTx.Rollback(ctx)
		legacyConn.Release()
		t.Fatalf("read the legacy migration catalog: %v", err)
	}
	if err := executeMigrationSQL(ctx, legacyTx, environment.harness.schema, catalog[0].sql); err != nil {
		_ = legacyTx.Rollback(ctx)
		legacyConn.Release()
		t.Fatalf("apply the unversioned legacy collection migration: %v", err)
	}
	if err := legacyTx.Commit(ctx); err != nil {
		legacyConn.Release()
		t.Fatalf("commit legacy collection claim shape: %v", err)
	}
	legacyConn.Release()
	if err := Migrate(ctx); err != nil {
		t.Fatalf("rerun current migration for legacy collection claim: %v", err)
	}
	legacyCore, err := OpenApplicationSelection(core.dir, core.sel)
	if err != nil {
		t.Fatalf("reopen application store after legacy collection migration: %v", err)
	}
	t.Cleanup(func() { _ = legacyCore.Close() })
	if err := legacyCore.UseServiceOwner("core"); err != nil {
		t.Fatalf("set core owner after legacy collection migration: %v", err)
	}
	legacyIDHash := md5.Sum([]byte(admin.TenantID + ":" + admin.OrgID + ":" + caseID + ":" + admin.ID + ":operator-collect-1"))
	backfilledCollectionID := "col_" + hex.EncodeToString(legacyIDHash[:])
	backfilledJob, err := legacyCore.GetOperatorCollectionJobByID(ctx, admin, caseID, backfilledCollectionID)
	if err != nil || backfilledJob.State != "completed" || backfilledJob.SnapshotSetID == nil ||
		*backfilledJob.SnapshotSetID != set.SnapshotSetID {
		t.Fatalf("legacy collection claim did not migrate to a recoverable stable ID: job=%+v err=%v", backfilledJob, err)
	}
	if _, err := coreForRetry.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, businessAt.Add(time.Minute),
		"fixture.oss", "fixture-line-001", connector, "operator-collect-1"); !isConflictFault(err) || connector.reads.Load() != 1 {
		t.Fatalf("collection idempotency key accepted changed business time or called the connector: reads=%d err=%v", connector.reads.Load(), err)
	}
	if len(set.Observations) != 1 || set.SnapshotSetID == "" {
		t.Fatalf("unexpected snapshot set: %+v", set)
	}
	evaluationCutoffAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := core.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, binding.EffectiveUntil,
		"fixture.oss", "fixture-line-001", operatorFixedConnector{observation: observation}, "operator-collect-outside-binding"); !isConflictFault(err) {
		t.Fatalf("external collection accepted business_at outside the pinned binding interval: %v", err)
	}

	manifest := operatorManifestForRulePack(t, pack.Pack.ID, pack.Pack.Version)
	if _, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: binding.EffectiveUntil,
		EvidenceCutoffAt: &evaluationCutoffAt, Scope: &OperatorEvaluationScope{Kind: "explicit_subset", ServiceObjectIDs: []string{serviceObject.ServiceObjectID}},
	}, manifest, fulfillment.New(), "operator-evaluation-outside-binding"); !isConflictFault(err) {
		t.Fatalf("evaluation accepted business_at at the end-exclusive binding boundary: %v", err)
	}
	evaluation, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: businessAt,
		EvidenceCutoffAt: &evaluationCutoffAt, Scope: &OperatorEvaluationScope{Kind: "explicit_subset", ServiceObjectIDs: []string{serviceObject.ServiceObjectID}},
	}, manifest, fulfillment.New(), "operator-evaluation-1")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluation.EvidenceCutoffAt == nil || !evaluation.EvidenceCutoffAt.Equal(evaluationCutoffAt) ||
		evaluation.Input.EvidenceCutoffAt == nil || !evaluation.Input.EvidenceCutoffAt.Equal(evaluationCutoffAt) {
		t.Fatalf("evaluation did not freeze its evidence cutoff: evaluation=%+v input=%+v", evaluation.EvidenceCutoffAt, evaluation.Input.EvidenceCutoffAt)
	}
	loadedEvaluation, err := coreForRetry.GetOperatorFulfillmentEvaluation(ctx, admin, caseID, evaluation.EvaluationID)
	if err != nil || loadedEvaluation.EvidenceCutoffAt == nil || !loadedEvaluation.EvidenceCutoffAt.Equal(evaluationCutoffAt) ||
		loadedEvaluation.Input.EvidenceCutoffAt == nil || !loadedEvaluation.Input.EvidenceCutoffAt.Equal(evaluationCutoffAt) {
		t.Fatalf("evaluation cutoff was not durable across a separate store handle: evaluation=%+v err=%v", loadedEvaluation, err)
	}
	loadedSnapshot, err := coreForRetry.operatorReadSnapshotSet(ctx, admin, caseID, set.SnapshotSetID)
	if err != nil {
		t.Fatalf("read immutable collected snapshot after evaluation: %v", err)
	}
	loadedSnapshotBytes, loadedSnapshotErr := json.Marshal(loadedSnapshot)
	if loadedSnapshotErr != nil || !bytes.Equal(firstSetBytes, loadedSnapshotBytes) {
		t.Fatalf("evaluation rewrote or changed the original collected snapshot: err=%v", loadedSnapshotErr)
	}
	if len(evaluation.Checks) != 1 || evaluation.Checks[0].Relation != auditext.RelationConflict {
		t.Fatalf("unexpected evaluation: %+v", evaluation.Checks)
	}
	if evaluation.Checks[0].ReasonCode != "configured_bandwidth_below_contract" || !evaluation.Checks[0].RequiresHumanReview {
		t.Fatalf("unexpected relation/review flag: %+v", evaluation.Checks[0])
	}
	var documentEvidence, snapshotEvidence bool
	for _, evidence := range evaluation.Checks[0].Evidence {
		switch evidence.Kind {
		case auditext.EvidenceDocument:
			documentEvidence = true
		case auditext.EvidenceExternalSnapshot:
			snapshotEvidence = true
		}
	}
	if !documentEvidence || !snapshotEvidence {
		t.Fatalf("evidence kinds were not kept separate: %+v", evaluation.Checks[0].Evidence)
	}

	plan, err := core.GetOperatorImpactPlan(ctx, admin, caseID, evaluation.EvaluationID,
		auditext.ImpactExplain, reviewimpact.New())
	if err != nil {
		t.Fatalf("impact plan: %v", err)
	}
	if plan.Fallback != auditext.FallbackFull || plan.DependencyComplete {
		t.Fatalf("baseline-less plan must fall back to full: %+v", plan)
	}
	for _, check := range plan.Checks {
		if check.Action != auditext.ImpactRecompute {
			t.Fatalf("baseline-less plan offered reuse: %+v", check)
		}
	}

	// A second identical evaluation is idempotent by key and returns the same
	// persisted result without creating a second evaluation row.
	replay, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: businessAt,
		EvidenceCutoffAt: &evaluationCutoffAt, Scope: &OperatorEvaluationScope{Kind: "explicit_subset", ServiceObjectIDs: []string{serviceObject.ServiceObjectID}},
	}, manifest, fulfillment.New(), "operator-evaluation-1")
	if err != nil || replay.EvaluationID != evaluation.EvaluationID {
		t.Fatalf("idempotent replay: id=%q err=%v", replay.EvaluationID, err)
	}

	// A new evaluation over the same frozen inputs may offer a reuse candidate;
	// the business result still comes from the full path.
	time.Sleep(5 * time.Millisecond)
	evaluation2, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: businessAt,
		EvidenceCutoffAt: &evaluationCutoffAt, Scope: &OperatorEvaluationScope{Kind: "explicit_subset", ServiceObjectIDs: []string{serviceObject.ServiceObjectID}},
	}, manifest, fulfillment.New(), "operator-evaluation-2")
	if err != nil {
		t.Fatalf("second evaluation: %v", err)
	}
	plan2, err := core.GetOperatorImpactPlan(ctx, admin, caseID, evaluation2.EvaluationID,
		auditext.ImpactShadow, reviewimpact.New())
	if err != nil {
		t.Fatalf("second impact plan: %v", err)
	}
	if plan2.Fallback != auditext.FallbackNone || !plan2.DependencyComplete || plan2.BusinessResultSource != "full" {
		t.Fatalf("identical input plan must not fall back: %+v", plan2)
	}
	if len(plan2.Checks) != 1 || plan2.Checks[0].Action != auditext.ImpactReuseCandidate {
		t.Fatalf("identical input plan did not offer a candidate: %+v", plan2.Checks)
	}
	if err := core.updateCaseAs(admin, caseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseID {
				archivedAt := time.Now().UTC().Format(time.RFC3339Nano)
				st.Cases[i].BusinessStatus = "archived"
				st.Cases[i].ArchivedAt = &archivedAt
				return nil
			}
		}
		return errors.New("case not found while preparing archived SOD fixture")
	}); err != nil {
		t.Fatalf("archive case for SOD no-write boundary: %v", err)
	}
	archiveDecision := OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: archiveCAS.ExpectedMappingVersion,
		ExpectedBindingVersion: archiveCAS.ExpectedBindingVersion, ExpectedObjectVersion: archiveCAS.ExpectedObjectVersion,
		ExpectedAssociationVersion: archiveCAS.ExpectedAssociationVersion, ExpectedAfterSHA256: archiveProposal.AfterSHA256,
		ExpectedImpactSHA256: archiveProposal.ImpactSHA256, Reason: "归档后不得继续批准对象映射更正"}
	if _, _, err := core.DecideOperatorMappingChange(ctx, reviewer, caseID, archiveProposal.RequestID, archiveDecision, "operator-archive-boundary-decision"); !isConflictFault(err) {
		t.Fatalf("archived case accepted a mapping correction decision: %v", err)
	}
	var archivedDecisionCount int
	if err := environment.harness.pool.QueryRow(ctx, `SELECT count(*) FROM operator_mapping_change_decisions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`, admin.TenantID, admin.OrgID, caseID, archiveProposal.RequestID).Scan(&archivedDecisionCount); err != nil || archivedDecisionCount != 0 {
		t.Fatalf("archived decision attempt persisted a terminal decision: count=%d err=%v", archivedDecisionCount, err)
	}
}

func operatorManifestForRulePack(t *testing.T, rulePackID, rulePackVersion string) capabilitypack.Manifest {
	t.Helper()
	connector := "fixture.oss"
	externalField := "configured_bandwidth"
	manifest := capabilitypack.Manifest{
		SchemaVersion:       auditext.CapabilityPackSchemaVersion,
		PackID:              "operator.dedicated_line",
		Version:             "1.0.0",
		HostContractVersion: auditext.HostContractVersion,
		DisplayName:         "专线配置核验（测试）",
		Scenario:            capabilitypack.ScenarioTelecomDedicatedLine,
		RequiredMaterialRoles: []string{
			"draft_contract", "procurement",
		},
		RulePackRef: capabilitypack.RulePackRef{ID: rulePackID, Version: rulePackVersion},
		Capabilities: []capabilitypack.Capability{{
			ID: "operator.oss.read_config", Effect: capabilitypack.EffectRead, ConnectorRef: &connector,
		}},
		Checks: []capabilitypack.Check{{
			ID: "line.configured_bandwidth", ComparatorRef: "builtin.configured_bandwidth_at_least/v1",
			ContractField: "minimum_bandwidth", ExternalField: &externalField,
		}},
		UI: capabilitypack.UI{
			View: capabilitypack.ViewFieldComparison,
			Fields: []string{
				"minimum_bandwidth", "configured_bandwidth", "observed_at",
			},
		},
		FixtureSuite: "operatoraudit.dedicated_line.v1",
		DataClass:    auditext.DataClassSynthetic,
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("test capability manifest: %v", err)
	}
	return manifest
}

func strPtr(value string) *string { return &value }

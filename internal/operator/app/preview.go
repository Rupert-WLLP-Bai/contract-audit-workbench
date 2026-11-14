package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	knowledgemodel "contractaudit/internal/knowledge/model"
	legalmodel "contractaudit/internal/legal/model"
	"contractaudit/internal/operator/model"
	"contractaudit/internal/rulepack"
	subjectmodel "contractaudit/internal/subject/model"
	"contractaudit/internal/telecom"
)

type PreviewSnapshot = executionmodel.RunSnapshot[subjectmodel.PartyContextItem, knowledgemodel.ReviewKnowledgeSnapshot[legalmodel.FrozenLegalAuthority], model.ServiceObjectReviewContext]
type PreviewScope struct {
	TenantID, OrgID, CaseID, BusinessLine, Scenario, ContractType string
}
type PreviewFacts[D executionmodel.OriginalDocument] interface {
	Run(string) (executionmodel.Run, bool)
	Attempt(string) (executionmodel.Attempt, bool)
	Input(string) (executionmodel.AttemptInput[D], bool)
	SnapshotForHash(string) PreviewSnapshot
	Selections(string) []executionmodel.AttemptParseSelection
	VerifyDocuments(context.Context, []executionmodel.FrozenDocument[D]) error
	AgentDigest(context.Context, string) (string, error)
	OutputRows(string, string) ([]executionmodel.Finding, []executionmodel.ConstraintEval)
	HasMaterial(string, int, string) bool
	TelecomSource(context.Context, executionmodel.FrozenDocument[D]) (rulepack.Source, error)
	ExtractionAuthorization() (access.Principal, access.Resource)
	Now() time.Time
}

func Preview[D executionmodel.OriginalDocument](ctx context.Context, p PreviewFacts[D], scope PreviewScope, head model.ServiceObjectHead, input model.ServiceObjectAssociationPreviewInput) (model.ServiceObjectAssociationPreview, error) {
	input.RunID, input.AttemptID = strings.TrimSpace(input.RunID), strings.TrimSpace(input.AttemptID)
	input.MaterialID, input.DocumentID = strings.TrimSpace(input.MaterialID), strings.TrimSpace(input.DocumentID)
	input.ParsedDocumentID = strings.TrimSpace(input.ParsedDocumentID)
	if input.RunID == "" || input.AttemptID == "" || input.MaterialID == "" || input.MaterialVersion < 1 || input.DocumentID == "" || input.ParsedDocumentID == "" {
		return model.ServiceObjectAssociationPreview{}, fault.Invalid("冻结审查及材料解析选择无效")
	}
	run, ok := p.Run(input.RunID)
	if !ok || !executionmodel.RunMatchesCase(run, scope.CaseID) {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("执行记录不存在，或不属于当前案件")
	}
	attempt, ok := p.Attempt(input.AttemptID)
	if !ok || !executionmodel.AttemptTerminalForRun(attempt, input.RunID) {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("这次执行尚未形成可比较的最终证据")
	}
	if !executionmodel.HasFrozenAttemptHashes(attempt) {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("之前的执行记录缺少保存的输入或输出编号")
	}
	frozen, ok := p.Input(input.AttemptID)
	if !ok || frozen.RunID != input.RunID || frozen.AttemptID != input.AttemptID || frozen.InputHash != *attempt.InputHash {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("来源审查的冻结输入未通过校验")
	}
	snapshot := p.SnapshotForHash(input.RunID)
	hash, err := executionmodel.FrozenInputHash(snapshot, frozen.Documents)
	if err != nil || hash != frozen.InputHash || !executionmodel.FrozenInputMatchesSnapshot(snapshot, frozen.Documents) || !executionmodel.AttemptSelectionsMatchFrozen(p.Selections(input.AttemptID), frozen.Documents) {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("来源审查的冻结输入未通过校验")
	}
	if err := p.VerifyDocuments(ctx, frozen.Documents); err != nil {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("来源审查的冻结材料未通过校验")
	}
	digest, err := p.AgentDigest(ctx, input.AttemptID)
	if err != nil {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("来源审查的冻结输出未通过校验")
	}
	findings, evaluations := p.OutputRows(input.RunID, input.AttemptID)
	output, err := executionmodel.AttemptOutputHash(findings, evaluations, attempt.RulePackHash, attempt.RulePackSnapshotDigest, attempt.RulePackCompilerRef, digest)
	if err != nil || output != *attempt.OutputHash {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("来源审查的冻结输出未通过校验")
	}
	if _, err := executionmodel.FrozenRulePack(attempt, executionmodel.FrozenRulePackScope{TenantID: scope.TenantID, BusinessLine: scope.BusinessLine, Scenario: scope.Scenario, ContractType: scope.ContractType}); err != nil {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("来源审查缺少有效的冻结规则包")
	}
	if attempt.RunID != input.RunID {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("冻结审查尝试编号不匹配")
	}
	var selected *executionmodel.FrozenDocument[D]
	for i := range frozen.Documents {
		doc := &frozen.Documents[i]
		if doc.Document.OriginalProof().ID == input.DocumentID && doc.ParsedDocumentID == input.ParsedDocumentID {
			selected = doc
			break
		}
	}
	if selected == nil || selected.Material == nil || !((selected.Material.Purpose == "primary_contract" && selected.Document.OriginalProof().Role == "draft_contract") || (selected.Material.Purpose == "supplement" && selected.Document.OriginalProof().Role == "attachment")) || !selected.Ready || selected.ParsedDocumentID == "" || selected.Material.MaterialID != input.MaterialID || selected.Material.Version != input.MaterialVersion {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("合同材料不在该次冻结审查中")
	}
	if !p.HasMaterial(input.MaterialID, input.MaterialVersion, input.DocumentID) {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("材料版本与冻结合同原件不匹配")
	}
	selectedParse := false
	for _, selection := range p.Selections(input.AttemptID) {
		if selection.RunID == input.RunID && selection.DocumentID == input.DocumentID && selection.ParsedDocumentID == input.ParsedDocumentID && selection.OriginalDigest == selected.Document.OriginalProof().Digest {
			selectedParse = true
			break
		}
	}
	if !selectedParse {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("解析版本不在该次冻结审查中")
	}
	source, err := p.TelecomSource(ctx, *selected)
	if err != nil {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("冻结合同原文未通过完整性校验")
	}
	sources := []rulepack.Source{source}
	principal, resource := p.ExtractionAuthorization()
	request := rulepack.Request{Principal: principal, Resource: resource, BusinessLine: scope.BusinessLine, Scenario: scope.Scenario, ContractType: scope.ContractType, At: p.Now().UTC(), Sources: sources}
	facts, err := telecom.ExtractContractMaterial(request, source)
	if err != nil {
		return model.ServiceObjectAssociationPreview{}, fault.Conflict("冻结合同中没有可用的服务对象事实")
	}
	byObject := make(map[string][]model.ServiceObjectFactRef)
	for _, fact := range facts {
		if strings.TrimSpace(fact.ObjectRef) == "" {
			continue
		}
		allCitationsSelected := len(fact.Citations) > 0
		for _, citation := range fact.Citations {
			if selected.NormalizedHash == nil || citation.SourceID != input.DocumentID || citation.SourceVersion != input.ParsedDocumentID ||
				citation.TenantID != scope.TenantID || citation.CaseID != scope.CaseID || citation.SourceDigest != *selected.NormalizedHash {
				allCitationsSelected = false
				break
			}
		}
		if !allCitationsSelected {
			continue
		}
		factHash, err := model.CanonicalRuleFactSHA256(fact)
		if err != nil {
			return model.ServiceObjectAssociationPreview{}, err
		}
		byObject[fact.ObjectRef] = append(byObject[fact.ObjectRef], model.ServiceObjectFactRef{Fact: fact, DocumentID: input.DocumentID,
			ParsedDocumentID: input.ParsedDocumentID, CanonicalFactSHA256: factHash})
	}
	objectRefs := make([]string, 0, len(byObject))
	for objectRef := range byObject {
		objectRefs = append(objectRefs, objectRef)
	}
	sort.Strings(objectRefs)
	options := make([]model.ServiceObjectAssociationOption, 0, len(objectRefs))
	for _, objectRef := range objectRefs {
		refs := byObject[objectRef]
		factsJSON, digest, err := model.CanonicalServiceObjectFacts(refs)
		if err != nil {
			return model.ServiceObjectAssociationPreview{}, err
		}
		basisRefs, err := model.ServiceObjectBasisRefs(refs)
		if err != nil {
			return model.ServiceObjectAssociationPreview{}, err
		}
		_ = factsJSON
		options = append(options, model.ServiceObjectAssociationOption{ObjectRef: objectRef, Facts: refs, FactsSHA256: digest, BasisRefs: basisRefs})
	}
	preview := model.ServiceObjectAssociationPreview{ServiceObjectID: head.ID, ExpectedObjectVersion: head.CurrentVersion,
		ExpectedAssociationVersion: head.CurrentAssociationVersion, RunID: input.RunID, AttemptID: input.AttemptID, MaterialID: input.MaterialID,
		MaterialVersion: input.MaterialVersion, DocumentID: input.DocumentID, ParsedDocumentID: input.ParsedDocumentID, Options: options}
	pins := make([]model.ServiceObjectCandidateDigestPin, 0, len(preview.Options))
	for _, option := range preview.Options {
		pins = append(pins, model.ServiceObjectCandidateDigestPin{ObjectRef: option.ObjectRef, FactsSHA256: option.FactsSHA256})
	}
	preview.CandidatesSHA256, err = model.ServiceObjectCandidatesSHA256(model.ServiceObjectCandidateDigestInput{
		ServiceObjectID: preview.ServiceObjectID, ExpectedObjectVersion: preview.ExpectedObjectVersion,
		ExpectedAssociationVersion: preview.ExpectedAssociationVersion, RunID: preview.RunID, AttemptID: preview.AttemptID,
		MaterialID: preview.MaterialID, MaterialVersion: preview.MaterialVersion, DocumentID: preview.DocumentID,
		ParsedDocumentID: preview.ParsedDocumentID, Candidates: pins,
	})
	if err != nil {
		return model.ServiceObjectAssociationPreview{}, err
	}
	return preview, nil
}

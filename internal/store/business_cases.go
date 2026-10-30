package store

import (
	"context"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docversion"
	"contractaudit/internal/fault"
)

// businessWritable is shared by every business mutation path. Call it only
// after the current case has been resolved inside the state transaction.
func businessWritable(item Case) error {
	if item.BusinessStatus == "archived" || item.ArchivedAt != nil {
		return fault.Conflict("案件已归档，不能再修改业务记录")
	}
	return nil
}

func (s *Store) requireBusinessWrite(st *state, user accounts.User, caseID string) (Case, error) {
	item, err := s.visibleCase(st, user, caseID)
	if err != nil {
		return Case{}, err
	}
	if err := businessWritable(item); err != nil {
		return Case{}, err
	}
	return item, nil
}

func requireCaseAction(item Case, user accounts.User, action access.Action) error {
	if !authorizeCase(item, user, action) {
		return fault.Forbidden("当前身份无权执行此案件操作")
	}
	return nil
}

// CreateCaseWithBusiness creates a case with the business scope frozen at
// creation. The ordinary C1-A CreateCase path remains available for legacy
// callers and explicitly uses the unspecified scope.
func (s *Store) CreateCaseWithBusiness(user accounts.User, title, contractType, businessLine, scenario string) (string, error) {
	if s.ReadOnly() {
		return "", fault.New(503, "read_only", "只读模式下不能创建案件")
	}
	current, err := trustedUser(user)
	if err != nil {
		return "", err
	}
	if !accounts.CanCreate(current.Role) {
		return "", fault.Forbidden("当前角色不能创建案件")
	}
	input, err := validateBusinessCaseInput(title, contractType, businessLine, scenario)
	if err != nil {
		return "", err
	}
	id, err := newID("case_")
	if err != nil {
		return "", err
	}
	now := stamp()
	err = s.updateAs(current, func(st *state) error {
		return appendBusinessCase(st, current, input, id, now)
	})
	return id, err
}

type businessCaseInput struct {
	title, contractType, businessLine, scenario string
}

func validateBusinessCaseInput(title, contractType, businessLine, scenario string) (businessCaseInput, error) {
	title, err := validateBusinessCaseTitle(title)
	if err != nil {
		return businessCaseInput{}, err
	}
	if err := validateBusinessContractType(contractType); err != nil {
		return businessCaseInput{}, err
	}
	businessLine, scenario, err = normalizeBusinessCaseScope(businessLine, scenario)
	if err != nil {
		return businessCaseInput{}, err
	}
	if err := validateBusinessContractScope(contractType, businessLine, scenario); err != nil {
		return businessCaseInput{}, err
	}
	return businessCaseInput{title, contractType, businessLine, scenario}, nil
}

func validateBusinessCaseTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || runeCount(title) > 120 {
		return "", fault.Invalid("案件标题需要 1 到 120 个字")
	}
	return title, nil
}

func validateBusinessContractType(contractType string) error {
	switch contractType {
	case "procurement_ict", "network_engineering", "unspecified", "service":
		return nil
	default:
		return fault.Invalid("合同类型不在本版范围内")
	}
}

func normalizeBusinessCaseScope(businessLine, scenario string) (string, string, error) {
	businessLine = strings.TrimSpace(businessLine)
	scenario = strings.TrimSpace(scenario)
	if businessLine == "" && scenario == "" {
		return "unspecified", "unspecified", nil
	}
	if !validBusinessCaseScope(businessLine, scenario) {
		return "", "", fault.Invalid("业务线与场景不在本版范围内")
	}
	return businessLine, scenario, nil
}

func validBusinessCaseScope(businessLine, scenario string) bool {
	if businessLine != "ENT" {
		return false
	}
	return scenario == "telecom_dedicated_line" || scenario == "cloud_services"
}

func validateBusinessContractScope(contractType, businessLine, scenario string) error {
	if businessLine == "ENT" && contractType == "unspecified" {
		return fault.Invalid("政企业务案件需要明确合同类型")
	}
	if scenario == "cloud_services" && contractType != "service" {
		return fault.Invalid("云服务案件需要服务合同类型")
	}
	return nil
}

func appendBusinessCase(st *state, user accounts.User, input businessCaseInput, id, now string) error {
	item := Case{
		ID: id, TenantID: user.TenantID, OwnerOrgID: user.OrgID,
		BusinessLine: input.businessLine, Scenario: input.scenario, BusinessStatus: "active",
		Title: input.title, ContractType: input.contractType, OwnerID: user.ID,
		MemberIDs: []string{user.ID}, CreatedAt: now, UpdatedAt: now,
	}
	st.Cases = append(st.Cases, item)
	created := st.Cases[len(st.Cases)-1]
	if !authorizeCase(created, user, access.Manage) {
		return fault.Forbidden("当前授权不能创建案件")
	}
	activityID, err := newID("act_")
	if err != nil {
		return err
	}
	return appendBusinessCaseActivity(st, created, input.businessLine, activityID, now)
}

func appendBusinessCaseActivity(st *state, item Case, businessLine, activityID, now string) error {
	summary := "创建了案件"
	if businessLine == "ENT" {
		summary = "创建了政企业务案件"
	}
	st.Activity = append(st.Activity, Activity{ID: activityID, CaseID: item.ID, At: now, Summary: summary})
	return nil
}

// Generate carries the authenticated request deadline through original
// storage and initial text parsing.
func (s *Store) Generate(ctx context.Context, user accounts.User, caseID, templateID string, fields map[string]string) (GenerationRecord, error) {
	currentUser, caseItem, body, err := s.prepareBusinessGeneration(ctx, user, caseID, templateID, fields)
	if err != nil {
		return GenerationRecord{}, err
	}
	prepared, err := s.buildBusinessGeneration(ctx, caseItem, caseID, templateID, body)
	if err != nil {
		return GenerationRecord{}, err
	}
	persistCtx := ctx
	persistCancel := func() {}
	if ctx.Err() != nil {
		persistCtx, persistCancel = parseFailurePersistenceContext(ctx)
	}
	defer persistCancel()
	if err := s.scopedCaseMutation(persistCtx, currentUser, caseID, func(next *state) error {
		return s.persistBusinessGeneration(next, currentUser, caseID, prepared)
	}); err != nil {
		return GenerationRecord{}, err
	}
	return prepared.record, nil
}

type preparedBusinessGeneration struct {
	document Document
	parsed   ParsedDocument
	record   GenerationRecord
	now      string
}

func (s *Store) prepareBusinessGeneration(ctx context.Context, user accounts.User, caseID, templateID string, fields map[string]string) (accounts.User, Case, []byte, error) {
	current, item, err := s.documentWriteCase(ctx, user, caseID, access.Review)
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if err := businessWritable(item); err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if err := requireCaseAction(item, current, access.Review); err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if !businessTemplateMatches(item, templateID) {
		return accounts.User{}, Case{}, nil, fault.Invalid("合同模板与案件业务场景不匹配")
	}
	body, err := renderBusinessTemplate(templateID, fields)
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if len(body) > 20<<20 {
		return accounts.User{}, Case{}, nil, fault.TooLarge("生成的合同草稿超过 20MB")
	}
	return current, item, body, nil
}

func businessTemplateMatches(item Case, templateID string) bool {
	return isTelecomDedicatedLineCase(item) && templateID == telecomTemplateID ||
		isCloudServicesCase(item) && templateID == cloudServicesTemplateID
}

func (s *Store) buildBusinessGeneration(ctx context.Context, item Case, caseID, templateID string, body []byte) (preparedBusinessGeneration, error) {
	documentID, err := newID("doc_")
	if err != nil {
		return preparedBusinessGeneration{}, err
	}
	generationID, err := newID("gen_")
	if err != nil {
		return preparedBusinessGeneration{}, err
	}
	filename, templateVersion := businessDraftIdentity(templateID)
	media := "text/plain"
	version, err := docversion.New(documentID, docversion.Source{Origin: "generated", Filename: filename, MediaType: media}, body)
	if err != nil {
		return preparedBusinessGeneration{}, fault.Invalid("无法生成合同草稿")
	}
	if err := s.putObject(ctx, item.TenantID, caseID, version.Digest().String(), body); err != nil {
		return preparedBusinessGeneration{}, err
	}
	now := stamp()
	document := Document{
		ID: documentID, TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID,
		Role: "draft_contract", Filename: filename, MediaType: media,
		Digest: version.Digest().String(), Size: version.Size(), Origin: "generated", StorageBackend: s.objectBackend(), CreatedAt: now,
	}
	parsed, err := s.parseBusinessDraft(ctx, document)
	if err != nil {
		return preparedBusinessGeneration{}, err
	}
	record := GenerationRecord{
		ID: generationID, DocumentID: documentID, TemplateID: templateID,
		TemplateVersion: templateVersion, Digest: version.Digest().String(), CreatedAt: now,
	}
	return preparedBusinessGeneration{document: document, parsed: parsed, record: record, now: now}, nil
}

func businessDraftIdentity(templateID string) (string, string) {
	if templateID == cloudServicesTemplateID {
		return "cloud-services-draft.txt", cloudServicesTemplateVersion
	}
	return "telecom-dedicated-line-draft.txt", telecomTemplateVersion
}

func (s *Store) parseBusinessDraft(ctx context.Context, document Document) (ParsedDocument, error) {
	models := s.currentModels()
	selection, err := s.reserveParseSelection(ctx, document, models)
	if err != nil {
		return ParsedDocument{}, err
	}
	interpreted := s.interpretUnpersistedWithModels(ctx, document, models)
	if contextErr := ctx.Err(); contextErr != nil {
		interpreted = Interpreted{Document: document, ParseError: contextErr.Error(), FromOCR: interpreted.FromOCR}
	}
	return makeParsedWithSelection(document, interpreted, selection)
}

func (s *Store) persistBusinessGeneration(next *state, user accounts.User, caseID string, prepared preparedBusinessGeneration) error {
	currentCase, err := s.requireBusinessWrite(next, user, caseID)
	if err != nil {
		return err
	}
	if err := allowBusinessGeneration(user, currentCase); err != nil {
		return err
	}
	if err := allowDraftReplacement(next, caseID); err != nil {
		return err
	}
	appendBusinessGenerationRecords(next, currentCase, caseID, prepared)
	activityID, err := newID("act_")
	if err != nil {
		return err
	}
	next.Activity = append(next.Activity, Activity{ID: activityID, CaseID: caseID, At: prepared.now, Summary: "生成了合同草稿 " + prepared.document.Filename})
	return nil
}

func allowBusinessGeneration(user accounts.User, item Case) error {
	if !accounts.CanUpload(user.Role) {
		return fault.Forbidden("当前角色不能生成合同草稿")
	}
	return requireCaseAction(item, user, access.Review)
}

func allowDraftReplacement(next *state, caseID string) error {
	current := currentDocuments(next.documents(caseID))
	for _, item := range current {
		if item.Role == "draft_contract" {
			return nil
		}
	}
	if len(current) >= 4 {
		return fault.Invalid("每个案件最多登记 4 份材料")
	}
	return nil
}

func appendBusinessGenerationRecords(next *state, item Case, caseID string, prepared preparedBusinessGeneration) {
	next.Documents = append(next.Documents, prepared.document)
	next.ParsedDocuments = append(next.ParsedDocuments, prepared.parsed)
	next.Generations = append(next.Generations, generationRecord{
		Record: prepared.record, TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID,
	})
}

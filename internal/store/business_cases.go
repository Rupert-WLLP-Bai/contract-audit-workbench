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
	title = strings.TrimSpace(title)
	if title == "" || runeCount(title) > 120 {
		return "", fault.Invalid("案件标题需要 1 到 120 个字")
	}
	switch contractType {
	case "procurement_ict", "network_engineering", "unspecified", "service":
	default:
		return "", fault.Invalid("合同类型不在本版范围内")
	}
	businessLine = strings.TrimSpace(businessLine)
	scenario = strings.TrimSpace(scenario)
	if businessLine == "" && scenario == "" {
		businessLine, scenario = "unspecified", "unspecified"
	} else if businessLine != "ENT" || (scenario != "telecom_dedicated_line" && scenario != "cloud_services") {
		return "", fault.Invalid("业务线与场景不在本版范围内")
	}
	if businessLine == "ENT" && contractType == "unspecified" {
		return "", fault.Invalid("政企业务案件需要明确合同类型")
	}
	if scenario == "cloud_services" && contractType != "service" {
		return "", fault.Invalid("云服务案件需要服务合同类型")
	}
	id, err := newID("case_")
	if err != nil {
		return "", err
	}
	now := stamp()
	err = s.updateAs(current, func(st *state) error {
		st.Cases = append(st.Cases, Case{
			ID: id, TenantID: current.TenantID, OwnerOrgID: current.OrgID,
			BusinessLine: businessLine, Scenario: scenario, BusinessStatus: "active",
			Title: title, ContractType: contractType, OwnerID: current.ID,
			MemberIDs: []string{current.ID}, CreatedAt: now, UpdatedAt: now,
		})
		if !authorizeCase(st.Cases[len(st.Cases)-1], current, access.Manage) {
			return fault.Forbidden("当前授权不能创建案件")
		}
		activityID, err := newID("act_")
		if err != nil {
			return err
		}
		summary := "创建了案件"
		if businessLine == "ENT" {
			summary = "创建了政企业务案件"
		}
		st.Activity = append(st.Activity, Activity{ID: activityID, CaseID: id, At: now, Summary: summary})
		return nil
	})
	return id, err
}

// Generate carries the authenticated request deadline through original
// storage and initial text parsing.
func (s *Store) Generate(ctx context.Context, user accounts.User, caseID, templateID string, fields map[string]string) (GenerationRecord, error) {
	currentUser, caseItem, err := s.documentWriteCase(ctx, user, caseID, access.Review)
	if err != nil {
		return GenerationRecord{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return GenerationRecord{}, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Review); err != nil {
		return GenerationRecord{}, err
	}
	if !((isTelecomDedicatedLineCase(caseItem) && templateID == telecomTemplateID) ||
		(isCloudServicesCase(caseItem) && templateID == cloudServicesTemplateID)) {
		return GenerationRecord{}, fault.Invalid("合同模板与案件业务场景不匹配")
	}
	body, err := renderBusinessTemplate(templateID, fields)
	if err != nil {
		return GenerationRecord{}, err
	}
	if len(body) > 20<<20 {
		return GenerationRecord{}, fault.TooLarge("生成的合同草稿超过 20MB")
	}
	documentID, err := newID("doc_")
	if err != nil {
		return GenerationRecord{}, err
	}
	generationID, err := newID("gen_")
	if err != nil {
		return GenerationRecord{}, err
	}
	filename, templateVersion := "telecom-dedicated-line-draft.txt", telecomTemplateVersion
	if templateID == cloudServicesTemplateID {
		filename, templateVersion = "cloud-services-draft.txt", cloudServicesTemplateVersion
	}
	media := "text/plain"
	version, err := docversion.New(documentID, docversion.Source{Origin: "generated", Filename: filename, MediaType: media}, body)
	if err != nil {
		return GenerationRecord{}, fault.Invalid("无法生成合同草稿")
	}
	if err := s.putObject(ctx, caseItem.TenantID, caseID, version.Digest().String(), body); err != nil {
		return GenerationRecord{}, err
	}
	now := stamp()
	document := Document{
		ID: documentID, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID,
		Role: "draft_contract", Filename: filename, MediaType: media,
		Digest: version.Digest().String(), Size: version.Size(), Origin: "generated", StorageBackend: s.objectBackend(), CreatedAt: now,
	}
	selection, err := s.reserveParseSelection(ctx, document, s.models)
	if err != nil {
		return GenerationRecord{}, err
	}
	interpreted := s.interpretUnpersistedWithModels(ctx, document, s.models)
	if contextErr := ctx.Err(); contextErr != nil {
		interpreted = Interpreted{Document: document, ParseError: contextErr.Error(), FromOCR: interpreted.FromOCR}
	}
	parsed, err := makeParsedWithSelection(document, interpreted, selection)
	if err != nil {
		return GenerationRecord{}, err
	}
	record := GenerationRecord{
		ID: generationID, DocumentID: documentID, TemplateID: templateID,
		TemplateVersion: templateVersion, Digest: version.Digest().String(), CreatedAt: now,
	}
	persistCtx := ctx
	persistCancel := func() {}
	if ctx.Err() != nil {
		persistCtx, persistCancel = parseFailurePersistenceContext(ctx)
	}
	defer persistCancel()
	if err := s.scopedCaseMutation(persistCtx, currentUser, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if !accounts.CanUpload(currentUser.Role) {
			return fault.Forbidden("当前角色不能生成合同草稿")
		}
		if err := requireCaseAction(currentCase, currentUser, access.Review); err != nil {
			return err
		}
		current := currentDocuments(next.documents(caseID))
		replacing := false
		for _, item := range current {
			if item.Role == "draft_contract" {
				replacing = true
			}
		}
		if !replacing && len(current) >= 4 {
			return fault.Invalid("每个案件最多登记 4 份材料")
		}
		next.Documents = append(next.Documents, document)
		next.ParsedDocuments = append(next.ParsedDocuments, parsed)
		next.Generations = append(next.Generations, generationRecord{
			Record: record, TenantID: currentCase.TenantID, OrgID: currentCase.OwnerOrgID, CaseID: caseID,
		})
		// Case.UpdatedAt is derived on read by applyReadProjections from the
		// documents, parsed artifacts, generations, activity and runs of the
		// case, so the document service must not write the core case row here.
		activityID, err := newID("act_")
		if err != nil {
			return err
		}
		next.Activity = append(next.Activity, Activity{ID: activityID, CaseID: caseID, At: now, Summary: "生成了合同草稿 " + filename})
		return nil
	}); err != nil {
		return GenerationRecord{}, err
	}
	return record, nil
}

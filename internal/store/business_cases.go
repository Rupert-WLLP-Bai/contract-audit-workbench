package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docversion"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateCaseWithBusiness(user accounts.User, title, contractType, businessLine, scenario string) (string, error) {
	return s.CreateCaseWithBusinessContext(context.Background(), user, title, contractType, businessLine, scenario, "")
}

// CreateCaseWithBusinessContext records an optional request key in the same
// transaction as the case and its creation activity.
func (s *Store) CreateCaseWithBusinessContext(ctx context.Context, user accounts.User, title, contractType, businessLine, scenario, key string) (string, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return "", err
	}
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
	key, err = optionalIdempotencyKey(key)
	if err != nil {
		return "", err
	}
	digest := ""
	if key != "" {
		digest, err = canonicalHash([]string{input.title, input.contractType, input.businessLine, input.scenario})
		if err != nil {
			return "", err
		}
	}
	id, err := newID("case_")
	if err != nil {
		return "", err
	}
	const route = "POST /api/cases"
	now := stamp()
	var receipt writeReceipt
	var found bool
	var receiptTx pgx.Tx
	err = s.scopedCreateCaseWrite(ctx, current, id, func(tx pgx.Tx) (string, error) {
		receiptTx = tx
		if key == "" {
			return "", nil
		}
		if err := lockWriteReceipt(ctx, tx, current, route, key); err != nil {
			return "", err
		}
		receipt, found, err = loadWriteReceipt(ctx, tx, coreWriteReceipts, current, route, key)
		if err != nil {
			return "", err
		}
		if found {
			return receipt.CaseID, nil
		}
		return "", nil
	}, func(st *state) error {
		current, err = trustedUser(current)
		if err != nil {
			return err
		}
		if !accounts.CanCreate(current.Role) {
			return fault.Forbidden("当前角色不能创建案件")
		}
		if found {
			item, err := s.visibleCase(st, current, receipt.CaseID)
			if err != nil {
				return err
			}
			if err := requireCaseAction(item, current, access.Manage); err != nil {
				return err
			}
			if err := checkWriteReceipt(receipt, item, digest); err != nil {
				return err
			}
			var result struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(receipt.Response, &result); err != nil {
				return err
			}
			if result.ID != item.ID {
				return errors.New("case idempotency receipt has an invalid case identity")
			}
			id = result.ID
			return nil
		}
		scope := Case{TenantID: accounts.Tenant(current), BusinessLine: input.businessLine, Scenario: input.scenario, ContractType: input.contractType}
		if isDeclaredReviewCase(scope) {
			pack, err := selectActiveRulePackTx(ctx, receiptTx, scope.TenantID, scope.BusinessLine, scope.Scenario, scope.ContractType, time.Now().UTC())
			if err != nil {
				return err
			}
			if pack == nil {
				return fault.Conflict("该业务范围没有生效的已发布规则包")
			}
			if err := requireGovernedCapabilitiesForCase(scope, pack.Snapshot); err != nil {
				return err
			}
		}
		if err := appendBusinessCase(st, current, input, id, now); err != nil {
			return err
		}
		if key != "" {
			item, _ := st.caseByID(id)
			return insertWriteReceipt(ctx, receiptTx, coreWriteReceipts, current, item, route, key, digest, map[string]string{"id": id})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

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
	if !validIdentifier(contractType) {
		return fault.Invalid("合同类型需要有效的标识")
	}
	return nil
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
	if businessLine == "unspecified" && scenario == "unspecified" {
		return true
	}
	return validIdentifier(businessLine) && validIdentifier(scenario) && businessLine != "unspecified" && scenario != "unspecified"
}

func validateBusinessContractScope(contractType, businessLine, scenario string) error {
	if businessLine == "unspecified" && contractType != "procurement_ict" && contractType != "network_engineering" && contractType != "unspecified" && contractType != "service" {
		return fault.Invalid("扩展合同类型需要明确业务线和场景")
	}
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
	ctx = commandContext(ctx, user)
	return s.GenerateWithOptions(ctx, user, caseID, templateID, fields, MaterialUploadOptions{})
}

func (s *Store) GenerateWithOptions(ctx context.Context, user accounts.User, caseID, templateID string, fields map[string]string, options MaterialUploadOptions) (GenerationRecord, error) {
	ctx = commandContext(ctx, user)
	currentUser, caseItem, body, err := s.prepareBusinessGeneration(ctx, user, caseID, templateID, fields)
	if err != nil {
		return GenerationRecord{}, err
	}
	if err := validateMaterialUploadOptions(options); err != nil {
		return GenerationRecord{}, fault.Invalid(err.Error())
	}
	if options.Purpose != "" && options.Purpose != "primary_contract" {
		return GenerationRecord{}, fault.Invalid("生成合同用途固定为主合同")
	}
	options.IdempotencyKey = strings.TrimSpace(options.IdempotencyKey)
	if len(options.IdempotencyKey) > 200 || strings.ContainsAny(options.IdempotencyKey, "\r\n\x00") {
		return GenerationRecord{}, fault.Invalid("Idempotency-Key无效")
	}
	requestHash := [32]byte{}
	replayCandidate := false
	if options.IdempotencyKey != "" {
		filename, _ := businessDraftIdentity(templateID)
		requestHash = uploadRequestHash("generation", currentUser.ID, caseID, "draft_contract", "primary_contract", filename, "text/plain", body, options)
		priorState, err := s.snapshotScoped(ctx, userScope(currentUser))
		if err != nil {
			return GenerationRecord{}, err
		}
		for _, prior := range priorState.UploadIdempotency {
			if prior.CaseID == caseID && prior.ActorID == currentUser.ID && prior.Key == options.IdempotencyKey {
				if !bytes.Equal(prior.RequestHash, requestHash[:]) {
					return GenerationRecord{}, fault.Conflict("Idempotency-Key已用于不同生成内容")
				}
				replayCandidate = true
			}
		}
	}
	prepared := preparedBusinessGeneration{}
	if !replayCandidate {
		prepared, err = s.buildBusinessGeneration(ctx, caseItem, caseID, templateID, body)
		if err != nil {
			return GenerationRecord{}, err
		}
		prepared.requestHash = requestHash
	}
	var result GenerationRecord
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
		if err := allowBusinessGeneration(currentUser, currentCase); err != nil {
			return err
		}
		if options.IdempotencyKey != "" {
			for _, prior := range next.UploadIdempotency {
				if prior.CaseID == caseID && prior.ActorID == currentUser.ID && prior.Key == options.IdempotencyKey {
					if !bytes.Equal(prior.RequestHash, requestHash[:]) {
						return fault.Conflict("Idempotency-Key已用于不同生成内容")
					}
					for _, generation := range next.Generations {
						if generation.CaseID == caseID && generation.Record.DocumentID == prior.DocumentID {
							result = generationRecordWithMaterial(next, generation.Record)
							result.Replayed = true
							return nil
						}
					}
					return errors.New("generation idempotency record has no generation")
				}
			}
		}
		if err := s.persistBusinessGeneration(next, currentUser, caseID, prepared, options); err != nil {
			return err
		}
		for _, generation := range next.Generations {
			if generation.CaseID == caseID && generation.Record.DocumentID == prepared.document.ID {
				result = generationRecordWithMaterial(next, generation.Record)
				return nil
			}
		}
		return errors.New("generated document was not recorded")
	}); err != nil {
		return GenerationRecord{}, err
	}
	return result, nil
}

func generationRecordWithMaterial(st *state, record GenerationRecord) GenerationRecord {
	for _, version := range st.MaterialVersions {
		if version.DocumentID == record.DocumentID {
			record.MaterialID = version.MaterialID
			record.Version = version.Version
			break
		}
	}
	return record
}

type preparedBusinessGeneration struct {
	document    Document
	parsed      ParsedDocument
	record      GenerationRecord
	now         string
	requestHash [32]byte
}

func (s *Store) prepareBusinessGeneration(ctx context.Context, user accounts.User, caseID, templateID string, fields map[string]string) (accounts.User, Case, []byte, error) {
	ctx = commandContext(ctx, user)
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

func (s *Store) persistBusinessGeneration(next *state, user accounts.User, caseID string, prepared preparedBusinessGeneration, options MaterialUploadOptions) error {
	currentCase, err := s.requireBusinessWrite(next, user, caseID)
	if err != nil {
		return err
	}
	if err := allowBusinessGeneration(user, currentCase); err != nil {
		return err
	}
	if err := persistGeneratedMaterial(next, currentCase, caseID, prepared, options, user.ID); err != nil {
		return err
	}
	for _, version := range next.MaterialVersions {
		if version.CaseID == caseID && version.DocumentID == prepared.document.ID {
			prepared.record.MaterialID = version.MaterialID
			prepared.record.Version = version.Version
			break
		}
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

func appendBusinessGenerationRecords(next *state, item Case, caseID string, prepared preparedBusinessGeneration) {
	next.Documents = append(next.Documents, prepared.document)
	next.ParsedDocuments = append(next.ParsedDocuments, prepared.parsed)
	next.Generations = append(next.Generations, generationRecord{
		Record: prepared.record, TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID,
	})
}

func persistGeneratedMaterial(next *state, item Case, caseID string, prepared preparedBusinessGeneration, options MaterialUploadOptions, actorID string) error {
	intent := options.Intent
	if intent == "" {
		intent = "add"
	}
	var material *MaterialEntity
	previous := ""
	if intent == "replace" {
		for i := range next.Materials {
			if next.Materials[i].ID == options.MaterialID && next.Materials[i].CaseID == caseID && next.Materials[i].TenantID == item.TenantID && next.Materials[i].OrgID == item.OwnerOrgID {
				material = &next.Materials[i]
				break
			}
		}
		if material == nil {
			return fault.NotFound("没有找到对应材料")
		}
		if material.CurrentDocumentID != options.ReplacesDocumentID {
			return fault.Conflict("材料当前版本已变化，请刷新后重试")
		}
		if material.Role != "draft_contract" || material.Purpose != "primary_contract" {
			return fault.Invalid("生成合同只能替换主合同材料")
		}
		if err := validatePrimaryMaterialMutation(next, caseID, intent, material); err != nil {
			return err
		}
		previous = material.CurrentDocumentID
	} else {
		if len(next.Materials) >= maxMaterialsPerCase {
			return fault.TooLarge("案件材料数量达到上限")
		}
		if err := validatePrimaryMaterialMutation(next, caseID, intent, nil); err != nil {
			return err
		}
		id, err := newID("mat_")
		if err != nil {
			return err
		}
		material = &MaterialEntity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, ID: id, Role: "draft_contract", Purpose: "primary_contract", LegacyStatus: "native", CreatedBy: actorID, CreatedAt: time.Now().UTC()}
		next.Materials = append(next.Materials, *material)
		material = &next.Materials[len(next.Materials)-1]
	}
	material.CurrentVersion++
	material.CurrentDocumentID = prepared.document.ID
	created := time.Now().UTC()
	next.MaterialVersions = append(next.MaterialVersions, MaterialVersion{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, MaterialID: material.ID, Version: material.CurrentVersion, DocumentID: prepared.document.ID, PreviousDocument: previous, CreatedBy: actorID, CreatedAt: created})
	if options.IdempotencyKey != "" {
		next.UploadIdempotency = append(next.UploadIdempotency, UploadIdempotencyRecord{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, ActorID: actorID, Key: options.IdempotencyKey, RequestHash: append([]byte(nil), prepared.requestHash[:]...), DocumentID: prepared.document.ID, MaterialID: material.ID, CreatedAt: created})
	}
	prepared.record.MaterialID = material.ID
	prepared.record.Version = material.CurrentVersion
	for i := range next.MaterialChains {
		chain := &next.MaterialChains[i]
		if chain.TenantID != item.TenantID || chain.OrgID != item.OwnerOrgID || chain.CaseID != caseID {
			continue
		}
		if intent == "replace" {
			ids := chain.SelectedDocumentIDs[:0]
			for _, id := range chain.SelectedDocumentIDs {
				if id != previous {
					ids = append(ids, id)
				}
			}
			chain.SelectedDocumentIDs = ids
		}
		chain.SelectedDocumentIDs = append(chain.SelectedDocumentIDs, prepared.document.ID)
		chain.PrimaryMaterialID = &material.ID
		chain.SelectionVersion++
		chain.UpdatedAt = created
		if material.LegacySelected {
			material.LegacyStatus = "confirmed"
			chain.LegacySelectionStatus = "confirmed"
		}
		if chain.LegacySelectionStatus == "not_applicable" {
			chain.LegacySelectionStatus = "confirmed"
		}
		return nil
	}
	status := "confirmed"
	next.MaterialChains = append(next.MaterialChains, CaseMaterialChain{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, PrimaryMaterialID: &material.ID, LegacySelectionStatus: status, SelectedDocumentIDs: []string{prepared.document.ID}, SelectionVersion: 1, CreatedAt: created, UpdatedAt: created})
	return nil
}

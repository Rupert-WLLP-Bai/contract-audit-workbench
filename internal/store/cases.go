package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docversion"
	"contractaudit/internal/fault"
	"contractaudit/internal/labels"
	"contractaudit/internal/model"
)

func (s *Store) Login(username, password string) (string, accounts.User, error) {
	user, ok := accounts.Authenticate(strings.TrimSpace(username), password)
	if !ok {
		return "", accounts.User{}, fault.Unauthenticated("账号或密码不正确")
	}
	token, err := s.EstablishSession(user, "client")
	return token, user, err
}

func (s *Store) EstablishSession(user accounts.User, audience string) (string, error) {
	current, err := trustedUser(user)
	if err != nil {
		return "", err
	}
	if err := validateAudienceUser(current, audience); err != nil {
		return "", err
	}
	token, err := newSessionToken()
	if err != nil {
		return "", err
	}
	item := session{
		Token: sessionTokenDigest(token), ActorID: current.ID, Issuer: current.Issuer, Subject: current.Subject,
		TenantID: current.TenantID, OrgID: current.OrgID, Audience: audience,
		ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour).Format(timeLayout),
	}
	// Sessions are read straight from PostgreSQL and revoked by deleting the row,
	// so establishing one is a narrow single-row write: the token digest is the
	// transaction key, the tenant/actor authority comes from the trusted
	// identity, and no other entity is touched. The in-memory projection contains
	// the digest only; general persistence does not write sessions.
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: current.TenantID, OrgID: current.OrgID, Role: "api"}); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (token, actor_id, expires_at, issuer, subject, tenant_id, org_id, audience)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		item.Token, item.ActorID, item.ExpiresAt, item.Issuer, item.Subject, item.TenantID, item.OrgID, item.Audience); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.patchSessionProjection(item, false)
	return token, nil
}

// AuthenticateSession resolves identity and audience from one authoritative
// PostgreSQL read. A caller must start a new read for each HTTP request.
func (s *Store) AuthenticateSession(ctx context.Context, token string) (accounts.User, string, error) {
	current, user, err := s.sessionFromDB(ctx, token)
	if err != nil {
		return accounts.User{}, "", err
	}
	return user, current.Audience, nil
}

func (s *Store) SessionAudience(token string) (string, error) {
	_, audience, err := s.AuthenticateSession(context.Background(), token)
	return audience, err
}

// Logout revokes exactly the presented session token. The deletion is scoped to
// its digest (row level security only exposes token = app.session_token), so one
// session can never revoke another, and the revocation is durable immediately
// for every reader because sessions are read from PostgreSQL. Revoking an
// unknown or already revoked token is a no-op, which keeps logout idempotent.
func (s *Store) Logout(token string) error {
	if token == "" {
		return nil
	}
	digest := sessionTokenDigest(token)
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalSessionScope(ctx, tx, digest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE token=$1`, digest); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.patchSessionProjection(session{Token: digest}, true)
	return nil
}

func (s *Store) CreateCase(user accounts.User, title, contractType string) (string, error) {
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
	case "procurement_ict", "network_engineering", "unspecified":
	default:
		return "", fault.Invalid("合同类型不在本版范围内")
	}
	id, err := newID("case_")
	if err != nil {
		return "", err
	}
	now := stamp()
	item := Case{
		ID: id, TenantID: current.TenantID, OwnerOrgID: current.OrgID,
		BusinessLine: "unspecified", Scenario: "unspecified", BusinessStatus: "active",
		Title: title, ContractType: contractType, OwnerID: current.ID,
		MemberIDs: []string{current.ID}, CreatedAt: now, UpdatedAt: now,
	}
	if !authorizeCase(item, current, access.Manage) {
		return "", fault.Forbidden("当前授权不能创建案件")
	}
	// Creating a case is a narrow two-row write: the case row and its activity
	// projection, keyed by the case id, with the identity authority taken from the
	// trusted account and the activity table routed by the service owner. No other
	// entity is touched, so the global state writer is not involved.
	if s.serviceOwner != "" && !ownedTable(s.serviceOwner, "cases") {
		return "", fault.Forbidden("当前服务不能创建案件")
	}
	act, err := newID("act_")
	if err != nil {
		return "", err
	}
	activity := Activity{ID: act, CaseID: id, At: now, Summary: "创建了案件"}
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: current.TenantID, OrgID: current.OrgID, Role: "api"}); err != nil {
		return "", err
	}
	// Same case key as the general case-scoped writer, so a later archive/member
	// transition cannot interleave with this insert.
	if err := scopedCaseLock(ctx, tx, item.ID); err != nil {
		return "", err
	}
	members, err := jsonText(item.MemberIDs)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cases (id, tenant_id, org_id, business_line, scenario, business_status, archived_at, title, contract_type, owner_id, member_ids, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (id) DO NOTHING`,
		item.ID, item.TenantID, item.OwnerOrgID, item.BusinessLine, item.Scenario, item.BusinessStatus, item.ArchivedAt,
		item.Title, item.ContractType, item.OwnerID, members, item.CreatedAt, item.UpdatedAt); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO `+activityTableFor(s.serviceOwner)+` (id, case_id, occurred_at, summary) VALUES ($1,$2,$3,$4)`,
		activity.ID, activity.CaseID, activity.At, activity.Summary); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.patchCaseProjection(item, activity)
	return id, nil
}

// patchCaseProjection publishes a newly created case and its activity row to the
// read model. Only those two slices are touched under the state lock: the case
// write above is authoritative in PostgreSQL, so no whole-state clone is needed.
func (s *Store) patchCaseProjection(item Case, activity Activity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replaced := false
	for i := range s.st.Cases {
		if s.st.Cases[i].ID == item.ID {
			s.st.Cases[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		s.st.Cases = append(s.st.Cases, item)
	}
	for _, existing := range s.st.Activity {
		if existing.ID == activity.ID {
			return
		}
	}
	s.st.Activity = append(s.st.Activity, activity)
}

func (s *Store) visibleCase(st *state, user accounts.User, caseID string) (Case, error) {
	item, ok := st.caseByID(caseID)
	if !ok || !item.allows(user) {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	return item, nil
}

// CaseSummary is one row of the case list.
type CaseSummary struct {
	Case           Case
	DocumentCount  int
	Pending        int
	Run            *Run
	State          string
	PartialReason  string
	FailureMessage string
	Counts         *Counts
}

func (s *Store) ListCases(user accounts.User) []CaseSummary {
	items, err := s.ListCasesChecked(user)
	if err != nil {
		return nil
	}
	return items
}

func (s *Store) ListCasesChecked(user accounts.User) ([]CaseSummary, error) {
	current, err := trustedUser(user)
	if err != nil {
		return nil, err
	}
	st, err := s.snapshotScoped(context.Background(), userScope(current))
	if err != nil {
		return nil, err
	}
	var out []CaseSummary
	for i := len(st.Cases) - 1; i >= 0; i-- {
		item := st.Cases[i]
		if !authorizeCase(item, current, access.Read) {
			continue
		}
		out = append(out, s.summarize(&st, item))
	}
	return out, nil
}
func (s *Store) AddMember(user accounts.User, caseID, memberID string) error {
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能更改成员")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	member, ok := accounts.ByID(strings.TrimSpace(memberID))
	if !ok || member.TenantID != current.TenantID {
		return fault.Invalid("没有这个账号")
	}
	return s.scopedCaseMutation(context.Background(), current, caseID, func(st *state) error {
		idx := -1
		for i := range st.Cases {
			if st.Cases[i].ID == caseID {
				idx = i
				break
			}
		}
		if idx < 0 || !st.Cases[idx].allows(current) {
			return fault.NotFound("没有找到对应记录")
		}
		if !authorizeCase(st.Cases[idx], current, access.Manage) {
			return fault.Forbidden("当前身份不能更改案件成员")
		}
		if err := businessWritable(st.Cases[idx]); err != nil {
			return err
		}
		for _, id := range st.Cases[idx].MemberIDs {
			if id == member.ID {
				return nil
			}
		}
		st.Cases[idx].MemberIDs = append(append([]string{}, st.Cases[idx].MemberIDs...), member.ID)
		st.Cases[idx].UpdatedAt = stamp()
		return nil
	})
}

func (s *Store) summarize(st *state, item Case) CaseSummary {
	summary := CaseSummary{Case: item, DocumentCount: len(st.documents(item.ID))}
	run, ok := st.latestRun(item.ID)
	if !ok {
		return summary
	}
	view := s.viewRun(st, run)
	run.State = view.State
	run.UpdatedAt = item.UpdatedAt
	if view.Run.UpdatedAt > run.UpdatedAt {
		run.UpdatedAt = view.Run.UpdatedAt
	}
	summary.Run = &run
	summary.State = view.State
	summary.PartialReason = view.PartialReason
	summary.FailureMessage = view.FailureMessage
	summary.Pending = view.Counts.Undecided
	counts := view.Counts
	summary.Counts = &counts
	return summary
}

// CaseDetail is the case page.
type CaseDetail struct {
	CaseSummary
	Documents                           []Interpreted               `json:"documents"`
	Materials                           []MaterialProjection        `json:"materials"`
	MaterialVersions                    []MaterialVersionProjection `json:"material_versions"`
	MaterialSelection                   MaterialSelectionProjection `json:"material_selection"`
	Activities                          []Activity                  `json:"activities"`
	RevisionPending                     bool                        `json:"revision_pending"`
	ConfigurationPending                bool                        `json:"configuration_pending"`
	SubjectContextChangedAfterLatestRun bool                        `json:"subject_context_changed_after_latest_run"`
	BusinessLine                        string                      `json:"business_line"`
	Scenario                            string                      `json:"scenario"`
	BusinessStatus                      string                      `json:"business_status"`
	Generations                         []GenerationRecord          `json:"generations"`
	Remediations                        []Remediation               `json:"remediations"`
	Reports                             []ReportRecord              `json:"reports"`
	Archives                            []ArchiveRecord             `json:"archives"`
	ArchivedAt                          *string                     `json:"archived_at"`
}

func (s *Store) GetCase(user accounts.User, caseID string) (CaseDetail, error) {
	st, err := s.snapshotAs(user)
	if err != nil {
		return CaseDetail{}, err
	}
	item, err := s.visibleCase(&st, user, caseID)
	if err != nil {
		return CaseDetail{}, err
	}
	detail := CaseDetail{
		CaseSummary:     s.summarize(&st, item),
		Documents:       s.interpretAll(st.documents(item.ID)),
		RevisionPending: revisionPending(&st, item.ID),
		BusinessLine:    item.BusinessLine, Scenario: item.Scenario, BusinessStatus: item.BusinessStatus,
		ArchivedAt: item.ArchivedAt,
		Materials:  []MaterialProjection{}, MaterialVersions: []MaterialVersionProjection{},
		MaterialSelection: MaterialSelectionProjection{Version: 1, Status: "not_applicable", DocumentIDs: []string{}, CandidateDocumentIDs: []string{}},
		Generations:       []GenerationRecord{}, Remediations: []Remediation{}, Reports: []ReportRecord{}, Archives: []ArchiveRecord{},
	}
	selectedDocs := map[string]bool{}
	for _, doc := range selectedMaterialDocuments(st, item.ID) {
		selectedDocs[doc.ID] = true
		detail.MaterialSelection.DocumentIDs = append(detail.MaterialSelection.DocumentIDs, doc.ID)
	}
	var chain *CaseMaterialChain
	for i := range st.MaterialChains {
		if st.MaterialChains[i].CaseID == item.ID && st.MaterialChains[i].TenantID == item.TenantID && st.MaterialChains[i].OrgID == item.OwnerOrgID {
			chain = &st.MaterialChains[i]
			break
		}
	}
	if chain != nil {
		detail.MaterialSelection.Version = chain.SelectionVersion
		detail.MaterialSelection.Status = chain.LegacySelectionStatus
		detail.MaterialSelection.PrimaryMaterialID = chain.PrimaryMaterialID
		if chain.LegacySelectionStatus == "pending_confirmation" {
			detail.MaterialSelection.CandidateDocumentIDs = append([]string(nil), chain.SelectedDocumentIDs...)
		}
	}
	if primaryID := casePrimaryMaterialID(st, item.ID); primaryID != "" {
		detail.MaterialSelection.PrimaryMaterialID = &primaryID
	}
	for _, material := range st.Materials {
		if material.CaseID != item.ID || material.TenantID != item.TenantID || material.OrgID != item.OwnerOrgID {
			continue
		}
		selected := selectedDocs[material.CurrentDocumentID]
		detail.Materials = append(detail.Materials, MaterialProjection{MaterialID: material.ID, Role: material.Role, Purpose: material.Purpose, LegacyStatus: material.LegacyStatus, LegacySelected: material.LegacySelected, Selected: selected, CurrentDocumentID: material.CurrentDocumentID, CurrentVersion: material.CurrentVersion})
	}
	for _, version := range st.MaterialVersions {
		if version.CaseID == item.ID && version.TenantID == item.TenantID && version.OrgID == item.OwnerOrgID {
			var previous *string
			if version.PreviousDocument != "" {
				value := version.PreviousDocument
				previous = &value
			}
			detail.MaterialVersions = append(detail.MaterialVersions, MaterialVersionProjection{MaterialID: version.MaterialID, Version: version.Version, DocumentID: version.DocumentID, PreviousDocumentID: previous, CreatedAt: version.CreatedAt})
		}
	}
	if len(detail.Materials) > 0 && detail.MaterialSelection.Status == "not_applicable" {
		detail.MaterialSelection.Status = "confirmed"
	}
	if run, ok := st.latestRun(item.ID); ok {
		frozen := st.snapshotOf(run.ID)
		detail.ConfigurationPending = !sameRunConfiguration(frozen, withFrozenGovernance(s.currentSignature(nil), frozen))
		detail.SubjectContextChangedAfterLatestRun = subjectContextChangedSinceRun(st, item.ID, frozen)
	}
	for _, record := range st.Generations {
		if record.TenantID == item.TenantID && record.OrgID == item.OwnerOrgID && record.CaseID == item.ID {
			detail.Generations = append(detail.Generations, generationRecordWithMaterial(&st, record.Record))
		}
	}
	for _, record := range st.Remediations {
		if record.TenantID == item.TenantID && record.OrgID == item.OwnerOrgID && record.Record.CaseID == item.ID {
			detail.Remediations = append(detail.Remediations, record.Record)
		}
	}
	for _, record := range st.Reports {
		if record.TenantID == item.TenantID && record.OrgID == item.OwnerOrgID && record.CaseID == item.ID {
			detail.Reports = append(detail.Reports, record.Record)
		}
	}
	for _, record := range st.Archives {
		if record.TenantID == item.TenantID && record.OrgID == item.OwnerOrgID && record.Record.CaseID == item.ID {
			detail.Archives = append(detail.Archives, record.Record)
		}
	}
	for _, act := range st.Activity {
		if act.CaseID == caseID {
			detail.Activities = append(detail.Activities, act)
		}
	}
	if detail.Activities == nil {
		detail.Activities = []Activity{}
	}
	return detail, nil
}

// AddDocument stores an upload and carries the caller deadline through
// object access and initial deterministic parsing.
func (s *Store) AddDocument(ctx context.Context, user accounts.User, caseID, role, filename, media string, body []byte) (string, error) {
	result, err := s.UploadMaterial(ctx, user, caseID, role, filename, media, body, MaterialUploadOptions{})
	return result.DocumentID, err
}

func (s *Store) AddDocumentWithOptions(ctx context.Context, user accounts.User, caseID, role, filename, media string, body []byte, options MaterialUploadOptions) (string, error) {
	result, err := s.UploadMaterial(ctx, user, caseID, role, filename, media, body, options)
	return result.DocumentID, err
}

func (s *Store) UploadMaterial(ctx context.Context, user accounts.User, caseID, role, filename, media string, body []byte, options MaterialUploadOptions) (DocumentUploadResult, error) {
	currentUser, caseItem, err := s.documentWriteCase(ctx, user, caseID, access.Manage)
	if err != nil {
		return DocumentUploadResult{}, err
	}
	if len(body) > 32<<20 {
		return DocumentUploadResult{}, fault.TooLarge("文件超过 32 MiB")
	}
	switch role {
	case "procurement", "award", "draft_contract", "attachment":
	default:
		return DocumentUploadResult{}, fault.Invalid("材料角色不在本版范围内")
	}
	if err := validateMaterialUploadOptions(options); err != nil {
		return DocumentUploadResult{}, fault.Invalid(err.Error())
	}
	purpose, err := resolveMaterialPurpose(role, options.Purpose)
	if err != nil {
		return DocumentUploadResult{}, fault.Invalid(err.Error())
	}
	filename = cleanName(filename)
	media = cleanMedia(media)
	options.IdempotencyKey = strings.TrimSpace(options.IdempotencyKey)
	if len(options.IdempotencyKey) > 200 || strings.ContainsAny(options.IdempotencyKey, "\r\n\x00") {
		return DocumentUploadResult{}, fault.Invalid("Idempotency-Key无效")
	}
	requestHash := uploadRequestHash("upload", currentUser.ID, caseID, role, purpose, filename, media, body, options)
	if options.IdempotencyKey != "" {
		st, err := s.snapshotScoped(ctx, userScope(currentUser))
		if err != nil {
			return DocumentUploadResult{}, err
		}
		for _, prior := range st.UploadIdempotency {
			if prior.CaseID == caseID && prior.ActorID == currentUser.ID && prior.Key == options.IdempotencyKey {
				if !bytes.Equal(prior.RequestHash, requestHash[:]) {
					return DocumentUploadResult{}, fault.Conflict("Idempotency-Key已用于不同上传内容")
				}
				for _, v := range st.MaterialVersions {
					if v.CaseID == caseID && v.MaterialID == prior.MaterialID && v.DocumentID == prior.DocumentID {
						return DocumentUploadResult{DocumentID: prior.DocumentID, MaterialID: prior.MaterialID, Version: v.Version, Replayed: true}, nil
					}
				}
				return DocumentUploadResult{}, errors.New("upload idempotency record has no material version")
			}
		}
	}
	id, err := newID("doc_")
	if err != nil {
		return DocumentUploadResult{}, err
	}
	version, err := docversion.New(id, docversion.Source{Origin: "upload", Filename: filename, MediaType: media}, body)
	if err != nil {
		return DocumentUploadResult{}, err
	}
	digest := version.Digest().String()
	if err := s.putObject(ctx, caseItem.TenantID, caseID, digest, body); err != nil {
		return DocumentUploadResult{}, err
	}
	now := stamp()
	materialNow := time.Now().UTC()
	document := Document{
		ID: id, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID,
		Role: role, Filename: filename, MediaType: media,
		Digest: digest, Size: version.Size(), Origin: "upload", StorageBackend: s.objectBackend(), CreatedAt: now,
	}
	var initialParse *ParsedDocument
	if DocumentParseKind(filename, media) != "ocr" {
		models := s.currentModels()
		selection, err := s.reserveParseSelection(ctx, document, models)
		if err != nil {
			return DocumentUploadResult{}, err
		}
		interpreted := s.interpretUnpersistedWithModels(ctx, document, models)
		if contextErr := ctx.Err(); contextErr != nil {
			interpreted = Interpreted{Document: document, ParseError: contextErr.Error(), FromOCR: interpreted.FromOCR}
		}
		parsed, parseErr := makeParsedWithSelection(document, interpreted, selection)
		if parseErr != nil {
			return DocumentUploadResult{}, parseErr
		}
		initialParse = &parsed
	}
	persistCtx := ctx
	persistCancel := func() {}
	if initialParse != nil && ctx.Err() != nil {
		persistCtx, persistCancel = parseFailurePersistenceContext(ctx)
	}
	defer persistCancel()
	var uploadResult DocumentUploadResult
	err = s.scopedCaseMutation(persistCtx, currentUser, caseID, func(st *state) error {
		if _, err := s.writableCase(st, currentUser, caseID, access.Manage); err != nil {
			return err
		}
		if options.IdempotencyKey != "" {
			for _, prior := range st.UploadIdempotency {
				if prior.CaseID == caseID && prior.ActorID == currentUser.ID && prior.Key == options.IdempotencyKey {
					if !bytes.Equal(prior.RequestHash, requestHash[:]) {
						return fault.Conflict("Idempotency-Key已用于不同上传内容")
					}
					for _, priorVersion := range st.MaterialVersions {
						if priorVersion.CaseID == caseID && priorVersion.MaterialID == prior.MaterialID && priorVersion.DocumentID == prior.DocumentID {
							uploadResult = DocumentUploadResult{DocumentID: prior.DocumentID, MaterialID: prior.MaterialID, Version: priorVersion.Version, Replayed: true}
							return nil
						}
					}
					return errors.New("upload idempotency record has no material version")
				}
			}
		}
		intent := options.Intent
		if intent == "" {
			intent = "add"
		}
		var material *MaterialEntity
		if intent == "replace" {
			for i := range st.Materials {
				if st.Materials[i].ID == options.MaterialID {
					material = &st.Materials[i]
					break
				}
			}
			if material == nil {
				return fault.NotFound("没有找到对应材料")
			}
			if material.CurrentDocumentID != options.ReplacesDocumentID {
				return fault.Conflict("材料当前版本已变化，请刷新后重试")
			}
			if material.Role != role || material.Purpose != purpose {
				return fault.Invalid("替换不能改变材料角色或用途")
			}
			if purpose == "primary_contract" {
				if err := validatePrimaryMaterialMutation(st, caseID, intent, material); err != nil {
					return err
				}
			}
		} else {
			if len(st.Materials) >= maxMaterialsPerCase {
				return fault.TooLarge("案件材料数量达到上限")
			}
			if purpose == "primary_contract" {
				if err := validatePrimaryMaterialMutation(st, caseID, intent, nil); err != nil {
					return err
				}
			}
			materialID, err := newID("mat_")
			if err != nil {
				return err
			}
			material = &MaterialEntity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, ID: materialID, Role: role, Purpose: purpose, LegacyStatus: "native", CurrentVersion: 0, CreatedBy: currentUser.ID, CreatedAt: materialNow}
			st.Materials = append(st.Materials, *material)
			material = &st.Materials[len(st.Materials)-1]
		}
		st.Documents = append(st.Documents, document)
		previousDocument := ""
		if intent == "replace" {
			previousDocument = material.CurrentDocumentID
		}
		material.CurrentVersion++
		material.CurrentDocumentID = document.ID
		st.MaterialVersions = append(st.MaterialVersions, MaterialVersion{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, MaterialID: material.ID, Version: material.CurrentVersion, DocumentID: document.ID, PreviousDocument: previousDocument, CreatedBy: currentUser.ID, CreatedAt: materialNow})
		if options.IdempotencyKey != "" {
			st.UploadIdempotency = append(st.UploadIdempotency, UploadIdempotencyRecord{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, ActorID: currentUser.ID, Key: options.IdempotencyKey, RequestHash: append([]byte(nil), requestHash[:]...), DocumentID: document.ID, MaterialID: material.ID, CreatedAt: materialNow})
		}
		uploadResult = DocumentUploadResult{DocumentID: document.ID, MaterialID: material.ID, Version: material.CurrentVersion}
		var chain *CaseMaterialChain
		for i := range st.MaterialChains {
			if st.MaterialChains[i].TenantID == caseItem.TenantID && st.MaterialChains[i].OrgID == caseItem.OwnerOrgID && st.MaterialChains[i].CaseID == caseID {
				chain = &st.MaterialChains[i]
				break
			}
		}
		if chain == nil {
			status := "confirmed"
			st.MaterialChains = append(st.MaterialChains, CaseMaterialChain{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, LegacySelectionStatus: status, SelectionVersion: 1, CreatedAt: materialNow, UpdatedAt: materialNow})
			chain = &st.MaterialChains[len(st.MaterialChains)-1]
		}
		if intent == "replace" {
			filtered := chain.SelectedDocumentIDs[:0]
			for _, selectedID := range chain.SelectedDocumentIDs {
				if selectedID != previousDocument {
					filtered = append(filtered, selectedID)
				}
			}
			chain.SelectedDocumentIDs = filtered
		}
		chain.SelectedDocumentIDs = append(chain.SelectedDocumentIDs, document.ID)
		chain.SelectionVersion++
		chain.UpdatedAt = materialNow
		if purpose == "primary_contract" {
			chain.PrimaryMaterialID = &material.ID
			if material.LegacySelected {
				chain.LegacySelectionStatus = "confirmed"
				material.LegacyStatus = "confirmed"
			} else if chain.LegacySelectionStatus == "not_applicable" {
				chain.LegacySelectionStatus = "confirmed"
			}
		}
		if initialParse != nil {
			st.ParsedDocuments = append(st.ParsedDocuments, *initialParse)
		}
		// Case.UpdatedAt is derived on read by applyReadProjections; writing the
		// core case row from the document service would be a cross-owner write.
		act, err := newID("act_")
		if err != nil {
			return err
		}
		st.Activity = append(st.Activity, Activity{
			ID: act, CaseID: caseID, At: now,
			Summary: "登记了" + labels.Role(role) + " " + filename,
		})
		return nil
	})
	return uploadResult, err
}

// Reparse appends a new parse selection and makes cancellation visible
// to the actual parser while retaining failed parser output as history.
func (s *Store) Reparse(ctx context.Context, user accounts.User, caseID, docID string) error {
	currentUser, _, err := s.documentWriteCase(ctx, user, caseID, access.Manage)
	if err != nil {
		return err
	}
	st, err := s.snapshotScoped(ctx, userScope(currentUser))
	if err != nil {
		return err
	}
	if _, err := s.writableCase(&st, currentUser, caseID, access.Manage); err != nil {
		return err
	}
	for _, doc := range st.documents(caseID) {
		if doc.ID != docID {
			continue
		}
		parseCtx := model.WithDelegatedScope(ctx, modelScope(model.DelegatedScope{
			Kind: "document", TenantID: doc.TenantID, OrgID: doc.OrgID,
			CaseID: doc.CaseID, ActorID: currentUser.ID, DocumentID: doc.ID,
		}, s.currentModels()))
		parsed, err := s.ensureParsed(parseCtx, currentUser, doc, true)
		if err != nil {
			return err
		}
		if contextErr := ctx.Err(); contextErr != nil && parsed.Status == "failed" {
			if errors.Is(contextErr, context.DeadlineExceeded) {
				return fault.New(504, "document_timeout", "材料解析超时；失败状态已保存")
			}
			return contextErr
		}
		return nil
	}
	return fault.NotFound("没有找到对应记录")
}

func (s *Store) Original(user accounts.User, caseID, docID string) (Document, []byte, error) {
	st, err := s.snapshotAs(user)
	if err != nil {
		return Document{}, nil, err
	}
	if _, err := s.visibleCase(&st, user, caseID); err != nil {
		return Document{}, nil, err
	}
	for _, doc := range st.documents(caseID) {
		if doc.ID != docID {
			continue
		}
		body, err := s.getObject(context.Background(), doc)
		if err != nil {
			if err == errObjectIntegrity {
				return Document{}, nil, fault.New(409, "digest_mismatch", "原件摘要与保存的字节不一致")
			}
			if doc.StorageBackend == "remote" {
				return Document{}, nil, fault.New(503, "object_store_unavailable", "原件存储暂不可用")
			}
			return Document{}, nil, fault.NotFound("原文没有找到")
		}
		return doc, body, nil
	}
	return Document{}, nil, fault.NotFound("没有找到对应记录")
}

func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimSpace(filepathBase(name))
	if name == "" || name == "." || name == ".." {
		return "upload.txt"
	}
	return name
}

func filepathBase(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func cleanMedia(media string) string {
	media = strings.TrimSpace(media)
	if i := strings.Index(media, ";"); i >= 0 {
		media = strings.TrimSpace(media[:i])
	}
	if media == "" {
		return "application/octet-stream"
	}
	return media
}

// patchSessionProjection keeps the read-only in-memory session list in step
// with the narrow session writes. It mutates only that slice under the state
// lock: the projection is never a write source, so there is no reason to clone
// or serialize the whole contract store for a login or logout.
func (s *Store) patchSessionProjection(item session, remove bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remove {
		for i := range s.st.Sessions {
			if s.st.Sessions[i].Token == item.Token {
				s.st.Sessions = append(s.st.Sessions[:i], s.st.Sessions[i+1:]...)
				return
			}
		}
		return
	}
	for i := range s.st.Sessions {
		if s.st.Sessions[i].Token == item.Token {
			s.st.Sessions[i] = item
			return
		}
	}
	s.st.Sessions = append(s.st.Sessions, item)
}

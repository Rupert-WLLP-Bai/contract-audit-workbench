package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

type SubjectCreateInput struct {
	DisplayName string                      `json:"display_name"`
	LegalName   string                      `json:"legal_name,omitempty"`
	Identifiers []SubjectIdentityClaimInput `json:"identifiers,omitempty"`
	Aliases     []SubjectAliasInput         `json:"aliases,omitempty"`
}

type LegacyBindingInput struct {
	Field                  string `json:"field"`
	Value                  string `json:"value"`
	ExpectedProfileVersion int    `json:"expected_profile_version"`
}

type CaseSubjectCreateInput struct {
	Role          string              `json:"role"`
	Subject       SubjectCreateInput  `json:"subject"`
	LegacyBinding *LegacyBindingInput `json:"legacy_binding,omitempty"`
}

type CreateSubjectResult struct {
	SubjectID      string                   `json:"subject_id"`
	CurrentVersion int64                    `json:"current_version"`
	SubjectVersion SubjectVersionProjection `json:"subject_version"`
	CreatedAt      time.Time                `json:"created_at"`
}

type CreateCaseSubjectResult struct {
	BindingID         string                   `json:"binding_id"`
	BindingVersion    int64                    `json:"binding_version"`
	SubjectID         string                   `json:"subject_id"`
	SubjectVersion    int64                    `json:"subject_version"`
	Role              string                   `json:"role"`
	DisplayName       string                   `json:"display_name"`
	VerificationState string                   `json:"verification_state"`
	SourceKind        string                   `json:"source_kind"`
	LegacyLabel       *string                  `json:"legacy_label"`
	BindingState      string                   `json:"binding_state"`
	Subject           SubjectVersionProjection `json:"subject"`
}

type BindExistingSubjectInput struct {
	Role                   string `json:"role"`
	SubjectID              string `json:"subject_id"`
	ExpectedSubjectVersion int64  `json:"expected_subject_version"`
}

type BindExistingSubjectResult struct {
	BindingID         string    `json:"binding_id"`
	BindingVersion    int64     `json:"binding_version"`
	SubjectID         string    `json:"subject_id"`
	SubjectVersion    int64     `json:"subject_version"`
	Role              string    `json:"role"`
	DisplayName       string    `json:"display_name"`
	VerificationState string    `json:"verification_state"`
	BindingState      string    `json:"binding_state"`
	CreatedAt         time.Time `json:"created_at"`
	SourceKind        string    `json:"source_kind"`
}

type SubjectListResult struct {
	Items      []SubjectSummary `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type SubjectDetailResult struct {
	SubjectID      string                           `json:"subject_id"`
	CurrentVersion int64                            `json:"current_version"`
	Current        SubjectVersionProjection         `json:"current"`
	Identifiers    []SubjectIdentityClaimProjection `json:"identifiers"`
}

type SubjectChangeEventSummary struct {
	EventID   string                     `json:"event_id"`
	Kind      string                     `json:"kind"`
	RequestID string                     `json:"request_id"`
	CaseID    string                     `json:"case_id"`
	Actor     TrustedPrincipalProjection `json:"actor"`
	CreatedAt time.Time                  `json:"created_at"`
}

type SubjectHistoryResult struct {
	Versions []SubjectVersionProjection  `json:"versions"`
	Events   []SubjectChangeEventSummary `json:"events"`
}

type SubjectCaseLinksResult struct {
	Items        []SubjectCaseLinkProjection `json:"items"`
	VisibleCount int                         `json:"visible_count"`
	NextCursor   *string                     `json:"next_cursor"`
}

type SubjectCaseLinkProjection struct {
	BindingID      string                     `json:"binding_id"`
	BindingVersion int64                      `json:"binding_version"`
	CaseID         string                     `json:"case_id"`
	CaseTitle      string                     `json:"case_title"`
	BusinessStatus string                     `json:"business_status"`
	Role           string                     `json:"role"`
	BindingState   string                     `json:"binding_state"`
	SourceKind     string                     `json:"source_kind"`
	LegacyLabel    *string                    `json:"legacy_label"`
	CreatedBy      TrustedPrincipalProjection `json:"created_by"`
	Subject        SubjectVersionProjection   `json:"subject"`
	CreatedAt      time.Time                  `json:"created_at"`
}

func (s *Store) CreateUnboundSubject(ctx context.Context, user accounts.User, input SubjectCreateInput, key string) (CreateSubjectResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return CreateSubjectResult{}, false, err
	}
	if s.ReadOnly() {
		return CreateSubjectResult{}, false, fault.New(503, "read_only", "只读模式下不能创建主体")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CreateSubjectResult{}, false, err
	}
	input, err = normalizeSubjectCreateInput(input)
	if err != nil {
		return CreateSubjectResult{}, false, fault.Invalid(err.Error())
	}
	if err := validateIdempotencyKey(key); err != nil {
		return CreateSubjectResult{}, false, err
	}
	requestHash, err := canonicalSubjectHash(input)
	if err != nil {
		return CreateSubjectResult{}, false, err
	}
	var result CreateSubjectResult
	replayed := false
	err = s.updateAsContext(ctx, current, func(st *state) error {
		if prior, ok, err := readSubjectIdempotency(*st, current, "create_subject", "unbound", key, requestHash); err != nil {
			return err
		} else if ok {
			replayed = true
			return json.Unmarshal(prior.Response, &result)
		}
		subject, version, err := newSubjectAggregate(current, input, time.Now().UTC())
		if err != nil {
			return err
		}
		st.Subjects = append(st.Subjects, subject)
		st.SubjectVersions = append(st.SubjectVersions, version)
		result = CreateSubjectResult{SubjectID: subject.ID, CurrentVersion: 1, SubjectVersion: projectSubjectVersion(version, false), CreatedAt: subject.CreatedAt}
		return appendSubjectIdempotency(st, current, "create_subject", "unbound", key, requestHash, 201, result, time.Now().UTC())
	})
	return result, replayed, err
}

func (s *Store) CreateCaseSubject(ctx context.Context, user accounts.User, caseID string, input CaseSubjectCreateInput, key string) (CreateCaseSubjectResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	if s.ReadOnly() {
		return CreateCaseSubjectResult{}, false, fault.New(503, "read_only", "只读模式下不能创建案件主体")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	caseID = strings.TrimSpace(caseID)
	input.Role = strings.TrimSpace(input.Role)
	input.Subject, err = normalizeSubjectCreateInput(input.Subject)
	if caseID == "" || !validSubjectRole(input.Role) || err != nil {
		return CreateCaseSubjectResult{}, false, fault.Invalid("案件主体字段无效")
	}
	if input.LegacyBinding != nil {
		input.LegacyBinding.Field = strings.TrimSpace(input.LegacyBinding.Field)
		if input.LegacyBinding.Value == "" || input.LegacyBinding.ExpectedProfileVersion < 1 ||
			(input.LegacyBinding.Field != "customer" && input.LegacyBinding.Field != "supplier") || input.LegacyBinding.Field != input.Role {
			return CreateCaseSubjectResult{}, false, fault.Invalid("legacy_binding字段无效或与主体角色不一致")
		}
	}
	if err := validateIdempotencyKey(key); err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	requestHash, err := canonicalSubjectHash(input)
	if err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	var result CreateCaseSubjectResult
	replayed := false
	err = s.scopedCaseWrite(ctx, current, caseID, "", "", true, false, nil, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		if prior, ok, err := readSubjectIdempotency(*st, current, "create_case_subject", caseID, key, requestHash); err != nil {
			return err
		} else if ok {
			replayed = true
			return json.Unmarshal(prior.Response, &result)
		}
		if input.LegacyBinding != nil {
			profile, exists := profileFor(*st, *caseItem)
			if !exists || profile.Version != input.LegacyBinding.ExpectedProfileVersion {
				return fault.Conflict("案件旧资料版本已变化，请刷新后重试")
			}
			legacyValue := profile.Customer
			if input.LegacyBinding.Field == "supplier" {
				legacyValue = profile.Supplier
			}
			if legacyValue == "" || legacyValue != input.LegacyBinding.Value {
				return fault.Conflict("案件旧资料已变化，请刷新后重试")
			}
		}
		now := time.Now().UTC()
		subject, version, err := newSubjectAggregate(current, input.Subject, now)
		if err != nil {
			return err
		}
		bindingID, err := newID("bind_")
		if err != nil {
			return err
		}
		bindingState, sourceKind := "pending_confirmation", "user_asserted"
		var legacyLabel *string
		if input.LegacyBinding != nil {
			bindingState, sourceKind = "pending_confirmation", "legacy_label"
			value := input.LegacyBinding.Value
			legacyLabel = &value
		}
		binding := CaseSubjectRole{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, BindingID: bindingID, CurrentVersion: 1, CreatedByUser: current.ID, CreatedAt: now}
		bindingVersion := CaseSubjectRoleVersion{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, BindingID: bindingID, Version: 1, SubjectID: subject.ID, SubjectVersion: 1, Role: input.Role, BindingState: bindingState, SourceKind: sourceKind, CreatedByUser: current.ID, CreatedByIssuer: current.Issuer, CreatedBySubject: current.Subject, CreatedAt: now}
		if legacyLabel != nil {
			bindingVersion.LegacyLabel = *legacyLabel
		}
		st.Subjects = append(st.Subjects, subject)
		st.SubjectVersions = append(st.SubjectVersions, version)
		st.CaseSubjectRoles = append(st.CaseSubjectRoles, binding)
		st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, bindingVersion)
		result = CreateCaseSubjectResult{BindingID: bindingID, BindingVersion: 1, SubjectID: subject.ID, SubjectVersion: 1,
			Role: input.Role, DisplayName: version.DisplayName, VerificationState: version.VerificationState,
			SourceKind: sourceKind, LegacyLabel: legacyLabel, BindingState: bindingState, Subject: projectSubjectVersion(version, false)}
		return appendSubjectIdempotency(st, current, "create_case_subject", caseID, key, requestHash, 201, result, now)
	})
	return result, replayed, err
}

func (s *Store) BindExistingSubject(ctx context.Context, user accounts.User, caseID string, input BindExistingSubjectInput, key string) (BindExistingSubjectResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	if s.ReadOnly() {
		return BindExistingSubjectResult{}, false, fault.New(503, "read_only", "只读模式下不能绑定案件主体")
	}
	current, err := trustedUser(user)
	if err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	caseID, input.SubjectID, input.Role = strings.TrimSpace(caseID), strings.TrimSpace(input.SubjectID), strings.TrimSpace(input.Role)
	if caseID == "" || input.SubjectID == "" || input.ExpectedSubjectVersion < 1 || !validSubjectRole(input.Role) {
		return BindExistingSubjectResult{}, false, fault.Invalid("主体绑定字段无效")
	}
	if err := validateIdempotencyKey(key); err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	requestHash, err := canonicalSubjectHash(input)
	if err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	var result BindExistingSubjectResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		if prior, ok, err := readSubjectIdempotency(*st, current, "bind_existing_subject", caseID, key, requestHash); err != nil {
			return err
		} else if ok {
			replayed = true
			return json.Unmarshal(prior.Response, &result)
		}
		subject, version, visible := visibleSubject(*st, current, input.SubjectID)
		if !visible {
			return fault.NotFound("没有找到对应记录")
		}
		if subject.CurrentVersion != input.ExpectedSubjectVersion {
			return fault.Conflict("主体版本已变化，请刷新后重试")
		}
		now := time.Now().UTC()
		bindingID, err := newID("bind_")
		if err != nil {
			return err
		}
		binding := CaseSubjectRole{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, BindingID: bindingID, CurrentVersion: 1, CreatedByUser: current.ID, CreatedAt: now}
		bindingVersion := CaseSubjectRoleVersion{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, BindingID: bindingID, Version: 1, SubjectID: subject.ID, SubjectVersion: version.Version, Role: input.Role, BindingState: "active", SourceKind: "user_asserted", CreatedByUser: current.ID, CreatedByIssuer: current.Issuer, CreatedBySubject: current.Subject, CreatedAt: now}
		st.CaseSubjectRoles = append(st.CaseSubjectRoles, binding)
		st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, bindingVersion)
		result = BindExistingSubjectResult{BindingID: bindingID, BindingVersion: 1, SubjectID: subject.ID, SubjectVersion: version.Version, Role: input.Role, DisplayName: version.DisplayName, VerificationState: version.VerificationState, BindingState: "active", CreatedAt: now, SourceKind: "user_asserted"}
		return appendSubjectIdempotency(st, current, "bind_existing_subject", caseID, key, requestHash, 201, result, now)
	})
	return result, replayed, err
}

func (s *Store) ListCaseSubjects(ctx context.Context, user accounts.User, caseID string) (CaseSubjectListProjection, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return CaseSubjectListProjection{}, err
	}
	st, err := s.snapshotAsContext(ctx, user)
	if err != nil {
		return CaseSubjectListProjection{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return CaseSubjectListProjection{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return CaseSubjectListProjection{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return CaseSubjectListProjection{}, err
	}
	projection := projectCaseSubjects(st, current, caseItem)
	projection.CanManage = caseSubjectCanManage(caseItem, current, s.ReadOnly())
	return projection, nil
}

func caseSubjectCanManage(caseItem Case, user accounts.User, readOnly bool) bool {
	return !readOnly && authorizeCase(caseItem, user, access.Manage) && businessWritable(caseItem) == nil
}

func (s *Store) GetSubjectDetail(ctx context.Context, user accounts.User, subjectID string) (SubjectDetailResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectDetailResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectDetailResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectDetailResult{}, err
	}
	subject, version, visible := visibleSubject(st, current, strings.TrimSpace(subjectID))
	if !visible {
		return SubjectDetailResult{}, fault.NotFound("没有找到对应记录")
	}
	projected := projectSubjectVersionForUser(st, current, version, false)
	return SubjectDetailResult{SubjectID: subject.ID, CurrentVersion: subject.CurrentVersion, Current: projected, Identifiers: projected.IdentityClaims}, nil
}

func (s *Store) GetSubjectHistory(ctx context.Context, user accounts.User, subjectID string) (SubjectHistoryResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectHistoryResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectHistoryResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectHistoryResult{}, err
	}
	subject, _, visible := visibleSubject(st, current, strings.TrimSpace(subjectID))
	if !visible {
		return SubjectHistoryResult{}, fault.NotFound("没有找到对应记录")
	}
	versions := make([]SubjectVersion, 0)
	for _, item := range st.SubjectVersions {
		if item.SubjectID == subject.ID {
			versions = append(versions, item)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	projected := make([]SubjectVersionProjection, 0, len(versions))
	for _, item := range versions {
		projected = append(projected, projectSubjectVersionForUser(st, current, item, false))
	}
	events := visibleSubjectEvents(st, current, subject.ID)
	return SubjectHistoryResult{Versions: projected, Events: events}, nil
}

func (s *Store) ListSubjectCaseLinks(ctx context.Context, user accounts.User, subjectID, cursor string, limit int) (SubjectCaseLinksResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectCaseLinksResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectCaseLinksResult{}, err
	}
	subjectID = strings.TrimSpace(subjectID)
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectCaseLinksResult{}, err
	}
	if _, _, visible := visibleSubject(st, current, strings.TrimSpace(subjectID)); !visible {
		return SubjectCaseLinksResult{}, fault.NotFound("没有找到对应记录")
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return SubjectCaseLinksResult{}, fault.Invalid("每页案件关联最多为100")
	}
	links := make([]SubjectCaseLinkProjection, 0)
	for _, caseItem := range st.Cases {
		if caseItem.TenantID != current.TenantID || !caseReadAllowed(caseItem, current) {
			continue
		}
		for _, binding := range projectCaseSubjects(st, current, caseItem).Items {
			if binding.Subject.SubjectID == subjectID && binding.BindingState != "inactive" {
				links = append(links, SubjectCaseLinkProjection{BindingID: binding.BindingID, BindingVersion: binding.BindingVersion,
					CaseID: binding.CaseID, CaseTitle: caseItem.Title, BusinessStatus: caseItem.BusinessStatus,
					Role: binding.Role, BindingState: binding.BindingState, SourceKind: binding.SourceKind, LegacyLabel: binding.LegacyLabel,
					CreatedBy: binding.CreatedBy, Subject: binding.Subject, CreatedAt: binding.CreatedAt})
			}
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].CaseID == links[j].CaseID {
			return links[i].BindingID < links[j].BindingID
		}
		return links[i].CaseID < links[j].CaseID
	})
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return SubjectCaseLinksResult{}, fault.Invalid("案件关联游标无效")
		}
		start = sort.Search(len(links), func(i int) bool { return links[i].CaseID+"\x00"+links[i].BindingID > string(decoded) })
	}
	if start >= len(links) {
		return SubjectCaseLinksResult{Items: []SubjectCaseLinkProjection{}, VisibleCount: 0}, nil
	}
	end := min(start+limit, len(links))
	var next *string
	if end < len(links) {
		cursorValue := base64.RawURLEncoding.EncodeToString([]byte(links[end-1].CaseID + "\x00" + links[end-1].BindingID))
		next = &cursorValue
	}
	page := append([]SubjectCaseLinkProjection(nil), links[start:end]...)
	return SubjectCaseLinksResult{Items: page, VisibleCount: len(page), NextCursor: next}, nil
}

func (s *Store) ListSubjects(ctx context.Context, user accounts.User, caseID, query, cursor string, limit int) (SubjectListResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectListResult{}, err
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return SubjectListResult{}, fault.Invalid("每页主体数量最多为100")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectListResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectListResult{}, err
	}
	if caseID != "" {
		item, err := s.visibleCase(&st, current, caseID)
		if err != nil {
			return SubjectListResult{}, err
		}
		if err := requireCaseAction(item, current, access.Manage); err != nil {
			return SubjectListResult{}, err
		}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	items := make([]SubjectSummary, 0)
	for _, subject := range st.Subjects {
		if subject.TenantID != current.TenantID {
			continue
		}
		version, ok := subjectVersionAt(st.SubjectVersions, subject.ID, subject.CurrentVersion)
		if !ok {
			continue
		}
		// Apply case authorization before search and paging, so those operations
		// never change based on subjects the caller cannot see.
		if !subjectVisibleToUser(st, current, subject.ID) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(version.DisplayName+" "+version.LegalName), query) {
			continue
		}
		// case_id is only the target operation context. It never widens the
		// subject set: candidates must already be visible through another
		// authorized binding or be the caller's never-bound pending subject.
		items = append(items, projectSubjectSummary(st, current, subject, version))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SubjectID < items[j].SubjectID })
	return paginateSubjectSummaries(items, cursor, limit), nil
}

func paginateSubjectSummaries(items []SubjectSummary, cursor string, limit int) SubjectListResult {
	start := 0
	if cursor != "" {
		start = sort.Search(len(items), func(i int) bool { return items[i].SubjectID > cursor })
	}
	end := min(start+limit, len(items))
	var next *string
	if end > start && end < len(items) {
		value := items[end-1].SubjectID
		next = &value
	}
	if start >= len(items) {
		return SubjectListResult{Items: []SubjectSummary{}, NextCursor: nil}
	}
	return SubjectListResult{Items: append([]SubjectSummary(nil), items[start:end]...), NextCursor: next}
}

func normalizeSubjectCreateInput(input SubjectCreateInput) (SubjectCreateInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 200 || len([]rune(input.LegalName)) > 200 || len(input.Identifiers) > 20 || len(input.Aliases) > 50 {
		return SubjectCreateInput{}, errors.New("主体名称或材料数量无效")
	}
	input.LegalName = strings.TrimSpace(input.LegalName)
	claims := make([]SubjectIdentityClaimInput, 0, len(input.Identifiers))
	for _, item := range input.Identifiers {
		claim, err := userAssertedClaim(item)
		if err != nil {
			return SubjectCreateInput{}, err
		}
		claims = append(claims, SubjectIdentityClaimInput{Namespace: claim.Namespace, IdentifierType: claim.IdentifierType, Value: claim.Value})
	}
	aliases := make([]SubjectAliasInput, 0, len(input.Aliases))
	for _, item := range input.Aliases {
		alias, err := userAssertedAlias(item)
		if err != nil {
			return SubjectCreateInput{}, err
		}
		aliases = append(aliases, SubjectAliasInput{Value: alias.Value, Kind: alias.Kind})
	}
	input.Identifiers, input.Aliases = claims, aliases
	return input, nil
}

func newSubjectAggregate(actor accounts.User, input SubjectCreateInput, now time.Time) (Subject, SubjectVersion, error) {
	subjectID, err := newID("sub_")
	if err != nil {
		return Subject{}, SubjectVersion{}, err
	}
	version := SubjectVersion{TenantID: actor.TenantID, SubjectID: subjectID, Version: 1, DisplayName: input.DisplayName, LegalName: input.LegalName,
		VerificationState: "pending_verification", SourceKind: "user_asserted", BasisSummary: "用户提交，身份待核验", Aliases: []SubjectAlias{},
		CreatedByUser: actor.ID, CreatedByIssuer: actor.Issuer, CreatedBySubject: actor.Subject, CreatedAt: now}
	for _, item := range input.Identifiers {
		claim, err := userAssertedClaim(item)
		if err != nil {
			return Subject{}, SubjectVersion{}, err
		}
		version.IdentityClaims = append(version.IdentityClaims, claim)
	}
	if version.IdentityClaims == nil {
		version.IdentityClaims = []SubjectIdentityClaim{}
	}
	for _, item := range input.Aliases {
		alias, err := userAssertedAlias(item)
		if err != nil {
			return Subject{}, SubjectVersion{}, err
		}
		version.Aliases = append(version.Aliases, alias)
	}
	version.PayloadHash = hashSubjectVersion(version)
	subject := Subject{TenantID: actor.TenantID, ID: subjectID, CurrentVersion: 1, Status: "pending_verification", CreatedByUser: actor.ID, CreatedByIssuer: actor.Issuer, CreatedBySubject: actor.Subject, CreatedAt: now}
	return subject, version, nil
}

func hashSubjectVersion(version SubjectVersion) string {
	version.PayloadHash = ""
	version.CreatedByUser, version.CreatedByIssuer, version.CreatedBySubject = "", "", ""
	version.CreatedAt = time.Time{}
	encoded, _ := json.Marshal(version)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 200 {
		return fault.Invalid("Idempotency-Key长度必须是1到200字符")
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return fault.Invalid("Idempotency-Key只能包含可见ASCII字符")
		}
	}
	return nil
}

func canonicalSubjectHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func readSubjectIdempotency(st state, actor accounts.User, operation, targetID, key, payloadHash string) (SubjectIdempotencyRecord, bool, error) {
	for _, item := range st.SubjectIdempotency {
		if item.TenantID != actor.TenantID || item.ActorUserID != actor.ID || item.Operation != operation || item.TargetID != targetID || item.Key != key {
			continue
		}
		if item.PayloadHash != payloadHash {
			return SubjectIdempotencyRecord{}, false, fault.Conflict("Idempotency-Key已用于不同请求内容")
		}
		return item, true, nil
	}
	return SubjectIdempotencyRecord{}, false, nil
}

func appendSubjectIdempotency(st *state, actor accounts.User, operation, targetID, key, payloadHash string, status int, response any, now time.Time) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	st.SubjectIdempotency = append(st.SubjectIdempotency, SubjectIdempotencyRecord{TenantID: actor.TenantID, ActorUserID: actor.ID, Operation: operation, TargetID: targetID, Key: key, PayloadHash: payloadHash, Response: body, HTTPStatus: status, CreatedAt: now})
	return nil
}

func subjectVisibleToUser(st state, user accounts.User, subjectID string) bool {
	bound := false
	for _, role := range st.CaseSubjectRoles {
		if role.TenantID != user.TenantID || !caseRoleCurrent(st, role).hasSubject(subjectID) {
			continue
		}
		for _, caseItem := range st.Cases {
			if caseItem.ID == role.CaseID && caseReadAllowed(caseItem, user) {
				bound = true
			}
		}
	}
	if bound {
		return true
	}
	for _, subject := range st.Subjects {
		if subject.TenantID == user.TenantID && subject.ID == subjectID && subject.Status == "pending_verification" && subject.CreatedByUser == user.ID {
			return !subjectHasAnyBinding(st, subjectID)
		}
	}
	return false
}

func subjectHasAnyBinding(st state, subjectID string) bool {
	for _, binding := range st.CaseSubjectRoleVersions {
		if binding.SubjectID == subjectID {
			return true
		}
	}
	return false
}

func visibleSubject(st state, user accounts.User, subjectID string) (Subject, SubjectVersion, bool) {
	if !subjectVisibleToUser(st, user, subjectID) {
		return Subject{}, SubjectVersion{}, false
	}
	for _, item := range st.Subjects {
		if item.TenantID == user.TenantID && item.ID == subjectID {
			version, ok := subjectVersionAt(st.SubjectVersions, subjectID, item.CurrentVersion)
			return item, version, ok
		}
	}
	return Subject{}, SubjectVersion{}, false
}

func subjectBinding(st state, caseID, subjectID string, requireCurrent bool) (CaseSubjectRoleVersion, bool) {
	var found CaseSubjectRoleVersion
	for _, item := range st.CaseSubjectRoleVersions {
		if item.CaseID != caseID || item.SubjectID != subjectID || item.BindingState == "inactive" {
			continue
		}
		if !requireCurrent {
			return item, true
		}
		for _, head := range st.CaseSubjectRoles {
			if head.CaseID == caseID && head.BindingID == item.BindingID && head.CurrentVersion == item.Version {
				found = item
				break
			}
		}
	}
	return found, found.BindingID != ""
}

func projectCaseSubjects(st state, user accounts.User, caseItem Case) CaseSubjectListProjection {
	items := make([]CaseSubjectBindingProjection, 0)
	for _, head := range st.CaseSubjectRoles {
		if head.TenantID != caseItem.TenantID || head.OrgID != caseItem.OwnerOrgID || head.CaseID != caseItem.ID {
			continue
		}
		var binding *CaseSubjectRoleVersion
		for i := range st.CaseSubjectRoleVersions {
			candidate := &st.CaseSubjectRoleVersions[i]
			if candidate.TenantID == head.TenantID && candidate.OrgID == head.OrgID && candidate.CaseID == head.CaseID && candidate.BindingID == head.BindingID && candidate.Version == head.CurrentVersion {
				binding = candidate
				break
			}
		}
		if binding == nil {
			continue
		}
		version, ok := subjectVersionAt(st.SubjectVersions, binding.SubjectID, binding.SubjectVersion)
		if !ok {
			continue
		}
		var label *string
		if binding.LegacyLabel != "" {
			value := binding.LegacyLabel
			label = &value
		}
		items = append(items, CaseSubjectBindingProjection{BindingID: head.BindingID, BindingVersion: head.CurrentVersion, CaseID: head.CaseID, Role: binding.Role,
			BindingState: binding.BindingState, SourceKind: binding.SourceKind, LegacyLabel: label, Subject: projectSubjectVersionForUser(st, user, version, false),
			CreatedBy: TrustedPrincipalProjection{UserID: binding.CreatedByUser, Issuer: binding.CreatedByIssuer, Subject: binding.CreatedBySubject}, CreatedAt: binding.CreatedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Role == items[j].Role {
			return items[i].BindingID < items[j].BindingID
		}
		return items[i].Role < items[j].Role
	})
	var legacy *LegacySubjectProfileProjection
	if profile, ok := profileFor(st, caseItem); ok {
		legacy = &LegacySubjectProfileProjection{Version: profile.Version, Customer: nullableSubjectName(profile.Customer), Supplier: nullableSubjectName(profile.Supplier)}
	}
	return CaseSubjectListProjection{Items: items, LegacyProfile: legacy}
}

func caseRoleCurrent(st state, head CaseSubjectRole) CaseSubjectRoleVersion {
	for _, item := range st.CaseSubjectRoleVersions {
		if item.TenantID == head.TenantID && item.OrgID == head.OrgID && item.CaseID == head.CaseID && item.BindingID == head.BindingID && item.Version == head.CurrentVersion {
			return item
		}
	}
	return CaseSubjectRoleVersion{}
}

func (v CaseSubjectRoleVersion) hasSubject(subjectID string) bool {
	return v.SubjectID == subjectID && v.BindingState != "inactive"
}

func visibleSubjectEvents(st state, user accounts.User, subjectID string) []SubjectChangeEventSummary {
	caseByID := make(map[string]Case, len(st.Cases))
	for _, item := range st.Cases {
		caseByID[item.ID] = item
	}
	requests := make(map[string]SubjectChangeRequest)
	events := make([]SubjectChangeEventSummary, 0)
	for _, request := range st.SubjectChangeRequests {
		targetsSubject := request.TargetKind == "subject" && request.TargetSubjectID == subjectID
		if request.TargetKind == "case_subject_role" {
			for _, version := range st.CaseSubjectRoleVersions {
				if version.BindingID == request.TargetBindingID && version.CaseID == request.SourceCaseID && version.SubjectID == subjectID {
					targetsSubject = true
					break
				}
			}
		}
		caseItem, caseExists := caseByID[request.SourceCaseID]
		if !targetsSubject || !caseExists || !caseReadAllowed(caseItem, user) {
			continue
		}
		requests[request.RequestID] = request
		events = append(events, SubjectChangeEventSummary{EventID: "proposal:" + request.RequestID, Kind: "proposal_created", RequestID: request.RequestID,
			CaseID: request.SourceCaseID, Actor: request.ProposedBy, CreatedAt: request.CreatedAt})
	}
	for _, decision := range st.SubjectChangeDecisions {
		request, ok := requests[decision.RequestID]
		if !ok {
			continue
		}
		kind := "decision_rejected"
		if decision.Decision == "approve" || decision.Decision == "approved" {
			kind = "decision_approved"
		}
		events = append(events, SubjectChangeEventSummary{EventID: "decision:" + decision.DecisionID, Kind: kind, RequestID: request.RequestID,
			CaseID: request.SourceCaseID, Actor: decision.Actor, CreatedAt: decision.CreatedAt})
		if kind == "decision_approved" && (decision.AppliedSubjectVersion != nil || decision.BindingVersionsAppended > 0) {
			events = append(events, SubjectChangeEventSummary{EventID: "applied:" + decision.DecisionID, Kind: "correction_applied", RequestID: request.RequestID,
				CaseID: request.SourceCaseID, Actor: decision.Actor, CreatedAt: decision.CreatedAt})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].EventID < events[j].EventID
		}
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})
	return events
}

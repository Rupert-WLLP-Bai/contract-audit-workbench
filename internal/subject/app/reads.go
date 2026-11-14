package app

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"

	"contractaudit/internal/fault"
	"contractaudit/internal/subject/model"
)

// ReadFacts contains only server-resolved facts needed to project Subject-owned
// state. Case records, actor objects, and Store state stay in the Store layer.
type ReadFacts struct {
	Cases                map[string]CaseFact
	ProposerDisplayNames map[string]string
	CanDecideRole        bool
	ReadOnly             bool
}

func decisionFactsForRead(facts ReadFacts, request model.SubjectChangeRequest) DecisionFacts {
	display := facts.ProposerDisplayNames[request.ProposedBy.UserID]
	return DecisionFacts{Cases: facts.Cases, CanDecideRole: facts.CanDecideRole, ProposerDisplayName: display}
}

func VisibleSubjectWithFacts(snapshot Snapshot, actor Actor, subjectID string, facts ReadFacts) (model.Subject, model.SubjectVersion, bool) {
	if !SubjectVisible(snapshot, actor, subjectID, facts) {
		return model.Subject{}, model.SubjectVersion{}, false
	}
	for _, head := range snapshot.Collections.Subjects {
		if head.TenantID == actor.TenantID && head.ID == subjectID {
			version, ok := model.SubjectVersionAt(snapshot.Collections.SubjectVersions, subjectID, head.CurrentVersion)
			return head, version, ok
		}
	}
	return model.Subject{}, model.SubjectVersion{}, false
}

func SubjectVisible(snapshot Snapshot, actor Actor, subjectID string, facts ReadFacts) bool {
	c := snapshot.Collections
	visible := false
	for _, head := range c.CaseSubjectRoles {
		if head.TenantID == actor.TenantID && model.CaseRoleCurrent(c.CaseSubjectRoleVersions, head).HasSubject(subjectID) {
			caseFact := facts.Cases[head.CaseID]
			if caseFact.Found && caseFact.Read {
				visible = true
				break
			}
		}
	}
	if !visible {
		for _, head := range c.Subjects {
			if head.TenantID == actor.TenantID && head.ID == subjectID && head.Status == "pending_verification" &&
				head.CreatedByUser == actor.Principal.UserID && !model.SubjectHasAnyBinding(c.CaseSubjectRoleVersions, subjectID) {
				visible = true
				break
			}
		}
	}
	if !visible {
		return false
	}
	return true
}

func ProjectSubjectVersionForActor(snapshot Snapshot, actor Actor, facts ReadFacts, version model.SubjectVersion, revealIdentifiers bool) model.SubjectVersionProjection {
	projected := model.ProjectSubjectVersion(version, revealIdentifiers)
	if version.SourceKind != "authorized_correction" || version.Version < 2 {
		return projected
	}
	for _, request := range snapshot.Requests {
		if request.TargetKind != "subject" || request.TargetSubjectID != version.SubjectID || request.ExpectedSubjectVersion+1 != version.Version {
			continue
		}
		summary := DecisionSummary(snapshot, actor, request, decisionFactsForRead(facts, request))
		if summary.State != "approved" {
			continue
		}
		source := facts.Cases[request.SourceCaseID]
		if source.Found && source.Read {
			return projected
		}
		projected.BasisSummary = "经审批更正"
		return projected
	}
	return projected
}

func ProjectSubjectSummary(snapshot Snapshot, actor Actor, facts ReadFacts, head model.Subject, version model.SubjectVersion) model.SubjectSummary {
	projected := ProjectSubjectVersionForActor(snapshot, actor, facts, version, false)
	return model.SubjectSummary{
		SubjectID: head.ID, CurrentVersion: head.CurrentVersion, DisplayName: version.DisplayName,
		LegalName: model.NullableSubjectName(version.LegalName), VerificationState: version.VerificationState,
		VerificationScope: version.VerificationScope, Aliases: projected.Aliases, Identifiers: projected.IdentityClaims,
		CreatedBy: model.TrustedPrincipalProjection{UserID: head.CreatedByUser, Issuer: head.CreatedByIssuer, Subject: head.CreatedBySubject},
		CreatedAt: head.CreatedAt,
	}
}

func ProjectCaseSubjects(snapshot Snapshot, actor Actor, facts ReadFacts, caseIdentity CaseIdentity, profile LegacyProfile) model.CaseSubjectListProjection {
	c := snapshot.Collections
	items := make([]model.CaseSubjectBindingProjection, 0)
	for _, head := range c.CaseSubjectRoles {
		if head.TenantID != caseIdentity.TenantID || head.OrgID != caseIdentity.OrgID || head.CaseID != caseIdentity.CaseID {
			continue
		}
		binding := model.CaseRoleCurrent(c.CaseSubjectRoleVersions, head)
		if binding.BindingID == "" || binding.Version != head.CurrentVersion || binding.TenantID != head.TenantID || binding.OrgID != head.OrgID || binding.CaseID != head.CaseID {
			continue
		}
		version, ok := model.SubjectVersionAt(c.SubjectVersions, binding.SubjectID, binding.SubjectVersion)
		if !ok {
			continue
		}
		items = append(items, model.ProjectSubjectBinding(c, head, binding, false))
		items[len(items)-1].Subject = ProjectSubjectVersionForActor(snapshot, actor, facts, version, false)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Role == items[j].Role {
			return items[i].BindingID < items[j].BindingID
		}
		return items[i].Role < items[j].Role
	})
	var legacy *model.LegacySubjectProfileProjection
	if profile.Found {
		legacy = &model.LegacySubjectProfileProjection{Version: profile.Version, Customer: model.NullableSubjectName(profile.Customer), Supplier: model.NullableSubjectName(profile.Supplier)}
	}
	return model.CaseSubjectListProjection{Items: items, LegacyProfile: legacy,
		CanManage: CaseCanManage(facts.Cases[caseIdentity.CaseID], facts.ReadOnly)}
}

func CaseCanManage(fact CaseFact, readOnly bool) bool {
	return !readOnly && fact.Found && fact.Manage && fact.Writable
}

func GetSubjectDetail(snapshot Snapshot, actor Actor, subjectID string, facts ReadFacts) (model.SubjectDetailResult, error) {
	head, version, visible := VisibleSubjectWithFacts(snapshot, actor, subjectID, facts)
	if !visible {
		return model.SubjectDetailResult{}, fault.NotFound("没有找到对应记录")
	}
	projected := ProjectSubjectVersionForActor(snapshot, actor, facts, version, false)
	return model.SubjectDetailResult{SubjectID: head.ID, CurrentVersion: head.CurrentVersion, Current: projected, Identifiers: projected.IdentityClaims}, nil
}

func GetSubjectHistory(snapshot Snapshot, actor Actor, subjectID string, facts ReadFacts) (model.SubjectHistoryResult, error) {
	head, _, visible := VisibleSubjectWithFacts(snapshot, actor, subjectID, facts)
	if !visible {
		return model.SubjectHistoryResult{}, fault.NotFound("没有找到对应记录")
	}
	versions := make([]model.SubjectVersion, 0)
	for _, version := range snapshot.Collections.SubjectVersions {
		if version.SubjectID == head.ID {
			versions = append(versions, version)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	projected := make([]model.SubjectVersionProjection, 0, len(versions))
	for _, version := range versions {
		projected = append(projected, ProjectSubjectVersionForActor(snapshot, actor, facts, version, false))
	}
	return model.SubjectHistoryResult{Versions: projected, Events: SubjectEvents(snapshot, subjectID, facts)}, nil
}

func SubjectEvents(snapshot Snapshot, subjectID string, facts ReadFacts) []model.SubjectChangeEventSummary {
	requests := make(map[string]model.SubjectChangeRequest)
	events := make([]model.SubjectChangeEventSummary, 0)
	for _, request := range snapshot.Requests {
		targetsSubject := request.TargetKind == "subject" && request.TargetSubjectID == subjectID
		if request.TargetKind == "case_subject_role" {
			for _, version := range snapshot.Collections.CaseSubjectRoleVersions {
				if version.BindingID == request.TargetBindingID && version.CaseID == request.SourceCaseID && version.SubjectID == subjectID {
					targetsSubject = true
					break
				}
			}
		}
		caseFact := facts.Cases[request.SourceCaseID]
		if !targetsSubject || !caseFact.Found || !caseFact.Read {
			continue
		}
		requests[request.RequestID] = request
		events = append(events, model.SubjectChangeEventSummary{EventID: "proposal:" + request.RequestID, Kind: "proposal_created", RequestID: request.RequestID,
			CaseID: request.SourceCaseID, Actor: request.ProposedBy, CreatedAt: request.CreatedAt})
	}
	for _, decision := range snapshot.Decisions {
		request, ok := requests[decision.RequestID]
		if !ok {
			continue
		}
		kind := "decision_rejected"
		if decision.Decision == "approve" || decision.Decision == "approved" {
			kind = "decision_approved"
		}
		events = append(events, model.SubjectChangeEventSummary{EventID: "decision:" + decision.DecisionID, Kind: kind, RequestID: request.RequestID,
			CaseID: request.SourceCaseID, Actor: decision.Actor, CreatedAt: decision.CreatedAt})
		if kind == "decision_approved" && (decision.AppliedSubjectVersion != nil || decision.BindingVersionsAppended > 0) {
			events = append(events, model.SubjectChangeEventSummary{EventID: "applied:" + decision.DecisionID, Kind: "correction_applied", RequestID: request.RequestID,
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

func ListSubjectChanges(snapshot Snapshot, actor Actor, caseID string, facts ReadFacts, cursor string, limit int) (model.SubjectChangeListResult, error) {
	items := make([]model.SubjectChangeSummary, 0)
	for _, request := range snapshot.Requests {
		if request.TenantID != actor.TenantID || request.SourceCaseID != caseID {
			continue
		}
		items = append(items, DecisionSummary(snapshot, actor, request, decisionFactsForRead(facts, request)))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RequestID < items[j].RequestID })
	return PaginateSubjectChanges(items, cursor, limit)
}

func PaginateSubjectChanges(items []model.SubjectChangeSummary, cursor string, limit int) (model.SubjectChangeListResult, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return model.SubjectChangeListResult{}, fault.Invalid("每页主体更正最多为100")
	}
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return model.SubjectChangeListResult{}, fault.Invalid("主体更正游标无效")
		}
		start = sort.Search(len(items), func(i int) bool { return items[i].RequestID > string(decoded) })
	}
	if start >= len(items) {
		return model.SubjectChangeListResult{Items: []model.SubjectChangeSummary{}, NextCursor: nil}, nil
	}
	end := min(start+limit, len(items))
	var next *string
	if end < len(items) {
		value := base64.RawURLEncoding.EncodeToString([]byte(items[end-1].RequestID))
		next = &value
	}
	return model.SubjectChangeListResult{Items: append([]model.SubjectChangeSummary(nil), items[start:end]...), NextCursor: next}, nil
}

func GetSubjectChange(snapshot Snapshot, actor Actor, caseID, requestID string, facts ReadFacts) (model.SubjectChangeDetailResult, error) {
	for _, request := range snapshot.Requests {
		if request.TenantID != actor.TenantID || request.SourceCaseID != caseID || request.RequestID != requestID {
			continue
		}
		decisionFacts := decisionFactsForRead(facts, request)
		impact, err := DecisionImpact(snapshot, actor, request, decisionFacts)
		if err != nil {
			return model.SubjectChangeDetailResult{}, err
		}
		summary := DecisionSummary(snapshot, actor, request, decisionFacts)
		return model.SubjectChangeDetailResult{Summary: summary, Before: impact.Before, After: impact.After, BasisNote: request.BasisNote,
			BasisRefs: append(json.RawMessage(nil), request.BasisRefs...), ProposedByDisplayName: summary.ProposedByDisplayName,
			CanDecide: summary.CanDecide, DecisionBlockedReason: summary.DecisionBlockedReason}, nil
	}
	return model.SubjectChangeDetailResult{}, fault.NotFound("没有找到对应记录")
}

func NormalizeSubjectListLimit(limit int) (int, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return 0, fault.Invalid("每页主体数量最多为100")
	}
	return limit, nil
}

func ListSubjects(snapshot Snapshot, actor Actor, facts ReadFacts, query, cursor string, limit int) model.SubjectListResult {
	query = strings.ToLower(strings.TrimSpace(query))
	items := make([]model.SubjectSummary, 0)
	for _, head := range snapshot.Collections.Subjects {
		if head.TenantID != actor.TenantID {
			continue
		}
		version, ok := model.SubjectVersionAt(snapshot.Collections.SubjectVersions, head.ID, head.CurrentVersion)
		if !ok || !subjectVisibleToActor(snapshot, actor, head.ID, facts) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(version.DisplayName+" "+version.LegalName), query) {
			continue
		}
		items = append(items, ProjectSubjectSummary(snapshot, actor, facts, head, version))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SubjectID < items[j].SubjectID })
	return PaginateSubjectSummaries(items, cursor, limit)
}

func PaginateSubjectSummaries(items []model.SubjectSummary, cursor string, limit int) model.SubjectListResult {
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
		return model.SubjectListResult{Items: []model.SubjectSummary{}, NextCursor: nil}
	}
	return model.SubjectListResult{Items: append([]model.SubjectSummary(nil), items[start:end]...), NextCursor: next}
}

func subjectVisibleToActor(snapshot Snapshot, actor Actor, subjectID string, facts ReadFacts) bool {
	return SubjectVisible(snapshot, actor, subjectID, facts)
}

func ListSubjectCaseLinks(snapshot Snapshot, actor Actor, subjectID, cursor string, limit int, facts ReadFacts) (model.SubjectCaseLinksResult, error) {
	if _, _, visible := VisibleSubjectWithFacts(snapshot, actor, subjectID, facts); !visible {
		return model.SubjectCaseLinksResult{}, fault.NotFound("没有找到对应记录")
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return model.SubjectCaseLinksResult{}, fault.Invalid("每页案件关联最多为100")
	}
	links := make([]model.SubjectCaseLinkProjection, 0)
	for _, head := range snapshot.Collections.CaseSubjectRoles {
		if head.TenantID != actor.TenantID {
			continue
		}
		caseFact := facts.Cases[head.CaseID]
		if !caseFact.Found || caseFact.TenantID != head.TenantID || caseFact.OrgID != head.OrgID || !caseFact.Read {
			continue
		}
		binding := model.CaseRoleCurrent(snapshot.Collections.CaseSubjectRoleVersions, head)
		if binding.BindingID == "" || binding.Version != head.CurrentVersion || binding.TenantID != head.TenantID || binding.OrgID != head.OrgID || binding.CaseID != head.CaseID || binding.BindingState == "inactive" {
			continue
		}
		if binding.SubjectID != subjectID {
			continue
		}
		version, ok := model.SubjectVersionAt(snapshot.Collections.SubjectVersions, binding.SubjectID, binding.SubjectVersion)
		if !ok {
			continue
		}
		projection := model.ProjectSubjectBinding(snapshot.Collections, head, binding, false)
		projection.Subject = ProjectSubjectVersionForActor(snapshot, actor, facts, version, false)
		links = append(links, model.SubjectCaseLinkProjection{BindingID: projection.BindingID, BindingVersion: projection.BindingVersion,
			CaseID: projection.CaseID, CaseTitle: caseFact.Title, BusinessStatus: caseFact.Status, Role: projection.Role,
			BindingState: projection.BindingState, SourceKind: projection.SourceKind, LegacyLabel: projection.LegacyLabel,
			CreatedBy: projection.CreatedBy, Subject: projection.Subject, CreatedAt: projection.CreatedAt})
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
			return model.SubjectCaseLinksResult{}, fault.Invalid("案件关联游标无效")
		}
		start = sort.Search(len(links), func(i int) bool { return links[i].CaseID+"\x00"+links[i].BindingID > string(decoded) })
	}
	if start >= len(links) {
		return model.SubjectCaseLinksResult{Items: []model.SubjectCaseLinkProjection{}, VisibleCount: 0}, nil
	}
	end := min(start+limit, len(links))
	var next *string
	if end < len(links) {
		value := base64.RawURLEncoding.EncodeToString([]byte(links[end-1].CaseID + "\x00" + links[end-1].BindingID))
		next = &value
	}
	page := append([]model.SubjectCaseLinkProjection(nil), links[start:end]...)
	return model.SubjectCaseLinksResult{Items: page, VisibleCount: len(page), NextCursor: next}, nil
}

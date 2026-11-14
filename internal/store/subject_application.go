package store

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	subjectApp "contractaudit/internal/subject/app"
	subjectModel "contractaudit/internal/subject/model"
)

func subjectCommandActor(user accounts.User) subjectApp.Actor {
	return subjectApp.Actor{TenantID: user.TenantID, Principal: subjectModel.TrustedPrincipalProjection{UserID: user.ID, Issuer: user.Issuer, Subject: user.Subject}}
}
func subjectCommandSnapshot(st *state) subjectApp.Snapshot {
	return subjectApp.Snapshot{Collections: subjectModel.SubjectCollections{Subjects: st.Subjects, SubjectVersions: st.SubjectVersions, CaseSubjectRoles: st.CaseSubjectRoles, CaseSubjectRoleVersions: st.CaseSubjectRoleVersions}, Receipts: st.SubjectIdempotency, Sources: st.SubjectSourceSnapshots, Requests: st.SubjectChangeRequests, Decisions: st.SubjectChangeDecisions}
}
func applySubjectCommandDelta(st *state, delta subjectApp.Delta) {
	for _, update := range delta.SubjectHeads {
		for i := range st.Subjects {
			if st.Subjects[i] == update.Expected {
				st.Subjects[i] = update.Next
				break
			}
		}
	}
	for _, update := range delta.BindingHeads {
		for i := range st.CaseSubjectRoles {
			if st.CaseSubjectRoles[i] == update.Expected {
				st.CaseSubjectRoles[i] = update.Next
				break
			}
		}
	}
	st.Subjects = append(st.Subjects, delta.Subjects...)
	st.SubjectVersions = append(st.SubjectVersions, delta.Versions...)
	st.CaseSubjectRoles = append(st.CaseSubjectRoles, delta.Bindings...)
	st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, delta.BindingVersions...)
	st.SubjectIdempotency = append(st.SubjectIdempotency, delta.Receipts...)
	st.SubjectSourceSnapshots = append(st.SubjectSourceSnapshots, delta.Sources...)
	st.SubjectChangeRequests = append(st.SubjectChangeRequests, delta.Requests...)
	st.SubjectChangeDecisions = append(st.SubjectChangeDecisions, delta.Decisions...)
}
func subjectReadableBindingCases(st *state, user accounts.User) map[string]bool {
	result := make(map[string]bool)
	for _, head := range st.CaseSubjectRoles {
		if head.TenantID != user.TenantID {
			continue
		}
		for _, item := range st.Cases {
			if item.ID == head.CaseID && caseReadAllowed(item, user) {
				result[head.CaseID] = true
			}
		}
	}
	return result
}

func subjectCaseFact(item Case, user accounts.User) subjectApp.CaseFact {
	return subjectApp.CaseFact{Found: true, Read: caseReadAllowed(item, user), Manage: authorizeCase(item, user, access.Manage),
		Decide: authorizeCase(item, user, access.Decide), Writable: businessWritable(item) == nil,
		TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, Title: item.Title, Status: item.BusinessStatus}
}

// subjectReadFacts resolves only cases referenced by Subject bindings and
// change requests. The Subject app receives scalar authorization/presentation
// facts, never Store Case records or the surrounding state.
func subjectReadFacts(st *state, user accounts.User, readOnly bool, additional ...Case) subjectApp.ReadFacts {
	caseIDs := make(map[string]struct{})
	for _, head := range st.CaseSubjectRoles {
		if head.TenantID == user.TenantID {
			caseIDs[head.CaseID] = struct{}{}
		}
	}
	for _, request := range st.SubjectChangeRequests {
		if request.TenantID == user.TenantID {
			caseIDs[request.SourceCaseID] = struct{}{}
		}
	}
	for _, item := range additional {
		if item.TenantID == user.TenantID {
			caseIDs[item.ID] = struct{}{}
		}
	}
	cases := make(map[string]subjectApp.CaseFact, len(caseIDs))
	for id := range caseIDs {
		item, found := st.caseByID(id)
		if !found || item.TenantID != user.TenantID {
			cases[id] = subjectApp.CaseFact{}
			continue
		}
		cases[id] = subjectCaseFact(item, user)
	}
	displays := make(map[string]string)
	for _, request := range st.SubjectChangeRequests {
		if request.TenantID != user.TenantID {
			continue
		}
		id := request.ProposedBy.UserID
		if proposer, ok := accounts.ByID(id); ok && proposer.DisplayName != "" {
			displays[id] = proposer.DisplayName
		}
	}
	return subjectApp.ReadFacts{Cases: cases, ProposerDisplayNames: displays, CanDecideRole: accounts.CanDecide(user.Role), ReadOnly: readOnly}
}

type subjectCommandClock struct{}

func (subjectCommandClock) Now() time.Time                { return time.Now() }
func (subjectCommandClock) NewSubjectID() (string, error) { return newID("sub_") }
func (subjectCommandClock) NewBindingID() (string, error) { return newID("bind_") }

func (subjectCommandClock) NewSnapshotID() (string, error) { return newID("ssnap_") }
func (subjectCommandClock) NewRequestID() (string, error)  { return newID("sch_") }

type subjectProposalFacts struct {
	st       *state
	caseItem Case
	actor    accounts.User
}

func (f subjectProposalFacts) VerifyProposalBasis(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return validateSubjectBasisRefs(*f.st, &f.caseItem, raw)
}
func (f subjectProposalFacts) ProposalActorPresentation() (string, bool) {
	allowed := accounts.CanDecide(f.actor.Role)
	name := f.actor.ID
	if proposer, ok := accounts.ByID(f.actor.ID); ok && proposer.DisplayName != "" {
		name = proposer.DisplayName
	}
	return name, allowed
}

func (subjectCommandClock) NewDecisionID() (string, error) { return newID("schd_") }

func subjectDecisionFacts(st *state, user accounts.User, request SubjectChangeRequest) subjectApp.DecisionFacts {
	cases := make(map[string]subjectApp.CaseFact)
	collections := subjectCommandSnapshot(st).Collections
	for _, id := range subjectModel.AffectedSubjectCaseIDs(collections, request) {
		item, found := st.caseByID(id)
		if !found {
			cases[id] = subjectApp.CaseFact{}
			continue
		}
		cases[id] = subjectCaseFact(item, user)
	}
	display := request.ProposedBy.UserID
	if proposer, ok := accounts.ByID(request.ProposedBy.UserID); ok && proposer.DisplayName != "" {
		display = proposer.DisplayName
	}
	return subjectApp.DecisionFacts{Cases: cases, CanDecideRole: accounts.CanDecide(user.Role), ProposerDisplayName: display}
}

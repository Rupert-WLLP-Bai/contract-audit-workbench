package store

import (
	"context"
	subjectPostgres "contractaudit/internal/subject/postgres"
)

func subjectState(input subjectPostgres.Snapshot) state {
	var out state
	out.Subjects = input.Subjects
	out.SubjectVersions = input.SubjectVersions
	out.CaseSubjectRoles = input.CaseSubjectRoles
	out.CaseSubjectRoleVersions = input.CaseSubjectRoleVersions
	out.SubjectChangeRequests = input.SubjectChangeRequests
	out.SubjectChangeDecisions = input.SubjectChangeDecisions
	out.SubjectIdempotency = input.SubjectIdempotency
	out.SubjectSourceSnapshots = input.SubjectSourceSnapshots
	return out
}
func loadSubjectState(ctx context.Context, q queryer) (state, error) {
	value, err := subjectPostgres.LoadAll(ctx, q)
	return subjectState(value), err
}
func loadSubjectCaseState(ctx context.Context, q queryer, tenantID, orgID, caseID string) (state, error) {
	value, err := subjectPostgres.LoadCase(ctx, q, tenantID, orgID, caseID)
	return subjectState(value), err
}

func subjectPersistenceSnapshot(input state) subjectPostgres.Snapshot {
	return subjectPostgres.Snapshot{
		Subjects:                input.Subjects,
		SubjectVersions:         input.SubjectVersions,
		CaseSubjectRoles:        input.CaseSubjectRoles,
		CaseSubjectRoleVersions: input.CaseSubjectRoleVersions,
		SubjectChangeRequests:   input.SubjectChangeRequests,
		SubjectChangeDecisions:  input.SubjectChangeDecisions,
		SubjectIdempotency:      input.SubjectIdempotency,
		SubjectSourceSnapshots:  input.SubjectSourceSnapshots,
	}
}

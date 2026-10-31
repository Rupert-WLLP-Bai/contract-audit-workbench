package store

import (
	"context"
	"reflect"
	"strconv"
	"time"
)

// mergeProjection applies only committed rows. A newer local projection must not
// be replaced by a transaction that committed earlier but acquired mu later.
func mergeProjection[T any, K comparable](current, previous, removed, changed []T, key func(T) K) []T {
	if len(removed) == 0 && len(changed) == 0 {
		return current
	}
	before := make(map[K]T, len(previous))
	for _, item := range previous {
		before[key(item)] = item
	}
	for _, item := range removed {
		id := key(item)
		for i := range current {
			if key(current[i]) == id && reflect.DeepEqual(current[i], item) {
				current = append(current[:i], current[i+1:]...)
				break
			}
		}
	}
	for _, item := range changed {
		id := key(item)
		found := false
		for i := range current {
			if key(current[i]) != id {
				continue
			}
			found = true
			prior, existed := before[id]
			if existed && reflect.DeepEqual(current[i], prior) {
				current[i] = item
			}
			break
		}
		if !found {
			current = append(current, item)
		}
	}
	return current
}

func (s *Store) patchStateProjection(ctx context.Context, previous, removed, changed state) {
	if !s.mu.TryLock() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if s.mu.TryLock() {
				break
			}
		}
	}
	defer s.mu.Unlock()
	s.patchStateProjectionLocked(previous, removed, changed)
}

// Caller holds mu; PostgreSQL remains authoritative and other cases are untouched.
func (s *Store) patchStateProjectionLocked(previous, removed, changed state) {
	s.st.Sessions = mergeProjection(s.st.Sessions, previous.Sessions, removed.Sessions, changed.Sessions, func(v session) string { return v.Token })
	s.st.Cases = mergeProjection(s.st.Cases, previous.Cases, removed.Cases, changed.Cases, func(v Case) string { return v.ID })
	s.st.Documents = mergeProjection(s.st.Documents, previous.Documents, removed.Documents, changed.Documents, func(v Document) string { return v.ID })
	s.st.Materials = mergeProjection(s.st.Materials, previous.Materials, removed.Materials, changed.Materials, func(v MaterialEntity) [4]string { return [4]string{v.TenantID, v.OrgID, v.CaseID, v.ID} })
	s.st.MaterialVersions = mergeProjection(s.st.MaterialVersions, previous.MaterialVersions, removed.MaterialVersions, changed.MaterialVersions, func(v MaterialVersion) [5]string { return [5]string{v.TenantID, v.OrgID, v.CaseID, v.MaterialID, strconv.Itoa(v.Version)} })
	s.st.MaterialChains = mergeProjection(s.st.MaterialChains, previous.MaterialChains, removed.MaterialChains, changed.MaterialChains, func(v CaseMaterialChain) [3]string { return [3]string{v.TenantID, v.OrgID, v.CaseID} })
	s.st.UploadIdempotency = mergeProjection(s.st.UploadIdempotency, previous.UploadIdempotency, removed.UploadIdempotency, changed.UploadIdempotency, func(v UploadIdempotencyRecord) [5]string { return [5]string{v.TenantID, v.OrgID, v.CaseID, v.ActorID, v.Key} })
	s.st.ParsedDocuments = mergeProjection(s.st.ParsedDocuments, previous.ParsedDocuments, removed.ParsedDocuments, changed.ParsedDocuments, func(v ParsedDocument) string { return v.ID })
	s.st.Runs = mergeProjection(s.st.Runs, previous.Runs, removed.Runs, changed.Runs, func(v Run) string { return v.ID })
	s.st.Attempts = mergeProjection(s.st.Attempts, previous.Attempts, removed.Attempts, changed.Attempts, func(v Attempt) string { return v.ID })
	s.st.AttemptInputs = mergeProjection(s.st.AttemptInputs, previous.AttemptInputs, removed.AttemptInputs, changed.AttemptInputs, func(v AttemptInput) string { return v.AttemptID })
	s.st.AttemptParseSelections = mergeProjection(s.st.AttemptParseSelections, previous.AttemptParseSelections, removed.AttemptParseSelections, changed.AttemptParseSelections, func(v AttemptParseSelection) itemIdentity { return itemIdentity{Parent: v.AttemptID, ID: v.DocumentID} })
	s.st.ExecutionEvents = mergeProjection(s.st.ExecutionEvents, previous.ExecutionEvents, removed.ExecutionEvents, changed.ExecutionEvents, func(v ExecutionEvent) childSequence { return childSequence{Parent: v.AttemptID, Seq: v.Seq} })
	s.st.Findings = mergeProjection(s.st.Findings, previous.Findings, removed.Findings, changed.Findings, func(v Finding) string { return v.ID })
	s.st.Evaluations = mergeProjection(s.st.Evaluations, previous.Evaluations, removed.Evaluations, changed.Evaluations, func(v ConstraintEval) string { return v.ID })
	s.st.Decisions = mergeProjection(s.st.Decisions, previous.Decisions, removed.Decisions, changed.Decisions, func(v Decision) string { return v.ID })
	s.st.Events = mergeProjection(s.st.Events, previous.Events, removed.Events, changed.Events, func(v Event) childSequence { return childSequence{Parent: v.RunID, Seq: v.Seq} })
	s.st.Activity = mergeProjection(s.st.Activity, previous.Activity, removed.Activity, changed.Activity, func(v Activity) string { return v.ID })
	s.st.Idempotency = mergeProjection(s.st.Idempotency, previous.Idempotency, removed.Idempotency, changed.Idempotency, func(v Idem) itemIdentity { return itemIdentity{Parent: v.ActorID, ID: v.Key} })
	s.st.Snapshots = mergeProjection(s.st.Snapshots, previous.Snapshots, removed.Snapshots, changed.Snapshots, func(v RunSnapshot) string { return v.RunID })
	s.st.Audits = mergeProjection(s.st.Audits, previous.Audits, removed.Audits, changed.Audits, func(v AuditRecord) string { return v.EventID })
	s.st.Jobs = mergeProjection(s.st.Jobs, previous.Jobs, removed.Jobs, changed.Jobs, func(v Job) string { return v.ID })
	s.st.Outbox = mergeProjection(s.st.Outbox, previous.Outbox, removed.Outbox, changed.Outbox, func(v OutboxRecord) string { return v.ID })
	s.st.Generations = mergeProjection(s.st.Generations, previous.Generations, removed.Generations, changed.Generations, func(v generationRecord) string { return v.Record.ID })
	s.st.Remediations = mergeProjection(s.st.Remediations, previous.Remediations, removed.Remediations, changed.Remediations, func(v remediationRecord) string { return v.Record.ID })
	s.st.Reports = mergeProjection(s.st.Reports, previous.Reports, removed.Reports, changed.Reports, func(v frozenReport) string { return v.Record.ID })
	s.st.Archives = mergeProjection(s.st.Archives, previous.Archives, removed.Archives, changed.Archives, func(v archiveRecord) string { return v.Record.ID })
	s.st.Profiles = mergeProjection(s.st.Profiles, previous.Profiles, removed.Profiles, changed.Profiles, func(v caseProfileRecord) string { return v.Profile.CaseID })
	s.st.Tasks = mergeProjection(s.st.Tasks, previous.Tasks, removed.Tasks, changed.Tasks, func(v workbenchTaskRecord) string { return v.Task.ID })
	s.st.Relations = mergeProjection(s.st.Relations, previous.Relations, removed.Relations, changed.Relations, func(v caseRelationRecord) string { return v.Relation.ID })
	s.st.Knowledge = mergeProjection(s.st.Knowledge, previous.Knowledge, removed.Knowledge, changed.Knowledge, func(v knowledgeRecord) [3]string { return [3]string{v.TenantID, v.Item.ID, v.Item.Version} })
	s.st.WorkbenchAudit = mergeProjection(s.st.WorkbenchAudit, previous.WorkbenchAudit, removed.WorkbenchAudit, changed.WorkbenchAudit, func(v workbenchAuditRecord) string { return v.Item.ID })
	s.st.Subjects = mergeProjection(s.st.Subjects, previous.Subjects, removed.Subjects, changed.Subjects, func(v Subject) [2]string { return [2]string{v.TenantID, v.ID} })
	s.st.SubjectVersions = mergeProjection(s.st.SubjectVersions, previous.SubjectVersions, removed.SubjectVersions, changed.SubjectVersions, func(v SubjectVersion) [3]string { return [3]string{v.TenantID, v.SubjectID, strconv.FormatInt(v.Version, 10)} })
	s.st.CaseSubjectRoles = mergeProjection(s.st.CaseSubjectRoles, previous.CaseSubjectRoles, removed.CaseSubjectRoles, changed.CaseSubjectRoles, func(v CaseSubjectRole) [4]string { return [4]string{v.TenantID, v.OrgID, v.CaseID, v.BindingID} })
	s.st.CaseSubjectRoleVersions = mergeProjection(s.st.CaseSubjectRoleVersions, previous.CaseSubjectRoleVersions, removed.CaseSubjectRoleVersions, changed.CaseSubjectRoleVersions, func(v CaseSubjectRoleVersion) [5]string { return [5]string{v.TenantID, v.OrgID, v.CaseID, v.BindingID, strconv.FormatInt(v.Version, 10)} })
	s.st.SubjectChangeRequests = mergeProjection(s.st.SubjectChangeRequests, previous.SubjectChangeRequests, removed.SubjectChangeRequests, changed.SubjectChangeRequests, func(v SubjectChangeRequest) [2]string { return [2]string{v.TenantID, v.RequestID} })
	s.st.SubjectChangeDecisions = mergeProjection(s.st.SubjectChangeDecisions, previous.SubjectChangeDecisions, removed.SubjectChangeDecisions, changed.SubjectChangeDecisions, func(v SubjectChangeDecision) [2]string { return [2]string{v.TenantID, v.DecisionID} })
	s.st.SubjectIdempotency = mergeProjection(s.st.SubjectIdempotency, previous.SubjectIdempotency, removed.SubjectIdempotency, changed.SubjectIdempotency, func(v SubjectIdempotencyRecord) [5]string { return [5]string{v.TenantID, v.ActorUserID, v.Operation, v.TargetID, v.Key} })
	s.st.SubjectSourceSnapshots = mergeProjection(s.st.SubjectSourceSnapshots, previous.SubjectSourceSnapshots, removed.SubjectSourceSnapshots, changed.SubjectSourceSnapshots, func(v SubjectSourceSnapshot) [4]string { return [4]string{v.TenantID, v.OrgID, v.CaseID, v.SnapshotID} })
}

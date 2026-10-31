package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

type dbScope struct {
	ActorID      string
	TenantID     string
	OrgID        string
	CaseID       string
	Role         string
	SessionToken string
	WorkerID     string
	JobID        string
	Generation   int64
}

func setLocalScope(ctx context.Context, tx pgx.Tx, scope dbScope) error {
	if scope.ActorID == "" {
		scope.ActorID = "system"
	}
	if scope.TenantID == "" {
		scope.TenantID = "local-demo"
	}
	if scope.OrgID == "" {
		scope.OrgID = "demo-org"
	}
	if scope.Role == "" {
		scope.Role = "api"
	}
	_, err := tx.Exec(ctx, `SELECT
		set_config('app.actor_id', $1, true),
		set_config('app.tenant_id', $2, true),
		set_config('app.org_id', $3, true),
		set_config('app.actor_role', $4, true),
		set_config('app.session_token', $5, true),
		set_config('app.worker_id', $6, true),
		set_config('app.job_id', $7, true),
		set_config('app.job_generation', $8, true)`,
		scope.ActorID, scope.TenantID, scope.OrgID, scope.Role, scope.SessionToken, scope.WorkerID, scope.JobID, strconv.FormatInt(scope.Generation, 10))
	return err
}

// scopedCaseLock serialises every writer of one case without touching rows the
// caller does not own: revocation, archive and case-scoped business writes all
// take this key, so ordering is preserved even when the writer may not lock the
// case row itself (no cross-owner UPDATE privilege needed). Callers take the
// scheme-wide lock first when they still need it, then this key, then the
// attempt row and the claim row.
func scopedCaseLock(ctx context.Context, tx pgx.Tx, caseID string) error {
	if strings.TrimSpace(caseID) == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':case:' || $1, 0))`, caseID)
	return err
}

func scopedSubjectLocks(ctx context.Context, tx pgx.Tx, subjectIDs []string) error {
	ids := append([]string(nil), subjectIDs...)
	sort.Strings(ids)
	previous := ""
	for _, subjectID := range ids {
		if subjectID == "" || subjectID == previous {
			continue
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':subject:' || $1, 0))`, subjectID); err != nil {
			return err
		}
		previous = subjectID
	}
	return nil
}

func scopedCaseLocks(ctx context.Context, tx pgx.Tx, caseIDs []string) error {
	ids := append([]string(nil), caseIDs...)
	sort.Strings(ids)
	previous := ""
	for _, caseID := range ids {
		if caseID == "" || caseID == previous {
			continue
		}
		if err := scopedCaseLock(ctx, tx, caseID); err != nil {
			return err
		}
		previous = caseID
	}
	return nil
}

// scopedAttemptLock serialises writers of one attempt without the global schema
// lock. Every attempt-owned writer (narrow appends and the whole-state writer
// when it targets an attempt) takes the same key, so the two lock classes cannot
// interleave on the same attempt while unrelated attempts and business flows
// stay independent. The scheme-wide advisory lock, where still taken, is always
// acquired before this one, so the ordering cannot deadlock.
func scopedAttemptLock(ctx context.Context, tx pgx.Tx, attemptID string) error {
	if strings.TrimSpace(attemptID) == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':attempt:' || $1, 0))`, attemptID)
	return err
}

func validateAppendOnly(previous, next state) error {
	if err := validateAttemptRulePackHistory(previous.Attempts, next.Attempts); err != nil {
		return err
	}
	if err := validateAttemptPlannerHistory(previous.Attempts, next.Attempts); err != nil {
		return err
	}
	if err := validateFindingRuleIdentity(previous.Findings, next.Findings); err != nil {
		return err
	}
	if err := requireAppendOnly("parsed documents", previous.ParsedDocuments, next.ParsedDocuments); err != nil {
		return err
	}
	if err := requireAppendOnly("material versions", previous.MaterialVersions, next.MaterialVersions); err != nil {
		return err
	}
	if err := requireAppendOnly("subject versions", previous.SubjectVersions, next.SubjectVersions); err != nil {
		return err
	}
	if err := requireAppendOnly("case subject role versions", previous.CaseSubjectRoleVersions, next.CaseSubjectRoleVersions); err != nil {
		return err
	}
	if err := requireAppendOnly("subject change requests", previous.SubjectChangeRequests, next.SubjectChangeRequests); err != nil {
		return err
	}
	if err := requireAppendOnly("subject change decisions", previous.SubjectChangeDecisions, next.SubjectChangeDecisions); err != nil {
		return err
	}
	if err := requireAppendOnly("subject idempotency records", previous.SubjectIdempotency, next.SubjectIdempotency); err != nil {
		return err
	}
	if err := requireAppendOnly("subject source snapshots", previous.SubjectSourceSnapshots, next.SubjectSourceSnapshots); err != nil {
		return err
	}
	if err := requireAppendOnly("document upload idempotency records", previous.UploadIdempotency, next.UploadIdempotency); err != nil {
		return err
	}
	if err := requireAppendOnly("attempt inputs", previous.AttemptInputs, next.AttemptInputs); err != nil {
		return err
	}
	if err := requireAppendOnly("parse selections", previous.AttemptParseSelections, next.AttemptParseSelections); err != nil {
		return err
	}
	if err := requireAppendOnly("execution events", previous.ExecutionEvents, next.ExecutionEvents); err != nil {
		return err
	}
	if err := requireAppendOnly("evaluations", previous.Evaluations, next.Evaluations); err != nil {
		return err
	}
	if err := requireAppendOnly("decisions", previous.Decisions, next.Decisions); err != nil {
		return err
	}
	if err := requireAppendOnly("run events", previous.Events, next.Events); err != nil {
		return err
	}
	if err := requireAppendOnly("activity", previous.Activity, next.Activity); err != nil {
		return err
	}
	if err := requireAppendOnly("idempotency records", previous.Idempotency, next.Idempotency); err != nil {
		return err
	}
	if err := requireAppendOnly("run snapshots", previous.Snapshots, next.Snapshots); err != nil {
		return err
	}
	if err := requireAppendOnly("outbox records", previous.Outbox, next.Outbox); err != nil {
		return err
	}
	if err := requireAppendOnly("generation records", previous.Generations, next.Generations); err != nil {
		return err
	}
	if err := validateRemediationHistory(previous.Remediations, next.Remediations); err != nil {
		return err
	}
	if err := requireAppendOnly("reports", previous.Reports, next.Reports); err != nil {
		return err
	}
	if err := requireAppendOnly("archives", previous.Archives, next.Archives); err != nil {
		return err
	}
	if err := validateWorkbenchHistory(previous, next); err != nil {
		return err
	}
	if err := requireAppendOnly("workbench audit records", previous.WorkbenchAudit, next.WorkbenchAudit); err != nil {
		return err
	}
	if err := requireAppendOnly("knowledge versions", previous.Knowledge, next.Knowledge); err != nil {
		return err
	}
	return requireAppendOnly("audit records", previous.Audits, next.Audits)
}

func validateRemediationHistory(previous, next []remediationRecord) error {
	before := make(map[string]remediationRecord, len(previous))
	for _, item := range previous {
		before[item.Record.ID] = item
	}
	seen := make(map[string]struct{}, len(next))
	for _, item := range next {
		id := item.Record.ID
		seen[id] = struct{}{}
		if id == "" || len(item.History) == 0 || !reflect.DeepEqual(item.History[len(item.History)-1].Record, item.Record) {
			return errors.New("remediation history must end at its current immutable revision")
		}
		prior, exists := before[id]
		if !exists {
			continue
		}
		if item.TenantID != prior.TenantID || item.OrgID != prior.OrgID || item.Record.CaseID != prior.Record.CaseID {
			return errors.New("remediation tenant, organization, and case identity are immutable")
		}
		if !appendOnlyEqual(prior.History, item.History) {
			return errors.New("remediation revision history is append-only")
		}
		if !reflect.DeepEqual(prior.Record, item.Record) && len(item.History) == len(prior.History) {
			return errors.New("remediation projection changed without an appended revision")
		}
	}
	for _, prior := range previous {
		if _, exists := seen[prior.Record.ID]; !exists {
			return errors.New("stored remediation record was removed")
		}
	}
	return nil
}

func requireAppendOnly[T any](name string, previous, next []T) error {
	if !appendOnlyEqual(previous, next) {
		return errors.New("append-only store data was rewritten: " + name)
	}
	return nil
}

func validateAttemptRulePackHistory(previous, next []Attempt) error {
	before := make(map[string]Attempt, len(previous))
	for _, attempt := range previous {
		before[attempt.ID] = attempt
	}
	for _, current := range next {
		prior, existed := before[current.ID]
		if existed && prior.ActorID != current.ActorID {
			return errors.New("attempt initiating actor is immutable")
		}
		priorSet := len(prior.RulePackSnapshot) != 0 || prior.RulePackHash != "" || prior.RulePackSnapshotDigest != "" || prior.RulePackCompilerRef != ""
		currentSet := len(current.RulePackSnapshot) != 0 || current.RulePackHash != "" || current.RulePackSnapshotDigest != "" || current.RulePackCompilerRef != ""
		if priorSet {
			if !bytes.Equal(prior.RulePackSnapshot, current.RulePackSnapshot) || prior.RulePackHash != current.RulePackHash || prior.RulePackSnapshotDigest != current.RulePackSnapshotDigest || prior.RulePackCompilerRef != current.RulePackCompilerRef {
				return errors.New("attempt RulePack snapshot is immutable once frozen")
			}
			continue
		}
		if !currentSet {
			continue
		}
		if len(current.RulePackSnapshot) == 0 && current.RulePackHash == "" && current.RulePackSnapshotDigest == "" && strings.TrimSpace(current.RulePackCompilerRef) != "" {
			if !attemptActive(current.State) || (existed && !attemptActive(prior.State)) {
				return errors.New("terminal attempt cannot acquire compiler identity")
			}
			continue
		}
		if len(current.RulePackSnapshot) == 0 || current.RulePackHash == "" || current.RulePackSnapshotDigest == "" || strings.TrimSpace(current.RulePackCompilerRef) == "" || !json.Valid(current.RulePackSnapshot) {
			return errors.New("attempt RulePack snapshot identity is incomplete")
		}
		snapshotDigest := sha256.Sum256(current.RulePackSnapshot)
		if hex.EncodeToString(snapshotDigest[:]) != current.RulePackSnapshotDigest {
			return errors.New("attempt RulePack snapshot digest does not match its bytes")
		}
		pack, err := rulepack.Parse(current.RulePackSnapshot)
		if err != nil || pack.Hash() != current.RulePackHash || pack.Manifest().Lifecycle.State != rulepack.StatePublished {
			return errors.New("attempt RulePack content hash or published snapshot is invalid")
		}
		if current.State != "accepted" && current.State != "running" {
			return errors.New("attempt RulePack snapshot must be frozen while active")
		}
		if existed && prior.State != "accepted" && prior.State != "running" {
			return errors.New("terminal attempt cannot acquire a RulePack snapshot")
		}
	}
	return nil
}

func validateAttemptPlannerHistory(previous, next []Attempt) error {
	before := make(map[string]Attempt, len(previous))
	for _, attempt := range previous {
		before[attempt.ID] = attempt
	}
	for _, current := range next {
		prior, existed := before[current.ID]
		if existed {
			if prior.PlannerConfigurationID != current.PlannerConfigurationID ||
				!bytes.Equal(prior.PlannerConfiguration, current.PlannerConfiguration) {
				return errors.New("attempt planner configuration is immutable once accepted")
			}
			continue
		}
		if current.PlannerConfigurationID == "" && len(current.PlannerConfiguration) == 0 {
			continue
		}
		if !attemptActive(current.State) {
			return errors.New("planner configuration must be frozen at acceptance")
		}
		if _, err := FrozenPlannerConfiguration(current); err != nil {
			return err
		}
	}
	return nil
}

func validateFindingRuleIdentity(previous, next []Finding) error {
	before := make(map[string]Finding, len(previous))
	for _, finding := range previous {
		before[finding.ID] = finding
	}
	for _, finding := range next {
		prior, exists := before[finding.ID]
		priorSet := prior.RuleID != "" || prior.CapabilityID != "" || prior.ObjectRef != ""
		currentSet := finding.RuleID != "" || finding.CapabilityID != "" || finding.ObjectRef != ""
		if priorSet && (prior.RuleID != finding.RuleID || prior.CapabilityID != finding.CapabilityID || prior.ObjectRef != finding.ObjectRef) {
			return errors.New("finding RulePack provenance is immutable once set")
		}
		// ObjectRef cites the reviewed object and is legitimately absent when a rule
		// found no clause to cite, so only the rule and capability identity are required.
		if currentSet && (finding.RuleID == "" || finding.CapabilityID == "") {
			return errors.New("finding RulePack provenance is incomplete")
		}
		if !exists && finding.ID == "" {
			return errors.New("finding identity is required")
		}
	}
	return nil
}

type childSequence struct {
	Parent string
	Seq    int
}

type itemIdentity struct {
	Parent string
	ID     string
}

func deltaRows[T any, K comparable](previous, next []T, key func(T) K, allowRemoval bool) (removed, changed []T, err error) {
	before := make(map[K]T, len(previous))
	for _, item := range previous {
		before[key(item)] = item
	}
	after := make(map[K]struct{}, len(next))
	for _, item := range next {
		id := key(item)
		after[id] = struct{}{}
		prior, exists := before[id]
		if !exists || !reflect.DeepEqual(prior, item) {
			changed = append(changed, item)
		}
	}
	for _, item := range previous {
		if _, exists := after[key(item)]; exists {
			continue
		}
		if !allowRemoval {
			return nil, nil, errors.New("stored entity was removed")
		}
		removed = append(removed, item)
	}
	return removed, changed, nil
}
func deltaRowsBy[T any, K comparable](previous, next []T, key func(T) K, equal func(T, T) bool,
	allowRemoval bool) (removed, changed []T, err error) {
	before := make(map[K]T, len(previous))
	for _, item := range previous {
		before[key(item)] = item
	}
	after := make(map[K]struct{}, len(next))
	for _, item := range next {
		id := key(item)
		after[id] = struct{}{}
		prior, exists := before[id]
		if !exists || !equal(prior, item) {
			changed = append(changed, item)
		}
	}
	for _, item := range previous {
		if _, exists := after[key(item)]; exists {
			continue
		}
		if !allowRemoval {
			return nil, nil, errors.New("stored entity was removed")
		}
		removed = append(removed, item)
	}
	return removed, changed, nil
}

// sameOptionalTime compares nullable PostgreSQL instants without treating a
// JSON clone's different time.Location pointer as a persisted row change.
func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func sameJob(left, right Job) bool {
	if !sameOptionalTime(left.LeaseUntil, right.LeaseUntil) || !sameOptionalTime(left.Heartbeat, right.Heartbeat) {
		return false
	}
	left.LeaseUntil, right.LeaseUntil = nil, nil
	left.Heartbeat, right.Heartbeat = nil, nil
	return reflect.DeepEqual(left, right)
}

func stateDelta(previous, next state) (state, state, error) {
	var old, changed state
	var err error
	if old.Sessions, changed.Sessions, err = deltaRows(previous.Sessions, next.Sessions, func(v session) string { return v.Token }, true); err != nil {
		return state{}, state{}, err
	}
	if old.Cases, changed.Cases, err = deltaRows(previous.Cases, next.Cases, func(v Case) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Documents, changed.Documents, err = deltaRows(previous.Documents, next.Documents, func(v Document) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Materials, changed.Materials, err = deltaRows(previous.Materials, next.Materials, func(v MaterialEntity) [4]string { return [4]string{v.TenantID, v.OrgID, v.CaseID, v.ID} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.MaterialVersions, changed.MaterialVersions, err = deltaRows(previous.MaterialVersions, next.MaterialVersions, func(v MaterialVersion) [5]string {
		return [5]string{v.TenantID, v.OrgID, v.CaseID, v.MaterialID, strconv.Itoa(v.Version)}
	}, false); err != nil {
		return state{}, state{}, err
	}
	if old.MaterialChains, changed.MaterialChains, err = deltaRows(previous.MaterialChains, next.MaterialChains, func(v CaseMaterialChain) [3]string { return [3]string{v.TenantID, v.OrgID, v.CaseID} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.UploadIdempotency, changed.UploadIdempotency, err = deltaRows(previous.UploadIdempotency, next.UploadIdempotency, func(v UploadIdempotencyRecord) [5]string {
		return [5]string{v.TenantID, v.OrgID, v.CaseID, v.ActorID, v.Key}
	}, false); err != nil {
		return state{}, state{}, err
	}
	if old.ParsedDocuments, changed.ParsedDocuments, err = deltaRows(previous.ParsedDocuments, next.ParsedDocuments, func(v ParsedDocument) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Runs, changed.Runs, err = deltaRows(previous.Runs, next.Runs, func(v Run) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Attempts, changed.Attempts, err = deltaRows(previous.Attempts, next.Attempts, func(v Attempt) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.AttemptInputs, changed.AttemptInputs, err = deltaRows(previous.AttemptInputs, next.AttemptInputs, func(v AttemptInput) string { return v.AttemptID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.AttemptParseSelections, changed.AttemptParseSelections, err = deltaRows(previous.AttemptParseSelections, next.AttemptParseSelections, func(v AttemptParseSelection) itemIdentity { return itemIdentity{Parent: v.AttemptID, ID: v.DocumentID} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.ExecutionEvents, changed.ExecutionEvents, err = deltaRows(previous.ExecutionEvents, next.ExecutionEvents, func(v ExecutionEvent) childSequence { return childSequence{Parent: v.AttemptID, Seq: v.Seq} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Findings, changed.Findings, err = deltaRows(previous.Findings, next.Findings, func(v Finding) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Evaluations, changed.Evaluations, err = deltaRows(previous.Evaluations, next.Evaluations, func(v ConstraintEval) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Decisions, changed.Decisions, err = deltaRows(previous.Decisions, next.Decisions, func(v Decision) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Events, changed.Events, err = deltaRows(previous.Events, next.Events, func(v Event) childSequence { return childSequence{Parent: v.RunID, Seq: v.Seq} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Activity, changed.Activity, err = deltaRows(previous.Activity, next.Activity, func(v Activity) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Idempotency, changed.Idempotency, err = deltaRows(previous.Idempotency, next.Idempotency, func(v Idem) itemIdentity { return itemIdentity{Parent: v.ActorID, ID: v.Key} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Snapshots, changed.Snapshots, err = deltaRows(previous.Snapshots, next.Snapshots, func(v RunSnapshot) string { return v.RunID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Audits, changed.Audits, err = deltaRows(previous.Audits, next.Audits, func(v AuditRecord) string { return v.EventID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Jobs, changed.Jobs, err = deltaRowsBy(previous.Jobs, next.Jobs, func(v Job) string { return v.ID }, sameJob, false); err != nil {
		return state{}, state{}, err
	}
	if old.Outbox, changed.Outbox, err = deltaRows(previous.Outbox, next.Outbox, func(v OutboxRecord) string { return v.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Generations, changed.Generations, err = deltaRows(previous.Generations, next.Generations, func(v generationRecord) string { return v.Record.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Remediations, changed.Remediations, err = deltaRows(previous.Remediations, next.Remediations, func(v remediationRecord) string { return v.Record.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Reports, changed.Reports, err = deltaRows(previous.Reports, next.Reports, func(v frozenReport) string { return v.Record.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Archives, changed.Archives, err = deltaRows(previous.Archives, next.Archives, func(v archiveRecord) string { return v.Record.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Profiles, changed.Profiles, err = deltaRows(previous.Profiles, next.Profiles, func(v caseProfileRecord) string { return v.Profile.CaseID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Tasks, changed.Tasks, err = deltaRows(previous.Tasks, next.Tasks, func(v workbenchTaskRecord) string { return v.Task.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Relations, changed.Relations, err = deltaRows(previous.Relations, next.Relations, func(v caseRelationRecord) string { return v.Relation.ID }, true); err != nil {
		return state{}, state{}, err
	}
	if old.Knowledge, changed.Knowledge, err = deltaRows(previous.Knowledge, next.Knowledge, func(v knowledgeRecord) [3]string { return [3]string{v.TenantID, v.Item.ID, v.Item.Version} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.WorkbenchAudit, changed.WorkbenchAudit, err = deltaRows(previous.WorkbenchAudit, next.WorkbenchAudit, func(v workbenchAuditRecord) string { return v.Item.ID }, false); err != nil {
		return state{}, state{}, err
	}
	if old.Subjects, changed.Subjects, err = deltaRows(previous.Subjects, next.Subjects, func(v Subject) [2]string { return [2]string{v.TenantID, v.ID} }, true); err != nil {
		return state{}, state{}, err
	}
	if old.SubjectVersions, changed.SubjectVersions, err = deltaRows(previous.SubjectVersions, next.SubjectVersions, func(v SubjectVersion) [3]string {
		return [3]string{v.TenantID, v.SubjectID, strconv.FormatInt(v.Version, 10)}
	}, false); err != nil {
		return state{}, state{}, err
	}
	if old.CaseSubjectRoles, changed.CaseSubjectRoles, err = deltaRows(previous.CaseSubjectRoles, next.CaseSubjectRoles, func(v CaseSubjectRole) [4]string { return [4]string{v.TenantID, v.OrgID, v.CaseID, v.BindingID} }, true); err != nil {
		return state{}, state{}, err
	}
	if old.CaseSubjectRoleVersions, changed.CaseSubjectRoleVersions, err = deltaRows(previous.CaseSubjectRoleVersions, next.CaseSubjectRoleVersions, func(v CaseSubjectRoleVersion) [5]string {
		return [5]string{v.TenantID, v.OrgID, v.CaseID, v.BindingID, strconv.FormatInt(v.Version, 10)}
	}, false); err != nil {
		return state{}, state{}, err
	}
	if old.SubjectChangeRequests, changed.SubjectChangeRequests, err = deltaRows(previous.SubjectChangeRequests, next.SubjectChangeRequests, func(v SubjectChangeRequest) [2]string { return [2]string{v.TenantID, v.RequestID} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.SubjectChangeDecisions, changed.SubjectChangeDecisions, err = deltaRows(previous.SubjectChangeDecisions, next.SubjectChangeDecisions, func(v SubjectChangeDecision) [2]string { return [2]string{v.TenantID, v.DecisionID} }, false); err != nil {
		return state{}, state{}, err
	}
	if old.SubjectIdempotency, changed.SubjectIdempotency, err = deltaRows(previous.SubjectIdempotency, next.SubjectIdempotency, func(v SubjectIdempotencyRecord) [5]string {
		return [5]string{v.TenantID, v.ActorUserID, v.Operation, v.TargetID, v.Key}
	}, false); err != nil {
		return state{}, state{}, err
	}
	if old.SubjectSourceSnapshots, changed.SubjectSourceSnapshots, err = deltaRows(previous.SubjectSourceSnapshots, next.SubjectSourceSnapshots, func(v SubjectSourceSnapshot) [4]string { return [4]string{v.TenantID, v.OrgID, v.CaseID, v.SnapshotID} }, false); err != nil {
		return state{}, state{}, err
	}
	return old, changed, nil
}

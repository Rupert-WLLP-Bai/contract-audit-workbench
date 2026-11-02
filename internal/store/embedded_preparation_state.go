package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"
	"unicode"

	"contractaudit/internal/config"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

// Preparation is restricted to the in-process development runtime and an exact
// no-job identity. Ticket issuance is deliberately separate from this writer.
func (s *Store) validEmbeddedPreparationScope(scope model.DelegatedScope, claim JobClaim) bool {
	return (s.sel.Mode == config.ModeDevelopment || s.sel.Mode == config.ModeTest) && s.serviceOwner == "" &&
		scope.Kind == "execution_prepare" && scope.Action == "embedded_prepare" && scope.OperationTicket != "" &&
		scope.TenantID != "" && scope.OrgID != "" && scope.CaseID != "" && scope.ActorID != "" && scope.RunID != "" && scope.AttemptID != "" &&
		scope.JobID == "" && scope.WorkerID == "" && scope.Generation == 0 && scope.DocumentID == "" && scope.InputSHA256 == "" && scope.ParseSelectionSHA256 == "" && scope.ParseSelectionID == "" && scope.ConfigurationID == "" && scope.ProviderURL == "" &&
		claim.JobID == "" && claim.WorkerID == "" && claim.Generation == 0 && scope.TenantID == claim.TenantID && scope.OrgID == claim.OrgID && scope.CaseID == claim.CaseID && scope.ActorID == claim.ActorID && scope.RunID == claim.RunID && scope.AttemptID == claim.AttemptID
}

func validateEmbeddedPreparationDelta(previous, next state, runID, attemptID string) error {
	reject := func() error { return errors.New("preparation state change is outside its authorized scope") }
	if len(previous.Attempts) != 1 || len(next.Attempts) != 1 || len(previous.Runs) != 1 || len(next.Runs) != 1 {
		return reject()
	}
	a, b := previous.Attempts[0], next.Attempts[0]
	if a.ID != attemptID || a.RunID != runID || b.ID != attemptID || b.RunID != runID {
		return reject()
	}
	if b.State != a.State && !(a.State == "accepted" && b.State == "running") && !(attemptActive(a.State) && (b.State == "failed" || b.State == "cancelled")) {
		return reject()
	}
	// Every other attempt field, including frozen output and all configuration,
	// stays subject to DeepEqual after only these individual fields are normalized.
	normalized := b
	normalized.State, normalized.StartedAt, normalized.FinishedAt, normalized.ErrorCode = a.State, a.StartedAt, a.FinishedAt, a.ErrorCode
	normalized.InputHash, normalized.HistoryKind = a.InputHash, a.HistoryKind
	if !reflect.DeepEqual(a, normalized) {
		return reject()
	}
	if a.StartedAt != nil && !reflect.DeepEqual(a.StartedAt, b.StartedAt) || a.FinishedAt != nil && !reflect.DeepEqual(a.FinishedAt, b.FinishedAt) || a.ErrorCode != nil && !reflect.DeepEqual(a.ErrorCode, b.ErrorCode) {
		return reject()
	}
	if b.State == a.State && (!reflect.DeepEqual(a.StartedAt, b.StartedAt) || !reflect.DeepEqual(a.FinishedAt, b.FinishedAt) || !reflect.DeepEqual(a.ErrorCode, b.ErrorCode)) {
		return reject()
	}
	if !preparationTime(b.StartedAt) || !preparationTime(b.FinishedAt) || !preparationCode(b.ErrorCode) {
		return reject()
	}
	if b.State == "running" && b.StartedAt == nil || (b.State == "failed" || b.State == "cancelled") && b.FinishedAt == nil {
		return reject()
	}
	r, nr := previous.Runs[0], next.Runs[0]
	if r.ID != runID || nr.ID != runID || nr.CaseID != r.CaseID || nr.State != b.State && nr.State != r.State {
		return reject()
	}
	if nr.PartialReason != r.PartialReason || (nr.FailureMessage != r.FailureMessage && !(b.State == "failed" && nr.FailureMessage == "这次运行未能完成。")) {
		return reject()
	}
	nr.State, nr.UpdatedAt, nr.PartialReason, nr.FailureMessage = r.State, r.UpdatedAt, r.PartialReason, r.FailureMessage
	if !reflect.DeepEqual(r, nr) {
		return reject()
	}
	if len(next.ParsedDocuments) < len(previous.ParsedDocuments) || !reflect.DeepEqual(previous.ParsedDocuments, next.ParsedDocuments[:len(previous.ParsedDocuments)]) {
		return reject()
	}
	seen := map[string]bool{}
	for _, p := range previous.ParsedDocuments {
		seen[p.ID] = true
	}
	for _, p := range next.ParsedDocuments[len(previous.ParsedDocuments):] {
		if seen[p.ID] {
			return reject()
		}
		seen[p.ID] = true
		matched := false
		for _, sel := range previous.AttemptParseSelections {
			if sel.AttemptID == attemptID && sel.RunID == runID && sel.ParsedDocumentID == p.ID && sel.DocumentID == p.DocumentID && sel.OriginalDigest == p.OriginalDigest && sel.SelectionOrder == p.SelectionOrder && sel.ParserID == p.ParserID && sel.ParserVersion == p.ParserVersion && sel.ConfigHash == p.ConfigHash && sel.NormalizationVersion == p.NormalizationVersion && (sel.ArtifactDigest == "" || sel.ArtifactDigest == p.ArtifactDigest) {
				matched = true
				break
			}
		}
		doc, ok := previous.documentByIdentity(p.DocumentID, p.OriginalDigest)
		if !matched || !ok || verifyParsed(p, doc) != nil {
			return reject()
		}
	}
	if len(previous.AttemptInputs) > 1 || len(next.AttemptInputs) > 1 || len(next.AttemptInputs) < len(previous.AttemptInputs) {
		return reject()
	}
	if len(previous.AttemptInputs) == 1 {
		if !reflect.DeepEqual(previous.AttemptInputs, next.AttemptInputs) || !reflect.DeepEqual(a.InputHash, b.InputHash) || a.HistoryKind != b.HistoryKind {
			return reject()
		}
	} else if len(next.AttemptInputs) == 1 {
		in := next.AttemptInputs[0]
		snapshot := previous.snapshotOf(runID)
		snapshot.DocumentIDs = append([]string(nil), snapshot.DocumentIDs...)
		snapshot.ParsedDocumentIDs = append([]string(nil), snapshot.ParsedDocumentIDs...)
		hash, err := frozenInputHash(snapshot, in.Documents)
		if err != nil || len(previous.AttemptParseSelections) == 0 || in.AttemptID != attemptID || in.RunID != runID || in.FrozenAt == "" || in.InputHash != hash || b.InputHash == nil || *b.InputHash != hash || a.InputHash != nil || a.HistoryKind != "preparing" || b.HistoryKind != "frozen" || !frozenInputMatchesSnapshot(snapshot, in.Documents) || !attemptSelectionsMatchFrozen(previous.AttemptParseSelections, in.Documents) {
			return reject()
		}
		for _, f := range in.Documents {
			p, ok := next.parsedByID(f.ParsedDocumentID)
			doc, docOK := previous.documentByIdentity(f.Document.ID, f.OriginalHash)
			if !ok || !docOK || verifyParsed(p, doc) != nil || !preparationFrozenMatches(previous, doc, p, f) {
				return reject()
			}
		}
	} else if !reflect.DeepEqual(a.InputHash, b.InputHash) || a.HistoryKind != b.HistoryKind {
		return reject()
	}
	if len(next.ExecutionEvents) < len(previous.ExecutionEvents) || !reflect.DeepEqual(previous.ExecutionEvents, next.ExecutionEvents[:len(previous.ExecutionEvents)]) {
		return reject()
	}
	for _, e := range next.ExecutionEvents[len(previous.ExecutionEvents):] {
		if e.AttemptID != attemptID || e.NodeKind != "attempt" || e.Trace != nil || e.Usage != nil || e.ModelRef != nil || e.Generation != nil || e.ParentSpanID != nil || (e.SpanID != nil && *e.SpanID != attemptID) || e.DurationMS != nil || !preparationCode(e.ErrorCode) || !preparationTime(&e.At) || !preparationTime(e.RecordedAt) || (e.EventKey != nil && *e.EventKey != "attempt/started") {
			return reject()
		}
		if !(e.EventType == "attempt_started" && e.Phase == "execution" && e.Status == "running" || e.EventType == "attempt_finished" && e.Phase == "terminal_persistence" && (e.Status == "failed" || e.Status == "cancelled") || e.EventType == "attempt_cancelled" && e.Phase == "cancellation" && e.Status == "cancelled") {
			return reject()
		}
	}
	if len(next.Events) < len(previous.Events) || !reflect.DeepEqual(previous.Events, next.Events[:len(previous.Events)]) {
		return reject()
	}
	for _, e := range next.Events[len(previous.Events):] {
		if e.RunID != runID || e.Type != "state" || e.State != b.State || (e.State != "running" && e.State != "failed" && e.State != "cancelled") {
			return reject()
		}
	}
	if len(next.Audits) < len(previous.Audits) || !reflect.DeepEqual(previous.Audits, next.Audits[:len(previous.Audits)]) {
		return reject()
	}
	for _, audit := range next.Audits[len(previous.Audits):] {
		if audit.RunID != runID || audit.Actor != "system" || !reflect.DeepEqual(audit.CorrelationIDs, []string{attemptID}) || !(audit.EventType == "attempt_input_frozen" && len(next.AttemptInputs) == 1 && len(previous.AttemptInputs) == 0 || audit.EventType == "attempt_preparation_"+b.State && (b.State == "failed" || b.State == "cancelled") && b.State != a.State) {
			return reject()
		}
	}
	// Default deny for every state slice not individually checked above.
	x, y := previous, next
	x.Attempts, y.Attempts = nil, nil
	x.Runs, y.Runs = nil, nil
	x.ParsedDocuments, y.ParsedDocuments = nil, nil
	x.AttemptInputs, y.AttemptInputs = nil, nil
	x.ExecutionEvents, y.ExecutionEvents = nil, nil
	x.Events, y.Events = nil, nil
	x.Audits, y.Audits = nil, nil
	if !reflect.DeepEqual(x, y) {
		return reject()
	}
	return nil
}

func preparationFrozenMatches(st state, doc Document, parsed ParsedDocument, frozen FrozenDocument) bool {
	expected := frozenFromParsed(doc, parsed)
	var selected *FrozenMaterialSelection
	for _, version := range st.MaterialVersions {
		if version.DocumentID != doc.ID {
			continue
		}
		if version.TenantID != doc.TenantID || version.OrgID != doc.OrgID || version.CaseID != doc.CaseID || selected != nil {
			return false
		}
		for _, material := range st.Materials {
			if material.TenantID == version.TenantID && material.OrgID == version.OrgID && material.CaseID == version.CaseID && material.ID == version.MaterialID {
				if selected != nil {
					return false
				}
				selected = &FrozenMaterialSelection{MaterialID: material.ID, Version: version.Version, Role: material.Role, Purpose: material.Purpose}
			}
		}
		if selected == nil {
			return false
		}
	}
	expected.Material = selected
	return reflect.DeepEqual(expected, frozen)
}

// finishEmbeddedPreparation records only a preparation failure or cancellation.
// It cannot create or replace an output hash or any review result.
func (s *Store) finishEmbeddedPreparation(ctx context.Context, runID, attemptID, stateName, errorCode string) error {
	scope, ok := model.DelegatedScopeFromContext(ctx)
	if !ok || scope.Kind != "execution_prepare" || scope.Action != "embedded_prepare" || scope.RunID != runID || scope.AttemptID != attemptID || (stateName != "failed" && stateName != "cancelled") || errorCode == "" || !preparationCode(&errorCode) {
		return errExecutionEventScope
	}
	return s.scopedAttemptMutation(ctx, runID, attemptID, func(st *state) error {
		return applyEmbeddedPreparationTerminal(st, runID, attemptID, stateName, errorCode)
	})
}

func applyEmbeddedPreparationTerminal(st *state, runID, attemptID, stateName, errorCode string) error {
	if (stateName != "failed" && stateName != "cancelled") || errorCode == "" || !preparationCode(&errorCode) {
		return errExecutionEventScope
	}
	if len(st.Attempts) != 1 || len(st.Runs) != 1 || st.Attempts[0].ID != attemptID || st.Runs[0].ID != runID || !attemptActive(st.Attempts[0].State) {
		return errAttemptTerminal
	}
	now := stamp()
	st.Attempts[0].State = stateName
	st.Attempts[0].FinishedAt = &now
	st.Attempts[0].ErrorCode = &errorCode
	st.Runs[0].State = stateName
	st.Runs[0].UpdatedAt = now
	if stateName == "failed" {
		st.Runs[0].FailureMessage = "这次运行未能完成。"
	}
	appendExecutionEvent(st, ExecutionEvent{AttemptID: attemptID, SpanID: stringPointer(attemptID), NodeKind: "attempt", Phase: "terminal_persistence", EventType: "attempt_finished", At: now, Status: stateName, ErrorCode: &errorCode})
	appendRunEvent(st, runID, stateName)
	return appendAudit(st, runID, "system", "attempt_preparation_"+stateName, []string{attemptID})
}

func preparationTime(value *string) bool {
	if value == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, *value)
	return err == nil
}
func preparationCode(value *string) bool {
	if value == nil {
		return true
	}
	if len(*value) == 0 || len(*value) > 64 {
		return false
	}
	for _, r := range *value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// loadPreparationMaterials reads only immutable selected document versions and
// their owning entities inside the already bound case; the chain is case-scoped.
func loadPreparationMaterials(ctx context.Context, tx pgx.Tx, st *state, claim JobClaim, documentIDs []string) error {
	rows, err := tx.Query(ctx, `SELECT m.tenant_id,m.org_id,m.case_id,m.material_id,m.role,m.purpose,m.legacy_status,m.legacy_selected,m.current_document_id,m.current_version,m.created_by,m.created_at,
 v.tenant_id,v.org_id,v.case_id,v.material_id,v.version,v.document_id,COALESCE(v.previous_document_id,''),v.created_by,v.created_at
 FROM material_versions v JOIN material_entities m ON m.tenant_id=v.tenant_id AND m.org_id=v.org_id AND m.case_id=v.case_id AND m.material_id=v.material_id
 WHERE v.tenant_id=$1 AND v.org_id=$2 AND v.case_id=$3 AND v.document_id=ANY($4::text[]) ORDER BY v.material_id,v.version`, claim.TenantID, claim.OrgID, claim.CaseID, documentIDs)
	if err != nil {
		return err
	}
	entities := map[string]bool{}
	for rows.Next() {
		var m MaterialEntity
		var v MaterialVersion
		if err := rows.Scan(&m.TenantID, &m.OrgID, &m.CaseID, &m.ID, &m.Role, &m.Purpose, &m.LegacyStatus, &m.LegacySelected, &m.CurrentDocumentID, &m.CurrentVersion, &m.CreatedBy, &m.CreatedAt, &v.TenantID, &v.OrgID, &v.CaseID, &v.MaterialID, &v.Version, &v.DocumentID, &v.PreviousDocument, &v.CreatedBy, &v.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		if !entities[m.ID] {
			st.Materials = append(st.Materials, m)
			entities[m.ID] = true
		}
		st.MaterialVersions = append(st.MaterialVersions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	chains, err := tx.Query(ctx, `SELECT tenant_id,org_id,case_id,primary_material_id,legacy_selected_document_id,legacy_selection_status,selection_version,selected_document_ids,created_at,updated_at FROM case_contract_chains WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`, claim.TenantID, claim.OrgID, claim.CaseID)
	if err != nil {
		return err
	}
	defer chains.Close()
	for chains.Next() {
		var c CaseMaterialChain
		var selected string
		if err := chains.Scan(&c.TenantID, &c.OrgID, &c.CaseID, &c.PrimaryMaterialID, &c.LegacySelectedDocumentID, &c.LegacySelectionStatus, &c.SelectionVersion, &selected, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(selected), &c.SelectedDocumentIDs); err != nil {
			return err
		}
		st.MaterialChains = append(st.MaterialChains, c)
	}
	return chains.Err()
}

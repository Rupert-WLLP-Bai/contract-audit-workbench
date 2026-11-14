// File metadata (manifest mirror):
// Purpose: Appends Operator evaluation, check and object records in the original transaction without owning scope or commit.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"

	"contractaudit/internal/operator/app"
)

func (p *Evaluation) AppendEvaluation(ctx context.Context, w app.EvaluationWrite) error {
	id, v := w.Identity, w.Value
	if w.Aggregate {
		_, err := p.tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluations (tenant_id,org_id,case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,contract_version_id,rule_pack_version,binding_version,snapshot_set_id,business_at,input_json,input_sha256,result_json,result_sha256,machine_status,created_by,actor_id,idempotency_key,request_sha256,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULL,$9,NULL,NULL,$10,$11,$12,$13,$14,$15,$16,$16,$17,$18,$19)`, id.TenantID, id.OrgID, id.CaseID, v.EvaluationID, v.PackID, v.PackVersion, v.SourceReviewID, v.SourceAttemptID, v.RulePackVersion, v.BusinessAt, w.InputJSON, w.RequestSHA, w.ResultJSON, v.ResultSHA256, v.MachineStatus, id.ActorID, w.Key, w.RequestSHA, v.CreatedAt)
		return err
	}
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluations (tenant_id,org_id,case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,contract_version_id,rule_pack_version,binding_version,snapshot_set_id,business_at,input_json,input_sha256,result_json,result_sha256,machine_status,created_by,actor_id,idempotency_key,request_sha256,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19,$20,$21,$22)`, id.TenantID, id.OrgID, id.CaseID, v.EvaluationID, v.PackID, v.PackVersion, v.SourceReviewID, v.SourceAttemptID, v.ContractVersionID, v.RulePackVersion, v.ObjectBindingVersion, v.SnapshotSetID, v.BusinessAt, w.InputJSON, w.RequestSHA, w.ResultJSON, v.ResultSHA256, v.MachineStatus, id.ActorID, w.Key, w.RequestSHA, v.CreatedAt)
	return err
}
func (p *Evaluation) AppendCheck(ctx context.Context, w app.EvaluationCheckWrite) error {
	id, v := w.Identity, w.Value
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_fulfillment_checks (tenant_id,org_id,case_id,evaluation_id,check_id,relation,reason_code,machine_status,requires_human_review,evidence,limitations,dependencies,result_json,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id.TenantID, id.OrgID, id.CaseID, w.EvaluationID, v.CheckID, string(v.Relation), v.ReasonCode, string(v.MachineStatus), v.RequiresHumanReview, w.Evidence, w.Limitations, w.Dependencies, w.Result, w.Now)
	return err
}
func (p *Evaluation) AppendObject(ctx context.Context, w app.EvaluationObjectWrite) error {
	id, v := w.Identity, w.Value
	var sourceRun, sourceAttempt, materialID, materialVersion, documentID, parsedID, purpose, role any
	if v.Source != nil {
		sourceRun, sourceAttempt = v.Source.RunID, v.Source.AttemptID
		materialID, materialVersion = v.Source.MaterialID, v.Source.MaterialVersion
		documentID, parsedID, purpose, role = v.Source.DocumentID, v.Source.ParsedDocumentID, v.Source.Purpose, v.Source.Role
	}
	var serviceID, objectVersion, associationVersion any
	if v.ServiceObjectID != nil && v.ObjectVersion != nil {
		serviceID = *v.ServiceObjectID
		objectVersion = *v.ObjectVersion
		if v.AssociationVersion != nil {
			associationVersion = *v.AssociationVersion
		}
	}
	var mappingID, mappingVersion, bindingVersion, snapshotID any
	if v.MappingID != nil {
		mappingID = *v.MappingID
		mappingVersion = *v.MappingVersion
		bindingVersion = *v.BindingVersion
		if v.SnapshotSetID != nil {
			snapshotID = *v.SnapshotSetID
		}
	}
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluation_objects (tenant_id,org_id,case_id,evaluation_id,ordinal,object_ref,source_run_id,source_attempt_id,material_id,material_version,document_id,parsed_document_id,purpose,source_role,facts_canonical_json,facts_sha256,service_object_id,object_version,association_version,mapping_id,mapping_version,binding_version,snapshot_set_id,status,reason_code,result_json,result_sha256,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,clock_timestamp())`, id.TenantID, id.OrgID, id.CaseID, w.EvaluationID, w.Ordinal, v.ObjectRef, sourceRun, sourceAttempt, materialID, materialVersion, documentID, parsedID, purpose, role, w.FactsJSON, w.FactsSHA, serviceID, objectVersion, associationVersion, mappingID, mappingVersion, bindingVersion, snapshotID, v.Status, v.ReasonCode, w.ResultJSON, w.ResultSHA)
	return err
}

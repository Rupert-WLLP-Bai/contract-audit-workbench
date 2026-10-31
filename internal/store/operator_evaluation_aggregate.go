package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// summarizeOperatorEvaluationObjects derives aggregate coverage from the exact
// persisted per-object rows. It never infers completeness from successful rows
// alone because callers must persist every candidate as evaluated, unresolved,
// insufficient or not_applicable.
func summarizeOperatorEvaluationObjects(scopeKind string, objects []OperatorEvaluationObject) OperatorEvaluationCoverage {
	coverage := OperatorEvaluationCoverage{ScopeKind: scopeKind, CandidateCount: len(objects)}
	if len(objects) == 0 {
		coverage.Status = "partial"
		coverage.ReasonCode = "no_candidates"
		return coverage
	}
	for _, object := range objects {
		switch object.Status {
		case "evaluated", "not_applicable":
			coverage.EvaluatedCount++
		case "unresolved":
			coverage.UnresolvedCount++
		case "insufficient":
			coverage.InsufficientCount++
		default:
			// Unknown persisted outcomes cannot be interpreted as complete.
			coverage.UnresolvedCount++
			if coverage.ReasonCode == "" {
				coverage.ReasonCode = "unknown_object_status"
			}
		}
	}
	coverage.Status = "complete"
	if coverage.UnresolvedCount > 0 || coverage.InsufficientCount > 0 {
		coverage.Status = "partial"
	}
	return coverage
}

type operatorEvaluationCandidate struct {
	ObjectRef       string
	ServiceObjectID string
	Source          FrozenDocument
	Reason          string
}

type operatorEvaluationObjectRecord struct {
	Result      OperatorEvaluationObject
	FactsJSON   []byte
	FactsSHA256 string
}

// createAggregateOperatorFulfillmentEvaluation is the explicit aggregate path.
// All object identities and evidence are read under the same case transaction;
// it never falls through to the legacy single-binding evaluator.
func (s *Store) createAggregateOperatorFulfillmentEvaluation(ctx context.Context, user, current accounts.User, st state,
	caseItem Case, input OperatorEvaluationInput, scope OperatorEvaluationScope, pack capabilitypack.Manifest,
	evaluator auditext.Evaluator, key string) (OperatorEvaluation, error) {
	run, attempt, attemptInput, _, err := s.validateFrozenTerminalAttempt(st, caseItem, input.SourceReviewID, input.SourceAttemptID)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if run.CaseID != caseItem.ID || attemptInput.RunID != input.SourceReviewID || attemptInput.AttemptID != input.SourceAttemptID {
		return OperatorEvaluation{}, fault.Conflict("来源审查不属于当前案件")
	}
	rulePackVersion := attempt.RulePackVersion
	if rulePackVersion == "" {
		rulePackVersion = pack.RulePackRef.Version
	}
	if rulePackVersion != pack.RulePackRef.Version ||
		(attempt.RulePackID != "" && attempt.RulePackID != pack.RulePackRef.ID) {
		return OperatorEvaluation{}, fault.Conflict("来源审查使用的规则包与能力包不一致")
	}
	if err := validateOperatorAggregateSources(attemptInput); err != nil {
		return OperatorEvaluation{}, fault.Conflict("冻结合同来源未通过完整性校验")
	}
	requestSHA, err := operatorHash(input)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	var out OperatorEvaluation
	err = s.withOperatorTx(ctx, user, caseItem.ID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorEvaluationByKey(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, current.ID, key)
		if err == nil {
			if existing.requestSHA256 != requestSHA {
				return fault.Conflict("同一幂等键对应了不同的核验请求")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		fresh, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&fresh, current, caseItem.ID)
		if err != nil || !authorizeCase(freshCase, current, access.Review) {
			return fault.Forbidden("当前授权不能核验该案件")
		}
		if err := businessWritable(freshCase); err != nil {
			return err
		}
		freshAttempt, freshInput, err := s.validateOperatorFrozenAttempt(ctx, tx, fresh, freshCase, input.SourceReviewID, input.SourceAttemptID)
		if err != nil {
			return err
		}
		if *freshAttempt.InputHash != *attempt.InputHash || *freshAttempt.OutputHash != *attempt.OutputHash {
			return fault.Conflict("来源审查的冻结输入或输出已变化")
		}
		if freshAttempt.RulePackID != "" && freshAttempt.RulePackID != pack.RulePackRef.ID {
			return fault.Conflict("来源审查使用的规则包与能力包不一致")
		}
		candidates := operatorEvaluationCandidates(fresh, freshInput, input.SourceReviewID, input.SourceAttemptID)
		supplementCandidates, err := operatorSupplementCandidates(freshCase, freshInput)
		if err != nil {
			return err
		}
		candidates = mergeSupplementCandidates(candidates, supplementCandidates)
		objects, err := loadOperatorServiceObjectsAt(ctx, tx, accounts.Tenant(current), freshCase.OwnerOrgID, caseItem.ID, input.BusinessAt, *input.EvidenceCutoffAt)
		if err != nil {
			return err
		}
		candidates = unionRegisteredServiceObjects(candidates, objects, scope)
		candidates = normalizeOperatorEvaluationCandidates(candidates)
		if len(candidates) > 100 {
			return fault.TooLarge("本次整案核验候选对象超过100个")
		}
		runSnapshot := fresh.snapshotOf(input.SourceReviewID)
		modelID, plannerID, parserID := runSnapshot.ModelConfigurationID, runSnapshot.PlannerConfigurationID, runSnapshot.ParserConfigurationID
		if modelID == "" {
			modelID = "not_used"
		}
		if plannerID == "" {
			plannerID = "not_used"
		}
		if parserID == "" {
			parserID = "not_used"
		}
		frozen := auditext.FrozenInput{
			Scope: auditext.AuthorizedScope{TenantID: accounts.Tenant(current), CaseID: caseItem.ID, ActorID: current.ID,
				GrantVersion: operatorGrantVersion(freshCase, current)},
			Pack: auditext.PackRef{ID: pack.PackID, Version: pack.Version}, RulePackVersion: rulePackVersion,
			ModelConfigurationID: modelID, PlannerConfigurationID: plannerID, ParserConfigurationID: parserID,
			BusinessAt: input.BusinessAt, EvidenceCutoffAt: input.EvidenceCutoffAt,
			EvaluationScope: &auditext.EvaluationScope{Kind: scope.Kind, ServiceObjectIDs: append([]string(nil), scope.ServiceObjectIDs...)},
			Facts:           []auditext.Fact{},
		}
		if err := frozen.Validate(); err != nil {
			return fault.Conflict("整案核验冻结输入不完整")
		}
		records, err := s.evaluateAggregateObjects(ctx, tx, current, freshCase, fresh, freshInput, input, scope, candidates, objects, pack, evaluator, frozen)
		if err != nil {
			return err
		}
		publicObjects := make([]OperatorEvaluationObject, 0, len(records))
		for _, record := range records {
			publicObjects = append(publicObjects, record.Result)
		}
		coverage := summarizeOperatorEvaluationObjects(scope.Kind, publicObjects)
		machineStatus := "completed"
		if coverage.Status != "complete" {
			machineStatus = "partial"
		}
		checks := []auditext.CheckResult{}
		checksJSON, err := json.Marshal(checks)
		if err != nil {
			return err
		}
		objectsJSON, err := json.Marshal(publicObjects)
		if err != nil {
			return err
		}
		resultSum := sha256.Sum256(objectsJSON)
		resultSHA := hex.EncodeToString(resultSum[:])
		inputJSON, err := json.Marshal(frozen)
		if err != nil {
			return err
		}
		evaluationID, err := newID("eval_")
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluations
			(tenant_id,org_id,case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,
			 contract_version_id,rule_pack_version,binding_version,snapshot_set_id,business_at,input_json,input_sha256,
			 result_json,result_sha256,machine_status,created_by,actor_id,idempotency_key,request_sha256,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULL,$9,NULL,NULL,$10,$11,$12,$13,$14,$15,$16,$16,$17,$18,$19)`,
			accounts.Tenant(current), freshCase.OwnerOrgID, caseItem.ID, evaluationID, pack.PackID, pack.Version,
			input.SourceReviewID, input.SourceAttemptID, rulePackVersion, input.BusinessAt, inputJSON, requestSHA,
			checksJSON, resultSHA, machineStatus, current.ID, key, requestSHA, now); err != nil {
			return err
		}
		for index, record := range records {
			resultJSON, err := json.Marshal(record.Result)
			if err != nil {
				return err
			}
			resultSum := sha256.Sum256(resultJSON)
			serviceID, objectVersion, associationVersion := nullableObjectTuple(record.Result)
			mappingID, mappingVersion, bindingVersion, snapshotID := nullableMappingTuple(record.Result)
			var sourceRun, sourceAttempt, materialID, materialVersion, documentID, parsedID, purpose, role any
			if record.Result.Source != nil {
				sourceRun, sourceAttempt = record.Result.Source.RunID, record.Result.Source.AttemptID
				materialID, materialVersion = record.Result.Source.MaterialID, record.Result.Source.MaterialVersion
				documentID, parsedID, purpose, role = record.Result.Source.DocumentID, record.Result.Source.ParsedDocumentID,
					record.Result.Source.Purpose, record.Result.Source.Role
			}
			if _, err := tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluation_objects
				(tenant_id,org_id,case_id,evaluation_id,ordinal,object_ref,source_run_id,source_attempt_id,material_id,material_version,
				 document_id,parsed_document_id,purpose,source_role,facts_canonical_json,facts_sha256,service_object_id,object_version,
				 association_version,mapping_id,mapping_version,binding_version,snapshot_set_id,status,reason_code,result_json,result_sha256,created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,clock_timestamp())`,
				accounts.Tenant(current), freshCase.OwnerOrgID, caseItem.ID, evaluationID, index+1, record.Result.ObjectRef,
				sourceRun, sourceAttempt, materialID, materialVersion, documentID, parsedID, purpose, role,
				record.FactsJSON, record.FactsSHA256, serviceID, objectVersion, associationVersion,
				mappingID, mappingVersion, bindingVersion, snapshotID, record.Result.Status, record.Result.ReasonCode,
				resultJSON, hex.EncodeToString(resultSum[:])); err != nil {
				return err
			}
		}
		out = OperatorEvaluation{CaseID: caseItem.ID, EvaluationID: evaluationID, PackID: pack.PackID, PackVersion: pack.Version,
			SourceReviewID: input.SourceReviewID, SourceAttemptID: input.SourceAttemptID, RulePackVersion: rulePackVersion,
			BusinessAt: input.BusinessAt.UTC(), EvidenceCutoffAt: input.EvidenceCutoffAt, MachineStatus: machineStatus,
			Checks: checks, CreatedAt: now, Input: frozen, ResultSHA256: resultSHA, requestSHA256: requestSHA,
			Scope: &scope, Coverage: &coverage, Objects: publicObjects}
		return nil
	})
	return out, err
}

func validateOperatorAggregateSources(input AttemptInput) error {
	var sources []Interpreted
	for _, frozen := range input.Documents {
		if isOperatorContractSource(frozen) {
			sources = append(sources, interpretedFromFrozen([]FrozenDocument{frozen})[0])
		}
	}
	if len(sources) == 0 {
		return nil
	}
	_, err := telecomSources(sources)
	return err
}

func isOperatorContractMaterial(frozen FrozenDocument) bool {
	return frozen.Material != nil &&
		((frozen.Material.Purpose == "primary_contract" && frozen.Document.Role == "draft_contract") ||
			(frozen.Material.Purpose == "supplement" && frozen.Document.Role == "attachment"))
}

func isOperatorContractSource(frozen FrozenDocument) bool {
	return isOperatorContractMaterial(frozen) && frozen.Ready && frozen.ParsedDocumentID != ""
}

func operatorAssociationEvidenceBeforeCutoff(st state, association ServiceObjectContractAssociation, cutoff time.Time) bool {
	input, ok := st.attemptInput(association.SourceAttemptID)
	if !ok || input.RunID != association.SourceRunID {
		return false
	}
	for _, frozen := range input.Documents {
		if !isOperatorContractSource(frozen) || frozen.Document.ID != association.DocumentID ||
			frozen.ParsedDocumentID != association.ParsedDocumentID || frozen.Material.MaterialID != association.MaterialID ||
			frozen.Material.Version != association.MaterialVersion {
			continue
		}
		originalAt, originalErr := time.Parse(time.RFC3339Nano, frozen.Document.CreatedAt)
		parsedAt, parsedErr := time.Parse(time.RFC3339Nano, frozen.ParsedCreatedAt)
		return originalErr == nil && parsedErr == nil && !originalAt.After(cutoff) && !parsedAt.After(cutoff)
	}
	return false
}

func operatorEvaluationCandidates(st state, input AttemptInput, runID, attemptID string) []operatorEvaluationCandidate {
	sources := make(map[string]FrozenDocument)
	unavailable := make([]operatorEvaluationCandidate, 0)
	for _, frozen := range input.Documents {
		if isOperatorContractSource(frozen) {
			sources[frozen.Document.ID+"\x00"+frozen.ParsedDocumentID] = frozen
		} else if isOperatorContractMaterial(frozen) {
			unavailable = append(unavailable, operatorEvaluationCandidate{ObjectRef: "unresolved-source:" + frozen.Document.ID, Reason: "contract_source_not_ready"})
		}
	}
	if len(sources) == 0 {
		return normalizeOperatorEvaluationCandidates(unavailable)
	}
	byKey := make(map[string]operatorEvaluationCandidate)
	referencedSources := make(map[string]bool)
	for _, evaluation := range st.Evaluations {
		if evaluation.RunID != runID || evaluation.AttemptID != attemptID {
			continue
		}
		refs := uniqueSortedStrings(evaluation.ObjectRefs)
		if len(refs) == 0 {
			continue
		}
		evidenceSources := make(map[string]FrozenDocument)
		for _, evidence := range evaluation.Evidence {
			key := evidence.Ref.DocumentID + "\x00" + evidence.Ref.ParsedDocumentID
			if source, ok := sources[key]; ok {
				referencedSources[key] = true
				evidenceSources[key] = source
			}
		}
		selected := evidenceSources
		missingSourceEvidence := len(selected) == 0
		ambiguousSource := false
		if missingSourceEvidence {
			selected = sources
			ambiguousSource = len(sources) > 1
		}
		for _, ref := range refs {
			for _, source := range selected {
				key := ref + "\x00" + source.Document.ID + "\x00" + source.ParsedDocumentID
				reason := ""
				if ambiguousSource {
					reason = "contract_object_source_ambiguous"
				}
				if missingSourceEvidence && !ambiguousSource {
					reason = "contract_object_source_evidence_missing"
				}
				byKey[key] = operatorEvaluationCandidate{ObjectRef: ref, Source: source, Reason: reason}
			}
		}
	}
	for key, source := range sources {
		if !referencedSources[key] {
			ref := "unresolved-source:" + source.Document.ID
			byKey[ref+"\x00"+key] = operatorEvaluationCandidate{ObjectRef: ref, Source: source, Reason: "contract_object_candidates_missing"}
		}
	}
	refSources := make(map[string]map[string]struct{})
	for _, candidate := range byKey {
		key := candidate.Source.Document.ID + "\x00" + candidate.Source.ParsedDocumentID
		if refSources[candidate.ObjectRef] == nil {
			refSources[candidate.ObjectRef] = make(map[string]struct{})
		}
		refSources[candidate.ObjectRef][key] = struct{}{}
	}
	for key, candidate := range byKey {
		if len(refSources[candidate.ObjectRef]) > 1 {
			candidate.Reason = "contract_object_source_ambiguous"
			byKey[key] = candidate
		}
	}
	out := append(make([]operatorEvaluationCandidate, 0, len(byKey)+len(unavailable)), unavailable...)
	for _, candidate := range byKey {
		out = append(out, candidate)
	}
	return normalizeOperatorEvaluationCandidates(out)
}

func mergeSupplementCandidates(candidates, supplements []operatorEvaluationCandidate) []operatorEvaluationCandidate {
	identified := make(map[string]bool)
	for _, candidate := range supplements {
		identified[candidate.Source.Document.ID] = true
	}
	merged := make([]operatorEvaluationCandidate, 0, len(candidates)+len(supplements))
	for _, candidate := range candidates {
		if candidate.Reason == "contract_object_candidates_missing" && identified[candidate.Source.Document.ID] {
			continue
		}
		merged = append(merged, candidate)
	}
	merged = append(merged, supplements...)
	byRef := make(map[string]map[string]bool)
	for _, candidate := range merged {
		if candidate.Source.Document.ID == "" {
			continue
		}
		if byRef[candidate.ObjectRef] == nil {
			byRef[candidate.ObjectRef] = make(map[string]bool)
		}
		byRef[candidate.ObjectRef][candidate.Source.Document.ID+"\x00"+candidate.Source.ParsedDocumentID] = true
	}
	for i := range merged {
		if len(byRef[merged[i].ObjectRef]) > 1 {
			merged[i].Reason = "contract_object_source_ambiguous"
		}
	}
	return merged
}

func normalizeOperatorEvaluationCandidates(candidates []operatorEvaluationCandidate) []operatorEvaluationCandidate {
	byKey := make(map[string]operatorEvaluationCandidate, len(candidates))
	for _, candidate := range candidates {
		key := candidate.ObjectRef + "\x00" + candidate.ServiceObjectID
		if candidate.Source.Document.ID != "" {
			key += "\x00" + candidate.Source.Document.ID + "\x00" + candidate.Source.ParsedDocumentID
		}
		if previous, ok := byKey[key]; ok && previous.Reason != "" {
			candidate.Reason = previous.Reason
		}
		byKey[key] = candidate
	}
	out := make([]operatorEvaluationCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObjectRef != out[j].ObjectRef {
			return out[i].ObjectRef < out[j].ObjectRef
		}
		if out[i].ServiceObjectID != out[j].ServiceObjectID {
			return out[i].ServiceObjectID < out[j].ServiceObjectID
		}
		if out[i].Source.Document.ID != out[j].Source.Document.ID {
			return out[i].Source.Document.ID < out[j].Source.Document.ID
		}
		return out[i].Source.ParsedDocumentID < out[j].Source.ParsedDocumentID
	})
	return out
}

type operatorServiceObjectAtTime struct {
	Head        ServiceObjectHead
	Version     ServiceObjectVersion
	Association *ServiceObjectContractAssociation
}

func loadOperatorServiceObjectsAt(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID string, at, cutoff time.Time) ([]operatorServiceObjectAtTime, error) {
	rows, err := tx.Query(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY service_object_id`, tenantID, orgID, caseID)
	if err != nil {
		return nil, err
	}
	var heads []ServiceObjectHead
	for rows.Next() {
		var head ServiceObjectHead
		if err := rows.Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion); err != nil {
			rows.Close()
			return nil, err
		}
		heads = append(heads, head)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]operatorServiceObjectAtTime, 0, len(heads))
	for _, head := range heads {
		var versionNumber int
		err := tx.QueryRow(ctx, `SELECT version FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND effective_from<=$5 ORDER BY (created_at<=$6) DESC,effective_from DESC,version DESC LIMIT 1`, tenantID, orgID, caseID, head.ID, at, cutoff).Scan(&versionNumber)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		version, err := loadServiceObjectVersionExact(ctx, tx, tenantID, orgID, caseID, head.ID, versionNumber)
		if err != nil {
			return nil, err
		}
		var associationVersion int
		err = tx.QueryRow(ctx, `SELECT association_version FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND object_version=$5 ORDER BY (created_at<=$6 AND confirmed_at<=$6) DESC,association_version DESC LIMIT 1`, tenantID, orgID, caseID, head.ID, versionNumber, cutoff).Scan(&associationVersion)
		var association *ServiceObjectContractAssociation
		if err == nil {
			value, loadErr := loadServiceObjectAssociation(ctx, tx, tenantID, orgID, caseID, head.ID, associationVersion)
			if loadErr != nil {
				return nil, loadErr
			}
			association = &value
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out = append(out, operatorServiceObjectAtTime{Head: head, Version: version, Association: association})
	}
	return out, nil
}

func unionRegisteredServiceObjects(candidates []operatorEvaluationCandidate, objects []operatorServiceObjectAtTime, scope OperatorEvaluationScope) []operatorEvaluationCandidate {
	selected := make(map[string]bool, len(scope.ServiceObjectIDs))
	for _, id := range scope.ServiceObjectIDs {
		selected[id] = true
	}
	byRef := make(map[string][]operatorServiceObjectAtTime)
	for _, object := range objects {
		if object.Association != nil {
			byRef[object.Association.ObjectRef] = append(byRef[object.Association.ObjectRef], object)
		}
	}
	covered := make(map[string]bool)
	out := make([]operatorEvaluationCandidate, 0, len(candidates)+len(objects))
	for _, candidate := range candidates {
		matches := byRef[candidate.ObjectRef]
		if len(matches) == 0 && scope.Kind == "whole_case" {
			out = append(out, candidate)
		}
		for _, object := range matches {
			if scope.Kind == "explicit_subset" && !selected[object.Head.ID] {
				continue
			}
			row := candidate
			row.ServiceObjectID = object.Head.ID
			if len(matches) > 1 {
				row.Reason = "service_object_identity_ambiguous"
			}
			out = append(out, row)
			covered[object.Head.ID] = true
		}
	}
	for _, object := range objects {
		id := object.Head.ID
		if (scope.Kind == "explicit_subset" && !selected[id]) || covered[id] {
			continue
		}
		ref := "service_object:" + id
		reason := "service_object_contract_association_missing"
		if object.Association != nil {
			ref = object.Association.ObjectRef
			reason = "service_object_source_requires_reassociation"
		}
		if object.Version.State != ServiceObjectConfirmed {
			reason = "service_object_identity_unconfirmed"
		}
		out = append(out, operatorEvaluationCandidate{ObjectRef: ref, ServiceObjectID: id, Reason: reason})
		covered[id] = true
	}
	for _, id := range scope.ServiceObjectIDs {
		if !covered[id] {
			out = append(out, operatorEvaluationCandidate{ObjectRef: "service_object:" + id, ServiceObjectID: id, Reason: "service_object_source_unresolved"})
		}
	}
	return out
}

func (s *Store) evaluateAggregateObjects(ctx context.Context, tx pgx.Tx, user accounts.User, caseItem Case, st state,
	attemptInput AttemptInput, input OperatorEvaluationInput, scope OperatorEvaluationScope, candidates []operatorEvaluationCandidate,
	objects []operatorServiceObjectAtTime, pack capabilitypack.Manifest, evaluator auditext.Evaluator, base auditext.FrozenInput) ([]operatorEvaluationObjectRecord, error) {
	byRef := make(map[string][]operatorServiceObjectAtTime)
	byID := make(map[string]operatorServiceObjectAtTime, len(objects))
	for _, object := range objects {
		byID[object.Head.ID] = object
		if object.Association != nil {
			byRef[object.Association.ObjectRef] = append(byRef[object.Association.ObjectRef], object)
		}
	}
	out := make([]operatorEvaluationObjectRecord, 0, len(candidates))
	for _, candidate := range candidates {
		factsRaw := []byte("[]")
		factsSum := sha256.Sum256(factsRaw)
		factsSHA := hex.EncodeToString(factsSum[:])
		result := OperatorEvaluationObject{ObjectRef: candidate.ObjectRef, Status: "unresolved", ReasonCode: candidate.Reason, FactsSHA256: factsSHA, FactRefs: []ServiceObjectFactRef{}, Facts: []auditext.Fact{}, Checks: []auditext.CheckResult{}}
		if candidate.Source.Document.ID != "" && candidate.Source.Material != nil {
			result.Source = &OperatorEvaluationSource{RunID: input.SourceReviewID, AttemptID: input.SourceAttemptID, DocumentID: candidate.Source.Document.ID,
				ParsedDocumentID: candidate.Source.ParsedDocumentID, MaterialID: candidate.Source.Material.MaterialID, MaterialVersion: candidate.Source.Material.Version,
				Purpose: candidate.Source.Material.Purpose, Role: candidate.Source.Document.Role}
		}
		registered := byRef[candidate.ObjectRef]
		if candidate.ServiceObjectID != "" {
			registered = nil
			if object, ok := byID[candidate.ServiceObjectID]; ok {
				registered = append(registered, object)
			}
		}
		if len(registered) == 0 {
			if result.ReasonCode == "" {
				result.ReasonCode = "service_object_unregistered"
			}
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if len(registered) > 1 {
			result.ReasonCode = "service_object_identity_ambiguous"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		object := registered[0]
		id, ver := object.Head.ID, object.Version.Version
		result.ServiceObjectID, result.ObjectVersion = &id, &ver
		if object.Association != nil {
			assocVer := object.Association.AssociationVersion
			result.AssociationVersion = &assocVer
			result.AssociationRunID = object.Association.SourceRunID
			result.AssociationAttemptID = object.Association.SourceAttemptID
			if result.Source != nil {
				result.Source.AssociationRunID = object.Association.SourceRunID
				result.Source.AssociationAttemptID = object.Association.SourceAttemptID
			}
		}
		if object.Version.CreatedAt.After(*base.EvidenceCutoffAt) || object.Association != nil &&
			(object.Association.CreatedAt.After(*base.EvidenceCutoffAt) || object.Association.ConfirmedAt.After(*base.EvidenceCutoffAt)) {
			result.Status, result.ReasonCode = "insufficient", "identity_evidence_after_cutoff"
		}
		if object.Association != nil && result.Status != "insufficient" && !operatorAssociationEvidenceBeforeCutoff(st, *object.Association, *base.EvidenceCutoffAt) {
			result.Status, result.ReasonCode = "insufficient", "contract_evidence_after_cutoff"
		}
		if object.Association != nil && result.Status != "insufficient" {
			facts, err := operatorFactsForAssociation(*object.Association)
			if err != nil {
				return nil, err
			}
			factsRaw = append([]byte(nil), object.Association.FactsCanonicalJSON...)
			factsSHA = object.Association.FactsSHA256
			result.Facts, result.FactsSHA256 = facts, factsSHA
			result.FactRefs = append([]ServiceObjectFactRef{}, object.Association.FactRefs...)
		}
		if result.ReasonCode != "" {
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if object.Version.State != ServiceObjectConfirmed || object.Association == nil {
			result.ReasonCode = "service_object_identity_unconfirmed"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if candidate.Source.Document.ID == "" || candidate.Source.Document.ID != object.Association.DocumentID || candidate.Source.ParsedDocumentID != object.Association.ParsedDocumentID ||
			candidate.Source.Material == nil || candidate.Source.Material.MaterialID != object.Association.MaterialID || candidate.Source.Material.Version != object.Association.MaterialVersion {
			result.ReasonCode = "service_object_source_requires_reassociation"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		association := *object.Association
		createdAt, dateErr := time.Parse(time.RFC3339Nano, candidate.Source.Document.CreatedAt)
		parsedAt, parseErr := time.Parse(time.RFC3339Nano, candidate.Source.ParsedCreatedAt)
		if dateErr != nil || parseErr != nil || createdAt.After(*base.EvidenceCutoffAt) || parsedAt.After(*base.EvidenceCutoffAt) {
			result.Status, result.ReasonCode = "insufficient", "contract_evidence_after_cutoff"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		frozenFacts := result.Facts
		mappings, err := loadApplicableOperatorMappings(ctx, tx, user, caseItem, object, input.BusinessAt, *base.EvidenceCutoffAt)
		if err != nil {
			return nil, err
		}
		if len(mappings) == 0 {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.ReasonCode = "operator_mapping_missing"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		if len(mappings) > 1 {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.ReasonCode = "operator_mapping_ambiguous"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		mapping := mappings[0]
		mappingID, mappingVersion, bindingVersion := mapping.MappingID, mapping.MappingVersion, mapping.Binding.Version
		result.MappingID = &mappingID
		result.MappingVersion = &mappingVersion
		result.BindingVersion = &bindingVersion
		snapshot, found, err := loadAggregateSnapshot(ctx, tx, caseItem.ID, mapping.Binding.Version, input.BusinessAt, input.EvidenceCutoffAt)
		if err != nil {
			return nil, err
		}
		if !found {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.Status = "insufficient"
			result.ReasonCode = "external_snapshot_missing"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		snapshotID := snapshot.SnapshotSetID
		result.SnapshotSetID = &snapshotID
		result.SnapshotCollectedAt = &snapshot.CollectedAt
		selected := interpretedFromFrozen([]FrozenDocument{candidate.Source})
		if _, err := telecomSources(selected); err != nil {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.ReasonCode = "contract_source_integrity_error"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		objectFrozen := base
		objectFrozen.ContractVersion = association.DocumentID
		objectFrozen.BindingStatus = auditext.BindingConfirmed
		objectFrozen.BindingVersion = mapping.Binding.Version
		objectFrozen.SnapshotSetID = snapshot.SnapshotSetID
		objectFrozen.ExternalObjectID = mapping.Binding.ExternalObject.ExternalObjectID
		objectFrozen.SourceSystem = snapshot.SourceSystem
		objectFrozen.Facts = frozenFacts
		if err := objectFrozen.Validate(); err != nil {
			return nil, fault.Conflict("对象核验冻结输入不完整")
		}
		checks, err := evaluator.Evaluate(ctx, objectFrozen, operatorObservationsForEvaluation(snapshot, input.BusinessAt, *base.EvidenceCutoffAt))
		if errors.Is(err, auditext.ErrUnauthorized) {
			return nil, fault.Forbidden("当前授权不能核验该案件")
		}
		if err != nil {
			result.Facts = frozenFacts
			result.FactsSHA256 = factsSHA
			result.Status = "insufficient"
			result.ReasonCode = "evaluation_unavailable"
			out = append(out, operatorEvaluationObjectRecord{Result: result, FactsJSON: factsRaw, FactsSHA256: factsSHA})
			continue
		}
		machineStatus := "completed"
		for _, check := range checks {
			if err := check.Validate(); err != nil {
				return nil, fault.New(502, "fulfillment_invalid", "核验器返回了不合法的结果")
			}
			if check.MachineStatus != auditext.MachineCompleted {
				machineStatus = "partial"
			}
		}
		obj, err := operatorEvaluationObjectForAssociation(scope, input, mapping.Binding, association, snapshot, candidate.Source.Material.Purpose, candidate.Source.Document.Role, frozenFacts, checks, machineStatus)
		if err != nil {
			return nil, err
		}
		obj.Source.RunID = input.SourceReviewID
		obj.Source.AttemptID = input.SourceAttemptID
		out = append(out, operatorEvaluationObjectRecord{Result: obj, FactsJSON: factsRaw, FactsSHA256: factsSHA})
	}
	return out, nil
}

func loadApplicableOperatorMappings(ctx context.Context, tx pgx.Tx, user accounts.User, caseItem Case, object operatorServiceObjectAtTime, at, cutoff time.Time) ([]OperatorMappingVersion, error) {
	rows, err := tx.Query(ctx, `SELECT binding_version,mapping_id,mapping_version FROM (
		SELECT DISTINCT ON (mapping_id) binding_version,mapping_id,mapping_version,status
		FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4
		AND effective_from<=$5 AND $5<effective_until AND created_at<=$6 AND confirmed_at<=$6 AND mapping_id IS NOT NULL
		ORDER BY mapping_id,mapping_version DESC
	) selected WHERE status='confirmed' ORDER BY mapping_id`, accounts.Tenant(user), caseItem.OwnerOrgID, caseItem.ID, object.Head.ID, at, cutoff)
	if err != nil {
		return nil, err
	}
	type pin struct {
		version, mapping string
		mappingVersion   int64
	}
	var pins []pin
	for rows.Next() {
		var p pin
		if err := rows.Scan(&p.version, &p.mapping, &p.mappingVersion); err != nil {
			rows.Close()
			return nil, err
		}
		pins = append(pins, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]OperatorMappingVersion, 0, len(pins))
	for _, p := range pins {
		binding, err := loadOperatorBindingByVersion(ctx, tx, accounts.Tenant(user), caseItem.OwnerOrgID, caseItem.ID, p.version)
		if err != nil {
			return nil, err
		}
		if binding.ServiceObjectID != object.Head.ID || binding.ObjectVersion != object.Version.Version || binding.AssociationVersion != object.Association.AssociationVersion {
			continue
		}
		out = append(out, OperatorMappingVersion{MappingID: p.mapping, MappingVersion: p.mappingVersion, ServiceObjectID: object.Head.ID, ObjectVersion: object.Version.Version, AssociationVersion: object.Association.AssociationVersion, Binding: binding})
	}
	return out, nil
}

func loadAggregateSnapshot(ctx context.Context, tx pgx.Tx, caseID, bindingVersion string, businessAt time.Time, cutoff *time.Time) (OperatorSnapshotSet, bool, error) {
	cut := businessAt
	if cutoff != nil {
		cut = *cutoff
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT snapshot_set_id FROM operator_external_snapshot_sets WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND binding_version=$2 AND business_at=$3 AND collected_at<=$4 AND created_at<=$4 ORDER BY collected_at DESC,snapshot_set_id LIMIT 1`, caseID, bindingVersion, businessAt, cut).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorSnapshotSet{}, false, nil
	}
	if err != nil {
		return OperatorSnapshotSet{}, false, err
	}
	set, err := loadOperatorSnapshotSetByID(ctx, tx, caseID, id)
	return set, err == nil, err
}

func nullableObjectTuple(result OperatorEvaluationObject) (any, any, any) {
	if result.ServiceObjectID == nil || result.ObjectVersion == nil {
		return nil, nil, nil
	}
	var association any
	if result.AssociationVersion != nil {
		association = *result.AssociationVersion
	}
	return *result.ServiceObjectID, *result.ObjectVersion, association
}
func nullableMappingTuple(result OperatorEvaluationObject) (any, any, any, any) {
	if result.MappingID == nil {
		return nil, nil, nil, nil
	}
	var snapshot any
	if result.SnapshotSetID != nil {
		snapshot = *result.SnapshotSetID
	}
	return *result.MappingID, *result.MappingVersion, *result.BindingVersion, snapshot
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

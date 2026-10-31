package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

// ServiceObjectReviewContext is a deterministic, bounded projection of every
// recognized contract object and every registered service object at one
// business instant. It deliberately includes unresolved entries so omission
// cannot turn a partial object inventory into an all-clear.
type ServiceObjectReviewContext struct {
	BusinessAt time.Time                        `json:"business_at"`
	Items      []ServiceObjectReviewContextItem `json:"items"`
	Complete   bool                             `json:"complete"`
	SHA256     string                           `json:"sha256"`
}

type ServiceObjectReviewContextItem struct {
	ServiceObjectID    string                           `json:"service_object_id,omitempty"`
	CandidateObjectRef string                           `json:"candidate_object_ref,omitempty"`
	CandidateEvidence  []ServiceObjectCandidateEvidence `json:"candidate_evidence,omitempty"`
	ObjectVersion      int                              `json:"object_version,omitempty"`
	ObjectType         ServiceObjectType                `json:"object_type,omitempty"`
	ObjectState        ServiceObjectState               `json:"object_state,omitempty"`
	AssociationVersion int                              `json:"association_version,omitempty"`
	MaterialID         string                           `json:"material_id,omitempty"`
	MaterialVersion    int                              `json:"material_version,omitempty"`
	DocumentID         string                           `json:"document_id,omitempty"`
	ParsedDocumentID   string                           `json:"parsed_document_id,omitempty"`
	SourceRunID        string                           `json:"source_run_id,omitempty"`
	SourceAttemptID    string                           `json:"source_attempt_id,omitempty"`
	ObjectRef          string                           `json:"object_ref,omitempty"`
	FactsSHA256        string                           `json:"facts_sha256,omitempty"`
	MappingID          string                           `json:"mapping_id,omitempty"`
	MappingVersion     int64                            `json:"mapping_version,omitempty"`
	BindingVersion     string                           `json:"binding_version,omitempty"`
	UnresolvedReason   string                           `json:"unresolved_reason,omitempty"`
}

// ServiceObjectCandidateEvidence is derived from one frozen parse and binds
// its recognized object candidate to exact material, document, parse and
// facts identities.
type ServiceObjectCandidateEvidence struct {
	ObjectRef        string `json:"object_ref"`
	MaterialID       string `json:"material_id"`
	MaterialVersion  int    `json:"material_version"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	SourceRunID      string `json:"source_run_id"`
	SourceAttemptID  string `json:"source_attempt_id"`
	FactsSHA256      string `json:"facts_sha256"`
}

type serviceObjectReviewContextSource struct {
	Head        ServiceObjectHead
	Version     ServiceObjectVersion
	Association *ServiceObjectContractAssociation
	Mapping     *OperatorMappingVersion
}

func validateServiceObjectReviewContext(snapshot RunSnapshot) error {
	if snapshot.ServiceObjectContext == nil {
		if snapshot.ServiceObjectContextHash != "" {
			return errors.New("service object context hash exists without a payload")
		}
		return nil
	}
	if snapshot.ServiceObjectContextHash == "" || snapshot.ServiceObjectContextHash != snapshot.ServiceObjectContext.SHA256 {
		return errors.New("service object context payload and hash must be present together")
	}
	payload, digest, err := canonicalServiceObjectReviewContext(*snapshot.ServiceObjectContext)
	if err != nil {
		return err
	}
	if !validOperatorDigest(snapshot.ServiceObjectContextHash) || digest != snapshot.ServiceObjectContextHash || len(payload) == 0 {
		return errors.New("service object context payload is noncanonical or its hash does not match")
	}
	return nil
}

func encodeServiceObjectReviewContext(snapshot RunSnapshot) ([]byte, error) {
	if err := validateServiceObjectReviewContext(snapshot); err != nil {
		return nil, err
	}
	if snapshot.ServiceObjectContext == nil {
		return nil, nil
	}
	return json.Marshal(snapshot.ServiceObjectContext)
}

func decodeServiceObjectReviewContext(payload []byte, hash string) (*ServiceObjectReviewContext, error) {
	if len(payload) == 0 && hash == "" {
		return nil, nil
	}
	if len(payload) == 0 || hash == "" {
		return nil, errors.New("service object context payload and hash must be present together")
	}
	var context ServiceObjectReviewContext
	if err := json.Unmarshal(payload, &context); err != nil {
		return nil, err
	}
	_, digest, err := canonicalServiceObjectReviewContext(context)
	if err != nil {
		return nil, err
	}
	full, err := json.Marshal(context)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(payload, full) || digest != hash || context.SHA256 != hash {
		return nil, errors.New("service object context payload is noncanonical or its hash does not match")
	}
	return &context, nil
}

func canonicalServiceObjectReviewContext(context ServiceObjectReviewContext) ([]byte, string, error) {
	if context.BusinessAt.IsZero() || len(context.Items) == 0 || len(context.Items) > 1100 {
		return nil, "", errInvalidServiceObjectAssociation
	}
	if context.BusinessAt.Location() != time.UTC || context.BusinessAt.Nanosecond()%1000 != 0 {
		return nil, "", errInvalidServiceObjectAssociation
	}
	items := append([]ServiceObjectReviewContextItem(nil), context.Items...)
	for i := range items {
		items[i].CandidateEvidence = append([]ServiceObjectCandidateEvidence(nil), items[i].CandidateEvidence...)
	}
	payload, err := json.Marshal(struct {
		BusinessAt time.Time                        `json:"business_at"`
		Items      []ServiceObjectReviewContextItem `json:"items"`
		Complete   bool                             `json:"complete"`
	}{context.BusinessAt.UTC().Truncate(time.Microsecond), items, context.Complete})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

// serviceObjectReviewContextSourcesTx loads the authoritative service-object
// heads together with their current version, association and mapping pins
// under the caller's case-scoped transaction. It never selects a "latest"
// row: every source identity comes from the head's current pointers.
func serviceObjectReviewContextSourcesTx(ctx context.Context, tx pgx.Tx, caseItem Case) ([]serviceObjectReviewContextSource, error) {
	tenantID, orgID, caseID := caseItem.TenantID, caseItem.OwnerOrgID, caseItem.ID
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
	if len(heads) > 100 {
		return nil, fault.Invalid("每个案件最多登记100个服务对象")
	}

	sources := make([]serviceObjectReviewContextSource, 0, len(heads))
	for _, head := range heads {
		version, err := loadServiceObjectVersionValue(ctx, tx, tenantID, orgID, caseID, head)
		if err != nil {
			return nil, err
		}
		source := serviceObjectReviewContextSource{Head: head, Version: version}
		if head.CurrentAssociationVersion > 0 {
			association, err := loadServiceObjectAssociation(ctx, tx, tenantID, orgID, caseID, head.ID, head.CurrentAssociationVersion)
			if err != nil {
				return nil, err
			}
			source.Association = &association
		}
		var mappingID string
		err = tx.QueryRow(ctx, `SELECT mapping_id FROM operator_mapping_lineages
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`, tenantID, orgID, caseID, head.ID).Scan(&mappingID)
		if err == nil {
			mappingHead, mapping, identity, association, err := loadOperatorMappingCurrent(ctx, tx, tenantID, orgID, caseID, mappingID)
			if err != nil {
				return nil, err
			}
			if mappingHead.ServiceObjectID != head.ID || mapping.ObjectVersion != head.CurrentVersion ||
				mapping.AssociationVersion != head.CurrentAssociationVersion || identity.Version != head.CurrentVersion ||
				source.Association == nil || association.AssociationVersion != source.Association.AssociationVersion {
				return nil, fault.New(500, "integrity_error", "服务对象与当前映射版本指针不一致")
			}
			source.Mapping = &mapping
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// serviceObjectFrozenMaterials projects the selected documents with their
// immutable material identities so context planning sees exactly the material
// versions AttemptInput will later freeze. Without this projection an accepted
// context could not be recomputed byte for byte from the frozen attempt input.
// The caller's slice is never mutated.
func serviceObjectFrozenMaterials(st state, documents []FrozenDocument) []FrozenDocument {
	projected := append([]FrozenDocument(nil), documents...)
	for i := range projected {
		bindFrozenMaterialIdentity(st, &projected[i])
	}
	return projected
}

// withServiceObjectContextTx freezes the current service-object and mapping
// pins using the same acceptance transaction as the run snapshot. These tables
// are intentionally absent from the shared state loader, so this function
// reads their authoritative SQL heads directly. Object discovery for the new
// attempt has not run yet; the explicit unresolved inventory marker prevents a
// pre-review snapshot from claiming complete coverage.
//
// Freezing no longer depends on a governed knowledge pack. A case with
// registered service objects must freeze their exact dependencies, and it must
// name the business instant explicitly: the server clock is never substituted
// for the instant whose mappings are being reviewed. Ungoverned cases without
// any registered object keep the historical nil extension.
func withServiceObjectContextTx(ctx context.Context, tx pgx.Tx, caseItem Case, businessAt time.Time,
	selectedFrozenMaterials []FrozenDocument, snapshot RunSnapshot) (RunSnapshot, error) {
	sources, err := serviceObjectReviewContextSourcesTx(ctx, tx, caseItem)
	if err != nil {
		return RunSnapshot{}, err
	}
	if len(sources) == 0 && snapshot.GovernedKnowledge == nil {
		snapshot.ServiceObjectContext = nil
		snapshot.ServiceObjectContextHash = ""
		return snapshot, nil
	}
	if businessAt.IsZero() {
		return RunSnapshot{}, fault.Invalid("该案件已登记服务对象，必须提供明确的业务时间")
	}
	businessAt = businessAt.UTC().Truncate(time.Microsecond)

	var candidates []ServiceObjectCandidateEvidence
	parseInventoryReady := len(selectedFrozenMaterials) > 0
	for _, frozen := range selectedFrozenMaterials {
		if frozen.Material == nil || frozen.Material.MaterialID == "" || frozen.Material.Version < 1 ||
			frozen.Document.ID == "" || frozen.ParsedDocumentID == "" || !frozen.Ready || frozen.ParseError != "" {
			parseInventoryReady = false
		}
	}
	if parseInventoryReady {
		for _, source := range sources {
			association := source.Association
			if association == nil || !selectedMaterialHasParse(selectedFrozenMaterials, association.MaterialID, association.MaterialVersion,
				association.DocumentID, association.ParsedDocumentID) {
				continue
			}
			facts := make([]rulepack.Fact, len(association.FactRefs))
			for i := range association.FactRefs {
				facts[i] = association.FactRefs[i].Fact
			}
			items, err := serviceObjectCandidatesFromFrozenFacts(association.MaterialID, association.MaterialVersion,
				association.DocumentID, association.ParsedDocumentID, association.SourceRunID, association.SourceAttemptID, facts)
			if err != nil {
				return RunSnapshot{}, err
			}
			candidates = append(candidates, items...)
		}
	}
	contextSnapshot, err := planServiceObjectReviewContext(businessAt, sources, candidates)
	if err != nil {
		return RunSnapshot{}, err
	}
	// Admission precedes this run's extraction. Keep the unknown candidate
	// inventory explicit even when existing pinned associations match the
	// selected parse; only a later frozen result can establish candidate
	// coverage for newly recognized objects.
	contextSnapshot.Items = append(contextSnapshot.Items, ServiceObjectReviewContextItem{UnresolvedReason: "recognized_object_inventory_pending_review"})
	contextSnapshot.Complete = false
	_, digest, err := canonicalServiceObjectReviewContext(contextSnapshot)
	if err != nil {
		return RunSnapshot{}, err
	}
	contextSnapshot.SHA256 = digest
	snapshot.ServiceObjectContext = &contextSnapshot
	snapshot.ServiceObjectContextHash = digest
	return snapshot, nil
}

func selectedMaterialHasParse(documents []FrozenDocument, materialID string, materialVersion int, documentID, parsedDocumentID string) bool {
	for _, item := range documents {
		if item.Material != nil && item.Material.MaterialID == materialID && item.Material.Version == materialVersion &&
			item.Document.ID == documentID && item.ParsedDocumentID == parsedDocumentID {
			return true
		}
	}
	return false
}

// serviceObjectCandidatesFromFrozenFacts derives candidate evidence only from
// facts cited to the exact document and parse pinned by the frozen review
// attempt. Facts from sibling documents/parses do not bleed into this object
// candidate's digest.
func serviceObjectCandidatesFromFrozenFacts(materialID string, materialVersion int, documentID, parsedDocumentID, runID, attemptID string, facts []rulepack.Fact) ([]ServiceObjectCandidateEvidence, error) {
	if strings.TrimSpace(materialID) == "" || materialVersion < 1 || strings.TrimSpace(documentID) == "" ||
		strings.TrimSpace(parsedDocumentID) == "" || strings.TrimSpace(runID) == "" || strings.TrimSpace(attemptID) == "" || len(facts) > 1000 {
		return nil, errInvalidServiceObjectAssociation
	}
	grouped := make(map[string][]ServiceObjectFactRef)
	for _, sourceFact := range facts {
		objectRef := strings.TrimSpace(sourceFact.ObjectRef)
		if objectRef == "" {
			continue
		}
		fact := sourceFact
		fact.ObjectRef = objectRef
		fact.Citations = nil
		for _, citation := range sourceFact.Citations {
			if citation.SourceID == documentID && citation.SourceVersion == parsedDocumentID {
				fact.Citations = append(fact.Citations, citation)
			}
		}
		if len(fact.Citations) == 0 {
			continue
		}
		fact.Exclusions = append([]string(nil), sourceFact.Exclusions...)
		if sourceFact.Applicable != nil {
			applicable := *sourceFact.Applicable
			fact.Applicable = &applicable
		}
		factSHA, err := canonicalRuleFactSHA256(fact)
		if err != nil {
			return nil, err
		}
		grouped[objectRef] = append(grouped[objectRef], ServiceObjectFactRef{Fact: fact,
			DocumentID: documentID, ParsedDocumentID: parsedDocumentID, CanonicalFactSHA256: factSHA})
	}
	refs := make([]string, 0, len(grouped))
	for ref := range grouped {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	out := make([]ServiceObjectCandidateEvidence, 0, len(refs))
	for _, ref := range refs {
		_, digest, err := canonicalServiceObjectFacts(grouped[ref])
		if err != nil {
			return nil, err
		}
		out = append(out, ServiceObjectCandidateEvidence{ObjectRef: ref, MaterialID: materialID,
			MaterialVersion: materialVersion, DocumentID: documentID, ParsedDocumentID: parsedDocumentID,
			SourceRunID: runID, SourceAttemptID: attemptID, FactsSHA256: digest})
	}
	return out, nil
}

// planServiceObjectReviewContext operates only on already-authorized, exact
// source rows. The caller must resolve heads and immutable versions under its
// case-scoped transaction; this function does not select a "latest" row.
func planServiceObjectReviewContext(businessAt time.Time, sources []serviceObjectReviewContextSource, recognizedCandidates []ServiceObjectCandidateEvidence) (ServiceObjectReviewContext, error) {
	if businessAt.IsZero() || len(sources) > 100 || len(recognizedCandidates) > 1000 {
		return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
	}
	businessAt = businessAt.UTC().Truncate(time.Microsecond)
	candidatesByRef := make(map[string][]ServiceObjectCandidateEvidence, len(recognizedCandidates))
	seenCandidates := make(map[string]struct{}, len(recognizedCandidates))
	for _, candidate := range recognizedCandidates {
		candidate.ObjectRef = strings.TrimSpace(candidate.ObjectRef)
		candidate.MaterialID = strings.TrimSpace(candidate.MaterialID)
		candidate.DocumentID = strings.TrimSpace(candidate.DocumentID)
		candidate.ParsedDocumentID = strings.TrimSpace(candidate.ParsedDocumentID)
		candidate.SourceRunID = strings.TrimSpace(candidate.SourceRunID)
		candidate.SourceAttemptID = strings.TrimSpace(candidate.SourceAttemptID)
		candidate.FactsSHA256 = strings.TrimSpace(candidate.FactsSHA256)
		if candidate.ObjectRef == "" || candidate.MaterialID == "" || candidate.MaterialVersion < 1 ||
			candidate.DocumentID == "" || candidate.ParsedDocumentID == "" || candidate.SourceRunID == "" ||
			candidate.SourceAttemptID == "" || !validOperatorDigest(candidate.FactsSHA256) {
			return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
		}
		keyPayload, err := json.Marshal(candidate)
		if err != nil {
			return ServiceObjectReviewContext{}, err
		}
		key := string(keyPayload)
		if _, duplicate := seenCandidates[key]; duplicate {
			continue
		}
		seenCandidates[key] = struct{}{}
		candidatesByRef[candidate.ObjectRef] = append(candidatesByRef[candidate.ObjectRef], candidate)
	}
	for ref := range candidatesByRef {
		sort.Slice(candidatesByRef[ref], func(i, j int) bool {
			a, b := candidatesByRef[ref][i], candidatesByRef[ref][j]
			if a.MaterialID != b.MaterialID {
				return a.MaterialID < b.MaterialID
			}
			if a.MaterialVersion != b.MaterialVersion {
				return a.MaterialVersion < b.MaterialVersion
			}
			if a.DocumentID != b.DocumentID {
				return a.DocumentID < b.DocumentID
			}
			if a.ParsedDocumentID != b.ParsedDocumentID {
				return a.ParsedDocumentID < b.ParsedDocumentID
			}
			return a.FactsSHA256 < b.FactsSHA256
		})
	}
	sorted := append([]serviceObjectReviewContextSource(nil), sources...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Head.ID < sorted[j].Head.ID })
	seenIDs := make(map[string]struct{}, len(sorted))
	seenRefs := make(map[string]struct{}, len(sorted)+len(candidatesByRef))
	items := make([]ServiceObjectReviewContextItem, 0, len(sorted)+len(candidatesByRef))
	for _, source := range sorted {
		if strings.TrimSpace(source.Head.ID) == "" || source.Head.ID != source.Version.ServiceObjectID || source.Head.CurrentVersion != source.Version.Version {
			return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
		}
		if _, ok := seenIDs[source.Head.ID]; ok {
			return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
		}
		seenIDs[source.Head.ID] = struct{}{}
		item := ServiceObjectReviewContextItem{ServiceObjectID: source.Head.ID, ObjectVersion: source.Version.Version,
			ObjectType: source.Version.ObjectType, ObjectState: source.Version.State}
		if source.Association != nil {
			if source.Association.ServiceObjectID != source.Head.ID || source.Association.AssociationVersion != source.Head.CurrentAssociationVersion || source.Association.ObjectVersion != source.Version.Version {
				return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
			}
			if strings.TrimSpace(source.Association.MaterialID) == "" || source.Association.MaterialVersion < 1 ||
				strings.TrimSpace(source.Association.DocumentID) == "" || strings.TrimSpace(source.Association.ParsedDocumentID) == "" ||
				strings.TrimSpace(source.Association.SourceRunID) == "" || strings.TrimSpace(source.Association.SourceAttemptID) == "" ||
				strings.TrimSpace(source.Association.ObjectRef) == "" || !validOperatorDigest(source.Association.FactsSHA256) {
				return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
			}
			item.AssociationVersion = source.Association.AssociationVersion
			item.MaterialID, item.MaterialVersion = source.Association.MaterialID, source.Association.MaterialVersion
			item.DocumentID, item.ParsedDocumentID = source.Association.DocumentID, source.Association.ParsedDocumentID
			item.SourceRunID, item.SourceAttemptID = source.Association.SourceRunID, source.Association.SourceAttemptID
			item.ObjectRef, item.FactsSHA256 = source.Association.ObjectRef, source.Association.FactsSHA256
		}
		if source.Mapping != nil {
			binding := source.Mapping.Binding
			if strings.TrimSpace(source.Mapping.MappingID) == "" || source.Mapping.MappingVersion < 1 ||
				source.Mapping.ServiceObjectID != source.Head.ID || source.Mapping.ObjectVersion != source.Version.Version ||
				source.Association == nil || source.Mapping.AssociationVersion != source.Association.AssociationVersion ||
				strings.TrimSpace(binding.Version) == "" || !binding.EffectiveFrom.Before(binding.EffectiveUntil) {
				return ServiceObjectReviewContext{}, errInvalidServiceObjectAssociation
			}
			item.MappingID, item.MappingVersion = source.Mapping.MappingID, source.Mapping.MappingVersion
			item.BindingVersion = binding.Version
		}
		switch {
		case source.Version.State != ServiceObjectConfirmed:
			item.UnresolvedReason = "service_object_not_confirmed"
		case source.Association == nil || source.Head.CurrentAssociationVersion == 0:
			item.UnresolvedReason = "contract_association_missing"
		case source.Mapping == nil:
			item.UnresolvedReason = "operator_mapping_missing"
		case source.Mapping.Binding.Status != "confirmed":
			item.UnresolvedReason = "operator_mapping_not_confirmed"
		case businessAt.Before(source.Mapping.Binding.EffectiveFrom) || !businessAt.Before(source.Mapping.Binding.EffectiveUntil):
			item.UnresolvedReason = "operator_mapping_not_effective_at_business_time"
		}
		if item.ObjectRef != "" {
			seenRefs[item.ObjectRef] = struct{}{}
			item.CandidateEvidence = append([]ServiceObjectCandidateEvidence(nil), candidatesByRef[item.ObjectRef]...)
			if item.UnresolvedReason == "" && !serviceObjectCandidateMatchesAssociation(item.CandidateEvidence, source.Association) {
				item.UnresolvedReason = "association_not_pinned_to_frozen_candidate"
			}
		}
		items = append(items, item)
	}
	refItems := make(map[string][]int, len(items))
	for index, item := range items {
		if item.ServiceObjectID != "" && item.ObjectRef != "" {
			refItems[item.ObjectRef] = append(refItems[item.ObjectRef], index)
		}
	}
	for _, indexes := range refItems {
		if len(indexes) > 1 {
			for _, index := range indexes {
				items[index].UnresolvedReason = "ambiguous_object_ref_mapping"
			}
		}
	}
	for ref, evidence := range candidatesByRef {
		if _, registered := seenRefs[ref]; !registered {
			items = append(items, ServiceObjectReviewContextItem{
				CandidateObjectRef: ref, ObjectRef: ref, CandidateEvidence: append([]ServiceObjectCandidateEvidence(nil), evidence...),
				UnresolvedReason: "recognized_object_not_registered",
			})
		}
	}
	if len(items) == 0 {
		items = append(items, ServiceObjectReviewContextItem{UnresolvedReason: "object_inventory_unavailable"})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ServiceObjectID != items[j].ServiceObjectID {
			if items[i].ServiceObjectID == "" {
				return false
			}
			if items[j].ServiceObjectID == "" {
				return true
			}
			return items[i].ServiceObjectID < items[j].ServiceObjectID
		}
		return items[i].CandidateObjectRef < items[j].CandidateObjectRef
	})
	complete := true
	for _, item := range items {
		if item.UnresolvedReason != "" {
			complete = false
			break
		}
	}
	payload, err := json.Marshal(struct {
		BusinessAt time.Time                        `json:"business_at"`
		Items      []ServiceObjectReviewContextItem `json:"items"`
		Complete   bool                             `json:"complete"`
	}{businessAt, items, complete})
	if err != nil {
		return ServiceObjectReviewContext{}, err
	}
	digest := sha256.Sum256(payload)
	return ServiceObjectReviewContext{BusinessAt: businessAt, Items: items, Complete: complete, SHA256: hex.EncodeToString(digest[:])}, nil
}

func serviceObjectCandidateMatchesAssociation(candidates []ServiceObjectCandidateEvidence, association *ServiceObjectContractAssociation) bool {
	if association == nil || len(candidates) != 1 {
		return false
	}
	candidate := candidates[0]
	return candidate.ObjectRef == association.ObjectRef && candidate.MaterialID == association.MaterialID &&
		candidate.MaterialVersion == association.MaterialVersion && candidate.DocumentID == association.DocumentID &&
		candidate.ParsedDocumentID == association.ParsedDocumentID && candidate.SourceRunID == association.SourceRunID &&
		candidate.SourceAttemptID == association.SourceAttemptID && candidate.FactsSHA256 == association.FactsSHA256
}

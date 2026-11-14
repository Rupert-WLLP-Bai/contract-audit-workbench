// File metadata (manifest mirror):
// Purpose: Adapts exact selected frozen material facts and maps Operator context or hash results into the original admission and retry snapshots.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"time"

	operatorApp "contractaudit/internal/operator/app"
	operatorModel "contractaudit/internal/operator/model"
	operatorPostgres "contractaudit/internal/operator/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

type ServiceObjectReviewContext = operatorModel.ServiceObjectReviewContext
type ServiceObjectReviewContextItem = operatorModel.ServiceObjectReviewContextItem
type ServiceObjectCandidateEvidence = operatorModel.ServiceObjectCandidateEvidence

type serviceObjectReviewContextSource struct {
	Head        ServiceObjectHead
	Version     ServiceObjectVersion
	Association *ServiceObjectContractAssociation
	Mapping     *OperatorMappingVersion
}

func validateServiceObjectReviewContext(snapshot RunSnapshot) error {
	return operatorModel.ValidateServiceObjectReviewContext(snapshot.ServiceObjectContext, snapshot.ServiceObjectContextHash)
}
func encodeServiceObjectReviewContext(snapshot RunSnapshot) ([]byte, error) {
	return operatorModel.EncodeServiceObjectReviewContext(snapshot.ServiceObjectContext, snapshot.ServiceObjectContextHash)
}
func decodeServiceObjectReviewContext(payload []byte, hash string) (*ServiceObjectReviewContext, error) {
	return operatorModel.DecodeServiceObjectReviewContext(payload, hash)
}
func canonicalServiceObjectReviewContext(value ServiceObjectReviewContext) ([]byte, string, error) {
	return operatorModel.CanonicalServiceObjectReviewContext(value)
}

// serviceObjectReviewContextSourcesTx loads the authoritative service-object
// heads together with their current version, association and mapping pins
// under the caller's case-scoped transaction. It never selects a "latest"
// row: every source identity comes from the head's current pointers.
func serviceObjectReviewContextSourcesTx(ctx context.Context, tx pgx.Tx, item Case) ([]operatorModel.ContextSource, error) {
	return operatorApp.LoadContextSources(ctx, operatorPostgres.NewReads(platformpostgres.Bind(tx)), operatorApp.ReadIdentity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID})
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
func withServiceObjectContextTx(ctx context.Context, tx pgx.Tx, item Case, businessAt time.Time, docs []FrozenDocument, snapshot RunSnapshot) (RunSnapshot, error) {
	var selected []operatorApp.SelectedFrozenMaterial
	for _, doc := range docs {
		value := operatorApp.SelectedFrozenMaterial{DocumentID: doc.Document.ID, ParsedDocumentID: doc.ParsedDocumentID, Ready: doc.Ready, ParseError: doc.ParseError}
		if doc.Material != nil {
			value.HasMaterial = true
			value.MaterialID = doc.Material.MaterialID
			value.MaterialVersion = doc.Material.Version
		}
		selected = append(selected, value)
	}
	result, hash, err := operatorApp.FreezeContext(ctx, operatorPostgres.NewReads(platformpostgres.Bind(tx)), operatorApp.ReadIdentity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID}, businessAt, selected, snapshot.GovernedKnowledge != nil)
	if err != nil {
		return RunSnapshot{}, err
	}
	snapshot.ServiceObjectContext = result
	snapshot.ServiceObjectContextHash = hash
	return snapshot, nil
}

func serviceObjectCandidatesFromFrozenFacts(materialID string, materialVersion int, documentID, parsedDocumentID, runID, attemptID string, facts []rulepack.Fact) ([]ServiceObjectCandidateEvidence, error) {
	return operatorModel.ServiceObjectCandidatesFromFrozenFacts(materialID, materialVersion, documentID, parsedDocumentID, runID, attemptID, facts)
}
func planServiceObjectReviewContext(at time.Time, sources []serviceObjectReviewContextSource, candidates []ServiceObjectCandidateEvidence) (ServiceObjectReviewContext, error) {
	var inputs []operatorModel.ContextSource
	for _, source := range sources {
		item := operatorModel.ContextSource{Head: source.Head, Version: source.Version, Association: source.Association}
		if source.Mapping != nil {
			m := source.Mapping
			item.Mapping = &operatorModel.ContextMappingPin{MappingID: m.MappingID, MappingVersion: m.MappingVersion, ServiceObjectID: m.ServiceObjectID, ObjectVersion: m.ObjectVersion, AssociationVersion: m.AssociationVersion, BindingVersion: m.Binding.Version, Status: m.Binding.Status, EffectiveFrom: m.Binding.EffectiveFrom, EffectiveUntil: m.Binding.EffectiveUntil}
		}
		inputs = append(inputs, item)
	}
	return operatorModel.PlanServiceObjectReviewContext(at, inputs, candidates)
}

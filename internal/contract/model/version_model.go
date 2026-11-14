// File metadata (manifest mirror):
// Purpose: Defines frozen Contract revision, material, seal, plan-index and legacy provenance models with stable wire and digest field order.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contract

import (
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/fault"
)

var revisionPlanQuantityPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(?:\.[0-9]{1,6})?$`)

// ContractVersionSummary is the public, restricted-field-safe projection of a
// frozen revision. Field order and JSON names are kept compatible with Store.
type ContractVersionSummary struct {
	ContractRevisionID string    `json:"contract_revision_id"`
	RevisionNumber     int       `json:"revision_number"`
	Amount             *string   `json:"amount"`
	Currency           *string   `json:"currency"`
	SnapshotDigest     string    `json:"snapshot_digest"`
	MaterialsDigest    string    `json:"materials_digest,omitempty"`
	ContentFingerprint string    `json:"content_fingerprint,omitempty"`
	AmountsReconciled  bool      `json:"amounts_reconciled"`
	MaterialCount      int       `json:"material_count"`
	FrozenBy           string    `json:"frozen_by"`
	FrozenAt           time.Time `json:"frozen_at"`
	AuditState         string    `json:"audit_state"`
	AuditGateState     string    `json:"audit_gate_state"`
}

type FreezeContractVersionInput struct {
	ContractID         string
	ExpectedRowVersion int64
	CaseID             string
	IdempotencyKey     string
}

type FreezeContractVersionResult struct {
	Version           ContractVersionSummary `json:"version"`
	ContractState     string                 `json:"contract_state"`
	CurrentRevisionID string                 `json:"current_revision_id"`
	SourceCaseID      string                 `json:"source_case_id"`
	RowVersion        int64                  `json:"row_version"`
	AllowedCommands   []string               `json:"allowed_commands"`
}

type ContractVersionList struct {
	Items []ContractVersionSummary `json:"items"`
}

type ContractVersionMaterial struct {
	MaterialID        string `json:"material_id"`
	MaterialVersionID string `json:"material_version_id"`
	MaterialVersion   int    `json:"material_version"`
	Filename          string `json:"filename"`
	Digest            string `json:"digest"`
	Purpose           string `json:"purpose"`
	CaseID            string `json:"case_id"`
	CaseOrgID         string `json:"case_org_id"`
}

type ContractVersionDetail struct {
	Version   ContractVersionSummary    `json:"version"`
	Draft     *DraftContent             `json:"draft,omitempty"`
	Materials []ContractVersionMaterial `json:"materials"`
}

// FrozenContractRevision and its children are raw Contract-owned persistence
// rows. The raw material struct deliberately keeps its historical field order
// and default JSON names because the materials seal hashes this exact shape.
type FrozenContractRevision struct {
	RevisionID        string
	RevisionNumber    int
	Amount            *string
	Currency          *string
	Snapshot          []byte
	SnapshotDigest    string
	AmountsReconciled bool
	FrozenBy          string
	FrozenAt          time.Time
}

type FrozenVersionMaterial struct {
	MaterialID        string
	MaterialVersionID string
	CaseOrgID         string
	CaseID            string
	MaterialVersion   int
	Filename          string
	Digest            string
	Purpose           string
}

type ContractVersionSeal struct {
	SnapshotDigest         string
	MaterialsDigest        string
	ContentFingerprint     string
	RestrictedFingerprints []byte
}

type ResolvedVersionMaterial struct {
	MaterialID        string
	MaterialVersionID string
	MaterialVersion   int
	Filename          string
	Digest            string
	Purpose           string
}

// FrozenMaterialsFromReferences is the sole material snapshot projection for
// a resolved source case. The uniqueness rule avoids a late primary-key error
// when one contract cites two versions of a material.
func FrozenMaterialsFromReferences(caseOrgID, caseID string, refs []ResolvedVersionMaterial) ([]FrozenVersionMaterial, error) {
	seen := make(map[string]bool, len(refs))
	materials := make([]FrozenVersionMaterial, 0, len(refs))
	for _, ref := range refs {
		if seen[ref.MaterialID] {
			return nil, fault.Invalid("同一材料在冻结合同中只能引用一个版本")
		}
		seen[ref.MaterialID] = true
		materials = append(materials, FrozenVersionMaterial{
			MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID,
			CaseOrgID: caseOrgID, CaseID: caseID, MaterialVersion: ref.MaterialVersion,
			Filename: ref.Filename, Digest: ref.Digest, Purpose: ref.Purpose,
		})
	}
	sort.Slice(materials, func(i, j int) bool {
		if materials[i].MaterialID != materials[j].MaterialID {
			return materials[i].MaterialID < materials[j].MaterialID
		}
		return materials[i].MaterialVersionID < materials[j].MaterialVersionID
	})
	return materials, nil
}

// RevisionPlanItem is the immutable index Contract writes for Fulfillment.
type RevisionPlanItem struct {
	ItemID          string
	PlannedQuantity *string
	Unit            *string
}

// FrozenRevisionPlanItems projects validated snapshot lines into the
// append-only Contract revision index. Invalid or repeated identities remain
// in the snapshot but intentionally have no unique plan index row.
func FrozenRevisionPlanItems(items []DraftItem) []RevisionPlanItem {
	counts := make(map[string]int, len(items))
	ids := make([]string, len(items))
	for i, item := range items {
		if id, err := NormalizeContractUUID(item.ItemID); err == nil {
			ids[i] = id
			counts[id]++
		}
	}
	result := make([]RevisionPlanItem, 0, len(items))
	for i, item := range items {
		id := ids[i]
		if id == "" || counts[id] != 1 {
			continue
		}
		var quantity *string
		trimmedQuantity := strings.TrimSpace(item.Quantity)
		parsedQuantity, parsed := new(big.Rat).SetString(trimmedQuantity)
		if revisionPlanQuantityPattern.MatchString(trimmedQuantity) && parsed && parsedQuantity.Sign() > 0 {
			value := item.Quantity
			quantity = &value
		}
		var unit *string
		trimmedUnit := strings.TrimSpace(item.Unit)
		if trimmedUnit != "" && runeCount(trimmedUnit) <= 40 {
			value := trimmedUnit
			unit = &value
		}
		result = append(result, RevisionPlanItem{ItemID: id, PlannedQuantity: quantity, Unit: unit})
	}
	return result
}

// LegacyAmountPair preserves unknown amount/currency as a nullable pair for
// imported historical revisions and source observations.
func LegacyAmountPair(snapshot DraftContent) (*string, *string) {
	if snapshot.Contract == nil || snapshot.Contract.Amount == "" || snapshot.Contract.Currency == "" {
		return nil, nil
	}
	amount, currency := snapshot.Contract.Amount, snapshot.Contract.Currency
	return &amount, &currency
}

// LegacyImportInput registers one already-signed historical contract. It
// intentionally keeps the original field order and default JSON field names
// used by the durable idempotency digest.
type LegacyImportInput struct {
	ExpectedRowVersion          int64
	AuthorityMode               string
	SourceRef                   SourceRef
	SourceEvidence              MaterialRef
	Snapshot                    DraftContent
	ApprovalHistoryStatus       string
	HistoricalApprovalMaterials []MaterialRef
	Reason                      string
	IdempotencyKey              string
}

type HistoricalApprovalMaterial struct {
	MaterialID        string `json:"material_id"`
	MaterialVersionID string `json:"material_version_id"`
	SHA256            string `json:"sha256"`
	Purpose           string `json:"purpose"`
	CaseOrgID         string `json:"case_org_id"`
	CaseID            string `json:"case_id"`
	MaterialVersion   int    `json:"material_version"`
}

type LegacySourceEvidence struct {
	CaseOrgID       string
	CaseID          string
	MaterialID      string
	MaterialVersion int
	Digest          string
}

type LegacyImportFacts struct {
	SourceCaseOrgID string
	SourceCaseID    string
	SourceEvidence  LegacySourceEvidence
	Historical      []HistoricalApprovalMaterial
}

type VersionReferenceFacts struct {
	SourceCaseOrgID string
	SourceCaseID    string
	Materials       []ResolvedVersionMaterial
}

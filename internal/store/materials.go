package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func uploadRequestHash(operation, actorID, caseID, role, purpose, filename, media string, body []byte, options MaterialUploadOptions) [32]byte {
	content := sha256.Sum256(body)
	intent := options.Intent
	if intent == "" {
		intent = "add"
	}
	canonical := struct {
		Operation          string   `json:"operation"`
		ActorID            string   `json:"actor_id"`
		CaseID             string   `json:"case_id"`
		Intent             string   `json:"intent"`
		Role               string   `json:"role"`
		Purpose            string   `json:"purpose"`
		MaterialID         string   `json:"material_id"`
		ReplacesDocumentID string   `json:"replaces_document_id"`
		Filename           string   `json:"filename"`
		Media              string   `json:"media"`
		ContentDigest      [32]byte `json:"content_digest"`
	}{operation, actorID, caseID, intent, role, purpose, options.MaterialID, options.ReplacesDocumentID, filename, media, content}
	encoded, _ := json.Marshal(canonical)
	return sha256.Sum256(encoded)
}

// maxMaterialsPerCase bounds document parsing, storage and review admission
// without imposing the legacy four-role limit on repeated same-purpose items.
const maxMaterialsPerCase = 64

// MaterialUploadOptions is the explicit command for adding or replacing one
// material. Empty Intent is normalized to add at the API boundary.
type MaterialUploadOptions struct {
	Intent             string
	MaterialID         string
	ReplacesDocumentID string
	Purpose            string
	IdempotencyKey     string
}

type MaterialEntity struct {
	TenantID          string    `json:"tenant_id"`
	OrgID             string    `json:"org_id"`
	CaseID            string    `json:"case_id"`
	ID                string    `json:"id"`
	Role              string    `json:"role"`
	Purpose           string    `json:"purpose"`
	LegacyStatus      string    `json:"legacy_status"`
	LegacySelected    bool      `json:"legacy_selected"`
	CurrentDocumentID string    `json:"current_document_id"`
	CurrentVersion    int       `json:"current_version"`
	CreatedBy         string    `json:"created_by"`
	CreatedAt         time.Time `json:"created_at"`
}

type MaterialVersion struct {
	TenantID         string    `json:"tenant_id"`
	OrgID            string    `json:"org_id"`
	CaseID           string    `json:"case_id"`
	MaterialID       string    `json:"material_id"`
	Version          int       `json:"version"`
	DocumentID       string    `json:"document_id"`
	PreviousDocument string    `json:"previous_document_id,omitempty"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
}

type UploadIdempotencyRecord struct {
	TenantID    string    `json:"tenant_id"`
	OrgID       string    `json:"org_id"`
	CaseID      string    `json:"case_id"`
	ActorID     string    `json:"actor_id"`
	Key         string    `json:"key"`
	RequestHash []byte    `json:"request_hash"`
	DocumentID  string    `json:"document_id"`
	MaterialID  string    `json:"material_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type DocumentUploadResult struct {
	DocumentID string `json:"id"`
	MaterialID string `json:"material_id"`
	Version    int    `json:"version"`
	Replayed   bool   `json:"replayed"`
}

type CaseMaterialChain struct {
	TenantID                 string    `json:"tenant_id"`
	OrgID                    string    `json:"org_id"`
	CaseID                   string    `json:"case_id"`
	PrimaryMaterialID        *string   `json:"primary_material_id"`
	LegacySelectedDocumentID *string   `json:"legacy_selected_document_id"`
	LegacySelectionStatus    string    `json:"status"`
	SelectedDocumentIDs      []string  `json:"selected_document_ids"`
	SelectionVersion         int64     `json:"version"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

func resolveMaterialPurpose(role, purpose string) (string, error) {
	role, purpose = strings.TrimSpace(role), strings.TrimSpace(purpose)
	switch role {
	case "procurement":
		if purpose != "" && purpose != "procurement" {
			return "", errors.New("purpose is fixed for procurement role")
		}
		return "procurement", nil
	case "award":
		if purpose != "" && purpose != "award" {
			return "", errors.New("purpose is fixed for award role")
		}
		return "award", nil
	case "draft_contract":
		if purpose != "" && purpose != "primary_contract" {
			return "", errors.New("purpose is fixed for draft_contract role")
		}
		return "primary_contract", nil
	case "attachment":
		if purpose == "" {
			purpose = "other"
		}
		switch purpose {
		case "supplement", "acceptance", "invoice", "correspondence", "other":
			return purpose, nil
		default:
			return "", errors.New("attachment purpose is invalid")
		}
	default:
		return "", fmt.Errorf("unsupported material role %q", role)
	}
}

func validateMaterialUploadOptions(options MaterialUploadOptions) error {
	intent := options.Intent
	if intent == "" {
		intent = "add"
	}
	switch intent {
	case "add":
		if options.MaterialID != "" || options.ReplacesDocumentID != "" {
			return errors.New("add intent cannot name a replacement target")
		}
	case "replace":
		if strings.TrimSpace(options.MaterialID) == "" || strings.TrimSpace(options.ReplacesDocumentID) == "" {
			return errors.New("replace intent requires material_id and replaces_document_id")
		}
	default:
		return errors.New("intent must be add or replace")
	}
	return nil
}

// validatePrimaryMaterialMutation keeps the case's stable main-contract chain
// attached to the one chosen entity. A review collection may omit that entity,
// but another legacy primary cannot take its place through upload or generation.
func validatePrimaryMaterialMutation(st *state, caseID, intent string, target *MaterialEntity) error {
	var chain *CaseMaterialChain
	for i := range st.MaterialChains {
		if st.MaterialChains[i].CaseID == caseID {
			chain = &st.MaterialChains[i]
			break
		}
	}
	if chain != nil && chain.PrimaryMaterialID != nil {
		if intent == "add" {
			return fault.PrimaryContractExists()
		}
		if target == nil || target.ID != *chain.PrimaryMaterialID {
			return fault.Conflict("案件主合同链已建立，不能替换其他主合同材料")
		}
		return nil
	}
	if intent == "replace" && target != nil && chain != nil && chain.LegacySelectionStatus == "pending_confirmation" && !target.LegacySelected {
		return fault.Conflict("历史材料集合待确认；只有候选主合同可以建立主合同链")
	}
	if intent == "add" {
		for i := range st.Materials {
			item := &st.Materials[i]
			if item.CaseID == caseID && item.Purpose == "primary_contract" && (item.LegacySelected || item.LegacyStatus == "native" || item.LegacyStatus == "confirmed") {
				return fault.PrimaryContractExists()
			}
		}
	}
	return nil
}

func (s *Store) MaterialIdentity(ctx context.Context, user accounts.User, caseID, documentID string) (string, int, error) {
	current, _, err := s.documentWriteCase(ctx, user, caseID, access.Manage)
	if err != nil {
		return "", 0, err
	}
	st, err := s.snapshotScoped(ctx, userScope(current))
	if err != nil {
		return "", 0, err
	}
	if _, err := s.writableCase(&st, current, caseID, access.Manage); err != nil {
		return "", 0, err
	}
	for _, version := range st.MaterialVersions {
		if version.CaseID == caseID && version.DocumentID == documentID {
			return version.MaterialID, version.Version, nil
		}
	}
	return "", 0, fault.NotFound("没有找到对应材料")
}

// MaterialProjection is scoped to an authorized case response and excludes
// tenant and organization identifiers.
type MaterialProjection struct {
	MaterialID        string `json:"material_id"`
	Role              string `json:"role"`
	Purpose           string `json:"purpose"`
	LegacyStatus      string `json:"legacy_status"`
	LegacySelected    bool   `json:"legacy_selected"`
	Selected          bool   `json:"selected"`
	CurrentDocumentID string `json:"current_document_id"`
	CurrentVersion    int    `json:"current_version"`
}

type MaterialVersionProjection struct {
	MaterialID         string    `json:"material_id"`
	Version            int       `json:"version"`
	DocumentID         string    `json:"document_id"`
	PreviousDocumentID *string   `json:"previous_document_id"`
	CreatedAt          time.Time `json:"created_at"`
}

type MaterialSelectionProjection struct {
	Version              int64    `json:"version"`
	Status               string   `json:"status"`
	DocumentIDs          []string `json:"document_ids"`
	CandidateDocumentIDs []string `json:"candidate_document_ids"`
	PrimaryMaterialID    *string  `json:"primary_material_id"`
}

type MaterialSelectionInput struct {
	Version     int64    `json:"version"`
	DocumentIDs []string `json:"document_ids"`
}

func materialSelectionPending(st state, caseID string) bool {
	for _, chain := range st.MaterialChains {
		if chain.CaseID == caseID && chain.LegacySelectionStatus == "pending_confirmation" {
			return true
		}
	}
	return false
}

func casePrimaryMaterialID(st state, caseID string) string {
	for _, chain := range st.MaterialChains {
		if chain.CaseID == caseID {
			if chain.PrimaryMaterialID == nil {
				return ""
			}
			return *chain.PrimaryMaterialID
		}
	}
	for _, doc := range selectedMaterialDocuments(st, caseID) {
		if doc.Role != "draft_contract" {
			continue
		}
		for _, material := range st.Materials {
			if material.CaseID == caseID && material.CurrentDocumentID == doc.ID && material.Purpose == "primary_contract" {
				return material.ID
			}
		}
	}
	return ""
}

func bindFrozenMaterialIdentity(st state, frozen *FrozenDocument) {
	if frozen == nil || frozen.Document.ID == "" {
		return
	}
	frozen.Material = nil
	for _, version := range st.MaterialVersions {
		if version.DocumentID != frozen.Document.ID {
			continue
		}
		selection := &FrozenMaterialSelection{MaterialID: version.MaterialID, Version: version.Version}
		for _, material := range st.Materials {
			if material.CaseID == version.CaseID && material.ID == version.MaterialID {
				selection.Role = material.Role
				selection.Purpose = material.Purpose
				break
			}
		}
		frozen.Material = selection
		return
	}
}

func (s *Store) ConfirmMaterialSelection(ctx context.Context, user accounts.User, caseID string, expectedVersion int64, documentIDs []string) error {
	current, caseItem, err := s.documentWriteCase(ctx, user, caseID, access.Manage)
	if err != nil {
		return err
	}
	if expectedVersion < 1 || len(documentIDs) == 0 {
		return fault.Invalid("材料集合版本和材料列表必填")
	}
	return s.scopedCaseMutation(ctx, current, caseID, func(st *state) error {
		if _, err := s.writableCase(st, current, caseID, access.Manage); err != nil {
			return err
		}
		var chain *CaseMaterialChain
		for i := range st.MaterialChains {
			if st.MaterialChains[i].CaseID == caseID && st.MaterialChains[i].TenantID == caseItem.TenantID && st.MaterialChains[i].OrgID == caseItem.OwnerOrgID {
				chain = &st.MaterialChains[i]
				break
			}
		}
		if chain == nil {
			return fault.NotFound("没有找到材料集合")
		}
		if chain.SelectionVersion != expectedVersion {
			return fault.Conflict("材料集合版本已变化，请刷新后重试")
		}
		seen := map[string]bool{}
		var primary *string
		for _, docID := range documentIDs {
			if seen[docID] {
				return fault.Invalid("材料列表包含重复文档")
			}
			seen[docID] = true
			found := false
			for i := range st.Materials {
				entity := &st.Materials[i]
				if entity.CaseID != caseID || entity.TenantID != caseItem.TenantID || entity.OrgID != caseItem.OwnerOrgID || entity.CurrentDocumentID != docID {
					continue
				}
				found = true
				if entity.LegacyStatus == "pending_confirmation" {
					entity.LegacyStatus = "confirmed"
				}
				if entity.Purpose == "primary_contract" {
					if primary != nil {
						return fault.Invalid("材料集合只能选择一份主合同")
					}
					id := entity.ID
					primary = &id
				}
				break
			}
			if !found {
				return fault.NotFound("没有找到所选案件材料")
			}
		}
		if chain.PrimaryMaterialID != nil && primary != nil && *chain.PrimaryMaterialID != *primary {
			return fault.Conflict("案件主合同链已建立，不能改绑其他主合同；请另行建案或显式替换当前主合同")
		}
		chain.SelectedDocumentIDs = append([]string(nil), documentIDs...)
		if chain.PrimaryMaterialID == nil && primary != nil {
			chain.PrimaryMaterialID = primary
		}
		chain.LegacySelectionStatus = "confirmed"
		chain.SelectionVersion++
		chain.UpdatedAt = time.Now().UTC()
		return nil
	})
}

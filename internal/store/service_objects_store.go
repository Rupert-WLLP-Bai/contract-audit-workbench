package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
	"github.com/jackc/pgx/v5"
)

type ServiceObjectCreateInput struct {
	ObjectType   ServiceObjectType    `json:"object_type"`
	DisplayName  string               `json:"display_name"`
	BusinessCode string               `json:"business_code,omitempty"`
	Aliases      []ServiceObjectAlias `json:"aliases"`
	BasisSummary string               `json:"basis_summary,omitempty"`
}

type ServiceObjectMutationResult struct {
	ServiceObjectID    string `json:"service_object_id"`
	Version            int    `json:"version"`
	AssociationVersion int    `json:"association_version"`
}

type ServiceObjectIdentityHistory struct {
	Head         ServiceObjectHead                  `json:"head"`
	Versions     []ServiceObjectVersion             `json:"versions"`
	Associations []ServiceObjectContractAssociation `json:"associations"`
}

type ServiceObjectAssociationPin struct {
	AssociationVersion int       `json:"association_version"`
	ObjectVersion      int       `json:"object_version"`
	MaterialID         string    `json:"material_id"`
	MaterialVersion    int       `json:"material_version"`
	DocumentID         string    `json:"document_id"`
	ParsedDocumentID   string    `json:"parsed_document_id"`
	SourceRunID        string    `json:"source_run_id"`
	SourceAttemptID    string    `json:"source_attempt_id"`
	ObjectRef          string    `json:"object_ref"`
	FactsSHA256        string    `json:"facts_sha256"`
	ConfirmedBy        string    `json:"confirmed_by"`
	ConfirmedAt        time.Time `json:"confirmed_at"`
}

type ServiceObjectSummary struct {
	ServiceObjectID           string                       `json:"service_object_id"`
	ObjectType                ServiceObjectType            `json:"object_type"`
	State                     ServiceObjectState           `json:"state"`
	CurrentVersion            int                          `json:"current_version"`
	CurrentAssociationVersion int                          `json:"current_association_version"`
	DisplayName               string                       `json:"display_name"`
	BusinessCode              string                       `json:"business_code"`
	Aliases                   []ServiceObjectAlias         `json:"aliases"`
	CurrentAssociation        *ServiceObjectAssociationPin `json:"current_association"`
	CanManage                 bool                         `json:"can_manage"`
	NeedsReassociation        bool                         `json:"needs_reassociation"`
}

type ServiceObjectDetail struct {
	ServiceObjectID    string                            `json:"service_object_id"`
	CurrentVersion     int                               `json:"current_version"`
	Identity           ServiceObjectVersion              `json:"identity"`
	Association        *ServiceObjectContractAssociation `json:"association"`
	CanManage          bool                              `json:"can_manage"`
	NeedsReassociation bool                              `json:"needs_reassociation"`
}

type ServiceObjectListResult struct {
	Items      []ServiceObjectSummary `json:"items"`
	NextCursor *string                `json:"next_cursor"`
	CanManage  bool                   `json:"can_manage"`
}

type ServiceObjectVersionPage struct {
	Items      []ServiceObjectVersion `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

type ServiceObjectAssociationPage struct {
	Items      []ServiceObjectContractAssociation `json:"items"`
	NextCursor *string                            `json:"next_cursor"`
}

type ServiceObjectAssociationOption struct {
	ObjectRef   string                 `json:"object_ref"`
	Facts       []ServiceObjectFactRef `json:"facts"`
	FactsSHA256 string                 `json:"facts_sha256"`
	BasisRefs   json.RawMessage        `json:"basis_refs"`
}

type ServiceObjectAssociationPreview struct {
	ServiceObjectID            string                           `json:"service_object_id"`
	ExpectedObjectVersion      int                              `json:"expected_object_version"`
	ExpectedAssociationVersion int                              `json:"expected_association_version"`
	RunID                      string                           `json:"run_id"`
	AttemptID                  string                           `json:"attempt_id"`
	MaterialID                 string                           `json:"material_id"`
	MaterialVersion            int                              `json:"material_version"`
	DocumentID                 string                           `json:"document_id"`
	ParsedDocumentID           string                           `json:"parsed_document_id"`
	CandidatesSHA256           string                           `json:"candidate_sha256"`
	Options                    []ServiceObjectAssociationOption `json:"options"`
}

// CreateServiceObject creates only the pending identity head/version. The first
// contract association is a separate explicit confirmation against a frozen
// review attempt.
func (s *Store) CreateServiceObject(ctx context.Context, user accounts.User, caseID string, input ServiceObjectCreateInput, key string) (ServiceObjectMutationResult, bool, error) {
	var empty ServiceObjectMutationResult
	caseID = strings.TrimSpace(caseID)
	if err := s.requireServiceObjectCoreWritable(ctx); err != nil {
		return empty, false, err
	}
	current, caseItem, tx, err := s.serviceObjectMutationTx(ctx, user, caseID)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback(ctx)
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	version, err := normalizeServiceObjectVersion(ServiceObjectVersion{
		ObjectType: input.ObjectType, DisplayName: input.DisplayName, BusinessCode: input.BusinessCode,
		Aliases: input.Aliases, State: ServiceObjectPendingConfirmation, BasisSummary: input.BasisSummary,
		BasisRefs: json.RawMessage(`[]`), CreatedBy: current.ID, CreatedAt: now, EffectiveFrom: now,
	})
	if err != nil {
		return empty, false, fault.Invalid("服务对象身份资料无效")
	}
	canonicalInput := ServiceObjectCreateInput{ObjectType: version.ObjectType, DisplayName: version.DisplayName,
		BusinessCode: version.BusinessCode, Aliases: version.Aliases, BasisSummary: version.BasisSummary}
	requestHash, err := serviceObjectRequestSHA256(accounts.Tenant(current), caseItem.OwnerOrgID, caseID, current.ID, "create_object", canonicalInput)
	if err != nil {
		return empty, false, err
	}
	var result ServiceObjectMutationResult
	if replayed, err := readServiceObjectReceipt(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, current.ID, "create_object", key, requestHash, &result); err != nil || replayed {
		if err != nil {
			return empty, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return empty, false, err
		}
		return result, true, nil
	}
	if err := businessWritable(caseItem); err != nil {
		return empty, false, err
	}
	var objectCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseID).Scan(&objectCount); err != nil {
		return empty, false, err
	}
	if objectCount >= 100 {
		return empty, false, fault.Invalid("每个案件最多登记100个服务对象")
	}
	objectID, err := newID("svc_")
	if err != nil {
		return empty, false, err
	}
	version.ServiceObjectID, version.Version = objectID, 1
	version.PayloadSHA256, err = canonicalServiceObjectIdentitySHA256(version)
	if err != nil {
		return empty, false, err
	}
	aliasesJSON, err := json.Marshal(version.Aliases)
	if err != nil {
		return empty, false, err
	}
	aliases := json.RawMessage(aliasesJSON)
	result = ServiceObjectMutationResult{ServiceObjectID: objectID, Version: 1, AssociationVersion: 0}
	body, err := json.Marshal(result)
	if err != nil {
		return empty, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO service_objects
		(tenant_id,org_id,case_id,service_object_id,current_version,created_by,created_at)
		VALUES ($1,$2,$3,$4,1,$5,$6)`, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, objectID, current.ID, now); err != nil {
		return empty, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO service_object_versions
		(tenant_id,org_id,case_id,service_object_id,version,object_type,display_name,business_code,aliases,state,effective_from,basis_refs,basis_summary,payload_sha256,created_by,created_at)
		VALUES ($1,$2,$3,$4,1,$5,$6,NULLIF($7,''),$8::jsonb,'pending_confirmation',$9,'[]'::jsonb,NULLIF($10,''),$11,$12,$13)`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseID, objectID, version.ObjectType, version.DisplayName, version.BusinessCode,
		aliases, version.EffectiveFrom, version.BasisSummary, version.PayloadSHA256, current.ID, now); err != nil {
		return empty, false, err
	}
	if err := insertServiceObjectReceipt(ctx, tx, current, caseItem, "create_object", key, requestHash, 201, body, objectID, now); err != nil {
		return empty, false, err
	}
	if err := recheckServiceObjectManage(ctx, tx, current, caseItem); err != nil {
		return empty, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, false, err
	}
	return result, false, nil
}

func (s *Store) ListServiceObjects(ctx context.Context, user accounts.User, caseID, cursor string, limit int) (ServiceObjectListResult, error) {
	caseID = strings.TrimSpace(caseID)
	limit, err := serviceObjectPageLimit(limit)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	lastID, err := decodeServiceObjectCursor(cursor, "objects", caseID, "")
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	rows, err := tx.Query(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id>$4
		ORDER BY service_object_id LIMIT $5`, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, lastID, limit+1)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	heads := make([]ServiceObjectHead, 0)
	for rows.Next() {
		var head ServiceObjectHead
		if err := rows.Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion); err != nil {
			rows.Close()
			return ServiceObjectListResult{}, err
		}
		heads = append(heads, head)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ServiceObjectListResult{}, err
	}
	rows.Close()
	pageHeads := heads
	var next *string
	if len(pageHeads) > limit {
		pageHeads = pageHeads[:limit]
		value := encodeServiceObjectCursor("objects", caseID, "", pageHeads[len(pageHeads)-1].ID)
		next = &value
	}
	canManage := serviceObjectCanManage(s, current, caseItem)
	result := ServiceObjectListResult{Items: make([]ServiceObjectSummary, 0, len(pageHeads)), NextCursor: next, CanManage: canManage}
	for _, head := range pageHeads {
		version, err := loadServiceObjectVersionValue(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, head)
		if err != nil {
			return ServiceObjectListResult{}, err
		}
		var association *ServiceObjectAssociationPin
		if head.CurrentAssociationVersion > 0 {
			currentAssociation, err := loadServiceObjectAssociation(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, head.ID, head.CurrentAssociationVersion)
			if err != nil {
				return ServiceObjectListResult{}, err
			}
			pin := associationPin(currentAssociation)
			association = &pin
		}
		result.Items = append(result.Items, ServiceObjectSummary{
			ServiceObjectID: head.ID, ObjectType: version.ObjectType, State: version.State, CurrentVersion: head.CurrentVersion,
			CurrentAssociationVersion: head.CurrentAssociationVersion, DisplayName: version.DisplayName, BusinessCode: version.BusinessCode,
			Aliases: version.Aliases, CurrentAssociation: association, CanManage: canManage,
			NeedsReassociation: serviceObjectNeedsReassociation(st, caseID, association),
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectListResult{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObjectHistory(ctx context.Context, user accounts.User, caseID, objectID string) (ServiceObjectIdentityHistory, error) {
	caseID = strings.TrimSpace(caseID)
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	defer tx.Rollback(ctx)
	var head ServiceObjectHead
	err = tx.QueryRow(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseID, strings.TrimSpace(objectID)).Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceObjectIdentityHistory{}, fault.NotFound("没有找到服务对象")
	}
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	result, err := loadServiceObjectHistory(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, head)
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObject(ctx context.Context, user accounts.User, caseID, objectID string) (ServiceObjectDetail, error) {
	caseID, objectID = strings.TrimSpace(caseID), strings.TrimSpace(objectID)
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	defer tx.Rollback(ctx)
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, objectID)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	version, err := loadServiceObjectVersionValue(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, head)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	var association *ServiceObjectContractAssociation
	if head.CurrentAssociationVersion > 0 {
		currentAssociation, err := loadServiceObjectAssociation(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, objectID, head.CurrentAssociationVersion)
		if err != nil {
			return ServiceObjectDetail{}, err
		}
		association = &currentAssociation
	}
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	result := ServiceObjectDetail{ServiceObjectID: head.ID, CurrentVersion: head.CurrentVersion, Identity: version,
		Association: association, CanManage: serviceObjectCanManage(s, current, caseItem),
		NeedsReassociation: serviceObjectNeedsReassociation(st, caseID, associationPinPointer(association))}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectDetail{}, err
	}
	return result, nil
}

func (s *Store) ListServiceObjectVersions(ctx context.Context, user accounts.User, caseID, objectID, cursor string, limit int) (ServiceObjectVersionPage, error) {
	limit, err := serviceObjectPageLimit(limit)
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	caseID, objectID = strings.TrimSpace(caseID), strings.TrimSpace(objectID)
	lastVersion, err := decodeServiceObjectIntegerCursor(cursor, "versions", caseID, objectID)
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	defer tx.Rollback(ctx)
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT version FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND version<$5 ORDER BY version DESC LIMIT $6`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, head.ID, cursorIntOrMax(lastVersion), limit+1)
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	versions := make([]int, 0, limit+1)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return ServiceObjectVersionPage{}, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ServiceObjectVersionPage{}, err
	}
	rows.Close()
	var next *string
	if len(versions) > limit {
		versions = versions[:limit]
		value := encodeServiceObjectIntegerCursor("versions", caseItem.ID, head.ID, versions[len(versions)-1])
		next = &value
	}
	result := ServiceObjectVersionPage{Items: make([]ServiceObjectVersion, 0, len(versions)), NextCursor: next}
	for _, number := range versions {
		item, err := loadServiceObjectVersionExact(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, head.ID, number)
		if err != nil {
			return ServiceObjectVersionPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectVersionPage{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObjectVersion(ctx context.Context, user accounts.User, caseID, objectID string, version int) (ServiceObjectVersion, error) {
	if version < 1 {
		return ServiceObjectVersion{}, fault.Invalid("服务对象版本无效")
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	defer tx.Rollback(ctx)
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	item, err := loadServiceObjectVersionExact(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, head.ID, version)
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectVersion{}, err
	}
	return item, nil
}

func (s *Store) ListServiceObjectAssociations(ctx context.Context, user accounts.User, caseID, objectID, cursor string, limit int) (ServiceObjectAssociationPage, error) {
	limit, err := serviceObjectPageLimit(limit)
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	caseID, objectID = strings.TrimSpace(caseID), strings.TrimSpace(objectID)
	lastVersion, err := decodeServiceObjectIntegerCursor(cursor, "associations", caseID, objectID)
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	defer tx.Rollback(ctx)
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT association_version FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND association_version<$5 ORDER BY association_version DESC LIMIT $6`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, head.ID, cursorIntOrMax(lastVersion), limit+1)
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	versions := make([]int, 0, limit+1)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return ServiceObjectAssociationPage{}, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ServiceObjectAssociationPage{}, err
	}
	rows.Close()
	var next *string
	if len(versions) > limit {
		versions = versions[:limit]
		value := encodeServiceObjectIntegerCursor("associations", caseItem.ID, head.ID, versions[len(versions)-1])
		next = &value
	}
	result := ServiceObjectAssociationPage{Items: make([]ServiceObjectContractAssociation, 0, len(versions)), NextCursor: next}
	for _, number := range versions {
		item, err := loadServiceObjectAssociation(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, head.ID, number)
		if err != nil {
			return ServiceObjectAssociationPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObjectAssociation(ctx context.Context, user accounts.User, caseID, objectID string, associationVersion int) (ServiceObjectContractAssociation, error) {
	if associationVersion < 1 {
		return ServiceObjectContractAssociation{}, fault.Invalid("服务对象关联版本无效")
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	defer tx.Rollback(ctx)
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	item, err := loadServiceObjectAssociation(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, head.ID, associationVersion)
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	return item, nil
}

func (s *Store) PreviewServiceObjectAssociation(ctx context.Context, user accounts.User, caseID, objectID string, input ServiceObjectAssociationPreviewInput) (ServiceObjectAssociationPreview, error) {
	caseID = strings.TrimSpace(caseID)
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	preview, err := deriveServiceObjectAssociationPreview(s, st, current, caseItem, head, input)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	return preview, nil
}

func (s *Store) ConfirmServiceObjectAssociation(ctx context.Context, user accounts.User, caseID, objectID, key string, input ServiceObjectAssociationConfirmInput) (ServiceObjectMutationResult, bool, error) {
	var empty ServiceObjectMutationResult
	caseID = strings.TrimSpace(caseID)
	if err := s.requireServiceObjectCoreWritable(ctx); err != nil {
		return empty, false, err
	}
	current, caseItem, tx, err := s.serviceObjectMutationTx(ctx, user, caseID)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback(ctx)
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	objectID = strings.TrimSpace(objectID)
	caseID = strings.TrimSpace(caseID)
	input.ObjectRef = strings.TrimSpace(input.ObjectRef)
	input.Source.RunID = strings.TrimSpace(input.Source.RunID)
	input.Source.AttemptID = strings.TrimSpace(input.Source.AttemptID)
	input.Source.MaterialID = strings.TrimSpace(input.Source.MaterialID)
	input.Source.DocumentID = strings.TrimSpace(input.Source.DocumentID)
	input.Source.ParsedDocumentID = strings.TrimSpace(input.Source.ParsedDocumentID)
	if objectID == "" || !input.Confirmed || input.ExpectedObjectVersion < 1 || input.ExpectedAssociationVersion < 0 || input.ObjectRef == "" ||
		!validServiceObjectSHA256(input.FactsSHA256) || !validServiceObjectSHA256(input.CandidatesSHA256) {
		return empty, false, fault.Invalid("服务对象关联版本或证据标识无效")
	}
	requestHash, err := serviceObjectRequestSHA256(accounts.Tenant(current), caseItem.OwnerOrgID, caseID, current.ID, "confirm_association", struct {
		ServiceObjectID string                               `json:"service_object_id"`
		Input           ServiceObjectAssociationConfirmInput `json:"input"`
	}{objectID, input})
	if err != nil {
		return empty, false, err
	}
	var result ServiceObjectMutationResult
	if replayed, err := readServiceObjectReceipt(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, current.ID, "confirm_association", key, requestHash, &result); err != nil || replayed {
		if err != nil {
			return empty, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return empty, false, err
		}
		return result, true, nil
	}
	if err := businessWritable(caseItem); err != nil {
		return empty, false, err
	}
	st, err := loadState(ctx, tx, true)
	if err != nil {
		return empty, false, err
	}
	var head ServiceObjectHead
	err = tx.QueryRow(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 FOR UPDATE`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseID, objectID).Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, false, fault.NotFound("没有找到服务对象")
	}
	if err != nil {
		return empty, false, err
	}
	var currentVersion ServiceObjectVersion
	if err := loadServiceObjectVersion(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, head, &currentVersion); err != nil {
		return empty, false, err
	}
	preview, err := deriveServiceObjectAssociationPreview(s, st, current, caseItem, head, input.Source)
	if err != nil {
		return empty, false, err
	}
	var option *ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == input.ObjectRef {
			option = &preview.Options[i]
			break
		}
	}
	if option == nil || option.FactsSHA256 != input.FactsSHA256 || preview.CandidatesSHA256 != input.CandidatesSHA256 {
		return empty, false, fault.Conflict("合同对象候选已变化，请重新预览")
	}
	association := ServiceObjectContractAssociation{
		MaterialID: preview.MaterialID, MaterialVersion: preview.MaterialVersion, DocumentID: preview.DocumentID,
		ParsedDocumentID: preview.ParsedDocumentID, SourceRunID: preview.RunID, SourceAttemptID: preview.AttemptID,
		ObjectRef: option.ObjectRef, FactRefs: option.Facts, BasisRefs: option.BasisRefs,
	}
	plan, err := PlanServiceObjectAssociation(head, currentVersion, ServiceObjectAssociationCandidate{
		ExpectedObjectVersion: input.ExpectedObjectVersion, ExpectedAssociationVersion: input.ExpectedAssociationVersion,
		Confirmed: true, Association: association,
	}, current.ID, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return empty, false, fault.Conflict("服务对象身份或关联已变化，请刷新后重试")
	}
	if plan.ObjectVersion.Version != currentVersion.Version {
		plan.ObjectVersion.PayloadSHA256, err = canonicalServiceObjectIdentitySHA256(plan.ObjectVersion)
		if err != nil {
			return empty, false, err
		}
		if err := insertServiceObjectVersion(ctx, tx, current, caseItem, plan.ObjectVersion); err != nil {
			return empty, false, err
		}
	}
	plan.Association.PayloadSHA256, err = canonicalServiceObjectAssociationSHA256(plan.Association)
	if err != nil {
		return empty, false, err
	}
	if err := insertServiceObjectAssociation(ctx, tx, current, caseItem, plan.Association); err != nil {
		return empty, false, err
	}
	updated, err := tx.Exec(ctx, `UPDATE service_objects SET current_version=$1,current_association_version=$2
		WHERE tenant_id=$3 AND org_id=$4 AND case_id=$5 AND service_object_id=$6 AND current_version=$7 AND COALESCE(current_association_version,0)=$8`,
		plan.Head.CurrentVersion, plan.Head.CurrentAssociationVersion, accounts.Tenant(current), caseItem.OwnerOrgID, caseID,
		objectID, input.ExpectedObjectVersion, input.ExpectedAssociationVersion)
	if err != nil {
		return empty, false, err
	}
	if updated.RowsAffected() != 1 {
		return empty, false, fault.Conflict("服务对象身份或关联已变化，请刷新后重试")
	}
	result = ServiceObjectMutationResult{ServiceObjectID: objectID, Version: plan.Head.CurrentVersion, AssociationVersion: plan.Head.CurrentAssociationVersion}
	body, err := json.Marshal(result)
	if err != nil {
		return empty, false, err
	}
	if err := insertServiceObjectReceipt(ctx, tx, current, caseItem, "confirm_association", key, requestHash, 201, body, objectID, time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		return empty, false, err
	}
	if err := recheckServiceObjectManage(ctx, tx, current, caseItem); err != nil {
		return empty, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, false, err
	}
	return result, false, nil
}

func deriveServiceObjectAssociationPreview(s *Store, st state, user accounts.User, caseItem Case, head ServiceObjectHead, input ServiceObjectAssociationPreviewInput) (ServiceObjectAssociationPreview, error) {
	input.RunID, input.AttemptID = strings.TrimSpace(input.RunID), strings.TrimSpace(input.AttemptID)
	input.MaterialID, input.DocumentID = strings.TrimSpace(input.MaterialID), strings.TrimSpace(input.DocumentID)
	input.ParsedDocumentID = strings.TrimSpace(input.ParsedDocumentID)
	if input.RunID == "" || input.AttemptID == "" || input.MaterialID == "" || input.MaterialVersion < 1 || input.DocumentID == "" || input.ParsedDocumentID == "" {
		return ServiceObjectAssociationPreview{}, fault.Invalid("冻结审查及材料解析选择无效")
	}
	_, attempt, attemptInput, _, err := s.validateFrozenTerminalAttempt(st, caseItem, input.RunID, input.AttemptID)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	if attempt.RunID != input.RunID {
		return ServiceObjectAssociationPreview{}, fault.Conflict("冻结审查尝试编号不匹配")
	}
	var selected *FrozenDocument
	for i := range attemptInput.Documents {
		doc := &attemptInput.Documents[i]
		if doc.Document.ID == input.DocumentID && doc.ParsedDocumentID == input.ParsedDocumentID {
			selected = doc
			break
		}
	}
	if selected == nil || selected.Material == nil || selected.Material.MaterialID != input.MaterialID || selected.Material.Purpose != "primary_contract" || selected.Document.Role != "draft_contract" {
		return ServiceObjectAssociationPreview{}, fault.Conflict("合同材料不在该次冻结审查中")
	}
	var materialVersionFound bool
	for _, version := range st.MaterialVersions {
		if version.TenantID == caseItem.TenantID && version.OrgID == caseItem.OwnerOrgID && version.CaseID == caseItem.ID &&
			version.MaterialID == input.MaterialID && version.Version == input.MaterialVersion && version.DocumentID == input.DocumentID {
			materialVersionFound = true
			break
		}
	}
	if !materialVersionFound {
		return ServiceObjectAssociationPreview{}, fault.Conflict("材料版本与冻结合同原件不匹配")
	}
	selectedParse := false
	for _, selection := range st.parseSelectionsForAttempt(input.AttemptID) {
		if selection.RunID == input.RunID && selection.DocumentID == input.DocumentID && selection.ParsedDocumentID == input.ParsedDocumentID && selection.OriginalDigest == selected.Document.Digest {
			selectedParse = true
			break
		}
	}
	if !selectedParse {
		return ServiceObjectAssociationPreview{}, fault.Conflict("解析版本不在该次冻结审查中")
	}
	interpreted := interpretedFromFrozen(attemptInput.Documents)
	sources, err := telecomSources(interpreted)
	if err != nil {
		return ServiceObjectAssociationPreview{}, fault.Conflict("冻结合同原文未通过完整性校验")
	}
	principal := bindLocalDemoCaseGrant(caseItem, user, accounts.Principal(user))
	resource := access.Resource{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseItem.ID, OwnerID: caseItem.OwnerID}
	request := rulepack.Request{Principal: principal, Resource: resource, BusinessLine: caseItem.BusinessLine, Scenario: caseItem.Scenario,
		ContractType: caseItem.ContractType, At: time.Now().UTC(), Sources: sources}
	facts, err := telecom.Extract(request)
	if err != nil {
		return ServiceObjectAssociationPreview{}, fault.Conflict("冻结合同中没有可用的服务对象事实")
	}
	byObject := make(map[string][]ServiceObjectFactRef)
	for _, fact := range facts {
		if strings.TrimSpace(fact.ObjectRef) == "" {
			continue
		}
		allCitationsSelected := len(fact.Citations) > 0
		for _, citation := range fact.Citations {
			if selected.NormalizedHash == nil || citation.SourceID != input.DocumentID || citation.SourceVersion != input.ParsedDocumentID ||
				citation.TenantID != caseItem.TenantID || citation.CaseID != caseItem.ID || citation.SourceDigest != *selected.NormalizedHash {
				allCitationsSelected = false
				break
			}
		}
		if !allCitationsSelected {
			continue
		}
		factHash, err := canonicalRuleFactSHA256(fact)
		if err != nil {
			return ServiceObjectAssociationPreview{}, err
		}
		byObject[fact.ObjectRef] = append(byObject[fact.ObjectRef], ServiceObjectFactRef{Fact: fact, DocumentID: input.DocumentID,
			ParsedDocumentID: input.ParsedDocumentID, CanonicalFactSHA256: factHash})
	}
	objectRefs := make([]string, 0, len(byObject))
	for objectRef := range byObject {
		objectRefs = append(objectRefs, objectRef)
	}
	sort.Strings(objectRefs)
	options := make([]ServiceObjectAssociationOption, 0, len(objectRefs))
	for _, objectRef := range objectRefs {
		refs := byObject[objectRef]
		factsJSON, digest, err := canonicalServiceObjectFacts(refs)
		if err != nil {
			return ServiceObjectAssociationPreview{}, err
		}
		basisRefs, err := serviceObjectBasisRefs(refs)
		if err != nil {
			return ServiceObjectAssociationPreview{}, err
		}
		_ = factsJSON
		options = append(options, ServiceObjectAssociationOption{ObjectRef: objectRef, Facts: refs, FactsSHA256: digest, BasisRefs: basisRefs})
	}
	preview := ServiceObjectAssociationPreview{ServiceObjectID: head.ID, ExpectedObjectVersion: head.CurrentVersion,
		ExpectedAssociationVersion: head.CurrentAssociationVersion, RunID: input.RunID, AttemptID: input.AttemptID, MaterialID: input.MaterialID,
		MaterialVersion: input.MaterialVersion, DocumentID: input.DocumentID, ParsedDocumentID: input.ParsedDocumentID, Options: options}
	pins := make([]serviceObjectCandidateDigestPin, 0, len(preview.Options))
	for _, option := range preview.Options {
		pins = append(pins, serviceObjectCandidateDigestPin{ObjectRef: option.ObjectRef, FactsSHA256: option.FactsSHA256})
	}
	preview.CandidatesSHA256, err = serviceObjectCandidatesSHA256(serviceObjectCandidateDigestInput{
		ServiceObjectID: preview.ServiceObjectID, ExpectedObjectVersion: preview.ExpectedObjectVersion,
		ExpectedAssociationVersion: preview.ExpectedAssociationVersion, RunID: preview.RunID, AttemptID: preview.AttemptID,
		MaterialID: preview.MaterialID, MaterialVersion: preview.MaterialVersion, DocumentID: preview.DocumentID,
		ParsedDocumentID: preview.ParsedDocumentID, Candidates: pins,
	})
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	return preview, nil
}

func (s *Store) serviceObjectMutationTx(ctx context.Context, user accounts.User, caseID string) (accounts.User, Case, pgx.Tx, error) {
	if ctx == nil {
		return accounts.User{}, Case{}, nil, fault.Invalid("服务对象写入需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return accounts.User{}, Case{}, nil, fault.NotFound("没有找到对应案件")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if !authorizeCase(caseItem, current, access.Manage) {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, fault.Forbidden("当前授权不能维护服务对象")
	}
	if err := businessWritable(caseItem); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	return current, caseItem, tx, nil
}

func (s *Store) serviceObjectReadTx(ctx context.Context, user accounts.User, caseID string) (accounts.User, Case, pgx.Tx, error) {
	if ctx == nil {
		return accounts.User{}, Case{}, nil, fault.Invalid("服务对象读取需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	caseID = strings.TrimSpace(caseID)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, fault.Forbidden("当前授权不能读取服务对象")
	}
	return current, caseItem, tx, nil
}

func (s *Store) requireServiceObjectCoreWritable(ctx context.Context) error {
	if ctx == nil {
		return fault.Invalid("服务对象写入需要请求上下文")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能写入服务对象")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("只有 Core 服务可以写入服务对象")
	}
	return nil
}

func recheckServiceObjectManage(ctx context.Context, tx pgx.Tx, user accounts.User, before Case) error {
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, before.ID)
	if err != nil {
		return err
	}
	if caseItem.OwnerOrgID != before.OwnerOrgID || !authorizeCase(caseItem, current, access.Manage) {
		return fault.Forbidden("当前授权不能维护服务对象")
	}
	if err := businessWritable(caseItem); err != nil {
		return err
	}
	return nil
}

func serviceObjectRequestSHA256(tenantID, orgID, caseID, actorID, operation string, payload any) (string, error) {
	canonical, err := json.Marshal(struct {
		TenantID  string `json:"tenant_id"`
		OrgID     string `json:"org_id"`
		CaseID    string `json:"case_id"`
		ActorID   string `json:"actor_id"`
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{tenantID, orgID, caseID, actorID, operation, payload})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func readServiceObjectReceipt(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, operation, key, requestSHA string, target any) (bool, error) {
	var storedSHA string
	var status int
	var body []byte
	err := tx.QueryRow(ctx, `SELECT request_sha256,response_status,response_body FROM service_object_mutation_receipts
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND operation=$5 AND idempotency_key=$6`,
		tenantID, orgID, caseID, actorID, operation, key).Scan(&storedSHA, &status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if storedSHA != requestSHA {
		return false, fault.Conflict("同一幂等键不能用于不同服务对象请求")
	}
	if status < 200 || status >= 300 || len(body) == 0 {
		return false, fault.New(500, "integrity_error", "服务对象幂等回执无效")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return false, fault.New(500, "integrity_error", "服务对象幂等回执无法读取")
	}
	return true, nil
}

func insertServiceObjectReceipt(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, operation, key, requestSHA string, status int, body []byte, entityID string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO service_object_mutation_receipts
		(tenant_id,org_id,case_id,actor_id,operation,idempotency_key,request_sha256,response_status,response_body,entity_id,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, accounts.Tenant(user), item.OwnerOrgID, item.ID,
		user.ID, operation, key, requestSHA, status, body, entityID, now.UTC().Truncate(time.Microsecond))
	return err
}

func loadServiceObjectHistory(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID string, head ServiceObjectHead) (ServiceObjectIdentityHistory, error) {
	result := ServiceObjectIdentityHistory{Head: head, Versions: []ServiceObjectVersion{}, Associations: []ServiceObjectContractAssociation{}}
	rows, err := tx.Query(ctx, `SELECT service_object_id,version,object_type,display_name,COALESCE(business_code,''),aliases,state,effective_from,COALESCE(change_decision_id,''),basis_refs,COALESCE(basis_summary,''),payload_sha256,created_by,created_at
		FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 ORDER BY version`, tenantID, orgID, caseID, head.ID)
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	for rows.Next() {
		var item ServiceObjectVersion
		var aliases, basis []byte
		if err := rows.Scan(&item.ServiceObjectID, &item.Version, &item.ObjectType, &item.DisplayName, &item.BusinessCode, &aliases, &item.State, &item.EffectiveFrom, &item.ChangeDecisionID, &basis, &item.BasisSummary, &item.PayloadSHA256, &item.CreatedBy, &item.CreatedAt); err != nil {
			rows.Close()
			return ServiceObjectIdentityHistory{}, err
		}
		if err := json.Unmarshal(aliases, &item.Aliases); err != nil {
			rows.Close()
			return ServiceObjectIdentityHistory{}, fault.New(500, "integrity_error", "服务对象别名记录无效")
		}
		item.BasisRefs = append(json.RawMessage(nil), basis...)
		digest, err := canonicalServiceObjectIdentitySHA256(item)
		if err != nil || digest != item.PayloadSHA256 {
			rows.Close()
			return ServiceObjectIdentityHistory{}, fault.New(500, "integrity_error", "服务对象身份版本摘要校验失败")
		}
		result.Versions = append(result.Versions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ServiceObjectIdentityHistory{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT service_object_id,association_version,object_version,material_id,material_version,document_id,parsed_document_id,source_run_id,source_attempt_id,object_ref,facts_canonical_json,facts_sha256,basis_refs,COALESCE(identity_change_decision_id,''),payload_sha256,confirmed_by,confirmed_at,created_at
		FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 ORDER BY association_version`, tenantID, orgID, caseID, head.ID)
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	for rows.Next() {
		var item ServiceObjectContractAssociation
		var basis []byte
		if err := rows.Scan(&item.ServiceObjectID, &item.AssociationVersion, &item.ObjectVersion, &item.MaterialID, &item.MaterialVersion,
			&item.DocumentID, &item.ParsedDocumentID, &item.SourceRunID, &item.SourceAttemptID, &item.ObjectRef, &item.FactsCanonicalJSON,
			&item.FactsSHA256, &basis, &item.IdentityChangeDecisionID, &item.PayloadSHA256, &item.ConfirmedBy, &item.ConfirmedAt, &item.CreatedAt); err != nil {
			rows.Close()
			return ServiceObjectIdentityHistory{}, err
		}
		item.BasisRefs = append(json.RawMessage(nil), basis...)
		factsDigest := sha256.Sum256(item.FactsCanonicalJSON)
		if hex.EncodeToString(factsDigest[:]) != item.FactsSHA256 || json.Unmarshal(item.FactsCanonicalJSON, &item.FactRefs) != nil {
			rows.Close()
			return ServiceObjectIdentityHistory{}, fault.New(500, "integrity_error", "服务对象关联冻结事实校验失败")
		}
		if err := validateServiceObjectFactRefs(item); err != nil {
			rows.Close()
			return ServiceObjectIdentityHistory{}, fault.New(500, "integrity_error", "服务对象关联引用未通过校验")
		}
		canonical, digest, err := canonicalServiceObjectFacts(item.FactRefs)
		if err != nil || !bytes.Equal(canonical, item.FactsCanonicalJSON) || digest != item.FactsSHA256 {
			rows.Close()
			return ServiceObjectIdentityHistory{}, fault.New(500, "integrity_error", "服务对象关联事实与冻结字节不一致")
		}
		payloadSHA, err := canonicalServiceObjectAssociationSHA256(item)
		if err != nil || payloadSHA != item.PayloadSHA256 {
			rows.Close()
			return ServiceObjectIdentityHistory{}, fault.New(500, "integrity_error", "服务对象关联摘要校验失败")
		}
		result.Associations = append(result.Associations, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ServiceObjectIdentityHistory{}, err
	}
	rows.Close()
	return result, nil
}

func loadServiceObjectVersion(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID string, head ServiceObjectHead, target *ServiceObjectVersion) error {
	var aliases, basis []byte
	err := tx.QueryRow(ctx, `SELECT service_object_id,version,object_type,display_name,COALESCE(business_code,''),aliases,state,effective_from,COALESCE(change_decision_id,''),basis_refs,COALESCE(basis_summary,''),payload_sha256,created_by,created_at
		FROM service_object_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND version=$5`,
		tenantID, orgID, caseID, head.ID, head.CurrentVersion).Scan(&target.ServiceObjectID, &target.Version, &target.ObjectType, &target.DisplayName,
		&target.BusinessCode, &aliases, &target.State, &target.EffectiveFrom, &target.ChangeDecisionID, &basis, &target.BasisSummary, &target.PayloadSHA256, &target.CreatedBy, &target.CreatedAt)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(aliases, &target.Aliases); err != nil {
		return fault.New(500, "integrity_error", "服务对象别名记录无效")
	}
	target.BasisRefs = append(json.RawMessage(nil), basis...)
	digest, err := canonicalServiceObjectIdentitySHA256(*target)
	if err != nil || digest != target.PayloadSHA256 {
		return fault.New(500, "integrity_error", "服务对象身份版本摘要校验失败")
	}
	return nil
}

func loadServiceObjectVersionValue(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID string, head ServiceObjectHead) (ServiceObjectVersion, error) {
	var item ServiceObjectVersion
	if err := loadServiceObjectVersion(ctx, tx, tenantID, orgID, caseID, head, &item); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceObjectVersion{}, fault.NotFound("没有找到服务对象身份版本")
		}
		return ServiceObjectVersion{}, err
	}
	return item, nil
}

func loadServiceObjectVersionExact(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, objectID string, number int) (ServiceObjectVersion, error) {
	return loadServiceObjectVersionValue(ctx, tx, tenantID, orgID, caseID, ServiceObjectHead{ID: objectID, CurrentVersion: number})
}

func loadServiceObjectHead(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, objectID string) (ServiceObjectHead, error) {
	var head ServiceObjectHead
	err := tx.QueryRow(ctx, `SELECT service_object_id,current_version,COALESCE(current_association_version,0)
		FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`,
		tenantID, orgID, caseID, objectID).Scan(&head.ID, &head.CurrentVersion, &head.CurrentAssociationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceObjectHead{}, fault.NotFound("没有找到服务对象")
	}
	return head, err
}

func loadServiceObjectAssociation(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, objectID string, number int) (ServiceObjectContractAssociation, error) {
	var item ServiceObjectContractAssociation
	var basis []byte
	err := tx.QueryRow(ctx, `SELECT service_object_id,association_version,object_version,material_id,material_version,document_id,parsed_document_id,source_run_id,source_attempt_id,object_ref,facts_canonical_json,facts_sha256,basis_refs,COALESCE(identity_change_decision_id,''),payload_sha256,confirmed_by,confirmed_at,created_at
		FROM service_object_contract_associations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND association_version=$5`,
		tenantID, orgID, caseID, objectID, number).Scan(&item.ServiceObjectID, &item.AssociationVersion, &item.ObjectVersion, &item.MaterialID, &item.MaterialVersion,
		&item.DocumentID, &item.ParsedDocumentID, &item.SourceRunID, &item.SourceAttemptID, &item.ObjectRef, &item.FactsCanonicalJSON,
		&item.FactsSHA256, &basis, &item.IdentityChangeDecisionID, &item.PayloadSHA256, &item.ConfirmedBy, &item.ConfirmedAt, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceObjectContractAssociation{}, fault.NotFound("没有找到服务对象关联版本")
	}
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	item.BasisRefs = append(json.RawMessage(nil), basis...)
	factsDigest := sha256.Sum256(item.FactsCanonicalJSON)
	if hex.EncodeToString(factsDigest[:]) != item.FactsSHA256 || json.Unmarshal(item.FactsCanonicalJSON, &item.FactRefs) != nil {
		return ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联冻结事实校验失败")
	}
	if err := validateServiceObjectFactRefs(item); err != nil {
		return ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联引用未通过校验")
	}
	canonical, digest, err := canonicalServiceObjectFacts(item.FactRefs)
	if err != nil || !bytes.Equal(canonical, item.FactsCanonicalJSON) || digest != item.FactsSHA256 {
		return ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联事实与冻结字节不一致")
	}
	payloadSHA, err := canonicalServiceObjectAssociationSHA256(item)
	if err != nil || payloadSHA != item.PayloadSHA256 {
		return ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联摘要校验失败")
	}
	return item, nil
}

func serviceObjectPageLimit(limit int) (int, error) {
	if limit == 0 {
		return 20, nil
	}
	if limit < 0 || limit > 100 {
		return 0, fault.Invalid("服务对象分页范围为1到100")
	}
	return limit, nil
}

type serviceObjectCursor struct {
	Kind     string `json:"kind"`
	CaseID   string `json:"case_id"`
	ObjectID string `json:"object_id"`
	Last     string `json:"last"`
}

func encodeServiceObjectCursor(kind, caseID, objectID, value string) string {
	payload, _ := json.Marshal(serviceObjectCursor{Kind: kind, CaseID: caseID, ObjectID: objectID, Last: value})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeServiceObjectCursor(value, kind, caseID, objectID string) (string, error) {
	if value == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	var cursor serviceObjectCursor
	if err != nil || len(decoded) == 0 || len(decoded) > 512 || json.Unmarshal(decoded, &cursor) != nil ||
		cursor.Kind != kind || cursor.CaseID != caseID || cursor.ObjectID != objectID || cursor.Last == "" || strings.TrimSpace(cursor.Last) != cursor.Last {
		return "", fault.Invalid("服务对象游标无效")
	}
	return cursor.Last, nil
}

func encodeServiceObjectIntegerCursor(kind, caseID, objectID string, value int) string {
	return encodeServiceObjectCursor(kind, caseID, objectID, strconv.Itoa(value))
}

func decodeServiceObjectIntegerCursor(value, kind, caseID, objectID string) (int, error) {
	last, err := decodeServiceObjectCursor(value, kind, caseID, objectID)
	if err != nil || last == "" {
		if err != nil {
			return 0, err
		}
		return 0, nil
	}
	parsed, err := strconv.Atoi(last)
	if err != nil || parsed < 1 {
		return 0, fault.Invalid("服务对象版本游标无效")
	}
	return parsed, nil
}

func cursorIntOrMax(value int) int {
	if value == 0 {
		// The backing version columns use PostgreSQL int4, so a Go int max
		// cannot be encoded by pgx as the first-page upper bound.
		return int(1<<31 - 1)
	}
	return value
}

func associationPin(item ServiceObjectContractAssociation) ServiceObjectAssociationPin {
	return ServiceObjectAssociationPin{AssociationVersion: item.AssociationVersion, ObjectVersion: item.ObjectVersion,
		MaterialID: item.MaterialID, MaterialVersion: item.MaterialVersion, DocumentID: item.DocumentID,
		ParsedDocumentID: item.ParsedDocumentID, SourceRunID: item.SourceRunID, SourceAttemptID: item.SourceAttemptID,
		ObjectRef: item.ObjectRef, FactsSHA256: item.FactsSHA256, ConfirmedBy: item.ConfirmedBy, ConfirmedAt: item.ConfirmedAt}
}

func associationPinPointer(item *ServiceObjectContractAssociation) *ServiceObjectAssociationPin {
	if item == nil {
		return nil
	}
	pin := associationPin(*item)
	return &pin
}

func serviceObjectCanManage(s *Store, user accounts.User, item Case) bool {
	return authorizeCase(item, user, access.Manage) && businessWritable(item) == nil && !s.ReadOnly()
}

func serviceObjectNeedsReassociation(st state, caseID string, association *ServiceObjectAssociationPin) bool {
	if association == nil || materialSelectionPending(st, caseID) {
		return true
	}
	selected := false
	for _, document := range selectedMaterialDocuments(st, caseID) {
		if document.ID == association.DocumentID {
			selected = true
			break
		}
	}
	if !selected {
		return true
	}
	for _, material := range st.Materials {
		if material.CaseID == caseID && material.ID == association.MaterialID {
			return material.CurrentVersion != association.MaterialVersion || material.CurrentDocumentID != association.DocumentID
		}
	}
	return true
}

func insertServiceObjectVersion(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, version ServiceObjectVersion) error {
	aliasesJSON, err := json.Marshal(version.Aliases)
	if err != nil {
		return err
	}
	aliases := json.RawMessage(aliasesJSON)
	basis := version.BasisRefs
	if len(basis) == 0 {
		basis = json.RawMessage(`[]`)
	}
	_, err = tx.Exec(ctx, `INSERT INTO service_object_versions
		(tenant_id,org_id,case_id,service_object_id,version,object_type,display_name,business_code,aliases,state,effective_from,change_decision_id,change_decision_outcome,basis_refs,basis_summary,payload_sha256,created_by,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9::jsonb,$10,$11,NULLIF($12,''),CASE WHEN $12<>'' THEN 'approved' ELSE NULL END,$13::jsonb,NULLIF($14,''),$15,$16,$17)`,
		accounts.Tenant(user), item.OwnerOrgID, item.ID, version.ServiceObjectID, version.Version, version.ObjectType, version.DisplayName,
		version.BusinessCode, aliases, version.State, version.EffectiveFrom.UTC().Truncate(time.Microsecond), version.ChangeDecisionID, basis,
		version.BasisSummary, version.PayloadSHA256, user.ID, version.CreatedAt.UTC().Truncate(time.Microsecond))
	return err
}

func insertServiceObjectAssociation(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, association ServiceObjectContractAssociation) error {
	basis := association.BasisRefs
	if len(basis) == 0 {
		basis = json.RawMessage(`[]`)
	}
	_, err := tx.Exec(ctx, `INSERT INTO service_object_contract_associations
		(tenant_id,org_id,case_id,service_object_id,association_version,object_version,material_id,material_version,document_id,parsed_document_id,source_run_id,source_attempt_id,object_ref,facts_canonical_json,facts_sha256,basis_refs,identity_change_decision_id,identity_change_decision_outcome,payload_sha256,confirmed_by,confirmed_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb,NULLIF($17,''),CASE WHEN $17<>'' THEN 'approved' ELSE NULL END,$18,$19,$20,$21)`,
		accounts.Tenant(user), item.OwnerOrgID, item.ID, association.ServiceObjectID, association.AssociationVersion, association.ObjectVersion,
		association.MaterialID, association.MaterialVersion, association.DocumentID, association.ParsedDocumentID, association.SourceRunID,
		association.SourceAttemptID, association.ObjectRef, association.FactsCanonicalJSON, association.FactsSHA256, basis,
		association.IdentityChangeDecisionID, association.PayloadSHA256, association.ConfirmedBy, association.ConfirmedAt.UTC().Truncate(time.Microsecond),
		association.CreatedAt.UTC().Truncate(time.Microsecond))
	return err
}

func canonicalServiceObjectAssociationSHA256(association ServiceObjectContractAssociation) (string, error) {
	var basis []any
	if len(association.BasisRefs) == 0 {
		basis = []any{}
	} else if err := json.Unmarshal(association.BasisRefs, &basis); err != nil || basis == nil {
		return "", errors.New("service object association basis refs are invalid")
	}
	basisJSON, err := json.Marshal(basis)
	if err != nil {
		return "", err
	}
	payload := struct {
		ObjectID           string          `json:"service_object_id"`
		AssociationVersion int             `json:"association_version"`
		ObjectVersion      int             `json:"object_version"`
		MaterialID         string          `json:"material_id"`
		MaterialVersion    int             `json:"material_version"`
		DocumentID         string          `json:"document_id"`
		ParsedDocumentID   string          `json:"parsed_document_id"`
		RunID              string          `json:"source_run_id"`
		AttemptID          string          `json:"source_attempt_id"`
		ObjectRef          string          `json:"object_ref"`
		FactsSHA256        string          `json:"facts_sha256"`
		BasisRefs          json.RawMessage `json:"basis_refs"`
		IdentityDecisionID string          `json:"identity_change_decision_id,omitempty"`
		ConfirmedBy        string          `json:"confirmed_by"`
		ConfirmedAt        time.Time       `json:"confirmed_at"`
		CreatedAt          time.Time       `json:"created_at"`
	}{association.ServiceObjectID, association.AssociationVersion, association.ObjectVersion, association.MaterialID, association.MaterialVersion,
		association.DocumentID, association.ParsedDocumentID, association.SourceRunID, association.SourceAttemptID, association.ObjectRef,
		association.FactsSHA256, basisJSON, association.IdentityChangeDecisionID, association.ConfirmedBy,
		association.ConfirmedAt.UTC().Truncate(time.Microsecond), association.CreatedAt.UTC().Truncate(time.Microsecond)}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func serviceObjectBasisRefs(refs []ServiceObjectFactRef) (json.RawMessage, error) {
	type basisRef struct {
		DocumentID       string `json:"document_id"`
		ParsedDocumentID string `json:"parsed_document_id"`
		Start            int    `json:"start"`
		End              int    `json:"end"`
		QuoteSHA256      string `json:"quote_sha256"`
	}
	items := make([]basisRef, 0)
	seen := map[basisRef]struct{}{}
	for _, ref := range refs {
		for _, citation := range ref.Fact.Citations {
			quoteDigest := sha256.Sum256([]byte(citation.Quote))
			item := basisRef{DocumentID: ref.DocumentID, ParsedDocumentID: ref.ParsedDocumentID, Start: citation.Start, End: citation.End, QuoteSHA256: hex.EncodeToString(quoteDigest[:])}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			items = append(items, item)
		}
	}
	encoded, err := json.Marshal(items)
	return encoded, err
}

func (s *Store) serviceObjectReadState(ctx context.Context, user accounts.User, caseID string) (accounts.User, Case, state, error) {
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return accounts.User{}, Case{}, state{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return accounts.User{}, Case{}, state{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return accounts.User{}, Case{}, state{}, err
	}
	return current, caseItem, st, nil
}

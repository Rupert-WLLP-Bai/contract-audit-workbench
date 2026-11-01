package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// Freezing a draft appends one immutable clm_contract_revisions row. The draft
// stays editable so a later freeze can create the next version; frozen rows,
// their material summaries and their restricted-field fingerprints can never be
// updated or appended to outside the freezing transaction.
const (
	clmRouteFreezeVersion = "clm.freeze_version"
	clmEventVersionFrozen = "contract.version_frozen.v1"

	clmCommandFreezeVersion = "freeze_version"
)

// FreezeContractVersionInput is the typed freeze command. Tenant, actor and
// organization come only from the authenticated identity.
type FreezeContractVersionInput struct {
	ContractID         string
	ExpectedRowVersion int64
	CaseID             string
	IdempotencyKey     string
}

// ContractVersionSummary is the ledger-shaped projection of one frozen
// revision. It never contains restricted supplier fields.
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
	// AuditState reports whether a bound audit belongs to the current revision.
	// AuditGateState is the last immutable gate snapshot; not_applicable means
	// no audit has been bound to this revision.
	AuditState     string `json:"audit_state"`
	AuditGateState string `json:"audit_gate_state"`
}

// FreezeContractVersionResult is the idempotent freeze response.
type FreezeContractVersionResult struct {
	Version           ContractVersionSummary `json:"version"`
	ContractState     string                 `json:"contract_state"`
	CurrentRevisionID string                 `json:"current_revision_id"`
	SourceCaseID      string                 `json:"source_case_id"`
	RowVersion        int64                  `json:"row_version"`
	AllowedCommands   []string               `json:"allowed_commands"`
}

// ContractVersionList is the frozen revision ledger for one contract.
type ContractVersionList struct {
	Items []ContractVersionSummary `json:"items"`
}

// ContractVersionMaterial is one material summary captured at freeze time.
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

// ContractVersionDetail is one frozen revision with its projected draft. The
// draft follows the same restricted-field mask rule as the editable detail.
type ContractVersionDetail struct {
	Version   ContractVersionSummary    `json:"version"`
	Draft     *DraftContent             `json:"draft,omitempty"`
	Materials []ContractVersionMaterial `json:"materials"`
}

type frozenVersionMaterial struct {
	MaterialID        string `json:"material_id"`
	MaterialVersionID string `json:"material_version_id"`
	CaseOrgID         string `json:"case_org_id"`
	CaseID            string `json:"case_id"`
	MaterialVersion   int    `json:"material_version"`
	Filename          string `json:"filename"`
	Digest            string `json:"digest"`
	Purpose           string `json:"purpose"`
}

// FreezeContractVersion turns the current draft into a new immutable revision.
// It runs the complete StageFinal settlement policy unconditionally: there is
// no legacyAmountsKnown gate on this path, so an amount-unknown snapshot can
// never become a reconciled frozen version.
func (s *Store) FreezeContractVersion(ctx context.Context, user accounts.User, input FreezeContractVersionInput) (FreezeContractVersionResult, error) {
	id, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return FreezeContractVersionResult{}, fault.Invalid("冻结合同版本必须携带当前合同 row_version")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return FreezeContractVersionResult{}, err
	}
	caseID := strings.TrimSpace(input.CaseID)
	digest, err := clmRequestDigest(clmRouteFreezeVersion, "POST", "/api/contracts/"+id+"/revisions", input.ExpectedRowVersion,
		struct {
			CaseID string `json:"case_id"`
		}{CaseID: caseID})
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能冻结合同版本", nil)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteFreezeVersion, input.IdempotencyKey); err != nil {
		return FreezeContractVersionResult{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteFreezeVersion, input.IdempotencyKey, digest, id)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if found {
		// Recheck current visibility before replaying a successful result. The
		// actor/tenant scope already prevents another principal from using this
		// key, but a caller whose case/contract access was revoked must not keep
		// receiving the stored summary.
		record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
		if err != nil {
			return FreezeContractVersionResult{}, err
		}
		if !authorizeContractRead(ctx, tx, record, current) {
			return FreezeContractVersionResult{}, fault.NotFound("没有找到对应记录")
		}
		var result FreezeContractVersionResult
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return FreezeContractVersionResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FreezeContractVersionResult{}, err
		}
		return result, nil
	}
	if err := scopedContractLock(ctx, tx, id); err != nil {
		return FreezeContractVersionResult{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return FreezeContractVersionResult{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(record, current, access.Manage) {
		return FreezeContractVersionResult{}, fault.Forbidden("当前授权不能冻结合同版本")
	}
	if record.AuthorityMode != "native" {
		return FreezeContractVersionResult{}, fault.Forbidden("外部权威合同不允许在本地冻结合同版本")
	}
	if record.LifecycleState != "draft" || record.ValidityState != "active" {
		return FreezeContractVersionResult{}, fault.Conflict("合同当前状态不允许冻结合同版本")
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return FreezeContractVersionResult{}, fault.Conflict("合同内容已被他人修改，请刷新后确认")
	}
	var inReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`,
		accounts.Tenant(current), id).Scan(&inReview); err != nil {
		return FreezeContractVersionResult{}, err
	}
	if inReview {
		return FreezeContractVersionResult{}, fault.Conflict("合同已有进行中的会签轮次，不能重新冻结版本")
	}
	contentBytes, err := loadContractDraftBytes(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	// Pins are written only into the frozen snapshot, never into the editable
	// draft row. The strict #80 resolver is the single validator for subject
	// bindings, anchored-case material references and procurement snapshots;
	// this path must not grow a second, divergent set of checks.
	plaintext, err := unsealDraftForFreeze(contentBytes)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if err := validateDraftContent(&plaintext); err != nil {
		return FreezeContractVersionResult{}, err
	}
	// #78 hard requirement: the freeze path calls StageFinal with no
	// legacyAmountsKnown-style gate. A draft whose amount is absent, whose
	// settlement mode is unknown, or whose item/funding/payment identities do
	// not reconcile answers 422 before any revision row exists.
	if err := validateDraftAmounts(&plaintext, contractmoney.StageFinal); err != nil {
		return FreezeContractVersionResult{}, err
	}
	anchor, err := loadContractReferenceAnchor(ctx, tx, current, record, caseID)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	references, err := resolveContractReferences(ctx, tx, current, anchor, &plaintext, referenceModeImport)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	materials, err := frozenMaterialsFromReferences(anchor, references.Materials)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	_, sealedSnapshot, _, err := sealDraftContent(plaintext)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	contentBytes, err = canonicalStoredJSON(ctx, tx, sealedSnapshot)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	materialsDigest, err := canonicalHash(materials)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	fingerprints, err := computeRestrictedFingerprints(&plaintext)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	contentFingerprint, err := canonicalHash(struct {
		Fields     map[string]any              `json:"fields"`
		Restricted clmRestrictedFingerprintSet `json:"restricted"`
	}{
		Fields: flattenContractDraft(plaintext), Restricted: fingerprints,
	})
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	revisionID, err := newContractUUID()
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	var revisionNumber int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision_number),0)+1 FROM clm_contract_revisions
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(current), id).Scan(&revisionNumber); err != nil {
		return FreezeContractVersionResult{}, err
	}
	amount, currency := legacyAmountPair(plaintext)
	var frozenAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by,amounts_reconciled)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,true) RETURNING frozen_at`,
		accounts.Tenant(current), id, revisionID, revisionNumber, amount, currency,
		string(contentBytes), sha256Bytes(contentBytes), current.ID).Scan(&frozenAt)
	if err != nil {
		return FreezeContractVersionResult{}, clmUniqueViolation(err, "合同版本已被他人冻结，请刷新后确认")
	}
	if err := insertFrozenContractRevisionItems(ctx, tx, accounts.Tenant(current), id, revisionID, plaintext.Items); err != nil {
		return FreezeContractVersionResult{}, err
	}
	for _, material := range materials {
		if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_version_materials
			(tenant_id,contract_id,contract_revision_id,material_id,material_version_id,case_org_id,case_id,material_version,filename,digest,purpose)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			accounts.Tenant(current), id, revisionID, material.MaterialID, material.MaterialVersionID,
			material.CaseOrgID, material.CaseID, material.MaterialVersion, material.Filename, material.Digest, material.Purpose); err != nil {
			return FreezeContractVersionResult{}, err
		}
	}
	fingerprintJSON, err := json.Marshal(fingerprints)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_version_seals
		(tenant_id,contract_id,contract_revision_id,snapshot_digest,materials_digest,content_fingerprint,restricted_fingerprints)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)`,
		accounts.Tenant(current), id, revisionID, sha256Bytes(contentBytes), materialsDigest, contentFingerprint, string(fingerprintJSON)); err != nil {
		return FreezeContractVersionResult{}, err
	}
	var rowVersion int64
	err = tx.QueryRow(ctx, `UPDATE clm_contracts SET current_revision_id=$3, row_version=row_version+1, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$4
			AND authority_mode='native' AND lifecycle_state='draft' AND validity_state='active'
		RETURNING row_version`, accounts.Tenant(current), id, revisionID, record.RowVersion).Scan(&rowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return FreezeContractVersionResult{}, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	eventID, err := newContractUUID()
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"contract_id":          id,
		"contract_revision_id": revisionID,
		"revision_number":      revisionNumber,
		"snapshot_digest":      sha256Bytes(contentBytes),
		"materials_digest":     materialsDigest,
		"material_count":       len(materials),
	})
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events
		(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
		VALUES ($1,$2,$3,$4,$5,1,$6::jsonb)`,
		accounts.Tenant(current), eventID, id, rowVersion, clmEventVersionFrozen, string(payload)); err != nil {
		return FreezeContractVersionResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery (tenant_id,event_id) VALUES ($1,$2)`,
		accounts.Tenant(current), eventID); err != nil {
		return FreezeContractVersionResult{}, err
	}
	record.CurrentRevisionID = &revisionID
	result := FreezeContractVersionResult{
		Version: ContractVersionSummary{
			ContractRevisionID: revisionID, RevisionNumber: revisionNumber,
			Amount: amount, Currency: currency,
			SnapshotDigest: sha256Bytes(contentBytes), MaterialsDigest: materialsDigest,
			ContentFingerprint: contentFingerprint, AmountsReconciled: true,
			MaterialCount: len(materials), FrozenBy: current.ID, FrozenAt: frozenAt,
			AuditState: auditStateUnbound, AuditGateState: auditGateNotApplicable,
		},
		ContractState:     record.LifecycleState,
		CurrentRevisionID: revisionID,
		SourceCaseID:      anchor.ID,
		RowVersion:        rowVersion,
		AllowedCommands:   clmDraftCommands(record, true),
	}
	response, err := json.Marshal(result)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteFreezeVersion,
		input.IdempotencyKey, digest, id, 201, response); err != nil {
		return FreezeContractVersionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FreezeContractVersionResult{}, err
	}
	return result, nil
}

func loadContractDraftBytes(ctx context.Context, tx pgx.Tx, tenantID, contractID string) ([]byte, error) {
	var content []byte
	err := tx.QueryRow(ctx, `SELECT content FROM clm_contract_drafts WHERE tenant_id=$1 AND contract_id=$2`,
		tenantID, contractID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.Conflict("合同草稿已不存在，无法冻结合同版本")
	}
	return content, err
}

// frozenMaterialsFromReferences projects the strict resolver's material rows
// into the frozen summary table. The resolver already enforced that each
// version belongs to the anchor case, so every row carries the same case
// identity. clm_contract_version_materials keys on material_id, so a draft
// that cites two versions of the same material is rejected here instead of
// surfacing a primary-key error later.
func frozenMaterialsFromReferences(anchor Case, refs []ContractMaterialReference) ([]frozenVersionMaterial, error) {
	seenMaterial := make(map[string]bool, len(refs))
	materials := make([]frozenVersionMaterial, 0, len(refs))
	for _, ref := range refs {
		if seenMaterial[ref.MaterialID] {
			return nil, fault.Invalid("同一材料在冻结合同中只能引用一个版本")
		}
		seenMaterial[ref.MaterialID] = true
		materials = append(materials, frozenVersionMaterial{
			MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID,
			CaseOrgID: anchor.OwnerOrgID, CaseID: anchor.ID,
			MaterialVersion: ref.MaterialVersion, Filename: ref.Filename,
			Digest: ref.SHA256, Purpose: ref.Purpose,
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

// ListContractVersions returns the frozen revision ledger of one visible
// contract. It never unseals a snapshot and never returns restricted values.
func (s *Store) ListContractVersions(ctx context.Context, user accounts.User, contractID string) (ContractVersionList, error) {
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractVersionList{}, err
	}
	if !s.HasDatabase() {
		return ContractVersionList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractVersionList{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractVersionList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractVersionList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractVersionList{}, fault.NotFound("没有找到对应记录")
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: record.OwnerOrgID, Role: "api"}); err != nil {
		return ContractVersionList{}, err
	}
	rows, err := tx.Query(ctx, `SELECT r.contract_revision_id,r.revision_number,r.amount,r.currency,r.snapshot_digest,
			r.amounts_reconciled,r.frozen_by,r.frozen_at,
			COALESCE(s.materials_digest,''),COALESCE(s.content_fingerprint,''),
			(SELECT count(*) FROM clm_contract_version_materials m
			 WHERE m.tenant_id=r.tenant_id AND m.contract_id=r.contract_id AND m.contract_revision_id=r.contract_revision_id)
		FROM clm_contract_revisions r
		LEFT JOIN clm_contract_version_seals s
			ON s.tenant_id=r.tenant_id AND s.contract_id=r.contract_id AND s.contract_revision_id=r.contract_revision_id
		WHERE r.tenant_id=$1 AND r.contract_id=$2
		ORDER BY r.revision_number DESC`, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionList{}, err
	}
	defer rows.Close()
	items := []ContractVersionSummary{}
	for rows.Next() {
		var item ContractVersionSummary
		if err := rows.Scan(&item.ContractRevisionID, &item.RevisionNumber, &item.Amount, &item.Currency,
			&item.SnapshotDigest, &item.AmountsReconciled, &item.FrozenBy, &item.FrozenAt,
			&item.MaterialsDigest, &item.ContentFingerprint, &item.MaterialCount); err != nil {
			return ContractVersionList{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ContractVersionList{}, err
	}
	bindings, err := loadLatestContractAuditBindings(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionList{}, err
	}
	for i := range items {
		var binding *ContractAuditBinding
		if latest, ok := bindings[items[i].ContractRevisionID]; ok {
			binding = &latest
		}
		items[i].AuditState, items[i].AuditGateState = contractAuditStateFor(
			items[i].ContractRevisionID, record.CurrentRevisionID, binding)
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractVersionList{}, err
	}
	return ContractVersionList{Items: items}, nil
}

// LoadContractVersion returns one frozen revision plus its projected draft.
// Restricted fields reveal only while the caller holds manage authority; every
// other reader gets the same fixed mask as the editable detail.
func (s *Store) LoadContractVersion(ctx context.Context, user accounts.User, contractID, revisionID string) (ContractVersionDetail, error) {
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	revision, err := normalizeContractUUID(revisionID)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	if !s.HasDatabase() {
		return ContractVersionDetail{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractVersionDetail{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractVersionDetail{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractVersionDetail{}, fault.NotFound("没有找到对应记录")
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: record.OwnerOrgID, Role: "api"}); err != nil {
		return ContractVersionDetail{}, err
	}
	frozen, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), id, revision)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	seal, err := loadContractVersionSeal(ctx, tx, accounts.Tenant(current), id, revision)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	materials, err := loadContractVersionMaterials(ctx, tx, accounts.Tenant(current), id, revision)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	if seal != nil {
		materialsDigest, err := canonicalHash(materials)
		if err != nil {
			return ContractVersionDetail{}, err
		}
		if seal.SnapshotDigest != frozen.SnapshotDigest || sha256Bytes(frozen.Snapshot) != frozen.SnapshotDigest ||
			seal.MaterialsDigest != materialsDigest {
			return ContractVersionDetail{}, fault.Conflict("冻结版本的摘要与封印不一致")
		}
	}
	detail := ContractVersionDetail{
		Version: ContractVersionSummary{
			ContractRevisionID: frozen.RevisionID, RevisionNumber: frozen.RevisionNumber,
			Amount: frozen.Amount, Currency: frozen.Currency, SnapshotDigest: frozen.SnapshotDigest,
			AmountsReconciled: frozen.AmountsReconciled, MaterialCount: len(materials),
			FrozenBy: frozen.FrozenBy, FrozenAt: frozen.FrozenAt,
		},
		Materials: make([]ContractVersionMaterial, 0, len(materials)),
	}
	if seal != nil {
		detail.Version.MaterialsDigest = seal.MaterialsDigest
		detail.Version.ContentFingerprint = seal.ContentFingerprint
	}
	binding, err := loadLatestContractAuditBinding(ctx, tx, accounts.Tenant(current), id, revision)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	detail.Version.AuditState, detail.Version.AuditGateState = contractAuditStateFor(
		revision, record.CurrentRevisionID, binding)
	for _, material := range materials {
		detail.Materials = append(detail.Materials, ContractVersionMaterial{
			MaterialID: material.MaterialID, MaterialVersionID: material.MaterialVersionID,
			MaterialVersion: material.MaterialVersion, Filename: material.Filename,
			Digest: material.Digest, Purpose: material.Purpose,
			CaseID: material.CaseID, CaseOrgID: material.CaseOrgID,
		})
	}
	plaintext, err := unsealStoredDraft(frozen.Snapshot)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	detail.Draft = new(projectDraftForViewer(plaintext, authorizeContract(record, current, access.Manage)))
	if err := tx.Commit(ctx); err != nil {
		return ContractVersionDetail{}, err
	}
	return detail, nil
}

// frozenContractVersion is one persisted revision row used by read paths.
type frozenContractVersion struct {
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

func loadFrozenContractVersion(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (frozenContractVersion, error) {
	var frozen frozenContractVersion
	err := tx.QueryRow(ctx, `SELECT contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,
			amounts_reconciled,frozen_by,frozen_at
		FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		tenantID, contractID, revisionID).Scan(&frozen.RevisionID, &frozen.RevisionNumber, &frozen.Amount,
		&frozen.Currency, &frozen.Snapshot, &frozen.SnapshotDigest, &frozen.AmountsReconciled,
		&frozen.FrozenBy, &frozen.FrozenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return frozenContractVersion{}, fault.NotFound("没有找到对应的冻结版本")
	}
	if err != nil {
		return frozenContractVersion{}, err
	}
	// Revisions created before #81 (legacy import) stored a digest over the
	// bytes produced by the importer, not necessarily over PostgreSQL's jsonb
	// text form, and have no seal. Their digest is not re-verified here; sealed
	// #81 revisions are verified together with their seal below.
	return frozen, nil
}

type contractVersionSeal struct {
	SnapshotDigest         string
	MaterialsDigest        string
	ContentFingerprint     string
	RestrictedFingerprints clmRestrictedFingerprintSet
}

func loadContractVersionSeal(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (*contractVersionSeal, error) {
	var seal contractVersionSeal
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT snapshot_digest,materials_digest,content_fingerprint,restricted_fingerprints
		FROM clm_contract_version_seals WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		tenantID, contractID, revisionID).Scan(&seal.SnapshotDigest, &seal.MaterialsDigest,
		&seal.ContentFingerprint, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fingerprints, err := parseRestrictedFingerprints(raw)
	if err != nil {
		return nil, err
	}
	seal.RestrictedFingerprints = fingerprints
	return &seal, nil
}

func loadContractVersionMaterials(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) ([]frozenVersionMaterial, error) {
	rows, err := tx.Query(ctx, `SELECT material_id,material_version_id,case_org_id,case_id,material_version,filename,digest,purpose
		FROM clm_contract_version_materials WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3
		ORDER BY material_id`, tenantID, contractID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	materials := []frozenVersionMaterial{}
	for rows.Next() {
		var material frozenVersionMaterial
		if err := rows.Scan(&material.MaterialID, &material.MaterialVersionID, &material.CaseOrgID,
			&material.CaseID, &material.MaterialVersion, &material.Filename, &material.Digest,
			&material.Purpose); err != nil {
			return nil, err
		}
		materials = append(materials, material)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return materials, nil
}

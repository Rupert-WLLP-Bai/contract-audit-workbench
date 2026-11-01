package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/clmsource"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

const clmEventExternalSourceObserved = "contract.external_source_observed.v1"

// ContractSourceCaseScope is an exact server-configured machine source scope.
type ContractSourceCaseScope struct {
	OrgID  string `json:"org_id"`
	CaseID string `json:"case_id"`
}

// ContractSourceAuthority is resolved from deployment credentials, never from
// the signed request body. It contains no credential secret.
type ContractSourceAuthority struct {
	TenantID     string
	SourceID     string
	KeyID        string
	Kind         string
	Ordering     clmsource.VersionOrdering
	AllowedCases []ContractSourceCaseScope
}

type ContractSourceObservationInput struct {
	EventID            string
	ExternalContractID string
	SourceVersion      string
	SourceSequence     *uint64
	SourceState        string
	OccurredAt         time.Time
	CaseOrgID          string
	CaseID             string
	Snapshot           DraftContent
	RawBody            []byte
}

type ContractSourceObservationResult struct {
	ObservationID        string    `json:"observation_id"`
	ContractID           string    `json:"contract_id"`
	SourceEventID        string    `json:"source_event_id"`
	SourceKind           string    `json:"source_kind"`
	AuthorityMode        string    `json:"authority_mode"`
	Disposition          string    `json:"disposition"`
	Replayed             bool      `json:"replayed"`
	SourceVersion        string    `json:"source_version"`
	CurrentSourceVersion *string   `json:"current_source_version"`
	ContractRevisionID   *string   `json:"contract_revision_id,omitempty"`
	RecordedAt           time.Time `json:"recorded_at"`
}

type ContractSourceCapability struct {
	ContractID       string `json:"contract_id"`
	SourceID         string `json:"source_id"`
	SourceKind       string `json:"source_kind"`
	AuthorityMode    string `json:"authority_mode"`
	ReadOnly         bool   `json:"read_only"`
	CanReadContract  bool   `json:"can_read_contract"`
	VersionOrdering  string `json:"version_ordering"`
	ConnectionStatus string `json:"connection_status"`
}

type storedSourceObservation struct {
	ObservationID        string
	ContractID           string
	SourceKind           string
	PayloadDigest        string
	Disposition          string
	RevisionID           *string
	SourceVersion        string
	CurrentSourceVersion *string
	RecordedAt           time.Time
}

// RecordExternalContractObservation accepts one already-authenticated source
// event. Machine authentication and the current case allowlist are checked in
// HTTP and repeated here through the server-resolved authority object. The
// caller must never populate this object from request JSON.
func (s *Store) RecordExternalContractObservation(ctx context.Context, authority ContractSourceAuthority, input ContractSourceObservationInput) (ContractSourceObservationResult, error) {
	if s.pool == nil {
		return ContractSourceObservationResult{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if err := s.checkCLMWriteAllowed("只读模式下不能同步外部合同来源"); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if err := validateContractSourceAuthority(authority); err != nil {
		return ContractSourceObservationResult{}, fault.Invalid("合同来源配置无效")
	}
	if err := validateContractSourceObservationInput(&input, authority.Ordering); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if !contractSourceAllowsCase(authority.AllowedCases, input.CaseOrgID, input.CaseID) {
		return ContractSourceObservationResult{}, fault.NotFound("没有找到对应记录")
	}
	// Normalize only the typed semantic snapshot. RawBody remains byte-exact and
	// is independently encrypted after a stable observation ID is allocated.
	normalizedSnapshot, err := cloneSourceSnapshot(input.Snapshot)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	if normalizedSnapshot.Contract == nil {
		return ContractSourceObservationResult{}, fault.Invalid("snapshot.contract 必须是对象")
	}
	sanitizeDraftReferences(&normalizedSnapshot)
	if normalizedSnapshot.Contract.ProcurementResultPackage != nil || len(normalizedSnapshot.Materials) != 0 {
		return ContractSourceObservationResult{}, fault.Invalid("外部同步暂不接受本地采购或案件材料引用")
	}
	amountCurrencyValid := sourceAmountCurrencyValid(normalizedSnapshot.Contract)
	observedAmount, observedCurrency := normalizedSnapshot.Contract.Amount, normalizedSnapshot.Contract.Currency
	for i := range normalizedSnapshot.Contract.RelatedParties {
		normalizedSnapshot.Contract.RelatedParties[i].BindingID = ""
	}
	if input.SourceState == "" || strings.TrimSpace(input.SourceState) == "" {
		input.SourceState = "unknown"
	} else {
		input.SourceState = strings.TrimSpace(input.SourceState)
	}
	if !amountCurrencyValid {
		normalizedSnapshot.Contract.Amount = ""
		normalizedSnapshot.Contract.Currency = ""
	}
	if err := validateDraftContent(&normalizedSnapshot); err != nil {
		return ContractSourceObservationResult{}, err
	}
	digestSnapshot := normalizedSnapshot
	if !amountCurrencyValid {
		contract := *normalizedSnapshot.Contract
		contract.Amount = observedAmount
		contract.Currency = observedCurrency
		digestSnapshot.Contract = &contract
	}
	digestInput := input
	digestInput.Snapshot = digestSnapshot
	canonicalDigest, err := contractSourcePayloadDigest(authority, digestInput)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	snapshotDigest, err := contractSourceSnapshotDigest(authority, digestInput)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	input.Snapshot = normalizedSnapshot
	rawDigestBytes := sha256.Sum256(input.RawBody)
	rawDigest := hex.EncodeToString(rawDigestBytes[:])

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "clm-source:" + authority.SourceID, TenantID: authority.TenantID, OrgID: input.CaseOrgID, Role: "service"}); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if err := ensureSourceCaseScope(ctx, tx, authority, input.CaseOrgID, input.CaseID); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if err := scopedSourceEventLock(ctx, tx, authority.TenantID, authority.SourceID, input.EventID); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if err := scopedSourceContractLock(ctx, tx, authority.TenantID, authority.SourceID, input.ExternalContractID); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if prior, err := loadSourceEvent(ctx, tx, authority.TenantID, authority.SourceID, input.EventID); err == nil {
		if prior.PayloadDigest != canonicalDigest {
			return ContractSourceObservationResult{}, fault.Conflict("来源事件ID已用于不同载荷")
		}
		if err := ensureContractCaseBinding(ctx, tx, authority.TenantID, prior.ContractID, input.CaseOrgID, input.CaseID); err != nil {
			return ContractSourceObservationResult{}, err
		}
		result := sourceObservationResult(prior, input.EventID, true)
		if err := tx.Commit(ctx); err != nil {
			return ContractSourceObservationResult{}, err
		}
		return result, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ContractSourceObservationResult{}, err
	}

	record, exists, err := loadSourceContractForUpdate(ctx, tx, authority.TenantID, authority.SourceID, input.ExternalContractID)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	if exists && record.AuthorityMode != string(clmsource.AuthorityExternal) {
		return ContractSourceObservationResult{}, fault.Conflict("来源身份已绑定本地权威合同")
	}
	if exists {
		if err := ensureContractCaseBinding(ctx, tx, authority.TenantID, record.ContractID, input.CaseOrgID, input.CaseID); err != nil {
			return ContractSourceObservationResult{}, err
		}
	}
	if err := ensureSourceVersionImmutable(ctx, tx, authority.TenantID, authority.SourceID, input.ExternalContractID, input.SourceVersion, snapshotDigest, input.SourceSequence); err != nil {
		return ContractSourceObservationResult{}, err
	}
	newContract := !exists
	_, currentSnapshot, hasBaseline, err := sourceVersionBaseline(ctx, tx, authority.TenantID, authority.SourceID, input.ExternalContractID, authority.Ordering)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	incoming := clmsource.SnapshotVersion{Version: input.SourceVersion, Digest: snapshotDigest}
	if input.SourceSequence != nil {
		incoming.Sequence = *input.SourceSequence
	}
	decision := clmsource.VersionArchiveObservationOnly
	if newContract {
		decision, err = clmsource.CompareVersion(clmsource.SnapshotVersion{}, incoming, authority.Ordering)
	} else if hasBaseline {
		decision, err = clmsource.CompareVersion(currentSnapshot, incoming, authority.Ordering)
	} else {
		// Historical external rows without source observation history have no
		// trusted digest/sequence baseline. Preserve them without adopting a new current.
		decision = clmsource.VersionArchiveObservationOnly
	}
	if err != nil {
		return ContractSourceObservationResult{}, fault.Conflict("来源版本和摘要不一致")
	}
	if newContract {
		contractID, err := newContractUUID()
		if err != nil {
			return ContractSourceObservationResult{}, err
		}
		var initialCurrentVersion *string
		if decision == clmsource.VersionAdvanceCurrent && amountCurrencyValid {
			initialCurrentVersion = new(input.SourceVersion)
		}
		record, err = createExternalContractIdentity(ctx, tx, authority, input, contractID, initialCurrentVersion)
		if err != nil {
			return ContractSourceObservationResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_cases(tenant_id,contract_id,case_org_id,case_id,linked_by)
			VALUES($1,$2,$3,$4,$5)`, authority.TenantID, record.ContractID, input.CaseOrgID, input.CaseID, "clm-source:"+authority.SourceID); err != nil {
			return ContractSourceObservationResult{}, err
		}
	}

	observationID, err := newContractUUID()
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	ciphertext, err := sealCLMSourceObservation(input.RawBody, authority.TenantID, authority.SourceID, input.EventID, observationID)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	disposition := "observation_only"
	var revisionID *string
	apply := decision == clmsource.VersionAdvanceCurrent && amountCurrencyValid
	if !amountCurrencyValid {
		disposition = "unknown"
	} else if apply {
		disposition = "applied"
	} else if decision == clmsource.VersionConflict {
		return ContractSourceObservationResult{}, fault.Conflict("同一来源版本对应不同摘要或序号")
	}
	if decision == clmsource.VersionReplay {
		disposition = "observation_only"
	}

	if apply {
		revisionID, err = appendExternalContractRevision(ctx, tx, authority, input, record, normalizedSnapshot)
		if err != nil {
			return ContractSourceObservationResult{}, err
		}
	}
	observedCurrentVersion := record.SourceVersion
	if apply {
		observedCurrentVersion = new(input.SourceVersion)
	}
	if newContract {
		var contractNumber *string
		if apply {
			contractNumber = optionalTextOrPointer(normalizedSnapshot.Contract.Number)
		}
		if _, err := tx.Exec(ctx, `UPDATE clm_contracts SET source_version=$3, current_revision_id=$4,
			contract_number=$5, row_version=row_version+1, updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND contract_id=$2`, authority.TenantID, record.ContractID,
			observedCurrentVersion, revisionID, contractNumber); err != nil {
			return ContractSourceObservationResult{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE clm_contracts SET lifecycle_state='external_observed',
			source_version=$3, current_revision_id=COALESCE($4,current_revision_id),
			contract_number=CASE WHEN $4::uuid IS NOT NULL THEN $5 ELSE contract_number END,
			row_version=row_version+1, updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND contract_id=$2 AND authority_mode='external' AND row_version=$6`,
			authority.TenantID, record.ContractID, observedCurrentVersion, revisionID,
			optionalTextOrPointer(normalizedSnapshot.Contract.Number), record.RowVersion); err != nil {
			return ContractSourceObservationResult{}, err
		}
	}
	var sourceSequence any
	if input.SourceSequence != nil {
		sourceSequence = int64(*input.SourceSequence)
	}
	var observedRevision any
	if revisionID != nil {
		observedRevision = *revisionID
	}
	var recordedAt time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO clm_contract_source_observations
		(tenant_id,observation_id,contract_id,source_id,source_kind,source_key_id,external_contract_id,source_event_id,
		 source_version,current_source_version,source_sequence,payload_digest,source_snapshot_digest,raw_payload_digest,raw_payload_ciphertext,
		 source_observed_state,disposition,contract_revision_id,case_org_id,case_id,source_occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		RETURNING recorded_at`, authority.TenantID, observationID, record.ContractID, authority.SourceID,
		authority.Kind, authority.KeyID, input.ExternalContractID, input.EventID, input.SourceVersion, observedCurrentVersion, sourceSequence,
		canonicalDigest, snapshotDigest, rawDigest, ciphertext, input.SourceState, disposition, observedRevision,
		input.CaseOrgID, input.CaseID, input.OccurredAt.UTC()).Scan(&recordedAt); err != nil {
		return ContractSourceObservationResult{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, authority.TenantID, record.ContractID, record.RowVersion+1,
		clmEventExternalSourceObserved, map[string]any{
			"contract_id": record.ContractID, "observation_id": observationID,
			"source_id": authority.SourceID, "source_event_id": input.EventID,
			"source_version": input.SourceVersion, "payload_digest": canonicalDigest,
			"disposition": disposition, "contract_revision_id": revisionID,
		}); err != nil {
		return ContractSourceObservationResult{}, err
	}
	result := ContractSourceObservationResult{
		ObservationID: observationID, ContractID: record.ContractID, SourceEventID: input.EventID,
		SourceKind: authority.Kind, AuthorityMode: string(clmsource.AuthorityExternal),
		Disposition: disposition, SourceVersion: input.SourceVersion,
		CurrentSourceVersion: observedCurrentVersion, ContractRevisionID: revisionID, RecordedAt: recordedAt,
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSourceObservationResult{}, err
	}
	return result, nil
}

func ensureSourceVersionImmutable(ctx context.Context, tx pgx.Tx, tenantID, sourceID, externalID, version, digest string, sequence *uint64) error {
	var priorDigest string
	var priorSequence *int64
	err := tx.QueryRow(ctx, `SELECT source_snapshot_digest,source_sequence FROM clm_contract_source_observations
		WHERE tenant_id=$1 AND source_id=$2 AND external_contract_id=$3 AND source_version=$4
		ORDER BY recorded_at LIMIT 1`, tenantID, sourceID, externalID, version).Scan(&priorDigest, &priorSequence)
	hasPriorVersion := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if hasPriorVersion && priorDigest != digest {
		return fault.Conflict("同一来源版本对应不同摘要或序号")
	}
	if hasPriorVersion {
		if (priorSequence == nil) != (sequence == nil) ||
			(priorSequence != nil && (sequence == nil || uint64(*priorSequence) != *sequence)) {
			return fault.Conflict("同一来源版本的提供方序号不一致")
		}
	}
	if sequence != nil {
		var otherVersion string
		err := tx.QueryRow(ctx, `SELECT source_version FROM clm_contract_source_observations
			WHERE tenant_id=$1 AND source_id=$2 AND external_contract_id=$3 AND source_sequence=$4 AND source_version<>$5
			ORDER BY recorded_at LIMIT 1`, tenantID, sourceID, externalID, int64(*sequence), version).Scan(&otherVersion)
		if err == nil {
			return fault.Conflict("不同来源版本不能复用同一提供方序号")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}

func validateContractSourceAuthority(authority ContractSourceAuthority) error {
	if strings.TrimSpace(authority.TenantID) == "" || strings.TrimSpace(authority.SourceID) == "" || strings.TrimSpace(authority.KeyID) == "" {
		return errors.New("source authority identity is incomplete")
	}
	if authority.Kind != string(clmsource.SourceMock) && authority.Kind != string(clmsource.SourceReal) {
		return errors.New("source kind is invalid")
	}
	if authority.Ordering != clmsource.VersionOpaque && authority.Ordering != clmsource.VersionMonotonicSequence {
		return errors.New("source version ordering is invalid")
	}
	return nil
}

func validateContractSourceObservationInput(input *ContractSourceObservationInput, ordering clmsource.VersionOrdering) error {
	if strings.TrimSpace(input.EventID) == "" || len(input.EventID) > 300 || strings.TrimSpace(input.EventID) != input.EventID {
		return fault.Invalid("source event_id 无效")
	}
	if strings.TrimSpace(input.ExternalContractID) == "" || len(input.ExternalContractID) > 200 || strings.TrimSpace(input.ExternalContractID) != input.ExternalContractID {
		return fault.Invalid("external_contract_id 无效")
	}
	if strings.TrimSpace(input.SourceVersion) == "" || len(input.SourceVersion) > 200 || strings.TrimSpace(input.SourceVersion) != input.SourceVersion {
		return fault.Invalid("source_version 无效")
	}
	if input.SourceSequence != nil && (*input.SourceSequence == 0 || ordering != clmsource.VersionMonotonicSequence) {
		return fault.Invalid("source_sequence 与配置的来源版本策略不匹配")
	}
	if len(input.RawBody) == 0 || len(input.RawBody) > 1<<20 {
		return fault.Invalid("来源事件原文长度无效")
	}
	if input.OccurredAt.IsZero() {
		return fault.Invalid("occurred_at 无效")
	}
	if len(input.SourceState) > 200 || strings.ContainsAny(input.SourceState, "\x00\r\n") {
		return fault.Invalid("source_state 无效")
	}
	return nil
}

func sourceAmountCurrencyValid(contract *ContractBasics) bool {
	if contract == nil || contract.Amount == "" || contract.Currency == "" ||
		validateDraftMoney("contract.amount", contract.Amount) != nil || !currencyPattern.MatchString(contract.Currency) {
		return false
	}
	return true
}

func cloneSourceSnapshot(snapshot DraftContent) (DraftContent, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return DraftContent{}, err
	}
	var cloned DraftContent
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return DraftContent{}, err
	}
	return cloned, nil
}

func contractSourcePayloadDigest(authority ContractSourceAuthority, input ContractSourceObservationInput) (string, error) {
	return canonicalHash(struct {
		Schema             string       `json:"schema"`
		TenantID           string       `json:"tenant_id"`
		SourceID           string       `json:"source_id"`
		EventID            string       `json:"event_id"`
		ExternalContractID string       `json:"external_contract_id"`
		SourceVersion      string       `json:"source_version"`
		SourceSequence     *uint64      `json:"source_sequence,omitempty"`
		SourceState        string       `json:"source_state"`
		OccurredAt         time.Time    `json:"occurred_at"`
		CaseOrgID          string       `json:"case_org_id"`
		CaseID             string       `json:"case_id"`
		Snapshot           DraftContent `json:"snapshot"`
	}{"clm-contract-source-observation/v1", authority.TenantID, authority.SourceID, input.EventID,
		input.ExternalContractID, input.SourceVersion, input.SourceSequence, input.SourceState,
		input.OccurredAt.UTC(), input.CaseOrgID, input.CaseID, input.Snapshot})
}

func contractSourceSnapshotDigest(authority ContractSourceAuthority, input ContractSourceObservationInput) (string, error) {
	return canonicalHash(struct {
		Schema             string       `json:"schema"`
		TenantID           string       `json:"tenant_id"`
		SourceID           string       `json:"source_id"`
		ExternalContractID string       `json:"external_contract_id"`
		SourceVersion      string       `json:"source_version"`
		SourceSequence     *uint64      `json:"source_sequence,omitempty"`
		SourceState        string       `json:"source_state"`
		Snapshot           DraftContent `json:"snapshot"`
	}{"clm-contract-source-snapshot/v1", authority.TenantID, authority.SourceID, input.ExternalContractID,
		input.SourceVersion, input.SourceSequence, input.SourceState, input.Snapshot})
}

func contractSourceAllowsCase(scopes []ContractSourceCaseScope, orgID, caseID string) bool {
	for _, scope := range scopes {
		if scope.OrgID == orgID && scope.CaseID == caseID {
			return true
		}
	}
	return false
}

func ensureSourceCaseScope(ctx context.Context, tx pgx.Tx, authority ContractSourceAuthority, orgID, caseID string) error {
	if !contractSourceAllowsCase(authority.AllowedCases, orgID, caseID) {
		return fault.NotFound("没有找到对应记录")
	}
	var archivedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT archived_at FROM cases WHERE tenant_id=$1 AND org_id=$2 AND id=$3 FOR KEY SHARE`, authority.TenantID, orgID, caseID).Scan(&archivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	if archivedAt != nil {
		return fault.Conflict("来源绑定的案件已归档")
	}
	return nil
}

func ensureContractCaseBinding(ctx context.Context, tx pgx.Tx, tenantID, contractID, orgID, caseID string) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM clm_contract_cases WHERE tenant_id=$1 AND contract_id=$2`, tenantID, contractID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fault.Conflict("外部合同来源绑定不唯一")
	}
	var linked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_cases WHERE tenant_id=$1 AND contract_id=$2 AND case_org_id=$3 AND case_id=$4)`, tenantID, contractID, orgID, caseID).Scan(&linked); err != nil {
		return err
	}
	if !linked {
		return fault.NotFound("没有找到对应记录")
	}
	return nil
}

func scopedSourceEventLock(ctx context.Context, tx pgx.Tx, tenantID, sourceID, eventID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-source-event:' || $1 || ':' || $2 || ':' || $3,0))`, tenantID, sourceID, eventID)
	return err
}

func scopedSourceContractLock(ctx context.Context, tx pgx.Tx, tenantID, sourceID, externalID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-source-contract:' || $1 || ':' || $2 || ':' || $3,0))`, tenantID, sourceID, externalID)
	return err
}

func loadSourceEvent(ctx context.Context, tx pgx.Tx, tenantID, sourceID, eventID string) (storedSourceObservation, error) {
	var item storedSourceObservation
	err := tx.QueryRow(ctx, `SELECT o.observation_id::text,o.contract_id::text,o.source_kind,o.payload_digest,o.disposition,
		o.contract_revision_id::text,o.source_version,o.current_source_version,o.recorded_at
		FROM clm_contract_source_observations o WHERE o.tenant_id=$1 AND o.source_id=$2 AND o.source_event_id=$3`,
		tenantID, sourceID, eventID).Scan(&item.ObservationID, &item.ContractID, &item.SourceKind, &item.PayloadDigest, &item.Disposition,
		&item.RevisionID, &item.SourceVersion, &item.CurrentSourceVersion, &item.RecordedAt)
	if err != nil {
		return item, err
	}
	return item, nil
}

func sourceObservationResult(item storedSourceObservation, eventID string, replayed bool) ContractSourceObservationResult {
	return ContractSourceObservationResult{
		ObservationID: item.ObservationID, ContractID: item.ContractID, SourceEventID: eventID,
		SourceKind: item.SourceKind, AuthorityMode: string(clmsource.AuthorityExternal), Disposition: item.Disposition,
		Replayed: replayed, SourceVersion: item.SourceVersion, CurrentSourceVersion: item.CurrentSourceVersion,
		ContractRevisionID: item.RevisionID, RecordedAt: item.RecordedAt,
	}
}

func loadSourceContractForUpdate(ctx context.Context, tx pgx.Tx, tenantID, sourceID, externalID string) (clmContractRecord, bool, error) {
	record, err := scanCLMContract(tx.QueryRow(ctx, `SELECT `+clmContractColumns+` FROM clm_contracts
		WHERE tenant_id=$1 AND source_system=$2 AND external_contract_id=$3 FOR UPDATE`, tenantID, sourceID, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return clmContractRecord{}, false, nil
	}
	if err != nil {
		return clmContractRecord{}, false, err
	}
	return record, true, nil
}

func createExternalContractIdentity(ctx context.Context, tx pgx.Tx, authority ContractSourceAuthority, input ContractSourceObservationInput, contractID string, currentVersion *string) (clmContractRecord, error) {
	var createdAt time.Time
	err := tx.QueryRow(ctx, `INSERT INTO clm_contracts
		(tenant_id,contract_id,owner_org_id,authority_mode,contract_number,source_system,external_contract_id,source_version,lifecycle_state,row_version,created_by)
		VALUES($1,$2,$3,'external',NULL,$4,$5,$6,'external_observed',1,$7) RETURNING created_at`,
		authority.TenantID, contractID, input.CaseOrgID, authority.SourceID, input.ExternalContractID,
		currentVersion, "clm-source:"+authority.SourceID).Scan(&createdAt)
	if err != nil {
		return clmContractRecord{}, clmUniqueViolation(err, "来源合同身份已存在")
	}
	return clmContractRecord{TenantID: authority.TenantID, ContractID: contractID, OwnerOrgID: input.CaseOrgID,
		AuthorityMode: string(clmsource.AuthorityExternal), SourceSystem: new(authority.SourceID),
		ExternalContractID: new(input.ExternalContractID), SourceVersion: currentVersion,
		LifecycleState: "external_observed", ValidityState: "active", RowVersion: 1,
		CreatedBy: "clm-source:" + authority.SourceID, CreatedAt: createdAt, UpdatedAt: createdAt}, nil
}

func sourceVersionBaseline(ctx context.Context, tx pgx.Tx, tenantID, sourceID, externalID string, ordering clmsource.VersionOrdering) (string, clmsource.SnapshotVersion, bool, error) {
	var current clmsource.SnapshotVersion
	var sequence *int64
	orderBy := "recorded_at DESC"
	if ordering == clmsource.VersionMonotonicSequence {
		orderBy = "source_sequence DESC NULLS LAST, recorded_at DESC"
	}
	query := `SELECT source_version,source_snapshot_digest,source_sequence FROM clm_contract_source_observations
		WHERE tenant_id=$1 AND source_id=$2 AND external_contract_id=$3 ORDER BY ` + orderBy + ` LIMIT 1`
	err := tx.QueryRow(ctx, query, tenantID, sourceID, externalID).
		Scan(&current.Version, &current.Digest, &sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", clmsource.SnapshotVersion{}, false, nil
	}
	if err != nil {
		return "", current, false, err
	}
	if sequence != nil && *sequence > 0 {
		current.Sequence = uint64(*sequence)
	}
	return current.Version, current, true, nil
}

func appendExternalContractRevision(ctx context.Context, tx pgx.Tx, authority ContractSourceAuthority, input ContractSourceObservationInput, record clmContractRecord, snapshot DraftContent) (*string, error) {
	_, sealed, _, err := sealDraftContent(snapshot)
	if err != nil {
		return nil, err
	}
	sealed, err = canonicalStoredJSON(ctx, tx, sealed)
	if err != nil {
		return nil, err
	}
	revisionID, err := newContractUUID()
	if err != nil {
		return nil, err
	}
	var revisionNumber int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision_number),0)+1 FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2`, authority.TenantID, record.ContractID).Scan(&revisionNumber); err != nil {
		return nil, err
	}
	amount, currency := legacyAmountPair(snapshot)
	if amount == nil || currency == nil {
		return nil, fault.Invalid("来源金额或币种未知时不能创建合同修订")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by,amounts_reconciled)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,false)`, authority.TenantID, record.ContractID,
		revisionID, revisionNumber, amount, currency, string(sealed), sha256Bytes(sealed), "clm-source:"+authority.SourceID); err != nil {
		return nil, err
	}
	if err := insertFrozenContractRevisionItems(ctx, tx, authority.TenantID, record.ContractID, revisionID, snapshot.Items); err != nil {
		return nil, err
	}
	return &revisionID, nil
}

func checkExternalContractCaseRead(ctx context.Context, tx pgx.Tx, record clmContractRecord, current accounts.User) bool {
	if record.AuthorityMode != string(clmsource.AuthorityExternal) {
		return true
	}
	rows, err := tx.Query(ctx, `SELECT c.id,c.tenant_id,c.org_id,c.owner_id,c.member_ids,c.business_status,c.archived_at
		FROM clm_contract_cases cc JOIN cases c ON c.tenant_id=cc.tenant_id AND c.org_id=cc.case_org_id AND c.id=cc.case_id
		WHERE cc.tenant_id=$1 AND cc.contract_id=$2`, record.TenantID, record.ContractID)
	if err != nil {
		return false
	}
	defer rows.Close()
	var linked *Case
	for rows.Next() {
		item := new(Case)
		var members string
		if rows.Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members, &item.BusinessStatus, &item.ArchivedAt) != nil {
			return false
		}
		if json.Unmarshal([]byte(members), &item.MemberIDs) != nil {
			return false
		}
		if linked != nil {
			return false
		}
		linked = item
	}
	if rows.Err() != nil || linked == nil {
		return false
	}
	return authorizeCase(*linked, current, access.Read)
}

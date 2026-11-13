package postgres

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/clmsource"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// SourceObservations persists Contract-owned source identities, observations,
// and their immutable revision linkage in the caller's transaction.
type SourceObservations struct {
	tx       platform.Executor
	versions *VersionCommands
}

func NewSourceObservations(tx platform.Executor) *SourceObservations {
	return &SourceObservations{tx: tx, versions: NewVersionCommands(tx)}
}

func (r *SourceObservations) LockSourceEvent(ctx context.Context, tenantID, sourceID, eventID string) error {
	_, err := r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-source-event:' || $1 || ':' || $2 || ':' || $3,0))`, tenantID, sourceID, eventID)
	return err
}

func (r *SourceObservations) LockSourceContract(ctx context.Context, tenantID, sourceID, externalID string) error {
	_, err := r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-source-contract:' || $1 || ':' || $2 || ':' || $3,0))`, tenantID, sourceID, externalID)
	return err
}

func (r *SourceObservations) LoadSourceEvent(ctx context.Context, tenantID, sourceID, eventID string) (clmcontract.StoredContractSourceObservation, bool, error) {
	var item clmcontract.StoredContractSourceObservation
	err := r.tx.QueryRow(ctx, `SELECT o.observation_id::text,o.contract_id::text,o.source_kind,o.payload_digest,o.disposition,
		o.contract_revision_id::text,o.source_version,o.current_source_version,o.recorded_at
		FROM clm_contract_source_observations o WHERE o.tenant_id=$1 AND o.source_id=$2 AND o.source_event_id=$3`,
		tenantID, sourceID, eventID).Scan(&item.ObservationID, &item.ContractID, &item.SourceKind, &item.PayloadDigest,
		&item.Disposition, &item.RevisionID, &item.SourceVersion, &item.CurrentSourceVersion, &item.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.StoredContractSourceObservation{}, false, nil
	}
	if err != nil {
		return clmcontract.StoredContractSourceObservation{}, false, err
	}
	return item, true, nil
}

func (r *SourceObservations) EnsureContractCaseBinding(ctx context.Context, tenantID, contractID, orgID, caseID string) error {
	var count int
	if err := r.tx.QueryRow(ctx, `SELECT count(*) FROM clm_contract_cases WHERE tenant_id=$1 AND contract_id=$2`,
		tenantID, contractID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fault.Conflict("外部合同来源绑定不唯一")
	}
	var linked bool
	if err := r.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_cases
		WHERE tenant_id=$1 AND contract_id=$2 AND case_org_id=$3 AND case_id=$4)`,
		tenantID, contractID, orgID, caseID).Scan(&linked); err != nil {
		return err
	}
	if !linked {
		return fault.NotFound("没有找到对应记录")
	}
	return nil
}

func (r *SourceObservations) LoadExternalContractForUpdate(ctx context.Context,
	tenantID, sourceID, externalID string) (clmcontract.Aggregate, bool, error) {
	var record clmcontract.Aggregate
	err := r.tx.QueryRow(ctx, `SELECT `+sourceContractColumns+` FROM clm_contracts
		WHERE tenant_id=$1 AND source_system=$2 AND external_contract_id=$3 FOR UPDATE`,
		tenantID, sourceID, externalID).Scan(
		&record.TenantID, &record.ContractID, &record.OwnerOrgID, &record.AuthorityMode,
		&record.ContractNumber, &record.SourceSystem, &record.ExternalContractID, &record.SourceVersion,
		&record.LifecycleState, &record.ValidityState, &record.CurrentRevisionID, &record.RowVersion,
		&record.CreatedBy, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.Aggregate{}, false, nil
	}
	if err != nil {
		return clmcontract.Aggregate{}, false, err
	}
	return record, true, nil
}

func (r *SourceObservations) LoadSourceVersionIdentity(ctx context.Context, tenantID, sourceID,
	externalID, version string) (clmcontract.SourceVersionIdentity, bool, error) {
	var item clmcontract.SourceVersionIdentity
	var sequence *int64
	err := r.tx.QueryRow(ctx, `SELECT source_snapshot_digest,source_sequence FROM clm_contract_source_observations
		WHERE tenant_id=$1 AND source_id=$2 AND external_contract_id=$3 AND source_version=$4
		ORDER BY recorded_at LIMIT 1`, tenantID, sourceID, externalID, version).Scan(&item.SnapshotDigest, &sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.SourceVersionIdentity{}, false, nil
	}
	if err != nil {
		return clmcontract.SourceVersionIdentity{}, false, err
	}
	item.Sequence = sourceSequenceUint64(sequence)
	return item, true, nil
}

func (r *SourceObservations) LoadSourceSequenceVersion(ctx context.Context, tenantID, sourceID,
	externalID string, sequence uint64, exceptVersion string) (string, bool, error) {
	var version string
	err := r.tx.QueryRow(ctx, `SELECT source_version FROM clm_contract_source_observations
		WHERE tenant_id=$1 AND source_id=$2 AND external_contract_id=$3 AND source_sequence=$4 AND source_version<>$5
		ORDER BY recorded_at LIMIT 1`, tenantID, sourceID, externalID, int64(sequence), exceptVersion).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return version, true, nil
}

func (r *SourceObservations) LoadLatestSourceVersion(ctx context.Context, tenantID, sourceID,
	externalID string, ordering clmsource.VersionOrdering) (clmsource.SnapshotVersion, bool, error) {
	var current clmsource.SnapshotVersion
	var sequence *int64
	orderBy := "recorded_at DESC"
	if ordering == clmsource.VersionMonotonicSequence {
		orderBy = "source_sequence DESC NULLS LAST, recorded_at DESC"
	}
	err := r.tx.QueryRow(ctx, `SELECT source_version,source_snapshot_digest,source_sequence FROM clm_contract_source_observations
		WHERE tenant_id=$1 AND source_id=$2 AND external_contract_id=$3 ORDER BY `+orderBy+` LIMIT 1`,
		tenantID, sourceID, externalID).Scan(&current.Version, &current.Digest, &sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmsource.SnapshotVersion{}, false, nil
	}
	if err != nil {
		return clmsource.SnapshotVersion{}, false, err
	}
	if sequence != nil && *sequence > 0 {
		current.Sequence = uint64(*sequence)
	}
	return current, true, nil
}

func (r *SourceObservations) CreateExternalContractIdentity(ctx context.Context,
	record clmcontract.Aggregate) (clmcontract.Aggregate, error) {
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contracts
		(tenant_id,contract_id,owner_org_id,authority_mode,contract_number,source_system,external_contract_id,source_version,lifecycle_state,row_version,created_by)
		VALUES($1,$2,$3,'external',NULL,$4,$5,$6,'external_observed',1,$7) RETURNING created_at`,
		record.TenantID, record.ContractID, record.OwnerOrgID, record.SourceSystem,
		record.ExternalContractID, record.SourceVersion, record.CreatedBy).Scan(&record.CreatedAt)
	if err != nil {
		return clmcontract.Aggregate{}, uniqueViolation(err, "来源合同身份已存在")
	}
	record.LifecycleState = "external_observed"
	record.ValidityState = "active"
	record.RowVersion = 1
	record.UpdatedAt = record.CreatedAt
	return record, nil
}

func (r *SourceObservations) InsertContractCaseLink(ctx context.Context, tenantID, contractID,
	caseOrgID, caseID, actorID string) error {
	return r.versions.InsertContractCaseLink(ctx, tenantID, contractID, caseOrgID, caseID, actorID)
}

func (r *SourceObservations) CanonicalizeSnapshot(ctx context.Context, raw []byte) ([]byte, error) {
	return r.versions.CanonicalizeSnapshot(ctx, raw)
}

func (r *SourceObservations) NextRevisionNumber(ctx context.Context, tenantID, contractID string) (int, error) {
	return r.versions.NextRevisionNumber(ctx, tenantID, contractID)
}

func (r *SourceObservations) InsertSourceRevision(ctx context.Context, tenantID, contractID string,
	revision clmcontract.FrozenContractRevision) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by,amounts_reconciled)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,false)`, tenantID, contractID,
		revision.RevisionID, revision.RevisionNumber, revision.Amount, revision.Currency,
		string(revision.Snapshot), revision.SnapshotDigest, revision.FrozenBy)
	return err
}

func (r *SourceObservations) InsertRevisionPlanItems(ctx context.Context, tenantID, contractID,
	revisionID string, items []clmcontract.RevisionPlanItem) error {
	return r.versions.InsertFrozenRevisionItems(ctx, tenantID, contractID, revisionID, items)
}

func (r *SourceObservations) UpdateSourceContract(ctx context.Context, record clmcontract.Aggregate,
	isNew bool, currentVersion, revisionID, contractNumber *string) error {
	if isNew {
		_, err := r.tx.Exec(ctx, `UPDATE clm_contracts SET source_version=$3,current_revision_id=$4,
			contract_number=$5,row_version=row_version+1,updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND contract_id=$2`, record.TenantID, record.ContractID,
			currentVersion, revisionID, contractNumber)
		return err
	}
	_, err := r.tx.Exec(ctx, `UPDATE clm_contracts SET lifecycle_state='external_observed',
		source_version=$3,current_revision_id=COALESCE($4,current_revision_id),
		contract_number=CASE WHEN $4::uuid IS NOT NULL THEN $5 ELSE contract_number END,
		row_version=row_version+1,updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND authority_mode='external' AND row_version=$6`,
		record.TenantID, record.ContractID, currentVersion, revisionID, contractNumber, record.RowVersion)
	return err
}

func (r *SourceObservations) InsertSourceObservation(ctx context.Context,
	item clmcontract.SourceObservationWrite) (time.Time, error) {
	var sourceSequence any
	if item.SourceSequence != nil {
		sourceSequence = int64(*item.SourceSequence)
	}
	var revisionID any
	if item.ContractRevisionID != nil {
		revisionID = *item.ContractRevisionID
	}
	var recordedAt time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_source_observations
		(tenant_id,observation_id,contract_id,source_id,source_kind,source_key_id,external_contract_id,source_event_id,
		 source_version,current_source_version,source_sequence,payload_digest,source_snapshot_digest,raw_payload_digest,raw_payload_ciphertext,
		 source_observed_state,disposition,contract_revision_id,case_org_id,case_id,source_occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		RETURNING recorded_at`, item.TenantID, item.ObservationID, item.ContractID, item.SourceID,
		item.SourceKind, item.SourceKeyID, item.ExternalContractID, item.SourceEventID,
		item.SourceVersion, item.CurrentSourceVersion, sourceSequence, item.PayloadDigest, item.SnapshotDigest,
		item.RawPayloadDigest, item.RawPayloadCiphertext, item.SourceObservedState, item.Disposition,
		revisionID, item.CaseOrgID, item.CaseID, item.SourceOccurredAt).Scan(&recordedAt)
	return recordedAt, err
}

const sourceContractColumns = `tenant_id,contract_id,owner_org_id,authority_mode,contract_number,
	source_system,external_contract_id,source_version,lifecycle_state,validity_state,current_revision_id,
	row_version,created_by,created_at,updated_at`

func sourceSequenceUint64(sequence *int64) *uint64 {
	if sequence == nil {
		return nil
	}
	value := uint64(*sequence)
	return &value
}

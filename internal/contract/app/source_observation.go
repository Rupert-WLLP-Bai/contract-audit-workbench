// File metadata (manifest mirror):
// Purpose: Coordinates source observation preparation, semantic and raw digests, ordered locking, idempotency and version rules, frozen revision creation and atomic business events.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/clmsource"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

const ExternalSourceObservedEvent = "contract.external_source_observed.v1"

type SourceObservationCaseAccess interface {
	LockActiveSourceCase(context.Context, clmcontract.ContractSourceAuthority, string, string) error
}

// SourceObservationRepository exposes only Contract-owned rows in the
// caller's transaction. It cannot set scope or control transaction lifetime.
type SourceObservationRepository interface {
	LockSourceEvent(context.Context, string, string, string) error
	LockSourceContract(context.Context, string, string, string) error
	LoadSourceEvent(context.Context, string, string, string) (clmcontract.StoredContractSourceObservation, bool, error)
	EnsureContractCaseBinding(context.Context, string, string, string, string) error
	LoadExternalContractForUpdate(context.Context, string, string, string) (clmcontract.Aggregate, bool, error)
	LoadSourceVersionIdentity(context.Context, string, string, string, string) (clmcontract.SourceVersionIdentity, bool, error)
	LoadSourceSequenceVersion(context.Context, string, string, string, uint64, string) (string, bool, error)
	LoadLatestSourceVersion(context.Context, string, string, string, clmsource.VersionOrdering) (clmsource.SnapshotVersion, bool, error)
	CreateExternalContractIdentity(context.Context, clmcontract.Aggregate) (clmcontract.Aggregate, error)
	InsertContractCaseLink(context.Context, string, string, string, string, string) error
	CanonicalizeSnapshot(context.Context, []byte) ([]byte, error)
	NextRevisionNumber(context.Context, string, string) (int, error)
	InsertSourceRevision(context.Context, string, string, clmcontract.FrozenContractRevision) error
	InsertRevisionPlanItems(context.Context, string, string, string, []clmcontract.RevisionPlanItem) error
	UpdateSourceContract(context.Context, clmcontract.Aggregate, bool, *string, *string, *string) error
	InsertSourceObservation(context.Context, clmcontract.SourceObservationWrite) (time.Time, error)
}

type SourceObservationRawCodec interface {
	SealSourceObservation(context.Context, []byte, string, string, string, string) (string, error)
}

type SourceObservationEventWriter interface {
	AppendContractEvent(context.Context, string, string, string, int64, string, []byte) error
	EnqueueContractEvent(context.Context, string, string) error
}

type PreparedContractSourceObservation struct {
	Observation      clmcontract.PreparedContractSourceObservation
	PayloadDigest    string
	SnapshotDigest   string
	RawPayloadDigest string
}

func PrepareContractSourceObservation(authority clmcontract.ContractSourceAuthority,
	input clmcontract.ContractSourceObservationInput) (PreparedContractSourceObservation, error) {
	prepared, err := clmcontract.PrepareContractSourceObservation(authority, input)
	if err != nil {
		return PreparedContractSourceObservation{}, err
	}
	canonicalDigest, err := jsonDigest(struct {
		Schema             string                   `json:"schema"`
		TenantID           string                   `json:"tenant_id"`
		SourceID           string                   `json:"source_id"`
		EventID            string                   `json:"event_id"`
		ExternalContractID string                   `json:"external_contract_id"`
		SourceVersion      string                   `json:"source_version"`
		SourceSequence     *uint64                  `json:"source_sequence,omitempty"`
		SourceState        string                   `json:"source_state"`
		OccurredAt         time.Time                `json:"occurred_at"`
		CaseOrgID          string                   `json:"case_org_id"`
		CaseID             string                   `json:"case_id"`
		Snapshot           clmcontract.DraftContent `json:"snapshot"`
	}{"clm-contract-source-observation/v1", prepared.Authority.TenantID, prepared.Authority.SourceID,
		prepared.Input.EventID, prepared.Input.ExternalContractID, prepared.Input.SourceVersion,
		prepared.Input.SourceSequence, prepared.Input.SourceState, prepared.Input.OccurredAt.UTC(),
		prepared.Input.CaseOrgID, prepared.Input.CaseID, prepared.DigestSnapshot})
	if err != nil {
		return PreparedContractSourceObservation{}, err
	}
	snapshotDigest, err := jsonDigest(struct {
		Schema             string                   `json:"schema"`
		TenantID           string                   `json:"tenant_id"`
		SourceID           string                   `json:"source_id"`
		ExternalContractID string                   `json:"external_contract_id"`
		SourceVersion      string                   `json:"source_version"`
		SourceSequence     *uint64                  `json:"source_sequence,omitempty"`
		SourceState        string                   `json:"source_state"`
		Snapshot           clmcontract.DraftContent `json:"snapshot"`
	}{"clm-contract-source-snapshot/v1", prepared.Authority.TenantID, prepared.Authority.SourceID,
		prepared.Input.ExternalContractID, prepared.Input.SourceVersion, prepared.Input.SourceSequence,
		prepared.Input.SourceState, prepared.DigestSnapshot})
	if err != nil {
		return PreparedContractSourceObservation{}, err
	}
	return PreparedContractSourceObservation{
		Observation: prepared, PayloadDigest: canonicalDigest, SnapshotDigest: snapshotDigest,
		RawPayloadDigest: bytesDigest(prepared.Input.RawBody),
	}, nil
}

func RecordExternalContractObservation(ctx context.Context, prepared PreparedContractSourceObservation,
	caseAccess SourceObservationCaseAccess, repository SourceObservationRepository,
	codec DraftContentCodec, rawCodec SourceObservationRawCodec, events SourceObservationEventWriter) (
	clmcontract.ContractSourceObservationResult, error) {
	observation := prepared.Observation
	authority, input := observation.Authority, observation.Input
	if err := caseAccess.LockActiveSourceCase(ctx, authority, input.CaseOrgID, input.CaseID); err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	if err := repository.LockSourceEvent(ctx, authority.TenantID, authority.SourceID, input.EventID); err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	if err := repository.LockSourceContract(ctx, authority.TenantID, authority.SourceID, input.ExternalContractID); err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	if prior, found, err := repository.LoadSourceEvent(ctx, authority.TenantID, authority.SourceID, input.EventID); err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	} else if found {
		if prior.PayloadDigest != prepared.PayloadDigest {
			return clmcontract.ContractSourceObservationResult{}, fault.Conflict("来源事件ID已用于不同载荷")
		}
		if err := repository.EnsureContractCaseBinding(ctx, authority.TenantID, prior.ContractID, input.CaseOrgID, input.CaseID); err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
		return sourceObservationResult(prior, input.EventID, true), nil
	}

	record, exists, err := repository.LoadExternalContractForUpdate(ctx, authority.TenantID, authority.SourceID, input.ExternalContractID)
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	if exists && record.AuthorityMode != string(clmsource.AuthorityExternal) {
		return clmcontract.ContractSourceObservationResult{}, fault.Conflict("来源身份已绑定本地权威合同")
	}
	if exists {
		if err := repository.EnsureContractCaseBinding(ctx, authority.TenantID, record.ContractID, input.CaseOrgID, input.CaseID); err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
	}
	priorVersion, hasPriorVersion, err := repository.LoadSourceVersionIdentity(ctx,
		authority.TenantID, authority.SourceID, input.ExternalContractID, input.SourceVersion)
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	if hasPriorVersion && priorVersion.SnapshotDigest != prepared.SnapshotDigest {
		return clmcontract.ContractSourceObservationResult{}, fault.Conflict("同一来源版本对应不同摘要或序号")
	}
	if hasPriorVersion && !sameSourceSequence(priorVersion.Sequence, input.SourceSequence) {
		return clmcontract.ContractSourceObservationResult{}, fault.Conflict("同一来源版本的提供方序号不一致")
	}
	if input.SourceSequence != nil {
		otherVersion, found, err := repository.LoadSourceSequenceVersion(ctx, authority.TenantID,
			authority.SourceID, input.ExternalContractID, *input.SourceSequence, input.SourceVersion)
		if err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
		if found && otherVersion != input.SourceVersion {
			return clmcontract.ContractSourceObservationResult{}, fault.Conflict("不同来源版本不能复用同一提供方序号")
		}
	}

	currentSnapshot, hasBaseline, err := repository.LoadLatestSourceVersion(ctx,
		authority.TenantID, authority.SourceID, input.ExternalContractID, authority.Ordering)
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	incoming := clmsource.SnapshotVersion{Version: input.SourceVersion, Digest: prepared.SnapshotDigest}
	if input.SourceSequence != nil {
		incoming.Sequence = *input.SourceSequence
	}
	decision := clmsource.VersionArchiveObservationOnly
	if !exists {
		decision, err = clmsource.CompareVersion(clmsource.SnapshotVersion{}, incoming, authority.Ordering)
	} else if hasBaseline {
		decision, err = clmsource.CompareVersion(currentSnapshot, incoming, authority.Ordering)
	}
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, fault.Conflict("来源版本和摘要不一致")
	}
	newContract := !exists
	if newContract {
		contractID, err := clmcontract.NewContractUUID()
		if err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
		var initialCurrentVersion *string
		if decision == clmsource.VersionAdvanceCurrent && observation.AmountCurrencyValid {
			initialCurrentVersion = new(input.SourceVersion)
		}
		record = clmcontract.Aggregate{
			TenantID: authority.TenantID, ContractID: contractID, OwnerOrgID: input.CaseOrgID,
			AuthorityMode: string(clmsource.AuthorityExternal),
			SourceSystem:  new(authority.SourceID), ExternalContractID: new(input.ExternalContractID),
			SourceVersion: initialCurrentVersion, LifecycleState: "external_observed", ValidityState: "active",
			RowVersion: 1, CreatedBy: "clm-source:" + authority.SourceID,
		}
		record, err = repository.CreateExternalContractIdentity(ctx, record)
		if err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
		if err := repository.InsertContractCaseLink(ctx, authority.TenantID, record.ContractID,
			input.CaseOrgID, input.CaseID, record.CreatedBy); err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
	}

	observationID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	ciphertext, err := rawCodec.SealSourceObservation(ctx, input.RawBody, authority.TenantID,
		authority.SourceID, input.EventID, observationID)
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	disposition := "observation_only"
	var revisionID *string
	apply := decision == clmsource.VersionAdvanceCurrent && observation.AmountCurrencyValid
	if !observation.AmountCurrencyValid {
		disposition = "unknown"
	} else if apply {
		disposition = "applied"
	} else if decision == clmsource.VersionConflict {
		return clmcontract.ContractSourceObservationResult{}, fault.Conflict("同一来源版本对应不同摘要或序号")
	}
	if decision == clmsource.VersionReplay {
		disposition = "observation_only"
	}
	if apply {
		revisionID, err = appendExternalContractRevision(ctx, prepared, record, codec, repository)
		if err != nil {
			return clmcontract.ContractSourceObservationResult{}, err
		}
	}
	observedCurrentVersion := record.SourceVersion
	if apply {
		observedCurrentVersion = new(input.SourceVersion)
	}
	var contractNumber *string
	if apply && input.Snapshot.Contract.Number != nil {
		contractNumber = clmcontract.OptionalText(*input.Snapshot.Contract.Number)
	}
	if err := repository.UpdateSourceContract(ctx, record, newContract, observedCurrentVersion,
		revisionID, contractNumber); err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	recordedAt, err := repository.InsertSourceObservation(ctx, clmcontract.SourceObservationWrite{
		TenantID: authority.TenantID, ObservationID: observationID, ContractID: record.ContractID,
		SourceID: authority.SourceID, SourceKind: authority.Kind, SourceKeyID: authority.KeyID,
		ExternalContractID: input.ExternalContractID, SourceEventID: input.EventID,
		SourceVersion: input.SourceVersion, CurrentSourceVersion: observedCurrentVersion,
		SourceSequence: input.SourceSequence, PayloadDigest: prepared.PayloadDigest,
		SnapshotDigest: prepared.SnapshotDigest, RawPayloadDigest: prepared.RawPayloadDigest,
		RawPayloadCiphertext: ciphertext, SourceObservedState: input.SourceState,
		Disposition: disposition, ContractRevisionID: revisionID,
		CaseOrgID: input.CaseOrgID, CaseID: input.CaseID, SourceOccurredAt: input.OccurredAt.UTC(),
	})
	if err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	if err := appendSourceObservedEvent(ctx, events, authority.TenantID, authority.SourceID,
		record.ContractID, record.RowVersion+1, observationID, input,
		prepared.PayloadDigest, disposition, revisionID); err != nil {
		return clmcontract.ContractSourceObservationResult{}, err
	}
	return clmcontract.ContractSourceObservationResult{
		ObservationID: observationID, ContractID: record.ContractID,
		SourceEventID: input.EventID, SourceKind: authority.Kind,
		AuthorityMode: string(clmsource.AuthorityExternal), Disposition: disposition,
		SourceVersion: input.SourceVersion, CurrentSourceVersion: observedCurrentVersion,
		ContractRevisionID: revisionID, RecordedAt: recordedAt,
	}, nil
}

func appendExternalContractRevision(ctx context.Context, prepared PreparedContractSourceObservation,
	record clmcontract.Aggregate, codec DraftContentCodec, repository SourceObservationRepository) (*string, error) {
	authority, input := prepared.Observation.Authority, prepared.Observation.Input
	sealed, err := codec.Seal(ctx, authority.TenantID, record.ContractID, input.Snapshot)
	if err != nil {
		return nil, err
	}
	sealed, err = repository.CanonicalizeSnapshot(ctx, sealed)
	if err != nil {
		return nil, err
	}
	revisionID, err := clmcontract.NewContractUUID()
	if err != nil {
		return nil, err
	}
	revisionNumber, err := repository.NextRevisionNumber(ctx, authority.TenantID, record.ContractID)
	if err != nil {
		return nil, err
	}
	amount, currency := clmcontract.LegacyAmountPair(input.Snapshot)
	if amount == nil || currency == nil {
		return nil, fault.Invalid("来源金额或币种未知时不能创建合同修订")
	}
	frozen := clmcontract.FrozenContractRevision{
		RevisionID: revisionID, RevisionNumber: revisionNumber, Amount: amount, Currency: currency,
		Snapshot: sealed, SnapshotDigest: bytesDigest(sealed), FrozenBy: "clm-source:" + authority.SourceID,
	}
	if err := repository.InsertSourceRevision(ctx, authority.TenantID, record.ContractID, frozen); err != nil {
		return nil, err
	}
	items := clmcontract.FrozenRevisionPlanItems(input.Snapshot.Items)
	if err := repository.InsertRevisionPlanItems(ctx, authority.TenantID, record.ContractID, revisionID, items); err != nil {
		return nil, err
	}
	return &revisionID, nil
}

func appendSourceObservedEvent(ctx context.Context, events SourceObservationEventWriter,
	tenantID, sourceID, contractID string, aggregateVersion int64, observationID string,
	input clmcontract.ContractSourceObservationInput, payloadDigest, disposition string,
	revisionID *string) error {
	eventID, err := clmcontract.NewContractUUID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"contract_id": contractID, "observation_id": observationID,
		"source_id": sourceID, "source_event_id": input.EventID,
		"source_version": input.SourceVersion, "payload_digest": payloadDigest,
		"disposition": disposition, "contract_revision_id": revisionID,
	})
	if err != nil {
		return err
	}
	if err := events.AppendContractEvent(ctx, tenantID, eventID, contractID, aggregateVersion,
		ExternalSourceObservedEvent, payload); err != nil {
		return err
	}
	if err := events.EnqueueContractEvent(ctx, tenantID, eventID); err != nil {
		return err
	}
	return nil
}

func sourceObservationResult(item clmcontract.StoredContractSourceObservation,
	eventID string, replayed bool) clmcontract.ContractSourceObservationResult {
	return clmcontract.ContractSourceObservationResult{
		ObservationID: item.ObservationID, ContractID: item.ContractID, SourceEventID: eventID,
		SourceKind: item.SourceKind, AuthorityMode: string(clmsource.AuthorityExternal),
		Disposition: item.Disposition, Replayed: replayed, SourceVersion: item.SourceVersion,
		CurrentSourceVersion: item.CurrentSourceVersion, ContractRevisionID: item.RevisionID,
		RecordedAt: item.RecordedAt,
	}
}

func sameSourceSequence(left, right *uint64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

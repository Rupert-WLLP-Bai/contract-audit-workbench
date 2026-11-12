package store

import (
	"context"
	"encoding/json"

	"contractaudit/internal/accounts"
	clmoutboxpostgres "contractaudit/internal/clmoutbox/postgres"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type storeVersionCodec struct{ storeDraftCodec }

func (storeVersionCodec) OpenForFreeze(ctx context.Context, tenantID, contractID string, sealed []byte) (clmcontract.DraftContent, error) {
	return unsealDraftForFreezeBound(ctx, sealed, tenantID, contractID)
}

func (storeVersionCodec) FingerprintVersion(ctx context.Context, tenantID, contractID string,
	draft clmcontract.DraftContent) ([]byte, string, error) {
	fingerprints, err := computeRestrictedFingerprintsBound(ctx, &draft,
		clmFieldBinding(tenantID, contractID, "supplier.payee_name"), nil)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(fingerprints)
	if err != nil {
		return nil, "", err
	}
	contentFingerprint, err := canonicalHash(struct {
		Fields     map[string]any              `json:"fields"`
		Restricted clmRestrictedFingerprintSet `json:"restricted"`
	}{Fields: flattenContractDraft(draft), Restricted: fingerprints})
	if err != nil {
		return nil, "", err
	}
	return encoded, contentFingerprint, nil
}

func (storeVersionCodec) ValidateVersionSeal(raw []byte) error {
	_, err := parseRestrictedFingerprints(raw)
	return err
}

type storeVersionReferenceResolver struct {
	tx   pgx.Tx
	user accounts.User
}

func (r storeVersionReferenceResolver) ResolveFreezeReferences(ctx context.Context, record clmcontract.Aggregate,
	caseID string, draft *clmcontract.DraftContent) (clmcontract.VersionReferenceFacts, error) {
	ctx = commandContext(ctx, r.user)
	anchor, err := loadContractReferenceAnchor(ctx, r.tx, r.user, record, caseID)
	if err != nil {
		return clmcontract.VersionReferenceFacts{}, err
	}
	resolved, err := resolveContractReferences(ctx, r.tx, r.user, anchor, draft, referenceModeImport)
	if err != nil {
		return clmcontract.VersionReferenceFacts{}, err
	}
	facts := clmcontract.VersionReferenceFacts{SourceCaseOrgID: anchor.OwnerOrgID, SourceCaseID: anchor.ID,
		Materials: make([]clmcontract.ResolvedVersionMaterial, 0, len(resolved.Materials))}
	for _, material := range resolved.Materials {
		facts.Materials = append(facts.Materials, clmcontract.ResolvedVersionMaterial{
			MaterialID: material.MaterialID, MaterialVersionID: material.MaterialVersionID,
			MaterialVersion: material.MaterialVersion, Filename: material.Filename,
			Digest: material.SHA256, Purpose: material.Purpose,
		})
	}
	return facts, nil
}

func (r storeVersionReferenceResolver) ResolveLegacyImportReferences(ctx context.Context,
	input *clmcontract.LegacyImportInput) (clmcontract.LegacyImportFacts, error) {
	ctx = commandContext(ctx, r.user)
	tenantID := accounts.Tenant(r.user)
	source, err := resolveMaterialVersion(ctx, r.tx, tenantID, input.SourceEvidence)
	if err != nil {
		return clmcontract.LegacyImportFacts{}, err
	}
	sourceCase, err := loadCLMCase(ctx, r.tx, r.user, source.CaseOrgID, source.CaseID)
	if err != nil {
		return clmcontract.LegacyImportFacts{}, err
	}
	if _, err := resolveContractReferences(ctx, r.tx, r.user, sourceCase, &input.Snapshot, referenceModeImport); err != nil {
		return clmcontract.LegacyImportFacts{}, err
	}
	historical := make([]clmcontract.HistoricalApprovalMaterial, 0, len(input.HistoricalApprovalMaterials))
	for _, ref := range input.HistoricalApprovalMaterials {
		version, err := resolveMaterialVersion(ctx, r.tx, tenantID, ref)
		if err != nil {
			return clmcontract.LegacyImportFacts{}, err
		}
		if _, err := loadCLMCase(ctx, r.tx, r.user, version.CaseOrgID, version.CaseID); err != nil {
			return clmcontract.LegacyImportFacts{}, err
		}
		historical = append(historical, clmcontract.HistoricalApprovalMaterial{
			MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID, SHA256: ref.SHA256,
			Purpose: ref.Purpose, CaseOrgID: version.CaseOrgID, CaseID: version.CaseID,
			MaterialVersion: version.Version,
		})
	}
	return clmcontract.LegacyImportFacts{
		SourceCaseOrgID: sourceCase.OwnerOrgID, SourceCaseID: sourceCase.ID,
		SourceEvidence: clmcontract.LegacySourceEvidence{
			CaseOrgID: source.CaseOrgID, CaseID: source.CaseID, MaterialID: source.MaterialID,
			MaterialVersion: source.Version, Digest: source.Digest,
		},
		Historical: historical,
	}, nil
}

type storeVersionAuditReader struct{ tx pgx.Tx }

func (r storeVersionAuditReader) LatestVersionAuditBindings(ctx context.Context, tenantID, contractID string) (map[string]contractapp.VersionAuditBinding, error) {
	bindings, err := loadLatestContractAuditBindings(ctx, r.tx, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]contractapp.VersionAuditBinding, len(bindings))
	for revisionID, binding := range bindings {
		result[revisionID] = contractapp.VersionAuditBinding{GateState: binding.GateState, Bound: true}
	}
	return result, nil
}

func (r storeVersionAuditReader) LatestVersionAuditBinding(ctx context.Context, tenantID, contractID, revisionID string) (*contractapp.VersionAuditBinding, error) {
	binding, err := loadLatestContractAuditBinding(ctx, r.tx, tenantID, contractID, revisionID)
	if err != nil || binding == nil {
		return nil, err
	}
	return &contractapp.VersionAuditBinding{GateState: binding.GateState, Bound: true}, nil
}

type storeVersionEventAdapter struct{ tx pgx.Tx }

func (a storeVersionEventAdapter) AppendContractEvent(ctx context.Context, tenantID, eventID, contractID string,
	version int64, eventType string, payload []byte) error {
	return contractpostgres.Append(ctx, platformpostgres.Bind(a.tx), contractpostgres.BusinessEvent{
		TenantID: tenantID, EventID: eventID, ContractID: contractID, AggregateVersion: version,
		EventType: eventType, Payload: payload,
	})
}

func (a storeVersionEventAdapter) EnqueueContractEvent(ctx context.Context, tenantID, eventID string) error {
	return clmoutboxpostgres.Enqueue(ctx, platformpostgres.Bind(a.tx), tenantID, eventID)
}

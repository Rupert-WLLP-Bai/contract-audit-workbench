// File metadata (manifest mirror):
// Purpose: Verifies frozen and current contract draft seals, final amounts, materials and restricted fingerprints for approval submission.
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
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
	"encoding/json"
)

type ApprovalFreezeRepository interface {
	LoadFrozenRevision(context.Context, string, string, string) (contract.FrozenContractRevision, error)
	LoadVersionSeal(context.Context, string, string, string) (*contract.ContractVersionSeal, error)
	LoadVersionMaterials(context.Context, string, string, string) ([]contract.FrozenVersionMaterial, error)
	LoadDraftContent(context.Context, string, string) ([]byte, error)
}
type ApprovalFreezeCodec interface {
	OpenApprovalDraft(context.Context, string, string, []byte) (contract.DraftContent, error)
	ApprovalFingerprintFacts(context.Context, string, string, contract.DraftContent, []byte) (map[string]any, json.RawMessage, error)
	ValidateApprovalSeal([]byte) error
}
type ApprovalMaterialResolver interface {
	ResolveApprovalMaterials(context.Context, contract.FrozenVersionMaterial, []contract.MaterialRef) ([]contract.FrozenVersionMaterial, error)
}

func VerifyApprovalFreeze(ctx context.Context, tenantID, contractID, revisionID string, repo ApprovalFreezeRepository, codec ApprovalFreezeCodec, resolver ApprovalMaterialResolver) ([]contract.FrozenVersionMaterial, error) {
	frozen, err := repo.LoadFrozenRevision(ctx, tenantID, contractID, revisionID)
	if err != nil {
		return nil, err
	}
	seal, err := repo.LoadVersionSeal(ctx, tenantID, contractID, revisionID)
	if err != nil {
		return nil, err
	}
	if seal != nil {
		if err = codec.ValidateApprovalSeal(seal.RestrictedFingerprints); err != nil {
			return nil, err
		}
	}
	if seal == nil {
		return nil, fault.Conflict("送审须使用已完成完整性校验的定稿版本")
	}
	if !frozen.AmountsReconciled {
		return nil, fault.Conflict("定稿版本的金额核对未通过，无法送审")
	}
	if bytesDigest(frozen.Snapshot) != seal.SnapshotDigest {
		return nil, fault.Conflict("定稿版本的内容与保存时的摘要不一致")
	}
	first, err := codec.OpenApprovalDraft(ctx, tenantID, contractID, frozen.Snapshot)
	if err != nil {
		return nil, err
	}
	if err = contract.ValidateDraftContent(&first); err != nil {
		return nil, err
	}
	if err = contract.ValidateDraftAmounts(&first, contractmoney.StageFinal); err != nil {
		return nil, err
	}
	raw, err := repo.LoadDraftContent(ctx, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	second, err := codec.OpenApprovalDraft(ctx, tenantID, contractID, raw)
	if err != nil {
		return nil, err
	}
	if err = contract.ValidateDraftContent(&second); err != nil {
		return nil, err
	}
	if err = contract.ValidateDraftAmounts(&second, contractmoney.StageFinal); err != nil {
		return nil, err
	}
	materials, err := repo.LoadVersionMaterials(ctx, tenantID, contractID, revisionID)
	if err != nil {
		return nil, err
	}
	var anchor contract.FrozenVersionMaterial
	if len(materials) > 0 {
		anchor = materials[0]
	}
	for _, candidate := range []contract.DraftContent{first, second} {
		resolved, err := resolver.ResolveApprovalMaterials(ctx, anchor, candidate.Materials)
		if err != nil {
			return nil, err
		}
		digest, err := jsonDigest(resolved)
		if err != nil {
			return nil, err
		}
		if digest != seal.MaterialsDigest {
			return nil, fault.Conflict("合同材料在定稿后已有变更，请重新定稿后送审")
		}
		fields, restricted, err := codec.ApprovalFingerprintFacts(ctx, tenantID, contractID, candidate, seal.RestrictedFingerprints)
		if err != nil {
			return nil, err
		}
		digest, err = jsonDigest(struct {
			Fields     map[string]any  `json:"fields"`
			Restricted json.RawMessage `json:"restricted"`
		}{fields, restricted})
		if err != nil {
			return nil, err
		}
		if digest != seal.ContentFingerprint {
			return nil, fault.Conflict("合同内容在定稿后已有变更，请重新定稿后送审")
		}
	}
	return materials, nil
}

// ApprovalRevisionScope is the sealed revision evidence bound into approval credentials.
type ApprovalRevisionScope struct {
	RevisionNumber int
	Amount         string
	Currency       string
	SnapshotDigest string
}

// ApprovalMaterialKey identifies one exact Contract frozen material version.
type ApprovalMaterialKey struct{ RevisionID, MaterialID, MaterialVersionID string }

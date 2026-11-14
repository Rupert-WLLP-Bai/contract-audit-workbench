// File metadata (manifest mirror):
// Purpose: Coordinates normalized service-object creation and receipt replay within a caller-owned authorized transaction.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package app coordinates Operator use cases with caller-owned transactions.
package app

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type CreateIdentity struct{ TenantID, OrgID, CaseID, ActorID string }
type Receipt struct {
	RequestSHA string
	Status     int
	Body       []byte
}
type CreatePrepared struct {
	Identity        CreateIdentity
	Key, RequestSHA string
	Version         model.ServiceObjectVersion
	Now             time.Time
}
type CreateRepository interface {
	ReadCreateReceipt(context.Context, CreateIdentity, string) (Receipt, bool, error)
	CountObjects(context.Context, CreateIdentity) (int, error)
	InsertCreateHead(context.Context, CreateIdentity, string, time.Time) error
	InsertCreateVersion(context.Context, CreateIdentity, model.ServiceObjectVersion, json.RawMessage) error
	InsertCreateReceipt(context.Context, CreateIdentity, string, string, []byte, string, time.Time) error
}
type CreateClockIDs interface {
	Now() time.Time
	NewObjectID() (string, error)
}

func PrepareCreate(ctx context.Context, repo CreateRepository, clock CreateClockIDs, id CreateIdentity, input model.ServiceObjectCreateInput, key string) (CreatePrepared, model.ServiceObjectMutationResult, bool, error) {
	var empty model.ServiceObjectMutationResult
	now := clock.Now().UTC().Truncate(time.Microsecond)
	version, err := model.NormalizeServiceObjectVersion(model.ServiceObjectVersion{ObjectType: input.ObjectType, DisplayName: input.DisplayName, BusinessCode: input.BusinessCode, Aliases: input.Aliases, State: model.ServiceObjectPendingConfirmation, BasisSummary: input.BasisSummary, BasisRefs: json.RawMessage(`[]`), CreatedBy: id.ActorID, CreatedAt: now, EffectiveFrom: now})
	if err != nil {
		return CreatePrepared{}, empty, false, fault.Invalid("服务对象身份资料无效")
	}
	canonicalInput := model.ServiceObjectCreateInput{ObjectType: version.ObjectType, DisplayName: version.DisplayName, BusinessCode: version.BusinessCode, Aliases: version.Aliases, BasisSummary: version.BasisSummary}
	hash, err := ServiceObjectRequestSHA256(id, "create_object", canonicalInput)
	if err != nil {
		return CreatePrepared{}, empty, false, err
	}
	receipt, found, err := repo.ReadCreateReceipt(ctx, id, key)
	if err != nil {
		return CreatePrepared{}, empty, false, err
	}
	if found {
		result, err := decodeServiceObjectReceipt(receipt, hash)
		if err != nil {
			return CreatePrepared{}, empty, false, err
		}
		return CreatePrepared{}, result, true, nil
	}

	return CreatePrepared{Identity: id, Key: key, RequestSHA: hash, Version: version, Now: now}, empty, false, nil
}
func CreateAfterMiss(ctx context.Context, repo CreateRepository, clock CreateClockIDs, p CreatePrepared) (model.ServiceObjectMutationResult, error) {
	var empty model.ServiceObjectMutationResult
	count, err := repo.CountObjects(ctx, p.Identity)
	if err != nil {
		return empty, err
	}
	if count >= 100 {
		return empty, fault.Invalid("每个案件最多登记100个服务对象")
	}
	objectID, err := clock.NewObjectID()
	if err != nil {
		return empty, err
	}
	version := p.Version
	version.ServiceObjectID, version.Version = objectID, 1
	version.PayloadSHA256, err = model.CanonicalServiceObjectIdentitySHA256(version)
	if err != nil {
		return empty, err
	}
	aliases, err := json.Marshal(version.Aliases)
	if err != nil {
		return empty, err
	}
	result := model.ServiceObjectMutationResult{ServiceObjectID: objectID, Version: 1, AssociationVersion: 0}
	body, err := json.Marshal(result)
	if err != nil {
		return empty, err
	}
	if err := repo.InsertCreateHead(ctx, p.Identity, objectID, p.Now); err != nil {
		return empty, err
	}
	if err := repo.InsertCreateVersion(ctx, p.Identity, version, json.RawMessage(aliases)); err != nil {
		return empty, err
	}
	if err := repo.InsertCreateReceipt(ctx, p.Identity, p.Key, p.RequestSHA, body, objectID, p.Now); err != nil {
		return empty, err
	}
	return result, nil
}

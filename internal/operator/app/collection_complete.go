// File metadata (manifest mirror):
// Purpose: Coordinates the bounded external read, observation validation and canonical preparation, completion fencing and ordered immutable writes through Operator ports.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

func ReadCollectionExternal(ctx context.Context, connector auditext.ReadConnector, scope auditext.AuthorizedScope, binding model.OperatorObjectBinding, businessAt time.Time) (auditext.Observation, bool, error) {
	o, err := connector.Read(ctx, auditext.ReadRequest{Scope: scope, ConnectorID: binding.ExternalObject.ConnectorID, BindingVersion: binding.Version, ExternalObjectID: binding.ExternalObject.ExternalObjectID, BusinessAt: businessAt})
	if err != nil {
		switch {
		case errors.Is(err, auditext.ErrUnauthorized):
			return o, true, fault.Forbidden("外部来源拒绝了本次读取")
		case errors.Is(err, auditext.ErrInvalidRequest):
			return o, true, fault.Invalid("外部读取请求不合法")
		default:
			return o, true, fault.New(503, "external_evidence_unavailable", "外部证据读取未完成")
		}
	}
	if err = o.Validate(scope); err != nil {
		if errors.Is(err, auditext.ErrUnauthorized) {
			return o, true, fault.Forbidden("外部证据不属于当前租户或案件")
		}
		return o, true, fault.New(502, "external_evidence_invalid", "外部证据格式未通过校验")
	}
	return o, false, nil
}

type PreparedObservation struct {
	Value        auditext.Observation
	JSON, Fields []byte
	SHA, SetSHA  string
}

func PrepareObservation(o auditext.Observation, requestSHA string) (PreparedObservation, error) {
	b, err := json.Marshal(o)
	if err != nil {
		return PreparedObservation{}, err
	}
	h := sha256.Sum256(b)
	set := sha256.Sum256(append(b, []byte(requestSHA)...))
	fields, err := json.Marshal(o.Fields)
	if err != nil {
		return PreparedObservation{}, err
	}
	return PreparedObservation{Value: o, JSON: b, Fields: fields, SHA: hex.EncodeToString(h[:]), SetSHA: hex.EncodeToString(set[:])}, nil
}

type CompletionClaim struct {
	Generation int64
	Owner      *string
	Status     string
	LeaseValid bool
}
type CollectionCompletionRepository interface {
	LockCompletion(context.Context, model.CollectionLeaseToken) (CompletionClaim, error)
	InsertSnapshot(context.Context, model.CollectionLeaseToken, model.OperatorSnapshotSet, string) error
	InsertObservation(context.Context, model.CollectionLeaseToken, string, PreparedObservation, time.Time) error
	CompleteClaim(context.Context, model.CollectionLeaseToken, string, []byte, time.Time) (bool, error)
}

func LockCollectionCompletion(ctx context.Context, repo CollectionCompletionRepository, token model.CollectionLeaseToken) error {
	c, err := repo.LockCompletion(ctx, token)
	if err != nil {
		return err
	}
	if c.Status != "in_progress" || !c.LeaseValid || c.Generation != token.Generation || c.Owner == nil || *c.Owner != token.Owner {
		return fault.Conflict("采集租约已被更新的请求接管")
	}
	return nil
}
func CompleteCollectionAfterLock(ctx context.Context, repo CollectionCompletionRepository, token model.CollectionLeaseToken, binding model.OperatorObjectBinding, businessAt time.Time, setID string, o PreparedObservation, clock CollectionClock) (model.OperatorSnapshotSet, error) {
	var out model.OperatorSnapshotSet
	now := clock.Now().UTC().Truncate(time.Microsecond)
	v := o.Value
	set := model.OperatorSnapshotSet{CaseID: token.Identity.CaseID, CollectionID: token.CollectionID, SnapshotSetID: setID, BindingVersion: binding.Version, ConnectorID: v.ConnectorID, ExternalObjectID: v.ExternalObjectID, SourceSystem: v.SourceSystem, BusinessAt: businessAt, Status: string(v.Availability), ReasonCode: v.ReasonCode, CollectedBy: token.Identity.ActorID, CollectedAt: now}
	if err := repo.InsertSnapshot(ctx, token, set, o.SetSHA); err != nil {
		return out, err
	}
	if err := repo.InsertObservation(ctx, token, setID, o, now); err != nil {
		return out, err
	}
	set.Observations = []auditext.Observation{v}
	out = set
	body, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	ok, err := repo.CompleteClaim(ctx, token, setID, body, now)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, fault.Conflict("采集租约已被更新的请求接管")
	}
	return out, nil
}

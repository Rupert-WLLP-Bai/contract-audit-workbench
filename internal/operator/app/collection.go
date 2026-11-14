// File metadata (manifest mirror):
// Purpose: Coordinates collection claim acquisition, legacy or completed result replay, lease takeover and exact snapshot observation decoding through owned ports.
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
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type ObservationCursor interface {
	Next() bool
	ScanRaw() ([]byte, error)
	Err() error
	Close()
}
type CollectionRepository interface {
	LockClaim(context.Context, model.CollectionIdentity) (model.CollectionClaim, bool, error)
	SnapshotByKey(context.Context, model.CollectionIdentity) (model.SnapshotRecord, bool, error)
	SnapshotByID(context.Context, string, string) (model.OperatorSnapshotSet, error)
	Observations(context.Context, string, string) (ObservationCursor, error)
	InsertLegacyClaim(context.Context, model.CollectionLeaseToken, string, []byte) error
	InsertClaim(context.Context, model.CollectionLeaseToken) error
	RenewClaim(context.Context, model.CollectionLeaseToken) error
}
type CollectionClock interface{ Now() time.Time }
type CollectionWritable interface{ CheckWritable() error }
type CollectionRequest struct {
	Identity                        model.CollectionIdentity
	Binding                         model.OperatorObjectBinding
	BusinessAt                      time.Time
	RequestSHA, CollectionID, Owner string
	ConnectorAvailable              bool
}
type CollectionAcquire struct {
	Snapshot model.OperatorSnapshotSet
	Replay   bool
	Token    model.CollectionLeaseToken
}

func CollectionDigest(tenant, org, caseID, actor string, binding model.OperatorObjectBinding, businessAt time.Time) (string, error) {
	return collectionHash(struct {
		TenantID, OrgID, CaseID, ActorID, BindingVersion, ConnectorID, ExternalObjectID string
		BusinessAt                                                                      time.Time
	}{tenant, org, caseID, actor, binding.Version, binding.ExternalObject.ConnectorID, binding.ExternalObject.ExternalObjectID, businessAt})
}
func collectionHash(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func ReadCollectionObservations(ctx context.Context, repo CollectionRepository, caseID, setID string) ([]auditext.Observation, error) {
	rows, err := repo.Observations(ctx, caseID, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditext.Observation
	for rows.Next() {
		raw, e := rows.ScanRaw()
		if e != nil {
			return nil, e
		}
		var o auditext.Observation
		if e = json.Unmarshal(raw, &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func ReadCollectionSnapshot(ctx context.Context, repo CollectionRepository, caseID, setID string) (model.OperatorSnapshotSet, error) {
	v, err := repo.SnapshotByID(ctx, caseID, setID)
	if err != nil {
		return v, err
	}
	v.BusinessAt = v.BusinessAt.UTC()
	v.CollectedAt = v.CollectedAt.UTC()
	v.Observations, err = ReadCollectionObservations(ctx, repo, caseID, v.SnapshotSetID)
	return v, err
}
func AcquireCollection(ctx context.Context, repo CollectionRepository, writable CollectionWritable, request CollectionRequest, clock CollectionClock) (CollectionAcquire, error) {
	var out CollectionAcquire
	token := model.CollectionLeaseToken{Identity: request.Identity, CollectionID: request.CollectionID, RequestSHA256: request.RequestSHA, Owner: request.Owner}
	claim, found, err := repo.LockClaim(ctx, request.Identity)
	if err != nil {
		return out, err
	}
	if !found {
		existing, legacyFound, e := repo.SnapshotByKey(ctx, request.Identity)
		if e != nil {
			return out, e
		}
		if legacyFound {
			existing.Value.BusinessAt = existing.Value.BusinessAt.UTC()
			existing.Value.CollectedAt = existing.Value.CollectedAt.UTC()
			existing.Value.Observations, e = ReadCollectionObservations(ctx, repo, request.Identity.CaseID, existing.Value.SnapshotSetID)
			if e != nil {
				return out, e
			}
			sha, e := collectionHash(struct {
				CaseID, BindingVersion, ConnectorID, ExternalObjectID string
				BusinessAt                                            time.Time
			}{request.Identity.CaseID, request.Binding.Version, request.Binding.ExternalObject.ConnectorID, request.Binding.ExternalObject.ExternalObjectID, request.BusinessAt})
			if e != nil {
				return out, e
			}
			if existing.RequestSHA256 != sha {
				return out, fault.Conflict("同一幂等键对应了不同的采集请求")
			}
			body, e := json.Marshal(existing.Value)
			if e != nil {
				return out, e
			}
			token.Generation = 1
			out.Snapshot = existing.Value
			out.Snapshot.CollectionID = request.CollectionID
			out.Replay = true
			out.Token = token
			return out, repo.InsertLegacyClaim(ctx, token, existing.Value.SnapshotSetID, body)
		}
		if !request.ConnectorAvailable {
			return out, fault.New(503, "external_evidence_unavailable", "外部证据连接器未配置")
		}
		if err = writable.CheckWritable(); err != nil {
			return out, err
		}
		token.Generation = 1
		out.Token = token
		return out, repo.InsertClaim(ctx, token)
	}
	token.CollectionID = claim.CollectionID
	token.Generation = claim.Generation
	if claim.RequestSHA256 != request.RequestSHA {
		return out, fault.Conflict("同一幂等键对应了不同的采集请求")
	}
	if claim.Status == "completed" {
		if claim.ResultID == nil {
			return out, fault.New(500, "integrity_error", "采集幂等回执缺少快照结果")
		}
		v, e := ReadCollectionSnapshot(ctx, repo, request.Identity.CaseID, *claim.ResultID)
		if e != nil {
			return out, fault.New(500, "integrity_error", "采集幂等回执引用的快照不存在")
		}
		v.CollectionID = token.CollectionID
		out.Snapshot = v
		out.Replay = true
		out.Token = token
		return out, nil
	}
	instant := clock.Now().UTC()
	if claim.Status == "in_progress" && claim.Owner != nil && *claim.Owner != "" && claim.LeaseUntil != nil && claim.LeaseUntil.After(instant) {
		return out, fault.Conflict("相同采集请求仍在执行，请稍后重试")
	}
	if !request.ConnectorAvailable {
		return out, fault.New(503, "external_evidence_unavailable", "外部证据连接器未配置")
	}
	if err = writable.CheckWritable(); err != nil {
		return out, err
	}
	token.Generation++
	out.Token = token
	return out, repo.RenewClaim(ctx, token)
}

// File metadata (manifest mirror):
// Purpose: 实现 `TenantRuntimeControl`, `ReadTenantRuntimeControl`, `admissionEpoch`, `canonicalEpoch` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"fmt"
	"strconv"

	"contractaudit/internal/fault"
	"contractaudit/internal/model"

	"github.com/jackc/pgx/v5"
)

// TenantRuntimeControl is the small operational projection used by the
// tenant-operations endpoint and the asynchronous queue fence.
type TenantRuntimeControl struct {
	Status        string
	LiveEpoch     string
	PolicyHash    string
	PolicyVersion string
	TenantWeight  int
	InFlightLimit int
	PendingLimit  int
	TenantLive    bool
}

func (s *Store) ReadTenantRuntimeControl(ctx context.Context, tenantID string) (TenantRuntimeControl, error) {
	if s == nil || s.pool == nil || tenantID == "" {
		return TenantRuntimeControl{}, fmt.Errorf("tenant runtime control is unavailable")
	}
	allowDemo := s.sel.Mode != "production"
	var status string
	var epoch int64
	if err := s.pool.QueryRow(ctx, `SELECT status,live_epoch FROM tenant_runtime_control($1,$2)`, tenantID, allowDemo).Scan(&status, &epoch); err != nil {
		if err == pgx.ErrNoRows {
			return TenantRuntimeControl{}, fmt.Errorf("tenant runtime control is unavailable: %w", err)
		}
		return TenantRuntimeControl{}, err
	}
	policy := s.executionQueuePolicy()
	weight := policy.Weights[tenantID]
	if weight == 0 {
		weight = 1
	}
	return TenantRuntimeControl{Status: status, LiveEpoch: strconv.FormatInt(epoch, 10),
		PolicyHash: policy.hash, PolicyVersion: policy.Version, TenantWeight: weight,
		InFlightLimit: policy.TenantInFlight, PendingLimit: policy.TenantPending, TenantLive: status == "active"}, nil
}

// admissionEpoch locks the tenant row until the accepting transaction commits.
func (s *Store) admissionEpoch(ctx context.Context, tx pgx.Tx, tenant string) (*int64, error) {
	var status string
	var epoch int64
	if err := tx.QueryRow(ctx, `SELECT status,live_epoch FROM tenant_runtime_control($1,$2)`, tenant, s.sel.Mode != "production").Scan(&status, &epoch); err != nil {
		return nil, err
	}
	if status != "active" {
		return nil, fault.New(409, "tenant_inactive", "租户已暂停，不能接收新任务")
	}
	return &epoch, nil
}
func canonicalEpoch(value string) (int64, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == value
}
func (s *Store) fenceAttemptEpoch(ctx context.Context, tx pgx.Tx, scope model.DelegatedScope) error {
	epoch, ok := canonicalEpoch(scope.TenantLiveEpoch)
	if !ok {
		return errAttemptAuthorizationRevoked
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM review_attempts a JOIN review_runs r ON r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.org_id=a.org_id WHERE a.id=$1 AND a.run_id=$2 AND a.tenant_id=$3 AND a.org_id=$4 AND r.case_id=$5 AND a.actor_id=$6 AND a.tenant_live_epoch=$7) AND tenant_runtime_fence($3,$7,$8)`, scope.AttemptID, scope.RunID, scope.TenantID, scope.OrgID, scope.CaseID, scope.ActorID, epoch, s.sel.Mode != "production").Scan(&valid)
	if err != nil || !valid {
		return errAttemptAuthorizationRevoked
	}
	return nil
}

func (s *Store) fenceJobEpoch(ctx context.Context, tx pgx.Tx, scope model.DelegatedScope) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs j WHERE j.id=$1 AND j.tenant_id=$2 AND j.org_id=$3 AND j.case_id=$4 AND j.actor_id=$5 AND j.worker_id=$6 AND j.generation=$7 AND j.state='running' AND j.lease_until>clock_timestamp() AND COALESCE(j.run_id,'')=$8 AND COALESCE(j.attempt_id,'')=$9 AND tenant_runtime_fence(j.tenant_id,j.tenant_live_epoch,$10) AND (j.kind<>'review' OR EXISTS (SELECT 1 FROM review_attempts a WHERE a.tenant_id=j.tenant_id AND a.org_id=j.org_id AND a.run_id=j.run_id AND a.id=j.attempt_id AND a.tenant_live_epoch=j.tenant_live_epoch)))`, scope.JobID, scope.TenantID, scope.OrgID, scope.CaseID, scope.ActorID, scope.WorkerID, scope.Generation, scope.RunID, scope.AttemptID, s.sel.Mode != "production").Scan(&valid)
	if err != nil || !valid {
		return errLeaseLost
	}
	return nil
}

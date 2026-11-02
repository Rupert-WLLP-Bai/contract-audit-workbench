package store

import (
	"context"
	"fmt"
	"strconv"

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

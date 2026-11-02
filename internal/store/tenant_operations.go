// File metadata (manifest mirror):
// Purpose: 实现 `ReadJurisdictionOperations`, `JurisdictionOperationsView`, `JurisdictionOperationsSummary`, `TenantOperationsSummary` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) ReadJurisdictionOperations(ctx context.Context, user accounts.User, fromDate, toDate string) (JurisdictionOperationsView, error) {
	from, err1 := time.Parse("2006-01-02", fromDate)
	to, err2 := time.Parse("2006-01-02", toDate)
	if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) > 30*24*time.Hour {
		return JurisdictionOperationsView{}, fault.Invalid("统计日期范围必须是最多31天的完整UTC日期")
	}
	current, err := trustedUser(user)
	if err != nil {
		return JurisdictionOperationsView{}, err
	}
	if current.Role != "admin" || current.PrincipalID == "" || current.MembershipID == "" {
		return JurisdictionOperationsView{}, fault.Forbidden("当前身份未获辖区统计授权")
	}
	ctx = commandContext(ctx, current)
	// Rebuilding the replacement projection is part of this request, so this
	// narrowly scoped aggregate read needs a writable transaction.
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return JurisdictionOperationsView{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return JurisdictionOperationsView{}, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT tenant_jurisdiction_operations($1,$2,$3,$4::date,$5::date)`, current.ID, current.PrincipalID, current.MembershipID, fromDate, toDate).Scan(&raw); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			return JurisdictionOperationsView{}, fault.Forbidden("当前身份未获辖区统计授权")
		}
		return JurisdictionOperationsView{}, err
	}
	var out JurisdictionOperationsView
	if err := json.Unmarshal(raw, &out); err != nil || out.SchemaVersion != "jurisdiction-operations/v1" {
		return JurisdictionOperationsView{}, fmt.Errorf("jurisdiction operations response is invalid")
	}
	if err := tx.Commit(ctx); err != nil {
		return JurisdictionOperationsView{}, err
	}
	return out, nil
}

type JurisdictionOperationsView struct {
	SchemaVersion        string                          `json:"schema_version"`
	ObservedAt           string                          `json:"observed_at"`
	WatermarkKind        string                          `json:"watermark_kind"`
	Watermark            *string                         `json:"watermark"`
	Projection           string                          `json:"projection_state"`
	SourceSnapshotToken  *string                         `json:"source_snapshot_token"`
	BuiltAt              *string                         `json:"built_at"`
	DirtySince           *string                         `json:"dirty_since"`
	ProjectionAgeSeconds *int64                          `json:"projection_age_seconds"`
	FromDate             string                          `json:"from_date"`
	ToDate               string                          `json:"to_date"`
	Jurisdictions        []JurisdictionOperationsSummary `json:"jurisdictions"`
}

type JurisdictionOperationsSummary struct {
	JurisdictionID string                    `json:"jurisdiction_id"`
	Tenants        []TenantOperationsSummary `json:"tenants"`
	Totals         OperationsMetricSummary   `json:"totals"`
}

type TenantOperationsSummary struct {
	TenantID   string                  `json:"tenant_id"`
	TenantName string                  `json:"tenant_name"`
	Status     string                  `json:"status"`
	Metrics    OperationsMetricSummary `json:"metrics"`
}

type OperationsMetricSummary struct {
	CasesCreated      *string              `json:"cases_created"`
	RunsCompleted     *string              `json:"runs_completed"`
	AttemptsCompleted *string              `json:"attempts_completed"`
	Money             *OperationsCostView  `json:"money"`
	LogicalQuota      *OperationsQuotaView `json:"logical_quota"`
	SharedPool        *OperationsPoolView  `json:"shared_pool"`
}

type OperationsCostView struct {
	Currency         string  `json:"currency"`
	MinorUnitScale   int     `json:"minor_unit_scale"`
	Known            bool    `json:"known"`
	SettledMinor     *string `json:"settled_minor"`
	UnknownHoldMinor *string `json:"unknown_hold_minor"`
}

type OperationsQuotaView struct {
	PeriodBasis       string  `json:"period_basis"`
	Calls             *string `json:"calls"`
	ReservedTokens    *string `json:"reserved_tokens"`
	UnknownUsageCalls *string `json:"unknown_usage_calls"`
}

type OperationsPoolView struct {
	Attribution string  `json:"attribution"`
	Capacity    *string `json:"capacity"`
	InUse       *string `json:"in_use"`
}

// File metadata (manifest mirror):
// Purpose: Reads raw collection bindings, snapshot lists and collection jobs while preserving selector scopes, cursor error order and partial results.
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
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type SnapshotCursor interface {
	Next() bool
	ScanSnapshot() (model.OperatorSnapshotSet, error)
	Err() error
	Close()
}
type CollectionReadRepository interface {
	CollectionRepository
	RawBinding(context.Context, string, string) (model.OperatorObjectBinding, error)
	ListSnapshots(context.Context, string) (SnapshotCursor, error)
	ReadJob(context.Context, model.CollectionIdentity, string, bool) (CollectionJobRow, error)
}
type CollectionJobRow struct {
	Job        model.OperatorCollectionJob
	LeaseUntil *time.Time
	ResultID   *string
	Expired    bool
}

func ReadRawCollectionBinding(ctx context.Context, repo CollectionReadRepository, caseID, version string) (model.OperatorObjectBinding, error) {
	return repo.RawBinding(ctx, caseID, version)
}
func ListCollectionSnapshots(ctx context.Context, repo CollectionReadRepository, caseID string) ([]model.OperatorSnapshotSet, error) {
	var out []model.OperatorSnapshotSet
	rows, err := repo.ListSnapshots(ctx, caseID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		v, e := rows.ScanSnapshot()
		if e != nil {
			return out, e
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for i := range out {
		o, e := ReadCollectionObservations(ctx, repo, caseID, out[i].SnapshotSetID)
		if e != nil {
			return out, e
		}
		out[i].Observations = o
	}
	return out, nil
}
func ValidCollectionID(value string) bool {
	if !strings.HasPrefix(value, "col_") || (len(value) != 20 && len(value) != 36) {
		return false
	}
	for _, c := range value[4:] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func ReadCollectionJob(ctx context.Context, repo CollectionReadRepository, id model.CollectionIdentity, selector string, byKey bool) (model.OperatorCollectionJob, error) {
	row, err := repo.ReadJob(ctx, id, selector, byKey)
	out := row.Job
	if err != nil {
		return out, err
	}
	if out.State == "in_progress" && row.Expired {
		out.State = "retryable"
	}
	out.CaseID = id.CaseID
	if out.State == "in_progress" && row.LeaseUntil != nil {
		v := row.LeaseUntil.UTC()
		out.RetryAfter = &v
	}
	if out.State == "completed" {
		if row.ResultID == nil {
			return out, fault.New(500, "integrity_error", "采集幂等回执缺少快照结果")
		}
		set, e := ReadCollectionSnapshot(ctx, repo, id.CaseID, *row.ResultID)
		if e != nil {
			return out, fault.New(500, "integrity_error", "采集幂等回执引用的快照不存在")
		}
		set.CollectionID = out.CollectionID
		out.SnapshotSetID = row.ResultID
		out.SnapshotSet = &set
	}
	return out, nil
}

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"contractaudit/internal/fault"
	"contractaudit/internal/legal/model"
)

type historyCursor struct {
	Version       int    `json:"v"`
	TenantID      string `json:"tenant_id"`
	CaseID        string `json:"case_id"`
	Dimension     string `json:"dimension"`
	BeforeVersion int64  `json:"before_version"`
}
type HistoryRepository interface {
	ReadHead(context.Context, RecordIdentity, string) (int64, error)
	ListVersionNumbers(context.Context, RecordIdentity, string, int64, bool, int) ([]int64, error)
	ReadVersion(context.Context, RecordIdentity, string, int64) (model.LegalApplicabilityFactVersion, error)
}

func ValidateHistoryInput(caseID, dimension string, input model.LegalApplicabilityHistoryInput) (int, error) {
	if strings.TrimSpace(caseID) != caseID || caseID == "" || !model.ValidApplicabilityDimension(dimension) {
		return 0, fault.Invalid("法律适用事实历史范围无效")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return 0, fault.Invalid("法律适用事实历史limit必须在1到100之间")
	}
	if len(input.Cursor) > 2048 {
		return 0, fault.Invalid("法律适用事实历史cursor无效")
	}
	return limit, nil
}
func ReadApplicabilityHistory(ctx context.Context, repo HistoryRepository, id RecordIdentity, dimension string, input model.LegalApplicabilityHistoryInput, limit int) (model.LegalApplicabilityFactHistory, error) {
	head, err := repo.ReadHead(ctx, id, dimension)
	if err != nil {
		return model.LegalApplicabilityFactHistory{}, err
	}
	before := head
	exclusive := false
	if input.Cursor != "" {
		var cursor historyCursor
		decoded, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Version != 1 || cursor.TenantID != id.TenantID || cursor.CaseID != id.CaseID || cursor.Dimension != dimension || cursor.BeforeVersion < 1 || cursor.BeforeVersion > head {
			return model.LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史cursor与当前案件或维度不匹配")
		}
		before, exclusive = cursor.BeforeVersion, true
	}
	versions, err := repo.ListVersionNumbers(ctx, id, dimension, before, exclusive, limit+1)
	if err != nil {
		return model.LegalApplicabilityFactHistory{}, err
	}
	hasMore := len(versions) > limit
	if hasMore {
		versions = versions[:limit]
	}
	items := make([]model.LegalApplicabilityFactVersion, 0, len(versions))
	for _, version := range versions {
		fact, err := ReadRecordVersion(ctx, repo, id, dimension, version)
		if err != nil {
			return model.LegalApplicabilityFactHistory{}, err
		}
		items = append(items, fact)
	}
	var next *string
	if hasMore && len(items) > 0 {
		raw, err := json.Marshal(historyCursor{Version: 1, TenantID: id.TenantID, CaseID: id.CaseID, Dimension: dimension, BeforeVersion: items[len(items)-1].Version})
		if err != nil {
			return model.LegalApplicabilityFactHistory{}, err
		}
		cursor := base64.RawURLEncoding.EncodeToString(raw)
		next = &cursor
	}
	return model.LegalApplicabilityFactHistory{CaseID: id.CaseID, Dimension: dimension, CurrentVersion: head, Items: items, NextCursor: next}, nil
}

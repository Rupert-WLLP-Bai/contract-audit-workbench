package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	defaultLegalApplicabilityHistoryLimit = 20
	maxLegalApplicabilityHistoryLimit     = 100
)

type LegalApplicabilityHistoryInput struct {
	Cursor string
	Limit  int
}

// LegalApplicabilityFactHistory contains immutable authored records, not a
// business-time projection. current_version is the server-authoritative CAS
// head and can differ from the newest item on a page.
type LegalApplicabilityFactHistory struct {
	CaseID         string                          `json:"case_id"`
	Dimension      string                          `json:"dimension"`
	CurrentVersion int64                           `json:"current_version"`
	Items          []LegalApplicabilityFactVersion `json:"items"`
	NextCursor     *string                         `json:"next_cursor"`
	CanManage      bool                            `json:"can_manage"`
}

type legalApplicabilityHistoryCursor struct {
	Version       int    `json:"v"`
	TenantID      string `json:"tenant_id"`
	CaseID        string `json:"case_id"`
	Dimension     string `json:"dimension"`
	BeforeVersion int64  `json:"before_version"`
}

func (s *Store) ListLegalApplicabilityFactHistory(ctx context.Context, user accounts.User, caseID, dimension string, input LegalApplicabilityHistoryInput) (LegalApplicabilityFactHistory, error) {
	ctx = commandContext(ctx, user)
	if err := s.legalReady(false); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return LegalApplicabilityFactHistory{}, fault.Forbidden("只有 Core 服务可以读取法律适用事实历史")
	}
	if ctx == nil {
		return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史读取需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if strings.TrimSpace(caseID) != caseID || caseID == "" {
		return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史范围无效")
	}
	if _, ok := legalApplicabilityDimensions[dimension]; !ok {
		return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史范围无效")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultLegalApplicabilityHistoryLimit
	}
	if limit < 1 || limit > maxLegalApplicabilityHistoryLimit {
		return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史limit必须在1到100之间")
	}
	if len(input.Cursor) > 2048 {
		return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史cursor无效")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	defer tx.Rollback(ctx)
	scope := userScope(current)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		return LegalApplicabilityFactHistory{}, fault.NotFound("没有找到对应案件")
	}
	var headVersion int64
	err = tx.QueryRow(ctx, `SELECT current_version FROM case_legal_applicability_facts
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseID, dimension).Scan(&headVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		headVersion, err = 0, nil
	}
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	var cursorVersion int64
	if input.Cursor != "" {
		cursor, err := decodeLegalApplicabilityHistoryCursor(input.Cursor)
		if err != nil || cursor.TenantID != accounts.Tenant(current) || cursor.CaseID != caseID || cursor.Dimension != dimension ||
			cursor.BeforeVersion < 1 || cursor.BeforeVersion > headVersion {
			return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史cursor与当前案件或维度不匹配")
		}
		cursorVersion = cursor.BeforeVersion
	}
	query := `SELECT version FROM case_legal_applicability_fact_versions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND version<=$5
		ORDER BY version DESC LIMIT $6`
	args := []any{accounts.Tenant(current), caseItem.OwnerOrgID, caseID, dimension, headVersion, limit + 1}
	if cursorVersion > 0 {
		query = `SELECT version FROM case_legal_applicability_fact_versions
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND version<$5
			ORDER BY version DESC LIMIT $6`
		args[4] = cursorVersion
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	versions := make([]int64, 0, limit+1)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return LegalApplicabilityFactHistory{}, err
		}
		versions = append(versions, version)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	hasMore := len(versions) > limit
	if hasMore {
		versions = versions[:limit]
	}
	items := make([]LegalApplicabilityFactVersion, 0, len(versions))
	for _, version := range versions {
		fact, err := loadLegalApplicabilityVersion(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, dimension, version)
		if err != nil {
			return LegalApplicabilityFactHistory{}, err
		}
		items = append(items, fact)
	}
	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded, err := encodeLegalApplicabilityHistoryCursor(legalApplicabilityHistoryCursor{
			Version: 1, TenantID: accounts.Tenant(current), CaseID: caseID, Dimension: dimension, BeforeVersion: items[len(items)-1].Version,
		})
		if err != nil {
			return LegalApplicabilityFactHistory{}, err
		}
		nextCursor = &encoded
	}
	current, err = trustedUser(current)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		return LegalApplicabilityFactHistory{}, fault.NotFound("没有找到对应案件")
	}
	canManage := !s.ReadOnly() && authorizeCase(caseItem, current, access.Manage) && businessWritable(caseItem) == nil
	if err := tx.Commit(ctx); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	return LegalApplicabilityFactHistory{CaseID: caseID, Dimension: dimension, CurrentVersion: headVersion, Items: items, NextCursor: nextCursor,
		CanManage: canManage}, nil
}

func encodeLegalApplicabilityHistoryCursor(cursor legalApplicabilityHistoryCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeLegalApplicabilityHistoryCursor(raw string) (legalApplicabilityHistoryCursor, error) {
	var cursor legalApplicabilityHistoryCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Version != 1 {
		return legalApplicabilityHistoryCursor{}, errors.New("invalid legal applicability history cursor")
	}
	return cursor, nil
}

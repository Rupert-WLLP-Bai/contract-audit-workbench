// File metadata (manifest mirror):
// Purpose: Contract ledger reads the existing aggregate and its sealed content without adding a second projection table. Every row is authorized through the same case-scope policy as the… Related `store` operations include normalizeLedgerText, ledgerAmountScaled, normalizeLedgerAmount, normalizeLedgerDate.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-05
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	"contractaudit/internal/contract/ledger"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// Contract ledger data models and query rules are owned by internal/contract/ledger.
// Store aliases keep the current HTTP and saved-view consumers source compatible.
const (
	contractLedgerDefaultLimit   = ledger.DefaultLimit
	contractLedgerMaxLimit       = ledger.MaxLimit
	contractLedgerCursorVer      = ledger.CursorVersion
	contractLedgerSortCreated    = ledger.SortCreated
	contractSavedViewNameMax     = 60
	contractSavedViewColumnsMax  = 20
	contractExportMaxRows        = 5000
	contractExportMaxScanned     = 20000
	contractExportFormatCSV      = "csv"
	contractExportRowsBatch      = 500
	contractLedgerContentAmount  = ledger.ContentAmountPattern
	contractLedgerTimeLayout     = ledger.TimeLayout
	contractLedgerDateLayout     = ledger.DateLayout
	contractLedgerCursorMaxBytes = ledger.CursorMaxBytes
)

type ContractLedgerFilter = ledger.Filter
type ContractLedgerItem = ledger.Item
type ContractLedgerPage = ledger.Page
type ContractLedgerQuery = ledger.Query
type contractLedgerCursor = ledger.Cursor

type ContractSavedView struct {
	ViewID     string               `json:"view_id"`
	Name       string               `json:"name"`
	Filter     ContractLedgerFilter `json:"filter"`
	Columns    []string             `json:"columns"`
	RowVersion int64                `json:"row_version"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
}

type ContractSavedViewInput struct {
	ViewID  string               `json:"-"`
	Name    string               `json:"name"`
	Filter  ContractLedgerFilter `json:"filter"`
	Columns []string             `json:"columns"`
}

type ContractSavedViewUpdateInput struct {
	ViewID             string               `json:"-"`
	ExpectedRowVersion int64                `json:"expected_row_version"`
	Name               string               `json:"name"`
	Filter             ContractLedgerFilter `json:"filter"`
	Columns            []string             `json:"columns"`
}

// ContractExportInput freezes one export request.
type ContractExportInput struct {
	Filter         ContractLedgerFilter `json:"filter"`
	Columns        []string             `json:"columns"`
	Format         string               `json:"format"`
	IdempotencyKey string               `json:"-"`
}

// ContractExport is the metadata returned when an export is created or
// replayed. Rows are never returned here.
type ContractExport struct {
	ExportID  string    `json:"export_id"`
	State     string    `json:"state"`
	Format    string    `json:"format"`
	Columns   []string  `json:"columns"`
	RowCount  int       `json:"row_count"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// DownloadAvailable only reports whether the frozen record has not passed
	// ExpiresAt at response time. It is not a current authorization result;
	// LoadContractExportForDownload re-authorizes the identity and every row.
	DownloadAvailable bool `json:"download_available"`
	Replayed          bool `json:"replayed"`
}

// ContractExportDownload is a frozen export re-authorized at download time.
type ContractExportDownload struct {
	ExportID  string
	Format    string
	Columns   []string
	Rows      []map[string]string
	ExpiresAt time.Time
}

var contractLedgerColumnLabels = map[string]string{
	"contract_number":    "合同编号",
	"contract_name":      "合同名称",
	"authority_mode":     "权威模式",
	"supplier_name":      "供应商",
	"amount":             "金额",
	"currency":           "币种",
	"department_id":      "申请部门",
	"project_type":       "项目类型",
	"procurement_method": "采购方式",
	"contract_state":     "合同状态",
	"workflow_state":     "会签状态",
	"signing_date":       "签订日期",
	"source_system":      "来源系统",
	"created_at":         "创建时间",
	"updated_at":         "更新时间",
}

// Export-only keys preserve the original headers and raw values above for
// already frozen exports. Each key's header is part of its download contract.
var contractExportColumnLabels = map[string]string{
	"authority_mode_label": "来源类型名称",
	"authority_mode_code":  "来源类型代码",
	"contract_state_label": "合同状态名称",
	"contract_state_code":  "合同状态代码",
	"workflow_state_label": "会签状态名称",
	"workflow_state_code":  "会签状态代码",
}

// These presentation labels follow the ledger's existing domain values.
// They are resolved when rows are created, never when a download is rendered.
var contractLedgerAuthorityLabels = map[string]string{
	"native": "本地登记", "external": "外部来源",
}
var contractLedgerStateLabels = map[string]string{
	"draft": "草稿", "ready_to_sign": "待签署", "registered": "已登记",
	"performing": "履约中", "closed": "已关闭", "terminated": "已终止",
	"external_observed": "外部来源已观测",
}
var contractLedgerWorkflowLabels = map[string]string{
	"not_started": "当前无进行中会签", "in_review": "会签中", "external_observed": "外部来源已观测",
}

var contractLedgerDefaultColumns = []string{
	"contract_number", "contract_name", "supplier_name", "amount", "currency",
	"department_id", "contract_state", "workflow_state", "updated_at",
}

func ledgerAmountScaled(value string) (int64, bool) { return ledger.AmountScaled(value) }
func validateContractLedgerFilter(filter *ContractLedgerFilter) error {
	return ledger.ValidateFilter(filter)
}

func normalizeContractLedgerColumns(columns []string) ([]string, error) {
	return normalizeContractColumns(columns, false)
}

func normalizeContractExportColumns(columns []string) ([]string, error) {
	return normalizeContractColumns(columns, true)
}

func normalizeContractColumns(columns []string, allowExportOnly bool) ([]string, error) {
	if len(columns) == 0 {
		return append([]string(nil), contractLedgerDefaultColumns...), nil
	}
	if len(columns) > contractSavedViewColumnsMax {
		return nil, fault.Invalid("导出或视图列最多 20 项")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, ok := contractLedgerColumnLabels[column]; !ok {
			if _, exportOnly := contractExportColumnLabels[column]; !allowExportOnly || !exportOnly {
				return nil, fault.Invalid("不支持的台账列")
			}
		}
		if seen[column] {
			return nil, fault.Invalid("台账列不能重复")
		}
		seen[column] = true
		out = append(out, column)
	}
	return out, nil
}

func validateContractLedgerSort(sort string) (string, error) { return ledger.ValidateSort(sort) }
func contractLedgerFilterDigest(filter ContractLedgerFilter, sort string) (string, error) {
	return ledger.FilterDigest(filter, sort)
}
func encodeContractLedgerCursor(current accounts.User, sort, digest string, key time.Time, contractID string) string {
	return ledger.EncodeCursor(ledger.CursorIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}, sort, digest, key, contractID)
}
func decodeContractLedgerCursor(value string, current accounts.User, sort, digest string) (*contractLedgerCursor, error) {
	return ledger.DecodeCursor(value, ledger.CursorIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}, sort, digest)
}

type contractLedgerRow struct {
	record  clmContractRecord
	item    ContractLedgerItem
	sortKey time.Time
}

func (s *Store) beginContractLedgerRead(ctx context.Context, user accounts.User) (pgx.Tx, accounts.User, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return nil, accounts.User{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, accounts.User{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, accounts.User{}, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		rollbackCLM(tx)
		return nil, accounts.User{}, err
	}
	return tx, current, nil
}

func contractLedgerItemFrom(record clmContractRecord, content []byte, inReview bool) (ContractLedgerItem, error) {
	return ledger.ItemFromAggregate(record, content, inReview)
}

func queryContractLedgerRows(ctx context.Context, tx pgx.Tx, tenant string, filter ContractLedgerFilter,
	afterSet bool, afterKey time.Time, afterID string, limit int) ([]contractLedgerRow, error) {
	rows, err := contractpostgres.NewLedgerReader(platformpostgres.Bind(tx)).Read(ctx, tenant, filter,
		afterSet, afterKey, afterID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]contractLedgerRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, contractLedgerRow{record: row.Aggregate, item: row.Item, sortKey: row.SortKey})
	}
	return out, nil
}

// ListContractLedger returns one authorized keyset page. SQL narrows by the
// validated filter and the case-scope policy authorizes every returned row.
func (s *Store) ListContractLedger(ctx context.Context, user accounts.User, query ContractLedgerQuery) (ContractLedgerPage, error) {
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ContractLedgerPage{}, err
	}
	defer rollbackCLM(tx)
	identity := ledger.CursorIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	reader := contractpostgres.NewLedgerReader(platformpostgres.Bind(tx))
	page, err := contractapp.ListContractLedger(ctx, identity, query, reader, storeLedgerAccess{tx: tx, user: current})
	if err != nil {
		return ContractLedgerPage{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractLedgerPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractLedgerPage{}, err
	}
	return page, nil
}

// ListContractSavedViews returns only the caller's own personal views.
func (s *Store) ListContractSavedViews(ctx context.Context, user accounts.User) ([]ContractSavedView, error) {
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return nil, err
	}
	defer rollbackCLM(tx)
	rows, err := tx.Query(ctx, `SELECT view_id,name,filter,columns,row_version,created_at,updated_at
		FROM clm_contract_saved_views WHERE tenant_id=$1 AND owner_user_id=$2
		ORDER BY updated_at DESC,view_id DESC`, accounts.Tenant(current), current.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContractSavedView{}
	for rows.Next() {
		item, err := scanContractSavedView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := trustedUser(current); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

type contractSavedViewScanner interface {
	Scan(dest ...any) error
}

func scanContractSavedView(row contractSavedViewScanner) (ContractSavedView, error) {
	var item ContractSavedView
	var filterJSON, columnsJSON []byte
	if err := row.Scan(&item.ViewID, &item.Name, &filterJSON, &columnsJSON, &item.RowVersion, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return ContractSavedView{}, err
	}
	if err := unmarshalStoredDraft(filterJSON, &item.Filter); err != nil {
		return ContractSavedView{}, fault.Conflict("保存视图的筛选条件无法读取")
	}
	if err := validateContractLedgerFilter(&item.Filter); err != nil {
		return ContractSavedView{}, fault.Conflict("保存视图的筛选条件不再有效")
	}
	if err := json.Unmarshal(columnsJSON, &item.Columns); err != nil {
		return ContractSavedView{}, fault.Conflict("保存视图的列设置无法读取")
	}
	columns, err := normalizeContractLedgerColumns(item.Columns)
	if err != nil {
		return ContractSavedView{}, fault.Conflict("保存视图的列设置不再有效")
	}
	item.Columns = columns
	return item, nil
}

func normalizeContractSavedViewInput(name string, filter *ContractLedgerFilter, columns []string) (string, []string, error) {
	name = strings.TrimSpace(name)
	if name == "" || runeCount(name) > contractSavedViewNameMax {
		return "", nil, fault.Invalid("视图名称必须为 1 到 60 个字符")
	}
	for _, char := range name {
		if char < 0x20 || char == 0x7f {
			return "", nil, fault.Invalid("视图名称不能包含控制字符")
		}
	}
	if err := validateContractLedgerFilter(filter); err != nil {
		return "", nil, err
	}
	normalized, err := normalizeContractLedgerColumns(columns)
	if err != nil {
		return "", nil, err
	}
	return name, normalized, nil
}

// CreateContractSavedView stores one personal view. The owner and tenant come
// only from the authenticated session.
func (s *Store) CreateContractSavedView(ctx context.Context, user accounts.User, input ContractSavedViewInput) (ContractSavedView, error) {
	ctx = commandContext(ctx, user)
	name, columns, err := normalizeContractSavedViewInput(input.Name, &input.Filter, input.Columns)
	if err != nil {
		return ContractSavedView{}, err
	}
	filterJSON, err := json.Marshal(input.Filter)
	if err != nil {
		return ContractSavedView{}, err
	}
	columnsJSON, err := json.Marshal(columns)
	if err != nil {
		return ContractSavedView{}, err
	}
	viewID, err := newContractUUID()
	if err != nil {
		return ContractSavedView{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能保存视图", nil)
	if err != nil {
		return ContractSavedView{}, err
	}
	defer rollbackCLM(tx)
	item := ContractSavedView{ViewID: viewID, Name: name, Filter: input.Filter, Columns: columns}
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_saved_views
		(tenant_id,view_id,owner_user_id,name,filter,columns)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb)
		RETURNING row_version,created_at,updated_at`,
		accounts.Tenant(current), viewID, current.ID, name, string(filterJSON), string(columnsJSON)).Scan(
		&item.RowVersion, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return ContractSavedView{}, clmUniqueViolation(err, "同一名称的保存视图已存在")
	}
	if _, err := trustedUser(current); err != nil {
		return ContractSavedView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSavedView{}, err
	}
	return item, nil
}

// ReplaceContractSavedView replaces one personal view under optimistic
// concurrency.
func (s *Store) ReplaceContractSavedView(ctx context.Context, user accounts.User, input ContractSavedViewUpdateInput) (ContractSavedView, error) {
	ctx = commandContext(ctx, user)
	viewID, err := normalizeContractUUID(input.ViewID)
	if err != nil {
		return ContractSavedView{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return ContractSavedView{}, fault.Invalid("替换视图必须携带当前 row_version")
	}
	name, columns, err := normalizeContractSavedViewInput(input.Name, &input.Filter, input.Columns)
	if err != nil {
		return ContractSavedView{}, err
	}
	filterJSON, err := json.Marshal(input.Filter)
	if err != nil {
		return ContractSavedView{}, err
	}
	columnsJSON, err := json.Marshal(columns)
	if err != nil {
		return ContractSavedView{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能修改视图", nil)
	if err != nil {
		return ContractSavedView{}, err
	}
	defer rollbackCLM(tx)
	item := ContractSavedView{ViewID: viewID, Name: name, Filter: input.Filter, Columns: columns}
	err = tx.QueryRow(ctx, `UPDATE clm_contract_saved_views
		SET name=$4,filter=$5::jsonb,columns=$6::jsonb,row_version=row_version+1,updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND view_id=$2 AND owner_user_id=$3 AND row_version=$7
		RETURNING row_version,created_at,updated_at`,
		accounts.Tenant(current), viewID, current.ID, name, string(filterJSON), string(columnsJSON), input.ExpectedRowVersion).Scan(
		&item.RowVersion, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractSavedView{}, s.savedViewMutationMiss(ctx, tx, current, viewID)
	}
	if err != nil {
		return ContractSavedView{}, clmUniqueViolation(err, "同一名称的保存视图已存在")
	}
	if _, err := trustedUser(current); err != nil {
		return ContractSavedView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSavedView{}, err
	}
	return item, nil
}

// DeleteContractSavedView removes one personal view under optimistic
// concurrency. Preferences are not audit facts, so a hard delete is allowed.
func (s *Store) DeleteContractSavedView(ctx context.Context, user accounts.User, viewID string, expectedRowVersion int64) error {
	ctx = commandContext(ctx, user)
	normalized, err := normalizeContractUUID(viewID)
	if err != nil {
		return err
	}
	if expectedRowVersion < 1 {
		return fault.Invalid("删除视图必须携带当前 row_version")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能删除视图", nil)
	if err != nil {
		return err
	}
	defer rollbackCLM(tx)
	tag, err := tx.Exec(ctx, `DELETE FROM clm_contract_saved_views
		WHERE tenant_id=$1 AND view_id=$2 AND owner_user_id=$3 AND row_version=$4`,
		accounts.Tenant(current), normalized, current.ID, expectedRowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.savedViewMutationMiss(ctx, tx, current, normalized)
	}
	if _, err := trustedUser(current); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// savedViewMutationMiss distinguishes a missing/foreign view (404) from a
// stale expected_row_version (409). Both mutation paths share the same miss
// handling because ownership is rechecked in the WHERE clause.
func (s *Store) savedViewMutationMiss(ctx context.Context, tx pgx.Tx, current accounts.User, viewID string) error {
	ctx = commandContext(ctx, current)
	var liveVersion int64
	err := tx.QueryRow(ctx, `SELECT row_version FROM clm_contract_saved_views
		WHERE tenant_id=$1 AND view_id=$2 AND owner_user_id=$3`,
		accounts.Tenant(current), viewID, current.ID).Scan(&liveVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound("没有找到对应的保存视图")
	}
	if err != nil {
		return err
	}
	return fault.Conflict("保存视图已被修改，请刷新后重试")
}

func contractLedgerRowValues(item ContractLedgerItem, columns []string) map[string]string {
	values := make(map[string]string, len(columns))
	for _, column := range columns {
		switch column {
		case "contract_number":
			values[column] = ledgerStringValue(item.Number)
		case "contract_name":
			values[column] = item.Name
		case "authority_mode", "authority_mode_code":
			values[column] = item.AuthorityMode
		case "authority_mode_label":
			values[column] = contractLedgerDisplayLabel(item.AuthorityMode, contractLedgerAuthorityLabels)
		case "supplier_name":
			values[column] = ledgerStringValue(item.SupplierName)
		case "amount":
			values[column] = ledgerStringValue(item.Amount)
		case "currency":
			values[column] = ledgerStringValue(item.Currency)
		case "department_id":
			values[column] = ledgerStringValue(item.DepartmentID)
		case "project_type":
			values[column] = ledgerStringValue(item.ProjectType)
		case "procurement_method":
			values[column] = ledgerStringValue(item.ProcurementMethod)
		case "contract_state", "contract_state_code":
			values[column] = item.ContractState
		case "contract_state_label":
			values[column] = contractLedgerDisplayLabel(item.ContractState, contractLedgerStateLabels)
		case "workflow_state", "workflow_state_code":
			values[column] = item.WorkflowState
		case "workflow_state_label":
			values[column] = contractLedgerDisplayLabel(item.WorkflowState, contractLedgerWorkflowLabels)
		case "signing_date":
			values[column] = ledgerStringValue(item.SigningDate)
		case "source_system":
			values[column] = ledgerStringValue(item.SourceSystem)
		case "created_at":
			values[column] = item.CreatedAt.UTC().Format(contractLedgerTimeLayout)
		case "updated_at":
			values[column] = item.UpdatedAt.UTC().Format(contractLedgerTimeLayout)
		}
	}
	return values
}

func contractLedgerDisplayLabel(value string, labels map[string]string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	if value == "" {
		return "未记录"
	}
	return "待核对"
}

func ledgerStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// CreateContractExport freezes the filter, columns and visible row set, and
// records the authorization snapshot used at creation. The same idempotency
// key replays the original export metadata.
func (s *Store) CreateContractExport(ctx context.Context, user accounts.User, input ContractExportInput) (ContractExport, error) {
	ctx = commandContext(ctx, user)
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractExport{}, err
	}
	format := strings.TrimSpace(input.Format)
	if format == "" {
		format = contractExportFormatCSV
	}
	if format != contractExportFormatCSV {
		return ContractExport{}, fault.Invalid("本版导出只支持 csv")
	}
	if err := validateContractLedgerFilter(&input.Filter); err != nil {
		return ContractExport{}, err
	}
	columns, err := normalizeContractExportColumns(input.Columns)
	if err != nil {
		return ContractExport{}, err
	}
	digest, err := canonicalHash(struct {
		Schema  string               `json:"schema"`
		Filter  ContractLedgerFilter `json:"filter"`
		Columns []string             `json:"columns"`
		Format  string               `json:"format"`
	}{Schema: "clm-contract-export/v1", Filter: input.Filter, Columns: columns, Format: format})
	if err != nil {
		return ContractExport{}, err
	}
	tx, current, err := s.beginCLMWriteIsolated(ctx, user, "只读模式下不能创建导出", nil,
		pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ContractExport{}, err
	}
	defer rollbackCLM(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()
		|| ':clm-export:' || $1 || ':' || $2 || ':' || $3, 0))`,
		accounts.Tenant(current), current.ID, input.IdempotencyKey); err != nil {
		return ContractExport{}, err
	}
	existing, found, err := loadContractExportByKey(ctx, tx, current, input.IdempotencyKey)
	if err != nil {
		return ContractExport{}, err
	}
	if found {
		if existing.requestDigest != digest {
			return ContractExport{}, fault.Conflict("Idempotency-Key已用于不同导出请求")
		}
		if _, err := trustedUser(current); err != nil {
			return ContractExport{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractExport{}, err
		}
		return existing.export, nil
	}
	rows, contractIDs, err := collectContractExportRows(ctx, tx, current, input.Filter, columns)
	if err != nil {
		return ContractExport{}, err
	}
	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		return ContractExport{}, err
	}
	idsJSON, err := json.Marshal(contractIDs)
	if err != nil {
		return ContractExport{}, err
	}
	filterJSON, err := json.Marshal(input.Filter)
	if err != nil {
		return ContractExport{}, err
	}
	columnsJSON, err := json.Marshal(columns)
	if err != nil {
		return ContractExport{}, err
	}
	grantsDigest, err := canonicalHash(struct {
		Policy string         `json:"policy"`
		Role   string         `json:"role"`
		Grants []access.Grant `json:"grants"`
	}{Policy: access.PolicyVersion, Role: current.Role, Grants: current.Grants})
	if err != nil {
		return ContractExport{}, err
	}
	exportID, err := newContractUUID()
	if err != nil {
		return ContractExport{}, err
	}
	// A fresh record cannot be expired yet. This flag still says nothing
	// about the caller's current download authorization.
	item := ContractExport{
		ExportID: exportID, State: "ready", Format: format, Columns: columns,
		RowCount: len(rows), DownloadAvailable: true, Replayed: false,
	}
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_exports
		(tenant_id,export_id,actor_id,idempotency_key,request_digest,filter,columns,format,
		 contract_ids,rows,row_count,policy_version,grants_digest,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9::jsonb,$10::jsonb,$11,$12,$13,
			clock_timestamp()+interval '24 hours')
		RETURNING created_at,expires_at`,
		accounts.Tenant(current), exportID, current.ID, input.IdempotencyKey, digest,
		string(filterJSON), string(columnsJSON), format, string(idsJSON), string(rowsJSON),
		len(rows), access.PolicyVersion, grantsDigest).Scan(&item.CreatedAt, &item.ExpiresAt)
	if err != nil {
		return ContractExport{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractExport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractExport{}, err
	}
	return item, nil
}

type loadedContractExport struct {
	export        ContractExport
	requestDigest string
}

func loadContractExportByKey(ctx context.Context, tx pgx.Tx, current accounts.User, key string) (loadedContractExport, bool, error) {
	var loaded loadedContractExport
	var columnsJSON []byte
	err := tx.QueryRow(ctx, `SELECT export_id,format,columns,row_count,created_at,expires_at,request_digest
		FROM clm_contract_exports WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`,
		accounts.Tenant(current), current.ID, key).Scan(
		&loaded.export.ExportID, &loaded.export.Format, &columnsJSON, &loaded.export.RowCount,
		&loaded.export.CreatedAt, &loaded.export.ExpiresAt, &loaded.requestDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return loadedContractExport{}, false, nil
	}
	if err != nil {
		return loadedContractExport{}, false, err
	}
	if err := json.Unmarshal(columnsJSON, &loaded.export.Columns); err != nil {
		return loadedContractExport{}, false, fault.Conflict("导出列设置无法读取")
	}
	loaded.export.State = "ready"
	// Replay is scoped to the original actor and key, then recomputes expiry
	// only. It deliberately does not re-check current Read/Export grants;
	// LoadContractExportForDownload is the download authorization boundary.
	loaded.export.DownloadAvailable = loaded.export.ExpiresAt.After(time.Now().UTC())
	loaded.export.Replayed = true
	return loaded, true, nil
}

func collectContractExportRows(ctx context.Context, tx pgx.Tx, current accounts.User, filter ContractLedgerFilter, columns []string) ([]map[string]string, []string, error) {
	rowsOut := []map[string]string{}
	ids := []string{}
	afterSet, afterID := false, ""
	var afterKey time.Time
	scanned := 0
	for {
		rows, err := queryContractLedgerRows(ctx, tx, accounts.Tenant(current), filter, afterSet, afterKey, afterID, contractExportRowsBatch)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			scanned++
			afterSet, afterKey, afterID = true, row.sortKey, row.item.ContractID
			if scanned > contractExportMaxScanned {
				return nil, nil, fault.Invalid("筛选范围过大，请缩小条件后重试")
			}
			if !authorizeContractRead(ctx, tx, row.record, current) {
				continue
			}
			if !authorizeContract(row.record, current, access.Export) {
				return nil, nil, fault.Forbidden("当前授权不能导出筛选范围内的全部合同")
			}
			rowsOut = append(rowsOut, contractLedgerRowValues(row.item, columns))
			ids = append(ids, row.item.ContractID)
			if len(rowsOut) > contractExportMaxRows {
				return nil, nil, fault.Invalid("导出行数超过上限，请缩小筛选范围")
			}
		}
		if len(rows) < contractExportRowsBatch {
			break
		}
	}
	if len(rowsOut) == 0 {
		return nil, nil, fault.Invalid("当前筛选条件下没有可导出的合同")
	}
	return rowsOut, ids, nil
}

// LoadContractExportForDownload re-authorizes every frozen contract at pickup.
// Any lost Read/Export permission, identity change, foreign owner or expiry is
// answered as the same 404. The metadata field DownloadAvailable is expiry-only
// and must never be treated as this authorization decision.
func (s *Store) LoadContractExportForDownload(ctx context.Context, user accounts.User, exportID string) (ContractExportDownload, error) {
	ctx = commandContext(ctx, user)
	normalized, err := normalizeContractUUID(exportID)
	if err != nil {
		return ContractExportDownload{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ContractExportDownload{}, err
	}
	defer rollbackCLM(tx)
	var actorID, format, policyVersion string
	var columnsJSON, idsJSON, rowsJSON []byte
	var rowCount int
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT actor_id,format,columns,contract_ids,rows,row_count,policy_version,expires_at
		FROM clm_contract_exports WHERE tenant_id=$1 AND export_id=$2`,
		accounts.Tenant(current), normalized).Scan(&actorID, &format, &columnsJSON, &idsJSON, &rowsJSON, &rowCount, &policyVersion, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractExportDownload{}, fault.NotFound("没有找到对应的导出")
	}
	if err != nil {
		return ContractExportDownload{}, err
	}
	if actorID != current.ID || policyVersion == "" || !expiresAt.After(time.Now().UTC()) {
		return ContractExportDownload{}, fault.NotFound("没有找到对应的导出")
	}
	if format != contractExportFormatCSV {
		return ContractExportDownload{}, fault.Conflict("导出格式无法识别")
	}
	var columns []string
	var contractIDs []string
	var rows []map[string]string
	if err := json.Unmarshal(columnsJSON, &columns); err != nil {
		return ContractExportDownload{}, fault.Conflict("导出列设置无法读取")
	}
	normalizedColumns, err := normalizeContractExportColumns(columns)
	if err != nil {
		return ContractExportDownload{}, fault.Conflict("导出列设置无法读取")
	}
	if err := json.Unmarshal(idsJSON, &contractIDs); err != nil || len(contractIDs) != rowCount {
		return ContractExportDownload{}, fault.Conflict("导出内容无法读取")
	}
	if err := json.Unmarshal(rowsJSON, &rows); err != nil || len(rows) != rowCount {
		return ContractExportDownload{}, fault.Conflict("导出内容无法读取")
	}
	records := make([]clmContractRecord, 0, len(contractIDs))
	for _, contractID := range contractIDs {
		record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
		if err != nil {
			var faultErr *fault.Error
			if errors.As(err, &faultErr) && faultErr.Status == 404 {
				return ContractExportDownload{}, fault.NotFound("没有找到对应的导出")
			}
			return ContractExportDownload{}, err
		}
		records = append(records, record)
	}
	if !contractExportAuthorized(ctx, tx, records, current) {
		return ContractExportDownload{}, fault.NotFound("没有找到对应的导出")
	}
	for _, row := range rows {
		if len(row) != len(normalizedColumns) {
			return ContractExportDownload{}, fault.Conflict("导出内容无法读取")
		}
		for _, column := range normalizedColumns {
			if _, ok := row[column]; !ok {
				return ContractExportDownload{}, fault.Conflict("导出内容无法读取")
			}
		}
	}
	if _, err := trustedUser(current); err != nil {
		return ContractExportDownload{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractExportDownload{}, err
	}
	return ContractExportDownload{ExportID: normalized, Format: format, Columns: normalizedColumns, Rows: rows, ExpiresAt: expiresAt}, nil
}

// ReauthorizeContractExportDownload is a legacy identity-only check. It does
// not recheck export ownership or contract scope and must not be used as the
// download authorization boundary; callers must reload with
// LoadContractExportForDownload after rendering and before sending bytes.
func ReauthorizeContractExportDownload(user accounts.User) error {
	if _, err := trustedUser(user); err != nil {
		return fault.NotFound("没有找到对应的导出")
	}
	return nil
}

// contractExportAuthorized is the single download-time policy check. It is a
// pure function so the revocation case can be tested without a live directory.
func contractExportAuthorized(ctx context.Context, tx pgx.Tx, records []clmContractRecord, current accounts.User) bool {
	for _, record := range records {
		if !authorizeContractRead(ctx, tx, record, current) || !authorizeContract(record, current, access.Export) {
			return false
		}
	}
	return true
}

// RenderContractExportCSV writes a UTF-8 BOM CSV with Chinese headers and a
// formula-injection guard. Frozen rows are already restricted-field free.
func RenderContractExportCSV(columns []string, rows []map[string]string) ([]byte, error) {
	normalized, err := normalizeContractExportColumns(columns)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	buffer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buffer)
	header := make([]string, 0, len(normalized))
	for _, column := range normalized {
		label, ok := contractLedgerColumnLabels[column]
		if !ok {
			label = contractExportColumnLabels[column]
		}
		header = append(header, label)
	}
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	for _, row := range rows {
		record := make([]string, 0, len(normalized))
		for _, column := range normalized {
			record = append(record, csvSafeCell(row[column]))
		}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func csvSafeCell(value string) string {
	if value == "" {
		return value
	}
	first := rune(value[0])
	if first == '=' || first == '+' || first == '-' || first == '@' || first == '\t' || first == '\r' || first < 0x20 {
		return "'" + value
	}
	return value
}

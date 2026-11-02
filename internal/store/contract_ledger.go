package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// Contract ledger reads the existing aggregate and its sealed content without
// adding a second projection table. Every row is authorized through the same
// case-scope policy as the detail endpoint; SQL predicates only narrow data.
const (
	contractLedgerDefaultLimit = 25
	contractLedgerMaxLimit     = 100
	contractLedgerCursorVer    = 1
	contractLedgerSortCreated  = "created_at"

	contractSavedViewNameMax     = 60
	contractSavedViewColumnsMax  = 20
	contractExportMaxRows        = 5000
	contractExportMaxScanned     = 20000
	contractExportFormatCSV      = "csv"
	contractExportRowsBatch      = 500
	contractLedgerContentAmount  = `^(0|[1-9][0-9]{0,15})\.[0-9]{2}$`
	contractLedgerTimeLayout     = time.RFC3339
	contractLedgerDateLayout     = "2006-01-02"
	contractLedgerCursorMaxBytes = 2048
)

// ContractLedgerFilter is the shared filter shape of list, saved views and
// export. Unknown values are rejected before they reach SQL.
type ContractLedgerFilter struct {
	Text              string     `json:"text,omitempty"`
	SupplierName      string     `json:"supplier_name,omitempty"`
	DepartmentID      string     `json:"department_id,omitempty"`
	ContractState     string     `json:"contract_state,omitempty"`
	WorkflowState     string     `json:"workflow_state,omitempty"`
	AuthorityMode     string     `json:"authority_mode,omitempty"`
	ValidityState     string     `json:"validity_state,omitempty"`
	ProjectType       string     `json:"project_type,omitempty"`
	ProcurementMethod string     `json:"procurement_method,omitempty"`
	AmountMin         *string    `json:"amount_min,omitempty"`
	AmountMax         *string    `json:"amount_max,omitempty"`
	SigningDateFrom   *string    `json:"signing_date_from,omitempty"`
	SigningDateTo     *string    `json:"signing_date_to,omitempty"`
	CreatedFrom       *time.Time `json:"created_from,omitempty"`
	CreatedTo         *time.Time `json:"created_to,omitempty"`
}

// ContractLedgerItem is one visible ledger row. Restricted supplier fields are
// never projected into this shape.
type ContractLedgerItem struct {
	ContractID        string    `json:"contract_id"`
	AuthorityMode     string    `json:"authority_mode"`
	Name              string    `json:"name"`
	Number            *string   `json:"number"`
	Amount            *string   `json:"amount"`
	Currency          *string   `json:"currency"`
	ContractState     string    `json:"contract_state"`
	WorkflowState     string    `json:"workflow_state"`
	SupplierName      *string   `json:"supplier_name,omitempty"`
	DepartmentID      *string   `json:"department_id,omitempty"`
	ProjectType       *string   `json:"project_type,omitempty"`
	ProcurementMethod *string   `json:"procurement_method,omitempty"`
	SigningDate       *string   `json:"signing_date,omitempty"`
	SourceSystem      *string   `json:"source_system,omitempty"`
	OwnerOrgID        string    `json:"owner_org_id"`
	RowVersion        int64     `json:"row_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ContractLedgerPage is one keyset page. The cursor is opaque to clients.
type ContractLedgerPage struct {
	Items      []ContractLedgerItem `json:"items"`
	NextCursor *string              `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

// ContractLedgerQuery carries one validated list request.
type ContractLedgerQuery struct {
	Filter ContractLedgerFilter
	Sort   string
	Cursor string
	Limit  int
}

// ContractSavedView is a personal filter/column preset. It is a preference,
// not an audit fact, so it may be replaced or deleted by its owner.
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
	ExportID          string    `json:"export_id"`
	State             string    `json:"state"`
	Format            string    `json:"format"`
	Columns           []string  `json:"columns"`
	RowCount          int       `json:"row_count"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	DownloadAvailable bool      `json:"download_available"`
	Replayed          bool      `json:"replayed"`
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

var contractLedgerDefaultColumns = []string{
	"contract_number", "contract_name", "supplier_name", "amount", "currency",
	"department_id", "contract_state", "workflow_state", "updated_at",
}

var contractLifecycleStates = map[string]bool{
	"draft": true, "ready_to_sign": true, "registered": true,
	"performing": true, "closed": true, "terminated": true, "external_observed": true,
}

var contractWorkflowStates = map[string]bool{"not_started": true, "in_review": true, "external_observed": true}
var contractAuthorityModes = map[string]bool{"native": true, "external": true}
var contractValidityStates = map[string]bool{"active": true, "voided": true}

func normalizeLedgerText(field string, value *string, max int) error {
	trimmed := strings.TrimSpace(*value)
	if runeCount(trimmed) > max {
		return fault.Invalid(field + "超过最大长度")
	}
	*value = trimmed
	return nil
}

func ledgerAmountScaled(value string) (int64, bool) {
	if !moneyPattern.MatchString(value) {
		return 0, false
	}
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	cents, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return whole*100 + cents, true
}

func normalizeLedgerAmount(field string, value **string) error {
	if value == nil || *value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(**value)
	if trimmed == "" {
		*value = nil
		return nil
	}
	if _, ok := ledgerAmountScaled(trimmed); !ok {
		return fault.Invalid(field + "必须是两位小数的非负金额字符串")
	}
	*value = &trimmed
	return nil
}

func normalizeLedgerDate(field string, value **string) error {
	if value == nil || *value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(**value)
	if trimmed == "" {
		*value = nil
		return nil
	}
	parsed, err := time.Parse(contractLedgerDateLayout, trimmed)
	if err != nil {
		return fault.Invalid(field + "必须是有效日期")
	}
	formatted := parsed.Format(contractLedgerDateLayout)
	*value = &formatted
	return nil
}

func normalizeLedgerTime(field string, value *time.Time) error {
	if value == nil {
		return nil
	}
	if value.IsZero() {
		return fault.Invalid(field + "必须是有效时间")
	}
	*value = value.UTC()
	return nil
}

// validateContractLedgerFilter normalizes and validates every field in place.
// The validated shape is the value persisted in saved views and exports.
func validateContractLedgerFilter(filter *ContractLedgerFilter) error {
	for _, item := range []struct {
		field string
		value *string
		max   int
	}{
		{"text", &filter.Text, 200},
		{"supplier_name", &filter.SupplierName, 200},
		{"department_id", &filter.DepartmentID, 100},
		{"project_type", &filter.ProjectType, 100},
		{"procurement_method", &filter.ProcurementMethod, 100},
	} {
		if err := normalizeLedgerText(item.field, item.value, item.max); err != nil {
			return err
		}
	}
	if filter.ContractState != "" && !contractLifecycleStates[filter.ContractState] {
		return fault.Invalid("contract_state 不是有效的合同状态")
	}
	if filter.WorkflowState != "" && !contractWorkflowStates[filter.WorkflowState] {
		return fault.Invalid("workflow_state 不是有效的会签状态")
	}
	if filter.AuthorityMode != "" && !contractAuthorityModes[filter.AuthorityMode] {
		return fault.Invalid("authority_mode 必须是 native 或 external")
	}
	if filter.ValidityState != "" && !contractValidityStates[filter.ValidityState] {
		return fault.Invalid("validity_state 必须是 active 或 voided")
	}
	if err := normalizeLedgerAmount("amount_min", &filter.AmountMin); err != nil {
		return err
	}
	if err := normalizeLedgerAmount("amount_max", &filter.AmountMax); err != nil {
		return err
	}
	if filter.AmountMin != nil && filter.AmountMax != nil {
		minimum, _ := ledgerAmountScaled(*filter.AmountMin)
		maximum, _ := ledgerAmountScaled(*filter.AmountMax)
		if minimum > maximum {
			return fault.Invalid("amount_min 不能大于 amount_max")
		}
	}
	if err := normalizeLedgerDate("signing_date_from", &filter.SigningDateFrom); err != nil {
		return err
	}
	if err := normalizeLedgerDate("signing_date_to", &filter.SigningDateTo); err != nil {
		return err
	}
	if filter.SigningDateFrom != nil && filter.SigningDateTo != nil && *filter.SigningDateFrom > *filter.SigningDateTo {
		return fault.Invalid("signing_date_from 不能晚于 signing_date_to")
	}
	if err := normalizeLedgerTime("created_from", filter.CreatedFrom); err != nil {
		return err
	}
	if err := normalizeLedgerTime("created_to", filter.CreatedTo); err != nil {
		return err
	}
	if filter.CreatedFrom != nil && filter.CreatedTo != nil && filter.CreatedFrom.After(*filter.CreatedTo) {
		return fault.Invalid("created_from 不能晚于 created_to")
	}
	return nil
}

func normalizeContractLedgerColumns(columns []string) ([]string, error) {
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
			return nil, fault.Invalid("不支持的台账列")
		}
		if seen[column] {
			return nil, fault.Invalid("台账列不能重复")
		}
		seen[column] = true
		out = append(out, column)
	}
	return out, nil
}

func validateContractLedgerSort(sort string) (string, error) {
	if sort == "" {
		return contractLedgerSortCreated, nil
	}
	if sort != contractLedgerSortCreated {
		return "", fault.Invalid("当前只支持按创建时间排序")
	}
	return sort, nil
}

func contractLedgerFilterDigest(filter ContractLedgerFilter, sort string) (string, error) {
	return canonicalHash(struct {
		Schema string               `json:"schema"`
		Filter ContractLedgerFilter `json:"filter"`
		Sort   string               `json:"sort"`
	}{Schema: "clm-contract-ledger-filter/v1", Filter: filter, Sort: sort})
}

type contractLedgerCursor struct {
	Version      int       `json:"v"`
	TenantID     string    `json:"t"`
	ActorID      string    `json:"a"`
	Sort         string    `json:"s"`
	FilterDigest string    `json:"f"`
	Key          time.Time `json:"k"`
	ContractID   string    `json:"i"`
}

func encodeContractLedgerCursor(current accounts.User, sort, digest string, key time.Time, contractID string) string {
	raw, _ := json.Marshal(contractLedgerCursor{
		Version: contractLedgerCursorVer, TenantID: accounts.Tenant(current), ActorID: current.ID,
		Sort: sort, FilterDigest: digest, Key: key.UTC(), ContractID: contractID,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeContractLedgerCursor(value string, current accounts.User, sort, digest string) (*contractLedgerCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > contractLedgerCursorMaxBytes {
		return nil, fault.Invalid("台账分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor contractLedgerCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil ||
		cursor.Version != contractLedgerCursorVer || cursor.TenantID != accounts.Tenant(current) ||
		cursor.ActorID != current.ID || cursor.Sort != sort || cursor.FilterDigest != digest ||
		cursor.Key.IsZero() || !validContractUUID(cursor.ContractID) {
		return nil, fault.Invalid("台账分页游标无效")
	}
	return &cursor, nil
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
	var draft DraftContent
	if len(content) != 0 {
		if err := unmarshalStoredDraft(content, &draft); err != nil {
			return ContractLedgerItem{}, err
		}
	}
	item := ContractLedgerItem{
		ContractID: record.ContractID, AuthorityMode: record.AuthorityMode,
		Number: record.ContractNumber, ContractState: record.LifecycleState,
		WorkflowState: "not_started", OwnerOrgID: record.OwnerOrgID,
		SourceSystem: record.SourceSystem, RowVersion: record.RowVersion,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.AuthorityMode == "external" {
		item.ContractState = "external_observed"
		item.WorkflowState = "external_observed"
	} else if inReview {
		item.WorkflowState = "in_review"
	}
	if basics := draft.Contract; basics != nil {
		item.Name = basics.Name
		if basics.Number != nil {
			item.Number = basics.Number
		}
		item.Amount = optionalText(basics.Amount)
		item.Currency = optionalText(basics.Currency)
		item.DepartmentID = optionalText(basics.DepartmentID)
		item.ProjectType = optionalText(basics.ProjectType)
		item.ProcurementMethod = optionalText(basics.ProcurementMethod)
		item.SigningDate = optionalText(basics.SigningDate)
	}
	if draft.Supplier != nil {
		item.SupplierName = optionalText(draft.Supplier.Name)
	}
	return item, nil
}

func appendContractLedgerFilter(where *strings.Builder, args *[]any, filter ContractLedgerFilter) {
	add := func(predicate string, value any) {
		*args = append(*args, value)
		where.WriteString(fmt.Sprintf(predicate, len(*args)))
	}
	if filter.Text != "" {
		*args = append(*args, filter.Text)
		position := len(*args)
		where.WriteString(fmt.Sprintf(` AND (position(lower($%d) in lower(coalesce(k.content->'contract'->>'name','')))>0`+
			` OR position(lower($%d) in lower(coalesce(k.contract_number,'')))>0`+
			` OR position(lower($%d) in lower(coalesce(k.content->'contract'->>'number','')))>0)`,
			position, position, position))
	}
	if filter.SupplierName != "" {
		add(` AND position(lower($%d) in lower(coalesce(k.content->'supplier'->>'name','')))>0`, filter.SupplierName)
	}
	if filter.DepartmentID != "" {
		add(` AND k.content->'contract'->>'department_id'=$%d`, filter.DepartmentID)
	}
	if filter.ProjectType != "" {
		add(` AND k.content->'contract'->>'project_type'=$%d`, filter.ProjectType)
	}
	if filter.ProcurementMethod != "" {
		add(` AND k.content->'contract'->>'procurement_method'=$%d`, filter.ProcurementMethod)
	}
	if filter.ContractState != "" {
		add(` AND (CASE WHEN k.authority_mode='external' THEN 'external_observed' ELSE k.lifecycle_state END)=$%d`, filter.ContractState)
	}
	if filter.AuthorityMode != "" {
		add(` AND k.authority_mode=$%d`, filter.AuthorityMode)
	}
	if filter.ValidityState != "" {
		add(` AND k.validity_state=$%d`, filter.ValidityState)
	}
	if filter.WorkflowState == "in_review" {
		where.WriteString(` AND k.authority_mode='native' AND k.in_review`)
	} else if filter.WorkflowState == "not_started" {
		where.WriteString(` AND k.authority_mode='native' AND NOT k.in_review`)
	} else if filter.WorkflowState == "external_observed" {
		where.WriteString(` AND k.authority_mode='external'`)
	}
	if filter.AmountMin != nil {
		add(` AND (CASE WHEN k.content->'contract'->>'amount' ~ '`+contractLedgerContentAmount+`' THEN (k.content->'contract'->>'amount')::numeric END) >= $%d::numeric`, *filter.AmountMin)
	}
	if filter.AmountMax != nil {
		add(` AND (CASE WHEN k.content->'contract'->>'amount' ~ '`+contractLedgerContentAmount+`' THEN (k.content->'contract'->>'amount')::numeric END) <= $%d::numeric`, *filter.AmountMax)
	}
	if filter.SigningDateFrom != nil {
		add(` AND k.signing_date >= $%d`, *filter.SigningDateFrom)
	}
	if filter.SigningDateTo != nil {
		add(` AND k.signing_date <= $%d`, *filter.SigningDateTo)
	}
	if filter.CreatedFrom != nil {
		add(` AND k.created_at >= $%d::timestamptz`, filter.CreatedFrom.UTC())
	}
	if filter.CreatedTo != nil {
		add(` AND k.created_at <= $%d::timestamptz`, filter.CreatedTo.UTC())
	}
}

func queryContractLedgerRows(ctx context.Context, tx pgx.Tx, tenant string, filter ContractLedgerFilter,
	afterSet bool, afterKey time.Time, afterID string, limit int) ([]contractLedgerRow, error) {
	where := &strings.Builder{}
	where.WriteString(`WHERE k.tenant_id=$1`)
	args := []any{tenant}
	appendContractLedgerFilter(where, &args, filter)
	if afterSet {
		args = append(args, afterKey.UTC(), afterID)
		where.WriteString(fmt.Sprintf(` AND (k.created_at,k.contract_id) < ($%d::timestamptz,$%d::uuid)`, len(args)-1, len(args)))
	}
	args = append(args, limit)
	query := `SELECT k.tenant_id,k.contract_id,k.owner_org_id,k.authority_mode,k.contract_number,
		k.lifecycle_state,k.validity_state,k.row_version,k.created_by,k.created_at,k.updated_at,
		k.source_system,k.in_review,k.content,k.signing_date
	FROM (
		SELECT c.tenant_id,c.contract_id,c.owner_org_id,c.authority_mode,c.contract_number,
			c.lifecycle_state,c.validity_state,c.row_version,c.created_by,c.created_at,c.updated_at,
			c.source_system,COALESCE(d.content,r.snapshot) AS content,
			COALESCE((SELECT registration.signing_date::text FROM clm_contract_signing_registrations registration
				WHERE registration.tenant_id=c.tenant_id AND registration.contract_id=c.contract_id),
				COALESCE(d.content->'contract'->>'signing_date',r.snapshot->'contract'->>'signing_date')) AS signing_date,
			EXISTS(SELECT 1 FROM clm_approval_rounds ar
				WHERE ar.tenant_id=c.tenant_id AND ar.contract_id=c.contract_id AND ar.state='in_review') AS in_review
		FROM clm_contracts c
		LEFT JOIN clm_contract_drafts d ON d.tenant_id=c.tenant_id AND d.contract_id=c.contract_id
		LEFT JOIN clm_contract_revisions r ON r.tenant_id=c.tenant_id AND r.contract_id=c.contract_id
			AND r.contract_revision_id=c.current_revision_id
	) k ` + where.String() + ` ORDER BY k.created_at DESC,k.contract_id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contractLedgerRow{}
	for rows.Next() {
		var record clmContractRecord
		var content []byte
		var inReview bool
		var signingDate *string
		if err := rows.Scan(&record.TenantID, &record.ContractID, &record.OwnerOrgID, &record.AuthorityMode,
			&record.ContractNumber, &record.LifecycleState, &record.ValidityState, &record.RowVersion,
			&record.CreatedBy, &record.CreatedAt, &record.UpdatedAt, &record.SourceSystem, &inReview, &content, &signingDate); err != nil {
			return nil, err
		}
		item, err := contractLedgerItemFrom(record, content, inReview)
		if err != nil {
			return nil, err
		}
		item.SigningDate = signingDate
		out = append(out, contractLedgerRow{record: record, item: item, sortKey: record.CreatedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListContractLedger returns one authorized keyset page. SQL narrows by the
// validated filter and the case-scope policy authorizes every returned row.
func (s *Store) ListContractLedger(ctx context.Context, user accounts.User, query ContractLedgerQuery) (ContractLedgerPage, error) {
	ctx = commandContext(ctx, user)
	limit := query.Limit
	if limit == 0 {
		limit = contractLedgerDefaultLimit
	}
	if limit < 1 || limit > contractLedgerMaxLimit {
		return ContractLedgerPage{}, fault.Invalid("limit 必须在 1 到 100 之间")
	}
	if err := validateContractLedgerFilter(&query.Filter); err != nil {
		return ContractLedgerPage{}, err
	}
	sort, err := validateContractLedgerSort(query.Sort)
	if err != nil {
		return ContractLedgerPage{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ContractLedgerPage{}, err
	}
	defer rollbackCLM(tx)
	digest, err := contractLedgerFilterDigest(query.Filter, sort)
	if err != nil {
		return ContractLedgerPage{}, err
	}
	before, err := decodeContractLedgerCursor(query.Cursor, current, sort, digest)
	if err != nil {
		return ContractLedgerPage{}, err
	}
	page := ContractLedgerPage{Items: []ContractLedgerItem{}}
	afterSet, afterID := false, ""
	var afterKey time.Time
	var lastKey time.Time
	if before != nil {
		afterSet, afterKey, afterID = true, before.Key, before.ContractID
	}
	for {
		rows, err := queryContractLedgerRows(ctx, tx, accounts.Tenant(current), query.Filter, afterSet, afterKey, afterID, limit+1)
		if err != nil {
			return ContractLedgerPage{}, err
		}
		foundExtra := false
		for _, row := range rows {
			afterSet, afterKey, afterID = true, row.sortKey, row.item.ContractID
			if !authorizeContractRead(ctx, tx, row.record, current) {
				continue
			}
			if len(page.Items) == limit {
				foundExtra = true
				break
			}
			page.Items = append(page.Items, row.item)
			lastKey = row.sortKey
		}
		if foundExtra {
			cursor := encodeContractLedgerCursor(current, sort, digest, lastKey, page.Items[len(page.Items)-1].ContractID)
			page.NextCursor = &cursor
			page.HasMore = true
			break
		}
		if len(rows) < limit+1 {
			break
		}
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
		case "authority_mode":
			values[column] = item.AuthorityMode
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
		case "contract_state":
			values[column] = item.ContractState
		case "workflow_state":
			values[column] = item.WorkflowState
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
	columns, err := normalizeContractLedgerColumns(input.Columns)
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
// answered as the same 404.
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
	normalizedColumns, err := normalizeContractLedgerColumns(columns)
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

// ReauthorizeContractExportDownload re-resolves the directory identity after
// the CSV is rendered and before any byte is sent. A revoked or changed
// identity is answered as the same 404 as an unknown export.
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
	normalized, err := normalizeContractLedgerColumns(columns)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	buffer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buffer)
	header := make([]string, 0, len(normalized))
	for _, column := range normalized {
		header = append(header, contractLedgerColumnLabels[column])
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

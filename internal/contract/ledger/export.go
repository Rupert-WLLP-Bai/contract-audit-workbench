// File metadata (manifest mirror):
// Purpose: Defines Contract ledger export DTOs, allowed columns, frozen row projection and compatible CSV encoding with formula protection.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package ledger

import (
	"bytes"
	"encoding/csv"
	"time"

	"contractaudit/internal/fault"
)

const (
	ExportMaxRows    = 5000
	ExportMaxScanned = 20000
	ExportFormatCSV  = "csv"
	ExportRowsBatch  = 500
)

// ExportInput is the source-compatible request shape for a frozen ledger export.
type ExportInput struct {
	Filter         Filter   `json:"filter"`
	Columns        []string `json:"columns"`
	Format         string   `json:"format"`
	IdempotencyKey string   `json:"-"`
}

// Export is metadata for a created or replayed export. It never contains rows.
type Export struct {
	ExportID  string    `json:"export_id"`
	State     string    `json:"state"`
	Format    string    `json:"format"`
	Columns   []string  `json:"columns"`
	RowCount  int       `json:"row_count"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// DownloadAvailable reports expiry only. Download always rechecks current
	// identity and permissions for every frozen contract.
	DownloadAvailable bool `json:"download_available"`
	Replayed          bool `json:"replayed"`
}

// ExportDownload contains a validated, frozen row set authorized for download.
type ExportDownload struct {
	ExportID  string
	Format    string
	Columns   []string
	Rows      []map[string]string
	ExpiresAt time.Time
}

// ExportIdentity is resolved by the authenticated Store boundary.
type ExportIdentity struct {
	TenantID string
	ActorID  string
}

// ExportReplay is the metadata needed to resolve a same-key replay.
type ExportReplay struct {
	Export        Export
	RequestDigest string
	ColumnsJSON   []byte
}

// ExportInsert is one immutable JSONB snapshot written by the Contract adapter.
type ExportInsert struct {
	TenantID       string
	ExportID       string
	ActorID        string
	IdempotencyKey string
	RequestDigest  string
	FilterJSON     []byte
	ColumnsJSON    []byte
	Format         string
	ContractIDs    []byte
	RowsJSON       []byte
	RowCount       int
	PolicyVersion  string
	GrantsDigest   string
}

// ExportArtifact is the stored snapshot as read from clm_contract_exports.
type ExportArtifact struct {
	ActorID       string
	Format        string
	ColumnsJSON   []byte
	ContractIDs   []byte
	RowsJSON      []byte
	RowCount      int
	PolicyVersion string
	ExpiresAt     time.Time
}

var exportOnlyColumnLabels = map[string]string{
	"authority_mode_label": "来源类型名称",
	"authority_mode_code":  "来源类型代码",
	"contract_state_label": "合同状态名称",
	"contract_state_code":  "合同状态代码",
	"workflow_state_label": "会签状态名称",
	"workflow_state_code":  "会签状态代码",
}

// NormalizeExportColumns validates columns using the same standard-column
// policy as personal views, while allowing the additional export-only fields.
func NormalizeExportColumns(columns []string) ([]string, error) {
	if len(columns) == 0 {
		return DefaultSavedViewColumns(), nil
	}
	if len(columns) > MaxSavedViewColumns {
		return nil, fault.Invalid("导出或视图列最多 20 项")
	}
	seen := make(map[string]bool, len(columns))
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		if !IsSavedViewColumn(column) {
			if _, exportOnly := exportOnlyColumnLabels[column]; !exportOnly {
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

// ExportRowValues projects only fields already present in the unrestricted
// Contract ledger item. Restricted supplier fields never enter the snapshot.
func ExportRowValues(item Item, columns []string) map[string]string {
	values := make(map[string]string, len(columns))
	for _, column := range columns {
		switch column {
		case "contract_number":
			values[column] = stringValue(item.Number)
		case "contract_name":
			values[column] = item.Name
		case "authority_mode", "authority_mode_code":
			values[column] = item.AuthorityMode
		case "authority_mode_label":
			values[column] = displayLabel(item.AuthorityMode, authorityLabels)
		case "supplier_name":
			values[column] = stringValue(item.SupplierName)
		case "amount":
			values[column] = stringValue(item.Amount)
		case "currency":
			values[column] = stringValue(item.Currency)
		case "department_id":
			values[column] = stringValue(item.DepartmentID)
		case "project_type":
			values[column] = stringValue(item.ProjectType)
		case "procurement_method":
			values[column] = stringValue(item.ProcurementMethod)
		case "contract_state", "contract_state_code":
			values[column] = item.ContractState
		case "contract_state_label":
			values[column] = displayLabel(item.ContractState, contractStateLabels)
		case "workflow_state", "workflow_state_code":
			values[column] = item.WorkflowState
		case "workflow_state_label":
			values[column] = displayLabel(item.WorkflowState, workflowStateLabels)
		case "signing_date":
			values[column] = stringValue(item.SigningDate)
		case "source_system":
			values[column] = stringValue(item.SourceSystem)
		case "created_at":
			values[column] = item.CreatedAt.UTC().Format(TimeLayout)
		case "updated_at":
			values[column] = item.UpdatedAt.UTC().Format(TimeLayout)
		}
	}
	return values
}

// RenderExportCSV writes the frozen rows with the legacy CSV contract.
func RenderExportCSV(columns []string, rows []map[string]string) ([]byte, error) {
	normalized, err := NormalizeExportColumns(columns)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	buffer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buffer)
	header := make([]string, 0, len(normalized))
	for _, column := range normalized {
		label, ok := SavedViewColumnLabel(column)
		if !ok {
			label = exportOnlyColumnLabels[column]
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

func displayLabel(value string, labels map[string]string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	if value == "" {
		return "未记录"
	}
	return "待核对"
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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

var (
	authorityLabels = map[string]string{
		"native": "本地登记", "external": "外部来源",
	}
	contractStateLabels = map[string]string{
		"draft": "草稿", "ready_to_sign": "待签署", "registered": "已登记",
		"performing": "履约中", "closed": "已关闭", "terminated": "已终止",
		"external_observed": "外部来源已观测",
	}
	workflowStateLabels = map[string]string{
		"not_started": "当前无进行中会签", "in_review": "会签中", "external_observed": "外部来源已观测",
	}
)

// File metadata (manifest mirror):
// Purpose: Preserves exact legacy CSV bytes and verifies export-only columns, formula safety, row projection and frozen label materialization.
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
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/fault"
)

func exportFaultStatus(err error) int {
	var item *fault.Error
	if errors.As(err, &item) {
		return item.Status
	}
	return 0
}

func TestRenderExportCSVGuardsFormulaAndUsesBOM(t *testing.T) {
	document, err := RenderExportCSV(
		[]string{"contract_name", "amount"},
		[]map[string]string{{"contract_name": `=HYPERLINK("https://example.invalid")`, "amount": "10.00"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(document, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV lacks UTF-8 BOM")
	}
	body := string(document[3:])
	if !strings.Contains(body, "'=HYPERLINK") {
		t.Fatalf("CSV formula was not quoted: %q", body)
	}
	if _, err := RenderExportCSV([]string{"bank_account"}, nil); exportFaultStatus(err) != 422 {
		t.Fatalf("restricted column = %v; want 422", err)
	}
}

func TestExportLegacyCSVExactBytes(t *testing.T) {
	columns := []string{"workflow_state", "contract_name", "authority_mode", "contract_state", "supplier_name"}
	rows := []map[string]string{{
		"workflow_state": "not_started", "contract_name": "=SUM(1,2)",
		"authority_mode": "native", "contract_state": "draft", "supplier_name": "甲,乙\"公司",
	}}
	want := "\xef\xbb\xbf会签状态,合同名称,权威模式,合同状态,供应商\nnot_started,\"'=SUM(1,2)\",native,draft,\"甲,乙\"\"公司\"\n"
	got, err := RenderExportCSV(columns, rows)
	if err != nil || string(got) != want {
		t.Fatalf("legacy CSV changed: got %q, want %q, err=%v", got, want, err)
	}
	allColumns := []string{"updated_at", "contract_state", "authority_mode", "contract_number", "contract_name", "supplier_name", "amount", "currency", "department_id", "project_type", "procurement_method", "workflow_state", "signing_date", "source_system", "created_at"}
	allRows := []map[string]string{{
		"updated_at": "2026-11-05T00:00:00Z", "contract_state": "registered", "authority_mode": "external",
		"contract_number": "HT-1", "contract_name": "合同甲", "supplier_name": "供应商甲", "amount": "1.00",
		"currency": "CNY", "department_id": "dept", "project_type": "project", "procurement_method": "procure",
		"workflow_state": "external_observed", "signing_date": "2026-11-01", "source_system": "system", "created_at": "2026-11-01T00:00:00Z",
	}}
	allWant := "\xef\xbb\xbf更新时间,合同状态,权威模式,合同编号,合同名称,供应商,金额,币种,申请部门,项目类型,采购方式,会签状态,签订日期,来源系统,创建时间\n2026-11-05T00:00:00Z,registered,external,HT-1,合同甲,供应商甲,1.00,CNY,dept,project,procure,external_observed,2026-11-01,system,2026-11-01T00:00:00Z\n"
	allGot, err := RenderExportCSV(allColumns, allRows)
	if err != nil || string(allGot) != allWant {
		t.Fatalf("legacy all-column CSV changed: got %q, want %q, err=%v", allGot, allWant, err)
	}
}

func TestExportReadableProjection(t *testing.T) {
	columns := []string{"contract_name", "authority_mode_label", "authority_mode_code", "contract_state_label", "contract_state_code", "workflow_state_label", "workflow_state_code"}
	data, err := os.ReadFile("../../../web/src/pages/contracts-ledger-labels.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Authority, State, Workflow, AuthorityLabel, StateLabel, WorkflowLabel string
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("read shared UI/CSV fixture: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			item := Item{Name: "示例合同", AuthorityMode: tc.Authority, ContractState: tc.State, WorkflowState: tc.Workflow}
			row := ExportRowValues(item, columns)
			want := map[string]string{
				"contract_name": "示例合同", "authority_mode_label": tc.AuthorityLabel, "authority_mode_code": tc.Authority,
				"contract_state_label": tc.StateLabel, "contract_state_code": tc.State,
				"workflow_state_label": tc.WorkflowLabel, "workflow_state_code": tc.Workflow,
			}
			if !reflect.DeepEqual(row, want) {
				t.Fatalf("readable row = %#v, want %#v", row, want)
			}
			document, err := RenderExportCSV(columns, []map[string]string{row})
			if err != nil {
				t.Fatalf("render readable row: %v", err)
			}
			records, err := csv.NewReader(bytes.NewReader(document[3:])).ReadAll()
			wantRecord := []string{"示例合同", tc.AuthorityLabel, tc.Authority, tc.StateLabel, tc.State, tc.WorkflowLabel, tc.Workflow}
			if err != nil || len(records) != 2 || !reflect.DeepEqual(records[1], wantRecord) {
				t.Fatalf("readable CSV values = %v, want %v, err=%v", records, wantRecord, err)
			}
		})
	}
}

func TestExportReadableCodeUsesFormulaProtection(t *testing.T) {
	columns := []string{"contract_state_label", "contract_state_code"}
	row := ExportRowValues(Item{ContractState: "=future"}, columns)
	if row["contract_state_code"] != "=future" {
		t.Fatalf("code changed before CSV protection: %q", row["contract_state_code"])
	}
	document, err := RenderExportCSV(columns, []map[string]string{row})
	if err != nil || string(document) != "\xef\xbb\xbf合同状态名称,合同状态代码\n待核对,'=future\n" {
		t.Fatalf("readable CSV formula protection = %q, err=%v", document, err)
	}
}

func TestExportColumnLimitsDoNotExpandSavedViews(t *testing.T) {
	allColumns := []string{
		"contract_number", "contract_name", "authority_mode", "supplier_name", "amount", "currency", "department_id",
		"project_type", "procurement_method", "contract_state", "workflow_state", "signing_date", "source_system", "created_at", "updated_at",
		"authority_mode_label", "authority_mode_code", "contract_state_label", "contract_state_code", "workflow_state_label", "workflow_state_code",
	}
	if _, err := NormalizeSavedViewColumns([]string{"contract_state_label"}); exportFaultStatus(err) != 422 {
		t.Fatalf("export-only column accepted for a saved view: %v", err)
	}
	if _, err := RenderExportCSV(allColumns[:20], nil); err != nil {
		t.Fatalf("twenty distinct export columns should be accepted: %v", err)
	}
	if _, err := RenderExportCSV(allColumns, nil); exportFaultStatus(err) != 422 {
		t.Fatalf("twenty-one requested columns should be rejected: %v", err)
	}
}

func TestExportProjectionMaterializesLabelsAndOnlySelectedFields(t *testing.T) {
	columns := []string{"contract_state_label", "supplier_name"}
	item := Item{ContractState: "draft", SupplierName: stringPointer("供应商")}
	row := ExportRowValues(item, columns)
	if len(row) != len(columns) || row["supplier_name"] != "供应商" {
		t.Fatalf("projection shape = %#v", row)
	}
	original := contractStateLabels["draft"]
	contractStateLabels["draft"] = "未来标签"
	defer func() { contractStateLabels["draft"] = original }()
	document, err := RenderExportCSV(columns, []map[string]string{row})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(document, []byte("\n草稿,供应商\n")) {
		t.Fatalf("render re-resolved the frozen label: %q", document)
	}
}

func stringPointer(value string) *string { return &value }

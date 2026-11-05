// File metadata (manifest mirror):
// Purpose: Exercises the `store` package behavior through TestContractLedgerFilterValidation, TestContractLedgerCursorBindsActorFilterAndTenant, TestContractExportAuthorizedRequiresExportForEveryRow, TestRenderContractExportCSVGuardsFormulaAndUsesBOM; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-01
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

func textPointer(value string) *string { return &value }

func faultStatus(err error) int {
	var item *fault.Error
	if errors.As(err, &item) {
		return item.Status
	}
	return 0
}

func TestContractLedgerFilterValidation(t *testing.T) {
	cases := []struct {
		name   string
		filter ContractLedgerFilter
	}{
		{"amount order", ContractLedgerFilter{AmountMin: textPointer("100.00"), AmountMax: textPointer("99.99")}},
		{"amount shape", ContractLedgerFilter{AmountMin: textPointer("100")}},
		{"negative amount", ContractLedgerFilter{AmountMin: textPointer("-1.00")}},
		{"date order", ContractLedgerFilter{SigningDateFrom: textPointer("2026-02-02"), SigningDateTo: textPointer("2026-01-01")}},
		{"date shape", ContractLedgerFilter{SigningDateFrom: textPointer("2026-2-1")}},
		{"created order", ContractLedgerFilter{CreatedFrom: ledgerTimePointer("2026-02-02T00:00:00Z"), CreatedTo: ledgerTimePointer("2026-01-01T00:00:00Z")}},
		{"state enum", ContractLedgerFilter{ContractState: "unknown"}},
		{"workflow enum", ContractLedgerFilter{WorkflowState: "approved"}},
		{"authority enum", ContractLedgerFilter{AuthorityMode: "other"}},
		{"validity enum", ContractLedgerFilter{ValidityState: "expired"}},
		{"text length", ContractLedgerFilter{Text: strings.Repeat("x", 201)}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			filter := testCase.filter
			if err := validateContractLedgerFilter(&filter); faultStatus(err) != 422 {
				t.Fatalf("validateContractLedgerFilter() = %v; want 422", err)
			}
		})
	}
	valid := ContractLedgerFilter{
		Text: "  合同  ", SupplierName: " 供应商 ",
		AmountMin: textPointer("0.00"), AmountMax: textPointer("100.00"),
		SigningDateFrom: textPointer("2026-01-01"), SigningDateTo: textPointer("2026-12-31"),
		CreatedFrom: ledgerTimePointer("2026-01-01T08:00:00+08:00"),
	}
	if err := validateContractLedgerFilter(&valid); err != nil {
		t.Fatalf("valid filter rejected: %v", err)
	}
	if valid.Text != "合同" || valid.SupplierName != "供应商" {
		t.Fatalf("filter text was not trimmed: %+v", valid)
	}
	if valid.CreatedFrom.Location() != time.UTC {
		t.Fatalf("created_from was not normalized to UTC: %v", valid.CreatedFrom)
	}
}

func ledgerTimePointer(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return &parsed
}

func TestContractLedgerCursorBindsActorFilterAndTenant(t *testing.T) {
	user := accounts.User{ID: "actor-1", TenantID: "tenant-1"}
	sort := contractLedgerSortCreated
	digest, err := contractLedgerFilterDigest(ContractLedgerFilter{Text: "合同"}, sort)
	if err != nil {
		t.Fatal(err)
	}
	contractID := "11111111-1111-4111-8111-111111111111"
	key := time.Date(2026, 11, 1, 3, 4, 5, 123, time.UTC)
	encoded := encodeContractLedgerCursor(user, sort, digest, key, contractID)
	decoded, err := decodeContractLedgerCursor(encoded, user, sort, digest)
	if err != nil {
		t.Fatalf("decode own cursor: %v", err)
	}
	if decoded.ContractID != contractID || !decoded.Key.Equal(key) {
		t.Fatalf("decoded cursor drift: %+v", decoded)
	}
	otherActor := accounts.User{ID: "actor-2", TenantID: "tenant-1"}
	if _, err := decodeContractLedgerCursor(encoded, otherActor, sort, digest); faultStatus(err) != 422 {
		t.Fatalf("cursor reused by another actor = %v; want 422", err)
	}
	otherTenant := accounts.User{ID: "actor-1", TenantID: "tenant-2"}
	if _, err := decodeContractLedgerCursor(encoded, otherTenant, sort, digest); faultStatus(err) != 422 {
		t.Fatalf("cursor reused across tenant = %v; want 422", err)
	}
	otherDigest, err := contractLedgerFilterDigest(ContractLedgerFilter{Text: "另一份合同"}, sort)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeContractLedgerCursor(encoded, user, sort, otherDigest); faultStatus(err) != 422 {
		t.Fatalf("cursor reused after filter change = %v; want 422", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(encoded)
	raw[len(raw)-1] ^= 0xff
	if _, err := decodeContractLedgerCursor(base64.RawURLEncoding.EncodeToString(raw), user, sort, digest); faultStatus(err) != 422 {
		t.Fatalf("tampered cursor = %v; want 422", err)
	}
}

func TestContractExportAuthorizedRequiresExportForEveryRow(t *testing.T) {
	var tx pgx.Tx
	contractID := "22222222-2222-4222-8222-222222222222"
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo-admin missing")
	}
	adminRecord := clmContractRecord{
		TenantID: accounts.LocalDemoTenant, ContractID: contractID,
		OwnerOrgID: "demo-org", CreatedBy: "demo-admin",
	}
	if !contractExportAuthorized(context.Background(), tx, []clmContractRecord{adminRecord}, admin) {
		t.Fatal("case owner cannot export a visible contract")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo-reviewer missing")
	}
	reviewerRecord := adminRecord
	reviewerRecord.CreatedBy = "demo-reviewer"
	if !authorizeContract(reviewerRecord, reviewer, "case.read") {
		t.Fatal("reviewer should be able to read the bound contract")
	}
	if contractExportAuthorized(context.Background(), tx, []clmContractRecord{reviewerRecord}, reviewer) {
		t.Fatal("reviewer without case.export was allowed to download")
	}
	if err := ReauthorizeContractExportDownload(accounts.User{ID: "missing", TenantID: accounts.LocalDemoTenant}); faultStatus(err) != 404 {
		t.Fatalf("reauthorize revoked identity = %v; want 404", err)
	}
}

func TestRenderContractExportCSVGuardsFormulaAndUsesBOM(t *testing.T) {
	document, err := RenderContractExportCSV(
		[]string{"contract_name", "amount"},
		[]map[string]string{{"contract_name": "=HYPERLINK(\"https://example.invalid\")", "amount": "10.00"}},
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
	if _, err := RenderContractExportCSV([]string{"bank_account"}, nil); faultStatus(err) != 422 {
		t.Fatalf("restricted column = %v; want 422", err)
	}
}

func TestContractExportLegacyCSVExactBytes(t *testing.T) {
	columns := []string{"workflow_state", "contract_name", "authority_mode", "contract_state", "supplier_name"}
	rows := []map[string]string{{
		"workflow_state": "not_started", "contract_name": "=SUM(1,2)",
		"authority_mode": "native", "contract_state": "draft", "supplier_name": "甲,乙\"公司",
	}}
	want := "\xef\xbb\xbf会签状态,合同名称,权威模式,合同状态,供应商\nnot_started,\"'=SUM(1,2)\",native,draft,\"甲,乙\"\"公司\"\n"
	got, err := RenderContractExportCSV(columns, rows)
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
	allGot, err := RenderContractExportCSV(allColumns, allRows)
	if err != nil || string(allGot) != allWant {
		t.Fatalf("legacy all-column CSV changed: got %q, want %q, err=%v", allGot, allWant, err)
	}
}

func TestContractExportReadableProjection(t *testing.T) {
	columns := []string{"contract_name", "authority_mode_label", "authority_mode_code", "contract_state_label", "contract_state_code", "workflow_state_label", "workflow_state_code"}
	data, err := os.ReadFile("../../web/src/pages/contracts-ledger-labels.fixture.json")
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
			item := ContractLedgerItem{Name: "示例合同", AuthorityMode: tc.Authority, ContractState: tc.State, WorkflowState: tc.Workflow}
			row := contractLedgerRowValues(item, columns)
			want := map[string]string{
				"contract_name": "示例合同", "authority_mode_label": tc.AuthorityLabel, "authority_mode_code": tc.Authority,
				"contract_state_label": tc.StateLabel, "contract_state_code": tc.State,
				"workflow_state_label": tc.WorkflowLabel, "workflow_state_code": tc.Workflow,
			}
			if !reflect.DeepEqual(row, want) {
				t.Fatalf("readable row = %#v, want %#v", row, want)
			}
			document, err := RenderContractExportCSV(columns, []map[string]string{row})
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

func TestContractExportReadableCodeUsesFormulaProtection(t *testing.T) {
	columns := []string{"contract_state_label", "contract_state_code"}
	row := contractLedgerRowValues(ContractLedgerItem{ContractState: "=future"}, columns)
	if row["contract_state_code"] != "=future" {
		t.Fatalf("code changed before CSV protection: %q", row["contract_state_code"])
	}
	document, err := RenderContractExportCSV(columns, []map[string]string{row})
	if err != nil || string(document) != "\xef\xbb\xbf合同状态名称,合同状态代码\n待核对,'=future\n" {
		t.Fatalf("readable CSV formula protection = %q, err=%v", document, err)
	}
}

func TestContractExportColumnLimitsDoNotExpandSavedViews(t *testing.T) {
	allColumns := []string{
		"contract_number", "contract_name", "authority_mode", "supplier_name", "amount", "currency", "department_id",
		"project_type", "procurement_method", "contract_state", "workflow_state", "signing_date", "source_system", "created_at", "updated_at",
		"authority_mode_label", "authority_mode_code", "contract_state_label", "contract_state_code", "workflow_state_label", "workflow_state_code",
	}
	if _, err := normalizeContractLedgerColumns([]string{"contract_state_label"}); faultStatus(err) != 422 {
		t.Fatalf("export-only column accepted for a saved view: %v", err)
	}
	if _, err := RenderContractExportCSV(allColumns[:20], nil); err != nil {
		t.Fatalf("twenty distinct export columns should be accepted: %v", err)
	}
	if _, err := RenderContractExportCSV(allColumns, nil); faultStatus(err) != 422 {
		t.Fatalf("twenty-one requested columns should be rejected: %v", err)
	}
}

func TestContractExportFrozenReadableAndLegacyPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	testCLMDataKey(t)
	core := env.openRole(t, "core")
	ctx := context.Background()
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("synthetic admin fixture missing")
	}
	token, err := core.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err = core.AuthenticateSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	draft := DraftContent{Contract: &ContractBasics{Name: "示例合同"}}
	detail, err := core.CreateContractDraft(ctx, admin, CreateContractDraftInput{
		AuthorityMode: "native", Draft: draft, IdempotencyKey: "issue240-draft-create",
	})
	if err != nil {
		t.Fatalf("create authorized synthetic contract: %v", err)
	}
	legacyInput := ContractExportInput{
		Columns: []string{"contract_name", "authority_mode", "contract_state", "workflow_state"},
		Format:  "csv", IdempotencyKey: "issue240-legacy-export",
	}
	legacy, err := core.CreateContractExport(ctx, admin, legacyInput)
	if err != nil {
		t.Fatalf("create legacy authorized export: %v", err)
	}
	readCSV := func(exportID string) []byte {
		t.Helper()
		download, err := core.LoadContractExportForDownload(ctx, admin, exportID)
		if err != nil {
			t.Fatalf("load authorized export: %v", err)
		}
		csv, err := RenderContractExportCSV(download.Columns, download.Rows)
		if err != nil {
			t.Fatalf("render authorized export: %v", err)
		}
		return csv
	}
	readDigests := func(exportID string) [2]string {
		t.Helper()
		tx, _, err := core.beginContractLedgerRead(ctx, admin)
		if err != nil {
			t.Fatal(err)
		}
		defer rollbackCLM(tx)
		var digests [2]string
		if err := tx.QueryRow(ctx, `SELECT request_digest,grants_digest FROM clm_contract_exports WHERE tenant_id=$1 AND export_id=$2`, admin.TenantID, exportID).Scan(&digests[0], &digests[1]); err != nil {
			t.Fatal(err)
		}
		return digests
	}
	legacyBytes := readCSV(legacy.ExportID)
	legacyDigests := readDigests(legacy.ExportID)
	legacyWant := "\xef\xbb\xbf合同名称,权威模式,合同状态,会签状态\n示例合同,native,draft,not_started\n"
	if string(legacyBytes) != legacyWant {
		t.Fatalf("legacy authorized export = %q, want %q", legacyBytes, legacyWant)
	}
	t.Logf("ISSUE240_LEGACY_CSV_HEX=%x", legacyBytes)
	foreignProfile, ok := accounts.ByID("demo-tenant2-admin")
	if !ok {
		t.Fatal("synthetic foreign tenant fixture missing")
	}
	foreignToken, err := core.EstablishSession(foreignProfile, "client")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := core.AuthenticateSession(ctx, foreignToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadContractExportForDownload(ctx, foreign, legacy.ExportID); faultStatus(err) != 404 {
		t.Fatalf("cross-tenant download = %v, want 404", err)
	}
	readableInput := ContractExportInput{
		Columns: []string{"contract_name", "authority_mode_label", "authority_mode_code", "contract_state_label", "contract_state_code", "workflow_state_label", "workflow_state_code"},
		Format:  "csv", IdempotencyKey: "issue240-readable-export",
	}
	readable, err := core.CreateContractExport(ctx, admin, readableInput)
	if err != nil {
		t.Fatalf("create readable authorized export: %v", err)
	}
	if ttl := readable.ExpiresAt.Sub(readable.CreatedAt); ttl < 24*time.Hour-time.Second || ttl > 24*time.Hour+time.Second {
		t.Fatalf("new export lifetime = %v, want 24 hours", ttl)
	}
	readableBytes := readCSV(readable.ExportID)
	readableDigests := readDigests(readable.ExportID)
	readableWant := "\xef\xbb\xbf合同名称,来源类型名称,来源类型代码,合同状态名称,合同状态代码,会签状态名称,会签状态代码\n示例合同,本地登记,native,草稿,draft,当前无进行中会签,not_started\n"
	if string(readableBytes) != readableWant {
		t.Fatalf("readable authorized export = %q, want %q", readableBytes, readableWant)
	}
	t.Logf("ISSUE240_READABLE_CSV_HEX=%x", readableBytes)
	updated := DraftContent{Contract: &ContractBasics{Name: "更新后的合同"}}
	if _, err := core.ReplaceContractDraft(ctx, admin, ReplaceContractDraftInput{
		ContractID: detail.Summary.ContractID, ExpectedRowVersion: detail.Summary.RowVersion,
		Draft: updated, IdempotencyKey: "issue240-draft-update",
	}); err != nil {
		t.Fatalf("update live contract: %v", err)
	}
	originalDraftLabel := contractLedgerStateLabels["draft"]
	contractLedgerStateLabels["draft"] = "日后更新的文案"
	defer func() { contractLedgerStateLabels["draft"] = originalDraftLabel }()
	for _, item := range []struct {
		name string
		id   string
		want []byte
	}{{"legacy", legacy.ExportID, legacyBytes}, {"readable", readable.ExportID, readableBytes}} {
		if got := readCSV(item.id); !bytes.Equal(got, item.want) {
			t.Fatalf("%s frozen bytes changed after live update: got %q, want %q", item.name, got, item.want)
		}
	}
	if readDigests(legacy.ExportID) != legacyDigests || readDigests(readable.ExportID) != readableDigests {
		t.Fatal("frozen request or grant digest changed")
	}
	for _, item := range []struct {
		input ContractExportInput
		want  ContractExport
	}{{legacyInput, legacy}, {readableInput, readable}} {
		replayed, err := core.CreateContractExport(ctx, admin, item.input)
		if err != nil || !replayed.Replayed || replayed.ExportID != item.want.ExportID ||
			replayed.RowCount != item.want.RowCount || !replayed.CreatedAt.Equal(item.want.CreatedAt) ||
			!replayed.ExpiresAt.Equal(item.want.ExpiresAt) || !reflect.DeepEqual(replayed.Columns, item.want.Columns) {
			t.Fatalf("replay changed frozen export: got %+v, want %+v, err=%v", replayed, item.want, err)
		}
	}
	conflict := readableInput
	conflict.Columns = legacyInput.Columns
	if _, err := core.CreateContractExport(ctx, admin, conflict); faultStatus(err) != 409 {
		t.Fatalf("same key with different columns = %v, want 409", err)
	}
}

func TestContractExportCurrentRowPermissionsAndExpiryPostgres(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" || strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	approvalDecisionTestDirectory(t)
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Fatal("isolated service roles unavailable")
	}
	testCLMDataKey(t)
	core := env.openRole(t, "core")
	ctx := context.Background()
	crosswalk := approvalIdentityCrosswalk()
	importApprovalIdentity(t, core, crosswalk)
	profile, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("synthetic enterprise profile missing")
	}
	login, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || !login.User.HasResolvedMembership() {
		t.Fatalf("resolve authorized membership: %v", err)
	}
	actor := login.User
	var ids []string
	for _, sample := range []struct{ name, key string }{{"授权合同甲", "issue240-permission-a"}, {"授权合同乙", "issue240-permission-b"}} {
		detail, err := core.CreateContractDraft(ctx, actor, CreateContractDraftInput{
			AuthorityMode: "native", Draft: DraftContent{Contract: &ContractBasics{Name: sample.name}},
			IdempotencyKey: sample.key,
		})
		if err != nil {
			t.Fatalf("create authorized contract: %v", err)
		}
		ids = append(ids, detail.Summary.ContractID)
	}
	input := ContractExportInput{Columns: []string{"contract_name", "contract_state_label", "contract_state_code"}, Format: "csv", IdempotencyKey: "issue240-two-row-export"}
	export, err := core.CreateContractExport(ctx, actor, input)
	if err != nil || export.RowCount != 2 {
		t.Fatalf("create two-row authorized export: %+v, err=%v", export, err)
	}
	// Keep read rights for both contracts while revoking Export on the second.
	crosswalk.Memberships[0].Grants = []access.Grant{
		{Role: "case_owner", Scope: "case", CaseID: ids[0]},
		{Role: "reviewer", Scope: "case", CaseID: ids[1]},
	}
	importApprovalIdentity(t, core, crosswalk)
	current, _, err := core.AuthenticateSession(ctx, login.SessionToken)
	if err != nil || len(current.Grants) != 2 {
		t.Fatalf("resolve changed membership: %v", err)
	}
	if _, err := core.LoadContractExportForDownload(ctx, current, export.ExportID); faultStatus(err) != 404 {
		t.Fatalf("one row lost export permission: got %v, want 404", err)
	}
	denied := input
	denied.IdempotencyKey = "issue240-denied-new-export"
	if _, err := core.CreateContractExport(ctx, current, denied); faultStatus(err) != 403 {
		t.Fatalf("readable row without export permission: got %v, want 403", err)
	}
	tx, _, err := core.beginContractLedgerRead(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	var partial int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM clm_contract_exports WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, current.TenantID, current.ID, denied.IdempotencyKey).Scan(&partial); err != nil {
		rollbackCLM(tx)
		t.Fatal(err)
	}
	rollbackCLM(tx)
	if partial != 0 {
		t.Fatal("denied export persisted a partial artifact")
	}
	// Restore the membership and exercise the expiry check on an isolated row.
	crosswalk.Memberships[0].Grants = []access.Grant{{Role: "case_owner", Scope: "tenant"}}
	importApprovalIdentity(t, core, crosswalk)
	current, _, err = core.AuthenticateSession(ctx, login.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadContractExportForDownload(ctx, current, export.ExportID); err != nil {
		t.Fatalf("restored export permission: %v", err)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE clm_contract_exports SET created_at=clock_timestamp()-interval '25 hours', expires_at=clock_timestamp()-interval '1 hour' WHERE tenant_id=$1 AND export_id=$2`, current.TenantID, export.ExportID); err != nil {
		t.Fatalf("expire isolated test export: %v", err)
	}
	if _, err := core.LoadContractExportForDownload(ctx, current, export.ExportID); faultStatus(err) != 404 {
		t.Fatalf("expired export download: got %v, want 404", err)
	}
}

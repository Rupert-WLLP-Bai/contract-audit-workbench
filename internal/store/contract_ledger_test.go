// File metadata (manifest mirror):
// Purpose: Exercises the `store` package behavior through TestContractLedgerFilterValidation, TestContractLedgerCursorBindsActorFilterAndTenant, TestContractExportAuthorizedRequiresExportForEveryRow, TestRenderContractExportCSVGuardsFormulaAndUsesBOM; these checks pin observable state, error handling, or wire/database compatibility for this module.
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
	"encoding/base64"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/contract/ledger"
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
	adminAccess := storeContractExportAccess{tx: tx, user: admin}
	if !adminAccess.CanRead(context.Background(), adminRecord) || !adminAccess.CanExport(context.Background(), adminRecord) {
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
	reviewerAccess := storeContractExportAccess{tx: tx, user: reviewer}
	if reviewerAccess.CanRead(context.Background(), reviewerRecord) && reviewerAccess.CanExport(context.Background(), reviewerRecord) {
		t.Fatal("reviewer without case.export was allowed to download")
	}
	if err := ReauthorizeContractExportDownload(accounts.User{ID: "missing", TenantID: accounts.LocalDemoTenant}); faultStatus(err) != 404 {
		t.Fatalf("reauthorize revoked identity = %v; want 404", err)
	}
}

func TestContractSavedViewRulesNormalizeAndDecodeStoredJSONStrictly(t *testing.T) {
	filter := ContractLedgerFilter{Text: " 合同 "}
	name, columns, err := ledger.NormalizeSavedViewInput(" 个人视图 ", &filter, nil)
	if err != nil || name != "个人视图" || filter.Text != "合同" || len(columns) == 0 {
		t.Fatalf("normalize saved view = %q, %+v, %+v, err=%v", name, filter, columns, err)
	}
	if _, _, err := ledger.NormalizeSavedViewInput("坏\x01名称", &filter, nil); faultStatus(err) != 422 {
		t.Fatalf("saved-view control character = %v; want 422", err)
	}
	if _, _, err := ledger.NormalizeSavedViewInput(strings.Repeat("视", 61), &filter, nil); faultStatus(err) != 422 {
		t.Fatalf("saved-view name over 60 code points = %v; want 422", err)
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{"text":"合同","text":"篡改"}`), []byte(`[]`)); faultStatus(err) != 409 {
		t.Fatalf("stored filter duplicate key = %v; want 409", err)
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{"Text":"合同","text":"篡改"}`), []byte(`[]`)); faultStatus(err) != 409 {
		t.Fatalf("stored filter duplicate field alias = %v; want 409", err)
	}
	for _, testCase := range []struct {
		name   string
		filter string
	}{
		{"long s field alias", `{"supplier_name":"A","ſupplier_name":"B"}`},
		{"Kelvin sign field alias", `{"workflow_state":"not_started","worKflow_state":"in_review"}`},
	} {
		t.Run("stored duplicate Unicode "+testCase.name, func(t *testing.T) {
			filter, _, err := ledger.DecodeStoredSavedView([]byte(testCase.filter), []byte(`[]`))
			if faultStatus(err) != 409 {
				t.Fatalf("stored filter duplicate Unicode alias = %v; want 409", err)
			}
			if filter.SupplierName != "" || filter.WorkflowState != "" {
				t.Fatalf("failed stored filter decode returned a partial filter: %+v", filter)
			}
		})
	}
	for _, testCase := range []struct {
		name   string
		filter string
	}{
		{"ASCII case alias", `{"SUPPLIER_NAME":"供应商"}`},
		{"long s alias", `{"ſupplier_name":"供应商"}`},
	} {
		t.Run("stored single field alias "+testCase.name, func(t *testing.T) {
			filter, _, err := ledger.DecodeStoredSavedView([]byte(testCase.filter), []byte(`[]`))
			if err != nil || filter.SupplierName != "供应商" {
				t.Fatalf("stored single field alias = %+v, err=%v; want supplier name", filter, err)
			}
		})
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{"unexpected":true}`), []byte(`[]`)); faultStatus(err) != 409 {
		t.Fatalf("stored filter unknown field = %v; want 409", err)
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{"text":"合同"} {}`), []byte(`[]`)); faultStatus(err) != 409 {
		t.Fatalf("stored filter trailing value = %v; want 409", err)
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{"amount_min":"10"}`), []byte(`[]`)); faultStatus(err) != 409 {
		t.Fatalf("stored invalid filter = %v; want 409", err)
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{}`), []byte(`["contract_state_label"]`)); faultStatus(err) != 409 {
		t.Fatalf("stored export-only column = %v; want 409", err)
	}
	if _, _, err := ledger.DecodeStoredSavedView(
		[]byte(`{"text":"合同"}`), []byte(`[`)); faultStatus(err) != 409 {
		t.Fatalf("stored columns malformed JSON = %v; want 409", err)
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
	if err != nil || export.RowCount != 2 || !export.DownloadAvailable || export.Replayed {
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
	replayed, err := core.CreateContractExport(ctx, current, input)
	if err != nil || !replayed.Replayed || replayed.ExportID != export.ExportID || !replayed.DownloadAvailable {
		t.Fatalf("metadata replay must survive lost export permission: got %+v, err=%v", replayed, err)
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
	expiredReplay, err := core.CreateContractExport(ctx, current, input)
	if err != nil || !expiredReplay.Replayed || expiredReplay.ExportID != export.ExportID || expiredReplay.DownloadAvailable {
		t.Fatalf("expired metadata replay must report download_available=false: got %+v, err=%v", expiredReplay, err)
	}
}

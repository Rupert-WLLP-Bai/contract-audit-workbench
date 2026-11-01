package store

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
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
	contractID := "22222222-2222-4222-8222-222222222222"
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo-admin missing")
	}
	adminRecord := clmContractRecord{
		TenantID: accounts.LocalDemoTenant, ContractID: contractID,
		OwnerOrgID: "demo-org", CreatedBy: "demo-admin",
	}
	if !contractExportAuthorized([]clmContractRecord{adminRecord}, admin) {
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
	if contractExportAuthorized([]clmContractRecord{reviewerRecord}, reviewer) {
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

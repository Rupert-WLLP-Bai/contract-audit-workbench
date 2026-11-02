package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"contractaudit/internal/clmenvelope"
)

const testRestrictedBankAccount = "6222020200001234567"

const testCLMTenant = "local-demo"
const testCLMObject = "test-contract"

func configureCLMTestKeys(t *testing.T, wrapByte, fingerprintByte byte) {
	t.Helper()
	wrap, fingerprint, legacy := make([]byte, 32), make([]byte, 32), make([]byte, 32)
	for i := range wrap {
		wrap[i], fingerprint[i], legacy[i] = wrapByte, fingerprintByte, byte(i)
	}
	var entries []clmenvelope.LocalKeyEntry
	// These are explicit synthetic test tenants, each with its own key scope.
	for _, tenant := range []string{"local-demo", "local-demo-2", "tenant-a", "tenant-b"} {
		for _, purpose := range []string{clmPurpose, clmSourceObservationPurpose} {
			entries = append(entries,
				clmenvelope.LocalKeyEntry{TenantID: tenant, Purpose: purpose, Kind: clmenvelope.WrapKey, Ref: clmenvelope.KeyRef{ID: "test-wrap", Version: 1}, MaterialBase64: base64.StdEncoding.EncodeToString(wrap), Active: true},
				clmenvelope.LocalKeyEntry{TenantID: tenant, Purpose: purpose, Kind: clmenvelope.FingerprintKey, Ref: clmenvelope.KeyRef{ID: "test-fingerprint", Version: uint64(fingerprintByte)}, MaterialBase64: base64.StdEncoding.EncodeToString(fingerprint), Active: true},
				clmenvelope.LocalKeyEntry{TenantID: tenant, Purpose: purpose, Kind: clmenvelope.LegacyDataKey, Ref: clmenvelope.KeyRef{ID: "test-legacy", Version: 1}, MaterialBase64: base64.StdEncoding.EncodeToString(legacy)},
			)
		}
	}
	raw, err := json.Marshal(clmenvelope.LocalKeyConfig{Version: 1, Keys: entries})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(clmEnvelopeKeysEnv, string(raw))
	t.Setenv(clmLegacyRefEnv, "test-legacy:1")
}
func testCLMDataKey(t *testing.T) {
	t.Helper()
	configureCLMTestKeys(t, 0x31, 0x42)
}

func sampleDraftContent() DraftContent {
	return DraftContent{
		Contract: &ContractBasics{Name: "测试设备采购合同", Currency: "CNY", Amount: "100000.00"},
		Supplier: &Supplier{Name: "示例供应商有限公司", BankAccount: testRestrictedBankAccount, PayeeName: "示例供应商有限公司"},
		Items: []DraftItem{{
			ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10", Name: "交换机", Quantity: "10.000000",
			Unit: "台", UnitPrice: "10000.0000", LineAmount: "100000.00",
		}},
	}
}

func TestValidateDraftContentAllowsIncompleteDrafts(t *testing.T) {
	draft := DraftContent{Contract: &ContractBasics{}}
	if err := validateDraftContent(&draft); err != nil {
		t.Fatalf("empty draft must stay valid: %v", err)
	}
	if draft.Contract.Name != "" {
		t.Fatalf("validation must not invent values")
	}
	if err := validateDraftContent(&DraftContent{}); err == nil {
		t.Fatal("draft without contract object must fail")
	}
}

func TestValidateDraftContentRejectsBadFormats(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DraftContent)
	}{
		{"amount", func(d *DraftContent) { d.Contract.Amount = "100" }},
		{"currency", func(d *DraftContent) { d.Contract.Currency = "cny" }},
		{"signing_date", func(d *DraftContent) { d.Contract.SigningDate = "2026/01/02" }},
		{"drafting_party", func(d *DraftContent) { d.Contract.DraftingParty = "agent" }},
		{"name_length", func(d *DraftContent) { d.Contract.Name = strings.Repeat("长", 201) }},
		{"end_before_start", func(d *DraftContent) {
			d.Contract.PerformanceStartDate, d.Contract.PerformanceEndDate = "2026-02-01", "2026-01-01"
		}},
		{"item_uuid", func(d *DraftContent) { d.Items = []DraftItem{{ItemID: "not-a-uuid"}} }},
		{"item_quantity", func(d *DraftContent) {
			d.Items = []DraftItem{{ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c11", Quantity: "10"}}
		}},
		{"item_unit_price", func(d *DraftContent) {
			d.Items = []DraftItem{{ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c12", UnitPrice: "10.00"}}
		}},
		{"payment_sequence", func(d *DraftContent) {
			d.Payments = []DraftPaymentPlan{{Sequence: 0}}
		}},
		{"material_digest", func(d *DraftContent) {
			d.Materials = []MaterialRef{{MaterialID: "legacy_1", MaterialVersionID: "doc_1", SHA256: "XYZ", Purpose: "draft"}}
		}},
		{"material_purpose", func(d *DraftContent) {
			d.Materials = []MaterialRef{{MaterialID: "legacy_1", MaterialVersionID: "doc_1",
				SHA256: strings.Repeat("a", 64), Purpose: "unknown"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := DraftContent{Contract: &ContractBasics{}}
			tc.mutate(&draft)
			if err := validateDraftContent(&draft); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

func TestValidateDraftContentTrimsNameAndNumber(t *testing.T) {
	draft := DraftContent{Contract: &ContractBasics{Name: "  设备采购  ", Number: new("  HT-2026-001  ")}}
	if err := validateDraftContent(&draft); err != nil {
		t.Fatalf("trimmed values must stay valid: %v", err)
	}
	if draft.Contract.Name != "设备采购" || *draft.Contract.Number != "HT-2026-001" {
		t.Fatalf("name/number were not trimmed: %q %q", draft.Contract.Name, *draft.Contract.Number)
	}
}

func TestSealDraftContentRequiresExplicitKey(t *testing.T) {
	t.Setenv("CLM_DATA_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32))) // obsolete global must not act as a fallback
	t.Setenv(clmEnvelopeKeysEnv, "")
	t.Setenv(clmLegacyRefEnv, "")
	if _, _, _, err := sealDraftContentBound(context.Background(), sampleDraftContent(), testCLMTenant, testCLMObject); err == nil {
		t.Fatal("restricted fields without a configured key must fail explicitly")
	}
	plain := DraftContent{Contract: &ContractBasics{Name: "无敏感字段"}}
	if _, _, _, err := sealDraftContentBound(context.Background(), plain, testCLMTenant, testCLMObject); err != nil {
		t.Fatalf("content without restricted fields must not need the key: %v", err)
	}
}

func TestSealDraftContentRoundTrip(t *testing.T) {
	testCLMDataKey(t)
	draft := sampleDraftContent()
	sealed, encoded, digest, err := sealDraftContentBound(context.Background(), draft, testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest %q", digest)
	}
	encodedText := string(encoded)
	if strings.Contains(encodedText, testRestrictedBankAccount) {
		t.Fatal("sealed bytes contain the plaintext bank account")
	}
	if !strings.HasPrefix(sealed.Supplier.BankAccount, clmenvelope.PrefixV2) {
		t.Fatalf("sealed value %q", sealed.Supplier.BankAccount)
	}
	if draft.Supplier.BankAccount != testRestrictedBankAccount {
		t.Fatal("sealing must not mutate the input draft")
	}
	restored, err := unsealStoredDraftBound(context.Background(), encoded, testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Supplier.BankAccount != testRestrictedBankAccount || restored.Supplier.PayeeName != draft.Supplier.PayeeName {
		t.Fatalf("round trip lost values: %+v", restored.Supplier)
	}
	if restored.Contract.Name != draft.Contract.Name {
		t.Fatalf("unrestricted fields changed: %+v", restored.Contract)
	}
}

func TestSealRejectsPersistedPlaintextAndWrongKey(t *testing.T) {
	testCLMDataKey(t)
	sealed, _, _, err := sealDraftContentBound(context.Background(), sampleDraftContent(), testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	tampered := sealed
	tampered.Supplier = copySupplier(sealed.Supplier)
	tampered.Supplier.BankAccount = testRestrictedBankAccount
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsealStoredDraftBound(context.Background(), raw, testCLMTenant, testCLMObject); err == nil {
		t.Fatal("persisted plaintext in a restricted field must be rejected")
	}

	configureCLMTestKeys(t, 0x7f, 0x42)
	if _, err := unsealStoredDraftBound(context.Background(), mustJSON(t, sealed), testCLMTenant, testCLMObject); err == nil {
		t.Fatal("a different configured key must not decrypt stored values")
	}
}

func TestProjectDraftForViewerMasksRestrictedFields(t *testing.T) {
	testCLMDataKey(t)
	sealed, _, _, err := sealDraftContentBound(context.Background(), sampleDraftContent(), testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := unsealStoredDraftBound(context.Background(), mustJSON(t, sealed), testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	masked := projectDraftForViewer(plaintext, false)
	if masked.Supplier.BankAccount != "••••••••" || masked.Supplier.PayeeName != "•••" {
		t.Fatalf("masked projection %+v", masked.Supplier)
	}
	if masked.Supplier.BankName != "" || masked.Supplier.TaxIdentifier != "" {
		t.Fatal("masking must not invent values for absent restricted fields")
	}
	if masked.Supplier.Name != plaintext.Supplier.Name || masked.Contract.Name != plaintext.Contract.Name {
		t.Fatal("masking must not touch unrestricted fields")
	}
	revealed := projectDraftForViewer(plaintext, true)
	if revealed.Supplier.BankAccount != testRestrictedBankAccount {
		t.Fatalf("revealed projection %q", revealed.Supplier.BankAccount)
	}

	empty := DraftContent{Contract: &ContractBasics{Name: "无账户"}, Supplier: &Supplier{Name: "供应商"}}
	maskedEmpty := projectDraftForViewer(empty, false)
	if maskedEmpty.Supplier.BankAccount != "" {
		t.Fatal("absent restricted fields must stay absent")
	}
}

func TestValidateCLMIdempotencyKeyBounds(t *testing.T) {
	if err := validateCLMIdempotencyKey(strings.Repeat("k", 7)); err == nil {
		t.Fatal("short key must fail")
	}
	if err := validateCLMIdempotencyKey(strings.Repeat("k", 129)); err == nil {
		t.Fatal("long key must fail")
	}
	if err := validateCLMIdempotencyKey("has space"); err == nil {
		t.Fatal("non visible ASCII must fail")
	}
	if err := validateCLMIdempotencyKey(strings.Repeat("k", 8)); err != nil {
		t.Fatalf("lower bound key failed: %v", err)
	}
}

func TestLegacyAmountPairStaysNullTogether(t *testing.T) {
	if amount, currency := legacyAmountPair(DraftContent{Contract: &ContractBasics{Amount: "1.00"}}); amount != nil || currency != nil {
		t.Fatal("amount without currency must stay unknown")
	}
	if amount, currency := legacyAmountPair(DraftContent{Contract: &ContractBasics{Currency: "CNY"}}); amount != nil || currency != nil {
		t.Fatal("currency without amount must stay unknown")
	}
	amount, currency := legacyAmountPair(DraftContent{Contract: &ContractBasics{Amount: "12.00", Currency: "CNY"}})
	if amount == nil || currency == nil || *amount != "12.00" || *currency != "CNY" {
		t.Fatalf("known pair lost: %v %v", amount, currency)
	}
}

func TestContractUUIDHelper(t *testing.T) {
	id, err := newContractUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !validContractUUID(id) {
		t.Fatalf("generated id %q is not a UUID", id)
	}
	if validContractUUID("not-a-uuid") {
		t.Fatal("invalid id accepted")
	}
	normalized, err := normalizeContractUUID(strings.ToUpper(id))
	if err != nil || normalized != strings.ToLower(id) {
		t.Fatalf("normalize failed: %q %v", normalized, err)
	}
	if _, err := normalizeContractUUID("../escaped"); err == nil {
		t.Fatal("non UUID path values must be rejected")
	}
}

func TestContractDraftCommandsFollowManageAuthority(t *testing.T) {
	record := clmContractRecord{AuthorityMode: "native", LifecycleState: "draft", ValidityState: "active"}
	if got := clmDraftCommands(record, true); len(got) != 2 || got[0] != "replace_draft" || got[1] != clmCommandFreezeVersion {
		t.Fatalf("manager commands = %v", got)
	}
	if got := clmDraftCommands(record, false); len(got) != 0 {
		t.Fatalf("viewer must not receive a write command: %v", got)
	}
	closed := record
	closed.LifecycleState = "registered"
	if got := clmDraftCommands(closed, true); len(got) != 0 {
		t.Fatalf("registered contract commands = %v", got)
	}
	external := record
	external.AuthorityMode = "external"
	if got := clmDraftCommands(external, true); len(got) != 0 {
		t.Fatalf("external mirror commands = %v", got)
	}
}

func TestDraftSummaryFactsIgnoreRestrictedFields(t *testing.T) {
	testCLMDataKey(t)
	_, encoded, _, err := sealDraftContentBound(context.Background(), sampleDraftContent(), testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	name, amount, currency, err := draftSummaryFacts(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if name != "测试设备采购合同" || amount == nil || *amount != "100000.00" || currency == nil || *currency != "CNY" {
		t.Fatalf("facts %q %v %v", name, amount, currency)
	}
	if _, _, _, err := draftSummaryFacts([]byte(`{}`)); err == nil {
		t.Fatal("content without a contract object must fail")
	}
}

// TestAmountDomainFieldsSurviveSealing pins lossless preservation of the
// amount-domain declarations (settlement mode, tax basis, ratio base,
// funding year/cost center, per-installment ratio, currency) through the
// sealed storage representation; freeze-time completeness stays a later
// command's job.
func TestAmountDomainFieldsSurviveSealing(t *testing.T) {
	testCLMDataKey(t)
	draft := DraftContent{
		Contract: &ContractBasics{
			Name: "比例结算框架合同", Currency: "CNY", Amount: "88800.00",
			SettlementMode:   "fixed_total_price",
			PricesIncludeTax: new(true),
		},
		Supplier: &Supplier{Name: "示例供应商", BankAccount: testRestrictedBankAccount},
		Payments: []DraftPaymentPlan{{
			Sequence: 1, IsAdvance: new(false), Amount: "44400.00",
			Ratio: "0.500000", RatioBase: "contract_amount_including_tax",
		}},
		Funding: []DraftFunding{{
			Nature: "自筹", Channel: "unit", Amount: "88800.00",
			FundingYear: new(2026), CostCenter: "CC-NET-01",
		}},
	}
	if err := validateDraftContent(&draft); err != nil {
		t.Fatalf("amount-domain draft must stay valid: %v", err)
	}
	sealed, encoded, _, err := sealDraftContentBound(context.Background(), draft, testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := unsealStoredDraftBound(context.Background(), encoded, testCLMTenant, testCLMObject)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Contract.SettlementMode != "fixed_total_price" ||
		restored.Contract.SettlementMode != "fixed_total_price" {
		t.Fatal("settlement_mode lost")
	}
	if sealed.Contract.PricesIncludeTax == nil || !*sealed.Contract.PricesIncludeTax ||
		restored.Contract.PricesIncludeTax == nil || !*restored.Contract.PricesIncludeTax {
		t.Fatal("prices_include_tax lost")
	}
	if len(restored.Payments) != 1 || restored.Payments[0].Ratio != "0.500000" ||
		restored.Payments[0].RatioBase != "contract_amount_including_tax" ||
		restored.Payments[0].Amount != "44400.00" {
		t.Fatalf("payment ratio facts lost: %+v", restored.Payments)
	}
	if len(restored.Funding) != 1 || restored.Funding[0].FundingYear == nil ||
		*restored.Funding[0].FundingYear != 2026 || restored.Funding[0].CostCenter != "CC-NET-01" {
		t.Fatalf("funding facts lost: %+v", restored.Funding)
	}
	if restored.Contract.Currency != "CNY" || restored.Contract.Amount != "88800.00" {
		t.Fatal("currency/amount lost")
	}
	if restored.Supplier.BankAccount != testRestrictedBankAccount {
		t.Fatal("restricted field round trip broken")
	}
	// Invalid declarations are still rejected at draft time.
	badYear := draft
	badYear.Funding[0].FundingYear = new(0)
	if err := validateDraftContent(&badYear); err == nil {
		t.Fatal("funding year 0 must be rejected")
	}
}

func TestUnsealDraftLegacyVectorRequiresExplicitRef(t *testing.T) {
	testCLMDataKey(t)
	const frozen = "clmv1:AAECAwQFBgcICQoLK2exeqac4mnoMuP52IoMCOf24V2VFzvJaFLfM8C61I8hoseQ1RmJ"
	got, err := unsealCLMValue(context.Background(), testCLMTenant, testCLMObject, "supplier.bank_account", frozen)
	if err != nil || got != "legacy restricted field" {
		t.Fatalf("legacy vector mismatch: %q %v", got, err)
	}
	t.Setenv(clmLegacyRefEnv, "")
	if _, err := unsealCLMValue(context.Background(), testCLMTenant, testCLMObject, "supplier.bank_account", frozen); err == nil {
		t.Fatal("legacy value opened without an explicit historical reference")
	}
}

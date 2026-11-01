package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

func freezeCompleteDraft() DraftContent {
	return DraftContent{
		Contract: &ContractBasics{
			Name: "版本冻结测试合同", Currency: "CNY", Amount: "100000.00",
			SettlementMode: "fixed_total", PricesIncludeTax: new(true),
		},
		Supplier: &Supplier{Name: "示例供应商有限公司", BankAccount: "6222000000000001", PayeeName: "示例供应商有限公司"},
		Items: []DraftItem{{
			ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10", Name: "交换机", Quantity: "10.000000",
			Unit: "台", UnitPrice: "10000.0000", LineAmount: "100000.00",
		}},
		Funding: []DraftFunding{{Nature: "self", Channel: "unit", Amount: "100000.00"}},
		Payments: []DraftPaymentPlan{{
			Sequence: 1, Amount: "100000.00", AdvanceAmount: "0.00",
			UnitAmount: "100000.00", FiscalAmount: "0.00",
		}},
	}
}

// The freeze command must not repeat the legacy registration gate. An
// incomplete snapshot has no known amount, yet StageFinal still rejects it, so
// calling the freeze path with the legacy known-amount test in front would have
// let this draft through. This test pins the unconditional call the freeze
// command uses.
func TestFreezeStageFinalIsUnconditional(t *testing.T) {
	incomplete := DraftContent{Contract: &ContractBasics{Name: "金额未知的草稿"}}
	if legacyAmountsKnown(&incomplete) {
		t.Fatal("test setup: incomplete snapshot must not count as a known amount")
	}
	err := validateDraftAmounts(&incomplete, contractmoney.StageFinal)
	if err == nil {
		t.Fatal("StageFinal must reject an amount-unknown snapshot even when legacyAmountsKnown is false")
	}
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != 422 {
		t.Fatalf("expected 422 invalid, got %v", err)
	}
	complete := freezeCompleteDraft()
	if err := validateDraftAmounts(&complete, contractmoney.StageFinal); err != nil {
		t.Fatalf("complete draft must pass StageFinal: %v", err)
	}
}

// The freeze transaction writes server-resolved pins into the snapshot. Their
// captured_at changes on every resolution, so flattening them would report a
// field difference (and change the content fingerprint) for two versions whose
// business content is identical.
func TestFlattenContractDraftOmitsServerPins(t *testing.T) {
	draft := freezeCompleteDraft()
	draft.Contract.RelatedParties = []PartyRef{{
		PartyID: "subject-1", Role: "supplier", NameSnapshot: "示例供应商有限公司",
		ReferenceSnapshot: &PartyReferenceSnapshot{
			BindingID: "binding-1", BindingVersion: 3, CaseRole: "supplier",
			BindingState: "active", SubjectID: "subject-1", SubjectVersion: 2,
			SubjectPayloadHash: "hash", VerificationState: "verified",
			DisplayName: "示例供应商有限公司", CapturedAt: time.Now().UTC(),
		},
	}}
	draft.Contract.ProcurementResultPackage = &ProcurementRef{
		System: "purchase", ResultID: "result-1", PackageID: "package-1", SnapshotRef: "doc-1",
		SourceSnapshot: &ProcurementSourceSnapshot{
			System: "purchase", ResultID: "result-1", PackageID: "package-1",
			SnapshotRef: "doc-1", MaterialID: "material-1", MaterialVersionID: "doc-1",
			MaterialVersion: 1, MaterialRole: "procurement", SHA256: strings.Repeat("a", 64),
			CapturedAt: time.Now().UTC(),
		},
	}
	fields := flattenContractDraft(draft)
	for path := range fields {
		if strings.Contains(path, "reference_snapshot") || strings.Contains(path, "source_snapshot") {
			t.Fatalf("server pin leaked into business fields: %s", path)
		}
	}
	first := flattenContractDraft(draft)
	draft.Contract.RelatedParties[0].ReferenceSnapshot.CapturedAt = time.Now().Add(time.Hour).UTC()
	draft.Contract.ProcurementResultPackage.SourceSnapshot.CapturedAt = time.Now().Add(time.Hour).UTC()
	second := flattenContractDraft(draft)
	if len(first) != len(second) {
		t.Fatalf("pin timestamp changed the field set: %d vs %d", len(first), len(second))
	}
	for path, value := range first {
		if second[path] != value {
			t.Fatalf("pin timestamp changed business field %s", path)
		}
	}
}

func TestFrozenMaterialsRejectTwoVersionsOfOneMaterial(t *testing.T) {
	anchor := Case{ID: "case-1", OwnerOrgID: "org-1"}
	refs := []ContractMaterialReference{
		{MaterialID: "material-1", MaterialVersionID: "doc-1", MaterialVersion: 1},
		{MaterialID: "material-1", MaterialVersionID: "doc-2", MaterialVersion: 2},
	}
	if _, err := frozenMaterialsFromReferences(anchor, refs); err == nil {
		t.Fatal("two versions of one material must be rejected before the primary key does")
	}
	materials, err := frozenMaterialsFromReferences(anchor, refs[:1])
	if err != nil || len(materials) != 1 || materials[0].CaseID != anchor.ID || materials[0].CaseOrgID != anchor.OwnerOrgID {
		t.Fatalf("unexpected projection: %+v err=%v", materials, err)
	}
}

func TestFlattenContractDraftUsesStableKeysAndOmitsRestricted(t *testing.T) {
	fields := flattenContractDraft(freezeCompleteDraft())
	for _, path := range []string{
		"contract.amount",
		"items[item_id=c6052c81-8b05-49ce-89e9-f6e9c6403c10].line_amount",
		"funding[ordinal=0].amount",
		"payments[sequence=1].amount",
	} {
		if _, ok := fields[path]; !ok {
			t.Fatalf("missing canonical path %q in %v", path, sortedFieldPaths(fields))
		}
	}
	for path := range fields {
		if strings.Contains(path, "bank_account") || strings.Contains(path, "payee_name") {
			t.Fatalf("restricted path leaked into public field map: %s", path)
		}
	}
	if _, ok := fields["supplier.name"]; !ok {
		t.Fatal("unrestricted supplier name must remain comparable")
	}
}

func TestDiffFrozenFieldsLocatesAmountTerms(t *testing.T) {
	before := flattenContractDraft(freezeCompleteDraft())
	afterDraft := freezeCompleteDraft()
	afterDraft.Contract.Amount = "105000.00"
	afterDraft.Items[0].LineAmount = "105000.00"
	afterDraft.Payments[0].Amount = "105000.00"
	afterDraft.Payments[0].UnitAmount = "105000.00"
	afterDraft.Funding[0].Amount = "105000.00"
	after := flattenContractDraft(afterDraft)
	changes := diffFrozenFields(before, after)
	byPath := make(map[string]ContractVersionFieldChange, len(changes))
	for _, change := range changes {
		byPath[change.Path] = change
	}
	amount, ok := byPath["contract.amount"]
	if !ok || amount.Category != "amount" || amount.Change != "changed" ||
		amount.Before != "100000.00" || amount.After != "105000.00" {
		t.Fatalf("contract.amount change not located: %+v", amount)
	}
	if amount.Classification != "confidential" {
		t.Fatalf("contract.amount classification = %q", amount.Classification)
	}
	item, ok := byPath["items[item_id=c6052c81-8b05-49ce-89e9-f6e9c6403c10].line_amount"]
	if !ok || item.Category != "amount" {
		t.Fatalf("item line amount change not located: %+v", item)
	}
	payment, ok := byPath["payments[sequence=1].amount"]
	if !ok || payment.Category != "amount" {
		t.Fatalf("payment amount change not located: %+v", payment)
	}
	for _, change := range changes {
		if fieldCategoryIsAmountTerm(change.Category) {
			continue
		}
		t.Fatalf("unexpected non-amount term in this fixture: %+v", change)
	}
}

func TestComputeRestrictedFingerprintsIsStableAndKeyBound(t *testing.T) {
	testCLMDataKey(t)
	draft := freezeCompleteDraft()
	first, err := computeRestrictedFingerprints(&draft)
	if err != nil {
		t.Fatal(err)
	}
	second, err := computeRestrictedFingerprints(&draft)
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyID == "" {
		t.Fatal("present restricted fields must record a key id")
	}
	if first.Fields["supplier.bank_account"] != second.Fields["supplier.bank_account"] {
		t.Fatal("same plaintext under the same key must fingerprint identically")
	}
	changed := freezeCompleteDraft()
	changed.Supplier.BankAccount = "6222000000000002"
	third, err := computeRestrictedFingerprints(&changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.Fields["supplier.bank_account"].Digest == first.Fields["supplier.bank_account"].Digest {
		t.Fatal("different plaintext must fingerprint differently")
	}

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(200 - i)
	}
	t.Setenv("CLM_DATA_KEY", base64KeyForTest(raw))
	otherKey, err := computeRestrictedFingerprints(&draft)
	if err != nil {
		t.Fatal(err)
	}
	if otherKey.KeyID == first.KeyID {
		t.Fatal("rotating CLM_DATA_KEY must change the fingerprint key id")
	}
	if otherKey.Fields["supplier.bank_account"].Digest == first.Fields["supplier.bank_account"].Digest {
		t.Fatal("rotating CLM_DATA_KEY must change the deterministic fingerprint")
	}
}

func TestComputeRestrictedFingerprintsWithoutValuesNeedsNoKey(t *testing.T) {
	t.Setenv("CLM_DATA_KEY", "")
	draft := DraftContent{Contract: &ContractBasics{Name: "无受限字段"}}
	set, err := computeRestrictedFingerprints(&draft)
	if err != nil {
		t.Fatal(err)
	}
	if set.KeyID != "" {
		t.Fatalf("absent restricted fields must not invent a key id: %q", set.KeyID)
	}
	for _, path := range clmRestrictedFieldPaths {
		if set.Fields[path].Present {
			t.Fatalf("%s must be recorded absent", path)
		}
	}
}

func TestDiffRestrictedFieldsReportsOnlyChanged(t *testing.T) {
	testCLMDataKey(t)
	draft := freezeCompleteDraft()
	first, err := computeRestrictedFingerprints(&draft)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	changedDraft := freezeCompleteDraft()
	changedDraft.Supplier.BankAccount = "6222000000000002"
	second, err = computeRestrictedFingerprints(&changedDraft)
	if err != nil {
		t.Fatal(err)
	}
	before := &contractVersionSeal{RestrictedFingerprints: first}
	after := &contractVersionSeal{RestrictedFingerprints: second}
	changes := diffRestrictedFields(true, before, after)
	if len(changes) != len(clmRestrictedFieldPaths) {
		t.Fatalf("restricted field count %d", len(changes))
	}
	for _, change := range changes {
		if change.Changed == nil {
			t.Fatalf("%s unexpectedly unknown", change.Path)
		}
		want := change.Path == "supplier.bank_account"
		if *change.Changed != want {
			t.Fatalf("%s changed=%v want %v", change.Path, *change.Changed, want)
		}
	}
	payload, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"6222000000000001", "6222000000000002", "•••"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("restricted diff leaked %q", forbidden)
		}
	}

	unknown := diffRestrictedFields(true, nil, after)
	for _, change := range unknown {
		if change.Changed != nil || change.Reason != "fingerprint_unavailable" {
			t.Fatalf("missing legacy seal must be unknown: %+v", change)
		}
	}
	legacyFirst := diffRestrictedFields(false, nil, nil)
	for _, change := range legacyFirst {
		if change.Changed != nil || change.Reason != "fingerprint_unavailable" {
			t.Fatalf("seal-less first revision must be unknown: %+v", change)
		}
	}
}

func TestParseRestrictedFingerprintsRejectsMalformedSeal(t *testing.T) {
	testCLMDataKey(t)
	draft := freezeCompleteDraft()
	set, err := computeRestrictedFingerprints(&draft)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRestrictedFingerprints(raw); err != nil {
		t.Fatalf("freshly computed seal must parse: %v", err)
	}
	var malformed map[string]any
	if err := json.Unmarshal(raw, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed["algorithm"] = "plain-sha256"
	broken, err := json.Marshal(malformed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRestrictedFingerprints(broken); err == nil {
		t.Fatal("unsupported algorithm must fail closed")
	}
}

func sortedFieldPaths(fields map[string]any) []string {
	paths := make([]string, 0, len(fields))
	for path := range fields {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func base64KeyForTest(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

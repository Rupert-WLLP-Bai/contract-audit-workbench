package store

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeDraftReferencesStripsServerManagedSnapshots(t *testing.T) {
	captured := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	draft := DraftContent{Contract: &ContractBasics{
		RelatedParties: []PartyRef{{
			PartyID: "subj_demo", Role: "buyer", NameSnapshot: "示例采购方", BindingID: "bind_demo",
			ReferenceSnapshot: &PartyReferenceSnapshot{BindingID: "bind_demo", BindingVersion: 1,
				CaseRole: "customer", BindingState: "active", SubjectID: "subj_demo", SubjectVersion: 1,
				SubjectPayloadHash: strings.Repeat("a", 64), VerificationState: "verified",
				DisplayName: "示例采购方", CapturedAt: captured},
		}},
		ProcurementResultPackage: &ProcurementRef{
			System: "erp", ResultID: "R-1", PackageID: "PKG-1", SnapshotRef: "doc_award",
			SourceSnapshot: &ProcurementSourceSnapshot{System: "erp", ResultID: "R-1", PackageID: "PKG-1",
				SnapshotRef: "doc_award", MaterialID: "mat_award", MaterialVersionID: "doc_award",
				MaterialVersion: 1, MaterialRole: "award", SHA256: strings.Repeat("b", 64), CapturedAt: captured},
		},
	}}
	sanitizeDraftReferences(&draft)
	party := draft.Contract.RelatedParties[0]
	if party.ReferenceSnapshot != nil {
		t.Fatal("client-supplied subject pin must be removed")
	}
	if party.BindingID != "bind_demo" {
		t.Fatalf("client binding hint must be preserved: %q", party.BindingID)
	}
	if draft.Contract.ProcurementResultPackage == nil || draft.Contract.ProcurementResultPackage.SourceSnapshot != nil {
		t.Fatal("client-supplied procurement source snapshot must be removed")
	}
}

func TestCaseRoleAllowsContractRole(t *testing.T) {
	cases := []struct {
		contractRole, caseRole string
		want                   bool
	}{
		{"buyer", SubjectRoleCustomer, true},
		{"buyer", SubjectRoleSupplier, false},
		{"supplier", SubjectRoleSupplier, true},
		{"supplier", SubjectRoleCustomer, false},
		{"guarantor", SubjectRoleContractor, true},
		{"guarantor", SubjectRoleOther, true},
		{"guarantor", SubjectRoleCustomer, false},
		{"consortium_member", SubjectRoleServiceProvider, true},
		{"other", SubjectRoleCustomer, true},
		{"other", "unknown", false},
		{"unknown", SubjectRoleCustomer, false},
	}
	for _, tc := range cases {
		if got := caseRoleAllowsContractRole(tc.contractRole, tc.caseRole); got != tc.want {
			t.Fatalf("caseRoleAllowsContractRole(%q,%q)=%v want %v", tc.contractRole, tc.caseRole, got, tc.want)
		}
	}
}

func TestProcurementMaterialRole(t *testing.T) {
	for _, role := range []string{"procurement", "award"} {
		if !procurementMaterialRole(role) {
			t.Fatalf("%s must be a procurement source role", role)
		}
	}
	for _, role := range []string{"attachment", "draft_contract", ""} {
		if procurementMaterialRole(role) {
			t.Fatalf("%s must not be a procurement source role", role)
		}
	}
}

func TestContractOriginalPathReusesDocumentEndpoint(t *testing.T) {
	if got := contractOriginalPath("case-1", "doc-1"); got != "/api/cases/case-1/documents/doc-1/original" {
		t.Fatalf("original path %q", got)
	}
	if got := contractOriginalPath("case/1", "doc 1"); strings.Contains(got, "case/1") || strings.Contains(got, "doc 1") {
		t.Fatalf("path segments must be escaped: %q", got)
	}
}

func TestValidateDraftContentChecksBindingHint(t *testing.T) {
	draft := DraftContent{Contract: &ContractBasics{RelatedParties: []PartyRef{{
		PartyID: "subj_demo", Role: "buyer", NameSnapshot: "采购方", BindingID: "bind_demo",
	}}}}
	if err := validateDraftContent(&draft); err != nil {
		t.Fatalf("valid binding hint must pass: %v", err)
	}
	draft.Contract.RelatedParties[0].BindingID = strings.Repeat("x", 101)
	if err := validateDraftContent(&draft); err == nil {
		t.Fatal("overlong binding id must be rejected")
	}
}

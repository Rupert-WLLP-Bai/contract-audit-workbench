package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

func TestSubjectWireContractFixtures(t *testing.T) {
	createdAt := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	principal := TrustedPrincipalProjection{UserID: "user-1", Issuer: "issuer.example", Subject: "principal-1"}
	noClaims := SubjectVersionProjection{
		SubjectID: "subject-legacy-1", Version: 1, DisplayName: "旧档案客户", LegalName: nil,
		VerificationState: "pending_verification", SourceKind: "user_asserted", BasisSummary: "legacy label adoption",
		PayloadHash:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IdentityClaims: []SubjectIdentityClaimProjection{}, Aliases: []SubjectAlias{}, CreatedBy: principal, CreatedAt: createdAt,
	}
	maskedClaims := SubjectVersionProjection{
		SubjectID: "subject-2", Version: 2, DisplayName: "示例企业甲", LegalName: nil,
		VerificationState: "pending_verification", SourceKind: "user_asserted", BasisSummary: "user supplied",
		PayloadHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IdentityClaims: []SubjectIdentityClaimProjection{{
			Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code",
			MaskedValue: "9135••••••••••0Y43", VerificationState: "pending_verification",
			SourceKind: "user_asserted", SourceTime: nil,
			PayloadHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		}}, Aliases: []SubjectAlias{}, CreatedBy: principal, CreatedAt: createdAt,
	}
	legacyLabel := "旧档案客户"
	legacyBinding := CaseSubjectListProjection{
		CanManage: true,
		Items: []CaseSubjectBindingProjection{{
			BindingID: "binding-legacy-1", BindingVersion: 1, CaseID: "case-1", Role: SubjectRoleCustomer,
			BindingState: "pending_confirmation", SourceKind: "legacy_label", LegacyLabel: &legacyLabel,
			Subject: noClaims, CreatedBy: principal, CreatedAt: createdAt,
		}},
		LegacyProfile: &LegacySubjectProfileProjection{Version: 1, Customer: &legacyLabel, Supplier: nil},
	}
	maskedBinding := CaseSubjectListProjection{
		CanManage: false,
		Items: []CaseSubjectBindingProjection{{
			BindingID: "binding-2", BindingVersion: 1, CaseID: "case-2", Role: SubjectRoleSupplier,
			BindingState: "active", SourceKind: "user_asserted", LegacyLabel: nil,
			Subject: maskedClaims, CreatedBy: principal, CreatedAt: createdAt,
		}}, LegacyProfile: nil,
	}
	caseLink := SubjectCaseLinksResult{Items: []SubjectCaseLinkProjection{{
		BindingID: "binding-2", BindingVersion: 1, CaseID: "case-2", CaseTitle: "有权案件", BusinessStatus: "active",
		Role: SubjectRoleSupplier, BindingState: "active", SourceKind: "user_asserted", LegacyLabel: nil,
		CreatedBy: principal, Subject: maskedClaims, CreatedAt: createdAt,
	}}, VisibleCount: 1}
	fixtures := map[string]any{
		"subject-version-no-claims.json": noClaims,
		"case-subjects-legacy.json":      legacyBinding,
		"case-subjects-masked.json":      maskedBinding,
		"subject-case-links.json":        caseLink,
	}
	for name, value := range fixtures {
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, '\n')
		path := filepath.Join("../../testdata/contracts/subjects", name)
		if os.Getenv("UPDATE_SUBJECT_CONTRACT_FIXTURES") == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		actual, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(actual) != string(encoded) {
			t.Errorf("fixture %s differs from Go wire projection; regenerate only after an approved contract change", name)
		}
	}
}

func TestProjectSubjectVersionSerializesEmptyCollectionsAsArrays(t *testing.T) {
	projected := projectSubjectVersion(SubjectVersion{
		TenantID: "local-demo", SubjectID: "subject-1", Version: 1,
		DisplayName: "示例企业", VerificationState: "pending_verification",
		SourceKind: "user_asserted", BasisSummary: "user supplied",
		PayloadHash:   "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedByUser: "demo-admin", CreatedAt: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC),
	}, false)
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, key := range []string{`"identity_claims":[]`, `"aliases":[]`} {
		if !strings.Contains(text, key) {
			t.Errorf("projection JSON lacks %s: %s", key, text)
		}
	}
	if strings.Contains(text, `"aliases":null`) || strings.Contains(text, `"identity_claims":null`) {
		t.Errorf("projection serialized empty collections as null: %s", text)
	}
}

func TestCaseSubjectCanManageProjectionRequiresManageWritableAndNotReadOnly(t *testing.T) {
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("demo viewer missing")
	}
	item := Case{ID: "case-1", TenantID: admin.TenantID, OwnerOrgID: admin.OrgID, OwnerID: admin.ID, MemberIDs: []string{admin.ID, viewer.ID}, BusinessStatus: "active"}
	if !caseSubjectCanManage(item, admin, false) {
		t.Fatal("case manager should be allowed to manage a writable case")
	}
	if caseSubjectCanManage(item, viewer, false) {
		t.Fatal("read-only viewer must not be allowed to manage case subjects")
	}
	if caseSubjectCanManage(item, admin, true) {
		t.Fatal("maintenance read-only mode must disable case subject mutations")
	}
	item.BusinessStatus = "archived"
	if caseSubjectCanManage(item, admin, false) {
		t.Fatal("archived case must not advertise subject management")
	}
}

func TestSubjectStateClonePreservesInternalRecords(t *testing.T) {
	createdAt := time.Date(2026, 10, 31, 12, 34, 56, 0, time.UTC)
	st := state{
		Subjects:                []Subject{{TenantID: "tenant-1", ID: "subject-1", CurrentVersion: 1, Status: "pending_verification", CreatedAt: createdAt}},
		SubjectVersions:         []SubjectVersion{{TenantID: "tenant-1", SubjectID: "subject-1", Version: 1, DisplayName: "名称", CreatedAt: createdAt}},
		CaseSubjectRoles:        []CaseSubjectRole{{TenantID: "tenant-1", OrgID: "org-1", CaseID: "case-1", BindingID: "binding-1", CurrentVersion: 1, CreatedAt: createdAt}},
		CaseSubjectRoleVersions: []CaseSubjectRoleVersion{{TenantID: "tenant-1", OrgID: "org-1", CaseID: "case-1", BindingID: "binding-1", Version: 1, SubjectID: "subject-1", SubjectVersion: 1, Role: SubjectRoleCustomer, BindingState: "active", CreatedAt: createdAt}},
		SubjectIdempotency:      []SubjectIdempotencyRecord{{TenantID: "tenant-1", ActorUserID: "user-1", Operation: "create", TargetID: "subject-1", Key: "idem-1", PayloadHash: "hash", Response: json.RawMessage(`{"subject_id":"subject-1"}`), HTTPStatus: 201, CreatedAt: createdAt}},
	}
	clone, err := st.clone()
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.Subjects) != 1 || !clone.Subjects[0].CreatedAt.Equal(createdAt) || len(clone.SubjectVersions) != 1 || !clone.SubjectVersions[0].CreatedAt.Equal(createdAt) || len(clone.CaseSubjectRoles) != 1 || !clone.CaseSubjectRoles[0].CreatedAt.Equal(createdAt) || len(clone.CaseSubjectRoleVersions) != 1 || !clone.CaseSubjectRoleVersions[0].CreatedAt.Equal(createdAt) || len(clone.SubjectIdempotency) != 1 || !clone.SubjectIdempotency[0].CreatedAt.Equal(createdAt) || string(clone.SubjectIdempotency[0].Response) != string(st.SubjectIdempotency[0].Response) {
		t.Fatalf("clone lost internal subject records or timestamps: %+v", clone)
	}
}

func TestPartyContextFreezesCurrentBindingsAndSyntheticProvenance(t *testing.T) {
	st := state{
		Subjects: []Subject{{ID: "subject-a", CurrentVersion: 2}},
		SubjectVersions: []SubjectVersion{{
			SubjectID: "subject-a", Version: 2, DisplayName: "演示企业", VerificationState: "verified", VerificationScope: "synthetic_controlled",
			IdentityClaims: []SubjectIdentityClaim{{
				VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceCatalogID: "synthetic.uscc.demo",
				SourceSnapshotID: "snapshot-1", SourceDigest: "digest-1", SourceValidFrom: timePtr(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
			}},
		}, {
			SubjectID: "subject-a", Version: 1, DisplayName: "旧版本演示企业", VerificationState: "verified", VerificationScope: "synthetic_controlled",
			IdentityClaims: []SubjectIdentityClaim{{VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceCatalogID: "synthetic.uscc.demo",
				SourceSnapshotID: "snapshot-1", SourceDigest: "digest-1", SourceValidFrom: timePtr(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))}},
		}},
		CaseSubjectRoles: []CaseSubjectRole{
			{CaseID: "case-1", BindingID: "b-active", CurrentVersion: 1},
			{CaseID: "case-1", BindingID: "b-pending", CurrentVersion: 1},
			{CaseID: "case-1", BindingID: "b-inactive", CurrentVersion: 1},
			{CaseID: "case-2", BindingID: "b-other-case", CurrentVersion: 1},
		},
		CaseSubjectRoleVersions: []CaseSubjectRoleVersion{
			{CaseID: "case-1", BindingID: "b-active", Version: 1, SubjectID: "subject-a", SubjectVersion: 1, Role: SubjectRoleCustomer, BindingState: "active"},
			{CaseID: "case-1", BindingID: "b-active", Version: 2, SubjectID: "subject-a", SubjectVersion: 2, Role: SubjectRoleCustomer, BindingState: "active"},
			{CaseID: "case-1", BindingID: "b-pending", Version: 1, SubjectID: "subject-a", SubjectVersion: 2, Role: SubjectRoleSupplier, BindingState: "pending_confirmation"},
			{CaseID: "case-1", BindingID: "b-inactive", Version: 1, SubjectID: "subject-a", SubjectVersion: 2, Role: SubjectRoleOther, BindingState: "inactive"},
			{CaseID: "case-2", BindingID: "b-other-case", Version: 1, SubjectID: "subject-a", SubjectVersion: 2, Role: SubjectRoleOther, BindingState: "active"},
		},
	}
	items, digest, err := partyContextForCase(st, "case-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].BindingID != "b-active" || items[1].BindingID != "b-pending" || digest == "" {
		t.Fatalf("party context did not select active and pending current bindings in stable order: %+v hash=%q", items, digest)
	}
	if items[0].BindingVersion != 1 || items[0].SubjectVersion != 1 {
		t.Fatalf("party context used an unselected append-only version instead of the binding head: %+v", items[0])
	}
	if items[0].VerificationScope != "synthetic_controlled" || items[0].SourceCatalogID != "synthetic.uscc.demo" || items[0].SourceSnapshotID != "snapshot-1" || items[0].SourceDigest != "digest-1" || items[0].SourceTime != nil || items[0].SourceValidFrom == nil {
		t.Fatalf("party context lost synthetic provenance or confused snapshot capture time with source time: %+v", items[0])
	}
	st.CaseSubjectRoles[0].CurrentVersion = 2
	updated, updatedHash, err := partyContextForCase(st, "case-1")
	if err != nil || len(updated) != 2 || updated[0].BindingVersion != 2 || updated[0].SubjectVersion != 2 || updatedHash == digest {
		t.Fatalf("party context did not follow the selected current head: items=%+v hash=%q err=%v", updated, updatedHash, err)
	}
	st.CaseSubjectRoles[0].CurrentVersion = 3
	if _, _, err := partyContextForCase(st, "case-1"); err == nil {
		t.Fatal("party context accepted a head that points to a missing append-only binding version")
	}
	if _, otherDigest, err := partyContextForCase(st, "case-2"); err != nil || otherDigest == digest {
		t.Fatalf("different case binding set produced same digest: other=%q current=%q err=%v", otherDigest, digest, err)
	}
	prior := RunSnapshot{DocumentIDs: []string{"doc-1"}, ParsedDocumentIDs: []string{"parse-1"}, PartyContextHash: digest}
	changed := prior
	changed.PartyContextHash = "changed"
	if sameRunSignature(prior, changed) {
		t.Fatal("subject context changes did not invalidate run signature reuse")
	}
}

func TestPartyContextSnapshotCodecRejectsPayloadHashMismatchAndPreservesLegacyAbsence(t *testing.T) {
	legacy := RunSnapshot{}
	if err := validatePartyContextSnapshot(legacy); err != nil {
		t.Fatalf("legacy empty party context is not compatible: %v", err)
	}
	if decoded, err := decodePartyContext(nil, ""); err != nil || decoded != nil {
		t.Fatalf("legacy absent payload did not round trip: %+v err=%v", decoded, err)
	}
	items := []PartyContextItem{{BindingID: "binding-1", BindingVersion: 2, Role: SubjectRoleSupplier, BindingState: "active", SubjectID: "subject-1", SubjectVersion: 3, DisplayName: "演示主体", VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceCatalogID: "synthetic.uscc.demo", SourceSnapshotID: "snapshot-1", SourceDigest: strings.Repeat("a", 64)}}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	hash := hashJSON(encoded)
	snapshot := RunSnapshot{PartyContext: items, PartyContextHash: hash}
	if err := validatePartyContextSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePartyContext(encoded, hash)
	if err != nil || !reflect.DeepEqual(decoded, items) {
		t.Fatalf("valid party context failed to round trip: got=%+v err=%v", decoded, err)
	}
	for _, malformed := range []RunSnapshot{{PartyContext: items}, {PartyContextHash: hash}, {PartyContext: items, PartyContextHash: strings.Repeat("0", 64)}} {
		if err := validatePartyContextSnapshot(malformed); err == nil {
			t.Fatalf("malformed party context passed validation: %+v", malformed)
		}
	}
	if _, err := decodePartyContext([]byte(`[ {"binding_id":"binding-1"} ]`), hash); err == nil {
		t.Fatal("noncanonical JSON or mismatched hash was accepted")
	}
}

func TestSubjectPaginationHandlesSmallAndOutOfRangePages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		count  int
		cursor string
		limit  int
		want   int
		next   bool
	}{
		{name: "empty", count: 0, limit: 50, want: 0},
		{name: "one default page", count: 1, limit: 50, want: 1},
		{name: "exact limit", count: 2, limit: 2, want: 2},
		{name: "first page", count: 3, limit: 2, want: 2, next: true},
		{name: "last partial page", count: 3, cursor: "subject-2", limit: 2, want: 1},
		{name: "cursor past end", count: 3, cursor: "subject-z", limit: 2, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]SubjectSummary, tc.count)
			for i := range items {
				items[i].SubjectID = "subject-" + string(rune('1'+i))
			}
			got := paginateSubjectSummaries(items, tc.cursor, tc.limit)
			if len(got.Items) != tc.want || (got.NextCursor != nil) != tc.next {
				t.Fatalf("page mismatch: got=%+v want length=%d next=%v", got, tc.want, tc.next)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time { return &value }

func TestSubjectPatchUsesFlatTypedUnionAndPreservesOmittedArrays(t *testing.T) {
	current := SubjectVersion{
		SubjectID: "subject-1", Version: 2, DisplayName: "旧名称", LegalName: "旧法定名称",
		VerificationState: "pending_verification", SourceKind: "user_asserted",
		IdentityClaims: []SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43", VerificationState: "pending_verification", SourceKind: "user_asserted"}},
		Aliases:        []SubjectAlias{{Value: "旧别名", Kind: "trade"}},
	}
	got, err := applySubjectPatch(current, json.RawMessage(`{"display_name":"新名称","legal_name":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "新名称" || got.LegalName != "" || got.VerificationState != "pending_verification" ||
		len(got.IdentityClaims) != 1 || got.IdentityClaims[0].Value != current.IdentityClaims[0].Value || got.IdentityClaims[0].VerificationState != "pending_verification" ||
		!reflect.DeepEqual(got.Aliases, current.Aliases) {
		t.Fatalf("flat patch did not preserve omitted user data or invalidate identity proof: %+v", got)
	}
	cleared, err := applySubjectPatch(current, json.RawMessage(`{"identity_claims":[],"aliases":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if cleared.IdentityClaims == nil || len(cleared.IdentityClaims) != 0 || cleared.Aliases == nil || len(cleared.Aliases) != 0 {
		t.Fatalf("explicit empty arrays did not clear values: claims=%+v aliases=%+v", cleared.IdentityClaims, cleared.Aliases)
	}
}

func TestSubjectPatchRejectsVerificationAndCrossKindFields(t *testing.T) {
	current := SubjectVersion{SubjectID: "subject-1", Version: 1, DisplayName: "名称", VerificationState: "pending_verification", SourceKind: "user_asserted"}
	for _, body := range []string{
		`{"verification_state":"verified"}`,
		`{"verified":true}`,
		`{"subject_id":"subject-2"}`,
		`{"role":"supplier"}`,
		`{"binding_state":"inactive"}`,
		`{"legal_name":null}`,
		`{"unexpected":true}`,
		`{"identity_claims":null}`,
		`{}`,
	} {
		if _, err := applySubjectPatch(current, json.RawMessage(body)); err == nil {
			t.Errorf("accepted forbidden subject patch %s", body)
		}
	}
	if _, err := applySubjectPatch(current, json.RawMessage(`{"identity_claims":[{"namespace":"cn.gov.samr.uscc","identifier_type":"unified_social_credit_code","value":"91350100M000100Y43","verification_state":"verified"}]}`)); err == nil {
		t.Fatal("client-supplied identifier verification state was accepted")
	}
}

func TestCaseRolePatchRequiresExplicitVersionedRebind(t *testing.T) {
	if _, err := parseCaseSubjectRolePatch(json.RawMessage(`{"subject_id":"subject-2"}`), "subject-1"); err == nil {
		t.Fatal("rebind without expected target subject version was accepted")
	}
	patch, err := parseCaseSubjectRolePatch(json.RawMessage(`{"subject_id":"subject-2","expected_new_subject_version":4,"role":"supplier"}`), "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	if patch.SubjectID == nil || *patch.SubjectID != "subject-2" || patch.ExpectedNewSubjectVersion == nil || *patch.ExpectedNewSubjectVersion != 4 || patch.Role == nil || *patch.Role != "supplier" {
		t.Fatalf("explicit role patch was not decoded: %+v", patch)
	}
	if _, err := parseCaseSubjectRolePatch(json.RawMessage(`{"display_name":"not a binding field"}`), "subject-1"); err == nil {
		t.Fatal("subject patch field was accepted for a role binding target")
	}
	if _, err := parseCaseSubjectRolePatch(json.RawMessage(`{"subject_id":"subject-1","expected_new_subject_version":3}`), "subject-1"); err == nil {
		t.Fatal("unexpected expected_new_subject_version without a rebind was accepted")
	}
	if _, err := parseCaseSubjectRolePatch(json.RawMessage(`{}`), "subject-1"); err == nil {
		t.Fatal("empty binding patch was accepted")
	}
}

func TestUSCCInputIsCanonicalizedBeforeValidation(t *testing.T) {
	claim, err := userAssertedClaim(SubjectIdentityClaimInput{
		Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: " 91350100m000100y43 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Value != "91350100M000100Y43" {
		t.Fatalf("claim was not canonicalized: %q", claim.Value)
	}
}

func TestSubjectNameCorrectionInvalidatesOnlyNewVersionVerification(t *testing.T) {
	sourceTime := time.Date(2026, 10, 31, 1, 2, 3, 0, time.UTC)
	current := SubjectVersion{
		SubjectID: "subject-1", Version: 3, DisplayName: "旧名", LegalName: "旧法定名",
		VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot",
		IdentityClaims: []SubjectIdentityClaim{{
			Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43",
			VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot",
			SourceTime: &sourceTime, SourceCatalogID: "synthetic.uscc.demo", SourceSnapshotID: "snapshot-1", SourceDigest: "digest-1",
			SourceValidFrom: timePtr(sourceTime.Add(-time.Hour)), SourceValidUntil: timePtr(sourceTime.Add(time.Hour)),
		}},
	}
	got, err := applySubjectPatch(current, json.RawMessage(`{"display_name":"新名"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.VerificationState != "pending_verification" || got.VerificationScope != "" || got.IdentityClaims[0].VerificationState != "pending_verification" || got.IdentityClaims[0].VerificationScope != "" || got.IdentityClaims[0].SourceKind != "authorized_correction" || got.IdentityClaims[0].SourceCatalogID != "" || got.IdentityClaims[0].SourceSnapshotID != "" || got.IdentityClaims[0].SourceDigest != "" || got.IdentityClaims[0].SourceTime != nil || got.IdentityClaims[0].SourceValidFrom != nil || got.IdentityClaims[0].SourceValidUntil != nil {
		t.Fatalf("name correction retained verification evidence: %+v", got)
	}
	if current.VerificationState != "verified" || current.IdentityClaims[0].VerificationState != "verified" || current.IdentityClaims[0].SourceCatalogID != "synthetic.uscc.demo" || current.IdentityClaims[0].SourceSnapshotID != "snapshot-1" || current.IdentityClaims[0].SourceTime == nil || !current.IdentityClaims[0].SourceTime.Equal(sourceTime) || current.IdentityClaims[0].SourceValidFrom == nil || current.IdentityClaims[0].SourceValidUntil == nil {
		t.Fatalf("prior immutable version was mutated: %+v", current)
	}
	aliasOnly, err := applySubjectPatch(current, json.RawMessage(`{"aliases":[{"value":"别名","kind":"trade"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if aliasOnly.VerificationState != "verified" || aliasOnly.IdentityClaims[0].VerificationState != "verified" {
		t.Fatalf("alias-only change invalidated identity verification: %+v", aliasOnly)
	}
}

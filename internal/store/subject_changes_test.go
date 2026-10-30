package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestSubjectChangeProjectionMasksReadersAndShowsFullUSCCToAuthorizedReviewers(t *testing.T) {
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	reader, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("demo reader missing")
	}
	const uscc = "91350100M000100Y43"
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	subject := Subject{TenantID: admin.TenantID, ID: "subject-1", CurrentVersion: 1, Status: "verified", CreatedByUser: admin.ID, CreatedAt: now}
	version := SubjectVersion{TenantID: admin.TenantID, SubjectID: subject.ID, Version: 1, DisplayName: "旧名", VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot", CreatedAt: now,
		IdentityClaims: []SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: uscc, VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot"}}, Aliases: []SubjectAlias{}}
	after := version
	after.Version = 2
	after.DisplayName = "新名"
	after.SourceKind = "authorized_correction"
	after.VerificationState = "pending_verification"
	after.VerificationScope = ""
	after.IdentityClaims = append([]SubjectIdentityClaim(nil), version.IdentityClaims...)
	after.IdentityClaims[0].VerificationState = "pending_verification"
	after.IdentityClaims[0].VerificationScope = ""
	afterBytes, err := json.Marshal(subjectAfterPayload{SubjectVersion: after})
	if err != nil {
		t.Fatal(err)
	}
	caseItem := Case{ID: "case-1", TenantID: admin.TenantID, OwnerOrgID: admin.OrgID, OwnerID: admin.ID, MemberIDs: []string{admin.ID, reviewer.ID, reader.ID}, BusinessStatus: "active"}
	st := state{Cases: []Case{caseItem}, Subjects: []Subject{subject}, SubjectVersions: []SubjectVersion{version},
		CaseSubjectRoles:        []CaseSubjectRole{{TenantID: admin.TenantID, OrgID: admin.OrgID, CaseID: caseItem.ID, BindingID: "binding-1", CurrentVersion: 1}},
		CaseSubjectRoleVersions: []CaseSubjectRoleVersion{{TenantID: admin.TenantID, OrgID: admin.OrgID, CaseID: caseItem.ID, BindingID: "binding-1", Version: 1, SubjectID: subject.ID, SubjectVersion: 1, Role: SubjectRoleCustomer, BindingState: "active"}}}
	request := SubjectChangeRequest{TenantID: admin.TenantID, OrgID: admin.OrgID, RequestID: "change-1", SourceCaseID: caseItem.ID, TargetKind: "subject", TargetSubjectID: subject.ID, ExpectedSubjectVersion: 1, CanonicalPayload: afterBytes, CanonicalPayloadHash: "payload", ExpectedBindingsHash: "bindings", ProposedBy: TrustedPrincipalProjection{UserID: admin.ID, Issuer: admin.Issuer, Subject: admin.Subject}, CreatedAt: now}
	for _, tc := range []struct {
		name     string
		user     accounts.User
		wantFull bool
	}{{"ordinary reader", reader, false}, {"independent authorized reviewer", reviewer, true}, {"source case manager", admin, true}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := subjectChangeImpact(st, tc.user, request)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			hasRaw := strings.Contains(string(encoded), uscc)
			if hasRaw != tc.wantFull {
				t.Fatalf("USCC visibility mismatch: want_full=%v body=%s", tc.wantFull, encoded)
			}
			if tc.wantFull && !got.CanDecide && tc.name == "independent authorized reviewer" {
				t.Fatalf("eligible reviewer was not advertised as able to decide: %+v", got)
			}
		})
	}
}

func TestSubjectChangeListPaginationIsBoundedAndStable(t *testing.T) {
	items := []SubjectChangeSummary{{RequestID: "change-a"}, {RequestID: "change-b"}, {RequestID: "change-c"}}
	page, err := paginateSubjectChangeSummaries(items, "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil || page.Items[1].RequestID != "change-b" {
		t.Fatalf("first page mismatch: %+v err=%v", page, err)
	}
	page, err = paginateSubjectChangeSummaries(items, *page.NextCursor, 2)
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil || page.Items[0].RequestID != "change-c" {
		t.Fatalf("last page mismatch: %+v err=%v", page, err)
	}
	pastEnd := base64.RawURLEncoding.EncodeToString([]byte("change-z"))
	page, err = paginateSubjectChangeSummaries(items, pastEnd, 2)
	if err != nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("out-of-range cursor mismatch: %+v err=%v", page, err)
	}
	if _, err := paginateSubjectChangeSummaries(items, "bad%cursor", 2); err == nil {
		t.Fatal("invalid cursor was accepted")
	}
	if _, err := paginateSubjectChangeSummaries(items, "", 101); err == nil {
		t.Fatal("unbounded page size was accepted")
	}
}

func TestSubjectDecisionSeparationUsesTrustedDirectoryIdentityNotAccountIDOnly(t *testing.T) {
	proposal := TrustedPrincipalProjection{UserID: "account-proposer", Issuer: "https://directory.example", Subject: "principal-17"}
	tests := []struct {
		name string
		who  TrustedPrincipalProjection
		same bool
	}{
		{name: "same user id", who: TrustedPrincipalProjection{UserID: "account-proposer", Issuer: "https://directory.example", Subject: "principal-17"}, same: true},
		{name: "same trusted principal through alias account", who: TrustedPrincipalProjection{UserID: "alias-account", Issuer: "https://directory.example", Subject: "principal-17"}, same: true},
		{name: "different trusted principal", who: TrustedPrincipalProjection{UserID: "other-account", Issuer: "https://directory.example", Subject: "principal-18"}},
		{name: "different issuer", who: TrustedPrincipalProjection{UserID: "alias-account", Issuer: "https://other.example", Subject: "principal-17"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := subjectPrincipalSame(proposal, tc.who); got != tc.same {
				t.Fatalf("subjectPrincipalSame(%+v,%+v)=%v, want %v", proposal, tc.who, got, tc.same)
			}
		})
	}
}

func TestSubjectChangeWritesRespectReadOnlyMaintenanceMode(t *testing.T) {
	st := &Store{}
	st.UseReadOnly(true)
	if _, _, err := st.ProposeSubjectChange(context.Background(), accounts.User{}, "case", SubjectChangeCreateInput{}, "key"); !isReadOnlyFault(err) {
		t.Fatalf("proposal write was not blocked in read-only mode: %v", err)
	}
	if _, _, err := st.DecideSubjectChange(context.Background(), accounts.User{}, "case", "request", SubjectChangeDecisionInput{}, "key"); !isReadOnlyFault(err) {
		t.Fatalf("decision write was not blocked in read-only mode: %v", err)
	}
}

func isReadOnlyFault(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 503 && apiErr.Code == "read_only"
}

func TestCanonicalRawJSONIgnoresObjectPropertyOrderAndRejectsTrailingValues(t *testing.T) {
	left, err := canonicalRawJSON(json.RawMessage(`{"display_name":"甲","legal_name":"乙"}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := canonicalRawJSON(json.RawMessage(`{"legal_name":"乙","display_name":"甲"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) {
		t.Fatalf("equivalent JSON object property order produced different canonical payloads: %s vs %s", left, right)
	}
	arrayA, err := canonicalRawJSON(json.RawMessage(`[{"a":1,"b":2},{"a":3}]`))
	if err != nil {
		t.Fatal(err)
	}
	arrayB, err := canonicalRawJSON(json.RawMessage(`[{"a":3},{"b":2,"a":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(arrayA) == string(arrayB) {
		t.Fatal("array element order should remain part of the request identity")
	}
	if _, err := canonicalRawJSON(json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}

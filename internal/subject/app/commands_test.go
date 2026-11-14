package app

import (
	"testing"
	"time"

	"contractaudit/internal/subject/model"
)

type commandTestClock struct {
	calls []string
	now   time.Time
}

func (c *commandTestClock) Now() time.Time { c.calls = append(c.calls, "now"); return c.now }
func (c *commandTestClock) NewSubjectID() (string, error) {
	c.calls = append(c.calls, "subject")
	return "sub", nil
}
func (c *commandTestClock) NewBindingID() (string, error) {
	c.calls = append(c.calls, "binding")
	return "bind", nil
}
func TestUnboundPreservesClaimsAliasesAndReceiptClock(t *testing.T) {
	input, err := NormalizeUnbound(model.SubjectCreateInput{DisplayName: " A ", Identifiers: []model.SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: " 91350100m000100y43 "}}, Aliases: []model.SubjectAliasInput{{Value: " 别名 ", Kind: "trade"}}})
	if err != nil {
		t.Fatal(err)
	}
	clock := &commandTestClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	delta, result, err := CreateUnbound(Actor{TenantID: "tenant", Principal: model.TrustedPrincipalProjection{UserID: "actor"}}, input, "key", "hash", clock)
	if err != nil || len(delta.Versions) != 1 || len(delta.Versions[0].IdentityClaims) != 1 || len(delta.Versions[0].Aliases) != 1 || delta.Versions[0].IdentityClaims[0].Value != "91350100M000100Y43" || delta.Versions[0].Aliases[0].Value != "别名" || result.SubjectID != "sub" {
		t.Fatalf("delta=%+v result=%+v err=%v", delta, result, err)
	}
	if len(clock.calls) != 3 || clock.calls[0] != "now" || clock.calls[1] != "subject" || clock.calls[2] != "now" {
		t.Fatalf("clock calls=%v", clock.calls)
	}
}
func TestPendingCreatorVisibilityRequiresNoHistoricalBinding(t *testing.T) {
	actor := Actor{TenantID: "tenant", Principal: model.TrustedPrincipalProjection{UserID: "actor"}}
	snap := Snapshot{Collections: model.SubjectCollections{Subjects: []model.Subject{{TenantID: "tenant", ID: "sub", CurrentVersion: 1, Status: "pending_verification", CreatedByUser: "actor"}}, SubjectVersions: []model.SubjectVersion{{TenantID: "tenant", SubjectID: "sub", Version: 1}}}}
	if _, _, ok := VisibleSubject(snap, actor, "sub", nil); !ok {
		t.Fatal("creator lost visibility of never-bound pending subject")
	}
	snap.Collections.CaseSubjectRoleVersions = []model.CaseSubjectRoleVersion{{SubjectID: "sub", BindingState: "inactive"}}
	if _, _, ok := VisibleSubject(snap, actor, "sub", nil); ok {
		t.Fatal("historically bound subject remained visible without case access")
	}
}

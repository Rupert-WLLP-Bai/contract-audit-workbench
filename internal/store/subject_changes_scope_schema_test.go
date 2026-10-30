package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestSubjectDecisionRechecksEveryAffectedCaseAndHidesUnauthorizedCases(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	ctx := context.Background()
	caseA, err := core.CreateCase(admin, "共享主体案件A", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseB, err := core.CreateCase(admin, "提案来源案件B", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseC, err := core.CreateCase(admin, "隐藏案件C机密标题", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, caseID := range []string{caseA, caseB} {
		if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
			t.Fatal(err)
		}
	}
	subject, _, err := core.CreateCaseSubject(ctx, admin, caseA, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "共享主体待核验"}}, "scope-cas-create")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.BindExistingSubject(ctx, admin, caseB, BindExistingSubjectInput{Role: SubjectRoleSupplier, SubjectID: subject.SubjectID, ExpectedSubjectVersion: 1}, "scope-cas-bind-b"); err != nil {
		t.Fatal(err)
	}
	const sourceText = "材料证据证明该共享主体的登记名称。"
	upload, err := document.UploadMaterial(ctx, admin, caseB, "procurement", "scope-source.txt", "text/plain", []byte(sourceText), MaterialUploadOptions{IdempotencyKey: "scope-source-upload"})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := document.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var source Document
	for _, item := range docs.Documents {
		if item.ID == upload.DocumentID {
			source = item
			break
		}
	}
	if source.ID == "" {
		t.Fatal("source document missing")
	}
	parsed, err := document.ensureParsed(ctx, admin, source, false)
	if err != nil || parsed.Status != "ready" {
		t.Fatalf("parse proposal evidence: %+v err=%v", parsed, err)
	}
	quote := []rune(parsed.Text)
	digest := sha256.Sum256([]byte(string(quote)))
	basis, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: upload.MaterialID, MaterialVersion: upload.Version,
		DocumentID: upload.DocumentID, ParsedDocumentID: parsed.ID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(digest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, replayed, err := core.ProposeSubjectChange(ctx, admin, caseB, SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: subject.SubjectID},
		ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"display_name":"共享主体更正名称"}`), BasisNote: "按来源案件材料更正共享主体", BasisRefs: basis}, "scope-change-proposal")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("propose subject correction: %+v replayed=%v err=%v", proposal, replayed, err)
	}
	initialImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || !initialImpact.CanDecide || initialImpact.VisibleAffectedCount != 2 {
		t.Fatalf("reviewer with all initial cases lacks decision scope: %+v err=%v", initialImpact, err)
	}
	_, selfReplay, selfErr := core.DecideSubjectChange(ctx, admin, caseB, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: initialImpact.ImpactETag, Reason: "提出人尝试自行批准"}, "scope-cas-self-approval")
	var selfAPIError *fault.Error
	if !errors.As(selfErr, &selfAPIError) || selfAPIError.Status != 403 || selfAPIError.Code != "forbidden" || selfReplay || !strings.Contains(selfErr.Error(), "提出人不能批准") {
		t.Fatalf("source proposer was not rejected as self-approval: replay=%v err=%v", selfReplay, selfErr)
	}

	if _, _, err := core.BindExistingSubject(ctx, admin, caseC, BindExistingSubjectInput{Role: SubjectRoleSupplier, SubjectID: subject.SubjectID, ExpectedSubjectVersion: 1}, "scope-cas-bind-hidden-c"); err != nil {
		t.Fatal(err)
	}
	staleDecision := SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: proposal.PayloadHash,
		ExpectedImpactETag: initialImpact.ImpactETag, Reason: "决定身份更正"}
	if _, _, err := core.DecideSubjectChange(ctx, reviewer, caseB, proposal.RequestID, staleDecision, "scope-cas-stale-decision"); !isConflictFault(err) {
		t.Fatalf("new affected binding did not stale the proposal decision: %v", err)
	}
	firstRequestID := proposal.RequestID
	proposal, replayed, err = core.ProposeSubjectChange(ctx, admin, caseB, SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: subject.SubjectID},
		ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"display_name":"共享主体更正名称"}`), BasisNote: "按来源案件材料更正共享主体", BasisRefs: basis}, "scope-change-proposal-after-new-binding")
	if err != nil || replayed || proposal.RequestID == "" || proposal.RequestID == firstRequestID {
		t.Fatalf("new proposal after affected-set change failed: %+v replayed=%v err=%v", proposal, replayed, err)
	}
	impact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || impact.CanDecide || impact.VisibleAffectedCount != 2 || len(impact.AuthorizedImpactCases) != 2 {
		t.Fatalf("hidden case must block decision without exposing it: %+v err=%v", impact, err)
	}
	impactWire, _ := json.Marshal(impact)
	if strings.Contains(string(impactWire), "隐藏案件C机密标题") || strings.Contains(string(impactWire), caseC) {
		t.Fatalf("impact disclosed hidden case identity: %s", impactWire)
	}
	if _, _, err := core.DecideSubjectChange(ctx, reviewer, caseB, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: impact.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "当前没有所有案件权限"}, "scope-cas-hidden-case"); !isConflictFault(err) {
		t.Fatalf("reviewer without access to a newly affected case decided the change: %v", err)
	}

	if err := core.updateCaseAs(admin, caseC, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseC {
				archivedAt := stamp()
				st.Cases[i].BusinessStatus = "archived"
				st.Cases[i].ArchivedAt = &archivedAt
				return nil
			}
		}
		return errors.New("case missing from test state")
	}); err != nil {
		t.Fatal(err)
	}
	archivedImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || archivedImpact.CanDecide {
		t.Fatalf("archived affected case did not block decisionability: %+v err=%v", archivedImpact, err)
	}
	if _, _, err := core.DecideSubjectChange(ctx, reviewer, caseB, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: archivedImpact.PayloadHash, ExpectedImpactETag: archivedImpact.ImpactETag, Reason: "受影响案件已归档"}, "scope-cas-archived-case"); !isConflictFault(err) {
		t.Fatalf("archived affected case did not block approval: %v", err)
	}
	if err := core.updateCaseAs(admin, caseC, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseC {
				st.Cases[i].BusinessStatus = "active"
				st.Cases[i].ArchivedAt = nil
				return nil
			}
		}
		return errors.New("case missing from test state")
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.AddMember(admin, caseC, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	users := accounts.List()
	for i := range users {
		if users[i].Issuer == "" {
			users[i].Issuer = "https://test-directory.invalid"
			users[i].Subject = "principal-" + users[i].ID
		}
		switch users[i].ID {
		case admin.ID:
			users[i].Grants = caseSubjectTestGrants("case_owner", caseA, caseB, caseC)
		case reviewer.ID:
			users[i].Grants = caseSubjectTestGrants("reviewer", caseA, caseB)
		}
	}
	reviewer2 := reviewer
	reviewer2.ID = "scope-reviewer-two"
	reviewer2.DisplayName = "第二独立审查人"
	reviewer2.Issuer = "https://test-directory.invalid"
	reviewer2.Subject = "principal-scope-reviewer-two"
	reviewer2.Grants = caseSubjectTestGrants("reviewer", caseA, caseB, caseC)
	users = append(users, reviewer2)
	directoryPayload, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{Users: users})
	if err != nil {
		t.Fatal(err)
	}
	directoryPath := filepath.Join(t.TempDir(), "trusted-reviewers.json")
	if err := os.WriteFile(directoryPath, directoryPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directoryPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	setReviewerCaseGrant := func(includeCaseC bool) {
		currentUsers := accounts.List()
		for i := range currentUsers {
			if currentUsers[i].ID == reviewer.ID {
				caseIDs := []string{caseA, caseB}
				if includeCaseC {
					caseIDs = append(caseIDs, caseC)
				}
				currentUsers[i].Grants = caseSubjectTestGrants("reviewer", caseIDs...)
			}
		}
		payload, err := json.Marshal(struct {
			Users []accounts.User `json:"users"`
		}{Users: currentUsers})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(directoryPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := accounts.ConfigureDirectory(directoryPath); err != nil {
			t.Fatal(err)
		}
		var ok bool
		reviewer, ok = accounts.ByID("demo-reviewer")
		if !ok {
			t.Fatal("reviewer disappeared after grant update")
		}
	}
	// Directory normalization above binds blank demo principals to trusted
	// issuer/subject identities. Refresh these values before later store calls,
	// which intentionally reject stale unbound account copies.
	admin, ok = accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin disappeared after trusted directory setup")
	}
	reviewer, ok = accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer disappeared after trusted directory setup")
	}
	if err := core.updateCaseAs(admin, caseA, func(st *state) error { addSubjectTestMember(st, caseA, reviewer2.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := core.updateCaseAs(admin, caseB, func(st *state) error { addSubjectTestMember(st, caseB, reviewer2.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := core.updateCaseAs(admin, caseC, func(st *state) error { addSubjectTestMember(st, caseC, reviewer2.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	coreSecond := env.openRole(t, "core")
	sameKeySubject, replayed, err := core.CreateCaseSubject(ctx, admin, caseB, CaseSubjectCreateInput{Role: SubjectRoleCustomer, Subject: SubjectCreateInput{DisplayName: "同键并发审批主体"}}, "scope-concurrent-same-key-subject")
	if err != nil || replayed {
		t.Fatalf("create same-key race subject: %+v replayed=%v err=%v", sameKeySubject, replayed, err)
	}
	sameKeyProposal, replayed, err := core.ProposeSubjectChange(ctx, admin, caseB, SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: sameKeySubject.SubjectID},
		ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"display_name":"同键并发审批主体二版"}`), BasisNote: "并发幂等审核依据", BasisRefs: basis}, "scope-concurrent-same-key-proposal")
	if err != nil || replayed || sameKeyProposal.RequestID == "" {
		t.Fatalf("propose same-key race subject correction: %+v replayed=%v err=%v", sameKeyProposal, replayed, err)
	}
	sameKeyImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, sameKeyProposal.RequestID)
	if err != nil || !sameKeyImpact.CanDecide {
		t.Fatalf("same-key race reviewer lacks decision scope: %+v err=%v", sameKeyImpact, err)
	}
	sameKeyDecisionInput := SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: sameKeyProposal.PayloadHash,
		ExpectedImpactETag: sameKeyImpact.ImpactETag, Reason: "复核同键并发主体更正"}
	type decisionResult struct {
		result   SubjectChangeDecisionResult
		replayed bool
		err      error
	}
	sameKeyResults := make(chan decisionResult, 2)
	var sameKeyWriters sync.WaitGroup
	for _, store := range []*Store{core, coreSecond} {
		store := store
		sameKeyWriters.Add(1)
		go func() {
			defer sameKeyWriters.Done()
			result, wasReplay, err := store.DecideSubjectChange(ctx, reviewer, caseB, sameKeyProposal.RequestID, sameKeyDecisionInput, "scope-concurrent-same-key-decision")
			sameKeyResults <- decisionResult{result: result, replayed: wasReplay, err: err}
		}()
	}
	sameKeyWriters.Wait()
	close(sameKeyResults)
	var sameKeyBusinessResult SubjectChangeDecisionResult
	sameKeyReplayCount := 0
	for got := range sameKeyResults {
		if got.err != nil {
			t.Fatalf("same-key concurrent decision failed: %+v", got)
		}
		if got.replayed {
			sameKeyReplayCount++
		}
		if sameKeyBusinessResult.RequestID == "" {
			sameKeyBusinessResult = got.result
		} else if !reflect.DeepEqual(sameKeyBusinessResult, got.result) {
			t.Fatalf("same-key concurrent retries returned different business results: first=%+v next=%+v", sameKeyBusinessResult, got.result)
		}
	}
	if sameKeyBusinessResult.Decision != "approved" || sameKeyBusinessResult.AppliedSubjectVersion == nil || *sameKeyBusinessResult.AppliedSubjectVersion != 2 || sameKeyReplayCount != 1 {
		t.Fatalf("same-key concurrent retries did not commit once then replay: result=%+v replay_count=%d", sameKeyBusinessResult, sameKeyReplayCount)
	}
	sameKeyState, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var sameKeyDecisions, sameKeyIdempotency, sameKeyVersions int
	for _, item := range sameKeyState.SubjectChangeDecisions {
		if item.RequestID == sameKeyProposal.RequestID {
			sameKeyDecisions++
		}
	}
	for _, item := range sameKeyState.SubjectIdempotency {
		if item.Operation == "decide_subject_change" && item.TargetID == sameKeyProposal.RequestID && item.Key == "scope-concurrent-same-key-decision" {
			sameKeyIdempotency++
		}
	}
	for _, item := range sameKeyState.SubjectVersions {
		if item.SubjectID == sameKeySubject.SubjectID {
			sameKeyVersions++
		}
	}
	if sameKeyDecisions != 1 || sameKeyIdempotency != 1 || sameKeyVersions != 2 {
		t.Fatalf("same-key race duplicated decision, idempotency result or subject version: decisions=%d keys=%d versions=%d", sameKeyDecisions, sameKeyIdempotency, sameKeyVersions)
	}
	setReviewerCaseGrant(true)
	fullImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || !fullImpact.CanDecide || fullImpact.VisibleAffectedCount != 3 {
		t.Fatalf("reviewer did not regain complete decision scope: %+v err=%v", fullImpact, err)
	}
	if err := core.updateCaseAs(admin, caseC, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseC {
				members := make([]string, 0, len(st.Cases[i].MemberIDs))
				for _, member := range st.Cases[i].MemberIDs {
					if member != reviewer.ID {
						members = append(members, member)
					}
				}
				st.Cases[i].MemberIDs = members
				return nil
			}
		}
		return errors.New("case missing from test state")
	}); err != nil {
		t.Fatal(err)
	}
	stillAuthorizedImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || !stillAuthorizedImpact.CanDecide || stillAuthorizedImpact.VisibleAffectedCount != 3 {
		t.Fatalf("removing only MemberIDs unexpectedly overrode an explicit trusted case grant: %+v err=%v", stillAuthorizedImpact, err)
	}
	setReviewerCaseGrant(false)
	revokedImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || revokedImpact.CanDecide || revokedImpact.VisibleAffectedCount != 2 {
		t.Fatalf("trusted directory case grant revocation was not reflected in impact: %+v err=%v", revokedImpact, err)
	}
	beforeRevokedDecision, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.DecideSubjectChange(ctx, reviewer, caseB, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: fullImpact.ImpactETag, Reason: "授权已撤销"}, "scope-cas-revoked"); !isConflictFault(err) {
		t.Fatalf("revoked case grant was not rechecked under the mutation lock: %v", err)
	}
	afterRevokedDecision, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeRevokedDecision.SubjectVersions) != len(afterRevokedDecision.SubjectVersions) || len(beforeRevokedDecision.SubjectChangeDecisions) != len(afterRevokedDecision.SubjectChangeDecisions) || len(beforeRevokedDecision.SubjectIdempotency) != len(afterRevokedDecision.SubjectIdempotency) {
		t.Fatal("revoked trusted case grant left subject, decision or idempotency writes")
	}
	setReviewerCaseGrant(true)
	if err := core.AddMember(admin, caseC, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	latestImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseB, proposal.RequestID)
	if err != nil || !latestImpact.CanDecide {
		t.Fatalf("reviewer could not refresh after grant restoration: %+v err=%v", latestImpact, err)
	}
	decisionInput := SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: latestImpact.ImpactETag, Reason: "全部影响案件权限复核完成"}
	decisionResults := make(chan decisionResult, 2)
	var decisionWriters sync.WaitGroup
	for _, call := range []struct {
		store *Store
		actor accounts.User
		key   string
	}{{core, reviewer, "scope-cas-success-a"}, {coreSecond, reviewer2, "scope-cas-success-b"}} {
		call := call
		decisionWriters.Add(1)
		go func() {
			defer decisionWriters.Done()
			result, replayed, err := call.store.DecideSubjectChange(ctx, call.actor, caseB, proposal.RequestID, decisionInput, call.key)
			decisionResults <- decisionResult{result: result, replayed: replayed, err: err}
		}()
	}
	decisionWriters.Wait()
	close(decisionResults)
	var approved SubjectChangeDecisionResult
	conflicts := 0
	for got := range decisionResults {
		if got.err == nil && !got.replayed && got.result.Decision == "approved" {
			approved = got.result
			continue
		}
		if isConflictFault(got.err) && !got.replayed {
			conflicts++
			continue
		}
		t.Fatalf("concurrent approval result was neither one approval nor a conflict: %+v", got)
	}
	if approved.RequestID != proposal.RequestID || approved.BindingVersionsAppended != 3 || approved.AppliedSubjectVersion == nil || *approved.AppliedSubjectVersion != 2 || conflicts != 1 {
		t.Fatalf("two distinct decisions did not produce exactly one atomic winner: approved=%+v conflicts=%d", approved, conflicts)
	}
	afterConcurrentDecisions, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	decisionCount, decisionKeys := 0, 0
	for _, item := range afterConcurrentDecisions.SubjectChangeDecisions {
		if item.RequestID == proposal.RequestID {
			decisionCount++
		}
	}
	for _, item := range afterConcurrentDecisions.SubjectIdempotency {
		if item.Operation == "decide_subject_change" && item.TargetID == proposal.RequestID && (item.Key == "scope-cas-success-a" || item.Key == "scope-cas-success-b") {
			decisionKeys++
		}
	}
	if decisionCount != 1 || decisionKeys != 1 {
		t.Fatalf("concurrent approval wrote duplicate decisions or idempotency results: decisions=%d keys=%d", decisionCount, decisionKeys)
	}
}

func isConflictFault(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 409 && apiErr.Code == "conflict"
}

func addSubjectTestMember(st *state, caseID, userID string) error {
	for i := range st.Cases {
		if st.Cases[i].ID != caseID {
			continue
		}
		for _, memberID := range st.Cases[i].MemberIDs {
			if memberID == userID {
				return nil
			}
		}
		st.Cases[i].MemberIDs = append(st.Cases[i].MemberIDs, userID)
		return nil
	}
	return errors.New("case missing while adding independent reviewer")
}

func caseSubjectTestGrants(role string, caseIDs ...string) []access.Grant {
	grants := make([]access.Grant, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		grants = append(grants, access.Grant{Role: role, Scope: "case", CaseID: caseID})
	}
	return grants
}

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestSubjectSourceVersionContentIsStableAcrossCasesStoresAndReopen(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	coreA, coreB := env.openRole(t, "core"), env.openRole(t, "core")
	caseA, err := coreA.CreateCase(admin, "来源版本一致性案件A", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseB, err := coreA.CreateCase(admin, "来源版本一致性案件B", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	manifestA := []byte(`{"source_id":"synthetic.uscc.demo","source_version":1,"display_name":"目录A","verification_scope":"synthetic_controlled","records":[{"record_id":"record-a","display_name":"公司甲","uscc":"91350100M000100Y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`)
	manifestB := []byte(`{"source_id":"synthetic.uscc.demo","source_version":1,"display_name":"目录B","verification_scope":"synthetic_controlled","records":[{"record_id":"record-b","display_name":"公司乙","uscc":"91350100M000100Y44","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`)
	if err := coreA.ConfigureSubjectSourceManifest(manifestA); err != nil {
		t.Fatal(err)
	}
	if err := coreB.ConfigureSubjectSourceManifest(manifestB); err != nil {
		t.Fatal(err)
	}
	type snapshotAttempt struct {
		store       *Store
		caseID, key string
		manifest    []byte
	}
	type snapshotResult struct {
		attempt  snapshotAttempt
		result   SubjectSourceSnapshotResult
		replayed bool
		err      error
	}
	attempts := []snapshotAttempt{{coreA, caseA, "source-version-concurrent-a", manifestA}, {coreB, caseB, "source-version-concurrent-b", manifestB}}
	results := make(chan snapshotResult, len(attempts))
	var writers sync.WaitGroup
	for _, attempt := range attempts {
		attempt := attempt
		writers.Add(1)
		go func() {
			defer writers.Done()
			result, replayed, err := attempt.store.CreateSubjectSourceSnapshot(context.Background(), admin, attempt.caseID,
				SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 1}, attempt.key)
			results <- snapshotResult{attempt: attempt, result: result, replayed: replayed, err: err}
		}()
	}
	writers.Wait()
	close(results)
	var winner, loser snapshotResult
	for result := range results {
		if result.err == nil && !result.replayed {
			winner = result
			continue
		}
		var apiErr *fault.Error
		if errors.As(result.err, &apiErr) && apiErr.Status == 409 && apiErr.Code == "conflict" {
			loser = result
			continue
		}
		t.Fatalf("unexpected concurrent source snapshot result: %+v", result)
	}
	if winner.result.SnapshotID == "" || loser.err == nil || strings.Contains(loser.err.Error(), caseA) || strings.Contains(loser.err.Error(), caseB) {
		t.Fatalf("same catalog/version accepted divergent payloads or leaked case details: winner=%+v loser=%+v", winner, loser)
	}
	if err := loser.attempt.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := env.openRole(t, "core")
	defer reopened.Close()
	if err := reopened.ConfigureSubjectSourceManifest(loser.attempt.manifest); err != nil {
		t.Fatal(err)
	}
	_, replayed, err := reopened.CreateSubjectSourceSnapshot(context.Background(), admin, loser.attempt.caseID,
		SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 1}, "source-version-reopen-conflict")
	if !isConflictFault(err) || replayed {
		t.Fatalf("a new store bypassed persisted catalog/version digest consistency: replayed=%v err=%v", replayed, err)
	}
	manifestLoserV2 := strings.Replace(string(loser.attempt.manifest), `"source_version":1`, `"source_version":2`, 1)
	if err := reopened.ConfigureSubjectSourceManifest([]byte(manifestLoserV2)); err != nil {
		t.Fatal(err)
	}
	newVersion, replayed, err := reopened.CreateSubjectSourceSnapshot(context.Background(), admin, loser.attempt.caseID,
		SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 2}, "source-version-reopen-v2")
	if err != nil || replayed || newVersion.SourceVersion != 2 || newVersion.SourceDigest == winner.result.SourceDigest {
		t.Fatalf("new source version was rejected or reused old digest: %+v replayed=%v err=%v", newVersion, replayed, err)
	}
}

func TestControlledSubjectSourceSnapshotVerificationIsIdempotentAndAppendOnly(t *testing.T) {
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
	core := env.openRole(t, "core")
	ctx := context.Background()
	caseID, err := core.CreateCase(admin, "受控主体来源核验", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	manifest := `{"source_id":"synthetic.uscc.demo","source_version":1,"display_name":"演示受控主体目录","verification_scope":"synthetic_controlled","records":[{"record_id":"demo-a","display_name":"受控示例公司","uscc":"91350100m000100y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null},{"record_id":"demo-b","display_name":"受控示例公司","uscc":"91350100M000100Y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null},{"record_id":"demo-c","display_name":"第三条不同记录","uscc":"91350100M000100Y44","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`
	if err := core.ConfigureSubjectSourceManifest([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	directory, err := core.ListConfiguredSubjectSources(ctx, admin, caseID)
	if err != nil || len(directory.Items) != 1 || directory.Items[0].SourceVersion != 1 {
		t.Fatalf("configured source directory is wrong: %+v err=%v", directory, err)
	}
	wire, _ := json.Marshal(directory)
	if strings.Contains(string(wire), "91350100") || strings.Contains(string(wire), "record_id") {
		t.Fatalf("source directory disclosed source record identity: %s", wire)
	}
	exact, _, err := core.CreateCaseSubject(ctx, admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "受控示例公司", Identifiers: []SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43"}}}}, "source-verify-exact-subject")
	if err != nil {
		t.Fatal(err)
	}
	miss, _, err := core.CreateCaseSubject(ctx, admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "另一家名称", Identifiers: []SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y44"}}}}, "source-verify-miss-subject")
	if err != nil {
		t.Fatal(err)
	}
	conflicting, _, err := core.CreateCaseSubject(ctx, admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "受控示例公司", Identifiers: []SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43"}}}}, "source-verify-conflicting-subject")
	if err != nil {
		t.Fatal(err)
	}
	ambiguousSnapshot, replayed, err := core.CreateSubjectSourceSnapshot(ctx, admin, caseID, SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 1}, "source-verify-snapshot-v1")
	if err != nil || replayed || ambiguousSnapshot.SnapshotID == "" || len(ambiguousSnapshot.SourceDigest) != 64 || ambiguousSnapshot.RecordCount != 3 {
		t.Fatalf("source snapshot creation failed: snapshot=%+v replay=%v err=%v", ambiguousSnapshot, replayed, err)
	}
	noMatch, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseID, ambiguousSnapshot.SnapshotID, SubjectSourceVerifyInput{SubjectID: miss.SubjectID, ExpectedSubjectVersion: 1}, "source-verify-no-match")
	if err != nil || replayed || noMatch.State != "not_proposed" || noMatch.MatchOutcome != "no_match" || noMatch.RequestID != nil || noMatch.MatchedRecord != nil {
		t.Fatalf("nonmatching source record created a proposal: result=%+v replay=%v err=%v", noMatch, replayed, err)
	}
	ambiguous, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseID, ambiguousSnapshot.SnapshotID, SubjectSourceVerifyInput{SubjectID: exact.SubjectID, ExpectedSubjectVersion: 1}, "source-verify-ambiguous")
	if err != nil || replayed || ambiguous.State != "not_proposed" || ambiguous.MatchOutcome != "ambiguous" || ambiguous.RequestID != nil || ambiguous.MatchedRecord != nil {
		t.Fatalf("duplicate source claims did not return ambiguous without a proposal: result=%+v replay=%v err=%v", ambiguous, replayed, err)
	}
	newerManifest := `{"source_id":"synthetic.uscc.demo","source_version":2,"display_name":"演示受控主体目录","verification_scope":"synthetic_controlled","records":[{"record_id":"demo-a-v2","display_name":"受控示例公司","uscc":"91350100M000100Y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`
	if err := core.ConfigureSubjectSourceManifest([]byte(newerManifest)); err != nil {
		t.Fatal(err)
	}
	snapshot, replayed, err := core.CreateSubjectSourceSnapshot(ctx, admin, caseID, SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 2}, "source-verify-snapshot-v2")
	if err != nil || replayed || snapshot.SnapshotID == "" || snapshot.RecordCount != 1 {
		t.Fatalf("unique source snapshot creation failed: snapshot=%+v replay=%v err=%v", snapshot, replayed, err)
	}
	verified, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseID, snapshot.SnapshotID, SubjectSourceVerifyInput{SubjectID: exact.SubjectID, ExpectedSubjectVersion: 1}, "source-verify-exact")
	if err != nil || replayed || verified.State != "pending_decision" || verified.MatchOutcome != "exact_match" || verified.RequestID == nil || verified.MatchedRecord == nil || verified.SourceAsOf == nil || verified.ValidFrom == nil || verified.SourceScope != "synthetic_controlled" {
		t.Fatalf("exact source match failed to create a pending proposal: result=%+v replay=%v err=%v", verified, replayed, err)
	}
	verifyWire, _ := json.Marshal(verified)
	if strings.Contains(string(verifyWire), "91350100M000100Y43") || verified.MatchedRecord.MaskedIdentifier == "" {
		t.Fatalf("verify result exposed full identifier: %s", verifyWire)
	}
	newestManifest := strings.Replace(newerManifest, `"source_version":2`, `"source_version":3`, 1)
	if err := core.ConfigureSubjectSourceManifest([]byte(newestManifest)); err != nil {
		t.Fatal(err)
	}
	replay, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseID, snapshot.SnapshotID, SubjectSourceVerifyInput{SubjectID: exact.SubjectID, ExpectedSubjectVersion: 1}, "source-verify-exact")
	if err != nil || !replayed || replay.RequestID == nil || *replay.RequestID != *verified.RequestID {
		t.Fatalf("same request did not replay after source catalog update: result=%+v replay=%v err=%v", replay, replayed, err)
	}
	oldSnapshot, err := core.GetSubjectSourceSnapshot(ctx, admin, caseID, ambiguousSnapshot.SnapshotID)
	if err != nil || oldSnapshot.SourceVersion != 1 || oldSnapshot.SourceDigest != ambiguousSnapshot.SourceDigest || oldSnapshot.RecordCount != 3 {
		t.Fatalf("manifest update changed old frozen snapshot metadata: snapshot=%+v err=%v", oldSnapshot, err)
	}
	impact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseID, *verified.RequestID)
	if err != nil || !impact.CanDecide {
		t.Fatalf("independent decision actor lacks scope: %+v err=%v", impact, err)
	}
	decision, replayed, err := core.DecideSubjectChange(ctx, reviewer, caseID, *verified.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: impact.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "核对受控来源快照中名称与标识记录"}, "source-verify-decision")
	if err != nil || replayed || decision.Decision != "approved" || decision.AppliedSubjectVersion == nil || *decision.AppliedSubjectVersion != 2 {
		t.Fatalf("independent approval did not apply verified identity version: decision=%+v replay=%v err=%v", decision, replayed, err)
	}
	approvedState, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var oldVersion SubjectVersion
	for _, item := range approvedState.SubjectVersions {
		if item.SubjectID == exact.SubjectID && item.Version == 2 {
			oldVersion = item
		}
	}
	if oldVersion.VerificationScope != "synthetic_controlled" || len(oldVersion.IdentityClaims) != 1 || oldVersion.IdentityClaims[0].SourceKind != "synthetic_controlled_snapshot" || oldVersion.IdentityClaims[0].SourceSnapshotID != snapshot.SnapshotID || oldVersion.IdentityClaims[0].SourceTime == nil {
		t.Fatalf("approved source provenance was not frozen in the subject version: %+v", oldVersion)
	}
	latestSnapshot, replayed, err := core.CreateSubjectSourceSnapshot(ctx, admin, caseID, SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 3}, "source-verify-snapshot-v3")
	if err != nil || replayed || latestSnapshot.RecordCount != 1 {
		t.Fatalf("latest unique source snapshot creation failed: snapshot=%+v replay=%v err=%v", latestSnapshot, replayed, err)
	}
	conflictingNames := `{"source_id":"synthetic.uscc.demo","source_version":4,"display_name":"演示受控主体目录","verification_scope":"synthetic_controlled","records":[{"record_id":"demo-match","display_name":"受控示例公司","uscc":"91350100M000100Y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null},{"record_id":"demo-conflict","display_name":"另一条冲突名称","uscc":"91350100M000100Y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`
	if err := core.ConfigureSubjectSourceManifest([]byte(conflictingNames)); err != nil {
		t.Fatal(err)
	}
	conflictNamesSnapshot, replayed, err := core.CreateSubjectSourceSnapshot(ctx, admin, caseID, SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 4}, "source-verify-conflicting-names-snapshot")
	if err != nil || replayed || conflictNamesSnapshot.RecordCount != 2 {
		t.Fatalf("conflicting-name source snapshot failed: %+v replay=%v err=%v", conflictNamesSnapshot, replayed, err)
	}
	ambiguousByIdentifier, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseID, conflictNamesSnapshot.SnapshotID, SubjectSourceVerifyInput{SubjectID: conflicting.SubjectID, ExpectedSubjectVersion: 1}, "source-verify-conflicting-names")
	if err != nil || replayed || ambiguousByIdentifier.MatchOutcome != "ambiguous" || ambiguousByIdentifier.RequestID != nil || ambiguousByIdentifier.MatchedRecord != nil {
		t.Fatalf("matching name incorrectly disambiguated conflicting records sharing one identifier: result=%+v replay=%v err=%v", ambiguousByIdentifier, replayed, err)
	}
	conflictProposal, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseID, latestSnapshot.SnapshotID, SubjectSourceVerifyInput{SubjectID: conflicting.SubjectID, ExpectedSubjectVersion: 1}, "source-verify-conflict-proposal")
	if err != nil || replayed || conflictProposal.State != "pending_decision" || conflictProposal.RequestID == nil {
		t.Fatalf("second matching subject did not remain pending for independent decision: result=%+v replay=%v err=%v", conflictProposal, replayed, err)
	}
	conflictImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseID, *conflictProposal.RequestID)
	if err != nil || !conflictImpact.CanDecide {
		t.Fatalf("independent reviewer lacks conflict proposal scope: %+v err=%v", conflictImpact, err)
	}
	beforeConflictDecision, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	_, conflictReplayed, conflictErr := core.DecideSubjectChange(ctx, reviewer, caseID, *conflictProposal.RequestID,
		SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: conflictImpact.PayloadHash, ExpectedImpactETag: conflictImpact.ImpactETag, Reason: "复核同一受控来源身份"}, "source-verify-conflict-decision")
	var apiErr *fault.Error
	if !errors.As(conflictErr, &apiErr) || apiErr.Status != 409 || apiErr.Code != "conflict" || conflictReplayed || strings.Contains(conflictErr.Error(), "91350100") || strings.Contains(conflictErr.Error(), "受控示例公司") {
		t.Fatalf("verified identifier conflict was not a generic 409: replay=%v err=%v", conflictReplayed, conflictErr)
	}
	afterConflictDecision, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	conflictSubjectStillPending, foundConflictSubject := false, false
	for _, item := range afterConflictDecision.Subjects {
		if item.ID == conflicting.SubjectID {
			foundConflictSubject = true
			conflictSubjectStillPending = item.CurrentVersion == 1 && item.Status == "pending_verification"
		}
	}
	decisionStored, decisionKeyStored := false, false
	for _, item := range afterConflictDecision.SubjectChangeDecisions {
		decisionStored = decisionStored || item.RequestID == *conflictProposal.RequestID
	}
	for _, item := range afterConflictDecision.SubjectIdempotency {
		decisionKeyStored = decisionKeyStored || item.Operation == "decide_subject_change" && item.Key == "source-verify-conflict-decision"
	}
	if !foundConflictSubject || !conflictSubjectStillPending || decisionStored || decisionKeyStored ||
		len(afterConflictDecision.SubjectVersions) != len(beforeConflictDecision.SubjectVersions) ||
		len(afterConflictDecision.SubjectChangeDecisions) != len(beforeConflictDecision.SubjectChangeDecisions) ||
		len(afterConflictDecision.SubjectIdempotency) != len(beforeConflictDecision.SubjectIdempotency) {
		t.Fatalf("failed uniqueness decision partially committed: subject=%v decision=%v key=%v versions=%d/%d decisions=%d/%d keys=%d/%d",
			conflictSubjectStillPending, decisionStored, decisionKeyStored, len(beforeConflictDecision.SubjectVersions), len(afterConflictDecision.SubjectVersions),
			len(beforeConflictDecision.SubjectChangeDecisions), len(afterConflictDecision.SubjectChangeDecisions), len(beforeConflictDecision.SubjectIdempotency), len(afterConflictDecision.SubjectIdempotency))
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := env.openRole(t, "core")
	reloaded, err := reopened.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var storedSnapshot *SubjectSourceSnapshot
	for i := range reloaded.SubjectSourceSnapshots {
		item := &reloaded.SubjectSourceSnapshots[i]
		if item.SnapshotID == snapshot.SnapshotID {
			storedSnapshot = item
		}
	}
	if storedSnapshot == nil {
		t.Fatal("frozen source snapshot disappeared after reopening store")
	}
	digest := sha256.Sum256([]byte(storedSnapshot.SourcePayload))
	if hex.EncodeToString(digest[:]) != storedSnapshot.SourceDigest || storedSnapshot.SourceVersion != 2 || storedSnapshot.CapturedAt.IsZero() || storedSnapshot.CapturedAt.Location() != time.UTC {
		t.Fatalf("source snapshot bytes/provenance changed after reopening: %+v", storedSnapshot)
	}
	detail, err := reopened.GetSubjectSourceSnapshot(ctx, admin, caseID, snapshot.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	detailWire, _ := json.Marshal(detail)
	if strings.Contains(string(detailWire), "91350100M000100Y43") || strings.Contains(string(detailWire), "source_payload") {
		t.Fatalf("snapshot metadata endpoint disclosed raw source: %s", detailWire)
	}
}

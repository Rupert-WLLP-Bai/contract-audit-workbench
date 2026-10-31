package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestLegalApplicabilityRecordReplayResolveAndReopenInPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	execution := env.openRole(t, "execution")
	ctx := context.Background()
	caseID, err := core.CreateCase(admin, "法律适用事实持久化", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	buyer, _, err := core.CreateCaseSubject(ctx, admin, caseID, CaseSubjectCreateInput{
		Role: SubjectRoleCustomer, Subject: SubjectCreateInput{DisplayName: "事实验收采购人"},
	}, "legal-fact-party-create")
	if err != nil || buyer.BindingID == "" || buyer.SubjectID == "" {
		t.Fatalf("create buyer context for pinned legal fact: %+v err=%v", buyer, err)
	}
	const evidence = "本采购人属于企业，依据材料仅用于适用事实持久化验收。"
	basis := legalApplicabilityMaterialBasis(t, document, admin, caseID, "legal-fact-basis.txt", evidence, "legal-fact-basis-upload")
	buyer = activateLegalFactBuyerBinding(t, core, admin, caseID, buyer, basis, "legal-fact-party-activate")
	input := LegalApplicabilityFactInput{
		ExpectedVersion: 0, IdempotencyKey: "legal-fact-record-and-replay", Dimension: "buyer_kind", Status: "established", Value: "enterprise",
		TemporalBasis: "current_state", BasisNote: "按已冻结的案件材料确认买方类别", BasisRefs: basis,
		Party: LegalApplicabilityPartyPin{Side: "buyer", BindingID: buyer.BindingID, BindingVersion: buyer.BindingVersion,
			SubjectID: buyer.SubjectID, SubjectVersion: buyer.SubjectVersion, ScopeNote: "本案当前绑定的采购人"},
	}
	first, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, input)
	if err != nil {
		t.Fatalf("record pinned established legal fact: %v", err)
	}
	reordered := input
	reordered.BasisRefs, err = canonicalRawJSON(input.BasisRefs)
	if err != nil || string(reordered.BasisRefs) == string(input.BasisRefs) {
		t.Fatalf("test did not produce equivalent basis JSON with different key order: %s err=%v", reordered.BasisRefs, err)
	}
	replayed, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, reordered)
	if err != nil || !sameLegalFactWire(first, replayed) {
		t.Fatalf("same idempotency key did not return exact immutable version: same=%v err=%v first=%+v replay=%+v", sameLegalFactWire(first, replayed), err, first, replayed)
	}
	if first.BasisObtainedAt == nil {
		t.Fatal("server did not derive evidence acquisition time from the stored source material")
	}
	if _, err := execution.RecordLegalApplicabilityFact(ctx, admin, caseID, input); err == nil {
		t.Fatal("Execution service wrote a Core-owned legal applicability fact")
	}

	changed := input
	changed.Value = "natural_person"
	if _, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, changed); !isConflictFault(err) {
		t.Fatalf("same idempotency key accepted changed payload: %v", err)
	}
	changedAnchor := input
	var anchorInputs []materialAnchorInput
	if err := json.Unmarshal(input.BasisRefs, &anchorInputs); err != nil || len(anchorInputs) != 1 {
		t.Fatalf("decode exact source anchor for changed-payload case: anchors=%+v err=%v", anchorInputs, err)
	}
	anchorInputs[0].QuoteDigest = strings.Repeat("0", 64)
	changedAnchor.BasisRefs, err = json.Marshal(anchorInputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, changedAnchor); !isConflictFault(err) {
		t.Fatalf("same idempotency key accepted materially changed evidence anchor: %v", err)
	}
	staleCAS := input
	staleCAS.IdempotencyKey = "legal-fact-stale-cas"
	if _, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, staleCAS); !isConflictFault(err) {
		t.Fatalf("stale expected version created a second fact: %v", err)
	}

	businessAt := first.ValidFrom.Format(time.RFC3339Nano)
	cutoff := first.ValidFrom.Format(time.RFC3339Nano)
	resolved, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: businessAt, EvidenceCutoffAt: cutoff})
	if err != nil {
		t.Fatalf("resolve persisted legal fact: %v", err)
	}
	got := legalResolvedFact(t, resolved, "buyer_kind")
	if got.Status != "established" || got.Value != "enterprise" || got.Fact == nil || got.Fact.Version != 1 || got.Fact.Party != input.Party {
		t.Fatalf("resolved projection did not retain exact pinned immutable version: %+v", got)
	}
	beforeStart, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{
		BusinessAt: first.ValidFrom.Add(-time.Microsecond).Format(time.RFC3339Nano), EvidenceCutoffAt: cutoff,
	})
	if err != nil || legalResolvedFact(t, beforeStart, "buyer_kind").UnknownReason != "missing_fact" {
		t.Fatalf("fact became applicable before its inclusive start: facts=%+v err=%v", beforeStart.Facts, err)
	}
	beforeObservation, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{
		BusinessAt: businessAt, EvidenceCutoffAt: first.ValidFrom.Add(-time.Microsecond).Format(time.RFC3339Nano),
	})
	if err != nil || legalResolvedFact(t, beforeObservation, "buyer_kind").UnknownReason != "missing_fact" {
		t.Fatalf("current-state observation leaked past the evidence cutoff: facts=%+v err=%v", beforeObservation.Facts, err)
	}
	preciseInput := LegalApplicabilityFactInput{ExpectedVersion: 0, IdempotencyKey: "legal-fact-nanosecond-interval", Dimension: "project_kind",
		Status: "established", Value: "standalone_services", TemporalBasis: "historical_interval",
		ValidFrom: "2025-04-01T00:00:00.123456789Z", ValidUntil: "2025-06-01T00:00:00.987654321Z",
		BasisNote: "依据原文核对项目类型及其明确期间", BasisRefs: basis}
	precise, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, preciseInput)
	if err != nil {
		t.Fatalf("record nanosecond historical interval: %v", err)
	}
	preciseReplay, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, preciseInput)
	if err != nil || !sameLegalFactWire(precise, preciseReplay) || precise.ValidFrom.Nanosecond()%1000 != 0 || precise.ValidUntil == nil || precise.ValidUntil.Nanosecond()%1000 != 0 {
		t.Fatalf("nanosecond interval first response/replay was not canonical at PostgreSQL precision: first=%+v replay=%+v err=%v", precise, preciseReplay, err)
	}
	resolved, err = core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: businessAt, EvidenceCutoffAt: cutoff})
	if err != nil {
		t.Fatalf("resolve full persisted fact set before close/reopen: %v", err)
	}

	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	coreAfterReopen := env.openRole(t, "core")
	reopenedReplay, err := coreAfterReopen.RecordLegalApplicabilityFact(ctx, admin, caseID, input)
	if err != nil || !sameLegalFactWire(first, reopenedReplay) {
		t.Fatalf("close/reopen replay changed immutable fact bytes: same=%v err=%v", sameLegalFactWire(first, reopenedReplay), err)
	}
	preciseAfterReopen, err := coreAfterReopen.RecordLegalApplicabilityFact(ctx, admin, caseID, preciseInput)
	if err != nil || !sameLegalFactWire(precise, preciseAfterReopen) {
		t.Fatalf("close/reopen changed historical nanosecond-input replay bytes: same=%v err=%v", sameLegalFactWire(precise, preciseAfterReopen), err)
	}
	reopenedResolved, err := coreAfterReopen.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: businessAt, EvidenceCutoffAt: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if !sameLegalFactSetWire(resolved, reopenedResolved) {
		t.Fatalf("close/reopen changed resolved fact-set bytes: before=%+v after=%+v", resolved, reopenedResolved)
	}

	// The review time and the material acquisition time remain distinct: this
	// cutoff includes the original evidence and the current-state observation.
	if first.BasisObtainedAt == nil || first.CreatedAt.Before(*first.BasisObtainedAt) || first.CreatedAt.Before(first.ValidFrom) {
		t.Fatalf("server-derived evidence/recording timestamps are inconsistent: %+v", first)
	}
	assertLegalApplicabilityRoleBoundaries(t, env, admin, execution, first)
}

func TestLegalApplicabilityConcurrentIdempotencyAndCASTransitionsInPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	coreA, coreB := env.openRole(t, "core"), env.openRole(t, "core")
	caseID, err := coreA.CreateCase(admin, "法律适用事实并发CAS", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := LegalApplicabilityFactInput{ExpectedVersion: 0, IdempotencyKey: "legal-fact-concurrent-same-key", Dimension: "transaction_kind",
		Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "现有材料不足，等待进一步核验"}
	type outcome struct {
		fact LegalApplicabilityFactVersion
		err  error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{coreA, coreB} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			<-start
			fact, err := st.RecordLegalApplicabilityFact(ctx, admin, caseID, base)
			results <- outcome{fact: fact, err: err}
		}(st)
	}
	close(start)
	wg.Wait()
	close(results)
	var sameKey []LegalApplicabilityFactVersion
	for got := range results {
		if got.err != nil {
			t.Fatalf("concurrent identical idempotent write failed: %v", got.err)
		}
		sameKey = append(sameKey, got.fact)
	}
	if len(sameKey) != 2 || sameKey[0].Version != 1 || sameKey[1].Version != 1 || !sameLegalFactWire(sameKey[0], sameKey[1]) {
		t.Fatalf("same-key concurrent writes did not return one exact immutable version: %+v", sameKey)
	}

	start = make(chan struct{})
	results = make(chan outcome, 2)
	for i, st := range []*Store{coreA, coreB} {
		input := base
		input.ExpectedVersion = 1
		input.IdempotencyKey = []string{"legal-fact-concurrent-cas-a", "legal-fact-concurrent-cas-b"}[i]
		input.BasisNote = []string{"现有材料仍不足，等待核验A", "现有材料仍不足，等待核验B"}[i]
		wg.Add(1)
		go func(st *Store, input LegalApplicabilityFactInput) {
			defer wg.Done()
			<-start
			fact, err := st.RecordLegalApplicabilityFact(ctx, admin, caseID, input)
			results <- outcome{fact: fact, err: err}
		}(st, input)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for got := range results {
		if got.err == nil && got.fact.Version == 2 {
			succeeded++
		} else if isConflictFault(got.err) {
			conflicted++
		} else {
			t.Fatalf("different-key concurrent stale CAS had unexpected result: fact=%+v err=%v", got.fact, got.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("different-key concurrent CAS results = success:%d conflict:%d, want one each", succeeded, conflicted)
	}
	resolved, err := coreA.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got := legalResolvedFact(t, resolved, "transaction_kind"); got.Fact == nil || got.Fact.Version != 2 {
		t.Fatalf("concurrent CAS left an extra or missing head version: %+v", got)
	}
	tx, err := coreA.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := userScope(admin)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	var versions int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND case_id=$2 AND dimension='transaction_kind'`, accounts.Tenant(admin), caseID).Scan(&versions); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if versions != 2 {
		_ = tx.Rollback(ctx)
		t.Fatalf("concurrent same-key/CAS writes left %d immutable versions, want exactly 2", versions)
	}
	foreign, ok := accounts.ByID("demo-tenant2-viewer")
	if !ok {
		_ = tx.Rollback(ctx)
		t.Fatal("foreign tenant viewer missing")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	foreignTx, err := coreA.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foreignScope := userScope(foreign)
	foreignScope.CaseID = caseID
	if err := setLocalScope(ctx, foreignTx, foreignScope); err != nil {
		_ = foreignTx.Rollback(ctx)
		t.Fatal(err)
	}
	var foreignRows int
	if err := foreignTx.QueryRow(ctx, `SELECT count(*) FROM case_legal_applicability_fact_versions WHERE case_id=$1`, caseID).Scan(&foreignRows); err != nil {
		_ = foreignTx.Rollback(ctx)
		t.Fatal(err)
	}
	if foreignRows != 0 {
		_ = foreignTx.Rollback(ctx)
		t.Fatalf("foreign tenant RLS exposed %d applicability fact versions", foreignRows)
	}
	if err := foreignTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLegalApplicabilityRechecksReadAuthorizationAndArchivedWritesInPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("case viewer missing")
	}
	core := env.openRole(t, "core")
	caseID, err := core.CreateCase(admin, "法律适用授权撤回与归档", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddMember(admin, caseID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	input := LegalApplicabilityFactInput{ExpectedVersion: 0, IdempotencyKey: "legal-fact-authz-revocation", Dimension: "funding_kind",
		Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "尚无材料证明资金性质"}
	if _, err := core.RecordLegalApplicabilityFact(context.Background(), admin, caseID, input); err != nil {
		t.Fatal(err)
	}
	viewerWrite := input
	viewerWrite.IdempotencyKey = "legal-fact-read-only-member-write"
	requireKnowledgeFault(t, func() error {
		_, err := core.RecordLegalApplicabilityFact(context.Background(), viewer, caseID, viewerWrite)
		return err
	}(), 403)
	if _, err := core.ResolveLegalApplicabilityFacts(context.Background(), viewer, caseID, LegalApplicabilityResolutionInput{}); err != nil {
		t.Fatalf("read-authorized case member could not resolve facts: %v", err)
	}
	if err := core.updateCaseAs(admin, caseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseID {
				members := st.Cases[i].MemberIDs[:0]
				for _, member := range st.Cases[i].MemberIDs {
					if member != viewer.ID {
						members = append(members, member)
					}
				}
				st.Cases[i].MemberIDs = append([]string(nil), members...)
				return nil
			}
		}
		return errors.New("legal fact authorization fixture case missing")
	}); err != nil {
		t.Fatalf("revoke viewer case membership fixture: %v", err)
	}
	if _, err := core.ResolveLegalApplicabilityFacts(context.Background(), viewer, caseID, LegalApplicabilityResolutionInput{}); err == nil {
		t.Fatal("viewer continued reading legal applicability facts after case authorization was revoked")
	}
	if err := core.updateCaseAs(admin, caseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseID {
				archivedAt := stamp()
				st.Cases[i].BusinessStatus = "archived"
				st.Cases[i].ArchivedAt = &archivedAt
				return nil
			}
		}
		return errors.New("legal fact archive fixture case missing")
	}); err != nil {
		t.Fatalf("archive case fixture: %v", err)
	}
	input.ExpectedVersion = 1
	input.IdempotencyKey = "legal-fact-archived-new-write"
	if _, err := core.RecordLegalApplicabilityFact(context.Background(), admin, caseID, input); !isConflictFault(err) {
		t.Fatalf("archived case accepted new legal applicability fact: %v", err)
	}
}

func TestLegalApplicabilityHistoricalTimelineAndPartyCorrectionInPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	ctx := context.Background()
	caseID, err := core.CreateCase(admin, "法律适用历史区间与主体固定", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	buyer, _, err := core.CreateCaseSubject(ctx, admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleCustomer, Subject: SubjectCreateInput{DisplayName: "历史事实采购人"}}, "legal-fact-history-party")
	if err != nil {
		t.Fatal(err)
	}
	const evidence = "本原件载明采购人主体类别与合同期间，作为历史适用事实依据。"
	basis := legalApplicabilityMaterialBasis(t, document, admin, caseID, "legal-fact-history.txt", evidence, "legal-fact-history-upload")
	buyer = activateLegalFactBuyerBinding(t, core, admin, caseID, buyer, basis, "legal-fact-history-party-activate")
	party := LegalApplicabilityPartyPin{Side: "buyer", BindingID: buyer.BindingID, BindingVersion: buyer.BindingVersion, SubjectID: buyer.SubjectID,
		SubjectVersion: buyer.SubjectVersion, ScopeNote: "本案历史期间的采购人"}
	write := func(expected int64, key, from, until, value string) LegalApplicabilityFactVersion {
		t.Helper()
		input := LegalApplicabilityFactInput{ExpectedVersion: expected, IdempotencyKey: key, Dimension: "buyer_kind", Status: "established", Value: value,
			TemporalBasis: "historical_interval", ValidFrom: from, ValidUntil: until, BasisNote: "依据原件记载核对历史期间", BasisRefs: basis, Party: party}
		fact, err := core.RecordLegalApplicabilityFact(ctx, admin, caseID, input)
		if err != nil {
			t.Fatalf("append historical fact %d: %v", expected+1, err)
		}
		return fact
	}
	write(0, "legal-fact-jan-v1", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "enterprise")
	write(1, "legal-fact-july-v2", "2025-07-01T00:00:00Z", "2025-08-01T00:00:00Z", "institution")
	write(2, "legal-fact-jan-correction-v3", "2025-01-01T00:00:00Z", "2025-07-01T00:00:00Z", "natural_person")
	history1, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1})
	if err != nil || !history1.CanManage || history1.CurrentVersion != 3 || len(history1.Items) != 1 || history1.Items[0].Version != 3 || history1.NextCursor == nil {
		t.Fatalf("history page 1 did not return latest immutable version and current CAS head: history=%+v err=%v", history1, err)
	}
	history2, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1, Cursor: *history1.NextCursor})
	if err != nil || history2.CurrentVersion != 3 || len(history2.Items) != 1 || history2.Items[0].Version != 2 || history2.NextCursor == nil {
		t.Fatalf("history page 2 repeated/skipped an immutable version: history=%+v err=%v", history2, err)
	}
	history3, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1, Cursor: *history2.NextCursor})
	if err != nil || len(history3.Items) != 1 || history3.Items[0].Version != 1 || history3.NextCursor != nil {
		t.Fatalf("history final page did not end at the oldest version: history=%+v err=%v", history3, err)
	}
	if _, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "seller_kind", LegalApplicabilityHistoryInput{Limit: 1, Cursor: *history1.NextCursor}); err == nil {
		t.Fatal("history cursor was accepted for a different dimension")
	}
	otherCase, err := core.CreateCase(admin, "法律事实历史游标案件绑定", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ListLegalApplicabilityFactHistory(ctx, admin, otherCase, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1, Cursor: *history1.NextCursor}); err == nil {
		t.Fatal("history cursor was accepted for a different case")
	}
	emptyHistory, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "funding_kind", LegalApplicabilityHistoryInput{})
	if err != nil || emptyHistory.CurrentVersion != 0 || len(emptyHistory.Items) != 0 || emptyHistory.NextCursor != nil {
		t.Fatalf("history fabricated an entry for an unrecorded dimension: %+v err=%v", emptyHistory, err)
	}
	if _, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 101}); err == nil {
		t.Fatal("history accepted a page size larger than 100")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	readOnlyHistory, err := core.ListLegalApplicabilityFactHistory(ctx, reviewer, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1})
	if err != nil || readOnlyHistory.CanManage {
		t.Fatalf("case Read role did not receive the correct manage affordance: history=%+v err=%v", readOnlyHistory, err)
	}
	core.UseReadOnly(true)
	readOnlyStoreHistory, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1})
	core.UseReadOnly(false)
	if err != nil || readOnlyStoreHistory.CanManage {
		t.Fatalf("read-only store exposed a writable history affordance: history=%+v err=%v", readOnlyStoreHistory, err)
	}
	cutoff := time.Now().UTC().Format(time.RFC3339Nano)
	jan, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-03-01T00:00:00Z", EvidenceCutoffAt: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if got := legalResolvedFact(t, jan, "buyer_kind"); got.Fact == nil || got.Fact.Version != 3 || got.Value != "natural_person" {
		t.Fatalf("later January correction did not apply to January: %+v", got)
	}
	july, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-07-15T00:00:00Z", EvidenceCutoffAt: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if got := legalResolvedFact(t, july, "buyer_kind"); got.Fact == nil || got.Fact.Version != 2 || got.Value != "institution" {
		t.Fatalf("January correction incorrectly replaced the July interval: %+v", got)
	}
	julyStart, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-07-01T00:00:00Z", EvidenceCutoffAt: cutoff})
	if err != nil || legalResolvedFact(t, julyStart, "buyer_kind").Value != "institution" {
		t.Fatalf("historical interval start was not inclusive: %+v err=%v", julyStart.Facts, err)
	}
	augustEnd, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-08-01T00:00:00Z", EvidenceCutoffAt: cutoff})
	if err != nil || legalResolvedFact(t, augustEnd, "buyer_kind").UnknownReason != "expired_basis" {
		t.Fatalf("historical interval end was not exclusive or fell back to older data: %+v err=%v", augustEnd.Facts, err)
	}
	september, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-09-01T00:00:00Z", EvidenceCutoffAt: cutoff})
	if err != nil || legalResolvedFact(t, september, "buyer_kind").UnknownReason != "expired_basis" {
		t.Fatalf("expired newer basis fell back to an older fact: %+v err=%v", september.Facts, err)
	}
	tooEarly, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-03-01T00:00:00Z", EvidenceCutoffAt: "2025-06-01T00:00:00Z"})
	if err != nil || legalResolvedFact(t, tooEarly, "buyer_kind").UnknownReason != "missing_fact" {
		t.Fatalf("historical fact with later-acquired evidence crossed its cutoff: %+v err=%v", tooEarly.Facts, err)
	}

	proposal, replayed, err := core.ProposeSubjectChange(ctx, admin, caseID, SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: buyer.SubjectID},
		ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"display_name":"历史采购人更正"}`), BasisNote: "按原件更正主体登记", BasisRefs: basis}, "legal-fact-party-correction-propose")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("propose source-backed party correction: %+v replay=%v err=%v", proposal, replayed, err)
	}
	reviewer, ok = accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	impact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseID, proposal.RequestID)
	if err != nil || !impact.CanDecide {
		t.Fatalf("reviewer cannot decide party correction: %+v err=%v", impact, err)
	}
	if _, _, err := core.DecideSubjectChange(ctx, reviewer, caseID, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "复核并核准主体更正"}, "legal-fact-party-correction-approve"); err != nil {
		t.Fatalf("approve source-backed party correction: %v", err)
	}
	afterCorrection, err := core.ResolveLegalApplicabilityFacts(ctx, admin, caseID, LegalApplicabilityResolutionInput{BusinessAt: "2025-03-01T00:00:00Z", EvidenceCutoffAt: cutoff})
	if err != nil || legalResolvedFact(t, afterCorrection, "buyer_kind").UnknownReason != "stale_party_context" {
		t.Fatalf("fact silently migrated to corrected party identity: %+v err=%v", afterCorrection.Facts, err)
	}
	if err := core.updateCaseAs(admin, caseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseID {
				archivedAt := stamp()
				st.Cases[i].BusinessStatus = "archived"
				st.Cases[i].ArchivedAt = &archivedAt
				return nil
			}
		}
		return errors.New("legal applicability history case missing")
	}); err != nil {
		t.Fatalf("archive legal applicability case fixture: %v", err)
	}
	archivedHistory, err := core.ListLegalApplicabilityFactHistory(ctx, admin, caseID, "buyer_kind", LegalApplicabilityHistoryInput{Limit: 1})
	if err != nil || archivedHistory.CanManage || len(archivedHistory.Items) != 1 {
		t.Fatalf("archived case should remain readable without a write affordance: history=%+v err=%v", archivedHistory, err)
	}
}

func activateLegalFactBuyerBinding(t *testing.T, core *Store, admin accounts.User, caseID string, buyer CreateCaseSubjectResult, basis json.RawMessage, key string) CreateCaseSubjectResult {
	t.Helper()
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatalf("grant reviewer case scope for separation-of-duties binding confirmation: %v", err)
	}
	expectedBindingVersion := buyer.BindingVersion
	proposal, replayed, err := core.ProposeSubjectChange(context.Background(), admin, caseID, SubjectChangeCreateInput{
		Target:                 SubjectChangeTarget{Kind: "case_subject_role", SubjectID: buyer.SubjectID, BindingID: buyer.BindingID},
		ExpectedSubjectVersion: buyer.SubjectVersion, ExpectedBindingVersion: &expectedBindingVersion,
		Patch:     json.RawMessage(`{"binding_state":"active","role":"customer"}`),
		BasisNote: "依据本案原始材料由独立复核人确认买方关联", BasisRefs: basis,
	}, key+"-proposal")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("propose binding confirmation through separation-of-duties workflow: proposal=%+v replay=%v err=%v", proposal, replayed, err)
	}
	impact, err := core.GetSubjectChangeImpact(context.Background(), reviewer, caseID, proposal.RequestID)
	if err != nil || !impact.CanDecide {
		t.Fatalf("independent reviewer cannot decide binding confirmation: impact=%+v err=%v", impact, err)
	}
	decision, replayed, err := core.DecideSubjectChange(context.Background(), reviewer, caseID, proposal.RequestID, SubjectChangeDecisionInput{
		Decision: "approve", ExpectedSubjectVersion: buyer.SubjectVersion, ExpectedBindingVersion: &expectedBindingVersion,
		ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "复核原始材料并确认案件买方关联",
	}, key+"-approve")
	if err != nil || replayed || decision.Decision != "approved" || decision.BindingVersionsAppended != 1 {
		t.Fatalf("independent reviewer did not confirm binding: decision=%+v replay=%v err=%v", decision, replayed, err)
	}
	listed, err := core.ListCaseSubjects(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range listed.Items {
		if current.BindingID == buyer.BindingID {
			if current.BindingState != "active" || current.Role != SubjectRoleCustomer {
				t.Fatalf("reviewed buyer binding did not become active: %+v", current)
			}
			return CreateCaseSubjectResult{BindingID: current.BindingID, BindingVersion: current.BindingVersion, SubjectID: current.Subject.SubjectID,
				SubjectVersion: current.Subject.Version, Role: current.Role, BindingState: current.BindingState}
		}
	}
	t.Fatalf("confirmed buyer binding %s missing from current role head", buyer.BindingID)
	return CreateCaseSubjectResult{}
}

func legalApplicabilityMaterialBasis(t *testing.T, documentStore *Store, user accounts.User, caseID, filename, text, key string) json.RawMessage {
	t.Helper()
	upload, err := documentStore.UploadMaterial(context.Background(), user, caseID, "attachment", filename, "text/plain", []byte(text), MaterialUploadOptions{IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	state, err := documentStore.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	var source Document
	for _, item := range state.Documents {
		if item.ID == upload.DocumentID {
			source = item
			break
		}
	}
	if source.ID == "" {
		t.Fatal("uploaded legal applicability evidence document missing")
	}
	parsed, err := documentStore.ensureParsed(context.Background(), user, source, false)
	if err != nil || parsed.Status != "ready" {
		t.Fatalf("parse legal applicability evidence: status=%s err=%v", parsed.Status, err)
	}
	quote := []rune(parsed.Text)
	digest := sha256.Sum256([]byte(string(quote)))
	refs, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: upload.MaterialID, MaterialVersion: upload.Version,
		DocumentID: upload.DocumentID, ParsedDocumentID: parsed.ID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(digest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func sameLegalFactWire(a, b LegalApplicabilityFactVersion) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && reflect.DeepEqual(left, right)
}

func sameLegalFactSetWire(a, b LegalApplicabilityFactSet) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && reflect.DeepEqual(left, right)
}

func legalResolvedFact(t *testing.T, set LegalApplicabilityFactSet, dimension string) LegalApplicabilityResolvedFact {
	t.Helper()
	for _, item := range set.Facts {
		if item.Dimension == dimension {
			return item
		}
	}
	t.Fatalf("resolution omitted expected dimension %q", dimension)
	return LegalApplicabilityResolvedFact{}
}

func assertLegalApplicabilityRoleBoundaries(t *testing.T, env *durableRoleEnvironment, admin accounts.User, execution *Store, fact LegalApplicabilityFactVersion) {
	t.Helper()
	ctx := context.Background()
	if _, err := execution.pool.Exec(ctx, `INSERT INTO case_legal_applicability_fact_versions SELECT * FROM case_legal_applicability_fact_versions LIMIT 1`); pgErrorCode(err) != "42501" {
		t.Fatalf("Execution role INSERT was not denied by PostgreSQL privileges (SQLSTATE 42501): %v", err)
	}
	for _, operation := range []string{"UPDATE", "DELETE"} {
		tx, err := env.harness.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		scope := userScope(admin)
		scope.CaseID = fact.CaseID
		if err := setLocalScope(ctx, tx, scope); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		var statement string
		if operation == "UPDATE" {
			statement = `UPDATE case_legal_applicability_fact_versions SET basis_note='rewritten' WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND fact_id=$4 AND version=$5`
		} else {
			statement = `DELETE FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND fact_id=$4 AND version=$5`
		}
		_, err = tx.Exec(ctx, statement, fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID, fact.Version)
		if pgErrorCode(err) != "P0001" {
			_ = tx.Rollback(ctx)
			t.Fatalf("owner %s bypassed legal fact append-only trigger (SQLSTATE P0001): %v", operation, err)
		}
		_ = tx.Rollback(ctx)
	}
	tx, err := env.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := userScope(admin)
	scope.CaseID = fact.CaseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO case_legal_applicability_fact_versions
		(tenant_id,org_id,case_id,fact_id,version,dimension,status,value,unknown_reason,temporal_basis,valid_from,valid_until,basis_note,basis_refs,basis_sha256,basis_obtained_at,
		 party_side,party_binding_id,party_binding_version,party_subject_id,party_subject_version,party_scope_note,request_digest,idempotency_key,actor_user_id,actor_issuer,actor_subject,created_at)
		SELECT tenant_id,org_id,case_id,fact_id,version+1,dimension,status,value,unknown_reason,temporal_basis,valid_from,valid_until,basis_note,basis_refs,basis_sha256,basis_obtained_at,
		 party_side,'missing-binding-for-fk',party_binding_version,party_subject_id,party_subject_version,party_scope_note,request_digest,'fk-mismatch-attempt',actor_user_id,actor_issuer,actor_subject,created_at
		FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND fact_id=$4 AND version=$5`,
		fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID, fact.Version)
	if pgErrorCode(err) != "23503" {
		_ = tx.Rollback(ctx)
		t.Fatalf("mismatched buyer binding/subject pin did not fail its composite foreign key (SQLSTATE 23503): %v", err)
	}
	_ = tx.Rollback(ctx)

	columns := []string{"tenant_id", "org_id", "case_id", "fact_id", "version", "dimension", "status", "value", "unknown_reason", "temporal_basis",
		"valid_from", "valid_until", "basis_note", "basis_refs", "basis_sha256", "basis_obtained_at", "party_side", "party_binding_id",
		"party_binding_version", "party_subject_id", "party_subject_version", "party_scope_note", "request_digest", "idempotency_key", "actor_user_id",
		"actor_issuer", "actor_subject", "created_at"}
	expressions := []string{"source.tenant_id", "source.org_id", "source.case_id", "source.fact_id", "source.version+1", "source.dimension", "source.status",
		"source.value", "source.unknown_reason", "source.temporal_basis", "source.valid_from", "source.valid_until", "source.basis_note", "source.basis_refs",
		"source.basis_sha256", "source.basis_obtained_at", "source.party_side", "source.party_binding_id", "source.party_binding_version",
		"source.party_subject_id", "source.party_subject_version", "source.party_scope_note", "source.request_digest", "source.idempotency_key || '-' || $6",
		"source.actor_user_id", "source.actor_issuer", "source.actor_subject", "source.created_at"}
	for _, mutation := range []struct {
		name       string
		column     string
		expression string
	}{
		{name: "party_side null", column: "party_side", expression: "NULL::text"},
		{name: "party_binding_id null", column: "party_binding_id", expression: "NULL::text"},
		{name: "party_binding_version null", column: "party_binding_version", expression: "NULL::bigint"},
		{name: "party_binding_version zero", column: "party_binding_version", expression: "0"},
		{name: "party_subject_id null", column: "party_subject_id", expression: "NULL::text"},
		{name: "party_subject_version null", column: "party_subject_version", expression: "NULL::bigint"},
		{name: "party_subject_version zero", column: "party_subject_version", expression: "0"},
		{name: "party_scope_note null", column: "party_scope_note", expression: "NULL::text"},
		{name: "party_scope_note empty", column: "party_scope_note", expression: "''"},
	} {
		selectExpr := append([]string(nil), expressions...)
		for i, column := range columns {
			if column == mutation.column {
				selectExpr[i] = mutation.expression
			}
		}
		statement := `INSERT INTO case_legal_applicability_fact_versions (` + strings.Join(columns, ",") + `)
			SELECT ` + strings.Join(selectExpr, ",") + ` FROM case_legal_applicability_fact_versions source
			WHERE source.tenant_id=$1 AND source.org_id=$2 AND source.case_id=$3 AND source.fact_id=$4 AND source.version=$5`
		tx, err := env.harness.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		scope := userScope(admin)
		scope.CaseID = fact.CaseID
		if err := setLocalScope(ctx, tx, scope); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, statement, fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID, fact.Version, strings.ReplaceAll(mutation.name, " ", "-"))
		if pgErrorCode(err) != "23514" {
			_ = tx.Rollback(ctx)
			t.Fatalf("partial/invalid party pin %q was not rejected by the explicit completeness constraint (SQLSTATE 23514): %v", mutation.name, err)
		}
		_ = tx.Rollback(ctx)
	}
}

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestLegalApplicabilitySelectsLatestEffectiveStartNotLatestAppend(t *testing.T) {
	jan1 := legalTime("2025-01-01T00:00:00Z")
	jul1 := legalTime("2025-07-01T00:00:00Z")
	aug1 := legalTime("2025-08-01T00:00:00Z")
	versions := []LegalApplicabilityFactVersion{
		legalFactVersion(1, "historical_interval", "2025-01-01T00:00:00Z", "", "state_organ", jan1, "2025-01-01T00:00:00Z"),
		legalFactVersion(2, "historical_interval", "2025-07-01T00:00:00Z", "", "institution", jul1, "2025-07-01T00:00:00Z"),
		// Version 3 is recorded later, but corrects only the January interval.
		legalFactVersion(3, "historical_interval", "2025-01-01T00:00:00Z", "", "enterprise", aug1, "2025-08-01T00:00:00Z"),
	}

	jan := selectLegalApplicabilityFactVersion(3, versions, legalTime("2025-03-01T00:00:00Z"), legalTime("2025-09-01T00:00:00Z"))
	if jan.UnknownReason != "" || jan.Version.Version != 3 || jan.Version.Value != "enterprise" {
		t.Fatalf("January should select retroactive version 3: %+v", jan)
	}
	july := selectLegalApplicabilityFactVersion(3, versions, legalTime("2025-07-15T00:00:00Z"), legalTime("2025-09-01T00:00:00Z"))
	if july.UnknownReason != "" || july.Version.Version != 2 || july.Version.Value != "institution" {
		t.Fatalf("July should retain version 2 despite later January correction: %+v", july)
	}
	beforeCorrection := selectLegalApplicabilityFactVersion(3, versions, legalTime("2025-03-01T00:00:00Z"), legalTime("2025-07-15T00:00:00Z"))
	if beforeCorrection.UnknownReason != "" || beforeCorrection.Version.Version != 1 {
		t.Fatalf("a correction recorded after the fact cutoff leaked into the earlier snapshot: %+v", beforeCorrection)
	}
}

func TestLegalApplicabilitySelectionUsesExclusiveEndAndDoesNotFallback(t *testing.T) {
	versions := []LegalApplicabilityFactVersion{
		legalFactVersion(1, "historical_interval", "2025-01-01T00:00:00Z", "2025-07-01T00:00:00Z", "state_organ", legalTime("2025-01-01T00:00:00Z"), "2025-01-01T00:00:00Z"),
		legalFactVersion(2, "historical_interval", "2025-07-01T00:00:00Z", "2025-09-01T00:00:00Z", "institution", legalTime("2025-07-01T00:00:00Z"), "2025-07-01T00:00:00Z"),
	}
	start := selectLegalApplicabilityFactVersion(2, versions, legalTime("2025-07-01T00:00:00Z"), legalTime("2025-10-01T00:00:00Z"))
	if start.UnknownReason != "" || start.Version.Version != 2 {
		t.Fatalf("valid_from is inclusive: %+v", start)
	}
	expired := selectLegalApplicabilityFactVersion(2, versions, legalTime("2025-10-01T00:00:00Z"), legalTime("2025-10-01T00:00:00Z"))
	if expired.UnknownReason != "expired_basis" || expired.Version.Version != 2 {
		t.Fatalf("expired selected version should be unknown without falling back to version 1: %+v", expired)
	}
}

func TestLegalApplicabilitySelectionHonorsHeadAndEvidenceCutoff(t *testing.T) {
	versions := []LegalApplicabilityFactVersion{
		legalFactVersion(1, "historical_interval", "2025-01-01T00:00:00Z", "", "state_organ", legalTime("2025-01-01T00:00:00Z"), "2025-01-01T00:00:00Z"),
		legalFactVersion(2, "historical_interval", "2025-01-01T00:00:00Z", "", "enterprise", legalTime("2025-09-01T00:00:00Z"), "2025-09-01T00:00:00Z"),
	}
	byHead := selectLegalApplicabilityFactVersion(1, versions, legalTime("2025-04-01T00:00:00Z"), legalTime("2025-10-01T00:00:00Z"))
	if byHead.UnknownReason != "" || byHead.Version.Version != 1 {
		t.Fatalf("version above head current_version was selected: %+v", byHead)
	}
	byCutoff := selectLegalApplicabilityFactVersion(2, versions, legalTime("2025-04-01T00:00:00Z"), legalTime("2025-08-01T00:00:00Z"))
	if byCutoff.UnknownReason != "" || byCutoff.Version.Version != 1 {
		t.Fatalf("later-recorded historical evidence crossed an earlier fact cutoff: %+v", byCutoff)
	}
}

func TestLegalApplicabilityCutoffUsesEvidenceAcquisitionNotReviewTime(t *testing.T) {
	cutoff := legalTime("2025-06-01T00:00:00Z")
	validAt := legalTime("2025-05-01T00:00:00Z")
	obtainedInMay := legalTime("2025-05-15T00:00:00Z")
	reviewedInOctober := legalTime("2025-10-01T00:00:00Z")
	backfilledReview := legalFactVersion(1, "historical_interval", "2025-05-01T00:00:00Z", "2025-12-01T00:00:00Z", "enterprise", reviewedInOctober, "2025-05-15T00:00:00Z")
	backfilledReview.BasisObtainedAt = &obtainedInMay
	visible := selectLegalApplicabilityFactVersion(1, []LegalApplicabilityFactVersion{backfilledReview}, validAt, cutoff)
	if visible.UnknownReason != "" || visible.Version.Version != 1 {
		t.Fatalf("evidence obtained before cutoff must remain usable when reviewed later: %+v", visible)
	}

	obtainedInOctober := legalTime("2025-10-02T00:00:00Z")
	lateEvidence := legalFactVersion(1, "historical_interval", "2025-05-01T00:00:00Z", "2025-12-01T00:00:00Z", "enterprise", reviewedInOctober, "2025-10-02T00:00:00Z")
	lateEvidence.BasisObtainedAt = &obtainedInOctober
	hidden := selectLegalApplicabilityFactVersion(1, []LegalApplicabilityFactVersion{lateEvidence}, validAt, cutoff)
	if hidden.UnknownReason != "missing_fact" {
		t.Fatalf("evidence obtained after cutoff must be excluded despite backdated business validity: %+v", hidden)
	}
}

func TestLegalApplicabilityCurrentStateCutoffIncludesObservationTime(t *testing.T) {
	cutoff := legalTime("2025-06-01T00:00:00Z")
	createdInOctober := legalTime("2025-10-01T00:00:00Z")
	evidenceFromMay := legalTime("2025-05-15T00:00:00Z")
	currentObservation := legalFactVersion(1, "current_state", "2025-10-01T00:00:00Z", "", "enterprise", createdInOctober, "2025-05-15T00:00:00Z")
	currentObservation.BasisObtainedAt = &evidenceFromMay
	selected := selectLegalApplicabilityFactVersion(1, []LegalApplicabilityFactVersion{currentObservation}, createdInOctober, cutoff)
	if selected.UnknownReason != "missing_fact" {
		t.Fatalf("later current-state observation cannot leak across earlier cutoff: %+v", selected)
	}

	historical := legalFactVersion(1, "historical_interval", "2025-05-01T00:00:00Z", "2025-12-01T00:00:00Z", "enterprise", createdInOctober, "2025-05-15T00:00:00Z")
	historical.BasisObtainedAt = &evidenceFromMay
	selected = selectLegalApplicabilityFactVersion(1, []LegalApplicabilityFactVersion{historical}, legalTime("2025-05-20T00:00:00Z"), cutoff)
	if selected.UnknownReason != "" {
		t.Fatalf("historical interval can use pre-cutoff evidence despite later review: %+v", selected)
	}
}

func TestLegalApplicabilityCurrentStateCannotApplyBeforeEstablishment(t *testing.T) {
	version := legalFactVersion(1, "current_state", "2025-01-01T00:00:00Z", "", "enterprise", legalTime("2025-01-10T00:00:00Z"), "2025-01-10T00:00:00Z")
	before := selectLegalApplicabilityFactVersion(1, []LegalApplicabilityFactVersion{version}, legalTime("2025-01-09T23:59:59Z"), legalTime("2025-01-11T00:00:00Z"))
	if before.UnknownReason != "missing_fact" {
		t.Fatalf("current-state fact must not be backdated before its server establishment: %+v", before)
	}
	known := selectLegalApplicabilityFactVersion(1, []LegalApplicabilityFactVersion{version}, legalTime("2025-01-10T00:00:00Z"), legalTime("2025-01-11T00:00:00Z"))
	if known.UnknownReason != "" || known.Version.Value != "enterprise" {
		t.Fatalf("current-state fact should be available at establishment instant: %+v", known)
	}
}

func TestLegalApplicabilityInputKeepsServerTimeAndNullableEvidenceTime(t *testing.T) {
	now := legalTime("2025-10-01T12:00:00Z")
	unknown := LegalApplicabilityFactInput{Dimension: "funding_kind", Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "尚无足够材料确认"}
	version, err := normalizeLegalApplicabilityInput(unknown, now, json.RawMessage("[]"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !version.ValidFrom.Equal(now) || !version.CreatedAt.Equal(now) || version.BasisObtainedAt != nil {
		t.Fatalf("server should use one time for current-state creation and leave missing evidence time null: %+v", version)
	}
	unknown.ValidFrom = "2020-01-01T00:00:00Z"
	if _, err := normalizeLegalApplicabilityInput(unknown, now, json.RawMessage("[]"), nil); err == nil {
		t.Fatal("current-state input must reject caller backdating")
	}
}

func TestLegalApplicabilityPartyPinsAreAllOrNothingAndEstablishedRequiresContext(t *testing.T) {
	created := legalTime("2025-10-01T12:00:00Z")
	refs := json.RawMessage(`[{"kind":"material_anchor","material_id":"m","material_version":1,"document_id":"d","parsed_document_id":"p","filename":"x.txt","start":0,"end":1,"quote":"a","quote_digest":"` + strings.Repeat("a", 64) + `"}]`)
	input := LegalApplicabilityFactInput{Dimension: "buyer_kind", Status: "established", Value: "enterprise", TemporalBasis: "current_state", BasisNote: "material confirms party role", BasisRefs: refs}
	if _, err := normalizeLegalApplicabilityInput(input, created, refs, &created); err == nil {
		t.Fatal("established buyer fact must pin the exact role binding and subject version")
	}
	input.Party = LegalApplicabilityPartyPin{Side: "buyer", BindingID: "binding", BindingVersion: 1, SubjectID: "subject", SubjectVersion: 2, ScopeNote: "contracting buyer"}
	if _, err := normalizeLegalApplicabilityInput(input, created, refs, &created); err != nil {
		t.Fatalf("complete buyer pin should be accepted: %v", err)
	}
	input.Party.SubjectVersion = 0
	if _, err := normalizeLegalApplicabilityInput(input, created, refs, &created); err == nil {
		t.Fatal("partial party pin must not bypass the composite version relation")
	}
}

func TestLegalApplicabilityHistoricalUnknownRequiresIntervalAndBasisNote(t *testing.T) {
	created := legalTime("2025-10-01T12:00:00Z")
	refs := json.RawMessage(`[{}]`)
	input := LegalApplicabilityFactInput{Dimension: "project_kind", Status: "unknown", UnknownReason: "uncertain_evidence", TemporalBasis: "historical_interval", ValidFrom: "2025-05-01T00:00:00+08:00", ValidUntil: "2025-06-01T00:00:00+08:00", BasisNote: "material does not settle the project classification"}
	version, err := normalizeLegalApplicabilityInput(input, created, refs, nil)
	if err != nil || version.ValidFrom.Format(time.RFC3339) != "2025-04-30T16:00:00Z" || version.BasisObtainedAt != nil {
		t.Fatalf("historical unknown should retain UTC interval without invented evidence time: version=%+v err=%v", version, err)
	}
	input.ValidUntil = ""
	if _, err := normalizeLegalApplicabilityInput(input, created, refs, nil); err == nil {
		t.Fatal("historical interval must be explicit")
	}
}

func TestLegalApplicabilityHistoricalTimesCanonicalizeToPostgresPrecision(t *testing.T) {
	created := legalTime("2025-10-01T12:00:00Z")
	input := LegalApplicabilityFactInput{
		Dimension: "project_kind", Status: "unknown", UnknownReason: "uncertain_evidence",
		TemporalBasis: "historical_interval", ValidFrom: "2025-05-01T00:00:00.123456789+08:00",
		ValidUntil: "2025-06-01T00:00:00.123456789+08:00", BasisNote: "材料不足以确认项目类别",
	}
	refs := json.RawMessage("[{}]")
	fact, err := normalizeLegalApplicabilityInput(input, created, refs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fact.ValidFrom.Format(time.RFC3339Nano) != "2025-04-30T16:00:00.123456Z" ||
		fact.ValidUntil == nil || fact.ValidUntil.Format(time.RFC3339Nano) != "2025-05-31T16:00:00.123456Z" {
		t.Fatalf("historical interval was not canonicalized to UTC microseconds: from=%s until=%v", fact.ValidFrom.Format(time.RFC3339Nano), fact.ValidUntil)
	}
	parsed, err := parseLegalFactTime("2025-05-01T00:00:00.000000999Z")
	if err != nil || parsed.Format(time.RFC3339Nano) != "2025-05-01T00:00:00Z" {
		t.Fatalf("valid nanosecond RFC3339 input should truncate to UTC microseconds: parsed=%s err=%v", parsed.Format(time.RFC3339Nano), err)
	}
}

func TestLegalApplicabilityRejectsIntervalThatCollapsesAtDatabasePrecision(t *testing.T) {
	input := LegalApplicabilityFactInput{
		Dimension: "project_kind", Status: "unknown", UnknownReason: "uncertain_evidence",
		TemporalBasis: "historical_interval", ValidFrom: "2025-05-01T00:00:00.000000100Z",
		ValidUntil: "2025-05-01T00:00:00.000000900Z", BasisNote: "材料不足以确认项目类别",
	}
	if _, err := normalizeLegalApplicabilityInput(input, legalTime("2025-10-01T00:00:00Z"), json.RawMessage("[{}]"), nil); err == nil {
		t.Fatal("historical interval that collapses after microsecond canonicalization must be rejected")
	}
}

func TestLegalApplicabilityBasisJSONBRoundTripRestoresCanonicalDigest(t *testing.T) {
	canonical, err := canonicalRawJSON(json.RawMessage(`[{"document_id":"文档-甲","quote":"第496条","start":4,"end":8}]`))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256Bytes(canonical)
	// PostgreSQL jsonb may return semantically identical objects with a different
	// key order and whitespace. The reader must restore the canonical wire bytes.
	jsonbRead := []byte(`[{ "start": 4, "quote": "第496条", "document_id": "文档-甲", "end": 8 }]`)
	verified, err := verifyLegalApplicabilityBasisManifest(jsonbRead, digest)
	if err != nil || string(verified) != string(canonical) {
		t.Fatalf("jsonb round trip did not restore canonical bytes: got=%s err=%v", verified, err)
	}
	if _, err := verifyLegalApplicabilityBasisManifest(jsonbRead, strings.Repeat("0", 64)); err == nil {
		t.Fatal("a stored digest inconsistent with the canonicalized jsonb value must be rejected")
	}
}

func TestLegalApplicabilityRequestDigestCanonicalizesEquivalentBasisJSON(t *testing.T) {
	base := LegalApplicabilityFactInput{ExpectedVersion: 2, IdempotencyKey: "basis-json-order", Dimension: "project_kind", Status: "established",
		Value: "standalone_services", TemporalBasis: "historical_interval", ValidFrom: "2025-01-01T00:00:00Z", ValidUntil: "2026-01-01T00:00:00Z",
		BasisNote: "按原文核验范围"}
	base.BasisRefs = json.RawMessage(`[{"kind":"material_anchor","quote":"采购人","start":0,"end":3}]`)
	first, err := legalApplicabilityRequestDigest("tenant", "org", "case", "actor", base.IdempotencyKey, base)
	if err != nil {
		t.Fatal(err)
	}
	reordered := base
	reordered.BasisRefs = json.RawMessage(`[{"end":3,"quote":"\u91c7\u8d2d\u4eba","start":0,"kind":"material_anchor"}]`)
	second, err := legalApplicabilityRequestDigest("tenant", "org", "case", "actor", reordered.IdempotencyKey, reordered)
	if err != nil || first != second {
		t.Fatalf("equivalent reordered/escaped evidence JSON changed the request identity: first=%s second=%s err=%v", first, second, err)
	}
	changed := reordered
	changed.BasisRefs = json.RawMessage(`[{"end":3,"quote":"卖方主体","start":0,"kind":"material_anchor"}]`)
	third, err := legalApplicabilityRequestDigest("tenant", "org", "case", "actor", changed.IdempotencyKey, changed)
	if err != nil || first == third {
		t.Fatalf("materially changed evidence JSON retained the request identity: first=%s changed=%s err=%v", first, third, err)
	}
	missing := base
	missing.BasisRefs = json.RawMessage("null")
	missingDigest, err := legalApplicabilityRequestDigest("tenant", "org", "case", "actor", missing.IdempotencyKey, missing)
	if err != nil {
		t.Fatal(err)
	}
	empty := base
	empty.BasisRefs = json.RawMessage("[]")
	emptyDigest, err := legalApplicabilityRequestDigest("tenant", "org", "case", "actor", empty.IdempotencyKey, empty)
	if err != nil || missingDigest != emptyDigest {
		t.Fatalf("basis absence forms did not retain the established empty-list semantics: null=%s empty=%s err=%v", missingDigest, emptyDigest, err)
	}
}

func legalFactVersion(version int64, temporalBasis, validFrom, validUntil, value string, createdAt time.Time, obtainedAt string) LegalApplicabilityFactVersion {
	var until *time.Time
	if validUntil != "" {
		parsed := legalTime(validUntil)
		until = &parsed
	}
	obtained := legalTime(obtainedAt)
	return LegalApplicabilityFactVersion{
		Version: version, Status: "established", Value: value, TemporalBasis: temporalBasis,
		ValidFrom: legalTime(validFrom), ValidUntil: until, BasisObtainedAt: &obtained, CreatedAt: createdAt,
	}
}

func legalTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return parsed.UTC()
}

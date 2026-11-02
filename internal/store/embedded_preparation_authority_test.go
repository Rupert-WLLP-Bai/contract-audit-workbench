package store

import (
	"context"
	"contractaudit/internal/model"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPreparationSelectionDigest(t *testing.T) {
	a := AttemptParseSelection{AttemptID: "a", RunID: "r", DocumentID: "d", OriginalDigest: "digest", ParsedDocumentID: "p", SelectionOrder: 1, ParserID: "parser", ParserVersion: "1", ConfigHash: "config", NormalizationVersion: "v1"}
	hash := preparationSelectionHash([]AttemptParseSelection{a})
	if !validPreparationHash(hash) {
		t.Fatal("invalid digest")
	}
	mutations := []func(*AttemptParseSelection){func(x *AttemptParseSelection) { x.OriginalDigest += "x" }, func(x *AttemptParseSelection) { x.ParsedDocumentID += "x" }, func(x *AttemptParseSelection) { x.ParserVersion += "x" }, func(x *AttemptParseSelection) { x.ConfigHash += "x" }, func(x *AttemptParseSelection) { x.NormalizationVersion += "x" }}
	for _, mutate := range mutations {
		b := a
		mutate(&b)
		if preparationSelectionHash([]AttemptParseSelection{b}) == hash {
			t.Fatal("selection mutation did not change digest")
		}
	}
	b := a
	b.DocumentID = "other"
	if preparationSelectionHash([]AttemptParseSelection{a, b}) != "" {
		t.Fatal("duplicate order accepted")
	}
	if validPreparationHash("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("noncanonical digest accepted")
	}
}

func TestPreparationCallerCannotUseModel(t *testing.T) {
	s := Store{}
	scope := model.DelegatedScope{Kind: "execution_prepare", Action: "embedded_prepare", TenantID: "t", OrgID: "o", CaseID: "c", ActorID: "a"}
	if _, err := s.resolveOperationAuthority(t.Context(), scope, "model"); err == nil {
		t.Fatal("preparation authorized for model")
	}
	if _, err := s.resolveOperationAuthority(t.Context(), scope, "document"); err == nil {
		t.Fatal("preparation authorized for document")
	}
}

func TestPreparationRejectsDurableJobPG(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Fatal("PG fixture unavailable")
	}
	fixture.core.serviceOwner = ""
	fixture.core.sel.Mode = "test"
	item := Case{ID: fixture.caseID, TenantID: fixture.actor.TenantID, OwnerOrgID: fixture.actor.OrgID}
	if _, err := fixture.core.prepareEmbeddedAuthority(t.Context(), fixture.actor, item, fixture.runID, fixture.attemptID); err == nil {
		t.Fatal("durable job admitted as embedded preparation")
	}
}

func TestPreparationAuthoritySelectionAndFrozenInput(t *testing.T) {
	digest := digestBytes([]byte("original"))
	doc := Document{ID: "doc", CaseID: "case", Digest: digest}
	parsed := ParsedDocument{ID: "parsed", SelectionOrder: 1, DocumentID: doc.ID, OriginalDigest: digest, ParserID: "parser", ParserVersion: "1", ConfigHash: digestBytes([]byte("config")), NormalizationVersion: "text-normalization-v1", CreatedAt: "2026-01-01T00:00:00Z", Status: "failed", ParseError: "parse failed"}
	payload, err := parsed.canonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	parsed.ArtifactDigest = digestBytes(payload)
	frozen := frozenFromParsed(doc, parsed)
	snapshot := RunSnapshot{RunID: "run", DocumentIDs: []string{"doc"}, ParsedDocumentIDs: []string{"parsed"}, ParseSelectionHash: selectionHash([]FrozenDocument{frozen}), ParserConfigurationID: frozenConfigHash([]FrozenDocument{frozen})}
	selection := AttemptParseSelection{AttemptID: "attempt", RunID: "run", DocumentID: doc.ID, OriginalDigest: digest, ParsedDocumentID: parsed.ID, SelectionOrder: 1, ParserID: parsed.ParserID, ParserVersion: parsed.ParserVersion, ConfigHash: parsed.ConfigHash, NormalizationVersion: parsed.NormalizationVersion}
	st := state{Runs: []Run{{ID: "run", CaseID: "case", State: "running"}}, Attempts: []Attempt{{ID: "attempt", RunID: "run", ActorID: "actor", State: "running", HistoryKind: "preparing"}}, Documents: []Document{doc}, Snapshots: []RunSnapshot{snapshot}, AttemptParseSelections: []AttemptParseSelection{selection}}
	scope := model.DelegatedScope{Kind: "execution_prepare", CaseID: "case", ActorID: "actor", RunID: "run", AttemptID: "attempt", InputSHA256: preparationSelectionHash([]AttemptParseSelection{selection})}
	var store Store
	if !store.validEmbeddedAuthorityState(st, scope) {
		t.Fatal("complete preparing state rejected")
	}
	st.AttemptParseSelections[0].OriginalDigest = "changed"
	if store.validEmbeddedAuthorityState(st, scope) {
		t.Fatal("changed selection accepted")
	}
	st.AttemptParseSelections[0] = selection
	inputHash, err := frozenInputHash(snapshot, []FrozenDocument{frozen})
	if err != nil {
		t.Fatal(err)
	}
	st.ParsedDocuments = []ParsedDocument{parsed}
	st.AttemptInputs = []AttemptInput{{AttemptID: "attempt", RunID: "run", InputHash: inputHash, FrozenAt: "2026-01-01T00:00:00Z", Documents: []FrozenDocument{frozen}}}
	st.Attempts[0].HistoryKind = "frozen"
	st.Attempts[0].InputHash = &inputHash
	scope.Kind = "execution"
	scope.AdmissionRevision = "revision"
	scope.InputSHA256 = inputHash
	if !store.validEmbeddedAuthorityState(st, scope) {
		t.Fatal("frozen input rejected")
	}
	st.AttemptInputs[0].Documents[0].Text = "tampered"
	if store.validEmbeddedAuthorityState(st, scope) {
		t.Fatal("tampered frozen input accepted")
	}
}

func TestPreparationMintAndPromotePG(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Fatal("PG fixture unavailable")
	}
	ctx := context.Background()
	s := fixture.environment.harness
	// Isolated fixture only: remove delivery machinery to construct an embedded,
	// no-job attempt while retaining the genuine accepted immutable selections.
	for _, table := range []string{"worker_claim_identities", "outbox_events", "worker_jobs"} {
		column := "job_id"
		if table == "worker_jobs" {
			column = "id"
		}
		if _, err := s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE "+column+"=$1", fixture.jobID); err != nil {
			t.Fatalf("remove fixture %s: %v", table, err)
		}
	}
	item := Case{ID: fixture.caseID, TenantID: fixture.actor.TenantID, OwnerOrgID: fixture.actor.OrgID}
	scope, err := s.prepareEmbeddedAuthority(ctx, fixture.actor, item, fixture.runID, fixture.attemptID)
	if err != nil {
		t.Fatalf("mint preparing authority: %v", err)
	}
	if scope.Kind != "execution_prepare" || scope.AdmissionRevision == "" {
		t.Fatalf("invalid preparation scope: %+v", scope)
	}
	if _, err = s.ResolveOperationAuthority(ctx, scope, "worker"); err != nil {
		t.Fatalf("resolve preparation: %v", err)
	}
	forged := scope
	forged.AdmissionRevision += "changed"
	if _, err := s.ResolveOperationAuthority(ctx, forged, "worker"); err == nil {
		t.Fatal("changed revision accepted")
	}
	if _, err := s.ResolveOperationAuthority(ctx, scope, "model"); err == nil {
		t.Fatal("model accepted preparation ticket")
	}
	st, err := s.embeddedAuthorityState(ctx, fixture.actor, scope)
	if err != nil {
		t.Fatal(err)
	}
	docs := make([]FrozenDocument, 0, len(st.AttemptParseSelections))
	for _, sel := range st.AttemptParseSelections {
		doc, docOK := st.documentByIdentity(sel.DocumentID, sel.OriginalDigest)
		parsed, parsedOK := st.parsedByID(sel.ParsedDocumentID)
		if !docOK || !parsedOK {
			t.Fatal("fixture must contain existing parsed text")
		}
		frozen := frozenFromParsed(doc, parsed)
		bindFrozenMaterialIdentity(st, &frozen)
		docs = append(docs, frozen)
	}
	hash, err := frozenInputHash(st.Snapshots[0], docs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO review_attempt_inputs(attempt_id,run_id,input_hash,frozen_at,documents) VALUES($1,$2,$3,$4,$5)`, fixture.attemptID, fixture.runID, hash, stamp(), string(encoded)); err != nil {
		t.Fatalf("fixture freeze input: %v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE review_attempts SET input_hash=$1,history_kind='frozen' WHERE id=$2`, hash, fixture.attemptID); err != nil {
		t.Fatalf("fixture freeze attempt: %v", err)
	}
	frozenState, err := s.embeddedAuthorityState(ctx, fixture.actor, scope)
	if err != nil {
		t.Fatalf("read frozen state: %v", err)
	}
	next := scope
	next.Kind = "execution"
	next.Action = "embedded_review"
	next.InputSHA256 = hash
	if !s.validEmbeddedAuthorityState(frozenState, next) {
		t.Fatalf("frozen state rejected: selection=%v documents=%v snapshot=%v compare=%v actual=%q saved=%q", validEmbeddedSelectionSnapshot(frozenState, frozenState.Attempts[0], frozenState.AttemptParseSelections), validEmbeddedFrozenDocuments(frozenState, frozenState.AttemptInputs[0].Documents), frozenInputMatchesSnapshot(frozenState.Snapshots[0], frozenState.AttemptInputs[0].Documents), attemptSelectionsMatchFrozen(frozenState.AttemptParseSelections, frozenState.AttemptInputs[0].Documents), frozenState.AttemptInputs[0].InputHash, hash)
	}
	tampered := frozenState.AttemptInputs[0].Documents[0]
	if tampered.Material == nil {
		t.Fatal("fixture lacks material identity")
	}
	material := *tampered.Material
	material.Version++
	tampered.Material = &material
	if validEmbeddedFrozenDocuments(frozenState, []FrozenDocument{tampered}) {
		t.Fatal("changed material version accepted")
	}
	forged = scope
	forged.InputSHA256 = strings.Repeat("a", 64)
	if _, err := s.promoteEmbeddedAuthority(ctx, fixture.actor, forged); err == nil {
		t.Fatal("changed selection digest promoted")
	}
	promoted, err := s.promoteEmbeddedAuthority(ctx, fixture.actor, scope)
	if err != nil {
		t.Fatalf("promote frozen authority: %v", err)
	}
	if promoted.Kind != "execution" || promoted.InputSHA256 != hash || promoted.AdmissionRevision != scope.AdmissionRevision {
		t.Fatalf("invalid promoted scope: %+v", promoted)
	}
	if _, err = s.ResolveOperationAuthority(ctx, promoted, "model"); err != nil {
		t.Fatalf("resolve frozen authority: %v", err)
	}
}

func TestPreparationPromotionRejectsParentExpiredWhileLockedPG(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Fatal("PG fixture unavailable")
	}
	ctx := context.Background()
	s := fixture.environment.harness
	for _, table := range []string{"worker_claim_identities", "outbox_events", "worker_jobs"} {
		column := "job_id"
		if table == "worker_jobs" {
			column = "id"
		}
		if _, err := s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE "+column+"=$1", fixture.jobID); err != nil {
			t.Fatal(err)
		}
	}
	item := Case{ID: fixture.caseID, TenantID: fixture.actor.TenantID, OwnerOrgID: fixture.actor.OrgID}
	parent, err := s.prepareEmbeddedAuthority(ctx, fixture.actor, item, fixture.runID, fixture.attemptID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.embeddedAuthorityState(ctx, fixture.actor, parent)
	if err != nil {
		t.Fatal(err)
	}
	docs := make([]FrozenDocument, 0, len(st.AttemptParseSelections))
	for _, sel := range st.AttemptParseSelections {
		doc, docOK := st.documentByIdentity(sel.DocumentID, sel.OriginalDigest)
		parsed, parsedOK := st.parsedByID(sel.ParsedDocumentID)
		if !docOK || !parsedOK {
			t.Fatal("fixture missing selected artifact")
		}
		frozen := frozenFromParsed(doc, parsed)
		bindFrozenMaterialIdentity(st, &frozen)
		docs = append(docs, frozen)
	}
	hash, err := frozenInputHash(st.Snapshots[0], docs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO review_attempt_inputs(attempt_id,run_id,input_hash,frozen_at,documents) VALUES($1,$2,$3,$4,$5)`, fixture.attemptID, fixture.runID, hash, stamp(), string(encoded)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE review_attempts SET input_hash=$1,history_kind='frozen' WHERE id=$2`, hash, fixture.attemptID); err != nil {
		t.Fatal(err)
	}
	parentDigest := identityTokenDigest(parent.OperationTicket)
	var expires time.Time
	if err = s.pool.QueryRow(ctx, `UPDATE operation_authority_tickets SET expires_at=clock_timestamp()+interval '3 seconds' WHERE ticket_digest=$1 RETURNING expires_at`, parentDigest).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	lock, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	var pid int
	if err = lock.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `SELECT ticket_digest FROM operation_authority_tickets WHERE ticket_digest=$1 FOR UPDATE`, parentDigest); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.promoteEmbeddedAuthority(context.Background(), fixture.actor, parent)
		result <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%FOR SHARE%')`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("promotion did not wait on parent ticket lock")
	}
	if delay := time.Until(expires) + 150*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("expired parent minted review ticket")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promotion did not finish")
	}
	var issued int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM operation_authority_tickets WHERE tenant_id=$1 AND scope->>'kind'='execution' AND scope->>'attempt_id'=$2`, parent.TenantID, parent.AttemptID).Scan(&issued); err != nil {
		t.Fatal(err)
	}
	if issued != 0 {
		t.Fatalf("issued %d execution tickets", issued)
	}
}

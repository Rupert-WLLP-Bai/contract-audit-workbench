// File metadata (manifest mirror):
// Purpose: Verifies new admissions after cancelled preparation, exact command replay across sessions, concurrent requests, current identity and tenant epochs, fresh frozen inputs and migration preservation using restricted PostgreSQL roles.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func readmissionContext(ctx context.Context, key string, a JobAcceptance) context.Context {
	ctx = WithReviewIdempotency(ctx, key)
	return WithReviewReadmission(ctx, ReviewReadmission{RunID: a.RunID, AttemptID: a.AttemptID})
}

func TestReviewReadmissionPreparingScanPostgres(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%v", running), func(t *testing.T) {
			f, ok := newDurableCaseFixture(t)
			if !ok {
				t.Skip("isolated PostgreSQL and restricted roles required")
			}
			ctx := WithAdmissionSessionToken(context.Background(), f.sessionToken)
			execution := f.environment.openRole(t, "execution")
			execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
			execution.UseDocumentPreparer(f.document)
			execution.UseModels(&frozenOCRModel{transcript: "合成扫描件"})
			f.document.UseModels(&frozenOCRModel{transcript: "合成扫描件"})
			scan, err := f.document.AddDocument(ctx, f.admin, f.caseID, "attachment", "scan.png", "image/png", model.SampleWarrantyPNG)
			if err != nil {
				t.Fatal(err)
			}
			first, err := execution.StartReviewAsync(WithReviewIdempotency(ctx, "original"), f.admin, f.caseID)
			if err != nil {
				t.Fatal(err)
			}
			if running {
				claim, err := execution.ClaimNextJob(ctx, "readmit-worker", time.Minute)
				if err != nil || claim.JobID != first.JobID {
					t.Fatalf("claim: %+v %v", claim, err)
				}
				if err := execution.markAttemptStarted(withExecutionClaim(ctx, claim), first.RunID, first.AttemptID); err != nil {
					t.Fatal(err)
				}
			}
			before, err := execution.snapshotScoped(ctx, userScope(f.admin))
			if err != nil {
				t.Fatal(err)
			}
			var selected AttemptParseSelection
			for _, s := range before.parseSelectionsForAttempt(first.AttemptID) {
				if s.DocumentID == scan {
					selected = s
				}
			}
			if selected.OriginalDigest == "" || selected.ParsedDocumentID == "" {
				t.Fatalf("missing reserved source: %+v", selected)
			}
			if _, found := before.parsedByID(selected.ParsedDocumentID); found {
				t.Fatal("scan parsed before acceptance")
			}
			if _, found := before.attemptInput(first.AttemptID); found {
				t.Fatal("preparation already had frozen input")
			}
			if err := execution.CancelReview(ctx, f.admin, f.caseID, first.RunID); err != nil {
				t.Fatal(err)
			}
			cancelled, err := execution.snapshotScoped(ctx, userScope(f.admin))
			if err != nil {
				t.Fatal(err)
			}
			old, _ := cancelled.attemptByID(first.AttemptID)
			if old.State != "cancelled" || old.HistoryKind != "preparing" || old.ErrorCode == nil || *old.ErrorCode != "cancelled_by_user" {
				t.Fatalf("unexpected cancellation: %+v", old)
			}
			if _, err := execution.RetryAsync(ctx, f.admin, f.caseID, first.RunID); !isConflictFault(err) {
				t.Fatalf("missing frozen input must reject retry: %v", err)
			}
			replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctx, "original"), f.admin, f.caseID)
			if err != nil || replay != first {
				t.Fatalf("original replay: %+v %v", replay, err)
			}
			legacy, err := execution.StartReviewAsync(ctx, f.admin, f.caseID)
			if err != nil || legacy != first {
				t.Fatalf("expected unchanged signature to reuse old run without explicit readmission: %+v %v", legacy, err)
			}
			if _, err := execution.StartReviewAsync(readmissionContext(ctx, "original", first), f.admin, f.caseID); !isConflictFault(err) {
				t.Fatalf("same key with changed content: %v", err)
			}
			if _, err := execution.StartReviewAsync(WithReviewReadmission(ctx, ReviewReadmission{first.RunID, first.AttemptID}), f.admin, f.caseID); err == nil {
				t.Fatal("readmission without key accepted")
			}
			viewer, _ := accounts.ByID("demo-viewer")
			if _, err := execution.StartReviewAsync(readmissionContext(ctx, "viewer", first), viewer, f.caseID); err == nil {
				t.Fatal("viewer admitted")
			}
			foreign, _ := accounts.ByID("demo-tenant2-admin")
			if _, err := execution.StartReviewAsync(readmissionContext(ctx, "foreign", first), foreign, f.caseID); err == nil {
				t.Fatal("foreign tenant admitted")
			}
			expired, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := execution.StartReviewAsync(readmissionContext(expired, "expired", first), f.admin, f.caseID); !errors.Is(err, context.Canceled) {
				t.Fatalf("expired context: %v", err)
			}
			execution.UseReadOnly(true)
			if _, err := execution.StartReviewAsync(readmissionContext(ctx, "readonly", first), f.admin, f.caseID); err == nil {
				t.Fatal("read-only admitted")
			}
			execution.UseReadOnly(false)
			if _, err := execution.StartReviewAsync(readmissionContext(ctx, "wrong", JobAcceptance{RunID: first.RunID, AttemptID: "att_wrong"}), f.admin, f.caseID); !isConflictFault(err) {
				t.Fatalf("wrong attempt accepted: %v", err)
			}
			additional := ""
			if running {
				detail, err := f.core.GetCase(f.admin, f.caseID)
				if err != nil {
					t.Fatal(err)
				}
				originalSelection := append([]string(nil), detail.MaterialSelection.DocumentIDs...)
				if err := f.document.ConfirmMaterialSelection(ctx, f.admin, f.caseID, detail.MaterialSelection.Version, []string{scan}); err != nil {
					t.Fatal(err)
				}
				if _, err := execution.StartReviewAsync(readmissionContext(ctx, "unready", first), f.admin, f.caseID); err == nil {
					t.Fatal("new admission ignored current readiness")
				}
				detail, err = f.core.GetCase(f.admin, f.caseID)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.document.ConfirmMaterialSelection(ctx, f.admin, f.caseID, detail.MaterialSelection.Version, originalSelection); err != nil {
					t.Fatal(err)
				}
				additional, err = f.document.AddDocument(ctx, f.admin, f.caseID, "attachment", "additional.txt", "text/plain", []byte("重新发起时新增的合成材料"))
				if err != nil {
					t.Fatal(err)
				}
			}

			token, err := f.core.EstablishSession(f.admin, "client")
			if err != nil {
				t.Fatal(err)
			}
			actor, _, err := f.core.AuthenticateSession(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			reopened := f.environment.openRole(t, "execution")
			reopened.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
			reopened.UseDocumentPreparer(f.document)
			reopened.UseModels(&frozenOCRModel{transcript: "合成扫描件"})
			resumed := WithAdmissionSessionToken(context.Background(), token)
			var accepted [2]JobAcceptance
			var errs [2]error
			var wg sync.WaitGroup
			wg.Add(2)
			for i, st := range []*Store{execution, reopened} {
				go func(i int, st *Store) {
					defer wg.Done()
					accepted[i], errs[i] = st.StartReviewAsync(readmissionContext(resumed, "new-command", first), actor, f.caseID)
				}(i, st)
			}
			wg.Wait()
			if errs[0] != nil || errs[1] != nil || accepted[0] != accepted[1] {
				t.Fatalf("concurrent same-key replay: %+v %+v %v", accepted[0], accepted[1], errs)
			}
			next := accepted[0]
			if next.RunID == first.RunID || next.AttemptID == first.AttemptID || next.JobID == first.JobID {
				t.Fatalf("reused cancelled identity: first=%+v next=%+v", first, next)
			}
			if _, err := execution.StartReviewAsync(readmissionContext(ctx, "stale-new-command", first), f.admin, f.caseID); !isConflictFault(err) {
				t.Fatalf("stale target accepted: %v", err)
			}
			after, err := execution.snapshotScoped(ctx, userScope(f.admin))
			if err != nil {
				t.Fatal(err)
			}
			preserved, _ := after.attemptByID(first.AttemptID)
			if !reflect.DeepEqual(old, preserved) || !reflect.DeepEqual(cancelled.parseSelectionsForAttempt(first.AttemptID), after.parseSelectionsForAttempt(first.AttemptID)) {
				t.Fatal("old cancellation or source selections changed")
			}
			var oldCancel, newStart bool
			audits, err := loadAudits(ctx, f.environment.harness.pool, f.caseID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range audits {
				if event.RunID == first.RunID && event.EventType == "review_cancelled" {
					oldCancel = true
				}
				if event.RunID == next.RunID && event.EventType == "review_started" && slices.Contains(event.CorrelationIDs, first.RunID) && slices.Contains(event.CorrelationIDs, first.AttemptID) {
					newStart = true
				}
			}
			if !oldCancel || !newStart {
				t.Fatalf("cancellation or new admission audit linkage was not preserved: %+v", audits)
			}
			if _, found := after.attemptInput(first.AttemptID); found {
				t.Fatal("old attempt acquired frozen input")
			}
			fresh, ok := after.attemptByID(next.AttemptID)
			if !ok || fresh.HistoryKind != "preparing" || fresh.State != "accepted" {
				t.Fatalf("new attempt not preparing: %+v", fresh)
			}
			if _, found := after.attemptInput(next.AttemptID); found {
				t.Fatal("new preparation fabricated frozen input")
			}
			foundScan, foundAdditional := false, !running
			for _, s := range after.parseSelectionsForAttempt(next.AttemptID) {
				if s.DocumentID == scan && s.OriginalDigest == selected.OriginalDigest {
					foundScan = true
				}
				if running && s.DocumentID == additional {
					foundAdditional = true
				}
			}
			if !foundScan || !foundAdditional {
				t.Fatal("new admission did not select current original scan and added material")
			}
			var count int
			if err := f.environment.harness.pool.QueryRow(ctx, `SELECT count(*) FROM review_runs WHERE case_id=$1`, f.caseID).Scan(&count); err != nil || count != 2 {
				t.Fatalf("run count=%d err=%v", count, err)
			}
			claim, err := reopened.ClaimNextJob(resumed, "readmission-worker", time.Minute)
			if err != nil || claim.JobID != next.JobID {
				t.Fatalf("claim readmitted work: %+v %v", claim, err)
			}
			work := withExecutionClaim(resumed, claim)
			if err := reopened.markAttemptStarted(work, next.RunID, next.AttemptID); err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.prepareAttempt(work, next.RunID, next.AttemptID, reopened.currentModels()); err != nil {
				t.Fatalf("prepare new attempt: %v", err)
			}
			prepared, err := reopened.snapshotScoped(resumed, userScope(actor))
			if err != nil {
				t.Fatal(err)
			}
			input, ok := prepared.attemptInput(next.AttemptID)
			if !ok || len(input.Documents) == 0 {
				t.Fatal("new attempt did not freeze its prepared inputs")
			}
			if _, ok := prepared.attemptInput(first.AttemptID); ok {
				t.Fatal("preparing the new run altered the cancelled attempt")
			}
			original, sourceBytes, err := f.document.Original(actor, f.caseID, scan)
			if err != nil || original.Digest != selected.OriginalDigest || !bytes.Equal(sourceBytes, model.SampleWarrantyPNG) {
				t.Fatalf("original scan changed after readmission: digest=%s err=%v", original.Digest, err)
			}
			t.Log("PASS: actual restricted roles, unparsed PNG, retained original cancellation, cross-session same-key concurrent receipt and current source")
		})
	}
}

func TestReviewReadmissionDifferentKeysAndBoundariesPostgres(t *testing.T) {
	f, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	ctx := WithAdmissionSessionToken(context.Background(), f.sessionToken)
	execution := f.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
	execution.UseDocumentPreparer(f.document)
	execution.UseModels(&frozenOCRModel{})
	first, err := execution.StartReviewAsync(ctx, f.admin, f.caseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.CancelReview(ctx, f.admin, f.caseID, first.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.environment.harness.pool.Exec(ctx, `UPDATE cases SET business_status='archived', archived_at='2026-11-05T00:00:00Z' WHERE id=$1`, f.caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.StartReviewAsync(readmissionContext(ctx, "archived", first), f.admin, f.caseID); err == nil {
		t.Fatal("archived case admitted")
	}
	if _, err := f.environment.harness.pool.Exec(ctx, `UPDATE cases SET business_status='active', archived_at=NULL WHERE id=$1`, f.caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.environment.harness.pool.Exec(ctx, `UPDATE cases SET member_ids='[]' WHERE id=$1`, f.caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.StartReviewAsync(readmissionContext(ctx, "revoked", first), f.admin, f.caseID); err == nil {
		t.Fatal("revoked membership admitted")
	}
	if _, err := f.environment.harness.pool.Exec(ctx, `UPDATE cases SET member_ids=$2 WHERE id=$1`, f.caseID, fmt.Sprintf(`["%s"]`, f.admin.ID)); err != nil {
		t.Fatal(err)
	}
	reopened := f.environment.openRole(t, "execution")
	reopened.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
	reopened.UseDocumentPreparer(f.document)
	reopened.UseModels(&frozenOCRModel{})
	var values [2]JobAcceptance
	var errs [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	for i, st := range []*Store{execution, reopened} {
		go func(i int, st *Store) {
			defer wg.Done()
			values[i], errs[i] = st.StartReviewAsync(readmissionContext(ctx, fmt.Sprintf("distinct-%d", i), first), f.admin, f.caseID)
		}(i, st)
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if isConflictFault(err) {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("distinct concurrent commands: values=%+v errors=%v", values, errs)
	}
	var count int
	if err := f.environment.harness.pool.QueryRow(ctx, `SELECT count(*) FROM review_runs WHERE case_id=$1`, f.caseID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("run count=%d err=%v", count, err)
	}
}

func TestReviewReadmissionFrozenAndRevokedPostgres(t *testing.T) {
	t.Run("frozen", func(t *testing.T) {
		f, ok := newDurableFixture(t)
		if !ok {
			t.Skip("isolated PostgreSQL and restricted roles required")
		}
		ctx := withExecutionClaim(context.Background(), f.claim)
		if err := f.execution.markAttemptStarted(ctx, f.runID, f.attemptID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.execution.prepareAttempt(ctx, f.runID, f.attemptID, f.execution.currentModels()); err != nil {
			t.Fatal(err)
		}
		if err := f.execution.CancelReview(context.Background(), f.admin, f.caseID, f.runID); err != nil {
			t.Fatal(err)
		}
		target := JobAcceptance{RunID: f.runID, AttemptID: f.attemptID}
		if _, err := f.execution.StartReviewAsync(readmissionContext(context.Background(), "frozen", target), f.admin, f.caseID); !isConflictFault(err) {
			t.Fatalf("frozen readmission: %v", err)
		}
		token, err := f.core.EstablishSession(f.admin, "client")
		if err != nil {
			t.Fatal(err)
		}
		reopened := f.environment.openRole(t, "execution")
		reopened.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
		reopened.UseDocumentPreparer(f.environment.openRole(t, "document"))
		reopened.UseModels(&frozenOCRModel{})
		next, err := reopened.RetryAsync(WithAdmissionSessionToken(context.Background(), token), f.admin, f.caseID, f.runID)
		if err != nil || next.RunID != f.runID || next.AttemptID == f.attemptID {
			t.Fatalf("frozen retry: %+v %v", next, err)
		}
	})
	t.Run("revoked", func(t *testing.T) {
		f, ok := newDurableFixture(t)
		if !ok {
			t.Skip("isolated PostgreSQL and restricted roles required")
		}
		f.execution.UseOperationAuthority(revokedFinalizeAuthority{})
		if err := f.execution.finishAttemptNarrow(withExecutionClaim(context.Background(), f.claim), f.runID, f.attemptID, "completed", "", ""); err != nil {
			t.Fatal(err)
		}
		f.execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
		target := JobAcceptance{RunID: f.runID, AttemptID: f.attemptID}
		if _, err := f.execution.StartReviewAsync(readmissionContext(context.Background(), "revoked-cancel", target), f.admin, f.caseID); !isConflictFault(err) {
			t.Fatalf("revoked cancellation readmission: %v", err)
		}
	})
	t.Run("unknown cancellation source", func(t *testing.T) {
		f, ok := newDurableFixture(t)
		if !ok {
			t.Skip("isolated PostgreSQL and restricted roles required")
		}
		if err := f.execution.CancelReview(context.Background(), f.admin, f.caseID, f.runID); err != nil {
			t.Fatal(err)
		}
		// Simulate a legacy cancellation whose origin was never recorded.
		if _, err := f.environment.harness.pool.Exec(context.Background(), `UPDATE review_attempts SET error_code=NULL WHERE id=$1`, f.attemptID); err != nil {
			t.Fatal(err)
		}
		target := JobAcceptance{RunID: f.runID, AttemptID: f.attemptID}
		if _, err := f.execution.StartReviewAsync(readmissionContext(context.Background(), "unknown-cancel", target), f.admin, f.caseID); !isConflictFault(err) {
			t.Fatalf("unknown cancellation readmission: %v", err)
		}
	})
}

func TestReviewReadmissionRejectsExpiredEpochPostgres(t *testing.T) {
	f, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	ctx := WithAdmissionSessionToken(context.Background(), f.sessionToken)
	if _, err := f.environment.harness.pool.Exec(ctx, `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES($1,'readmission epoch','active')`, f.admin.TenantID); err != nil {
		t.Fatal(err)
	}
	execution := f.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
	execution.UseDocumentPreparer(f.document)
	execution.UseModels(&frozenOCRModel{})
	first, err := execution.StartReviewAsync(ctx, f.admin, f.caseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.CancelReview(ctx, f.admin, f.caseID, first.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.environment.harness.pool.Exec(ctx, `UPDATE identity_tenants SET status='suspended',live_epoch=live_epoch+1 WHERE tenant_id=$1`, f.admin.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.environment.harness.pool.Exec(ctx, `UPDATE identity_tenants SET status='active',live_epoch=live_epoch+1 WHERE tenant_id=$1`, f.admin.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.StartReviewAsync(readmissionContext(ctx, "expired-epoch", first), f.admin, f.caseID); !isConflictFault(err) {
		t.Fatalf("expired epoch accepted: %v", err)
	}
}

// An accepted command must retain its receipt when the same actor logs in
// again after the original response was lost.
func TestReviewReceiptReplayAfterNewSessionPostgres(t *testing.T) {
	for _, readmit := range []bool{false, true} {
		t.Run(fmt.Sprintf("readmit=%v", readmit), func(t *testing.T) {
			f, ok := newDurableCaseFixture(t)
			if !ok {
				t.Skip("isolated PostgreSQL and restricted roles required")
			}
			ctxA := WithAdmissionSessionToken(context.Background(), f.sessionToken)
			execution := f.environment.openRole(t, "execution")
			execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
			execution.UseDocumentPreparer(f.document)
			execution.UseModels(&frozenOCRModel{})
			scan, err := f.document.AddDocument(ctxA, f.admin, f.caseID, "attachment", "scan.png", "image/png", model.SampleWarrantyPNG)
			if err != nil {
				t.Fatal(err)
			}
			var target JobAcceptance
			if readmit {
				var err error
				target, err = execution.StartReviewAsync(ctxA, f.admin, f.caseID)
				if err != nil {
					t.Fatal(err)
				}
				if err = execution.CancelReview(ctxA, f.admin, f.caseID, target.RunID); err != nil {
					t.Fatal(err)
				}
			}
			command := func(ctx context.Context) context.Context {
				if readmit {
					return readmissionContext(ctx, "lost-response", target)
				}
				return WithReviewIdempotency(ctx, "lost-response")
			}
			original, err := execution.StartReviewAsync(command(ctxA), f.admin, f.caseID)
			if err != nil {
				t.Fatalf("session A acceptance: %v", err)
			}
			token, err := f.core.EstablishSession(f.admin, "client")
			if err != nil {
				t.Fatal(err)
			}
			actor, _, err := f.core.AuthenticateSession(context.Background(), token)
			if err != nil {
				t.Fatal(err)
			}
			ctxB := WithAdmissionSessionToken(context.Background(), token)
			replayed, err := execution.StartReviewAsync(command(ctxB), actor, f.caseID)
			if err != nil || replayed != original {
				t.Fatalf("session B must replay original acceptance: original=%+v replay=%+v error=%v", original, replayed, err)
			}
			if _, err := execution.StartReviewAsyncScope(command(ctxB), actor, f.caseID, "", "", ReviewScopeSingleContract); !isConflictFault(err) {
				t.Fatalf("same key with different body must conflict: %v", err)
			}
			detail, err := f.core.GetCase(actor, f.caseID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.document.ConfirmMaterialSelection(ctxB, actor, f.caseID, detail.MaterialSelection.Version, []string{scan}); err != nil {
				t.Fatal(err)
			}
			replayed, err = execution.StartReviewAsync(command(ctxB), actor, f.caseID)
			if err != nil || replayed != original {
				t.Fatalf("changed readiness must still replay original: %+v %v", replayed, err)
			}
			if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "new-intent"), actor, f.caseID); err == nil {
				t.Fatal("new intent ignored current material readiness")
			}
			var stamp string
			var version int64
			var runs, attempts, jobs, outbox, receipts int
			err = f.environment.harness.pool.QueryRow(ctxB, `SELECT command_session,command_version,
				(SELECT count(*) FROM review_runs WHERE case_id=$1),
				(SELECT count(*) FROM review_attempts WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)),
				(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
				(SELECT count(*) FROM outbox_events WHERE case_id=$1),
				(SELECT count(*) FROM execution_write_receipts WHERE case_id=$1)
				FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='lost-response'`, f.caseID).
				Scan(&stamp, &version, &runs, &attempts, &jobs, &outbox, &receipts)
			want := 1
			if readmit {
				want = 2
			}
			if err != nil || stamp != sessionTokenDigest(f.sessionToken) || version != 1 || runs != want || attempts != want || jobs != want || outbox != want || receipts != 1 {
				t.Fatalf("replay changed command stamp or duplicated facts: version=%d runs=%d attempts=%d jobs=%d outbox=%d receipts=%d err=%v", version, runs, attempts, jobs, outbox, receipts, err)
			}
		})
	}
}

func TestReviewReceiptConcurrentSessionsPostgres(t *testing.T) {
	for _, scenario := range []struct{ readmit, differentKeys bool }{{false, false}, {true, false}, {true, true}} {
		t.Run(fmt.Sprintf("readmit=%v/different_keys=%v", scenario.readmit, scenario.differentKeys), func(t *testing.T) {
			f, ok := newDurableCaseFixture(t)
			if !ok {
				t.Skip("isolated PostgreSQL and restricted roles required")
			}
			ctxA := WithAdmissionSessionToken(context.Background(), f.sessionToken)
			first := f.environment.openRole(t, "execution")
			second := f.environment.openRole(t, "execution")
			for _, st := range []*Store{first, second} {
				st.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
				st.UseDocumentPreparer(f.document)
				st.UseModels(&frozenOCRModel{})
			}
			var target JobAcceptance
			if scenario.readmit {
				if _, err := f.document.AddDocument(ctxA, f.admin, f.caseID, "attachment", "scan.png", "image/png", model.SampleWarrantyPNG); err != nil {
					t.Fatal(err)
				}
				var err error
				target, err = first.StartReviewAsync(ctxA, f.admin, f.caseID)
				if err != nil {
					t.Fatal(err)
				}
				if err := first.CancelReview(ctxA, f.admin, f.caseID, target.RunID); err != nil {
					t.Fatal(err)
				}
			}
			token, err := f.core.EstablishSession(f.admin, "client")
			if err != nil {
				t.Fatal(err)
			}
			actor, _, err := f.core.AuthenticateSession(context.Background(), token)
			if err != nil {
				t.Fatal(err)
			}
			ctxB := WithAdmissionSessionToken(context.Background(), token)
			var values [2]JobAcceptance
			var errs [2]error
			var group sync.WaitGroup
			for i, call := range []struct {
				st   *Store
				ctx  context.Context
				user accounts.User
			}{{first, ctxA, f.admin}, {second, ctxB, actor}} {
				group.Add(1)
				go func(i int, call struct {
					st   *Store
					ctx  context.Context
					user accounts.User
				}) {
					defer group.Done()
					key := "concurrent-sessions"
					if scenario.differentKeys {
						key = fmt.Sprintf("concurrent-sessions-%d", i)
					}
					ctx := WithReviewIdempotency(call.ctx, key)
					if scenario.readmit {
						ctx = WithReviewReadmission(ctx, ReviewReadmission{target.RunID, target.AttemptID})
					}
					values[i], errs[i] = call.st.StartReviewAsync(ctx, call.user, f.caseID)
				}(i, call)
			}
			group.Wait()
			if scenario.differentKeys {
				successes, conflicts := 0, 0
				for _, err := range errs {
					if err == nil {
						successes++
					} else if isConflictFault(err) {
						conflicts++
					}
				}
				if successes != 1 || conflicts != 1 {
					t.Fatalf("different new intents: values=%+v errors=%v", values, errs)
				}
			} else if errs[0] != nil || errs[1] != nil || values[0] != values[1] {
				t.Fatalf("concurrent sessions must receive one acceptance: values=%+v errors=%v", values, errs)
			}
			var runs, attempts, jobs, outbox, receipts int
			err = f.environment.harness.pool.QueryRow(ctxA, `SELECT
				(SELECT count(*) FROM review_runs WHERE case_id=$1),
				(SELECT count(*) FROM review_attempts WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)),
				(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
				(SELECT count(*) FROM outbox_events WHERE case_id=$1),
				(SELECT count(*) FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key LIKE 'concurrent-sessions%')`, f.caseID).
				Scan(&runs, &attempts, &jobs, &outbox, &receipts)
			want := 1
			if scenario.readmit {
				want = 2
			}
			if err != nil || runs != want || attempts != want || jobs != want || outbox != want || receipts != 1 {
				t.Fatalf("concurrent facts: runs=%d attempts=%d jobs=%d outbox=%d receipts=%d err=%v", runs, attempts, jobs, outbox, receipts, err)
			}
		})
	}
}

func TestReviewReceiptSessionAndServiceBoundariesPostgres(t *testing.T) {
	f, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	ctxA := WithAdmissionSessionToken(context.Background(), f.sessionToken)
	execution := f.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
	execution.UseDocumentPreparer(f.document)
	execution.UseModels(&frozenOCRModel{})
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "boundary"), f.admin, f.caseID); err != nil {
		t.Fatal(err)
	}
	token, err := f.core.EstablishSession(f.admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := f.core.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	ctxB := WithAdmissionSessionToken(context.Background(), token)
	route := "POST /api/cases/" + f.caseID + "/reviews"
	query := func(st *Store, user accounts.User, ctx context.Context, route, key string) (int, error) {
		t.Helper()
		tx, err := st.beginContextTx(commandContext(ctx, user), pgx.TxOptions{})
		if err != nil {
			return 0, err
		}
		defer tx.Rollback(context.Background())
		if err = setLocalScope(ctx, tx, userScope(user)); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.review_receipt_route',$1,true),set_config('app.review_receipt_key',$2,true)`, route, key); err != nil {
			return 0, err
		}
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM execution_write_receipts WHERE case_id=$1`, f.caseID).Scan(&count)
		return count, err
	}
	for _, role := range []string{"execution", "core", "document", "model", "tool"} {
		st := execution
		if role != "execution" {
			st = f.environment.openRole(t, role)
		}
		var sessionUser, currentUser string
		if err := st.pool.QueryRow(ctxB, `SELECT session_user,current_user`).Scan(&sessionUser, &currentUser); err != nil || sessionUser != currentUser {
			t.Fatalf("%s actual login: %v", role, err)
		}
		count, err := query(st, actor, ctxB, route, "boundary")
		want := 0
		if role == "execution" {
			want = 1
		}
		if err != nil || count != want {
			t.Fatalf("%s projection count=%d want=%d err=%v", role, count, want, err)
		}
	}
	for _, selector := range []struct{ route, key string }{{route, "wrong"}, {route + "/other", "boundary"}, {"POST /api/cases/other/reviews", "boundary"}} {
		if count, err := query(execution, actor, ctxB, selector.route, selector.key); err != nil || count != 0 {
			t.Fatalf("wrong selector read=%d err=%v", count, err)
		}
	}
	for _, statement := range []string{`UPDATE execution_write_receipts SET response_json='{}'::jsonb WHERE case_id=$1`, `DELETE FROM execution_write_receipts WHERE case_id=$1`} {
		tx, err := execution.beginContextTx(commandContext(ctxB, actor), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctxB, tx, userScope(actor)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctxB, `SELECT set_config('app.review_receipt_route',$1,true),set_config('app.review_receipt_key','boundary',true)`, route); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctxB, statement, f.caseID)
		_ = tx.Rollback(ctxB)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("execution role could rewrite a receipt: %v", err)
		}
	}
	execution.UseReadOnly(true)
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "boundary"), actor, f.caseID); err == nil {
		t.Fatal("read-only replay accepted")
	}
	execution.UseReadOnly(false)
	if _, err := f.environment.harness.pool.Exec(ctxB, `UPDATE cases SET business_status='archived',archived_at=clock_timestamp()::text WHERE id=$1`, f.caseID); err != nil {
		t.Fatal(err)
	}
	if count, err := query(execution, actor, ctxB, route, "boundary"); err != nil || count != 0 {
		t.Fatalf("archived selector read=%d err=%v", count, err)
	}
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "boundary"), actor, f.caseID); err == nil {
		t.Fatal("archived replay accepted")
	}
	if _, err := f.environment.harness.pool.Exec(ctxB, `UPDATE cases SET business_status='active',archived_at=NULL,member_ids='[]' WHERE id=$1`, f.caseID); err != nil {
		t.Fatal(err)
	}
	if count, err := query(execution, actor, ctxB, route, "boundary"); err != nil || count != 0 {
		t.Fatalf("revoked selector read=%d err=%v", count, err)
	}
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "boundary"), actor, f.caseID); err == nil {
		t.Fatal("revoked replay accepted")
	}
	if _, err := f.environment.harness.pool.Exec(ctxB, `UPDATE cases SET member_ids=$2 WHERE id=$1`, f.caseID, fmt.Sprintf(`["%s"]`, actor.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.environment.harness.pool.Exec(ctxB, `UPDATE sessions SET context_version=context_version+1 WHERE token=$1`, sessionTokenDigest(token)); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "boundary"), actor, f.caseID); err == nil {
		t.Fatal("stale context replay accepted")
	}
	actor, _, err = f.core.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.environment.harness.pool.Exec(ctxB, `UPDATE sessions SET expires_at='2000-01-01T00:00:00Z' WHERE token=$1`, sessionTokenDigest(token)); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "boundary"), actor, f.caseID); err == nil {
		t.Fatal("expired session replay accepted")
	}
}

func TestReviewReceiptPolicyProjectionExecuteRequirement(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	tool := env.openRole(t, "tool")
	cfg, err := pgx.ParseConfig(env.roleDSNs["tool"])
	if err != nil {
		t.Fatal(err)
	}
	qualified := pgx.Identifier{env.harness.schema}.Sanitize() + `.tenant_review_receipt_recovery_allowed(text,text,text,text,text,text)`
	role := pgx.Identifier{cfg.User}.Sanitize()
	ctx := context.Background()
	if _, err := env.harness.pool.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+qualified+` FROM `+role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := env.harness.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+qualified+` TO `+role); err != nil {
			t.Errorf("restore projection ACL: %v", err)
		}
	})
	var count int
	err = tool.pool.QueryRow(ctx, `SELECT count(*) FROM execution_write_receipts`).Scan(&count)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("projection requires policy EXECUTE, got %v", err)
	}
	t.Log("actual Tool projection without policy EXECUTE: SQLSTATE 42501")
	if _, err := env.harness.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+qualified+` TO `+role+`; GRANT EXECUTE ON FUNCTION `+qualified+` TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err == nil {
		t.Fatal("unexpected PUBLIC policy function grant accepted")
	}
	if _, err := env.harness.pool.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+qualified+` FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("exact service function grants should be accepted: %v", err)
	}
}

func TestReviewReceiptMigration41PreservesPostgres(t *testing.T) {
	f, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	ctxA := WithAdmissionSessionToken(context.Background(), f.sessionToken)
	execution := f.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
	execution.UseDocumentPreparer(f.document)
	execution.UseModels(&frozenOCRModel{})
	original, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "pre-upgrade"), f.admin, f.caseID)
	if err != nil {
		t.Fatal(err)
	}
	var stamp, digest string
	var version int64
	var response []byte
	if err := f.environment.harness.pool.QueryRow(ctxA, `SELECT command_session,command_version,request_digest,response_json FROM execution_write_receipts WHERE case_id=$1`, f.caseID).Scan(&stamp, &version, &digest, &response); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the exact 0041 policy in this isolated fixture, retaining
	// its genuine accepted row; then exercise the production migration.
	if _, err := f.environment.harness.pool.Exec(ctxA, `ALTER POLICY command_receipt_context ON execution_write_receipts
		USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint);
		DROP FUNCTION tenant_review_receipt_recovery_allowed(text,text,text,text,text,text);
		DELETE FROM schema_migrations WHERE version=42`); err != nil {
		t.Fatal(err)
	}
	var beforeCheck string
	if err := f.environment.harness.pool.QueryRow(ctxA, `SELECT pg_get_expr(polwithcheck,polrelid) FROM pg_policy WHERE polrelid='execution_write_receipts'::regclass AND polname='command_receipt_context'`).Scan(&beforeCheck); err != nil {
		t.Fatal(err)
	}
	token, err := f.core.EstablishSession(f.admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := f.core.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	ctxB := WithAdmissionSessionToken(context.Background(), token)
	tx, err := execution.beginContextTx(commandContext(ctxB, actor), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctxB, tx, userScope(actor)); err != nil {
		t.Fatal(err)
	}
	var before int
	err = tx.QueryRow(ctxB, `SELECT count(*) FROM execution_write_receipts WHERE case_id=$1`, f.caseID).Scan(&before)
	_ = tx.Rollback(ctxB)
	if err != nil || before != 0 {
		t.Fatalf("schema 41 did not retain its old session fence: %d %v", before, err)
	}
	if err := migrate(ctxB, f.environment.harness.pool, f.environment.harness.schema); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctxB); err != nil {
		t.Fatal(err)
	}
	var afterCheck string
	if err := f.environment.harness.pool.QueryRow(ctxB, `SELECT pg_get_expr(polwithcheck,polrelid) FROM pg_policy WHERE polrelid='execution_write_receipts'::regclass AND polname='command_receipt_context'`).Scan(&afterCheck); err != nil || afterCheck != beforeCheck {
		t.Fatalf("upgrade changed the original receipt write check: %v", err)
	}
	if replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "pre-upgrade"), actor, f.caseID); err != nil || replay != original {
		t.Fatalf("upgraded receipt replay: %+v %v", replay, err)
	}
	var afterStamp, afterDigest string
	var afterVersion int64
	var afterResponse []byte
	if err := f.environment.harness.pool.QueryRow(ctxB, `SELECT command_session,command_version,request_digest,response_json FROM execution_write_receipts WHERE case_id=$1`, f.caseID).Scan(&afterStamp, &afterVersion, &afterDigest, &afterResponse); err != nil || stamp != afterStamp || version != afterVersion || digest != afterDigest || !bytes.Equal(response, afterResponse) {
		t.Fatalf("upgrade rewrote the original receipt: %v", err)
	}
}

func TestReviewReceiptRejectsChangedEpochPostgres(t *testing.T) {
	f, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	ctxA := WithAdmissionSessionToken(context.Background(), f.sessionToken)
	if _, err := f.environment.harness.pool.Exec(ctxA, `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES($1,'receipt epoch','active')`, f.admin.TenantID); err != nil {
		t.Fatal(err)
	}
	execution := f.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: f.core})
	execution.UseDocumentPreparer(f.document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "epoch"), f.admin, f.caseID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.core.EstablishSession(f.admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := f.core.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	ctxB := WithAdmissionSessionToken(context.Background(), token)
	_, err = f.environment.harness.pool.Exec(ctxB, `UPDATE worker_jobs SET tenant_live_epoch=tenant_live_epoch+1 WHERE id=$1`, accepted.JobID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		t.Fatalf("the accepted job epoch must be immutable: %v", err)
	}
	if replayed, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "epoch"), actor, f.caseID); err != nil || replayed != accepted {
		t.Fatalf("current target epoch must replay: %+v %v", replayed, err)
	}
	if _, err := f.environment.harness.pool.Exec(ctxB, `UPDATE identity_tenants SET live_epoch=live_epoch+1 WHERE tenant_id=$1`, actor.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "epoch"), actor, f.caseID); err == nil {
		t.Fatal("old attempt epoch replayed after tenant epoch changed")
	}
}

func TestReviewReceiptCurrentMembershipPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	document.UseOperationAuthority(durableTestOperationAuthority{core: core})
	execution.UseOperationAuthority(durableTestOperationAuthority{core: core})
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	profile := accounts.User{ID: "review-recovery-owner", DisplayName: "Review owner", Role: "clerk", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://review-identity.invalid", Subject: "owner"}
	raw, err := json.Marshal(map[string]any{"users": []accounts.User{profile}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	const principal = "22700000-0000-4000-8000-000000000001"
	const memberA = "22700000-0000-4000-8000-000000000002"
	const memberOtherOrg = "22700000-0000-4000-8000-000000000003"
	const memberOtherTenant = "22700000-0000-4000-8000-000000000004"
	mapping := IdentityCrosswalk{
		Version:    1,
		Tenants:    []IdentityTenantMap{{ID: "review-tenant-a", DisplayName: "Tenant A", Status: "active"}, {ID: "review-tenant-b", DisplayName: "Tenant B", Status: "active"}},
		OrgUnits:   []IdentityOrgUnitMap{{TenantID: "review-tenant-a", ID: "org-a", DisplayName: "Org A", Status: "active"}, {TenantID: "review-tenant-a", ID: "org-b", DisplayName: "Org B", Status: "active"}, {TenantID: "review-tenant-b", ID: "org-c", DisplayName: "Org C", Status: "active"}},
		Principals: []IdentityPrincipalMap{{ID: principal, ActorID: profile.ID, Issuer: profile.Issuer, Subject: profile.Subject, Status: "active"}},
		Memberships: []IdentityMembershipMap{
			{ID: memberA, ActorID: profile.ID, TenantID: "review-tenant-a", OrgUnitID: "org-a", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: memberOtherOrg, ActorID: profile.ID, TenantID: "review-tenant-a", OrgUnitID: "org-b", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: memberOtherTenant, ActorID: profile.ID, TenantID: "review-tenant-b", OrgUnitID: "org-c", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
		},
	}
	cfg, err := pgx.ParseConfig(env.harness.sel.TestDSN)
	if err != nil {
		t.Fatal(err)
	}
	base := context.Background()
	importMapping := func() {
		t.Helper()
		if _, err := ImportIdentityCrosswalk(base, env.harness.sel.TestDSN, env.harness.schema, cfg.Database, mapping, true); err != nil {
			t.Fatal(err)
		}
	}
	importMapping()
	login := func(member string) (context.Context, accounts.User) {
		t.Helper()
		result, err := core.ResolveVerifiedOIDC(base, profile, "client")
		if err != nil {
			t.Fatal(err)
		}
		if result.PendingToken != "" {
			result, err = core.ConsumePendingMembership(base, result.PendingToken, member, "client")
			if err != nil {
				t.Fatal(err)
			}
		}
		user, _, err := core.AuthenticateSession(base, result.SessionToken)
		if err != nil || user.MembershipID != member {
			t.Fatalf("login membership: %v", err)
		}
		return WithAdmissionSessionToken(base, result.SessionToken), user
	}
	ctxA, actorA := login(memberA)
	caseID, err := core.CreateCase(actorA, "企业成员受理恢复", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ role, body string }{{"procurement", "采购要求质保三年。"}, {"draft_contract", "合同约定质保两年。"}} {
		if _, err := document.AddDocument(ctxA, actorA, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatal(err)
		}
	}
	original, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "member-key"), actorA, caseID)
	if err != nil {
		t.Fatal(err)
	}
	var originalStamp, originalDigest string
	var originalVersion int64
	var originalBody []byte
	if err := env.harness.pool.QueryRow(base, `SELECT command_session,command_version,request_digest,response_json FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='member-key'`, caseID).Scan(&originalStamp, &originalVersion, &originalDigest, &originalBody); err != nil {
		t.Fatal(err)
	}
	route := "POST /api/cases/" + caseID + "/reviews"
	for _, member := range []string{memberOtherOrg, memberOtherTenant} {
		ctx, foreign := login(member)
		if member == memberOtherOrg {
			seen, err := execution.snapshotAs(foreign)
			if err != nil {
				t.Fatal(err)
			}
			item, ok := seen.caseByID(caseID)
			if !ok || !authorizeCase(item, foreign, access.Review) {
				t.Fatal("other organization did not retain its tenant-wide Review grant")
			}
		}
		tx, err := execution.beginContextTx(commandContext(ctx, foreign), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(foreign)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.review_receipt_route',$1,true),set_config('app.review_receipt_key','member-key',true)`, route); err != nil {
			t.Fatal(err)
		}
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM execution_write_receipts WHERE case_id=$1`, caseID).Scan(&count)
		_ = tx.Rollback(ctx)
		if err != nil || count != 0 {
			t.Fatalf("other membership saw receipt: count=%d err=%v", count, err)
		}
		_, err = execution.StartReviewAsync(WithReviewIdempotency(ctx, "member-key"), foreign, caseID)
		if err == nil {
			t.Fatal("other organization or tenant replayed receipt")
		}
		if member == memberOtherOrg && !isConflictFault(err) {
			t.Fatalf("authorized cross-org collision must be a controlled conflict: %v", err)
		}
		var runs, attempts, jobs, outbox, receipts int
		var stamp, digest string
		var version int64
		var body []byte
		err = env.harness.pool.QueryRow(base, `SELECT command_session,command_version,request_digest,response_json,
			(SELECT count(*) FROM review_runs WHERE case_id=$1),
			(SELECT count(*) FROM review_attempts WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)),
			(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
			(SELECT count(*) FROM outbox_events WHERE case_id=$1),
			(SELECT count(*) FROM execution_write_receipts WHERE case_id=$1)
			FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='member-key'`, caseID).
			Scan(&stamp, &version, &digest, &body, &runs, &attempts, &jobs, &outbox, &receipts)
		if err != nil || runs != 1 || attempts != 1 || jobs != 1 || outbox != 1 || receipts != 1 || stamp != originalStamp || version != originalVersion || digest != originalDigest || !bytes.Equal(body, originalBody) {
			t.Fatalf("other membership changed receipt or created facts: runs=%d attempts=%d jobs=%d outbox=%d receipts=%d err=%v", runs, attempts, jobs, outbox, receipts, err)
		}
	}
	// A fresh reviewer membership with a current case-review grant may replay.
	mapping.Memberships[0].RoleCode = "reviewer"
	mapping.Memberships[0].Grants = []access.Grant{{Role: "reviewer", Scope: "tenant"}}
	importMapping()
	ctxB, actorB := login(memberA)
	if replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "member-key"), actorB, caseID); err != nil || replay != original {
		t.Fatalf("current reviewer grant cannot replay: %+v %v", replay, err)
	}
	for _, grant := range []access.Grant{{Role: "viewer", Scope: "tenant"}, {Role: "reviewer", Scope: "tenant", ExpiresAt: time.Now().UTC().Add(-time.Hour)}} {
		mapping.Memberships[0].Grants = []access.Grant{grant}
		importMapping()
		if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "member-key"), actorB, caseID); err == nil {
			t.Fatal("stale membership replayed")
		}
		ctx, actor := login(memberA)
		if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctx, "member-key"), actor, caseID); err == nil {
			t.Fatalf("revoked or expired grant replayed: %+v", grant)
		}
	}
}

// File metadata (manifest mirror):
// Purpose: Verifies explicit new admission after user-cancelled scan preparation, exact command replay, concurrent requests, current authorization and tenant epoch, and fresh frozen inputs using restricted PostgreSQL roles.
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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
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

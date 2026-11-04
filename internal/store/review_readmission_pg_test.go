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
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func readmissionContext(ctx context.Context, key string, a JobAcceptance) context.Context {
	ctx = WithReviewIdempotency(ctx, key)
	return WithReviewReadmission(ctx, ReviewReadmission{RunID: a.RunID, AttemptID: a.AttemptID})
}

// Reconstruct a historical receipt policy only inside an isolated test schema.
func restoreReviewReceiptVersionForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string, version int) {
	t.Helper()
	if version != 41 && version != 42 {
		t.Fatalf("unsupported historical fixture version: %d", version)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var database string
	if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER POLICY command_receipt_context ON execution_write_receipts
		USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint);
		DROP TRIGGER review_receipt_origin ON execution_write_receipts;
		DROP FUNCTION tenant_review_receipt_key_conflict(text,text,text,text,text,text);
		DROP FUNCTION tenant_review_receipt_recovery_allowed(text,text,text,text,text,text,text);
		DROP FUNCTION stamp_review_receipt_origin()`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `REVOKE SELECT (tenant_id,org_id,case_id,actor_id,route,idempotency_key,command_session,command_version,command_origin_org) ON execution_write_receipts FROM `+pgx.Identifier{tenantIdentityOwnerName(database, schema)}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE execution_write_receipts DROP COLUMN command_origin_org`); err != nil {
		t.Fatal(err)
	}
	if version == 42 {
		catalog, err := migrationCatalog()
		if err != nil {
			t.Fatal(err)
		}
		if err := executeMigrationSQL(ctx, tx, schema, catalog[41].sql); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(ctx, `SELECT role_name FROM service_database_roles`)
		if err != nil {
			t.Fatal(err)
		}
		var roles []string
		for rows.Next() {
			var role string
			if err := rows.Scan(&role); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			roles = append(roles, role)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{tenantIdentityOwnerName(database, schema)}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		for _, role := range roles {
			if _, err := tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+pgx.Identifier{schema}.Sanitize()+`.tenant_review_receipt_recovery_allowed(text,text,text,text,text,text) TO `+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(ctx, `RESET ROLE`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version>$1`, version); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
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
	var database string
	if err := env.harness.pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	owner := tenantIdentityOwnerName(database, env.harness.schema)
	var canReadDigest, canReadBody bool
	if err := env.harness.pool.QueryRow(context.Background(), `SELECT has_column_privilege($1,'execution_write_receipts','request_digest','SELECT'),has_column_privilege($1,'execution_write_receipts','response_json','SELECT')`, owner).Scan(&canReadDigest, &canReadBody); err != nil || canReadDigest || canReadBody {
		t.Fatalf("AUTH owner can read receipt payload: digest=%t body=%t err=%v", canReadDigest, canReadBody, err)
	}
	conflictFunction := pgx.Identifier{env.harness.schema}.Sanitize() + `.tenant_review_receipt_key_conflict(text,text,text,text,text,text)`
	for _, service := range []string{"execution", "core", "document", "model", "tool"} {
		cfg, err := pgx.ParseConfig(env.roleDSNs[service])
		if err != nil {
			t.Fatal(err)
		}
		var allowed bool
		if err := env.harness.pool.QueryRow(context.Background(), `SELECT has_function_privilege($1,$2,'EXECUTE')`, cfg.User, conflictFunction).Scan(&allowed); err != nil || allowed != (service == "execution") {
			t.Fatalf("%s conflict function execute=%t err=%v", service, allowed, err)
		}
	}
	var forbiddenResult bool
	denied := tool.pool.QueryRow(context.Background(), `SELECT tenant_review_receipt_key_conflict('','','','','','')`).Scan(&forbiddenResult)
	var permission *pgconn.PgError
	if !errors.As(denied, &permission) || permission.Code != "42501" {
		t.Fatalf("actual Tool login could call conflict function: %v", denied)
	}
	cfg, err := pgx.ParseConfig(env.roleDSNs["tool"])
	if err != nil {
		t.Fatal(err)
	}
	qualified := pgx.Identifier{env.harness.schema}.Sanitize() + `.tenant_review_receipt_recovery_allowed(text,text,text,text,text,text,text)`
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
	stamp := pgx.Identifier{env.harness.schema}.Sanitize() + `.stamp_review_receipt_origin()`
	if _, err := env.harness.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+stamp+` TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err == nil {
		t.Fatal("unexpected PUBLIC origin trigger function grant accepted")
	}
	if _, err := env.harness.pool.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+stamp+` FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("exact origin function grants should be accepted: %v", err)
	}
	if _, err := env.harness.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+conflictFunction+` TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err == nil {
		t.Fatal("unexpected PUBLIC conflict function grant accepted")
	}
	if _, err := env.harness.pool.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+conflictFunction+` FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("exact conflict function grants should be accepted: %v", err)
	}
	if _, err := env.harness.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+conflictFunction+` TO `+role); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err == nil {
		t.Fatal("unexpected Tool conflict function grant accepted")
	}
	if _, err := env.harness.pool.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+conflictFunction+` FROM `+role); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("exact conflict grants after Tool revocation: %v", err)
	}
}

func TestReviewReceiptLegacyMigrationPreservesPostgres(t *testing.T) {
	for _, oldVersion := range []int{41, 42} {
		t.Run(fmt.Sprintf("schema=%d", oldVersion), func(t *testing.T) {
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
			// Reconstruct the old policy without inventing a historical origin.
			restoreReviewReceiptVersionForTest(t, ctxA, f.environment.harness.pool, f.environment.harness.schema, oldVersion)
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
				t.Fatalf("historical schema did not retain its old session fence: %d %v", before, err)
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
			if replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "pre-upgrade"), f.admin, f.caseID); err != nil || replay != original {
				t.Fatalf("legacy receipt must replay in its original session: %+v %v", replay, err)
			}
			for _, candidate := range []struct {
				ctx  context.Context
				user accounts.User
				want bool
			}{{ctxA, f.admin, false}, {ctxB, actor, true}} {
				tx, err := execution.beginContextTx(commandContext(candidate.ctx, candidate.user), pgx.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if err := setLocalScope(candidate.ctx, tx, userScope(candidate.user)); err != nil {
					t.Fatal(err)
				}
				route := "POST /api/cases/" + f.caseID + "/reviews"
				if _, err := tx.Exec(candidate.ctx, `SELECT set_config('app.review_receipt_route',$1,true),set_config('app.review_receipt_key','pre-upgrade',true)`, route); err != nil {
					t.Fatal(err)
				}
				var conflict bool
				err = tx.QueryRow(candidate.ctx, `SELECT tenant_review_receipt_key_conflict($1,$2,$3,$4,$5,'pre-upgrade')`, candidate.user.TenantID, candidate.user.OrgID, f.caseID, candidate.user.ID, route).Scan(&conflict)
				_ = tx.Rollback(candidate.ctx)
				if err != nil || conflict != candidate.want {
					t.Fatalf("legacy original session conflict=%t want=%t err=%v", conflict, candidate.want, err)
				}
			}
			if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "pre-upgrade"), actor, f.caseID); !isConflictFault(err) {
				t.Fatalf("legacy receipt without frozen origin must reject a new session: %v", err)
			}
			detail, err := f.core.GetCase(f.admin, f.caseID)
			if err != nil || len(detail.MaterialSelection.DocumentIDs) < 2 {
				t.Fatalf("legacy readiness fixture: %v", err)
			}
			if err := f.document.ConfirmMaterialSelection(ctxA, f.admin, f.caseID, detail.MaterialSelection.Version, detail.MaterialSelection.DocumentIDs[:1]); err != nil {
				t.Fatal(err)
			}
			if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "pre-upgrade"), actor, f.caseID); !isConflictFault(err) || !strings.Contains(err.Error(), "查看既有审查记录") {
				t.Fatalf("legacy conflict must precede changed readiness: %v", err)
			}
			if replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "pre-upgrade"), f.admin, f.caseID); err != nil || replay != original {
				t.Fatalf("same original session lost its receipt after readiness changed: %+v %v", replay, err)
			}
			if _, err := f.environment.harness.pool.Exec(ctxA, `UPDATE sessions SET context_version=context_version+1 WHERE token=$1`, sessionTokenDigest(f.sessionToken)); err != nil {
				t.Fatal(err)
			}
			rebound, _, err := f.core.AuthenticateSession(context.Background(), f.sessionToken)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxA, "pre-upgrade"), rebound, f.caseID); !isConflictFault(err) {
				t.Fatalf("legacy receipt must reject the original session after its version changed: %v", err)
			}
			var afterStamp, afterDigest string
			var afterVersion int64
			var afterResponse []byte
			var afterOrigin *string
			if err := f.environment.harness.pool.QueryRow(ctxB, `SELECT command_session,command_version,request_digest,response_json,command_origin_org FROM execution_write_receipts WHERE case_id=$1`, f.caseID).Scan(&afterStamp, &afterVersion, &afterDigest, &afterResponse, &afterOrigin); err != nil || stamp != afterStamp || version != afterVersion || digest != afterDigest || !bytes.Equal(response, afterResponse) || afterOrigin != nil {
				t.Fatalf("upgrade rewrote the original receipt: %v", err)
			}
			if err := f.core.Logout(f.sessionToken); err != nil {
				t.Fatal(err)
			}
			if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "pre-upgrade"), actor, f.caseID); !isConflictFault(err) {
				t.Fatalf("deleted old session must not supply an inferred origin: %v", err)
			}
			var runs, attempts, jobs, outbox, receipts int
			err = f.environment.harness.pool.QueryRow(ctxB, `SELECT
				(SELECT count(*) FROM review_runs WHERE case_id=$1),
				(SELECT count(*) FROM review_attempts WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)),
				(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
				(SELECT count(*) FROM outbox_events WHERE case_id=$1),
				(SELECT count(*) FROM execution_write_receipts WHERE case_id=$1)`, f.caseID).Scan(&runs, &attempts, &jobs, &outbox, &receipts)
			if err != nil || runs != 1 || attempts != 1 || jobs != 1 || outbox != 1 || receipts != 1 {
				t.Fatalf("legacy retry created facts: runs=%d attempts=%d jobs=%d outbox=%d receipts=%d err=%v", runs, attempts, jobs, outbox, receipts, err)
			}
		})
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

func TestReviewReceiptOriginDiffersFromCaseOrganizationPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL and restricted roles required")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	document.UseOperationAuthority(durableTestOperationAuthority{core: core})
	execution.UseOperationAuthority(durableTestOperationAuthority{core: core})
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	profile := accounts.User{ID: "review-cross-org-origin", DisplayName: "Review owner", Role: "clerk", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://origin-identity.invalid", Subject: "owner"}
	otherProfile := accounts.User{ID: "review-other-actor", DisplayName: "Other reviewer", Role: "clerk", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: profile.Issuer, Subject: "other"}
	raw, err := json.Marshal(map[string]any{"users": []accounts.User{profile, otherProfile}})
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
	const principal = "33700000-0000-4000-8000-000000000001"
	const memberA = "33700000-0000-4000-8000-000000000002"
	const memberB = "33700000-0000-4000-8000-000000000003"
	const memberOtherTenant = "33700000-0000-4000-8000-000000000004"
	const otherPrincipal = "33700000-0000-4000-8000-000000000005"
	const memberOtherActor = "33700000-0000-4000-8000-000000000006"
	mapping := IdentityCrosswalk{
		Version:    1,
		Tenants:    []IdentityTenantMap{{ID: "origin-tenant", DisplayName: "Origin tenant", Status: "active"}, {ID: "other-tenant", DisplayName: "Other tenant", Status: "active"}},
		OrgUnits:   []IdentityOrgUnitMap{{TenantID: "origin-tenant", ID: "org-a", DisplayName: "Case organization", Status: "active"}, {TenantID: "origin-tenant", ID: "org-b", DisplayName: "Origin organization", Status: "active"}, {TenantID: "other-tenant", ID: "org-c", DisplayName: "Other tenant", Status: "active"}},
		Principals: []IdentityPrincipalMap{{ID: principal, ActorID: profile.ID, Issuer: profile.Issuer, Subject: profile.Subject, Status: "active"}, {ID: otherPrincipal, ActorID: otherProfile.ID, Issuer: otherProfile.Issuer, Subject: otherProfile.Subject, Status: "active"}},
		Memberships: []IdentityMembershipMap{
			{ID: memberA, ActorID: profile.ID, TenantID: "origin-tenant", OrgUnitID: "org-a", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: memberB, ActorID: profile.ID, TenantID: "origin-tenant", OrgUnitID: "org-b", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: memberOtherTenant, ActorID: profile.ID, TenantID: "other-tenant", OrgUnitID: "org-c", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: memberOtherActor, ActorID: otherProfile.ID, TenantID: "origin-tenant", OrgUnitID: "org-a", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
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
	loginFor := func(person accounts.User, member string) (context.Context, accounts.User, string) {
		t.Helper()
		result, err := core.ResolveVerifiedOIDC(base, person, "client")
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
		return WithAdmissionSessionToken(base, result.SessionToken), user, result.SessionToken
	}
	login := func(member string) (context.Context, accounts.User, string) { return loginFor(profile, member) }
	ctxA, actorA, _ := login(memberA)
	caseID, err := core.CreateCase(actorA, "跨组织原命令回执", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ role, body string }{{"procurement", "采购要求质保三年。"}, {"draft_contract", "合同约定质保两年。"}} {
		if _, err := document.AddDocument(ctxA, actorA, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatal(err)
		}
	}
	ctxB, actorB, tokenB := login(memberB)
	seen, err := execution.snapshotAs(actorB)
	if err != nil {
		t.Fatal(err)
	}
	item, found := seen.caseByID(caseID)
	if !found || item.OwnerOrgID != "org-a" || actorB.OrgID != "org-b" || !authorizeCase(item, actorB, access.Review) {
		t.Fatal("fixture lacks cross-organization review authority")
	}
	original, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "origin-key"), actorB, caseID)
	if err != nil {
		t.Fatalf("authorized origin command: %v", err)
	}
	keyConflict := func(ctx context.Context, actor accounts.User, requestedActor, key string) (bool, error) {
		t.Helper()
		tx, err := execution.beginContextTx(commandContext(ctx, actor), pgx.TxOptions{})
		if err != nil {
			return false, err
		}
		defer tx.Rollback(context.Background())
		if err := setLocalScope(ctx, tx, userScope(actor)); err != nil {
			return false, err
		}
		route := "POST /api/cases/" + caseID + "/reviews"
		if _, err := tx.Exec(ctx, `SELECT set_config('app.review_receipt_route',$1,true),set_config('app.review_receipt_key',$2,true)`, route, key); err != nil {
			return false, err
		}
		var unavailable bool
		err = tx.QueryRow(ctx, `SELECT tenant_review_receipt_key_conflict($1,$2,$3,$4,$5,$6)`, "origin-tenant", "org-a", caseID, requestedActor, route, key).Scan(&unavailable)
		return unavailable, err
	}
	t.Run("same_origin_new_session", func(t *testing.T) {
		ctx, actor, _ := login(memberB)
		if unavailable, err := keyConflict(ctx, actor, profile.ID, "origin-key"); err != nil || unavailable {
			t.Fatalf("same-origin committed row must not become a conflict: %t %v", unavailable, err)
		}
		replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctx, "origin-key"), actor, caseID)
		if err != nil || replay != original {
			t.Fatalf("same origin must replay: original=%+v replay=%+v error=%v", original, replay, err)
		}
		if _, err := execution.StartReviewAsyncScope(WithReviewIdempotency(ctx, "origin-key"), actor, caseID, "", "", ReviewScopeSingleContract); !isConflictFault(err) {
			t.Fatalf("same key with different body must conflict: %v", err)
		}
	})
	t.Run("case_organization_must_not_replay", func(t *testing.T) {
		ctx, actor, _ := login(memberA)
		if unavailable, err := keyConflict(ctx, actor, profile.ID, "origin-key"); err != nil || !unavailable {
			t.Fatalf("other-origin key was not identified as unavailable: %t %v", unavailable, err)
		}
		if unavailable, err := keyConflict(ctx, actor, profile.ID, "unknown-key"); err != nil || unavailable {
			t.Fatalf("another key was reported as unavailable: %t %v", unavailable, err)
		}
		replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctx, "origin-key"), actor, caseID)
		if err == nil {
			t.Fatalf("other organization replayed original acceptance: %+v", replay)
		}
		detail, err := core.GetCase(actorA, caseID)
		if err != nil || len(detail.MaterialSelection.DocumentIDs) < 2 {
			t.Fatalf("cross-org readiness fixture: %v", err)
		}
		if err := document.ConfirmMaterialSelection(ctxA, actorA, caseID, detail.MaterialSelection.Version, detail.MaterialSelection.DocumentIDs[:1]); err != nil {
			t.Fatal(err)
		}
		if _, err := execution.StartReviewAsync(WithReviewIdempotency(ctx, "origin-key"), actor, caseID); !isConflictFault(err) || !strings.Contains(err.Error(), "查看既有审查记录") {
			t.Fatalf("other-origin conflict must precede changed readiness: %v", err)
		}
		if replay, err := execution.StartReviewAsync(WithReviewIdempotency(ctxB, "origin-key"), actorB, caseID); err != nil || replay != original {
			t.Fatalf("origin lost its receipt after readiness changed: %+v %v", replay, err)
		}
	})
	t.Run("other_tenant_and_other_actor_cannot_read_receipt", func(t *testing.T) {
		for _, candidate := range []struct {
			person accounts.User
			member string
		}{{profile, memberOtherTenant}, {otherProfile, memberOtherActor}} {
			ctx, actor, _ := loginFor(candidate.person, candidate.member)
			if unavailable, err := keyConflict(ctx, actor, profile.ID, "origin-key"); err != nil || unavailable {
				t.Fatalf("foreign tenant or actor learned key occupancy: %t %v", unavailable, err)
			}
			if candidate.member == memberOtherActor {
				seen, err := execution.snapshotAs(actor)
				if err != nil {
					t.Fatal(err)
				}
				item, ok := seen.caseByID(caseID)
				if !ok || !authorizeCase(item, actor, access.Review) {
					t.Fatal("other actor lacks its independent case Review grant")
				}
			}
			tx, err := execution.beginContextTx(commandContext(ctx, actor), pgx.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := setLocalScope(ctx, tx, userScope(actor)); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('app.review_receipt_route',$1,true),set_config('app.review_receipt_key','origin-key',true)`, "POST /api/cases/"+caseID+"/reviews"); err != nil {
				t.Fatal(err)
			}
			var count int
			err = tx.QueryRow(ctx, `SELECT count(*) FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='origin-key'`, caseID).Scan(&count)
			_ = tx.Rollback(ctx)
			if err != nil || count != 0 {
				t.Fatalf("foreign tenant or actor saw origin receipt: count=%d err=%v", count, err)
			}
		}
	})
	t.Run("origin_is_immutable_after_switch_and_logout", func(t *testing.T) {
		var caseOrg, originOrg, stamp string
		var version int64
		if err := env.harness.pool.QueryRow(base, `SELECT org_id,command_origin_org,command_session,command_version FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='origin-key'`, caseID).Scan(&caseOrg, &originOrg, &stamp, &version); err != nil || caseOrg != "org-a" || originOrg != "org-b" || stamp != sessionTokenDigest(tokenB) || version != 1 {
			t.Fatalf("incorrect frozen origin or session: case_org=%q origin=%q version=%d err=%v", caseOrg, originOrg, version, err)
		}
		// A caller-supplied origin is overwritten from the validated DB session.
		tx, err := execution.beginContextTx(commandContext(ctxB, actorB), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctxB, tx, userScope(actorB)); err != nil {
			t.Fatal(err)
		}
		var derived string
		err = tx.QueryRow(ctxB, `INSERT INTO execution_write_receipts(tenant_id,org_id,actor_id,route,idempotency_key,request_digest,case_id,response_json,command_origin_org)
			VALUES($1,$2,$3,$4,'origin-spoof',repeat('a',64),$5,'{}'::jsonb,'org-a') RETURNING command_origin_org`, actorB.TenantID, caseOrg, actorB.ID, "POST /api/cases/"+caseID+"/reviews", caseID).Scan(&derived)
		_ = tx.Rollback(ctxB)
		if err != nil || derived != "org-b" {
			t.Fatalf("caller-supplied origin was trusted: origin=%q err=%v", derived, err)
		}
		for _, stmt := range []string{`UPDATE execution_write_receipts SET command_origin_org='org-a' WHERE case_id=$1`, `DELETE FROM execution_write_receipts WHERE case_id=$1`} {
			if _, err := env.harness.pool.Exec(base, stmt, caseID); err == nil {
				t.Fatal("append-only receipt origin was rewritten")
			}
		}
		if err := core.SwitchSessionContext(base, tokenB, "1", memberA); err != nil {
			t.Fatal(err)
		}
		switched, _, err := core.AuthenticateSession(base, tokenB)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := execution.StartReviewAsync(WithReviewIdempotency(WithAdmissionSessionToken(base, tokenB), "origin-key"), switched, caseID); !isConflictFault(err) {
			t.Fatalf("switched source session must not replay in org-a: %v", err)
		}
		freshCtx, fresh, _ := login(memberB)
		if replay, err := execution.StartReviewAsync(WithReviewIdempotency(freshCtx, "origin-key"), fresh, caseID); err != nil || replay != original {
			t.Fatalf("same origin could not replay after original session switched: %+v %v", replay, err)
		}
		if err := core.Logout(tokenB); err != nil {
			t.Fatal(err)
		}
		var sessionExists bool
		if err := env.harness.pool.QueryRow(base, `SELECT EXISTS(SELECT 1 FROM sessions WHERE token=$1)`, sessionTokenDigest(tokenB)).Scan(&sessionExists); err != nil || sessionExists {
			t.Fatalf("original session was not deleted: exists=%t err=%v", sessionExists, err)
		}
		if replay, err := execution.StartReviewAsync(WithReviewIdempotency(freshCtx, "origin-key"), fresh, caseID); err != nil || replay != original {
			t.Fatalf("same origin could not replay after original session logout: %+v %v", replay, err)
		}
		var after string
		if err := env.harness.pool.QueryRow(base, `SELECT command_origin_org FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='origin-key'`, caseID).Scan(&after); err != nil || after != originOrg {
			t.Fatalf("frozen origin changed: %q %v", after, err)
		}
	})
	t.Run("subtree_authorized_readmission", func(t *testing.T) {
		mapping.Memberships[1].Grants = []access.Grant{{Role: "case_owner", Scope: "org_subtree", ResolvedOrgIDs: []string{"org-a"}}}
		importMapping()
		ctxA, owner, _ := login(memberA)
		caseID, err := core.CreateCase(owner, "跨组织子树授权重新受理", "procurement_ict")
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct{ role, body string }{{"procurement", "采购文件"}, {"draft_contract", "拟签合同"}} {
			if _, err := document.AddDocument(ctxA, owner, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := document.AddDocument(ctxA, owner, caseID, "attachment", "scan.png", "image/png", model.SampleWarrantyPNG); err != nil {
			t.Fatal(err)
		}
		ctxB, actor, token := login(memberB)
		seen, err := execution.snapshotAs(actor)
		if err != nil {
			t.Fatal(err)
		}
		item, ok := seen.caseByID(caseID)
		if !ok || !authorizeCase(item, actor, access.Review) {
			t.Fatal("org subtree did not grant Review on case organization")
		}
		first, err := execution.StartReviewAsync(ctxB, actor, caseID)
		if err != nil {
			t.Fatal(err)
		}
		if err := execution.CancelReview(ctxB, actor, caseID, first.RunID); err != nil {
			t.Fatal(err)
		}
		otherExecution := env.openRole(t, "execution")
		otherExecution.UseOperationAuthority(durableTestOperationAuthority{core: core})
		otherExecution.UseDocumentPreparer(document)
		otherExecution.UseModels(&frozenOCRModel{})
		initialCtx, initialActor, initialToken := login(memberB)
		var initial [2]JobAcceptance
		var initialErrs [2]error
		var initialGroup sync.WaitGroup
		for i, call := range []struct {
			st   *Store
			ctx  context.Context
			user accounts.User
		}{{execution, ctxB, actor}, {otherExecution, initialCtx, initialActor}} {
			initialGroup.Add(1)
			go func(i int, call struct {
				st   *Store
				ctx  context.Context
				user accounts.User
			}) {
				defer initialGroup.Done()
				initial[i], initialErrs[i] = call.st.StartReviewAsync(readmissionContext(call.ctx, "subtree-origin", first), call.user, caseID)
			}(i, call)
		}
		initialGroup.Wait()
		if initialErrs[0] != nil || initialErrs[1] != nil || initial[0] != initial[1] {
			t.Fatalf("cross-organization concurrent first admission: values=%+v errors=%v", initial, initialErrs)
		}
		accepted := initial[0]
		var winningStamp string
		if err := env.harness.pool.QueryRow(base, `SELECT command_session FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='subtree-origin'`, caseID).Scan(&winningStamp); err != nil {
			t.Fatal(err)
		}
		if winningStamp == sessionTokenDigest(initialToken) {
			token = initialToken
		} else if winningStamp != sessionTokenDigest(token) {
			t.Fatal("receipt was not stamped with an admitting session")
		}
		freshCtx, fresh, _ := login(memberB)
		otherCtx, other, _ := login(memberB)
		var values [2]JobAcceptance
		var errs [2]error
		var group sync.WaitGroup
		for i, call := range []struct {
			ctx  context.Context
			user accounts.User
		}{{freshCtx, fresh}, {otherCtx, other}} {
			group.Add(1)
			go func(i int, call struct {
				ctx  context.Context
				user accounts.User
			}) {
				defer group.Done()
				values[i], errs[i] = execution.StartReviewAsync(readmissionContext(call.ctx, "subtree-origin", first), call.user, caseID)
			}(i, call)
		}
		group.Wait()
		if errs[0] != nil || errs[1] != nil || values[0] != accepted || values[1] != accepted {
			t.Fatalf("cross-session subtree replays: values=%+v errs=%v", values, errs)
		}
		if _, err := execution.StartReviewAsync(WithReviewIdempotency(freshCtx, "subtree-origin"), fresh, caseID); !isConflictFault(err) {
			t.Fatalf("changed request must conflict: %v", err)
		}
		if _, err := execution.StartReviewAsync(readmissionContext(freshCtx, "other-new-intent", first), fresh, caseID); !isConflictFault(err) {
			t.Fatalf("different key must revalidate the target: %v", err)
		}
		caseCtx, caseActor, _ := login(memberA)
		if _, err := execution.StartReviewAsync(readmissionContext(caseCtx, "subtree-origin", first), caseActor, caseID); !isConflictFault(err) {
			t.Fatalf("case organization must not replay subtree command: %v", err)
		}
		if err := core.SwitchSessionContext(base, token, "1", memberA); err != nil {
			t.Fatal(err)
		}
		if err := core.Logout(token); err != nil {
			t.Fatal(err)
		}
		var sessionExists bool
		if err := env.harness.pool.QueryRow(base, `SELECT EXISTS(SELECT 1 FROM sessions WHERE token=$1)`, sessionTokenDigest(token)).Scan(&sessionExists); err != nil || sessionExists {
			t.Fatalf("original readmission session was not deleted: exists=%t err=%v", sessionExists, err)
		}
		if replay, err := execution.StartReviewAsync(readmissionContext(freshCtx, "subtree-origin", first), fresh, caseID); err != nil || replay != accepted {
			t.Fatalf("subtree origin lost after original session switch and logout: %+v %v", replay, err)
		}
		var caseOrg, originOrg string
		var runs, attempts, jobs, outbox, receipts int
		err = env.harness.pool.QueryRow(base, `SELECT org_id,command_origin_org,
			(SELECT count(*) FROM review_runs WHERE case_id=$1),
			(SELECT count(*) FROM review_attempts WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)),
			(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
			(SELECT count(*) FROM outbox_events WHERE case_id=$1),
			(SELECT count(*) FROM execution_write_receipts WHERE case_id=$1)
			FROM execution_write_receipts WHERE case_id=$1 AND idempotency_key='subtree-origin'`, caseID).Scan(&caseOrg, &originOrg, &runs, &attempts, &jobs, &outbox, &receipts)
		if err != nil || caseOrg != "org-a" || originOrg != "org-b" || runs != 2 || attempts != 2 || jobs != 2 || outbox != 2 || receipts != 1 {
			t.Fatalf("subtree facts: case_org=%q origin=%q runs=%d attempts=%d jobs=%d outbox=%d receipts=%d err=%v", caseOrg, originOrg, runs, attempts, jobs, outbox, receipts, err)
		}
		state, err := execution.snapshotScoped(freshCtx, userScope(fresh))
		if err != nil {
			t.Fatal(err)
		}
		old, ok := state.attemptByID(first.AttemptID)
		if !ok || old.State != "cancelled" || old.ErrorCode == nil || *old.ErrorCode != "cancelled_by_user" {
			t.Fatalf("cancelled history changed: %+v", old)
		}
	})
}

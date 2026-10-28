package store

import (
	"context"
	"errors"
	"testing"

	"contractaudit/internal/accounts"
)

// TestSyncJobsRejectsSupersededGeneration pins the job write contract: a state
// write computed from a snapshot the worker no longer owns must fail closed
// instead of silently reporting success while the authoritative row keeps the
// other worker's claim.
func TestSyncJobsRejectsSupersededGeneration(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(admin, "队列代际回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。")); err != nil {
		t.Fatal(err)
	}
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := st.snapshot()
	var stale Job
	for _, job := range snapshot.Jobs {
		if job.ID == accepted.JobID {
			stale = job
		}
	}
	if stale.ID == "" || stale.State != "queued" {
		t.Fatalf("accepted review did not retain its queued job: %+v", stale)
	}
	// Another replica claims the job after this snapshot was read.
	if _, err := st.pool.Exec(context.Background(), `UPDATE worker_jobs
		SET state='running', generation=generation+1, worker_id='other-worker', lease_until=clock_timestamp()+interval '5 minutes'
		WHERE id=$1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := syncJobs(ctx, tx, []Job{stale}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("superseded job generation write was accepted: err=%v job=%+v", err, stale)
	}
	current := stale
	current.State, current.Generation, current.WorkerID = "cancelled", stale.Generation+1, "other-worker"
	if err := syncJobs(ctx, tx, []Job{current}); err != nil {
		t.Fatalf("current job generation write was rejected: %v", err)
	}
}

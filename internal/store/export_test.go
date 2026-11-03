package store

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

// RevokedFinalizeFixture exposes the existing distinct database-role fixture
// to external integration tests without exporting production functionality.
type RevokedFinalizeFixture struct{ fixture durableFixture }

func NewRevokedFinalizeFixture(t *testing.T) *RevokedFinalizeFixture {
	t.Helper()
	approvalDecisionTestDirectory(t)
	setup, ok := newDurableCaseFixture(t, initializeApprovalSession)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL required")
	}
	execution := setup.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: setup.core})
	execution.UseDocumentPreparer(setup.document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsync(WithAdmissionSessionToken(context.Background(), setup.sessionToken), setup.admin, setup.caseID)
	if err != nil {
		t.Fatalf("accept enterprise attempt: %v", err)
	}
	claim, err := execution.ClaimNextJob(context.Background(), "revoked-http-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim enterprise attempt: %v", err)
	}
	return &RevokedFinalizeFixture{fixture: durableFixture{environment: setup.environment, core: setup.core, document: setup.document,
		execution: execution, admin: setup.admin, actor: setup.admin, caseID: setup.caseID, runID: accepted.RunID,
		attemptID: accepted.AttemptID, jobID: accepted.JobID, claim: claim, dir: setup.dir}}
}
func (f *RevokedFinalizeFixture) Core() *Store         { return f.fixture.core }
func (f *RevokedFinalizeFixture) Execution() *Store    { return f.fixture.execution }
func (f *RevokedFinalizeFixture) Actor() accounts.User { return f.fixture.actor }
func (f *RevokedFinalizeFixture) Snapshot(t *testing.T) any {
	return durableMutationSnapshot(t, f.fixture, f.fixture.claim)
}
func (f *RevokedFinalizeFixture) Finalize() error {
	claim := f.fixture.claim
	return f.fixture.execution.finishAttemptNarrow(withExecutionClaim(context.Background(), claim), claim.RunID, claim.AttemptID, "completed", "", "")
}
func (f *RevokedFinalizeFixture) State(t *testing.T) (string, string, string, int, int) {
	t.Helper()
	state := durableSnapshot(t, f.fixture.execution, f.fixture.claim)
	attempt, ok := state.attemptByID(f.fixture.attemptID)
	if !ok {
		t.Fatal("attempt missing")
	}
	run, ok := state.runByID(f.fixture.runID)
	if !ok {
		t.Fatal("run missing")
	}
	code := ""
	if attempt.ErrorCode != nil {
		code = *attempt.ErrorCode
	}
	return attempt.State, run.State, code, len(state.findingsForAttempt(f.fixture.runID, f.fixture.attemptID)), len(state.evaluationsForAttempt(f.fixture.runID, f.fixture.attemptID))
}

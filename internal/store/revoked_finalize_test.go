package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"contractaudit/internal/operationauthority"
)

type revokedFinalizeAuthority struct{}

func (revokedFinalizeAuthority) ResolveOperation(context.Context, model.DelegatedScope) (accounts.User, error) {
	return accounts.User{}, operationauthority.ErrConfirmedRevocation
}

// A previously revoked claim can record only cancellation even if ordinary
// authorization is restored between initial refusal and the final transition.
func TestRevokedFinalizeForcesCancellationAndCannotUpgrade(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL required")
	}
	ctx := withExecutionClaim(context.Background(), fixture.claim)
	fixture.execution.UseOperationAuthority(revokedFinalizeAuthority{})
	if err := fixture.execution.finishAttemptNarrow(ctx, fixture.runID, fixture.attemptID, "completed", "incorrect partial", "incorrect error"); err != nil {
		t.Fatalf("revoked cancellation: %v", err)
	}
	after := durableMutationSnapshot(t, fixture, fixture.claim)
	if after.Attempt.State != "cancelled" || after.Run.State != "cancelled" || after.Attempt.ErrorCode == nil || *after.Attempt.ErrorCode != "authorization_revoked" || after.Run.PartialReason != "" {
		t.Fatalf("revoked attempt did not remain cancellation: %+v", after)
	}
	snapshot := durableSnapshot(t, fixture.execution, fixture.claim)
	if len(snapshot.findingsForAttempt(fixture.runID, fixture.attemptID)) != 0 || len(snapshot.evaluationsForAttempt(fixture.runID, fixture.attemptID)) != 0 {
		t.Fatal("revoked cancellation published results")
	}
	fixture.execution.UseOperationAuthority(durableTestOperationAuthority{core: fixture.core})
	if err := fixture.execution.finishAttemptNarrow(ctx, fixture.runID, fixture.attemptID, "completed", "", ""); err == nil {
		t.Fatalf("restored actor upgraded terminal cancellation: %v", err)
	}
	if got := durableMutationSnapshot(t, fixture, fixture.claim); !reflect.DeepEqual(got, after) {
		t.Fatalf("terminal cancellation changed after restoration: before=%+v after=%+v", after, got)
	}
}

func TestRevokedFinalizeRejectsStaleWorkerGenerationLeaseAndEpoch(t *testing.T) {
	for _, name := range []string{"worker", "generation", "expired", "epoch"} {
		t.Run(name, func(t *testing.T) {
			fixture, ok := newDurableFixture(t)
			if !ok {
				t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL required")
			}
			fixture.execution.UseOperationAuthority(revokedFinalizeAuthority{})
			old := fixture.claim
			bound := old
			if name == "expired" {
				expireDurableClaim(t, fixture)
			} else if name == "epoch" {
				// Keep the admitted database epoch immutable; only the cached
				// claim is stale relative to authoritative PostgreSQL state.
				old.TenantLiveEpoch++
				fixture.execution.setClaim(old)
				bound = old
			} else {
				tx, err := fixture.execution.pool.Begin(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if err := setLocalScope(context.Background(), tx, old.scope()); err != nil {
					t.Fatal(err)
				}
				query := map[string]string{
					"worker":     `UPDATE worker_jobs SET worker_id='replacement-worker' WHERE id=$1`,
					"generation": `UPDATE worker_jobs SET generation=generation+1 WHERE id=$1`,
				}[name]
				tag, err := tx.Exec(context.Background(), query, old.JobID)
				if err != nil || tag.RowsAffected() != 1 {
					t.Fatalf("change database %s fence: rows=%d err=%v", name, tag.RowsAffected(), err)
				}
				if err := tx.Commit(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			before := durableMutationSnapshot(t, fixture, old)
			err := fixture.execution.finishAttemptNarrow(withExecutionClaim(context.Background(), bound), fixture.runID, fixture.attemptID, "completed", "", "")
			if !errors.Is(err, errLeaseLost) {
				t.Fatalf("stale %s claim cancellation: %v", name, err)
			}
			after := durableMutationSnapshot(t, fixture, old)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("stale %s changed durable state: before=%+v after=%+v", name, before, after)
			}
		})
	}
}

// breakDirectoryAfterAuthority reproduces a directory failure after the
// initial authority response but before terminal reauthorization.
type breakDirectoryAfterAuthority struct {
	t    *testing.T
	core *Store
	path string
}

func (a breakDirectoryAfterAuthority) ResolveOperation(ctx context.Context, scope model.DelegatedScope) (accounts.User, error) {
	user, err := (durableTestOperationAuthority{core: a.core}).ResolveOperation(ctx, scope)
	if err != nil {
		return user, err
	}
	profile := user
	profile.Issuer, profile.Subject = "fixture-issuer", "fixture-subject"
	raw, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{Users: []accounts.User{profile}})
	if err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(a.path, raw, 0600); err != nil {
		a.t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(a.path); err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(a.path, []byte(`{"users":`), 0600); err != nil {
		a.t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(a.path); err == nil {
		a.t.Fatal("broken directory accepted")
	}
	return user, nil
}

func TestFinalizeDirectoryUnavailableAfterInitialAuthorityDoesNotCancel(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL required")
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	before := durableMutationSnapshot(t, fixture, fixture.claim)
	fixture.execution.UseOperationAuthority(breakDirectoryAfterAuthority{t: t, core: fixture.core, path: filepath.Join(t.TempDir(), "directory.json")})
	ctx := withExecutionClaim(context.Background(), fixture.claim)
	if err := fixture.execution.finishAttemptNarrow(ctx, fixture.runID, fixture.attemptID, "completed", "", ""); err == nil {
		t.Fatal("unavailable directory allowed completion")
	}
	if got := durableMutationSnapshot(t, fixture, fixture.claim); !reflect.DeepEqual(got, before) {
		t.Fatalf("directory failure changed attempt: before=%+v after=%+v", before, got)
	}
	if err := accounts.ConfigureDirectory(""); err != nil {
		t.Fatal(err)
	}
	fixture.execution.UseOperationAuthority(durableTestOperationAuthority{core: fixture.core})
	if err := fixture.execution.finishAttemptNarrow(ctx, fixture.runID, fixture.attemptID, "completed", "", ""); err != nil {
		t.Fatalf("completion after directory recovery: %v", err)
	}
	got := durableMutationSnapshot(t, fixture, fixture.claim)
	if got.Attempt.State != "completed" {
		t.Fatalf("recovered attempt state: %+v", got.Attempt)
	}
}

package app

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type completionCASRepo struct {
	CollectionCompletionRepository
	inserted int
}

func (r *completionCASRepo) InsertSnapshot(context.Context, model.CollectionLeaseToken, model.OperatorSnapshotSet, string) error {
	r.inserted++
	return nil
}
func (r *completionCASRepo) InsertObservation(context.Context, model.CollectionLeaseToken, string, PreparedObservation, time.Time) error {
	r.inserted++
	return nil
}
func (r *completionCASRepo) CompleteClaim(context.Context, model.CollectionLeaseToken, string, []byte, time.Time) (bool, error) {
	return false, nil
}

type completionClock struct{}

func (completionClock) Now() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 123456789, time.UTC) }
func TestCompletionCASFailureReturnsConstructedResult(t *testing.T) {
	repo := &completionCASRepo{}
	result, err := CompleteCollectionAfterLock(context.Background(), repo, model.CollectionLeaseToken{Identity: model.CollectionIdentity{CaseID: "case"}, CollectionID: "col"}, model.OperatorObjectBinding{}, time.Time{}, "set", PreparedObservation{Value: auditext.Observation{SnapshotID: "obs"}}, completionClock{})
	f, ok := err.(*fault.Error)
	if !ok || f.Status != 409 || repo.inserted != 2 || result.SnapshotSetID != "set" || len(result.Observations) != 1 || result.CollectedAt.Nanosecond() != 123456000 {
		t.Fatalf("result=%#v err=%v inserts=%d", result, err, repo.inserted)
	}
}

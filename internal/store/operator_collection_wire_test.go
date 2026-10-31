package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOperatorCollectionJobWireStateShapes(t *testing.T) {
	retryAfter := time.Date(2026, 10, 31, 3, 4, 5, 0, time.UTC)
	active, err := json.Marshal(OperatorCollectionJob{CollectionID: "col-1", CaseID: "case-1", State: "in_progress", RetryAfter: &retryAfter})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(active), `{"collection_id":"col-1","case_id":"case-1","state":"in_progress","retry_after":"2026-10-31T03:04:05Z"}`; got != want {
		t.Fatalf("active collection recovery wire changed: got %s want %s", got, want)
	}
	resultID := "snapshot-set-1"
	completed, err := json.Marshal(OperatorCollectionJob{CollectionID: "col-1", CaseID: "case-1", State: "completed", SnapshotSetID: &resultID,
		SnapshotSet: &OperatorSnapshotSet{CaseID: "case-1", CollectionID: "col-1", SnapshotSetID: resultID, Status: "available"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(completed), `{"collection_id":"col-1","case_id":"case-1","state":"completed","snapshot_set_id":"snapshot-set-1","snapshot_set":{"case_id":"case-1","snapshot_set_id":"snapshot-set-1","binding_version":"","connector_id":"","external_object_id":"","source_system":"","business_at":"0001-01-01T00:00:00Z","status":"available","reason_code":"","collected_by":"","collected_at":"0001-01-01T00:00:00Z","observations":null}}`; got != want {
		t.Fatalf("completed collection recovery wire changed: got %s want %s", got, want)
	}
}

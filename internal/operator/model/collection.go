package model

import (
	"time"

	"contractaudit/internal/auditext"
)

type OperatorSnapshotSet struct {
	CaseID           string                 `json:"case_id"`
	CollectionID     string                 `json:"-"`
	SnapshotSetID    string                 `json:"snapshot_set_id"`
	BindingVersion   string                 `json:"binding_version"`
	ConnectorID      string                 `json:"connector_id"`
	ExternalObjectID string                 `json:"external_object_id"`
	SourceSystem     string                 `json:"source_system"`
	BusinessAt       time.Time              `json:"business_at"`
	Status           string                 `json:"status"`
	ReasonCode       string                 `json:"reason_code"`
	CollectedBy      string                 `json:"collected_by"`
	CollectedAt      time.Time              `json:"collected_at"`
	Observations     []auditext.Observation `json:"observations"`
}
type OperatorCollectionJob struct {
	CollectionID  string               `json:"collection_id"`
	CaseID        string               `json:"case_id"`
	State         string               `json:"state"`
	RetryAfter    *time.Time           `json:"retry_after,omitempty"`
	SnapshotSetID *string              `json:"snapshot_set_id,omitempty"`
	SnapshotSet   *OperatorSnapshotSet `json:"snapshot_set,omitempty"`
}
type SnapshotRecord struct {
	Value         OperatorSnapshotSet `json:"-"`
	RequestSHA256 string              `json:"-"`
}
type CollectionIdentity struct{ TenantID, OrgID, CaseID, ActorID, Key string }
type CollectionClaim struct {
	CollectionID, RequestSHA256, Status string
	Generation                          int64
	Owner                               *string
	LeaseUntil                          *time.Time
	ResultID                            *string
}
type CollectionLeaseToken struct {
	Identity                           CollectionIdentity
	CollectionID, RequestSHA256, Owner string
	Generation                         int64
}

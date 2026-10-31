package store

import "time"

// Job is the durable lease record for one immutable attempt.
type Job struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	OrgID      string     `json:"org_id"`
	CaseID     string     `json:"case_id"`
	RunID      string     `json:"run_id"`
	AttemptID  string     `json:"attempt_id"`
	ActorID    string     `json:"actor_id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	Generation int64      `json:"generation"`
	WorkerID   string     `json:"worker_id,omitempty"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	Heartbeat  *time.Time `json:"heartbeat_at,omitempty"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at"`
}

// OutboxRecord is the durable acceptance notice committed with the attempt and job.
type OutboxRecord struct {
	ID          string  `json:"id"`
	JobID       string  `json:"job_id"`
	TenantID    string  `json:"tenant_id"`
	OrgID       string  `json:"org_id"`
	CaseID      string  `json:"case_id"`
	EventType   string  `json:"event_type"`
	Payload     string  `json:"payload"`
	State       string  `json:"state"`
	CreatedAt   string  `json:"created_at"`
	DeliveredAt *string `json:"delivered_at,omitempty"`
}

// JobAcceptance identifies the durable review accepted by the API.
type JobAcceptance struct {
	RunID     string `json:"id"`
	AttemptID string `json:"attempt_id"`
	JobID     string `json:"job_id"`
}

// JobClaim carries the fencing generation assigned by PostgreSQL.
type JobClaim struct {
	JobID      string
	TenantID   string
	OrgID      string
	CaseID     string
	RunID      string
	AttemptID  string
	ActorID    string
	Kind       string
	WorkerID   string
	Generation int64
	LeaseUntil time.Time
	stopReason string
	deadline   time.Time
}

func (claim JobClaim) scope() dbScope {
	return dbScope{
		ActorID: claim.ActorID, TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID, Role: "api",
		WorkerID: claim.WorkerID, JobID: claim.JobID, Generation: claim.Generation,
	}
}

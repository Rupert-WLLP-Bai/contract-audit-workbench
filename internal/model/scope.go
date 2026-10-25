package model

import "context"

// DelegatedScope identifies the current operation that is allowed to use a model.
// The gateway authenticates the calling service separately and checks these IDs
// against current PostgreSQL authority before forwarding a request.
type DelegatedScope struct {
	Kind             string `json:"kind"`
	TenantID         string `json:"tenant_id"`
	OrgID            string `json:"org_id"`
	CaseID           string `json:"case_id"`
	ActorID          string `json:"actor_id"`
	DocumentID       string `json:"document_id,omitempty"`
	JobID            string `json:"job_id,omitempty"`
	RunID            string `json:"run_id,omitempty"`
	AttemptID        string `json:"attempt_id,omitempty"`
	WorkerID         string `json:"worker_id,omitempty"`
	Generation       int64  `json:"generation,omitempty"`
	ParseSelectionID string `json:"parse_selection_id,omitempty"`
}

type delegatedScopeKey struct{}

// WithDelegatedScope attaches operation-local identity for authenticated gateway
// authorization. It never grants authority by itself.
func WithDelegatedScope(ctx context.Context, scope DelegatedScope) context.Context {
	return context.WithValue(ctx, delegatedScopeKey{}, scope)
}

// DelegatedScopeFromContext returns the operation identity, if present.
func DelegatedScopeFromContext(ctx context.Context) (DelegatedScope, bool) {
	scope, ok := ctx.Value(delegatedScopeKey{}).(DelegatedScope)
	return scope, ok
}

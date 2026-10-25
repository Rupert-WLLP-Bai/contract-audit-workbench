package toolgateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"contractaudit/internal/store"
)

const maxJobContextBytes = 4 << 10

// workerJobContext is the durable claim a worker presents on a read-only tool
// call. tenant_id and org_id are routing hints taken from the worker's own job
// claim; a wrong hint only hides rows (fail closed) and is never authority.
type workerJobContext struct {
	TenantID   string `json:"tenant_id"`
	OrgID      string `json:"org_id"`
	JobID      string `json:"job_id"`
	WorkerID   string `json:"worker_id"`
	Generation int64  `json:"generation"`
	AttemptID  string `json:"attempt_id"`
}

// authorizeWorkerCall accepts only the service-pair credential a durable worker
// holds. It is deliberately separate from the edge credential: a browser never
// sends it, and the edge strips the claim header before any public request.
func (h *handler) authorizeWorkerCall(r *http.Request) *rpcFailure {
	if len(h.workerCredential) < 32 {
		return unauthorizedFailure()
	}
	values := r.Header.Values("X-Contract-Service-Key")
	if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(h.workerCredential)) != 1 {
		return unauthorizedFailure()
	}
	return nil
}

// callFrozenEvidenceForWorker serves the read-only tool on behalf of a durable
// claim. The claim is resolved against the current job row before the read and
// again before the evidence is returned, so a cancelled, re-claimed, or
// re-leased job cannot deliver a stale result.
func (h *handler) callFrozenEvidenceForWorker(parent context.Context, raw string, args toolArguments) ([]byte, *rpcFailure) {
	resolver, ok := h.store.(ClaimResolver)
	if !ok {
		return nil, &rpcFailure{status: http.StatusNotImplemented, code: codeMethodNotFound, message: "Job-scoped tool calls are not enabled."}
	}
	claim, err := decodeWorkerJobContext(raw)
	if err != nil {
		return nil, invalidParams("Job context is invalid.")
	}
	// The requested attempt must be the one the claim names; the resolver then
	// proves it against the database row rather than trusting either side.
	if args.attemptID == "" || claim.AttemptID != args.attemptID {
		return nil, invalidParams("Job context does not match the requested attempt.")
	}
	request := store.ToolInvocationContext{
		TenantID: claim.TenantID, OrgID: claim.OrgID,
		JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation,
		CaseID: args.caseID, RunID: args.runID, AttemptID: args.attemptID,
	}
	authority, err := resolver.AuthorizeToolResolution(parent, request)
	if err != nil {
		return nil, workerResolutionFailure(err)
	}
	result, failure := h.callFrozenEvidence(parent, authority.User, args)
	if failure != nil {
		return nil, failure
	}
	if _, err := resolver.AuthorizeToolResolution(parent, request); err != nil {
		return nil, workerResolutionFailure(err)
	}
	return result, nil
}

func workerResolutionFailure(err error) *rpcFailure {
	if errors.Is(err, store.ErrToolScopeUnavailable) {
		return &rpcFailure{
			status: http.StatusConflict, code: codeInvalidRequest,
			message: "The current job scope is unavailable.",
		}
	}
	log.Printf("tool gateway worker resolution failed: %s", sanitizeStoreError(err))
	return storeFailure(nil, err)
}

// sanitizeStoreError keeps an internal storage error readable in the log without
// carrying a DSN or a credential into it.
func sanitizeStoreError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ReplaceAll(err.Error(), "\n", " ")
	message = postgresDSNPattern.ReplaceAllString(message, "postgres://REDACTED")
	if len(message) > 400 {
		message = message[:400]
	}
	return message
}

var postgresDSNPattern = regexp.MustCompile(`postgres://[^ "]+`)

func decodeWorkerJobContext(raw string) (workerJobContext, error) {
	var claim workerJobContext
	if len(raw) > maxJobContextBytes {
		return workerJobContext{}, errors.New("job context is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claim); err != nil {
		return workerJobContext{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return workerJobContext{}, errors.New("job context contains trailing data")
	}
	if strings.TrimSpace(claim.JobID) == "" || strings.TrimSpace(claim.WorkerID) == "" ||
		strings.TrimSpace(claim.AttemptID) == "" || claim.Generation < 1 ||
		strings.TrimSpace(claim.TenantID) == "" || strings.TrimSpace(claim.OrgID) == "" {
		return workerJobContext{}, errors.New("job context is incomplete")
	}
	return claim, nil
}

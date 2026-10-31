package toolgateway

import (
	"context"
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

const (
	ProtocolVersion = "2026-07-28"
	ToolName        = "frozen_evidence_read"
	ToolVersion     = "frozen-evidence/v1"

	maxRequestBytes  = 64 << 10
	maxToolResult    = 64 << 10
	maxSourceBytes   = 2 << 20
	maxWorkspaceDocs = 128
	maxFacts         = 32
	maxQuoteRunes    = 4096
	maxCallDeadline  = 60
)

// Backend is the minimal trusted store boundary required by the gateway.
// The concrete PostgreSQL Store implements it directly.
type Backend interface {
	AuthenticateSession(ctx context.Context, token string) (accounts.User, string, error)
	WorkspaceAttempt(user accounts.User, caseID, runID, attemptID string) (store.Workspace, error)
	ReserveToolCall(ctx context.Context, user accounts.User, in store.ToolCallRequest) (string, error)
	FinishToolCall(ctx context.Context, user accounts.User, callID, status string, outputBytes int64) error
}

// Options holds transport policy supplied by the hosting service.
type Options struct {
	// AllowedOrigins is an exact allowlist for browser requests carrying Origin.
	// Requests without Origin (for example native MCP clients) do not use CORS.
	AllowedOrigins []string
	// WorkerCredential authenticates a durable worker's read-only tool call. It
	// is required to enable that path and is never accepted from a browser: the
	// external edge strips every X-Contract-* header before forwarding.
	WorkerCredential string
}

// ClaimResolver resolves one durable worker claim to the authority a read-only
// tool call may use. The concrete PostgreSQL store implements it; a backend
// without it serves session-scoped callers only.
type ClaimResolver interface {
	AuthorizeToolResolution(ctx context.Context, request store.ToolInvocationContext) (store.ToolAuthority, error)
}

// NewHandler builds a stateless MCP HTTP handler for the 2026-07-28 profile.
// It has no initialize handshake, session state, or session-ID response header.
func NewHandler(st Backend, opts Options) http.Handler {
	return newHandler(st, opts)
}

var _ Backend = (*store.Store)(nil)

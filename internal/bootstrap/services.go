package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"contractaudit/internal/config"
	"contractaudit/internal/documentclient"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"contractaudit/internal/toolclient"
)

// OpenService assembles one independent service with its own database identity.
func OpenService(ctx context.Context, role string) (*Runtime, error) {
	switch role {
	case "core", "document", "execution", "worker", "model", "tool":
	default:
		return nil, errors.New("unknown service identity")
	}
	if configured := strings.TrimSpace(os.Getenv("SERVICE_ROLE")); configured != "" && configured != role {
		return nil, errors.New("configured service identity does not match executable")
	}
	return openConfigured(ctx, role == "core", role)
}

func serviceSelection(role string) (config.Selection, error) {
	if role == "" {
		return config.FromProcess()
	}
	key := map[string]string{"core": "CORE_DATABASE_URL", "document": "DOCUMENT_DATABASE_URL", "execution": "EXECUTION_DATABASE_URL", "worker": "EXECUTION_DATABASE_URL", "model": "MODEL_DATABASE_URL", "tool": "TOOL_DATABASE_URL"}[role]
	dsn := strings.TrimSpace(os.Getenv(key))
	if dsn == "" {
		return config.Selection{}, errors.New("service database identity is required")
	}
	environ := config.Environ{
		AppEnv: os.Getenv("APP_ENV"), DevDatabaseURL: os.Getenv("DEV_DATABASE_URL"),
		TestDatabaseURL: os.Getenv("TEST_DATABASE_URL"), ProdDatabaseURL: os.Getenv("PROD_DATABASE_URL"),
	}
	switch strings.ToLower(strings.TrimSpace(environ.AppEnv)) {
	case "", config.ModeDevelopment:
		environ.DevDatabaseURL = dsn
	case config.ModeTest:
		environ.TestDatabaseURL = dsn
	case config.ModeProduction:
		environ.ProdDatabaseURL = dsn
	default:
		return config.Selection{}, config.ErrUnknownMode
	}
	return config.Select(environ)
}

// serviceModelConfig assembles the model identity for one service role.
//
// Only the worker and the document service make model calls, so only they are
// required to hold a gateway URL and a private caller credential. The execution
// service never calls a model: in durable mode it accepts a persistent task and
// the worker runs the attempt, so execution only needs the same model
// *identity* to compute and compare a run signature. It therefore keeps the
// FromEnv chat/OCR model and endpoint values (which is what configurationID
// hashes) with the API key cleared, and is never given a caller credential.
//
// This makes LLM_BASE_URL / LLM_MODEL / OCR_MODEL part of the deployment
// contract for every service that computes a run signature: if an operator sets
// them, they must be set identically on the execution service and the worker,
// otherwise the two would derive different run signatures for the same run.
func serviceModelConfig(role string) (model.Config, error) {
	cfg := model.FromEnv()
	if role == "" || role == "model" {
		return cfg, nil
	}
	cfg.APIKey = ""
	if role != "worker" && role != "document" {
		return cfg, nil
	}
	gateway := strings.TrimRight(strings.TrimSpace(os.Getenv("MODEL_GATEWAY_URL")), "/")
	if gateway == "" {
		return model.Config{}, errors.New("MODEL_GATEWAY_URL is required for the worker and document services")
	}
	credential := strings.TrimSpace(os.Getenv("WORKER_MODEL_TOKEN"))
	if role == "document" {
		credential = strings.TrimSpace(os.Getenv("DOCUMENT_MODEL_TOKEN"))
	}
	if len(credential) < 32 {
		return model.Config{}, errors.New("a private model caller credential is required")
	}
	cfg.ProviderURL, cfg.BaseURL, cfg.APIKey, cfg.Gateway = cfg.BaseURL, gateway, credential, true
	return cfg, nil
}

func configuredDocumentPreparer() (store.DocumentPreparer, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("DOCUMENT_SERVICE_URL")), "/")
	credential := strings.TrimSpace(os.Getenv("WORKER_DOCUMENT_TOKEN"))
	if endpoint == "" || len(credential) < 32 {
		return nil, errors.New("document service endpoint and caller credential are required")
	}
	return documentclient.New(endpoint, credential, 2*time.Minute)
}

// configuredToolEvidenceReader assembles the worker's read-only tool caller. It
// is the only place the tool credential is read: the endpoint and the
// service-pair credential come from the deployment, never from a request.
func configuredToolEvidenceReader() (store.ToolEvidenceReader, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("TOOL_GATEWAY_URL")), "/")
	credential := strings.TrimSpace(os.Getenv("WORKER_TOOL_TOKEN"))
	if endpoint == "" || len(credential) < 32 {
		return nil, errors.New("tool gateway endpoint and worker caller credential are required")
	}
	client, err := toolclient.New(endpoint, credential, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return toolEvidenceReader{client: client}, nil
}

// toolEvidenceReader adapts the MCP tool client to the store's evidence
// boundary: the pipeline asks for one post-input read under a frozen claim, and
// every field is copied unchanged, so the payload the consumer sees is the tool
// response itself and a failure stays an error.
type toolEvidenceReader struct{ client *toolclient.Client }

func (r toolEvidenceReader) FrozenEvidence(ctx context.Context, in store.ToolEvidenceRequest) (json.RawMessage, error) {
	return r.client.FrozenEvidence(ctx, toolclient.Request{
		Claim: toolclient.Claim{
			TenantID: in.TenantID, OrgID: in.OrgID, JobID: in.JobID, WorkerID: in.WorkerID,
			Generation: in.Generation, AttemptID: in.AttemptID,
		},
		CaseID: in.CaseID, RunID: in.RunID, AttemptID: in.AttemptID,
		CapabilityID: in.CapabilityID, Deadline: in.Deadline,
	})
}

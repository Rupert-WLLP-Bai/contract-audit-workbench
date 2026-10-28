package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"contractaudit/internal/advisory"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"

	"github.com/jackc/pgx/v5"
)

const advisoryJudgmentSchemaSQL = `
CREATE TABLE IF NOT EXISTS advisory_judgments (
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	judgment_id text NOT NULL CHECK (judgment_id ~ '^[0-9a-f]{64}$'),
	feature text NOT NULL,
	question_version text NOT NULL,
	configuration_id text NOT NULL CHECK (configuration_id ~ '^[0-9a-f]{64}$'),
	provider text NOT NULL,
	model_requested text NOT NULL,
	model_observed text NOT NULL,
	frozen_input_sha256 text NOT NULL CHECK (frozen_input_sha256 ~ '^[0-9a-f]{64}$'),
	snapshot_sha256 text NOT NULL CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'),
	request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
	state_sha256 text NOT NULL CHECK (state_sha256 ~ '^[0-9a-f]{64}$'),
	data_classification text NOT NULL,
	status text NOT NULL CHECK (status = 'completed'),
	error_code text NOT NULL DEFAULT '',
	response_json bytea NOT NULL,
	usage_json bytea NOT NULL,
	created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
	PRIMARY KEY (tenant_id, org_id, judgment_id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id)
);
CREATE INDEX IF NOT EXISTS advisory_judgments_run ON advisory_judgments (tenant_id, org_id, run_id);
DROP TRIGGER IF EXISTS advisory_judgments_append_only ON advisory_judgments;
CREATE TRIGGER advisory_judgments_append_only BEFORE UPDATE OR DELETE ON advisory_judgments
	FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

type advisoryFailure struct {
	code string
	err  error
}

func (failure *advisoryFailure) Error() string {
	if failure == nil || failure.err == nil {
		return failure.code
	}
	return failure.code + ": " + failure.err.Error()
}

func (failure *advisoryFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.err
}

func advisoryErrorCode(err error) string {
	var failure *advisoryFailure
	if errors.As(err, &failure) && failure.code != "" {
		return failure.code
	}
	switch {
	case errors.Is(err, advisory.ErrModelMismatch):
		return "model_mismatch"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, advisory.ErrNotApplicable):
		return "not_applicable"
	default:
		return "provider_unavailable"
	}
}

type advisoryJudgment struct {
	TenantID           string
	OrgID              string
	CaseID             string
	RunID              string
	AttemptID          string
	JudgmentID         string
	Feature            string
	QuestionVersion    string
	ConfigurationID    string
	Provider           string
	ModelRequested     string
	ModelObserved      string
	FrozenInputSHA256  string
	SnapshotSHA256     string
	RequestSHA256      string
	StateSHA256        string
	DataClassification string
	ResponseJSON       []byte
	UsageJSON          []byte
}

func (j advisoryJudgment) validate() error {
	for _, value := range []string{j.TenantID, j.OrgID, j.CaseID, j.RunID, j.AttemptID, j.Feature, j.QuestionVersion, j.Provider, j.ModelRequested, j.ModelObserved} {
		if strings.TrimSpace(value) == "" {
			return errors.New("advisory judgment identity is incomplete")
		}
	}
	for _, value := range []string{j.JudgmentID, j.ConfigurationID, j.FrozenInputSHA256, j.SnapshotSHA256, j.RequestSHA256, j.StateSHA256} {
		if len(value) != 64 || !isSHA256Hex(value) {
			return errors.New("advisory judgment digest is invalid")
		}
	}
	if j.DataClassification != agentflow.AdvisoryEgressMinimized || j.ResponseJSON == nil || !json.Valid(j.ResponseJSON) {
		return errors.New("advisory judgment payload is invalid")
	}
	return nil
}

type advisoryResponse struct {
	Model         string             `json:"model"`
	Probabilities map[string]float64 `json:"probabilities"`
	Usage         struct {
		InputTokens  *int64 `json:"input_tokens,omitempty"`
		OutputTokens *int64 `json:"output_tokens,omitempty"`
	} `json:"usage"`
}

func encodeAdvisoryResponse(result advisory.KnowledgeResult) (responseJSON, usageJSON []byte, err error) {
	response := advisoryResponse{Model: result.Model, Probabilities: result.Probabilities}
	response.Usage.InputTokens = result.Usage.InputTokens
	response.Usage.OutputTokens = result.Usage.OutputTokens
	responseJSON, err = json.Marshal(response)
	if err != nil {
		return nil, nil, err
	}
	usageJSON, err = json.Marshal(response.Usage)
	if err != nil {
		return nil, nil, err
	}
	return responseJSON, usageJSON, nil
}

func decodeAdvisoryResponse(responseJSON []byte) (advisory.KnowledgeResult, error) {
	var response advisoryResponse
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return advisory.KnowledgeResult{}, err
	}
	if response.Model == "" || len(response.Probabilities) == 0 {
		return advisory.KnowledgeResult{}, errors.New("advisory judgment payload is empty")
	}
	result := advisory.KnowledgeResult{
		Model: response.Model, Probabilities: response.Probabilities,
		Bands: make(map[string]advisory.Band, len(response.Probabilities)),
		Usage: jev.Usage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens},
	}
	for alias, probability := range response.Probabilities {
		if probability < 0 || probability > 1 {
			return advisory.KnowledgeResult{}, errors.New("advisory judgment probability is invalid")
		}
		band, err := advisory.BandFor(probability, agentflow.AdvisoryThresholdHigh, agentflow.AdvisoryThresholdLow)
		if err != nil {
			return advisory.KnowledgeResult{}, err
		}
		result.Bands[alias] = band
	}
	return result, nil
}

// storeAdvisoryEvaluator owns the per-run cache. A successful judgment is
// persisted before the planner sees its bands; persistence failure is an error
// that forces deterministic order.
type storeAdvisoryEvaluator struct {
	store                  *Store
	claim                  JobClaim
	configuration          agentflow.AdvisoryConfiguration
	frozenInputSHA256      string
	snapshotSHA256         string
	plannerConfigurationID string
	provider               advisory.Provider
}

func newStoreAdvisoryEvaluator(st *Store, claim JobClaim, configuration agentflow.AdvisoryConfiguration,
	frozenInputSHA256, snapshotSHA256, plannerConfigurationID string, provider advisory.Provider) (*storeAdvisoryEvaluator, error) {
	if st == nil || claim.JobID == "" || claim.RunID == "" || claim.AttemptID == "" ||
		!isSHA256Hex(frozenInputSHA256) || !isSHA256Hex(snapshotSHA256) || !isSHA256Hex(plannerConfigurationID) || provider == nil {
		return nil, errors.New("advisory evaluator configuration is incomplete")
	}
	return &storeAdvisoryEvaluator{
		store: st, claim: claim, configuration: configuration,
		frozenInputSHA256: frozenInputSHA256, snapshotSHA256: snapshotSHA256, plannerConfigurationID: plannerConfigurationID,
		provider: provider,
	}, nil
}

func (e *storeAdvisoryEvaluator) Evaluate(ctx context.Context, request advisory.KnowledgeRequest) (advisoryOutcome, error) {
	if e == nil || e.store == nil || e.provider == nil {
		return advisoryOutcome{}, &advisoryFailure{code: "provider_unavailable", err: advisory.ErrUnavailable}
	}
	if identified, ok := e.provider.(advisory.IdentifiedProvider); ok && identified.ProviderIdentity() != e.configuration.Provider {
		return advisoryOutcome{}, &advisoryFailure{code: "config_mismatch", err: errors.New("advisory transport does not match the frozen provider")}
	}
	judgmentID, err := advisoryJudgmentID(e.claim.RunID, request.Feature, e.frozenInputSHA256, e.snapshotSHA256, request.RequestSHA256)
	if err != nil {
		return advisoryOutcome{}, &advisoryFailure{code: "persistence_failed", err: err}
	}
	if existing, ok, loadErr := e.load(ctx, judgmentID); loadErr != nil {
		return advisoryOutcome{}, &advisoryFailure{code: "persistence_failed", err: loadErr}
	} else if ok {
		return advisoryOutcome{Result: existing, JudgmentID: judgmentID, Cached: true}, nil
	}
	result, err := advisory.EvaluateKnowledge(ctx, e.provider, e.configuration, request)
	if err != nil {
		return advisoryOutcome{}, err
	}
	responseJSON, usageJSON, err := encodeAdvisoryResponse(result)
	if err != nil {
		return advisoryOutcome{}, &advisoryFailure{code: "persistence_failed", err: err}
	}
	judgment := advisoryJudgment{
		TenantID: e.claim.TenantID, OrgID: e.claim.OrgID, CaseID: e.claim.CaseID,
		RunID: e.claim.RunID, AttemptID: e.claim.AttemptID, JudgmentID: judgmentID,
		Feature: request.Feature, QuestionVersion: request.QuestionSetVersion,
		ConfigurationID: e.plannerConfigurationID, Provider: e.configuration.Provider,
		ModelRequested: e.configuration.Model, ModelObserved: result.Model,
		FrozenInputSHA256: e.frozenInputSHA256, SnapshotSHA256: e.snapshotSHA256,
		RequestSHA256: request.RequestSHA256, StateSHA256: request.StateSHA256,
		DataClassification: e.configuration.Egress, ResponseJSON: responseJSON, UsageJSON: usageJSON,
	}
	if err := e.save(ctx, judgment); err != nil {
		return advisoryOutcome{}, &advisoryFailure{code: "persistence_failed", err: err}
	}
	// A concurrent worker may have inserted the same judgment first. Re-read so
	// the run uses the persisted answer, not a second non-identical response.
	// If the persisted row cannot be read back, the answer is not auditable and
	// must not be applied.
	existing, ok, loadErr := e.load(ctx, judgmentID)
	if loadErr != nil {
		return advisoryOutcome{}, &advisoryFailure{code: "persistence_failed", err: loadErr}
	}
	if !ok {
		return advisoryOutcome{}, &advisoryFailure{code: "persistence_failed", err: errors.New("advisory judgment is not visible after save")}
	}
	return advisoryOutcome{Result: existing, JudgmentID: judgmentID}, nil
}

func advisoryJudgmentID(runID, feature, frozenInputSHA256, snapshotSHA256, requestSHA256 string) (string, error) {
	for _, value := range []string{runID, feature, frozenInputSHA256, snapshotSHA256, requestSHA256} {
		if strings.TrimSpace(value) == "" {
			return "", errors.New("advisory judgment key is incomplete")
		}
	}
	if !isSHA256Hex(frozenInputSHA256) || !isSHA256Hex(snapshotSHA256) || !isSHA256Hex(requestSHA256) {
		return "", errors.New("advisory judgment key digest is invalid")
	}
	payload := runID + "\x00" + feature + "\x00" + frozenInputSHA256 + "\x00" + snapshotSHA256 + "\x00" + requestSHA256
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:]), nil
}

func (e *storeAdvisoryEvaluator) load(ctx context.Context, judgmentID string) (advisory.KnowledgeResult, bool, error) {
	tx, err := e.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return advisory.KnowledgeResult{}, false, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, e.claim.scope()); err != nil {
		return advisory.KnowledgeResult{}, false, err
	}
	var responseJSON []byte
	err = tx.QueryRow(ctx, `SELECT response_json FROM advisory_judgments
		WHERE tenant_id=$1 AND org_id=$2 AND judgment_id=$3`,
		e.claim.TenantID, e.claim.OrgID, judgmentID).Scan(&responseJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return advisory.KnowledgeResult{}, false, nil
	}
	if err != nil {
		return advisory.KnowledgeResult{}, false, err
	}
	result, err := decodeAdvisoryResponse(responseJSON)
	if err != nil {
		return advisory.KnowledgeResult{}, false, err
	}
	if result.Model != e.configuration.Model {
		return advisory.KnowledgeResult{}, false, errors.New("persisted advisory model differs from the frozen model")
	}
	return result, true, nil
}

func (e *storeAdvisoryEvaluator) save(ctx context.Context, judgment advisoryJudgment) error {
	if err := judgment.validate(); err != nil {
		return err
	}
	if judgment.TenantID != e.claim.TenantID || judgment.OrgID != e.claim.OrgID || judgment.CaseID != e.claim.CaseID ||
		judgment.RunID != e.claim.RunID || judgment.AttemptID != e.claim.AttemptID {
		return errors.New("advisory judgment identity does not match its claim")
	}
	tx, err := e.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, e.claim.scope()); err != nil {
		return err
	}
	if err := e.store.requireLiveClaim(ctx, tx, e.claim, e.claim.RunID, e.claim.AttemptID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO advisory_judgments
		(tenant_id,org_id,case_id,run_id,attempt_id,judgment_id,feature,question_version,configuration_id,provider,
		 model_requested,model_observed,frozen_input_sha256,snapshot_sha256,request_sha256,state_sha256,data_classification,
		 status,error_code,response_json,usage_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'completed','',$18,$19)
		ON CONFLICT (tenant_id,org_id,judgment_id) DO NOTHING`,
		judgment.TenantID, judgment.OrgID, judgment.CaseID, judgment.RunID, judgment.AttemptID, judgment.JudgmentID,
		judgment.Feature, judgment.QuestionVersion, judgment.ConfigurationID, judgment.Provider,
		judgment.ModelRequested, judgment.ModelObserved, judgment.FrozenInputSHA256, judgment.SnapshotSHA256,
		judgment.RequestSHA256, judgment.StateSHA256, judgment.DataClassification, judgment.ResponseJSON, judgment.UsageJSON); err != nil {
		return fmt.Errorf("persist advisory judgment: %w", err)
	}
	return tx.Commit(ctx)
}

// UseAdvisory installs the suggestion-layer transport. It is deliberately
// separate from Models: a Jev outage must not change chat/OCR readiness or the
// review model identity. Call it before the worker accepts traffic.
func (s *Store) UseAdvisory(provider advisory.Provider) {
	s.mu.Lock()
	s.advisoryProvider = provider
	s.mu.Unlock()
}

func (s *Store) advisoryProviderForRun() advisory.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.advisoryProvider
}

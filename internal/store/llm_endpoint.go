package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// LLMEndpointSettings is the single platform-level model endpoint configured
// by the admin surface. The provider credential stays sealed in
// SecretCiphertext and is never part of the HTTP view.
type LLMEndpointSettings struct {
	Name             string
	Protocol         string
	BaseURL          string
	Models           []string
	ReviewModel      string
	OCRModel         string
	SecretCiphertext []byte
	Revision         int64
	UpdatedAt        string
	UpdatedBy        string
}

// HasAPIKey reports whether a sealed credential is stored.
func (s LLMEndpointSettings) HasAPIKey() bool { return len(s.SecretCiphertext) > 0 }

// Active reports whether this row carries every identity the runtime needs.
func (s LLMEndpointSettings) Active() bool {
	return s.Protocol == "openai-compatible" && strings.TrimSpace(s.BaseURL) != "" &&
		strings.TrimSpace(s.ReviewModel) != "" && strings.TrimSpace(s.OCRModel) != "" &&
		containsModelID(s.Models, s.ReviewModel) && containsModelID(s.Models, s.OCRModel)
}

// LLMEndpointInput is one atomic replacement of the endpoint identity. The
// ciphertext is produced by the caller; the store never sees a plaintext key.
type LLMEndpointInput struct {
	Name             string
	Protocol         string
	BaseURL          string
	Models           []string
	ReviewModel      string
	OCRModel         string
	SecretCiphertext []byte
	// PreserveSecret keeps the stored secret when the caller did not submit a
	// replacement. ClearSecret and PreserveSecret are mutually exclusive.
	PreserveSecret bool
	// ExpectedRevision enables optimistic concurrency. Nil means last writer
	// wins, which is only used by internal callers.
	ExpectedRevision *int64
}

const llmEndpointColumns = `name, protocol, base_url, models, review_model, ocr_model, secret_ciphertext, revision, updated_at, updated_by`

// LLMEndpoint reads the active platform endpoint. It is deliberately outside
// tenant row-level security: the endpoint is platform control data, while the
// sealed provider key remains unreadable to roles without LLM_CONFIG_KEY.
func (s *Store) LLMEndpoint(ctx context.Context) (LLMEndpointSettings, bool, error) {
	if ctx == nil {
		return LLMEndpointSettings{}, false, fault.Invalid("缺少读取上下文")
	}
	if !s.HasDatabase() {
		return LLMEndpointSettings{}, false, errors.New("LLM endpoint settings require PostgreSQL")
	}
	var (
		out    LLMEndpointSettings
		models []byte
	)
	err := s.pool.QueryRow(ctx, `SELECT `+llmEndpointColumns+` FROM llm_endpoint_settings WHERE id='active'`).
		Scan(&out.Name, &out.Protocol, &out.BaseURL, &models, &out.ReviewModel, &out.OCRModel, &out.SecretCiphertext, &out.Revision, &out.UpdatedAt, &out.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return LLMEndpointSettings{}, false, nil
	}
	if err != nil {
		return LLMEndpointSettings{}, false, err
	}
	if err := json.Unmarshal(models, &out.Models); err != nil {
		return LLMEndpointSettings{}, false, errors.New("stored LLM model list is invalid")
	}
	return out, true, nil
}

// SaveLLMEndpoint replaces the platform endpoint. Only an admin session may
// call it, and the whole row is rewritten in one transaction.
func (s *Store) SaveLLMEndpoint(ctx context.Context, user accounts.User, input LLMEndpointInput) (LLMEndpointSettings, error) {
	if !s.HasDatabase() {
		return LLMEndpointSettings{}, errors.New("LLM endpoint settings require PostgreSQL")
	}
	if s.ReadOnly() {
		return LLMEndpointSettings{}, fault.New(503, "read_only", "维护模式不允许修改模型端点")
	}
	if user.Role != "admin" {
		return LLMEndpointSettings{}, fault.Forbidden("只有管理员可以配置模型端点")
	}
	if input.PreserveSecret && len(input.SecretCiphertext) > 0 {
		return LLMEndpointSettings{}, fault.Invalid("不能同时保留和替换模型密钥")
	}
	models, err := json.Marshal(input.Models)
	if err != nil {
		return LLMEndpointSettings{}, fault.Invalid("模型列表无法保存")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LLMEndpointSettings{}, err
	}
	defer tx.Rollback(ctx)

	// The single row must be serialized even when it does not exist yet: a
	// row lock locks nothing on the first save, so two concurrent creators
	// could both write revision 1 and the revision-only watcher would miss the
	// second configuration.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('llm_endpoint_settings.active'))`); err != nil {
		return LLMEndpointSettings{}, err
	}
	var previousRevision int64
	var previousSecret []byte
	err = tx.QueryRow(ctx, `SELECT revision, secret_ciphertext FROM llm_endpoint_settings WHERE id='active' FOR UPDATE`).
		Scan(&previousRevision, &previousSecret)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LLMEndpointSettings{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != previousRevision {
		return LLMEndpointSettings{}, fault.Conflict("模型端点已被其他管理员更新，请刷新后重试")
	}
	secret := input.SecretCiphertext
	if input.PreserveSecret {
		secret = previousSecret
	}
	if secret == nil {
		// The column is NOT NULL; an absent credential is stored as empty bytes
		// rather than NULL.
		secret = []byte{}
	}
	revision := previousRevision + 1
	now := stamp()
	_, err = tx.Exec(ctx, `INSERT INTO llm_endpoint_settings
		(id, name, protocol, base_url, models, review_model, ocr_model, secret_ciphertext, revision, updated_at, updated_by)
		VALUES ('active',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, protocol=EXCLUDED.protocol, base_url=EXCLUDED.base_url,
			models=EXCLUDED.models, review_model=EXCLUDED.review_model, ocr_model=EXCLUDED.ocr_model,
			secret_ciphertext=EXCLUDED.secret_ciphertext, revision=EXCLUDED.revision,
			updated_at=EXCLUDED.updated_at, updated_by=EXCLUDED.updated_by`,
		input.Name, input.Protocol, input.BaseURL, models, input.ReviewModel, input.OCRModel,
		secret, revision, now, user.ID)
	if err != nil {
		return LLMEndpointSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LLMEndpointSettings{}, err
	}
	return LLMEndpointSettings{
		Name: input.Name, Protocol: input.Protocol, BaseURL: input.BaseURL, Models: append([]string(nil), input.Models...),
		ReviewModel: input.ReviewModel, OCRModel: input.OCRModel, SecretCiphertext: secret,
		Revision: revision, UpdatedAt: now, UpdatedBy: user.ID,
	}, nil
}

// SaveLLMModelSelection selects the review and image-transcription models from
// the already saved catalog. It never changes the endpoint or credential.
func (s *Store) SaveLLMModelSelection(ctx context.Context, user accounts.User, reviewModel, ocrModel string, expectedRevision *int64) (LLMEndpointSettings, error) {
	if !s.HasDatabase() {
		return LLMEndpointSettings{}, errors.New("LLM endpoint settings require PostgreSQL")
	}
	if s.ReadOnly() {
		return LLMEndpointSettings{}, fault.New(503, "read_only", "维护模式不允许修改模型选择")
	}
	if user.Role != "admin" {
		return LLMEndpointSettings{}, fault.Forbidden("只有管理员可以选择模型")
	}
	reviewModel = strings.TrimSpace(reviewModel)
	ocrModel = strings.TrimSpace(ocrModel)
	if reviewModel == "" || ocrModel == "" {
		return LLMEndpointSettings{}, fault.Invalid("请同时选择审查模型和图片转写模型")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LLMEndpointSettings{}, err
	}
	defer tx.Rollback(ctx)
	var (
		out    LLMEndpointSettings
		models []byte
	)
	err = tx.QueryRow(ctx, `SELECT `+llmEndpointColumns+` FROM llm_endpoint_settings WHERE id='active' FOR UPDATE`).
		Scan(&out.Name, &out.Protocol, &out.BaseURL, &models, &out.ReviewModel, &out.OCRModel, &out.SecretCiphertext, &out.Revision, &out.UpdatedAt, &out.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		// Same precondition as SaveLLMEndpoint: a model selection is an update
		// of an existing endpoint, so a concurrent creator is a conflict, not a
		// missing route.
		if expectedRevision != nil && *expectedRevision > 0 {
			return LLMEndpointSettings{}, fault.Conflict("模型端点已被其他管理员更新，请刷新后重试")
		}
		return LLMEndpointSettings{}, fault.NotFound("尚未配置 LLM 端点")
	}
	if err != nil {
		return LLMEndpointSettings{}, err
	}
	if err := json.Unmarshal(models, &out.Models); err != nil {
		return LLMEndpointSettings{}, errors.New("stored LLM model list is invalid")
	}
	if expectedRevision != nil && *expectedRevision != out.Revision {
		return LLMEndpointSettings{}, fault.Conflict("模型端点已被其他管理员更新，请刷新后重试")
	}
	if !containsModelID(out.Models, reviewModel) || !containsModelID(out.Models, ocrModel) {
		return LLMEndpointSettings{}, fault.Invalid("所选模型不在端点模型列表中，请重新探测")
	}
	out.ReviewModel = reviewModel
	out.OCRModel = ocrModel
	out.Revision++
	out.UpdatedAt = stamp()
	out.UpdatedBy = user.ID
	if _, err := tx.Exec(ctx, `UPDATE llm_endpoint_settings SET review_model=$1, ocr_model=$2, revision=$3, updated_at=$4, updated_by=$5 WHERE id='active'`,
		out.ReviewModel, out.OCRModel, out.Revision, out.UpdatedAt, out.UpdatedBy); err != nil {
		return LLMEndpointSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LLMEndpointSettings{}, err
	}
	return out, nil
}

func containsModelID(models []string, want string) bool {
	for _, model := range models {
		if model == want {
			return true
		}
	}
	return false
}

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

// llmStatusTimeout bounds the stored endpoint health check, matching the
// default model client request timeout.
const llmStatusTimeout = 120 * time.Second

type llmEndpointView struct {
	Configured  bool     `json:"configured"`
	Name        string   `json:"name"`
	Protocol    string   `json:"protocol"`
	BaseURL     string   `json:"base_url"`
	Models      []string `json:"models"`
	ReviewModel string   `json:"review_model"`
	OCRModel    string   `json:"ocr_model"`
	HasAPIKey   bool     `json:"has_api_key"`
	Revision    int64    `json:"revision"`
	UpdatedAt   string   `json:"updated_at"`
	UpdatedBy   string   `json:"updated_by"`
}

func llmEndpointJSON(settings store.LLMEndpointSettings) llmEndpointView {
	models := append([]string(nil), settings.Models...)
	if models == nil {
		models = []string{}
	}
	return llmEndpointView{
		Configured: true, Name: settings.Name, Protocol: settings.Protocol, BaseURL: settings.BaseURL,
		Models: models, ReviewModel: settings.ReviewModel, OCRModel: settings.OCRModel,
		HasAPIKey: settings.HasAPIKey(), Revision: settings.Revision,
		UpdatedAt: settings.UpdatedAt, UpdatedBy: settings.UpdatedBy,
	}
}

func emptyLLMEndpointView() llmEndpointView {
	return llmEndpointView{Protocol: llmendpoint.ProtocolOpenAICompatible, Models: []string{}}
}

// llmEndpoint is the admin-only platform endpoint. It is separate from
// /api/settings so reading a review page never exposes provider configuration.
func (h *handler) llmEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actorForAudience(w, r, "admin"); !ok {
		return
	}
	settings, found, err := h.store.LLMEndpoint(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, emptyLLMEndpointView())
		return
	}
	writeJSON(w, http.StatusOK, llmEndpointJSON(settings))
}

func (h *handler) probeLLMEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actorForAudience(w, r, "admin"); !ok {
		return
	}
	if h.store.ReadOnly() {
		writeErr(w, fault.New(503, "read_only", "维护模式不允许探测模型端点"))
		return
	}
	var body struct {
		Protocol    string `json:"protocol"`
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
		ClearAPIKey bool   `json:"clear_api_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if body.ClearAPIKey && key != "" {
		writeErr(w, fault.Invalid("不能同时清除和替换模型密钥"))
		return
	}
	if key == "" && !body.ClearAPIKey {
		settings, found, err := h.store.LLMEndpoint(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		if found && len(settings.SecretCiphertext) > 0 && sameProbeEndpoint(settings, body.Protocol, body.BaseURL) {
			key, err = llmendpoint.Open(settings.SecretCiphertext)
			if err != nil {
				writeErr(w, fault.New(503, "llm_credential_unavailable", "服务端无法读取已保存的模型密钥"))
				return
			}
		}
	}
	result, err := llmendpoint.Probe(r.Context(), body.Protocol, body.BaseURL, key)
	if err != nil {
		if errors.Is(err, llmendpoint.ErrEndpointForbidden) {
			writeErr(w, fault.New(502, "llm_probe_failed", "模型端点探测失败：目标地址被部署出站策略拒绝"))
			return
		}
		writeErr(w, fault.New(502, "llm_probe_failed", "模型端点探测失败："+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": result.Models, "latency_ms": result.LatencyMS})
}

func (h *handler) saveLLMEndpoint(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	if h.store.ReadOnly() {
		writeErr(w, fault.New(503, "read_only", "维护模式不允许修改模型端点"))
		return
	}
	var body struct {
		Name             string   `json:"name"`
		Protocol         string   `json:"protocol"`
		BaseURL          string   `json:"base_url"`
		Models           []string `json:"models"`
		ReviewModel      string   `json:"review_model"`
		OCRModel         string   `json:"ocr_model"`
		APIKey           string   `json:"api_key"`
		ClearAPIKey      bool     `json:"clear_api_key"`
		ExpectedRevision *int64   `json:"expected_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.ClearAPIKey && strings.TrimSpace(body.APIKey) != "" {
		writeErr(w, fault.Invalid("不能同时清除和替换模型密钥"))
		return
	}
	previous, found, err := h.store.LLMEndpoint(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	reviewModel := strings.TrimSpace(body.ReviewModel)
	ocrModel := strings.TrimSpace(body.OCRModel)
	if found {
		// Keep the prior role selections only when they still exist in the
		// submitted catalog. Replacing the endpoint must not fail just because
		// a previous model id disappeared; the runtime simply stays inactive
		// until the admin selects models again.
		if reviewModel == "" && modelInList(body.Models, previous.ReviewModel) {
			reviewModel = previous.ReviewModel
		}
		if ocrModel == "" && modelInList(body.Models, previous.OCRModel) {
			ocrModel = previous.OCRModel
		}
	}
	config, err := llmendpoint.Normalize(body.Name, body.Protocol, body.BaseURL, body.Models, reviewModel, ocrModel)
	if err != nil {
		writeErr(w, fault.Invalid(err.Error()))
		return
	}
	if found && previous.HasAPIKey() && !body.ClearAPIKey && strings.TrimSpace(body.APIKey) == "" &&
		(previous.BaseURL != config.BaseURL || previous.Protocol != config.Protocol) {
		writeErr(w, fault.Invalid("更换模型端点时必须重新输入或清除已保存的密钥"))
		return
	}
	var secret []byte
	preserve := false
	switch {
	case strings.TrimSpace(body.APIKey) != "":
		secret, err = llmendpoint.Seal(body.APIKey)
		if err != nil {
			if errors.Is(err, llmendpoint.ErrEncryptionKey) {
				writeErr(w, fault.New(503, "llm_encryption_unavailable", "服务端未配置 LLM_CONFIG_KEY，无法保存模型密钥"))
				return
			}
			writeErr(w, err)
			return
		}
	case body.ClearAPIKey:
		secret = nil
	default:
		preserve = true
	}
	settings, err := h.store.SaveLLMEndpoint(r.Context(), user, store.LLMEndpointInput{
		Name: config.Name, Protocol: config.Protocol, BaseURL: config.BaseURL, Models: config.Models,
		ReviewModel: config.ReviewModel, OCRModel: config.OCRModel, SecretCiphertext: secret,
		PreserveSecret: preserve, ExpectedRevision: body.ExpectedRevision,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, llmEndpointJSON(settings))
}

func modelInList(models []string, want string) bool {
	if want == "" {
		return false
	}
	for _, model := range models {
		if strings.TrimSpace(model) == want {
			return true
		}
	}
	return false
}

// sameProbeEndpoint limits stored-credential reuse to the endpoint that owns
// it. Probing a different address must not forward the saved provider key.
func sameProbeEndpoint(settings store.LLMEndpointSettings, protocol, baseURL string) bool {
	if strings.TrimSpace(protocol) == "" {
		protocol = llmendpoint.ProtocolOpenAICompatible
	}
	normalized, err := llmendpoint.NormalizeBaseURL(baseURL)
	if err != nil {
		return false
	}
	return settings.Protocol == protocol && settings.BaseURL == normalized
}

func (h *handler) saveLLMModels(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	if h.store.ReadOnly() {
		writeErr(w, fault.New(503, "read_only", "维护模式不允许修改模型选择"))
		return
	}
	var body struct {
		ReviewModel      string `json:"review_model"`
		OCRModel         string `json:"ocr_model"`
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	settings, err := h.store.SaveLLMModelSelection(r.Context(), user, body.ReviewModel, body.OCRModel, body.ExpectedRevision)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, llmEndpointJSON(settings))
}

// llmEndpointStatus reports the currently active stored endpoint for the
// settings summary. It never falls back to the environment: an incomplete or
// absent row returns handled=false so the caller keeps the deployment view.
func (h *handler) llmEndpointStatus(ctx context.Context) (status, label string, handled bool) {
	settings, found, err := h.store.LLMEndpoint(ctx)
	if err != nil || !found || !settings.Active() {
		return "", "", false
	}
	key, err := llmendpoint.Open(settings.SecretCiphertext)
	if err != nil && len(settings.SecretCiphertext) > 0 {
		return "unknown", "已保存模型端点，但本进程无法读取密钥", true
	}
	// The stored endpoint is operator control data: its health check uses the
	// guarded outbound client, exactly like the probe and provider calls.
	client := model.NewWithHTTPClient(model.Config{
		BaseURL: settings.BaseURL, ChatModel: settings.ReviewModel, OCRModel: settings.OCRModel, APIKey: key,
	}, llmendpoint.GuardedClient(llmStatusTimeout))
	status, label = client.Reachable(ctx)
	return status, label, true
}

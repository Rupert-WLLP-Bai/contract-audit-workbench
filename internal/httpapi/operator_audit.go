package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	"contractaudit/internal/fulfillment"
	"contractaudit/internal/reviewimpact"
	"contractaudit/internal/store"

	"github.com/jackc/pgx/v5"
)

// OperatorConnectorInfo is the non-secret catalog shown to an authorized
// operator. It contains identifiers and allow-listed object ids only; it never
// contains a URL, token or raw credential.
type OperatorConnectorInfo struct {
	ConnectorID    string   `json:"connector_id"`
	SourceSystem   string   `json:"source_system"`
	AllowedObjects []string `json:"allowed_objects"`
}

func (h *handler) operatorAuditReady(w http.ResponseWriter) bool {
	if !h.operatorAuditEnabled {
		writeErr(w, fault.New(503, "operator_audit_disabled", "跨系统核验未启用"))
		return false
	}
	if h.capabilityPacks == nil {
		writeErr(w, fault.New(503, "operator_audit_unavailable", "能力包目录未配置"))
		return false
	}
	return true
}

func (h *handler) listCapabilityPacks(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	items := h.capabilityPacks.List()
	if items == nil {
		items = []capabilitypack.Manifest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) createObjectBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	var body struct {
		ContractObject struct {
			ObjectType string  `json:"object_type"`
			ObjectID   string  `json:"object_id"`
			Label      *string `json:"label"`
		} `json:"contract_object"`
		ExternalObject struct {
			ConnectorID      string `json:"connector_id"`
			ExternalObjectID string `json:"external_object_id"`
		} `json:"external_object"`
		EffectiveFrom  *string `json:"effective_from"`
		EffectiveUntil *string `json:"effective_until"`
		Confirmed      bool    `json:"confirmed"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if _, registered := h.externalConnectors[body.ExternalObject.ConnectorID]; !registered {
		writeErr(w, fault.Invalid("外部连接器未登记"))
		return
	}
	from, err := parseOperatorTime(body.EffectiveFrom)
	if err != nil {
		writeErr(w, fault.Invalid("映射生效时间不合法"))
		return
	}
	until, err := parseOperatorTime(body.EffectiveUntil)
	if err != nil {
		writeErr(w, fault.Invalid("映射失效时间不合法"))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	binding, err := h.store.CreateOperatorObjectBinding(r.Context(), user, r.PathValue("caseID"), store.OperatorObjectBindingInput{
		ContractObject: store.OperatorContractObject{
			ObjectType: body.ContractObject.ObjectType, ObjectID: body.ContractObject.ObjectID, Label: body.ContractObject.Label,
		},
		ExternalObject: store.OperatorExternalObject{
			ConnectorID: body.ExternalObject.ConnectorID, ExternalObjectID: body.ExternalObject.ExternalObjectID,
		},
		EffectiveFrom: from, EffectiveUntil: until, Confirmed: body.Confirmed,
	}, key)
	if err != nil {
		writeOperatorErr(w, err, "没有找到对应案件")
		return
	}
	writeJSON(w, http.StatusCreated, binding)
}

func (h *handler) collectExternalSnapshot(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	var body struct {
		ConnectorID          string `json:"connector_id"`
		ExternalObjectID     string `json:"external_object_id"`
		ObjectBindingVersion string `json:"object_binding_version"`
		BusinessAt           string `json:"business_at"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	connector, registered := h.externalConnectors[body.ConnectorID]
	if !registered {
		writeErr(w, fault.Invalid("外部连接器未登记"))
		return
	}
	businessAt, err := parseOperatorRequiredTime(body.BusinessAt)
	if err != nil {
		writeErr(w, fault.Invalid("业务时间不合法"))
		return
	}
	set, err := h.store.CollectOperatorExternalSnapshot(r.Context(), user, r.PathValue("caseID"),
		body.ObjectBindingVersion, businessAt, body.ConnectorID, body.ExternalObjectID, connector,
		strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		writeOperatorErr(w, err, "没有找到对象映射")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"job_id": set.SnapshotSetID, "snapshot_set_id": set.SnapshotSetID, "state": set.Status,
		"connector_id": set.ConnectorID, "external_object_id": set.ExternalObjectID,
		"accepted_at": set.CollectedAt.Format(time.RFC3339), "snapshot_set": set,
	})
}

func (h *handler) listExternalSnapshots(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	sets, err := h.store.ListOperatorExternalSnapshots(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeOperatorErr(w, err, "没有找到对应案件")
		return
	}
	type observationItem struct {
		auditext.Observation
		SnapshotSetID string `json:"snapshot_set_id"`
	}
	items := make([]observationItem, 0)
	for _, set := range sets {
		for _, observation := range set.Observations {
			items = append(items, observationItem{Observation: observation, SnapshotSetID: set.SnapshotSetID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "sets": sets})
}

func (h *handler) createFulfillmentEvaluation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	var body struct {
		PackID               string `json:"pack_id"`
		PackVersion          string `json:"pack_version"`
		SourceReviewID       string `json:"source_review_id"`
		SourceAttemptID      string `json:"source_attempt_id"`
		ContractVersionID    string `json:"contract_version_id"`
		ObjectBindingVersion string `json:"object_binding_version"`
		SnapshotSetID        string `json:"snapshot_set_id"`
		BusinessAt           string `json:"business_at"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	pack, found := h.capabilityPacks.Get(body.PackID, body.PackVersion)
	if !found {
		writeErr(w, fault.Invalid("能力包或版本未登记"))
		return
	}
	businessAt, err := parseOperatorRequiredTime(body.BusinessAt)
	if err != nil {
		writeErr(w, fault.Invalid("业务时间不合法"))
		return
	}
	evaluation, err := h.store.CreateOperatorFulfillmentEvaluation(r.Context(), user, r.PathValue("caseID"),
		store.OperatorEvaluationInput{
			PackID: body.PackID, PackVersion: body.PackVersion, SourceReviewID: body.SourceReviewID,
			SourceAttemptID: body.SourceAttemptID, ContractVersionID: body.ContractVersionID,
			ObjectBindingVersion: body.ObjectBindingVersion, SnapshotSetID: body.SnapshotSetID,
			BusinessAt: businessAt,
		}, pack, fulfillment.New(), strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		writeOperatorErr(w, err, "没有找到来源审查、对象映射或快照集")
		return
	}
	writeJSON(w, http.StatusCreated, operatorEvaluationJSON(evaluation))
}

func (h *handler) getFulfillmentEvaluation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	evaluation, err := h.store.GetOperatorFulfillmentEvaluation(r.Context(), user, r.PathValue("caseID"), r.PathValue("evaluationID"))
	if err != nil {
		writeOperatorErr(w, err, "没有找到履约核验记录")
		return
	}
	writeJSON(w, http.StatusOK, operatorEvaluationJSON(evaluation))
}

func (h *handler) getImpactPlan(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.operatorAuditReady(w) {
		return
	}
	mode := auditext.ImpactMode(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = auditext.ImpactExplain
	}
	if !mode.Valid() {
		writeErr(w, fault.Invalid("影响计划模式必须为 explain 或 shadow"))
		return
	}
	plan, err := h.store.GetOperatorImpactPlan(r.Context(), user, r.PathValue("caseID"),
		r.PathValue("evaluationID"), mode, reviewimpact.New())
	if err != nil {
		writeOperatorErr(w, err, "没有找到履约核验记录")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func operatorEvaluationJSON(evaluation store.OperatorEvaluation) map[string]any {
	facts := evaluation.Input.Facts
	if facts == nil {
		facts = []auditext.Fact{}
	}
	return map[string]any{
		"evaluation_id":          evaluation.EvaluationID,
		"pack":                   map[string]string{"id": evaluation.PackID, "version": evaluation.PackVersion},
		"source_review_id":       evaluation.SourceReviewID,
		"source_attempt_id":      evaluation.SourceAttemptID,
		"contract_version":       evaluation.ContractVersionID,
		"rule_pack_version":      evaluation.RulePackVersion,
		"object_binding_version": evaluation.ObjectBindingVersion,
		"snapshot_set_id":        evaluation.SnapshotSetID,
		"business_at":            evaluation.BusinessAt.Format(time.RFC3339),
		"machine_status":         evaluation.MachineStatus,
		"checks":                 evaluation.Checks,
		"contract_facts":         facts,
		"data_class":             auditext.DataClassSynthetic,
		"created_at":             evaluation.CreatedAt.Format(time.RFC3339),
	}
}

func parseOperatorTime(raw *string) (time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
	if err != nil {
		return time.Time{}, err
	}
	return value.UTC(), nil
}

func parseOperatorRequiredTime(raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, err
	}
	return value.UTC(), nil
}

func writeOperatorErr(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, fault.NotFound(notFound))
		return
	}
	writeErr(w, err)
}

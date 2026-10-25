package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"contractaudit/internal/store"
)

// Governance routes are Core-owned admin surface. Every handler resolves the
// trusted admin session first: tenant and actor are server-bound, never taken
// from the request body, and the platform admin role grants no contract-body
// authority.

// listRulePacks returns the tenant's persisted rule pack versions.
func (h *handler) listRulePacks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	list, err := h.store.ListRulePackVersions(user)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackListJSON(list))
}

// createRulePack stores one new draft version from a strict manifest.
func (h *handler) createRulePack(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.CreateRulePack(user, body.Manifest)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rulePackDetailJSON(detail))
}

// getRulePackVersion returns one exact persisted version and its manifest.
func (h *handler) getRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	detail, err := h.store.GetRulePackVersion(user, r.PathValue("packID"), r.PathValue("version"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// editRulePackVersion replaces draft content and resets validation/approval.
func (h *handler) editRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64           `json:"expected_revision"`
		Manifest         json.RawMessage `json:"manifest"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.EditRulePackVersion(user, store.GovernanceInput{
		PackID: r.PathValue("packID"), Version: r.PathValue("version"),
		ExpectedRevision: body.ExpectedRevision, Manifest: body.Manifest,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// validateRulePackVersion records the deterministic verifier record.
func (h *handler) validateRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.ValidateRulePackVersion(user, store.GovernanceInput{
		PackID: r.PathValue("packID"), Version: r.PathValue("version"), ExpectedRevision: body.ExpectedRevision,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// approveRulePackVersion binds the trusted admin identity to the content hash.
func (h *handler) approveRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision    int64  `json:"expected_revision"`
		ExpectedContentHash string `json:"expected_content_hash"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.ApproveRulePackVersion(user, store.GovernanceInput{
		PackID: r.PathValue("packID"), Version: r.PathValue("version"),
		ExpectedRevision: body.ExpectedRevision, ExpectedContentHash: body.ExpectedContentHash,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// publishRulePackVersion makes bytes immutable and moves the active pointer.
func (h *handler) publishRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision        int64 `json:"expected_revision"`
		ExpectedPointerRevision int64 `json:"expected_pointer_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.PublishRulePackVersion(user, store.GovernanceInput{
		PackID: r.PathValue("packID"), Version: r.PathValue("version"),
		ExpectedRevision: body.ExpectedRevision, ExpectedPointerRevision: body.ExpectedPointerRevision,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// disableRulePackVersion clears the active pointer, keeping published bytes.
func (h *handler) disableRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision        int64 `json:"expected_revision"`
		ExpectedPointerRevision int64 `json:"expected_pointer_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.DisableRulePackVersion(user, store.GovernanceInput{
		PackID: r.PathValue("packID"), Version: r.PathValue("version"),
		ExpectedRevision: body.ExpectedRevision, ExpectedPointerRevision: body.ExpectedPointerRevision,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// rollbackRulePackVersion points at an exact already published version.
func (h *handler) rollbackRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		Version                 string `json:"version"`
		ExpectedPointerRevision int64  `json:"expected_pointer_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	detail, err := h.store.RollbackRulePackVersion(user, store.GovernanceInput{
		PackID: r.PathValue("packID"), Version: strings.TrimSpace(body.Version),
		ExpectedPointerRevision: body.ExpectedPointerRevision,
		IdempotencyKey:          strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackDetailJSON(detail))
}

// rulePackHistory returns the append-only governance history of one pack.
func (h *handler) rulePackHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	history, err := h.store.RulePackHistory(user, r.PathValue("packID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackHistoryJSON(history))
}

// testRulePackVersion runs one bounded synthetic sample execution.
func (h *handler) testRulePackVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body store.RulePackTestInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	body.PackID = r.PathValue("packID")
	body.Version = r.PathValue("version")
	result, err := h.store.TestRulePackVersion(user, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revision":     result.Revision,
		"content_hash": result.ContentHash,
		"report":       result.Report,
	})
}

func rulePackListJSON(list store.RulePackList) map[string]any {
	items := make([]any, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, rulePackSummaryJSON(item))
	}
	return map[string]any{"items": items}
}

func rulePackDetailJSON(detail store.RulePackDetail) map[string]any {
	return map[string]any{"pack": rulePackSummaryJSON(detail.Pack), "manifest": detail.Manifest}
}

func rulePackSummaryJSON(item store.RulePackSummary) map[string]any {
	return map[string]any{
		"id": item.ID, "version": item.Version, "tenant_id": item.TenantID,
		"status": item.Status, "revision": item.Revision, "content_hash": item.ContentHash,
		"snapshot_sha256": item.SnapshotSHA256, "effective_from": item.EffectiveFrom,
		"effective_until": item.EffectiveUntil, "authority_sources": item.AuthoritySources,
		"is_active": item.IsActive, "active_pointer_revision": item.ActivePointerRevision,
	}
}

func rulePackHistoryJSON(history store.RulePackHistory) map[string]any {
	items := make([]any, 0, len(history.Items))
	for _, item := range history.Items {
		items = append(items, map[string]any{
			"sequence": item.Sequence, "pack_id": item.PackID, "version": item.Version,
			"action": item.Action, "actor_id": item.ActorID, "content_hash": item.ContentHash,
			"pointer_revision": item.PointerRevision, "created_at": item.CreatedAt,
			"previous_version": item.PreviousVersion, "previous_active": item.PreviousActive,
			"resulting_version": item.ResultingVersion, "resulting_active": item.ResultingActive,
		})
	}
	return map[string]any{"items": items}
}

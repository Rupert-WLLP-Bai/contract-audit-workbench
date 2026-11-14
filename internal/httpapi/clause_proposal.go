// File metadata (manifest mirror):
// Purpose: Serves authenticated clause proposal creation, listing, derived text and explicit decisions with request-scoped Document session forwarding.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/store"
	"net/http"
)

func registerClauseProposalRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux, routeBinding{"GET /api/cases/{caseID}/clause-proposals", h.listClauseProposals}, routeBinding{"POST /api/cases/{caseID}/clause-proposals", h.createClauseProposal}, routeBinding{"POST /api/cases/{caseID}/clause-proposals/{proposalID}/derived-text", h.deriveClauseText}, routeBinding{"POST /api/cases/{caseID}/clause-proposals/{proposalID}/decisions", h.decideClauseProposal})
}
func (h *handler) listClauseProposals(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListClauseProposals(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *handler) createClauseProposal(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input contract.ClauseProposalCreate
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	key, err := requiredIdempotencyKey(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	input.CaseID = r.PathValue("caseID")
	input.IdempotencyKey = key
	item, err := h.store.CreateClauseProposal(r.Context(), user, input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h *handler) deriveClauseText(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64   `json:"expected_version"`
		EditedText      *string `json:"edited_text,omitempty"`
	}
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	text, err := h.store.DerivedClauseText(r.Context(), user, r.PathValue("caseID"), r.PathValue("proposalID"), input.ExpectedVersion, input.EditedText)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "format": "derived_plain_text"})
}
func (h *handler) decideClauseProposal(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input contract.ClauseProposalDecision
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	key, err := requiredIdempotencyKey(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	input.IdempotencyKey = key
	ctx := store.WithClauseDocumentSession(r.Context(), audienceCookieValue(r, "client"))
	item, err := h.store.DecideClauseProposal(ctx, user, r.PathValue("caseID"), r.PathValue("proposalID"), input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

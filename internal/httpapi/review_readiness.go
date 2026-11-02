// File metadata (manifest mirror):
// Purpose: Returns a case-scoped readiness projection for the selected review inputs, including missing or incompatible dependencies; it reports prerequisites and does not start or certify a review.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"
)

func (h *handler) reviewReadiness(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	readiness, err := h.store.GetReviewReadiness(r.Context(), user, r.PathValue("caseID"), r.URL.Query().Get("business_at"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, readiness)
}

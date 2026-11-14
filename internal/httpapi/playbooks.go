// File metadata (manifest mirror):
// Purpose: Serves authenticated case and contract playbook catalogs through current resource and frozen-material authorization.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"
)

func (h *handler) casePlaybooks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	out, err := h.store.ListCasePlaybooks(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) contractPlaybooks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	out, err := h.store.ListContractPlaybooks(r.Context(), user, r.PathValue("contractID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

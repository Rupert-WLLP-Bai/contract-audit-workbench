// File metadata (manifest mirror):
// Purpose: Builds read-only system health snapshots for service and admin routes, separating configured expectations from observed instances and requiring admin audience for topology details.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import "net/http"

func (h *handler) healthSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, h.systemHealth.Snapshot(r.Context()))
}

func (h *handler) adminSystemHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actorForAudience(w, r, "admin"); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, h.systemHealth.Snapshot(r.Context()))
}

// File metadata (manifest mirror):
// Purpose: contractReferenceRoute is the service-ownership rule for the reference check. It is additive on top of contractDraftRoute so the ledger patch can extend the latter independently. Related `httpapi` operations include checkContractReferences, contractReferenceRoute.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-01
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
)

func (h *handler) checkContractReferences(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.CheckContractReferences(req.Context(), user, req.PathValue("contractID"), req.URL.Query().Get("case_id"))
	})
}

// contractReferenceRoute is the service-ownership rule for the reference
// check. It is additive on top of contractDraftRoute so the ledger patch can
// extend the latter independently.
func contractReferenceRoute(role string, methods routeMethodSet, parts []string) bool {
	return role == "core" && len(parts) == 3 && parts[2] == "reference-checks" && methods.read
}

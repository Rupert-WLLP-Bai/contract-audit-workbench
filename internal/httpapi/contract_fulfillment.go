// File metadata (manifest mirror):
// Purpose: 注册并处理 `registerContractFulfillmentRoutes`, `listContractDeliveries`, `createContractDelivery`, `listContractAcceptances` 等 Core HTTP 路由或会话命令，完成请求解析、服务端授权与响应映射。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-03
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

func registerContractFulfillmentRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/lifecycle-overview", h.contractLifecycleOverview},
		routeBinding{"GET /api/contracts/{contractID}/deliveries", h.listContractDeliveries},
		routeBinding{"POST /api/contracts/{contractID}/deliveries", h.createContractDelivery},
		routeBinding{"GET /api/contracts/{contractID}/acceptances", h.listContractAcceptances},
		routeBinding{"POST /api/contracts/{contractID}/acceptances", h.createContractAcceptance},
		routeBinding{"POST /api/contracts/{contractID}/acceptances/{acceptanceID}/rectifications", h.recordContractRectification},
	)
}

func (h *handler) contractLifecycleOverview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ContractLifecycleOverview(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) listContractDeliveries(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractDeliveries(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) createContractDelivery(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractDeliveryInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID, body.IdempotencyKey = req.PathValue("contractID"), key
		return h.store.CreateContractDelivery(req.Context(), user, body)
	})
}

func (h *handler) listContractAcceptances(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractAcceptances(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) createContractAcceptance(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractAcceptanceInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID, body.IdempotencyKey = req.PathValue("contractID"), key
		return h.store.CreateContractAcceptance(req.Context(), user, body)
	})
}

// contractRectificationRequest excludes server-owned route and receipt identity.
type contractRectificationRequest struct {
	ExpectedRowVersion int64               `json:"expected_row_version"`
	ContractRevisionID string              `json:"contract_revision_id"`
	DefectID           string              `json:"defect_id"`
	Description        string              `json:"description"`
	Evidence           []store.MaterialRef `json:"evidence"`
}

func (h *handler) recordContractRectification(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body contractRectificationRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		input := store.ContractRectificationInput{
			ContractID: req.PathValue("contractID"), AcceptanceID: req.PathValue("acceptanceID"), IdempotencyKey: key,
			ExpectedRowVersion: body.ExpectedRowVersion, ContractRevisionID: body.ContractRevisionID,
			DefectID: body.DefectID, Description: body.Description, Evidence: body.Evidence,
		}
		return h.store.RecordContractRectification(req.Context(), user, input)
	})
}

package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

func registerContractFulfillmentRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/deliveries", h.listContractDeliveries},
		routeBinding{"POST /api/contracts/{contractID}/deliveries", h.createContractDelivery},
		routeBinding{"GET /api/contracts/{contractID}/acceptances", h.listContractAcceptances},
		routeBinding{"POST /api/contracts/{contractID}/acceptances", h.createContractAcceptance},
		routeBinding{"POST /api/contracts/{contractID}/acceptances/{acceptanceID}/rectifications", h.recordContractRectification},
	)
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

func (h *handler) recordContractRectification(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractRectificationInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID, body.AcceptanceID, body.IdempotencyKey = req.PathValue("contractID"), req.PathValue("acceptanceID"), key
		return h.store.RecordContractRectification(req.Context(), user, body)
	})
}

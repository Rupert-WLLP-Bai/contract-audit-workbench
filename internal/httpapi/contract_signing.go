package httpapi

import (
	"net/http"
	"os"
	"strconv"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func registerContractSigningRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"POST /api/contracts/{contractID}/signing-checks", h.checkContractSigning},
		routeBinding{"POST /api/contracts/{contractID}/registrations", h.registerContractSigning},
		routeBinding{"GET /api/contracts/{contractID}/registrations", h.listContractSigningRegistrations},
		routeBinding{"POST /api/contracts/{contractID}/supplements", h.createContractSupplement},
		routeBinding{"GET /api/contracts/{contractID}/supplements", h.listContractSupplements},
	)
}

func contractSigningEnabled() bool {
	value, exists := os.LookupEnv("CONTRACT_SIGNING_ENABLED")
	if !exists {
		return true
	}
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func signingDisabledError() error {
	return fault.New(http.StatusServiceUnavailable, "contract_signing_disabled", "合同签署登记功能当前已停用")
}

func (h *handler) checkContractSigning(w http.ResponseWriter, r *http.Request) {
	if !contractSigningEnabled() {
		writeErr(w, signingDisabledError())
		return
	}
	serveClientJSONCommand(h, w, r, http.StatusOK, func(user accounts.User, req *http.Request, body store.ContractSigningCheckInput) (any, error) {
		body.ContractID = req.PathValue("contractID")
		return h.store.CheckContractSigning(req.Context(), user, body)
	})
}

func (h *handler) registerContractSigning(w http.ResponseWriter, r *http.Request) {
	if !contractSigningEnabled() {
		writeErr(w, signingDisabledError())
		return
	}
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractSigningRegistrationInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID = req.PathValue("contractID")
		body.IdempotencyKey = key
		return h.store.RegisterContractSigning(req.Context(), user, body)
	})
}

func (h *handler) listContractSigningRegistrations(w http.ResponseWriter, r *http.Request) {
	if !contractSigningEnabled() {
		writeErr(w, signingDisabledError())
		return
	}
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractSigningRegistrations(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) createContractSupplement(w http.ResponseWriter, r *http.Request) {
	if !contractSigningEnabled() {
		writeErr(w, signingDisabledError())
		return
	}
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.CreateContractSupplementInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ParentContractID = req.PathValue("contractID")
		body.IdempotencyKey = key
		return h.store.CreateContractSupplement(req.Context(), user, body)
	})
}

func (h *handler) listContractSupplements(w http.ResponseWriter, r *http.Request) {
	if !contractSigningEnabled() {
		writeErr(w, signingDisabledError())
		return
	}
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractSupplements(req.Context(), user, req.PathValue("contractID"))
	})
}

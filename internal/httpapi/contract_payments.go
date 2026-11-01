package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

// PaymentReceiptSourceCredential is provisioned from deployment secrets and
// binds one machine key to one source and tenant. It is never accepted in a
// request body and is not the ordinary gateway/service credential.
type PaymentReceiptSourceCredential struct {
	TenantID string `json:"tenant_id"`
	SourceID string `json:"source_id"`
	KeyID    string `json:"key_id"`
	Secret   string `json:"secret"`
}

func registerContractPaymentRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/payments", h.listContractPayments},
		routeBinding{"POST /api/contracts/{contractID}/payment-eligibility-checks", h.checkContractPaymentEligibility},
	)
}

func (h *handler) listContractPayments(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractPayments(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) checkContractPaymentEligibility(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractPaymentEligibilityInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID = req.PathValue("contractID")
		body.IdempotencyKey = key
		return h.store.CheckContractPaymentEligibility(req.Context(), user, body)
	})
}

func paymentReceiptHandler(st *store.Store, configured []PaymentReceiptSourceCredential) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/internal/v1/payment-receipts" {
			http.NotFound(w, r)
			return
		}
		if len(r.Header.Values("X-Payment-Source-Key-ID")) != 1 || len(r.Header.Values("X-Payment-Source-Timestamp")) != 1 || len(r.Header.Values("X-Payment-Source-Signature")) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "payment source authentication required", "code": "payment_source_unauthenticated"})
			return
		}
		stamp := r.Header.Get("X-Payment-Source-Timestamp")
		seconds, err := strconv.ParseInt(stamp, 10, 64)
		nowSeconds := time.Now().UTC().Unix()
		if err != nil || seconds < nowSeconds-300 || seconds > nowSeconds+300 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "payment source authentication required", "code": "payment_source_unauthenticated"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid receipt payload", "code": "invalid_request"})
			return
		}
		provided, err := hex.DecodeString(r.Header.Get("X-Payment-Source-Signature"))
		if err != nil || len(provided) != sha256.Size {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "payment source authentication required", "code": "payment_source_unauthenticated"})
			return
		}
		keyID := r.Header.Get("X-Payment-Source-Key-ID")
		var selected *PaymentReceiptSourceCredential
		for i := range configured {
			if configured[i].KeyID != keyID {
				continue
			}
			mac := hmac.New(sha256.New, []byte(configured[i].Secret))
			_, _ = mac.Write([]byte(configured[i].TenantID + "\n" + configured[i].SourceID + "\n" + configured[i].KeyID + "\n" + stamp + "\n"))
			_, _ = mac.Write(body)
			expected := mac.Sum(nil)
			if subtle.ConstantTimeCompare(expected, provided) == 1 {
				selected = &configured[i]
			}
		}
		if selected == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "payment source authentication required", "code": "payment_source_unauthenticated"})
			return
		}
		var input store.ContractPaymentReceiptInput
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid receipt payload", "code": "invalid_request"})
			return
		}
		var extra any
		if err = decoder.Decode(&extra); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid receipt payload", "code": "invalid_request"})
			return
		}
		if strings.TrimSpace(input.ContractID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "contract_id is required", "code": "invalid_request"})
			return
		}
		result, replayed, err := st.RecordTrustedContractPaymentReceipt(r.Context(), store.TrustedPaymentSource{TenantID: selected.TenantID, SourceID: selected.SourceID, KeyID: selected.KeyID}, input)
		if err != nil {
			writeErr(w, err)
			return
		}
		if replayed {
			w.Header().Set("Idempotent-Replay", "true")
		}
		writeJSON(w, http.StatusCreated, result)
	})
}

// paymentReceiptSignature is shared with tests so the fixture exercises the
// same machine-authenticated route as production. Keys remain test-only.
func paymentReceiptSignature(secret, tenant, source, keyID, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(tenant + "\n" + source + "\n" + keyID + "\n" + timestamp + "\n"))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

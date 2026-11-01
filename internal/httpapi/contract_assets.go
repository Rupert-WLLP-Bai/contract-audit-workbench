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

// AssetReceiptSourceCredential is action-specific and must never be reused as
// a payment-source or ordinary service credential.
type AssetReceiptSourceCredential struct {
	TenantID string `json:"tenant_id"`
	SourceID string `json:"source_id"`
	KeyID    string `json:"key_id"`
	Secret   string `json:"secret"`
}

func registerContractAssetRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/assets", h.listContractAssets},
		routeBinding{"POST /api/contracts/{contractID}/asset-handovers", h.createContractAssetHandover},
	)
}

func (h *handler) listContractAssets(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractAssets(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) createContractAssetHandover(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractAssetHandoverInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID = req.PathValue("contractID")
		body.IdempotencyKey = key
		return h.store.CreateContractAssetHandover(req.Context(), user, body)
	})
}

func assetReceiptHandler(st *store.Store, configured []AssetReceiptSourceCredential) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/internal/v1/asset-receipts" {
			http.NotFound(w, r)
			return
		}
		if len(configured) == 0 || len(r.Header.Values("X-Asset-Source-Key-ID")) != 1 || len(r.Header.Values("X-Asset-Source-Timestamp")) != 1 || len(r.Header.Values("X-Asset-Source-Signature")) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "asset source authentication required", "code": "asset_source_unauthenticated"})
			return
		}
		stamp := r.Header.Get("X-Asset-Source-Timestamp")
		seconds, err := strconv.ParseInt(stamp, 10, 64)
		now := time.Now().UTC().Unix()
		if err != nil || seconds < now-300 || seconds > now+300 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "asset source authentication required", "code": "asset_source_unauthenticated"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid asset receipt payload", "code": "invalid_request"})
			return
		}
		provided, err := hex.DecodeString(r.Header.Get("X-Asset-Source-Signature"))
		if err != nil || len(provided) != sha256.Size {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "asset source authentication required", "code": "asset_source_unauthenticated"})
			return
		}
		keyID := r.Header.Get("X-Asset-Source-Key-ID")
		var selected *AssetReceiptSourceCredential
		for i := range configured {
			if configured[i].KeyID != keyID {
				continue
			}
			expected := assetReceiptSignature(configured[i].Secret, configured[i].TenantID, configured[i].SourceID, configured[i].KeyID, stamp, body)
			providedMAC, decodeErr := hex.DecodeString(expected)
			if decodeErr == nil && subtle.ConstantTimeCompare(providedMAC, provided) == 1 {
				selected = &configured[i]
			}
		}
		if selected == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "asset source authentication required", "code": "asset_source_unauthenticated"})
			return
		}
		var input store.ContractAssetReceiptInput
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid asset receipt payload", "code": "invalid_request"})
			return
		}
		var extra any
		if err = decoder.Decode(&extra); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid asset receipt payload", "code": "invalid_request"})
			return
		}
		if strings.TrimSpace(input.ContractID) == "" || strings.TrimSpace(input.HandoverID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "contract_id and handover_id are required", "code": "invalid_request"})
			return
		}
		result, replayed, err := st.RecordTrustedContractAssetReceipt(r.Context(), store.ContractAssetSource{TenantID: selected.TenantID, SourceID: selected.SourceID, KeyID: selected.KeyID}, input)
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

// The action and path are part of the signed data. Test callers and configured
// sources cannot use the payment action's signature for an asset receipt.
func assetReceiptSignature(secret, tenant, source, keyID, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("asset_receipt\n/internal/v1/asset-receipts\n" + tenant + "\n" + source + "\n" + keyID + "\n" + timestamp + "\n"))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func validateAssetReceiptSourceCredentials(configured []AssetReceiptSourceCredential) error {
	keys, secrets, mappings := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, source := range configured {
		if !visibleSourceValue(source.TenantID, 200) || !visibleSourceValue(source.SourceID, 200) || !visibleSourceValue(source.KeyID, 200) || len(source.Secret) < 32 {
			return errInvalidAssetSourceConfiguration
		}
		mapping := source.TenantID + "\x00" + source.SourceID + "\x00" + source.KeyID
		if keys[source.KeyID] || secrets[source.Secret] || mappings[mapping] {
			return errInvalidAssetSourceConfiguration
		}
		keys[source.KeyID], secrets[source.Secret], mappings[mapping] = true, true, true
	}
	return nil
}

func visibleSourceValue(value string, max int) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > max {
		return false
	}
	for _, char := range value {
		if char < 32 || char == 127 {
			return false
		}
	}
	return true
}

var errInvalidAssetSourceConfiguration = &assetSourceConfigError{}

type assetSourceConfigError struct{}

func (*assetSourceConfigError) Error() string {
	return "invalid action-specific asset receipt source configuration"
}

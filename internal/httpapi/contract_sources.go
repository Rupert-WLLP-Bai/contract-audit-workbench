package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/clmsource"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

const contractSourceObservationPath = "/internal/v1/contract-source-observations"

// ContractSourceCredential is server-owned source authentication and exact
// case scope. Credentials and scopes are never accepted from an event body.
type ContractSourceCredential struct {
	TenantID        string                          `json:"tenant_id"`
	SourceID        string                          `json:"source_id"`
	KeyID           string                          `json:"key_id"`
	Secret          string                          `json:"secret"`
	Kind            clmsource.SourceKind            `json:"kind"`
	ReadOnly        bool                            `json:"read_only"`
	VersionOrdering clmsource.VersionOrdering       `json:"version_ordering"`
	AllowedCases    []store.ContractSourceCaseScope `json:"allowed_cases"`
}

func registerContractSourceRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/source-capability", h.getContractSourceCapability},
	)
}

func (h *handler) getContractSourceCapability(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		detail, err := h.store.LoadContractDetail(req.Context(), user, req.PathValue("contractID"))
		if err != nil {
			return nil, err
		}
		if detail.Summary.AuthorityMode != string(clmsource.AuthorityExternal) || detail.SourceID == "" {
			return nil, fault.NotFound("没有找到对应记录")
		}
		result := store.ContractSourceCapability{
			ContractID: detail.Summary.ContractID, SourceID: detail.SourceID,
			SourceKind: "unconfigured", AuthorityMode: string(clmsource.AuthorityExternal),
			ReadOnly: true, CanReadContract: false, VersionOrdering: "unknown",
			ConnectionStatus: "not_probed",
		}
		for _, configured := range h.contractSources {
			if configured.TenantID != accounts.Tenant(user) || configured.SourceID != detail.SourceID {
				continue
			}
			result.SourceKind = string(configured.Kind)
			result.CanReadContract = true
			result.VersionOrdering = string(configured.VersionOrdering)
			break
		}
		return result, nil
	})
}

type contractSourceObservationRequest struct {
	EventID            string             `json:"event_id"`
	ExternalContractID string             `json:"external_contract_id"`
	SourceVersion      string             `json:"source_version"`
	SourceSequence     *uint64            `json:"source_sequence"`
	SourceState        string             `json:"source_state"`
	OccurredAt         time.Time          `json:"occurred_at"`
	CaseOrgID          string             `json:"case_org_id"`
	CaseID             string             `json:"case_id"`
	Snapshot           store.DraftContent `json:"snapshot"`
}

func contractSourceObservationHandler(st *store.Store, configured []ContractSourceCredential) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != contractSourceObservationPath || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		keyIDs, stamps, signatures := r.Header.Values("X-CLM-Source-Key-ID"), r.Header.Values("X-CLM-Source-Timestamp"), r.Header.Values("X-CLM-Source-Signature")
		if len(configured) == 0 || len(keyIDs) != 1 || len(stamps) != 1 || len(signatures) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "contract source authentication required", "code": "contract_source_unauthenticated"})
			return
		}
		stamp := stamps[0]
		seconds, err := strconv.ParseInt(stamp, 10, 64)
		now := time.Now().UTC().Unix()
		if err != nil || seconds < now-300 || seconds > now+300 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "contract source authentication required", "code": "contract_source_unauthenticated"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid contract source payload", "code": "invalid_request"})
			return
		}
		provided, err := hex.DecodeString(signatures[0])
		if err != nil || len(provided) != sha256.Size {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "contract source authentication required", "code": "contract_source_unauthenticated"})
			return
		}
		var selected *ContractSourceCredential
		for i := range configured {
			if configured[i].KeyID != keyIDs[0] {
				continue
			}
			expected := contractSourceSignature(configured[i].Secret, configured[i].TenantID, configured[i].SourceID, configured[i].KeyID, stamp, body)
			expectedMAC, decodeErr := hex.DecodeString(expected)
			if decodeErr == nil && subtle.ConstantTimeCompare(expectedMAC, provided) == 1 {
				selected = &configured[i]
			}
		}
		if selected == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "contract source authentication required", "code": "contract_source_unauthenticated"})
			return
		}
		var input contractSourceObservationRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid contract source payload", "code": "invalid_request"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid contract source payload", "code": "invalid_request"})
			return
		}
		if input.SourceSequence != nil && *input.SourceSequence > math.MaxInt64 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_sequence is out of range", "code": "invalid_request"})
			return
		}
		result, err := st.RecordExternalContractObservation(r.Context(), store.ContractSourceAuthority{
			TenantID: selected.TenantID, SourceID: selected.SourceID, KeyID: selected.KeyID,
			Kind: string(selected.Kind), Ordering: selected.VersionOrdering,
			AllowedCases: append([]store.ContractSourceCaseScope(nil), selected.AllowedCases...),
		}, store.ContractSourceObservationInput{
			EventID: input.EventID, ExternalContractID: input.ExternalContractID,
			SourceVersion: input.SourceVersion, SourceSequence: input.SourceSequence,
			SourceState: input.SourceState, OccurredAt: input.OccurredAt,
			CaseOrgID: input.CaseOrgID, CaseID: input.CaseID, Snapshot: input.Snapshot,
			RawBody: body,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replay", "true")
		}
		writeJSON(w, http.StatusCreated, result)
	})
}

func contractSourceSignature(secret, tenantID, sourceID, keyID, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("clm_contract_source_observation/v1\nPOST\n" + contractSourceObservationPath + "\n" + tenantID + "\n" + sourceID + "\n" + keyID + "\n" + timestamp + "\n"))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func validateContractSourceCredentials(configured []ContractSourceCredential) error {
	seenKeyID, seenSecret := map[string]bool{}, map[string]bool{}
	type sourceConfig struct {
		kind     clmsource.SourceKind
		ordering clmsource.VersionOrdering
		scopes   string
	}
	bySource := map[string]sourceConfig{}
	for _, source := range configured {
		if !validPaymentSourceID(source.TenantID, 200) || !validPaymentSourceID(source.SourceID, 200) ||
			!validPaymentSourceID(source.KeyID, 200) || len(source.Secret) < 32 || strings.TrimSpace(source.Secret) != source.Secret || !source.ReadOnly ||
			source.VersionOrdering == "" || len(source.AllowedCases) == 0 {
			return errors.New("contract source configuration is invalid")
		}
		authProfile := clmsource.AuthHMACSHA256
		if source.Kind == clmsource.SourceMock {
			authProfile = clmsource.AuthFixture
		}
		if err := (clmsource.Capability{SourceID: source.SourceID, Kind: source.Kind,
			Authority: clmsource.AuthorityExternal, ReadOnly: true, Authentication: authProfile,
			CanReadContract: true, VersionOrdering: source.VersionOrdering}).Validate(); err != nil {
			return errors.New("contract source capability is invalid")
		}
		if seenKeyID[source.KeyID] || seenSecret[source.Secret] {
			return errors.New("contract source credential mapping is ambiguous")
		}
		seenKeyID[source.KeyID], seenSecret[source.Secret] = true, true
		scopes := append([]store.ContractSourceCaseScope(nil), source.AllowedCases...)
		sort.Slice(scopes, func(i, j int) bool {
			if scopes[i].OrgID != scopes[j].OrgID {
				return scopes[i].OrgID < scopes[j].OrgID
			}
			return scopes[i].CaseID < scopes[j].CaseID
		})
		for i, scope := range scopes {
			if !validPaymentSourceID(scope.OrgID, 200) || !validPaymentSourceID(scope.CaseID, 200) || (i > 0 && scopes[i-1] == scope) {
				return errors.New("contract source case scope is invalid")
			}
		}
		scopeJSON, _ := json.Marshal(scopes)
		mapKey := source.TenantID + "\x00" + source.SourceID
		config := sourceConfig{kind: source.Kind, ordering: source.VersionOrdering, scopes: string(scopeJSON)}
		if prior, ok := bySource[mapKey]; ok && prior != config {
			return errors.New("rotated contract source keys must preserve one source scope and capability")
		}
		bySource[mapKey] = config
	}
	return nil
}

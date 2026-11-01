package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/clmsource"
	"contractaudit/internal/store"
)

func TestContractSourceCredentialsAreActionSpecific(t *testing.T) {
	source := ContractSourceCredential{
		TenantID: "tenant-a", SourceID: "erp-a", KeyID: "source-key-a", Secret: strings.Repeat("s", 32),
		Kind: clmsource.SourceReal, ReadOnly: true, VersionOrdering: clmsource.VersionOpaque,
		AllowedCases: []store.ContractSourceCaseScope{{OrgID: "org-a", CaseID: "case-a"}},
	}
	opt := Options{ContractSourceCredentials: []ContractSourceCredential{source}}
	if err := validateContractSources("core", strings.Repeat("g", 32), &opt); err != nil {
		t.Fatalf("valid source credentials rejected: %v", err)
	}
	opt.PaymentReceiptSources = []PaymentReceiptSourceCredential{{TenantID: "tenant-a", SourceID: "pay-a", KeyID: source.KeyID, Secret: strings.Repeat("p", 32)}}
	if err := validateContractSources("core", strings.Repeat("g", 32), &opt); err == nil {
		t.Fatal("source credential reused another action's key ID")
	}
	opt.PaymentReceiptSources[0].KeyID = "payment-key-a"
	opt.PaymentReceiptSources[0].Secret = source.Secret
	if err := validateContractSources("core", strings.Repeat("g", 32), &opt); err == nil {
		t.Fatal("source secret reused another action's credential")
	}
}

func TestContractSourceObservationAuthAndBodyStrictness(t *testing.T) {
	configured := []ContractSourceCredential{{
		TenantID: "tenant-a", SourceID: "erp-a", KeyID: "source-key-a", Secret: strings.Repeat("s", 32),
		Kind: clmsource.SourceReal, ReadOnly: true, VersionOrdering: clmsource.VersionOpaque,
		AllowedCases: []store.ContractSourceCaseScope{{OrgID: "org-a", CaseID: "case-a"}},
	}}
	handler := contractSourceObservationHandler(nil, configured)
	stamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	body := []byte(`{"authenticated":true}`)
	request := func(signature string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, contractSourceObservationPath, strings.NewReader(string(body)))
		req.Header.Set("X-CLM-Source-Key-ID", configured[0].KeyID)
		req.Header.Set("X-CLM-Source-Timestamp", stamp)
		req.Header.Set("X-CLM-Source-Signature", signature)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	if got := request(strings.Repeat("0", 64)).Code; got != http.StatusUnauthorized {
		t.Fatalf("invalid HMAC status %d", got)
	}
	valid := contractSourceSignature(configured[0].Secret, configured[0].TenantID, configured[0].SourceID, configured[0].KeyID, stamp, body)
	if got := request(valid).Code; got != http.StatusBadRequest {
		t.Fatalf("body auth claim should fail strict decoding before store access, got %d", got)
	}
}

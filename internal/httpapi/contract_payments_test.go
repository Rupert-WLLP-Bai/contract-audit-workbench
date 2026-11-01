package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/store"
)

func TestPaymentReceiptSourceBindingAndHMACBoundary(t *testing.T) {
	key := strings.Repeat("k", 40)
	keyID := "finance-key-2026-01"
	opt := &Options{PaymentReceiptSources: []PaymentReceiptSourceCredential{{TenantID: "tenant-a", SourceID: "finance-a", KeyID: keyID, Secret: key}}}
	if err := validatePaymentReceiptSources("core", strings.Repeat("g", 40), opt); err != nil {
		t.Fatal(err)
	}
	disabled := buildServiceHandler(&store.Store{}, Options{Durable: true}, "core", strings.Repeat("g", 40))
	disabledRequest := httptest.NewRequest("POST", "/internal/v1/payment-receipts", strings.NewReader(`{}`))
	disabledResult := httptest.NewRecorder()
	disabled.ServeHTTP(disabledResult, disabledRequest)
	if disabledResult.Code != 404 {
		t.Fatalf("unconfigured payment source endpoint must not be mounted: status=%d", disabledResult.Code)
	}
	rotating := &Options{PaymentReceiptSources: []PaymentReceiptSourceCredential{{TenantID: "tenant-a", SourceID: "finance-a", KeyID: "finance-key-old", Secret: strings.Repeat("o", 40)}, {TenantID: "tenant-a", SourceID: "finance-a", KeyID: "finance-key-new", Secret: strings.Repeat("n", 40)}}}
	if err := validatePaymentReceiptSources("core", strings.Repeat("g", 40), rotating); err != nil {
		t.Fatalf("old/new source key rotation mapping: %v", err)
	}
	if err := validatePaymentReceiptSources("document", strings.Repeat("g", 40), opt); err == nil {
		t.Fatal("non-Core service must not accept payment sources")
	}
	if err := validatePaymentReceiptSources("core", key, opt); err == nil {
		t.Fatal("gateway credential must not double as a finance source key")
	}
	ambiguous := &Options{PaymentReceiptSources: []PaymentReceiptSourceCredential{{TenantID: "tenant-a", SourceID: "finance-a", KeyID: keyID, Secret: key}, {TenantID: "tenant-b", SourceID: "finance-b", KeyID: "finance-key-2026-02", Secret: key}}}
	if err := validatePaymentReceiptSources("core", strings.Repeat("g", 40), ambiguous); err == nil {
		t.Fatal("reused source credential must be rejected")
	}

	endpoint := paymentReceiptHandler(nil, opt.PaymentReceiptSources)
	stamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	invalidIdentity := `{"contract_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c10","contract_revision_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c11","payment_sequence":1,"event_id":"event-1","event_type":"payment","amount":"1.00","currency":"CNY","occurred_at":"2026-11-02T00:00:00Z","tenant_id":"tenant-b","source_id":"finance-b"}`
	unauthorized := httptest.NewRequest("POST", "/internal/v1/payment-receipts", strings.NewReader(invalidIdentity))
	unauthorized.Header.Set("X-Payment-Source-Key-ID", keyID)
	unauthorized.Header.Set("X-Payment-Source-Timestamp", stamp)
	unauthorized.Header.Set("X-Payment-Source-Signature", paymentReceiptSignature(strings.Repeat("x", 40), "tenant-a", "finance-a", keyID, stamp, []byte(invalidIdentity)))
	unauthorizedResult := httptest.NewRecorder()
	endpoint.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != 401 {
		t.Fatalf("wrong credential status=%d body=%s", unauthorizedResult.Code, unauthorizedResult.Body.String())
	}
	unknownKeyID := httptest.NewRequest("POST", "/internal/v1/payment-receipts", strings.NewReader(invalidIdentity))
	unknownKeyID.Header.Set("X-Payment-Source-Key-ID", "not-configured")
	unknownKeyID.Header.Set("X-Payment-Source-Timestamp", stamp)
	unknownKeyID.Header.Set("X-Payment-Source-Signature", strings.Repeat("0", 64))
	unknownResult := httptest.NewRecorder()
	endpoint.ServeHTTP(unknownResult, unknownKeyID)
	if unknownResult.Code != 401 {
		t.Fatalf("unknown source key ID status=%d body=%s", unknownResult.Code, unknownResult.Body.String())
	}

	badFields := `{"contract_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c10","contract_revision_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c11","payment_sequence":1,"event_id":"event-1","event_type":"payment","amount":"1.00","currency":"CNY","occurred_at":"2026-11-02T00:00:00Z","tenant_id":"tenant-a"}`
	request := httptest.NewRequest("POST", "/internal/v1/payment-receipts", strings.NewReader(badFields))
	request.Header.Set("X-Payment-Source-Key-ID", keyID)
	request.Header.Set("X-Payment-Source-Timestamp", stamp)
	request.Header.Set("X-Payment-Source-Signature", paymentReceiptSignature(key, "tenant-a", "finance-a", keyID, stamp, []byte(badFields)))
	result := httptest.NewRecorder()
	endpoint.ServeHTTP(result, request)
	if result.Code != 400 {
		t.Fatalf("client tenant assertion status=%d body=%s", result.Code, result.Body.String())
	}
	publicMux := http.NewServeMux()
	registerContractPaymentRoutes(publicMux, &handler{})
	userReceipt := httptest.NewRequest("POST", "/api/contracts/c6052c81-8b05-49ce-89e9-f6e9c6403c10/payment-receipts", strings.NewReader(`{}`))
	userResult := httptest.NewRecorder()
	publicMux.ServeHTTP(userResult, userReceipt)
	if userResult.Code != 404 {
		t.Fatalf("ordinary user receipt route must not exist, status=%d", userResult.Code)
	}
}

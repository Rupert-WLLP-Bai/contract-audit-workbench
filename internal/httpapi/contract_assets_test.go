package httpapi

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAssetReceiptHMACUsesIndependentActionNamespace(t *testing.T) {
	secret := strings.Repeat("a", 32)
	tenant, source, keyID := "tenant-a", "erp-fixture", "asset-key-1"
	stamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	body := []byte(`{"contract_id":"00000000-0000-4000-8000-000000000001","handover_id":"00000000-0000-4000-8000-000000000002","event_id":"erp-event-1","event_type":"receipt","asset_code":"A-1","quantity":"1.000000","unit":"台","occurred_at":"2026-11-01T00:00:00Z"}`)
	assetMAC := assetReceiptSignature(secret, tenant, source, keyID, stamp, body)
	if paymentReceiptSignature(secret, tenant, source, keyID, stamp, body) == assetMAC {
		t.Fatal("asset and payment actions unexpectedly share a signature namespace")
	}
	configured := []AssetReceiptSourceCredential{{TenantID: tenant, SourceID: source, KeyID: keyID, Secret: secret}}
	request := httptest.NewRequest("POST", "/internal/v1/asset-receipts", strings.NewReader(`{"tenant_id":"attacker"}`))
	request.Header.Set("X-Asset-Source-Key-ID", keyID)
	request.Header.Set("X-Asset-Source-Timestamp", stamp)
	request.Header.Set("X-Asset-Source-Signature", assetReceiptSignature(secret, tenant, source, keyID, stamp, []byte(`{"tenant_id":"attacker"}`)))
	response := httptest.NewRecorder()
	assetReceiptHandler(nil, configured).ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatalf("tenant assertion body should be rejected after authentication, got %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest("POST", "/internal/v1/asset-receipts", strings.NewReader(string(body)))
	request.Header.Set("X-Asset-Source-Key-ID", keyID)
	request.Header.Set("X-Asset-Source-Timestamp", stamp)
	request.Header.Set("X-Asset-Source-Signature", paymentReceiptSignature(secret, tenant, source, keyID, stamp, body))
	response = httptest.NewRecorder()
	assetReceiptHandler(nil, configured).ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("payment action signature must not authorize asset receipt, got %d", response.Code)
	}

	request = httptest.NewRequest("POST", "/internal/v1/asset-receipts", strings.NewReader(string(body)))
	request.Header.Set("X-Asset-Source-Key-ID", keyID)
	request.Header.Set("X-Asset-Source-Timestamp", stamp)
	request.Header.Set("X-Asset-Source-Signature", assetMAC)
	response = httptest.NewRecorder()
	assetReceiptHandler(nil, configured).ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatalf("valid asset signature should reach the store boundary (nil store returns unavailable), got %d", response.Code)
	}
}

func TestAssetReceiptSourceConfigurationIsStrictAndDisabledByDefault(t *testing.T) {
	if err := validateAssetReceiptSourceCredentials(nil); err != nil {
		t.Fatalf("empty source list should leave endpoint disabled: %v", err)
	}
	base := AssetReceiptSourceCredential{TenantID: "tenant-a", SourceID: "erp-fixture", KeyID: "asset-key-a", Secret: strings.Repeat("s", 32)}
	if err := validateAssetReceiptSourceCredentials([]AssetReceiptSourceCredential{base}); err != nil {
		t.Fatal(err)
	}
	rotated := base
	rotated.KeyID = "asset-key-b"
	rotated.Secret = strings.Repeat("r", 32)
	if err := validateAssetReceiptSourceCredentials([]AssetReceiptSourceCredential{base, rotated}); err != nil {
		t.Fatalf("separate action-scoped rotation key should be allowed: %v", err)
	}
	cases := []struct {
		name string
		list []AssetReceiptSourceCredential
	}{
		{"short secret", []AssetReceiptSourceCredential{{TenantID: "t", SourceID: "s", KeyID: "k", Secret: "short"}}},
		{"duplicate key id", []AssetReceiptSourceCredential{base, rotatedWith(base, "asset-key-a", strings.Repeat("r", 32))}},
		{"duplicate secret", []AssetReceiptSourceCredential{base, rotatedWith(base, "asset-key-b", base.Secret)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAssetReceiptSourceCredentials(tc.list); err == nil {
				t.Fatal("invalid source configuration accepted")
			}
		})
	}
	response := httptest.NewRecorder()
	assetReceiptHandler(nil, nil).ServeHTTP(response, httptest.NewRequest("POST", "/internal/v1/asset-receipts", strings.NewReader(`{}`)))
	if response.Code != 401 {
		t.Fatalf("unconfigured source endpoint must fail closed, got %d", response.Code)
	}
}

func TestAssetReceiptSourcesAreCoreOnlyAndDoNotReusePaymentKeys(t *testing.T) {
	asset := AssetReceiptSourceCredential{TenantID: "tenant-a", SourceID: "erp", KeyID: "asset-key", Secret: strings.Repeat("a", 40)}
	if err := validateAssetReceiptSources("document", strings.Repeat("g", 40), &Options{AssetReceiptSources: []AssetReceiptSourceCredential{asset}}); err == nil {
		t.Fatal("non-Core service accepted an asset source")
	}
	if err := validateAssetReceiptSources("core", strings.Repeat("g", 40), &Options{AssetReceiptSources: []AssetReceiptSourceCredential{asset}}); err != nil {
		t.Fatalf("valid asset source rejected: %v", err)
	}
	for name, payment := range map[string]PaymentReceiptSourceCredential{
		"same key id": {TenantID: "tenant-a", SourceID: "pay", KeyID: asset.KeyID, Secret: strings.Repeat("p", 40)},
		"same secret": {TenantID: "tenant-a", SourceID: "pay", KeyID: "payment-key", Secret: asset.Secret},
	} {
		t.Run(name, func(t *testing.T) {
			opt := &Options{AssetReceiptSources: []AssetReceiptSourceCredential{asset}, PaymentReceiptSources: []PaymentReceiptSourceCredential{payment}}
			if err := validateAssetReceiptSources("core", strings.Repeat("g", 40), opt); err == nil {
				t.Fatal("cross-action credential reuse accepted")
			}
		})
	}
}

func rotatedWith(base AssetReceiptSourceCredential, key, secret string) AssetReceiptSourceCredential {
	base.KeyID, base.Secret = key, secret
	return base
}

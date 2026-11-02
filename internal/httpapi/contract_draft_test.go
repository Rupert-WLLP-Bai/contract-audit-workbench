// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi_test` package behavior through TestContractDraftHTTPRequiresSessionAndKey, TestContractDraftHTTPCreateReadReplaceList, TestContractDraftHTTPVisibilityBoundary, TestContractDraftHTTPRestrictedFieldRoundTrip, TestContractDraftHTTPMissingDataKey; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"contractaudit/internal/clmenvelope"
)

// Each entry belongs to the explicit synthetic tenant and purpose used here.
func clmDraftAPI(t *testing.T) *testAPI {
	t.Helper()
	const purpose = "contract.supplier.restricted"
	entries := []clmenvelope.LocalKeyEntry{
		{TenantID: "local-demo", Purpose: purpose, Kind: clmenvelope.WrapKey, Ref: clmenvelope.KeyRef{ID: "http-wrap", Version: 1}, MaterialBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x6b}, 32)), Active: true},
		{TenantID: "local-demo", Purpose: purpose, Kind: clmenvelope.FingerprintKey, Ref: clmenvelope.KeyRef{ID: "http-fingerprint", Version: 1}, MaterialBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x6c}, 32)), Active: true},
		{TenantID: "local-demo", Purpose: purpose, Kind: clmenvelope.LegacyDataKey, Ref: clmenvelope.KeyRef{ID: "http-legacy", Version: 1}, MaterialBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x6d}, 32))},
	}
	raw, err := json.Marshal(clmenvelope.LocalKeyConfig{Version: 1, Keys: entries})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLM_ENVELOPE_KEYS", string(raw))
	t.Setenv("CLM_LEGACY_DATA_KEY_REF", "http-legacy:1")
	return newAPI(t)
}

func clmJSONRequest(t *testing.T, api *testAPI, method, path string, headers map[string]string, payload any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, api.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", api.base)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := api.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return res.StatusCode, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json %d %s", res.StatusCode, raw)
	}
	return res.StatusCode, out
}

func clmCreatePayload(name string) map[string]any {
	return map[string]any{
		"expected_row_version": 0,
		"authority_mode":       "native",
		"draft": map[string]any{
			"contract": map[string]any{"name": name, "currency": "CNY", "amount": "50000.00"},
		},
	}
}

func clmHeaders(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

func TestContractDraftHTTPRequiresSessionAndKey(t *testing.T) {
	api := clmDraftAPI(t)

	status, body := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("unauthenticated-01"), clmCreatePayload("未登录"))
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: %d %v", status, body)
	}

	api.login(t, "demo-clerk", "clerk-demo")
	status, body = clmJSONRequest(t, api, http.MethodPost, "/api/contracts", nil, clmCreatePayload("缺少幂等键"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("missing Idempotency-Key: %d %v", status, body)
	}

	unknown := clmCreatePayload("未知属性")
	unknown["tenant_id"] = "local-demo"
	status, body = clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("unknown-prop-0001"), unknown)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown top-level property: %d %v", status, body)
	}

	nested := clmCreatePayload("嵌套未知属性")
	draft := nested["draft"].(map[string]any)
	draft["extra_block"] = map[string]any{"oops": true}
	status, body = clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("unknown-nested-001"), nested)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown nested property: %d %v", status, body)
	}
}

func TestContractDraftHTTPCreateReadReplaceList(t *testing.T) {
	api := clmDraftAPI(t)
	api.login(t, "demo-clerk", "clerk-demo")

	status, created := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("http-create-00001"), clmCreatePayload(" HTTP 草稿"))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, created)
	}
	contractID, _ := created["summary"].(map[string]any)["contract_id"].(string)
	if contractID == "" {
		t.Fatalf("create response %+v", created)
	}

	status, detail := clmJSONRequest(t, api, http.MethodGet, "/api/contracts/"+contractID, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("detail: %d %v", status, detail)
	}
	summary := detail["summary"].(map[string]any)
	if summary["row_version"].(float64) != 1 || summary["contract_state"] != "draft" {
		t.Fatalf("detail summary %v", summary)
	}

	status, drafts := clmJSONRequest(t, api, http.MethodGet, "/api/contracts/drafts", nil, nil)
	if status != http.StatusOK || len(drafts["items"].([]any)) != 1 {
		t.Fatalf("own drafts: %d %v", status, drafts)
	}

	status, _ = clmJSONRequest(t, api, http.MethodGet, "/api/contracts/not-a-uuid", nil, nil)
	if status != http.StatusNotFound {
		t.Fatalf("non-UUID contract id: %d", status)
	}

	stale := map[string]any{
		"expected_row_version": 42,
		"replacement_draft":    clmCreatePayload("过期版本")["draft"],
	}
	status, body := clmJSONRequest(t, api, http.MethodPut, "/api/contracts/"+contractID+"/draft", clmHeaders("http-stale-00001"), stale)
	if status != http.StatusConflict {
		t.Fatalf("stale replace: %d %v", status, body)
	}

	replacement := map[string]any{
		"expected_row_version": 1,
		"replacement_draft": map[string]any{
			"contract": map[string]any{"name": "替换后的草稿"},
		},
	}
	status, replaced := clmJSONRequest(t, api, http.MethodPut, "/api/contracts/"+contractID+"/draft", clmHeaders("http-replace-0001"), replacement)
	if status != http.StatusOK {
		t.Fatalf("replace: %d %v", status, replaced)
	}
	if replaced["summary"].(map[string]any)["row_version"].(float64) != 2 {
		t.Fatalf("replaced summary %v", replaced["summary"])
	}

	// Full replacement drops fields the caller no longer sends.
	status, detail = clmJSONRequest(t, api, http.MethodGet, "/api/contracts/"+contractID, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("detail after replace: %d", status)
	}
	draft := detail["draft"].(map[string]any)
	if draft["contract"].(map[string]any)["amount"] != nil {
		t.Fatalf("full replacement must drop omitted fields: %v", draft)
	}

	status, replayed := clmJSONRequest(t, api, http.MethodPut, "/api/contracts/"+contractID+"/draft", clmHeaders("http-replace-0001"), replacement)
	if status != http.StatusOK || replayed["summary"].(map[string]any)["row_version"].(float64) != 2 {
		t.Fatalf("same-key replay: %d %v", status, replayed)
	}
}

func TestContractDraftHTTPVisibilityBoundary(t *testing.T) {
	api := clmDraftAPI(t)
	api.login(t, "demo-clerk", "clerk-demo")
	status, created := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("boundary-http-0001"), clmCreatePayload("经办人的合同"))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, created)
	}
	contractID := created["summary"].(map[string]any)["contract_id"].(string)

	// The same jar now carries the admin session; an identity that did not
	// create the contract must not even learn that it exists.
	api.login(t, "demo-admin", "admin-demo")
	status, body := api.do(t, http.MethodGet, "/api/contracts/"+contractID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("non-creator read: %d %v", status, body)
	}
	status, body = clmJSONRequest(t, api, http.MethodPut, "/api/contracts/"+contractID+"/draft", clmHeaders("boundary-put-000001"), map[string]any{
		"expected_row_version": 1,
		"replacement_draft":    clmCreatePayload("冒名替换")["draft"],
	})
	if status != http.StatusNotFound {
		t.Fatalf("non-creator replace: %d %v", status, body)
	}
}

func TestContractDraftHTTPRestrictedFieldRoundTrip(t *testing.T) {
	api := clmDraftAPI(t)
	api.login(t, "demo-clerk", "clerk-demo")

	payload := clmCreatePayload("含账户草稿")
	payload["draft"].(map[string]any)["supplier"] = map[string]any{
		"name": "示例供应商", "bank_account": "6222020200001234567",
	}
	status, created := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", clmHeaders("restricted-http-001"), payload)
	if status != http.StatusCreated {
		t.Fatalf("restricted create: %d %v", status, created)
	}
	contractID := created["summary"].(map[string]any)["contract_id"].(string)
	draft := created["draft"].(map[string]any)
	if draft["supplier"].(map[string]any)["bank_account"] != "6222020200001234567" {
		t.Fatalf("creator readback %v", draft["supplier"])
	}

	status, detail := clmJSONRequest(t, api, http.MethodGet, "/api/contracts/"+contractID, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("detail: %d", status)
	}
	if detail["draft"].(map[string]any)["supplier"].(map[string]any)["bank_account"] != "6222020200001234567" {
		t.Fatalf("recovered draft lost the restricted field: %v", detail["draft"])
	}
}

// TestContractDraftHTTPMissingDataKey pins the explicit failure mode: with no
// CLM_ENVELOPE_KEYS configured, restricted-field writes are refused instead of
// storing plaintext.
func TestContractDraftHTTPMissingDataKey(t *testing.T) {
	t.Setenv("CLM_DATA_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7e}, 32))) // obsolete global is not a fallback
	t.Setenv("CLM_ENVELOPE_KEYS", "")
	t.Setenv("CLM_LEGACY_DATA_KEY_REF", "")
	api := newAPI(t)
	api.login(t, "demo-clerk", "clerk-demo")
	status, session := clmJSONRequest(t, api, http.MethodGet, "/api/session", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("read original command context: %d %v", status, session)
	}
	active, ok := session["active_context"].(map[string]any)
	if !ok {
		t.Fatalf("session has no active context: %v", session)
	}
	commandHeaders := func(key string) map[string]string {
		headers := clmHeaders(key)
		headers["X-Context-Version"], _ = active["context_version"].(string)
		headers["X-CSRF-Token"], _ = session["csrf_token"].(string)
		return headers
	}

	payload := clmCreatePayload("缺密钥草稿")
	payload["draft"].(map[string]any)["supplier"] = map[string]any{
		"name": "示例供应商", "bank_account": "6222020200001234567",
	}
	status, body := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", commandHeaders("no-key-http-0001"), payload)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("restricted create without key: %d %v", status, body)
	}

	plain := clmCreatePayload("无敏感字段")
	status, created := clmJSONRequest(t, api, http.MethodPost, "/api/contracts", commandHeaders("no-key-plain-001"), plain)
	if status != http.StatusCreated {
		t.Fatalf("unrestricted create without key: %d %v", status, created)
	}
}

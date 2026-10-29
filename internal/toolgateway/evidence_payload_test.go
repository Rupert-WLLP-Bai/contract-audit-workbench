package toolgateway

import (
	"net/http/httptest"
	"testing"
)

func TestEvidencePayloadPublicBoundaryRejectsIdentityAndAuthorizationDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gatewayFakeStore)
	}{
		{"parsed identity is no longer pinned", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.ID = "replacement" }},
		{"document tenant does not match principal", func(b *gatewayFakeStore) { b.workspace.Documents[0].Document.TenantID = "tenant-other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			test.mutate(backend)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			if response.Code == 200 || backend.finishStatus != "failed" || backend.finishOutputSize != 0 {
				t.Fatalf("boundary drift was not refused: status=%d finish=%s bytes=%d body=%s", response.Code, backend.finishStatus, backend.finishOutputSize, response.Body.String())
			}
		})
	}
}

func TestEvidencePayloadPublicBoundaryDeniesDisabledPrincipalBeforeReservation(t *testing.T) {
	backend := testBackend(t)
	backend.user.Enabled = false
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
	protocolFailure(t, response, 401, codeUnauthenticated, "unauthenticated")
	if backend.reserveCalls != 0 || backend.workspaceCalls != 0 {
		t.Fatalf("disabled principal reached data path: reserve=%d workspace=%d", backend.reserveCalls, backend.workspaceCalls)
	}
}

func TestEvidencePayloadPublicBoundaryReturnsEmptyResultWithoutSources(t *testing.T) {
	backend := testBackend(t)
	backend.workspace.Execution.Attempt.ParsedDocumentIDs = nil
	backend.workspace.Documents = nil
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
	evidence := protocolEvidence(t, response)
	if evidence.Status != "no_supported_statement" || len(evidence.Facts) != 0 {
		t.Fatalf("empty source result=%+v", evidence)
	}
}

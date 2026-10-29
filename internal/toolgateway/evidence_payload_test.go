package toolgateway

import (
	"net/http/httptest"
	"testing"

	"contractaudit/internal/store"
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

func TestEvidencePayloadIgnoresMalformedUnpinnedSourceThroughHandler(t *testing.T) {
	backend := testBackend(t)
	unselected := backend.workspace.Documents[0]
	parsed := *unselected.ParsedDocument
	unselected.ParsedDocument = &parsed
	unselected.Document.ID = "doc-unpinned"
	unselected.Document.TenantID = "tenant-other"
	unselected.ParsedDocument.ID = "parsed-unpinned"
	unselected.ParsedDocument.Text = string([]byte{0xff})
	backend.workspace.Documents = append([]store.Interpreted{unselected}, backend.workspace.Documents...)

	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
	evidence := protocolEvidence(t, response)
	if evidence.Status != "found" || len(evidence.Facts) != 1 || evidence.Facts[0].Value != "100" {
		t.Fatalf("an unpinned malformed source affected selected evidence: %+v", evidence)
	}
	citation := evidence.Facts[0].Citations[0]
	if citation.DocumentID != "doc-1" || citation.ParsedDocumentID != "parsed-1" || citation.SourceDigest != citation.NormalizedDigest {
		t.Fatalf("citation did not remain bound to the pinned normalized source: %+v", citation)
	}
	if backend.finishStatus != "completed" || backend.finishOutputSize <= 0 {
		t.Fatalf("successful frozen read did not settle: status=%s bytes=%d", backend.finishStatus, backend.finishOutputSize)
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

package httpapi_test

import (
	"net/http"
	"testing"

	"contractaudit/internal/docversion"
)

func TestHTTPReparseKeepsFrozenProvenanceAndCreatesNewRun(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "解析来源", "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create %d: %v", status, created)
	}
	caseID := created["id"].(string)
	original := []byte("采购文件要求质保36个月。   ")
	docID := api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", original)
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	reviewPath := "/api/cases/" + caseID + "/reviews"
	status, started := api.do(t, http.MethodPost, reviewPath, map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start %d: %v", status, started)
	}
	oldPath := reviewPath + "/" + started["id"].(string)
	status, old := api.do(t, http.MethodGet, oldPath, nil)
	if status != http.StatusOK {
		t.Fatalf("old workspace %d: %v", status, old)
	}
	assertWireSchema(t, openAPI(t), "Workspace", old)
	byDocument := map[string]map[string]any{}
	for _, raw := range old["documents"].([]any) {
		doc := raw.(map[string]any)
		parsed, ok := doc["parsed_document"].(map[string]any)
		if !ok || parsed["document_id"] != doc["id"] || parsed["original_digest"] != doc["digest"] || parsed["normalized_digest"] != doc["normalized_text_digest"] {
			t.Fatalf("parsed identity does not match frozen input: %+v", doc)
		}
		byDocument[doc["id"].(string)] = parsed
	}
	firstParse := byDocument[docID]
	inputIDs := old["execution"].(map[string]any)["attempt"].(map[string]any)["parsed_document_ids"].([]any)
	selectedIDs := map[any]bool{}
	for _, id := range inputIDs {
		if selectedIDs[id] {
			t.Fatalf("duplicate input parse reference: %v", id)
		}
		selectedIDs[id] = true
	}
	if len(selectedIDs) != len(byDocument) {
		t.Fatalf("attempt does not identify every frozen input: %v", inputIDs)
	}
	for _, parsed := range byDocument {
		if !selectedIDs[parsed["id"]] {
			t.Fatalf("workspace input is absent from attempt references: %+v", parsed)
		}
	}
	if firstParse["original_digest"] != docversion.Hash(original).String() || firstParse["normalized_digest"] == firstParse["original_digest"] {
		t.Fatalf("original and normalized identities were conflated: %+v", firstParse)
	}
	for _, rawFinding := range old["findings"].([]any) {
		finding := rawFinding.(map[string]any)
		for _, rawAnchor := range finding["evidence"].([]any) {
			anchor := rawAnchor.(map[string]any)
			parsed := byDocument[anchor["document_version_id"].(string)]
			if anchor["status"] != "located" || anchor["parsed_document_id"] != parsed["id"] || anchor["normalized_digest"] != parsed["normalized_digest"] {
				t.Fatalf("evidence is not bound to its frozen parse: %+v", anchor)
			}
		}
	}
	status, body := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/documents/"+docID+"/parse", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("reparse %d: %v", status, body)
	}
	status, detail := api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if status != http.StatusOK || detail["revision_pending"] != true {
		t.Fatalf("new parse was not marked pending: %d %+v", status, detail)
	}
	status, next := api.do(t, http.MethodPost, reviewPath, map[string]any{})
	if status != http.StatusCreated || next["id"] == started["id"] {
		t.Fatalf("explicit same-text reparse reused old run: %d %+v", status, next)
	}
	status, history := api.do(t, http.MethodGet, oldPath, nil)
	if status != http.StatusOK {
		t.Fatalf("history %d: %v", status, history)
	}
	for _, raw := range history["documents"].([]any) {
		doc := raw.(map[string]any)
		if doc["id"] == docID && doc["parsed_document"].(map[string]any)["artifact_digest"] != firstParse["artifact_digest"] {
			t.Fatalf("reparse replaced historical provenance: %+v", doc)
		}
	}
}

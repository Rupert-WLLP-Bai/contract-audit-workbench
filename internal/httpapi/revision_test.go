package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"contractaudit/internal/engine"
	"contractaudit/internal/model"
)

func TestRevisedDocumentOpensNewRun(t *testing.T) {
	api := newAPIWith(t, quotingModel{}, nil)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "修改稿复审", "contract_type": "procurement_ict",
	})
	caseID := created["id"].(string)
	docA := api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	docB := api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))

	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start %d %v", status, review)
	}
	oldID := review["id"].(string)
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+oldID, nil)
	finding := ws["findings"].([]any)[0].(map[string]any)
	findingID := finding["id"].(string)
	status, _ = api.send(t, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+oldID+"/findings/"+findingID+"/decisions", map[string]any{
		"action": "confirm_risk", "reason": "两份材料质保月数不同", "expected_version": 1,
	}, "rev-1")
	if status != http.StatusNoContent {
		t.Fatalf("decide %d", status)
	}
	_, before := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+oldID, nil)
	beforeDiagnostics := api.reviewDiagnostics(t, "/api/cases/"+caseID+"/reviews/"+oldID)

	materialID, currentDocumentID := api.materialForCurrentDocument(t, caseID, docB)
	docC := api.replaceUpload(t, caseID, "draft_contract", "draft-v2.txt", "text/plain", []byte("根据谈判，拟签合同约定质保24个月。其余义务不变。"), materialID, currentDocumentID)
	_, detail := api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if detail["revision_pending"] != true {
		t.Fatalf("pending %+v", detail["revision_pending"])
	}
	if !documentSuperseded(detail["documents"].([]any), docB) {
		t.Fatal("old draft was not marked superseded")
	}

	status, again := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated || again["id"] == oldID {
		t.Fatalf("new run %d %v", status, again)
	}
	newID := again["id"].(string)
	_, persisted := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+newID, nil)
	if persisted["previous_run_id"] != oldID {
		t.Fatalf("previous %v", persisted["previous_run_id"])
	}
	if !sameIDList(persisted["snapshot"].(map[string]any)["document_ids"].([]any), docA, docC) {
		t.Fatalf("new snapshot %v", persisted["snapshot"])
	}
	persisting := revisionByMark(t, persisted["revisions"].([]any), "持续")
	if persisting["previous_finding_id"] != findingID {
		t.Fatalf("持续 %+v", persisting)
	}
	prior := persisting["prior_decision"].(map[string]any)
	if prior["action"] != "confirm_risk" || prior["reason"] != "两份材料质保月数不同" {
		t.Fatalf("prior %+v", prior)
	}
	newFinding := persisted["findings"].([]any)[0].(map[string]any)
	if newFinding["id"] == findingID || newFinding["relation"] != "conflict" || newFinding["revision_mark"] != "持续" {
		t.Fatalf("new finding %+v", newFinding)
	}
	if len(newFinding["decisions"].([]any)) != 0 {
		t.Fatal("new run copied the decision")
	}
	if stringsContain(newFinding["claim"].(string), "模型写成") {
		t.Fatalf("model sentence became the claim: %s", newFinding["claim"])
	}
	assertUnlocatableWithoutPage(t, persisting["evidence"].([]any))

	_, old := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+oldID, nil)
	oldDiagnostics := api.reviewDiagnostics(t, "/api/cases/"+caseID+"/reviews/"+oldID)
	if !sameWorkspaceCore(before, old, beforeDiagnostics, oldDiagnostics) {
		t.Fatalf("old run changed\nbefore %+v\nafter %+v", beforeDiagnostics["snapshot"], oldDiagnostics["snapshot"])
	}
	oldFinding := old["findings"].([]any)[0].(map[string]any)
	if oldFinding["id"] != findingID || len(oldFinding["decisions"].([]any)) != 1 {
		t.Fatalf("old finding %+v", oldFinding)
	}
	if !sameIDList(old["snapshot"].(map[string]any)["document_ids"].([]any), docA, docB) {
		t.Fatalf("old snapshot %v", old["snapshot"])
	}

	materialID, currentDocumentID = api.materialForCurrentDocument(t, caseID, docC)
	docD := api.replaceUpload(t, caseID, "draft_contract", "draft-v3.txt", "text/plain", []byte("拟签合同约定质保36个月。"), materialID, currentDocumentID)
	_, cleared := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	_, clearedWS := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+cleared["id"].(string), nil)
	resolved := revisionByMark(t, clearedWS["revisions"].([]any), "消除")
	if resolved["finding_id"] != nil || resolved["previous_finding_id"] != newFinding["id"] {
		t.Fatalf("消除 %+v", resolved)
	}
	assertUnlocatableWithoutPage(t, resolved["evidence"].([]any))
	if len(clearedWS["findings"].([]any)) != 0 {
		t.Fatalf("aligned run still has findings %+v", clearedWS["findings"])
	}

	materialID, currentDocumentID = api.materialForCurrentDocument(t, caseID, docD)
	api.replaceUpload(t, caseID, "draft_contract", "draft-v4.txt", "text/plain", []byte("拟签合同改为质保12个月。"), materialID, currentDocumentID)
	_, addedRun := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	_, addedWS := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+addedRun["id"].(string), nil)
	added := revisionByMark(t, addedWS["revisions"].([]any), "新增")
	if added["previous_finding_id"] != nil || added["prior_decision"] != nil {
		t.Fatalf("新增 %+v", added)
	}
	addedFinding := addedWS["findings"].([]any)[0].(map[string]any)
	if addedFinding["relation"] != "conflict" || addedFinding["revision_mark"] != "新增" || len(addedFinding["decisions"].([]any)) != 0 {
		t.Fatalf("added finding %+v", addedFinding)
	}
	_, later := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+oldID, nil)
	laterDiagnostics := api.reviewDiagnostics(t, "/api/cases/"+caseID+"/reviews/"+oldID)
	if !sameWorkspaceCore(old, later, oldDiagnostics, laterDiagnostics) {
		t.Fatal("old run changed after later revisions")
	}
}

func revisionByMark(t *testing.T, items []any, mark string) map[string]any {
	t.Helper()
	for _, item := range items {
		row := item.(map[string]any)
		if row["mark"] == mark {
			return row
		}
	}
	t.Fatalf("missing %s in %+v", mark, items)
	return nil
}

func assertUnlocatableWithoutPage(t *testing.T, evidence []any) {
	t.Helper()
	for _, item := range evidence {
		anchor := item.(map[string]any)
		if anchor["quote"] != "这句话不在原文中" {
			continue
		}
		if anchor["status"] != "unlocatable" || anchor["page"] != nil || anchor["text_offset"] != nil {
			t.Fatalf("unlocatable page %+v", anchor)
		}
		return
	}
	t.Fatal("missing unlocatable quote")
}

func documentSuperseded(docs []any, id string) bool {
	for _, item := range docs {
		doc := item.(map[string]any)
		if doc["id"] == id {
			return doc["superseded"] == true
		}
	}
	return false
}

func sameIDList(got []any, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range got {
		seen[item.(string)] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}

func sameWorkspaceCore(before, after, beforeDiagnostics, afterDiagnostics map[string]any) bool {
	beforeSnapshot := beforeDiagnostics["snapshot"].(map[string]any)
	afterSnapshot := afterDiagnostics["snapshot"].(map[string]any)
	ruleHash, ok := beforeSnapshot["rule_set_hash"].(string)
	if !ok || ruleHash == "" || ruleHash != afterSnapshot["rule_set_hash"] {
		return false
	}
	left := before["snapshot"].(map[string]any)["document_ids"].([]any)
	right := after["snapshot"].(map[string]any)["document_ids"].([]any)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	bf := before["findings"].([]any)[0].(map[string]any)
	af := after["findings"].([]any)[0].(map[string]any)
	return bf["id"] == af["id"] && bf["claim"] == af["claim"] && len(bf["decisions"].([]any)) == len(af["decisions"].([]any))
}

func stringsContain(text, part string) bool {
	return strings.Contains(text, part)
}

type quotingModel struct{}

func (quotingModel) Name() string              { return "script-model" }
func (m quotingModel) ConfigurationID() string { return m.Name() }

func (quotingModel) Warranty(_ context.Context, docs []model.Material) (model.Warranty, error) {
	sources := make([]engine.Source, len(docs))
	for i, doc := range docs {
		status := "failed"
		if doc.Ready {
			status = "ready"
		}
		sources[i] = engine.Source{ID: doc.ID, Role: doc.Role, ParseStatus: status, Text: doc.Text}
	}
	drafts := engine.Compare(sources)
	if len(drafts) == 0 {
		return model.Warranty{Claim: "模型写成没有差异", Relation: "compatible", Disposition: "false_positive"}, nil
	}
	var quotes []model.Quote
	for _, anchor := range drafts[0].Anchors {
		if anchor.Status == "located" && anchor.Quote != "" {
			quotes = append(quotes, model.Quote{DocumentID: anchor.DocumentID, Quote: anchor.Quote})
		}
	}
	quotes = append(quotes, model.Quote{DocumentID: docs[0].ID, Quote: "这句话不在原文中"})
	disposition := "insufficient_evidence"
	if drafts[0].Relation == "conflict" {
		disposition = "confirm_risk"
	}
	return model.Warranty{Claim: "模型写成没有差异", Relation: drafts[0].Relation, Disposition: disposition, Quotes: quotes}, nil
}

func (quotingModel) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

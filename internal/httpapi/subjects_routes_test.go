package httpapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSubjectRegistryRoutesCreateAndReadAuthorizedSubject(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")

	status, created := api.send(t, http.MethodPost, "/api/subjects", map[string]any{
		"display_name": "路由契约主体",
	}, "subject-route-create-1")
	if status != http.StatusCreated {
		t.Fatalf("create subject: status=%d body=%v", status, created)
	}
	subjectID := requiredString(t, created, "subject_id")
	if created["current_version"] != float64(1) {
		t.Fatalf("unexpected created subject version: %v", created)
	}

	status, detail := api.do(t, http.MethodGet, "/api/subjects/"+subjectID, nil)
	if status != http.StatusOK || detail["subject_id"] != subjectID {
		t.Fatalf("read subject: status=%d body=%v", status, detail)
	}
	status, history := api.do(t, http.MethodGet, "/api/subjects/"+subjectID+"/history", nil)
	versions, hasVersions := history["versions"].([]any)
	if status != http.StatusOK || !hasVersions || len(versions) != 1 || versions[0].(map[string]any)["subject_id"] != subjectID {
		t.Fatalf("read subject history: status=%d body=%v", status, history)
	}
	status, links := api.do(t, http.MethodGet, "/api/subjects/"+subjectID+"/case-links", nil)
	if status != http.StatusOK || links["visible_count"] != float64(0) {
		t.Fatalf("read subject case links: status=%d body=%v", status, links)
	}
	status, list := api.do(t, http.MethodGet, "/api/subjects", nil)
	if status != http.StatusOK {
		t.Fatalf("list subjects: status=%d body=%v", status, list)
	}
	items, ok := list["items"].([]any)
	if !ok || len(items) != 1 || items[0].(map[string]any)["subject_id"] != subjectID {
		t.Fatalf("subject missing from registry: %v", list)
	}
}

func TestSubjectBasisNoteIsScopedToItsSourceCase(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")

	createCase := func(title string) string {
		status, body := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": title, "contract_type": "unspecified"})
		if status != http.StatusCreated {
			t.Fatalf("create case %q: %d %v", title, status, body)
		}
		return requiredString(t, body, "id")
	}
	caseA := createCase("授权案件A")
	caseB := createCase("授权案件B")
	for _, caseID := range []string{caseA, caseB} {
		status, body := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/members", map[string]any{"actor_id": "demo-reviewer"})
		if status != http.StatusOK {
			t.Fatalf("add reviewer to %s: %d %v", caseID, status, body)
		}
	}
	status, body := api.do(t, http.MethodPost, "/api/cases/"+caseA+"/members", map[string]any{"actor_id": "demo-clerk"})
	if status != http.StatusOK {
		t.Fatalf("add clerk to case A: %d %v", status, body)
	}

	const uscc = "91350100M000100Y43"
	status, subject := api.send(t, http.MethodPost, "/api/subjects", map[string]any{"display_name": "共享主体", "identifiers": []any{map[string]any{
		"namespace": "cn.gov.samr.uscc", "identifier_type": "unified_social_credit_code", "value": uscc,
	}}}, "basis-scope-subject")
	if status != http.StatusCreated {
		t.Fatalf("create subject: %d %v", status, subject)
	}
	subjectID := requiredString(t, subject, "subject_id")
	for _, caseID := range []string{caseA, caseB} {
		status, body := api.send(t, http.MethodPost, "/api/cases/"+caseID+"/subjects/bind-existing", map[string]any{
			"subject_id": subjectID, "expected_subject_version": 1, "role": "supplier",
		}, "basis-scope-bind-"+caseID)
		if status != http.StatusCreated {
			t.Fatalf("bind subject to %s: %d %v", caseID, status, body)
		}
	}

	const marker = "PRIVATE_CASE_B_BASIS_MARKER_83917"
	documentID := api.upload(t, caseB, "attachment", "basis.txt", "text/plain", []byte("受控材料记载了主体信息。"))
	materialID, _ := api.materialForCurrentDocument(t, caseB, documentID)
	status, parseList := api.do(t, http.MethodGet, "/api/cases/"+caseB+"/materials/"+materialID+"/versions/1/parsed-documents", nil)
	if status != http.StatusOK {
		t.Fatalf("list pinned parses: %d %v", status, parseList)
	}
	parses, ok := parseList["items"].([]any)
	if !ok || len(parses) == 0 {
		t.Fatalf("no pinned parse available: %v", parseList)
	}
	parsedID := requiredString(t, parses[0].(map[string]any), "parsed_document_id")
	status, parsed := api.do(t, http.MethodGet, "/api/cases/"+caseB+"/materials/"+materialID+"/versions/1/parsed-documents/"+parsedID, nil)
	if status != http.StatusOK {
		t.Fatalf("read pinned parse: %d %v", status, parsed)
	}
	quote := "受控材料记载了主体信息。"
	digest := sha256.Sum256([]byte(quote))
	start := 0
	status, proposal := api.send(t, http.MethodPost, "/api/cases/"+caseB+"/subject-changes", map[string]any{
		"target":                   map[string]any{"kind": "subject", "subject_id": subjectID},
		"expected_subject_version": 1,
		"patch":                    map[string]any{"display_name": "更正后的共享主体"},
		"basis_note":               marker,
		"basis_refs": []any{map[string]any{
			"kind": "material_anchor", "material_id": materialID, "material_version": 1, "document_id": documentID,
			"parsed_document_id": parsedID, "start": start, "end": len([]rune(quote)), "quote_digest": hex.EncodeToString(digest[:]),
		}},
	}, "basis-scope-proposal")
	if status != http.StatusCreated {
		t.Fatalf("propose change: %d %v", status, proposal)
	}
	requestID := requiredString(t, proposal, "request_id")

	api.login(t, "demo-reviewer", "reviewer-demo")
	status, impact := api.do(t, http.MethodGet, "/api/cases/"+caseB+"/subject-changes/"+requestID+"/impact", nil)
	if status != http.StatusOK {
		t.Fatalf("read change impact: %d %v", status, impact)
	}
	impactJSON, err := json.Marshal(impact)
	if err != nil || !strings.Contains(string(impactJSON), uscc) {
		t.Fatalf("eligible independent approver did not receive full identifier preview: %s err=%v", impactJSON, err)
	}
	status, decision := api.send(t, http.MethodPost, "/api/cases/"+caseB+"/subject-changes/"+requestID+"/decisions", map[string]any{
		"decision": "approve", "expected_subject_version": 1, "expected_payload_hash": proposal["payload_hash"],
		"expected_impact_etag": impact["impact_etag"], "reason": "按材料核验并批准",
	}, "basis-scope-decision")
	if status != http.StatusOK {
		t.Fatalf("approve change: %d %v", status, decision)
	}

	api.login(t, "demo-clerk", "clerk-demo")
	responses := []struct {
		name string
		path string
	}{
		{name: "subject detail", path: "/api/subjects/" + subjectID},
		{name: "subject history", path: "/api/subjects/" + subjectID + "/history"},
		{name: "case A binding", path: "/api/cases/" + caseA + "/subjects"},
	}
	for _, item := range responses {
		t.Run(item.name, func(t *testing.T) {
			status, result := api.do(t, http.MethodGet, item.path, nil)
			if status != http.StatusOK {
				t.Fatalf("read %s: %d %v", item.name, status, result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), marker) {
				t.Fatalf("private basis from case B leaked through %s: %s", item.name, encoded)
			}
			if strings.Contains(string(encoded), uscc) {
				t.Fatalf("raw USCC leaked through ordinary Read response %s: %s", item.name, encoded)
			}
			if !strings.Contains(string(encoded), "经审批更正") {
				t.Fatalf("generic basis provenance missing from %s: %s", item.name, encoded)
			}
		})
	}
	api.login(t, "demo-admin", "admin-demo")
	status, sourceCaseDetail := api.do(t, http.MethodGet, "/api/cases/"+caseB+"/subject-changes/"+requestID, nil)
	sourceCaseJSON, err := json.Marshal(sourceCaseDetail)
	if status != http.StatusOK || err != nil || !strings.Contains(string(sourceCaseJSON), marker) || !strings.Contains(string(sourceCaseJSON), uscc) {
		t.Fatalf("source-case manager did not retain approved basis and full-identifier access: status=%d body=%s err=%v", status, sourceCaseJSON, err)
	}
}

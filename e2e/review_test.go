package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestProcurementWarrantyConflictAndConfirm(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenTest(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(e2eModels{})
	srv := httptest.NewServer(httpapi.Handler(st, httpapi.Options{}))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	status, login := do(t, client, srv.URL, http.MethodPost, "/api/session", map[string]any{
		"username": "demo-admin", "password": "admin-demo",
	}, "")
	if status != http.StatusOK || login["actor"].(map[string]any)["id"] != "demo-admin" {
		t.Fatalf("login %d %v", status, login)
	}
	status, created := do(t, client, srv.URL, http.MethodPost, "/api/cases", map[string]any{
		"title": "采购质保核对", "contract_type": "procurement_ict",
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("create %d %v", status, created)
	}
	caseID := created["id"].(string)
	rawA := []byte("采购文件要求质保36个月。")
	rawB := []byte("拟签合同约定质保24个月。")
	docA := upload(t, client, srv.URL, caseID, "procurement", "purchase.txt", rawA)
	docB := upload(t, client, srv.URL, caseID, "draft_contract", "draft.txt", rawB)
	if docA == docB {
		t.Fatal("documents were not distinct")
	}

	status, review := do(t, client, srv.URL, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{}, "")
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	runID := review["id"].(string)
	status, ws := do(t, client, srv.URL, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil, "")
	if status != http.StatusOK {
		t.Fatalf("workspace %d %v", status, ws)
	}
	findings := ws["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("findings %+v", findings)
	}
	finding := findings[0].(map[string]any)
	if finding["relation"] != "conflict" || finding["origin"] != "rule" || finding["domain"] != "warranty" {
		t.Fatalf("finding %+v", finding)
	}
	evidence := finding["evidence"].([]any)
	if len(evidence) != 2 {
		t.Fatalf("evidence %+v", evidence)
	}
	for _, item := range evidence {
		anchor := item.(map[string]any)
		if anchor["status"] != "located" || anchor["page"] != nil || anchor["bbox"] != nil || anchor["text_offset"] == nil {
			t.Fatalf("anchor %+v", anchor)
		}
		offset := anchor["text_offset"].(map[string]any)
		start := int(offset["start"].(float64))
		end := int(offset["end"].(float64))
		var text string
		for _, doc := range ws["documents"].([]any) {
			row := doc.(map[string]any)
			if row["id"] == anchor["document_version_id"] {
				text = row["text"].(string)
			}
		}
		quote, ok := docir.Slice(text, start, end)
		if !ok || quote != anchor["quote"] {
			t.Fatalf("quote %q slice %q", anchor["quote"], quote)
		}
	}
	snap := ws["snapshot"].(map[string]any)
	if snap["comparator_ref"] != engine.ComparatorRef || snap["rule_set_hash"] != engine.RuleSetHash() {
		t.Fatalf("snapshot %+v", snap)
	}

	status, _ = do(t, client, srv.URL, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+runID+"/findings/"+finding["id"].(string)+"/decisions", map[string]any{
		"action": "confirm_risk", "reason": "两份材料写明的质保月数不同", "expected_version": 1,
	}, "e2e-1")
	if status != http.StatusNoContent {
		t.Fatalf("decide %d", status)
	}
	_, ws = do(t, client, srv.URL, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil, "")
	finding = ws["findings"].([]any)[0].(map[string]any)
	history := finding["decisions"].([]any)
	if len(history) != 1 || history[0].(map[string]any)["action"] != "confirm_risk" {
		t.Fatalf("decision %+v", finding)
	}
	if ws["run"].(map[string]any)["state"] != "completed" {
		t.Fatalf("state %+v", ws["run"])
	}
}

func do(t *testing.T, client *http.Client, base, method, path string, payload any, idem string) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	res, err := client.Do(req)
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
	if method == http.MethodPost && res.StatusCode >= 200 && res.StatusCode < 300 &&
		(strings.HasSuffix(path, "/reviews") || strings.HasSuffix(path, "/retry")) {
		workspacePath := strings.SplitN(path, "/reviews", 2)[0] + "/reviews/" + out["id"].(string)
		deadline := time.Now().Add(10 * time.Second)
		for {
			status, workspace := do(t, client, base, http.MethodGet, workspacePath, nil, "")
			if status != http.StatusOK {
				t.Fatalf("waiting workspace %d: %v", status, workspace)
			}
			state := workspace["run"].(map[string]any)["state"]
			if state != "accepted" && state != "running" && state != "queued" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("accepted review did not terminate")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return res.StatusCode, out
}

func upload(t *testing.T, client *http.Client, base, caseID, role, name string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="` + name + `"`},
		"Content-Type":        {"text/plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("role", role); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/cases/"+caseID+"/documents", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload %d %s", res.StatusCode, raw)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload["id"].(string)
}

type e2eModels struct{}

func (e2eModels) Name() string              { return "script-model" }
func (m e2eModels) ConfigurationID() string { return m.Name() }

func (e2eModels) Warranty(_ context.Context, docs []model.Material) (model.Warranty, error) {
	return warrantyOf(docs)
}

func warrantyOf(docs []model.Material) (model.Warranty, error) {
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
		return model.Warranty{Claim: "质保月数一致", Relation: "compatible"}, nil
	}
	var quotes []model.Quote
	for _, anchor := range drafts[0].Anchors {
		if anchor.Status == "located" && anchor.Quote != "" {
			quotes = append(quotes, model.Quote{DocumentID: anchor.DocumentID, Quote: anchor.Quote})
		}
	}
	return model.Warranty{Claim: drafts[0].Claim, Relation: drafts[0].Relation, Disposition: "confirm_risk", Quotes: quotes}, nil
}

func (e2eModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

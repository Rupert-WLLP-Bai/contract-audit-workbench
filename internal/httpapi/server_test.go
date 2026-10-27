package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/docversion"
	"contractaudit/internal/engine"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestReviewFlow(t *testing.T) {
	api := newAPI(t)

	admin := api.login(t, "demo-admin", "admin-demo")
	if admin["role"] != "admin" || admin["display_name"] != "演示管理员" {
		t.Fatalf("actor %+v", admin)
	}
	status, session := api.do(t, http.MethodGet, "/api/session", nil)
	if status != http.StatusOK || session["actor"].(map[string]any)["id"] != "demo-admin" {
		t.Fatalf("session %d %v", status, session)
	}

	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "质保期限核对", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create %d %v", status, created)
	}
	caseID := created["id"].(string)

	rawA := []byte("采购文件要求质保36个月。   ")
	rawB := []byte("拟签合同约定质保24个月。")
	docA := api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", rawA)
	docB := api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", rawB)
	if docA == docB {
		t.Fatal("versions must stay distinct")
	}

	status, detail := api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if status != http.StatusOK {
		t.Fatalf("detail %d %v", status, detail)
	}
	docs := detail["documents"].([]any)
	if len(docs) != 2 {
		t.Fatalf("docs %+v", docs)
	}
	first := docs[0].(map[string]any)
	if first["digest"] != docversion.Hash(rawA).String() {
		t.Fatalf("digest %v", first["digest"])
	}
	if first["normalized_text_digest"] == first["digest"] {
		t.Fatal("padding must not collapse into the original digest")
	}
	if first["parse_status"] != "ready" || first["coordinate_space"] != "unicode_codepoint" {
		t.Fatalf("parse %+v", first)
	}

	status, body := api.bytes(t, http.MethodGet, "/api/cases/"+caseID+"/documents/"+docA+"/original", nil, "")
	if status != http.StatusOK || !bytes.Equal(body, rawA) {
		t.Fatalf("original %d %q", status, body)
	}

	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	runID := review["id"].(string)
	status, again := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated || again["id"] != runID {
		t.Fatalf("second start %d %v", status, again)
	}

	status, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("workspace %d %v", status, ws)
	}
	run := ws["run"].(map[string]any)
	if run["state"] != "awaiting_human" {
		t.Fatalf("run %+v", run)
	}
	diagnostics := api.reviewDiagnostics(t, "/api/cases/"+caseID+"/reviews/"+runID)
	if snapshot := diagnostics["snapshot"].(map[string]any); snapshot["model_ref"] != "script-model" {
		t.Fatalf("frozen model reference %+v", snapshot)
	}
	findings := ws["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("findings %+v", findings)
	}
	finding := findings[0].(map[string]any)
	if finding["origin"] != "rule" || finding["relation"] != "conflict" || finding["version"] != 1.0 {
		t.Fatalf("finding %+v", finding)
	}
	if len(finding["decisions"].([]any)) != 0 {
		t.Fatal("a rule finding must not arrive with a decision")
	}
	evidence := finding["evidence"].([]any)
	if len(evidence) != 2 {
		t.Fatalf("evidence %+v", evidence)
	}
	for _, item := range evidence {
		anchor := item.(map[string]any)
		if anchor["status"] != "located" || anchor["page"] != nil || anchor["bbox"] != nil {
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
		quote := []rune(text)[start:end]
		if string(quote) != anchor["quote"] {
			t.Fatalf("highlight %q vs %q", string(quote), anchor["quote"])
		}
	}

	findingID := finding["id"].(string)
	decide := "/api/cases/" + caseID + "/reviews/" + runID + "/findings/" + findingID + "/decisions"
	status, _ = api.send(t, http.MethodPost, decide, map[string]any{
		"action": "confirm_risk", "reason": "两份材料写明的质保月数不同", "expected_version": 1,
	}, "idem-1")
	if status != http.StatusNoContent {
		t.Fatalf("decide %d", status)
	}
	status, _ = api.send(t, http.MethodPost, decide, map[string]any{
		"action": "confirm_risk", "reason": "两份材料写明的质保月数不同", "expected_version": 1,
	}, "idem-1")
	if status != http.StatusNoContent {
		t.Fatalf("replay %d", status)
	}
	status, ws = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	finding = ws["findings"].([]any)[0].(map[string]any)
	history := finding["decisions"].([]any)
	if status != http.StatusOK || len(history) != 1 || finding["version"] != 2.0 {
		t.Fatalf("after decide %+v", finding)
	}
	row := history[0].(map[string]any)
	if row["old_disposition"] != "pending" || row["new_disposition"] != "confirmed" || row["actor"] != "演示管理员" {
		t.Fatalf("decision %+v", row)
	}
	if ws["run"].(map[string]any)["state"] != "completed" {
		t.Fatalf("state %+v", ws["run"])
	}

	status, _ = api.send(t, http.MethodPost, decide, map[string]any{
		"action": "false_positive", "reason": "这是更正，保留上一条", "expected_version": 1,
	}, "idem-2")
	if status != http.StatusConflict {
		t.Fatalf("stale version %d", status)
	}
	status, _ = api.send(t, http.MethodPost, decide, map[string]any{
		"action": "false_positive", "reason": "这是更正，保留上一条", "expected_version": 2,
	}, "idem-2")
	if status != http.StatusNoContent {
		t.Fatalf("correction %d", status)
	}
	_, ws = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	history = ws["findings"].([]any)[0].(map[string]any)["decisions"].([]any)
	if len(history) != 2 {
		t.Fatalf("history was replaced: %+v", history)
	}
	originalAttemptID := ws["review_attempts"].(map[string]any)["attempt"].(map[string]any)["id"].(string)
	status, retried := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+runID+"/retry", map[string]any{})
	if status != http.StatusOK || retried["attempt_id"] == originalAttemptID {
		t.Fatalf("retry did not append a new attempt: %d %v", status, retried)
	}
	_, historical := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID+"?attempt_id="+originalAttemptID, nil)
	keptFinding := historical["findings"].([]any)[0].(map[string]any)
	if keptFinding["id"] != findingID || len(keptFinding["decisions"].([]any)) != 2 {
		t.Fatalf("retry overwrote prior decisions: %+v", keptFinding)
	}
	_, current := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	currentFinding := current["findings"].([]any)[0].(map[string]any)
	if currentFinding["id"] == findingID || len(currentFinding["decisions"].([]any)) != 0 {
		t.Fatalf("retry moved prior decisions into the new attempt: %+v", currentFinding)
	}

	status, list := api.do(t, http.MethodGet, "/api/cases", nil)
	if status != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("list %d %v", status, list)
	}

	stream := api.events(t, "/api/cases/"+caseID+"/reviews/"+runID+"/events")
	if !strings.Contains(stream, `"type":"state"`) || !strings.Contains(stream, `"seq":`) {
		t.Fatalf("events %s", stream)
	}
}

func TestAccessAndParseFailures(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "权限", "contract_type": "unspecified",
	})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "a.txt", "text/plain", []byte("质保36个月。"))
	api.upload(t, caseID, "draft_contract", "b.txt", "text/plain", []byte("质保36个月。"))
	pdf := api.upload(t, caseID, "attachment", "scan.pdf", "application/pdf", []byte("%PDF-1.4\x00binary"))
	// Formats that may need OCR are admitted without provider work at upload.
	// Explicit parsing validates the actual PDF and preserves its failed artifact.
	status, _ := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/documents/"+pdf+"/parse", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("parse corrupt PDF: %d", status)
	}

	_, detail := api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	var failed bool
	for _, item := range detail["documents"].([]any) {
		doc := item.(map[string]any)
		if doc["id"] == pdf {
			failed = doc["parse_status"] == "failed" && doc["parse_error"] != nil
		}
	}
	if !failed {
		t.Fatalf("pdf was treated as text: %+v", detail["documents"])
	}

	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start %d %v", status, review)
	}
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+review["id"].(string), nil)
	if ws["run"].(map[string]any)["state"] != "partial" {
		t.Fatalf("run %+v", ws["run"])
	}
	findings := ws["findings"].([]any)
	if len(findings) != 1 || findings[0].(map[string]any)["relation"] != "insufficient" {
		t.Fatalf("agreement plus a failed file must stay insufficient: %+v", findings)
	}
	var sawMissing bool
	for _, item := range findings[0].(map[string]any)["evidence"].([]any) {
		anchor := item.(map[string]any)
		if anchor["status"] == "missing" {
			sawMissing = anchor["text_offset"] == nil && anchor["bbox"] == nil
		}
	}
	if !sawMissing {
		t.Fatal("failed file did not stay unlocated")
	}

	api.login(t, "demo-viewer", "viewer-demo")
	status, _ = api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("viewer %d", status)
	}

	api.login(t, "demo-admin", "admin-demo")
	status, _ = api.do(t, http.MethodPost, "/api/cases/"+caseID+"/members", map[string]any{"actor_id": "demo-clerk"})
	if status != http.StatusOK {
		t.Fatalf("member %d", status)
	}
	api.login(t, "demo-clerk", "clerk-demo")
	status, _ = api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	if status != http.StatusOK {
		t.Fatalf("clerk read %d", status)
	}
	findingID := findings[0].(map[string]any)["id"].(string)
	status, _ = api.send(t, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+review["id"].(string)+"/findings/"+findingID+"/decisions", map[string]any{
		"action": "confirm_risk", "reason": "经办不能结案", "expected_version": 1,
	}, "")
	if status != http.StatusForbidden {
		t.Fatalf("clerk decide %d", status)
	}

	status, bad := api.do(t, http.MethodPost, "/api/session", map[string]any{"username": "demo-admin", "password": "wrong"})
	if status != http.StatusUnauthorized || bad["error"].(map[string]any)["code"] != "unauthenticated" {
		t.Fatalf("login %d %v", status, bad)
	}
}

func TestCompatibleEvaluationIsNotAFinding(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "质保一致", "contract_type": "procurement_ict",
	})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "award", "award.txt", "text/plain", []byte("中标承诺的质量保证期为36个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+review["id"].(string), nil)
	if ws["run"].(map[string]any)["state"] != "completed" {
		t.Fatalf("run %+v", ws["run"])
	}
	if findings := ws["findings"].([]any); len(findings) != 0 {
		t.Fatalf("compatible became a finding: %+v", findings)
	}
	evals := ws["evaluations"].([]any)
	if len(evals) != 1 || evals[0].(map[string]any)["relation"] != "compatible" || evals[0].(map[string]any)["finding_id"] != nil {
		t.Fatalf("evaluation %+v", evals)
	}
}

func TestPagedClauseAnchorHasNoInventedBox(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "分页质保", "contract_type": "network_engineering",
	})
	caseID := created["id"].(string)
	pagedID := api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("封面未列期限。\f第二条 质保期为36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+review["id"].(string), nil)
	var sawClause bool
	for _, item := range ws["documents"].([]any) {
		doc := item.(map[string]any)
		if doc["id"] != pagedID {
			continue
		}
		for _, raw := range doc["clauses"].([]any) {
			clause := raw.(map[string]any)
			if clause["page"] == 2.0 {
				sawClause = true
			}
		}
	}
	finding := ws["findings"].([]any)[0].(map[string]any)
	if finding["relation"] != "conflict" || !sawClause {
		t.Fatalf("clause %v finding %+v", sawClause, finding)
	}
	var sawPage bool
	for _, item := range finding["evidence"].([]any) {
		anchor := item.(map[string]any)
		if anchor["bbox"] != nil {
			t.Fatalf("text anchor invented a box: %+v", anchor)
		}
		if anchor["status"] != "located" {
			t.Fatalf("anchor %+v", anchor)
		}
		if anchor["document_version_id"] == pagedID {
			if anchor["page"] != 2.0 {
				t.Fatalf("page %+v", anchor)
			}
			sawPage = true
			offset := anchor["text_offset"].(map[string]any)
			start := int(offset["start"].(float64))
			end := int(offset["end"].(float64))
			var text string
			for _, doc := range ws["documents"].([]any) {
				row := doc.(map[string]any)
				if row["id"] == pagedID {
					text = row["text"].(string)
				}
			}
			if string([]rune(text)[start:end]) != anchor["quote"] {
				t.Fatalf("quote %v", anchor["quote"])
			}
		}
	}
	if !sawPage {
		t.Fatalf("evidence %+v", finding["evidence"])
	}
}

func TestOneReadyDocumentCannotStart(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-clerk", "clerk-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "单份", "contract_type": "network_engineering",
	})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "only.txt", "text/plain", []byte("质保12个月。"))
	status, body := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("start %d %v", status, body)
	}
}

func TestWaitReviewPollsUntilTerminalWithoutBusyWaiting(t *testing.T) {
	var mu sync.Mutex
	var requests []time.Time
	states := []string{"accepted", "running", "completed"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		index := len(requests)
		requests = append(requests, time.Now())
		mu.Unlock()
		if index >= len(states) {
			http.Error(w, "unexpected workspace request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"run":{"state":%q}}`, states[index])
	}))
	defer srv.Close()

	api := &testAPI{base: srv.URL, client: srv.Client()}
	workspace := api.waitReview(t, "/review")
	if got := reviewState(workspace); got != "completed" {
		t.Fatalf("wait returned before the terminal state: %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != len(states) {
		t.Fatalf("workspace requests = %d, want %d", len(requests), len(states))
	}
	for i := 1; i < len(requests); i++ {
		if elapsed := requests[i].Sub(requests[i-1]); elapsed < 100*time.Millisecond {
			t.Fatalf("workspace requests %d and %d were only %s apart", i, i+1, elapsed)
		}
	}
}

type testAPI struct {
	base   string
	client *http.Client
}

func newAPI(t *testing.T) *testAPI {
	t.Helper()
	return newAPIWith(t, scriptModels{}, nil)
}

func newAPIWith(t *testing.T, models store.Models, settings *model.Client) *testAPI {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(models)
	srv := httptest.NewServer(httpapi.Handler(st, httpapi.Options{Models: settings}))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testAPI{base: srv.URL, client: &http.Client{Jar: jar}}
}

// scriptModels stands in for the live model inside tests that are not about the network.
// It still goes through quote location. It is not used when the server has no model configured.
type scriptModels struct{}

func (scriptModels) Name() string              { return "script-model" }
func (m scriptModels) ConfigurationID() string { return m.Name() }

func (scriptModels) Warranty(_ context.Context, docs []model.Material) (model.Warranty, error) {
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
		return model.Warranty{Claim: "质保月数一致", Relation: "compatible", Disposition: "false_positive"}, nil
	}
	var quotes []model.Quote
	for _, anchor := range drafts[0].Anchors {
		if anchor.Status == "located" && anchor.Quote != "" {
			quotes = append(quotes, model.Quote{DocumentID: anchor.DocumentID, Quote: anchor.Quote})
		}
	}
	disposition := "insufficient_evidence"
	if drafts[0].Relation == "conflict" {
		disposition = "confirm_risk"
	}
	return model.Warranty{
		Claim: "模型建议：" + drafts[0].Claim, Relation: drafts[0].Relation,
		Disposition: disposition, Quotes: quotes,
	}, nil
}

func (scriptModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func (a *testAPI) login(t *testing.T, username, password string) map[string]any {
	t.Helper()
	status, body := a.do(t, http.MethodPost, "/api/session", map[string]any{"username": username, "password": password})
	if status != http.StatusOK {
		t.Fatalf("login %s %d %v", username, status, body)
	}
	return body["actor"].(map[string]any)
}

func (a *testAPI) upload(t *testing.T, caseID, role, name, media string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="` + name + `"`},
		"Content-Type":        {media},
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
	status, body := a.bytes(t, http.MethodPost, "/api/cases/"+caseID+"/documents", bytes.NewReader(buf.Bytes()), w.FormDataContentType())
	if status != http.StatusCreated {
		t.Fatalf("upload %d %s", status, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	return payload["id"].(string)
}

func (a *testAPI) do(t *testing.T, method, path string, payload any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	status, raw := a.bytes(t, method, path, body, "application/json")
	if len(bytes.TrimSpace(raw)) == 0 {
		return status, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json %d %s", status, raw)
	}
	if method == http.MethodPost && status >= 200 && status < 300 &&
		(strings.HasSuffix(path, "/reviews") || strings.HasSuffix(path, "/retry")) {
		casePath := strings.SplitN(path, "/reviews", 2)[0]
		a.waitReview(t, casePath+"/reviews/"+out["id"].(string))
	}
	return status, out
}

func (a *testAPI) reviewDiagnostics(t *testing.T, reviewPath string) map[string]any {
	t.Helper()
	status, body := a.do(t, http.MethodGet, reviewPath+"/diagnostics", nil)
	if status != http.StatusOK {
		t.Fatalf("review diagnostics %s: %d %+v", reviewPath, status, body)
	}
	return body
}

const (
	// A progressing asynchronous review can take longer under race/coverage and
	// concurrent PostgreSQL tests. Still fail a stalled review and cap total time.
	reviewWaitLimit    = 60 * time.Second
	reviewStallLimit   = 15 * time.Second
	reviewPollInterval = 500 * time.Millisecond
)

func (a *testAPI) waitReview(t *testing.T, path string) map[string]any {
	t.Helper()
	start := time.Now()
	overallDeadline := start.Add(reviewWaitLimit)
	progressDeadline := start.Add(reviewStallLimit)
	var last map[string]any
	lastErr := ""
	previous := reviewProgress{eventSeq: -1}
	for {
		deadline := progressDeadline
		if overallDeadline.Before(deadline) {
			deadline = overallDeadline
		}
		if !time.Now().Before(deadline) {
			break
		}
		workspace, err := a.workspaceWithin(t, path, deadline)
		if err != nil {
			lastErr = err.Error()
		} else {
			last = workspace
			state := reviewState(workspace)
			if state == "" {
				t.Fatalf("waiting workspace has no valid run state: %v", workspace["run"])
			}
			if state != "accepted" && state != "running" && state != "queued" {
				return workspace
			}
			current := reviewProgressOf(workspace)
			if current.runState != previous.runState || current.attemptID != previous.attemptID ||
				current.attemptState != previous.attemptState || current.eventSeq > previous.eventSeq {
				progressDeadline = time.Now().Add(reviewStallLimit)
			}
			if current.attemptID == previous.attemptID && current.eventSeq < previous.eventSeq {
				current.eventSeq = previous.eventSeq
			}
			previous = current
		}
		// Do not sleep beyond either the progress or overall deadline.
		deadline = progressDeadline
		if overallDeadline.Before(deadline) {
			deadline = overallDeadline
		}
		if wait := min(reviewPollInterval, time.Until(deadline)); wait > 0 {
			time.Sleep(wait)
		}
	}
	t.Fatalf("review did not reach terminal execution state: %s; %s", path, reviewWaitDiagnostics(last, lastErr))
	return nil
}

// workspaceWithin reads one workspace with the review deadline attached, so a
// stalled response cannot hang the test past its own bound. A refusal is still
// a hard failure: only the deadline ends the wait early.
func (a *testAPI) workspaceWithin(t *testing.T, path string, deadline time.Time) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("workspace read remained in flight until the wait deadline: %w", ctx.Err())
		}
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("workspace body remained unread until the wait deadline: %w", ctx.Err())
		}
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("waiting workspace %d: %s", res.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json %d %s", res.StatusCode, raw)
	}
	return out, nil
}

func reviewState(workspace map[string]any) string {
	run, _ := workspace["run"].(map[string]any)
	state, _ := run["state"].(string)
	return state
}

type reviewProgress struct {
	runState     string
	attemptID    string
	attemptState string
	eventSeq     float64
}

func reviewProgressOf(workspace map[string]any) reviewProgress {
	progress := reviewProgress{runState: reviewState(workspace), eventSeq: -1}
	attempts, _ := workspace["review_attempts"].(map[string]any)
	attempt, _ := attempts["attempt"].(map[string]any)
	progress.attemptID, _ = attempt["id"].(string)
	progress.attemptState, _ = attempt["state"].(string)
	if events, ok := workspace["business_events"].([]any); ok && len(events) > 0 {
		if event, ok := events[len(events)-1].(map[string]any); ok {
			if seq, ok := event["seq"].(float64); ok {
				progress.eventSeq = seq
			}
		}
	}
	return progress
}

// reviewWaitDiagnostics reports the last observed business run, attempt and
// audit event; it does not assume that execution has stopped.
func reviewWaitDiagnostics(workspace map[string]any, lastErr string) string {
	if workspace == nil {
		if lastErr == "" {
			return "no workspace response before the deadline"
		}
		return "no workspace response before the deadline: " + lastErr
	}
	detail := fmt.Sprintf("last run state=%q", reviewState(workspace))
	attempts, _ := workspace["review_attempts"].(map[string]any)
	if attempt, ok := attempts["attempt"].(map[string]any); ok {
		detail += fmt.Sprintf(" attempt=%v state=%v history_kind=%v started_at=%v",
			attempt["id"], attempt["state"], attempt["history_kind"], attempt["started_at"])
	} else {
		detail += " attempt=absent"
	}
	if events, ok := workspace["business_events"].([]any); ok && len(events) > 0 {
		event, _ := events[len(events)-1].(map[string]any)
		detail += fmt.Sprintf(" last_event={seq=%v event_type=%v actor=%v}", event["seq"], event["event_type"], event["actor"])
	} else {
		detail += " last_event=absent"
	}
	if lastErr != "" {
		detail += " last_read_error=" + lastErr
	}
	return detail
}

func (a *testAPI) send(t *testing.T, method, path string, payload any, idem string) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, a.base+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	res, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if len(bytes.TrimSpace(raw)) == 0 {
		return res.StatusCode, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json %d %s", res.StatusCode, raw)
	}
	return res.StatusCode, out
}

func (a *testAPI) bytes(t *testing.T, method, path string, body io.Reader, contentType string) (int, []byte) {
	t.Helper()
	if body == nil {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, a.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" && method != http.MethodGet {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}

func (a *testAPI) events(t *testing.T, path string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	res, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events status %d %s", res.StatusCode, raw)
	}
	return string(raw)
}

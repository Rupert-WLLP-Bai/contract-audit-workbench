package httpapi_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/model"
)

func reviewFixture(t *testing.T, api *testAPI) (string, string, string) {
	t.Helper()
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "持续事件流", "contract_type": "procurement_ict"})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "a.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "b.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	runID := review["id"].(string)
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	findingID := ws["findings"].([]any)[0].(map[string]any)["id"].(string)
	return caseID, runID, findingID
}

func TestEventStreamReceivesLaterDecisionsAndResumesWithoutReplay(t *testing.T) {
	api := newAPI(t)
	caseID, runID, findingID := reviewFixture(t, api)
	path := "/api/cases/" + caseID + "/reviews/" + runID
	cursor := 0
	for _, line := range strings.Split(api.events(t, path+"/events"), "\n") {
		if strings.HasPrefix(line, "id: ") {
			seq, err := strconv.Atoi(strings.TrimPrefix(line, "id: "))
			if err != nil {
				t.Fatal(err)
			}
			if seq > cursor {
				cursor = seq
			}
		}
	}
	if cursor == 0 {
		t.Fatal("completed execution did not publish a resumable event cursor")
	}
	for expected := 1; expected <= 2; expected++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.base+path+"/events", nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Last-Event-ID", strconv.Itoa(cursor))
		res, err := api.client.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			cancel()
			t.Fatalf("stream status %d", res.StatusCode)
		}
		status, _ := api.send(t, http.MethodPost, path+"/findings/"+findingID+"/decisions", map[string]any{
			"action": "confirm_risk", "reason": "已核对原文月数", "expected_version": expected,
		}, "stream-"+strconv.Itoa(expected))
		if status != http.StatusNoContent {
			res.Body.Close()
			cancel()
			t.Fatalf("decision status %d", status)
		}
		scan := bufio.NewScanner(res.Body)
		var id, data string
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, "id: ") {
				id = strings.TrimPrefix(line, "id: ")
			}
			if strings.HasPrefix(line, "data: ") {
				data = line
				break
			}
		}
		res.Body.Close()
		cancel()
		if id != strconv.Itoa(cursor+1) || !strings.Contains(data, `"type":"decision"`) {
			t.Fatalf("missing live event or duplicate replay: id=%q data=%q err=%v", id, data, scan.Err())
		}
		cursor++
	}
}

func TestLogoutRevokesExistingEventStream(t *testing.T) {
	api := newAPI(t)
	caseID, runID, _ := reviewFixture(t, api)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.base+"/api/cases/"+caseID+"/reviews/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "1")
	res, err := api.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	status, _ := api.do(t, http.MethodDelete, "/api/session", nil)
	if status != http.StatusNoContent {
		t.Fatalf("logout status %d", status)
	}
	_, err = io.ReadAll(res.Body)
	if err != nil || ctx.Err() != nil {
		t.Fatalf("revoked stream did not close cleanly: %v context=%v", err, ctx.Err())
	}
}

func TestOriginalHTMLIsDownloadedAsPassiveBytes(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "原文内容边界", "contract_type": "procurement_ict"})
	caseID := created["id"].(string)
	body := []byte(`<script>fetch('/api/cases')</script>`)
	docID := api.upload(t, caseID, "attachment", "原始材料.html", "text/html", body)
	res, err := api.client.Get(api.base + "/api/cases/" + caseID + "/documents/" + docID + "/original")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil || string(raw) != string(body) {
		t.Fatalf("original bytes changed: %q %v", raw, err)
	}
	if res.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment;") || !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("active original returned unsafely: %v", res.Header)
	}
}

func TestHTTP503RetainsDeterministicFinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model offline", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	client := model.New(model.Config{BaseURL: upstream.URL, ChatModel: "offline", OCRModel: "offline"})
	api := newAPIWith(t, client, nil)
	caseID, runID, _ := reviewFixture(t, api)
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	run := ws["run"].(map[string]any)
	finding := ws["findings"].([]any)[0].(map[string]any)
	if run["state"] != "partial" || finding["origin"] != "rule" || finding["relation"] != "conflict" {
		t.Fatalf("HTTP failure erased a deterministic conflict: %v %v", run, finding)
	}
}

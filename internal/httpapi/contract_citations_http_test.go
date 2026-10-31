package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestContractCitationHTTPRefusesWithoutAnAgentRecord exercises the real HTTP
// surface: the route is registered, it requires a session, and an accepted
// frozen attempt that never persisted an agent projection is refused with a
// clear conflict instead of falling back to the latest text.
func TestContractCitationHTTPRefusesWithoutAnAgentRecord(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "冻结合同引用HTTP回归", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create case %d %v", status, created)
	}
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "procurement.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("accept review %d %v", status, review)
	}
	runID := review["id"].(string)
	status, workspace := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("read workspace %d %v", status, workspace)
	}
	attempts, _ := workspace["review_attempts"].(map[string]any)
	selected, _ := attempts["attempt"].(map[string]any)
	attemptID, _ := selected["id"].(string)
	if attemptID == "" || selected["history_kind"] != "frozen" {
		t.Fatalf("this regression needs a frozen attempt: %v", attempts)
	}
	base := "/api/cases/" + caseID + "/runs/" + runID + "/attempts/" + attemptID + "/contract-citations/"
	status, body := api.do(t, http.MethodGet, base+"fact_0123456789abcdef01234567", nil)
	if status != http.StatusConflict {
		t.Fatalf("attempt without an agent record answered %d %v", status, body)
	}
	errorBody, _ := body["error"].(map[string]any)
	message, _ := errorBody["message"].(string)
	if !strings.Contains(message, "智能体审查记录") {
		t.Fatalf("refusal did not explain the missing frozen agent record: %v", body)
	}

	for _, path := range []string{
		"/api/cases/other-case/runs/" + runID + "/attempts/" + attemptID + "/contract-citations/fact_0123456789abcdef01234567",
		"/api/cases/" + caseID + "/runs/other-run/attempts/" + attemptID + "/contract-citations/fact_0123456789abcdef01234567",
		"/api/cases/" + caseID + "/runs/" + runID + "/attempts/other-attempt/contract-citations/fact_0123456789abcdef01234567",
	} {
		status, body = api.do(t, http.MethodGet, path, nil)
		if status != http.StatusNotFound {
			t.Fatalf("citation reader accepted an identity outside its exact case/run/attempt: %s %d %v", path, status, body)
		}
	}

	anonymous := http.Client{}
	response, err := anonymous.Get(api.base + base + "fact_0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(string(raw), "unauthenticated") {
		t.Fatalf("anonymous citation read was not refused: HTTP %d body=%s err=%v", response.StatusCode, raw, readErr)
	}
}

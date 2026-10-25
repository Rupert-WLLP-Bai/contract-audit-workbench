package httpapi_test

import (
	"net/http"
	"sort"
	"testing"

	"contractaudit/internal/audit"
	"contractaudit/internal/engine"
)

func TestPaymentAmountAndSnapshot(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "付款与金额", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create %d %v", status, created)
	}
	caseID := created["id"].(string)
	docA := api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte(
		"采购文件要求质保36个月。采购方案约定20%预付款、验收通过后支付70%、质保期满10%。采购金额为120万元。",
	))
	docB := api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte(
		"拟签合同约定质保36个月。合同约定设备到货后支付90%。合同金额为100万元。",
	))

	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	runID := review["id"].(string)
	status, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("workspace %d %v", status, ws)
	}
	if ws["run"].(map[string]any)["state"] != "awaiting_human" {
		t.Fatalf("run %+v", ws["run"])
	}

	findings := ws["findings"].([]any)
	if len(findings) != 3 {
		t.Fatalf("findings %+v", findings)
	}
	var domains []string
	for _, item := range findings {
		finding := item.(map[string]any)
		domains = append(domains, finding["domain"].(string))
		if finding["origin"] != "rule" || finding["relation"] != "conflict" {
			t.Fatalf("finding %+v", finding)
		}
		for _, raw := range finding["evidence"].([]any) {
			anchor := raw.(map[string]any)
			if anchor["status"] != "located" || anchor["bbox"] != nil || anchor["text_offset"] == nil {
				t.Fatalf("anchor %+v", anchor)
			}
		}
	}
	if domains[0] != "payment_sum" || domains[1] != "payment_condition" || domains[2] != "amount" {
		t.Fatalf("domains %v", domains)
	}

	evals := ws["evaluations"].([]any)
	if len(evals) != 4 {
		t.Fatalf("evaluations %+v", evals)
	}
	if evals[0].(map[string]any)["domain"] != "warranty" || evals[0].(map[string]any)["relation"] != "compatible" {
		t.Fatalf("warranty eval %+v", evals[0])
	}
	if evals[0].(map[string]any)["finding_id"] != nil {
		t.Fatal("compatible warranty became a finding")
	}

	snap, _ := ws["snapshot"].(map[string]any)
	if snap == nil {
		t.Fatal("missing snapshot")
	}
	ids, _ := snap["document_ids"].([]any)
	gotIDs := make([]string, len(ids))
	for i, id := range ids {
		gotIDs[i] = id.(string)
	}
	wantIDs := []string{docA, docB}
	sort.Strings(wantIDs)
	if len(gotIDs) != 2 || gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("document ids %v want %v", gotIDs, wantIDs)
	}
	if snap["rule_set_hash"] != engine.RuleSetHash() || snap["playbook_hash"] != engine.PlaybookHash() {
		t.Fatalf("snapshot %+v", snap)
	}
	if snap["comparator_ref"] != engine.ComparatorRef || snap["model_ref"] != "script-model" {
		t.Fatalf("refs %+v", snap)
	}
	events := checkedAuditEvents(t, ws["audit_events"])
	if len(events) == 0 {
		t.Fatal("review creation did not append an audit root")
	}
	first := events[0]
	if first.EventType != "review_started" || first.Actor != "demo-admin" || first.RunID != runID {
		t.Fatalf("event %+v", first)
	}

	findingID := findings[0].(map[string]any)["id"].(string)
	status, _ = api.send(t, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+runID+"/findings/"+findingID+"/decisions", map[string]any{
		"action": "confirm_risk", "reason": "付款比例合计不同", "expected_version": 1,
	}, "pay-1")
	if status != http.StatusNoContent {
		t.Fatalf("decide %d", status)
	}
	_, ws = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	decision := requireAuditAppend(t, events, checkedAuditEvents(t, ws["audit_events"]), "human_decision")
	if decision.Actor != "demo-admin" || decision.RunID != runID {
		t.Fatalf("decision audit %+v", decision)
	}
	if ws["run"].(map[string]any)["state"] != "awaiting_human" {
		t.Fatalf("two findings remain %+v", ws["run"])
	}
}

func TestSnapshotIsStableAcrossRetry(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "快照不改写", "contract_type": "procurement_ict",
	})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	_, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	runID := review["id"].(string)
	_, before := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	snap := before["snapshot"].(map[string]any)
	hash := snap["rule_set_hash"]
	ids := snap["document_ids"]

	status, body := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+runID+"/retry", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("retry %d %v", status, body)
	}
	_, after := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	again := after["snapshot"].(map[string]any)
	if again["rule_set_hash"] != hash || again["playbook_hash"] != snap["playbook_hash"] || again["comparator_ref"] != snap["comparator_ref"] {
		t.Fatalf("snapshot changed %+v", again)
	}
	got := again["document_ids"].([]any)
	prev := ids.([]any)
	if len(got) != len(prev) || got[0] != prev[0] || got[1] != prev[1] {
		t.Fatalf("ids %v vs %v", got, prev)
	}
	retried := requireAuditAppend(t, checkedAuditEvents(t, before["audit_events"]), checkedAuditEvents(t, after["audit_events"]), "review_retried")
	if retried.Actor != "demo-admin" || retried.RunID != runID {
		t.Fatalf("retry audit %+v", retried)
	}
}

func checkedAuditEvents(t *testing.T, raw any) []audit.Event {
	t.Helper()
	rows := raw.([]any)
	events := make([]audit.Event, len(rows))
	for i, rawEvent := range rows {
		row := rawEvent.(map[string]any)
		correlation := []string{}
		for _, id := range row["correlation_ids"].([]any) {
			correlation = append(correlation, id.(string))
		}
		events[i] = audit.Event{
			EventID: row["event_id"].(string), RunID: row["run_id"].(string), Actor: row["actor"].(string),
			Seq: int(row["seq"].(float64)), EventType: row["event_type"].(string),
			PrevHash: row["prev_hash"].(string), Hash: row["hash"].(string), CorrelationIDs: correlation,
		}
	}
	if err := audit.Verify(events); err != nil {
		t.Fatalf("HTTP audit chain is invalid: %v", err)
	}
	return events
}

func requireAuditAppend(t *testing.T, before, after []audit.Event, eventType string) audit.Event {
	t.Helper()
	if len(after) < len(before) {
		t.Fatal("an audit action removed previous records")
	}
	for i, previous := range before {
		if after[i].Hash != previous.Hash {
			t.Fatalf("an audit action rewrote record %s", previous.EventID)
		}
	}
	for _, event := range after[len(before):] {
		if event.EventType == eventType {
			return event
		}
	}
	t.Fatalf("action did not append %s to the preserved audit chain", eventType)
	return audit.Event{}
}

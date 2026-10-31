package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

// This is an actual PostgreSQL/HTTP exercise. It intentionally uses the isolated
// TEST_DATABASE_URL profile and exercises the same client session and case
// authorization middleware as the running application.
func TestServiceObjectHTTPPostgresFrozenAssociationLifecycle(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL is required for service object HTTP integration")
	}
	st, err := store.OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open isolated service-object HTTP schema: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.UseServiceOwner("core"); err != nil {
		t.Fatal(err)
	}
	documentStore, err := st.Reopen()
	if err != nil {
		t.Fatalf("reopen isolated document service store: %v", err)
	}
	t.Cleanup(func() { _ = documentStore.Close() })
	if err := documentStore.UseServiceOwner("document"); err != nil {
		t.Fatal(err)
	}
	documentStore.UseModels(serviceObjectHTTPModels{})
	executionStore, err := st.Reopen()
	if err != nil {
		t.Fatalf("reopen isolated execution service store: %v", err)
	}
	t.Cleanup(func() { _ = executionStore.Close() })
	if err := executionStore.UseServiceOwner("execution"); err != nil {
		t.Fatal(err)
	}
	executionStore.UseModels(serviceObjectHTTPModels{})
	executionStore.UseDocumentPreparer(documentStore)
	operatorCatalog, err := capabilitypack.NewRegistry(capabilitypack.RegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	coreHandler := Handler(st, Options{OperatorAuditEnabled: true, CapabilityPacks: operatorCatalog,
		ExternalConnectors: map[string]auditext.ReadConnector{"fixture.oss": serviceObjectHTTPConnector{}}})
	documentHandler := Handler(documentStore, Options{})
	executionHandler := Handler(executionStore, Options{Durable: true})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/documents"):
			documentHandler.ServeHTTP(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/materials/") && strings.Contains(r.URL.Path, "/parsed-documents"):
			documentHandler.ServeHTTP(w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reviews"):
			executionHandler.ServeHTTP(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/reviews/"):
			executionHandler.ServeHTTP(w, r)
		default:
			coreHandler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := serviceObjectHTTPClient(t, server.URL)
	admin := client // Keep both client and admin audience cookies for diagnostics.
	serviceObjectHTTPLoginAudience(t, admin, server.URL, "demo-admin", "admin-demo", "admin")
	serviceObjectHTTPPublishRulePack(t, admin, server.URL)
	serviceObjectHTTPLogin(t, client, server.URL, "demo-admin", "admin-demo")

	status, createdCase := serviceObjectHTTPJSON(t, client, http.MethodPost, server.URL+"/api/cases", map[string]any{
		"title": "P3 服务对象 HTTP 冻结关联", "contract_type": "service", "business_line": "ENT", "scenario": "telecom_dedicated_line",
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("client Manage could not create case: HTTP %d: %v", status, createdCase)
	}
	caseID := serviceObjectHTTPString(t, createdCase, "id")
	content := []byte("拟签合同\n" +
		"电路 LINE-HTTP-001：最低保障带宽 100 Mbit/s。\n" +
		"电路 LINE-HTTP-001：SLA 可用率 99.9%（统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项）。\n" +
		"电路 LINE-HTTP-001：预付款比例 20%，预付款以合同总额为基础，含税。\n" +
		"电路 LINE-HTTP-001：合同总额 100000 元，含税。")
	documentID := serviceObjectHTTPUpload(t, client, server.URL, caseID, "draft_contract", "http-line.txt", content)
	procurementID := serviceObjectHTTPUpload(t, client, server.URL, caseID, "procurement", "http-purchase.txt", []byte("政企专线采购依据\n电路 LINE-HTTP-001：最低保障带宽 100 Mbit/s。\n电路 LINE-HTTP-001：SLA 可用率 99.9%（统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项）。\n电路 LINE-HTTP-001：合同总额 100000 元，含税。"))
	materialID, materialVersion := serviceObjectHTTPFindMaterial(t, client, server.URL, caseID, documentID)
	serviceObjectHTTPAssertParseReady(t, client, server.URL, caseID, materialID, materialVersion, documentID)
	procurementMaterialID, procurementVersion := serviceObjectHTTPFindMaterial(t, client, server.URL, caseID, procurementID)
	serviceObjectHTTPAssertParseReady(t, client, server.URL, caseID, procurementMaterialID, procurementVersion, procurementID)
	adminUser, _ := accounts.ByID("demo-admin")
	for _, item := range []struct {
		materialID string
		version    string
	}{{materialID, materialVersion}, {procurementMaterialID, procurementVersion}} {
		versionNumber, err := strconv.Atoi(item.version)
		if err != nil {
			t.Fatal(err)
		}
		parses, err := executionStore.ListMaterialVersionParses(context.Background(), adminUser, caseID, item.materialID, versionNumber)
		if err != nil || len(parses.Items) == 0 || parses.Items[0].Status != "ready" {
			t.Fatalf("execution owner cannot read document owner's pinned ready parse before acceptance: material=%s version=%d parses=%+v err=%v", item.materialID, versionNumber, parses, err)
		}
	}
	base := server.URL + "/api/cases/" + caseID + "/service-objects"
	status, pending := serviceObjectHTTPJSON(t, client, http.MethodPost, base, map[string]any{
		"object_type": "dedicated_line", "display_name": "专线 LINE-HTTP-001", "business_code": "LINE-HTTP-001",
		"aliases": []map[string]string{{"kind": "circuit", "value": "LINE-HTTP-001"}},
	}, "http-object-create")
	if status != http.StatusCreated || pending["version"] != float64(1) || pending["association_version"] != float64(0) {
		t.Fatalf("case manager did not create a pending identity: HTTP %d: %v", status, pending)
	}
	objectID := serviceObjectHTTPString(t, pending, "service_object_id")

	status, review := serviceObjectHTTPJSON(t, client, http.MethodPost, server.URL+"/api/cases/"+caseID+"/reviews", map[string]any{
		"business_at": "2026-10-31T10:00:00Z",
	}, "")
	if status != http.StatusAccepted {
		t.Fatalf("accept a durable frozen review attempt: HTTP %d: %v", status, review)
	}
	runID := serviceObjectHTTPString(t, review, "id")
	attemptID := serviceObjectHTTPString(t, review, "attempt_id")
	jobID := serviceObjectHTTPString(t, review, "job_id")
	claim, err := executionStore.ClaimNextJob(context.Background(), "service-object-http-worker", time.Minute)
	if err != nil || claim.JobID != jobID || claim.RunID != runID || claim.AttemptID != attemptID {
		t.Fatalf("claim accepted review job through Execution owner: claim=%+v err=%v", claim, err)
	}
	if err := executionStore.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("run accepted review attempt with its durable execution claim: %v", err)
	}
	workspaceURL := server.URL + "/api/cases/" + caseID + "/reviews/" + runID + "?attempt_id=" + url.QueryEscape(attemptID)
	deadline := time.Now().Add(15 * time.Second)
	for {
		status, workspace := serviceObjectHTTPJSON(t, client, http.MethodGet, workspaceURL, nil, "")
		if status != http.StatusOK {
			t.Fatalf("read review execution status: HTTP %d: %v", status, workspace)
		}
		run, ok := workspace["run"].(map[string]any)
		if !ok {
			t.Fatalf("review workspace omitted run status: %v", workspace)
		}
		if run["state"] == "failed" {
			serviceObjectHTTPLoginAudience(t, admin, server.URL, "demo-admin", "admin-demo", "admin")
			diagnosticURL := server.URL + "/api/cases/" + caseID + "/reviews/" + runID + "/diagnostics?attempt_id=" + url.QueryEscape(attemptID)
			diagnosticStatus, diagnostics := serviceObjectHTTPJSON(t, admin, http.MethodGet, diagnosticURL, nil, "")
			t.Fatalf("review attempt failed before producing candidate evidence: run=%v diagnostics_http=%d diagnostics=%v", run, diagnosticStatus, diagnostics)
		}
		attempts, _ := workspace["review_attempts"].(map[string]any)
		attempt, _ := attempts["attempt"].(map[string]any)
		attemptState, _ := attempt["state"].(string)
		if (run["state"] == "completed" || run["state"] == "partial" || run["state"] == "awaiting_human") &&
			(attemptState == "completed" || attemptState == "partial") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("review attempt did not form final evidence within 15s: %v", run)
		}
		time.Sleep(100 * time.Millisecond)
	}
	parsesURL := server.URL + "/api/cases/" + caseID + "/materials/" + materialID + "/versions/" + materialVersion + "/parsed-documents"
	status, parseList := serviceObjectHTTPJSON(t, client, http.MethodGet, parsesURL, nil, "")
	if status != http.StatusOK {
		t.Fatalf("list pinned material parses: HTTP %d: %v", status, parseList)
	}
	parseItems, ok := parseList["items"].([]any)
	if !ok || len(parseItems) == 0 {
		t.Fatalf("material has no parse to pin: %v", parseList)
	}
	parsedDocumentID := serviceObjectHTTPString(t, parseItems[0].(map[string]any), "parsed_document_id")

	// The HTTP case owner created the pending identity first; continue through
	// the Core-owned P3 routes using the real persisted Execution review attempt.
	query := url.Values{"run_id": {runID}, "attempt_id": {attemptID}, "material_id": {materialID}, "material_version": {materialVersion}, "document_id": {documentID}, "parsed_document_id": {parsedDocumentID}}
	candidatesURL := base + "/" + objectID + "/association-candidates?" + query.Encode()
	status, preview := serviceObjectHTTPJSON(t, client, http.MethodGet, candidatesURL, nil, "")
	if status != http.StatusOK {
		t.Fatalf("read candidates from the frozen attempt: HTTP %d: %v", status, preview)
	}
	options, ok := preview["options"].([]any)
	if !ok || len(options) == 0 {
		t.Fatalf("frozen contract did not yield object candidates: %v", preview)
	}
	option := options[0].(map[string]any)
	versionNumber, err := strconv.Atoi(materialVersion)
	if err != nil {
		t.Fatal(err)
	}
	confirm := map[string]any{
		"confirmed": true, "expected_object_version": preview["expected_object_version"],
		"expected_association_version": preview["expected_association_version"],
		"source": map[string]any{"run_id": runID, "attempt_id": attemptID, "material_id": materialID,
			"material_version": versionNumber, "document_id": documentID, "parsed_document_id": parsedDocumentID},
		"object_ref": option["object_ref"], "facts_sha256": option["facts_sha256"], "candidate_sha256": preview["candidate_sha256"],
	}
	confirmURL := base + "/" + objectID + "/associations"
	falseConfirm := cloneServiceObjectHTTPMap(confirm)
	falseConfirm["confirmed"] = false
	status, body := serviceObjectHTTPJSON(t, client, http.MethodPost, confirmURL, falseConfirm, "http-assoc-false")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("confirmed=false must be rejected: HTTP %d: %v", status, body)
	}
	firstBody := serviceObjectHTTPRawJSON(t, client, confirmURL, confirm, "http-assoc-first")
	var receipt map[string]any
	if err := json.Unmarshal(firstBody, &receipt); err != nil || receipt["association_version"] != float64(1) || receipt["version"] != float64(2) {
		t.Fatalf("association did not atomically confirm the pending identity: body=%s err=%v", firstBody, err)
	}
	replayBody := serviceObjectHTTPRawJSON(t, client, confirmURL, confirm, "http-assoc-first")
	if !bytes.Equal(firstBody, replayBody) {
		t.Fatalf("same-key HTTP retry changed the immutable receipt: first=%s replay=%s", firstBody, replayBody)
	}
	changed := cloneServiceObjectHTTPMap(confirm)
	changedSource := changed["source"].(map[string]any)
	changedSource["material_version"] = 2
	status, conflict := serviceObjectHTTPJSON(t, client, http.MethodPost, confirmURL, changed, "http-assoc-first")
	if status != http.StatusConflict {
		t.Fatalf("same key with changed source tuple must be 409: HTTP %d: %v", status, conflict)
	}

	// Append a second association to prove that the history route is bounded and
	// its cursor returns a real second page.
	status, secondPreview := serviceObjectHTTPJSON(t, client, http.MethodGet, candidatesURL, nil, "")
	if status != http.StatusOK {
		t.Fatalf("refresh candidates before association append: HTTP %d: %v", status, secondPreview)
	}
	secondOption := secondPreview["options"].([]any)[0].(map[string]any)
	secondConfirm := map[string]any{
		"confirmed": true, "expected_object_version": secondPreview["expected_object_version"],
		"expected_association_version": secondPreview["expected_association_version"],
		"source":                       confirm["source"], "object_ref": secondOption["object_ref"],
		"facts_sha256": secondOption["facts_sha256"], "candidate_sha256": secondPreview["candidate_sha256"],
	}
	status, secondReceipt := serviceObjectHTTPJSON(t, client, http.MethodPost, confirmURL, secondConfirm, "http-assoc-second")
	if status != http.StatusCreated || secondReceipt["association_version"] != float64(2) {
		t.Fatalf("append second association: HTTP %d: %v", status, secondReceipt)
	}
	status, versions := serviceObjectHTTPJSON(t, client, http.MethodGet, base+"/"+objectID+"/versions?limit=1", nil, "")
	if status != http.StatusOK || versions["next_cursor"] == nil {
		t.Fatalf("identity history did not paginate: HTTP %d: %v", status, versions)
	}
	cursor := url.QueryEscape(versions["next_cursor"].(string))
	status, olderVersion := serviceObjectHTTPJSON(t, client, http.MethodGet, base+"/"+objectID+"/versions?limit=1&cursor="+cursor, nil, "")
	if status != http.StatusOK || len(olderVersion["items"].([]any)) != 1 {
		t.Fatalf("identity second page failed: HTTP %d: %v", status, olderVersion)
	}
	status, versionOne := serviceObjectHTTPJSON(t, client, http.MethodGet, base+"/"+objectID+"/versions/1", nil, "")
	if status != http.StatusOK || versionOne["version"] != float64(1) {
		t.Fatalf("exact identity version read failed: HTTP %d: %v", status, versionOne)
	}
	status, associations := serviceObjectHTTPJSON(t, client, http.MethodGet, base+"/"+objectID+"/associations?limit=1", nil, "")
	if status != http.StatusOK || associations["next_cursor"] == nil {
		t.Fatalf("association history did not paginate: HTTP %d: %v", status, associations)
	}
	status, exactAssociation := serviceObjectHTTPJSON(t, client, http.MethodGet, base+"/"+objectID+"/associations/1", nil, "")
	if status != http.StatusOK || exactAssociation["association_version"] != float64(1) {
		t.Fatalf("exact association read failed: HTTP %d: %v", status, exactAssociation)
	}

	// The legacy operator mapping POST may create a new mapping only when it
	// pins the current service identity and its current confirmed association.
	status, mapping := serviceObjectHTTPJSON(t, client, http.MethodPost, server.URL+"/api/cases/"+caseID+"/object-bindings", map[string]any{
		"contract_object":   map[string]any{"object_type": "service", "object_id": option["object_ref"], "label": "专线 LINE-HTTP-001"},
		"external_object":   map[string]any{"connector_id": "fixture.oss", "external_object_id": "fixture-line-http-001"},
		"service_object_id": objectID, "expected_object_version": 2, "expected_association_version": 2,
		"effective_from": "2026-10-30T00:00:00Z", "effective_until": "2027-10-30T00:00:00Z", "confirmed": true,
	}, "http-mapping-create")
	if status != http.StatusCreated || mapping["mapping_version"] != float64(1) || mapping["service_object_id"] != objectID {
		t.Fatalf("create mapping pinned to current service identity: HTTP %d: %v", status, mapping)
	}
	mappingID := serviceObjectHTTPString(t, mapping, "mapping_id")
	collectionURL := server.URL + "/api/cases/" + caseID + "/external-collection-jobs"
	collectionRequest := map[string]any{"object_binding_version": mapping["version"],
		"connector_id": "fixture.oss", "external_object_id": "fixture-line-http-001",
		"business_at": "2026-10-31T10:00:00Z"}
	collectionRaw := serviceObjectHTTPRawJSON(t, client, collectionURL, collectionRequest, "http-collection-1")
	var collectionReceipt map[string]any
	if err := json.Unmarshal(collectionRaw, &collectionReceipt); err != nil {
		t.Fatalf("decode HTTP collection receipt: %v", err)
	}
	collectionID := serviceObjectHTTPString(t, collectionReceipt, "collection_id")
	if collectionReceipt["job_id"] != collectionID || collectionReceipt["state"] != "available" {
		t.Fatalf("collection response omitted stable public ID or completed state: %s", collectionRaw)
	}
	collectionRetryRaw := serviceObjectHTTPRawJSON(t, client, collectionURL, collectionRequest, "http-collection-1")
	if !bytes.Equal(collectionRaw, collectionRetryRaw) {
		t.Fatalf("same-key HTTP collection retry changed exact response bytes: first=%s replay=%s", collectionRaw, collectionRetryRaw)
	}
	collectionReadURL := server.URL + "/api/cases/" + caseID + "/external-collection-jobs/" + collectionID
	status, recoveredCollection := serviceObjectHTTPJSON(t, client, http.MethodGet, collectionReadURL, nil, "")
	if status != http.StatusOK || recoveredCollection["collection_id"] != collectionID || recoveredCollection["state"] != "completed" {
		t.Fatalf("case reader could not recover completed collection by stable ID: HTTP %d: %v", status, recoveredCollection)
	}
	recoveredSnapshot, _ := recoveredCollection["snapshot_set"].(map[string]any)
	if recoveredSnapshot["snapshot_set_id"] != collectionReceipt["snapshot_set_id"] {
		t.Fatalf("collection recovery changed the durable snapshot identity: response=%v recovered=%v", collectionReceipt, recoveredCollection)
	}
	quote := []rune(string(content))
	quoteDigest := sha256.Sum256([]byte(string(quote)))
	basisRefs := []map[string]any{{"kind": "material_anchor", "material_id": materialID, "material_version": versionNumber,
		"document_id": documentID, "parsed_document_id": parsedDocumentID, "start": 0, "end": len(quote),
		"quote_digest": hex.EncodeToString(quoteDigest[:])}}
	cas := map[string]any{"expected_mapping_version": mapping["mapping_version"], "expected_binding_version": mapping["version"],
		"expected_object_version": mapping["object_version"], "expected_association_version": mapping["association_version"]}
	patch := map[string]any{"kind": "replace_mapping", "external_object": map[string]any{"connector_id": "fixture.oss", "external_object_id": "fixture-line-http-001-new"},
		"effective_from": "2026-10-30T00:00:00Z", "effective_until": "2027-10-30T00:00:00Z"}
	proposalBase := server.URL + "/api/cases/" + caseID + "/object-bindings/" + mappingID + "/changes"
	previewBody := map[string]any{"cas": cas, "patch": patch, "basis_refs": basisRefs}
	status, mappingPreview := serviceObjectHTTPJSON(t, client, http.MethodPost, proposalBase+"/preview", previewBody, "")
	if status != http.StatusOK {
		t.Fatalf("preview independent mapping correction: HTTP %d: %v", status, mappingPreview)
	}
	proposalBody := map[string]any{"cas": cas, "patch": patch, "basis_refs": basisRefs, "impact_sha256": mappingPreview["impact_sha256"]}
	proposalRaw := serviceObjectHTTPRawJSON(t, client, proposalBase, proposalBody, "http-mapping-proposal")
	proposalReplayRaw := serviceObjectHTTPRawJSON(t, client, proposalBase, proposalBody, "http-mapping-proposal")
	if !bytes.Equal(proposalRaw, proposalReplayRaw) {
		t.Fatalf("mapping proposal retry changed response bytes: first=%s replay=%s", proposalRaw, proposalReplayRaw)
	}
	var proposal map[string]any
	if err := json.Unmarshal(proposalRaw, &proposal); err != nil || proposal["state"] != "pending_decision" {
		t.Fatalf("mapping proposal response invalid: %s err=%v", proposalRaw, err)
	}
	requestID := serviceObjectHTTPString(t, proposal, "request_id")
	decisionBody := map[string]any{"decision": "approve", "expected_mapping_version": cas["expected_mapping_version"],
		"expected_binding_version": cas["expected_binding_version"], "expected_object_version": cas["expected_object_version"],
		"expected_association_version": cas["expected_association_version"], "expected_after_sha256": proposal["after_sha256"],
		"expected_impact_sha256": proposal["impact_sha256"], "reason": "根据冻结合同原文修正映射"}
	decisionURL := server.URL + "/api/cases/" + caseID + "/object-binding-changes/" + requestID + "/decision"
	status, selfDecision := serviceObjectHTTPJSON(t, client, http.MethodPost, decisionURL, decisionBody, "http-mapping-self-decision")
	if status != http.StatusForbidden {
		t.Fatalf("mapping proposer self-approval was not blocked: HTTP %d: %v", status, selfDecision)
	}
	adminForGrant, _ := accounts.ByID("demo-admin")
	reviewerForGrant, _ := accounts.ByID("demo-reviewer")
	if err := st.AddMember(adminForGrant, caseID, reviewerForGrant.ID); err != nil {
		t.Fatalf("grant case decision access to independent reviewer: %v", err)
	}
	reviewerClient := serviceObjectHTTPClient(t, server.URL)
	serviceObjectHTTPLogin(t, reviewerClient, server.URL, "demo-reviewer", "reviewer-demo")
	decisionRaw := serviceObjectHTTPRawJSON(t, reviewerClient, decisionURL, decisionBody, "http-mapping-decision")
	decisionReplayRaw := serviceObjectHTTPRawJSON(t, reviewerClient, decisionURL, decisionBody, "http-mapping-decision")
	if !bytes.Equal(decisionRaw, decisionReplayRaw) {
		t.Fatalf("mapping decision retry changed response bytes: first=%s replay=%s", decisionRaw, decisionReplayRaw)
	}
	status, mappingHistory := serviceObjectHTTPJSON(t, reviewerClient, http.MethodGet,
		server.URL+"/api/cases/"+caseID+"/object-binding-changes/"+requestID, nil, "")
	if status != http.StatusOK || mappingHistory["decision"] == nil {
		t.Fatalf("independent mapping decision not available in HTTP history: HTTP %d: %v", status, mappingHistory)
	}

	// A non-member sees no existence information; the API intentionally returns
	// 404 for an inaccessible case.
	viewer := serviceObjectHTTPClient(t, server.URL)
	serviceObjectHTTPLogin(t, viewer, server.URL, "demo-viewer", "viewer-demo")
	status, denied := serviceObjectHTTPJSON(t, viewer, http.MethodGet, base, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("unauthorized case did not hide service objects: HTTP %d: %v", status, denied)
	}
	status, denied = serviceObjectHTTPJSON(t, viewer, http.MethodGet, collectionReadURL, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("unauthorized case did not hide collection recovery state: HTTP %d: %v", status, denied)
	}
	status, denied = serviceObjectHTTPJSON(t, viewer, http.MethodGet, base+"/"+objectID, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("unauthorized case did not hide exact service-object identity: HTTP %d: %v", status, denied)
	}
	status, denied = serviceObjectHTTPJSON(t, viewer, http.MethodPost, base, map[string]any{
		"object_type": "service", "display_name": "无权创建", "aliases": []map[string]string{},
	}, "http-viewer-create")
	if status != http.StatusNotFound {
		t.Fatalf("unauthorized viewer created a service object: HTTP %d: %v", status, denied)
	}
	status, beforeReadonly := serviceObjectHTTPJSON(t, client, http.MethodGet, base, nil, "")
	beforeItems, beforeItemsOK := beforeReadonly["items"].([]any)
	if status != http.StatusOK || !beforeItemsOK || len(beforeItems) != 1 {
		t.Fatalf("read current object rows before read-only check: HTTP %d: %v", status, beforeReadonly)
	}
	st.UseReadOnly(true)
	status, readonly := serviceObjectHTTPJSON(t, client, http.MethodPost, base, map[string]any{
		"object_type": "service", "display_name": "只读下不应创建", "aliases": []map[string]string{},
	}, "http-readonly")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("read-only mode accepted a service-object write: HTTP %d: %v", status, readonly)
	}
	status, afterReadonly := serviceObjectHTTPJSON(t, client, http.MethodGet, base, nil, "")
	afterItems, afterItemsOK := afterReadonly["items"].([]any)
	if status != http.StatusOK || !afterItemsOK || len(afterItems) != len(beforeItems) {
		t.Fatalf("read-only rejection changed durable object rows: before=%v after HTTP %d=%v", beforeReadonly, status, afterReadonly)
	}
	t.Logf("PostgreSQL service-object HTTP lifecycle committed and reread: case=%s run=%s attempt=%s object=%s identity_versions=2 associations=2", caseID, runID, attemptID, objectID)
}

type serviceObjectHTTPModels struct{}

type serviceObjectHTTPConnector struct{}

func (serviceObjectHTTPConnector) ID() string { return "fixture.oss" }
func (serviceObjectHTTPConnector) Read(_ context.Context, request auditext.ReadRequest) (auditext.Observation, error) {
	payload := "fixture:operator-collection-http"
	businessAt := request.BusinessAt.UTC().Truncate(time.Microsecond)
	return auditext.Observation{
		SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: "snapshot-service-object-http",
		TenantID: request.Scope.TenantID, CaseID: request.Scope.CaseID, ConnectorID: request.ConnectorID,
		SourceSystem: "fixture-oss", ObjectBindingVersion: request.BindingVersion,
		ExternalObjectID: request.ExternalObjectID, RecordID: "fixture-line-http-001",
		ObservedAt: businessAt, EffectiveFrom: businessAt.Add(-24 * time.Hour), EffectiveUntil: businessAt.Add(24 * time.Hour),
		Availability: auditext.AvailabilityAvailable, ReasonCode: "read_succeeded", PayloadRef: &payload,
		Fields: []auditext.Field{{Name: "configured_bandwidth", Value: "100", Unit: "Mbit/s",
			SourcePointer: "/serviceCharacteristic/0/value", SemanticKind: "configured_value"}},
		DataClass: auditext.DataClassSynthetic,
	}, nil
}

func (serviceObjectHTTPModels) Name() string            { return "service-object-http-test" }
func (serviceObjectHTTPModels) ConfigurationID() string { return "service-object-http-test" }
func (serviceObjectHTTPModels) Warranty(_ context.Context, _ []model.Material) (model.Warranty, error) {
	return model.Warranty{}, nil
}
func (serviceObjectHTTPModels) Transcribe(_ context.Context, _ string, content []byte) (string, error) {
	return string(content), nil
}

func serviceObjectHTTPClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func serviceObjectHTTPLogin(t *testing.T, client *http.Client, base, username, password string) {
	t.Helper()
	status, payload := serviceObjectHTTPJSON(t, client, http.MethodPost, base+"/api/session", map[string]any{"username": username, "password": password}, "")
	if status != http.StatusOK || payload["actor"] == nil {
		t.Fatalf("client login %s failed: HTTP %d: %v", username, status, payload)
	}
}

func serviceObjectHTTPLoginAudience(t *testing.T, client *http.Client, base, username, password, audience string) {
	t.Helper()
	status, payload := serviceObjectHTTPJSON(t, client, http.MethodPost, base+"/api/session", map[string]any{
		"username": username, "password": password, "audience": audience,
	}, "")
	if status != http.StatusOK || payload["actor"] == nil {
		t.Fatalf("login %s with audience %s failed: HTTP %d: %v", username, audience, status, payload)
	}
}

func serviceObjectHTTPPublishRulePack(t *testing.T, client *http.Client, base string) {
	t.Helper()
	manifest := map[string]any{
		"schema_version": "rulepack/v1", "id": "telecom-p3-http", "version": "1", "tenant_id": "local-demo",
		"effective_from": "2020-01-01T00:00:00Z", "effective_until": "2100-01-01T00:00:00Z",
		"rules": []any{
			map[string]any{
				"rule_id": "ENT-TECH-BW-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.minimum_bandwidth", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      "local-demo", "comparator": "minimum_bandwidth",
				"threshold":         map[string]any{"value": "100", "unit": "Mbit/s"},
				"required_fact":     map[string]any{"dimension": "minimum_bandwidth", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "bandwidth_decimal"},
				"authority_sources": []any{map[string]any{"id": "p3-http-telecom-policy", "version": "1", "kind": "synthetic_demo"}},
			},
			map[string]any{
				"rule_id": "ENT-TECH-SLA-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.monthly_availability", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      "local-demo", "comparator": "monthly_sla",
				"threshold": map[string]any{"value": "99.9", "unit": "percent"},
				"required_fact": map[string]any{"dimension": "monthly_availability", "source_roles": []string{"draft_contract"}, "object_required": true,
					"unit_family": "percentage", "measurement_window": "calendar_month", "time_zone": "Asia/Shanghai", "calendar": "gregorian",
					"outage_definition": "all_service_unavailability", "exclusions": []string{}},
				"authority_sources": []any{map[string]any{"id": "p3-http-telecom-policy", "version": "1", "kind": "synthetic_demo"}},
			},
			map[string]any{
				"rule_id": "ENT-COM-ADV-001", "version": "1", "domain": "commercial",
				"capability_id": "payment.advance_ratio.ceiling", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      "local-demo", "comparator": "advance_payment_ceiling",
				"threshold":         map[string]any{"value": "1/3", "unit": "ratio", "basis": "contract_total"},
				"required_fact":     map[string]any{"dimension": "advance_payment_ratio", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "ratio", "basis": "contract_total"},
				"authority_sources": []any{map[string]any{"id": "p3-http-telecom-policy", "version": "1", "kind": "synthetic_demo"}},
			},
		},
	}
	status, created := serviceObjectHTTPJSON(t, client, http.MethodPost, base+"/api/admin/rulepacks", map[string]any{"manifest": manifest}, "")
	if status != http.StatusCreated {
		t.Fatalf("create P3 HTTP synthetic rule pack: HTTP %d: %v", status, created)
	}
	pack, ok := created["pack"].(map[string]any)
	if !ok {
		t.Fatalf("created P3 HTTP rule pack omitted summary: %v", created)
	}
	versionPath := base + "/api/admin/rulepacks/telecom-p3-http/versions/1"
	status, validated := serviceObjectHTTPJSON(t, client, http.MethodPost, versionPath+"/validate", map[string]any{"expected_revision": pack["revision"]}, "")
	if status != http.StatusOK {
		t.Fatalf("validate P3 HTTP synthetic rule pack: HTTP %d: %v", status, validated)
	}
	pack = validated["pack"].(map[string]any)
	status, approved := serviceObjectHTTPJSON(t, client, http.MethodPost, versionPath+"/approve", map[string]any{
		"expected_revision": pack["revision"], "expected_content_hash": pack["content_hash"],
	}, "")
	if status != http.StatusOK {
		t.Fatalf("approve P3 HTTP synthetic rule pack: HTTP %d: %v", status, approved)
	}
	pack = approved["pack"].(map[string]any)
	status, published := serviceObjectHTTPJSON(t, client, http.MethodPost, versionPath+"/publish", map[string]any{
		"expected_revision": pack["revision"], "expected_pointer_revision": pack["active_pointer_revision"],
	}, "")
	if status != http.StatusOK {
		t.Fatalf("publish P3 HTTP synthetic rule pack: HTTP %d: %v", status, published)
	}
	pack = published["pack"].(map[string]any)
	var bodyText string
	var ruleIDs []string
	var spans []any
	for _, rule := range []struct{ id, quote, suffix string }{
		{"ENT-TECH-BW-001", "带宽不低于100 Mbit/s", ""},
		{"ENT-TECH-SLA-001", "月可用率不低于99.9%", "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项"},
		{"ENT-COM-ADV-001", "预付款比例不超过1/3", "，预付款以合同总额为基础"},
	} {
		start := len([]rune(bodyText))
		bodyText += rule.quote + rule.suffix + "。"
		ruleIDs = append(ruleIDs, rule.id)
		spans = append(spans, map[string]any{"rule_id": rule.id, "start": start, "end": start + len([]rune(rule.quote)), "quote": rule.quote})
	}
	content := map[string]any{
		"knowledge_id": "p3-http-telecom-policy", "version": "1", "kind": "policy", "title": "专线HTTP集成合成治理制度",
		"body": bodyText, "summary": "核验专线带宽、可用率和预付款比例", "source_type": "synthetic_policy", "source_reference": "P3 HTTP集成合成制度",
		"scope":      map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
		"valid_from": "2020-01-01T00:00:00Z", "valid_until": "2100-01-01T00:00:00Z",
		"binding": map[string]any{"pack_id": "telecom-p3-http", "pack_version": "1", "pack_content_hash": pack["content_hash"],
			"pack_snapshot_digest": pack["snapshot_sha256"], "compiler_ref": "governed-rulepack/v1", "rule_ids": ruleIDs, "source_spans": spans,
			"candidate_diff": "P3 HTTP合成规则审核", "validation_evidence": "P3 HTTP测试已核对合成依据"},
	}
	governedBase := base + "/api/admin/governed-knowledge"
	status, knowledge := serviceObjectHTTPJSON(t, client, http.MethodPost, governedBase, map[string]any{
		"content": content, "idempotency_key": "p3-http-policy-create",
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("create P3 HTTP synthetic governed policy: HTTP %d: %v", status, knowledge)
	}
	ref := knowledge["ref"].(map[string]any)
	commandPath := governedBase + "/p3-http-telecom-policy/versions/" + serviceObjectHTTPString(t, ref, "version_id") + "/commands"
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		status, knowledge = serviceObjectHTTPJSON(t, client, http.MethodPost, commandPath, map[string]any{
			"action": action, "expected_revision": knowledge["revision"], "expected_content_sha256": ref["content_sha256"],
			"expected_pointer_revision": knowledge["pointer_revision"], "reason": "P3合成集成测试",
			"idempotency_key": "p3-http-policy-" + action,
		}, "")
		if status != http.StatusOK {
			t.Fatalf("%s P3 HTTP synthetic governed policy: HTTP %d: %v", action, status, knowledge)
		}
	}
}

func serviceObjectHTTPJSON(t *testing.T, client *http.Client, method, target string, payload any, idempotency string) (int, map[string]any) {
	t.Helper()
	body := []byte{}
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = encoded
	}
	request, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("HTTP %d returned invalid JSON %s: %v", response.StatusCode, raw, err)
		}
	}
	return response.StatusCode, decoded
}

func serviceObjectHTTPRawJSON(t *testing.T, client *http.Client, target string, payload any, idempotency string) []byte {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotency)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("HTTP mutation returned %d: %s", response.StatusCode, raw)
	}
	return raw
}

func serviceObjectHTTPUpload(t *testing.T, client *http.Client, base, caseID, role, name string, content []byte) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="` + name + `"`},
		"Content-Type":        {"text/plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("role", role); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, base+"/api/cases/"+caseID+"/documents", &buffer)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("HTTP document upload returned %d: result=%v err=%v", response.StatusCode, result, err)
	}
	return serviceObjectHTTPString(t, result, "id")
}

func serviceObjectHTTPFindMaterial(t *testing.T, client *http.Client, base, caseID, documentID string) (string, string) {
	t.Helper()
	status, detail := serviceObjectHTTPJSON(t, client, http.MethodGet, base+"/api/cases/"+caseID, nil, "")
	if status != http.StatusOK {
		t.Fatalf("read case materials: HTTP %d: %v", status, detail)
	}
	materials, ok := detail["materials"].([]any)
	if !ok {
		t.Fatalf("case detail omitted materials: %v", detail)
	}
	for _, raw := range materials {
		item := raw.(map[string]any)
		if item["current_document_id"] == documentID {
			version, ok := item["current_version"].(float64)
			if !ok || version < 1 {
				t.Fatalf("case detail has invalid current material version: %v", item)
			}
			return serviceObjectHTTPString(t, item, "material_id"), strconv.Itoa(int(version))
		}
	}
	t.Fatalf("material version for document %q missing: %v", documentID, materials)
	return "", ""
}

func serviceObjectHTTPAssertParseReady(t *testing.T, client *http.Client, base, caseID, materialID, version, documentID string) {
	t.Helper()
	parsesURL := base + "/api/cases/" + caseID + "/materials/" + materialID + "/versions/" + version + "/parsed-documents"
	status, payload := serviceObjectHTTPJSON(t, client, http.MethodGet, parsesURL, nil, "")
	if status != http.StatusOK {
		t.Fatalf("read uploaded document parse before review: HTTP %d: %v", status, payload)
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("uploaded document %s has no persisted parse selection before review: %v", documentID, payload)
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && item["status"] == "ready" {
			return
		}
	}
	t.Fatalf("uploaded document %s has no ready parse before review: %v", documentID, payload)
}

func serviceObjectHTTPString(t *testing.T, payload map[string]any, key string) string {
	t.Helper()
	value, ok := payload[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		t.Fatalf("response omitted %q string field: %v", key, payload)
	}
	return value
}

func cloneServiceObjectHTTPMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(encoded, &clone)
	return clone
}

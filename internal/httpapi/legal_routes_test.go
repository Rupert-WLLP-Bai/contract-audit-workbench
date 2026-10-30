package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/legalnorm"
	"contractaudit/internal/store"
)

func TestLegalSearchFilterParsesBoundedFilters(t *testing.T) {
	request := httptest.NewRequest("GET", "/?q=%20民法典%20&source_id=CN-CIVIL-CODE&article_number=496&business_at=2025-06-01T00%3A00%3A00%2B08%3A00&cursor=abc&limit=25", nil)
	filter, err := legalSearchFilter(request)
	if err != nil {
		t.Fatalf("parse valid legal search: %v", err)
	}
	if filter.Query != "民法典" || filter.SourceID != "CN-CIVIL-CODE" || filter.ArticleNumber != 496 ||
		filter.BusinessAt != "2025-06-01T00:00:00+08:00" || filter.Cursor != "abc" || filter.Limit != 25 {
		t.Fatalf("unexpected filter: %+v", filter)
	}
}

func TestLegalSourceSnapshotPathBindsSourceAndExactCapture(t *testing.T) {
	capture := store.LegalSourceCapture{SourceID: "CN-CIVIL-CODE", CaptureSnapshotID: "lsc_exact_01"}
	if !legalSourceIdentityMatches("CN-CIVIL-CODE", capture) {
		t.Fatal("exact source id should bind to its captured snapshot")
	}
	for _, wrong := range []string{"", "CN-TENDER-LAW"} {
		if legalSourceIdentityMatches(wrong, capture) {
			t.Fatalf("source path %q was accepted for another source capture", wrong)
		}
	}
}

func TestLegalApplicabilityRequestBindsDimensionAndHeaderIdempotencyKey(t *testing.T) {
	input := store.LegalApplicabilityFactInput{Dimension: "buyer_kind", IdempotencyKey: "fact-write-1"}
	if err := bindLegalApplicabilityRequest("buyer_kind", "fact-write-1", &input); err != nil {
		t.Fatalf("matching body/path/header should bind: %v", err)
	}
	if input.Dimension != "buyer_kind" || input.IdempotencyKey != "fact-write-1" {
		t.Fatalf("bound request changed its identities: %+v", input)
	}
	for _, test := range []struct {
		name      string
		dimension string
		header    string
		bodyKey   string
	}{
		{name: "missing authoritative header", dimension: "buyer_kind", bodyKey: "fact-write-1"},
		{name: "body key cannot override header", dimension: "buyer_kind", header: "header-key", bodyKey: "body-key"},
		{name: "path cannot override body dimension", dimension: "seller_kind", header: "fact-write-1", bodyKey: "fact-write-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := store.LegalApplicabilityFactInput{Dimension: "buyer_kind", IdempotencyKey: test.bodyKey}
			if test.name == "path cannot override body dimension" {
				request.IdempotencyKey = test.header
			}
			if err := bindLegalApplicabilityRequest(test.dimension, test.header, &request); err == nil {
				t.Fatal("invalid route/body idempotency binding was accepted")
			}
		})
	}
}

func TestLegalApplicabilityHTTPRecordReplayAndResolve(t *testing.T) {
	st, err := store.OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open isolated legal applicability API test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server := httptest.NewServer(Handler(st, Options{}))
	t.Cleanup(server.Close)
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCase(admin, "法律适用事实HTTP验收", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	client := legalRouteLogin(t, server.URL, "demo-admin", "admin-demo", "client")
	requestBody := `{"expected_version":0,"dimension":"transaction_kind","status":"unknown","unknown_reason":"insufficient_evidence","temporal_basis":"current_state","basis_note":"等待案件材料补充核验","basis_refs":[]}`
	writeURL := server.URL + "/api/cases/" + caseID + "/legal-applicability-facts/transaction_kind"
	request := func() []byte {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, writeURL, strings.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Idempotency-Key", "legal-fact-http-write")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusCreated {
			t.Fatalf("record legal applicability fact HTTP %d: body=%s err=%v", response.StatusCode, body, readErr)
		}
		return body
	}
	first := request()
	replayed := request()
	if !bytes.Equal(first, replayed) {
		t.Fatalf("HTTP idempotent replay changed immutable response bytes: first=%s replay=%s", first, replayed)
	}
	var fact store.LegalApplicabilityFactVersion
	if err := json.Unmarshal(first, &fact); err != nil || fact.Version != 1 || fact.IdempotencyKey != "legal-fact-http-write" {
		t.Fatalf("HTTP write response is not the immutable version: fact=%+v err=%v", fact, err)
	}
	readURL := server.URL + "/api/cases/" + caseID + "/legal-applicability-facts"
	response := routeRequest(t, client, http.MethodGet, readURL, nil)
	var facts store.LegalApplicabilityFactSet
	decodeErr := json.NewDecoder(response.Body).Decode(&facts)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || len(facts.Facts) == 0 {
		t.Fatalf("read resolved legal applicability facts HTTP %d: facts=%+v err=%v", response.StatusCode, facts, decodeErr)
	}
	for _, item := range facts.Facts {
		if item.Dimension == "transaction_kind" {
			if item.Status != "unknown" || item.UnknownReason != "insufficient_evidence" || item.Fact == nil || item.Fact.Version != 1 {
				t.Fatalf("HTTP read did not separate the immutable version from its resolution: %+v", item)
			}
			return
		}
	}
	t.Fatal("HTTP resolved response omitted transaction_kind")
}

func TestLegalApplicabilityHTTPCanonicalAnchorReplayAndChangedAnchorConflict(t *testing.T) {
	st, err := store.OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open isolated legal applicability API test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server := httptest.NewServer(Handler(st, Options{}))
	t.Cleanup(server.Close)
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCase(admin, "法律依据锚点幂等", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	client := legalRouteLogin(t, server.URL, "demo-admin", "admin-demo", "client")
	const sourceText = "本材料说明项目属于独立服务采购。"
	upload, err := st.UploadMaterial(t.Context(), admin, caseID, "attachment", "锚点材料.txt", "text/plain", []byte(sourceText), store.MaterialUploadOptions{IdempotencyKey: "legal-http-anchor-upload"})
	if err != nil {
		t.Fatal(err)
	}
	parsedResponse := routeRequest(t, client, http.MethodPost, server.URL+"/api/cases/"+caseID+"/documents/"+upload.DocumentID+"/parse", strings.NewReader(`{}`))
	parsedResponse.Body.Close()
	if parsedResponse.StatusCode != http.StatusOK {
		t.Fatalf("parse anchored legal applicability source: HTTP %d", parsedResponse.StatusCode)
	}
	parses, err := st.ListMaterialVersionParses(t.Context(), admin, caseID, upload.MaterialID, upload.Version)
	if err != nil || len(parses.Items) == 0 || parses.Items[len(parses.Items)-1].Status != "ready" {
		t.Fatalf("list exact source parse for legal fact: parses=%+v err=%v", parses, err)
	}
	parsedID := parses.Items[len(parses.Items)-1].ParsedDocumentID
	chunk, err := st.GetMaterialVersionParsedChunk(t.Context(), admin, caseID, upload.MaterialID, upload.Version, parsedID, 0, 10000)
	if err != nil || chunk.Text == "" {
		t.Fatalf("read exact parsed legal fact anchor: chunk=%+v err=%v", chunk, err)
	}
	runes := []rune(chunk.Text)
	digest := sha256.Sum256([]byte(string(runes)))
	anchor := map[string]any{"kind": "material_anchor", "material_id": upload.MaterialID, "material_version": upload.Version,
		"document_id": upload.DocumentID, "parsed_document_id": parsedID, "start": 0, "end": len(runes), "quote_digest": hex.EncodeToString(digest[:])}
	ordered := legalAnchorJSONForTest(t, anchor, false)
	reordered := legalAnchorJSONForTest(t, anchor, true)
	if bytes.Equal(ordered, reordered) {
		t.Fatal("anchor fixture did not change JSON object key order")
	}
	encodeRequest := func(refs json.RawMessage, basisNote string) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]any{"expected_version": 0, "dimension": "project_kind", "status": "established", "value": "standalone_services",
			"temporal_basis": "current_state", "basis_note": basisNote, "basis_refs": refs})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	write := func(body []byte) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, server.URL+"/api/cases/"+caseID+"/legal-applicability-facts/project_kind", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Idempotency-Key", "legal-http-anchor-canonical-replay")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	note := "按原文核对项目类型"
	firstResponse := write(encodeRequest(ordered, note))
	firstBody, readErr := io.ReadAll(firstResponse.Body)
	firstStatus := firstResponse.StatusCode
	firstResponse.Body.Close()
	if readErr != nil || firstStatus != http.StatusCreated {
		t.Fatalf("record exact source-backed legal fact HTTP %d: %s err=%v", firstStatus, firstBody, readErr)
	}
	secondRequest := encodeRequest(reordered, note)
	secondRequest = []byte(strings.ReplaceAll(string(secondRequest), note, `\u6309\u539f\u6587\u6838\u5bf9\u9879\u76ee\u7c7b\u578b`))
	replayResponse := write(secondRequest)
	replayBody, readErr := io.ReadAll(replayResponse.Body)
	replayStatus := replayResponse.StatusCode
	replayResponse.Body.Close()
	if readErr != nil || replayStatus != http.StatusCreated || !bytes.Equal(firstBody, replayBody) {
		t.Fatalf("equivalent key order/Unicode encoding did not replay the exact frozen response: firstHTTP=%d replayHTTP=%d equal=%v err=%v", firstStatus, replayStatus, bytes.Equal(firstBody, replayBody), readErr)
	}
	var changedAnchors []map[string]any
	if err := json.Unmarshal(reordered, &changedAnchors); err != nil || len(changedAnchors) != 1 {
		t.Fatal(err)
	}
	changedAnchors[0]["quote_digest"] = strings.Repeat("0", 64)
	changedRefs := legalAnchorJSONForTest(t, changedAnchors[0], false)
	changedResponse := write(encodeRequest(changedRefs, note))
	changedBody, _ := io.ReadAll(changedResponse.Body)
	changedStatus := changedResponse.StatusCode
	changedResponse.Body.Close()
	if changedStatus != http.StatusConflict {
		t.Fatalf("materially changed evidence anchor did not conflict on an existing idempotency key: HTTP %d body=%s", changedStatus, changedBody)
	}
}

func legalAnchorJSONForTest(t *testing.T, value map[string]any, reverse bool) json.RawMessage {
	t.Helper()
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if reverse {
		for left, right := 0, len(keys)-1; left < right; left, right = left+1, right-1 {
			keys[left], keys[right] = keys[right], keys[left]
		}
	}
	var output bytes.Buffer
	output.WriteByte('[')
	output.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			output.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		encodedValue, err := json.Marshal(value[key])
		if err != nil {
			t.Fatal(err)
		}
		output.Write(encodedKey)
		output.WriteByte(':')
		output.Write(encodedValue)
	}
	output.WriteString("}]")
	return output.Bytes()
}

func TestCompanyPolicySourceStatusHasNoInventedPolicyContentOrReference(t *testing.T) {
	status := awaitingCompanyPolicySourceStatus()
	if status.State != "awaiting_source" || status.DisplayLabel != "公司制度资料待提供" {
		t.Fatalf("unexpected company policy source status: %+v", status)
	}
	if status.RequestedOwner != nil || status.HasBody || status.HasPublishedVersion || status.HasContentHash ||
		status.KnowledgeRef != nil || status.CanBeCited || status.SatisfiesMandatoryDependencies {
		t.Fatalf("awaiting-source directory invented content, ownership, or a dependency ref: %+v", status)
	}
}

func TestCompanyPolicySourceStatusWireFixture(t *testing.T) {
	value := map[string]any{"items": []companyPolicySourceStatus{awaitingCompanyPolicySourceStatus()}}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("../../testdata/contracts/legal", "company-status.json")
	if os.Getenv("UPDATE_LEGAL_WIRE_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, encoded) {
		t.Errorf("company status fixture differs from the actual HTTP Go wire projection")
	}
}

func TestCompanyPolicySourceStatusHTTPAuthorizationAndCaseRead(t *testing.T) {
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatalf("open isolated API test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server := httptest.NewServer(Handler(st, Options{}))
	t.Cleanup(server.Close)

	response := routeRequest(t, &http.Client{}, http.MethodGet, server.URL+"/api/admin/company-policy-sources", nil)
	if response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("anonymous admin directory status = %d, want 401", response.StatusCode)
	}
	response.Body.Close()
	adminClient := legalRouteLogin(t, server.URL, "demo-admin", "admin-demo", "admin")
	adminResponse := routeRequest(t, adminClient, http.MethodGet, server.URL+"/api/admin/company-policy-sources", nil)
	var adminBody struct {
		Items []companyPolicySourceStatus `json:"items"`
	}
	adminDecodeErr := json.NewDecoder(adminResponse.Body).Decode(&adminBody)
	adminResponse.Body.Close()
	if adminResponse.StatusCode != http.StatusOK || adminDecodeErr != nil {
		t.Fatalf("admin source directory failed: HTTP %d decode=%v", adminResponse.StatusCode, adminDecodeErr)
	}
	if len(adminBody.Items) != 1 || adminBody.Items[0].State != "awaiting_source" || adminBody.Items[0].CanBeCited || adminBody.Items[0].KnowledgeRef != nil {
		t.Fatalf("admin endpoint returned a citable or invented placeholder: %+v", adminBody.Items)
	}
	response = routeRequest(t, adminClient, http.MethodGet, server.URL+"/api/cases/case-absent/company-policy-sources", nil)
	if response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("admin audience crossed into case Read route: HTTP %d", response.StatusCode)
	}
	response.Body.Close()

	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo administrator missing")
	}
	caseID, err := st.CreateCaseWithBusiness(admin, "公司制度来源目录", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create case for route authorization: %v", err)
	}
	client := legalRouteLogin(t, server.URL, "demo-admin", "admin-demo", "client")
	caseResponse := routeRequest(t, client, http.MethodGet, server.URL+"/api/cases/"+caseID+"/company-policy-sources", nil)
	var caseBody struct {
		Items []companyPolicySourceStatus `json:"items"`
	}
	caseDecodeErr := json.NewDecoder(caseResponse.Body).Decode(&caseBody)
	caseResponse.Body.Close()
	if caseResponse.StatusCode != http.StatusOK || caseDecodeErr != nil || len(caseBody.Items) != 1 {
		t.Fatalf("case Read status endpoint failed: HTTP %d decode=%v", caseResponse.StatusCode, caseDecodeErr)
	}
	otherTenant := legalRouteLogin(t, server.URL, "demo-tenant2-viewer", "tenant2-viewer-demo", "client")
	foreignResponse := routeRequest(t, otherTenant, http.MethodGet, server.URL+"/api/cases/"+caseID+"/company-policy-sources", nil)
	if foreignResponse.StatusCode == http.StatusOK {
		foreignResponse.Body.Close()
		t.Fatal("foreign tenant read another tenant's company policy source status")
	}
	foreignResponse.Body.Close()
}

func TestLegalKnowledgeHTTPImportSearchAndExactCaseReads(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR must point to the external official legal-sources directory")
	}
	st, err := store.OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open isolated API test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server := httptest.NewServer(Handler(st, Options{}))
	t.Cleanup(server.Close)
	anonymous := routeRequest(t, &http.Client{}, http.MethodPost, server.URL+"/api/admin/legal-sources/imports", strings.NewReader(`{}`))
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous legal import was not rejected: HTTP %d", anonymous.StatusCode)
	}
	adminClient := legalRouteLogin(t, server.URL, "demo-admin", "admin-demo", "admin")
	input := legalHTTPImportFixture(t, assetDir)
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := routeRequest(t, adminClient, http.MethodPost, server.URL+"/api/admin/legal-sources/imports", bytes.NewReader(payload))
	var imported store.LegalSourceImportResult
	decodeErr := json.NewDecoder(response.Body).Decode(&imported)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || decodeErr != nil || imported.CaptureSnapshotID == "" || len(imported.Articles) != len(input.Articles) {
		t.Fatalf("admin legal import failed: HTTP %d decode=%v result=%+v", response.StatusCode, decodeErr, imported)
	}

	sourceURL := server.URL + "/api/admin/legal-sources/CN-CIVIL-CODE/snapshots/" + imported.CaptureSnapshotID
	response = routeRequest(t, adminClient, http.MethodGet, sourceURL, nil)
	var capture store.LegalSourceCapture
	decodeErr = json.NewDecoder(response.Body).Decode(&capture)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || capture.SourceID != "CN-CIVIL-CODE" || capture.CaptureSnapshotID != imported.CaptureSnapshotID {
		t.Fatalf("admin exact source capture read failed: HTTP %d decode=%v capture=%+v", response.StatusCode, decodeErr, capture)
	}
	wrongSource := routeRequest(t, adminClient, http.MethodGet, server.URL+"/api/admin/legal-sources/CN-TENDER-LAW/snapshots/"+imported.CaptureSnapshotID, nil)
	wrongSource.Body.Close()
	if wrongSource.StatusCode != http.StatusNotFound {
		t.Fatalf("source path was not bound to exact capture identity: HTTP %d", wrongSource.StatusCode)
	}

	articleRef := store.KnowledgeRef{}
	for _, ref := range imported.Articles {
		if strings.HasSuffix(ref.KnowledgeID, "-496") {
			articleRef = ref
			break
		}
	}
	if articleRef.KnowledgeID == "" {
		t.Fatalf("import response has no article 496: %+v", imported.Articles)
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("test administrator missing")
	}
	version, err := st.GetGovernedKnowledgeVersion(admin, articleRef.KnowledgeID, articleRef.VersionID)
	if err != nil {
		t.Fatalf("read imported article for normal governance lifecycle: %v", err)
	}
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		command := store.GovernedKnowledgeCommandInput{
			KnowledgeID: version.Ref.KnowledgeID, VersionID: version.Ref.VersionID, Action: action,
			ExpectedRevision: version.Revision, ExpectedContentSHA256: version.Ref.ContentSHA256,
			ExpectedPointerRevision: version.PointerRevision, Reason: "HTTP legal route integration",
			CrossCaseReviewed: true, IdempotencyKey: "http-legal-496-" + action,
		}
		version, err = st.GovernedKnowledgeCommand(admin, command)
		if err != nil {
			t.Fatalf("run legal article governance %s: %v", action, err)
		}
	}
	adminSearch := url.Values{"kind": {"legal"}, "source_id": {"CN-CIVIL-CODE"}, "article_number": {"496"}, "q": {"第四百九十六条"}}
	response = routeRequest(t, adminClient, http.MethodGet, server.URL+"/api/admin/governed-knowledge?"+adminSearch.Encode(), nil)
	var adminPage store.LegalKnowledgePage
	decodeErr = json.NewDecoder(response.Body).Decode(&adminPage)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || len(adminPage.Items) != 1 || adminPage.Items[0].Ref != articleRef {
		t.Fatalf("admin legal search failed: HTTP %d decode=%v page=%+v", response.StatusCode, decodeErr, adminPage)
	}
	caseID, err := st.CreateCaseWithBusiness(admin, "法律来源 HTTP 验收", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create case for legal Read routes: %v", err)
	}
	client := legalRouteLogin(t, server.URL, "demo-admin", "admin-demo", "client")
	filter := url.Values{"source_id": {"CN-CIVIL-CODE"}, "article_number": {"496"}, "business_at": {"2025-06-01T00:00:00+08:00"}}
	caseSearchURL := server.URL + "/api/cases/" + caseID + "/legal-knowledge?" + filter.Encode()
	response = routeRequest(t, client, http.MethodGet, caseSearchURL, nil)
	var page store.LegalKnowledgePage
	decodeErr = json.NewDecoder(response.Body).Decode(&page)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || len(page.Items) != 1 || page.Items[0].Ref != articleRef || page.Items[0].Applicability != "unknown" {
		t.Fatalf("case legal search failed: HTTP %d decode=%v page=%+v", response.StatusCode, decodeErr, page)
	}
	articleURL := server.URL + "/api/cases/" + caseID + "/legal-knowledge/" + articleRef.KnowledgeID + "/versions/" + articleRef.VersionID
	response = routeRequest(t, client, http.MethodGet, articleURL, nil)
	var exact store.GovernedKnowledgeVersion
	decodeErr = json.NewDecoder(response.Body).Decode(&exact)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || exact.Ref != articleRef || exact.Content.LegalMetadata.SourceSnapshotID != imported.CaptureSnapshotID {
		t.Fatalf("case exact article read failed: HTTP %d decode=%v ref=%+v", response.StatusCode, decodeErr, exact.Ref)
	}
	captureURL := server.URL + "/api/cases/" + caseID + "/legal-sources/snapshots/" + imported.CaptureSnapshotID
	response = routeRequest(t, client, http.MethodGet, captureURL, nil)
	var caseCapture store.LegalSourceCapture
	decodeErr = json.NewDecoder(response.Body).Decode(&caseCapture)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || caseCapture.CaptureSnapshotID != imported.CaptureSnapshotID {
		t.Fatalf("case exact capture read failed: HTTP %d decode=%v capture=%+v", response.StatusCode, decodeErr, caseCapture)
	}
	badVersionURL := server.URL + "/api/cases/" + caseID + "/legal-knowledge/" + articleRef.KnowledgeID + "/versions/" + strings.Repeat("0", 64)
	response = routeRequest(t, client, http.MethodGet, badVersionURL, nil)
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("case exact-version route fell back to another or latest version")
	}
}

func legalHTTPImportFixture(t *testing.T, assetDir string) store.LegalSourceImportInput {
	t.Helper()
	type sourceRecord struct {
		SourceSnapshotID string `json:"source_snapshot_id"`
		SourceID         string `json:"source_id"`
		Title            string `json:"title"`
		VersionID        string `json:"version_id"`
		PromulgatedOn    string `json:"promulgated_on"`
		EffectiveFrom    string `json:"effective_from"`
		OfficialURL      string `json:"official_url"`
		SnapshotFile     string `json:"snapshot_file"`
		RawSHA256        string `json:"sha256_raw_bytes"`
		StatusEvidenceID string `json:"currency_status_evidence_id"`
	}
	var sourceManifest struct {
		Sources []sourceRecord `json:"sources"`
	}
	readLegalHTTPJSON(t, filepath.Join(assetDir, "articles.json"), &sourceManifest)
	var canonical struct {
		Articles []struct {
			SourceID         string `json:"source_id"`
			SourceSnapshotID string `json:"source_snapshot_id"`
			VersionID        string `json:"version_id"`
			ArticleNumber    int    `json:"article_no"`
			ArticleLabel     string `json:"article_label"`
			FullText         string `json:"full_text"`
			PromulgatedOn    string `json:"promulgated_on"`
			EffectiveFrom    string `json:"effective_from"`
			EffectiveUntil   string `json:"effective_to"`
			StatusEvidenceID string `json:"currency_status_evidence_id"`
			SHA256           string `json:"canonical_text_sha256"`
		} `json:"articles"`
	}
	readLegalHTTPJSON(t, filepath.Join(filepath.Dir(assetDir), "p4-canonical-articles-v2.json"), &canonical)
	const snapshotID = "S1_civil_code_contract"
	var source *sourceRecord
	for i := range sourceManifest.Sources {
		if sourceManifest.Sources[i].SourceSnapshotID == snapshotID {
			source = &sourceManifest.Sources[i]
			break
		}
	}
	if source == nil {
		t.Fatalf("official source snapshot %q missing from manifest", snapshotID)
	}
	var captureManifest struct {
		Sources []struct {
			ID          string `json:"id"`
			ContentType string `json:"content_type"`
			CapturedAt  string `json:"captured_at"`
		} `json:"sources"`
	}
	readLegalHTTPJSON(t, filepath.Join(assetDir, "capture-manifest.json"), &captureManifest)
	type statusRecord struct {
		ID         string `json:"id"`
		CapturedAt string `json:"captured_at"`
		Snapshot   string `json:"snapshot_path"`
		SHA256     string `json:"sha256_raw_bytes"`
	}
	var statusRecords []statusRecord
	for _, name := range []string{"status-capture-manifest.json", "moj-status-capture-manifest.json"} {
		var current struct {
			Sources []statusRecord `json:"sources"`
		}
		readLegalHTTPJSON(t, filepath.Join(assetDir, name), &current)
		statusRecords = append(statusRecords, current.Sources...)
	}
	contentType, sourceCapturedAt := "", ""
	for _, item := range captureManifest.Sources {
		if item.ID == snapshotID {
			contentType, sourceCapturedAt = item.ContentType, item.CapturedAt
		}
	}
	statusCapturedAt, statusPath, statusSHA := "", "", ""
	for _, item := range statusRecords {
		if item.ID == source.StatusEvidenceID {
			statusCapturedAt, statusPath, statusSHA = item.CapturedAt, item.Snapshot, item.SHA256
		}
	}
	if contentType == "" || sourceCapturedAt == "" || statusPath == "" || statusSHA == "" {
		t.Fatal("source capture or status evidence manifest is incomplete")
	}
	var refs []legalnorm.ArticleRef
	var versions []store.LegalArticleVersionInput
	for _, article := range canonical.Articles {
		if article.SourceSnapshotID != snapshotID {
			continue
		}
		refs = append(refs, legalnorm.ArticleRef{
			SourceID: article.SourceID, SourceSnapshotID: snapshotID, VersionID: article.VersionID,
			ArticleNumber: article.ArticleNumber, ArticleLabel: article.ArticleLabel, FullText: article.FullText,
			TextSHA256: article.SHA256, CitationSpan: legalnorm.TextSpan{Start: 0, End: len([]rune(article.ArticleLabel)), Quote: article.ArticleLabel},
			PromulgatedOn: article.PromulgatedOn, EffectiveFrom: article.EffectiveFrom, EffectiveUntil: article.EffectiveUntil,
			CurrencyStatus: legalnorm.StatusEffective, StatusEvidenceID: article.StatusEvidenceID,
		})
		versions = append(versions, store.LegalArticleVersionInput{ArticleNumber: article.ArticleNumber, KnowledgeID: "legal-civil-code-" + strconv.Itoa(article.ArticleNumber), DisplayVersion: article.VersionID})
	}
	if len(refs) != 5 {
		t.Fatalf("civil code test import has %d article spans, want all five", len(refs))
	}
	return store.LegalSourceImportInput{Capture: legalnorm.CaptureInput{
		SourceID: source.SourceID, SourceSnapshotID: snapshotID, VersionID: source.VersionID, Title: source.Title,
		OfficialURL: source.OfficialURL, ContentType: contentType, RawBytes: readLegalHTTPBytes(t, filepath.Join(assetDir, source.SnapshotFile)),
		ExpectedRawSHA256: source.RawSHA256, SourceCapturedAt: sourceCapturedAt, PromulgatedOn: source.PromulgatedOn,
		EffectiveFrom: source.EffectiveFrom, CurrencyStatus: legalnorm.StatusEffective, StatusEvidenceID: source.StatusEvidenceID,
		StatusObservedAt: statusCapturedAt, StatusEvidenceRaw: readLegalHTTPBytes(t, filepath.Join(assetDir, statusPath)),
		ExpectedStatusSHA256: statusSHA, Articles: refs,
	}, Articles: versions, IdempotencyKey: "http-legal-civil-code-baseline"}
}

func readLegalHTTPJSON(t *testing.T, path string, target any) {
	t.Helper()
	if err := json.Unmarshal(readLegalHTTPBytes(t, path), target); err != nil {
		t.Fatalf("decode %s: %v", filepath.Base(path), err)
	}
}

func readLegalHTTPBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return raw
}

func legalRouteLogin(t *testing.T, base, username, password, audience string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	payload := strings.NewReader(`{"username":"` + username + `","password":"` + password + `","audience":"` + audience + `"}`)
	response := routeRequest(t, client, http.MethodPost, base+"/api/session", payload)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login %s/%s: HTTP %d", username, audience, response.StatusCode)
	}
	return client
}

func routeRequest(t *testing.T, client *http.Client, method, target string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLegalSearchFilterRejectsInvalidBounds(t *testing.T) {
	cases := []struct {
		name  string
		query url.Values
	}{
		{name: "noninteger article", query: url.Values{"article_number": {"49a"}}},
		{name: "zero article", query: url.Values{"article_number": {"0"}}},
		{name: "zero limit", query: url.Values{"limit": {"0"}}},
		{name: "limit over maximum", query: url.Values{"limit": {"51"}}},
		{name: "noninteger limit", query: url.Values{"limit": {"many"}}},
		{name: "oversized cursor", query: url.Values{"cursor": {string(make([]byte, 257))}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/?"+test.query.Encode(), nil)
			_, err := legalSearchFilter(request)
			var problem *fault.Error
			if err == nil || !errors.As(err, &problem) || problem.Status != 422 {
				t.Fatalf("expected invalid request error, got %v", err)
			}
		})
	}
}

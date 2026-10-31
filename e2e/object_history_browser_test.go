//go:build browser

package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/legalnorm"
	"contractaudit/internal/store"
)

// TestBrowserObjectHistory uses the real PostgreSQL-backed handlers and the
// reviewed legal capture; the OSS observation and contract materials are
// expressly synthetic. It does not constitute legal or production OSS review.
// Run from the committed candidate with TEST_DATABASE_URL, an absolute
// CAW_DELIVERY_DIR outside the repository and LEGALNORM_ASSET_DIR pointing to
// the reviewed external legal-sources directory. The explicit browser gate is:
// go test -tags browser ./e2e -run '^TestBrowserObjectHistory$' -count=1
// The per-run delivery directory contains the candidate manifest, complete
// Vite and Bun logs, real Bun exit code, trace and failure screenshot/DOM.
func TestBrowserObjectHistory(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	delivery := strings.TrimSpace(os.Getenv("CAW_DELIVERY_DIR"))
	if !filepath.IsAbs(delivery) {
		t.Fatal("CAW_DELIVERY_DIR must be an absolute path outside the source repository")
	}
	if err := os.MkdirAll(delivery, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = filepath.EvalSymlinks(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(root, delivery); err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		t.Fatal("CAW_DELIVERY_DIR resolves inside the source repository")
	}
	outDir, err := os.MkdirTemp(delivery, "object-history-browser-")
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"status": "NOT_RUN", "source_cwd": root, "web_cwd": filepath.Join(root, "web"),
		"journey": "web/e2e/object-history.ts", "browser_log": filepath.Join(outDir, "browser.log"),
		"vite_log": filepath.Join(outDir, "vite.log"), "data_class": "synthetic contract and synthetic OSS; legal review NOT_RUN"}
	defer func() {
		raw, marshalErr := json.MarshalIndent(manifest, "", "  ")
		if marshalErr != nil {
			t.Errorf("marshal source manifest: %v", marshalErr)
			return
		}
		if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), append(raw, '\n'), 0o600); err != nil {
			t.Errorf("write source manifest: %v", err)
		}
	}()
	t.Logf("browser artifacts: %s", outDir)
	git := exec.Command("git", "rev-parse", "HEAD")
	git.Dir = root
	commit, err := git.Output()
	if err != nil {
		t.Fatalf("resolve source commit: %v", err)
	}
	sourceSHA := strings.TrimSpace(string(commit))
	if expected := strings.TrimSpace(os.Getenv("E2E_SOURCE_SHA")); expected != "" && expected != sourceSHA {
		t.Fatalf("E2E_SOURCE_SHA %s differs from source commit %s", expected, sourceSHA)
	}
	manifest["source_sha"] = sourceSHA
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=normal")
	status.Dir = root
	workingTree, err := status.Output()
	if err != nil {
		t.Fatalf("inspect candidate source state: %v", err)
	}
	manifest["source_status_porcelain"] = string(workingTree)
	if len(workingTree) != 0 {
		t.Fatalf("browser acceptance requires a committed candidate; source status: %s", workingTree)
	}
	fileHashes := make(map[string]string)
	for _, name := range []string{"go.mod", "go.sum", "web/package.json", "web/bun.lock", "e2e/object_history_browser_test.go", "web/e2e/object-history.ts", "web/e2e/materials-ui.ts", "web/e2e/c1b-select.ts", "testdata/operatoraudit/packs/valid/dedicated-line.json"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		fileHashes[name] = hex.EncodeToString(sum[:])
	}
	manifest["source_file_sha256"] = fileHashes
	assetDir := strings.TrimSpace(os.Getenv("LEGALNORM_ASSET_DIR"))
	if !filepath.IsAbs(assetDir) {
		t.Fatal("LEGALNORM_ASSET_DIR must name the external reviewed legal-sources directory")
	}
	manifest["legal_asset_dir"] = assetDir
	chrome := chromiumBinary(t)
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun unavailable: install Bun 1.4.3 and web dependencies")
	}
	manifest["chromium"] = chrome
	manifest["bun"] = bun
	runtimeDir := filepath.Join(outDir, "runtime")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenTest(runtimeDir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(e2eModels{})
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	article := objectHistoryLegalFixture(t, st, admin, assetDir)
	manifest["legal_capture_snapshot_id"] = article.Content.LegalMetadata.SourceSnapshotID
	manifest["legal_source_sha256"] = article.Content.LegalMetadata.SourceSHA256
	manifest["legal_status_sha256"] = article.Content.LegalMetadata.StatusEvidenceSHA256
	objectHistoryGovernance(t, st, admin, article)
	update := article.Content
	update.Version = "object-history-later-version"
	update.Summary += "（后续知识版本，用于冻结历史测试）"
	updateJSON, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	updatePath := filepath.Join(outDir, "legal-update-content.json")
	if err := os.WriteFile(updatePath, updateJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest["legal_original_ref"] = article.Ref

	catalog, err := capabilitypack.NewRegistry(capabilitypack.RegistryConfig{
		AllowedCapabilities: []string{"operator.oss.read_config"}, AllowedConnectors: []string{"fixture.oss"},
		AllowedComparators: []string{"builtin.configured_bandwidth_at_least/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	packBytes, err := os.ReadFile(filepath.Join(root, "testdata/operatoraudit/packs/valid/dedicated-line.json"))
	if err != nil {
		t.Fatal(err)
	}
	pack, err := capabilitypack.Parse(packBytes)
	if err != nil {
		t.Fatal(err)
	}
	pack.RulePackRef.ID, pack.RulePackRef.Version = "telecom-object-history", "1"
	if err := catalog.Register(pack); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	webPort := freePort(t)
	webURL := "http://127.0.0.1:" + webPort
	api := &http.Server{Handler: httpapi.Handler(st, httpapi.Options{
		CORSOrigins: []string{webURL}, OperatorAuditEnabled: true, CapabilityPacks: catalog,
		ExternalConnectors: map[string]auditext.ReadConnector{"fixture.oss": objectHistoryConnector{}},
		ExternalConnectorInfo: []httpapi.OperatorConnectorInfo{{ConnectorID: "fixture.oss", SourceSystem: "fixture-oss", AllowedObjects: []string{"fixture-line-001", "fixture-line-002"}}},
	})}
	go func() { _ = api.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
	})
	viteLog, err := os.Create(filepath.Join(outDir, "vite.log"))
	if err != nil {
		t.Fatal(err)
	}
	vite := exec.Command(bun, "run", "dev", "--", "--port", webPort, "--strictPort", "--host", "127.0.0.1")
	vite.Dir = filepath.Join(root, "web")
	vite.Env = append(os.Environ(), "VITE_DEV_API_PROXY=http://"+listener.Addr().String())
	vite.Stdout, vite.Stderr = viteLog, viteLog
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := vite.Start(); err != nil {
		_ = viteLog.Close()
		t.Fatal(err)
	}
	defer func() {
		stopGroup(vite)
		if err := viteLog.Close(); err != nil {
			t.Errorf("close Vite log: %v", err)
		}
	}()
	waitHTTP(t, webURL+"/")
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	env := append(os.Environ(), "CHROMIUM_BIN="+chrome, "E2E_WEB="+webURL,
		"E2E_OBJECT_HISTORY_OUT="+outDir, "E2E_SCREENSHOT_DIR="+outDir,
		"E2E_LEGAL_UPDATE_CONTENT="+updatePath, "E2E_SOURCE_SHA="+sourceSHA)
	cmd := exec.CommandContext(ctx, bun, "e2e/object-history.ts")
	cmd.Dir, cmd.Env = filepath.Join(root, "web"), localBrowserEnv(env)
	manifest["command"] = []string{bun, "e2e/object-history.ts"}
	manifest["web_url"] = webURL
	output, runErr := runBrowserCommand(ctx, cmd)
	logErr := os.WriteFile(filepath.Join(outDir, "browser.log"), output, 0o600)
	if logErr != nil {
		t.Errorf("write complete browser log: %v", logErr)
	}
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	manifest["exit_code"] = code
	manifest["status"] = "FAIL"
	if runErr == nil && code == 0 && logErr == nil {
		manifest["status"] = "PASS"
	}
	t.Log(string(output))
	if runErr != nil || code != 0 {
		t.Fatalf("browser journey failed (exit=%d, context=%v): %v", code, ctx.Err(), runErr)
	}
}

type objectHistoryConnector struct{}

func (objectHistoryConnector) ID() string { return "fixture.oss" }
func (objectHistoryConnector) Read(_ context.Context, request auditext.ReadRequest) (auditext.Observation, error) {
	observed := time.Now().UTC().Truncate(time.Microsecond)
	from, until := observed.Add(-24*time.Hour), observed.Add(24*time.Hour)
	version, payload := "synthetic-record-v1", "fixture:object-history-oss"
	return auditext.Observation{
		SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: fmt.Sprintf("snapshot-object-history-%d", observed.UnixNano()),
		TenantID: request.Scope.TenantID, CaseID: request.Scope.CaseID, ConnectorID: request.ConnectorID,
		SourceSystem: "fixture-oss", ObjectBindingVersion: request.BindingVersion, ExternalObjectID: request.ExternalObjectID,
		RecordID: "record-" + request.ExternalObjectID, RecordVersion: &version, ObservedAt: observed,
		EffectiveFrom: from, EffectiveUntil: until, Availability: auditext.AvailabilityAvailable,
		ReasonCode: "read_succeeded", PayloadRef: &payload,
		Fields: []auditext.Field{{Name: "configured_bandwidth", Value: "100", Unit: "Mbit/s", SourcePointer: "/configured_bandwidth", SemanticKind: "configured_value"}},
		DataClass: auditext.DataClassSynthetic,
		TemporalProof: &auditext.TemporalProof{Kind: auditext.TemporalProofHistoricalInterval, SourceCatalogID: "fixture.oss",
			DatasetVersion: "synthetic-v1", RecordVersion: version, SourceAsOf: &observed, ValidFrom: &from, ValidUntil: &until},
	}, nil
}

// objectHistoryLegalFixture adapts the reviewed external capture used by the
// legal-store integration fixture. Import verifies the pinned raw and status
// digests and extracts the article through the production governance path.
func objectHistoryLegalFixture(t *testing.T, st *store.Store, admin accounts.User, assetDir string) store.GovernedKnowledgeVersion {
	t.Helper()
	var sources struct { Sources []struct {
		SnapshotID string `json:"source_snapshot_id"`; SourceID string `json:"source_id"`; Title string `json:"title"`
		Version string `json:"version_id"`; Promulgated string `json:"promulgated_on"`; Effective string `json:"effective_from"`
		URL string `json:"official_url"`; File string `json:"snapshot_file"`; SHA string `json:"sha256_raw_bytes"`; StatusID string `json:"currency_status_evidence_id"`
	} `json:"sources"` }
	objectHistoryReadJSON(t, filepath.Join(assetDir, "articles.json"), &sources)
	var source = sources.Sources[:0]
	for _, item := range sources.Sources {
		if item.SnapshotID == "S1_civil_code_contract" { source = append(source, item); break }
	}
	if len(source) != 1 { t.Fatal("reviewed legal source S1 is missing") }
	s := source[0]
	var capture struct { Sources []struct { ID string `json:"id"`; ContentType string `json:"content_type"`; CapturedAt string `json:"captured_at"` } `json:"sources"` }
	objectHistoryReadJSON(t, filepath.Join(assetDir, "capture-manifest.json"), &capture)
	var contentType, capturedAt string
	for _, item := range capture.Sources { if item.ID == s.SnapshotID { contentType, capturedAt = item.ContentType, item.CapturedAt; break } }
	var statuses struct { Sources []struct { ID string `json:"id"`; CapturedAt string `json:"captured_at"`; File string `json:"snapshot_path"`; SHA string `json:"sha256_raw_bytes"` } `json:"sources"` }
	objectHistoryReadJSON(t, filepath.Join(assetDir, "status-capture-manifest.json"), &statuses)
	var statusAt, statusFile, statusSHA string
	for _, item := range statuses.Sources { if item.ID == s.StatusID { statusAt, statusFile, statusSHA = item.CapturedAt, item.File, item.SHA; break } }
	if contentType == "" || capturedAt == "" || statusAt == "" || statusFile == "" || statusSHA == "" { t.Fatal("reviewed legal capture metadata is incomplete") }
	var canonical struct { Articles []struct {
		SourceID string `json:"source_id"`; SnapshotID string `json:"source_snapshot_id"`; Version string `json:"version_id"`
		Number int `json:"article_no"`; Label string `json:"article_label"`; Text string `json:"full_text"`
		Promulgated string `json:"promulgated_on"`; Effective string `json:"effective_from"`; Until string `json:"effective_to"`
		StatusID string `json:"currency_status_evidence_id"`; SHA string `json:"canonical_text_sha256"`
	} `json:"articles"` }
	objectHistoryReadJSON(t, filepath.Join(filepath.Dir(assetDir), "p4-canonical-articles-v2.json"), &canonical)
	var articles []legalnorm.ArticleRef
	for _, item := range canonical.Articles {
		if item.SnapshotID != s.SnapshotID || item.Number != 496 { continue }
		articles = append(articles, legalnorm.ArticleRef{SourceID: item.SourceID, SourceSnapshotID: item.SnapshotID, VersionID: item.Version,
			ArticleNumber: item.Number, ArticleLabel: item.Label, FullText: item.Text, TextSHA256: item.SHA,
			CitationSpan: legalnorm.TextSpan{Start: 0, End: len([]rune(item.Label)), Quote: item.Label}, PromulgatedOn: item.Promulgated,
			EffectiveFrom: item.Effective, EffectiveUntil: item.Until, CurrencyStatus: legalnorm.StatusEffective, StatusEvidenceID: item.StatusID})
	}
	if len(articles) != 1 { t.Fatal("reviewed article 496 is missing or ambiguous") }
	imported, err := st.ImportLegalSource(context.Background(), admin, store.LegalSourceImportInput{Capture: legalnorm.CaptureInput{
		SourceID: s.SourceID, SourceSnapshotID: s.SnapshotID, VersionID: s.Version, Title: s.Title, OfficialURL: s.URL,
		ContentType: contentType, RawBytes: objectHistoryReadBytes(t, filepath.Join(assetDir, s.File)), ExpectedRawSHA256: s.SHA,
		SourceCapturedAt: capturedAt, PromulgatedOn: s.Promulgated, EffectiveFrom: s.Effective, CurrencyStatus: legalnorm.StatusEffective,
		StatusEvidenceID: s.StatusID, StatusObservedAt: statusAt, StatusEvidenceRaw: objectHistoryReadBytes(t, filepath.Join(assetDir, statusFile)),
		ExpectedStatusSHA256: statusSHA, Articles: articles}, Articles: []store.LegalArticleVersionInput{{ArticleNumber: 496, KnowledgeID: "legal-civil-code-496", DisplayVersion: s.Version}}, IdempotencyKey: "object-history-legal-import"})
	if err != nil { t.Fatalf("import reviewed legal fixture: %v", err) }
	if len(imported.Articles) != 1 { t.Fatalf("legal fixture imported %d articles", len(imported.Articles)) }
	article, err := st.GetGovernedKnowledgeVersion(admin, imported.Articles[0].KnowledgeID, imported.Articles[0].VersionID)
	if err != nil { t.Fatal(err) }
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		article, err = st.GovernedKnowledgeCommand(admin, store.GovernedKnowledgeCommandInput{KnowledgeID: article.Ref.KnowledgeID,
			VersionID: article.Ref.VersionID, Action: action, ExpectedRevision: article.Revision, ExpectedContentSHA256: article.Ref.ContentSHA256,
			ExpectedPointerRevision: article.PointerRevision, Reason: "浏览器冻结引用测试的已核验来源", CrossCaseReviewed: true, IdempotencyKey: "object-history-legal-" + action})
		if err != nil { t.Fatalf("%s legal fixture: %v", action, err) }
	}
	return article
}

func objectHistoryReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	return value
}
func objectHistoryReadJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := json.Unmarshal(objectHistoryReadBytes(t, path), value); err != nil { t.Fatalf("decode %s: %v", path, err) }
}

func objectHistoryGovernance(t *testing.T, st *store.Store, admin accounts.User, article store.GovernedKnowledgeVersion) {
	t.Helper()
	tenant := accounts.Tenant(admin)
	scope := map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"}
	policyAuthority := []any{map[string]any{"id": "object-history-policy", "version": "1", "kind": "synthetic_demo"}}
	metadata := article.Content.LegalMetadata
	if metadata == nil { t.Fatal("published legal article has no source metadata") }
	quote := []rune(article.Content.Body)
	if len(quote) > 32 { quote = quote[:32] }
	legalRef := map[string]any{"authority_source_id": metadata.SourceID, "knowledge_id": article.Ref.KnowledgeID, "version_id": article.Ref.VersionID,
		"content_sha256": article.Ref.ContentSHA256, "source_id": metadata.SourceID, "source_snapshot_id": metadata.SourceSnapshotID,
		"norm_title": metadata.NormTitle, "norm_version": metadata.NormVersion, "article_number": metadata.ArticleNumber,
		"document_type": metadata.DocumentType, "source_sha256": metadata.SourceSHA256, "article_text_sha256": metadata.NormTextSHA256,
		"effective_from": metadata.EffectiveFrom, "effective_until": metadata.EffectiveUntil,
		"quote_span": map[string]any{"start": 0, "end": len(quote), "quote": string(quote)},
		"applicability": map[string]any{"all_of": []any{map[string]any{"field": "transaction_kind", "one_of": []string{"civil_contract"}}}}}
	rules := []any{
		map[string]any{"rule_id": "ENT-TECH-BW-001", "version": "1", "domain": "technical", "capability_id": "dedicated_line.minimum_bandwidth", "mandatory": true,
			"business_scope": scope, "tenant_id": tenant, "comparator": "minimum_bandwidth", "threshold": map[string]any{"value": "100", "unit": "Mbit/s"},
			"required_fact": map[string]any{"dimension": "minimum_bandwidth", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "bandwidth_decimal"}, "authority_sources": policyAuthority},
		map[string]any{"rule_id": "ENT-TECH-SLA-001", "version": "1", "domain": "technical", "capability_id": "dedicated_line.monthly_availability", "mandatory": true,
			"business_scope": scope, "tenant_id": tenant, "comparator": "monthly_sla", "threshold": map[string]any{"value": "99.9", "unit": "percent"},
			"required_fact": map[string]any{"dimension": "monthly_availability", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "percentage", "measurement_window": "calendar_month", "time_zone": "Asia/Shanghai", "calendar": "gregorian", "outage_definition": "all_service_unavailability", "exclusions": []string{}},
			"authority_sources": []any{map[string]any{"id": metadata.SourceID, "version": metadata.NormVersion, "kind": "normative"}}, "legal_authorities": []any{legalRef}},
		map[string]any{"rule_id": "ENT-COM-ADV-001", "version": "1", "domain": "commercial", "capability_id": "payment.advance_ratio.ceiling", "mandatory": true,
			"business_scope": scope, "tenant_id": tenant, "comparator": "advance_payment_ceiling", "threshold": map[string]any{"value": "1/3", "unit": "ratio", "basis": "contract_total"},
			"required_fact": map[string]any{"dimension": "advance_payment_ratio", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "ratio", "basis": "contract_total"}, "authority_sources": policyAuthority},
	}
	raw, err := json.Marshal(map[string]any{"schema_version": "rulepack/v1", "id": "telecom-object-history", "version": "1", "tenant_id": tenant,
		"effective_from": "2020-01-01T00:00:00Z", "effective_until": "2100-01-01T00:00:00Z", "rules": rules})
	if err != nil { t.Fatal(err) }
	created, err := st.CreateRulePack(admin, raw)
	if err != nil { t.Fatalf("create fixture pack: %v", err) }
	validated, err := st.ValidateRulePackVersion(admin, store.GovernanceInput{PackID: "telecom-object-history", Version: "1", ExpectedRevision: created.Pack.Revision})
	if err != nil { t.Fatalf("validate fixture pack: %v", err) }
	approved, err := st.ApproveRulePackVersion(admin, store.GovernanceInput{PackID: "telecom-object-history", Version: "1", ExpectedRevision: validated.Pack.Revision, ExpectedContentHash: validated.Pack.ContentHash})
	if err != nil { t.Fatalf("approve fixture pack: %v", err) }
	published, err := st.PublishRulePackVersion(admin, store.GovernanceInput{PackID: "telecom-object-history", Version: "1", ExpectedRevision: approved.Pack.Revision, ExpectedPointerRevision: approved.Pack.ActivePointerRevision})
	if err != nil { t.Fatalf("publish fixture pack: %v", err) }
	body := "带宽不低于100 Mbit/s。预付款比例不超过1/3，预付款以合同总额为基础。"
	first, second := "带宽不低于100 Mbit/s", "预付款比例不超过1/3"
	secondStart := len([]rune(first + "。"))
	policy, err := st.CreateGovernedKnowledge(admin, store.CreateGovernedKnowledgeInput{Content: store.GovernedKnowledgeContent{
		KnowledgeID: "object-history-policy", Version: "1", Kind: "policy", Title: "合成专线制度", Body: body, Summary: "合成测试用带宽和预付款规则",
		SourceType: "synthetic_policy", SourceReference: "合成浏览器验收制度", Scope: store.KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z",
		Binding: store.PolicyRuleBinding{PackID: "telecom-object-history", PackVersion: "1", PackContentHash: published.Pack.ContentHash, PackSnapshotDigest: published.Pack.SnapshotSHA256,
			CompilerRef: "governed-rulepack/v1", RuleIDs: []string{"ENT-TECH-BW-001", "ENT-COM-ADV-001"}, SourceSpans: []store.KnowledgeSourceSpan{{RuleID: "ENT-TECH-BW-001", Start: 0, End: len([]rune(first)), Quote: first}, {RuleID: "ENT-COM-ADV-001", Start: secondStart, End: secondStart + len([]rune(second)), Quote: second}}, CandidateDiff: "规则差异已审阅", ValidationEvidence: "合成制度校验"}}, IdempotencyKey: "object-history-policy-create"})
	if err != nil { t.Fatalf("create synthetic policy: %v", err) }
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		policy, err = st.GovernedKnowledgeCommand(admin, store.GovernedKnowledgeCommandInput{KnowledgeID: policy.Ref.KnowledgeID, VersionID: policy.Ref.VersionID,
			Action: action, ExpectedRevision: policy.Revision, ExpectedContentSHA256: policy.Ref.ContentSHA256, ExpectedPointerRevision: policy.PointerRevision,
			Reason: "合成制度浏览器测试", IdempotencyKey: "object-history-policy-" + action})
		if err != nil { t.Fatalf("%s synthetic policy: %v", action, err) }
	}
}

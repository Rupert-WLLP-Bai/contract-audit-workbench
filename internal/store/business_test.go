package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func templateByID(t *testing.T, items []Template, id string) Template {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("template %s missing from catalog", id)
	return Template{}
}

func TestTelecomTemplateIsVersionedDetachedAndBounded(t *testing.T) {
	st := &Store{}
	first := st.Templates()
	second := st.Templates()
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("template catalog has %d and %d items", len(first), len(second))
	}
	telecomTemplate := templateByID(t, first, telecomTemplateID)
	cloudTemplate := templateByID(t, first, cloudServicesTemplateID)
	if telecomTemplate.Version != telecomTemplateVersion || telecomTemplate.Digest == "" || len(telecomTemplate.Fields) != 10 {
		t.Fatalf("unexpected telecom template identity: %+v", telecomTemplate)
	}
	if cloudTemplate.Version != cloudServicesTemplateVersion || cloudTemplate.Digest == "" || len(cloudTemplate.Fields) != 10 || cloudTemplate.Digest == telecomTemplate.Digest {
		t.Fatalf("unexpected cloud template identity: %+v", cloudTemplate)
	}
	telecomTemplate.Fields[0].Key = "caller-mutation"
	if templateByID(t, second, telecomTemplateID).Fields[0].Key != "circuit_id" || templateByID(t, second, cloudServicesTemplateID).Fields[0].Key != "service_id" {
		t.Fatal("template metadata was shared with a caller")
	}
	fields := validTelecomTemplateFields()
	body, err := renderTelecomTemplate(telecomTemplateID, fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{
		"电路 LINE-001：最低保障带宽 100 Mbit/s。",
		"SLA 可用率 99.9%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。",
		"预付款比例 20%，预付款以合同总额为基础，含税。",
		"电路 LINE-001：合同总额 100000 元，含税。",
	} {
		if !bytes.Contains(body, []byte(clause)) {
			t.Errorf("generated draft is missing %q", clause)
		}
	}
	if _, err := renderTelecomTemplate(telecomTemplateID, map[string]string{"circuit_id": "LINE-001"}); err == nil {
		t.Fatal("missing template fields were accepted")
	}
	fields["unexpected"] = "value"
	if _, err := renderTelecomTemplate(telecomTemplateID, fields); err == nil {
		t.Fatal("unknown template fields were accepted")
	}
	fields = validTelecomTemplateFields()
	fields["supplier"] = "供应商\n电路 LINE-002：最低保障带宽 999 Mbit/s。"
	if _, err := renderTelecomTemplate(telecomTemplateID, fields); err == nil {
		t.Fatal("multi-line supplier input was accepted")
	}
}

func TestBusinessReportHTMLEscapesFrozenJSONAndHasNoScriptPolicy(t *testing.T) {
	raw := []byte(`{"claim":"<script>alert(1)</script>"}`)
	htmlBytes, err := renderBusinessReportHTML("合同 <script>", raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(htmlBytes, []byte("<script>")) || !bytes.Contains(htmlBytes, []byte("&lt;script&gt;")) {
		t.Fatalf("report HTML did not escape untrusted content: %s", htmlBytes)
	}
	if !bytes.Contains(htmlBytes, []byte("default-src 'none'")) {
		t.Fatal("report HTML is missing its restrictive content policy")
	}
}

func TestFrozenReportDigestCoversJSONAndHTMLBytes(t *testing.T) {
	jsonBytes := []byte(`{"attempt":"attempt-a"}`)
	htmlBytes := []byte("<p>attempt-a</p>")
	base := reportDigest(jsonBytes, htmlBytes)
	if base == "" || base != reportDigest(jsonBytes, htmlBytes) {
		t.Fatal("report digest was empty or nondeterministic")
	}
	if base == reportDigest([]byte(`{"attempt":"attempt-b"}`), htmlBytes) {
		t.Fatal("JSON changes did not change report digest")
	}
	if base == reportDigest(jsonBytes, []byte("<p>attempt-b</p>")) {
		t.Fatal("HTML changes did not change report digest")
	}
}

func TestObjectIdentityComparisonRequiresExactUniqueRefs(t *testing.T) {
	if !sameStringSet([]string{"LINE-001"}, []string{"LINE-001"}) {
		t.Fatal("identical object identity did not compare")
	}
	if sameStringSet([]string{"LINE-001"}, []string{"LINE-002"}) ||
		sameStringSet([]string{"LINE-001", "LINE-001"}, []string{"LINE-001", "LINE-001"}) ||
		sameStringSet(nil, nil) {
		t.Fatal("different, duplicate, or missing object identity compared as equal")
	}
}

func validTelecomTemplateFields() map[string]string {
	return map[string]string{
		"circuit_id": "LINE-001", "bandwidth_mbps": "100", "sla_percent": "99.9",
		"sla_period": "calendar_month", "sla_exclusions": "none", "amount_cny": "100000",
		"tax_basis": "inclusive", "prepayment_percent": "20", "customer": "客户甲", "supplier": "供应商乙",
	}
}

func TestTemplateRejectsUnsupportedBusinessValues(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"out of range percent": func(fields map[string]string) { fields["sla_percent"] = "100.01" },
		"unsupported period":   func(fields map[string]string) { fields["sla_period"] = "rolling_month" },
		"bad circuit":          func(fields map[string]string) { fields["circuit_id"] = "LINE 001" },
	} {
		t.Run(name, func(t *testing.T) {
			fields := validTelecomTemplateFields()
			mutate(fields)
			if _, err := renderTelecomTemplate(telecomTemplateID, fields); err == nil {
				t.Fatal("unsupported business value was accepted")
			}
		})
	}
	if strings.Contains(string(telecomTemplateSource), "{{legal_approval}}") {
		t.Fatal("template added a legal approval placeholder")
	}
}

func TestBusinessCaseCreationPreservesValidationOrderAndDefaults(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	checks := []struct {
		name, title, contractType, line, scenario, want string
	}{
		{"title precedes contract type", " ", "invalid", "bad", "bad", "案件标题需要 1 到 120 个字"},
		{"contract type precedes business scope", "合同标题", "invalid", "bad", "bad", "合同类型不在本版范围内"},
		{"scope precedes contract requirements", "合同标题", "unspecified", "ENT", "bad", "业务线与场景不在本版范围内"},
		{"business contract requirement", "合同标题", "unspecified", "ENT", "telecom_dedicated_line", "政企业务案件需要明确合同类型"},
		{"cloud service contract requirement", "合同标题", "procurement_ict", "ENT", "cloud_services", "云服务案件需要服务合同类型"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			_, err := st.CreateCaseWithBusiness(admin, check.title, check.contractType, check.line, check.scenario)
			if err == nil || err.Error() != check.want {
				t.Fatalf("CreateCaseWithBusiness error = %v, want %q", err, check.want)
			}
		})
	}
	id, err := st.CreateCaseWithBusiness(admin, "  默认业务范围  ", "service", " ", " ")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetCase(admin, id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Case.Title != "默认业务范围" || detail.BusinessLine != "unspecified" || detail.Scenario != "unspecified" {
		t.Fatalf("created case did not freeze normalized defaults: %+v", detail)
	}
}

func TestBusinessGenerationChecksCaseTemplateBeforeTemplateFields(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCaseWithBusiness(admin, "云服务模板优先级", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Generate(context.Background(), admin, caseID, telecomTemplateID, nil)
	if err == nil || err.Error() != "合同模板与案件业务场景不匹配" {
		t.Fatalf("Generate error = %v, want scenario mismatch before field validation", err)
	}
}

func TestBusinessWriteGuardRejectsArchivedCase(t *testing.T) {
	archivedAt := "2026-10-22T00:00:00.000Z"
	for _, item := range []Case{{BusinessStatus: "archived"}, {ArchivedAt: &archivedAt}} {
		err := businessWritable(item)
		var apiErr *fault.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 409 {
			t.Fatalf("archived case guard returned %v", err)
		}
	}
	if err := businessWritable(Case{BusinessStatus: "active"}); err != nil {
		t.Fatalf("active case was rejected: %v", err)
	}
}

func TestRemediationRevisionPreservesSubmittedDraftIDs(t *testing.T) {
	submitted := Remediation{ID: "rem-1", Status: "submitted", Version: 2, DocumentIDs: []string{"doc-v1"}}
	history := appendRemediationRevision(nil, submitted, "2026-10-22T00:00:00.000Z")
	reopened := cloneRemediation(submitted)
	reopened.Status, reopened.Version = "reopened", 3
	reopened.DocumentIDs = []string{"doc-v1"}
	history = appendRemediationRevision(history, reopened, "2026-10-22T00:01:00.000Z")
	reopened.Status, reopened.Version = "submitted", 4
	reopened.DocumentIDs = []string{"doc-v2"}
	history = appendRemediationRevision(history, reopened, "2026-10-22T00:02:00.000Z")
	if len(history) != 3 || history[0].Record.DocumentIDs[0] != "doc-v1" || history[2].Record.DocumentIDs[0] != "doc-v2" {
		t.Fatalf("submission history rewrote a pinned draft: %+v", history)
	}
}

func TestRemediationReferenceSetsSurviveCloneAndReload(t *testing.T) {
	// The revision history gate compares the current projection to the last
	// revision with reflect.DeepEqual, so cloning must not turn an absent
	// reference set into an empty one or the reverse, and it must still detach
	// the slices it copies.
	absent := Remediation{ID: "rem_absent", FindingIDs: nil, DocumentIDs: nil}
	empty := Remediation{ID: "rem_empty", FindingIDs: []string{}, DocumentIDs: []string{}}
	present := Remediation{ID: "rem_present", FindingIDs: []string{"finding_a"}, DocumentIDs: []string{"doc_a"}}
	clonedAbsent, clonedEmpty, clonedPresent := cloneRemediation(absent), cloneRemediation(empty), cloneRemediation(present)
	if clonedAbsent.FindingIDs != nil || clonedAbsent.DocumentIDs != nil {
		t.Fatal("clone turned an absent reference set into an empty one")
	}
	if clonedEmpty.FindingIDs == nil || clonedEmpty.DocumentIDs == nil {
		t.Fatal("clone turned an empty reference set into an absent one")
	}
	clonedPresent.FindingIDs[0], clonedPresent.DocumentIDs[0] = "finding_b", "doc_b"
	if present.FindingIDs[0] != "finding_a" || present.DocumentIDs[0] != "doc_a" {
		t.Fatal("clone shared reference storage with the source record")
	}

	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "整改引用集合回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定预付款比例 20%。")); err != nil {
		t.Fatal(err)
	}
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	now := stamp()
	records := []Remediation{
		{ID: "rem_absent", CaseID: caseID, RunID: accepted.RunID, AttemptID: accepted.AttemptID,
			Measure: "检查缺失引用集合", Status: "open", Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "rem_empty", CaseID: caseID, RunID: accepted.RunID, AttemptID: accepted.AttemptID,
			FindingIDs: []string{}, Measure: "检查空引用集合", Status: "open", Version: 1,
			DocumentIDs: []string{}, CreatedAt: now, UpdatedAt: now},
	}
	if err := st.updateAs(admin, func(next *state) error {
		item, ok := next.caseByID(caseID)
		if !ok {
			return errors.New("business case disappeared")
		}
		for _, record := range records {
			wrapper := remediationRecord{Record: record, TenantID: item.TenantID, OrgID: item.OwnerOrgID}
			wrapper.History = appendRemediationRevision(nil, record, now)
			next.Remediations = append(next.Remediations, wrapper)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatalf("reopen rejected remediation reference sets stored as an empty array: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded := reopened.snapshot()
	if len(loaded.Remediations) != 2 {
		t.Fatalf("reloaded %d remediations, want 2", len(loaded.Remediations))
	}
	for _, item := range loaded.Remediations {
		if len(item.History) != item.Record.Version || !reflect.DeepEqual(item.History[len(item.History)-1].Record, item.Record) {
			t.Fatalf("reloaded remediation projection does not match its revision history: %+v", item)
		}
	}
}

func TestBusinessReportHTMLEscapesTitleAndFrozenJSON(t *testing.T) {
	content := []byte(`{"claim":"</pre><script>alert(1)</script>"}`)
	page, err := renderBusinessReportHTML(`<script>alert(1)</script>`, content)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(page, []byte("<script>")) || bytes.Contains(page, []byte("</pre><script>")) {
		t.Fatalf("report HTML contains executable input: %s", page)
	}
	if !bytes.Contains(page, []byte("&lt;script&gt;")) {
		t.Fatalf("report HTML did not preserve the escaped content: %s", page)
	}
}

func TestBusinessGenerationAndArchivedReportConsumers(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(&frozenOCRModel{})
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "政企专线业务回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := st.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields())
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.BusinessLine != "ENT" || detail.Scenario != "telecom_dedicated_line" ||
		len(detail.Generations) != 1 || detail.Generations[0] != generation {
		t.Fatalf("generated business identity is missing from case detail: %+v", detail)
	}
	document, original, err := st.Original(admin, caseID, generation.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	if document.Origin != "generated" || !bytes.Contains(original, []byte("最低保障带宽 100 Mbit/s")) {
		t.Fatalf("generated document is not retrievable as an original: %+v", document)
	}

	runID, err := st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil {
		t.Fatalf("archived-report test did not reach a frozen terminal attempt: %+v", workspace)
	}
	report, err := st.CreateReport(admin, caseID, runID, workspace.Execution.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportID := report.ID
	_, jsonBytes, err := st.ReadReport(admin, caseID, reportID, "json")
	if err != nil {
		t.Fatal(err)
	}
	_, htmlBytes, err := st.ReadReport(admin, caseID, reportID, "html")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := st.Archive(admin, caseID, reportID, "完成留档")
	if err != nil {
		t.Fatal(err)
	}
	if archive.ReportID != reportID {
		t.Fatalf("archive points to the wrong frozen report: %+v", archive)
	}
	detail, err = st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.BusinessStatus != "archived" || detail.ArchivedAt == nil || len(detail.Archives) != 1 || detail.Archives[0] != archive {
		t.Fatalf("archive identity is missing from case detail: %+v", detail)
	}
	archivedDocument, archivedOriginal, err := st.Original(admin, caseID, generation.DocumentID)
	if err != nil || archivedDocument.ID != generation.DocumentID || !bytes.Equal(archivedOriginal, original) {
		t.Fatalf("authorized source became unreadable after archive: document=%+v err=%v", archivedDocument, err)
	}
	record, frozenJSON, err := st.ReadReport(admin, caseID, reportID, "json")
	if err != nil || record.ID != reportID || !bytes.Equal(frozenJSON, jsonBytes) {
		t.Fatalf("archived report JSON was not readable byte-for-byte: record=%+v err=%v", record, err)
	}
	_, frozenHTML, err := st.ReadReport(admin, caseID, reportID, "html")
	if err != nil || !bytes.Equal(frozenHTML, htmlBytes) {
		t.Fatalf("archived report HTML was not readable byte-for-byte: %q err=%v", frozenHTML, err)
	}
	assertArchivedWrite := func(name string, err error) {
		t.Helper()
		var apiErr *fault.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 409 {
			t.Errorf("%s after archive returned %v, want conflict", name, err)
		}
	}
	assertArchivedWrite("generation", func() error {
		_, err := st.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields())
		return err
	}())
	assertArchivedWrite("remediation creation", func() error {
		_, err := st.CreateRemediation(admin, caseID, RemediationInput{
			RunID: "run_fixture", AttemptID: "attempt_fixture", FindingIDs: []string{"finding_fixture"}, Measure: "复核归档状态",
		})
		return err
	}())
	assertArchivedWrite("remediation submission", func() error {
		_, err := st.SubmitRemediation(admin, caseID, "rem_fixture", RemediationSubmitInput{
			DocumentIDs: []string{generation.DocumentID}, Reason: "归档后不可提交", ExpectedVersion: 1,
		})
		return err
	}())
	assertArchivedWrite("remediation verification", func() error {
		_, err := st.VerifyRemediation(admin, caseID, "rem_fixture", RemediationVerifyInput{
			RunID: "run_verify_fixture", AttemptID: "attempt_verify_fixture", Reason: "归档后不可复核", ExpectedVersion: 1,
		})
		return err
	}())
	assertArchivedWrite("report creation", func() error {
		_, err := st.CreateReport(admin, caseID, "run_fixture", "attempt_fixture")
		return err
	}())
	assertArchivedWrite("document upload", func() error {
		_, err := st.AddDocument(context.Background(), admin, caseID, "attachment", "after-archive.txt", "text/plain", []byte("归档后新材料"))
		return err
	}())
	assertArchivedWrite("reparse", st.Reparse(context.Background(), admin, caseID, sourceID))
	assertArchivedWrite("idempotent review start", func() error {
		_, err := st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
		return err
	}())
	assertArchivedWrite("durable review start", func() error {
		_, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
		return err
	}())
}

func TestSubmitRemediationPinsExactDraftAndVersion(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "整改提交回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := st.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。")); err != nil {
		t.Fatal(err)
	}
	remediationID, err := newID("rem_")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	now := stamp()
	remediation := Remediation{
		ID: remediationID, CaseID: caseID, RunID: accepted.RunID, AttemptID: accepted.AttemptID,
		FindingIDs: []string{"finding_fixture"}, Measure: "替换不一致条款", Status: "open", Version: 1,
		DocumentIDs: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	wrapper := remediationRecord{Record: remediation}
	if err := st.updateAs(admin, func(next *state) error {
		item, ok := next.caseByID(caseID)
		if !ok {
			return errors.New("business case disappeared")
		}
		wrapper.TenantID, wrapper.OrgID = item.TenantID, item.OwnerOrgID
		wrapper.History = appendRemediationRevision(nil, remediation, now)
		next.Remediations = append(next.Remediations, wrapper)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SubmitRemediation(admin, caseID, remediationID, RemediationSubmitInput{
		DocumentIDs: []string{"not-the-current-draft"}, Reason: "提交修改稿", ExpectedVersion: 1,
	}); err == nil {
		t.Fatal("submission accepted a draft outside the case")
	}
	input := RemediationSubmitInput{
		DocumentIDs: []string{generation.DocumentID}, Reason: "提交已生成修改稿", ExpectedVersion: 1,
	}
	submitted, err := st.SubmitRemediation(admin, caseID, remediationID, input)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Status != "submitted" || submitted.Version != 2 || len(submitted.DocumentIDs) != 1 ||
		submitted.DocumentIDs[0] != generation.DocumentID {
		t.Fatalf("submission did not pin the selected draft: %+v", submitted)
	}
	input.DocumentIDs[0] = "caller-mutated"
	submitted.DocumentIDs[0] = "response-mutated"
	stored := st.snapshot()
	var persisted remediationRecord
	for _, item := range stored.Remediations {
		if item.Record.ID == remediationID {
			persisted = item
			break
		}
	}
	if persisted.Record.ID == "" || persisted.Record.DocumentIDs[0] != generation.DocumentID ||
		len(persisted.History) != 2 || persisted.History[1].Record.DocumentIDs[0] != generation.DocumentID {
		t.Fatalf("caller changes rewrote the immutable submitted draft identity: %+v", persisted)
	}
	if _, err := st.SubmitRemediation(admin, caseID, remediationID, RemediationSubmitInput{
		DocumentIDs: []string{generation.DocumentID}, Reason: "旧版本重提", ExpectedVersion: 1,
	}); err == nil {
		t.Fatal("stale remediation version was accepted")
	}
}

// insertLegacyTelecomReportAttempt reconstructs a pending pre-governance run.
// Its empty policy and RulePack identities are inserted at creation; no frozen
// snapshot or input is rewritten to simulate historical evidence.
func TestGenerationIdempotencySeparatesUploadOperation(t *testing.T) {
	ctx := context.Background()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCaseWithBusiness(admin, "生成幂等操作隔离", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	fields := validTelecomTemplateFields()
	options := MaterialUploadOptions{IdempotencyKey: "shared-operation-key"}
	first, err := st.GenerateWithOptions(ctx, admin, caseID, telecomTemplateID, fields, options)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := st.GenerateWithOptions(ctx, admin, caseID, telecomTemplateID, fields, options)
	if err != nil || !replay.Replayed || replay.DocumentID != first.DocumentID || replay.MaterialID != first.MaterialID || replay.Version != first.Version {
		t.Fatalf("generation retry did not return its original result: first=%+v replay=%+v err=%v", first, replay, err)
	}
	filename, _ := businessDraftIdentity(telecomTemplateID)
	body, err := renderBusinessTemplate(telecomTemplateID, fields)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UploadMaterial(ctx, admin, caseID, "draft_contract", filename, "text/plain", body, options)
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != "conflict" {
		t.Fatalf("cross-operation key reuse should return 409 instead of generation replay failure: %v", err)
	}
}

func insertLegacyTelecomReportAttempt(t *testing.T, st *Store, user accounts.User, caseID string) string {
	t.Helper()
	ctx := context.Background()
	seen, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	caseItem, ok := seen.caseByID(caseID)
	if !ok || !isTelecomDedicatedLineCase(caseItem) {
		t.Fatal("historical telecom case is unavailable")
	}
	documents := currentDocuments(seen.documents(caseID))
	if len(documents) < 2 {
		t.Fatal("historical telecom fixture needs two documents")
	}
	frozen, selections, err := st.acceptanceParsePlan(ctx, seen, documents, st.models)
	if err != nil {
		t.Fatal(err)
	}
	signature := st.currentSignatureWithRulePack(documentIDs(documents), frozen, nil, telecomCompilerRefForCase(caseItem), "")
	runID, err := newID("run_")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := newID("att_")
	if err != nil {
		t.Fatal(err)
	}
	acceptedAt := stamp()
	attempt := Attempt{ID: attemptID, RunID: runID, Ordinal: 1, ActorID: user.ID, State: "accepted", AcceptedAt: acceptedAt, HistoryKind: "preparing"}
	if !st.reserveExecution() {
		t.Fatal("could not reserve historical fixture execution")
	}
	err = st.updateCaseAs(user, caseID, func(next *state) error {
		if !sameDocumentSet(documentIDs(currentDocuments(next.documents(caseID))), signature.DocumentIDs) {
			return errors.New("historical fixture documents changed")
		}
		next.Runs = append(next.Runs, Run{ID: runID, CaseID: caseID, State: "accepted", ModelRef: signature.ModelRef, CreatedAt: acceptedAt, UpdatedAt: acceptedAt})
		if err := sealSnapshot(next, runID, signature); err != nil {
			return err
		}
		next.Attempts = append(next.Attempts, attempt)
		for _, selection := range selections {
			selection.RunID, selection.AttemptID = runID, attemptID
			next.AttemptParseSelections = append(next.AttemptParseSelections, selection)
		}
		next.ExecutionEvents = append(next.ExecutionEvents, acceptedEvent(attempt))
		appendRunEvent(next, runID, "accepted")
		return appendAudit(next, runID, user.ID, "review_started", []string{caseID})
	})
	if err != nil {
		st.releaseExecution()
		t.Fatal(err)
	}
	st.launchAttempt(runID, attemptID, st.models, caseItem, user.ID)
	return runID
}

func TestCreateReportFreezesAttemptAndDecisionBytes(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(admin, "专线授权不足报告", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。")); err != nil {
		t.Fatal(err)
	}
	runID := insertLegacyTelecomReportAttempt(t, st, admin, caseID)
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || len(workspace.Findings) == 0 {
		t.Fatalf("report test did not produce a terminal attempt and finding: %+v", workspace)
	}
	if workspace.Snapshot.GovernedKnowledge != nil || len(workspace.Execution.Attempt.RulePackSnapshot) != 0 || workspace.Execution.Attempt.InputHash == nil {
		t.Fatalf("historical fixture unexpectedly acquired governed evidence: %+v", workspace.Execution.Attempt)
	}
	legacySnapshot := workspace.Snapshot
	legacyInputHash := *workspace.Execution.Attempt.InputHash
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	restored, err := st.Workspace(admin, caseID, runID, workspace.Execution.Attempt.ID)
	if err != nil || !reflect.DeepEqual(restored.Snapshot, legacySnapshot) || restored.Execution == nil || restored.Execution.Attempt == nil || restored.Execution.Attempt.InputHash == nil || *restored.Execution.Attempt.InputHash != legacyInputHash {
		t.Fatalf("publication changed the historical run or its frozen input: workspace=%+v err=%v", restored, err)
	}
	mandatoryResults := 0
	for _, evaluation := range workspace.Evaluations {
		if evaluation.Mandatory {
			mandatoryResults++
		}
	}
	if mandatoryResults != 3 {
		t.Fatalf("report requires all three telecom mandatory rules, got %d", mandatoryResults)
	}
	attemptID := workspace.Execution.Attempt.ID
	first, err := st.CreateReport(admin, caseID, runID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	_, originalJSON, err := st.ReadReport(admin, caseID, first.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(originalJSON, []byte(`"availability":"unavailable"`)) ||
		!bytes.Contains(originalJSON, []byte(`"snapshot":null`)) ||
		!bytes.Contains(originalJSON, []byte(`"unavailable_reason":"此尝试未冻结可用 RulePack`)) ||
		!bytes.Contains(originalJSON, []byte("contractual_authority_missing")) {
		t.Fatalf("report omitted the honest unavailable RulePack evidence and reason: %s", originalJSON)
	}
	if !bytes.Contains(originalJSON, []byte("不构成法律意见或业务审批")) {
		t.Fatal("report omitted the legal and business approval boundary")
	}
	_, originalHTML, err := st.ReadReport(admin, caseID, first.ID, "html")
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, secondHTML := []byte(nil), []byte(nil)
	finding := workspace.Findings[0]
	decisionReason := "按实际规则复核报告版本"
	if err := st.Decide(admin, caseID, runID, finding.ID, "freeze-report-decision", "insufficient_evidence", decisionReason, finding.Version); err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateReport(admin, caseID, runID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != first.Version+1 || second.InputHash != first.InputHash || second.OutputHash != first.OutputHash ||
		second.DecisionSnapshotHash == first.DecisionSnapshotHash || second.Digest == first.Digest {
		t.Fatalf("new decision did not produce a separately versioned report snapshot: first=%+v second=%+v", first, second)
	}
	_, preservedJSON, err := st.ReadReport(admin, caseID, first.ID, "json")
	if err != nil || !bytes.Equal(preservedJSON, originalJSON) {
		t.Fatalf("new decision rewrote old report JSON: err=%v", err)
	}
	_, preservedHTML, err := st.ReadReport(admin, caseID, first.ID, "html")
	if err != nil || !bytes.Equal(preservedHTML, originalHTML) {
		t.Fatalf("new decision rewrote old report HTML: err=%v", err)
	}
	_, nextJSON, err := st.ReadReport(admin, caseID, second.ID, "json")
	if err != nil || !bytes.Contains(nextJSON, []byte(decisionReason)) {
		t.Fatalf("new report omitted the actual human decision: err=%v", err)
	}
	_, secondJSON, err = st.ReadReport(admin, caseID, second.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	_, secondHTML, err = st.ReadReport(admin, caseID, second.ID, "html")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(admin, caseID, "demo-reviewer"); err != nil {
		t.Fatal(err)
	}
	created, replayed, err := st.CreateCaseSubject(context.Background(), admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier,
		Subject: SubjectCreateInput{DisplayName: "报告冻结前的历史主体"}}, "report-subject-create")
	if err != nil || replayed || created.SubjectID == "" {
		t.Fatalf("create subject before report-preservation change: %+v replayed=%v err=%v", created, replayed, err)
	}
	current, err := st.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var source Document
	for _, item := range current.Documents {
		if item.CaseID == caseID && item.Role == "procurement" {
			source = item
			break
		}
	}
	if source.ID == "" {
		t.Fatal("procurement evidence for the subject correction is missing")
	}
	parsed, err := st.ensureParsed(context.Background(), admin, source, false)
	if err != nil || parsed.Status != "ready" {
		t.Fatalf("parse report subject evidence: parsed=%+v err=%v", parsed, err)
	}
	var materialVersion MaterialVersion
	for _, item := range current.MaterialVersions {
		if item.DocumentID == source.ID && item.CaseID == caseID {
			materialVersion = item
			break
		}
	}
	if materialVersion.MaterialID == "" {
		t.Fatal("pinned material version for report evidence is missing")
	}
	quote := []rune(parsed.Text)
	quoteDigest := sha256.Sum256([]byte(string(quote)))
	basis, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: materialVersion.MaterialID, MaterialVersion: materialVersion.Version,
		DocumentID: source.ID, ParsedDocumentID: parsed.ID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(quoteDigest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, replayed, err := st.ProposeSubjectChange(context.Background(), admin, caseID, SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: created.SubjectID},
		ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"display_name":"报告审批后的新主体"}`), BasisNote: "依据已冻结材料更正主体名称", BasisRefs: basis}, "report-subject-correction")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("propose source-backed subject change: %+v replayed=%v err=%v", proposal, replayed, err)
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	impact, err := st.GetSubjectChangeImpact(context.Background(), reviewer, caseID, proposal.RequestID)
	if err != nil || !impact.CanDecide {
		t.Fatalf("independent reviewer could not see subject change: %+v err=%v", impact, err)
	}
	decision, replayed, err := st.DecideSubjectChange(context.Background(), reviewer, caseID, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1,
		ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "复核主体更正依据"}, "report-subject-correction-decision")
	if err != nil || replayed || decision.Decision != "approved" || decision.AppliedSubjectVersion == nil || *decision.AppliedSubjectVersion != 2 {
		t.Fatalf("independent approver did not apply subject correction: %+v replayed=%v err=%v", decision, replayed, err)
	}
	governedRunID, err := st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	governed := waitTerminalWorkspace(t, st, admin, caseID, governedRunID)
	if governedRunID == runID || governed.Snapshot.GovernedKnowledge == nil || governed.Snapshot.RulePackID != pack.Pack.ID {
		t.Fatalf("new review did not freeze published governance: %+v", governed.Snapshot)
	}
	_, afterGovernedJSON, err := st.ReadReport(admin, caseID, first.ID, "json")
	if err != nil || !bytes.Equal(afterGovernedJSON, originalJSON) {
		t.Fatalf("governed review rewrote historical report JSON: %v", err)
	}
	firstAfter, firstAfterJSON, err := st.ReadReport(admin, caseID, first.ID, "json")
	if err != nil || firstAfter.Digest != first.Digest || firstAfter.InputHash != first.InputHash || firstAfter.OutputHash != first.OutputHash || !bytes.Equal(firstAfterJSON, originalJSON) {
		t.Fatalf("approved subject and new review changed the original report record/JSON: report=%+v err=%v", firstAfter, err)
	}
	_, firstAfterHTML, err := st.ReadReport(admin, caseID, first.ID, "html")
	if err != nil || !bytes.Equal(firstAfterHTML, originalHTML) {
		t.Fatalf("approved subject and new review changed the original report HTML: %v", err)
	}
	secondAfter, secondAfterJSON, err := st.ReadReport(admin, caseID, second.ID, "json")
	if err != nil || secondAfter.Digest != second.Digest || !bytes.Equal(secondAfterJSON, secondJSON) {
		t.Fatalf("approved subject and new review changed the decision-version report/JSON: report=%+v err=%v", secondAfter, err)
	}
	_, secondAfterHTML, err := st.ReadReport(admin, caseID, second.ID, "html")
	if err != nil || !bytes.Equal(secondAfterHTML, secondHTML) {
		t.Fatalf("approved subject and new review changed the decision-version report HTML: %v", err)
	}
}

func TestRemediationCannotCloseOnSameOrUncomparableAttempt(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	assertConflict := func(err error, reason string) {
		t.Helper()
		var apiErr *fault.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 409 || !strings.Contains(apiErr.Message, reason) {
			t.Fatalf("incomparable remediation verification returned %v, want conflict containing %q", err, reason)
		}
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(admin, "授权不足整改回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := st.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供合同依据。")); err != nil {
		t.Fatal(err)
	}
	firstRunID, err := st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	firstWorkspace := waitTerminalWorkspace(t, st, admin, caseID, firstRunID)
	if firstWorkspace.Execution == nil || firstWorkspace.Execution.Attempt == nil {
		t.Fatal("first review did not produce a terminal attempt")
	}
	var target Finding
	for _, finding := range firstWorkspace.Findings {
		if finding.CapabilityID == "dedicated_line.minimum_bandwidth" && finding.Relation == "insufficient" {
			target = finding
			break
		}
	}
	if target.ID == "" {
		t.Fatalf("review did not retain the insufficient bandwidth finding: %+v", firstWorkspace.Findings)
	}
	created, err := st.CreateRemediation(admin, caseID, RemediationInput{
		RunID: firstRunID, AttemptID: firstWorkspace.Execution.Attempt.ID,
		FindingIDs: []string{target.ID}, Measure: "补充可核验的合同依据并重新审查",
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := st.SubmitRemediation(admin, caseID, created.ID, RemediationSubmitInput{
		DocumentIDs: []string{draft.DocumentID}, Reason: "提交修改稿", ExpectedVersion: created.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, firstVerifyErr := st.VerifyRemediation(admin, caseID, created.ID, RemediationVerifyInput{
		RunID: firstRunID, AttemptID: firstWorkspace.Execution.Attempt.ID,
		Reason: "同一尝试不得关闭整改", ExpectedVersion: submitted.Version,
	})
	if firstVerifyErr == nil {
		t.Fatal("the original attempt closed its own remediation")
	}
	assertConflict(firstVerifyErr, "已完成或部分完成的新审查执行记录")
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Remediations) != 1 || detail.Remediations[0].Status != "reopened" || detail.Remediations[0].Version != submitted.Version+1 {
		t.Fatalf("same-attempt verification did not reopen the remediation: %+v", detail.Remediations)
	}
	submitted, err = st.SubmitRemediation(admin, caseID, created.ID, RemediationSubmitInput{
		DocumentIDs: []string{draft.DocumentID}, Reason: "重新提交修改稿", ExpectedVersion: detail.Remediations[0].Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A repeated StartReview with an unchanged configuration reuses the existing
	// run. A comparable second attempt has to come from an explicit retry of the
	// same run, which appends a frozen terminal attempt for the same inputs.
	if err := st.Retry(context.Background(), admin, caseID, firstRunID); err != nil {
		t.Fatal(err)
	}
	secondWorkspace := firstWorkspace
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		secondWorkspace, err = st.Workspace(admin, caseID, firstRunID)
		if err != nil {
			t.Fatal(err)
		}
		if secondWorkspace.Execution != nil && secondWorkspace.Execution.Attempt != nil &&
			secondWorkspace.Execution.Attempt.ID != firstWorkspace.Execution.Attempt.ID && secondWorkspace.Execution.Attempt.FinishedAt != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if secondWorkspace.Execution == nil || secondWorkspace.Execution.Attempt == nil ||
		secondWorkspace.Execution.Attempt.ID == firstWorkspace.Execution.Attempt.ID {
		t.Fatalf("retry did not create a new terminal attempt: %+v", secondWorkspace.Execution)
	}
	_, secondVerifyErr := st.VerifyRemediation(admin, caseID, created.ID, RemediationVerifyInput{
		RunID: firstRunID, AttemptID: secondWorkspace.Execution.Attempt.ID,
		Reason: "新尝试仍缺少可比较的授权规则证据", ExpectedVersion: submitted.Version,
	})
	if secondVerifyErr == nil {
		t.Fatal("an attempt without comparable authority evidence closed the remediation")
	}
	assertConflict(secondVerifyErr, "规则包检查项目")
	detail, err = st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Remediations) != 1 || detail.Remediations[0].Status != "reopened" ||
		detail.Remediations[0].VerificationRunID == nil || *detail.Remediations[0].VerificationRunID != firstRunID {
		t.Fatalf("incomparable verification was not retained as an open remediation revision: %+v", detail.Remediations)
	}
}

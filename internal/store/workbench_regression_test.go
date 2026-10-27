package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"contractaudit/internal/accounts"
)

func TestWorkbenchProfileAndTaskSurviveUnrelatedWritesAndReopen(t *testing.T) {
	st, owner := openExecutionStore(t)
	caseID, err := st.CreateCase(owner, "工作台资料持久化回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.UpdateCaseProfile(owner, caseID, CaseProfileInput{
		Number: "WB-2026-001", Customer: "采购方", Supplier: "云服务商", ContractCategory: "云服务",
		OwnerID: owner.ID, ExpectedVersion: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Version != 1 {
		t.Fatalf("initial profile version = %d, want 1", profile.Version)
	}
	if _, err := st.AddDocument(context.Background(), owner, caseID, "procurement", "采购要求.txt", "text/plain", []byte("最低带宽为1000 Mbit/s。")); err != nil {
		t.Fatalf("upload after profile: %v", err)
	}
	if _, err := st.UpdateCaseProfile(owner, caseID, CaseProfileInput{
		Number: profile.Number, Customer: profile.Customer, Supplier: profile.Supplier, ContractCategory: profile.ContractCategory,
		OwnerID: profile.OwnerID, ExpectedVersion: 0,
	}); err == nil {
		t.Fatal("stale profile version was accepted")
	}
	task, err := st.CreateWorkbenchTask(owner, caseID, WorkbenchTaskInput{
		Title: "复核云服务依据", Kind: "review", AssigneeID: owner.ID, Status: "open", Priority: "high",
	})
	if err != nil {
		t.Fatalf("create task after profile and document: %v", err)
	}
	if task.CreatedBy == nil || *task.CreatedBy != owner.ID {
		t.Fatalf("task creator = %v, want %q", task.CreatedBy, owner.ID)
	}
	if _, err := st.AddDocument(context.Background(), owner, caseID, "draft_contract", "合同草稿.txt", "text/plain", []byte("服务合同草稿。")); err != nil {
		t.Fatalf("upload after task: %v", err)
	}
	task, err = st.UpdateWorkbenchTask(owner, caseID, task.ID, WorkbenchTaskInput{
		Title: "复核云服务依据", Kind: "review", AssigneeID: owner.ID, Status: "in_progress", Priority: "high",
		ExpectedVersion: task.Version,
	})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if task.CreatedBy == nil || *task.CreatedBy != owner.ID {
		t.Fatalf("task creator changed on update: %v", task.CreatedBy)
	}
	if _, err := st.UpdateWorkbenchTask(owner, caseID, task.ID, WorkbenchTaskInput{
		Title: "过期任务版本", Kind: "review", AssigneeID: owner.ID, Status: "completed", Priority: "high",
		ExpectedVersion: 1,
	}); err == nil {
		t.Fatal("stale task version was accepted")
	}
	relatedCaseID, err := st.CreateCase(owner, "关联案件持久化回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	relation, err := st.CreateCaseRelation(owner, caseID, CaseRelationInput{RelatedCaseID: relatedCaseID, Reason: "同一采购项目"})
	if err != nil {
		t.Fatalf("create relation: %v", err)
	}
	relatedCaseID2, err := st.CreateCase(owner, "另一关联案件持久化回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	relation2, err := st.CreateCaseRelation(owner, caseID, CaseRelationInput{RelatedCaseID: relatedCaseID2, Reason: "同一采购项目的补充合同"})
	if err != nil {
		t.Fatalf("create second relation: %v", err)
	}
	knowledge, err := st.CreateKnowledge(owner, KnowledgeInput{
		ID: "cloud-procurement-policy", Title: "云服务采购审查制度", Version: "1.0.0",
		Body: "云服务审查使用本地合成采购制度。", SourceType: "synthetic_policy",
	})
	if err != nil {
		t.Fatalf("create knowledge: %v", err)
	}
	newer, err := st.CreateKnowledge(owner, KnowledgeInput{
		ID: knowledge.ID, Title: knowledge.Title, Version: "2.0.0",
		Body: "第二版采购制度正文。", SourceType: "synthetic_policy",
	})
	if err != nil {
		t.Fatalf("create newer knowledge version: %v", err)
	}
	if knowledge.VersionID == "" || newer.VersionID == "" || knowledge.VersionID == newer.VersionID {
		t.Fatalf("knowledge versions lack distinct identities: %+v %+v", knowledge, newer)
	}

	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	snapshot, err := reopened.WorkbenchSnapshot(context.Background(), owner, "week", WorkbenchModel{})
	if err != nil {
		t.Fatalf("read reopened workbench: %v", err)
	}
	if len(snapshot.Profiles) != 1 || snapshot.Profiles[0].CaseID != caseID || snapshot.Profiles[0].Version != 1 {
		t.Fatalf("reopened profile = %+v", snapshot.Profiles)
	}
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].ID != task.ID || snapshot.Tasks[0].Version != 2 ||
		snapshot.Tasks[0].CreatedBy == nil || *snapshot.Tasks[0].CreatedBy != owner.ID {
		t.Fatalf("reopened task = %+v", snapshot.Tasks)
	}
	if len(snapshot.Documents) != 2 {
		t.Fatalf("reopened documents = %d, want 2", len(snapshot.Documents))
	}
	if len(snapshot.Relations) != 2 {
		t.Fatalf("unrelated knowledge write lost a case relation: %+v", snapshot.Relations)
	}
	relationIDs := map[string]bool{}
	for _, item := range snapshot.Relations {
		relationIDs[item.ID] = true
	}
	if !relationIDs[relation.ID] || !relationIDs[relation2.ID] {
		t.Fatalf("reopened relations = %+v, want %q and %q", snapshot.Relations, relation.ID, relation2.ID)
	}
	if len(snapshot.Knowledge) != 2 {
		t.Fatalf("reopened knowledge = %+v", snapshot.Knowledge)
	}
	versions := map[string]KnowledgeView{}
	for _, item := range snapshot.Knowledge {
		versions[item.VersionID] = item
	}
	if !reflect.DeepEqual(versions[knowledge.VersionID], knowledge) || !reflect.DeepEqual(versions[newer.VersionID], newer) {
		t.Fatalf("reopened workbench changed exact versions: %+v", snapshot.Knowledge)
	}
	for _, expected := range []KnowledgeView{knowledge, newer} {
		actual, err := reopened.GetKnowledgeVersion(owner, expected.ID, expected.VersionID)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("reopened exact version: got=%+v want=%+v err=%v", actual, expected, err)
		}
	}
	platformAuditFound := false
	for _, event := range snapshot.Audit {
		if event.Action == "knowledge.created" {
			platformAuditFound = true
			if event.CaseID != nil || event.CaseTitle != nil {
				t.Fatalf("platform knowledge audit has a fabricated case link: %+v", event)
			}
		}
	}
	if !platformAuditFound {
		t.Fatal("knowledge creation audit was not persisted")
	}
	readKnowledge, err := reopened.GetKnowledge(owner, knowledge.ID)
	if err != nil || readKnowledge.ID != knowledge.ID {
		t.Fatalf("read reopened knowledge = %+v, err=%v", readKnowledge, err)
	}
	if err := reopened.DeleteCaseRelation(owner, caseID, relation.ID); err != nil {
		t.Fatalf("delete relation: %v", err)
	}
	snapshot, err = reopened.WorkbenchSnapshot(context.Background(), owner, "week", WorkbenchModel{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Relations) != 1 || snapshot.Relations[0].ID != relation2.ID {
		t.Fatalf("deleting one relation removed or retained the wrong rows: %+v", snapshot.Relations)
	}
	deletedAudit := false
	for _, event := range snapshot.Audit {
		deletedAudit = deletedAudit || event.Action == "case.relation.deleted"
	}
	if !deletedAudit {
		t.Fatal("relation deletion audit was not persisted")
	}
}

func TestWorkbenchDocumentUsesPublicParsedMetadataProjection(t *testing.T) {
	document := Document{
		ID: "doc-1", TenantID: "tenant-private", OrgID: "org-private", CaseID: "case-1", Role: "draft_contract",
		Filename: "合同.txt", MediaType: "text/plain", Digest: "original-digest", Size: 12, CreatedAt: "2026-10-24T10:00:00Z",
		StorageBackend: "private-backend",
	}
	parsed := ParsedDocument{
		ID: "parsed-1", SelectionOrder: 4, DocumentID: document.ID, OriginalDigest: document.Digest,
		ParserID: "plain-text", ParserVersion: "1", ConfigHash: "config-hash", NormalizationVersion: "normalization-v1",
		NormalizedDigest: "normalized-digest", IRDigest: "ir-digest", ArtifactDigest: "artifact-digest",
		CreatedAt: "2026-10-24T10:01:00Z", Status: "ready", Text: "正文",
	}
	items := workbenchDocumentsForCase(state{Documents: []Document{document}, ParsedDocuments: []ParsedDocument{parsed}}, Case{ID: "case-1", Title: "合同审查"})
	if len(items) != 1 || items[0].ParsedDocument == nil || items[0].ParseStatus != "ready" {
		t.Fatalf("unexpected workbench document projection: %+v", items)
	}
	if items[0].ParsedDocument.DocumentID != document.ID || items[0].ParsedDocument.OriginalDigest != document.Digest ||
		items[0].ParsedDocument.NormalizedDigest == nil || *items[0].ParsedDocument.NormalizedDigest != parsed.NormalizedDigest {
		t.Fatalf("parsed metadata lost document identity: %+v", items[0].ParsedDocument)
	}
	body, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	metadata, ok := payload["parsed_document"].(map[string]any)
	if !ok || metadata["id"] != parsed.ID || metadata["normalized_digest"] != parsed.NormalizedDigest || payload["parse_status"] != "ready" {
		t.Fatalf("public document metadata shape mismatch: %s", body)
	}
	for _, privateField := range []string{"tenant_id", "org_id", "storage_backend", "parsed_document_id", "normalized_text_digest"} {
		if _, exists := payload[privateField]; exists {
			t.Fatalf("workbench document exposed unexpected field %q: %s", privateField, body)
		}
	}
	if _, exists := metadata["text"]; exists {
		t.Fatalf("workbench returned parsed body in metadata: %s", body)
	}
}

func TestWorkbenchOperationsRespectCaseAccessAndReadOnly(t *testing.T) {
	st, owner := openExecutionStore(t)
	caseID, err := st.CreateCase(owner, "工作台权限回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("demo viewer missing")
	}
	input := CaseProfileInput{
		Number: "WB-AUTH-001", Customer: "采购方", Supplier: "服务商", ContractCategory: "云服务",
		OwnerID: owner.ID, ExpectedVersion: 0,
	}
	if _, err := st.UpdateCaseProfile(viewer, caseID, input); err == nil {
		t.Fatal("nonmember viewer updated a case profile")
	}
	snapshot, err := st.WorkbenchSnapshot(context.Background(), viewer, "week", WorkbenchModel{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range snapshot.Cases {
		if item.ID == caseID {
			t.Fatal("nonmember viewer saw the protected case")
		}
	}
	if _, err := st.UpdateCaseProfile(owner, caseID, input); err != nil {
		t.Fatalf("case owner could not create profile: %v", err)
	}
	st.UseReadOnly(true)
	input.ExpectedVersion = 1
	if _, err := st.UpdateCaseProfile(owner, caseID, input); err == nil {
		t.Fatal("read-only store updated a profile")
	}
	if _, err := st.CreateWorkbenchTask(owner, caseID, WorkbenchTaskInput{
		Title: "只读任务", Kind: "general", AssigneeID: owner.ID, Status: "open", Priority: "medium",
	}); err == nil {
		t.Fatal("read-only store created a task")
	}
}

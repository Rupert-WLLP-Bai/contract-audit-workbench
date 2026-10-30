package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestMaterialMigrationUploadReplaceAndRerunPreservesEntityHistory(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCase(user, "材料迁移重跑", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.UploadMaterial(context.Background(), user, caseID, "draft_contract", "v1.txt", "text/plain", []byte("主合同第一版"), MaterialUploadOptions{IdempotencyKey: "upload-v1"})
	if err != nil {
		t.Fatal(err)
	}
	oldID := first.DocumentID
	replay, err := st.UploadMaterial(context.Background(), user, caseID, "draft_contract", "v1.txt", "text/plain", []byte("主合同第一版"), MaterialUploadOptions{IdempotencyKey: "upload-v1"})
	if err != nil || !replay.Replayed || replay.DocumentID != oldID || replay.Version != 1 {
		t.Fatalf("upload retry was not idempotent: %+v %v", replay, err)
	}
	if _, err := st.UploadMaterial(context.Background(), user, caseID, "draft_contract", "v1.txt", "text/plain", []byte("different bytes"), MaterialUploadOptions{IdempotencyKey: "upload-v1"}); !isMaterialConflict(err) {
		t.Fatalf("changed payload should return 409 conflict, got %v", err)
	}
	materialID, version, err := st.MaterialIdentity(context.Background(), user, caseID, oldID)
	if err != nil || version != 1 {
		t.Fatalf("first material identity=(%q,%d,%v)", materialID, version, err)
	}
	newID, err := st.AddDocumentWithOptions(context.Background(), user, caseID, "draft_contract", "v2.txt", "text/plain", []byte("主合同第二版"), MaterialUploadOptions{Intent: "replace", MaterialID: materialID, ReplacesDocumentID: oldID, IdempotencyKey: "upload-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateMaterials(context.Background(), st.pool); err != nil {
		t.Fatalf("repeat material migration: %v", err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	identity, currentVersion, err := reopened.MaterialIdentity(context.Background(), user, caseID, newID)
	if err != nil || identity != materialID || currentVersion != 2 {
		t.Fatalf("after rerun identity=(%q,%d,%v)", identity, currentVersion, err)
	}
	state, err := reopened.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	var versions []MaterialVersion
	for _, item := range state.MaterialVersions {
		if item.CaseID == caseID && item.MaterialID == materialID {
			versions = append(versions, item)
		}
	}
	if len(versions) != 2 || versions[0].DocumentID != oldID || versions[1].DocumentID != newID {
		t.Fatalf("material history changed or duplicated after migration: %+v", versions)
	}
	for _, item := range state.Materials {
		if item.CaseID == caseID && item.ID == "legacy_"+newID {
			t.Fatalf("native document was reclassified as legacy: %+v", item)
		}
	}
}

func TestLegacyMigrationPreservesFrozenEvidenceAndSelection(t *testing.T) {
	ctx := context.Background()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(user, "旧材料迁移证据冻结", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	firstDraft, err := st.AddDocument(context.Background(), user, caseID, "draft_contract", "old-draft-a.txt", "text/plain", []byte("旧主合同 A"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)
	secondDraft, err := st.AddDocument(context.Background(), user, caseID, "draft_contract", "old-draft-b.txt", "text/plain", []byte("旧主合同 B"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)
	procurement, err := st.AddDocument(context.Background(), user, caseID, "procurement", "old-purchase.txt", "text/plain", []byte("旧采购文件"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)

	runID := insertLegacyTelecomReportAttempt(t, st, user, caseID)
	workspace := waitTerminalWorkspace(t, st, user, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || len(workspace.Findings) == 0 {
		t.Fatalf("legacy fixture lacks a frozen attempt and findings: %+v", workspace)
	}
	if err := st.Decide(user, caseID, runID, workspace.Findings[0].ID, "migration-legacy-decision", "confirm_risk", "迁移前已核对冻结原文", workspace.Findings[0].Version); err != nil {
		t.Fatal(err)
	}
	report, err := st.CreateReport(user, caseID, runID, workspace.Execution.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, reportJSON, err := st.ReadReport(user, caseID, report.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	_, reportHTML, err := st.ReadReport(user, caseID, report.ID, "html")
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	beforeFrozen := frozenMaterialEvidence(t, before, caseID)

	if err := migrateMaterials(ctx, st.pool); err != nil {
		t.Fatalf("migrate legacy documents: %v", err)
	}
	after, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	if got := frozenMaterialEvidence(t, after, caseID); !bytes.Equal(got, beforeFrozen) {
		t.Fatal("material migration changed historical attempt/input/parse/decision/report records")
	}
	_, afterJSON, err := st.ReadReport(user, caseID, report.ID, "json")
	if err != nil || !bytes.Equal(afterJSON, reportJSON) {
		t.Fatalf("migration changed frozen report JSON: %v", err)
	}
	_, afterHTML, err := st.ReadReport(user, caseID, report.ID, "html")
	if err != nil || !bytes.Equal(afterHTML, reportHTML) {
		t.Fatalf("migration changed frozen report HTML: %v", err)
	}
	var firstMaterial, secondMaterial string
	legacySelected := map[string]bool{}
	for _, material := range after.Materials {
		if material.CaseID != caseID {
			continue
		}
		if material.CurrentDocumentID == firstDraft {
			firstMaterial = material.ID
			if material.LegacySelected {
				t.Fatal("older same-role draft was marked legacy selected")
			}
		}
		if material.CurrentDocumentID == secondDraft {
			secondMaterial = material.ID
			legacySelected[secondDraft] = material.LegacySelected
		}
		if material.CurrentDocumentID == procurement {
			legacySelected[procurement] = material.LegacySelected
		}
	}
	if firstMaterial == "" || secondMaterial == "" || firstMaterial == secondMaterial || !legacySelected[secondDraft] || !legacySelected[procurement] {
		t.Fatalf("legacy docs did not receive independent entities and latest-role selection: materials=%+v", after.Materials)
	}
	for _, version := range after.MaterialVersions {
		if version.CaseID == caseID && (version.DocumentID == firstDraft || version.DocumentID == secondDraft) && version.Version != 1 {
			t.Fatalf("legacy draft was incorrectly folded into a version chain: %+v", version)
		}
	}
	if _, err := st.StartReviewAt(ctx, user, caseID, acceptanceBusinessTime); !isMaterialConflict(err) {
		t.Fatalf("pending legacy selection should block new review with 409, got %v", err)
	}
	detail, err := st.GetCase(user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.MaterialSelection.Status != "pending_confirmation" || !sameDocumentSet(detail.MaterialSelection.CandidateDocumentIDs, []string{secondDraft, procurement}) || len(detail.MaterialSelection.DocumentIDs) != 0 {
		t.Fatalf("pending projection did not expose server-selected candidates separately from the confirmed set: %+v", detail.MaterialSelection)
	}
	if err := st.ConfirmMaterialSelection(ctx, user, caseID, detail.MaterialSelection.Version, []string{firstDraft, procurement}); err != nil {
		t.Fatalf("explicit legacy set confirmation: %v", err)
	}
	confirmed, err := st.GetCase(user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.MaterialSelection.PrimaryMaterialID == nil || *confirmed.MaterialSelection.PrimaryMaterialID != firstMaterial {
		t.Fatalf("explicitly chosen legacy primary did not establish the chain: %+v", confirmed.MaterialSelection)
	}
	confirmedState, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	if currentDraft := currentPrimaryDraftID(confirmedState, caseID); currentDraft != firstDraft {
		t.Fatalf("remediation primary did not follow the explicitly chosen legacy material: got %q want %q", currentDraft, firstDraft)
	}
	remediation, err := st.CreateRemediation(user, caseID, RemediationInput{RunID: runID, AttemptID: workspace.Execution.Attempt.ID, FindingIDs: []string{workspace.Findings[0].ID}, AssigneeID: "demo-admin", Measure: "按显式选择的主合同完成修订"})
	if err != nil {
		t.Fatalf("create remediation for old frozen attempt: %v", err)
	}
	if _, err := st.SubmitRemediation(user, caseID, remediation.ID, RemediationSubmitInput{DocumentIDs: []string{firstDraft}, Reason: "提交已确认的主合同版本", ExpectedVersion: remediation.Version}); err != nil {
		t.Fatalf("submit explicitly selected non-latest-by-role primary: %v", err)
	}
	if err := st.ConfirmMaterialSelection(ctx, user, caseID, confirmed.MaterialSelection.Version, []string{secondDraft, procurement}); !isMaterialConflict(err) {
		t.Fatalf("switching to another old primary should return 409, got %v", err)
	}
	confirmed, err = st.GetCase(user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ConfirmMaterialSelection(ctx, user, caseID, confirmed.MaterialSelection.Version, []string{procurement}); err != nil {
		t.Fatalf("omitting the selected primary from the review collection should retain its chain identity: %v", err)
	}
	withoutPrimary, err := st.GetCase(user, caseID)
	if err != nil || withoutPrimary.MaterialSelection.PrimaryMaterialID == nil || *withoutPrimary.MaterialSelection.PrimaryMaterialID != firstMaterial {
		t.Fatalf("omitting primary cleared the stable chain identity: %+v err=%v", withoutPrimary.MaterialSelection, err)
	}
	if _, err := st.AddDocument(ctx, user, caseID, "draft_contract", "new-primary.txt", "text/plain", []byte("新的主合同")); !isMaterialConflict(err) {
		t.Fatalf("adding another primary after omitting the selected primary should return 409, got %v", err)
	}
	final, err := st.GetCase(user, caseID)
	if err != nil || final.MaterialSelection.PrimaryMaterialID == nil || *final.MaterialSelection.PrimaryMaterialID != firstMaterial {
		t.Fatalf("primary chain identity changed after rejected requests: %+v err=%v", final.MaterialSelection, err)
	}
}

func TestReviewSelectionKeepsSamePurposeMaterialsAcrossReplacement(t *testing.T) {
	ctx := context.Background()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCase(user, "同用途材料复审集合", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	primary, err := st.UploadMaterial(ctx, user, caseID, "draft_contract", "main.txt", "text/plain", []byte("主合同"), MaterialUploadOptions{IdempotencyKey: "set-primary"})
	if err != nil {
		t.Fatal(err)
	}
	attachmentA, err := st.UploadMaterial(ctx, user, caseID, "attachment", "supplement-a.txt", "text/plain", []byte("补充协议 A"), MaterialUploadOptions{Purpose: "supplement", IdempotencyKey: "set-a"})
	if err != nil {
		t.Fatal(err)
	}
	attachmentB, err := st.UploadMaterial(ctx, user, caseID, "attachment", "supplement-b.txt", "text/plain", []byte("补充协议 B"), MaterialUploadOptions{Purpose: "supplement", IdempotencyKey: "set-b"})
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := st.StartReview(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	waitTerminalWorkspace(t, st, user, caseID, firstRun)
	firstState, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	firstInput := attemptInputForRun(t, firstState, firstRun)
	if !sameDocumentSet(frozenAttemptDocumentIDs(firstInput), []string{primary.DocumentID, attachmentA.DocumentID, attachmentB.DocumentID}) {
		t.Fatalf("first attempt did not freeze all same-purpose materials: %+v", firstInput.Documents)
	}
	assertFrozenMaterial(t, firstInput, primary.DocumentID, primary.MaterialID, "draft_contract", "primary_contract", 1)
	assertFrozenMaterial(t, firstInput, attachmentA.DocumentID, attachmentA.MaterialID, "attachment", "supplement", 1)
	assertFrozenMaterial(t, firstInput, attachmentB.DocumentID, attachmentB.MaterialID, "attachment", "supplement", 1)
	replacement, err := st.UploadMaterial(ctx, user, caseID, "attachment", "supplement-a-v2.txt", "text/plain", []byte("补充协议 A 修订"), MaterialUploadOptions{Purpose: "supplement", Intent: "replace", MaterialID: attachmentA.MaterialID, ReplacesDocumentID: attachmentA.DocumentID, IdempotencyKey: "set-a-v2"})
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := st.StartReview(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	waitTerminalWorkspace(t, st, user, caseID, secondRun)
	if secondRun == firstRun {
		t.Fatal("replacement reused the old run instead of creating a new frozen attempt")
	}
	secondState, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := attemptInputForRun(t, secondState, secondRun)
	if !sameDocumentSet(frozenAttemptDocumentIDs(secondInput), []string{primary.DocumentID, replacement.DocumentID, attachmentB.DocumentID}) {
		t.Fatalf("new attempt did not retain attachment B while replacing A: %+v", secondInput.Documents)
	}
	assertFrozenMaterial(t, secondInput, replacement.DocumentID, attachmentA.MaterialID, "attachment", "supplement", 2)
	assertFrozenMaterial(t, secondInput, attachmentB.DocumentID, attachmentB.MaterialID, "attachment", "supplement", 1)
	if !sameDocumentSet(frozenAttemptDocumentIDs(attemptInputForRun(t, secondState, firstRun)), frozenAttemptDocumentIDs(firstInput)) {
		t.Fatal("replacement changed the old frozen attempt input")
	}
}

func TestLegacyPrimaryChainCannotBeReboundByReplacingUnselectedMaterial(t *testing.T) {
	t.Run("upload", func(t *testing.T) {
		ctx := context.Background()
		st, err := OpenTest(t.TempDir(), "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		user, ok := accounts.ByID("demo-admin")
		if !ok {
			t.Fatal("demo admin missing")
		}
		caseID, err := st.CreateCase(user, "锁定旧主合同实体", "procurement_ict")
		if err != nil {
			t.Fatal(err)
		}
		legacy := makeLegacyPrimaryPair(t, st, user, caseID)
		if err := migrateMaterials(ctx, st.pool); err != nil {
			t.Fatal(err)
		}
		detail, err := st.GetCase(user, caseID)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ConfirmMaterialSelection(ctx, user, caseID, detail.MaterialSelection.Version, []string{legacy.primary, legacy.procurement}); err != nil {
			t.Fatal(err)
		}
		unselectedID, version, err := st.MaterialIdentity(ctx, user, caseID, legacy.unselected)
		if err != nil || version != 1 {
			t.Fatalf("unselected legacy primary identity=(%q,%d,%v)", unselectedID, version, err)
		}
		before := st.snapshot()
		_, err = st.UploadMaterial(ctx, user, caseID, "draft_contract", "legacy-b-v2.txt", "text/plain", []byte("替换未选中的旧主合同 B"), MaterialUploadOptions{Intent: "replace", MaterialID: unselectedID, ReplacesDocumentID: legacy.unselected, IdempotencyKey: "replace-unselected-legacy-primary"})
		if !isMaterialConflict(err) {
			t.Fatalf("replacing an unselected legacy primary must not rebind the established chain: %v", err)
		}
		after := st.snapshot()
		if !reflect.DeepEqual(before.Materials, after.Materials) || !reflect.DeepEqual(before.MaterialVersions, after.MaterialVersions) || !reflect.DeepEqual(before.MaterialChains, after.MaterialChains) || !reflect.DeepEqual(before.Documents, after.Documents) {
			t.Fatal("rejected replacement changed material identity, chain, versions, or documents")
		}
	})

	t.Run("generated_contract", func(t *testing.T) {
		ctx := context.Background()
		st, err := OpenTest(t.TempDir(), "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		user, ok := accounts.ByID("demo-admin")
		if !ok {
			t.Fatal("demo admin missing")
		}
		caseID, err := st.CreateCaseWithBusiness(user, "生成不得改绑旧主链", "service", "ENT", "telecom_dedicated_line")
		if err != nil {
			t.Fatal(err)
		}
		legacy := makeLegacyPrimaryPair(t, st, user, caseID)
		if err := migrateMaterials(ctx, st.pool); err != nil {
			t.Fatal(err)
		}
		detail, err := st.GetCase(user, caseID)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ConfirmMaterialSelection(ctx, user, caseID, detail.MaterialSelection.Version, []string{legacy.primary, legacy.procurement}); err != nil {
			t.Fatal(err)
		}
		unselectedID, version, err := st.MaterialIdentity(ctx, user, caseID, legacy.unselected)
		if err != nil || version != 1 {
			t.Fatalf("unselected legacy primary identity=(%q,%d,%v)", unselectedID, version, err)
		}
		before := st.snapshot()
		options := MaterialUploadOptions{Intent: "replace", MaterialID: unselectedID, ReplacesDocumentID: legacy.unselected, IdempotencyKey: "generate-replace-unselected-legacy-primary"}
		_, err = st.GenerateWithOptions(ctx, user, caseID, telecomTemplateID, validTelecomTemplateFields(), options)
		if !isMaterialConflict(err) {
			t.Fatalf("generation must not rebind the established chain through an unselected legacy primary: %v", err)
		}
		after := st.snapshot()
		if !reflect.DeepEqual(before.Materials, after.Materials) || !reflect.DeepEqual(before.MaterialVersions, after.MaterialVersions) || !reflect.DeepEqual(before.MaterialChains, after.MaterialChains) || !reflect.DeepEqual(before.Documents, after.Documents) || !reflect.DeepEqual(before.Generations, after.Generations) {
			t.Fatal("rejected generation changed material identity, chain, versions, documents, or generation records")
		}
	})
}

func TestAddingFirstNativePrimaryRetainsLegacySelectedNonPrimaryMaterials(t *testing.T) {
	ctx := context.Background()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCase(user, "无旧主合同保留采购材料", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	procurement, err := st.AddDocument(ctx, user, caseID, "procurement", "old-procurement.txt", "text/plain", []byte("旧采购文件"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)
	if err := migrateMaterials(ctx, st.pool); err != nil {
		t.Fatal(err)
	}
	state := st.snapshot()
	var chain *CaseMaterialChain
	for i := range state.MaterialChains {
		if state.MaterialChains[i].CaseID == caseID {
			chain = &state.MaterialChains[i]
			break
		}
	}
	if chain == nil || chain.LegacySelectionStatus != "not_applicable" || !sameDocumentSet(chain.SelectedDocumentIDs, []string{procurement}) {
		t.Fatalf("legacy no-primary case should begin without a confirmed selection: %+v", chain)
	}
	primary, err := st.UploadMaterial(ctx, user, caseID, "draft_contract", "new-main.txt", "text/plain", []byte("新主合同"), MaterialUploadOptions{IdempotencyKey: "new-primary-after-legacy-procurement"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetCase(user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.MaterialSelection.Status != "confirmed" || !sameDocumentSet(detail.MaterialSelection.DocumentIDs, []string{procurement, primary.DocumentID}) {
		t.Fatalf("adding the first primary discarded previously selected legacy procurement: %+v", detail.MaterialSelection)
	}
}

type legacyPrimaryPair struct {
	primary     string
	unselected  string
	procurement string
}

func makeLegacyPrimaryPair(t *testing.T, st *Store, user accounts.User, caseID string) legacyPrimaryPair {
	t.Helper()
	ctx := context.Background()
	first, err := st.AddDocument(ctx, user, caseID, "draft_contract", "old-primary-a.txt", "text/plain", []byte("旧主合同 A"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)
	second, err := st.AddDocument(ctx, user, caseID, "draft_contract", "old-primary-b.txt", "text/plain", []byte("旧主合同 B"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)
	procurement, err := st.AddDocument(ctx, user, caseID, "procurement", "old-procurement.txt", "text/plain", []byte("旧采购材料"))
	if err != nil {
		t.Fatal(err)
	}
	clearMaterialProjectionForTest(t, st, user, caseID)
	return legacyPrimaryPair{primary: first, unselected: second, procurement: procurement}
}

func replaceCurrentPrimaryForTest(t *testing.T, st *Store, user accounts.User, caseID, filename string, body []byte) (string, error) {
	t.Helper()
	current, err := st.snapshotAs(user)
	if err != nil {
		return "", err
	}
	var target *MaterialEntity
	for i := range current.Materials {
		item := &current.Materials[i]
		if item.CaseID != caseID || item.Purpose != "primary_contract" || item.CurrentDocumentID == "" {
			continue
		}
		if target != nil {
			return "", errors.New("test fixture has multiple primary material entities")
		}
		target = item
	}
	if target == nil {
		return "", errors.New("test fixture has no primary material to replace")
	}
	result, err := st.UploadMaterial(context.Background(), user, caseID, "draft_contract", filename, "text/plain", body,
		MaterialUploadOptions{Intent: "replace", MaterialID: target.ID, ReplacesDocumentID: target.CurrentDocumentID})
	return result.DocumentID, err
}

func TestConcurrentUploadIdempotencyAndReplacementCAS(t *testing.T) {
	ctx := context.Background()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	caseID, err := st.CreateCase(user, "并发材料幂等和CAS", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		upload DocumentUploadResult
		err    error
	}
	concurrentUploads := func(options MaterialUploadOptions, bodies [][]byte) []result {
		t.Helper()
		start := make(chan struct{})
		out := make(chan result, len(bodies))
		var workers sync.WaitGroup
		for i := range bodies {
			workers.Add(1)
			go func(body []byte) {
				defer workers.Done()
				<-start
				value, err := st.UploadMaterial(ctx, user, caseID, "draft_contract", "concurrent.txt", "text/plain", body, options)
				out <- result{upload: value, err: err}
			}(bodies[i])
		}
		close(start)
		workers.Wait()
		close(out)
		values := make([]result, 0, len(bodies))
		for value := range out {
			values = append(values, value)
		}
		return values
	}
	sameKey := concurrentUploads(MaterialUploadOptions{IdempotencyKey: "concurrent-same"}, [][]byte{[]byte("same bytes"), []byte("same bytes")})
	if len(sameKey) != 2 || sameKey[0].err != nil || sameKey[1].err != nil || sameKey[0].upload.DocumentID != sameKey[1].upload.DocumentID || sameKey[0].upload.MaterialID != sameKey[1].upload.MaterialID || sameKey[0].upload.Version != 1 || sameKey[1].upload.Version != 1 || sameKey[0].upload.Replayed == sameKey[1].upload.Replayed {
		t.Fatalf("same-key concurrent retries should yield one version and one replay: %+v", sameKey)
	}
	materialID, oldDocumentID := sameKey[0].upload.MaterialID, sameKey[0].upload.DocumentID
	casResults := make(chan result, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i, key := range []string{"replace-race-a", "replace-race-b"} {
		workers.Add(1)
		go func(i int, key string) {
			defer workers.Done()
			<-start
			body := []byte{byte('A' + i)}
			value, err := st.UploadMaterial(ctx, user, caseID, "draft_contract", "concurrent-v2.txt", "text/plain", body, MaterialUploadOptions{Intent: "replace", MaterialID: materialID, ReplacesDocumentID: oldDocumentID, IdempotencyKey: key})
			casResults <- result{upload: value, err: err}
		}(i, key)
	}
	close(start)
	workers.Wait()
	close(casResults)
	succeeded, conflicted := 0, 0
	for value := range casResults {
		if value.err == nil {
			if value.upload.Version != 2 || value.upload.MaterialID != materialID {
				t.Fatalf("replacement winner has wrong identity: %+v", value.upload)
			}
			succeeded++
		} else if isMaterialConflict(value.err) {
			conflicted++
		} else {
			t.Fatalf("replacement loser returned non-conflict: %v", value.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("two CAS replacements of the same current document: success=%d conflict=%d", succeeded, conflicted)
	}
	state, err := st.snapshotAs(user)
	if err != nil {
		t.Fatal(err)
	}
	versions := 0
	for _, version := range state.MaterialVersions {
		if version.CaseID == caseID && version.MaterialID == materialID {
			versions++
		}
	}
	if versions != 2 {
		t.Fatalf("concurrent key/CAS retries created %d versions, want 2", versions)
	}
}

func assertFrozenMaterial(t *testing.T, input AttemptInput, documentID, materialID, role, purpose string, version int) {
	t.Helper()
	for _, frozen := range input.Documents {
		if frozen.Document.ID == documentID {
			if frozen.Material == nil || frozen.Material.MaterialID != materialID || frozen.Material.Role != role || frozen.Material.Purpose != purpose || frozen.Material.Version != version {
				t.Fatalf("attempt input omitted material identity for %s: material=%+v", documentID, frozen.Material)
			}
			return
		}
	}
	t.Fatalf("attempt input omitted document %s", documentID)
}

func attemptInputForRun(t *testing.T, st state, runID string) AttemptInput {
	t.Helper()
	for _, input := range st.AttemptInputs {
		if input.RunID == runID {
			return input
		}
	}
	t.Fatalf("attempt input for run %s missing", runID)
	return AttemptInput{}
}

func frozenAttemptDocumentIDs(input AttemptInput) []string {
	ids := make([]string, 0, len(input.Documents))
	for _, document := range input.Documents {
		ids = append(ids, document.Document.ID)
	}
	return ids
}

func clearMaterialProjectionForTest(t *testing.T, st *Store, user accounts.User, caseID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE material_versions DISABLE TRIGGER material_versions_append_only`); err != nil {
		t.Fatalf("prepare old-schema fixture: %v", err)
	}
	for _, table := range []string{"case_contract_chains", "document_upload_idempotency", "material_versions", "material_entities"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3", user.TenantID, user.OrgID, caseID); err != nil {
			t.Fatalf("clear material projection %s: %v", table, err)
		}
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE material_versions ENABLE TRIGGER material_versions_append_only`); err != nil {
		t.Fatalf("restore material history protection: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func frozenMaterialEvidence(t *testing.T, st state, caseID string) []byte {
	t.Helper()
	value := struct {
		Attempts   []Attempt
		Inputs     []AttemptInput
		Selections []AttemptParseSelection
		Snapshots  []RunSnapshot
		Parsed     []ParsedDocument
		Decisions  []Decision
		Reports    []frozenReport
	}{}
	runs := map[string]bool{}
	documents := map[string]bool{}
	for _, run := range st.Runs {
		if run.CaseID == caseID {
			runs[run.ID] = true
		}
	}
	for _, document := range st.Documents {
		if document.CaseID == caseID {
			documents[document.ID] = true
		}
	}
	for _, item := range st.Attempts {
		if runs[item.RunID] {
			value.Attempts = append(value.Attempts, item)
		}
	}
	for _, item := range st.AttemptInputs {
		if runs[item.RunID] {
			value.Inputs = append(value.Inputs, item)
		}
	}
	for _, item := range st.AttemptParseSelections {
		if runs[item.RunID] {
			value.Selections = append(value.Selections, item)
		}
	}
	for _, item := range st.Snapshots {
		if runs[item.RunID] {
			value.Snapshots = append(value.Snapshots, item)
		}
	}
	for _, item := range st.ParsedDocuments {
		if documents[item.DocumentID] {
			value.Parsed = append(value.Parsed, item)
		}
	}
	for _, item := range st.Decisions {
		if item.CaseID == caseID {
			value.Decisions = append(value.Decisions, item)
		}
	}
	for _, item := range st.Reports {
		if item.CaseID == caseID {
			value.Reports = append(value.Reports, item)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func isMaterialConflict(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 409 && (apiErr.Code == "conflict" || apiErr.Code == "primary_contract_exists")
}

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConcurrentDuplicateOlderKnowledgeReturnsExactStoredVersion(t *testing.T) {
	st, owner := openExecutionStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var blockerPID int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(), 0))`); err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, tx, userScope(owner)); err != nil {
		t.Fatal(err)
	}

	older := Knowledge{ID: "race-policy", Title: "采购制度", Version: "1.0", Body: "第一版正文。",
		SourceType: "reference", CreatedBy: owner.ID, CreatedAt: "2020-01-01T00:00:00.000Z"}
	newer := Knowledge{ID: older.ID, Title: older.Title, Version: "2.0", Body: "第二版正文。",
		SourceType: "reference", CreatedBy: owner.ID, CreatedAt: "2021-01-01T00:00:00.000Z"}
	input := KnowledgeInput{ID: older.ID, Title: older.Title, Version: older.Version, Body: older.Body, SourceType: older.SourceType}
	type result struct {
		item KnowledgeView
		err  error
	}
	done := make(chan result, 1)
	go func() {
		item, err := st.CreateKnowledge(owner, input)
		done <- result{item: item, err: err}
	}()

	// The creator has finished its initial read when its write waits on our lock.
	// Commit both versions before releasing it to exercise the duplicate race.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := st.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case outcome := <-done:
			t.Fatalf("creator finished before the concurrent write: %+v", outcome)
		case <-ctx.Done():
			t.Fatalf("creator did not reach the blocked write: %v", ctx.Err())
		case <-ticker.C:
		}
	}
	for _, item := range []Knowledge{older, newer} {
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_knowledge (tenant_id,org_id,id,version,source_type,record_json) VALUES ($1,$2,$3,$4,$5,$6)`,
			owner.TenantID, owner.OrgID, item.ID, item.Version, item.SourceType, raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !reflect.DeepEqual(got.item.Knowledge, older) || got.item.VersionID == "" {
			t.Fatalf("duplicate race returned the wrong version: item=%+v err=%v", got.item, got.err)
		}
		exact, err := st.GetKnowledgeVersion(owner, older.ID, got.item.VersionID)
		if err != nil || !reflect.DeepEqual(exact, got.item) {
			t.Fatalf("race result does not address the older version: got=%+v err=%v", exact, err)
		}
	case <-ctx.Done():
		t.Fatalf("duplicate creator did not finish: %v", ctx.Err())
	}
	latest, err := st.GetKnowledge(owner, older.ID)
	if err != nil || !reflect.DeepEqual(latest.Knowledge, newer) {
		t.Fatalf("legacy latest did not select the newer version: got=%+v err=%v", latest, err)
	}
}

func TestKnowledgeViewsPreserveLegacyCloudPolicyFreeze(t *testing.T) {
	st, owner := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	const legacyJSON = `{"id":"cloud-procurement-policy","title":"Legacy policy","version":"1.0.0","body":"Original policy text.","source_type":"synthetic_policy","effective_at":null,"created_by":"demo-admin","created_at":"2020-01-01T00:00:00.000Z"}`
	if _, err := st.pool.Exec(context.Background(), `INSERT INTO business_knowledge (tenant_id,org_id,id,version,source_type,record_json) VALUES ($1,$2,$3,$4,$5,$6)`,
		owner.TenantID, owner.OrgID, "cloud-procurement-policy", "1.0.0", "synthetic_policy", []byte(legacyJSON)); err != nil {
		t.Fatal(err)
	}
	var legacyRow string
	if err := st.pool.QueryRow(context.Background(), `SELECT record_json::text FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3`,
		owner.TenantID, "cloud-procurement-policy", "1.0.0").Scan(&legacyRow); err != nil {
		t.Fatal(err)
	}

	// Adapt the existing governed fixture to the required cloud capabilities and
	// exact synthetic policy reference, then accept a real cloud review.
	var manifest map[string]any
	if err := json.Unmarshal(governedPackManifest(t, owner.TenantID, "1", "1000"), &manifest); err != nil {
		t.Fatal(err)
	}
	definitions := []struct{ id, capability, threshold string }{
		{"CLOUD-TECH-BW-001", "cloud_services.minimum_bandwidth", "1000"},
		{"CLOUD-TECH-SLA-001", "cloud_services.monthly_availability", "99.95"},
		{"CLOUD-COM-ADV-001", "cloud_services.advance_payment_ceiling", "3/10"},
	}
	for i, raw := range manifest["rules"].([]any) {
		rule := raw.(map[string]any)
		rule["rule_id"], rule["capability_id"] = definitions[i].id, definitions[i].capability
		rule["business_scope"].(map[string]any)["scenario"] = "cloud_services"
		rule["threshold"].(map[string]any)["value"] = definitions[i].threshold
		rule["authority_sources"] = []any{map[string]any{"id": "cloud-procurement-policy", "version": "1.0.0", "kind": "synthetic_demo"}}
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	publishGovernedPackStore(t, st, owner, "1", "1000", rawManifest)
	caseID, err := st.CreateCaseWithBusiness(owner, "云政策冻结回归", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	for role, text := range map[string]string{
		"procurement":    "采购文件要求云服务 CLOUD-001 最低带宽为1000 Mbit/s。",
		"draft_contract": "云服务 CLOUD-001 的最低带宽为1000 Mbit/s。",
	} {
		if _, err := st.AddDocument(context.Background(), owner, caseID, role, role+".txt", "text/plain", []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReview(context.Background(), owner, caseID)
	if err != nil {
		t.Fatalf("accept cloud review: %v", err)
	}
	workspace := waitTerminalWorkspace(t, st, owner, caseID, runID)
	frozen := workspace.Snapshot
	if string(frozen.CloudPolicySnapshot) != legacyJSON || frozen.CloudPolicyDigest != sha256HexOf([]byte(legacyJSON)) {
		t.Fatalf("cloud review did not freeze the legacy bytes: snapshot=%s digest=%s", frozen.CloudPolicySnapshot, frozen.CloudPolicyDigest)
	}

	newer, err := st.CreateKnowledge(owner, KnowledgeInput{
		ID: "cloud-procurement-policy", Title: "Later policy", Version: "2.0.0", Body: "Later policy text.", SourceType: "synthetic_policy",
	})
	if err != nil {
		t.Fatal(err)
	}
	workbench, err := st.WorkbenchSnapshot(context.Background(), owner, "week", WorkbenchModel{})
	if err != nil {
		t.Fatal(err)
	}
	if len(workbench.Knowledge) != 2 {
		t.Fatalf("cloud knowledge views missing: %+v", workbench.Knowledge)
	}
	var oldView KnowledgeView
	for _, item := range workbench.Knowledge {
		if item.Version == "1.0.0" {
			oldView = item
		}
	}
	if oldView.VersionID == "" || oldView.VersionID == newer.VersionID {
		t.Fatalf("cloud knowledge identities are wrong: old=%+v new=%+v", oldView, newer)
	}
	readOld, err := st.GetKnowledgeVersion(owner, oldView.ID, oldView.VersionID)
	if err != nil || !reflect.DeepEqual(readOld, oldView) {
		t.Fatalf("legacy policy exact read: got=%+v err=%v", readOld, err)
	}

	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedView, err := reopened.GetKnowledgeVersion(owner, oldView.ID, oldView.VersionID)
	if err != nil || !reflect.DeepEqual(reopenedView, oldView) {
		t.Fatalf("reopened legacy policy view changed: got=%+v err=%v", reopenedView, err)
	}
	var preservedRow, digest string
	var preservedFreeze []byte
	if err := reopened.pool.QueryRow(context.Background(), `SELECT record_json::text FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3`,
		owner.TenantID, oldView.ID, oldView.Version).Scan(&preservedRow); err != nil {
		t.Fatal(err)
	}
	if err := reopened.pool.QueryRow(context.Background(), `SELECT cloud_policy_snapshot,cloud_policy_digest FROM review_run_snapshots WHERE run_id=$1`, runID).
		Scan(&preservedFreeze, &digest); err != nil {
		t.Fatal(err)
	}
	if preservedRow != legacyRow || strings.Contains(preservedRow, `"version_id"`) {
		t.Fatalf("legacy stored JSON changed: before=%s after=%s", legacyRow, preservedRow)
	}
	var newRow string
	if err := reopened.pool.QueryRow(context.Background(), `SELECT record_json::text FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3`,
		owner.TenantID, newer.ID, newer.Version).Scan(&newRow); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(newRow, `"version_id"`) {
		t.Fatalf("view identity was persisted with the new knowledge record: %s", newRow)
	}
	if !bytes.Equal(preservedFreeze, frozen.CloudPolicySnapshot) || digest != frozen.CloudPolicyDigest {
		t.Fatalf("historical cloud freeze changed: before=%s after=%s", frozen.CloudPolicySnapshot, preservedFreeze)
	}
}

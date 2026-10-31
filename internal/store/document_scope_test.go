package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCrossOrganizationGenerationPreservesCaseIdentity(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, document := environment.openRole(t, "core"), environment.openRole(t, "document")
	owner := accounts.User{ID: "org-owner", DisplayName: "案件所属部门", Role: "clerk", TenantID: "shared-tenant", OrgID: "branch-sales", Enabled: true, Issuer: "https://directory.invalid", Subject: "org-owner", Grants: []access.Grant{{Role: "case_owner", Scope: "self"}}}
	collaborator := accounts.User{ID: "org-collaborator", DisplayName: "跨部门经办", Role: "clerk", TenantID: owner.TenantID, OrgID: "central-office", Enabled: true, Issuer: owner.Issuer, Subject: "org-collaborator", Grants: []access.Grant{{Role: "case_owner", Scope: "org_subtree", ResolvedOrgIDs: []string{owner.OrgID}}}}
	governanceAdmin := accounts.User{ID: "org-governance-admin", DisplayName: "制度管理员", Role: "admin", TenantID: owner.TenantID, OrgID: owner.OrgID, Enabled: true, Issuer: owner.Issuer, Subject: "org-governance-admin"}
	body, err := json.Marshal(map[string]any{"users": []accounts.User{owner, collaborator, governanceAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	pack := publishSyntheticAcceptancePack(t, core, governanceAdmin)
	publishSyntheticAcceptancePolicy(t, core, governanceAdmin, pack)
	caseID, err := core.CreateCaseWithBusiness(owner, "跨部门专线生成", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := document.Generate(ctx, collaborator, caseID, telecomTemplateID, validTelecomTemplateFields())
	if err != nil {
		t.Fatalf("authorized cross-organization generation failed: %v", err)
	}
	fields := validTelecomTemplateFields()
	fields["bandwidth_mbps"] = "200"
	second, err := document.GenerateWithOptions(ctx, collaborator, caseID, telecomTemplateID, fields, MaterialUploadOptions{Intent: "replace", MaterialID: first.MaterialID, ReplacesDocumentID: first.DocumentID})
	if err != nil {
		t.Fatalf("append cross-organization generation: %v", err)
	}
	detail, err := core.GetCase(owner, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Case.OwnerOrgID != owner.OrgID || len(detail.Generations) != 2 || detail.Generations[0] != first || detail.Generations[1] != second {
		t.Fatalf("case organization or immutable generation history changed: %+v", detail)
	}
	for _, generation := range []GenerationRecord{first, second} {
		original, _, err := document.Original(collaborator, caseID, generation.DocumentID)
		if err != nil || original.TenantID != owner.TenantID || original.OrgID != owner.OrgID {
			t.Fatalf("cross-organization original has the wrong resource scope: document=%+v err=%v", original, err)
		}
	}
	if len(detail.Documents) != 2 {
		t.Fatalf("generated document history missing: %+v", detail.Documents)
	}
	for _, parsed := range detail.Documents {
		if !parsed.Ready || parsed.ParsedDocument == nil || parsed.ParsedDocument.Status != "ready" {
			t.Fatalf("cross-organization parsed artifact is unavailable: %+v", parsed)
		}
	}

	t.Run("agent review and report follow authorized case organization", func(t *testing.T) {
		t.Setenv("AGENT_PLANNER_MODE", "deterministic")
		execution := environment.openRole(t, "execution")
		execution.UseDocumentPreparer(document)
		execution.UseModels(&frozenOCRModel{})
		reviewCaseID, err := core.CreateCaseWithBusiness(owner, "跨部门审查报告", "service", "ENT", "telecom_dedicated_line")
		if err != nil {
			t.Fatal(err)
		}
		for role, text := range map[string]string{
			"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。",
			"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。",
		} {
			if _, err := document.AddDocument(context.Background(), owner, reviewCaseID, role, role+".txt", "text/plain", []byte(text)); err != nil {
				t.Fatalf("add %s review document: %v", role, err)
			}
		}
		workerContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		accepted, err := execution.StartReviewAsyncAt(workerContext, collaborator, reviewCaseID, acceptanceBusinessTime)
		if err != nil {
			t.Fatalf("accept cross-organization review: %v", err)
		}
		claim, err := execution.ClaimNextJob(workerContext, "cross-organization-report-worker", time.Minute)
		if err != nil || claim.JobID != accepted.JobID {
			t.Fatalf("claim cross-organization review: claim=%+v err=%v", claim, err)
		}
		if err := execution.RunClaimedJob(workerContext, claim); err != nil {
			t.Fatalf("run cross-organization review: %v", err)
		}
		workspace, err := core.Workspace(collaborator, reviewCaseID, claim.RunID, claim.AttemptID)
		if err != nil {
			t.Fatalf("authorized collaborator could not read reviewed workspace: %v", err)
		}
		if workspace.Execution == nil || workspace.Execution.Attempt == nil || len(workspace.Execution.AgentReview) == 0 {
			t.Fatal("authorized collaborator workspace omitted the persisted AgentReview")
		}
		payload, present, err := core.AgentReviewForAttempt(context.Background(), collaborator, reviewCaseID, claim.RunID, claim.AttemptID)
		if err != nil || !present {
			t.Fatalf("authorized collaborator could not read case-owned AgentReview: present=%v err=%v", present, err)
		}
		report, err := core.CreateReport(collaborator, reviewCaseID, claim.RunID, claim.AttemptID)
		if err != nil {
			t.Fatalf("create cross-organization report: %v", err)
		}
		readRecord, raw, err := core.ReadReport(collaborator, reviewCaseID, report.ID, "json")
		if err != nil {
			t.Fatalf("read cross-organization report: %v", err)
		}
		if readRecord.Digest == "" || readRecord.Digest != report.Digest {
			t.Fatalf("cross-organization report digest was not preserved: read=%q created=%q", readRecord.Digest, report.Digest)
		}
		var envelope reportEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode cross-organization report: %v", err)
		}
		if !bytes.Equal(envelope.AgentReviewBytes, payload) || envelope.AgentReviewSHA256 != sha256Bytes(payload) || envelope.OutputHash != report.OutputHash {
			t.Fatalf("report did not preserve the case-owned AgentReview and its output identity: digest=%q payload_digest=%q", envelope.AgentReviewSHA256, sha256Bytes(payload))
		}
		revoked := collaborator
		revoked.Grants = nil
		revokedBody, err := json.Marshal(map[string]any{"users": []accounts.User{owner, revoked, governanceAdmin}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Errorf("restore cross-organization directory grant: %v", err)
			}
		})
		if err := os.WriteFile(path, revokedBody, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := core.AgentReviewForAttempt(context.Background(), collaborator, reviewCaseID, claim.RunID, claim.AttemptID); err == nil {
			t.Fatal("revoked cross-organization grant still authorized AgentReview reads")
		}
		if _, _, err := core.ReadReport(collaborator, reviewCaseID, report.ID, "json"); err == nil {
			t.Fatal("revoked cross-organization grant still authorized report reads")
		}
	})
	t.Run("frozen tool authority follows current cross-organization grant", func(t *testing.T) {
		if _, err := document.AddDocument(context.Background(), owner, caseID, "procurement", "purchase.txt", "text/plain", []byte("电路 LINE-001：最低保障带宽 100 Mbit/s。")); err != nil {
			t.Fatalf("add cross-organization review baseline: %v", err)
		}
		execution, tool := environment.openRole(t, "execution"), environment.openRole(t, "tool")
		execution.UseDocumentPreparer(document)
		gate := newHeldReviewModel(1)
		execution.UseModels(gate)
		workerContext, stop := context.WithTimeout(context.Background(), 20*time.Second)
		accepted, err := execution.StartReviewAsyncAt(workerContext, collaborator, caseID, acceptanceBusinessTime)
		if err != nil {
			stop()
			t.Fatalf("accept authorized cross-organization review: %v", err)
		}
		claim, err := execution.ClaimNextJob(workerContext, "cross-organization-worker", time.Minute)
		if err != nil || claim.JobID != accepted.JobID {
			stop()
			t.Fatalf("claim accepted cross-organization job: claim=%+v err=%v", claim, err)
		}
		done := make(chan struct{})
		t.Cleanup(func() {
			stop()
			gate.unblock()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("cross-organization worker did not stop")
			}
		})
		go func() {
			defer close(done)
			_ = execution.RunClaimedJob(workerContext, claim)
		}()
		waitSignal(t, gate.entered, "cross-organization attempt frozen before model completion")
		request := ToolInvocationContext{
			TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID,
			WorkerID: claim.WorkerID, Generation: claim.Generation, CaseID: claim.CaseID,
			RunID: claim.RunID, AttemptID: claim.AttemptID,
		}
		authority, err := tool.AuthorizeToolResolution(workerContext, request)
		if err != nil || authority.User.ID != collaborator.ID || authority.OrgID != owner.OrgID || authority.CaseID != caseID {
			t.Fatalf("current explicit grant did not authorize the case-owned tool scope: authority=%+v err=%v", authority, err)
		}
		forged := request
		forged.OrgID = collaborator.OrgID
		if _, err := tool.AuthorizeToolResolution(workerContext, forged); !errors.Is(err, ErrToolScopeUnavailable) {
			t.Fatalf("actor home organization replaced the authoritative case organization: %v", err)
		}
		collaborator.Grants = nil
		revoked, err := json.Marshal(map[string]any{"users": []accounts.User{owner, collaborator, governanceAdmin}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, revoked, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.AuthorizeToolResolution(workerContext, request); !errors.Is(err, ErrToolScopeUnavailable) {
			t.Fatalf("revoked cross-organization grant still authorized frozen tool evidence: %v", err)
		}
	})
}

func TestMaterialTablesEnforceTenantRLSAndAppendOnlyServiceOwnership(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	document := env.openRole(t, "document")
	execution := env.openRole(t, "execution")
	caseID, err := core.CreateCase(admin, "材料角色与租户隔离", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	upload, err := document.UploadMaterial(context.Background(), admin, caseID, "draft_contract", "main.txt", "text/plain", []byte("主合同"), MaterialUploadOptions{IdempotencyKey: "material-rls-test"})
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{"case_contract_chains", "material_entities", "material_versions", "document_upload_idempotency"}
	for _, table := range tables {
		t.Run(table+" own row visible", func(t *testing.T) {
			tx, err := document.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if err := setLocalScope(context.Background(), tx, userScope(admin)); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE case_id=$1", caseID).Scan(&count); err != nil || count == 0 {
				t.Fatalf("document owner cannot read its material rows: count=%d err=%v", count, err)
			}
		})

		t.Run(table+" foreign tenant hidden", func(t *testing.T) {
			tx, err := document.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			scope := userScope(admin)
			scope.TenantID = "different-tenant"
			if err := setLocalScope(context.Background(), tx, scope); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE case_id=$1", caseID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("foreign tenant rows leaked from %s: count=%d err=%v", table, count, err)
			}
		})
	}

	insertions := []struct {
		table string
		query string
		args  []any
	}{
		{"case_contract_chains", `INSERT INTO case_contract_chains (tenant_id,org_id,case_id) VALUES ($1,$2,$3)`, []any{admin.TenantID, admin.OrgID, caseID}},
		{"material_entities", `INSERT INTO material_entities (tenant_id,org_id,case_id,material_id,role,purpose,legacy_status,current_document_id,current_version,created_by) VALUES ($1,$2,$3,'foreign-material','attachment','other','native',$4,1,$5)`, []any{admin.TenantID, admin.OrgID, caseID, upload.DocumentID, admin.ID}},
		{"material_versions", `INSERT INTO material_versions (tenant_id,org_id,case_id,material_id,version,document_id,created_by) VALUES ($1,$2,$3,$4,9,$5,$6)`, []any{admin.TenantID, admin.OrgID, caseID, upload.MaterialID, upload.DocumentID, admin.ID}},
		{"document_upload_idempotency", `INSERT INTO document_upload_idempotency (tenant_id,org_id,case_id,actor_id,idempotency_key,request_hash,document_id,material_id) VALUES ($1,$2,$3,$4,'foreign-key',decode('00','hex'),$5,$6)`, []any{admin.TenantID, admin.OrgID, caseID, admin.ID, upload.DocumentID, upload.MaterialID}},
	}
	for _, attempt := range insertions {
		t.Run(attempt.table+" rejects foreign tenant insert", func(t *testing.T) {
			tx, err := document.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			scope := userScope(admin)
			scope.TenantID = "different-tenant"
			if err := setLocalScope(context.Background(), tx, scope); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(context.Background(), attempt.query, attempt.args...)
			if !isPostgresError(err, "42501", "row-level security") {
				t.Fatalf("foreign-tenant insert into %s did not fail its RLS check: %v", attempt.table, err)
			}
		})
	}

	for _, role := range []*Store{core, execution} {
		for _, table := range tables {
			t.Run(role.serviceOwner+" cannot write "+table, func(t *testing.T) {
				tx, err := role.pool.Begin(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if err := setLocalScope(context.Background(), tx, userScope(admin)); err != nil {
					t.Fatal(err)
				}
				var canInsert, canUpdate, canDelete bool
				if err := tx.QueryRow(context.Background(), `SELECT has_table_privilege(current_user,$1,'INSERT'), has_table_privilege(current_user,$1,'UPDATE'), has_table_privilege(current_user,$1,'DELETE')`, table).Scan(&canInsert, &canUpdate, &canDelete); err != nil {
					t.Fatal(err)
				}
				if canInsert || canUpdate || canDelete {
					t.Fatalf("%s has material table write privileges: %s insert=%v update=%v delete=%v", role.serviceOwner, table, canInsert, canUpdate, canDelete)
				}
				query := map[string]string{
					"case_contract_chains":        `UPDATE case_contract_chains SET selected_document_ids=selected_document_ids WHERE case_id=$1`,
					"material_entities":           `UPDATE material_entities SET created_by=created_by WHERE case_id=$1`,
					"material_versions":           `UPDATE material_versions SET created_by=created_by WHERE case_id=$1`,
					"document_upload_idempotency": `UPDATE document_upload_idempotency SET idempotency_key=idempotency_key WHERE case_id=$1`,
				}[table]
				_, execErr := tx.Exec(context.Background(), query, caseID)
				if !isPostgresError(execErr, "42501", "permission denied") {
					t.Fatalf("%s UPDATE of %s was not denied by database privileges: %v", role.serviceOwner, table, execErr)
				}
			})
		}
	}

	t.Run("document role cannot update or delete material versions", func(t *testing.T) {
		tx, err := document.pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(context.Background(), tx, userScope(admin)); err != nil {
			t.Fatal(err)
		}
		var canUpdate, canDelete bool
		if err := tx.QueryRow(context.Background(), `SELECT has_table_privilege(current_user,'material_versions','UPDATE'), has_table_privilege(current_user,'material_versions','DELETE')`).Scan(&canUpdate, &canDelete); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback(context.Background())
		if canUpdate || canDelete {
			t.Fatalf("document role can mutate append-only material versions: update=%v delete=%v", canUpdate, canDelete)
		}
	})

	for name, query := range map[string]string{
		"update": `UPDATE material_versions SET created_by='rewritten' WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND document_id=$4`,
		"delete": `DELETE FROM material_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND document_id=$4`,
	} {
		t.Run("append-only trigger rejects "+name, func(t *testing.T) {
			tx, err := env.harness.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := setLocalScope(context.Background(), tx, userScope(admin)); err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatal(err)
			}
			_, err = tx.Exec(context.Background(), query, admin.TenantID, admin.OrgID, caseID, upload.DocumentID)
			_ = tx.Rollback(context.Background())
			if !isPostgresError(err, "P0001", "append-only") {
				t.Fatalf("material version %s did not hit the append-only trigger: %v", name, err)
			}
		})
	}
}

func isPostgresError(err error, code, message string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && strings.Contains(strings.ToLower(pgErr.Message), strings.ToLower(message))
}

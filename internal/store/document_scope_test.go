package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
)

func TestCrossOrganizationGenerationPreservesCaseIdentity(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, document := environment.openRole(t, "core"), environment.openRole(t, "document")
	owner := accounts.User{ID: "org-owner", DisplayName: "案件所属部门", Role: "clerk", TenantID: "shared-tenant", OrgID: "branch-sales", Enabled: true, Issuer: "https://directory.invalid", Subject: "org-owner", Grants: []access.Grant{{Role: "case_owner", Scope: "self"}}}
	collaborator := accounts.User{ID: "org-collaborator", DisplayName: "跨部门经办", Role: "clerk", TenantID: owner.TenantID, OrgID: "central-office", Enabled: true, Issuer: owner.Issuer, Subject: "org-collaborator", Grants: []access.Grant{{Role: "case_owner", Scope: "org_subtree", ResolvedOrgIDs: []string{owner.OrgID}}}}
	body, err := json.Marshal(map[string]any{"users": []accounts.User{owner, collaborator}})
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
	second, err := document.Generate(ctx, collaborator, caseID, telecomTemplateID, fields)
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
		accepted, err := execution.StartReviewAsync(workerContext, collaborator, reviewCaseID)
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
		revokedBody, err := json.Marshal(map[string]any{"users": []accounts.User{owner, revoked}})
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
		accepted, err := execution.StartReviewAsync(workerContext, collaborator, caseID)
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
		revoked, err := json.Marshal(map[string]any{"users": []accounts.User{owner, collaborator}})
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

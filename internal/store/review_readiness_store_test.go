// File metadata (manifest mirror):
// Purpose: Exercises the `store` package behavior through TestGetReviewReadinessWithoutPoolReturnsUnavailable, TestReviewReadinessReadsCurrentMaterialsWithoutCreatingReviewState, TestReviewReadinessBusinessTimeMatchesUngovernedAcceptance; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/fault"
)

func TestGetReviewReadinessWithoutPoolReturnsUnavailable(t *testing.T) {
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	_, err := (&Store{}).GetReviewReadiness(context.Background(), admin, "case-missing", "")
	var problem *fault.Error
	if !errors.As(err, &problem) || problem.Status != 503 || problem.Code != "store_unavailable" {
		t.Fatalf("missing database pool should return the standard unavailable fault, got %v", err)
	}
}

// This is a synthetic cloud-service scenario and synthetic internal policy,
// not a legal source or a test of an actual model/OCR service.
func TestReadinessCloudPackAndFrozenSyntheticEvidence(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	admin, _ := accounts.ByID("demo-admin")
	pack, policy := seedGovernedCloudPolicy(t, core, admin)
	token, err := core.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err = core.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAdmissionSessionToken(context.Background(), token)
	authority := durableTestOperationAuthority{core: core}
	document.UseOperationAuthority(authority)
	execution.UseOperationAuthority(authority)
	execution.UseDocumentPreparer(document)
	reviewer := newGovernedCloudModel()
	reviewer.unblock()
	execution.UseModels(reviewer)
	caseID, err := core.CreateCaseWithBusiness(admin, "合成云服务合同", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range syntheticCloudReviewMaterials() {
		if _, err := document.AddDocument(ctx, admin, caseID, role, "synthetic-"+role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := execution.GetReviewReadiness(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil || !ready.CanStart || ready.Scopes[0].RulePack == nil {
		t.Fatalf("matching cloud readiness: %+v, %v", ready, err)
	}
	selected := ready.Scopes[0]
	if selected.RulePack.PackID != pack.Pack.ID || selected.RulePack.Version != pack.Pack.Version ||
		len(selected.RulePack.ApplicableScopes) != 1 || selected.RulePack.ApplicableScopes[0].Scenario != "cloud_services" || len(selected.Checks) != 3 {
		t.Fatalf("wrong declared rules or business scope: %+v", selected)
	}
	for _, check := range selected.Checks {
		if check.Comparator == "" || len(check.SourceRoles) == 0 {
			t.Fatalf("check lost manifest facts: %+v", check)
		}
	}
	otherID, err := core.CreateCaseWithBusiness(admin, "合成不同业务", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	other, err := execution.GetReviewReadiness(ctx, admin, otherID, acceptanceBusinessTime)
	if err != nil || other.CanStart || other.Scopes[0].RulePack != nil || !hasReadinessBlocker(other.Scopes[0], "governed_dependencies_incomplete") {
		t.Fatalf("cloud pack was offered to a dedicated-line case: %+v, %v", other, err)
	}
	accepted, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execution.ClaimNextJob(ctx, "synthetic-cloud-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim synthetic review: %+v, %v", claim, err)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	workspace, err := core.Workspace(admin, caseID, accepted.RunID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	input, ok := state.attemptInput(accepted.AttemptID)
	if !ok || workspace.Snapshot.RulePackID != pack.Pack.ID || len(workspace.Evaluations) != 3 {
		t.Fatalf("matching rule pack or frozen input missing: %+v", workspace.Snapshot)
	}
	verified, draftVerified := 0, false
	for _, evaluation := range workspace.Evaluations {
		if evaluation.Relation != "compatible" {
			t.Fatalf("unexpected synthetic evaluation: %+v", evaluation)
		}
		for _, evidence := range evaluation.Evidence {
			ref, found := evidence.Ref, false
			for _, frozen := range input.Documents {
				if frozen.Document.ID != ref.DocumentID || frozen.ParsedDocumentID != ref.ParsedDocumentID || frozen.NormalizedHash == nil || *frozen.NormalizedHash != ref.NormalizedDigest {
					continue
				}
				quote, exact := docir.Slice(frozen.Text, ref.Start, ref.End)
				if !exact || quote != ref.Quote {
					t.Fatalf("citation does not match frozen original text: %+v", ref)
				}
				found, draftVerified = true, draftVerified || frozen.Document.Role == "draft_contract"
				verified++
			}
			if !found {
				t.Fatalf("citation not bound to a frozen parse: %+v", ref)
			}
		}
	}
	if verified == 0 || !draftVerified {
		t.Fatal("synthetic review produced no verified draft citations")
	}
	advanceKnowledge(t, core, admin, policy, "withdraw", "synthetic-cloud-withdraw")
	blocked, err := execution.GetReviewReadiness(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil || blocked.CanStart || blocked.Scopes[0].RulePack == nil || len(blocked.Scopes[0].Checks) != 3 || !hasReadinessBlocker(blocked.Scopes[0], "governed_dependencies_incomplete") {
		t.Fatalf("withdrawn policy must block admission while preserving readable checks: %+v, %v", blocked, err)
	}
	if _, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime); err == nil {
		t.Fatal("withdrawn policy was accepted for a new review")
	}
	after, err := core.Workspace(admin, caseID, accepted.RunID, accepted.AttemptID)
	if err != nil || !reflect.DeepEqual(after.Snapshot, workspace.Snapshot) || !reflect.DeepEqual(after.Evaluations, workspace.Evaluations) {
		t.Fatalf("governance change altered the frozen historical review: %v", err)
	}
	t.Logf("synthetic cloud evaluation: %d frozen citations verified; real model and OCR NOT_RUN", verified)
}

func TestReviewReadinessReadsCurrentMaterialsWithoutCreatingReviewState(t *testing.T) {
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
	token, err := core.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err = core.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	document.UseOperationAuthority(durableTestOperationAuthority{core: core})
	ctx := WithAdmissionSessionToken(context.Background(), token)
	caseID, err := core.CreateCaseWithBusiness(admin, "准备清单规则接纳", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ role, name, key, body string }{
		{"procurement", "采购文件.txt", "readiness-procurement", "采购文件正文"},
		{"draft_contract", "拟签合同.txt", "readiness-draft", "拟签合同正文"},
	} {
		if _, err := document.UploadMaterial(ctx, admin, caseID, item.role, item.name, "text/plain", []byte(item.body), MaterialUploadOptions{IdempotencyKey: item.key}); err != nil {
			t.Fatalf("upload %s: %v", item.role, err)
		}
	}

	missingTime, err := execution.GetReviewReadiness(ctx, admin, caseID, "")
	if err != nil {
		t.Fatalf("read readiness without business time: %v", err)
	}
	if missingTime.BusinessAt != nil || missingTime.CanStart || len(missingTime.Materials) != 2 || len(missingTime.Scopes) != 2 {
		t.Fatalf("readiness did not expose the exact current two-material selection and explicit missing-time state: %+v", missingTime)
	}
	if !hasReadinessBlocker(missingTime.Scopes[0], "business_at_required") {
		t.Fatalf("missing business time was not a blocker: %+v", missingTime.Scopes[0])
	}
	if !hasReadinessBlocker(missingTime.Scopes[1], "business_at_required") {
		t.Fatalf("single-contract precheck bypassed the business-time gate: %+v", missingTime.Scopes[1])
	}
	for _, material := range missingTime.Materials {
		if !material.Selected || material.Parse.Status != "ready" || material.DocumentID == "" || material.MaterialID == "" || material.Version < 1 {
			t.Fatalf("current material identity/parse state was not returned exactly: %+v", material)
		}
	}

	withTime, err := execution.GetReviewReadiness(ctx, admin, caseID, "2026-10-31T10:00:00Z")
	if err != nil {
		t.Fatalf("read readiness with business time: %v", err)
	}
	if withTime.BusinessAt == nil || withTime.BusinessAt.UTC().Format("2006-01-02T15:04:05Z07:00") != "2026-10-31T10:00:00Z" {
		t.Fatalf("provided business time was not preserved canonically: %+v", withTime.BusinessAt)
	}
	if withTime.CanStart || !hasReadinessBlocker(withTime.Scopes[0], "governed_dependencies_incomplete") {
		t.Fatalf("missing published pack must block admission instead of advertising a ruleset: %+v", withTime.Scopes[0])
	}
	if withTime.Scopes[1].CanStart || !hasReadinessBlocker(withTime.Scopes[1], "governed_dependencies_incomplete") {
		t.Fatalf("single-contract precheck bypassed required governed dependencies: %+v", withTime.Scopes[1])
	}

	var runs, attempts int
	if err := env.harness.pool.QueryRow(ctx, `SELECT count(*) FROM review_runs WHERE case_id=$1`, caseID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := env.harness.pool.QueryRow(ctx, `SELECT count(*) FROM review_attempts a JOIN review_runs r ON r.id=a.run_id WHERE r.case_id=$1`, caseID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || attempts != 0 {
		t.Fatalf("readiness read created durable review state: runs=%d attempts=%d", runs, attempts)
	}
}

func TestReviewReadinessBusinessTimeMatchesUngovernedAcceptance(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	ctx := context.Background()
	for _, withObject := range []bool{false, true} {
		name := "legacy_without_object"
		if withObject {
			name = "registered_object"
		}
		t.Run(name, func(t *testing.T) {
			caseID, err := core.CreateCaseWithBusiness(admin, name, "service", "", "")
			if err != nil {
				t.Fatal(err)
			}
			for role, text := range map[string]string{"procurement": "采购文件要求质保36个月。", "draft_contract": "拟签合同约定质保24个月。"} {
				if _, err := document.AddDocument(ctx, admin, caseID, role, role+".txt", "text/plain", []byte(text)); err != nil {
					t.Fatal(err)
				}
			}
			if withObject {
				if _, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
					ObjectType: ServiceObjectService, DisplayName: "合同服务对象",
					Aliases: []ServiceObjectAlias{{Kind: "service_code", Value: "READINESS-SVC"}},
				}, "readiness-object"); err != nil {
					t.Fatal(err)
				}
			}
			withoutTime, err := execution.GetReviewReadiness(ctx, admin, caseID, "")
			if err != nil {
				t.Fatal(err)
			}
			if withoutTime.BusinessAt != nil || withoutTime.CanStart == withObject ||
				hasReadinessBlocker(withoutTime.Scopes[0], "business_at_required") != withObject {
				t.Fatalf("readiness does not preserve legacy/object time policy: %+v", withoutTime)
			}
			_, startErr := execution.StartReviewAsync(ctx, admin, caseID)
			if !withObject {
				if startErr != nil {
					t.Fatalf("readiness allowed legacy acceptance but start rejected it: %v", startErr)
				}
				return
			}
			var problem *fault.Error
			if !errors.As(startErr, &problem) || problem.Status != 422 {
				t.Fatalf("missing object business time was not rejected at acceptance: %v", startErr)
			}
			withTime, err := execution.GetReviewReadiness(ctx, admin, caseID, acceptanceBusinessTime)
			if err != nil || !withTime.CanStart || withTime.BusinessAt == nil {
				t.Fatalf("explicit object business time did not make readiness startable: %+v err=%v", withTime, err)
			}
			if _, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime); err != nil {
				t.Fatalf("readiness allowed explicit-time acceptance but start rejected it: %v", err)
			}
		})
	}
}

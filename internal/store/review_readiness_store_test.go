package store

import (
	"context"
	"errors"
	"testing"

	"contractaudit/internal/accounts"
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
	ctx := context.Background()
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
	if missingTime.BusinessAt != nil || missingTime.CanStart || len(missingTime.Materials) != 2 || len(missingTime.Scopes) != 1 {
		t.Fatalf("readiness did not expose the exact current two-material selection and explicit missing-time state: %+v", missingTime)
	}
	if !hasReadinessBlocker(missingTime.Scopes[0], "business_at_required") {
		t.Fatalf("missing business time was not a blocker: %+v", missingTime.Scopes[0])
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

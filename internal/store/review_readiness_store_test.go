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
